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
	"github.com/Moritz-Dorn/Hasenbau/internal/backchannel"
	"github.com/Moritz-Dorn/Hasenbau/internal/bau"
	"github.com/Moritz-Dorn/Hasenbau/internal/hase"
	"github.com/Moritz-Dorn/Hasenbau/internal/lauf"
	"github.com/Moritz-Dorn/Hasenbau/internal/opencode"
	"github.com/Moritz-Dorn/Hasenbau/internal/provider"
	"github.com/Moritz-Dorn/Hasenbau/internal/runner"
	"github.com/Moritz-Dorn/Hasenbau/internal/scheduler"
	"github.com/Moritz-Dorn/Hasenbau/internal/store"
	"github.com/Moritz-Dorn/Hasenbau/internal/supervisor"
	"github.com/Moritz-Dorn/Hasenbau/internal/watcher"
)

const usage = `hasenbau — a daemon that orchestrates opencode headless.

Commands:
  init <path>           create an empty Bau (non-destructive, idempotent)
  fix                   add the missing parts of a Bau — the same thing
                        for a Bau that already exists
  daemon                start the daemon (triggers + opencode server)
  lauf <auftrag> [in]   trigger an Auftrag by hand; [in] is the
                        triggering file (Bau-relative, watch only)
  get <resource>        show what the Bau knows (auftraege, hasen,
                        gaenge, laeufe, lauf, provider)
  describe <res> <name> one object in detail (auftrag, gang, hase, lauf)
  new <res> <name>      create a scaffold (auftrag, hase)
  dig [-json] <target>  material for the Baumeister: the trace of a
                        Lauf, or <auftrag>#<n> for a finding
  findings <auftrag>    what can be computed over the Läufe: Gang
                        candidates, friction, outliers (no model)
  baumeister [-finding N] <target>
                        put the Baumeister (from hasenbau.yaml) to work —
                        on one Lauf (Lauf ID or Auftrag), or with
                        -finding on a finding across many Läufe
  tool <verb> <name>    take a Schmied tool through its three stages:
                        review (read it and take responsibility), test
                        (run it and show the output), release (release
                        it). Each stage requires the previous one
  provider fetch <id>   fetch the model list from the provider endpoint
  status                show the state of the Bau
  mcp                   serve the back channel over stdio (opencode
                        starts this itself; do not call by hand)
  sandbox-incident      report a tool call that leads out of the sandbox
                        (called by the guard in the opencode server;
                        do not call by hand)

Global flags (before the command):
  -bau <path>      root of the Bau (default: .)
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, out, errw io.Writer) int {
	return runMitEingabe(os.Stdin, args, out, errw)
}

// runMitEingabe ist run mit austauschbarer Eingabe — die interaktiven
// Befehle (`tool release`, `provider fetch`) sind sonst nicht prüfbar,
// und gerade bei ihnen hängt etwas an der Rückfrage.
func runMitEingabe(in io.Reader, args []string, out, errw io.Writer) int {
	fs := flag.NewFlagSet("hasenbau", flag.ContinueOnError)
	fs.SetOutput(errw)
	bauFlag := fs.String("bau", ".", "root of the Bau")
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
		fmt.Fprintf(errw, "hasenbau: Bau path: %v\n", err)
		return 1
	}

	switch rest[0] {
	case "init":
		if len(rest) != 2 {
			fmt.Fprintln(errw, "Usage: hasenbau init <path>")
			return 2
		}
		return cmdInit(rest[1], out, errw)
	case "fix":
		if len(rest) != 1 {
			fmt.Fprintln(errw, "Usage: hasenbau [-bau <path>] fix")
			return 2
		}
		return cmdFix(bau, out, errw)
	case "daemon":
		return cmdDaemon(bau, errw)
	case "lauf":
		if len(rest) != 2 && len(rest) != 3 {
			fmt.Fprintln(errw, "Usage: hasenbau lauf <auftrag> [input]")
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
		fmt.Fprintln(errw, "hasenbau laeufe is now `hasenbau get laeufe`")
		return 2
	case "dig":
		return cmdDig(bau, rest[1:], out, errw)
	case "findings":
		return cmdFindings(bau, rest[1:], out, errw)
	case "baumeister":
		return cmdBaumeister(bau, rest[1:], out, errw)
	case "get":
		return cmdGet(bau, rest[1:], out, errw)
	case "new":
		return cmdNew(bau, rest[1:], out, errw)
	case "tool":
		return cmdTool(bau, rest[1:], in, out, errw)
	case "provider":
		return cmdProvider(bau, rest[1:], in, out, errw)
	case "status":
		return cmdStatus(bau, out, errw)
	case "mcp":
		return cmdMCP(bau, errw)
	case "sandbox-incident":
		return cmdSandboxIncident(bau, rest[1:], out, errw)
	default:
		fmt.Fprintf(errw, "hasenbau: unknown command %q\n\n%s", rest[0], usage)
		return 2
	}
}

// cmdFix ergänzt, was einem bestehenden Bau fehlt. Es ist derselbe
// Vorgang wie `init` — der ist seit jeher idempotent und
// nicht-destruktiv — nur mit umgekehrter Erwartung: init legt an, fix
// stellt her. Deshalb auch die andere Abgrenzung: wo nichts ist, ist
// nichts zu reparieren, und der Nutzer soll `init` lesen statt einen
// halben Bau im falschen Verzeichnis zu bekommen.
//
// Was fix schreibt, ist genau das, was `describe bau` als fehlend
// meldet — beide gehen über dieselbe Tabelle (internal/bau).
func cmdFix(pfad string, out, errw io.Writer) int {
	if _, err := os.Stat(filepath.Join(pfad, "hasenbau.yaml")); err != nil {
		fmt.Fprintf(errw, "hasenbau fix: %s is not a Bau (hasenbau.yaml missing) — `hasenbau init %s` creates a new one.\n", pfad, pfad)
		return 1
	}
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(errw, "hasenbau: determining own path: %v\n", err)
		return 1
	}
	// Vor Init nachsehen, denn Init ersetzt das Bau-Plugin still: es ist
	// ein Artefakt, kein Ergänztes, und fiele sonst durch beide Meldungen
	// (weder „ergänzt" noch der Log des Starts). Ein Befehl, der eine
	// Sicherheitsdatei tauscht und „nichts zu tun" sagt, ist genau die
	// Stille aus Hasenbau-uei.
	pluginWarAktuell, pluginErr := bau.PluginAktuell(pfad)

	ergaenzt, err := bau.Init(pfad, exe)
	for _, c := range ergaenzt {
		fmt.Fprintf(out, "added: %s\n", c)
	}
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	ersetzt := pluginErr == nil && !pluginWarAktuell
	if ersetzt {
		fmt.Fprintf(out, "replaced: %s (was not the version of this binary)\n", bau.PluginDatei)
	}
	if _, err := loadAndGenerate(pfad); err != nil {
		fmt.Fprintf(errw, "hasenbau fix: agents not generated: %v\n", err)
	}
	if len(ergaenzt) == 0 && !ersetzt {
		fmt.Fprintln(out, "Bau is complete, nothing to do")
	} else {
		fmt.Fprintln(out, "Whether the contents are right is what `hasenbau describe bau` tells you.")
	}
	return 0
}

func cmdInit(pfad string, out, errw io.Writer) int {
	// Absolut, wie das -bau-Flag auch: der Pfad landet im
	// Rückkanal-Eintrag, und den startet opencode mit einem CWD, das
	// nicht das des Aufrufers ist — `init testbau` würde sonst zu
	// `-bau testbau` relativ zum Bau selbst.
	pfad, err := filepath.Abs(pfad)
	if err != nil {
		fmt.Fprintf(errw, "hasenbau: Bau path: %v\n", err)
		return 1
	}
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(errw, "hasenbau: determining own path: %v\n", err)
		return 1
	}
	created, err := bau.Init(pfad, exe)
	for _, c := range created {
		fmt.Fprintf(out, "created: %s\n", c)
	}
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	// Agenten gleich mitschreiben. Seit init den Baumeister anlegt,
	// käme ein frischer Bau sonst mit einem offenen Punkt auf die Welt
	// („nicht generiert"), und genau das soll die Diagnose nicht melden
	// müssen. Ein Fehler hier bricht init nicht ab: angelegt ist
	// angelegt, und der nächste Daemon- oder Lauf-Start generiert
	// ohnehin neu.
	if _, err := loadAndGenerate(pfad); err != nil {
		fmt.Fprintf(errw, "hasenbau init: agents not generated yet: %v\n", err)
	}
	if len(created) == 0 {
		fmt.Fprintln(out, "Bau is complete, nothing to do")
	} else {
		fmt.Fprintf(out, "Back channel registered: %s (corrected to the running binary on every start).\n", exe)
		fmt.Fprintln(out, "Note: custom providers need their scaffold (npm, options.baseURL) in the provider: block of .opencode-home/opencode/opencode.json — auth.json only shares credentials (PLAN.md §3).")
		fmt.Fprintln(out, "The model list is then fetched by `hasenbau provider fetch <provider-id>`.")
	}
	return 0
}

// bauBudget baut den Bau-weiten Deckel aus hasenbau.yaml
// (Hasenbau-cvf). Ist keiner gesetzt — oder ist die Config unlesbar —
// kommt ein Budget ohne Grenze heraus: ein Deckel, den niemand gesetzt
// hat, darf keinen Lauf aufhalten.
//
// Ein Config-Fehler wird gemeldet und nicht verschluckt. Ihn hier zum
// Abbruch zu machen wäre falsch: `hasenbau lauf` soll auch dann noch
// gehen, wenn in der yaml etwas klemmt.
func bauBudget(root string, st *store.Store, logf func(string, ...any)) *runner.Budget {
	conf, err := bau.LoadConfig(root)
	if err != nil {
		logf("Bau cap: %v — none in effect", err)
		return &runner.Budget{}
	}
	return budgetAus(conf, st, logf)
}

// budgetAus baut den Deckel aus einer schon geladenen Config — für
// Aufrufer, die den Ladefehler selbst behandeln wollen (der Status muss
// ihn zeigen, nicht schlucken).
func budgetAus(conf *bau.Config, st *store.Store, logf func(string, ...any)) *runner.Budget {
	if !conf.Throttle.An() {
		return &runner.Budget{}
	}
	return &runner.Budget{
		Rate:   auftrag.Throttle{Max: conf.Throttle.Max, Per: conf.Throttle.Per},
		Laeufe: st.LaeufeSinceAll,
		Logf:   logf,
	}
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

	// Der Wunsch-Raum kommt aus der Config; ist keiner gesetzt, bleibt
	// das Werkzeug aus. Ein Config-Fehler darf den Rückkanal nicht
	// aufhalten — notiz und summary sind wichtiger als der Wunsch.
	wunschRaum := ""
	if cfg, err := bau.LoadConfig(root); err == nil {
		wunschRaum = cfg.Requests
	} else {
		fmt.Fprintln(errw, err)
	}

	if err := backchannel.Serve(st, version, root, wunschRaum); err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	return 0
}

// ensureBackchannel hält den mcp:-Eintrag der Bau-Config auf dem
// laufenden Binary. Muss vor dem Server-Start passieren — opencode
// liest die Config beim Hochfahren.
func ensureBackchannel(root string, logf func(string, ...any)) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("hasenbau: determining own path: %w", err)
	}
	update, err := bau.EnsureMCP(root, exe)
	switch {
	case err != nil:
		return err
	case update.Previous != "":
		// Sichtbar machen: der Eintrag zeigte auf ein anderes Binary,
		// und genau das hat schon einmal still den Rückkanal gekostet
		// (Hasenbau-2nq).
		logf("back channel in %s pointed at %s — corrected to %s",
			bau.OpencodeConfig, update.Previous, exe)
	case update.Written:
		logf("back channel registered in %s (%s)", bau.OpencodeConfig, exe)
	}
	return nil
}

// verifyBackchannel prüft nach dem Server-Start, ob der Rückkanal
// wirklich verbunden ist — und lässt keinen Hasen los, solange er es
// nicht ist.
//
// Ein gescheiterter MCP-Client fällt sonst nirgends auf: opencode
// kommt hoch, sagt nichts, und der Hase sieht `hasenbau_summary`
// einfach nicht. Der generierte Agent verspricht ihm die Werkzeuge
// trotzdem, also schreibt er seine Meldung als Fließtext, die Summary
// kommt aus dem Fallback und die Notizen entstehen gar nicht. Genau so
// ist Lauf 10 im Test-Bau gelaufen (Hasenbau-08u), und man sieht es
// dem Lauf nicht an: er steht als 'ok' in der DB.
func verifyBackchannel(ctx context.Context, baseURL string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	status, err := opencode.MCPStatus(ctx, opencode.New(baseURL))
	if err != nil {
		return fmt.Errorf("back channel: state not queryable: %w", err)
	}
	s, da := status[bau.MCPEintrag]
	switch {
	case !da:
		return fmt.Errorf("back channel: opencode knows no MCP server %q — is the entry in %s missing?",
			bau.MCPEintrag, bau.OpencodeConfig)
	case s.Status == opencode.MCPConnected:
		return nil
	case s.Error != "":
		return fmt.Errorf("back channel %q: %s — %s (entry in %s)",
			bau.MCPEintrag, s.Status, s.Error, bau.OpencodeConfig)
	default:
		return fmt.Errorf("back channel %q: %s (entry in %s)",
			bau.MCPEintrag, s.Status, bau.OpencodeConfig)
	}
}

// cleanupLaeufe schließt die Läufe ab, deren Prozess gestorben ist,
// bevor dieser hier selbst welche anlegt. Ohne das zählt `hasenbau
// status` für immer falsch und der Rückkanal findet keinen eindeutigen
// aktiven Lauf mehr (PLAN.md §5, §11.7).
func cleanupLaeufe(st *store.Store, logf func(string, ...any)) error {
	leichen, err := st.CleanupLaeufe()
	if err != nil {
		return err
	}
	for _, l := range leichen {
		logf("Lauf %d (%s, %s, since %s) cleaned up: %s",
			l.ID, l.Auftrag, l.Trigger,
			l.Started.Local().Format("2006-01-02 15:04"), l.Error)
	}
	return nil
}

