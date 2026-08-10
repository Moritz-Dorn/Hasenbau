// hase.go orchestriert einen ganzen Lauf (§6, Ablauf): die Sperre hält
// der Trigger, hier passiert Environment → Gänge → Prompt → Hase →
// nachher → persistieren. Der LLM-Schritt läuft asynchron; die
// Wahrheitsquelle für das Ende ist der Event-Stream (Funnel), der
// synchrone Prompt-Call nur ein zweiter Zeuge — er riss im Spike bei
// langen Läufen ab (Hasenbau-q4y).
package runner

import (
	"context"
	"encoding/json"
	"errors"
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
	SummarySource
	StartLauf(auftrag, trigger, ausloeser string) (int64, error)
	EndLauf(id int64, e store.LaufResult) error
	WriteTrace(lauf int64, sessionID string, roh []byte) error
	WriteToolCalls(lauf int64, calls []store.ToolCall) error
}

// statusInterval ist der Vorgabe-Takt, in dem der Runner den Zustand
// der Session nachfragt, während er auf das Lauf-Ende wartet. Kurz
// genug, dass ein verlorenes idle Sekunden statt einer halben Stunde
// kostet; lang genug, dass die Abfrage neben einem LLM-Call nicht ins
// Gewicht fällt (Hasenbau-0f4).
const statusInterval = 15 * time.Second

// progressInterval ist der Abstand, in dem ein Lauf meldet, dass er
// noch arbeitet. Lang genug, um das Log nicht zu fluten; kurz genug,
// dass niemand die Denkpause eines Hasen für einen hängenden Prozess
// hält.
const progressInterval = 2 * time.Minute

// DefaultHaseTimeout gilt, wenn weder der Auftrag (`hase_timeout:`,
// §6) noch der Runner ein Limit setzt. Exportiert, damit die CLI
// dieselbe Zahl anzeigt, die tatsächlich greift.
const DefaultHaseTimeout = 30 * time.Minute

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
	// StatusInterval ist der Takt der Statusabfrage während eines
	// Laufs; 0 = statusInterval. Nur Tests setzen das.
	StatusInterval time.Duration
	// HaseTimeout ist die Vorgabe für den LLM-Schritt; 0 = 30m. Ein
	// Auftrag mit eigenem `hase_timeout:` sticht sie (§6). Ein Lauf darf
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

// ActiveLaeufe zählt die gerade laufenden Läufe (für Status/Shutdown).
func (r *Runner) ActiveLaeufe() int64 { return r.aktiv.Load() }

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

// Execute ist die ExecFunc für Scheduler, Watcher und manuelle
// Trigger. Die Overlap-Sperre hält der Aufrufer; trigger ist 'cron',
// 'watch' oder 'manuell', input der Bau-relative Pfad der auslösenden
// Datei (nur watch). Fehler nil ⇒ Lauf ok (der Watcher merkt den
// Input dann als gesehen).
//
// Zurück kommt außerdem die ID der Zeile in laeufe — 0 nur, wenn schon
// das Anlegen scheiterte. Damit kann der Aufrufer hinterher berichten,
// was wirklich in der Datenbank steht, statt aus dem eigenen Verlauf zu
// raten (Hasenbau-0f4).
func (r *Runner) Execute(ctx context.Context, a *auftrag.Auftrag, trigger, input string) (int64, error) {
	r.laufMu.RLock()
	defer r.laufMu.RUnlock()
	r.aktiv.Add(1)
	defer r.aktiv.Add(-1)
	logf := r.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}

	id, err := r.Store.StartLauf(a.Name, trigger, input)
	if err != nil {
		return 0, err
	}
	laufID := fmt.Sprintf("%s-%d", time.Now().UTC().Format("20060102-150405"), id)
	logf("lauf %s: auftrag %s (%s) beginnt", laufID, a.Name, trigger)

	// scheitere beendet den Lauf als 'fehler'. Session-Daten, die bis
	// dahin angefallen sind (Tokens kosten auch bei Fehlläufen), gehen
	// mit in die Zeile. $WORK bleibt liegen (§6, Ablauf 7).
	scheitere := func(erg haseResult, grund error) error {
		erg.Status = "failed"
		if ctx.Err() != nil {
			erg.Status = "aborted"
		}
		e := erg.alsErgebnis()
		e.Error = grund.Error()
		r.storeTrace(id, erg, logf)
		if err := r.Store.EndLauf(id, e); err != nil {
			logf("lauf %s: %v", laufID, err)
		}
		logf("lauf %s: %s — %v", laufID, e.Status, grund)
		return fmt.Errorf("lauf %s (%s): %w", laufID, a.Name, grund)
	}

	u, err := lauf.Neue(r.Root, a, laufID, input)
	if err != nil {
		return id, scheitere(haseResult{}, err)
	}
	if _, err := RunGaenge(ctx, u, a, r.GangTimeout); err != nil {
		return id, scheitere(haseResult{}, err)
	}
	prompt, err := BuildPrompt(u, a, r.Store)
	if err != nil {
		return id, scheitere(haseResult{}, err)
	}

	erg, err := r.runHase(ctx, a, laufID, prompt, logf)
	if err != nil {
		return id, scheitere(erg, err)
	}
	if err := RunAfter(u, a); err != nil {
		return id, scheitere(erg, err)
	}

	if u.Work != "" {
		if err := os.RemoveAll(filepath.Join(r.Root, u.Work)); err != nil {
			logf("lauf %s: $WORK aufräumen: %v", laufID, err)
		}
	}
	erg.Status = "ok"
	r.storeTrace(id, erg, logf)
	if err := r.Store.EndLauf(id, erg.alsErgebnis()); err != nil {
		return id, err
	}
	logf("lauf %s: ok — %s", laufID, erg.Summary)
	return id, nil
}

