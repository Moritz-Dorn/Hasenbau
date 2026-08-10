// Package lauf baut die Environment einer Auftrags-Ausführung (PLAN.md §6):
// Räume existieren, $WORK ist pro Lauf angelegt, Variablen sind gebunden.
// Alles hier ist deterministisch — kein LLM, keine Substitution im Stillen.
package lauf

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Moritz-Dorn/Hasenbau/internal/auftrag"
)

// Environment ist der aufgelöste Kontext eines Laufs. Alle Pfade außer Bau
// sind Bau-relativ — Prozesse laufen mit CWD im Bau (§3-Invariante),
// und die Permission-Patterns der Hasen ankern ebenfalls dort.
type Environment struct {
	Bau    string            // absoluter Bau-Root ($BAU)
	Input  string            // Auslöser ($INPUT): auslösende Datei bei watch, freies Argument bei manuell
	Work   string            // Scratch dieses Laufs ($WORK), leer ohne work-Raum
	Raeume map[string]string // Rolle → Pfad ($RAUM_<rolle>)

	// TriggerKind ist die Art des Auftrags (auftrag.TriggerWatch|Cron|
	// Manuell), nicht der Trigger der laeufe-Zeile. Nur bei watch ist
	// Input ein Pfad — daran hängt die Quarantäne (§7).
	TriggerKind string

	// Hasenbau ist der absolute Pfad des laufenden Binaries
	// ($HASENBAU). Ein Gang, der den Hasenbau selbst aufruft, darf
	// sich nicht auf den PATH des Daemons verlassen.
	Hasenbau string
}

var laufIDPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

// Neue legt fehlende Räume an (Räume sind Konvention, kein Vertrag —
// §4), erzeugt das $WORK-Verzeichnis dieses Laufs und bindet die
// Variablen. input ist bei watch der Bau-relative Pfad der auslösenden
// Datei (Pflicht), bei cron verboten und bei manuell das übergebene
// Argument (optional, kein Pfad).
func Neue(root string, a *auftrag.Auftrag, laufID, input string) (*Environment, error) {
	// Ohne Lauf-Präfix: der einzige Aufrufer ist der Runner, und der
	// setzt es selbst davor. Sonst stünde es zweimal in derselben Zeile
	// (Hasenbau-vwr).
	fehler := func(format string, args ...any) error {
		return fmt.Errorf(format, args...)
	}

	if !filepath.IsAbs(root) {
		return nil, fehler("bau-Root %q muss absolut sein", root)
	}
	if !laufIDPattern.MatchString(laufID) {
		return nil, fehler("ungültige Lauf-ID %q", laufID)
	}
	switch {
	case a.Trigger.Watch != "" && input == "":
		return nil, fehler("watch-Trigger ohne $INPUT — die auslösende Datei fehlt")
	case a.Trigger.Cron != "" && input != "":
		return nil, fehler("cron-Trigger mit $INPUT %q — woher kommt die Datei?", input)
	}
	if err := checkInput(input); err != nil {
		return nil, fehler("%v", err)
	}

	// os.Executable scheitert praktisch nie; wenn doch, bleibt
	// $HASENBAU ungebunden und Ersetze sagt das deutlich, statt einen
	// Gang mit leerem Kommando zu starten.
	exe, _ := os.Executable()

	u := &Environment{
		Bau:         root,
		Input:       input,
		Raeume:      a.Raeume,
		TriggerKind: a.Trigger.Kind(),
		Hasenbau:    exe,
	}
	for rolle, pfad := range a.Raeume {
		if err := os.MkdirAll(filepath.Join(root, pfad), 0o755); err != nil {
			return nil, fehler("raum %s anlegen: %w", rolle, err)
		}
	}
	if arbeitsRaum, ok := a.Raeume["work"]; ok {
		u.Work = filepath.Join(arbeitsRaum, laufID)
		if err := os.MkdirAll(filepath.Join(root, u.Work), 0o755); err != nil {
			return nil, fehler("$WORK anlegen: %w", err)
		}
	}
	return u, nil
}

