// Package hase lädt Hasen-Templates aus hasen/*.md und generiert
// daraus opencode-Agenten pro Auftrag×Hase (PLAN.md §6). Das Template
// gehört Hasenbau, nicht opencode: Permissions kommen aus den Räumen
// des Auftrags; das Template kann sie nur weiter einschränken.
package hase

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/Moritz-Dorn/Hasenbau/internal/auftrag"
	"github.com/Moritz-Dorn/Hasenbau/internal/frontmatter"
	"gopkg.in/yaml.v3"
)

// hasenbauWissen erklärt einem Hasen das System, in dem er läuft. Der
// Text liegt im Binary statt im Bau: eine kopierte Datei driftet, sobald
// der Hasenbau sich ändert, und dann erzählt der Hase von einem System,
// das es so nicht mehr gibt. Eingebunden wird er über `kennt_hasenbau`.
//
//go:embed wissen/hasenbau.md
var hasenbauWissen string

// Template ist eine geparste Hasen-Definition aus hasen/<name>.md.
type Template struct {
	Name        string
	Description string
	Model       string
	Temperature *float64
	Denies      []Regel // zusätzliche Einschränkungen, ausschließlich deny
	Prompt      string

	// Wissen sind die Texte, die dem Prompt beigelegt werden — aus
	// `kennt_hasenbau` und `wissen:`. Lade füllt sie, damit Generiere
	// keine Dateien mehr anfassen muss.
	Wissen []WissenStueck
}

// WissenStueck ist ein beigelegter Text mit seiner Herkunft; die
// Herkunft steht als Überschrift im generierten Agenten, damit im Trace
// erkennbar bleibt, woher eine Anweisung kam.
type WissenStueck struct {
	Herkunft string
	Text     string
}

// Regel ist ein Permission-Eintrag (Aktion ist immer "deny" —
// alles andere lehnt Lade ab).
type Regel struct {
	Permission string
	Pattern    string
}

// tplFrontmatter: erlaubte Template-Felder. Alles andere (mode, tools,
// steps, …) entscheidet der Generator, nicht das Template.
type tplFrontmatter struct {
	Description   string    `yaml:"description"`
	Model         string    `yaml:"model"`
	Temperature   *float64  `yaml:"temperature"`
	Permission    yaml.Node `yaml:"permission"`
	KenntHasenbau bool      `yaml:"kennt_hasenbau"`
	Wissen        []string  `yaml:"wissen"`
}

// Lade liest das Template hasen/<name>.md unter root.
func Lade(root, name string) (*Template, error) {
	fehler := func(format string, args ...any) error {
		return fmt.Errorf("hase %s: %s", name, fmt.Sprintf(format, args...))
	}

	src, err := os.ReadFile(filepath.Join(root, "hasen", name+".md"))
	if err != nil {
		return nil, fehler("template lesen: %w", err)
	}
	kopf, body, err := frontmatter.Split(src)
	if err != nil {
		return nil, fehler("%v", err)
	}

	dec := yaml.NewDecoder(bytes.NewReader(kopf))
	dec.KnownFields(true)
	var fm tplFrontmatter
	if err := dec.Decode(&fm); err != nil && !errors.Is(err, io.EOF) { // EOF = leeres Frontmatter, alles optional
		return nil, fehler("frontmatter: %v (erlaubt: description, model, temperature, permission, kennt_hasenbau, wissen)", err)
	}

	t := &Template{
		Name:        name,
		Description: fm.Description,
		Model:       fm.Model,
		Temperature: fm.Temperature,
		Prompt:      strings.TrimSpace(body),
	}
	if t.Prompt == "" {
		return nil, fehler("prompt fehlt — der Markdown-Teil ist der System-Prompt")
	}

	t.Denies, err = parseDenies(&fm.Permission)
	if err != nil {
		return nil, fehler("%v", err)
	}
	t.Wissen, err = ladeWissen(root, fm.KenntHasenbau, fm.Wissen)
	if err != nil {
		return nil, fehler("%v", err)
	}
	return t, nil
}

