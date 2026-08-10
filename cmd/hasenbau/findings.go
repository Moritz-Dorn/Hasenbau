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

	"github.com/Moritz-Dorn/Hasenbau/internal/findings"
	"github.com/Moritz-Dorn/Hasenbau/internal/store"
)

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
	auftrag := fs.Arg(0)

	st, err := store.Open(dbPath(root))
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	defer st.Close()
	// Altläufe, deren Trace erst später entstand, zählen sonst nicht mit.
	backfillToolCalls(st, log.New(errw, "", log.LstdFlags).Printf)

	history, err := st.ToolCallHistory(auftrag, *n)
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	laeufe, err := st.RecentLaeufeByAuftrag(auftrag, *n)
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}

	report := findings.Analyze(auftrag, history, laeufe)
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
