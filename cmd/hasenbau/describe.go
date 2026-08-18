// describe.go: das erklärende Verb der CLI (Hasenbau-ha0).
//
// `describe` ist kein `cat`. Es zeigt ein Objekt gerendert, samt allem,
// was der Hasenbau darüber hinaus darüber weiß — bei einem Lauf also die
// Notizen aus dem Rückkanal, die sonst nur als Beifang von `dig`
// sichtbar wären. Dateien gibt es nie im Volltext aus; wer sie will,
// bekommt den Pfad genannt.
package main

import (
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/Moritz-Dorn/Hasenbau/internal/auftrag"
	"github.com/Moritz-Dorn/Hasenbau/internal/hase"
	"github.com/Moritz-Dorn/Hasenbau/internal/provider"
	"github.com/Moritz-Dorn/Hasenbau/internal/runner"
	"github.com/Moritz-Dorn/Hasenbau/internal/store"
)

const describeUsage = `Usage: hasenbau describe <resource> [name]

Resources:
  bau              diagnosis: is this Bau in order?
  auftrag <name>   trigger, Gänge, Räume, write rights, recent Läufe
  gang <file>      a Gang script and every Auftrag that calls it
  tool <name>      a Schmied tool: state, review, who may call it
  hase <name>      template and the effective permissions per Auftrag
  lauf <id>        a Lauf with notes, errors, tokens and cost
  provider <id>    endpoint, key, and the models of the Bau
`

func cmdDescribe(root string, args []string, out, errw io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(errw, describeUsage)
		return 2
	}
	switch args[0] {
	case "bau":
		return describeBau(root, args[1:], out, errw)
	case "lauf":
		return describeLauf(root, args[1:], out, errw)
	case "auftrag":
		return describeAuftrag(root, args[1:], out, errw)
	case "hase":
		return describeHase(root, args[1:], out, errw)
	case "gang":
		return describeGang(root, args[1:], out, errw)
	case "tool":
		if len(args) != 2 {
			fmt.Fprintln(errw, "Usage: hasenbau describe tool <name>")
			return 2
		}
		return describeTool(root, args[1], out, errw)
	case "provider":
		return describeProvider(root, args[1:], out, errw)
	default:
		fmt.Fprintf(errw, "hasenbau describe: unknown resource %q\n\n%s", args[0], describeUsage)
		return 2
	}
}

func describeLauf(root string, args []string, out, errw io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(errw, "Usage: hasenbau describe lauf <id>")
		return 2
	}
	st, l, code := openLauf(root, args[0], errw)
	if st != nil {
		defer st.Close()
	}
	if code != 0 {
		return code
	}

	a := newSection(out)
	a.field("Lauf", "%d", l.ID)
	a.field("Auftrag", "%s", l.Auftrag)
	a.field("Trigger", "%s", l.Trigger)
	if l.Input != "" {
		a.field("Trigger file", "%s", l.Input)
	}
	a.field("Status", "%s", l.Status)
	a.field("Started", "%s", l.Started.Local().Format("2006-01-02 15:04:05"))
	if l.Ended != nil {
		a.field("Ended", "%s  (%s)", l.Ended.Local().Format("2006-01-02 15:04:05"), laufDuration(*l))
	} else {
		a.field("Ended", "still running")
	}
	if l.SessionID != "" {
		a.field("Session", "%s", l.SessionID)
	}
	if l.TokensIn > 0 || l.TokensOut > 0 {
		a.field("Tokens", "%d in, %d out", l.TokensIn, l.TokensOut)
	}
	a.field("Cost", "%s", cost(l.CostCent))
	if l.Summary != "" {
		a.field("Summary", "%s", l.Summary)
	}

	a.done()

	// Der Fehler eines Laufs ist mehrzeilig und der Grund, warum man
	// überhaupt hinsieht — hier steht er vollständig, nicht gekürzt.
	if l.Error != "" {
		fmt.Fprintf(out, "\nError\n%s\n", indent(l.Error))
	}

	notizen, err := st.Notes(l.ID)
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	if len(notizen) > 0 {
		fmt.Fprint(out, "\nNotes from the Hase\n")
		for _, n := range notizen {
			fmt.Fprintf(out, "  %s  %s\n", n.Written.Local().Format("15:04:05"), indent(n.Text))
		}
	}

	fmt.Fprintln(out)
	if l.SessionID == "" {
		fmt.Fprintln(out, "No trace — the Lauf failed before the Hase.")
	} else {
		fmt.Fprintf(out, "Trace: hasenbau dig %d\n", l.ID)
	}
	return 0
}

