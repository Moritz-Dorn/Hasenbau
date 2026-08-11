// findings.go: `hasenbau findings <auftrag>` — was sich über die Läufe
// eines Auftrags rechnen lässt (PLAN.md §8 Phase 2, Hasenbau-4cx.2).
//
// Kein opencode-Server, kein Modell: die Zahlen liegen in der Bau-DB.
// Das ist der Punkt — das Anschauen kostet nichts, ist reproduzierbar,
// und jeder Vorschlag nennt die Läufe, auf denen er beruht. Ausgesucht
// wird von Hand; ausgearbeitet vom Baumeister, aber erst danach.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Moritz-Dorn/Hasenbau/internal/auftrag"
	"github.com/Moritz-Dorn/Hasenbau/internal/findings"
	"github.com/Moritz-Dorn/Hasenbau/internal/opencode"
	"github.com/Moritz-Dorn/Hasenbau/internal/store"
)

// selector ist die Auswahl eines einzelnen Befunds: `<auftrag>#<n>`,
// die Nummer aus der Liste von `hasenbau findings`.
//
// Warum ein String und keine zwei Argumente: Das Material des
// Baumeisters holt ein **Gang**, und ein Gang bekommt genau eine
// Variable mit — `$INPUT` (§6). Die Nummer ist der Griff, den der
// Mensch ohnehin schon in der Hand hat.
// maxTraces begrenzt, wie viele Traces unter einen Befund kommen.
const maxTraces = 3

type selector struct {
	Auftrag string
	Nr      int
}

func parseSelector(s string) (selector, bool) {
	name, nr, ok := strings.Cut(s, "#")
	if !ok {
		return selector{}, false
	}
	n, err := strconv.Atoi(nr)
	if err != nil || n < 1 || auftrag.ValidName(name) != nil {
		return selector{}, false
	}
	return selector{Auftrag: name, Nr: n}, true
}

func (s selector) String() string { return fmt.Sprintf("%s#%d", s.Auftrag, s.Nr) }

// resolveFinding rechnet die Befunde eines Auftrags und greift den
// gewählten heraus. Der Fehler nennt, was es stattdessen gibt — bei
// einer falschen Nummer ist das die halbe Antwort.
func resolveFinding(st *store.Store, sel selector, n int) (findings.Report, findings.Finding, error) {
	report, err := analyzeAuftrag(st, sel.Auftrag, n)
	if err != nil {
		return findings.Report{}, findings.Finding{}, err
	}
	if sel.Nr > len(report.Findings) {
		return report, findings.Finding{}, fmt.Errorf(
			"Befund %d gibt es nicht — %s hat %d (`hasenbau findings %s`)",
			sel.Nr, sel.Auftrag, len(report.Findings), sel.Auftrag)
	}
	return report, report.Findings[sel.Nr-1], nil
}

// digFinding schreibt das Material für einen ausgewählten Befund: den
// Befund selbst und die Traces der Läufe, auf denen er beruht.
//
// Das ist der Unterschied zwischen Stufe 1 und Stufe 2 des Baumeisters
// (PLAN.md §8): vorher sah er einen Trace und musste raten, was
// Parameter war; jetzt steht die Antwort gerechnet daneben — und die
// Traces stehen darunter, damit er sie belegen kann.
func digFinding(st *store.Store, sel selector, alsJSON bool, out, errw io.Writer) int {
	report, f, err := resolveFinding(st, sel, 20)
	if err != nil {
		fmt.Fprintf(errw, "hasenbau dig: %v\n", err)
		return 1
	}
	if alsJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if err := enc.Encode(f); err != nil {
			fmt.Fprintln(errw, err)
			return 1
		}
		return 0
	}

	fmt.Fprintf(out, "# Befund %d zu %s\n\n", sel.Nr, sel.Auftrag)
	fmt.Fprintf(out, "Gerechnet über %d ausgewertete Läufe — deterministisch, ohne Modell.\n\n", report.Laeufe)
	fmt.Fprint(out, f.Markdown(sel.Nr))

	if len(f.Laeufe) == 0 {
		return 0
	}
	// Nicht alle: vier vollständige Traces sind über zweitausend Zeilen,
	// und der Befund darüber ginge in der Mitte verloren — dieselbe
	// Falle, die schon den Rückkanal gekostet hat (Hasenbau-ifg). Der
	// gerechnete Befund IST die Verdichtung; die Traces darunter sind
	// Belege, keine Vollständigkeitspflicht.
	zeigen := f.Laeufe
	if len(zeigen) > maxTraces {
		zeigen = zeigen[:maxTraces]
	}
	fmt.Fprintf(out, "\n---\n\n# Die Läufe, auf denen der Befund beruht\n")
	if len(zeigen) < len(f.Laeufe) {
		fmt.Fprintf(out, "\nAbgedruckt sind die jüngsten %d von %d — die übrigen (%s)\n"+
			"stehen einzeln unter `hasenbau dig <id>`.\n",
			len(zeigen), len(f.Laeufe), laufListe(f.Laeufe[maxTraces:]))
	}
	for _, id := range zeigen {
		l, err := st.LaufByID(id)
		if err != nil {
			fmt.Fprintf(out, "\n## Lauf %d — nicht mehr in der Datenbank\n", id)
			continue
		}
		// Aus der DB, nie vom Server: dieser Befehl läuft in einem Gang,
		// und ein dort gestarteter opencode bliebe bei einem
		// Gang-Timeout verwaist zurück (§2).
		roh, da, err := st.ReadTrace(id)
		if err != nil || !da {
			fmt.Fprintf(out, "\n## Lauf %d (%s) — kein abgelegter Trace\n", id, l.Status)
			continue
		}
		var tr opencode.Trace
		if err := json.Unmarshal(roh, &tr); err != nil {
			fmt.Fprintf(out, "\n## Lauf %d — Trace unlesbar\n", id)
			continue
		}
		fmt.Fprintf(out, "\n## Lauf %d — %s, %s\n\n", id, l.Trigger, l.Status)
		fmt.Fprint(out, tr.Markdown())
	}
	return 0
}

