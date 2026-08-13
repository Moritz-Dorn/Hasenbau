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

const usage = `hasenbau — Daemon, der opencode headless orchestriert.

Befehle:
  init <pfad>           leeren Bau anlegen (nicht-destruktiv, idempotent)
  fix                   fehlende Teile des Baus ergänzen — dasselbe für
                        einen Bau, den es schon gibt
  daemon                Daemon starten (Trigger + opencode-Server)
  lauf <auftrag> [in]   Auftrag manuell triggern; [in] ist die
                        auslösende Datei (Bau-relativ, nur watch)
  get <ressource>       zeigen, was der Bau kennt (auftraege, hasen,
                        gaenge, laeufe, lauf, provider)
  describe <res> <name> ein Objekt im Detail (auftrag, gang, hase, lauf)
  new <res> <name>      Gerüst anlegen (auftrag, hase)
  dig [-json] <ziel>    Material für den Baumeister: der Trace eines
                        Laufs, oder <auftrag>#<n> für einen Befund
  findings <auftrag>    was sich über die Läufe rechnen lässt: Gang-
                        Kandidaten, Reibung, Ausreißer (kein Modell)
  baumeister [-finding N] <ziel>
                        Baumeister-Auftrag (aus hasenbau.yaml) ansetzen —
                        auf einen Lauf (Lauf-ID oder Auftrag) oder mit
                        -finding auf einen Befund über viele Läufe
  tool <verb> <name>    ein Schmied-Werkzeug durch seine drei Stufen
                        führen: review (lesen und verantworten), test
                        (ausführen und zeigen), release (freigeben).
                        Jede Stufe setzt die vorige voraus
  provider fetch <id>   Modell-Liste beim Provider-Endpoint holen
  status                Zustand des Baus zeigen
  mcp                   Rückkanal über stdio bedienen (startet opencode
                        selbst; nicht von Hand aufrufen)
  sandbox-vorfall       meldet einen Werkzeug-Aufruf, der aus der Sandbox
                        führt (ruft der Wächter im opencode-Server auf;
                        nicht von Hand aufrufen)

Globale Flags (vor dem Befehl):
  -bau <pfad>      Root des Baus (Default: .)
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
		fmt.Fprintf(errw, "hasenbau: Bau-Path: %v\n", err)
		return 1
	}

	switch rest[0] {
	case "init":
		if len(rest) != 2 {
			fmt.Fprintln(errw, "Aufruf: hasenbau init <pfad>")
			return 2
		}
		return cmdInit(rest[1], out, errw)
	case "fix":
		if len(rest) != 1 {
			fmt.Fprintln(errw, "Aufruf: hasenbau [-bau <pfad>] fix")
			return 2
		}
		return cmdFix(bau, out, errw)
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
	case "sandbox-vorfall":
		return cmdSandboxVorfall(bau, rest[1:], out, errw)
	default:
		fmt.Fprintf(errw, "hasenbau: unbekannter Befehl %q\n\n%s", rest[0], usage)
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
		fmt.Fprintf(errw, "hasenbau fix: %s ist kein Bau (hasenbau.yaml fehlt) — einen neuen legt `hasenbau init %s` an.\n", pfad, pfad)
		return 1
	}
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(errw, "hasenbau: eigenen Pfad bestimmen: %v\n", err)
		return 1
	}
	ergaenzt, err := bau.Init(pfad, exe)
	for _, c := range ergaenzt {
		fmt.Fprintf(out, "ergänzt: %s\n", c)
	}
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	if _, err := loadAndGenerate(pfad); err != nil {
		fmt.Fprintf(errw, "hasenbau fix: Agenten nicht generiert: %v\n", err)
	}
	if len(ergaenzt) == 0 {
		fmt.Fprintln(out, "Bau ist vollständig, nichts zu tun")
	} else {
		fmt.Fprintln(out, "Was davon inhaltlich stimmt, sagt `hasenbau describe bau`.")
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
		fmt.Fprintf(errw, "hasenbau: Bau-Pfad: %v\n", err)
		return 1
	}
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(errw, "hasenbau: eigenen Pfad bestimmen: %v\n", err)
		return 1
	}
	created, err := bau.Init(pfad, exe)
	for _, c := range created {
		fmt.Fprintf(out, "angelegt: %s\n", c)
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
		fmt.Fprintf(errw, "hasenbau init: Agenten noch nicht generiert: %v\n", err)
	}
	if len(created) == 0 {
		fmt.Fprintln(out, "Bau ist vollständig, nichts zu tun")
	} else {
		fmt.Fprintf(out, "Rückkanal eingetragen: %s (wird bei jedem Start auf das laufende Binary korrigiert).\n", exe)
		fmt.Fprintln(out, "Hinweis: custom Provider brauchen ihr Gerüst (npm, options.baseURL) im provider:-Block von .opencode-home/opencode/opencode.json — auth.json teilt nur Credentials (PLAN.md §3).")
		fmt.Fprintln(out, "Die Modell-Liste holt danach `hasenbau provider fetch <provider-id>`.")
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
		logf("bau-deckel: %v — es gilt keiner", err)
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
		return fmt.Errorf("hasenbau: eigenen Pfad bestimmen: %w", err)
	}
	update, err := bau.EnsureMCP(root, exe)
	switch {
	case err != nil:
		return err
	case update.Previous != "":
		// Sichtbar machen: der Eintrag zeigte auf ein anderes Binary,
		// und genau das hat schon einmal still den Rückkanal gekostet
		// (Hasenbau-2nq).
		logf("Rückkanal in %s zeigte auf %s — korrigiert auf %s",
			bau.OpencodeConfig, update.Previous, exe)
	case update.Written:
		logf("Rückkanal in %s eingetragen (%s)", bau.OpencodeConfig, exe)
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
		return fmt.Errorf("Rückkanal: Zustand nicht abfragbar: %w", err)
	}
	s, da := status[bau.MCPEintrag]
	switch {
	case !da:
		return fmt.Errorf("Rückkanal: opencode kennt keinen MCP-Server %q — fehlt der Eintrag in %s?",
			bau.MCPEintrag, bau.OpencodeConfig)
	case s.Status == opencode.MCPConnected:
		return nil
	case s.Error != "":
		return fmt.Errorf("Rückkanal %q: %s — %s (Eintrag in %s)",
			bau.MCPEintrag, s.Status, s.Error, bau.OpencodeConfig)
	default:
		return fmt.Errorf("Rückkanal %q: %s (Eintrag in %s)",
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
		logf("Lauf %d (%s, %s, seit %s) aufgeräumt: %s",
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
		logf("Tool-Calls nachziehen: %v", err)
		return
	}
	if n > 0 {
		logf("Tool-Calls von %d Lauf/Läufen nachgezogen", n)
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
		log.Printf("valintent nicht nachgezogen: %v", err)
	} else if len(nachgezogen) > 0 {
		// Nur melden, wenn wirklich etwas nachgezogen wurde — das ist
		// der seltene Fall, und dann will man ihn sehen.
		log.Printf("valintent nachgezogen: %s", strings.Join(nachgezogen, ", "))
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
		logger.Print("hasenbau daemon: sauber beendet")
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
	alsJSON := fs.Bool("json", false, "Trace als JSON statt Markdown")
	live := fs.Bool("live", false, "Trace beim Server holen statt aus der Bau-DB")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(errw, "Aufruf: hasenbau dig [-json] [-live] <lauf-id|auftrag#n>")
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
		fmt.Fprintf(errw, "hasenbau dig: %q ist weder eine Lauf-ID noch ein Befund (<auftrag>#<n>)\n", fs.Arg(0))
		return 2
	}

	l, err := st.LaufByID(id)
	if err != nil {
		fmt.Fprintf(errw, "hasenbau dig: %v\n", err)
		return 1
	}
	if l.SessionID == "" {
		fmt.Fprintf(errw, "hasenbau dig: Lauf %d (%s, %s) hat keine Session — der Lauf scheiterte vor dem Hasen (Gänge? Prompt?).\n",
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
	fmt.Fprintf(out, "# Trace Lauf %d — Auftrag %s (%s, %s)\n\n", l.ID, l.Auftrag, l.Trigger, l.Status)
	// Notizen aus dem Rückkanal zuerst: was der Hase selbst für
	// erwähnenswert hielt, ordnet den Trace darunter ein.
	notizen, err := st.Notes(l.ID)
	if err != nil {
		logger.Print(err)
		return 1
	}
	if len(notizen) > 0 {
		fmt.Fprint(out, "## Notizen des Hasen\n\n")
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
				return nil, fmt.Errorf("hasenbau dig: abgelegter Trace von Lauf %d ist unlesbar: %w", l.ID, err)
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

	fmt.Fprintf(out, "%s: %d Modelle von %s\n\n", id, len(modelle), baseURL)
	ae := conf.Merge(id, modelle)
	if ae.Empty() {
		fmt.Fprintln(out, "Bau-Config ist auf Stand, nichts zu tun")
		return 0
	}
	fmt.Fprint(out, ae.Report())

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
	if err := conf.Write(); err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	fmt.Fprintf(out, "geschrieben: %s\n", conf.Pfad)
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
		fmt.Fprintf(out, "Kennt: %d Aufträge, %d Hasen, %d Gänge",
			len(auftraege), len(hasen), gaenge)
		if len(entwuerfe) > 0 {
			fmt.Fprintf(out, ", %d Entwürfe (ungeprüft, nicht aktiv)", len(entwuerfe))
		}
		fmt.Fprintln(out)
	}

	total := 0
	for _, n := range counts {
		total += n
	}
	fmt.Fprintf(out, "Läufe: %d gesamt", total)
	for _, s := range []string{"running", "ok", "failed", "aborted"} {
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
		fmt.Fprint(out, "\nDie letzten Läufe\n")
		writeLaufTable(out, laeufe)
	}
	fmt.Fprintln(out, "\nIst der Bau in Ordnung? `hasenbau describe bau`")
	return 0
}
