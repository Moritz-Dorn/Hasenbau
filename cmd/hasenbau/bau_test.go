package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Hasenbau-ha0.6: Die beiden Befunde, die `describe bau` zeigen muss —
// ein Bau ohne Git-Commit und ein Rückkanal-Eintrag auf ein
// verschwundenes Binary. Beide sieht man dem Bau sonst nicht an.
func TestDescribeBauMeldetDieStillenFehler(t *testing.T) {
	bau := t.TempDir()
	conf := filepath.Join(bau, ".opencode-home", "opencode", "opencode.json")
	if err := os.MkdirAll(filepath.Dir(conf), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(conf, []byte(`{"plugin":[],"mcp":{"hasenbau":{"type":"local",`+
		`"command":["/gibt/es/nicht/hasenbau","-bau",".","mcp"],"enabled":true}}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	var out, errw strings.Builder
	if code := run([]string{"-bau", bau, "describe", "bau"}, &out, &errw); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errw.String())
	}
	got := out.String()
	for _, muss := range []string{
		"kein .git", "Raum-Permissions", // ohne Commit greifen sie nicht (§11.5)
		"/gibt/es/nicht/hasenbau", "gibt es nicht",
		"PRÜFEN", "zum Nachsehen",
	} {
		if !strings.Contains(got, muss) {
			t.Errorf("Diagnose ohne %q:\n%s", muss, got)
		}
	}
}

// Ein vollständiger Bau meldet nichts — sonst gewöhnt sich jeder an
// gelbe Zeilen und liest sie nicht mehr.
func TestDescribeBauSchweigtWennAllesStimmt(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("ohne git nicht prüfbar")
	}
	bau := filepath.Join(t.TempDir(), "bau")
	var out, errw strings.Builder
	if code := run([]string{"init", bau}, &out, &errw); code != 0 {
		t.Fatalf("init: exit %d, stderr %q", code, errw.String())
	}
	// Der Rückkanal-Eintrag entsteht erst beim Server-Start; hier von
	// Hand auf ein Binary, das es gibt.
	conf := filepath.Join(bau, ".opencode-home", "opencode", "opencode.json")
	roh, err := os.ReadFile(conf)
	if err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	neu := strings.Replace(string(roh), `"plugin": []`,
		`"plugin": [], "mcp": {"hasenbau": {"type":"local","command":["`+exe+`","mcp"],"enabled":true}}`, 1)
	if err := os.WriteFile(conf, []byte(neu), 0o644); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	errw.Reset()
	if code := run([]string{"-bau", bau, "describe", "bau"}, &out, &errw); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errw.String())
	}
	got := out.String()
	if strings.Contains(got, "PRÜFEN") {
		t.Errorf("frischer Bau meldet etwas:\n%s", got)
	}
	if !strings.Contains(got, "Nichts zu tun") {
		t.Errorf("Ausgabe:\n%s", got)
	}
}

// status ist das Dashboard und prüft NICHTS — der Unterschied zu
// describe bau ist der ganze Sinn der Aufteilung.
func TestStatusZeigtOhneZuPruefen(t *testing.T) {
	bau := t.TempDir()
	laufMitNotizen(t, bau).Close()

	var out, errw strings.Builder
	if code := run([]string{"-bau", bau, "status"}, &out, &errw); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errw.String())
	}
	got := out.String()
	for _, muss := range []string{"Bau:", "Läufe:", "Die letzten Läufe", "describe bau"} {
		if !strings.Contains(got, muss) {
			t.Errorf("Dashboard ohne %q:\n%s", muss, got)
		}
	}
	// Dieser Bau hat weder .git noch Bau-Config — status sagt trotzdem
	// kein Wort dazu.
	for _, darfNicht := range []string{"PRÜFEN", "kein .git", "fehlt"} {
		if strings.Contains(got, darfNicht) {
			t.Errorf("status prüft (%q):\n%s", darfNicht, got)
		}
	}
}

func TestDescribeBauMitArgument(t *testing.T) {
	var out, errw strings.Builder
	if code := run([]string{"-bau", t.TempDir(), "describe", "bau", "zuviel"}, &out, &errw); code != 2 {
		t.Errorf("exit %d, erwartet 2", code)
	}
}
