// hase.go orchestriert einen ganzen Lauf (§6, Ablauf): die Sperre hält
// der Trigger, hier passiert Umgebung → Gänge → Prompt → Hase →
// nachher → persistieren. Der LLM-Schritt läuft asynchron; die
// Wahrheitsquelle für das Ende ist der Event-Stream (Funnel), der
// synchrone Prompt-Call nur ein zweiter Zeuge — er riss im Spike bei
// langen Läufen ab (Hasenbau-q4y).
package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	sdk "github.com/sst/opencode-sdk-go"

	"github.com/Moritz-Dorn/Hasenbau/internal/auftrag"
	"github.com/Moritz-Dorn/Hasenbau/internal/hase"
	"github.com/Moritz-Dorn/Hasenbau/internal/lauf"
	"github.com/Moritz-Dorn/Hasenbau/internal/opencode"
	"github.com/Moritz-Dorn/Hasenbau/internal/store"
)

// LaufStore ist die Schreib- und Kontext-Sicht des Runners auf den
// Store; *store.Store erfüllt das Interface.
type LaufStore interface {
	SummaryQuelle
	LaufBeginne(auftrag, trigger, ausloeser string) (int64, error)
	LaufBeende(id int64, e store.LaufErgebnis) error
	TraceSchreibe(lauf int64, sessionID string, roh []byte) error
}

// traceMax ist die Kappungsgrenze pro Feld für den abgelegten Trace.
// Ein einzelnes read-Tool kann Megabytes ausgeben; für die Verdichtung
// zählen Werkzeug, Argumente und Status, nicht der Volltext.
const traceMax = 8 << 10

// Runner führt Läufe aus und serialisiert instance/dispose gegen sie.
type Runner struct {
	Root    string        // absoluter Bau-Root
	BaseURL func() string // typischerweise supervisor.BaseURL; "" = Server weg
	Store   LaufStore
	Funnel  *opencode.Funnel

	// GangTimeout greift für Gänge ohne eigenes timeout; 0 = unbegrenzt.
	GangTimeout time.Duration
	// HaseTimeout begrenzt den LLM-Schritt. 0 = 30m — ein Lauf darf
	// lange dauern, aber nie für immer hängen (Backstop gegen verlorene
	// idle-Events und hängende Sessions).
	HaseTimeout time.Duration
	Logf        func(format string, args ...any)

	// laufMu serialisiert dispose gegen Läufe: Läufe halten die
	// Lese-Seite, Dispose die Schreib-Seite. Neue Läufe warten also,
	// solange ein dispose läuft — und umgekehrt (PLAN.md §11.6:
	// dispose cancelt aktive Sessions).
	laufMu sync.RWMutex
	aktiv  atomic.Int64
}

// AktiveLaeufe zählt die gerade laufenden Läufe (für Status/Shutdown).
func (r *Runner) AktiveLaeufe() int64 { return r.aktiv.Load() }

// Dispose verwirft die Instanz-Caches des Servers (Agent-Reload,
// §11.6) — erst, wenn kein Lauf mehr aktiv ist; neue Läufe warten.
func (r *Runner) Dispose(ctx context.Context) error {
	r.laufMu.Lock()
	defer r.laufMu.Unlock()
	url := r.BaseURL()
	if url == "" {
		return fmt.Errorf("dispose: kein opencode-Server erreichbar")
	}
	return opencode.DisposeInstance(ctx, opencode.New(url))
}

