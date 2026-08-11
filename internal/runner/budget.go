// budget.go: der Bau-weite Deckel über alle Aufträge (Hasenbau-cvf).
//
// Der Deckel je Auftrag (§6, `throttle:`) schützt einen Auftrag vor sich
// selbst. Dieser schützt das Budget vor allen zusammen: zehn Aufträge
// mit je fünf Läufen je Stunde sind fünfzig.
//
// Zwei Unterschiede zum Deckel je Auftrag, und beide sind der Grund,
// warum das hier steht und nicht im Watcher:
//
//  1. **Er sitzt im Runner, nicht im Watcher.** Ein Budget-Deckel, der
//     cron-Läufe nicht mitzählt, wäre keiner — das Geld ist dasselbe.
//     Durch Execute geht jeder Lauf, egal welcher Trigger.
//  2. **`manual` wird gezählt, aber nicht gebremst.** Die Regel aus
//     Hasenbau-do0.2 bleibt: wer selbst davorsteht, wartet nicht. Seine
//     laeufe-Zeile entsteht trotzdem und zählt für die folgenden mit.
package runner

import (
	"context"
	"sync"
	"time"

	"github.com/Moritz-Dorn/Hasenbau/internal/auftrag"
)

// budgetMaxWarten begrenzt einen einzelnen Schlaf; danach wird neu
// gerechnet. Wie beim Deckel je Auftrag (Hasenbau-do0.3): ein einziger
// langer Timer wacht falsch auf, wenn die Uhr springt.
const budgetMaxWarten = time.Minute

// Budget ist das Bau-weite Kontingent. Der Nullwert (Max 0) lässt alles
// durch — ein Bau ohne `throttle:` in hasenbau.yaml verhält sich exakt
// wie vorher.
type Budget struct {
	// Rate ist die Grenze. Nur Max/Per; ein Tageszeitfenster wäre eine
	// andere Aussage und steht bewusst nicht drin.
	Rate auftrag.Throttle

	// Laeufe liefert die Startzeiten aller Läufe seit `since`.
	// *store.Store erfüllt das mit LaeufeSinceAll.
	Laeufe func(since time.Time) ([]time.Time, error)

	// Jetzt und MaxWarten sind im Test steuerbar; leer = time.Now und
	// budgetMaxWarten.
	Jetzt     func() time.Time
	MaxWarten time.Duration

	Logf func(format string, args ...any)

	// mu serialisiert Prüfung UND Anlegen der laeufe-Zeile.
	//
	// Beim Deckel je Auftrag genügt die lauf.Lock, weil jeder Lauf
	// desselben Auftrags dieselbe Sperre braucht. Bau-weit gilt das
	// nicht: zwei Arbeiter VERSCHIEDENER Aufträge halten verschiedene
	// Sperren und sähen sonst beide gleichzeitig denselben letzten
	// Platz. Deshalb liegt hier eine eigene, und deshalb liegt das
	// Anlegen der Zeile mit darin — sobald sie steht, zählt der Lauf für
	// den nächsten Prüfer mit.
	//
	// Sie gilt je Prozess. Das reicht, weil nur der Daemon gedrosselte
	// Läufe startet: `hasenbau lauf` läuft als 'manual' und wird von
	// vornherein durchgelassen.
	mu sync.Mutex
}

func (b *Budget) jetzt() time.Time {
	if b == nil || b.Jetzt == nil {
		return time.Now()
	}
	return b.Jetzt()
}

func (b *Budget) maxWarten() time.Duration {
	if b == nil || b.MaxWarten == 0 {
		return budgetMaxWarten
	}
	return b.MaxWarten
}

// An sagt, ob überhaupt gedeckelt wird.
func (b *Budget) An() bool { return b != nil && b.Rate.Max > 0 && b.Laeufe != nil }

// Start wartet, bis das Bau-weite Kontingent Platz hat, und ruft dann
// `anlegen` — noch unter derselben Sperre, damit die neue laeufe-Zeile
// steht, bevor der nächste Prüfer zählt.
//
// `manual` wartet nie. Ist kein Deckel gesetzt, wird direkt angelegt.
func (b *Budget) Start(ctx context.Context, trigger string, anlegen func() (int64, error)) (int64, error) {
	if !b.An() || trigger == auftrag.TriggerManual {
		return anlegen()
	}

	gemeldet := false
	for {
		b.mu.Lock()
		jetzt := b.jetzt()
		starts, err := b.Laeufe(jetzt.Add(-b.Rate.Per))
		if err != nil {
			b.mu.Unlock()
			return 0, err
		}
		warten := b.Rate.Wait(jetzt, starts)
		if warten == 0 {
			id, err := anlegen()
			b.mu.Unlock()
			return id, err
		}
		b.mu.Unlock()

		// Einmal melden, nicht bei jedem Versuch — dieselbe Regel wie im
		// Watcher (Hasenbau-do0.1).
		if !gemeldet && b.Logf != nil {
			b.Logf("bau-deckel: %s erreicht (%d Läufe im Fenster) — %s wartet %s",
				b.Rate, len(starts), trigger, auftrag.FormatDuration(warten.Round(time.Second)))
			gemeldet = true
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(min(warten, b.maxWarten())):
		}
	}
}

// Frei sagt, wie lange bis zum nächsten freien Platz — für die Anzeige
// (`hasenbau status`). Ohne Deckel immer 0.
func (b *Budget) Frei() (belegt int, warten time.Duration, err error) {
	if !b.An() {
		return 0, 0, nil
	}
	jetzt := b.jetzt()
	starts, err := b.Laeufe(jetzt.Add(-b.Rate.Per))
	if err != nil {
		return 0, 0, err
	}
	return len(starts), b.Rate.Wait(jetzt, starts), nil
}
