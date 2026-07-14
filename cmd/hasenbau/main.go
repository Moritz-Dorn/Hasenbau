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

	"github.com/Moritz-Dorn/Hasenbau/internal/auftrag"
	"github.com/Moritz-Dorn/Hasenbau/internal/bau"
	"github.com/Moritz-Dorn/Hasenbau/internal/hase"
	"github.com/Moritz-Dorn/Hasenbau/internal/lauf"
	"github.com/Moritz-Dorn/Hasenbau/internal/opencode"
	"github.com/Moritz-Dorn/Hasenbau/internal/runner"
	"github.com/Moritz-Dorn/Hasenbau/internal/scheduler"
	"github.com/Moritz-Dorn/Hasenbau/internal/store"
	"github.com/Moritz-Dorn/Hasenbau/internal/supervisor"
	"github.com/Moritz-Dorn/Hasenbau/internal/watcher"
)

const usage = `hasenbau — Daemon, der opencode headless orchestriert.

Befehle:
  init <pfad>           leeren Bau anlegen (nicht-destruktiv, idempotent)
  daemon                Daemon starten (Trigger + opencode-Server)
  lauf <auftrag> [in]   Auftrag manuell triggern; [in] ist die
                        auslösende Datei (Bau-relativ, nur watch)
  laeufe [-n N]         letzte Läufe zeigen
  status                Zustand des Baus zeigen

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
	case "init":
		if len(rest) != 2 {
			fmt.Fprintln(errw, "Aufruf: hasenbau init <pfad>")
			return 2
		}
		return cmdInit(rest[1], out, errw)
	case "daemon":
		return cmdDaemon(bau, errw)
	case "lauf":
		if len(rest) != 2 && len(rest) != 3 {
			fmt.Fprintln(errw, "Aufruf: hasenbau lauf <auftrag> [input]")
			return 2
		}
		input := ""
		if len(rest) == 3 {
			input = rest[2]
		}
		return cmdLauf(bau, rest[1], input, errw)
	case "laeufe":
		return cmdLaeufe(bau, rest[1:], out, errw)
	case "status":
		return cmdStatus(bau, out, errw)
	default:
		fmt.Fprintf(errw, "hasenbau: unbekannter Befehl %q\n\n%s", rest[0], usage)
		return 2
	}
}

func cmdInit(pfad string, out, errw io.Writer) int {
	created, err := bau.Init(pfad)
	for _, c := range created {
		fmt.Fprintf(out, "angelegt: %s\n", c)
	}
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	if len(created) == 0 {
		fmt.Fprintln(out, "Bau ist vollständig, nichts zu tun")
	} else {
		fmt.Fprintln(out, "Hinweis: custom Provider brauchen ihre Definition im provider:-Block von .opencode-home/opencode/opencode.json — auth.json teilt nur Credentials (PLAN.md §3).")
	}
	return 0
}

// dbPath ist die Konvention aus PLAN.md §4: state/hasenbau.db im Bau.
func dbPath(bau string) string {
	return filepath.Join(bau, "state", "hasenbau.db")
}

// ladeUndGeneriere lädt die Aufträge und schreibt die generierten
// Agenten (§6: beim Laden der Definitionen, nicht pro Lauf). Läuft vor
// dem Server-Start — dispose braucht es hier deshalb nicht.
func ladeUndGeneriere(root string) ([]*auftrag.Auftrag, error) {
	auftraege, err := auftrag.Load(root)
	if err != nil {
		return nil, err
	}
	for _, a := range auftraege {
		t, err := hase.Lade(root, a.Hase)
		if err != nil {
			return nil, err
		}
		if _, err := hase.SchreibeAgent(root, a, t); err != nil {
			return nil, err
		}
	}
	return auftraege, nil
}

// warteAufServer blockiert, bis der Supervisor eine BaseURL meldet —
// Trigger werden erst danach scharf.
func warteAufServer(ctx context.Context, sup *supervisor.Supervisor, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if sup.BaseURL() != "" {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	return fmt.Errorf("hasenbau daemon: opencode-Server kam nicht hoch")
}

func cmdDaemon(root string, errw io.Writer) int {
	logger := log.New(errw, "", log.LstdFlags)

	st, err := store.Open(dbPath(root))
	if err != nil {
		logger.Print(err)
		return 1
	}
	defer st.Close()

	auftraege, err := ladeUndGeneriere(root)
	if err != nil {
		logger.Print(err)
		return 1
	}

	sup, err := supervisor.New(supervisor.Config{BauDir: root, Logf: logger.Printf})
	if err != nil {
		logger.Print(err)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	logger.Printf("hasenbau daemon: Bau %s, %d Aufträge", root, len(auftraege))
	supFertig := make(chan error, 1)
	go func() { supFertig <- sup.Run(ctx) }()

	if err := warteAufServer(ctx, sup, 60*time.Second); err != nil {
		logger.Print(err)
		stop()
		<-supFertig
		return 1
	}

	funnel := opencode.NewFunnel(sup.BaseURL, logger.Printf)
	funnel.Start(ctx)
	r := &runner.Runner{Root: root, BaseURL: sup.BaseURL, Store: st, Funnel: funnel, Logf: logger.Printf}
	sperre := lauf.NeueSperre()

	sched, err := scheduler.New(auftraege, sperre, func(a *auftrag.Auftrag) {
		if err := r.FuehreAus(ctx, a, "cron", ""); err != nil && ctx.Err() == nil {
			logger.Printf("scheduler: %v", err)
		}
	}, logger.Printf)
	if err != nil {
		logger.Print(err)
		return 1
	}
	w, err := watcher.New(root, auftraege, sperre, st, func(a *auftrag.Auftrag, input string) error {
		return r.FuehreAus(ctx, a, "watch", input)
	}, logger.Printf)
	if err != nil {
		logger.Print(err)
		return 1
	}

	sched.Start()
	if err := w.Start(ctx); err != nil {
		logger.Print(err)
		stop()
		sched.Stop()
		<-supFertig
		return 1
	}

	err = <-supFertig
	w.Stop()
	sched.Stop()
	if ctx.Err() != nil {
		logger.Print("hasenbau daemon: sauber beendet")
		return 0
	}
	logger.Printf("hasenbau daemon: %v", err)
	return 1
}

// cmdLauf triggert einen Auftrag manuell: eigener Server hoch, Lauf
// ausführen, Server runter. Kein Konflikt mit einem laufenden Daemon —
// beide binden eigene Ports; die DB teilt SQLite im WAL-Modus.
func cmdLauf(root, name, input string, errw io.Writer) int {
	logger := log.New(errw, "", log.LstdFlags)

	st, err := store.Open(dbPath(root))
	if err != nil {
		logger.Print(err)
		return 1
	}
	defer st.Close()

	auftraege, err := ladeUndGeneriere(root)
	if err != nil {
		logger.Print(err)
		return 1
	}
	var ziel *auftrag.Auftrag
	for _, a := range auftraege {
		if a.Name == name {
			ziel = a
		}
	}
	if ziel == nil {
		fmt.Fprintf(errw, "hasenbau lauf: unbekannter Auftrag %q\n", name)
		return 1
	}

	sup, err := supervisor.New(supervisor.Config{BauDir: root, Logf: logger.Printf})
	if err != nil {
		logger.Print(err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := sup.Start(ctx); err != nil {
		logger.Print(err)
		return 1
	}
	defer sup.Stop()

	funnel := opencode.NewFunnel(sup.BaseURL, logger.Printf)
	funnel.Start(ctx)
	r := &runner.Runner{Root: root, BaseURL: sup.BaseURL, Store: st, Funnel: funnel, Logf: logger.Printf}

	if err := r.FuehreAus(ctx, ziel, "manuell", input); err != nil {
		logger.Print(err)
		return 1
	}
	return 0
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