// backfillToolCalls zieht die Tool-Calls der Läufe nach, die schon
// einen Trace haben, aber noch keine Zeilen (Hasenbau-4cx.1).
//
// Läuft bei jedem Start und ist trotzdem billig: die Auswahl ist die
// Bedingung, und sobald alles nachgezogen ist, liefert sie nichts mehr.
// Ein Fehler dabei hält niemanden auf — die Analyse ist dann eben
// lückenhaft, aber kein Lauf hängt daran.
func backfillToolCalls(st *store.Store, logf func(string, ...any)) {
	n, err := st.BackfillToolCalls(runner.ToolCallsFromTrace)
	if err != nil {
		logf("backfilling tool calls: %v", err)
		return
	}
	if n > 0 {
		logf("backfilled tool calls of %d Läufe", n)
	}
}

// loadAndGenerate lädt die Aufträge und schreibt die generierten
// Agenten (§6: beim Laden der Definitionen, nicht pro Lauf). Läuft vor
// dem Server-Start — dispose braucht es hier deshalb nicht.
func loadAndGenerate(root string) ([]*auftrag.Auftrag, error) {
	auftraege, err := auftrag.Load(root)
	if err != nil {
		return nil, err
	}
	// Veraltete valintent-Einträge nachziehen, bevor die Agenten
	// entstehen. Der Eintrag steht nie von selbst auf `outdated`, weil
	// nur review, test und release schreiben — wer die Datei danach
	// ändert, hinterlässt eine Zeile, die „actual" behauptet. In eine
	// Datei zu schreiben, die ohnehin gerade verändert wurde, nimmt
	// niemandem etwas weg (Moritz, 2026-08-13). Der Hash bleibt dabei
	// unangetastet — sonst wäre die fremde Änderung gesegnet.
	if nachgezogen, err := bau.AktualisiereValIntent(root); err != nil {
		log.Printf("valintent not updated: %v", err)
	} else if len(nachgezogen) > 0 {
		// Nur melden, wenn wirklich etwas nachgezogen wurde — das ist
		// der seltene Fall, und dann will man ihn sehen.
		log.Printf("valintent updated: %s", strings.Join(nachgezogen, ", "))
	}
	o, err := generierOptionen(root)
	if err != nil {
		return nil, err
	}
	for _, a := range auftraege {
		t, err := hase.Lade(root, a.Hase)
		if err != nil {
			return nil, err
		}
		if _, err := hase.SchreibeAgent(root, a, t, o); err != nil {
			return nil, err
		}
	}
	// Die Raum-Grenzen der Werkzeuge entstehen mit den Agenten und aus
	// derselben Quelle (Hasenbau-9w6): ein Werkzeug darf nie mehr als der
	// Hase, der es ruft. Angewendet werden sie im Bau-Plugin, das die
	// Datei beim Server-Start liest.
	if err := hase.SchreibeGrenzen(root, auftraege); err != nil {
		return nil, err
	}
	// Und das Plugin, das beides anwendet, gehört in dieselbe Runde: es
	// ist ein Artefakt wie die Agenten, und nur hier erreicht eine
	// gehärtete Fassung einen Bau, der vor Monaten angelegt wurde
	// (Hasenbau-uei). Ein Fehler dabei ist kein Grund weiterzumachen —
	// ohne das Plugin gibt es weder Wächter noch Werkzeug-Sandkasten.
	erg, err := bau.SchreibePlugin(root)
	if err != nil {
		return nil, err
	}
	if erg == bau.PluginErsetzt {
		// Laut sagen, was verschwunden ist: die Datei ist generiert, aber
		// wer sie angefasst hatte, soll erfahren, warum seine Änderung weg
		// ist — und wo sie hingehört.
		log.Printf("%s was not the version of this binary and has been replaced "+
			"(the file is generated; own hooks belong in a plugin of your own next to it)", bau.PluginDatei)
	}
	return auftraege, nil
}

