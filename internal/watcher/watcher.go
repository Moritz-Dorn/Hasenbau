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
	"io/fs"
	"os"
	"path/filepath"
	"strings"
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

// Store ist, was der Watcher aus der Bau-DB braucht: den
// Idempotenz-Backstop (§7) und die vergangenen Läufe für den Deckel
// (§6). *store.Store erfüllt ihn.
type Store interface {
	IsSeen(auftrag, hash string) (bool, error)
	MarkSeen(auftrag, hash string) error

	// LaeufeSince liefert die Startzeitpunkte aller Läufe des Auftrags
	// seit `since`, älteste zuerst.
	LaeufeSince(auftrag string, since time.Time) ([]time.Time, error)
}

type Watcher struct {
	root       string
	auftraege  []*auftrag.Auftrag // nur watch-Trigger
	lock       *lauf.Lock
	db         Store
	ausfuehren ExecFunc
	logf       func(format string, args ...any)

	fsw      *fsnotify.Watcher
	wg       sync.WaitGroup
	cancel   context.CancelFunc
	arbeiter map[string]*arbeiter // Auftragsname → sein einziger Verarbeiter

	// jetzt liefert die Zeit. Im Test steuerbar, weil ein Zeitfenster
	// gegen die Wanduhr zu prüfen den Test davon abhängig machte, zu
	// welcher Tageszeit er läuft (vgl. Hasenbau-eav).
	jetzt func() time.Time

	// maxWarten begrenzt einen einzelnen Schlaf. Der Rest wird nach dem
	// Aufwachen neu gerechnet — ein Tagesfenster hängt an der Wanduhr,
	// und die springt: NTP, Sommerzeit, ein zugeklappter Laptop. Ein
	// einziger Timer über acht Stunden wachte dann eine Stunde daneben
	// auf. Neu rechnen kostet eine Abfrage je Minute.
	maxWarten time.Duration
}

