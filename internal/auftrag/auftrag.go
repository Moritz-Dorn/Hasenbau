// Package auftrag parst Auftrags-Definitionen aus auftraege/*.md
// (PLAN.md §6): YAML-Frontmatter mit Trigger, Gängen, Hase und Räumen,
// darunter der Markdown-Body als Prompt-Kern.
package auftrag

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Moritz-Dorn/Hasenbau/internal/frontmatter"
	"github.com/robfig/cron/v3"
	"gopkg.in/yaml.v3"
)

// Auftrag ist eine geparste, validierte Job-Definition.
type Auftrag struct {
	Name    string // Dateiname ohne .md — wird Teil des generierten Agent-Namens (§6)
	Trigger Trigger
	Gaenge  []Gang
	Hase    string            // Name des Templates in hasen/
	Raeume  map[string]string // Rolle → Bau-relativer Pfad

	// HaseTimeout begrenzt den LLM-Schritt dieses Auftrags; 0 = der
	// Vorgabewert des Runners. Ein Wert pro Auftrag, weil ein einziger
	// nicht beides sein kann: für einen Einsortier-Lauf sind 30 Minuten
	// großzügig, ein Baumeister auf einem großen Trace braucht mehr
	// (gemessen: 12m und >30m für dasselbe Material, Hasenbau-uh0).
	HaseTimeout time.Duration

	Context []Context
	After   []After
	Body    string // Prompt-Kern
}

// Trigger-Arten (§6). Genau eine gilt pro Auftrag.
const (
	TriggerWatch  = "watch"
	TriggerCron   = "cron"
	TriggerManual = "manual"
)

// Trigger ist genau eines von dreien: Datei-Watch, Cron oder manuell.
type Trigger struct {
	Watch    string        // Glob, Bau-relativ
	Cron     string        // Standard-Cron (5 Felder)
	Manual   bool          // läuft nur auf Zuruf (hasenbau lauf / hasenbau baumeister)
	Debounce time.Duration // nur bei Watch
}

// Art nennt die Trigger-Art des Auftrags. Nicht zu verwechseln mit dem
// Trigger einer laeufe-Zeile (§5): ein watch-Auftrag, den `hasenbau
// lauf` startet, läuft als DB-Trigger 'manuell' — seine Art bleibt
// 'watch', und daran hängt die Pfad-Semantik von $INPUT.
func (t Trigger) Kind() string {
	switch {
	case t.Cron != "":
		return TriggerCron
	case t.Manual:
		return TriggerManual
	default:
		return TriggerWatch
	}
}

// Gang ist ein deterministischer Vorverarbeitungs-Schritt.
type Gang struct {
	Name    string
	Run     string
	Timeout time.Duration // 0 = nicht gesetzt, Default entscheidet der Runner
}

// Kontext ist eine Prompt-Quelle: entweder eine Datei oder die letzten
// N Lauf-Summaries dieses Auftrags.
type Context struct {
	File          string
	LastSummaries int
}

// Nachher ist ein Aufräum-Schritt nach erfolgreichem Lauf. Die
// Ausführung (inkl. Variablen-Substitution) übernimmt der Runner.
type After struct {
	Action string // "move", "copy" oder "delete"
	From   string
	To     string // leer bei delete
}

// namePattern gilt für Auftrags- und Hasen-Namen: beide landen im
// Dateinamen des generierten Agenten (<auftrag>__<hase>.md, §6).
var namePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

// ValidName prüft einen Auftrags- oder Hasen-Namen. Beide landen im
// Dateinamen des generierten Agenten (<auftrag>__<hase>.md, §6),
// deshalb dieselbe Regel — und deshalb exportiert: `hasenbau new`
// prüft, bevor es eine Datei anlegt.
func ValidName(name string) error {
	if !namePattern.MatchString(name) {
		return fmt.Errorf("ungültiger Name %q (erlaubt: Buchstaben, Ziffern, . _ -)", name)
	}
	return nil
}

// dauer parst YAML-Strings wie "5s" oder "120s" nach time.Duration.
type duration time.Duration

func (d *duration) UnmarshalYAML(node *yaml.Node) error {
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
	*d = duration(v)
	return nil
}

