// hasenbau ist das CLI und der Daemon (PLAN.md §2, §8 Phase 0).
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/Moritz-Dorn/Hasenbau/internal/store"
	"github.com/Moritz-Dorn/Hasenbau/internal/supervisor"
)

const usage = `hasenbau — Daemon, der opencode headless orchestriert.

Befehle:
  daemon           Daemon starten (hält den opencode-Server am Leben)
  lauf <auftrag>   Auftrag manuell triggern (kommt mit Phase 1)
  laeufe [-n N]    letzte Läufe zeigen
  status           Zustand des Baus zeigen

Globale Flags (vor dem Befehl):
  -bau <pfad>      Root des Baus (Default: .)
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, out, errw io.Writer) int {
	fs := flag.NewFlagSet("hasenbau", flag.ContinueOnError)
	fs.SetOutput(errw)
	bauFlag := fs.String("bau", ".", "Root des Baus")
	fs.Usage = func() { fmt.Fprint(errw, usage) }
	if err := fs.Parse(args); err != nil {
		return 2
	}
	rest := fs.Args()
	if len(rest) == 0 {
		fmt.Fprint(errw, usage)
		return 2
	}
	bau, err := filepath.Abs(*bauFlag)
	if err != nil {
		fmt.Fprintf(errw, "hasenbau: Bau-Pfad: %v\n", err)
		return 1
	}

	switch rest[0] {
	case "daemon":
		return cmdDaemon(bau, errw)
	case "lauf":
		if len(rest) != 2 {
			fmt.Fprintln(errw, "Aufruf: hasenbau lauf <auftrag>")
			return 2
		}
		fmt.Fprintf(errw, "hasenbau lauf %q: noch nicht umgesetzt — kommt mit Phase 1 (Auftrags-Parser + Runner).\n", rest[1])
		return 1
	case "laeufe":
		return cmdLaeufe(bau, rest[1:], out, errw)
	case "status":
		return cmdStatus(bau, out, errw)
	default:
		fmt.Fprintf(errw, "hasenbau: unbekannter Befehl %q\n\n%s", rest[0], usage)
		return 2
	}
}

// dbPath ist die Konvention aus PLAN.md §4: state/hasenbau.db im Bau.
func dbPath(bau string) string {
	return filepath.Join(bau, "state", "hasenbau.db")
}

func cmdDaemon(bau string, errw io.Writer) int {
	logger := log.New(errw, "", log.LstdFlags)

	st, err := store.Open(dbPath(bau))
	if err != nil {
		logger.Print(err)
		return 1
	}
	defer st.Close()

	sup, err := supervisor.New(supervisor.Config{BauDir: bau, Logf: logger.Printf})
	if err != nil {
		logger.Print(err)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	logger.Printf("hasenbau daemon: Bau %s", bau)
	err = sup.Run(ctx)
	if ctx.Err() != nil {
		logger.Print("hasenbau daemon: sauber beendet")
		return 0
	}
	logger.Printf("hasenbau daemon: %v", err)
	return 1
}

func cmdLaeufe(bau string, args []string, out, errw io.Writer) int {
	fs := flag.NewFlagSet("laeufe", flag.ContinueOnError)
	fs.SetOutput(errw)
	n := fs.Int("n", 20, "Anzahl Läufe")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	st, err := store.Open(dbPath(bau))
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	defer st.Close()

	laeufe, err := st.LetzteLaeufe(*n)
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	if len(laeufe) == 0 {
		fmt.Fprintln(out, "keine Läufe")
		return 0
	}

	w := tabwriter.NewWriter(out, 2, 4, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tAUFTRAG\tTRIGGER\tSTATUS\tGESTARTET\tDAUER\tSUMMARY")
	for _, l := range laeufe {
		dauer := "läuft"
		if l.Beendet != nil {
			dauer = l.Beendet.Sub(l.Gestartet).Round(time.Second).String()
		}
		summary := l.Summary
		if l.Status == "fehler" && l.Fehler != "" {
			summary = l.Fehler
		}
		fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\t%s\n",
			l.ID, l.Auftrag, l.Trigger, l.Status,
			l.Gestartet.Local().Format("2006-01-02 15:04"), dauer, summary)
	}
	w.Flush()
	return 0
}

func cmdStatus(bau string, out, errw io.Writer) int {
	st, err := store.Open(dbPath(bau))
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	defer st.Close()

	counts, err := st.StatusZaehler()
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	states, err := st.AuftragStates()
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}

	fmt.Fprintf(out, "Bau: %s\n", bau)
	total := 0
	for _, n := range counts {
		total += n
	}
	fmt.Fprintf(out, "Läufe: %d gesamt", total)
	for _, s := range []string{"laeuft", "ok", "fehler", "abgebrochen"} {
		if counts[s] > 0 {
			fmt.Fprintf(out, ", %d %s", counts[s], s)
		}
	}
	fmt.Fprintln(out)

	if len(states) == 0 {
		fmt.Fprintln(out, "Aufträge: noch keine gelaufen")
		return 0
	}
	w := tabwriter.NewWriter(out, 2, 4, 2, ' ', 0)
	fmt.Fprintln(w, "AUFTRAG\tLETZTER LAUF\tLETZTER OK\tFEHLERSERIE")
	fmtTime := func(t *time.Time) string {
		if t == nil {
			return "-"
		}
		return t.Local().Format("2006-01-02 15:04")
	}
	for _, a := range states {
		fmt.Fprintf(w, "%s\t%s\t%s\t%d\n",
			a.Auftrag, fmtTime(a.LetzterLauf), fmtTime(a.LetzterOk), a.FehlerSerie)
	}
	w.Flush()
	return 0
}