// DefaultMaxWarten ist die Obergrenze eines einzelnen Schlafs.
const DefaultMaxWarten = time.Minute

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
func New(root string, auftraege []*auftrag.Auftrag, lock *lauf.Lock, db Store, ausfuehren ExecFunc, logf func(format string, args ...any)) (*Watcher, error) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	w := &Watcher{
		root: root, lock: lock, db: db, ausfuehren: ausfuehren, logf: logf,
		arbeiter:  map[string]*arbeiter{},
		jetzt:     time.Now,
		maxWarten: DefaultMaxWarten,
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

	for _, a := range w.auftraege {
		// Angelegt wird bei einem flachen Muster das Verzeichnis, in dem
		// die Dateien liegen sollen (also samt fester Unterverzeichnisse),
		// bei einem rekursiven nur die Wurzel — was darunter entsteht,
		// benennt der Auftrag ja gerade nicht.
		anzulegen := filepath.Dir(a.WatchGlob())
		if a.WatchRekursiv() {
			anzulegen = a.WatchWurzel()
		}
		if err := os.MkdirAll(filepath.Join(w.root, anzulegen), 0o755); err != nil {
			return fmt.Errorf("watcher: creating %s: %w", anzulegen, err)
		}
		if err := w.beobachte(a, anzulegen); err != nil {
			return err
		}
	}

	w.wg.Add(1)
	go w.lausche(ctx)

	// Nachholen: vorhandene Dateien als Trigger behandeln (§7).
	//
	// Vollständig VOR dem ersten Arbeiter, nicht nebenher: „ältester
	// zuerst" gilt nur über das, was der Arbeiter beim Zugreifen kennt.
	// Liefe er schon, während der Glob noch meldet, griffe er sich den
	// ältesten der ersten paar Treffer — und die Reihenfolge des ganzen
	// Rückstaus hinge daran, wie schnell die Schleife hier ist. Der
	// Wecker ist gepuffert, das Signal geht dabei nicht verloren.
	for _, a := range w.auftraege {
		if err := w.holeNach(a, a.WatchWurzel()); err != nil {
			return err
		}
	}

	for _, a := range w.auftraege {
		w.wg.Add(1)
		go w.arbeite(ctx, w.arbeiter[a.Name])
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
				w.logf("watcher: Auftrag %s, input %s: %v", arb.a.Name, rel, err)
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

// beobachte registriert ein Verzeichnis bei fsnotify — bei einem
// rekursiven Muster samt allem, was darunter schon liegt.
//
// Symlinks werden dabei NICHT verfolgt: WalkDir tut es von sich aus
// nicht, und das bleibt so. Ein Link, der auf einen Vorfahren zeigt,
// ergäbe sonst eine Schleife, und ein Link aus dem Bau heraus machte den
// Eingang zu einem Fenster in fremde Verzeichnisse (§3).
func (w *Watcher) beobachte(a *auftrag.Auftrag, dir string) error {
	abs := filepath.Join(w.root, dir)
	if !a.WatchRekursiv() {
		if err := w.fsw.Add(abs); err != nil {
			return fmt.Errorf("watcher: watching %s: %w", dir, err)
		}
		return nil
	}
	// Ein Verzeichnis, das zwischen WalkDir und Add verschwindet, ist kein
	// Fehler des Baus — es kann ein anderer Lauf gewesen sein, der
	// aufgeräumt hat. Gemeldet, nicht abgebrochen.
	return filepath.WalkDir(abs, func(pfad string, d fs.DirEntry, err error) error {
		if err != nil {
			w.logf("watcher: %s not read: %v", pfad, err)
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		if err := w.fsw.Add(pfad); err != nil {
			w.logf("watcher: %s not watched: %v", pfad, err)
		}
		return nil
	})
}

// holeNach meldet alles, was unter dir schon liegt und zum Muster passt.
//
// Zwei Aufgaben in einer Bewegung: beim Start ist es das Nachholen aus
// §7 (was im Eingang liegt, wurde nie verarbeitet oder blieb nach einem
// Crash), zur Laufzeit ist es das Gegenmittel gegen das Rennen beim
// Nachregistrieren. `mv ordner/ raeume/eingang/` ist EIN Rename: der
// Ordner ist mit Inhalt sofort da, und für die Dateien darin hat nie
// jemand ein Event gehört. Doppelmeldungen hält melde() ohnehin fern.
func (w *Watcher) holeNach(a *auftrag.Auftrag, dir string) error {
	treffer, err := a.WatchTreffer(w.root, dir)
	if err != nil {
		return fmt.Errorf("watcher: scanning %s: %w", dir, err)
	}
	for _, rel := range treffer {
		w.arbeiter[a.Name].melde(rel)
	}
	return nil
}

func (w *Watcher) lausche(ctx context.Context) {
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
			// Ein neues Verzeichnis unter einer rekursiven Wurzel wird
			// nachregistriert — und danach einmal durchgesehen, denn
			// zwischen seinem Entstehen und dem Add liegt ein Fenster, in
			// dem Events niemand hört.
			if ev.Has(fsnotify.Create) {
				if fi, err := os.Stat(ev.Name); err == nil && fi.IsDir() {
					w.neuesVerzeichnis(rel)
					continue
				}
			}
			// Zugeordnet wird über das Muster, nicht über eine Tabelle
			// „Verzeichnis → Aufträge": mit rekursiven Wurzeln stünden dort
			// Verzeichnisse, die es beim Start noch gar nicht gab. Es sind
			// wenige Aufträge, und der Match ist billig.
			for _, a := range w.auftraege {
				if a.WatchTrifft(rel) {
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

// neuesVerzeichnis registriert einen frisch entstandenen Ordner für jeden
// Auftrag, unter dessen Wurzel er liegt, und liest ihn einmal durch.
func (w *Watcher) neuesVerzeichnis(rel string) {
	for _, a := range w.auftraege {
		if !a.WatchRekursiv() || !unterhalb(a.WatchWurzel(), rel) {
			continue
		}
		if err := w.beobachte(a, rel); err != nil {
			w.logf("watcher: %v", err)
		}
		if err := w.holeNach(a, rel); err != nil {
			w.logf("watcher: rescanning %s: %v", rel, err)
		}
	}
}

// unterhalb sagt, ob rel im Baum unter wurzel liegt.
func unterhalb(wurzel, rel string) bool {
	imRaum, err := filepath.Rel(wurzel, rel)
	if err != nil {
		return false
	}
	return imRaum != ".." && !strings.HasPrefix(imRaum, ".."+string(filepath.Separator))
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
	schon, err := w.db.IsSeen(a.Name, hash)
	if err != nil {
		return err
	}
	if schon {
		w.logf("watcher: Auftrag %s, input %s: already processed (already-seen backstop) — skipped", a.Name, rel)
		return nil
	}

	// Sperre nehmen und den Deckel prüfen — in dieser Reihenfolge und in
	// einer Schleife. Beides zusammen, weil sonst ein Fenster entstünde,
	// durch das der Deckel überschritten wird: wer erst den Deckel prüft
	// und dann auf die Sperre wartet, rechnet mit dem Stand von vorhin,
	// und der Lauf, auf den er wartet, hat inzwischen den letzten Platz
	// verbraucht. Unter der Sperre gilt die Zahl, denn jeder Lauf dieses
	// Auftrags braucht dieselbe Sperre.
	//
	// Ist kein Platz frei, wird die Sperre wieder abgegeben: ein
	// schlafender Arbeiter darf einen cron-Lauf nicht eine Stunde lang
	// blockieren.
	//
	// Gemeldet wird beides einmal, nicht bei jedem Versuch: die
	// Wiederholung trug keine neue Information und füllte bei einem
	// vollen Eingang das Log schneller, als es jemand lesen konnte.
	gemeldet := false
	for {
		if !w.lock.Acquire(a.Name) {
			if !gemeldet {
				w.logf("watcher: Auftrag %s still running — input %s waiting", a.Name, rel)
				gemeldet = true
			}
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(debounce):
			}
			continue
		}

		warten, err := w.platzFrei(a)
		if err != nil {
			w.lock.Release(a.Name)
			return err
		}
		if warten == 0 {
			break // Platz frei, Sperre bleibt bei uns
		}
		w.lock.Release(a.Name)
		if !gemeldet {
			w.logf("watcher: Auftrag %s throttled (%s) — input %s waits %s",
				a.Name, a.Throttle, rel, warten.Round(time.Second))
			gemeldet = true
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(min(warten, w.maxWarten)):
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
	return w.db.MarkSeen(a.Name, hash)
}

// platzFrei prüft den Deckel des Auftrags (throttle:, §6). Rückgabe 0 =
// ein Lauf darf jetzt starten; sonst die Zeit, bis der älteste Lauf aus
// dem rollenden Fenster fällt und einen Platz freigibt.
//
// Gezählt wird aus der laeufe-Tabelle statt aus einem Zähler im
// Speicher: so übersteht der Deckel einen Daemon-Neustart, und
// ausgerechnet ein Crash-Loop bekommt nicht jedes Mal frisches Budget.
func (w *Watcher) platzFrei(a *auftrag.Auftrag) (time.Duration, error) {
	if !a.Throttle.An() {
		return 0, nil
	}
	jetzt := w.jetzt()
	var starts []time.Time
	if a.Throttle.Max > 0 {
		var err error
		if starts, err = w.db.LaeufeSince(a.Name, jetzt.Add(-a.Throttle.Per)); err != nil {
			return 0, err
		}
	}
	// Die Rechnung selbst steht in auftrag.Throttle, damit `hasenbau
	// status` dieselbe benutzt und nicht etwas anderes vorhersagt, als
	// hier passiert (Hasenbau-do0.4).
	return a.Throttle.Wait(jetzt, starts), nil
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
