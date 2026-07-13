package watcher

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Moritz-Dorn/Hasenbau/internal/auftrag"
	"github.com/Moritz-Dorn/Hasenbau/internal/lauf"
)

type fakeGesehen struct {
	mu   sync.Mutex
	menge map[string]bool
}

func neuerFakeGesehen() *fakeGesehen { return &fakeGesehen{menge: map[string]bool{}} }

func (f *fakeGesehen) IstGesehen(auftrag, hash string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.menge[auftrag+"/"+hash], nil
}

func (f *fakeGesehen) MerkeGesehen(auftrag, hash string) error {
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
func starte(t *testing.T, root string, a *auftrag.Auftrag, gesehen GesehenStore, verhalten func(input string) error) (<-chan aufruf, func()) {
	t.Helper()
	aufrufe := make(chan aufruf, 16)
	w, err := New(root, []*auftrag.Auftrag{a}, lauf.NeueSperre(), gesehen,
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
	if ok, _ := gesehen.IstGesehen("pdf-einlagern", "egal"); ok {
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
