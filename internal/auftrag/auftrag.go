// Package auftrag parst Auftrags-Definitionen aus auftraege/*.md
// (PLAN.md §6): YAML-Frontmatter mit Trigger, Gängen, Hase und Räumen,
// darunter der Markdown-Body als Prompt-Kern.
package auftrag

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Moritz-Dorn/Hasenbau/internal/frontmatter"
	"github.com/bmatcuk/doublestar/v4"
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

	// Throttle deckelt, wie oft dieser Auftrag laufen darf. Der
	// Nullwert heißt „ungedrosselt".
	Throttle Throttle

	// Monitored schaltet die routinemäßige MELDUNG ein, nicht die
	// Erfassung: aufgezeichnet und analysierbar ist immer alles, und
	// `hasenbau findings <auftrag>` rechnet über jeden Auftrag. Wer das
	// Feld setzt, sagt nur, dass er die Befunde dieses Auftrags
	// ungefragt sehen will — in `hasenbau status` (Hasenbau-4cx.3).
	Monitored bool

	// Tools sind die Namen der Schmied-Werkzeuge, die der Hase in
	// DIESEM Auftrag rufen darf (Hasenbau-hcs). Leer heißt keine — die
	// Freigabe ist eine Einschluss- und keine Ausschlussliste, anders
	// als die Grund-Verbote des generierten Agenten.
	//
	// Der Unterschied hat einen Grund: die Grund-Verbote zählen auf, was
	// es an opencode-Werkzeugen gibt und was davon wegfällt; die
	// Schmied-Werkzeuge entstehen dagegen laufend neu, und ein neues
	// soll nicht dadurch bei jedem Hasen landen, dass es niemand
	// verboten hat. Ein Werkzeug darf existieren, ohne dass jeder es
	// sieht — deshalb ist die Freigabe zweistufig: ein Mensch verschiebt
	// die Datei nach tools/, und ein Auftrag nennt sie hier.
	Tools []string

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
	// Watch ist das Muster ALLEIN, relativ zum input-Raum des Auftrags —
	// nicht der ganze Pfad. Der Eingang steht genau einmal im Auftrag,
	// nämlich unter raeume: input:, und WatchGlob setzt beides zusammen
	// (Hasenbau-d6d).
	Watch    string
	Cron     string        // Standard-Cron (5 Felder)
	Manual   bool          // läuft nur auf Zuruf (hasenbau lauf / hasenbau baumeister)
	Debounce time.Duration // nur bei Watch
}

// Art nennt die Trigger-Art des Auftrags. Nicht zu verwechseln mit dem
// Trigger einer laeufe-Zeile (§5): ein watch-Auftrag, den `hasenbau
// lauf` startet, läuft als DB-Trigger 'manuell' — seine Art bleibt
// 'watch', und daran hängt, welche Auslöser-Variable gebunden ist.
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

// RolleInput ist die Raum-Rolle, aus der ein watch-Auftrag seinen
// Eingang bezieht. Sie ist bei watch Pflicht (Parse besteht darauf) und
// bei den anderen Trigger-Arten frei — dort ist sie der Suchraum, den
// Gänge über $RAUM_input ansprechen.
const RolleInput = "input"

// WatchGlob ist der Pfad, den der Watcher tatsächlich beobachtet:
// input-Raum plus Muster. Leer, wenn der Auftrag kein watch-Trigger ist.
//
// Nur hier werden die beiden Hälften zusammengesetzt — Watcher, `get`
// und `findings` fragen diese Methode, statt selbst zu joinen. Sonst
// stünde die Regel an vier Stellen und liefe auseinander.
func (a *Auftrag) WatchGlob() string {
	if a.Trigger.Watch == "" {
		return ""
	}
	return filepath.Join(a.Raeume[RolleInput], a.Trigger.Watch)
}

// WatchWurzel ist das Verzeichnis, unter dem der Watcher sucht: der
// input-Raum, Bau-relativ. Er steht fest, seit `watch:` nur noch das
// Muster trägt (Hasenbau-d6d) — deshalb braucht es kein Zerlegen des
// Musters, um die Wurzel zu finden, und ein Verzeichnis namens `*`
// bringt niemanden mehr durcheinander (Hasenbau-h64).
func (a *Auftrag) WatchWurzel() string {
	if a.Trigger.Watch == "" {
		return ""
	}
	return a.Raeume[RolleInput]
}