// ladeWissen sammelt die beigelegten Texte: erst das mitgelieferte
// Wissen über den Hasenbau, dann die eigenen Dateien des Nutzers.
// Gelesen wird hier, nicht beim Generieren — so bleibt Generiere eine
// reine Funktion über das, was schon im Speicher steht.
func ladeWissen(root string, kenntHasenbau bool, muster []string) ([]WissenStueck, error) {
	var out []WissenStueck
	if kenntHasenbau {
		out = append(out, WissenStueck{Herkunft: "Der Hasenbau", Text: strings.TrimSpace(hasenbauWissen)})
	}
	for _, m := range muster {
		if err := auftrag.BauRelativ(m); err != nil {
			return nil, fmt.Errorf("wissen %q: %v", m, err)
		}
		treffer, err := filepath.Glob(filepath.Join(root, m))
		if err != nil {
			return nil, fmt.Errorf("wissen %q: %v", m, err)
		}
		if len(treffer) == 0 {
			return nil, fmt.Errorf("wissen %q: keine Datei gefunden", m)
		}
		sort.Strings(treffer)
		for _, datei := range treffer {
			roh, err := os.ReadFile(datei)
			if err != nil {
				return nil, fmt.Errorf("wissen: %v", err)
			}
			rel, err := filepath.Rel(root, datei)
			if err != nil {
				rel = datei
			}
			out = append(out, WissenStueck{Herkunft: rel, Text: strings.TrimSpace(string(roh))})
		}
	}
	return out, nil
}

// parseDenies akzeptiert `bash: deny` (skalar ⇒ Pattern "*") und
// `edit: {"pfad": deny}`. Jede andere Aktion ist ein Ladefehler:
// Templates dürfen Rechte nur einschränken, nie aufweiten (§6).
func parseDenies(node *yaml.Node) ([]Regel, error) {
	if node.IsZero() {
		return nil, nil
	}
	if node.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("permission muss ein Mapping sein")
	}
	var denies []Regel
	for i := 0; i+1 < len(node.Content); i += 2 {
		perm, wert := node.Content[i].Value, node.Content[i+1]
		switch wert.Kind {
		case yaml.ScalarNode:
			if wert.Value != "deny" {
				return nil, fmt.Errorf("permission %s: %q — Templates dürfen nur deny setzen, allow/ask kommen aus dem Auftrag", perm, wert.Value)
			}
			denies = append(denies, Regel{Permission: perm, Pattern: "*"})
		case yaml.MappingNode:
			for j := 0; j+1 < len(wert.Content); j += 2 {
				pattern, aktion := wert.Content[j].Value, wert.Content[j+1].Value
				if aktion != "deny" {
					return nil, fmt.Errorf("permission %s[%s]: %q — Templates dürfen nur deny setzen, allow/ask kommen aus dem Auftrag", perm, pattern, aktion)
				}
				denies = append(denies, Regel{Permission: perm, Pattern: pattern})
			}
		default:
			return nil, fmt.Errorf("permission %s: erwartet deny oder ein Pattern-Mapping", perm)
		}
	}
	return denies, nil
}

// AgentName ist der Name des generierten Agenten für einen Auftrag —
// unter diesem Namen promptet der Runner.
func AgentName(a *auftrag.Auftrag) string {
	return a.Name + "__" + a.Hase
}

// schreibRollen sind die Raum-Rollen, für die der Hase Schreibrecht
// bekommt (§6): sein Scratch und seine Ausgabe. `done` bedient der
// Runner über nachher:-Schritte, nicht der Hase.
var schreibRollen = []string{"work", "out"}

// grundDenies sind die Pauschal-Verbote jedes generierten Agenten.
// Reihenfolge = Ausgabe-Reihenfolge.
var grundDenies = []string{"bash", "webfetch", "websearch", "external_directory"}

