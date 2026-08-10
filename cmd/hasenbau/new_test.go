package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Moritz-Dorn/Hasenbau/internal/auftrag"
	"github.com/Moritz-Dorn/Hasenbau/internal/hase"
)

// Der Kern von Hasenbau-ha0.8: Was `new` schreibt, muss der Bau lesen
// können — sonst steht der Nutzer vor einem Fehler, den er nicht
// verursacht hat.
func TestNewLegtLadbaresAn(t *testing.T) {
	bau := t.TempDir()

	var out, errw strings.Builder
	if code := run([]string{"-bau", bau, "new", "hase", "sortierer"}, &out, &errw); code != 0 {
		t.Fatalf("new hase: exit %d, stderr %q", code, errw.String())
	}
	if _, err := hase.Lade(bau, "sortierer"); err != nil {
		t.Fatalf("erzeugtes Template lädt nicht: %v", err)
	}

	out.Reset()
	errw.Reset()
	if code := run([]string{"-bau", bau, "new", "auftrag", "notizen", "-hase", "sortierer"}, &out, &errw); code != 0 {
		t.Fatalf("new auftrag: exit %d, stderr %q", code, errw.String())
	}
	// Load statt Parse: das ist der Weg, den jeder andere Befehl geht,
	// und er prüft zusätzlich, dass der Hase existiert.
	auftraege, err := auftrag.Load(bau)
	if err != nil {
		t.Fatalf("Bau lädt nach new nicht mehr: %v", err)
	}
	if len(auftraege) != 1 || auftraege[0].Name != "notizen" || auftraege[0].Hase != "sortierer" {
		t.Errorf("geladen: %+v", auftraege)
	}

	// Der Nutzer bekommt gesagt, was als Nächstes dran ist.
	if !strings.Contains(out.String(), "angelegt: auftraege/notizen.md") ||
		!strings.Contains(out.String(), "describe auftrag notizen") {
		t.Errorf("Ausgabe hilft nicht weiter:\n%s", out.String())
	}
}

