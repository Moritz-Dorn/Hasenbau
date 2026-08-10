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
	"strings"
	"text/tabwriter"
)

const describeUsage = `Aufruf: hasenbau describe <ressource> <name>

Ressourcen:
  lauf <id>   ein Lauf mit Notizen, Fehlern, Tokens und Kosten
`

func cmdDescribe(root string, args []string, out, errw io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(errw, describeUsage)
		return 2
	}
	switch args[0] {
	case "lauf":
		return describeLauf(root, args[1:], out, errw)
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
