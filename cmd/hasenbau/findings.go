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
	"strconv"
	"strings"

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
	history, err := st.ToolCallHistory(sel.Auftrag, n)
	if err != nil {
		return findings.Report{}, findings.Finding{}, err
	}
	laeufe, err := st.RecentLaeufeByAuftrag(sel.Auftrag, n)
	if err != nil {
		return findings.Report{}, findings.Finding{}, err
	}
	report := findings.Analyze(sel.Auftrag, history, laeufe)
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

	history, err := st.ToolCallHistory(name, *n)
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	laeufe, err := st.RecentLaeufeByAuftrag(name, *n)
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}

	report := findings.Analyze(name, history, laeufe)
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
