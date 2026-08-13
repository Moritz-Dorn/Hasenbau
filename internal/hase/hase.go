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

// hasenbauKnowledge erklärt einem Hasen das System, in dem er läuft. Der
// Text liegt im Binary statt im Bau: eine kopierte Datei driftet, sobald
// der Hasenbau sich ändert, und dann erzählt der Hase von einem System,
// das es so nicht mehr gibt. Eingebunden wird er über `kennt_hasenbau`.
//
//go:embed wissen/hasenbau.md
var hasenbauKnowledge string

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
	Knowledge []Knowledge
}

// Knowledge ist ein beigelegter Text mit seiner Herkunft; die
// Herkunft steht als Überschrift im generierten Agenten, damit im Trace
// erkennbar bleibt, woher eine Anweisung kam.
type Knowledge struct {
	Origin string
	Text   string
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
	KnowsHasenbau bool      `yaml:"knows_hasenbau"`
	Wissen        []string  `yaml:"knowledge"`
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
		return nil, fehler("frontmatter: %v (erlaubt: description, model, temperature, permission, knows_hasenbau, knowledge)", err)
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
	t.Knowledge, err = loadKnowledge(root, fm.KnowsHasenbau, fm.Wissen)
	if err != nil {
		return nil, fehler("%v", err)
	}
	return t, nil
}

