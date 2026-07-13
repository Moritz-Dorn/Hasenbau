package bau

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Moritz-Dorn/Hasenbau/internal/supervisor"
)

func TestInitLegtLayoutAn(t *testing.T) {
	root := filepath.Join(t.TempDir(), "neuer-bau")
	created, err := Init(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(created) != len(dirs)+len(files) {
		t.Errorf("created = %v", created)
	}
	for _, p := range []string{
		"hasenbau.yaml",
		".opencode-home/opencode/opencode.json",
		".opencode-home/opencode/agents",
		"auftraege", "gaenge", "hasen", "raeume", "state",
	} {
		if _, err := os.Stat(filepath.Join(root, p)); err != nil {
			t.Errorf("%s fehlt: %v", p, err)
		}
	}

	// Der frische Bau muss die Supervisor-Validierung bestehen —
	// init und Supervisor teilen dieselbe Layout-Konvention.
	if _, err := supervisor.New(supervisor.Config{BauDir: root}); err != nil {
		t.Errorf("frischer Bau besteht Supervisor-Validierung nicht: %v", err)
	}
}

func TestInitIstIdempotentUndNichtDestruktiv(t *testing.T) {
	root := t.TempDir()
	if _, err := Init(root); err != nil {
		t.Fatal(err)
	}

	// Nutzer-Config verändern — ein zweites Init darf sie nicht anfassen.
	conf := filepath.Join(root, ".opencode-home", "opencode", "opencode.json")
	eigen := `{"plugin":[],"provider":{"scc":{}}}`
	if err := os.WriteFile(conf, []byte(eigen), 0o644); err != nil {
		t.Fatal(err)
	}

	created, err := Init(root)
	if err != nil {
		t.Fatalf("zweites Init: %v", err)
	}
	if len(created) != 0 {
		t.Errorf("zweites Init legte an: %v", created)
	}
	raw, err := os.ReadFile(conf)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "scc") {
		t.Error("Init hat bestehende opencode.json überschrieben")
	}
}

func TestInitLehntDateiAlsRootAb(t *testing.T) {
	f := filepath.Join(t.TempDir(), "datei")
	if err := os.WriteFile(f, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Init(f); err == nil {
		t.Error("Init auf einer Datei muss fehlschlagen")
	}
}