// haseResult sammelt, was der LLM-Schritt über sich weiß.
type haseResult struct {
	Status    string
	SessionID string
	Summary   string
	TokensIn  int64
	TokensOut int64
	CostCent  int64
	Trace     *opencode.Trace // nil, wenn der Lauf vor der Auswertung scheiterte
}

func (h haseResult) alsErgebnis() store.LaufResult {
	return store.LaufResult{
		Status:    h.Status,
		SessionID: h.SessionID,
		Summary:   h.Summary,
		TokensIn:  h.TokensIn,
		TokensOut: h.TokensOut,
		CostCent:  h.CostCent,
	}
}

// runHase macht den LLM-Schritt: Session anlegen, Prompt
// asynchron an den generierten Agenten, Event-Stream mitlesen
// (Tool-Calls loggen), auf das Ende warten, dann die Session auswerten.
//
// Fertig ist der Lauf, wenn einer von drei Zeugen es sagt — was zuerst
// eintritt: der Stream meldet session.idle, der synchrone Prompt-Call
// kommt sauber zurück, oder die Statusabfrage sieht die Session nicht
// mehr als busy. Der dritte ist der einzige, der sich nachfragen lässt
// statt abgewartet werden zu müssen, und deshalb der einzige, der ein
// verlorenes Ereignis überlebt (Hasenbau-0f4).
func (r *Runner) runHase(ctx context.Context, a *auftrag.Auftrag, laufID, prompt string, logf func(string, ...any)) (haseResult, error) {
	// Der Auftrag gewinnt gegen die Vorgabe: er weiß, was seine Arbeit
	// kostet. Für einen Einsortier-Lauf sind 30 Minuten großzügig, ein
	// Baumeister auf einem großen Trace lief damit ins Limit
	// (Hasenbau-uh0).
	timeout := a.HaseTimeout
	if timeout == 0 {
		timeout = r.HaseTimeout
	}
	if timeout == 0 {
		timeout = DefaultHaseTimeout
	}
	oben := ctx // ohne Zeitlimit: unterscheidet Timeout von Strg-C
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	url := r.BaseURL()
	if url == "" {
		return haseResult{}, fmt.Errorf("kein opencode-Server erreichbar")
	}
	client := opencode.New(url)

	sess, err := client.Session.New(ctx, sdk.SessionNewParams{
		Title: sdk.F(a.Name + " " + laufID),
	})
	if err != nil {
		return haseResult{}, fmt.Errorf("session anlegen: %w", err)
	}
	erg := haseResult{SessionID: sess.ID}

	// Erst abonnieren, dann prompten — sonst kann idle am Abo vorbeilaufen.
	ereignisse, abmelden := r.Funnel.Subscribe(sess.ID)
	defer abmelden()

	promptFehler := make(chan error, 1)
	go func() {
		_, err := client.Session.Prompt(ctx, sess.ID, sdk.SessionPromptParams{
			Parts: sdk.F([]sdk.SessionPromptParamsPartUnion{opencode.TextPart(prompt)}),
			Agent: sdk.F(hase.AgentName(a)),
		})
		promptFehler <- err
	}()

	// Dritter Zeuge: der abfragbare Zustand der Session. Der Stream
	// meldet das Ende nur einmal — wer den Moment verpasst, wartet
	// sonst bis zum Timeout auf etwas, das längst passiert ist
	// (Hasenbau-0f4). Akzeptiert wird das Urteil erst nach einem
	// beobachteten Übergang busy → nicht mehr busy; sonst gälte eine
	// Session, die noch gar nicht angelaufen ist, als fertig.
	takt := r.StatusInterval
	if takt == 0 {
		takt = statusInterval
	}
	nachfragen := time.NewTicker(takt)
	defer nachfragen.Stop()
	warBusy := false
	begonnen := time.Now()
	letzteMeldung := begonnen

	gesehen := false // schon Stream-Ereignisse für diese Session?
	grund := "session.idle"
warten:
	for {
		select {
		case ev := <-ereignisse:
			switch {
			case ev.Idle:
				break warten
			case ev.Error != "":
				return erg, fmt.Errorf("session %s: %s", sess.ID, ev.Error)
			case ev.Tool != nil:
				gesehen = true
				zeile := fmt.Sprintf("lauf %s: tool %s [%s] %s", laufID, ev.Tool.Name, ev.Tool.CallID, ev.Tool.Status)
				if ev.Tool.Error != "" {
					zeile += " — " + ev.Tool.Error
				}
				logf("%s", zeile)
			case ev.Reconnected:
				logf("lauf %s: Event-Stream neu verbunden — ein verlorenes idle fängt die Statusabfrage", laufID)
			}
		case err := <-promptFehler:
			if err == nil {
				grund = "den Prompt-Call"
				break warten // synchroner Call kam sauber zurück
			}
			if !gesehen {
				// Server hat den Prompt abgelehnt, bevor irgendetwas
				// lief (Config-/Agent-Fehler) — nicht auf idle warten.
				return erg, fmt.Errorf("prompt: %w", err)
			}
			// Reconnected bei laufender Session (Spike-Befund) — der Stream
			// bleibt die Wahrheitsquelle, der Timeout der Backstop.
			logf("lauf %s: prompt-Call riss ab (%v) — warte auf session.idle", laufID, err)
			promptFehler = nil
		case <-nachfragen.C:
			busy, err := opencode.SessionBusy(ctx, client, sess.ID)
			if err != nil {
				// Server gerade weg oder im Neustart: der Supervisor
				// kümmert sich, hier bleibt der Timeout der Backstop.
				continue
			}
			if busy {
				warBusy = true
				// Ein Hase kann minutenlang denken, ohne ein Werkzeug
				// anzufassen — nach dem letzten Tool-Log passiert dann
				// sichtbar nichts mehr. Wer das für einen hängenden
				// Prozess hält, drückt Strg-C und bricht einen Lauf ab,
				// der gerade arbeitet (Hasenbau-0f4).
				if time.Since(letzteMeldung) >= progressInterval {
					letzteMeldung = time.Now()
					logf("lauf %s: der Hase arbeitet noch (seit %s)", laufID,
						time.Since(begonnen).Round(time.Second))
				}
				continue
			}
			if warBusy {
				grund = "die Statusabfrage (session.idle kam nie an)"
				break warten
			}
		case <-ctx.Done():
			// „context deadline exceeded" sagt niemandem, was zu tun
			// ist. Gemessen: ein Baumeister auf einem großen Trace
			// braucht zwischen 3 und über 30 Minuten — dasselbe
			// Material, dasselbe Modell (Hasenbau-0f4).
			if errors.Is(ctx.Err(), context.DeadlineExceeded) && oben.Err() == nil {
				return erg, fmt.Errorf("hase: nach %s abgebrochen — das Zeitlimit für den LLM-Schritt ist %s (Session %s lief noch)",
					time.Since(begonnen).Round(time.Second), timeout, sess.ID)
			}
			return erg, fmt.Errorf("hase: %w", ctx.Err())
		}
	}
	logf("lauf %s: der Hase ist fertig — beendet durch %s", laufID, grund)

	// Auswertung auch dann, wenn ctx gleich abläuft: eigener kurzer
	// Kontext, die Daten liegen ja schon beim Server.
	mctx, mcancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer mcancel()
	msgs, err := client.Session.Messages(mctx, sess.ID, sdk.SessionMessagesParams{})
	if err != nil {
		return erg, fmt.Errorf("session %s auswerten: %w", sess.ID, err)
	}
	erg.Summary, erg.TokensIn, erg.TokensOut, erg.CostCent = evaluate(*msgs)
	erg.Trace = opencode.TraceFrom(sess.ID, *msgs).Truncated(traceMax)
	return erg, nil
}