// loadKnowledge sammelt die beigelegten Texte: erst das mitgelieferte
// Wissen über den Hasenbau, dann die eigenen Dateien des Nutzers.
// Gelesen wird hier, nicht beim Generieren — so bleibt Generiere eine
// reine Funktion über das, was schon im Speicher steht.
func loadKnowledge(root string, knowsHasenbau bool, muster []string) ([]Knowledge, error) {
	var out []Knowledge
	if knowsHasenbau {
		out = append(out, Knowledge{Origin: "Der Hasenbau", Text: strings.TrimSpace(hasenbauKnowledge)})
	}
	for _, m := range muster {
		if err := auftrag.BauRelative(m); err != nil {
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
			out = append(out, Knowledge{Origin: rel, Text: strings.TrimSpace(string(roh))})
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
// Runner über nachher:-Steps, nicht der Hase.
var schreibRollen = []string{"work", "out"}

// grundDenies sind die Pauschal-Verbote jedes generierten Agenten.
// Reihenfolge = Ausgabe-Reihenfolge.
//
//	bash               Ausführung. Der Hase arbeitet über seine Räume.
//	webfetch           Netz. Was er braucht, legt ihm ein Gang hin.
//	websearch          wie webfetch.
//	external_directory Alles außerhalb des Baus.
//	task               Subagenten starten. Das Loch aus Hasenbau-wiu: ein
//	                   Subagent ist ein eigener Agent und erbt weder die
//	                   Permissions noch die Raum-Grenzen.
//	question           Rückfrage an einen Menschen. Ein Lauf im Daemon
//	                   hat keinen, der antwortet.
//
// Hier stand bis 2026-08-12 nur die Hälfte davon, und task und question
// kamen über ein zweites Feld — `tools: {name: false}` — dazu. Die
// Begründung lautete, ein Deny lasse das Werkzeug in der Liste des
// Modells stehen und lehne erst den Aufruf ab, ein Hase sehe also, was
// er nicht darf, und suche einen Weg drumherum.
//
// Diese Begründung ist gemessen falsch (Hasenbau-8fd, opencode
// 1.15.13). Beide Felder ENTZIEHEN das Werkzeug: ein Hase mit
// `bash: deny` zählt seine Werkzeuge auf und bash ist nicht darunter —
// während ein Werkzeug ohne jede Regel in derselben Liste steht. Das
// ist die Gegenprobe, und sie stammt aus einem einzigen Lauf, damit
// Modell und Session nicht variieren. Dazu die opencode-Doku, die
// `tools` ausdrücklich als deprecated führt („prefer the agent's
// permission field") und `false` als `{"*": "deny"}` übersetzt.
//
// Deshalb jetzt ein Mechanismus statt zweier. Was NICHT hier steht,
// bekommt der Hase weiterhin — auch die Werkzeuge des Rückkanals, die
// opencode erst zur Laufzeit registriert. Es bleibt eine Ausschluss-
// und keine Einschlussliste: eine Whitelist müsste die MCP-Namen kennen
// und nähme dem Hasen sonst still seine Meldewege. Dass die Liste
// vollständig ist, sichert ein Test gegen den echten Server
// (hase_integration_test.go) — kommt in opencode ein Werkzeug dazu,
// wird er rot.
//
// Die Grenze, die unabhängig von diesem Feld hält, ist der
// Sandbox-Wächter im Bau-Plugin: er sitzt im Server-Prozess und meldet,
// wenn ein Entzug irgendwann nicht mehr greift (Hasenbau-d2p).
var grundDenies = []string{"bash", "webfetch", "websearch", "external_directory", "task", "question"}

// Optionen sind die Umstände des Baus, die in den generierten Agenten
// einfließen — alles, was weder im Auftrag noch im Template steht. Der
// Nullwert ist der karge Fall: nichts zusätzlich angeboten.
type Optionen struct {
	// Tools sind die Namen ALLER Schmied-Werkzeuge, die im Bau liegen —
	// nicht die des Auftrags. Der Generator braucht die Gesamtliste,
	// weil die Freigabe je Auftrag über ein Verbot der übrigen
	// entsteht: ein plugin-registriertes Werkzeug steht in keiner
	// Grund-Verbotsliste und wäre sonst für jeden Hasen sichtbar
	// (gemessen 2026-08-12, Hasenbau-hcs).
	//
	// Damit ist die Liste hier so wichtig wie das `tools:` des
	// Auftrags: fehlt ein Werkzeug darin, bekommt es jeder Hase.
	Tools []string

	// ToolsBereit ist die Teilmenge von Tools, die ein Auftrag wirksam
	// freigeben kann: gelesen, unverändert seit dem Review und im
	// Probelauf gezeigt (ValIntent `actual`, Hasenbau-9w6). Was ein
	// Auftrag nennt, aber hier fehlt, bleibt verboten — ein Werkzeug,
	// das niemand gelesen hat, bekommt kein Hase, auch wenn es im
	// Auftrag steht.
	ToolsBereit []string

	// ToolRequests sagt, ob der Bau einen `requests:`-Raum hat. Nur
	// dann bietet der Rückkanal `hasenbau_tool_request` überhaupt an
	// (cmd/hasenbau: wunschRaum aus cfg.Requests), und nur dann darf
	// der Prompt den Hasen darauf verweisen.
	//
	// Ein Verweis auf ein Werkzeug, das es nicht gibt, ist an dieser
	// Stelle schlimmer als keiner: der Absatz ist genau die Stelle, die
	// einem Hasen den LEGALEN Weg zeigen soll, wenn ihm etwas fehlt.
	// Zeigt er ins Leere, steht der Hase in der Lage aus Hasenbau-wiu —
	// Aufgabe unlösbar, kein angebotener Ausweg — und dort nahm er den
	// Umweg über einen Subagenten (Hasenbau-2lq).
	ToolRequests bool
}

// Generiere baut den opencode-Agenten für einen Auftrag. Die Ausgabe
// ist deterministisch: gleicher Input ⇒ gleiche Bytes.
func Generiere(a *auftrag.Auftrag, t *Template, o Optionen) ([]byte, error) {
	if a.Hase != t.Name {
		return nil, fmt.Errorf("auftrag %s verlangt Hase %q, Template heißt %q", a.Name, a.Hase, t.Name)
	}

	// Freigabe je Auftrag: das Plugin registriert seine Werkzeuge beim
	// Server, sichtbar sind sie damit zunächst für JEDEN Agenten. Wer
	// hier nicht genannt ist, wird deshalb ausdrücklich verboten.
	//
	// Ein genanntes Werkzeug, das es nicht gibt, ist ein Ladefehler und
	// kein stiller Verzicht: sonst merkt man den Tippfehler erst an
	// einem Lauf, in dem der Hase behauptet, er habe das Werkzeug
	// nicht — und das sieht aus wie ein Modellfehler.
	bereit := map[string]bool{}
	for _, name := range o.ToolsBereit {
		bereit[name] = true
	}
	freigegeben := map[string]bool{}
	for _, name := range a.Tools {
		freigegeben[name] = true
	}
	var gesperrteTools []string
	for _, name := range o.Tools {
		// Verboten wird alles, was der Auftrag nicht nennt — und
		// zusätzlich, was er zwar nennt, was aber nicht einsatzbereit
		// ist. Ein Werkzeug, das seit dem Review geändert wurde, ist
		// nicht gelesen worden; dass ein Auftrag es einmal freigegeben
		// hat, ändert daran nichts.
		if !freigegeben[name] || !bereit[name] {
			gesperrteTools = append(gesperrteTools, name)
		}
		delete(freigegeben, name)
	}
	if len(freigegeben) > 0 {
		fehlend := make([]string, 0, len(freigegeben))
		for name := range freigegeben {
			fehlend = append(fehlend, name)
		}
		sort.Strings(fehlend)
		return nil, fmt.Errorf("auftrag %s: tools: %s — kein solches Werkzeug im Bau (tools/<name>.json)",
			a.Name, strings.Join(fehlend, ", "))
	}
	sort.Strings(gesperrteTools)

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
	for _, name := range gesperrteTools {
		delete(sonst, name)
		fmt.Fprintf(&b, "  %s: deny\n", name)
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
	// Der Rückkanal steht zweimal: kurz hier oben, ausführlich ganz
	// unten. Siehe rueckkanalKopf.
	b.WriteString(rueckkanalKopf)
	b.WriteString(t.Prompt)
	b.WriteString("\n")
	// Beigelegtes Wissen nach der Role: erst wer der Hase ist und was
	// er tun soll, dann das Nachschlagewerk. Die Herkunft steht als
	// Überschrift dabei — sonst ist im Trace nicht zu erkennen, woher
	// eine Anweisung kam.
	for _, w := range t.Knowledge {
		fmt.Fprintf(&b, "\n## Wissen: %s\n\n%s\n", w.Origin, w.Text)
	}
	// Injektionspunkt: was das Framework jedem Hasen mitgibt,
	// unabhängig vom Template. Bisher nur der Rückkanal.
	b.WriteString(rueckkanalPrompt)
	if o.ToolRequests {
		b.WriteString(toolRequestPrompt)
	}
	return []byte(b.String()), nil
}

// rueckkanalKopf und rueckkanalPrompt sind dieselbe Anweisung, einmal
// kurz am Anfang und einmal ausführlich am Ende des generierten
// Agenten.
//
// Die Doppelung ist kein Versehen. Ein Hase mit langem Template, dem
// Hasenbau-Wissen und einem großen Kontext hat die Anweisung sonst
// irgendwo in der Mitte stehen — und genau dort geht sie verloren.
// Beobachtet an zwei Läufen desselben Auftrags mit demselben Modell:
// einer rief `hasenbau_summary` auf, der andere schrieb die Meldung
// als Fließtext in seine Antwort (Hasenbau-ifg). Beide Fassungen sagen
// deshalb dasselbe in einem Satz: der Aufruf ist die Abschlusshandlung,
// kein Text ersetzt ihn.
//
// Ohne diesen Absatz ruft kein Hase die Werkzeuge auf, und die Summary
// bliebe für immer die geratene letzte Assistant-Message (PLAN.md §5,
// §8 Phase 2).
const rueckkanalKopf = `**Dein Lauf endet mit einem Werkzeug-Aufruf, nicht mit einem Satz:**
` + "`hasenbau_summary`" + ` meldet in einer Zeile, was du getan hast. Kein Text in
deiner Antwort ersetzt ihn — was du nicht über das Werkzeug meldest,
kommt nicht an. Das Genauere steht unten unter „Rückkanal".

`

const rueckkanalPrompt = `
## Rückkanal

Der Lauf gilt als abgeschlossen, wenn du ` + "`hasenbau_summary`" + ` aufgerufen
hast — das Werkzeug, nicht eine Zeile Text darüber. Gemeldet wird in
einer Zeile, was du getan hast. Der nächste Lauf desselben Auftrags
bekommt diese Zeile als Kontext; schreib sie für dein künftiges Ich,
nicht als Höflichkeit. Sie ersetzt keine Ausgabe in deinen Raum.

Was dir unterwegs auffällt und später jemanden interessieren könnte,
aber nicht in die eine Zeile passt, gehört in ` + "`hasenbau_notiz`" + `.
Auch das ist ein Aufruf, keine Überschrift in deiner Antwort.
`

// toolRequestPrompt kommt nur dazu, wenn der Bau einen `requests:`-Raum
// hat — sonst gibt es das Werkzeug nicht (Optionen.ToolRequests).
const toolRequestPrompt = `
Fehlt dir ein **Werkzeug**, um deine Aufgabe zu lösen — etwa weil sie
ohne Ausführung nicht geht —, dann fordere eines an:
` + "`hasenbau_tool_request`" + ` mit Zweck, Eingabe und Ausgabe. Es wird
geprüft und gebaut; in diesem Lauf bekommst du es nicht mehr. Bau dir
nie selbst einen Weg an deinen Grenzen vorbei — was du dort findest,
ist ein Loch und kein Werkzeug.
`

// SchreibeAgent generiert und schreibt den Agenten in den Bau.
// Zurück kommt der Bau-relative Pfad der Datei.
func SchreibeAgent(root string, a *auftrag.Auftrag, t *Template, o Optionen) (string, error) {
	inhalt, err := Generiere(a, t, o)
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
