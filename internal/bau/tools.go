// tools.go kennt die Schmied-Werkzeuge eines Baus (Hasenbau-hcs).
//
// Ein Werkzeug sind zwei Dateien nebeneinander unter `tools/`: das
// Skript (Python oder Bash) und ein Manifest, das sagt, wie es heißt,
// wozu es da ist und welche Argumente es nimmt. Gerufen wird es zur
// Laufzeit vom Bau-Plugin, das die Manifeste beim Server-Start liest
// und daraus je ein opencode-Werkzeug registriert — deshalb liegt hier
// kein generiertes TypeScript und keine Wrapper-Datei.
//
// Warum das Manifest und nicht das Skript die Wahrheit ist: der Schmied
// schreibt beides, aber nur das Manifest wird von Menschen gelesen und
// von Maschinen ausgewertet. Ein Skript ohne Manifest ist deshalb kein
// halbes Werkzeug, sondern gar keines.
package bau

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ToolsDir ist die Ablage der freigegebenen Werkzeuge, ToolsEntwurfDir
// die des Schmieds. Dieselbe Form wie bei den Gängen: was ein Mensch
// noch nicht angesehen hat, liegt unter entwurf/ und wird nicht geladen.
const (
	ToolsDir        = "tools"
	ToolsEntwurfDir = "tools/entwurf"
)

// Tool ist ein Werkzeug, wie es im Bau liegt.
type Tool struct {
	Name         string // Dateiname ohne Endung; zugleich der Werkzeugname beim Modell
	Beschreibung string
	Skript       string // Bau-relativer Pfad des Skripts
	Manifest     string // Bau-relativer Pfad des Manifests
	Args         []ToolArg
	Entwurf      bool // liegt unter tools/entwurf/, also nicht verschoben

	// Review und Zustand kommen aus dem Skript selbst (review.go). Der
	// Zustand ist abgeleitet und nirgends gespeichert — er lässt sich
	// jederzeit nachrechnen, von diesem Code wie von einer GUI.
	Review  Review
	Zustand Zustand
	Zeilen  int // des Skripts, damit man weiß, wie viel zu lesen ist
}

// Einsatzbereit heißt: dieses Werkzeug darf einem Hasen gegeben werden.
// Beides muss stimmen — verschoben UND im Probelauf gezeigt. Ein
// `hypothetical` ist gelesen, aber niemand hat es je laufen sehen; ein
// `outdated` ist seit dem Lesen verändert worden.
func (t Tool) Einsatzbereit() bool {
	return !t.Entwurf && t.Zustand == Actual
}

// ToolArg ist ein Argument, wie das Manifest es beschreibt. Mehr Typen
// als diese drei gibt es bewusst nicht: was ein Schmied-Werkzeug
// entgegennimmt, soll man in einer Zeile lesen können.
type ToolArg struct {
	Name         string `json:"name"`
	Typ          string `json:"type"` // string, number, boolean
	Beschreibung string `json:"description"`
	Pflicht      bool   `json:"required"`
}

// manifest ist die JSON-Form auf der Platte.
type manifest struct {
	Beschreibung string    `json:"description"`
	Skript       string    `json:"script"`
	Args         []ToolArg `json:"args"`
}

var erlaubteTypen = map[string]bool{"string": true, "number": true, "boolean": true}

// LadeTools liest die Werkzeuge unter tools/ und tools/entwurf/.
// Zurück kommen sie nach Namen sortiert, damit die Ausgabe und der
// generierte Agent deterministisch bleiben.
//
// Ein kaputtes Manifest ist ein Fehler und wird nicht übergangen: es
// still zu überspringen hieße, dass ein Hase sein Werkzeug ohne Grund
// nicht bekommt.
func LadeTools(root string) ([]Tool, error) {
	var alle []Tool
	for _, dir := range []string{ToolsDir, ToolsEntwurfDir} {
		treffer, err := filepath.Glob(filepath.Join(root, dir, "*.json"))
		if err != nil {
			return nil, err
		}
		sort.Strings(treffer)
		for _, pfad := range treffer {
			t, err := ladeTool(root, dir, pfad)
			if err != nil {
				return nil, err
			}
			alle = append(alle, t)
		}
	}
	sort.Slice(alle, func(i, j int) bool {
		if alle[i].Entwurf != alle[j].Entwurf {
			return !alle[i].Entwurf
		}
		return alle[i].Name < alle[j].Name
	})
	return alle, nil
}