// storeTrace schreibt den Verlauf zum Lauf — einmal als Protokoll zum
// Lesen (trace) und einmal normalisiert zum Rechnen (tool_calls, §8
// Phase 2). Ein Fehler dabei wird gemeldet, scheitert den Lauf aber
// nie: beides ist Material für später, das Ergebnis des Laufs hängt
// nicht daran.
func (r *Runner) storeTrace(id int64, erg haseResult, logf func(string, ...any)) {
	if erg.Trace == nil || erg.SessionID == "" {
		return
	}
	roh, err := json.Marshal(erg.Trace)
	if err == nil {
		err = r.Store.WriteTrace(id, erg.SessionID, roh)
	}
	if err != nil {
		logf("lauf %d: Trace nicht abgelegt: %v", id, err)
	}
	if err := r.Store.WriteToolCalls(id, toolCalls(erg.Trace)); err != nil {
		logf("lauf %d: Tool-Calls nicht abgelegt: %v", id, err)
	}
}

// evaluate zieht Summary, Tokens und Kosten aus den Session-Messages.
// Die Summary hier ist nur der Fallback — der Text der letzten
// Assistant-Message. Hat der Hase seine Summary über den Rückkanal
// geschrieben, gewinnt sie (§5, §8 Phase 2). In eine Zeile presst sie
// der Store, der die Invariante hält.
func evaluate(msgs []sdk.SessionMessagesResponse) (summary string, tokensIn, tokensOut, kostenCent int64) {
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
