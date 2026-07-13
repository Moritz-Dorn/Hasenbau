// Package auftrag parst Auftrags-Definitionen aus auftraege/*.md
// (PLAN.md §6): YAML-Frontmatter mit Trigger, Gängen, Hase und Räumen,
// darunter der Markdown-Body als Prompt-Kern.
package auftrag

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
	"gopkg.in/yaml.v3"
)

// Auftrag ist eine geparste, validierte Job-Definition.
type Auftrag struct {
	Name    string            // Dateiname ohne .md — wird Teil des generierten Agent-Namens (§6)
	Trigger Trigger
	Gaenge  []Gang
	Hase    string            // Name des Templates in hasen/
	CWD     string            // Bau-relativ; leer = Bau-Root
	Raeume  map[string]string // Rolle → Bau-relativer Pfad
	Kontext []Kontext
	Nachher []Nachher
	Body    string // Prompt-Kern
}

// Trigger ist genau eines von beiden: Datei-Watch oder Cron.
type Trigger struct {
	Watch    string        // Glob, Bau-relativ
	Cron     string        // Standard-Cron (5 Felder)
	Debounce time.Duration // nur bei Watch
}

// Gang ist ein deterministischer Vorverarbeitungs-Schritt.
type Gang struct {
	Name    string
	Run     string
	Timeout time.Duration // 0 = nicht gesetzt, Default entscheidet der Runner
}

// Kontext ist eine Prompt-Quelle: entweder eine Datei oder die letzten
// N Lauf-Summaries dieses Auftrags.
type Kontext struct {
	Datei           string
	LetzteSummaries int
}

// Nachher ist ein Aufräum-Schritt nach erfolgreichem Lauf. Die
// Ausführung (inkl. Variablen-Substitution) übernimmt der Runner.
type Nachher struct {
	Aktion string // "move", "copy" oder "delete"
	Von    string
	Nach   string // leer bei delete
}

// namensMuster gilt für Auftrags- und Hasen-Namen: beide landen im
// Dateinamen des generierten Agenten (<auftrag>__<hase>.md, §6).
var namensMuster = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

// dauer parst YAML-Strings wie "5s" oder "120s" nach time.Duration.
type dauer time.Duration

func (d *dauer) UnmarshalYAML(node *yaml.Node) error {
	var s string
	if err := node.Decode(&s); err != nil {
		return fmt.Errorf("dauer erwartet einen String wie \"5s\", Zeile %d", node.Line)
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("ungültige Dauer %q, Zeile %d", s, node.Line)
	}
	if v < 0 {
		return fmt.Errorf("negative Dauer %q, Zeile %d", s, node.Line)
	}
	*d = dauer(v)
	return nil
}

// frontmatter spiegelt das YAML aus §6. Pointer-Felder unterscheiden
// „fehlt" von „leer"; unbekannte Felder lehnt der Decoder ab.
type frontmatter struct {
	Trigger *struct {
		Watch    string `yaml:"watch"`
		Cron     string `yaml:"cron"`
		Debounce dauer  `yaml:"debounce"`
	} `yaml:"trigger"`
	Gaenge []struct {
		Name    string `yaml:"name"`
		Run     string `yaml:"run"`
		Timeout dauer  `yaml:"timeout"`
	} `yaml:"gaenge"`
	Hase    string            `yaml:"hase"`
	CWD     string            `yaml:"cwd"`
	Raeume  map[string]string `yaml:"raeume"`
	Kontext []struct {
		Datei           string `yaml:"datei"`
		LetzteSummaries *int   `yaml:"letzte_summaries"`
	} `yaml:"kontext"`
	Nachher []map[string]string `yaml:"nachher"`
}

