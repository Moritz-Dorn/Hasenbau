package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Moritz-Dorn/Hasenbau/internal/bau"
)

// bauMitWerkzeug legt einen Bau mit einem Werkzeug im Entwurfs-Raum an.
func bauMitWerkzeug(t *testing.T, name, skript string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, bau.ToolsEntwurfDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".py"), []byte(skript), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"description": "Ein Testwerkzeug.", "script": "` + name + `.py",
	  "args": [{"name": "datei", "type": "string", "description": "Pfad", "required": true}]}`
	if err := os.WriteFile(filepath.Join(dir, name+".json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// TestToolTestFaengtWasSonstDurchrutscht ist der Test zu Hasenbau-kf5.
// Der erste echte Schmied-Lauf lieferte ein Werkzeug mit gueltigem
// Manifest, das zur Laufzeit abstuerzte — py_compile lief durch,
// `get tools` fuehrte es klaglos, die Diagnose meldete "1 im Entwurf".
// Erst der Aufruf zeigte es. Genau diesen Fall haelt der Test fest,
// samt der Zeile, an der es im Original scheiterte.
func TestToolTestFaengtWasSonstDurchrutscht(t *testing.T) {
	// Syntaktisch einwandfrei, stuerzt erst beim Ausfuehren: str gegen
	// bytes, wie im echten Entwurf.
	kaputt := "#!/usr/bin/env python3\n" +
		"import argparse, re\n" +
		"p = argparse.ArgumentParser(); p.add_argument('--datei', required=True)\n" +
		"a = p.parse_args()\n" +
		"re.search(rb'/' + re.escape('Type'), b'egal')\n"
	root := bauMitWerkzeug(t, "kaputt", kaputt)

	// Vorbedingung: der bisherige Weg meldet nichts. Waere das anders,
	// wuerde dieser Test etwas anderes sichern, als er behauptet.
	var out, errw strings.Builder
	if code := run([]string{"-bau", root, "get", "tools"}, &out, &errw); code != 0 {
		t.Fatalf("get tools: exit %d — %s", code, errw.String())
	}
	if !strings.Contains(out.String(), "kaputt") {
		t.Fatalf("get tools fuehrt das Werkzeug nicht:\n%s", out.String())
	}

	out.Reset()
	errw.Reset()
	code := run([]string{"-bau", root, "tool", "test", "kaputt", "--datei", "egal.txt"}, &out, &errw)
	if code == 0 {
		t.Errorf("ein abstuerzendes Werkzeug gilt als in Ordnung:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "TypeError") {
		t.Errorf("der Fehlertext des Skripts fehlt in der Ausgabe:\n%s", out.String())
	}
	// Der Text aus stderr ist das, was ein Hase spaeter zu sehen
	// bekaeme — der Befehl muss ihn zeigen, nicht nur den Exit-Code.
	if !strings.Contains(out.String(), "stderr") {
		t.Errorf("stderr wird nicht ausgewiesen:\n%s", out.String())
	}
}

// TestToolTestZeigtDasErgebnisUndUrteiltNicht: bei Exit 0 sagt der
// Befehl, was zurueckkam — aber nicht, dass es stimmt. Ein Test, der
// nur "stuerzt es ab?" fragt, haette den zweiten Fehler des echten
// Entwurfs (ein Parser, der reale PDFs nicht liest) gruen gemeldet.
func TestToolTestZeigtDasErgebnisUndUrteiltNicht(t *testing.T) {
	heil := "#!/usr/bin/env python3\n" +
		"import argparse\n" +
		"p = argparse.ArgumentParser(); p.add_argument('--datei', required=True)\n" +
		"a = p.parse_args()\n" +
		"print('ANTWORT:', a.datei)\n"
	root := bauMitWerkzeug(t, "heil", heil)

	var out, errw strings.Builder
	if code := run([]string{"-bau", root, "tool", "test", "heil", "--datei", "x.txt"}, &out, &errw); code != 0 {
		t.Fatalf("exit %d — %s\n%s", code, errw.String(), out.String())
	}
	if !strings.Contains(out.String(), "ANTWORT: x.txt") {
		t.Errorf("stdout des Werkzeugs fehlt:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "Entwurf") {
		t.Errorf("dass es ein ungeprueter Entwurf ist, steht nicht da:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "STIMMT") {
		t.Errorf("der Befehl verschweigt, dass er die Richtigkeit nicht prueft:\n%s", out.String())
	}
}

// TestToolTestPrueftGegenDasManifest: ein Argument, das ein Hase nie
// schicken koennte, darf auch im Test nicht durchgehen — sonst prueft
// man das Werkzeug mit einer Eingabe, die es im Ernstfall nicht gibt.
func TestToolTestPrueftGegenDasManifest(t *testing.T) {
	root := bauMitWerkzeug(t, "w", "#!/usr/bin/env python3\nprint('x')\n")

	faelle := map[string][]string{
		"Pflichtargument fehlt": {"tool", "test", "w"},
		"unbekanntes Argument":  {"tool", "test", "w", "--gibtsnicht", "1"},
		"Wert fehlt":            {"tool", "test", "w", "--datei"},
		"unbekanntes Werkzeug":  {"tool", "test", "andereswerkzeug"},
	}
	for name, args := range faelle {
		t.Run(name, func(t *testing.T) {
			var out, errw strings.Builder
			if code := run(append([]string{"-bau", root}, args...), &out, &errw); code == 0 {
				t.Errorf("%s wurde angenommen:\n%s", name, out.String())
			}
			if errw.Len() == 0 {
				t.Errorf("%s ohne Begruendung abgelehnt", name)
			}
		})
	}
}
