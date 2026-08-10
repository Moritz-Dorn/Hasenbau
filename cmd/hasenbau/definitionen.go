// definitionen.go: die lesende Sicht auf das, was im Bau definiert ist —
// Aufträge, Hasen und die Gang-Dateien, die sie benutzen.
//
// Zwei Dinge stehen hier, die in keiner Datei stehen und deshalb der
// eigentliche Grund für `describe` sind: welche Räume eines Auftrags
// Schreibrecht geben, und welche Permissions ein Hase in einem
// konkreten Auftrag effektiv hat (die entstehen erst aus dessen Räumen,
// PLAN.md §6).
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Moritz-Dorn/Hasenbau/internal/auftrag"
	"github.com/Moritz-Dorn/Hasenbau/internal/hase"
)

// gangVerzeichnis ist der Ort der Gang-Skripte, Bau-relativ (§4).
const gangVerzeichnis = "gaenge/"

// gangDateien zieht die Bau-relativen Skript-Pfade aus einer run-Zeile.
// Bewusst simpel: ein Token, das mit `gaenge/` beginnt, ist eine
// Referenz. Das deckt die realen Formen ab (`python3 gaenge/x.py …`,
// `gaenge/x.py …`) und erfindet keinen Shell-Parser — eine run-Zeile
// darf alles Mögliche sein, und was hier nicht erkannt wird, fehlt
// höchstens in einer Übersicht.
func gangDateien(run string) []string {
	var out []string
	for _, feld := range strings.Fields(run) {
		feld = strings.Trim(feld, `"'`)
		if strings.HasPrefix(feld, gangVerzeichnis) {
			out = append(out, feld)
		}
	}
	return out
}

// ladeDefinitionen liest Aufträge und Hasen, ohne Agenten zu schreiben —
// anders als ladeUndGeneriere, das zum Lauf gehört. Ein Lesebefehl
// verändert den Bau nicht.
func ladeDefinitionen(root string) ([]*auftrag.Auftrag, error) {
	return auftrag.Load(root)
}

// hasenNamen listet die Templates unter hasen/.
func hasenNamen(root string) ([]string, error) {
	dateien, err := filepath.Glob(filepath.Join(root, "hasen", "*.md"))
	if err != nil {
		return nil, fmt.Errorf("hasen suchen: %w", err)
	}
	sort.Strings(dateien)
	namen := make([]string, 0, len(dateien))
	for _, d := range dateien {
		namen = append(namen, strings.TrimSuffix(filepath.Base(d), ".md"))
	}
	return namen, nil
}

// nutzer sind die Aufträge, die einen Hasen einsetzen.
func nutzer(auftraege []*auftrag.Auftrag, haseName string) []*auftrag.Auftrag {
	var out []*auftrag.Auftrag
	for _, a := range auftraege {
		if a.Hase == haseName {
			out = append(out, a)
		}
	}
	return out
}

// schreibRaeume nennt die Rollen, aus denen Schreibrecht entsteht — die
// Stelle, an der sich Nutzer am ehesten irren. Die Liste spiegelt
// hase.Generiere; weicht sie ab, lügt die Anzeige, deshalb prüft ein
// Test beides gegeneinander.
var schreibRaeume = []string{"work", "out"}

func gibtSchreibrecht(rolle string) bool {
	for _, r := range schreibRaeume {
		if r == rolle {
			return true
		}
	}
	return false
}

// existiert meldet, ob ein Bau-relativer Pfad da ist.
func existiert(root, rel string) bool {
	_, err := os.Stat(filepath.Join(root, rel))
	return err == nil
}

// effektivePermissions rendert den permission-Block, den ein Hase in
// einem konkreten Auftrag bekommt. Genau dafür gibt es describe: die
// Rechte stehen weder im Template noch im Auftrag, sie entstehen erst
// beim Generieren.
func effektivePermissions(root string, a *auftrag.Auftrag, t *hase.Template) ([]string, error) {
	roh, err := hase.Generiere(a, t)
	if err != nil {
		return nil, err
	}
	var zeilen []string
	drin := false
	for _, zeile := range strings.Split(string(roh), "\n") {
		switch {
		case zeile == "permission:":
			drin = true
		case drin && strings.HasPrefix(zeile, "  "):
			zeilen = append(zeilen, strings.TrimPrefix(zeile, "  "))
		case drin:
			return zeilen, nil
		}
	}
	return zeilen, nil
}
