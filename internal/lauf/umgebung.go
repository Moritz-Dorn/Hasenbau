// Package lauf baut die Umgebung einer Auftrags-Ausführung (PLAN.md §6):
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

// Umgebung ist der aufgelöste Kontext eines Laufs. Alle Pfade außer Bau
// sind Bau-relativ — Prozesse laufen mit CWD im Bau (§3-Invariante),
// und die Permission-Patterns der Hasen ankern ebenfalls dort.
type Umgebung struct {
	Bau    string            // absoluter Bau-Root ($BAU)
	Input  string            // auslösende Datei ($INPUT), leer bei cron
	Work   string            // Scratch dieses Laufs ($WORK), leer ohne work-Raum
	Raeume map[string]string // Rolle → Pfad ($RAUM_<rolle>)
}

var laufIDMuster = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

// Neue legt fehlende Räume an (Räume sind Konvention, kein Vertrag —
// §4), erzeugt das $WORK-Verzeichnis dieses Laufs und bindet die
// Variablen. input ist der Bau-relative Pfad der auslösenden Datei —
// Pflicht bei watch-Triggern, verboten bei cron.
func Neue(root string, a *auftrag.Auftrag, laufID, input string) (*Umgebung, error) {
	fehler := func(format string, args ...any) error {
		return fmt.Errorf("lauf %s (%s): %s", laufID, a.Name, fmt.Sprintf(format, args...))
	}

	if !filepath.IsAbs(root) {
		return nil, fehler("bau-Root %q muss absolut sein", root)
	}
	if !laufIDMuster.MatchString(laufID) {
		return nil, fehler("ungültige Lauf-ID %q", laufID)
	}
	switch {
	case a.Trigger.Watch != "" && input == "":
		return nil, fehler("watch-Trigger ohne $INPUT — die auslösende Datei fehlt")
	case a.Trigger.Cron != "" && input != "":
		return nil, fehler("cron-Trigger mit $INPUT %q — woher kommt die Datei?", input)
	}

	u := &Umgebung{Bau: root, Input: input, Raeume: a.Raeume}
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

// variableMuster: $NAME, beginnend mit Großbuchstabe — deckt $BAU,
// $INPUT, $WORK und $RAUM_<rolle> ab. Shell-Variablen wie $HOME sind
// absichtlich keine Ausnahme: unbekannt ⇒ Fehler.
var variableMuster = regexp.MustCompile(`\$([A-Z][A-Za-z0-9_]*)`)

// Ersetze substituiert die Lauf-Variablen in s. Unbekannte oder in
// diesem Lauf nicht gebundene Variablen sind ein Fehler — nie ein
// stilles Leerersetzen.
func (u *Umgebung) Ersetze(s string) (string, error) {
	var fehler []string
	ersetzt := variableMuster.ReplaceAllStringFunc(s, func(treffer string) string {
		name := treffer[1:]
		switch {
		case name == "BAU":
			return u.Bau
		case name == "INPUT":
			if u.Input == "" {
				fehler = append(fehler, "$INPUT ist nur bei watch-Triggern gebunden")
				return treffer
			}
			return u.Input
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
			fehler = append(fehler, fmt.Sprintf("unbekannte Variable $%s (erlaubt: $BAU, $INPUT, $WORK, $RAUM_<rolle>)", name))
			return treffer
		}
	})
	if len(fehler) > 0 {
		return "", fmt.Errorf("%s — in %q", strings.Join(fehler, "; "), s)
	}
	return ersetzt, nil
}
