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

	"github.com/Moritz-Dorn/Hasenbau/internal/auftrag"
	"github.com/Moritz-Dorn/Hasenbau/internal/hase"
	"github.com/Moritz-Dorn/Hasenbau/internal/provider"
	"github.com/Moritz-Dorn/Hasenbau/internal/runner"
	"github.com/Moritz-Dorn/Hasenbau/internal/store"
)

const describeUsage = `Aufruf: hasenbau describe <ressource> <name>

Ressourcen:
  auftrag <name>   Trigger, Gänge, Räume, Schreibrechte, letzte Läufe
  gang <datei>     ein Gang-Skript und alle Aufträge, die es rufen
  hase <name>      Template und die effektiven Permissions je Auftrag
  lauf <id>        ein Lauf mit Notizen, Fehlern, Tokens und Kosten
  provider <id>    Endpoint, Schlüssel, und die Modelle des Baus
`

func cmdDescribe(root string, args []string, out, errw io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(errw, describeUsage)
		return 2
	}
	switch args[0] {
	case "lauf":
		return describeLauf(root, args[1:], out, errw)
	case "auftrag":
		return describeAuftrag(root, args[1:], out, errw)
	case "hase":
		return describeHase(root, args[1:], out, errw)
	case "gang":
		return describeGang(root, args[1:], out, errw)
	case "provider":
		return describeProvider(root, args[1:], out, errw)
	default:
		fmt.Fprintf(errw, "hasenbau describe: unbekannte Ressource %q\n\n%s", args[0], describeUsage)
		return 2
	}
}