// WatchTrifft sagt, ob ein Bau-relativer Pfad diesen Trigger auslöst.
//
// Gematcht wird gegen das Muster, nicht gegen den zusammengesetzten
// Pfad: nur so bedeutet `**` „unter dem input-Raum" und nicht „irgendwo
// im Bau". Der Doppelstern steht dabei für NULL oder mehr
// Verzeichnisse — `**/*.pdf` trifft also auch eine PDF, die direkt im
// Eingang liegt.
func (a *Auftrag) WatchTrifft(rel string) bool {
	wurzel := a.WatchWurzel()
	if wurzel == "" {
		return false
	}
	imRaum, err := filepath.Rel(wurzel, rel)
	if err != nil || imRaum == ".." || strings.HasPrefix(imRaum, ".."+string(filepath.Separator)) {
		return false
	}
	passt, err := doublestar.Match(filepath.ToSlash(a.Trigger.Watch), filepath.ToSlash(imRaum))
	return err == nil && passt
}

// WatchTreffer listet, was unter `unter` liegt und den Trigger auslösen
// würde — Bau-relativ und sortiert. `unter` ist Bau-relativ; leer heißt
// der ganze input-Raum.
//
// Diese Funktion gibt es, weil `filepath.Glob` den Doppelstern nicht
// kennt und ihn als einfachen Stern liest: `describe auftrag` zählte
// damit stillschweigend die falschen Dateien (eine statt zwei), und
// `findings` ebenso. Ein Zähler, der etwas anderes zählt als der
// Watcher auslöst, ist schlimmer als keiner.
func (a *Auftrag) WatchTreffer(root, unter string) ([]string, error) {
	wurzel := a.WatchWurzel()
	if wurzel == "" {
		return nil, nil
	}
	if unter == "" {
		unter = wurzel
	}
	var treffer []string
	err := filepath.WalkDir(filepath.Join(root, unter), func(pfad string, d fs.DirEntry, err error) error {
		if err != nil {
			// Ein Verzeichnis, das gerade verschwindet, ist kein Fehler
			// des Auftrags — der Rest wird trotzdem gelesen.
			return nil
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, pfad)
		if err != nil || !a.WatchTrifft(rel) {
			return nil
		}
		treffer = append(treffer, rel)
		return nil
	})
	sort.Strings(treffer)
	return treffer, err
}

// WatchRekursiv sagt, ob das Muster in Unterverzeichnisse greift und der
// Watcher den Baum deshalb rekursiv registrieren muss.
//
// Entschieden wird am Verzeichnis-Anteil, nicht am Doppelstern allein:
// auch `*/eingang/*.pdf` nennt Verzeichnisse, deren Namen erst zur
// Laufzeit feststehen. Wer nur `*.pdf` schreibt, bekommt weiterhin genau
// einen inotify-Watch — rekursiv ist ein Opt-in, kein neues
// Standardverhalten.
func (a *Auftrag) WatchRekursiv() bool {
	if a.Trigger.Watch == "" {
		return false
	}
	segmente := strings.Split(filepath.ToSlash(a.Trigger.Watch), "/")
	for _, teil := range segmente[:max(len(segmente)-1, 0)] {
		if strings.ContainsAny(teil, globMeta) {
			return true
		}
	}
	return false
}

// Throttle ist der Deckel eines Auftrags: höchstens Max Läufe je Per
// (Hasenbau-do0.2). Max == 0 heißt ungedrosselt.
//
// Gezählt wird nicht mitlaufend, sondern aus der laeufe-Tabelle (§5):
// ein Zähler im Speicher wäre nach jedem Daemon-Neustart wieder voll,
// und ausgerechnet ein Crash-Loop bekäme so jedes Mal frisches Budget.
// Das Fenster ist rollend — fünf pro Stunde heißt nicht „fünf, dann zur
// vollen Stunde wieder fünf", sondern dass zu jedem Zeitpunkt höchstens
// fünf Läufe in der zurückliegenden Stunde stehen.
type Throttle struct {
	Max int
	Per time.Duration

	// Between begrenzt die Tageszeit, zu der ein Lauf STARTEN darf;
	// nil = jederzeit. Beide Knöpfe gehören meist zusammen: ein Fenster
	// allein verschiebt die Arbeit nur in die Nacht, es deckelt sie
	// nicht (Hasenbau-do0.3).
	Between *Window
}

