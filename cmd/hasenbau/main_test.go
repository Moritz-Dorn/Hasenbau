package main

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Moritz-Dorn/Hasenbau/internal/store"
)

func TestUnbekannterBefehlUndUsage(t *testing.T) {
	var out, errw strings.Builder
	if code := run([]string{"quatsch"}, &out, &errw); code != 2 {
		t.Errorf("exit %d, erwartet 2", code)
	}
	if !strings.Contains(errw.String(), "unbekannter Befehl") {
		t.Errorf("Fehlermeldung fehlt: %q", errw.String())
	}
	errw.Reset()
	if code := run(nil, &out, &errw); code != 2 || !strings.Contains(errw.String(), "Befehle:") {
		t.Errorf("ohne Argumente: exit %d, usage %q", code, errw.String())
	}
}

func TestLaufUnbekannterAuftrag(t *testing.T) {
	// Leerer Bau: der Auftrag existiert nicht — sauberer Fehler, bevor
	// irgendein Server gestartet wird.
	var out, errw strings.Builder
	if code := run([]string{"-bau", t.TempDir(), "lauf", "pdf-einlagern"}, &out, &errw); code != 1 {
		t.Errorf("exit %d, erwartet 1", code)
	}
	if !strings.Contains(errw.String(), "unbekannter Auftrag") {
		t.Errorf("Fehlermeldung fehlt: %q", errw.String())
	}
}

func TestGrabenFehlerpfade(t *testing.T) {
	bau := t.TempDir()

	// Unbekannter Lauf: klarer Fehler, bevor irgendein Server startet.
	var out, errw strings.Builder
	if code := run([]string{"-bau", bau, "graben", "7"}, &out, &errw); code != 1 {
		t.Errorf("unbekannter Lauf: exit %d, erwartet 1", code)
	}
	if !strings.Contains(errw.String(), "kein Lauf mit ID 7") {
		t.Errorf("Fehlermeldung fehlt: %q", errw.String())
	}

	// Lauf ohne Session (Gang scheiterte vor dem Hasen): klarer Fehler.
	st, err := store.Open(filepath.Join(bau, "state", "hasenbau.db"))
	if err != nil {
		t.Fatal(err)
	}
	id, err := st.LaufBeginne("pdf-einlagern", "watch", "kaputt.pdf")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.LaufBeende(id, store.LaufErgebnis{Status: "fehler", Fehler: "gang kaputt"}); err != nil {
		t.Fatal(err)
	}
	st.Close()

	errw.Reset()
	if code := run([]string{"-bau", bau, "graben", "1"}, &out, &errw); code != 1 {
		t.Errorf("Lauf ohne Session: exit %d, erwartet 1", code)
	}
	if !strings.Contains(errw.String(), "hat keine Session") {
		t.Errorf("Fehlermeldung fehlt: %q", errw.String())
	}

	// Kaputte Lauf-ID.
	errw.Reset()
	if code := run([]string{"-bau", bau, "graben", "vier"}, &out, &errw); code != 2 {
		t.Errorf("ungültige ID: exit %d, erwartet 2", code)
	}
}

func TestLaeufeUndStatus(t *testing.T) {
	bau := t.TempDir()

	var out, errw strings.Builder
	if code := run([]string{"-bau", bau, "laeufe"}, &out, &errw); code != 0 {
		t.Fatalf("laeufe (leer): exit %d, stderr %q", code, errw.String())
	}
	if !strings.Contains(out.String(), "keine Läufe") {
		t.Errorf("Leer-Ausgabe: %q", out.String())
	}

	// Einen Lauf direkt einfügen — hier zählt nur der Lesepfad.
	seed(t, filepath.Join(bau, "state", "hasenbau.db"))

	out.Reset()
	if code := run([]string{"-bau", bau, "laeufe"}, &out, &errw); code != 0 {
		t.Fatalf("laeufe: exit %d, stderr %q", code, errw.String())
	}
	if !strings.Contains(out.String(), "pdf-einlagern") || !strings.Contains(out.String(), "watch") {
		t.Errorf("Lauf fehlt in Ausgabe: %q", out.String())
	}

	out.Reset()
	if code := run([]string{"-bau", bau, "status"}, &out, &errw); code != 0 {
		t.Fatalf("status: exit %d, stderr %q", code, errw.String())
	}
	got := out.String()
	if !strings.Contains(got, "1 gesamt") || !strings.Contains(got, "1 ok") {
		t.Errorf("Status-Zähler fehlen: %q", got)
	}
	if !strings.Contains(got, "FEHLERSERIE") || !strings.Contains(got, "pdf-einlagern") {
		t.Errorf("Auftrag-Zustand fehlt: %q", got)
	}
}

func seed(t *testing.T, dbFile string) {
	t.Helper()
	st, err := store.Open(dbFile) // legt Schema an
	if err != nil {
		t.Fatal(err)
	}
	st.Close()

	db, err := sql.Open("sqlite", "file:"+dbFile)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`
		INSERT INTO laeufe (auftrag, "trigger", gestartet, beendet, status, summary)
		VALUES ('pdf-einlagern', 'watch', datetime('now','-2 minutes'), datetime('now'), 'ok', 'Rechnung einsortiert')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO auftrag_state (auftrag, letzter_lauf, letzter_ok, fehler_serie)
		VALUES ('pdf-einlagern', datetime('now'), datetime('now'), 0)`); err != nil {
		t.Fatal(err)
	}
}
