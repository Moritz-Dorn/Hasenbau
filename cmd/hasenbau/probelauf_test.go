package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Moritz-Dorn/Hasenbau/internal/bau"
)

// skriptSchreibt will in den Bau schreiben. Im Sandkasten geht das
// nicht; ohne ihn schon — an genau diesem Unterschied haengen die Tests
// unten.
const skriptSchreibt = "#!/usr/bin/env python3\n" +
	"import argparse, pathlib\n" +
	"p = argparse.ArgumentParser(); p.add_argument('--datei', required=True)\n" +
	"a = p.parse_args()\n" +
	"pathlib.Path(a.datei).write_text('geschrieben')\n" +
	"print('GESCHRIEBEN:', a.datei)\n"

func brauchtBwrap(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("bwrap liegt nicht im PATH — der Sandkasten ist hier nicht messbar")
	}
}

// TestProbelaufNimmtDasSchreibrecht ist der Kern von Hasenbau-9w6: der
// Probelauf FUEHRT AUS, und bis hierher tat er das mit den Rechten
// dessen, der ihn tippt. Gegen die Fassung ohne Sandkasten ist der Test
// rot — dort legt das Skript seine Datei an und meldet Exit 0.
func TestProbelaufNimmtDasSchreibrecht(t *testing.T) {
	brauchtBwrap(t)
	root := bauMitWerkzeug(t, "schreibt", skriptSchreibt, true)

	var out, errw strings.Builder
	code := run([]string{"-bau", root, "tool", "test", "schreibt", "--datei", "spur.txt"}, &out, &errw)
	if code == 0 {
		t.Fatalf("das Werkzeug durfte im Sandkasten schreiben:\n%s", out.String())
	}
	if _, err := os.Stat(filepath.Join(root, "spur.txt")); err == nil {
		t.Error("die Datei liegt im Bau — der Sandkasten hat nicht gehalten")
	}
	if !strings.Contains(out.String(), "Sandkasten:") {
		t.Errorf("die Bedingungen des Laufs stehen nicht in der Ausgabe:\n%s", out.String())
	}
}

// TestFehlschlagImSandkastenWiderlegtNichts haelt die Regel fest, ohne
// die der Sandkasten mehr kaputt machen wuerde, als er schuetzt:
// `invalid` heisst "der Probelauf hat die Behauptung WIDERLEGT". Unter
// geschlossenem Netz und nur lesbarem Bau kann derselbe Exit-Code aber
// vom Sandkasten kommen, und das sieht keine Maschine.
//
// Teuer waere die Verwechslung, weil `invalid` auch das erneute Testen
// sperrt: ein Werkzeug, das bloss schreiben wollte, kaeme ohne neues
// Review nicht mehr aus dem Zustand heraus.
func TestFehlschlagImSandkastenWiderlegtNichts(t *testing.T) {
	brauchtBwrap(t)
	root := bauMitWerkzeug(t, "schreibt", skriptSchreibt, true)

	var out, errw strings.Builder
	if code := run([]string{"-bau", root, "tool", "test", "schreibt", "--datei", "spur.txt"}, &out, &errw); code == 0 {
		t.Fatalf("der Probelauf ging durch:\n%s", out.String())
	}
	werkzeuge, err := bau.LadeTools(root)
	if err != nil {
		t.Fatal(err)
	}
	if werkzeuge[0].Zustand != bau.Hypothetical {
		t.Errorf("Zustand nach Fehlschlag im Sandkasten = %q, erwartet hypothetical — "+
			"widerlegt hat nur ein Lauf unter Ernstfall-Bedingungen", werkzeuge[0].Zustand)
	}
	if !strings.Contains(out.String(), "-"+probeSandboxFlag) {
		t.Errorf("der Weg zum Urteil (-%s) fehlt in der Ausgabe:\n%s", probeSandboxFlag, out.String())
	}

	// Die Gegenprobe: unter Ernstfall-Bedingungen laeuft dasselbe
	// Werkzeug durch. Ohne sie stuende nur fest, dass irgendetwas
	// scheitert — nicht, dass der Sandkasten der Grund war.
	out.Reset()
	errw.Reset()
	if code := run([]string{"-bau", root, "tool", "test", "schreibt", "-" + probeSandboxFlag, "--datei", "spur.txt"}, &out, &errw); code != 0 {
		t.Fatalf("ohne Sandkasten: exit %d — %s\n%s", code, errw.String(), out.String())
	}
	if _, err := os.Stat(filepath.Join(root, "spur.txt")); err != nil {
		t.Errorf("ohne Sandkasten wurde nicht geschrieben: %v", err)
	}
	if !strings.Contains(out.String(), "OHNE SANDKASTEN") {
		t.Errorf("der abgeschaltete Sandkasten wird nicht gemeldet — dieser Fall muss laut sein:\n%s", out.String())
	}

	// Und die Gegenrichtung der Rueckfrage: sagt ein Mensch, dass es am
	// Werkzeug lag, widerlegt der Lauf sehr wohl. Den Beleg liefert die
	// Maschine, das Urteil faellt der Mensch.
	out.Reset()
	errw.Reset()
	if code := runMitEingabe(strings.NewReader("j\n"), []string{"-bau", root, "tool", "test", "schreibt", "--datei", "spur.txt"}, &out, &errw); code == 0 {
		t.Fatalf("der Probelauf ging durch:\n%s", out.String())
	}
	werkzeuge, err = bau.LadeTools(root)
	if err != nil {
		t.Fatal(err)
	}
	if werkzeuge[0].Zustand != bau.Invalid {
		t.Errorf("Zustand nach bestaetigtem Fehlschlag = %q, erwartet invalid", werkzeuge[0].Zustand)
	}
}

// TestSandkastenLaesstDenBauLesen sichert die Reihenfolge der Mounts:
// erst wird $HOME weggeworfen, DANN der Bau wieder eingeblendet. Faellt
// das um, ist der Sandkasten zwar dicht, aber unbrauchbar — kein
// Werkzeug fande mehr seine Eingabe.
func TestSandkastenLaesstDenBauLesen(t *testing.T) {
	brauchtBwrap(t)
	const skriptLiest = "#!/usr/bin/env python3\n" +
		"import argparse, pathlib\n" +
		"p = argparse.ArgumentParser(); p.add_argument('--datei', required=True)\n" +
		"a = p.parse_args()\n" +
		"print('GELESEN:', pathlib.Path(a.datei).read_text().strip())\n"
	root := bauMitWerkzeug(t, "liest", skriptLiest, true)
	if err := os.WriteFile(filepath.Join(root, "eingabe.txt"), []byte("material"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out, errw strings.Builder
	if code := run([]string{"-bau", root, "tool", "test", "liest", "--datei", "eingabe.txt"}, &out, &errw); code != 0 {
		t.Fatalf("exit %d — %s\n%s", code, errw.String(), out.String())
	}
	if !strings.Contains(out.String(), "GELESEN: material") {
		t.Errorf("der Bau war im Sandkasten nicht lesbar:\n%s", out.String())
	}
}