// An sagt, ob der Auftrag überhaupt gedrosselt ist.
func (t Throttle) An() bool { return t.Max > 0 || t.Between != nil }

// FormatDuration kürzt die Go-Schreibweise für die Anzeige: „1h0m0s"
// ist als Fenstergröße korrekt und in einem Dashboard nur laut.
// Sekunden bleiben stehen, wo es welche gibt.
func FormatDuration(d time.Duration) string {
	s := d.String()
	if d >= time.Minute && strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if d >= time.Hour && strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}

// String beschreibt den Deckel für Menschen.
func (t Throttle) String() string {
	var teile []string
	if t.Max == 1 {
		teile = append(teile, "1 Lauf je "+FormatDuration(t.Per))
	} else if t.Max > 1 {
		teile = append(teile, fmt.Sprintf("%d Läufe per %s", t.Max, FormatDuration(t.Per)))
	}
	if t.Between != nil {
		teile = append(teile, "nur "+t.Between.String())
	}
	if len(teile) == 0 {
		return "ungedrosselt"
	}
	return strings.Join(teile, ", ")
}

// Wait sagt, wie lange ein Lauf noch warten muss; 0 heißt „jetzt".
// `starts` sind die Startzeitpunkte der Läufe im rollenden Fenster,
// aufsteigend — bei Max == 0 wird die Liste nicht angesehen.
//
// Eine reine Funktion, und das mit Absicht: der Watcher entscheidet
// damit, ob er losläuft, und `hasenbau status` sagt damit voraus, wann
// es weitergeht. Zwei Rechnungen für dieselbe Frage liefen auseinander,
// und dann zeigte das Dashboard etwas anderes an, als der Daemon tut
// (Hasenbau-do0.4).
func (t Throttle) Wait(jetzt time.Time, starts []time.Time) time.Duration {
	// Das Tagesfenster zuerst: es kostet keine Abfrage.
	if t.Between != nil {
		if warten := t.Between.Until(jetzt); warten > 0 {
			return warten
		}
	}
	if t.Max <= 0 || len(starts) < t.Max {
		return 0
	}
	// Der älteste Lauf im Fenster fällt als erster heraus. Sind es mehr
	// als Max (etwa weil `hasenbau lauf` den Deckel umgeht), zählt der,
	// der als Max-letzter herausfällt.
	frei := starts[len(starts)-t.Max].Add(t.Per)
	warten := frei.Sub(jetzt)
	if warten <= 0 {
		// Grenzfall: die Uhr ist während der Abfrage weitergelaufen.
		return time.Millisecond
	}
	// Fällt der Platz erst frei, wenn das Fenster schon wieder zu ist,
	// gilt der spätere der beiden Zeitpunkte.
	if t.Between != nil {
		if nach := t.Between.Until(jetzt.Add(warten)); nach > 0 {
			return warten + nach
		}
	}
	return warten
}

// Window ist ein Tagesfenster in **Ortszeit** — „nachts" meint die Nacht
// des Menschen, der den Bau betreibt, nicht die von UTC. Gespeichert als
// Minuten seit Mitternacht.
//
// Halboffen [From, To): ein Fenster 22:00-06:00 lässt um 06:00 keinen
// Lauf mehr starten. Sonst hinge an der Sekunde, ob noch einer losgeht.
type Window struct {
	From, To int // Minuten seit Mitternacht
}

// UeberMitternacht sagt, ob das Fenster den Tageswechsel überspannt —
// der Normalfall für „nachts", nicht der Sonderfall.
func (w Window) UeberMitternacht() bool { return w.From > w.To }

// Contains sagt, ob t im Fenster liegt.
func (w Window) Contains(t time.Time) bool {
	m := t.Hour()*60 + t.Minute()
	if w.UeberMitternacht() {
		return m >= w.From || m < w.To
	}
	return m >= w.From && m < w.To
}

// Until liefert 0, wenn t im Fenster liegt, sonst die Zeit bis zum
// nächsten Öffnen. Gerechnet wird über time.Date, nicht über Addition
// von 24h: an einem Tag mit Zeitumstellung sind es nicht 24 Stunden,
// und dann öffnete das Fenster eine Stunde daneben.
func (w Window) Until(t time.Time) time.Duration {
	if w.Contains(t) {
		return 0
	}
	auf := time.Date(t.Year(), t.Month(), t.Day(), 0, w.From, 0, 0, t.Location())
	if !auf.After(t) {
		morgen := t.AddDate(0, 0, 1)
		auf = time.Date(morgen.Year(), morgen.Month(), morgen.Day(), 0, w.From, 0, 0, t.Location())
	}
	return auf.Sub(t)
}

