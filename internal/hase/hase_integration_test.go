package hase

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Moritz-Dorn/Hasenbau/internal/auftrag"
	"github.com/Moritz-Dorn/Hasenbau/internal/bau"
	"github.com/Moritz-Dorn/Hasenbau/internal/opencode"
	"github.com/Moritz-Dorn/Hasenbau/internal/supervisor"
)

// auftragMD ist der §6-Beispiel-Auftrag, minimal gekürzt.
const auftragMD = `---
trigger:
  watch: "*.pdf"
hase: archivar
raeume:
  input: raeume/laderampe/sources/
  work: raeume/laderampe/work/
  out:  raeume/lager/
---
Sortiere ein.
`

type agentInfo struct {
	Name       string `json:"name"`
	Permission []struct {
		Permission string `json:"permission"`
		Pattern    string `json:"pattern"`
		Action     string `json:"action"`
	} `json:"permission"`
}

func holeAgent(t *testing.T, baseURL, name string) *agentInfo {
	t.Helper()
	resp, err := http.Get(baseURL + "/agent")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var agents []agentInfo
	if err := json.NewDecoder(resp.Body).Decode(&agents); err != nil {
		t.Fatal(err)
	}
	for i := range agents {
		if agents[i].Name == name {
			return &agents[i]
		}
	}
	return nil
}

// TestGenerierterAgentImEchtenServer prüft die ganze Kette: Bau anlegen,
// Template + Auftrag schreiben, Agent generieren, Server starten — der
// Agent muss mit den generierten Permissions auflösen. Danach Template
// verschärfen, neu generieren, DisposeInstance — die Änderung muss ohne
// Server-Restart sichtbar sein (§11.6). Kein LLM-Call.
func TestGenerierterAgentImEchtenServer(t *testing.T) {
	if _, err := exec.LookPath("opencode"); err != nil {
		t.Skip("opencode nicht im PATH — Integrationstest übersprungen")
	}

	root := t.TempDir()
	// Der Rückkanal-Eintrag zeigt hier ins Leere: geprüft wird die
	// Permission-Auflösung des Agenten, nicht das Werkzeug-Angebot.
	if _, err := bau.Init(root, filepath.Join(root, "kein-hasenbau")); err != nil {
		t.Fatal(err)
	}
	schreibeTemplate(t, root, "archivar", templateArchivar)
	if err := os.WriteFile(filepath.Join(root, "auftraege", "pdf-einlagern.md"), []byte(auftragMD), 0o644); err != nil {
		t.Fatal(err)
	}

	auftraege, err := auftrag.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	tpl, err := Lade(root, "archivar")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := SchreibeAgent(root, nimm(t, auftraege, "pdf-einlagern"), tpl, Optionen{}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	sup, err := supervisor.New(supervisor.Config{BauDir: root})
	if err != nil {
		t.Fatal(err)
	}
	if err := sup.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer sup.Stop()

	agent := holeAgent(t, sup.BaseURL(), "pdf-einlagern__archivar")
	if agent == nil {
		t.Fatal("generierter Agent pdf-einlagern__archivar fehlt im Server")
	}
	// Die Basis-Regeln des Auftrags müssen aufgelöst sein — und die
	// Template-Denies nach den Allows (letzte matchende Regel gewinnt).
	var allowIdx, denyIdx int = -1, -1
	for i, r := range agent.Permission {
		if r.Permission == "edit" && r.Pattern == "raeume/lager/**" && r.Action == "allow" {
			allowIdx = i
		}
		if r.Permission == "edit" && r.Pattern == "raeume/lager/geheim/**" && r.Action == "deny" {
			denyIdx = i
		}
	}
	if allowIdx < 0 || denyIdx < 0 || denyIdx < allowIdx {
		t.Fatalf("edit-Regeln falsch aufgelöst (allow=%d deny=%d): %+v", allowIdx, denyIdx, agent.Permission)
	}

	// Template verschärfen, neu generieren, Caches verwerfen: die neue
	// Regel muss ohne Server-Restart erscheinen.
	tpl.Denies = append(tpl.Denies, Regel{Permission: "edit", Pattern: "raeume/lager/intern/**"})
	if _, err := SchreibeAgent(root, nimm(t, auftraege, "pdf-einlagern"), tpl, Optionen{}); err != nil {
		t.Fatal(err)
	}
	if err := opencode.DisposeInstance(ctx, opencode.New(sup.BaseURL())); err != nil {
		t.Fatal(err)
	}
	agent = holeAgent(t, sup.BaseURL(), "pdf-einlagern__archivar")
	if agent == nil {
		t.Fatal("Agent nach DisposeInstance verschwunden")
	}
	gefunden := false
	for _, r := range agent.Permission {
		if r.Pattern == "raeume/lager/intern/**" && r.Action == "deny" {
			gefunden = true
		}
	}
	if !gefunden {
		t.Errorf("neue Template-Regel nach DisposeInstance nicht sichtbar: %+v", agent.Permission)
	}
}

// nimm sucht einen Auftrag nach Namen. Nicht auftraege[0]: seit
// bau.Init den Baumeister mitanlegt, liegen zwei Aufträge im Bau, und
// Load sortiert alphabetisch.
func nimm(t *testing.T, auftraege []*auftrag.Auftrag, name string) *auftrag.Auftrag {
	t.Helper()
	for _, a := range auftraege {
		if a.Name == name {
			return a
		}
	}
	t.Fatalf("Auftrag %q nicht geladen", name)
	return nil
}