// ToolNamen liefert zwei Listen für den Generator.
//
// `alle` sind die Namen, die das Plugin überhaupt registrieren KÖNNTE —
// alles außerhalb von entwurf/, unabhängig vom Zustand. Genau diese
// werden im generierten Agenten verboten, wenn der Auftrag sie nicht
// nennt. Bewusst großzügig: sollte die Hash-Prüfung im Plugin je
// danebengreifen, hält das Verbot trotzdem.
//
// `bereit` sind die, die ein Auftrag wirksam freigeben kann — gelesen,
// unverändert und im Probelauf gezeigt. Ein Werkzeug, das ein Auftrag
// nennt, das aber nicht bereit ist, bleibt verboten; sichtbar wird das
// in `get tools` und `describe bau`.
func ToolNamen(root string) (alle, bereit []string, err error) {
	werkzeuge, err := LadeTools(root)
	if err != nil {
		return nil, nil, err
	}
	for _, t := range werkzeuge {
		if t.Entwurf {
			continue
		}
		alle = append(alle, t.Name)
		if t.Einsatzbereit() {
			bereit = append(bereit, t.Name)
		}
	}
	return alle, bereit, nil
}

func ladeTool(root, dir, manifestPfad string) (Tool, error) {
	name := strings.TrimSuffix(filepath.Base(manifestPfad), ".json")
	rel := filepath.Join(dir, filepath.Base(manifestPfad))
	fehler := func(format string, args ...any) (Tool, error) {
		return Tool{}, fmt.Errorf("werkzeug %s (%s): %s", name, rel, fmt.Sprintf(format, args...))
	}

	roh, err := os.ReadFile(manifestPfad)
	if err != nil {
		return fehler("%v", err)
	}
	var m manifest
	dec := json.NewDecoder(strings.NewReader(string(roh)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return fehler("manifest: %v (erlaubt: description, script, args)", err)
	}
	if strings.TrimSpace(m.Beschreibung) == "" {
		return fehler("description fehlt — sie ist das, woran das Modell erkennt, wozu das Werkzeug da ist")
	}
	if strings.TrimSpace(m.Skript) == "" {
		return fehler("script fehlt")
	}
	// Das Skript muss neben dem Manifest liegen. Ein Pfad, der
	// woandershin zeigt, wäre ein Weg aus dem Bau heraus — und das
	// Skript läuft im Server-Prozess, nicht in der Sandbox des Hasen.
	if filepath.Base(m.Skript) != m.Skript {
		return fehler("script %q enthält einen Pfad — erlaubt ist nur ein Dateiname neben dem Manifest", m.Skript)
	}
	skript := filepath.Join(dir, m.Skript)
	roh, lesefehler := os.ReadFile(filepath.Join(root, skript))
	if lesefehler != nil {
		return fehler("script %s fehlt", skript)
	}
	review, body := LiesReview(roh)

	gesehen := map[string]bool{}
	for i, a := range m.Args {
		if strings.TrimSpace(a.Name) == "" {
			return fehler("arg %d: name fehlt", i+1)
		}
		if gesehen[a.Name] {
			return fehler("arg %q steht zweimal", a.Name)
		}
		gesehen[a.Name] = true
		if !erlaubteTypen[a.Typ] {
			return fehler("arg %q: type %q — erlaubt sind string, number, boolean", a.Name, a.Typ)
		}
		if strings.TrimSpace(a.Beschreibung) == "" {
			return fehler("arg %q: description fehlt", a.Name)
		}
	}

	return Tool{
		Name:         name,
		Beschreibung: strings.TrimSpace(m.Beschreibung),
		Skript:       skript,
		Manifest:     rel,
		Args:         m.Args,
		Entwurf:      dir == ToolsEntwurfDir,
		Review:       review,
		Zustand:      LeiteZustandAb(review, body),
		Zeilen:       strings.Count(string(roh), "\n"),
	}, nil
}