// generierOptionen liest aus der Bau-Config, was der generierte Agent
// über seinen Bau wissen muss. Ein Config-Fehler ist hier kein Grund
// abzubrechen: er meldet sich an anderer Stelle laut genug, und der
// karge Agent ist der sichere Fall — er verweist auf nichts, was es
// womöglich nicht gibt.
func generierOptionen(root string) (hase.Optionen, error) {
	var o hase.Optionen
	// Ein Config-Fehler ist hier kein Grund abzubrechen: er meldet sich
	// an anderer Stelle laut genug, und der karge Agent ist der sichere
	// Fall — er verweist auf nichts, was es womöglich nicht gibt.
	if cfg, err := bau.LoadConfig(root); err == nil {
		o.ToolRequests = cfg.Requests != ""
	}
	// Bei den Werkzeugen ist es umgekehrt, und deshalb bricht es hier
	// ab: die Freigabe je Auftrag entsteht dadurch, dass der Generator
	// alles VERBIETET, was der Auftrag nicht nennt. Eine leere Liste
	// hiesse also „nichts zu verbieten" — und ein Werkzeug, das das
	// Plugin trotzdem registriert, stünde jedem Hasen offen. Ein Bau,
	// dessen Werkzeuge man nicht lesen kann, darf keine Agenten
	// generieren.
	alle, bereit, err := bau.ToolNamen(root)
	if err != nil {
		return o, err
	}
	o.Tools, o.ToolsBereit = alle, bereit
	return o, nil
}

