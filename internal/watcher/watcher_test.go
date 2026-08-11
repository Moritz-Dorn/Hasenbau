package watcher

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Moritz-Dorn/Hasenbau/internal/auftrag"
	"github.com/Moritz-Dorn/Hasenbau/internal/lauf"
)

type fakeGesehen struct {
	mu    sync.Mutex
	menge map[string]bool
}

func neuerFakeGesehen() *fakeGesehen { return &fakeGesehen{menge: map[string]bool{}} }

func (f *fakeGesehen) IsSeen(auftrag, hash string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.menge[auftrag+"/"+hash], nil
}

func (f *fakeGesehen) MarkSeen(auftrag, hash string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.menge[auftrag+"/"+hash] = true
	return nil
}

type aufruf struct {
	auftrag string
	input   string
}

func watchAuftrag(debounce time.Duration) *auftrag.Auftrag {
	return &auftrag.Auftrag{
		Name:    "pdf-einlagern",
		Hase:    "archivar",
		Trigger: auftrag.Trigger{Watch: "raeume/eingang/*.txt", Debounce: debounce},
	}
}

// starte baut einen Watcher mit Aufruf-Kanal und optionalem Verhalten.
func starte(t *testing.T, root string, a *auftrag.Auftrag, gesehen SeenStore, verhalten func(input string) error) (<-chan aufruf, func()) {
	t.Helper()
	aufrufe := make(chan aufruf, 16)
	w, err := New(root, []*auftrag.Auftrag{a}, lauf.NewLock(), gesehen,
		func(a *auftrag.Auftrag, input string) error {
			if verhalten != nil {
				if err := verhalten(input); err != nil {
					return err
				}
			}
			aufrufe <- aufruf{a.Name, input}
			return nil
		}, t.Logf)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := w.Start(ctx); err != nil {
		t.Fatal(err)
	}
	return aufrufe, func() { cancel(); w.Stop() }
}

func erwarteAufruf(t *testing.T, aufrufe <-chan aufruf, input string) {
	t.Helper()
	select {
	case got := <-aufrufe:
		if got.input != input {
			t.Fatalf("verarbeitet %q, erwartet %q", got.input, input)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("kein Lauf für %q", input)
	}
}

func erwarteKeinenAufruf(t *testing.T, aufrufe <-chan aufruf, dauer time.Duration) {
	t.Helper()
	select {
	case got := <-aufrufe:
		t.Fatalf("unerwarteter Lauf: %+v", got)
	case <-time.After(dauer):
	}
}

func TestNeueDateiFeuert(t *testing.T) {
	root := t.TempDir()
	aufrufe, stop := starte(t, root, watchAuftrag(50*time.Millisecond), neuerFakeGesehen(), nil)
	defer stop()

	if err := os.WriteFile(filepath.Join(root, "raeume/eingang/doc.txt"), []byte("material"), 0o644); err != nil {
		t.Fatal(err)
	}
	erwarteAufruf(t, aufrufe, "raeume/eingang/doc.txt")
}

func TestPartiellerWriteWartetAufStabileGroesse(t *testing.T) {
	root := t.TempDir()
	aufrufe, stop := starte(t, root, watchAuftrag(150*time.Millisecond), neuerFakeGesehen(), nil)
	defer stop()

	// Datei wächst über mehrere Debounce-Ticks — wie eine langsame Kopie.
	pfad := filepath.Join(root, "raeume/eingang/gross.txt")
	f, err := os.Create(pfad)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		if _, err := f.WriteString("chunk-"); err != nil {
			t.Fatal(err)
		}
		erwarteKeinenAufruf(t, aufrufe, 120*time.Millisecond) // wächst noch — kein Lauf
	}
	f.Close()

	erwarteAufruf(t, aufrufe, "raeume/eingang/gross.txt") // stabil ⇒ Lauf
}

func TestGesehenBackstopUeberspringt(t *testing.T) {
	root := t.TempDir()
	gesehen := neuerFakeGesehen()
	aufrufe, stop := starte(t, root, watchAuftrag(50*time.Millisecond), gesehen, nil)

	if err := os.WriteFile(filepath.Join(root, "raeume/eingang/doc.txt"), []byte("material"), 0o644); err != nil {
		t.Fatal(err)
	}
	erwarteAufruf(t, aufrufe, "raeume/eingang/doc.txt")
	stop()

	// Der Move nach archiv/ „schlug fehl" — die Datei liegt noch da.
	// Ein Neustart darf sie nicht erneut verarbeiten.
	aufrufe2, stop2 := starte(t, root, watchAuftrag(50*time.Millisecond), gesehen, nil)
	defer stop2()
	erwarteKeinenAufruf(t, aufrufe2, 500*time.Millisecond)
}

func TestNachholenBeimStart(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "raeume/eingang"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "raeume/eingang/liegengeblieben.txt"), []byte("alt"), 0o644); err != nil {
		t.Fatal(err)
	}

	aufrufe, stop := starte(t, root, watchAuftrag(50*time.Millisecond), neuerFakeGesehen(), nil)
	defer stop()
	erwarteAufruf(t, aufrufe, "raeume/eingang/liegengeblieben.txt")
}

