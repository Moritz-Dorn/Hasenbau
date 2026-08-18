package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// aufrufMuster findet im Bau-Plugin jede Stelle, an der es das Binary
// ruft: `${hasenbau} -bau ${…} <Befehl> …`. Interessant ist nur, was
// zwischen dem Bau-Pfad und dem ersten Flag oder der ersten
// JS-Einsetzung steht — der Befehl samt seiner Unterbefehle.
var aufrufMuster = regexp.MustCompile(`\$\{hasenbau\} -bau \$\{[^}]+\} ([^\x60]+)`)

// befehlAusAufruf schneidet aus `tool state ${name}` das `tool state`
// heraus: alles bis zum ersten Argument, das kein Wort mehr ist.
func befehlAusAufruf(rest string) []string {
	var befehl []string
	for _, feld := range strings.Fields(rest) {
		if strings.HasPrefix(feld, "-") || strings.HasPrefix(feld, "${") {
			break
		}
		befehl = append(befehl, feld)
	}
	return befehl
}

// TestPluginRuftNurBekannteBefehle schließt die Lücke, die Hasenbau-yor
// beim Umbenennen von `sandbox-vorfall` aufgemacht hat: Plugin und
// Binary sind zwei Seiten desselben Vertrags, und wer nur eine dreht,
// merkt nichts. Das Plugin ist fail-closed und leise — ein unbekannter
// Befehl heißt „nicht gemeldet" beziehungsweise „kein Werkzeug", und
// die einzige Spur wäre eine Zeile im Server-Log des Baus.
//
// Geprüft wird nicht das Ergebnis des Aufrufs, sondern nur, dass die
// CLI den Befehl überhaupt kennt: ohne Argumente ist Exit 2 der
// erwartete Fall, „unknown command" dagegen nie.
func TestPluginRuftNurBekannteBefehle(t *testing.T) {
	root := probeBau(t)
	roh, err := os.ReadFile(filepath.Join(root, ".opencode-home", "opencode", "plugin", "hasenbau.js"))
	if err != nil {
		t.Fatal(err)
	}

	treffer := aufrufMuster.FindAllStringSubmatch(string(roh), -1)
	// Zwei sind es heute (tool state, sandbox-incident). Findet das
	// Muster gar nichts, hat jemand die Aufrufform geändert — dann prüft
	// dieser Test still nichts mehr.
	if len(treffer) < 2 {
		t.Fatalf("nur %d Aufrufe des Binaries im Plugin gefunden — Muster veraltet?", len(treffer))
	}

	for _, m := range treffer {
		befehl := befehlAusAufruf(m[1])
		if len(befehl) == 0 {
			t.Errorf("Aufruf ohne Befehl: %q", m[1])
			continue
		}
		var out, errw strings.Builder
		run(append([]string{"-bau", root}, befehl...), &out, &errw)
		gesamt := out.String() + errw.String()
		for _, unbekannt := range []string{"unknown command", "unknown verb"} {
			if strings.Contains(gesamt, unbekannt) {
				t.Errorf("Plugin ruft %q, die CLI kennt es nicht:\n%s",
					strings.Join(befehl, " "), gesamt)
			}
		}
	}
}
