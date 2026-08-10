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
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

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

// gangDatei ist eine Skriptdatei unter gaenge/ samt ihrer Benutzungen.
type gangDatei struct {
	Pfad        string // Bau-relativ
	Groesse     int64
	Ausfuehrbar bool
	Entwurf     bool // liegt unter gaenge/entwurf/ — vom Baumeister, nicht eingetragen
	Benutzungen []gangBenutzung
}

// gangBenutzung ist ein Auftrag, der diese Datei in einer run-Zeile ruft.
type gangBenutzung struct {
	Auftrag string
	Gang    string
	Run     string
	Timeout time.Duration
}

// entwurfVerzeichnis ist der Ablageort des Baumeisters (§8).
const entwurfVerzeichnis = gangVerzeichnis + "entwurf/"

// sammleGaenge listet die Dateien unter gaenge/ und hängt jeder die
// Aufträge an, die sie benutzen. Die Beziehung wird abgeleitet, nicht
// gepflegt: die Wahrheit steht in den run-Zeilen.
func sammleGaenge(root string, auftraege []*auftrag.Auftrag) ([]gangDatei, error) {
	benutzung := map[string][]gangBenutzung{}
	for _, a := range auftraege {
		for _, g := range a.Gaenge {
			for _, datei := range gangDateien(g.Run) {
				benutzung[datei] = append(benutzung[datei], gangBenutzung{
					Auftrag: a.Name, Gang: g.Name, Run: g.Run, Timeout: g.Timeout,
				})
			}
		}
	}

	var out []gangDatei
	gesehen := map[string]bool{}
	wurzel := filepath.Join(root, gangVerzeichnis)
	err := filepath.WalkDir(wurzel, func(pfad string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) && pfad == wurzel {
				return nil // gaenge/ gibt es erst nach init
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, pfad)
		if err != nil {
			return err
		}
		gesehen[rel] = true
		out = append(out, gangDatei{
			Pfad:        rel,
			Groesse:     info.Size(),
			Ausfuehrbar: info.Mode()&0o111 != 0,
			Entwurf:     strings.HasPrefix(rel, entwurfVerzeichnis),
			Benutzungen: benutzung[rel],
		})
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("gaenge lesen: %w", err)
	}

	// Referenzen ins Leere gehören mit in die Liste — sonst fällt der
	// häufigste Fehler (Datei umbenannt, Auftrag nicht) niemandem auf.
	for datei, b := range benutzung {
		if !gesehen[datei] {
			out = append(out, gangDatei{Pfad: datei, Benutzungen: b})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Pfad < out[j].Pfad })
	return out, nil
}

// zweck zieht die erste erklärende Zeile aus einem Skript: den Anfang
// des Python-Docstrings oder den ersten Kommentar nach dem Shebang.
func zweck(root, rel string) string {
	roh, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		return ""
	}
	zeilen := strings.Split(string(roh), "\n")
	if len(zeilen) > 30 {
		zeilen = zeilen[:30]
	}
	for _, z := range zeilen {
		z = strings.TrimSpace(z)
		switch {
		case z == "" || strings.HasPrefix(z, "#!"):
			continue
		case strings.HasPrefix(z, `"""`), strings.HasPrefix(z, "'''"):
			z = strings.TrimLeft(z, `"'`)
		case strings.HasPrefix(z, "#"):
			z = strings.TrimLeft(z, "# ")
		default:
			return "" // Code vor Doku: dann gibt es hier nichts zu holen
		}
		if z = strings.TrimSpace(z); z != "" {
			return z
		}
	}
	return ""
}
