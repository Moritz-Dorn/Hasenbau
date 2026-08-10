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
		<-r.Context().Done()
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
	if err := r.Execute(ctx, a, "manual", ""); err != nil {
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
	err := r.Execute(ctx, a, "manual", "")
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
	err := r.Execute(ctx, a, "manual", "")
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
