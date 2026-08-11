package runner

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Moritz-Dorn/Hasenbau/internal/auftrag"
)

// fakeLaeufe steht für die laeufe-Tabelle: Start-Zeitpunkte, die der
// Deckel zählt. Wie in der echten DB entsteht der Eintrag beim Anlegen.
type fakeLaeufe struct {
	mu     sync.Mutex
	starts []time.Time
	jetzt  time.Time
	fehler error
}

func (f *fakeLaeufe) since(seit time.Time) ([]time.Time, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fehler != nil {
		return nil, f.fehler
	}
	var out []time.Time
	for _, t := range f.starts {
		if t.After(seit) {
			out = append(out, t)
		}
	}
	return out, nil
}

// anlegen entspricht store.StartLauf: es schreibt die Zeile. Der Schlaf
// steht hier mit Absicht — ein INSERT braucht Zeit, und genau in dieser
// Zeit sieht ein zweiter Prüfer die Zeile noch nicht. Ohne ihn ist das
// Fenster zwischen Prüfung und Eintrag so schmal, dass der Test auch
// ohne Sperre grün wäre und damit nichts belegte.
func (f *fakeLaeufe) anlegen() (int64, error) {
	time.Sleep(2 * time.Millisecond)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.starts = append(f.starts, f.jetzt)
	return int64(len(f.starts)), nil
}

func (f *fakeLaeufe) anzahl() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.starts)
}

// Hasenbau-cvf: Der Deckel gilt über Auftragsgrenzen hinweg. Genau das
// kann die lauf.Lock nicht: zwei Arbeiter verschiedener Aufträge halten
// verschiedene Sperren und sähen ohne eigene Serialisierung beide
// gleichzeitig denselben letzten Platz.
func TestBauDeckelHaeltUeberAuftragsgrenzen(t *testing.T) {
	const grenze = 5
	f := &fakeLaeufe{jetzt: time.Now()}
	b := &Budget{
		Rate:      auftrag.Throttle{Max: grenze, Per: time.Hour},
		Laeufe:    f.since,
		Jetzt:     func() time.Time { return f.jetzt },
		MaxWarten: time.Millisecond,
	}

	// Zwanzig Läufe aus zehn verschiedenen Aufträgen, alle gleichzeitig.
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	var durch atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := b.Start(ctx, "watch", f.anlegen); err == nil {
				durch.Add(1)
			}
		}()
	}
	wg.Wait()

	// Die Uhr steht: es fällt nie ein Platz nach. Also dürfen genau
	// `grenze` durchgekommen sein — nicht einer mehr.
	if n := durch.Load(); n != grenze {
		t.Errorf("%d Läufe durchgelassen, erlaubt sind %d", n, grenze)
	}
	if n := f.anzahl(); n != grenze {
		t.Errorf("%d Zeilen in laeufe, erwartet %d", n, grenze)
	}
}

// `manual` wartet nie — die Regel aus Hasenbau-do0.2 gilt weiter. Aber
// die Zeile entsteht, der Lauf zählt also für die folgenden mit.
func TestBauDeckelLaesstManuellDurchUndZaehltIhn(t *testing.T) {
	f := &fakeLaeufe{jetzt: time.Now()}
	b := &Budget{
		Rate:      auftrag.Throttle{Max: 1, Per: time.Hour},
		Laeufe:    f.since,
		Jetzt:     func() time.Time { return f.jetzt },
		MaxWarten: time.Millisecond,
	}
	ctx := context.Background()

	// Das Fenster ist nach einem watch-Lauf voll.
	if _, err := b.Start(ctx, "watch", f.anlegen); err != nil {
		t.Fatal(err)
	}
	// Trotzdem kommt der Mensch sofort durch — dreimal.
	for i := 0; i < 3; i++ {
		if _, err := b.Start(ctx, auftrag.TriggerManual, f.anlegen); err != nil {
			t.Fatalf("manueller Lauf %d gebremst: %v", i+1, err)
		}
	}
	if n := f.anzahl(); n != 4 {
		t.Errorf("%d Zeilen, erwartet 4 — manuelle Läufe müssen mitzählen", n)
	}

	// Und ein watch-Lauf wartet jetzt erst recht.
	kurz, stop := context.WithTimeout(ctx, 50*time.Millisecond)
	defer stop()
	if _, err := b.Start(kurz, "watch", f.anlegen); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("watch-Lauf nicht gebremst: %v", err)
	}
}

// Fällt ein Platz nach, läuft es weiter — der Deckel ist eine Rate, kein
// Riegel.
func TestBauDeckelGibtNachFensterablaufFrei(t *testing.T) {
	f := &fakeLaeufe{jetzt: time.Now()}
	b := &Budget{
		Rate:      auftrag.Throttle{Max: 1, Per: time.Second},
		Laeufe:    f.since,
		Jetzt:     func() time.Time { return f.jetzt },
		MaxWarten: time.Millisecond,
	}
	ctx := context.Background()
	if _, err := b.Start(ctx, "cron", f.anlegen); err != nil {
		t.Fatal(err)
	}

	fertig := make(chan error, 1)
	go func() { _, err := b.Start(ctx, "cron", f.anlegen); fertig <- err }()

	select {
	case err := <-fertig:
		t.Fatalf("zweiter Lauf nicht gebremst: %v", err)
	case <-time.After(30 * time.Millisecond):
	}

	// Uhr weiterstellen: der erste Lauf fällt aus dem Fenster.
	f.mu.Lock()
	f.jetzt = f.jetzt.Add(2 * time.Second)
	f.mu.Unlock()

	select {
	case err := <-fertig:
		if err != nil {
			t.Fatalf("nach Fensterablauf: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Platz wurde nie frei")
	}
}

// Ohne Deckel in hasenbau.yaml verhält sich alles wie vorher — die
// Historie wird nicht einmal befragt.
func TestOhneBauDeckelKeineAbfrage(t *testing.T) {
	f := &fakeLaeufe{jetzt: time.Now(), fehler: errors.New("hätte nicht gefragt werden dürfen")}
	for _, b := range []*Budget{nil, {}, {Rate: auftrag.Throttle{Max: 3, Per: time.Hour}}} {
		if b.An() {
			t.Errorf("Budget %+v gilt als gesetzt", b)
		}
		if _, err := b.Start(context.Background(), "watch", f.anlegen); err != nil {
			t.Errorf("ungedrosselt gebremst: %v", err)
		}
	}
}