func (w Window) String() string {
	return fmt.Sprintf("%02d:%02d-%02d:%02d", w.From/60, w.From%60, w.To/60, w.To%60)
}

// Laenge ist die Dauer eines geöffneten Fensters.
func (w Window) Laenge() time.Duration {
	minuten := w.To - w.From
	if w.UeberMitternacht() {
		minuten += 24 * 60
	}
	return time.Duration(minuten) * time.Minute
}

// parseWindow liest "22:00-06:00".
func parseWindow(s string) (*Window, error) {
	von, bis, ok := strings.Cut(s, "-")
	if !ok {
		return nil, fmt.Errorf("needs the form \"HH:MM-HH:MM\", got %q", s)
	}
	a, err := parseUhrzeit(strings.TrimSpace(von))
	if err != nil {
		return nil, err
	}
	b, err := parseUhrzeit(strings.TrimSpace(bis))
	if err != nil {
		return nil, err
	}
	// Gleicher Anfang und gleiches Ende ist nicht zu entscheiden: leeres
	// Fenster oder ganzer Tag? Lieber ablehnen als raten.
	if a == b {
		return nil, fmt.Errorf("start and end are the same (%q) — empty window or whole day? Leave the field out for any time", s)
	}
	return &Window{From: a, To: b}, nil
}

func parseUhrzeit(s string) (int, error) {
	t, err := time.Parse("15:04", s)
	if err != nil {
		return 0, fmt.Errorf("invalid time %q, expected HH:MM", s)
	}
	return t.Hour()*60 + t.Minute(), nil
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
		return fmt.Errorf("invalid name %q (allowed: letters, digits, . _ -)", name)
	}
	return nil
}

// dauer parst YAML-Strings wie "5s" oder "120s" nach time.Duration.
type duration time.Duration