// maxGemeldet begrenzt, wie viele Befunde je überwachtem Auftrag im
// Status stehen. Der Status ist ein Dashboard, keine Analyse — wer alle
// sehen will, ruft `hasenbau findings <auftrag>`.
const maxGemeldet = 3

// statusLaeufe ist die Tiefe der Auswertung im Status. Dieselbe Zahl wie
// die Vorgabe von `hasenbau findings`, damit beide dasselbe melden.
const statusLaeufe = 20

// writeMonitored meldet die Befunde der überwachten Aufträge
// (Hasenbau-4cx.3). Das Flag steuert genau das und nichts sonst:
// erfasst wird immer alles, und `hasenbau findings <auftrag>` rechnet
// über jeden Auftrag — überwacht heißt nur, ungefragt gemeldet zu
// werden.
func writeMonitored(st *store.Store, ueberwacht []string, gesamt int, out io.Writer) {
	if len(ueberwacht) == 0 {
		return
	}

	// Auftragsnamen sind per ValidName ASCII, deshalb reicht die
	// Byte-Länge als Spaltenbreite.
	breite := 0
	for _, name := range ueberwacht {
		breite = max(breite, len(name))
	}

	fmt.Fprintf(out, "\nÜberwacht  (%d von %d Aufträgen)\n", len(ueberwacht), gesamt)
	for _, name := range ueberwacht {
		zeile := func(format string, args ...any) {
			fmt.Fprintf(out, "  %-*s  %s\n", breite, name, fmt.Sprintf(format, args...))
		}
		report, err := analyzeAuftrag(st, name, statusLaeufe)
		if err != nil {
			zeile("nicht auswertbar: %v", err)
			continue
		}
		switch {
		case report.Laeufe == 0:
			zeile("noch kein ausgewerteter Lauf")
			continue
		case len(report.Findings) == 0:
			// Auch das ist eine Meldung: nichts gefunden über N Läufe
			// heißt, dass der Auftrag rund läuft.
			zeile("keine Befunde über %s", anzahlLaeufe(report.Laeufe))
			continue
		}
		zeile("%s über %s", anzahlBefunde(len(report.Findings)), anzahlLaeufe(report.Laeufe))
		for i, f := range report.Findings {
			if i == maxGemeldet {
				fmt.Fprintf(out, "      … und %d weitere\n", len(report.Findings)-maxGemeldet)
				break
			}
			fmt.Fprintf(out, "      %d. %s\n", i+1, f.Title)
		}
	}
	fmt.Fprintln(out, "\n  Ganz: `hasenbau findings <auftrag>` — ausarbeiten lassen: `hasenbau baumeister -finding <n> <auftrag>`")
}

// drossel beschreibt den Zustand eines gedrosselten Auftrags für die
// Anzeige (Hasenbau-do0.4). Ein Deckel, den man nicht sieht, ist von
// einem hängenden Daemon nicht zu unterscheiden: wer 200 PDFs ablegt
// und abends nachsieht, muss erkennen können, dass sich etwas staut —
// und zwar planmäßig.
type drossel struct {
	Auftrag   string
	Throttle  auftrag.Throttle
	Eingang   int           // Dateien, die gerade im Glob liegen
	Warten    time.Duration // bis zum nächsten möglichen Start; 0 = jetzt
	Naechster time.Time
}

