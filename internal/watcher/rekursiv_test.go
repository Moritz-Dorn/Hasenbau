package watcher

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Moritz-Dorn/Hasenbau/internal/auftrag"
)

// rekursiverAuftrag ist derselbe Auftrag wie watchAuftrag, nur mit dem
// Doppelstern — die Wurzel bleibt der input-Raum.
func rekursiverAuftrag(debounce time.Duration) *auftrag.Auftrag {
	a := watchAuftrag(debounce)
	a.Trigger.Watch = "**/*.txt"
	return a
}

// schreibe legt eine Datei samt Elternverzeichnissen an. Der Inhalt ist
// der Pfad selbst, und das ist kein Zierat: der gesehen-Backstop (§7)
// arbeitet über den Hash des Inhalts, und zwei Dateien mit demselben
// Text sind für ihn dieselbe — die zweite würde übersprungen und der
// Test bewiese das Gegenteil dessen, was er zeigen soll.
func schreibe(t *testing.T, pfad string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(pfad), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pfad, []byte("material aus "+pfad), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestRekursivFeuertImNeuenUnterverzeichnis ist der Kern von
// Hasenbau-5xv: ein Verzeichnis, dessen Namen niemand im Auftrag
// genannt hat, entsteht erst zur Laufzeit — und eine Datei darin löst
// trotzdem aus.
func TestRekursivFeuertImNeuenUnterverzeichnis(t *testing.T) {
	root := t.TempDir()
	aufrufe, stop := starte(t, root, rekursiverAuftrag(50*time.Millisecond), neuerFakeDB(), nil)
	defer stop()

	schreibe(t, filepath.Join(root, "raeume/eingang/2026-08/scans/doc.txt"))
	erwarteAufruf(t, aufrufe, "raeume/eingang/2026-08/scans/doc.txt")
}

// TestRekursivHoltVerschobenenOrdnerNach deckt das Rennen ab, das der
// Bead als eigentlichen Knackpunkt nennt: `mv ordner/ raeume/eingang/`
// ist EIN Rename. Der Ordner ist mit Inhalt sofort da, und für die
// Dateien darin hört niemand je ein Event — sie kommen nur herein, wenn
// das frisch registrierte Verzeichnis einmal durchgesehen wird.
func TestRekursivHoltVerschobenenOrdnerNach(t *testing.T) {
	root := t.TempDir()
	// Fertig gefüllter Ordner NEBEN dem Bau, damit das Verschieben ein
	// einziger Rename ist und nicht Datei für Datei geschieht.
	fremd := filepath.Join(t.TempDir(), "stapel")
	for _, n := range []string{"a.txt", "tiefer/b.txt"} {
		schreibe(t, filepath.Join(fremd, n))
	}

	aufrufe, stop := starte(t, root, rekursiverAuftrag(50*time.Millisecond), neuerFakeDB(), nil)
	defer stop()

	if err := os.Rename(fremd, filepath.Join(root, "raeume/eingang/stapel")); err != nil {
		t.Fatal(err)
	}

	// Beide, und die Reihenfolge steht nicht fest — die Datei eine Ebene
	// tiefer kommt über dasselbe Nachlesen herein.
	erwartet := map[string]bool{
		"raeume/eingang/stapel/a.txt":        false,
		"raeume/eingang/stapel/tiefer/b.txt": false,
	}
	for range erwartet {
		select {
		case got := <-aufrufe:
			if _, da := erwartet[got.input]; !da {
				t.Fatalf("unerwarteter Input %q", got.input)
			}
			erwartet[got.input] = true
		case <-time.After(10 * time.Second):
			t.Fatalf("nicht alle Dateien des verschobenen Ordners kamen an: %v", erwartet)
		}
	}
}

// TestRekursivHoltBeimStartNach: was schon dalag, als der Daemon startete
// — dieselbe Zusage wie bisher (§7), nur jetzt über den ganzen Baum.
func TestRekursivHoltBeimStartNach(t *testing.T) {
	root := t.TempDir()
	schreibe(t, filepath.Join(root, "raeume/eingang/alt/tief/doc.txt"))

	aufrufe, stop := starte(t, root, rekursiverAuftrag(50*time.Millisecond), neuerFakeDB(), nil)
	defer stop()

	erwarteAufruf(t, aufrufe, "raeume/eingang/alt/tief/doc.txt")
}

// TestRekursivTrifftAuchDirektImEingang: der Doppelstern steht für NULL
// oder mehr Verzeichnisse. Wer ihn schreibt, verliert den flachen Fall
// nicht — sonst müsste man zwei Aufträge für einen Eingang anlegen.
func TestRekursivTrifftAuchDirektImEingang(t *testing.T) {
	root := t.TempDir()
	aufrufe, stop := starte(t, root, rekursiverAuftrag(50*time.Millisecond), neuerFakeDB(), nil)
	defer stop()

	schreibe(t, filepath.Join(root, "raeume/eingang/doc.txt"))
	erwarteAufruf(t, aufrufe, "raeume/eingang/doc.txt")
}

// TestFlachIgnoriertUnterverzeichnis hält die andere Hälfte der Zusage:
// rekursiv ist ein Opt-in. Ein Auftrag mit `*.txt` darf von einer Datei
// im Unterverzeichnis nichts wissen — sonst wäre das Verhalten still
// gewachsen, und genau das war nicht gewollt.
func TestFlachIgnoriertUnterverzeichnis(t *testing.T) {
	root := t.TempDir()
	aufrufe, stop := starte(t, root, watchAuftrag(50*time.Millisecond), neuerFakeDB(), nil)
	defer stop()

	schreibe(t, filepath.Join(root, "raeume/eingang/unter/doc.txt"))
	erwarteKeinenAufruf(t, aufrufe, 2*time.Second)

	// Gegenprobe im selben Test: der flache Fall feuert weiterhin. Ohne
	// sie bewiese das Schweigen oben nur, dass gar nichts läuft.
	schreibe(t, filepath.Join(root, "raeume/eingang/doc.txt"))
	erwarteAufruf(t, aufrufe, "raeume/eingang/doc.txt")
}

// TestFestesUnterverzeichnisBleibtFlach: `unter/*.txt` nennt sein
// Verzeichnis beim Namen und braucht deshalb keine rekursive
// Registrierung — es kostet weiter genau einen inotify-Watch.
func TestFestesUnterverzeichnisBleibtFlach(t *testing.T) {
	root := t.TempDir()
	a := watchAuftrag(50 * time.Millisecond)
	a.Trigger.Watch = "unter/*.txt"
	if a.WatchRekursiv() {
		t.Fatal("festes Unterverzeichnis gilt als rekursiv")
	}
	aufrufe, stop := starte(t, root, a, neuerFakeDB(), nil)
	defer stop()

	schreibe(t, filepath.Join(root, "raeume/eingang/unter/doc.txt"))
	erwarteAufruf(t, aufrufe, "raeume/eingang/unter/doc.txt")
}