// waitForServer blockiert, bis der Supervisor eine BaseURL meldet —
// Trigger werden erst danach scharf.
func waitForServer(ctx context.Context, sup *supervisor.Supervisor, timeout time.Duration) error {
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
	return fmt.Errorf("hasenbau daemon: opencode server did not come up")
}

func cmdDaemon(root string, errw io.Writer) int {
	logger := log.New(errw, "", log.LstdFlags)

	st, err := store.Open(dbPath(root))
	if err != nil {
		logger.Print(err)
		return 1
	}
	defer st.Close()

	if err := cleanupLaeufe(st, logger.Printf); err != nil {
		logger.Print(err)
		return 1
	}
	backfillToolCalls(st, logger.Printf)
	auftraege, err := loadAndGenerate(root)
	if err != nil {
		logger.Print(err)
		return 1
	}
	if err := ensureBackchannel(root, logger.Printf); err != nil {
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

	if err := waitForServer(ctx, sup, 60*time.Second); err != nil {
		logger.Print(err)
		stop()
		<-supFertig
		return 1
	}
	if err := verifyBackchannel(ctx, sup.BaseURL()); err != nil {
		logger.Print(err)
		stop()
		<-supFertig
		return 1
	}

	funnel := opencode.NewFunnel(sup.BaseURL, logger.Printf)
	funnel.Start(ctx)
	r := &runner.Runner{
		Root: root, BaseURL: sup.BaseURL, Store: st, Funnel: funnel, Logf: logger.Printf,
		Budget: bauBudget(root, st, logger.Printf),
	}
	lock := lauf.NewLock()

	sched, err := scheduler.New(auftraege, lock, func(a *auftrag.Auftrag) {
		// Kein eigenes Log: der Runner hat den Lauf schon mit Grund
		// gemeldet, und der Scheduler hat dem nichts hinzuzufügen
		// (Hasenbau-vwr).
		_, _ = r.Execute(ctx, a, "cron", "")
	}, logger.Printf)
	if err != nil {
		logger.Print(err)
		return 1
	}
	w, err := watcher.New(root, auftraege, lock, st, func(a *auftrag.Auftrag, input string) error {
		_, err := r.Execute(ctx, a, "watch", input)
		return err
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
		logger.Print("hasenbau daemon: shut down cleanly")
		return 0
	}
	logger.Printf("hasenbau daemon: %v", err)
	return 1
}

// cmdDig druckt den Trace eines Laufs als Baumeister-Input
// (Markdown) oder mit -json strukturiert (§8 Phase 2).
//
// Zuerst aus der Bau-DB: seit die Läufe ihren Trace beim Ende ablegen,
// braucht dig dafür keinen opencode-Server — wichtig, weil der
// Baumeister dig in einem Gang aufruft. Fehlt die Zeile (Altlauf),
// holt es den Trace beim Server und trägt sie nach. -live erzwingt den
// Server-Weg und liefert die ungekürzten Ausgaben.
func cmdDig(root string, args []string, out, errw io.Writer) int {
	fs := flag.NewFlagSet("dig", flag.ContinueOnError)
	fs.SetOutput(errw)
	alsJSON := fs.Bool("json", false, "trace as JSON instead of Markdown")
	live := fs.Bool("live", false, "fetch the trace from the server instead of the Bau DB")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(errw, "Usage: hasenbau dig [-json] [-live] <lauf-id|auftrag#n>")
		return 2
	}

	logger := log.New(errw, "", log.LstdFlags)
	st, err := store.Open(dbPath(root))
	if err != nil {
		logger.Print(err)
		return 1
	}
	defer st.Close()

	// `<auftrag>#<n>` ist die Auswahl eines Befunds: dann ist das
	// Material nicht ein Trace, sondern der gerechnete Befund samt den
	// Läufen, auf denen er beruht (Hasenbau-4cx.4).
	if sel, ok := parseSelector(fs.Arg(0)); ok {
		backfillToolCalls(st, logger.Printf)
		return digFinding(st, sel, *alsJSON, out, errw)
	}
	id, err := strconv.ParseInt(fs.Arg(0), 10, 64)
	if err != nil {
		fmt.Fprintf(errw, "hasenbau dig: %q is neither a Lauf ID nor a finding (<auftrag>#<n>)\n", fs.Arg(0))
		return 2
	}

	l, err := st.LaufByID(id)
	if err != nil {
		fmt.Fprintf(errw, "hasenbau dig: %v\n", err)
		return 1
	}
	if l.SessionID == "" {
		fmt.Fprintf(errw, "hasenbau dig: Lauf %d (%s, %s) has no session — the Lauf failed before the Hase (Gänge? prompt?).\n",
			l.ID, l.Auftrag, l.Status)
		return 1
	}

	trace, err := fetchTrace(root, st, l, *live, logger)
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
	fmt.Fprintf(out, "# Trace of Lauf %d — Auftrag %s (%s, %s)\n\n", l.ID, l.Auftrag, l.Trigger, l.Status)
	// Notizen aus dem Rückkanal zuerst: was der Hase selbst für
	// erwähnenswert hielt, ordnet den Trace darunter ein.
	notizen, err := st.Notes(l.ID)
	if err != nil {
		logger.Print(err)
		return 1
	}
	if len(notizen) > 0 {
		fmt.Fprint(out, "## Notes from the Hase\n\n")
		for _, n := range notizen {
			fmt.Fprintf(out, "- %s — %s\n", n.Written.Local().Format("15:04:05"), n.Text)
		}
		fmt.Fprintln(out)
	}
	fmt.Fprint(out, trace.Markdown())
	return 0
}

// fetchTrace liefert den Trace eines Laufs — aus der Bau-DB, wenn er
// dort liegt, sonst über einen eigenen opencode-Server. Was so geholt
// wurde, wird nachgetragen: das erste dig eines Altlaufs füllt seine
// Zeile, danach geht es ohne Server.
func fetchTrace(root string, st *store.Store, l *store.Lauf, live bool, logger *log.Logger) (*opencode.Trace, error) {
	if !live {
		roh, da, err := st.ReadTrace(l.ID)
		if err != nil {
			return nil, err
		}
		if da {
			var t opencode.Trace
			if err := json.Unmarshal(roh, &t); err != nil {
				return nil, fmt.Errorf("hasenbau dig: stored trace of Lauf %d is unreadable: %w", l.ID, err)
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

	trace, err := opencode.FetchTrace(ctx, opencode.New(sup.BaseURL()), l.SessionID)
	if err != nil {
		return nil, err
	}
	if !live {
		if roh, err := json.Marshal(trace); err == nil {
			if err := st.WriteTrace(l.ID, l.SessionID, roh); err != nil {
				logger.Printf("trace of Lauf %d not backfilled: %v", l.ID, err)
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
		fmt.Fprintln(errw, "Usage: hasenbau provider fetch [-yes] <provider-id>")
		return 2
	}
	fs := flag.NewFlagSet("provider fetch", flag.ContinueOnError)
	fs.SetOutput(errw)
	ja := fs.Bool("yes", false, "write without asking")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(errw, "Usage: hasenbau provider fetch [-yes] <provider-id>")
		return 2
	}
	id := fs.Arg(0)

	conf, err := provider.LoadConfig(root)
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	baseURL, err := conf.BaseURL(id)
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	key, err := provider.Key(id)
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	modelle, err := provider.Fetch(ctx, baseURL, key)
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}

	fmt.Fprintf(out, "%s: %d models from %s\n\n", id, len(modelle), baseURL)
	ae := conf.Merge(id, modelle)
	if ae.Empty() {
		fmt.Fprintln(out, "Bau config is up to date, nothing to do")
		return 0
	}
	fmt.Fprint(out, ae.Report())

	if !*ja {
		fmt.Fprintf(out, "\nWrite %s? [y/N] ", conf.Pfad)
		antwort, _ := bufio.NewReader(in).ReadString('\n')
		switch strings.ToLower(strings.TrimSpace(antwort)) {
		case "j", "ja", "y", "yes":
		default:
			fmt.Fprintln(out, "aborted, nothing written")
			return 0
		}
	}
	if err := conf.Write(); err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	fmt.Fprintf(out, "written: %s\n", conf.Pfad)
	return 0
}

// cmdStatus ist das Mini-Dashboard: **was liegt hier und was ist
// passiert** (Hasenbau-ha0.6). Keine Prüfungen — die gehören zu
// `describe bau`. Der Unterschied ist nicht kosmetisch: ein Dashboard,
// das mahnt, liest sich niemand mehr freiwillig an.
func cmdStatus(root string, out, errw io.Writer) int {
	st, err := store.Open(dbPath(root))
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	defer st.Close()

	counts, err := st.StatusCounts()
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	states, err := st.AuftragStates()
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}

	// Einmal geladen, zweimal gebraucht: für die Zählung „was liegt
	// hier" und für die Meldung der überwachten Aufträge.
	auftraege, defFehler := loadDefinitions(root)

	fmt.Fprintf(out, "Bau: %s\n", root)

	// Was der Bau kennt — die Frage „was liegt hier" beantwortet sich
	// nicht aus der Datenbank, sondern aus den Definitionen.
	if defFehler == nil {
		hasen := map[string]bool{}
		for _, a := range auftraege {
			hasen[a.Hase] = true
		}
		// Nur Dateien: entwurf/ ist ein Verzeichnis und zählt nicht als Gang.
		var gaenge int
		if eintraege, err := os.ReadDir(filepath.Join(root, "gaenge")); err == nil {
			for _, e := range eintraege {
				if !e.IsDir() {
					gaenge++
				}
			}
		}
		entwuerfe, _ := filepath.Glob(filepath.Join(root, "gaenge", "entwurf", "*"))
		fmt.Fprintf(out, "Knows: %d Aufträge, %d Hasen, %d Gänge",
			len(auftraege), len(hasen), gaenge)
		if len(entwuerfe) > 0 {
			fmt.Fprintf(out, ", %d drafts (unreviewed, not active)", len(entwuerfe))
		}
		fmt.Fprintln(out)
	}

	total := 0
	for _, n := range counts {
		total += n
	}
	fmt.Fprintf(out, "Läufe: %d total", total)
	for _, s := range []string{"running", "ok", "failed", "aborted"} {
		if counts[s] > 0 {
			fmt.Fprintf(out, ", %d %s", counts[s], s)
		}
	}
	fmt.Fprintln(out)

	if len(states) == 0 {
		fmt.Fprintln(out, "Aufträge: none have run yet")
		return 0
	}
	w := tabwriter.NewWriter(out, 2, 4, 2, ' ', 0)
	fmt.Fprintln(w, "AUFTRAG\tLAST LAUF\tLAST OK\tFAILURE STREAK")
	fmtTime := func(t *time.Time) string {
		if t == nil {
			return "-"
		}
		return t.Local().Format("2006-01-02 15:04")
	}
	for _, a := range states {
		fmt.Fprintf(w, "%s\t%s\t%s\t%d\n",
			a.Auftrag, fmtTime(a.LastLauf), fmtTime(a.LastOk), a.ErrorStreak)
	}
	w.Flush()

	// Was routinemäßig gemeldet wird (Hasenbau-4cx.3). Der Backfill
	// davor, damit hier dieselben Zahlen stehen wie unter `hasenbau
	// findings` — Altläufe mit Trace, aber ohne Aufrufzeilen zählten
	// sonst nicht mit.
	// Was gedrosselt ist und wann es weitergeht (Hasenbau-do0.4). Vor den
	// Befunden, weil „es staut sich planmäßig" die dringendere Auskunft
	// ist als „hier wäre ein Gang-Kandidat".
	writeBauDeckel(root, st, out)
	if defFehler == nil {
		writeDrosseln(drosselStand(root, st, auftraege), out)
	}

	if ueberwacht := monitoredNames(auftraege); defFehler == nil && len(ueberwacht) > 0 {
		backfillToolCalls(st, func(format string, args ...any) {
			fmt.Fprintf(errw, format+"\n", args...)
		})
		writeMonitored(st, ueberwacht, len(auftraege), out)
	}

	// Die jüngsten Läufe gehören dazu: „was ist passiert" heißt selten
	// „wie viele", meistens „welche".
	if laeufe, err := st.RecentLaeufe(5); err == nil && len(laeufe) > 0 {
		fmt.Fprint(out, "\nThe most recent Läufe\n")
		writeLaufTable(out, laeufe)
	}
	fmt.Fprintln(out, "\nIs the Bau in order? `hasenbau describe bau`")
	return 0
}