// section sammelt Label-Wert-Zeilen und richtet sie gemeinsam aus.
// Gemeinsam heißt: erst beim fertig() — vorher weiß der Tabwriter die
// Spaltenbreite nicht.
type section struct{ w *tabwriter.Writer }

func newSection(out io.Writer) *section {
	return &section{w: tabwriter.NewWriter(out, 2, 4, 2, ' ', 0)}
}

func (a *section) field(label, format string, args ...any) {
	fmt.Fprintf(a.w, "%s\t%s\n", label, fmt.Sprintf(format, args...))
}

func (a *section) done() { a.w.Flush() }

// indent hängt mehrzeilige Werte unter ihre erste Zeile.
func indent(s string) string {
	return strings.ReplaceAll(strings.TrimSpace(s), "\n", "\n  ")
}

// describeProvider zeigt einen Provider im Detail — vor allem seine
// Modelle (Hasenbau-ha0.7). Der praktische Nutzen: hier steht der
// `model:`-String, den ein Hasen-Template braucht, ohne dass jemand die
// opencode.json öffnen muss.
//
// Der Schlüssel selbst bleibt draußen. Gezeigt wird nur, ob es einen
// gibt; er liegt in auth.json und gehört dorthin (PLAN.md §3).
func describeProvider(root string, args []string, out, errw io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(errw, "Usage: hasenbau describe provider <id>")
		return 2
	}
	conf, err := provider.LoadConfig(root)
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	d, ok := conf.Detail(args[0])
	if !ok {
		fmt.Fprintf(errw, "hasenbau describe provider: %s knows no provider %q\n", conf.Pfad, args[0])
		var ids []string
		for _, e := range conf.List() {
			ids = append(ids, e.ID)
		}
		if len(ids) > 0 {
			fmt.Fprintf(errw, "  available: %s\n", strings.Join(ids, ", "))
		}
		return 1
	}
	schluessel, err := provider.KeyIDs()
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}

	a := newSection(out)
	a.field("Provider", "%s", d.ID)
	if d.Name != "" {
		a.field("Name", "%s", d.Name)
	}
	a.field("Defined", "%s", jaNein(d.Definiert, "in the provider: block", "ONLY in enabled_providers — a typo?"))
	a.field("Active", "%s", jaNein(d.Aktiv, "listed in enabled_providers",
		"missing from enabled_providers — the server ignores the definition"))
	if d.NPM != "" {
		a.field("Adapter", "%s", d.NPM)
	}
	if d.BaseURL != "" {
		a.field("Endpoint", "%s", d.BaseURL)
	} else {
		a.field("Endpoint", "—  (built in, or the scaffold is incomplete)")
	}
	a.field("Key", "%s", jaNein(schluessel[d.ID], "in auth.json", "missing — `opencode auth login`"))
	a.field("File", "%s", conf.Pfad)
	a.done()

	fmt.Fprintf(out, "\nModels (%d)\n", len(d.Modelle))
	if len(d.Modelle) == 0 {
		fmt.Fprintln(out, "  none in the Bau config — `hasenbau provider fetch "+d.ID+"` fetches the list")
		return 0
	}
	m := newSection(out)
	for _, modell := range d.Modelle {
		name := modell.Name
		if name == "" {
			name = "—"
		}
		// Der String, den ein Hasen-Template als model: braucht.
		m.field("  "+d.ID+"/"+modell.ID, "%s", name)
	}
	m.done()
	return 0
}