// FuehreAus ist die AusfuehrFunc für Scheduler, Watcher und manuelle
// Trigger. Die Overlap-Sperre hält der Aufrufer; trigger ist 'cron',
// 'watch' oder 'manuell', input der Bau-relative Pfad der auslösenden
// Datei (nur watch). Rückgabe nil ⇒ Lauf ok (der Watcher merkt den
// Input dann als gesehen).
func (r *Runner) FuehreAus(ctx context.Context, a *auftrag.Auftrag, trigger, input string) error {
	r.laufMu.RLock()
	defer r.laufMu.RUnlock()
	r.aktiv.Add(1)
	defer r.aktiv.Add(-1)
	logf := r.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}

	id, err := r.Store.LaufBeginne(a.Name, trigger, input)
	if err != nil {
		return err
	}
	laufID := fmt.Sprintf("%s-%d", time.Now().UTC().Format("20060102-150405"), id)
	logf("lauf %s: auftrag %s (%s) beginnt", laufID, a.Name, trigger)

	// scheitere beendet den Lauf als 'fehler'. Session-Daten, die bis
	// dahin angefallen sind (Tokens kosten auch bei Fehlläufen), gehen
	// mit in die Zeile. $WORK bleibt liegen (§6, Ablauf 7).
	scheitere := func(erg haseErgebnis, grund error) error {
		erg.Status = "fehler"
		if ctx.Err() != nil {
			erg.Status = "abgebrochen"
		}
		e := erg.alsErgebnis()
		e.Fehler = grund.Error()
		r.legeTraceAb(id, erg, logf)
		if err := r.Store.LaufBeende(id, e); err != nil {
			logf("lauf %s: %v", laufID, err)
		}
		logf("lauf %s: %s — %v", laufID, e.Status, grund)
		return fmt.Errorf("lauf %s (%s): %w", laufID, a.Name, grund)
	}

	u, err := lauf.Neue(r.Root, a, laufID, input)
	if err != nil {
		return scheitere(haseErgebnis{}, err)
	}
	if _, err := FuehreGaengeAus(ctx, u, a, r.GangTimeout); err != nil {
		return scheitere(haseErgebnis{}, err)
	}
	prompt, err := BauePrompt(u, a, r.Store)
	if err != nil {
		return scheitere(haseErgebnis{}, err)
	}

	erg, err := r.fuehreHaseAus(ctx, a, laufID, prompt, logf)
	if err != nil {
		return scheitere(erg, err)
	}
	if err := FuehreNachherAus(u, a); err != nil {
		return scheitere(erg, err)
	}

	if u.Work != "" {
		if err := os.RemoveAll(filepath.Join(r.Root, u.Work)); err != nil {
			logf("lauf %s: $WORK aufräumen: %v", laufID, err)
		}
	}
	erg.Status = "ok"
	r.legeTraceAb(id, erg, logf)
	if err := r.Store.LaufBeende(id, erg.alsErgebnis()); err != nil {
		return err
	}
	logf("lauf %s: ok — %s", laufID, erg.Summary)
	return nil
}

// haseErgebnis sammelt, was der LLM-Schritt über sich weiß.
type haseErgebnis struct {
	Status     string
	SessionID  string
	Summary    string
	TokensIn   int64
	TokensOut  int64
	KostenCent int64
	Trace      *opencode.Trace // nil, wenn der Lauf vor der Auswertung scheiterte
}

func (h haseErgebnis) alsErgebnis() store.LaufErgebnis {
	return store.LaufErgebnis{
		Status:     h.Status,
		SessionID:  h.SessionID,
		Summary:    h.Summary,
		TokensIn:   h.TokensIn,
		TokensOut:  h.TokensOut,
		KostenCent: h.KostenCent,
	}
}

