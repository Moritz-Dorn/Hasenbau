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
	manifest := `{"description": "Ein Testwerkzeug.", "script": "` + name + `.py",
	  "args": [{"name": "datei", "type": "string", "description": "Pfad", "required": true}]}`
	return bauMitManifest(t, name, skript, gelesen, manifest)
}

// bauMitManifest ist dasselbe, aber mit frei gewaehltem Manifest — fuer
// alles, was am `example`-Block haengt.
func bauMitManifest(t *testing.T, name, skript string, gelesen bool, manifest string) string {
	t.Helper()
	root := t.TempDir()
	// Ein Werkzeug ist ein ORDNER (Hasenbau-lnk): tool.json und Skript
	// darin, der Ordner traegt den Namen.
	dir := filepath.Join(root, bau.ToolsEntwurfDir, name)
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
			// Gelesen heisst: Skript UND Manifest gelesen (Hasenbau-cgx).
			// Ohne diesen Hash waere der Block unvollstaendig und das
			// Werkzeug trotz Block `generated`.
			ManifestHash: bau.ManifestHash(manifest),
		}, skript)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".py"), []byte(inhalt), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, bau.ToolManifest), []byte(manifest), 0o644); err != nil {
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
	if code := run([]string{"-bau", root, "get", "tools", "-drafts"}, &out, &errw); code != 0 {
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
	if code := run([]string{"-bau", root, "tool", "release", "-yes", "heil"}, &out, &errw); code == 0 {
		t.Errorf("release ohne Probelauf ging durch:\n%s", out.String())
	}
	if !strings.Contains(errw.String(), "has never run") {
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
	if code := run([]string{"-bau", root, "tool", "release", "-yes", "heil"}, &out, &errw); code != 0 {
		t.Fatalf("release nach dem Probelauf: exit %d — %s", code, errw.String())
	}
	for _, rel := range []string{"tools/released/heil/heil.py", "tools/released/heil/tool.json"} {
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
	if !strings.Contains(out.String(), "aborted") {
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

	pfad := filepath.Join(root, bau.ToolsEntwurfDir, "heil", "heil.py")
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

// TestToolStateAntwortetMitDemExitCode: der Befehl, den das Bau-Plugin
// stellt, statt die Regel nachzurechnen (Hasenbau-cko). Er ist die
// einzige Stelle, an der „darf registriert werden?" beantwortet wird —
// vorher stand die Antwort zweimal da und ist abgedriftet.
func TestToolStateAntwortetMitDemExitCode(t *testing.T) {
	root := bauMitWerkzeug(t, "zaehlen", "#!/usr/bin/env python3\nprint(1)\n", true)
	var out, errw strings.Builder

	// Unbekannt: die Frage ergibt keinen Sinn, das ist nicht dasselbe wie
	// „nein" — sonst saehe ein Tippfehler im Manifest aus wie ein
	// ungelesenes Werkzeug.
	if code := run([]string{"-bau", root, "tool", "state", "gibtsnicht"}, &out, &errw); code != 2 {
		t.Errorf("unbekanntes Werkzeug: exit %d, erwartet 2", code)
	}

	// Entwurf, wenn auch gelesen: liegt da, ist aber nichts fuer einen
	// Hasen — die Freigabe ist das Verschieben nach tools/.
	out.Reset()
	errw.Reset()
	if code := run([]string{"-bau", root, "tool", "state", "zaehlen"}, &out, &errw); code != 1 {
		t.Errorf("Entwurf: exit %d, erwartet 1 (%s)", code, errw.String())
	}
	if !strings.Contains(out.String(), "draft") {
		t.Errorf("der Grund steht nicht auf stdout: %q", out.String())
	}

	// Freigegeben und bestaetigt: registrieren. Der Weg dahin ist der
	// echte — Probelauf vermerken, dann release.
	out.Reset()
	errw.Reset()
	if code := run([]string{"-bau", root, "tool", "test", "zaehlen", "--datei", "x"}, &out, &errw); code != 0 {
		t.Fatalf("test: exit %d, %s", code, errw.String())
	}
	out.Reset()
	errw.Reset()
	if code := runMitEingabe(strings.NewReader("j\n"), []string{"-bau", root, "tool", "release", "zaehlen"}, &out, &errw); code != 0 {
		t.Fatalf("release: exit %d, %s", code, errw.String())
	}
	out.Reset()
	errw.Reset()
	if code := run([]string{"-bau", root, "tool", "state", "zaehlen"}, &out, &errw); code != 0 {
		t.Errorf("freigegeben: exit %d (%s), erwartet 0", code, errw.String())
	}
	if strings.TrimSpace(out.String()) != "actual" {
		t.Errorf("Zustand = %q", out.String())
	}
}

// TestPluginRechnetDieRegelNichtSelbstNach hält fest, was Hasenbau-cko
// eigentlich hergestellt hat: die Ableitung steht nur noch an EINER
// Stelle. Ein Plugin, das wieder anfängt, `released-by` selbst zu lesen,
// ist die Doppelung von vorne — und die ist schon einmal still
// auseinandergelaufen.
func TestPluginRechnetDieRegelNichtSelbstNach(t *testing.T) {
	quelle := bau.PluginQuelle()
	if !strings.Contains(quelle, "tool state") {
		t.Error("das Plugin fragt den Hasenbau nicht nach dem Zustand")
	}
	for _, verboten := range []string{"released-by", "verified-exit", "body-sha256", "createHash"} {
		if strings.Contains(quelle, verboten) {
			t.Errorf("das Plugin liest %q selbst — die Regel gehört ins Binary (PLAN §3)", verboten)
		}
	}
}

// TestReviewNaechsterOhneKandidat: der aufgeraeumte Normalfall, und
// genau der ist abgestuerzt (Hasenbau-4rh). waehleFuerReview hat drei
// Ausgaenge, aber nur zwei Sorten Rueckgabe: "nichts wartet" ist kein
// Fehler, also kam (nil, 0) zurueck — und der Aufrufer prueft den Code,
// nicht den Zeiger. Die Meldung erschien noch, dann paniert es.
//
// Zwei Lagen, weil beide dieselbe Antwort verlangen: ein Bau ohne jedes
// Werkzeug, und einer, in dem alles schon gelesen ist.
func TestReviewNaechsterOhneKandidat(t *testing.T) {
	faelle := map[string]string{
		"leerer Bau":          t.TempDir(),
		"alles schon gelesen": bauMitWerkzeug(t, "gelesen", skriptHeil, true),
	}
	for name, root := range faelle {
		t.Run(name, func(t *testing.T) {
			var out, errw strings.Builder
			code := run([]string{"-bau", root, "tool", "review", "--next"}, &out, &errw)
			if code != 0 {
				t.Errorf("Exit = %d, erwartet 0 — nichts zu tun ist kein Fehler; stderr: %s", code, errw.String())
			}
			if !strings.Contains(out.String(), "Nothing is waiting for review") {
				t.Errorf("die Meldung fehlt:\n%s", out.String())
			}
		})
	}
}

// TestEntwurfslisteEmpfiehltNurWasGeht: die Liste zeigt ALLE Entwuerfe,
// auch die schon gelesenen — der Tipp darunter galt aber unbedingt und
// nannte `tool review --next`, das nur generated und outdated findet.
// Wer nur Gelesenes vor sich hatte, bekam eine Liste mit Eintraegen und
// einen Befehl, der sagt, es gebe nichts (Hasenbau-xp9). Gemeldet von
// Moritz, und der Tipp war die Ursache, nicht sein Vorgehen.
func TestEntwurfslisteEmpfiehltNurWasGeht(t *testing.T) {
	t.Run("alles gelesen", func(t *testing.T) {
		root := bauMitWerkzeug(t, "gelesen", skriptHeil, true)
		var out, errw strings.Builder
		if code := run([]string{"-bau", root, "get", "tools", "-drafts"}, &out, &errw); code != 0 {
			t.Fatalf("exit %d — %s", code, errw.String())
		}
		if !strings.Contains(out.String(), "gelesen") {
			t.Fatalf("der Entwurf fehlt in der Liste:\n%s", out.String())
		}
		// Geprueft wird die BEFEHLSZEILE, nicht das Wort: der Text darf
		// --next erwaehnen ("dort ist nichts mehr zu tun"), nur nicht
		// als naechsten Schritt hinstellen.
		if strings.Contains(out.String(), "\n  hasenbau tool review --next") {
			t.Errorf("empfiehlt --next, obwohl es dort nichts zu tun gibt:\n%s", out.String())
		}
		// Und der Nutzer darf nicht ohne naechsten Schritt dastehen.
		if !strings.Contains(out.String(), "tool test") {
			t.Errorf("nennt die naechste Stufe nicht:\n%s", out.String())
		}
	})

	t.Run("ungelesen", func(t *testing.T) {
		root := bauMitWerkzeug(t, "ungelesen", skriptHeil, false)
		var out, errw strings.Builder
		if code := run([]string{"-bau", root, "get", "tools", "-drafts"}, &out, &errw); code != 0 {
			t.Fatalf("exit %d — %s", code, errw.String())
		}
		if !strings.Contains(out.String(), "\n  hasenbau tool review --next") {
			t.Errorf("verschweigt --next, obwohl ein Entwurf ungelesen ist:\n%s", out.String())
		}
	})
}

// skriptZaehlt zaehlt die Zeilen der uebergebenen Datei — klein genug,
// dass sich seine Ausgabe vorhersagen laesst, und genau darum geht es.
const skriptZaehlt = "#!/usr/bin/env python3\n" +
	"import argparse, pathlib\n" +
	"p = argparse.ArgumentParser(); p.add_argument('--datei', required=True)\n" +
	"a = p.parse_args()\n" +
	"print(len(pathlib.Path(a.datei).read_text().splitlines()))\n"

// bauMitBeispiel legt ein gelesenes Werkzeug samt example/-Ordner an.
// erwartet ist die Vorhersage des Schmieds im Manifest.
func bauMitBeispiel(t *testing.T, name, erwartet string) string {
	t.Helper()
	manifest := `{"description": "Zaehlt Zeilen.", "script": "` + name + `.py",
	  "args": [{"name": "datei", "type": "string", "description": "Pfad", "required": true}],
	  "example": {"args": {"datei": "example/probe.txt"}, "expect": "` + erwartet + `"}}`
	root := bauMitManifest(t, name, skriptZaehlt, true, manifest)
	dir := filepath.Join(root, bau.ToolsEntwurfDir, name, "example")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "probe.txt"), []byte("eins\nzwei\ndrei\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// TestProbelaufNimmtDasBeispiel: der Fall, der den ganzen Ordner
// begruendet (Hasenbau-lnk). Ohne Argumente greift das Beispiel des
// Schmieds — sonst muesste ein Mensch raten, welche Datei hier
// hineingehoert, und das weiss nur der Hase, der das Werkzeug
// angefordert hat.
func TestProbelaufNimmtDasBeispiel(t *testing.T) {
	root := bauMitBeispiel(t, "zaehlt", "3")

	var out, errw strings.Builder
	if code := run([]string{"-bau", root, "tool", "test", "zaehlt"}, &out, &errw); code != 0 {
		t.Fatalf("exit %d — %s\n%s", code, errw.String(), out.String())
	}
	// Der Pfad aus dem Manifest ist werkzeug-relativ und muss auf den
	// Bau umgerechnet worden sein — sonst faende das Skript nichts.
	if !strings.Contains(out.String(), filepath.Join(bau.ToolsEntwurfDir, "zaehlt", "example", "probe.txt")) {
		t.Errorf("der Beispielpfad wurde nicht auf den Bau umgerechnet:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "Matches what the Schmied predicted") {
		t.Errorf("die Uebereinstimmung wird nicht gemeldet:\n%s", out.String())
	}

	// Und trotzdem NICHT actual: Vorhersage und Skript stammen vom
	// selben Modell, das bestaetigt nichts.
	werkzeuge, err := bau.LadeTools(root)
	if err != nil {
		t.Fatal(err)
	}
	if werkzeuge[0].Zustand != bau.Hypothetical {
		t.Errorf("Zustand = %q, erwartet hypothetical — eine getroffene Vorhersage ist kein Urteil", werkzeuge[0].Zustand)
	}
	if werkzeuge[0].Review.VerifiedExpect != bau.ExpectMatch {
		t.Errorf("verified-expect = %q, erwartet match", werkzeuge[0].Review.VerifiedExpect)
	}
}

// TestFalscheVorhersageWiderlegt: Exit 0 und trotzdem invalid. Das ist
// der Fall, den es vorher gar nicht geben konnte — ein Skript, das
// tadellos durchlaeuft und dabei etwas anderes tut, als sein Erbauer
// behauptet hat.
func TestFalscheVorhersageWiderlegt(t *testing.T) {
	root := bauMitBeispiel(t, "zaehlt", "7")

	var out, errw strings.Builder
	code := run([]string{"-bau", root, "tool", "test", "zaehlt"}, &out, &errw)
	if code == 0 {
		t.Errorf("exit 0 trotz widerlegter Vorhersage:\n%s", out.String())
	}
	for _, muss := range []string{"NOT what the Schmied predicted", "expected", "got"} {
		if !strings.Contains(out.String(), muss) {
			t.Errorf("die Ausgabe nennt %q nicht:\n%s", muss, out.String())
		}
	}

	werkzeuge, err := bau.LadeTools(root)
	if err != nil {
		t.Fatal(err)
	}
	if werkzeuge[0].Zustand != bau.Invalid {
		t.Errorf("Zustand = %q, erwartet invalid — die Vorhersage ist widerlegt", werkzeuge[0].Zustand)
	}
	if werkzeuge[0].Review.VerifiedExpect != bau.ExpectMismatch {
		t.Errorf("verified-expect = %q, erwartet mismatch", werkzeuge[0].Review.VerifiedExpect)
	}
	// Der Exit-Code bleibt ehrlich: es LIEF, es tat nur das Falsche.
	if werkzeuge[0].Review.VerifiedExit == nil || *werkzeuge[0].Review.VerifiedExit != 0 {
		t.Errorf("verified-exit = %v, erwartet 0 — der Lauf ist nicht gescheitert", werkzeuge[0].Review.VerifiedExit)
	}
}

// TestFreigabeNimmtDenGanzenOrdner: das Beispiel wandert mit, sonst
// waere der Probelauf nach der Freigabe wieder ein Ratespiel.
func TestFreigabeNimmtDenGanzenOrdner(t *testing.T) {
	root := bauMitBeispiel(t, "zaehlt", "3")

	var out, errw strings.Builder
	if code := run([]string{"-bau", root, "tool", "test", "zaehlt"}, &out, &errw); code != 0 {
		t.Fatalf("Probelauf: %s", errw.String())
	}
	out.Reset()
	if code := run([]string{"-bau", root, "tool", "release", "-yes", "zaehlt"}, &out, &errw); code != 0 {
		t.Fatalf("release: exit %d — %s", code, errw.String())
	}
	for _, rel := range []string{"tool.json", "zaehlt.py", "example/probe.txt"} {
		if _, err := os.Stat(filepath.Join(root, bau.ToolsDir, "zaehlt", rel)); err != nil {
			t.Errorf("%s fehlt nach der Freigabe: %v", rel, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, bau.ToolsEntwurfDir, "zaehlt")); err == nil {
		t.Error("der Entwurfsordner steht noch da — verschoben heisst verschoben")
	}
}

// TestManifestAendernMachtOutdated: das Review haengt an dem, was
// gelesen wurde — und dazu gehoert das Manifest (Hasenbau-cgx). Es sagt
// mit `description`, wozu ein Modell das Werkzeug ruft, mit `args`, was
// es entgegennimmt, und mit `example`, was der Schmied vorhersagt.
// Bis heute lag es ausserhalb des Hashes: ein freigegebenes Werkzeug
// blieb `actual`, auch wenn danach jemand seine Beschreibung austauschte.
//
// Aufgefallen beim Migrieren des Test-Baus, wo genau das passierte —
// ein nachtraeglich eingefuegter example-Block, den nie jemand gelesen
// hat, und der Zustand blieb stehen.
func TestManifestAendernMachtOutdated(t *testing.T) {
	root := bauMitBeispiel(t, "zaehlt", "3")

	var out, errw strings.Builder
	if code := run([]string{"-bau", root, "tool", "test", "zaehlt"}, &out, &errw); code != 0 {
		t.Fatalf("Probelauf: %s", errw.String())
	}
	if code := run([]string{"-bau", root, "tool", "release", "-yes", "zaehlt"}, &out, &errw); code != 0 {
		t.Fatalf("release: %s", errw.String())
	}
	werkzeuge, err := bau.LadeTools(root)
	if err != nil {
		t.Fatal(err)
	}
	if werkzeuge[0].Zustand != bau.Actual {
		t.Fatalf("Vorbedingung: Zustand = %q, erwartet actual", werkzeuge[0].Zustand)
	}

	// Jetzt die Beschreibung austauschen — das Skript bleibt unberuehrt.
	pfad := filepath.Join(root, bau.ToolsDir, "zaehlt", bau.ToolManifest)
	roh, err := os.ReadFile(pfad)
	if err != nil {
		t.Fatal(err)
	}
	geaendert := strings.Replace(string(roh), "Zaehlt Zeilen.", "Loescht das Lager.", 1)
	if geaendert == string(roh) {
		t.Fatal("die Beschreibung wurde nicht ersetzt — Fixture geaendert?")
	}
	if err := os.WriteFile(pfad, []byte(geaendert), 0o644); err != nil {
		t.Fatal(err)
	}

	werkzeuge, err = bau.LadeTools(root)
	if err != nil {
		t.Fatal(err)
	}
	if werkzeuge[0].Zustand != bau.Outdated {
		t.Errorf("Zustand nach der Manifest-Aenderung = %q, erwartet outdated — "+
			"gelesen wurde eine andere Beschreibung", werkzeuge[0].Zustand)
	}
	if werkzeuge[0].Einsatzbereit() {
		t.Error("das Werkzeug ist weiter einsatzbereit — ein Hase bekaeme es mit der neuen Beschreibung")
	}
}

// TestAltesReviewNenntSeinenGrund: ein Block aus der Zeit vor
// manifest-sha256 macht das Werkzeug `generated`. In der Tabelle stuende
// dann ein Name neben "unread" — ein Widerspruch, bei dem der
// Betroffene den Fehler bei sich sucht. Er ist keiner (Hasenbau-cgx).
func TestAltesReviewNenntSeinenGrund(t *testing.T) {
	root := bauMitWerkzeug(t, "alt", skriptHeil, true)

	// Den Manifest-Hash aus dem Block entfernen — so sah jeder Block vor
	// dieser Aenderung aus.
	pfad := filepath.Join(root, bau.ToolsEntwurfDir, "alt", "alt.py")
	roh, err := os.ReadFile(pfad)
	if err != nil {
		t.Fatal(err)
	}
	var behalten []string
	for _, zeile := range strings.Split(string(roh), "\n") {
		if !strings.Contains(zeile, "manifest-sha256") {
			behalten = append(behalten, zeile)
		}
	}
	if len(behalten) == len(strings.Split(string(roh), "\n")) {
		t.Fatal("der Block trug gar kein manifest-sha256 — Fixture geaendert?")
	}
	if err := os.WriteFile(pfad, []byte(strings.Join(behalten, "\n")), 0o755); err != nil {
		t.Fatal(err)
	}

	werkzeuge, err := bau.LadeTools(root)
	if err != nil {
		t.Fatal(err)
	}
	if werkzeuge[0].Zustand != bau.Generated {
		t.Errorf("Zustand = %q, erwartet generated — der Block ist unvollstaendig", werkzeuge[0].Zustand)
	}
	if !werkzeuge[0].ReviewVeraltet() {
		t.Error("der Fall wird nicht als veraltetes Review erkannt")
	}

	var out, errw strings.Builder
	if code := run([]string{"-bau", root, "get", "tools", "-drafts"}, &out, &errw); code != 0 {
		t.Fatalf("exit %d — %s", code, errw.String())
	}
	if !strings.Contains(out.String(), "manifest-sha256") {
		t.Errorf("die Tabelle nennt den Grund nicht:\n%s", out.String())
	}
}