// shellUnsafe sind die Zeichen, mit denen ein Input aus dem "$INPUT"
// einer Gang-Zeile ausbrechen kann: Anführungszeichen beenden die
// Quotierung, Backtick und $ starten eine Substitution, der Backslash
// hebelt das Escaping aus.
const shellUnsafe = "\"'`$\\"

// checkInput hält $INPUT von der Shell fern.
//
// Die Variablen werden vor `sh -c` TEXTUELL ersetzt (§6) — das ist
// Absicht, nur so ist `$HOME` in einer Gang-Zeile ein harter Fehler
// statt einer stillen Expansion. Der Preis: der Input landet
// unquotiert im Kommando, und bei einem watch-Trigger kommt er aus
// einem Dateinamen, den irgendwer in die Drop-Zone gelegt hat. Eine
// Datei namens `x";rm -rf ~;"y.pdf` wäre sonst ein Kommando
// (Hasenbau-bnh).
//
// Deshalb hier die Grenze und nicht im Gang: der Gang ist Text aus
// einem Auftrag, den ein Mensch geschrieben hat — wer ihn schreibt,
// darf darin alles. Der Input ist das Einzige, was von außen kommt.
// Leerzeichen, Klammern und Bindestriche bleiben erlaubt: die stehen
// in echten Dateinamen und sind in "$INPUT" harmlos.
func checkInput(input string) error {
	if i := strings.IndexAny(input, shellUnsafe); i >= 0 {
		return fmt.Errorf("$INPUT %q enthält %q — solche Zeichen brechen aus der Gang-Zeile aus (%s sind verboten); die Datei umbenennen",
			input, string(input[i]), shellUnsafe)
	}
	for _, r := range input {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("$INPUT %q enthält ein Steuerzeichen (%U) — die Datei umbenennen", input, r)
		}
	}
	return nil
}

// variablePattern: $NAME, beginnend mit Großbuchstabe — deckt $BAU,
// $INPUT, $WORK und $RAUM_<rolle> ab. Shell-Variablen wie $HOME sind
// absichtlich keine Ausnahme: unbekannt ⇒ Fehler.
var variablePattern = regexp.MustCompile(`\$([A-Z][A-Za-z0-9_]*)`)

// Substitute substituiert die Lauf-Variablen in s. Unbekannte oder in
// diesem Lauf nicht gebundene Variablen sind ein Fehler — nie ein
// stilles Leerersetzen.
func (u *Environment) Substitute(s string) (string, error) {
	var fehler []string
	ersetzt := variablePattern.ReplaceAllStringFunc(s, func(treffer string) string {
		name := treffer[1:]
		switch {
		case name == "BAU":
			return u.Bau
		case name == "INPUT":
			if u.Input == "" {
				fehler = append(fehler, "$INPUT ist bei watch-Triggern gebunden und bei manuell-Läufen mit Argument")
				return treffer
			}
			return u.Input
		case name == "HASENBAU":
			if u.Hasenbau == "" {
				fehler = append(fehler, "$HASENBAU: eigener Programmpfad nicht bestimmbar")
				return treffer
			}
			return u.Hasenbau
		case name == "WORK":
			if u.Work == "" {
				fehler = append(fehler, "$WORK braucht einen Raum mit Rolle work")
				return treffer
			}
			return u.Work
		case strings.HasPrefix(name, "RAUM_"):
			rolle := name[len("RAUM_"):]
			pfad, ok := u.Raeume[rolle]
			if !ok {
				fehler = append(fehler, fmt.Sprintf("$%s: Auftrag definiert keinen Raum %q", name, rolle))
				return treffer
			}
			return pfad
		default:
			fehler = append(fehler, fmt.Sprintf("unbekannte Variable $%s (erlaubt: $BAU, $INPUT, $WORK, $RAUM_<rolle>, $HASENBAU)", name))
			return treffer
		}
	})
	if len(fehler) > 0 {
		return "", fmt.Errorf("%s — in %q", strings.Join(fehler, "; "), s)
	}
	return ersetzt, nil
}
