package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Moritz-Dorn/Hasenbau/internal/auftrag"
	"github.com/Moritz-Dorn/Hasenbau/internal/opencode"
	"github.com/Moritz-Dorn/Hasenbau/internal/store"
)

// testAuftrag deckt die ganze Pipeline ab: ein Gang schreibt die
// Kontext-Datei, der Hase wird geprompt, nachher räumt auf.
const testAuftragQuelle = `---
trigger:
  cron: "0 7 * * *"
gaenge:
  - name: extrakt
    run: echo Inhalt > "$WORK/extrakt.md"
hase: testhase
raeume:
  work: raeume/werkstatt/
  out:  raeume/lager/
context:
  - file: $WORK/extrakt.md
---
Sortiere ein.
`

// fakeOpencode simuliert die vier Endpoints, die ein Lauf braucht. Der
// Prompt-Call antwortet nie (Spike-Befund: er reißt bei langen Läufen
// ohnehin ab) — das Lauf-Ende MUSS über den Event-Stream kommen.
type fakeOpencode struct {
	t           *testing.T
	ereignisse  []string // SSE-data-Zeilen, gesendet nachdem der Prompt ankam
	nachrichten string   // Antwort auf GET /session/ses_1/message

	verbunden  sync.Once
	streamDa   chan struct{} // /event hängt am Funnel
	promptDa   chan struct{} // Prompt ist eingegangen
	mu         sync.Mutex
	promptBody map[string]any

	// fertigNachEvents lässt die Session nach dem letzten Ereignis aus
	// /session/status verschwinden — so sieht ein Lauf aus, dessen
	// session.idle unterwegs verloren ging (Hasenbau-0f4). Die erste
	// Abfrage meldet trotzdem noch busy: der Runner akzeptiert das Ende
	// erst nach einem beobachteten Übergang.
	fertigNachEvents bool
	eventsGesendet   bool
	busyGemeldet     bool
	nichtMehrBusy    bool // hart: von Anfang an nichts im Status
}

func neuerFake(t *testing.T, ereignisse []string, nachrichten string) (*fakeOpencode, *httptest.Server) {
	f := &fakeOpencode{
		t: t, ereignisse: ereignisse, nachrichten: nachrichten,
		streamDa: make(chan struct{}), promptDa: make(chan struct{}),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /session", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"ses_1"}`)
	})
	mux.HandleFunc("GET /event", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.(http.Flusher).Flush()
		f.verbunden.Do(func() { close(f.streamDa) })
		select {
		case <-f.promptDa:
		case <-r.Context().Done():
			return
		}
		for _, e := range f.ereignisse {
			fmt.Fprintf(w, "data: %s\n\n", e)
			w.(http.Flusher).Flush()
		}
		f.mu.Lock()
		f.eventsGesendet = true
		f.mu.Unlock()
		<-r.Context().Done()
	})
	mux.HandleFunc("GET /session/status", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		weg := f.nichtMehrBusy || (f.fertigNachEvents && f.eventsGesendet && f.busyGemeldet)
		if !weg {
			f.busyGemeldet = true
		}
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if weg {
			fmt.Fprint(w, `{}`)
			return
		}
		fmt.Fprint(w, `{"ses_1":{"type":"busy"}}`)
	})
	mux.HandleFunc("POST /session/ses_1/message", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
			f.mu.Lock()
			f.promptBody = body
			f.mu.Unlock()
		}
		close(f.promptDa)
		<-r.Context().Done() // nie antworten — der Stream ist die Wahrheitsquelle
	})
	mux.HandleFunc("GET /session/ses_1/message", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, f.nachrichten)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return f, srv
}