func describeLauf(root string, args []string, out, errw io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(errw, "Aufruf: hasenbau describe lauf <id>")
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
		a.field("Auslöser", "%s", l.Input)
	}
	a.field("Status", "%s", l.Status)
	a.field("Gestartet", "%s", l.Started.Local().Format("2006-01-02 15:04:05"))
	if l.Ended != nil {
		a.field("Beendet", "%s  (%s)", l.Ended.Local().Format("2006-01-02 15:04:05"), laufDuration(*l))
	} else {
		a.field("Beendet", "läuft noch")
	}
	if l.SessionID != "" {
		a.field("Session", "%s", l.SessionID)
	}
	if l.TokensIn > 0 || l.TokensOut > 0 {
		a.field("Tokens", "%d ein, %d aus", l.TokensIn, l.TokensOut)
	}
	a.field("Kosten", "%s", cost(l.CostCent))
	if l.Summary != "" {
		a.field("Summary", "%s", l.Summary)
	}

	a.done()

	// Der Fehler eines Laufs ist mehrzeilig und der Grund, warum man
	// überhaupt hinsieht — hier steht er vollständig, nicht gekürzt.
	if l.Error != "" {
		fmt.Fprintf(out, "\nFehler\n%s\n", indent(l.Error))
	}

	notizen, err := st.Notes(l.ID)
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	if len(notizen) > 0 {
		fmt.Fprint(out, "\nNotizen des Hasen\n")
		for _, n := range notizen {
			fmt.Fprintf(out, "  %s  %s\n", n.Written.Local().Format("15:04:05"), indent(n.Text))
		}
	}

	fmt.Fprintln(out)
	if l.SessionID == "" {
		fmt.Fprintln(out, "Kein Trace — der Lauf scheiterte vor dem Hasen.")
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
		fmt.Fprintln(errw, "Aufruf: hasenbau describe provider <id>")
		return 2
	}
	conf, err := provider.LoadConfig(root)
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	d, ok := conf.Detail(args[0])
	if !ok {
		fmt.Fprintf(errw, "hasenbau describe provider: %s kennt keinen Provider %q\n", conf.Pfad, args[0])
		var ids []string
		for _, e := range conf.List() {
			ids = append(ids, e.ID)
		}
		if len(ids) > 0 {
			fmt.Fprintf(errw, "  vorhanden: %s\n", strings.Join(ids, ", "))
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
	a.field("Definiert", "%s", jaNein(d.Definiert, "im provider:-Block", "NUR in enabled_providers — Tippfehler?"))
	a.field("Aktiv", "%s", jaNein(d.Aktiv, "steht in enabled_providers",
		"fehlt in enabled_providers — der Server ignoriert die Definition"))
	if d.NPM != "" {
		a.field("Adapter", "%s", d.NPM)
	}
	if d.BaseURL != "" {
		a.field("Endpoint", "%s", d.BaseURL)
	} else {
		a.field("Endpoint", "—  (eingebaut, oder das Gerüst ist unvollständig)")
	}
	a.field("Schlüssel", "%s", jaNein(schluessel[d.ID], "in auth.json", "fehlt — `opencode auth login`"))
	a.field("Datei", "%s", conf.Pfad)
	a.done()

	fmt.Fprintf(out, "\nModelle (%d)\n", len(d.Modelle))
	if len(d.Modelle) == 0 {
		fmt.Fprintln(out, "  keine in der Bau-Config — `hasenbau provider fetch "+d.ID+"` holt die Liste")
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
		return "ja  (" + ja + ")"
	}
	return "NEIN  (" + nein + ")"
}

func describeAuftrag(root string, args []string, out, errw io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(errw, "Aufruf: hasenbau describe auftrag <name>")
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
		fmt.Fprintf(errw, "hasenbau: unbekannter Auftrag %q\n", args[0])
		return 1
	}

	ab := newSection(out)
	ab.field("Auftrag", "%s", a.Name)
	ab.field("Datei", "auftraege/%s.md", a.Name)
	ab.field("Trigger", "%s", triggerShort(a.Trigger))
	if a.Trigger.Watch != "" {
		if n, err := filepath.Glob(filepath.Join(root, a.Trigger.Watch)); err == nil {
			ab.field("", "%d Datei(en) liegen gerade im Glob", len(n))
		}
		if a.Trigger.Debounce > 0 {
			ab.field("", "debounce %s", a.Trigger.Debounce)
		}
	}
	ab.field("Hase", "%s  →  Agent %s", a.Hase, hase.AgentName(a))
	if a.HaseTimeout > 0 {
		ab.field("Zeitlimit", "%s für den LLM-Schritt (hase_timeout)", a.HaseTimeout)
	} else {
		ab.field("Zeitlimit", "%s (Vorgabe; hase_timeout: nicht gesetzt)", runner.DefaultHaseTimeout)
	}
	ab.done()

	if len(a.Gaenge) > 0 {
		fmt.Fprint(out, "\nGänge\n")
		for i, g := range a.Gaenge {
			timeout := "kein Timeout"
			if g.Timeout > 0 {
				timeout = g.Timeout.String()
			}
			fmt.Fprintf(out, "  %d  %s  (%s)\n     %s\n", i+1, g.Name, timeout, g.Run)
			for _, datei := range gangFiles(g.Run) {
				zustand := "vorhanden"
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
				hinweis = "  → Schreibrecht des Hasen"
			}
			r.field("  "+rolle, "%s%s", a.Raeume[rolle], hinweis)
		}
		r.done()
	}

	if len(a.Context) > 0 {
		fmt.Fprint(out, "\nKontext\n")
		for _, k := range a.Context {
			if k.File != "" {
				fmt.Fprintf(out, "  Datei %s\n", k.File)
			} else {
				fmt.Fprintf(out, "  die letzten %d Summaries\n", k.LastSummaries)
			}
		}
	}
	if len(a.After) > 0 {
		fmt.Fprint(out, "\nNachher\n")
		for _, n := range a.After {
			if n.To == "" {
				fmt.Fprintf(out, "  %s %s\n", n.Action, n.From)
			} else {
				fmt.Fprintf(out, "  %s %s -> %s\n", n.Action, n.From, n.To)
			}
		}
	}

	// Der Body ist der Prompt-Kern — Fließtext, den man liest oder
	// bearbeitet. describe nennt ihn, gibt ihn aber nicht aus.
	fmt.Fprintf(out, "\nPrompt-Kern  %d Zeilen  (cat auftraege/%s.md)\n",
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
		fmt.Fprintln(out, "\nNoch nie gelaufen.")
		return 0
	}
	fmt.Fprint(out, "\nLetzte Läufe\n")
	writeLaufTable(out, laeufe)
	return 0
}