// jaNein rendert ein Flag samt Begründung — der Zustand allein sagt
// niemandem, was er bedeutet.
func jaNein(b bool, ja, nein string) string {
	if b {
		return "yes  (" + ja + ")"
	}
	return "NO  (" + nein + ")"
}

func describeAuftrag(root string, args []string, out, errw io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(errw, "Usage: hasenbau describe auftrag <name>")
		return 2
	}
	auftraege, err := loadDefinitions(root)
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	var a *auftrag.Auftrag
	for _, k := range auftraege {
		if k.Name == args[0] {
			a = k
		}
	}
	if a == nil {
		fmt.Fprintf(errw, "hasenbau: unknown Auftrag %q\n", args[0])
		return 1
	}

	ab := newSection(out)
	ab.field("Auftrag", "%s", a.Name)
	ab.field("File", "auftraege/%s.md", a.Name)
	ab.field("Trigger", "%s", triggerShort(a))
	if a.Trigger.Watch != "" {
		// Gezählt wird mit derselben Regel, mit der der Watcher auslöst
		// (WatchTreffer) — filepath.Glob läse den Doppelstern als
		// einfachen Stern und zählte etwas anderes, als hier passiert.
		if n, err := a.WatchTreffer(root, ""); err == nil {
			ab.field("", "%d file(s) currently match the pattern", len(n))
		}
		if a.Trigger.Debounce > 0 {
			ab.field("", "debounce %s", a.Trigger.Debounce)
		}
	}
	ab.field("Hase", "%s  →  Agent %s", a.Hase, hase.AgentName(a))
	if a.HaseTimeout > 0 {
		ab.field("Time limit", "%s for the LLM step (hase_timeout)", auftrag.FormatDuration(a.HaseTimeout))
	} else {
		ab.field("Time limit", "%s (default; hase_timeout: not set)", auftrag.FormatDuration(runner.DefaultHaseTimeout))
	}
	if a.Throttle.An() {
		ab.field("Cap", "%s  (throttle)", a.Throttle)
		// Die ausgerechnete Folge: „5 je Stunde" und „nur nachts" sind
		// einzeln klar, zusammen rechnet sie niemand gern im Kopf.
		if f := a.Throttle.Between; f != nil && a.Throttle.Max > 0 {
			ab.field("", "runs at most %d Läufe per night (open %s)",
				a.Throttle.Max*int(f.Laenge()/a.Throttle.Per), auftrag.FormatDuration(f.Laenge()))
		}
		if a.Throttle.Between != nil && a.Throttle.Max == 0 {
			ab.field("", "only shifts — without max/per the number of Läufe per night is unlimited")
		}
	}
	// Erfasst wird immer alles; das Flag entscheidet nur, ob die Befunde
	// ungefragt in `hasenbau status` stehen (Hasenbau-4cx.3).
	if a.Monitored {
		ab.field("Monitored", "yes  (monitored: true — findings appear in `hasenbau status`)")
	} else {
		ab.field("Monitored", "no  (it stays analysable: `hasenbau findings %s`)", a.Name)
	}
	ab.done()

	if len(a.Gaenge) > 0 {
		fmt.Fprint(out, "\nGänge\n")
		for i, g := range a.Gaenge {
			timeout := "no timeout"
			if g.Timeout > 0 {
				timeout = g.Timeout.String()
			}
			fmt.Fprintf(out, "  %d  %s  (%s)\n     %s\n", i+1, g.Name, timeout, g.Run)
			for _, datei := range gangFiles(g.Run) {
				zustand := "present"
				if !exists(root, datei) {
					zustand = "FEHLT"
				}
				fmt.Fprintf(out, "     %s  %s\n", datei, zustand)
			}
		}
	}

	if len(a.Raeume) > 0 {
		fmt.Fprint(out, "\nRäume\n")
		r := newSection(out)
		for _, rolle := range sortedKeys(a.Raeume) {
			hinweis := ""
			if grantsWrite(rolle) {
				hinweis = "  → the Hase may write here"
			}
			r.field("  "+rolle, "%s%s", a.Raeume[rolle], hinweis)
		}
		r.done()
	}

	if len(a.Context) > 0 {
		fmt.Fprint(out, "\nContext\n")
		for _, k := range a.Context {
			if k.File != "" {
				fmt.Fprintf(out, "  file %s\n", k.File)
			} else {
				fmt.Fprintf(out, "  the last %d summaries\n", k.LastSummaries)
			}
		}
	}
	if len(a.After) > 0 {
		fmt.Fprint(out, "\nAfterwards\n")
		for _, n := range a.After {
			if n.To == "" {
				fmt.Fprintf(out, "  %s %s\n", n.Action, n.From)
			} else {
				fmt.Fprintf(out, "  %s %s -> %s\n", n.Action, n.From, n.To)
			}
		}
	}

	// Der aktuelle Stand der Drossel — die Definition sagt, was gelten
	// soll, das hier sagt, was gerade gilt (Hasenbau-do0.4).
	if a.Throttle.An() {
		if st, err := store.Open(dbPath(root)); err == nil {
			stand := drosselStand(root, st, []*auftrag.Auftrag{a})
			st.Close()
			if len(stand) == 1 {
				d := stand[0]
				fmt.Fprint(out, "\nThrottled\n")
				if d.Eingang > 0 {
					fmt.Fprintf(out, "  %s in the input\n", anzahlDateien(d.Eingang))
				}
				if d.Warten == 0 {
					fmt.Fprintln(out, "  next Lauf: now")
				} else {
					fmt.Fprintf(out, "  next Lauf at the earliest %s (in %s)\n",
						d.Naechster.Local().Format("15:04"), auftrag.FormatDuration(d.Warten.Round(time.Minute)))
				}
			}
		}
	}

	// Der Body ist der Prompt-Kern — Fließtext, den man liest oder
	// bearbeitet. describe nennt ihn, gibt ihn aber nicht aus.
	fmt.Fprintf(out, "\nPrompt core  %d lines  (cat auftraege/%s.md)\n",
		len(strings.Split(a.Body, "\n")), a.Name)

	return recentLaeufe(root, a.Name, out, errw)
}