// Parse zerlegt eine Auftrags-Datei in Frontmatter und Body und
// validiert beides. name ist der Dateiname ohne .md.
func Parse(name string, src []byte) (*Auftrag, error) {
	fehler := func(format string, args ...any) error {
		return fmt.Errorf("auftrag %s: %s", name, fmt.Sprintf(format, args...))
	}

	if !namensMuster.MatchString(name) {
		return nil, fehler("ungültiger Name (erlaubt: Buchstaben, Ziffern, . _ -)")
	}

	kopf, body, err := trenneFrontmatter(src)
	if err != nil {
		return nil, fehler("%v", err)
	}

	dec := yaml.NewDecoder(bytes.NewReader(kopf))
	dec.KnownFields(true)
	var fm frontmatter
	if err := dec.Decode(&fm); err != nil {
		return nil, fehler("frontmatter: %v", err)
	}

	a := &Auftrag{
		Name:   name,
		Hase:   fm.Hase,
		CWD:    fm.CWD,
		Raeume: fm.Raeume,
		Body:   strings.TrimSpace(body),
	}

	// Trigger: genau eines von watch oder cron.
	if fm.Trigger == nil {
		return nil, fehler("trigger fehlt (watch oder cron)")
	}
	a.Trigger = Trigger{
		Watch:    fm.Trigger.Watch,
		Cron:     fm.Trigger.Cron,
		Debounce: time.Duration(fm.Trigger.Debounce),
	}
	switch {
	case a.Trigger.Watch == "" && a.Trigger.Cron == "":
		return nil, fehler("trigger braucht genau eines von watch oder cron")
	case a.Trigger.Watch != "" && a.Trigger.Cron != "":
		return nil, fehler("trigger: watch und cron schließen sich aus")
	case a.Trigger.Cron != "":
		if _, err := cron.ParseStandard(a.Trigger.Cron); err != nil {
			return nil, fehler("ungültiger cron-Ausdruck %q: %v", a.Trigger.Cron, err)
		}
		if a.Trigger.Debounce != 0 {
			return nil, fehler("debounce gilt nur für watch-Trigger")
		}
	default:
		if err := bauRelativ(a.Trigger.Watch); err != nil {
			return nil, fehler("trigger.watch: %v", err)
		}
	}

	// Hase: Pflicht; der Name landet im generierten Agent-Dateinamen.
	if a.Hase == "" {
		return nil, fehler("hase fehlt")
	}
	if !namensMuster.MatchString(a.Hase) {
		return nil, fehler("ungültiger Hasen-Name %q (erlaubt: Buchstaben, Ziffern, . _ -)", a.Hase)
	}

	if a.CWD != "" {
		if err := bauRelativ(a.CWD); err != nil {
			return nil, fehler("cwd: %v", err)
		}
	}

	for rolle, pfad := range a.Raeume {
		if rolle == "" || pfad == "" {
			return nil, fehler("raeume: Rolle und Pfad dürfen nicht leer sein")
		}
		if err := bauRelativ(pfad); err != nil {
			return nil, fehler("raum %s: %v", rolle, err)
		}
	}

	gesehen := map[string]bool{}
	for i, g := range fm.Gaenge {
		if g.Name == "" {
			return nil, fehler("gang %d: name fehlt", i+1)
		}
		if g.Run == "" {
			return nil, fehler("gang %q: run fehlt", g.Name)
		}
		if gesehen[g.Name] {
			return nil, fehler("gang %q: Name doppelt", g.Name)
		}
		gesehen[g.Name] = true
		a.Gaenge = append(a.Gaenge, Gang{Name: g.Name, Run: g.Run, Timeout: time.Duration(g.Timeout)})
	}

	for i, k := range fm.Kontext {
		switch {
		case k.Datei != "" && k.LetzteSummaries != nil:
			return nil, fehler("kontext %d: datei und letzte_summaries schließen sich aus", i+1)
		case k.Datei == "" && k.LetzteSummaries == nil:
			return nil, fehler("kontext %d: braucht datei oder letzte_summaries", i+1)
		case k.LetzteSummaries != nil && *k.LetzteSummaries <= 0:
			return nil, fehler("kontext %d: letzte_summaries muss > 0 sein", i+1)
		case k.LetzteSummaries != nil:
			a.Kontext = append(a.Kontext, Kontext{LetzteSummaries: *k.LetzteSummaries})
		default:
			a.Kontext = append(a.Kontext, Kontext{Datei: k.Datei})
		}
	}

	for i, n := range fm.Nachher {
		schritt, err := parseNachher(n)
		if err != nil {
			return nil, fehler("nachher %d: %v", i+1, err)
		}
		a.Nachher = append(a.Nachher, schritt)
	}

	if a.Body == "" {
		return nil, fehler("body fehlt — der Markdown-Teil ist der Prompt-Kern")
	}
	return a, nil
}

