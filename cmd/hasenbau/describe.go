// describe.go: das erklärende Verb der CLI (Hasenbau-ha0).
//
// `describe` ist kein `cat`. Es zeigt ein Objekt gerendert, samt allem,
// was der Hasenbau darüber hinaus darüber weiß — bei einem Lauf also die
// Notizen aus dem Rückkanal, die sonst nur als Beifang von `graben`
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
	"github.com/Moritz-Dorn/Hasenbau/internal/store"
)

const describeUsage = `Aufruf: hasenbau describe <ressource> <name>

Ressourcen:
  auftrag <name>   Trigger, Gänge, Räume, Schreibrechte, letzte Läufe
  hase <name>      Template und die effektiven Permissions je Auftrag
  lauf <id>        ein Lauf mit Notizen, Fehlern, Tokens und Kosten
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
	st, l, code := oeffneLauf(root, args[0], errw)
	if st != nil {
		defer st.Close()
	}
	if code != 0 {
		return code
	}

	a := neuerAbschnitt(out)
	a.feld("Lauf", "%d", l.ID)
	a.feld("Auftrag", "%s", l.Auftrag)
	a.feld("Trigger", "%s", l.Trigger)
	if l.Ausloeser != "" {
		a.feld("Auslöser", "%s", l.Ausloeser)
	}
	a.feld("Status", "%s", l.Status)
	a.feld("Gestartet", "%s", l.Gestartet.Local().Format("2006-01-02 15:04:05"))
	if l.Beendet != nil {
		a.feld("Beendet", "%s  (%s)", l.Beendet.Local().Format("2006-01-02 15:04:05"), laufDauer(*l))
	} else {
		a.feld("Beendet", "läuft noch")
	}
	if l.SessionID != "" {
		a.feld("Session", "%s", l.SessionID)
	}
	if l.TokensIn > 0 || l.TokensOut > 0 {
		a.feld("Tokens", "%d ein, %d aus", l.TokensIn, l.TokensOut)
	}
	a.feld("Kosten", "%s", kosten(l.KostenCent))
	if l.Summary != "" {
		a.feld("Summary", "%s", l.Summary)
	}

	a.fertig()

	// Der Fehler eines Laufs ist mehrzeilig und der Grund, warum man
	// überhaupt hinsieht — hier steht er vollständig, nicht gekürzt.
	if l.Fehler != "" {
		fmt.Fprintf(out, "\nFehler\n%s\n", einruecken(l.Fehler))
	}

	notizen, err := st.Notizen(l.ID)
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	if len(notizen) > 0 {
		fmt.Fprint(out, "\nNotizen des Hasen\n")
		for _, n := range notizen {
			fmt.Fprintf(out, "  %s  %s\n", n.Geschrieben.Local().Format("15:04:05"), einruecken(n.Text))
		}
	}

	fmt.Fprintln(out)
	if l.SessionID == "" {
		fmt.Fprintln(out, "Kein Trace — der Lauf scheiterte vor dem Hasen.")
	} else {
		fmt.Fprintf(out, "Trace: hasenbau graben %d\n", l.ID)
	}
	return 0
}

// abschnitt sammelt Label-Wert-Zeilen und richtet sie gemeinsam aus.
// Gemeinsam heißt: erst beim fertig() — vorher weiß der Tabwriter die
// Spaltenbreite nicht.
type abschnitt struct{ w *tabwriter.Writer }

func neuerAbschnitt(out io.Writer) *abschnitt {
	return &abschnitt{w: tabwriter.NewWriter(out, 2, 4, 2, ' ', 0)}
}

func (a *abschnitt) feld(label, format string, args ...any) {
	fmt.Fprintf(a.w, "%s\t%s\n", label, fmt.Sprintf(format, args...))
}

func (a *abschnitt) fertig() { a.w.Flush() }

// einruecken hängt mehrzeilige Werte unter ihre erste Zeile.
func einruecken(s string) string {
	return strings.ReplaceAll(strings.TrimSpace(s), "\n", "\n  ")
}

func describeAuftrag(root string, args []string, out, errw io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(errw, "Aufruf: hasenbau describe auftrag <name>")
		return 2
	}
	auftraege, err := ladeDefinitionen(root)
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

	ab := neuerAbschnitt(out)
	ab.feld("Auftrag", "%s", a.Name)
	ab.feld("Datei", "auftraege/%s.md", a.Name)
	ab.feld("Trigger", "%s", triggerKurz(a.Trigger))
	if a.Trigger.Watch != "" {
		if n, err := filepath.Glob(filepath.Join(root, a.Trigger.Watch)); err == nil {
			ab.feld("", "%d Datei(en) liegen gerade im Glob", len(n))
		}
		if a.Trigger.Debounce > 0 {
			ab.feld("", "debounce %s", a.Trigger.Debounce)
		}
	}
	ab.feld("Hase", "%s  →  Agent %s", a.Hase, hase.AgentName(a))
	ab.fertig()

	if len(a.Gaenge) > 0 {
		fmt.Fprint(out, "\nGänge\n")
		for i, g := range a.Gaenge {
			timeout := "kein Timeout"
			if g.Timeout > 0 {
				timeout = g.Timeout.String()
			}
			fmt.Fprintf(out, "  %d  %s  (%s)\n     %s\n", i+1, g.Name, timeout, g.Run)
			for _, datei := range gangDateien(g.Run) {
				zustand := "vorhanden"
				if !existiert(root, datei) {
					zustand = "FEHLT"
				}
				fmt.Fprintf(out, "     %s  %s\n", datei, zustand)
			}
		}
	}

	if len(a.Raeume) > 0 {
		fmt.Fprint(out, "\nRäume\n")
		r := neuerAbschnitt(out)
		for _, rolle := range sortiert(a.Raeume) {
			hinweis := ""
			if gibtSchreibrecht(rolle) {
				hinweis = "  → Schreibrecht des Hasen"
			}
			r.feld("  "+rolle, "%s%s", a.Raeume[rolle], hinweis)
		}
		r.fertig()
	}

	if len(a.Kontext) > 0 {
		fmt.Fprint(out, "\nKontext\n")
		for _, k := range a.Kontext {
			if k.Datei != "" {
				fmt.Fprintf(out, "  Datei %s\n", k.Datei)
			} else {
				fmt.Fprintf(out, "  die letzten %d Summaries\n", k.LetzteSummaries)
			}
		}
	}
	if len(a.Nachher) > 0 {
		fmt.Fprint(out, "\nNachher\n")
		for _, n := range a.Nachher {
			if n.Nach == "" {
				fmt.Fprintf(out, "  %s %s\n", n.Aktion, n.Von)
			} else {
				fmt.Fprintf(out, "  %s %s -> %s\n", n.Aktion, n.Von, n.Nach)
			}
		}
	}

	// Der Body ist der Prompt-Kern — Fließtext, den man liest oder
	// bearbeitet. describe nennt ihn, gibt ihn aber nicht aus.
	fmt.Fprintf(out, "\nPrompt-Kern  %d Zeilen  (cat auftraege/%s.md)\n",
		len(strings.Split(a.Body, "\n")), a.Name)

	return letzteLaeufe(root, a.Name, out, errw)
}

// letzteLaeufe hängt die jüngste Historie an ein describe — die Frage
// „und, läuft das?" stellt sich sofort danach.
func letzteLaeufe(root, auftragName string, out, errw io.Writer) int {
	st, err := store.Open(dbPath(root))
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	defer st.Close()
	laeufe, err := st.LetzteLaeufeNachAuftrag(auftragName, 5)
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	if len(laeufe) == 0 {
		fmt.Fprintln(out, "\nNoch nie gelaufen.")
		return 0
	}
	fmt.Fprint(out, "\nLetzte Läufe\n")
	schreibeLaufTabelle(out, laeufe)
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

	ab := neuerAbschnitt(out)
	ab.feld("Hase", "%s", t.Name)
	ab.feld("Datei", "hasen/%s.md", t.Name)
	if t.Description != "" {
		ab.feld("Beschreibung", "%s", t.Description)
	}
	modell := t.Model
	if modell == "" {
		modell = "— (opencode entscheidet)"
	}
	ab.feld("Modell", "%s", modell)
	if t.Temperature != nil {
		ab.feld("Temperatur", "%g", *t.Temperature)
	}
	ab.fertig()

	if len(t.Denies) > 0 {
		fmt.Fprint(out, "\nEigene Einschränkungen (das Template darf nur verengen)\n")
		for _, d := range t.Denies {
			fmt.Fprintf(out, "  %s: %s deny\n", d.Permission, d.Pattern)
		}
	}

	// Der Grund für diesen Befehl: die effektiven Rechte stehen weder
	// im Template noch im Auftrag — sie entstehen erst beim Generieren.
	auftraege, ladefehler := ladeDefinitionen(root)
	if ladefehler != nil {
		fmt.Fprintf(errw, "hasenbau: Aufträge nicht lesbar, Permissions unbekannt: %v\n", ladefehler)
		return 1
	}
	benutzer := nutzer(auftraege, t.Name)
	if len(benutzer) == 0 {
		fmt.Fprintln(out, "\nKein Auftrag benutzt diesen Hasen — es gibt keinen generierten Agenten.")
	}
	for _, a := range benutzer {
		fmt.Fprintf(out, "\nIn Auftrag %s  (Agent %s)\n", a.Name, hase.AgentName(a))
		zeilen, err := effektivePermissions(root, a, t)
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

// sortiert liefert die Schlüssel einer Rollen-Tabelle in fester
// Reihenfolge — Map-Iteration wäre bei jedem Aufruf anders.
func sortiert(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
