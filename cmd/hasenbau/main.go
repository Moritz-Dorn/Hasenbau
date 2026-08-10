// hasenbau ist das CLI und der Daemon (PLAN.md §2, §8 Phase 0).
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/Moritz-Dorn/Hasenbau/internal/auftrag"
	"github.com/Moritz-Dorn/Hasenbau/internal/bau"
	"github.com/Moritz-Dorn/Hasenbau/internal/hase"
	"github.com/Moritz-Dorn/Hasenbau/internal/lauf"
	"github.com/Moritz-Dorn/Hasenbau/internal/opencode"
	"github.com/Moritz-Dorn/Hasenbau/internal/provider"
	"github.com/Moritz-Dorn/Hasenbau/internal/rueckkanal"
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
  get <ressource>       zeigen, was der Bau kennt (auftraege, hasen,
                        gaenge, laeufe, lauf, provider)
  describe <res> <name> ein Objekt im Detail (auftrag, gang, hase, lauf)
  graben [-json] <id>   Trace eines Laufs ziehen (Baumeister-Input)
  baumeister <ziel>     Baumeister-Auftrag (aus hasenbau.yaml) auf einen
                        Lauf ansetzen; <ziel> ist eine Lauf-ID oder ein
                        Auftrag (dann dessen letzter Lauf)
  provider fetch <id>   Modell-Liste beim Provider-Endpoint holen
  status                Zustand des Baus zeigen
  mcp                   Rückkanal über stdio bedienen (startet opencode
                        selbst; nicht von Hand aufrufen)

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
	case "describe":
		return cmdDescribe(bau, rest[1:], out, errw)
	case "laeufe":
		// Harter Schnitt zugunsten des get-Schemas (Hasenbau-ha0). Der
		// alte Name bleibt nur als Wegweiser stehen.
		fmt.Fprintln(errw, "hasenbau laeufe heißt jetzt `hasenbau get laeufe`")
		return 2
	case "graben":
		return cmdGraben(bau, rest[1:], out, errw)
	case "baumeister":
		return cmdBaumeister(bau, rest[1:], out, errw)
	case "get":
		return cmdGet(bau, rest[1:], out, errw)
	case "provider":
		return cmdProvider(bau, rest[1:], os.Stdin, out, errw)
	case "status":
		return cmdStatus(bau, out, errw)
	case "mcp":
		return cmdMCP(bau, errw)
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
		fmt.Fprintln(out, "Hinweis: custom Provider brauchen ihr Gerüst (npm, options.baseURL) im provider:-Block von .opencode-home/opencode/opencode.json — auth.json teilt nur Credentials (PLAN.md §3).")
		fmt.Fprintln(out, "Die Modell-Liste holt danach `hasenbau provider fetch <provider-id>`.")
	}
	return 0
}

// dbPath ist die Konvention aus PLAN.md §4: state/hasenbau.db im Bau.
func dbPath(bau string) string {
	return filepath.Join(bau, "state", "hasenbau.db")
}

// version meldet der Rückkanal seinen Clients. Der Hasenbau kennt noch
// keine Releases — sobald es welche gibt, gehört sie hierher.
const version = "dev"

// cmdMCP bedient den Rückkanal über stdio (PLAN.md §8, Phase 2).
// Aufrufer ist opencode, nicht der Mensch: stdout gehört dem Protokoll,
// alles Erklärende geht nach stderr.
func cmdMCP(root string, errw io.Writer) int {
	st, err := store.Open(dbPath(root))
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	defer st.Close()

	if err := rueckkanal.Bediene(st, version); err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	return 0
}

// rueckkanalEintragen hält den mcp:-Eintrag der Bau-Config auf dem
// laufenden Binary. Muss vor dem Server-Start passieren — opencode
// liest die Config beim Hochfahren.
func rueckkanalEintragen(root string, logf func(string, ...any)) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("hasenbau: eigenen Pfad bestimmen: %w", err)
	}
	geschrieben, err := bau.MCPSicherstellen(root, exe)
	if err != nil {
		return err
	}
	if geschrieben {
		logf("Rückkanal in %s eingetragen (%s)", bau.OpencodeConfig, exe)
	}
	return nil
}