// kopfdaten spiegelt das Frontmatter-YAML aus §6. Pointer-Felder
// unterscheiden „fehlt" von „leer"; Unbekanntes lehnt der Decoder ab.
type header struct {
	Trigger *struct {
		Watch    string   `yaml:"watch"`
		Cron     string   `yaml:"cron"`
		Manual   bool     `yaml:"manual"`
		Debounce duration `yaml:"debounce"`
	} `yaml:"trigger"`
	Gaenge []struct {
		Name    string   `yaml:"name"`
		Run     string   `yaml:"run"`
		Timeout duration `yaml:"timeout"`
	} `yaml:"gaenge"`
	Hase        string            `yaml:"hase"`
	HaseTimeout *duration         `yaml:"hase_timeout"`
	CWD         string            `yaml:"cwd"` // abgelehnt — bleibt im Schema für die klare Fehlermeldung
	Raeume      map[string]string `yaml:"raeume"`
	Context     []struct {
		File          string `yaml:"file"`
		LastSummaries *int   `yaml:"last_summaries"`
	} `yaml:"context"`
	After []map[string]string `yaml:"after"`
}

// Parse zerlegt eine Auftrags-Datei in Frontmatter und Body und
// validiert beides. name ist der Dateiname ohne .md.
func Parse(name string, src []byte) (*Auftrag, error) {
	fehler := func(format string, args ...any) error {
		return fmt.Errorf("auftrag %s: %s", name, fmt.Sprintf(format, args...))
	}

	if err := ValidName(name); err != nil {
		return nil, fehler("%v", err)
	}

	kopf, body, err := frontmatter.Split(src)
	if err != nil {
		return nil, fehler("%v", err)
	}

	dec := yaml.NewDecoder(bytes.NewReader(kopf))
	dec.KnownFields(true)
	var fm header
	if err := dec.Decode(&fm); err != nil && !errors.Is(err, io.EOF) { // EOF = leer; die Pflichtfeld-Prüfungen melden das präziser
		return nil, fehler("frontmatter: %v", err)
	}

	a := &Auftrag{
		Name:   name,
		Hase:   fm.Hase,
		Raeume: fm.Raeume,
		Body:   strings.TrimSpace(body),
	}

	// Trigger: genau eines von watch, cron oder manuell.
	if fm.Trigger == nil {
		return nil, fehler("trigger fehlt (watch, cron oder manuell)")
	}
	a.Trigger = Trigger{
		Watch:    fm.Trigger.Watch,
		Cron:     fm.Trigger.Cron,
		Manual:   fm.Trigger.Manual,
		Debounce: time.Duration(fm.Trigger.Debounce),
	}
	gesetzt := 0
	for _, an := range []bool{a.Trigger.Watch != "", a.Trigger.Cron != "", a.Trigger.Manual} {
		if an {
			gesetzt++
		}
	}
	switch {
	case gesetzt == 0:
		return nil, fehler("trigger braucht genau eines von watch, cron oder manuell")
	case gesetzt > 1:
		return nil, fehler("trigger: watch, cron und manuell schließen sich aus")
	case a.Trigger.Manual:
		if a.Trigger.Debounce != 0 {
			return nil, fehler("debounce gilt nur für watch-Trigger")
		}
	case a.Trigger.Cron != "":
		if _, err := cron.ParseStandard(a.Trigger.Cron); err != nil {
			return nil, fehler("ungültiger cron-Ausdruck %q: %v", a.Trigger.Cron, err)
		}
		if a.Trigger.Debounce != 0 {
			return nil, fehler("debounce gilt nur für watch-Trigger")
		}
	default:
		if err := BauRelative(a.Trigger.Watch); err != nil {
			return nil, fehler("trigger.watch: %v", err)
		}
	}

	// Hase: Pflicht; der Name landet im generierten Agent-Dateinamen.
	if a.Hase == "" {
		return nil, fehler("hase fehlt")
	}
	if err := ValidName(a.Hase); err != nil {
		return nil, fehler("hase: %v", err)
	}
	// „kein Zeitlimit" gibt es nicht: ein Lauf darf lange dauern, aber
	// nie für immer hängen. Wer 0s schreibt, meint vermutlich genau das
	// — deshalb ein Fehler statt eines stillen Rückfalls auf die Vorgabe.
	if fm.HaseTimeout != nil {
		if *fm.HaseTimeout == 0 {
			return nil, fehler("hase_timeout: 0 ist kein Zeitlimit — Feld weglassen für die Vorgabe des Runners")
		}
		a.HaseTimeout = time.Duration(*fm.HaseTimeout)
	}

	// Sessions ankern immer am Bau-Root: Räume dürfen eigene Git-Repos
	// sein, und ein CWD in einem Raum verschöbe den Worktree-Anker der
	// Permissions dorthin (§4, §11.5). Deshalb ist cwd: kein stilles
	// No-op, sondern ein Ladefehler.
	if fm.CWD != "" {
		return nil, fehler("cwd wird nicht unterstützt — Sessions ankern immer am Bau-Root (PLAN.md §4)")
	}

	for rolle, pfad := range a.Raeume {
		if rolle == "" || pfad == "" {
			return nil, fehler("raeume: Rolle und Pfad dürfen nicht leer sein")
		}
		if err := BauRelative(pfad); err != nil {
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

	for i, k := range fm.Context {
		switch {
		case k.File != "" && k.LastSummaries != nil:
			return nil, fehler("kontext %d: file und last_summaries schließen sich aus", i+1)
		case k.File == "" && k.LastSummaries == nil:
			return nil, fehler("kontext %d: braucht file oder last_summaries", i+1)
		case k.LastSummaries != nil && *k.LastSummaries <= 0:
			return nil, fehler("kontext %d: letzte_summaries muss > 0 sein", i+1)
		case k.LastSummaries != nil:
			a.Context = append(a.Context, Context{LastSummaries: *k.LastSummaries})
		default:
			a.Context = append(a.Context, Context{File: k.File})
		}
	}

	for i, n := range fm.After {
		schritt, err := parseAfter(n)
		if err != nil {
			return nil, fehler("nachher %d: %v", i+1, err)
		}
		a.After = append(a.After, schritt)
	}

	// $INPUT eines manuell-Auftrags ist ein freies Argument von der
	// Kommandozeile, kein Pfad — wer es als Datei liest oder verschiebt,
	// arbeitet auf einer Datei, die es nie gab. Lieber hier ablehnen als
	// mitten im Lauf danebengreifen.
	if a.Trigger.Manual {
		for i, k := range a.Context {
			if containsInput(k.File) {
				return nil, fehler("kontext %d: $INPUT ist bei manuell-Triggern kein Pfad, sondern das übergebene Argument", i+1)
			}
		}
		for i, n := range a.After {
			if containsInput(n.From) || containsInput(n.To) {
				return nil, fehler("nachher %d (%s): $INPUT ist bei manuell-Triggern kein Pfad, sondern das übergebene Argument", i+1, n.Action)
			}
		}
	}

	if a.Body == "" {
		return nil, fehler("body fehlt — der Markdown-Teil ist der Prompt-Kern")
	}
	return a, nil
}

// inputPattern trifft $INPUT, aber nicht $INPUTS o.ä. — dieselbe
// Wortgrenze, die lauf.Ersetze zieht.
var inputPattern = regexp.MustCompile(`\$INPUT([^A-Za-z0-9_]|$)`)

func containsInput(s string) bool { return inputPattern.MatchString(s) }

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

// parseAfter zerlegt einen Schritt wie {move: "$INPUT -> raeume/archiv/"}.
// Pfade dürfen Variablen enthalten; substituiert wird erst im Runner.
func parseAfter(schritt map[string]string) (After, error) {
	if len(schritt) != 1 {
		return After{}, fmt.Errorf("genau eine Aktion pro Schritt (move, copy oder delete)")
	}
	for aktion, wert := range schritt {
		switch aktion {
		case "move", "copy":
			von, nach, ok := strings.Cut(wert, "->")
			von, nach = strings.TrimSpace(von), strings.TrimSpace(nach)
			if !ok || von == "" || nach == "" {
				return After{}, fmt.Errorf("%s braucht die Form \"VON -> NACH\", bekam %q", aktion, wert)
			}
			return After{Action: aktion, From: von, To: nach}, nil
		case "delete":
			if strings.TrimSpace(wert) == "" {
				return After{}, fmt.Errorf("delete braucht einen Pfad")
			}
			return After{Action: aktion, From: strings.TrimSpace(wert)}, nil
		default:
			return After{}, fmt.Errorf("unbekannte Aktion %q (erlaubt: move, copy, delete)", aktion)
		}
	}
	return After{}, fmt.Errorf("leerer Schritt")
}

// BauRelative erzwingt die Isolations-Invariante aus §3: Pfade in
// Aufträgen bleiben im Bau — relativ, ohne Ausbruch nach oben.
func BauRelative(p string) error {
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
