// Package watcher feuert watch-Trigger (PLAN.md §2, §6) und setzt die
// drei Fallen aus §7 um: Größenstabilität gegen partielle Writes, den
// gesehen-Backstop gegen Doppelverarbeitung und das Nachholen beim
// Start. Die Overlap-Sperre ist die trigger-übergreifende lauf.Sperre.
package watcher

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Moritz-Dorn/Hasenbau/internal/auftrag"
	"github.com/Moritz-Dorn/Hasenbau/internal/lauf"
	"github.com/fsnotify/fsnotify"
)

// DefaultDebounce greift für Aufträge ohne eigenes debounce:.
const DefaultDebounce = 2 * time.Second

// AusfuehrFunc führt einen Lauf aus; input ist der Bau-relative Pfad
// der auslösenden Datei. Die Sperre ist beim Aufruf belegt. Rückgabe
// nil ⇒ der Input gilt als verarbeitet und wandert in die
// gesehen-Tabelle (Backstop, §7).
type AusfuehrFunc func(a *auftrag.Auftrag, input string) error

// SeenStore ist der Idempotenz-Backstop; *store.Store erfüllt ihn.
type SeenStore interface {
	IsSeen(auftrag, hash string) (bool, error)
	MarkSeen(auftrag, hash string) error
}

type Watcher struct {
	root       string
	auftraege  []*auftrag.Auftrag // nur watch-Trigger
	sperre     *lauf.Sperre
	gesehen    SeenStore
	ausfuehren AusfuehrFunc
	logf       func(format string, args ...any)

	fsw     *fsnotify.Watcher
	wg      sync.WaitGroup
	cancel  context.CancelFunc
	pending sync.Map // auftrag+"\x00"+pfad → struct{}{}, entprellt Mehrfach-Events
}

// New baut den Watcher für alle Aufträge mit watch-Trigger. Die
// Watch-Verzeichnisse (= Verzeichnisanteil des Globs) werden angelegt —
// sie sind die Eingangs-Räume der Aufträge.
func New(root string, auftraege []*auftrag.Auftrag, sperre *lauf.Sperre, gesehen SeenStore, ausfuehren AusfuehrFunc, logf func(format string, args ...any)) (*Watcher, error) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	w := &Watcher{root: root, sperre: sperre, gesehen: gesehen, ausfuehren: ausfuehren, logf: logf}
	for _, a := range auftraege {
		if a.Trigger.Watch != "" {
			w.auftraege = append(w.auftraege, a)
		}
	}
	return w, nil
}

// Start legt Watch-Verzeichnisse an, registriert fsnotify und holt
// bereits vorhandene Dateien nach (§7: was beim Daemon-Start in
// sources/ liegt, wurde nie verarbeitet oder blieb nach einem Crash).
func (w *Watcher) Start(ctx context.Context) error {
	ctx, w.cancel = context.WithCancel(ctx)

	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("watcher: %w", err)
	}
	w.fsw = fsw

	verzeichnisse := map[string][]*auftrag.Auftrag{}
	for _, a := range w.auftraege {
		dir := filepath.Dir(a.Trigger.Watch)
		if err := os.MkdirAll(filepath.Join(w.root, dir), 0o755); err != nil {
			return fmt.Errorf("watcher: %s anlegen: %w", dir, err)
		}
		verzeichnisse[dir] = append(verzeichnisse[dir], a)
	}
	for dir := range verzeichnisse {
		if err := fsw.Add(filepath.Join(w.root, dir)); err != nil {
			return fmt.Errorf("watcher: %s beobachten: %w", dir, err)
		}
	}

	w.wg.Add(1)
	go w.lausche(ctx, verzeichnisse)

	// Nachholen: vorhandene Dateien als Trigger behandeln.
	for _, a := range w.auftraege {
		treffer, err := filepath.Glob(filepath.Join(w.root, a.Trigger.Watch))
		if err != nil {
			return fmt.Errorf("watcher: glob %s: %w", a.Trigger.Watch, err)
		}
		for _, abs := range treffer {
			rel, err := filepath.Rel(w.root, abs)
			if err != nil {
				continue
			}
			w.starteVerarbeitung(ctx, a, rel)
		}
	}
	return nil
}

