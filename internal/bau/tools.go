// tools.go kennt die Schmied-Werkzeuge eines Baus (Hasenbau-hcs).
//
// Ein Werkzeug ist ein ORDNER (Hasenbau-lnk): darin `tool.json`, das
// Skript (Python oder Bash) und optional `example/` mit dem Material
// für den Probelauf. Der Ordnername ist der Werkzeugname — er steht
// damit genau einmal da und kann nicht auseinanderlaufen. Gerufen wird
// das Werkzeug zur Laufzeit vom Bau-Plugin, das die Manifeste beim
// Server-Start liest und daraus je ein opencode-Werkzeug registriert —
// deshalb liegt hier kein generiertes TypeScript und keine
// Wrapper-Datei.
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
// die des Schmieds. Was ein Mensch noch nicht angesehen hat, liegt unter
// drafts/ und wird dem Plugin nie angeboten.
//
// Beide liegen NEBENEINANDER, nicht ineinander: läge drafts/ unter dem
// released-Ordner, wäre jede Regel über „alles unter tools/released" um
// eine Ausnahme reicher — und genau solche Ausnahmen fallen später weg.
const (
	ToolsDir        = "tools/released"
	ToolsEntwurfDir = "tools/drafts"

	// ToolManifest ist der feste Name des Manifests im Werkzeug-Ordner.
	ToolManifest = "tool.json"

	// ToolExampleDir ist der Ordner mit dem Material für den Probelauf,
	// relativ zum Werkzeug-Ordner.
	ToolExampleDir = "example"
)

// Tool ist ein Werkzeug, wie es im Bau liegt.
type Tool struct {
	Name         string // Ordnername; zugleich der Werkzeugname beim Modell
	Beschreibung string
	Ordner       string // Bau-relativer Pfad des Werkzeug-Ordners
	Skript       string // Bau-relativer Pfad des Skripts
	Manifest     string // Bau-relativer Pfad des Manifests
	Args         []ToolArg
	Beispiel     *ToolBeispiel // was der Schmied vorhergesagt hat; nil, wenn keines dabei ist
	Entwurf      bool          // liegt unter tools/drafts/, also nicht verschoben

	// Review und Zustand kommen aus dem Skript selbst (review.go). Der
	// Zustand ist abgeleitet und nirgends gespeichert — er lässt sich
	// jederzeit nachrechnen, von diesem Code wie von einer GUI.
	Review  Review
	Zustand Zustand
	Zeilen  int // des Skripts, damit man weiß, wie viel zu lesen ist
}

// ReviewVeraltet erkennt den einen Fall, der sonst widersprüchlich
// aussieht: ein Block ist da und trägt einen Namen, aber er stammt aus
// der Zeit vor `manifest-sha256` (Hasenbau-cgx). Der Zustand ist dann
// `generated` — „ungelesen" —, während daneben steht, wer gelesen hat.
//
// Ohne diesen Hinweis sucht der Betroffene den Fehler bei sich. Er ist
// keiner: das Manifest hat unter der alten Zusage niemand geprüft, und
// deshalb zählt das Review nicht mehr.
func (t Tool) ReviewVeraltet() bool {
	return t.Review.By != "" && t.Review.Fehler == "" && t.Review.ManifestHash == ""
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

// ToolBeispiel ist die VORHERSAGE des Schmieds: mit diesen Argumenten
// gerufen, kommt genau das heraus. Sie ist der Grund, warum der
// Probelauf mehr sein kann als eine Vorführung — und sie ist möglich,
// WEIL der Schmied sein Skript nicht ausprobieren darf (Hasenbau-hiz).
// Wer nicht raten kann, muss überblicken.
//
// Die Asymmetrie bleibt die aus der Intentionssemantik: eine Abweichung
// widerlegt die Vorhersage, und das darf eine Maschine feststellen. Eine
// Übereinstimmung bestätigt nichts — sie zeigt nur, dass das Skript tut,
// was ein Modell über es behauptet hat. Beide sind vom selben Modell.
type ToolBeispiel struct {
	Args     map[string]any `json:"args"`
	Erwartet string         `json:"expect"`
}

// manifest ist die JSON-Form auf der Platte.
type manifest struct {
	Beschreibung string        `json:"description"`
	Skript       string        `json:"script"`
	Args         []ToolArg     `json:"args"`
	Beispiel     *ToolBeispiel `json:"example"`
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
		// Ein Werkzeug ist ein Ordner mit tool.json darin. Der Glob sucht
		// deshalb genau eine Ebene tief — was daneben liegt (eine lose
		// Datei, ein Ordner ohne Manifest), ist kein halbes Werkzeug,
		// sondern gar keines und wird still übergangen.
		treffer, err := filepath.Glob(filepath.Join(root, dir, "*", ToolManifest))
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
	// Der ORDNERNAME ist der Werkzeugname. Damit steht er genau einmal
	// da; ein Manifest, das sich anders nennt, gibt es nicht mehr.
	ordner := filepath.Join(dir, filepath.Base(filepath.Dir(manifestPfad)))
	name := filepath.Base(ordner)
	rel := filepath.Join(ordner, ToolManifest)
	fehler := func(format string, args ...any) (Tool, error) {
		return Tool{}, fmt.Errorf("werkzeug %s (%s): %s", name, rel, fmt.Sprintf(format, args...))
	}

	manifestRoh, err := os.ReadFile(manifestPfad)
	if err != nil {
		return fehler("%v", err)
	}
	var m manifest
	dec := json.NewDecoder(strings.NewReader(string(manifestRoh)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return fehler("manifest: %v (erlaubt: description, script, args, example)", err)
	}
	if strings.TrimSpace(m.Beschreibung) == "" {
		return fehler("description missing — it is how the model recognises what the tool is for")
	}
	if strings.TrimSpace(m.Skript) == "" {
		return fehler("script fehlt")
	}
	// Das Skript muss neben dem Manifest liegen. Ein Pfad, der
	// woandershin zeigt, wäre ein Weg aus dem Bau heraus — und das
	// Skript läuft im Server-Prozess, nicht in der Sandbox des Hasen.
	if filepath.Base(m.Skript) != m.Skript {
		return fehler("script %q contains a path — only a file name next to the manifest is allowed", m.Skript)
	}
	skript := filepath.Join(ordner, m.Skript)
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
			return fehler("arg %q listed twice", a.Name)
		}
		gesehen[a.Name] = true
		if !erlaubteTypen[a.Typ] {
			return fehler("arg %q: type %q — allowed are string, number, boolean", a.Name, a.Typ)
		}
		if strings.TrimSpace(a.Beschreibung) == "" {
			return fehler("arg %q: description missing", a.Name)
		}
	}

	// Das Beispiel muss zu den deklarierten Argumenten passen, sonst
	// scheitert der Probelauf erst am Skript und sieht dann wie dessen
	// Fehler aus.
	if m.Beispiel != nil {
		for arg, wert := range m.Beispiel.Args {
			if !gesehen[arg] {
				return fehler("example calls %q, which the manifest does not declare", arg)
			}
			// Pfade im Beispiel sind relativ zum WERKZEUG-ORDNER, nicht
			// zum Bau: der Ordner wandert bei der Freigabe von drafts/
			// nach released/, und ein Bau-relativer Pfad zeigte danach
			// ins Leere.
			s, ok := wert.(string)
			if !ok {
				continue
			}
			if filepath.IsAbs(s) || strings.HasPrefix(s, "..") {
				return fehler("example %s = %q — only paths inside the tool folder are allowed", arg, s)
			}
		}
		for _, a := range m.Args {
			if _, da := m.Beispiel.Args[a.Name]; a.Pflicht && !da {
				return fehler("example is missing the required argument %q", a.Name)
			}
		}
	}

	return Tool{
		Name:         name,
		Beschreibung: strings.TrimSpace(m.Beschreibung),
		Ordner:       ordner,
		Skript:       skript,
		Manifest:     rel,
		Args:         m.Args,
		Beispiel:     m.Beispiel,
		Entwurf:      dir == ToolsEntwurfDir,
		Review:       review,
		Zustand:      LeiteZustandAb(review, body, ManifestHash(string(manifestRoh))),
		Zeilen:       strings.Count(string(roh), "\n"),
	}, nil
}

