// lauf.go: der einmalige Lauf aus der CLI. `hasenbau lauf` und
// `hasenbau baumeister` teilen sich denselben Aufbau — DB auf,
// verwaiste Läufe schließen, Definitionen laden, Rückkanal eintragen,
// eigener Server hoch — und unterscheiden sich nur darin, welchen
// Auftrag sie womit ansetzen.
package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os/signal"
	"syscall"

	"github.com/Moritz-Dorn/Hasenbau/internal/auftrag"
	"github.com/Moritz-Dorn/Hasenbau/internal/opencode"
	"github.com/Moritz-Dorn/Hasenbau/internal/runner"
	"github.com/Moritz-Dorn/Hasenbau/internal/store"
	"github.com/Moritz-Dorn/Hasenbau/internal/supervisor"
)

// laufContext ist der Aufbau eines CLI-Laufs. Bewusst zweistufig:
// openLaufContext macht nur das Billige (DB, Definitionen), den
// Server startet StartServer. So scheitert ein Tippfehler im
// Auftragsnamen, bevor irgendein opencode hochfährt.
type laufContext struct {
	Root      string
	Store     *store.Store
	Auftraege []*auftrag.Auftrag
	Ctx       context.Context
	Runner    *runner.Runner // erst nach StartServer

	logger *log.Logger
	sup    *supervisor.Supervisor
	stop   context.CancelFunc
}

func openLaufContext(root string, logger *log.Logger) (*laufContext, error) {
	st, err := store.Open(dbPath(root))
	if err != nil {
		return nil, err
	}
	if err := cleanupLaeufe(st, logger.Printf); err != nil {
		st.Close()
		return nil, err
	}
	auftraege, err := loadAndGenerate(root)
	if err != nil {
		st.Close()
		return nil, err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	return &laufContext{Root: root, Store: st, Auftraege: auftraege, Ctx: ctx, logger: logger, stop: stop}, nil
}

// Auftrag sucht einen geladenen Auftrag. Der Fehler zählt auf, was es
// stattdessen gibt — bei einem Tippfehler ist das die halbe Antwort.
func (k *laufContext) Auftrag(name string) (*auftrag.Auftrag, error) {
	for _, a := range k.Auftraege {
		if a.Name == name {
			return a, nil
		}
	}
	var namen []string
	for _, a := range k.Auftraege {
		namen = append(namen, a.Name)
	}
	if len(namen) == 0 {
		return nil, fmt.Errorf("unbekannter Auftrag %q — der Bau hat keine Aufträge unter auftraege/", name)
	}
	return nil, fmt.Errorf("unbekannter Auftrag %q — vorhanden: %v", name, namen)
}

// StartServer trägt den Rückkanal ein, fährt den eigenen
// opencode-Server hoch und hängt den Funnel dran. Kein Konflikt mit
// einem laufenden Daemon: beide binden eigene Ports, die DB teilt
// SQLite im WAL-Modus.
func (k *laufContext) StartServer() error {
	if err := ensureBackchannel(k.Root, k.logger.Printf); err != nil {
		return err
	}
	sup, err := supervisor.New(supervisor.Config{BauDir: k.Root, Logf: k.logger.Printf})
	if err != nil {
		return err
	}
	if err := sup.Start(k.Ctx); err != nil {
		return err
	}
	k.sup = sup
	if err := verifyBackchannel(k.Ctx, sup.BaseURL()); err != nil {
		return err
	}

	funnel := opencode.NewFunnel(sup.BaseURL, k.logger.Printf)
	funnel.Start(k.Ctx)
	k.Runner = &runner.Runner{
		Root: k.Root, BaseURL: sup.BaseURL, Store: k.Store,
		Funnel: funnel, Logf: k.logger.Printf,
	}
	return nil
}

// Close gibt frei, was offen ist — in umgekehrter Reihenfolge.
//
// Erst der Kontext, dann der Server: der Funnel hängt am Event-Stream
// und meldet jeden Abriss. Stirbt der Server zuerst, ist das ein Abriss
// wie jeder andere, und am Ende jedes Laufs stand „Event-Stream weg
// (unexpected EOF), neu verbinden in 1s" — ein Fehler, der keiner war.
// Zuerst abgebrochen, weiß der Funnel, dass er gemeint ist, und
// schweigt (Hasenbau-vwr).
func (k *laufContext) Close() {
	k.stop()
	if k.sup != nil {
		k.sup.Stop()
	}
	k.Store.Close()
}

// cmdLauf triggert einen Auftrag manuell.
func cmdLauf(root, name, input string, errw io.Writer) int {
	logger := log.New(errw, "", log.LstdFlags)
	k, err := openLaufContext(root, logger)
	if err != nil {
		logger.Print(err)
		return 1
	}
	defer k.Close()

	ziel, err := k.Auftrag(name)
	if err != nil {
		fmt.Fprintf(errw, "hasenbau lauf: %v\n", err)
		return 1
	}
	if err := k.StartServer(); err != nil {
		logger.Print(err)
		return 1
	}
	// Nicht noch einmal ausgeben: der Runner hat den Fehlschlag schon
	// samt Grund geloggt, und zwar über denselben logger (Hasenbau-vwr).
	if _, err := k.Runner.Execute(k.Ctx, ziel, "manual", input); err != nil {
		return 1
	}
	return 0
}