// bauMitAuftrag legt einen Test-Bau im TempDir an und parst den Auftrag.
func bauMitAuftrag(t *testing.T) (string, *auftrag.Auftrag, *store.Store) {
	t.Helper()
	root := t.TempDir()
	a, err := auftrag.Parse("test-auftrag", []byte(testAuftragQuelle))
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(root, "state", "hasenbau.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return root, a, st
}

const nachrichtenOK = `[
  {"info":{"id":"msg_0","role":"user","sessionID":"ses_1","time":{"created":1}},
   "parts":[{"id":"prt_0","messageID":"msg_0","sessionID":"ses_1","type":"text","text":"prompt"}]},
  {"info":{"id":"msg_1","role":"assistant","sessionID":"ses_1","time":{"created":1},
           "cost":0.0234,"tokens":{"input":120,"output":30,"reasoning":0,"cache":{"read":0,"write":0}},
           "modelID":"m","providerID":"p","mode":"primary","path":{"cwd":"/","root":"/"},"system":[],"parentID":""},
   "parts":[{"id":"prt_2","messageID":"msg_1","sessionID":"ses_1","type":"text","text":"Alles  einsortiert.` + "\\n" + `Fertig."}]}
]`

func TestFuehreAusEndeUeberEventStream(t *testing.T) {
	f, srv := neuerFake(t, []string{
		`{"type":"message.part.updated","properties":{"part":{"id":"prt_1","messageID":"msg_1","sessionID":"ses_1","type":"tool","callID":"call_1","tool":"write","state":{"status":"completed","input":{},"output":"ok","title":"write","time":{"start":1,"end":2}}}}}`,
		`{"type":"session.idle","properties":{"sessionID":"ses_1"}}`,
	}, nachrichtenOK)
	root, a, st := bauMitAuftrag(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var mu sync.Mutex
	var logZeilen []string
	logf := func(format string, args ...any) {
		mu.Lock()
		logZeilen = append(logZeilen, fmt.Sprintf(format, args...))
		mu.Unlock()
	}

	funnel := opencode.NewFunnel(func() string { return srv.URL }, logf)
	funnel.Start(ctx)
	<-f.streamDa // Funnel hängt am Stream, bevor der Lauf beginnt

	r := &Runner{
		Root: root, BaseURL: func() string { return srv.URL },
		Store: st, Funnel: funnel,
		HaseTimeout: 10 * time.Second, Logf: logf,
	}
	if _, err := r.Execute(ctx, a, "manual", ""); err != nil {
		t.Fatal(err)
	}

	// Prompt: richtiger Agent, Body enthält Auftrags-Kern und Kontext.
	f.mu.Lock()
	body := f.promptBody
	f.mu.Unlock()
	if body["agent"] != "test-auftrag__testhase" {
		t.Errorf("agent = %v", body["agent"])
	}
	parts := body["parts"].([]any)
	text := parts[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, "Sortiere ein.") || !strings.Contains(text, "Inhalt") {
		t.Errorf("Prompt-Text = %q", text)
	}

	// laeufe-Zeile vollständig (Akzeptanzkriterium q4y).
	laeufe, err := st.RecentLaeufe(1)
	if err != nil || len(laeufe) != 1 {
		t.Fatalf("laeufe: %v, %v", laeufe, err)
	}
	l := laeufe[0]
	if l.Status != "ok" || l.SessionID != "ses_1" || l.Ended == nil {
		t.Errorf("Lauf = %+v", l)
	}
	if l.Summary != "Alles einsortiert. Fertig." {
		t.Errorf("Summary = %q", l.Summary)
	}
	if l.TokensIn != 120 || l.TokensOut != 30 || l.CostCent != 2 {
		t.Errorf("Tokens/Kosten = %d/%d/%d", l.TokensIn, l.TokensOut, l.CostCent)
	}

	// Tool-Call wurde strukturiert geloggt.
	mu.Lock()
	defer mu.Unlock()
	gefunden := false
	for _, z := range logZeilen {
		if strings.Contains(z, "tool write") && strings.Contains(z, "completed") {
			gefunden = true
		}
	}
	if !gefunden {
		t.Errorf("kein Tool-Log gefunden in %q", logZeilen)
	}

	// $WORK ist aufgeräumt, der work-Raum leer.
	eintraege, err := os.ReadDir(filepath.Join(root, "raeume", "werkstatt"))
	if err != nil || len(eintraege) != 0 {
		t.Errorf("werkstatt nicht leer: %v, %v", eintraege, err)
	}
}

func TestFuehreAusSessionError(t *testing.T) {
	f, srv := neuerFake(t, []string{
		`{"type":"session.error","properties":{"sessionID":"ses_1","error":{"name":"UnknownError","data":{"message":"kaputt"}}}}`,
	}, nachrichtenOK)
	root, a, st := bauMitAuftrag(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	funnel := opencode.NewFunnel(func() string { return srv.URL }, t.Logf)
	funnel.Start(ctx)
	<-f.streamDa

	r := &Runner{
		Root: root, BaseURL: func() string { return srv.URL },
		Store: st, Funnel: funnel,
		HaseTimeout: 10 * time.Second, Logf: t.Logf,
	}
	_, err := r.Execute(ctx, a, "manual", "")
	if err == nil || !strings.Contains(err.Error(), "UnknownError") {
		t.Fatalf("erwartete session.error, bekam %v", err)
	}

	laeufe, _ := st.RecentLaeufe(1)
	l := laeufe[0]
	if l.Status != "failed" || !strings.Contains(l.Error, "UnknownError") || l.SessionID != "ses_1" {
		t.Errorf("Lauf = %+v", l)
	}
	// Fehlerfall: $WORK bleibt zur Nachforschung liegen (§6, Ablauf 7).
	eintraege, err := os.ReadDir(filepath.Join(root, "raeume", "werkstatt"))
	if err != nil || len(eintraege) != 1 {
		t.Errorf("werkstatt: %v, %v", eintraege, err)
	}
}

func TestFuehreAusPromptAbgelehnt(t *testing.T) {
	// Server lehnt den Prompt sofort ab (Config-Fehler) — kein Event
	// wird je kommen; der Lauf muss schnell und als 'fehler' enden.
	mux := http.NewServeMux()
	mux.HandleFunc("POST /session", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"ses_1"}`)
	})
	streamDa := make(chan struct{})
	var einmal sync.Once
	mux.HandleFunc("GET /event", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.(http.Flusher).Flush()
		einmal.Do(func() { close(streamDa) })
		<-r.Context().Done()
	})
	mux.HandleFunc("POST /session/ses_1/message", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"data":{"message":"kein Modell"}}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	root, a, st := bauMitAuftrag(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	funnel := opencode.NewFunnel(func() string { return srv.URL }, t.Logf)
	funnel.Start(ctx)
	<-streamDa

	r := &Runner{
		Root: root, BaseURL: func() string { return srv.URL },
		Store: st, Funnel: funnel,
		HaseTimeout: time.Minute, Logf: t.Logf,
	}
	anfang := time.Now()
	_, err := r.Execute(ctx, a, "manual", "")
	if err == nil || !strings.Contains(err.Error(), "prompt") {
		t.Fatalf("erwartete Prompt-Fehler, bekam %v", err)
	}
	if time.Since(anfang) > 10*time.Second {
		t.Error("abgelehnter Prompt darf nicht auf den Timeout warten")
	}
	laeufe, _ := st.RecentLaeufe(1)
	if laeufe[0].Status != "failed" {
		t.Errorf("Status = %q", laeufe[0].Status)
	}
}

// Hasenbau-0f4: Der Hase ist fertig, aber session.idle ist unterwegs
// verloren gegangen und der Prompt-Call antwortet nie. Ohne dritten
// Zeugen hinge der Lauf bis zum HaseTimeout — hier muss ihn die
// Statusabfrage beenden.
func TestFuehreAusEndeUeberStatusabfrage(t *testing.T) {
	f, srv := neuerFake(t, []string{
		`{"type":"message.part.updated","properties":{"part":{"id":"prt_1","messageID":"msg_1","sessionID":"ses_1","type":"tool","callID":"call_1","tool":"write","state":{"status":"completed","input":{},"output":"ok","title":"write","time":{"start":1,"end":2}}}}}`,
	}, nachrichtenOK)
	f.fertigNachEvents = true // kein session.idle, Session verschwindet aus dem Status
	root, a, st := bauMitAuftrag(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var mu sync.Mutex
	var logZeilen []string
	logf := func(format string, args ...any) {
		mu.Lock()
		logZeilen = append(logZeilen, fmt.Sprintf(format, args...))
		mu.Unlock()
	}

	funnel := opencode.NewFunnel(func() string { return srv.URL }, logf)
	funnel.Start(ctx)
	<-f.streamDa

	r := &Runner{
		Root: root, BaseURL: func() string { return srv.URL },
		Store: st, Funnel: funnel,
		StatusInterval: 50 * time.Millisecond,
		// Deutlich kürzer als der Vorgabe-Timeout: schlägt der dritte
		// Zeuge fehl, scheitert der Test schnell statt nach 30 Minuten.
		HaseTimeout: 15 * time.Second, Logf: logf,
	}
	if _, err := r.Execute(ctx, a, "manual", ""); err != nil {
		t.Fatal(err)
	}

	laeufe, err := st.RecentLaeufe(1)
	if err != nil || len(laeufe) != 1 {
		t.Fatalf("laeufe: %v, %v", laeufe, err)
	}
	if l := laeufe[0]; l.Status != "ok" || l.Summary != "Alles einsortiert. Fertig." {
		t.Errorf("Lauf = %+v", l)
	}

	// Und es steht im Log, welcher Zeuge den Lauf beendet hat.
	mu.Lock()
	defer mu.Unlock()
	gefunden := false
	for _, z := range logZeilen {
		if strings.Contains(z, "Statusabfrage") && strings.Contains(z, "fertig") {
			gefunden = true
		}
	}
	if !gefunden {
		t.Errorf("kein Hinweis auf die Statusabfrage in %q", logZeilen)
	}
}

// Umgekehrt darf eine Session, die noch gar nicht angelaufen ist, nicht
// als fertig gelten: ohne beobachtetes busy zählt „nicht busy" nicht.
func TestStatusabfrageBeendetKeinenLaufOhneBusy(t *testing.T) {
	f, srv := neuerFake(t, nil, nachrichtenOK)
	f.mu.Lock()
	f.nichtMehrBusy = true // von Anfang an nichts im Status
	f.mu.Unlock()
	root, a, st := bauMitAuftrag(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	funnel := opencode.NewFunnel(func() string { return srv.URL }, t.Logf)
	funnel.Start(ctx)
	<-f.streamDa

	r := &Runner{
		Root: root, BaseURL: func() string { return srv.URL },
		Store: st, Funnel: funnel,
		StatusInterval: 20 * time.Millisecond,
		HaseTimeout:    600 * time.Millisecond, Logf: t.Logf,
	}
	_, err := r.Execute(ctx, a, "manual", "")
	if err == nil || !strings.Contains(err.Error(), "Zeitlimit") {
		t.Fatalf("erwartet: Lauf läuft in den Timeout statt sich fertig zu glauben, bekam %v", err)
	}
}

// Hasenbau-uh0: Das Zeitlimit des Auftrags sticht die Vorgabe des
// Runners. Sichtbar wird es an der Meldung, die das Limit nennt.
func TestAuftragsZeitlimitSchlaegtVorgabe(t *testing.T) {
	f, srv := neuerFake(t, nil, nachrichtenOK)
	root, a, st := bauMitAuftrag(t)
	a.HaseTimeout = 300 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	funnel := opencode.NewFunnel(func() string { return srv.URL }, t.Logf)
	funnel.Start(ctx)
	<-f.streamDa

	r := &Runner{
		Root: root, BaseURL: func() string { return srv.URL },
		Store: st, Funnel: funnel,
		StatusInterval: 50 * time.Millisecond,
		HaseTimeout:    time.Hour, // würde den Test hängen lassen, wenn sie gälte
		Logf:           t.Logf,
	}
	_, err := r.Execute(ctx, a, "manual", "")
	// Auf die Erklärung prüfen, nicht nur auf die Zahl: die alte Fassung
	// lieferte je nach Zufall „prompt: context deadline exceeded" und
	// damit weder das eine noch das andere (Hasenbau-eav).
	if err == nil || !strings.Contains(err.Error(), "300ms") || !strings.Contains(err.Error(), "Zeitlimit") {
		t.Fatalf("erwartet: Abbruch nach dem Limit des Auftrags, bekam %v", err)
	}
}

// Hasenbau-eav: Dieselbe Lage kommt über zwei Zweige herein — über
// ctx.Done() und über den am selben Kontext abgebrochenen Prompt-Call.
// Welcher zuerst dran ist, entscheidet select zufällig, deshalb muss die
// Entscheidung an einer Stelle stehen. Hier direkt geprüft: ohne Uhr,
// ohne Server, ohne Zufall.
func TestZeitlimitFehlerUnterscheidetTimeoutVonAbbruch(t *testing.T) {
	begonnen := time.Now().Add(-2 * time.Second)

	// Eigenes Zeitlimit zugeschlagen, von außen kam nichts.
	abgelaufen, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Millisecond))
	defer stop()
	err := zeitlimitFehler(abgelaufen, context.Background(), begonnen, 30*time.Minute, "ses_1")
	if err == nil {
		t.Fatal("kein Fehler bei abgelaufenem Zeitlimit")
	}
	for _, muss := range []string{"Zeitlimit", "30m", "ses_1", "2s"} {
		if !strings.Contains(err.Error(), muss) {
			t.Errorf("Meldung ohne %q: %v", muss, err)
		}
	}
	// Gekürzte Schreibweise, nicht „30m0s".
	if strings.Contains(err.Error(), "30m0s") {
		t.Errorf("ungekürzte Dauer: %v", err)
	}

	// Strg-C: der äußere Kontext ist zu, das ist kein Zeitlimit-Fall.
	obenZu, obenStop := context.WithCancel(context.Background())
	obenStop()
	if err := zeitlimitFehler(abgelaufen, obenZu, begonnen, time.Minute, "ses_1"); err != nil {
		t.Errorf("Abbruch von außen als Zeitlimit gemeldet: %v", err)
	}

	// Nichts abgelaufen: kein Fehler.
	if err := zeitlimitFehler(context.Background(), context.Background(), begonnen, time.Minute, "ses_1"); err != nil {
		t.Errorf("Fehler ohne Anlass: %v", err)
	}
}

// Hasenbau-bnh: Ein Dateiname mit Shell-Syntax darf nicht bloß
// „irgendwann" scheitern — er muss scheitern, BEVOR ein Gang läuft.
// Der Beweis ist die Datei, die der Gang geschrieben hätte.
func TestGefaehrlicherInputStartetKeinenGang(t *testing.T) {
	root := t.TempDir()
	a, err := auftrag.Parse("test-auftrag", []byte(`---
trigger:
  watch: "*.pdf"
gaenge:
  - name: marker
    run: echo da > "$BAU/beweis.txt"
hase: testhase
raeume:
  input: raeume/laderampe/
  work: raeume/werkstatt/
---
Sortiere ein.
`))
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(root, "state", "hasenbau.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	r := &Runner{Root: root, BaseURL: func() string { return "" }, Store: st, Logf: t.Logf}
	id, err := r.Execute(context.Background(), a, "watch", `raeume/laderampe/x";touch "$BAU/beweis.txt";"y.pdf`)
	if err == nil {
		t.Fatal("gefährlicher Input wurde angenommen")
	}
	if _, statErr := os.Stat(filepath.Join(root, "beweis.txt")); statErr == nil {
		t.Error("der Gang lief — die Prüfung kommt zu spät")
	}

	// Der Lauf steht trotzdem sauber in der DB, mit dem Grund.
	l, err := st.LaufByID(id)
	if err != nil {
		t.Fatal(err)
	}
	if l.Status != "failed" || !strings.Contains(l.Error, "brechen aus der Gang-Zeile aus") {
		t.Errorf("Lauf = %+v", l)
	}
}

// Hasenbau-4cx.1: Aus dem Trace werden Zeilen zum Rechnen. Genommen
// wird der Trace und nicht der Event-Stream — der meldet denselben
// Aufruf dreimal.
func TestToolCallsAusTrace(t *testing.T) {
	start := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	ende := start.Add(1500 * time.Millisecond)
	tr := &opencode.Trace{Steps: []opencode.TraceStep{
		{Kind: "text", Role: "user", Text: "los"},
		{Kind: "tool", Tool: "read", Input: `{"path":"a"}`, Status: "completed", Start: &start, End: &ende},
		{Kind: "reasoning", Text: "denk"},
		{Kind: "tool", Tool: "write", Input: `{"path":"b"}`, Status: "error", Error: "permission denied"},
	}}

	calls := toolCalls(tr)
	if len(calls) != 2 {
		t.Fatalf("%d Aufrufe, erwartet 2 (text und reasoning gehören nicht dazu)", len(calls))
	}
	if calls[0].Nr != 1 || calls[0].Tool != "read" || calls[0].DurationMs != 1500 {
		t.Errorf("erster Aufruf: %+v", calls[0])
	}
	if calls[1].Nr != 2 || calls[1].Status != "error" || calls[1].Error != "permission denied" {
		t.Errorf("zweiter Aufruf: %+v", calls[1])
	}
	if s := store.Signature(calls); s != "read>write" {
		t.Errorf("Signatur = %q", s)
	}

	if calls := toolCalls(nil); calls != nil {
		t.Errorf("ohne Trace: %+v", calls)
	}
}

// ToolCallsFromTrace ist derselbe Weg über abgelegtes JSON — der
// Nachzug alter Läufe hängt daran.
func TestToolCallsAusAbgelegtemTrace(t *testing.T) {
	roh := []byte(`{"session_id":"ses_1","steps":[
		{"kind":"tool","tool":"glob","input":"{}","status":"completed"},
		{"kind":"text","role":"assistant","text":"fertig"}]}`)
	calls, err := ToolCallsFromTrace(roh)
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0].Tool != "glob" {
		t.Errorf("calls = %+v", calls)
	}
	if _, err := ToolCallsFromTrace([]byte("kein json")); err == nil {
		t.Error("kaputtes JSON muss ein Fehler sein")
	}
}