// fuehreHaseAus macht den LLM-Schritt: Session anlegen, Prompt
// asynchron an den generierten Agenten, Event-Stream mitlesen
// (Tool-Calls loggen), auf session.idle warten, dann die Session
// auswerten. Fertig ist der Lauf, wenn der Stream idle meldet ODER der
// synchrone Call sauber zurückkommt — was zuerst eintritt.
func (r *Runner) fuehreHaseAus(ctx context.Context, a *auftrag.Auftrag, laufID, prompt string, logf func(string, ...any)) (haseErgebnis, error) {
	timeout := r.HaseTimeout
	if timeout == 0 {
		timeout = 30 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	url := r.BaseURL()
	if url == "" {
		return haseErgebnis{}, fmt.Errorf("kein opencode-Server erreichbar")
	}
	client := opencode.New(url)

	sess, err := client.Session.New(ctx, sdk.SessionNewParams{
		Title: sdk.F(a.Name + " " + laufID),
	})
	if err != nil {
		return haseErgebnis{}, fmt.Errorf("session anlegen: %w", err)
	}
	erg := haseErgebnis{SessionID: sess.ID}

	// Erst abonnieren, dann prompten — sonst kann idle am Abo vorbeilaufen.
	ereignisse, abmelden := r.Funnel.Abonniere(sess.ID)
	defer abmelden()

	promptFehler := make(chan error, 1)
	go func() {
		_, err := client.Session.Prompt(ctx, sess.ID, sdk.SessionPromptParams{
			Parts: sdk.F([]sdk.SessionPromptParamsPartUnion{opencode.TextPart(prompt)}),
			Agent: sdk.F(hase.AgentName(a)),
		})
		promptFehler <- err
	}()

	gesehen := false // schon Stream-Ereignisse für diese Session?
warten:
	for {
		select {
		case ev := <-ereignisse:
			switch {
			case ev.Idle:
				break warten
			case ev.Fehler != "":
				return erg, fmt.Errorf("session %s: %s", sess.ID, ev.Fehler)
			case ev.Tool != nil:
				gesehen = true
				zeile := fmt.Sprintf("lauf %s: tool %s [%s] %s", laufID, ev.Tool.Name, ev.Tool.CallID, ev.Tool.Status)
				if ev.Tool.Fehler != "" {
					zeile += " — " + ev.Tool.Fehler
				}
				logf("%s", zeile)
			case ev.Abriss:
				logf("lauf %s: Event-Stream neu verbunden — falls idle verloren ging, fängt der Prompt-Call oder der Timeout den Lauf", laufID)
			}
		case err := <-promptFehler:
			if err == nil {
				break warten // synchroner Call kam sauber zurück
			}
			if !gesehen {
				// Server hat den Prompt abgelehnt, bevor irgendetwas
				// lief (Config-/Agent-Fehler) — nicht auf idle warten.
				return erg, fmt.Errorf("prompt: %w", err)
			}
			// Abriss bei laufender Session (Spike-Befund) — der Stream
			// bleibt die Wahrheitsquelle, der Timeout der Backstop.
			logf("lauf %s: prompt-Call riss ab (%v) — warte auf session.idle", laufID, err)
			promptFehler = nil
		case <-ctx.Done():
			return erg, fmt.Errorf("hase: %w", ctx.Err())
		}
	}

	// Auswertung auch dann, wenn ctx gleich abläuft: eigener kurzer
	// Kontext, die Daten liegen ja schon beim Server.
	mctx, mcancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer mcancel()
	msgs, err := client.Session.Messages(mctx, sess.ID, sdk.SessionMessagesParams{})
	if err != nil {
		return erg, fmt.Errorf("session %s auswerten: %w", sess.ID, err)
	}
	erg.Summary, erg.TokensIn, erg.TokensOut, erg.KostenCent = werteAus(*msgs)
	erg.Trace = opencode.TraceAus(sess.ID, *msgs).Gekuerzt(traceMax)
	return erg, nil
}

// legeTraceAb schreibt den Verlauf zum Lauf. Ein Fehler dabei wird
// gemeldet, scheitert den Lauf aber nie: der Trace ist Material für
// später, das Ergebnis des Laufs hängt nicht an ihm.
func (r *Runner) legeTraceAb(id int64, erg haseErgebnis, logf func(string, ...any)) {
	if erg.Trace == nil || erg.SessionID == "" {
		return
	}
	roh, err := json.Marshal(erg.Trace)
	if err == nil {
		err = r.Store.TraceSchreibe(id, erg.SessionID, roh)
	}
	if err != nil {
		logf("lauf %d: Trace nicht abgelegt: %v", id, err)
	}
}

// werteAus zieht Summary, Tokens und Kosten aus den Session-Messages.
// Die Summary hier ist nur der Fallback — der Text der letzten
// Assistant-Message. Hat der Hase seine Summary über den Rückkanal
// geschrieben, gewinnt sie (§5, §8 Phase 2). In eine Zeile presst sie
// der Store, der die Invariante hält.
func werteAus(msgs []sdk.SessionMessagesResponse) (summary string, tokensIn, tokensOut, kostenCent int64) {
	var kosten float64
	for _, m := range msgs {
		am, ok := m.Info.AsUnion().(sdk.AssistantMessage)
		if !ok {
			continue
		}
		tokensIn += int64(am.Tokens.Input)
		tokensOut += int64(am.Tokens.Output)
		kosten += am.Cost
		if text := opencode.AnswerText(m.Parts); text != "" {
			summary = text
		}
	}
	return summary, tokensIn, tokensOut, int64(math.Round(kosten * 100))
}
