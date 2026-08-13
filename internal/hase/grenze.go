// grenze.go schreibt auf, was ein Werkzeug im Betrieb anfassen darf
// (Hasenbau-9w6, Stufe 2).
//
// Der Satz dahinter ist einer: EIN WERKZEUG DARF NIE MEHR ALS DER HASE,
// DER ES RUFT. Ein Schmied-Werkzeug läuft im opencode-Prozess und damit
// außerhalb jeder Hasen-Sandbox; ohne Grenze wäre es der bequemste Weg
// aus den Raum-Rechten des §6 heraus — der Hase darf `raeume/eingang`
// nicht schreiben, sein Werkzeug schon.
//
// Die Grenze ist deshalb keine eigene Erfindung, sondern dieselbe wie
// die des Hasen und aus derselben Quelle: den Räumen seines Auftrags.
// Lesen darf ein Werkzeug, was der Hase liest; schreiben, was er
// schreibt (`schreibRollen`, also work und out).
//
// GESCHRIEBEN wird die Grenze hier, ANGEWENDET im Bau-Plugin — dort
// läuft der Aufruf. Das ist dieselbe Arbeitsteilung wie beim
// Sandbox-Wächter (PLAN §6): die Regel bleibt im Hasenbau, das Plugin
// ist der dünne Hook. Sie liegt neben den generierten Agenten, weil sie
// dasselbe Leben hat: sie entsteht beim Laden der Definitionen und gilt,
// bis die Aufträge sich ändern.
package hase

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"

	"github.com/Moritz-Dorn/Hasenbau/internal/auftrag"
)

// GrenzenDatei liegt neben den generierten Agenten und trägt für jeden
// von ihnen, was ein Werkzeug in seinem Namen anfassen darf.
const GrenzenDatei = ".opencode-home/opencode/hasenbau-raeume.json"

// Grenze ist die Raum-Grenze eines Agenten. Die Schlüssel sind englisch
// wie jeder Formatschlüssel (AGENTS.md §1); die Pfade sind
// Bau-relativ — absolut würden sie den Bau an einen Ort binden, und ein
// Bau ist verschiebbar.
type Grenze struct {
	Agent string   `json:"agent"`
	Read  []string `json:"read"`
	Write []string `json:"write"`
}

// GrenzeVon leitet die Grenze aus den Räumen eines Auftrags ab.
//
// `Write` ist eine Teilmenge von `Read`: was man schreiben darf, darf
// man auch lesen. Das steht ausdrücklich in beiden Listen, damit die
// anwendende Seite nichts zusammenrechnen muss — eine Regel, die zwei
// Stellen unterschiedlich ausrechnen, ist schon einmal auseinander
// gelaufen (Hasenbau-7or).
func GrenzeVon(a *auftrag.Auftrag) Grenze {
	g := Grenze{Agent: AgentName(a)}
	schreib := map[string]bool{}
	for _, rolle := range schreibRollen {
		if pfad, ok := a.Raeume[rolle]; ok && pfad != "" {
			schreib[sauber(pfad)] = true
		}
	}
	gesehen := map[string]bool{}
	for _, pfad := range a.Raeume {
		if pfad == "" {
			continue
		}
		p := sauber(pfad)
		if gesehen[p] {
			continue
		}
		gesehen[p] = true
		g.Read = append(g.Read, p)
	}
	for p := range schreib {
		g.Write = append(g.Write, p)
	}
	// Sortiert, damit die Datei stabil bleibt: sonst erzeugte jeder
	// Daemon-Start ein anderes Ergebnis, und ein `git diff` im Bau wäre
	// nicht mehr zu lesen.
	sort.Strings(g.Read)
	sort.Strings(g.Write)
	return g
}

func sauber(pfad string) string {
	return filepath.ToSlash(filepath.Clean(pfad))
}

// SchreibeGrenzen legt die Datei neben den generierten Agenten ab. Sie
// wird bei jedem Laden der Definitionen NEU geschrieben und nicht
// ergänzt: ein Auftrag, den es nicht mehr gibt, soll keine Grenze
// hinterlassen, die noch jemanden hereinlässt.
func SchreibeGrenzen(root string, auftraege []*auftrag.Auftrag) error {
	grenzen := make([]Grenze, 0, len(auftraege))
	for _, a := range auftraege {
		grenzen = append(grenzen, GrenzeVon(a))
	}
	sort.Slice(grenzen, func(i, j int) bool { return grenzen[i].Agent < grenzen[j].Agent })

	roh, err := json.MarshalIndent(grenzen, "", "  ")
	if err != nil {
		return err
	}
	roh = append(roh, '\n')
	pfad := filepath.Join(root, filepath.FromSlash(GrenzenDatei))
	if err := os.MkdirAll(filepath.Dir(pfad), 0o755); err != nil {
		return err
	}
	return os.WriteFile(pfad, roh, 0o644)
}
