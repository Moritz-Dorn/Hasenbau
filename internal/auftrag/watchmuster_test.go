package auftrag

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestWatchTrifftUndRekursiv hält die Semantik fest, die `watch:` seit
// Hasenbau-5xv hat — und zwar an den Fällen, die man sich sonst
// verschieden vorstellt.
//
// Zwei Zusagen stehen darin: der Doppelstern ist ein OPT-IN (ein flaches
// Muster sieht nichts in Unterverzeichnissen), und er steht für NULL
// oder mehr Verzeichnisse (wer ihn schreibt, verliert den flachen Fall
// nicht).
func TestWatchTrifftUndRekursiv(t *testing.T) {
	const raum = "raeume/eingang/"

	faelle := []struct {
		muster   string
		rekursiv bool
		trifft   []string
		nicht    []string
	}{
		{
			muster:   "*.pdf",
			rekursiv: false,
			trifft:   []string{"raeume/eingang/a.pdf"},
			nicht:    []string{"raeume/eingang/unter/a.pdf", "raeume/eingang/a.txt"},
		},
		{
			muster:   "**/*.pdf",
			rekursiv: true,
			trifft: []string{
				"raeume/eingang/a.pdf",               // null Verzeichnisse
				"raeume/eingang/unter/a.pdf",         // eines
				"raeume/eingang/2026-08/scans/a.pdf", // mehrere
			},
			nicht: []string{"raeume/eingang/unter/a.txt", "raeume/anderer/a.pdf"},
		},
		{
			// Festes Unterverzeichnis: nennt seinen Namen, braucht also
			// keine rekursive Registrierung.
			muster:   "unter/*.pdf",
			rekursiv: false,
			trifft:   []string{"raeume/eingang/unter/a.pdf"},
			nicht:    []string{"raeume/eingang/a.pdf", "raeume/eingang/unter/tief/a.pdf"},
		},
		{
			// Ein einfacher Stern im Verzeichnis-Anteil greift genau eine
			// Ebene tief — auch das sind Namen, die erst zur Laufzeit
			// feststehen, also gilt es als rekursiv.
			muster:   "*/*.pdf",
			rekursiv: true,
			trifft:   []string{"raeume/eingang/unter/a.pdf"},
			nicht:    []string{"raeume/eingang/a.pdf", "raeume/eingang/tief/tiefer/a.pdf"},
		},
	}

	for _, f := range faelle {
		t.Run(f.muster, func(t *testing.T) {
			a := &Auftrag{
				Trigger: Trigger{Watch: f.muster},
				Raeume:  map[string]string{RolleInput: raum},
			}
			if a.WatchRekursiv() != f.rekursiv {
				t.Errorf("WatchRekursiv = %v, erwartet %v", a.WatchRekursiv(), f.rekursiv)
			}
			for _, p := range f.trifft {
				if !a.WatchTrifft(p) {
					t.Errorf("%q sollte auslösen", p)
				}
			}
			for _, p := range f.nicht {
				if a.WatchTrifft(p) {
					t.Errorf("%q sollte NICHT auslösen", p)
				}
			}
		})
	}
}

// TestWatchTrifftBleibtImRaum: der Doppelstern bedeutet „unter dem
// input-Raum", nicht „irgendwo im Bau". Gematcht wird deshalb gegen das
// Muster und den Pfad IM RAUM, nicht gegen den zusammengesetzten Pfad —
// sonst zöge ein Auftrag Material aus fremden Räumen herein.
func TestWatchTrifftBleibtImRaum(t *testing.T) {
	a := &Auftrag{
		Trigger: Trigger{Watch: "**/*.pdf"},
		Raeume:  map[string]string{RolleInput: "raeume/eingang/"},
	}
	for _, p := range []string{
		"raeume/archiv/a.pdf",
		"raeume/eingang-alt/a.pdf", // Präfix als Zeichenkette, aber anderer Raum
		"a.pdf",
	} {
		if a.WatchTrifft(p) {
			t.Errorf("%q liegt nicht im input-Raum und darf nicht auslösen", p)
		}
	}
}

// TestWatchTrefferZaehltWieDerWatcherAusloest: `describe auftrag` und
// `findings` zeigen, wie viel gerade im Eingang liegt. Sie taten das mit
// filepath.Glob, und das liest den Doppelstern als einfachen Stern —
// gemeldet wurde eine Datei, während der Watcher zwei ausgelöst hätte.
// Ein Zähler, der etwas anderes zählt als passiert, ist schlimmer als
// keiner, deshalb geht beides jetzt durch dieselbe Funktion.
func TestWatchTrefferZaehltWieDerWatcherAusloest(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{
		"raeume/eingang/flach.txt",
		"raeume/eingang/tief/drin.txt",
		"raeume/eingang/tief/egal.md", // falsche Endung
		"raeume/anderer/fremd.txt",    // anderer Raum
	} {
		pfad := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(pfad), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(pfad, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	a := &Auftrag{
		Trigger: Trigger{Watch: "**/*.txt"},
		Raeume:  map[string]string{RolleInput: "raeume/eingang/"},
	}
	treffer, err := a.WatchTreffer(root, "")
	if err != nil {
		t.Fatal(err)
	}
	erwartet := []string{
		filepath.FromSlash("raeume/eingang/flach.txt"),
		filepath.FromSlash("raeume/eingang/tief/drin.txt"),
	}
	if !reflect.DeepEqual(treffer, erwartet) {
		t.Errorf("Treffer = %v, erwartet %v", treffer, erwartet)
	}
}