func TestNichtPassendeDateienIgnoriert(t *testing.T) {
	root := t.TempDir()
	aufrufe, stop := starte(t, root, watchAuftrag(50*time.Millisecond), neuerFakeGesehen(), nil)
	defer stop()

	if err := os.WriteFile(filepath.Join(root, "raeume/eingang/notiz.pdf"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	erwarteKeinenAufruf(t, aufrufe, 400*time.Millisecond)
}

// Hasenbau-do0.1: Ein voller Eingang ergibt einen Arbeiter, nicht 200
// Goroutinen — und er nimmt sich die Dateien in der Reihenfolge, in der
// sie angekommen sind.
func TestVollerEingangLaeuftDurchEinenArbeiter(t *testing.T) {
	const anzahl = 200
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "raeume/eingang"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Namen aufsteigend, mtimes absteigend: Wer nach Glob-Reihenfolge
	// arbeitet, kommt genau falsch heraus.
	basis := time.Now().Add(-24 * time.Hour)
	var erwartet []string
	for i := 0; i < anzahl; i++ {
		rel := fmt.Sprintf("raeume/eingang/%03d.txt", i)
		abs := filepath.Join(root, rel)
		if err := os.WriteFile(abs, []byte(rel), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(abs, basis, basis.Add(time.Duration(anzahl-i)*time.Minute)); err != nil {
			t.Fatal(err)
		}
		erwartet = append([]string{rel}, erwartet...) // jüngster Name = ältester Input
	}

	var mu sync.Mutex
	var reihenfolge []string
	fertig := make(chan struct{})
	w, err := New(root, []*auftrag.Auftrag{watchAuftrag(time.Millisecond)}, lauf.NewLock(), neuerFakeGesehen(),
		func(a *auftrag.Auftrag, input string) error {
			mu.Lock()
			defer mu.Unlock()
			reihenfolge = append(reihenfolge, input)
			if len(reihenfolge) == anzahl {
				close(fertig)
			}
			return nil
		}, func(string, ...any) {})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); w.Stop() }()
	vorher := runtime.NumGoroutine()
	if err := w.Start(ctx); err != nil {
		t.Fatal(err)
	}
	// Jetzt liegen alle 200 gemeldet in der Menge des Arbeiters. Vorher
	// wären hier 200 Goroutinen gestartet, von denen 199 in der Sperre
	// drehten; jetzt sind es der Lauscher und ein Arbeiter.
	if neu := runtime.NumGoroutine() - vorher; neu > 20 {
		t.Errorf("%d neue Goroutinen für %d Dateien — der Arbeiter greift nicht", neu, anzahl)
	}

	select {
	case <-fertig:
	case <-time.After(30 * time.Second):
		mu.Lock()
		n := len(reihenfolge)
		mu.Unlock()
		t.Fatalf("nur %d von %d verarbeitet", n, anzahl)
	}

	mu.Lock()
	defer mu.Unlock()
	for i := range erwartet {
		if reihenfolge[i] != erwartet[i] {
			t.Fatalf("Position %d: %s, erwartet %s (ältester zuerst)", i, reihenfolge[i], erwartet[i])
		}
	}
}

// Wartet ein Input auf einen Lauf aus einem anderen Trigger, wird das
// einmal gemeldet. Vorher stand die Zeile bei jedem Versuch im Log —
// bei vollem Eingang schneller, als sie jemand lesen konnte.
func TestWartenWirdEinmalGemeldet(t *testing.T) {
	root := t.TempDir()
	a := watchAuftrag(20 * time.Millisecond)
	sperre := lauf.NewLock()
	if !sperre.Acquire(a.Name) {
		t.Fatal("Sperre nicht zu bekommen")
	}

	var mu sync.Mutex
	var wartezeilen int
	aufrufe := make(chan aufruf, 4)
	w, err := New(root, []*auftrag.Auftrag{a}, sperre, neuerFakeGesehen(),
		func(a *auftrag.Auftrag, input string) error {
			aufrufe <- aufruf{a.Name, input}
			return nil
		},
		func(format string, args ...any) {
			if strings.Contains(fmt.Sprintf(format, args...), "wartet") {
				mu.Lock()
				wartezeilen++
				mu.Unlock()
			}
		})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); w.Stop() }()
	if err := w.Start(ctx); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(root, "raeume/eingang/doc.txt"), []byte("material"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Lange genug für ein Dutzend Versuche im alten Takt.
	erwarteKeinenAufruf(t, aufrufe, 400*time.Millisecond)
	mu.Lock()
	n := wartezeilen
	mu.Unlock()
	if n != 1 {
		t.Errorf("%d Warte-Meldungen, erwartet genau 1", n)
	}

	// Und sobald der andere Trigger fertig ist, läuft der Input.
	sperre.Release(a.Name)
	erwarteAufruf(t, aufrufe, "raeume/eingang/doc.txt")
}

func TestFehlgeschlagenerLaufLandetNichtInGesehen(t *testing.T) {
	root := t.TempDir()
	gesehen := neuerFakeGesehen()
	fehlschlag := true
	var mu sync.Mutex

	aufrufe, stop := starte(t, root, watchAuftrag(50*time.Millisecond), gesehen,
		func(input string) error {
			mu.Lock()
			defer mu.Unlock()
			if fehlschlag {
				fehlschlag = false
				return os.ErrPermission // erster Versuch scheitert
			}
			return nil
		})
	defer stop()

	if err := os.WriteFile(filepath.Join(root, "raeume/eingang/doc.txt"), []byte("material"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Erster Versuch scheitert ⇒ kein Aufruf-Event, kein gesehen-Eintrag.
	erwarteKeinenAufruf(t, aufrufe, 500*time.Millisecond)
	if ok, _ := gesehen.IsSeen("pdf-einlagern", "egal"); ok {
		t.Fatal("gesehen trotz Fehlschlag")
	}

	// Ein neues Event (z.B. touch) stößt die Verarbeitung erneut an.
	if err := os.Chtimes(filepath.Join(root, "raeume/eingang/doc.txt"), time.Now(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "raeume/eingang/doc.txt"), []byte("material"), 0o644); err != nil {
		t.Fatal(err)
	}
	erwarteAufruf(t, aufrufe, "raeume/eingang/doc.txt")
}
