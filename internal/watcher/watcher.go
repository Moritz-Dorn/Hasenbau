// Package watcher feuert watch-Trigger (PLAN.md §2, §6) und setzt die
// drei Fallen aus §7 um: Größenstabilität gegen partielle Writes, den
// gesehen-Backstop gegen Doppelverarbeitung und das Nachholen beim
// Start. Die Overlap-Sperre ist die trigger-übergreifende lauf.Lock.
//
// Verarbeitet wird je Auftrag von genau einem Arbeiter, ältester Input
// zuerst (Hasenbau-do0.1). Die Warteschlange ist dabei das Dateisystem
// selbst: ein Input bleibt liegen, bis ein geglückter Lauf ihn per
// `after: move` wegräumt, und beim Start liest der Glob alles wieder
// ein. Deshalb übersteht ein voller Eingang jeden Neustart, ohne dass
// irgendwo ein Zustand mitgeschrieben werden müsste.
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

// ExecFunc führt einen Lauf aus; input ist der Bau-relative Pfad
// der auslösenden Datei. Die Sperre ist beim Aufruf belegt. Rückgabe
// nil ⇒ der Input gilt als verarbeitet und wandert in die
// gesehen-Tabelle (Backstop, §7).
type ExecFunc func(a *auftrag.Auftrag, input string) error

// SeenStore ist der Idempotenz-Backstop; *store.Store erfüllt ihn.
type SeenStore interface {
	IsSeen(auftrag, hash string) (bool, error)
	MarkSeen(auftrag, hash string) error
}

type Watcher struct {
	root       string
	auftraege  []*auftrag.Auftrag // nur watch-Trigger
	lock       *lauf.Lock
	gesehen    SeenStore
	ausfuehren ExecFunc
	logf       func(format string, args ...any)

	fsw      *fsnotify.Watcher
	wg       sync.WaitGroup
	cancel   context.CancelFunc
	arbeiter map[string]*arbeiter // Auftragsname → sein einziger Verarbeiter
}

// arbeiter ist der einzige Verarbeiter eines Auftrags (Hasenbau-do0.1).
// Vorher bekam jede Datei ihre eigene Goroutine, und alle drehten in der
// Overlap-Sperre: 200 Dateien im Glob hießen 200 Goroutinen, von denen
// 199 im Sekundentakt meldeten, dass sie warten. Sie taten dasselbe wie
// jetzt einer — nur lauter und in zufälliger Reihenfolge, denn
// Go-Mutexe sind nicht FIFO.
//
// Ein Arbeiter je Auftrag ist außerdem die Stelle, an der eine Drossel
// ansetzen kann: „jetzt nicht" sagt man einem, nicht zweihundert.
type arbeiter struct {
	a *auftrag.Auftrag

	mu       sync.Mutex
	wartend  map[string]bool // Bau-relative Pfade, noch nicht begonnen
	inArbeit string          // läuft gerade; hält Mehrfach-Events fern
	wecker   chan struct{}   // gepuffert auf 1: ein Signal genügt
}

func neuerArbeiter(a *auftrag.Auftrag) *arbeiter {
	return &arbeiter{a: a, wartend: map[string]bool{}, wecker: make(chan struct{}, 1)}
}

// melde trägt einen Input ein und weckt den Arbeiter. Mehrfach-Events
// für dieselbe Datei fallen hier weg — auch während sie verarbeitet
// wird, sonst liefe sie hinterher ein zweites Mal.
func (arb *arbeiter) melde(rel string) {
	arb.mu.Lock()
	if arb.wartend[rel] || arb.inArbeit == rel {
		arb.mu.Unlock()
		return
	}
	arb.wartend[rel] = true
	arb.mu.Unlock()

	select {
	case arb.wecker <- struct{}{}:
	default: // schon geweckt — der Arbeiter leert ohnehin die ganze Menge
	}
}