// recentLaeufe hängt die jüngste Historie an ein describe — die Frage
// „und, läuft das?" stellt sich sofort danach.
func recentLaeufe(root, auftragName string, out, errw io.Writer) int {
	st, err := store.Open(dbPath(root))
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	defer st.Close()
	laeufe, err := st.RecentLaeufeByAuftrag(auftragName, 5)
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	if len(laeufe) == 0 {
		fmt.Fprintln(out, "\nHas never run.")
		return 0
	}
	fmt.Fprint(out, "\nRecent Läufe\n")
	writeLaufTable(out, laeufe)
	return 0
}

func describeHase(root string, args []string, out, errw io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(errw, "Usage: hasenbau describe hase <name>")
		return 2
	}
	t, err := hase.Lade(root, args[0])
	if err != nil {
		fmt.Fprintf(errw, "hasenbau: %v\n", err)
		return 1
	}

	ab := newSection(out)
	ab.field("Hase", "%s", t.Name)
	ab.field("File", "hasen/%s.md", t.Name)
	if t.Description != "" {
		ab.field("Description", "%s", t.Description)
	}
	modell := t.Model
	if modell == "" {
		modell = "— (opencode decides)"
	}
	ab.field("Model", "%s", modell)
	if t.Temperature != nil {
		ab.field("Temperature", "%g", *t.Temperature)
	}
	ab.done()

	if len(t.Knowledge) > 0 {
		fmt.Fprint(out, "\nAttached knowledge (part of every prompt of this Hase)\n")
		for _, w := range t.Knowledge {
			zeilen := len(strings.Split(w.Text, "\n"))
			quelle := w.Origin
			if quelle == "Der Hasenbau" {
				quelle += "  (shipped with the binary, matches the installed version)"
			}
			fmt.Fprintf(out, "  %s  —  %d lines\n", quelle, zeilen)
		}
	}

	if len(t.Denies) > 0 {
		fmt.Fprint(out, "\nOwn restrictions (a template may only narrow)\n")
		for _, d := range t.Denies {
			fmt.Fprintf(out, "  %s: %s deny\n", d.Permission, d.Pattern)
		}
	}

	// Der Grund für diesen Befehl: die effektiven Rechte stehen weder
	// im Template noch im Auftrag — sie entstehen erst beim Generieren.
	auftraege, ladefehler := loadDefinitions(root)
	if ladefehler != nil {
		fmt.Fprintf(errw, "hasenbau: Aufträge not readable, permissions unknown: %v\n", ladefehler)
		return 1
	}
	benutzer := users(auftraege, t.Name)
	if len(benutzer) == 0 {
		fmt.Fprintln(out, "\nNo Auftrag uses this Hase — there is no generated agent.")
	}
	for _, a := range benutzer {
		fmt.Fprintf(out, "\nIn Auftrag %s  (agent %s)\n", a.Name, hase.AgentName(a))
		zeilen, err := effectivePermissions(root, a, t)
		if err != nil {
			fmt.Fprintf(errw, "hasenbau: %v\n", err)
			return 1
		}
		for _, z := range zeilen {
			fmt.Fprintf(out, "  %s\n", z)
		}
	}

	fmt.Fprintf(out, "\nPrompt  %d lines  (cat hasen/%s.md)\n",
		len(strings.Split(t.Prompt, "\n")), t.Name)
	return 0
}