// Generiere baut den opencode-Agenten für einen Auftrag. Die Ausgabe
// ist deterministisch: gleicher Input ⇒ gleiche Bytes.
func Generiere(a *auftrag.Auftrag, t *Template) ([]byte, error) {
	if a.Hase != t.Name {
		return nil, fmt.Errorf("auftrag %s verlangt Hase %q, Template heißt %q", a.Name, a.Hase, t.Name)
	}

	// edit-Regeln: alles verbieten, dann die Schreib-Räume des Auftrags
	// erlauben. Template-Denies kommen ans Ende — die letzte matchende
	// Regel gewinnt (§11.5), Denies können Allows also nur verengen.
	type eintrag struct{ pattern, aktion string }
	edit := []eintrag{{"*", "deny"}}
	for _, rolle := range schreibRollen {
		pfad, ok := a.Raeume[rolle]
		if !ok {
			continue
		}
		edit = append(edit, eintrag{strings.TrimSuffix(pfad, "/") + "/**", "allow"})
	}
	sonst := map[string][]eintrag{}
	var sonstReihenfolge []string
	for _, r := range t.Denies {
		if r.Permission == "edit" {
			// Gleiches Pattern wie ein Basis-Allow ⇒ Allow entfernen,
			// sonst entstünde ein doppelter YAML-Schlüssel.
			for i := len(edit) - 1; i >= 0; i-- {
				if edit[i].pattern == r.Pattern {
					edit = append(edit[:i], edit[i+1:]...)
				}
			}
			edit = append(edit, eintrag{r.Pattern, "deny"})
			continue
		}
		if _, ok := sonst[r.Permission]; !ok {
			sonstReihenfolge = append(sonstReihenfolge, r.Permission)
		}
		sonst[r.Permission] = append(sonst[r.Permission], eintrag{r.Pattern, "deny"})
	}

	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "# GENERIERT von hasenbau aus hasen/%s.md + auftraege/%s.md — nicht von Hand ändern.\n", t.Name, a.Name)
	beschreibung := t.Description
	if beschreibung == "" {
		beschreibung = fmt.Sprintf("Hase %s für Auftrag %s", t.Name, a.Name)
	}
	fmt.Fprintf(&b, "description: %q\n", beschreibung)
	b.WriteString("mode: primary\n")
	if t.Model != "" {
		fmt.Fprintf(&b, "model: %q\n", t.Model)
	}
	if t.Temperature != nil {
		fmt.Fprintf(&b, "temperature: %s\n", strconv.FormatFloat(*t.Temperature, 'g', -1, 64))
	}
	b.WriteString("permission:\n")
	b.WriteString("  edit:\n")
	for _, e := range edit {
		fmt.Fprintf(&b, "    %q: %s\n", e.pattern, e.aktion)
	}
	for _, perm := range grundDenies {
		// Ein Pauschal-Deny aus dem Template ist hier schon abgedeckt;
		// Pattern-Denies auf Grund-Permissions wären toter Code unter *.
		delete(sonst, perm)
		fmt.Fprintf(&b, "  %s: deny\n", perm)
	}
	for _, perm := range sonstReihenfolge {
		eintraege, ok := sonst[perm]
		if !ok {
			continue
		}
		fmt.Fprintf(&b, "  %s:\n", perm)
		for _, e := range eintraege {
			fmt.Fprintf(&b, "    %q: %s\n", e.pattern, e.aktion)
		}
	}
	b.WriteString("---\n")
	b.WriteString(t.Prompt)
	b.WriteString("\n")
	// Beigelegtes Wissen nach der Rolle: erst wer der Hase ist und was
	// er tun soll, dann das Nachschlagewerk. Die Herkunft steht als
	// Überschrift dabei — sonst ist im Trace nicht zu erkennen, woher
	// eine Anweisung kam.
	for _, w := range t.Wissen {
		fmt.Fprintf(&b, "\n## Wissen: %s\n\n%s\n", w.Herkunft, w.Text)
	}
	// Injektionspunkt: was das Framework jedem Hasen mitgibt,
	// unabhängig vom Template. Bisher nur der Rückkanal.
	b.WriteString(rueckkanalPrompt)
	return []byte(b.String()), nil
}

// rueckkanalPrompt hängt an jeden generierten Agenten. Ohne diesen
// Absatz ruft kein Hase die Werkzeuge auf, und die Summary bliebe für
// immer die geratene letzte Assistant-Message (PLAN.md §5, §8 Phase 2).
const rueckkanalPrompt = `
## Rückkanal

Melde am Ende deines Laufs mit ` + "`hasenbau_summary`" + ` in einer Zeile, was du
getan hast. Der nächste Lauf desselben Auftrags bekommt diese Zeile als
Kontext — schreib sie für dein künftiges Ich, nicht als Höflichkeit.
Sie ersetzt keine Ausgabe in deinen Raum.

Was dir unterwegs auffällt und später jemanden interessieren könnte,
aber nicht in die eine Zeile passt, gehört in ` + "`hasenbau_notiz`" + `.
`

// SchreibeAgent generiert und schreibt den Agenten in den Bau.
// Zurück kommt der Bau-relative Pfad der Datei.
func SchreibeAgent(root string, a *auftrag.Auftrag, t *Template) (string, error) {
	inhalt, err := Generiere(a, t)
	if err != nil {
		return "", err
	}
	rel := filepath.Join(".opencode-home", "opencode", "agents", AgentName(a)+".md")
	abs := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return "", fmt.Errorf("agent %s: %w", AgentName(a), err)
	}
	if err := os.WriteFile(abs, inhalt, 0o644); err != nil {
		return "", fmt.Errorf("agent %s schreiben: %w", AgentName(a), err)
	}
	return rel, nil
}