// naechster greift den ältesten wartenden Input heraus. Ältester zuerst,
// weil das die Reihenfolge ist, die ein Mensch erwartet, wenn er einen
// Stapel abarbeiten lässt; verschwundene Einträge fallen dabei weg.
func (arb *arbeiter) naechster(root string) (string, bool) {
	arb.mu.Lock()
	defer arb.mu.Unlock()

	var best string
	var bestZeit time.Time
	for rel := range arb.wartend {
		fi, err := os.Stat(filepath.Join(root, rel))
		if err != nil {
			// Weg — etwa von einem anderen Lauf nach archiv/ verschoben.
			delete(arb.wartend, rel)
			continue
		}
		// Bei gleicher mtime entscheidet der Pfad. Ohne das entschiede die
		// Map-Iteration, und zwei Läufe über denselben Stapel kämen in
		// verschiedener Reihenfolge heraus.
		if best == "" || fi.ModTime().Before(bestZeit) ||
			(fi.ModTime().Equal(bestZeit) && rel < best) {
			best, bestZeit = rel, fi.ModTime()
		}
	}
	if best == "" {
		return "", false
	}
	delete(arb.wartend, best)
	arb.inArbeit = best
	return best, true
}

func (arb *arbeiter) fertig() {
	arb.mu.Lock()
	arb.inArbeit = ""
	arb.mu.Unlock()
}

// New baut den Watcher für alle Aufträge mit watch-Trigger. Die
// Watch-Verzeichnisse (= Verzeichnisanteil des Globs) werden angelegt —
// sie sind die Eingangs-Räume der Aufträge.
func New(root string, auftraege []*auftrag.Auftrag, lock *lauf.Lock, gesehen SeenStore, ausfuehren ExecFunc, logf func(format string, args ...any)) (*Watcher, error) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	w := &Watcher{
		root: root, lock: lock, gesehen: gesehen, ausfuehren: ausfuehren, logf: logf,
		arbeiter: map[string]*arbeiter{},
	}
	for _, a := range auftraege {
		if a.Trigger.Watch != "" {
			w.auftraege = append(w.auftraege, a)
			w.arbeiter[a.Name] = neuerArbeiter(a)
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

	// Die Arbeiter zuerst: sie blockieren am Wecker, und der ist
	// gepuffert — ein Nachhol-Fund geht auch dann nicht verloren, wenn er
	// vor dem ersten Schlafen des Arbeiters eintrifft.
	for _, a := range w.auftraege {
		w.wg.Add(1)
		go w.arbeite(ctx, w.arbeiter[a.Name])
	}

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
			w.arbeiter[a.Name].melde(rel)
		}
	}
	return nil
}

// arbeite ist die Schleife eines Arbeiters: aufwachen, die gemeldeten
// Inputs nacheinander abarbeiten, wieder schlafen. Der Wecker wird beim
// Leeren nicht abgefragt — hereinkommende Meldungen findet die nächste
// Runde von naechster() ohnehin, und ein überzähliges Signal kostet
// höchstens ein Aufwachen ins Leere.
func (w *Watcher) arbeite(ctx context.Context, arb *arbeiter) {
	defer w.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case <-arb.wecker:
		}
		for {
			rel, ok := arb.naechster(w.root)
			if !ok {
				break
			}
			err := w.verarbeite(ctx, arb.a, rel)
			arb.fertig()
			if err != nil && ctx.Err() == nil {
				w.logf("watcher: auftrag %s, input %s: %v", arb.a.Name, rel, err)
			}
			if ctx.Err() != nil {
				return
			}
		}
	}
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
					w.arbeiter[a.Name].melde(rel)
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

// verarbeite führt einen einzelnen Input aus: Größenstabilität abwarten,
// gesehen prüfen, Sperre nehmen, ausführen, bei Erfolg gesehen merken.
// Läuft im Arbeiter des Auftrags, also nie zweimal nebeneinander.
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
	// liegt ja schon da, der Trigger darf nicht verloren gehen. Der
	// Gegner ist hier nur noch ein anderer Trigger desselben Auftrags
	// (cron oder `hasenbau lauf`); die übrigen Inputs stehen in der Menge
	// des Arbeiters und drängeln nicht mit.
	//
	// Gemeldet wird das einmal, nicht bei jedem Versuch: die Wiederholung
	// trug keine neue Information und füllte bei einem vollen Eingang das
	// Log schneller, als es jemand lesen konnte.
	if !w.lock.Acquire(a.Name) {
		w.logf("watcher: auftrag %s läuft noch — input %s wartet", a.Name, rel)
		for !w.lock.Acquire(a.Name) {
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(debounce):
			}
		}
	}
	defer w.lock.Release(a.Name)

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