// BeispielArgv baut die Kommandozeile des Probelaufs aus dem Beispiel:
// `--name wert`, in der Reihenfolge der deklarierten Argumente, damit
// zwei Läufe desselben Werkzeugs vergleichbar bleiben.
//
// Pfade werden dabei vom Werkzeug-Ordner auf den Bau umgerechnet. Das
// Skript läuft mit dem Bau als Arbeitsverzeichnis — dort ist
// `example/probe.txt` nichts, `tools/drafts/zeilen/example/probe.txt`
// dagegen die Datei, die der Schmied gemeint hat.
func (t Tool) BeispielArgv() []string {
	if t.Beispiel == nil {
		return nil
	}
	var argv []string
	for _, a := range t.Args {
		wert, da := t.Beispiel.Args[a.Name]
		if !da {
			continue
		}
		s := fmt.Sprint(wert)
		if strings.HasPrefix(s, ToolExampleDir+"/") {
			s = filepath.Join(t.Ordner, s)
		}
		argv = append(argv, "--"+a.Name, s)
	}
	return argv
}

// AktualisiereValIntent schreibt den abgeleiteten Zustand in die
// Dateien, wo der Eintrag veraltet ist. Zurück kommen die Namen der
// Werkzeuge, deren Zeile nachgezogen wurde.
//
// Gedacht ist das für `outdated`: der Eintrag steht dort nie von selbst,
// weil geschrieben nur bei review, test und release wird — und in jedem
// dieser Momente passt der Hash. Wer die Datei danach ändert, hinterlässt
// eine Zeile, die „actual" behauptet. In eine Datei zu schreiben, die
// ohnehin gerade verändert wurde, nimmt niemandem etwas weg.
//
// Angefasst wird ausschliesslich die valintent-Zeile. Der Hash bleibt,
// wie er ist — ihn neu zu berechnen hiesse, die fremde Änderung
// nachträglich zu segnen.
func AktualisiereValIntent(root string) ([]string, error) {
	werkzeuge, err := LadeTools(root)
	if err != nil {
		return nil, err
	}
	var nachgezogen []string
	for _, t := range werkzeuge {
		if t.Review.ValIntent == "" || Zustand(t.Review.ValIntent) == t.Zustand {
			continue
		}
		pfad := filepath.Join(root, t.Skript)
		roh, err := os.ReadFile(pfad)
		if err != nil {
			return nachgezogen, err
		}
		neu, geaendert := SetzeValIntent(roh, t.Zustand)
		if !geaendert {
			continue
		}
		info, err := os.Stat(pfad)
		if err != nil {
			return nachgezogen, err
		}
		if err := os.WriteFile(pfad, neu, info.Mode().Perm()); err != nil {
			return nachgezogen, err
		}
		nachgezogen = append(nachgezogen, t.Name)
	}
	return nachgezogen, nil
}