// laeufeAufraeumen schließt die Läufe ab, deren Prozess gestorben ist,
// bevor dieser hier selbst welche anlegt. Ohne das zählt `hasenbau
// status` für immer falsch und der Rückkanal findet keinen eindeutigen
// aktiven Lauf mehr (PLAN.md §5, §11.7).
func laeufeAufraeumen(st *store.Store, logf func(string, ...any)) error {
	leichen, err := st.LaeufeAufraeumen()
	if err != nil {
		return err
	}
	for _, l := range leichen {
		logf("Lauf %d (%s, %s, seit %s) aufgeräumt: %s",
			l.ID, l.Auftrag, l.Trigger,
			l.Gestartet.Local().Format("2006-01-02 15:04"), l.Fehler)
	}
	return nil
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

	if err := laeufeAufraeumen(st, logger.Printf); err != nil {
		logger.Print(err)
		return 1
	}
	auftraege, err := ladeUndGeneriere(root)
	if err != nil {
		logger.Print(err)
		return 1
	}
	if err := rueckkanalEintragen(root, logger.Printf); err != nil {
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

// cmdGraben druckt den Trace eines Laufs als Baumeister-Input
// (Markdown) oder mit -json strukturiert (§8 Phase 2).
//
// Zuerst aus der Bau-DB: seit die Läufe ihren Trace beim Ende ablegen,
// braucht graben dafür keinen opencode-Server — wichtig, weil der
// Baumeister graben in einem Gang aufruft. Fehlt die Zeile (Altlauf),
// holt es den Trace beim Server und trägt sie nach. -live erzwingt den
// Server-Weg und liefert die ungekürzten Ausgaben.
func cmdGraben(root string, args []string, out, errw io.Writer) int {
	fs := flag.NewFlagSet("graben", flag.ContinueOnError)
	fs.SetOutput(errw)
	alsJSON := fs.Bool("json", false, "Trace als JSON statt Markdown")
	live := fs.Bool("live", false, "Trace beim Server holen statt aus der Bau-DB")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(errw, "Aufruf: hasenbau graben [-json] [-live] <lauf-id>")
		return 2
	}
	id, err := strconv.ParseInt(fs.Arg(0), 10, 64)
	if err != nil {
		fmt.Fprintf(errw, "hasenbau graben: ungültige Lauf-ID %q\n", fs.Arg(0))
		return 2
	}

	logger := log.New(errw, "", log.LstdFlags)
	st, err := store.Open(dbPath(root))
	if err != nil {
		logger.Print(err)
		return 1
	}
	defer st.Close()

	l, err := st.LaufNachID(id)
	if err != nil {
		fmt.Fprintf(errw, "hasenbau graben: %v\n", err)
		return 1
	}
	if l.SessionID == "" {
		fmt.Fprintf(errw, "hasenbau graben: Lauf %d (%s, %s) hat keine Session — der Lauf scheiterte vor dem Hasen (Gänge? Prompt?).\n",
			l.ID, l.Auftrag, l.Status)
		return 1
	}

	trace, err := holeTrace(root, st, l, *live, logger)
	if err != nil {
		logger.Print(err)
		return 1
	}
	if *alsJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if err := enc.Encode(trace); err != nil {
			logger.Print(err)
			return 1
		}
		return 0
	}
	fmt.Fprintf(out, "# Trace Lauf %d — Auftrag %s (%s, %s)\n\n", l.ID, l.Auftrag, l.Trigger, l.Status)
	// Notizen aus dem Rückkanal zuerst: was der Hase selbst für
	// erwähnenswert hielt, ordnet den Trace darunter ein.
	notizen, err := st.Notizen(l.ID)
	if err != nil {
		logger.Print(err)
		return 1
	}
	if len(notizen) > 0 {
		fmt.Fprint(out, "## Notizen des Hasen\n\n")
		for _, n := range notizen {
			fmt.Fprintf(out, "- %s — %s\n", n.Geschrieben.Local().Format("15:04:05"), n.Text)
		}
		fmt.Fprintln(out)
	}
	fmt.Fprint(out, trace.Markdown())
	return 0
}

