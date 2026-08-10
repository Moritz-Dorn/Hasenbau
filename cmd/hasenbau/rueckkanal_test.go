package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// mcpServer stellt einen opencode-Server nach, der auf /mcp die
// übergebene Antwort liefert.
func mcpServer(t *testing.T, antwort string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/mcp" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(antwort))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestRueckkanalVerbundenIstInOrdnung(t *testing.T) {
	url := mcpServer(t, `{"hasenbau":{"status":"connected"}}`)
	if err := verifyBackchannel(context.Background(), url); err != nil {
		t.Errorf("verbundener Rückkanal soll durchgehen, bekam: %v", err)
	}
}

// Der Fall aus Hasenbau-08u: der MCP-Client kam nicht hoch, opencode
// sagt nichts, der Hase bekommt die Werkzeuge nicht. Das muss der
// Grund sein, aus dem gar kein Lauf startet — und der Grund muss in
// der Meldung stehen.
func TestRueckkanalGescheitertNenntDenGrund(t *testing.T) {
	url := mcpServer(t, `{"hasenbau":{"status":"failed","error":"ENOENT: no such file or directory, posix_spawn '/tmp/weg/hasenbau'"}}`)
	err := verifyBackchannel(context.Background(), url)
	if err == nil {
		t.Fatal("gescheiterter Rückkanal muss ein Fehler sein")
	}
	for _, teil := range []string{"failed", "posix_spawn", ".opencode-home"} {
		if !strings.Contains(err.Error(), teil) {
			t.Errorf("Meldung nennt %q nicht: %v", teil, err)
		}
	}
}

func TestRueckkanalFehltGanz(t *testing.T) {
	url := mcpServer(t, `{"anderer":{"status":"connected"}}`)
	err := verifyBackchannel(context.Background(), url)
	if err == nil || !strings.Contains(err.Error(), "kennt keinen MCP-Server") {
		t.Errorf("fehlender Eintrag muss auffallen, bekam: %v", err)
	}
}

// Zustände ohne error-Feld (disabled, needs_auth) sind genauso
// untauglich — die Werkzeuge kommen nicht beim Hasen an.
func TestRueckkanalAbgeschaltetIstAuchEinFehler(t *testing.T) {
	url := mcpServer(t, `{"hasenbau":{"status":"disabled"}}`)
	err := verifyBackchannel(context.Background(), url)
	if err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Errorf("abgeschalteter Rückkanal muss auffallen, bekam: %v", err)
	}
}

func TestRueckkanalServerNichtErreichbar(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close() // Port ist jetzt tot

	err := verifyBackchannel(context.Background(), url)
	if err == nil || !strings.Contains(err.Error(), "nicht abfragbar") {
		t.Errorf("unerreichbarer Server muss auffallen, bekam: %v", err)
	}
}
