package opencode

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// nachrichtenMitTrace bildet die in §11.3 verifizierte Antwort einer
// echten Session nach: reasoning, erfolgreicher Tool-Call, abgewehrter
// Tool-Call (status=error), patch, finale Antwort.
const nachrichtenMitTrace = `[
  {"info":{"id":"msg_0","role":"user","sessionID":"ses_t","time":{"created":1}},
   "parts":[{"id":"prt_0","messageID":"msg_0","sessionID":"ses_t","type":"text","text":"Sortiere ein."}]},
  {"info":{"id":"msg_1","role":"assistant","sessionID":"ses_t","time":{"created":1},
           "cost":0,"tokens":{"input":1,"output":1,"reasoning":0,"cache":{"read":0,"write":0}},
           "modelID":"m","providerID":"p","mode":"primary","path":{"cwd":"/","root":"/"},"system":[],"parentID":""},
   "parts":[
     {"id":"prt_1","messageID":"msg_1","sessionID":"ses_t","type":"step-start"},
     {"id":"prt_2","messageID":"msg_1","sessionID":"ses_t","type":"reasoning","text":"Erst lesen, dann schreiben."},
     {"id":"prt_3","messageID":"msg_1","sessionID":"ses_t","type":"tool","callID":"call_1","tool":"write",
      "state":{"status":"completed","input":{"filePath":"raeume/lager/x.md","content":"Inhalt"},
               "output":"Wrote file successfully.","title":"write","time":{"start":1784058737705,"end":1784058737722}}},
     {"id":"prt_4","messageID":"msg_1","sessionID":"ses_t","type":"tool","callID":"call_2","tool":"write",
      "state":{"status":"error","input":{"filePath":"/tmp/escape.md","content":"x"},
               "error":"The user has specified a rule which prevents this.","time":{"start":1784058737800,"end":1784058737801}}},
     {"id":"prt_5","messageID":"msg_1","sessionID":"ses_t","type":"patch","hash":"h","files":["x.md"]},
     {"id":"prt_6","messageID":"msg_1","sessionID":"ses_t","type":"step-finish"},
     {"id":"prt_7","messageID":"msg_1","sessionID":"ses_t","type":"text","text":"Abgelegt: x.md"}
   ]}
]`

func TestZieheTraceNormalisiert(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /session/ses_t/message", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, nachrichtenMitTrace)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	trace, err := ZieheTrace(context.Background(), New(srv.URL), "ses_t")
	if err != nil {
		t.Fatal(err)
	}
	if trace.SessionID != "ses_t" {
		t.Errorf("SessionID = %q", trace.SessionID)
	}

	// Reihenfolge = Ausführungsreihenfolge; step-start/-finish fehlen.
	arten := make([]string, len(trace.Schritte))
	for i, s := range trace.Schritte {
		arten[i] = s.Art
	}
	erwartet := []string{"text", "reasoning", "tool", "tool", "patch", "text"}
	if strings.Join(arten, ",") != strings.Join(erwartet, ",") {
		t.Fatalf("Arten = %v, erwartet %v", arten, erwartet)
	}

	ok := trace.Schritte[2]
	if ok.Tool != "write" || ok.CallID != "call_1" || ok.Status != "completed" ||
		!strings.Contains(ok.Input, `"filePath":"raeume/lager/x.md"`) ||
		ok.Output != "Wrote file successfully." || ok.Start == nil || ok.Ende == nil {
		t.Errorf("Erfolgs-Tool = %+v", ok)
	}
	if d := ok.Ende.Sub(*ok.Start).Milliseconds(); d != 17 {
		t.Errorf("Dauer = %dms, erwartet 17", d)
	}

	fehl := trace.Schritte[3]
	if fehl.Status != "error" || !strings.Contains(fehl.Fehler, "rule which prevents") ||
		!strings.Contains(fehl.Input, "/tmp/escape.md") {
		t.Errorf("Fehlversuch = %+v", fehl)
	}

	if trace.Schritte[0].Rolle != "user" || trace.Schritte[1].Rolle != "assistant" {
		t.Errorf("Rollen = %q/%q", trace.Schritte[0].Rolle, trace.Schritte[1].Rolle)
	}

	md := trace.Markdown()
	for _, muss := range []string{
		"[tool write — completed]",
		"[tool write — FEHLVERSUCH]",
		`"filePath":"raeume/lager/x.md"`,
		"[reasoning, assistant]",
		"Abgelegt: x.md",
	} {
		if !strings.Contains(md, muss) {
			t.Errorf("Markdown ohne %q:\n%s", muss, md)
		}
	}
}

func TestZieheTraceUnbekannteSession(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /session/ses_weg/message", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"data":{"message":"not found"}}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	_, err := ZieheTrace(context.Background(), New(srv.URL), "ses_weg")
	if err == nil || !strings.Contains(err.Error(), "ses_weg") {
		t.Fatalf("erwartete Fehler mit Session-ID, bekam %v", err)
	}
}