// Stop beendet das Lauschen und wartet auf alle Verarbeitungs-Goroutinen
// (laufende ausfuehren-Aufrufe eingeschlossen).
func (w *Watcher) Stop() {
	if w.cancel != nil {
		w.cancel()
	}
	if w.fsw != nil {
		w.fsw.Close()
	}
	w.wg.Wait()
}

func (w *Watcher) lausche(ctx context.Context, verzeichnisse map[string][]*auftrag.Auftrag) {
	defer w.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-w.fsw.Events:
			if !ok {
				return
			}
			if !ev.Has(fsnotify.Create) && !ev.Has(fsnotify.Write) {
				continue
			}
			rel, err := filepath.Rel(w.root, ev.Name)
			if err != nil {
				continue
			}
			for _, a := range verzeichnisse[filepath.Dir(rel)] {
				if passt, _ := filepath.Match(a.Trigger.Watch, rel); passt {
					w.starteVerarbeitung(ctx, a, rel)
				}
			}
		case err, ok := <-w.fsw.Errors:
			if !ok {
				return
			}
			w.logf("watcher: %v", err)
		}
	}
}

// starteVerarbeitung entprellt pro (Auftrag, Datei) und verarbeitet
// asynchron: Größenstabilität abwarten, gesehen prüfen, Sperre nehmen,
// ausführen, bei Erfolg gesehen merken.
func (w *Watcher) starteVerarbeitung(ctx context.Context, a *auftrag.Auftrag, rel string) {
	schluessel := a.Name + "\x00" + rel
	if _, laeuft := w.pending.LoadOrStore(schluessel, struct{}{}); laeuft {
		return
	}
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		defer w.pending.Delete(schluessel)
		if err := w.verarbeite(ctx, a, rel); err != nil && ctx.Err() == nil {
			w.logf("watcher: auftrag %s, input %s: %v", a.Name, rel, err)
		}
	}()
}

func (w *Watcher) verarbeite(ctx context.Context, a *auftrag.Auftrag, rel string) error {
	debounce := a.Trigger.Debounce
	if debounce == 0 {
		debounce = DefaultDebounce
	}

	stabil, err := w.warteAufStabileGroesse(ctx, rel, debounce)
	if err != nil || !stabil {
		return err // Datei verschwand (z.B. von einem anderen Lauf verschoben) oder ctx zu
	}

	hash, err := sha256Datei(filepath.Join(w.root, rel))
	if err != nil {
		return fmt.Errorf("hash: %w", err)
	}
	schon, err := w.gesehen.IsSeen(a.Name, hash)
	if err != nil {
		return err
	}
	if schon {
		w.logf("watcher: auftrag %s, input %s: schon verarbeitet (gesehen-Backstop) — übersprungen", a.Name, rel)
		return nil
	}

	// Sperre nehmen; läuft der Auftrag gerade, warten wir — die Datei
	// liegt ja schon da, der Trigger darf nicht verloren gehen.
	for !w.sperre.Belege(a.Name) {
		w.logf("watcher: auftrag %s läuft noch — input %s wartet", a.Name, rel)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(debounce):
		}
	}
	defer w.sperre.GibFrei(a.Name)

	// Zwischen Warten und Sperre kann ein anderer Lauf die Datei
	// verarbeitet haben (Move nach archiv/) — noch mal hinsehen.
	if _, err := os.Stat(filepath.Join(w.root, rel)); err != nil {
		return nil
	}

	if err := w.ausfuehren(a, rel); err != nil {
		return err
	}
	return w.gesehen.MarkSeen(a.Name, hash)
}

// warteAufStabileGroesse wartet, bis die Datei über zwei Ticks dieselbe
// Größe hat (§7, partielle Writes). false = Datei verschwand.
func (w *Watcher) warteAufStabileGroesse(ctx context.Context, rel string, tick time.Duration) (bool, error) {
	abs := filepath.Join(w.root, rel)
	letzte := int64(-1)
	for {
		fi, err := os.Stat(abs)
		if err != nil {
			return false, nil
		}
		if fi.Size() == letzte {
			return true, nil
		}
		letzte = fi.Size()
		select {
		case <-ctx.Done():
			return false, nil
		case <-time.After(tick):
		}
	}
}

func sha256Datei(pfad string) (string, error) {
	f, err := os.Open(pfad)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
