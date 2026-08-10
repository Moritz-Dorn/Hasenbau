package bau

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func schreibeConfig(t *testing.T, inhalt string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ConfigDatei), []byte(inhalt), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestLadeConfigDefaults(t *testing.T) {
	// Ohne Datei: Defaults, kein Fehler — ein Bau ohne Config ist
	// benutzbar, nur eben ohne Baumeister.
	c, err := LadeConfig(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if c.LogLevel != "info" || c.Baumeister != "" {
		t.Errorf("Defaults = %+v", c)
	}
}

func TestLadeConfigSkelettParst(t *testing.T) {
	// Was `hasenbau init` schreibt, muss der eigene Parser lesen können.
	c, err := LadeConfig(schreibeConfig(t, hasenbauYAML))
	if err != nil {
		t.Fatal(err)
	}
	if c.LogLevel != "info" {
		t.Errorf("log_level = %q", c.LogLevel)
	}
	if c.Baumeister != "" {
		t.Errorf("baumeister ist im Skelett auskommentiert, gelesen: %q", c.Baumeister)
	}
}

func TestLadeConfigBaumeister(t *testing.T) {
	c, err := LadeConfig(schreibeConfig(t, "log_level: debug\nbaumeister: baumeister\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.LogLevel != "debug" || c.Baumeister != "baumeister" {
		t.Errorf("Config = %+v", c)
	}
}

func TestLadeConfigFehler(t *testing.T) {
	faelle := []struct {
		name    string
		inhalt  string
		erwarte string
	}{
		{"unbekanntes Feld", "farbe: braun\n", "erlaubt: log_level, baumeister"},
		{"log_level ungültig", "log_level: laut\n", "erlaubt: debug, info, warn, error"},
		{"baumeister mit Pfad", "baumeister: ../fremd\n", "kein gültiger Auftrags-Name"},
		{"kaputtes YAML", "log_level: [\n", ConfigDatei},
	}
	for _, f := range faelle {
		t.Run(f.name, func(t *testing.T) {
			_, err := LadeConfig(schreibeConfig(t, f.inhalt))
			if err == nil {
				t.Fatal("Fehler erwartet, bekam nil")
			}
			if !strings.Contains(err.Error(), f.erwarte) {
				t.Errorf("Fehler %q enthält nicht %q", err, f.erwarte)
			}
		})
	}
}