func (d *duration) UnmarshalYAML(node *yaml.Node) error {
	var s string
	if err := node.Decode(&s); err != nil {
		return fmt.Errorf("duration expects a string like \"5s\", line %d", node.Line)
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q, line %d", s, node.Line)
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
	Hase        string    `yaml:"hase"`
	HaseTimeout *duration `yaml:"hase_timeout"`
	Monitored   bool      `yaml:"monitored"`
	Tools       []string  `yaml:"tools"`
	Throttle    *struct {
		Max     int       `yaml:"max"`
		Per     *duration `yaml:"per"`
		Between string    `yaml:"between"`
	} `yaml:"throttle"`
	CWD     string            `yaml:"cwd"` // abgelehnt — bleibt im Schema für die klare Fehlermeldung
	Raeume  map[string]string `yaml:"raeume"`
	Context []struct {
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
		Name:      name,
		Hase:      fm.Hase,
		Monitored: fm.Monitored,
		Tools:     fm.Tools,
		Raeume:    fm.Raeume,
		Body:      strings.TrimSpace(body),
	}
	// Ein Werkzeugname wandert in den permission-Block des generierten
	// Agenten und in einen Dateinamen unter tools/. Beides verträgt
	// keine Überraschungen, und ein Tippfehler soll hier auffallen und
	// nicht erst an einem Lauf, in dem der Hase das Werkzeug nicht hat.
	gesehenesTool := map[string]bool{}
	for _, w := range a.Tools {
		if err := ValidName(w); err != nil {
			return nil, fehler("tools: %v", err)
		}
		if gesehenesTool[w] {
			return nil, fehler("tools: %q listed twice", w)
		}
		gesehenesTool[w] = true
	}

	// Trigger: genau eines von watch, cron oder manuell.
	if fm.Trigger == nil {
		return nil, fehler("trigger missing (watch, cron or manual)")
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
		return nil, fehler("trigger needs exactly one of watch, cron or manual")
	case gesetzt > 1:
		return nil, fehler("trigger: watch, cron and manual are mutually exclusive")
	case a.Trigger.Manual:
		if a.Trigger.Debounce != 0 {
			return nil, fehler("debounce only applies to watch triggers")
		}
	case a.Trigger.Cron != "":
		if _, err := cron.ParseStandard(a.Trigger.Cron); err != nil {
			return nil, fehler("invalid cron expression %q: %v", a.Trigger.Cron, err)
		}
		if a.Trigger.Debounce != 0 {
			return nil, fehler("debounce only applies to watch triggers")
		}
	default:
		if err := pruefeWatchMuster(a.Trigger.Watch); err != nil {
			return nil, fehler("trigger.watch: %v", err)
		}
		// Der Eingang ist ein Raum, kein Pfad im Glob (Hasenbau-d6d).
		// Ohne ihn wüsste der Watcher nicht, wo er suchen soll — und ein
		// stiller Rückfall auf den Bau-Root hieße, den ganzen Bau zu
		// beobachten.
		if a.Raeume[RolleInput] == "" {
			return nil, fehler("watch trigger without Raum %q — the input lives under raeume: input:, watch: carries only the pattern", RolleInput)
		}
		if err := BauRelative(a.WatchGlob()); err != nil {
			return nil, fehler("trigger.watch: %v", err)
		}
	}

	// Hase: Pflicht; der Name landet im generierten Agent-Dateinamen.
	if a.Hase == "" {
		return nil, fehler("hase missing")
	}
	if err := ValidName(a.Hase); err != nil {
		return nil, fehler("hase: %v", err)
	}
	// „kein Zeitlimit" gibt es nicht: ein Lauf darf lange dauern, aber
	// nie für immer hängen. Wer 0s schreibt, meint vermutlich genau das
	// — deshalb ein Fehler statt eines stillen Rückfalls auf die Vorgabe.
	if fm.HaseTimeout != nil {
		if *fm.HaseTimeout == 0 {
			return nil, fehler("hase_timeout: 0 is not a time limit — leave the field out for the runner default")
		}
		a.HaseTimeout = time.Duration(*fm.HaseTimeout)
	}

	// Der Deckel braucht beide Hälften: eine Zahl ohne Fenster ist keine
	// Rate, ein Fenster ohne Zahl deckelt nichts. Beides einzeln
	// hinzuschreiben sieht aus wie eine Drossel und ist keine — deshalb
	// ein Ladefehler und kein stilles Ignorieren.
	if fm.Throttle != nil {
		t := fm.Throttle
		switch {
		case t.Max < 0:
			return nil, fehler("throttle: max has to be > 0 (leave throttle: out for unthrottled)")
		case t.Max > 0 && t.Per == nil:
			return nil, fehler("throttle: max without per — at most %d Läufe per … what?", t.Max)
		case t.Max == 0 && t.Per != nil:
			return nil, fehler("throttle: per without max — the window caps nothing")
		case t.Max > 0 && *t.Per == 0:
			return nil, fehler("throttle: per: 0 is not a window — leave the field out for unthrottled")
		case t.Max == 0 && t.Between == "":
			return nil, fehler("throttle: empty — leave the field out for unthrottled")
		}
		a.Throttle = Throttle{Max: t.Max}
		if t.Per != nil {
			a.Throttle.Per = time.Duration(*t.Per)
		}
		if t.Between != "" {
			fenster, err := parseWindow(t.Between)
			if err != nil {
				return nil, fehler("throttle.between: %v", err)
			}
			a.Throttle.Between = fenster
		}
	}

	// Sessions ankern immer am Bau-Root: Räume dürfen eigene Git-Repos
	// sein, und ein CWD in einem Raum verschöbe den Worktree-Anker der
	// Permissions dorthin (§4, §11.5). Deshalb ist cwd: kein stilles
	// No-op, sondern ein Ladefehler.
	if fm.CWD != "" {
		return nil, fehler("cwd is not supported — sessions always anchor at the Bau root (PLAN.md §4)")
	}

	for rolle, pfad := range a.Raeume {
		if rolle == "" || pfad == "" {
			return nil, fehler("raeume: role and path must not be empty")
		}
		if err := BauRelative(pfad); err != nil {
			return nil, fehler("raum %s: %v", rolle, err)
		}
	}

	gesehen := map[string]bool{}
	for i, g := range fm.Gaenge {
		if g.Name == "" {
			return nil, fehler("Gang %d: name missing", i+1)
		}
		if g.Run == "" {
			return nil, fehler("Gang %q: run missing", g.Name)
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
			return nil, fehler("context %d: file and last_summaries are mutually exclusive", i+1)
		case k.File == "" && k.LastSummaries == nil:
			return nil, fehler("context %d: needs file or last_summaries", i+1)
		case k.LastSummaries != nil && *k.LastSummaries <= 0:
			return nil, fehler("context %d: last_summaries has to be > 0", i+1)
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

	// Die Auslöser-Variable muss zur Trigger-Art passen. Früher war das
	// eine Sonderregel für manuell-Aufträge („$INPUT ist hier kein
	// Pfad"); seit der Auslöser zwei Namen hat, ist es eine Symmetrie:
	// gebunden ist genau der Name, den die Trigger-Art hergibt, und wer
	// den anderen schreibt, bekommt es beim Laden gesagt statt mitten im
	// Lauf (Hasenbau-d6d).
	for _, stelle := range a.ausloeserStellen() {
		if err := pruefeAusloeserVariablen(a.Trigger.Kind(), stelle.text); err != nil {
			return nil, fehler("%s: %v", stelle.wo, err)
		}
	}

	if a.Body == "" {
		return nil, fehler("body missing — the Markdown part is the prompt core")
	}
	return a, nil
}

// Namen der Auslöser-Variablen (§6). Gebunden ist immer höchstens eine,
// und welche, sagt die Trigger-Art.
const (
	VarTriggerFile = "TRIGGER_FILE" // watch: die auslösende Datei, ein Pfad
	VarTriggerArg  = "TRIGGER_ARG"  // manual: das Argument von `hasenbau lauf`, freier Text
	VarInput       = "INPUT"        // abgeschafft, wird nur noch erkannt, um es zu erklären
)

// AusloeserVariable nennt die Variable, die bei dieser Trigger-Art
// gebunden ist — leer bei cron, wo es keinen Auslöser gibt.
func AusloeserVariable(kind string) string {
	switch kind {
	case TriggerWatch:
		return VarTriggerFile
	case TriggerManual:
		return VarTriggerArg
	default:
		return ""
	}
}

// variablePattern trifft $NAME mit Wortgrenze — dieselbe, die
// lauf.Substitute zieht, damit hier nichts durchrutscht, was dort
// ersetzt würde.
var variablePattern = regexp.MustCompile(`\$([A-Z][A-Za-z0-9_]*)`)

// ausloeserStelle ist ein Feld, dessen Text substituiert wird, samt
// seiner Bezeichnung für die Fehlermeldung.
type ausloeserStelle struct {
	wo   string
	text string
}

// ausloeserStellen sammelt alles, was lauf.Substitute später anfasst.
// Die Gänge gehören dazu: dort fiele der Fehler sonst erst auf, wenn der
// Lauf schon begonnen hat.
func (a *Auftrag) ausloeserStellen() []ausloeserStelle {
	var stellen []ausloeserStelle
	for i, g := range a.Gaenge {
		stellen = append(stellen, ausloeserStelle{fmt.Sprintf("gang %d (%s)", i+1, g.Name), g.Run})
	}
	for i, k := range a.Context {
		stellen = append(stellen, ausloeserStelle{fmt.Sprintf("kontext %d", i+1), k.File})
	}
	for i, n := range a.After {
		wo := fmt.Sprintf("nachher %d (%s)", i+1, n.Action)
		stellen = append(stellen, ausloeserStelle{wo, n.From}, ausloeserStelle{wo, n.To})
	}
	return stellen
}

// pruefeAusloeserVariablen lehnt ab, was diese Trigger-Art nicht binden
// kann. $INPUT bekommt eine eigene Meldung: es gab die Variable einmal,
// und „unbekannte Variable" wäre für einen Auftrag aus der Zeit davor
// eine Sackgasse statt eines Wegweisers (Hasenbau-d6d).
func pruefeAusloeserVariablen(kind, text string) error {
	gebunden := AusloeserVariable(kind)
	for _, treffer := range variablePattern.FindAllStringSubmatch(text, -1) {
		name := treffer[1]
		switch name {
		case VarInput:
			if gebunden == "" {
				return fmt.Errorf("$INPUT no longer exists, and a %s Auftrag has no trigger file — the reference has to come from a Raum ($RAUM_<role>)", kind)
			}
			return fmt.Errorf("$INPUT is now called $%s (%s trigger)", gebunden, kind)
		case VarTriggerFile, VarTriggerArg:
			if name == gebunden {
				continue
			}
			if gebunden == "" {
				return fmt.Errorf("$%s is not bound for a %s Auftrag — cron has no trigger file", name, kind)
			}
			return fmt.Errorf("$%s is not bound for a %s Auftrag, use $%s here", name, kind, gebunden)
		}
	}
	return nil
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
			return nil, fmt.Errorf("Auftrag %s: unknown Hase %q — no template hasen/%s.md", name, a.Hase, a.Hase)
		}
		auftraege = append(auftraege, a)
	}
	return auftraege, nil
}

// parseAfter zerlegt einen Schritt wie {move: "$TRIGGER_FILE -> raeume/archiv/"}.
// Pfade dürfen Variablen enthalten; substituiert wird erst im Runner.
func parseAfter(schritt map[string]string) (After, error) {
	if len(schritt) != 1 {
		return After{}, fmt.Errorf("exactly one action per step (move, copy or delete)")
	}
	for aktion, wert := range schritt {
		switch aktion {
		case "move", "copy":
			von, nach, ok := strings.Cut(wert, "->")
			von, nach = strings.TrimSpace(von), strings.TrimSpace(nach)
			if !ok || von == "" || nach == "" {
				return After{}, fmt.Errorf("%s needs the form \"FROM -> TO\", got %q", aktion, wert)
			}
			return After{Action: aktion, From: von, To: nach}, nil
		case "delete":
			if strings.TrimSpace(wert) == "" {
				return After{}, fmt.Errorf("delete needs a path")
			}
			return After{Action: aktion, From: strings.TrimSpace(wert)}, nil
		default:
			return After{}, fmt.Errorf("unbekannte Aktion %q (erlaubt: move, copy, delete)", aktion)
		}
	}
	return After{}, fmt.Errorf("leerer Schritt")
}

// globMeta sind die Zeichen, die ein Muster zum Muster machen. `{` steht
// dabei, seit doublestar matcht: es liest `{a,b}` als Alternative, und
// damit ist eine Klammer im Verzeichnis-Anteil genauso wenig ein
// wörtlicher Name wie ein Stern.
const globMeta = `*?[{`

// pruefeWatchMuster nimmt das Muster ohne den Raum davor. Zwei Formen
// werden abgelehnt, beide mit Absicht ausführlich:
//
// Ein Muster, das wie der alte Bau-relative Pfad aussieht, ist die
// häufigste Fassung aus der Zeit vor Hasenbau-d6d. Es würde sonst
// klaglos an den input-Raum gehängt und ergäbe raeume/eingang/raeume/…,
// also einen Auftrag, der nie feuert.
//
// Ein Muster, das der Matcher nicht lesen kann (eine offene Klammer
// etwa), wird beim LADEN abgelehnt statt im Watcher stillschweigend nie
// zu treffen. Ein Auftrag lädt entweder, oder er lädt mit Begründung
// nicht — ein Trigger, der nie feuert, ist die schlechteste der drei
// Möglichkeiten (Hasenbau-h64).
//
// Platzhalter im Verzeichnis-Anteil waren bis Hasenbau-5xv verboten,
// weil der Watcher nur ein festes Verzeichnis beobachtete: der Glob traf
// beim Start einmal und danach nie wieder. Jetzt registriert er den Baum
// unter dem input-Raum rekursiv, und die Sperre fällt.
func pruefeWatchMuster(muster string) error {
	if filepath.IsAbs(muster) {
		return fmt.Errorf("pattern %q has to be relative to the input Raum, not absolute", muster)
	}
	if strings.HasPrefix(muster, "raeume/") {
		return fmt.Errorf("pattern %q looks like a Bau-relative path — watch: carries only the pattern now, the input lives under raeume: input:", muster)
	}
	for _, teil := range strings.Split(filepath.ToSlash(muster), "/") {
		if teil == ".." {
			return fmt.Errorf("pattern %q must not leave the input Raum (..)", muster)
		}
	}
	if !doublestar.ValidatePattern(filepath.ToSlash(muster)) {
		return fmt.Errorf("pattern %q is not a valid glob pattern", muster)
	}
	return nil
}

// BauRelative erzwingt die Isolations-Invariante aus §3: Pfade in
// Aufträgen bleiben im Bau — relativ, ohne Ausbruch nach oben.
func BauRelative(p string) error {
	if filepath.IsAbs(p) {
		return fmt.Errorf("path %q has to be Bau-relative, not absolute", p)
	}
	for _, teil := range strings.Split(filepath.ToSlash(p), "/") {
		if teil == ".." {
			return fmt.Errorf("path %q must not leave the Bau (..)", p)
		}
	}
	return nil
}