// Das Gerüst erklärt seine Felder — sonst wäre es nur eine leere Datei
// mit anderem Namen.
func TestNewGeruestErklaertSichSelbst(t *testing.T) {
	bau := t.TempDir()
	var out, errw strings.Builder
	run([]string{"-bau", bau, "new", "hase", "h"}, &out, &errw)
	run([]string{"-bau", bau, "new", "auftrag", "a", "-hase", "h"}, &out, &errw)

	a, err := os.ReadFile(filepath.Join(bau, "auftraege", "a.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, muss := range []string{"watch:", "cron:", "gaenge:", "hase_timeout", "context:", "after:", "quarantine"} {
		if !strings.Contains(string(a), muss) {
			t.Errorf("Auftrags-Gerüst erwähnt %q nicht", muss)
		}
	}

	h, err := os.ReadFile(filepath.Join(bau, "hasen", "h.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, muss := range []string{"description:", "model:", "knows_hasenbau", "knowledge:", "permission:"} {
		if !strings.Contains(string(h), muss) {
			t.Errorf("Hasen-Gerüst erwähnt %q nicht", muss)
		}
	}
	// Der Rückkanal gehört NICHT ins Template: den Absatz hängt der
	// Generator an jeden Agenten selbst an (§8 Phase 2).
	if strings.Contains(string(h), "hasenbau_summary") {
		t.Error("Hasen-Gerüst wiederholt den Rückkanal — der kommt vom Generator")
	}
}

func TestNewUeberschreibtNie(t *testing.T) {
	bau := t.TempDir()
	var out, errw strings.Builder
	run([]string{"-bau", bau, "new", "hase", "h"}, &out, &errw)

	// Eigener Inhalt darf nicht verschwinden.
	pfad := filepath.Join(bau, "hasen", "h.md")
	if err := os.WriteFile(pfad, []byte("meins"), 0o644); err != nil {
		t.Fatal(err)
	}
	errw.Reset()
	if code := run([]string{"-bau", bau, "new", "hase", "h"}, &out, &errw); code != 1 {
		t.Errorf("exit %d, erwartet 1", code)
	}
	if !strings.Contains(errw.String(), "gibt es schon") {
		t.Errorf("Meldung: %q", errw.String())
	}
	if b, _ := os.ReadFile(pfad); string(b) != "meins" {
		t.Errorf("Datei wurde angefasst: %q", b)
	}
}

// Ein Auftrag mit unbekanntem Hasen legt den ganzen Bau lahm — Load
// scheitert dann für ALLE Aufträge. Deshalb hier die Grenze.
func TestNewAuftragBestehtAufVorhandenemHasen(t *testing.T) {
	bau := t.TempDir()

	var out, errw strings.Builder
	if code := run([]string{"-bau", bau, "new", "auftrag", "a"}, &out, &errw); code != 2 {
		t.Errorf("ohne -hase: exit %d, erwartet 2", code)
	}
	if !strings.Contains(errw.String(), "noch keine Hasen") {
		t.Errorf("Meldung ohne Hinweis auf `new hase`: %q", errw.String())
	}

	run([]string{"-bau", bau, "new", "hase", "vorhanden"}, &out, &errw)
	errw.Reset()
	if code := run([]string{"-bau", bau, "new", "auftrag", "a", "-hase", "fehlt"}, &out, &errw); code != 1 {
		t.Errorf("unbekannter Hase: exit %d, erwartet 1", code)
	}
	if !strings.Contains(errw.String(), "vorhanden") {
		t.Errorf("Meldung zählt die vorhandenen Hasen nicht auf: %q", errw.String())
	}
	if _, err := os.Stat(filepath.Join(bau, "auftraege", "a.md")); err == nil {
		t.Error("Auftrag wurde trotzdem angelegt")
	}
}

func TestNewArgumenteInBeidenReihenfolgen(t *testing.T) {
	faelle := []struct {
		name         string
		args         []string
		wollName     string
		wollHase     string
		wollFehlerIn string
	}{
		{"Flag hinten", []string{"notizen", "-hase", "h"}, "notizen", "h", ""},
		{"Flag vorne", []string{"-hase", "h", "notizen"}, "notizen", "h", ""},
		{"mit Gleichheitszeichen", []string{"notizen", "--hase=h"}, "notizen", "h", ""},
		{"ohne Namen", []string{"-hase", "h"}, "", "", "Name fehlt"},
		{"zwei Namen", []string{"a", "b", "-hase", "h"}, "", "", "mehr als ein Name"},
		{"unbekanntes Flag", []string{"a", "-farbe", "braun"}, "", "", "unbekanntes Flag"},
		{"-hase ohne Wert", []string{"a", "-hase"}, "", "", "ohne Wert"},
	}
	for _, f := range faelle {
		t.Run(f.name, func(t *testing.T) {
			name, haseName, err := parseNewAuftragArgs(f.args)
			if f.wollFehlerIn != "" {
				if err == nil || !strings.Contains(err.Error(), f.wollFehlerIn) {
					t.Fatalf("erwartet Fehler mit %q, bekam %v", f.wollFehlerIn, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if name != f.wollName || haseName != f.wollHase {
				t.Errorf("name=%q hase=%q", name, haseName)
			}
		})
	}
}

func TestNewUnbekannteRessource(t *testing.T) {
	var out, errw strings.Builder
	if code := run([]string{"-bau", t.TempDir(), "new", "gang", "x"}, &out, &errw); code != 2 {
		t.Errorf("exit %d, erwartet 2", code)
	}
	if !strings.Contains(errw.String(), "unbekannte Ressource") {
		t.Errorf("Meldung: %q", errw.String())
	}
	errw.Reset()
	if code := run([]string{"-bau", t.TempDir(), "new"}, &out, &errw); code != 2 {
		t.Errorf("ohne Ressource: exit %d, erwartet 2", code)
	}
}

// Ein ungültiger Name landet nie als Datei im Bau.
func TestNewLehntUngueltigeNamenAb(t *testing.T) {
	bau := t.TempDir()
	var out, errw strings.Builder
	for _, name := range []string{"mit/slash", "-beginnt-mit-strich", ""} {
		errw.Reset()
		if code := run([]string{"-bau", bau, "new", "hase", name}, &out, &errw); code == 0 {
			t.Errorf("Name %q wurde angenommen", name)
		}
	}
	if eintraege, _ := os.ReadDir(filepath.Join(bau, "hasen")); len(eintraege) != 0 {
		t.Errorf("Dateien angelegt: %v", eintraege)
	}
}