// Load liest alle Aufträge unter <root>/auftraege/*.md und prüft, dass
// jeder referenzierte Hase ein Template unter <root>/hasen/ hat.
func Load(root string) ([]*Auftrag, error) {
	dateien, err := filepath.Glob(filepath.Join(root, "auftraege", "*.md"))
	if err != nil {
		return nil, fmt.Errorf("auftraege suchen: %w", err)
	}
	sort.Strings(dateien)

	var auftraege []*Auftrag
	for _, datei := range dateien {
		src, err := os.ReadFile(datei)
		if err != nil {
			return nil, fmt.Errorf("auftrag lesen: %w", err)
		}
		name := strings.TrimSuffix(filepath.Base(datei), ".md")
		a, err := Parse(name, src)
		if err != nil {
			return nil, err
		}
		vorlage := filepath.Join(root, "hasen", a.Hase+".md")
		if _, err := os.Stat(vorlage); err != nil {
			return nil, fmt.Errorf("auftrag %s: unbekannter Hase %q — kein Template hasen/%s.md", name, a.Hase, a.Hase)
		}
		auftraege = append(auftraege, a)
	}
	return auftraege, nil
}

// trenneFrontmatter erwartet "---" als erste Zeile und liefert den
// YAML-Block sowie den restlichen Body.
func trenneFrontmatter(src []byte) (kopf []byte, body string, err error) {
	const marke = "---"
	text := strings.ReplaceAll(string(src), "\r\n", "\n")
	zeilen := strings.SplitAfter(text, "\n")
	if len(zeilen) == 0 || strings.TrimRight(zeilen[0], "\n") != marke {
		return nil, "", fmt.Errorf("kein Frontmatter: Datei muss mit %q beginnen", marke)
	}
	for i := 1; i < len(zeilen); i++ {
		if strings.TrimRight(zeilen[i], "\n") == marke {
			return []byte(strings.Join(zeilen[1:i], "")), strings.Join(zeilen[i+1:], ""), nil
		}
	}
	return nil, "", fmt.Errorf("frontmatter nicht geschlossen: schließendes %q fehlt", marke)
}

// parseNachher zerlegt einen Schritt wie {move: "$INPUT -> raeume/archiv/"}.
// Pfade dürfen Variablen enthalten; substituiert wird erst im Runner.
func parseNachher(schritt map[string]string) (Nachher, error) {
	if len(schritt) != 1 {
		return Nachher{}, fmt.Errorf("genau eine Aktion pro Schritt (move, copy oder delete)")
	}
	for aktion, wert := range schritt {
		switch aktion {
		case "move", "copy":
			von, nach, ok := strings.Cut(wert, "->")
			von, nach = strings.TrimSpace(von), strings.TrimSpace(nach)
			if !ok || von == "" || nach == "" {
				return Nachher{}, fmt.Errorf("%s braucht die Form \"VON -> NACH\", bekam %q", aktion, wert)
			}
			return Nachher{Aktion: aktion, Von: von, Nach: nach}, nil
		case "delete":
			if strings.TrimSpace(wert) == "" {
				return Nachher{}, fmt.Errorf("delete braucht einen Pfad")
			}
			return Nachher{Aktion: aktion, Von: strings.TrimSpace(wert)}, nil
		default:
			return Nachher{}, fmt.Errorf("unbekannte Aktion %q (erlaubt: move, copy, delete)", aktion)
		}
	}
	return Nachher{}, fmt.Errorf("leerer Schritt")
}

// bauRelativ erzwingt die Isolations-Invariante aus §3: Pfade in
// Aufträgen bleiben im Bau — relativ, ohne Ausbruch nach oben.
func bauRelativ(p string) error {
	if filepath.IsAbs(p) {
		return fmt.Errorf("pfad %q muss Bau-relativ sein, nicht absolut", p)
	}
	for _, teil := range strings.Split(filepath.ToSlash(p), "/") {
		if teil == ".." {
			return fmt.Errorf("pfad %q darf den Bau nicht verlassen (..)", p)
		}
	}
	return nil
}