// sortedKeys liefert die Schlüssel einer Rollen-Tabelle in fester
// Reihenfolge — Map-Iteration wäre bei jedem Aufruf anders.
func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func describeGang(root string, args []string, out, errw io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(errw, "Usage: hasenbau describe gang <file>")
		return 2
	}
	auftraege, ladefehler := loadDefinitions(root)
	gaenge, err := collectGaenge(root, auftraege)
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	// Bequemlichkeit: `describe gang pdf_to_md.py` findet auch
	// `gaenge/pdf_to_md.py`.
	gesucht := args[0]
	var g *gangFile
	for i := range gaenge {
		if gaenge[i].Path == gesucht || filepath.Base(gaenge[i].Path) == gesucht {
			g = &gaenge[i]
			break
		}
	}
	if g == nil {
		fmt.Fprintf(errw, "hasenbau: no Gang %q under gaenge/\n", gesucht)
		return 1
	}

	ab := newSection(out)
	ab.field("Gang", "%s", g.Path)
	if !exists(root, g.Path) {
		ab.field("File", "MISSING — an Auftrag calls it, but it does not exist")
	} else {
		ab.field("Size", "%d bytes%s", g.Size, execFlag(g.Executable))
	}
	if g.Draft {
		ab.field("Draft", "written by the Baumeister, not activated (PLAN.md §8)")
	}
	if z := purpose(root, g.Path); z != "" {
		ab.field("Purpose", "%s", z)
	}
	ab.done()

	if len(g.Uses) == 0 {
		if g.Draft {
			fmt.Fprintln(out, "\nNo Auftrag registers it — read it and register the Gang yourself.")
		} else {
			fmt.Fprintln(out, "\nNo Auftrag uses it.")
		}
	} else {
		fmt.Fprint(out, "\nUsed by\n")
		for _, b := range g.Uses {
			timeout := "no timeout"
			if b.Timeout > 0 {
				timeout = b.Timeout.String()
			}
			fmt.Fprintf(out, "  %s / %s  (%s)\n    %s\n", b.Auftrag, b.Gang, timeout, b.Run)
		}
	}
	if ladefehler != nil {
		fmt.Fprintf(errw, "hasenbau: Aufträge not fully readable: %v\n", ladefehler)
	}
	return 0
}

func execFlag(b bool) string {
	if b {
		return ", executable"
	}
	return ", not executable"
}
