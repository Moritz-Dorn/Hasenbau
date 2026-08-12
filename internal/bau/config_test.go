package bau

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func schreibeConfig(t *testing.T, inhalt string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ConfigFile), []byte(inhalt), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestLadeConfigDefaults(t *testing.T) {
	// Ohne File: Defaults, kein Fehler — ein Bau ohne Config ist
	// benutzbar, nur eben ohne Baumeister.
	c, err := LoadConfig(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if c.LogLevel != "info" || c.Baumeister != "" {
		t.Errorf("Defaults = %+v", c)
	}
}

func TestLadeConfigSkelettParst(t *testing.T) {
	// Was `hasenbau init` schreibt, muss der eigene Parser lesen können.
	c, err := LoadConfig(schreibeConfig(t, hasenbauYAML))
	if err != nil {
		t.Fatal(err)
	}
	if c.LogLevel != "info" {
		t.Errorf("log_level = %q", c.LogLevel)
	}
	// Seit init den Baumeister mit anlegt, ist der Eintrag aktiv statt
	// auskommentiert — sonst zeigte die Config auf einen Auftrag, den
	// es nicht gibt, oder der Auftrag läge da, ohne dass ihn jemand
	// benennt.
	if c.Baumeister != "baumeister" {
		t.Errorf("baumeister = %q, erwartet \"baumeister\"", c.Baumeister)
	}
}

func TestLadeConfigBaumeister(t *testing.T) {
	c, err := LoadConfig(schreibeConfig(t, "log_level: debug\nbaumeister: baumeister\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.LogLevel != "debug" || c.Baumeister != "baumeister" {
		t.Errorf("Config = %+v", c)
	}
}

// Hasenbau-cvf: der Bau-weite Deckel. Dieselbe Regel wie beim Deckel je
// Auftrag — beide Hälften oder keine.
func TestLadeConfigThrottle(t *testing.T) {
	c, err := LoadConfig(schreibeConfig(t, "throttle:\n  max: 20\n  per: 1h\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !c.Throttle.An() || c.Throttle.Max != 20 || c.Throttle.Per != time.Hour {
		t.Errorf("Throttle = %+v", c.Throttle)
	}

	// Ohne den Block: kein Deckel, und das ist kein Fehler.
	ohne, err := LoadConfig(schreibeConfig(t, "log_level: info\n"))
	if err != nil {
		t.Fatal(err)
	}
	if ohne.Throttle.An() {
		t.Errorf("Deckel ohne throttle: %+v", ohne.Throttle)
	}
}

func TestLadeConfigFehler(t *testing.T) {
	faelle := []struct {
		name    string
		inhalt  string
		erwarte string
	}{
		{"unbekanntes Feld", "farbe: braun\n", "erlaubt: log_level, baumeister, sandbox, throttle"},
		{"throttle.max ohne per", "throttle:\n  max: 20\n", "max ohne per"},
		{"throttle.per ohne max", "throttle:\n  per: 1h\n", "per ohne max"},
		{"throttle leer", "throttle: {}\n", "throttle ist leer"},
		{"throttle.max negativ", "throttle:\n  max: -1\n  per: 1h\n", "muss > 0 sein"},
		{"throttle.per keine Dauer", "throttle:\n  max: 5\n  per: bald\n", "keine Dauer"},
		{"throttle.per null", "throttle:\n  max: 5\n  per: 0s\n", "kein Fenster"},
		{"log_level ungültig", "log_level: laut\n", "erlaubt: debug, info, warn, error"},
		{"baumeister mit Pfad", "baumeister: ../fremd\n", "kein gültiger Auftrags-Name"},
		{"kaputtes YAML", "log_level: [\n", ConfigFile},
	}
	for _, f := range faelle {
		t.Run(f.name, func(t *testing.T) {
			_, err := LoadConfig(schreibeConfig(t, f.inhalt))
			if err == nil {
				t.Fatal("Fehler erwartet, bekam nil")
			}
			if !strings.Contains(err.Error(), f.erwarte) {
				t.Errorf("Fehler %q enthält nicht %q", err, f.erwarte)
			}
		})
	}
}
