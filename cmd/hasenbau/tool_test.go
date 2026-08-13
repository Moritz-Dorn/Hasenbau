package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Moritz-Dorn/Hasenbau/internal/bau"
)

// bauMitWerkzeug legt einen Bau mit einem Werkzeug im Entwurfs-Raum an.
// Ist gelesen gesetzt, traegt das Skript einen gueltigen Review-Block —
// so, wie ihn ein Mensch (oder eine GUI, oder `tool review`) hinterlaesst.
func bauMitWerkzeug(t *testing.T, name, skript string, gelesen bool) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, bau.ToolsEntwurfDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	inhalt := skript
	if gelesen {
		inhalt = bau.SchreibeReviewBlock(bau.Review{
			By:   "Testerin",
			At:   "2026-08-13T08:00:00Z",
			Does: "Tut, was der Test braucht.",
			Safe: "Liest nichts, schreibt nichts, kein Netz.",
		}, skript)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".py"), []byte(inhalt), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"description": "Ein Testwerkzeug.", "script": "` + name + `.py",
	  "args": [{"name": "datei", "type": "string", "description": "Pfad", "required": true}]}`
	if err := os.WriteFile(filepath.Join(dir, name+".json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

const skriptKaputt = "#!/usr/bin/env python3\n" +
	"import argparse, re\n" +
	"p = argparse.ArgumentParser(); p.add_argument('--datei', required=True)\n" +
	"a = p.parse_args()\n" +
	"re.search(rb'/' + re.escape('Type'), b'egal')\n"

const skriptHeil = "#!/usr/bin/env python3\n" +
	"import argparse\n" +
	"p = argparse.ArgumentParser(); p.add_argument('--datei', required=True)\n" +
	"a = p.parse_args()\n" +
	"print('ANTWORT:', a.datei)\n"

// TestToolTestVerlangtEinReview ist das Gate aus Hasenbau-9w6: der
// Probelauf FUEHRT AUS, und wer ausfuehrt, ohne gelesen zu haben, hat
// die einzige Pruefung uebersprungen, die es gibt.
func TestToolTestVerlangtEinReview(t *testing.T) {
	root := bauMitWerkzeug(t, "ungelesen", skriptHeil, false)

	var out, errw strings.Builder
	code := run([]string{"-bau", root, "tool", "test", "ungelesen", "--datei", "x"}, &out, &errw)
	if code == 0 {
		t.Errorf("ungelesenes Werkzeug wurde ausgefuehrt:\n%s", out.String())
	}
	if !strings.Contains(errw.String(), "ungelesen") || !strings.Contains(errw.String(), "review") {
		t.Errorf("die Ablehnung nennt weder Zustand noch den naechsten Schritt:\n%s", errw.String())
	}
	// Und das Skript darf dabei nicht gelaufen sein.
	if strings.Contains(out.String(), "ANTWORT:") {
		t.Errorf("das Skript wurde trotz Ablehnung ausgefuehrt:\n%s", out.String())
	}
}

// TestToolTestFaengtWasSonstDurchrutscht: der Fall aus dem ersten echten
// Schmied-Lauf — gueltiges Manifest, syntaktisch einwandfreies Python,
// Absturz beim ersten Aufruf.
func TestToolTestFaengtWasSonstDurchrutscht(t *testing.T) {
	root := bauMitWerkzeug(t, "kaputt", skriptKaputt, true)

	// Vorbedingung: der bisherige Weg meldet nichts.
	var out, errw strings.Builder
	if code := run([]string{"-bau", root, "get", "tools", "-entwuerfe"}, &out, &errw); code != 0 {
		t.Fatalf("get tools: exit %d — %s", code, errw.String())
	}
	if !strings.Contains(out.String(), "kaputt") {
		t.Fatalf("get tools fuehrt das Werkzeug nicht:\n%s", out.String())
	}

	// Die Antwort "j" auf die Rueckfrage aus Hasenbau-9w6: seit dem
	// Sandkasten kann ein Fehlschlag auch von dessen Grenzen kommen, und
	// die Maschine widerlegt erst, wenn ein Mensch sagt, dass es am
	// Werkzeug lag. Hier lag es am Werkzeug — es stuerzt mit TypeError ab.
	out.Reset()
	errw.Reset()
	if code := runMitEingabe(strings.NewReader("j\n"), []string{"-bau", root, "tool", "test", "kaputt", "--datei", "egal.txt"}, &out, &errw); code == 0 {
		t.Errorf("ein abstuerzendes Werkzeug gilt als in Ordnung:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "TypeError") {
		t.Errorf("der Fehlertext des Skripts fehlt in der Ausgabe:\n%s", out.String())
	}
	// Der Probelauf KLASSIFIZIERT: gescheitert heisst invalid, und das
	// steht danach im Skript.
	if !strings.Contains(out.String(), string(bau.Invalid)) {
		t.Errorf("der Zustand invalid wird nicht gemeldet:\n%s", out.String())
	}
	werkzeuge, err := bau.LadeTools(root)
	if err != nil {
		t.Fatal(err)
	}
	if werkzeuge[0].Zustand != bau.Invalid {
		t.Errorf("Zustand nach gescheitertem Probelauf = %q, erwartet invalid", werkzeuge[0].Zustand)
	}
}

// TestProbelaufAlleinMachtNichtActual haelt die Asymmetrie fest, auf
// die Moritz am 2026-08-13 hingewiesen hat: ein FEHLSCHLAG widerlegt,
// ein ERFOLG bestaetigt nicht. Exit 0 heisst "es lief", nicht "es
// stimmt" — und `actual` heisst nach ValIntent "verifiziert und
// entspricht der Realitaet". Ob die Ausgabe richtig war, sieht nur ein
// Mensch, und der sagt es beim Freigeben.
func TestProbelaufAlleinMachtNichtActual(t *testing.T) {
	root := bauMitWerkzeug(t, "heil", skriptHeil, true)

	// release ohne jeden Probelauf: es gaebe nichts zu beurteilen.
	var out, errw strings.Builder
	if code := run([]string{"-bau", root, "tool", "release", "-ja", "heil"}, &out, &errw); code == 0 {
		t.Errorf("release ohne Probelauf ging durch:\n%s", out.String())
	}
	if !strings.Contains(errw.String(), "nie gelaufen") {
		t.Errorf("die Ablehnung nennt den Grund nicht:\n%s", errw.String())
	}

	// Probelauf besteht — und laesst den Zustand trotzdem hypothetical.
	out.Reset()
	errw.Reset()
	if code := run([]string{"-bau", root, "tool", "test", "heil", "--datei", "x.txt"}, &out, &errw); code != 0 {
		t.Fatalf("exit %d — %s\n%s", code, errw.String(), out.String())
	}
	if !strings.Contains(out.String(), "ANTWORT: x.txt") {
		t.Errorf("stdout des Werkzeugs fehlt:\n%s", out.String())
	}
	werkzeuge, err := bau.LadeTools(root)
	if err != nil {
		t.Fatal(err)
	}
	if werkzeuge[0].Zustand != bau.Hypothetical {
		t.Errorf("Zustand nach bestandenem Probelauf = %q, erwartet hypothetical — "+
			"ein Erfolg ist ein Beleg, kein Urteil", werkzeuge[0].Zustand)
	}
	if werkzeuge[0].Einsatzbereit() {
		t.Error("ein blosser Probelauf hat das Werkzeug einsatzbereit gemacht")
	}

	// Erst das Urteil eines Menschen macht actual.
	out.Reset()
	errw.Reset()
	if code := run([]string{"-bau", root, "tool", "release", "-ja", "heil"}, &out, &errw); code != 0 {
		t.Fatalf("release nach dem Probelauf: exit %d — %s", code, errw.String())
	}
	for _, rel := range []string{"tools/heil.py", "tools/heil.json"} {
		if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
			t.Errorf("%s fehlt nach release: %v", rel, err)
		}
	}
	werkzeuge, err = bau.LadeTools(root)
	if err != nil {
		t.Fatal(err)
	}
	if werkzeuge[0].Zustand != bau.Actual {
		t.Errorf("Zustand nach der Freigabe = %q, erwartet actual", werkzeuge[0].Zustand)
	}
	if werkzeuge[0].Review.ReleasedBy == "" {
		t.Error("die Freigabe steht ohne Namen im Block — dann kann sie niemand verantworten")
	}
}

// TestReleaseFragtNachDemUrteil: ohne Bestaetigung wird nichts
// verschoben. Die Rueckfrage IST der Verifikationsakt.
func TestReleaseFragtNachDemUrteil(t *testing.T) {
	root := bauMitWerkzeug(t, "heil", skriptHeil, true)
	var out, errw strings.Builder
	if code := run([]string{"-bau", root, "tool", "test", "heil", "--datei", "x"}, &out, &errw); code != 0 {
		t.Fatalf("Probelauf: %s", errw.String())
	}

	out.Reset()
	errw.Reset()
	// "n" — die Ausgabe war nicht richtig.
	if code := runMitEingabe(strings.NewReader("n\n"), []string{"-bau", root, "tool", "release", "heil"}, &out, &errw); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out.String(), "abgebrochen") {
		t.Errorf("ohne Bestaetigung wurde nicht abgebrochen:\n%s", out.String())
	}
	if _, err := os.Stat(filepath.Join(root, "tools", "heil.py")); err == nil {
		t.Error("trotz Ablehnung verschoben")
	}
	werkzeuge, err := bau.LadeTools(root)
	if err != nil {
		t.Fatal(err)
	}
	if werkzeuge[0].Zustand != bau.Hypothetical {
		t.Errorf("Zustand = %q, erwartet hypothetical", werkzeuge[0].Zustand)
	}
}

// TestGeaendertesSkriptFaelltAusDerReihe: wer nach dem Review eine Zeile
// aendert, faengt von vorn an — auch nach bestandenem Probelauf.
func TestGeaendertesSkriptFaelltAusDerReihe(t *testing.T) {
	root := bauMitWerkzeug(t, "heil", skriptHeil, true)
	var out, errw strings.Builder
	if code := run([]string{"-bau", root, "tool", "test", "heil", "--datei", "x"}, &out, &errw); code != 0 {
		t.Fatalf("Probelauf: exit %d — %s", code, errw.String())
	}

	pfad := filepath.Join(root, bau.ToolsEntwurfDir, "heil.py")
	roh, err := os.ReadFile(pfad)
	if err != nil {
		t.Fatal(err)
	}
	geaendert := strings.Replace(string(roh), "import argparse", "import argparse, os", 1)
	if err := os.WriteFile(pfad, []byte(geaendert), 0o755); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	errw.Reset()
	if code := run([]string{"-bau", root, "tool", "release", "heil"}, &out, &errw); code == 0 {
		t.Errorf("ein nach dem Review geaendertes Werkzeug wurde freigegeben:\n%s", out.String())
	}
	if !strings.Contains(errw.String(), string(bau.Outdated)) {
		t.Errorf("die Ablehnung nennt outdated nicht:\n%s", errw.String())
	}
	out.Reset()
	errw.Reset()
	if code := run([]string{"-bau", root, "tool", "test", "heil", "--datei", "x"}, &out, &errw); code == 0 {
		t.Errorf("ein geaendertes Werkzeug wurde ohne neues Review ausgefuehrt:\n%s", out.String())
	}
}

// TestToolTestPrueftGegenDasManifest: ein Argument, das ein Hase nie
// schicken koennte, darf auch im Test nicht durchgehen.
func TestToolTestPrueftGegenDasManifest(t *testing.T) {
	root := bauMitWerkzeug(t, "w", skriptHeil, true)

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

// TestNurGelesenesDarfGetestetWerden: testbar sind ausschliesslich
// `hypothetical` und `actual`. Ein widerlegter Anspruch (`invalid`) wird
// nicht durch Wiederholen wahr — wer erneut zeigen will, liest erst
// wieder.
func TestNurGelesenesDarfGetestetWerden(t *testing.T) {
	root := bauMitWerkzeug(t, "kaputt", skriptKaputt, true)

	// Erster Probelauf scheitert, und der Mensch bestaetigt auf die
	// Rueckfrage, dass es am Werkzeug lag -> invalid.
	var out, errw strings.Builder
	if code := runMitEingabe(strings.NewReader("j\n"), []string{"-bau", root, "tool", "test", "kaputt", "--datei", "x"}, &out, &errw); code == 0 {
		t.Fatalf("abstuerzendes Werkzeug galt als in Ordnung:\n%s", out.String())
	}
	werkzeuge, err := bau.LadeTools(root)
	if err != nil {
		t.Fatal(err)
	}
	if werkzeuge[0].Zustand != bau.Invalid {
		t.Fatalf("Zustand = %q, erwartet invalid", werkzeuge[0].Zustand)
	}

	// Zweiter Versuch: gesperrt, obwohl sich nichts geaendert hat.
	out.Reset()
	errw.Reset()
	if code := run([]string{"-bau", root, "tool", "test", "kaputt", "--datei", "x"}, &out, &errw); code == 0 {
		t.Errorf("ein widerlegtes Werkzeug liess sich erneut testen:\n%s", out.String())
	}
	if !strings.Contains(errw.String(), string(bau.Invalid)) {
		t.Errorf("die Ablehnung nennt invalid nicht:\n%s", errw.String())
	}
	if !strings.Contains(errw.String(), "review") {
		t.Errorf("die Ablehnung nennt den naechsten Schritt nicht:\n%s", errw.String())
	}
}