func describeHase(root string, args []string, out, errw io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(errw, "Aufruf: hasenbau describe hase <name>")
		return 2
	}
	t, err := hase.Lade(root, args[0])
	if err != nil {
		fmt.Fprintf(errw, "hasenbau: %v\n", err)
		return 1
	}

	ab := newSection(out)
	ab.field("Hase", "%s", t.Name)
	ab.field("Datei", "hasen/%s.md", t.Name)
	if t.Description != "" {
		ab.field("Beschreibung", "%s", t.Description)
	}
	modell := t.Model
	if modell == "" {
		modell = "— (opencode entscheidet)"
	}
	ab.field("Modell", "%s", modell)
	if t.Temperature != nil {
		ab.field("Temperatur", "%g", *t.Temperature)
	}
	ab.done()

	if len(t.Knowledge) > 0 {
		fmt.Fprint(out, "\nBeigelegtes Wissen (steht in jedem Prompt dieses Hasen)\n")
		for _, w := range t.Knowledge {
			zeilen := len(strings.Split(w.Text, "\n"))
			quelle := w.Origin
			if quelle == "Der Hasenbau" {
				quelle += "  (mitgeliefert, passt zur installierten Version)"
			}
			fmt.Fprintf(out, "  %s  —  %d Zeilen\n", quelle, zeilen)
		}
	}

	if len(t.Denies) > 0 {
		fmt.Fprint(out, "\nEigene Einschränkungen (das Template darf nur verengen)\n")
		for _, d := range t.Denies {
			fmt.Fprintf(out, "  %s: %s deny\n", d.Permission, d.Pattern)
		}
	}

	// Der Grund für diesen Befehl: die effektiven Rechte stehen weder
	// im Template noch im Auftrag — sie entstehen erst beim Generieren.
	auftraege, ladefehler := loadDefinitions(root)
	if ladefehler != nil {
		fmt.Fprintf(errw, "hasenbau: Aufträge nicht lesbar, Permissions unbekannt: %v\n", ladefehler)
		return 1
	}
	benutzer := users(auftraege, t.Name)
	if len(benutzer) == 0 {
		fmt.Fprintln(out, "\nKein Auftrag benutzt diesen Hasen — es gibt keinen generierten Agenten.")
	}
	for _, a := range benutzer {
		fmt.Fprintf(out, "\nIn Auftrag %s  (Agent %s)\n", a.Name, hase.AgentName(a))
		zeilen, err := effectivePermissions(root, a, t)
		if err != nil {
			fmt.Fprintf(errw, "hasenbau: %v\n", err)
			return 1
		}
		for _, z := range zeilen {
			fmt.Fprintf(out, "  %s\n", z)
		}
	}

	fmt.Fprintf(out, "\nPrompt  %d Zeilen  (cat hasen/%s.md)\n",
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
		fmt.Fprintln(errw, "Aufruf: hasenbau describe gang <datei>")
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
		fmt.Fprintf(errw, "hasenbau: kein Gang %q unter gaenge/\n", gesucht)
		return 1
	}

	ab := newSection(out)
	ab.field("Gang", "%s", g.Path)
	if !exists(root, g.Path) {
		ab.field("Datei", "FEHLT — ein Auftrag ruft sie, aber es gibt sie nicht")
	} else {
		ab.field("Größe", "%d Bytes%s", g.Size, execFlag(g.Executable))
	}
	if g.Draft {
		ab.field("Draft", "vom Baumeister geschrieben, nicht aktiviert (PLAN.md §8)")
	}
	if z := purpose(root, g.Path); z != "" {
		ab.field("Zweck", "%s", z)
	}
	ab.done()

	if len(g.Uses) == 0 {
		if g.Draft {
			fmt.Fprintln(out, "\nKein Auftrag trägt ihn ein — lies ihn und trag den Gang selbst ein.")
		} else {
			fmt.Fprintln(out, "\nKein Auftrag benutzt ihn.")
		}
	} else {
		fmt.Fprint(out, "\nBenutzt von\n")
		for _, b := range g.Uses {
			timeout := "kein Timeout"
			if b.Timeout > 0 {
				timeout = b.Timeout.String()
			}
			fmt.Fprintf(out, "  %s / %s  (%s)\n    %s\n", b.Auftrag, b.Gang, timeout, b.Run)
		}
	}
	if ladefehler != nil {
		fmt.Fprintf(errw, "hasenbau: Aufträge nicht vollständig lesbar: %v\n", ladefehler)
	}
	return 0
}

func execFlag(b bool) string {
	if b {
		return ", ausführbar"
	}
	return ", nicht ausführbar"
}