// drosselStand rechnet den Zustand aller gedrosselten Aufträge.
//
// Der Rückstau ist bewusst nur die Zahl der Glob-Treffer und nicht „die
// noch nicht gesehenen": das zu wissen hieße, jede Datei zu hashen, und
// `status` ist der billige Befehl. Bei einem Auftrag mit `after: move`
// — dem Normalfall — ist beides ohnehin dasselbe, denn ein geglückter
// Lauf räumt seinen Input weg.
func drosselStand(root string, st *store.Store, auftraege []*auftrag.Auftrag) []drossel {
	jetzt := time.Now()
	var out []drossel
	for _, a := range auftraege {
		if !a.Throttle.An() {
			continue
		}
		d := drossel{Auftrag: a.Name, Throttle: a.Throttle}
		if a.Trigger.Watch != "" {
			if treffer, err := filepath.Glob(filepath.Join(root, a.Trigger.Watch)); err == nil {
				d.Eingang = len(treffer)
			}
		}
		var starts []time.Time
		if a.Throttle.Max > 0 {
			starts, _ = st.LaeufeSince(a.Name, jetzt.Add(-a.Throttle.Per))
		}
		d.Warten = a.Throttle.Wait(jetzt, starts)
		d.Naechster = jetzt.Add(d.Warten)
		out = append(out, d)
	}
	return out
}

// writeDrosseln meldet die gedrosselten Aufträge im Status.
func writeDrosseln(stand []drossel, out io.Writer) {
	if len(stand) == 0 {
		return
	}
	breite := 0
	for _, d := range stand {
		breite = max(breite, len(d.Auftrag))
	}
	fmt.Fprintf(out, "\nGedrosselt (%d)\n", len(stand))
	for _, d := range stand {
		fmt.Fprintf(out, "  %-*s  %s\n", breite, d.Auftrag, d.Throttle)
		var teile []string
		if d.Eingang > 0 {
			teile = append(teile, fmt.Sprintf("%s im Eingang", anzahlDateien(d.Eingang)))
		}
		if d.Warten == 0 {
			teile = append(teile, "nächster Lauf: jetzt")
		} else {
			teile = append(teile, fmt.Sprintf("nächster Lauf frühestens %s (in %s)",
				d.Naechster.Local().Format("15:04"), auftrag.FormatDuration(d.Warten.Round(time.Minute))))
		}
		fmt.Fprintf(out, "  %-*s  %s\n", breite, "", strings.Join(teile, ", "))
	}
}

func anzahlDateien(n int) string {
	if n == 1 {
		return "1 Datei"
	}
	return fmt.Sprintf("%d Dateien", n)
}

// monitoredNames sammelt die überwachten Aufträge in Definitionsreihenfolge.
func monitoredNames(auftraege []*auftrag.Auftrag) []string {
	var out []string
	for _, a := range auftraege {
		if a.Monitored {
			out = append(out, a.Name)
		}
	}
	return out
}

func anzahlBefunde(n int) string {
	if n == 1 {
		return "1 Befund"
	}
	return fmt.Sprintf("%d Befunde", n)
}

func anzahlLaeufe(n int) string {
	if n == 1 {
		return "1 Lauf"
	}
	return fmt.Sprintf("%d Läufe", n)
}

// analyzeAuftrag holt das Material eines Auftrags aus der DB und rechnet
// darüber — der gemeinsame Kern von `findings` und der Status-Meldung.
func analyzeAuftrag(st *store.Store, name string, n int) (findings.Report, error) {
	history, err := st.ToolCallHistory(name, n)
	if err != nil {
		return findings.Report{}, err
	}
	laeufe, err := st.RecentLaeufeByAuftrag(name, n)
	if err != nil {
		return findings.Report{}, err
	}
	return findings.Analyze(name, history, laeufe), nil
}

func cmdFindings(root string, args []string, out, errw io.Writer) int {
	fs := flag.NewFlagSet("findings", flag.ContinueOnError)
	fs.SetOutput(errw)
	alsJSON := fs.Bool("json", false, "strukturiert ausgeben (für Gänge)")
	n := fs.Int("n", 20, "wie viele Läufe zurück")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(errw, "Aufruf: hasenbau findings [-json] [-n N] <auftrag>")
		return 2
	}
	name := fs.Arg(0) // nicht `auftrag`: so heißt in dieser Datei das Paket

	st, err := store.Open(dbPath(root))
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	defer st.Close()
	// Altläufe, deren Trace erst später entstand, zählen sonst nicht mit.
	backfillToolCalls(st, log.New(errw, "", log.LstdFlags).Printf)

	report, err := analyzeAuftrag(st, name, *n)
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	if *alsJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			fmt.Fprintln(errw, err)
			return 1
		}
		return 0
	}
	fmt.Fprint(out, report.Markdown())
	return 0
}
