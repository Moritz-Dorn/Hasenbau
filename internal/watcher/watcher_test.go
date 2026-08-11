package watcher

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Moritz-Dorn/Hasenbau/internal/auftrag"
	"github.com/Moritz-Dorn/Hasenbau/internal/lauf"
)

// fakeDB steht für die Bau-DB: der gesehen-Backstop und die Läufe, aus
// denen der Deckel rechnet.
type fakeDB struct {
	mu     sync.Mutex
	menge  map[string]bool
	starts map[string][]time.Time
	fehler error // wenn gesetzt, scheitert LaeufeSince
}

func neuerFakeDB() *fakeDB {
	return &fakeDB{menge: map[string]bool{}, starts: map[string][]time.Time{}}
}

func (f *fakeDB) IsSeen(auftrag, hash string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.menge[auftrag+"/"+hash], nil
}

func (f *fakeDB) MarkSeen(auftrag, hash string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.menge[auftrag+"/"+hash] = true
	return nil
}

func (f *fakeDB) LaeufeSince(auftrag string, since time.Time) ([]time.Time, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fehler != nil {
		return nil, f.fehler
	}
	var out []time.Time
	for _, t := range f.starts[auftrag] {
		if t.After(since) {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Before(out[j]) })
	return out, nil
}

// vermerkeLauf schreibt einen Lauf in die Historie — das tut sonst der
// Runner, den es im Watcher-Test nicht gibt.
func (f *fakeDB) vermerkeLauf(auftrag string, t time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.starts[auftrag] = append(f.starts[auftrag], t)
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
func starte(t *testing.T, root string, a *auftrag.Auftrag, db Store, verhalten func(input string) error) (<-chan aufruf, func()) {
	t.Helper()
	aufrufe := make(chan aufruf, 16)
	w, err := New(root, []*auftrag.Auftrag{a}, lauf.NewLock(), db,
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
	aufrufe, stop := starte(t, root, watchAuftrag(50*time.Millisecond), neuerFakeDB(), nil)
	defer stop()

	if err := os.WriteFile(filepath.Join(root, "raeume/eingang/doc.txt"), []byte("material"), 0o644); err != nil {
		t.Fatal(err)
	}
	erwarteAufruf(t, aufrufe, "raeume/eingang/doc.txt")
}

func TestPartiellerWriteWartetAufStabileGroesse(t *testing.T) {
	root := t.TempDir()
	aufrufe, stop := starte(t, root, watchAuftrag(150*time.Millisecond), neuerFakeDB(), nil)
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
	gesehen := neuerFakeDB()
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

	aufrufe, stop := starte(t, root, watchAuftrag(50*time.Millisecond), neuerFakeDB(), nil)
	defer stop()
	erwarteAufruf(t, aufrufe, "raeume/eingang/liegengeblieben.txt")
}

func TestNichtPassendeDateienIgnoriert(t *testing.T) {
	root := t.TempDir()
	aufrufe, stop := starte(t, root, watchAuftrag(50*time.Millisecond), neuerFakeDB(), nil)
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
	w, err := New(root, []*auftrag.Auftrag{watchAuftrag(time.Millisecond)}, lauf.NewLock(), neuerFakeDB(),
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
	w, err := New(root, []*auftrag.Auftrag{a}, sperre, neuerFakeDB(),
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

// Hasenbau-do0.2: Der Deckel rechnet aus der Lauf-Historie, nicht aus
// einem Zähler im Speicher — ein frisch gestarteter Daemon findet das
// Fenster deshalb voll vor und wartet, statt neu anzufangen.
func TestDeckelUeberstehtDenNeustart(t *testing.T) {
	root := t.TempDir()
	a := watchAuftrag(20 * time.Millisecond)
	a.Throttle = auftrag.Throttle{Max: 2, Per: 2 * time.Second}

	db := neuerFakeDB()
	// Zwei Läufe liegen im Fenster — aus einem früheren Daemon-Leben.
	// Der ältere fällt in einer halben Sekunde heraus.
	jetzt := time.Now()
	db.vermerkeLauf(a.Name, jetzt.Add(-1500*time.Millisecond))
	db.vermerkeLauf(a.Name, jetzt.Add(-1000*time.Millisecond))

	aufrufe, stop := starte(t, root, a, db, nil)
	defer stop()

	if err := os.WriteFile(filepath.Join(root, "raeume/eingang/doc.txt"), []byte("material"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Das Fenster ist voll: kein Lauf, obwohl die Datei bereit liegt.
	erwarteKeinenAufruf(t, aufrufe, 300*time.Millisecond)
	// Sobald der älteste Lauf herausfällt, geht es los.
	erwarteAufruf(t, aufrufe, "raeume/eingang/doc.txt")
}

// Der Deckel taktet einen Stapel: drei Dateien, zwei Plätze je Fenster —
// die dritte wartet, bis wieder Platz ist.
func TestDeckelTaktetDenStapel(t *testing.T) {
	root := t.TempDir()
	a := watchAuftrag(10 * time.Millisecond)
	a.Throttle = auftrag.Throttle{Max: 2, Per: time.Second}

	db := neuerFakeDB()
	var mu sync.Mutex
	var zeiten []time.Time
	aufrufe := make(chan aufruf, 8)
	w, err := New(root, []*auftrag.Auftrag{a}, lauf.NewLock(), db,
		func(a *auftrag.Auftrag, input string) error {
			// Was sonst der Runner tut: der Lauf landet in der Historie
			// und zählt damit für den Deckel.
			jetzt := time.Now()
			db.vermerkeLauf(a.Name, jetzt)
			mu.Lock()
			zeiten = append(zeiten, jetzt)
			mu.Unlock()
			aufrufe <- aufruf{a.Name, input}
			return nil
		}, t.Logf)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); w.Stop() }()
	if err := w.Start(ctx); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 3; i++ {
		pfad := filepath.Join(root, fmt.Sprintf("raeume/eingang/%d.txt", i))
		// Unterschiedlicher Inhalt: der gesehen-Backstop greift über den
		// Hash, drei gleiche Dateien wären zwei übersprungene Läufe.
		if err := os.WriteFile(pfad, []byte(fmt.Sprintf("material %d", i)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 3; i++ {
		select {
		case <-aufrufe:
		case <-time.After(10 * time.Second):
			t.Fatalf("nur %d von 3 Läufen", i)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	// Die ersten beiden dürfen sofort, der dritte erst, wenn der erste
	// aus dem Fenster gefallen ist.
	if abstand := zeiten[2].Sub(zeiten[0]); abstand < a.Throttle.Per {
		t.Errorf("dritter Lauf nach %s, Fenster ist %s — der Deckel greift nicht", abstand, a.Throttle.Per)
	}
	if abstand := zeiten[1].Sub(zeiten[0]); abstand > 500*time.Millisecond {
		t.Errorf("zweiter Lauf erst nach %s — der Deckel bremst zu früh", abstand)
	}
}

// Ohne throttle: bleibt alles, wie es war — die Historie wird gar nicht
// erst befragt.
func TestOhneDeckelKeineAbfrage(t *testing.T) {
	root := t.TempDir()
	db := neuerFakeDB()
	db.fehler = os.ErrPermission // eine Abfrage würde auffallen

	aufrufe, stop := starte(t, root, watchAuftrag(20*time.Millisecond), db, nil)
	defer stop()
	if err := os.WriteFile(filepath.Join(root, "raeume/eingang/doc.txt"), []byte("material"), 0o644); err != nil {
		t.Fatal(err)
	}
	erwarteAufruf(t, aufrufe, "raeume/eingang/doc.txt")
}

// uhr ist eine gestellte Uhr. Ein Tagesfenster gegen die Wanduhr zu
// prüfen machte den Test davon abhängig, zu welcher Tageszeit er läuft —
// nachts um drei wäre er grün und mittags rot (vgl. Hasenbau-eav).
type uhr struct {
	mu sync.Mutex
	t  time.Time
}

func (u *uhr) jetzt() time.Time {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.t
}

func (u *uhr) stelle(t time.Time) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.t = t
}

func um(h, m int) time.Time { return time.Date(2026, 3, 10, h, m, 0, 0, time.UTC) }

// Hasenbau-do0.3: Außerhalb des Fensters startet nichts — und sobald es
// aufgeht, läuft der wartende Input.
func TestZeitfensterHaeltDenStartZurueck(t *testing.T) {
	root := t.TempDir()
	a := watchAuftrag(10 * time.Millisecond)
	a.Throttle = auftrag.Throttle{Between: &auftrag.Window{From: 22 * 60, To: 6 * 60}}

	u := &uhr{t: um(14, 0)} // Nachmittag: zu
	aufrufe := make(chan aufruf, 4)
	w, err := New(root, []*auftrag.Auftrag{a}, lauf.NewLock(), neuerFakeDB(),
		func(a *auftrag.Auftrag, input string) error {
			aufrufe <- aufruf{a.Name, input}
			return nil
		}, t.Logf)
	if err != nil {
		t.Fatal(err)
	}
	w.jetzt = u.jetzt
	w.maxWarten = 10 * time.Millisecond // sonst schliefe er bis heute Abend

	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); w.Stop() }()
	if err := w.Start(ctx); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(root, "raeume/eingang/doc.txt"), []byte("material"), 0o644); err != nil {
		t.Fatal(err)
	}
	erwarteKeinenAufruf(t, aufrufe, 300*time.Millisecond)

	u.stelle(um(23, 0)) // Fenster auf
	erwarteAufruf(t, aufrufe, "raeume/eingang/doc.txt")
}

// Das Fenster begrenzt nur den START. Ein Lauf, der um 05:55 beginnt und
// eine halbe Stunde braucht, läuft zu Ende — ein Fenster, das mitten ins
// Schreiben schneidet, produziert halbe Ergebnisse.
func TestZeitfensterUnterbrichtKeinenLaufendenLauf(t *testing.T) {
	root := t.TempDir()
	a := watchAuftrag(10 * time.Millisecond)
	a.Throttle = auftrag.Throttle{Between: &auftrag.Window{From: 22 * 60, To: 6 * 60}}

	u := &uhr{t: um(5, 55)} // kurz vor Fensterschluss
	fertig := make(chan error, 1)
	w, err := New(root, []*auftrag.Auftrag{a}, lauf.NewLock(), neuerFakeDB(),
		func(a *auftrag.Auftrag, input string) error {
			// Mitten im Lauf schließt das Fenster.
			u.stelle(um(6, 30))
			time.Sleep(50 * time.Millisecond)
			fertig <- nil
			return nil
		}, t.Logf)
	if err != nil {
		t.Fatal(err)
	}
	w.jetzt = u.jetzt
	w.maxWarten = 10 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); w.Stop() }()
	if err := w.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "raeume/eingang/doc.txt"), []byte("material"), 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case <-fertig:
	case <-time.After(10 * time.Second):
		t.Fatal("der Lauf wurde beim Fensterschluss abgeschnitten")
	}
}

func TestFehlgeschlagenerLaufLandetNichtInGesehen(t *testing.T) {
	root := t.TempDir()
	gesehen := neuerFakeDB()
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