// holeTrace liefert den Trace eines Laufs — aus der Bau-DB, wenn er
// dort liegt, sonst über einen eigenen opencode-Server. Was so geholt
// wurde, wird nachgetragen: das erste graben eines Altlaufs füllt seine
// Zeile, danach geht es ohne Server.
func holeTrace(root string, st *store.Store, l *store.Lauf, live bool, logger *log.Logger) (*opencode.Trace, error) {
	if !live {
		roh, da, err := st.TraceLies(l.ID)
		if err != nil {
			return nil, err
		}
		if da {
			var t opencode.Trace
			if err := json.Unmarshal(roh, &t); err != nil {
				return nil, fmt.Errorf("hasenbau graben: abgelegter Trace von Lauf %d ist unlesbar: %w", l.ID, err)
			}
			return &t, nil
		}
	}

	sup, err := supervisor.New(supervisor.Config{BauDir: root, Logf: logger.Printf})
	if err != nil {
		return nil, err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := sup.Start(ctx); err != nil {
		return nil, err
	}
	defer sup.Stop()

	trace, err := opencode.ZieheTrace(ctx, opencode.New(sup.BaseURL()), l.SessionID)
	if err != nil {
		return nil, err
	}
	if !live {
		if roh, err := json.Marshal(trace); err == nil {
			if err := st.TraceSchreibe(l.ID, l.SessionID, roh); err != nil {
				logger.Printf("Trace von Lauf %d nicht nachgetragen: %v", l.ID, err)
			}
		}
	}
	return trace, nil
}

// cmdProvider hält die Modell-Liste eines custom Providers aktuell
// (PLAN.md §3, Hasenbau-op3.1). Nur auf Zuruf: der Daemon ruft das nie
// von selbst, sonst wäre die Isolation still unterlaufen.
func cmdProvider(root string, args []string, in io.Reader, out, errw io.Writer) int {
	if len(args) == 0 || args[0] != "fetch" {
		fmt.Fprintln(errw, "Aufruf: hasenbau provider fetch [-yes] <provider-id>")
		return 2
	}
	fs := flag.NewFlagSet("provider fetch", flag.ContinueOnError)
	fs.SetOutput(errw)
	ja := fs.Bool("yes", false, "ohne Rückfrage schreiben")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(errw, "Aufruf: hasenbau provider fetch [-yes] <provider-id>")
		return 2
	}
	id := fs.Arg(0)

	conf, err := provider.LadeConfig(root)
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	baseURL, err := conf.BaseURL(id)
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	key, err := provider.Schluessel(id)
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	modelle, err := provider.Hole(ctx, baseURL, key)
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}

	fmt.Fprintf(out, "%s: %d Modelle von %s\n\n", id, len(modelle), baseURL)
	ae := conf.Merge(id, modelle)
	if ae.Leer() {
		fmt.Fprintln(out, "Bau-Config ist auf Stand, nichts zu tun")
		return 0
	}
	fmt.Fprint(out, ae.Bericht())

	if !*ja {
		fmt.Fprintf(out, "\n%s schreiben? [j/N] ", conf.Pfad)
		antwort, _ := bufio.NewReader(in).ReadString('\n')
		switch strings.ToLower(strings.TrimSpace(antwort)) {
		case "j", "ja", "y", "yes":
		default:
			fmt.Fprintln(out, "abgebrochen, nichts geschrieben")
			return 0
		}
	}
	if err := conf.Schreibe(); err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	fmt.Fprintf(out, "geschrieben: %s\n", conf.Pfad)
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
