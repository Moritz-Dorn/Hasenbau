package store

import (
	"path/filepath"
	"testing"
	"time"
)

func TestOpenMigriertUndIstIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "hasenbau.db")

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}

	var mode string
	if err := s.db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != "wal" {
		t.Errorf("journal_mode = %q, erwartet wal", mode)
	}

	for _, tbl := range []string{"laeufe", "auftrag_state", "gesehen"} {
		var n int
		if err := s.db.QueryRow(
			"SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?", tbl,
		).Scan(&n); err != nil || n != 1 {
			t.Errorf("Tabelle %s fehlt (n=%d, err=%v)", tbl, n, err)
		}
	}
	var n int
	if err := s.db.QueryRow(
		"SELECT count(*) FROM sqlite_master WHERE type='index' AND name='idx_laeufe_auftrag'",
	).Scan(&n); err != nil || n != 1 {
		t.Errorf("Index idx_laeufe_auftrag fehlt (n=%d, err=%v)", n, err)
	}

	var version int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != len(migrations) {
		t.Errorf("user_version = %d, erwartet %d", version, len(migrations))
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	// Zweites Open auf derselben Datei: Migrationen dürfen nicht erneut
	// laufen (CREATE TABLE würde knallen) und nichts verlieren.
	s2, err := Open(path)
	if err != nil {
		t.Fatalf("zweites Open: %v", err)
	}
	defer s2.Close()
	if err := s2.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != len(migrations) {
		t.Errorf("user_version nach Reopen = %d (err=%v)", version, err)
	}
}

func TestTriggerSpalteIstBenutzbar(t *testing.T) {
	// "trigger" ist ein SQLite-Schlüsselwort — der Test stellt sicher,
	// dass Schreiben und Lesen mit Quoting funktionieren.
	s, err := Open(filepath.Join(t.TempDir(), "hasenbau.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if _, err := s.db.Exec(
		`INSERT INTO laeufe (auftrag, "trigger", gestartet, status) VALUES (?, ?, ?, ?)`,
		"pdf-einlagern", "watch", time.Now().UTC(), "laeuft",
	); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	var trigger, status string
	if err := s.db.QueryRow(
		`SELECT "trigger", status FROM laeufe WHERE auftrag = ?`, "pdf-einlagern",
	).Scan(&trigger, &status); err != nil {
		t.Fatalf("Select: %v", err)
	}
	if trigger != "watch" || status != "laeuft" {
		t.Errorf("gelesen: trigger=%q status=%q", trigger, status)
	}
}

func TestLetzteSummaries(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "hasenbau.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	basis := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	einfuegen := func(auftrag, summary string, minuten int) {
		t.Helper()
		if _, err := s.db.Exec(
			`INSERT INTO laeufe (auftrag, "trigger", gestartet, status, summary) VALUES (?, ?, ?, ?, ?)`,
			auftrag, "watch", basis.Add(time.Duration(minuten)*time.Minute), "ok", summary,
		); err != nil {
			t.Fatal(err)
		}
	}
	einfuegen("pdf-einlagern", "erster", 0)
	einfuegen("pdf-einlagern", "", 1) // leere Summary zählt nicht
	einfuegen("anderer", "fremd", 2)  // anderer Auftrag zählt nicht
	einfuegen("pdf-einlagern", "zweiter", 3)
	einfuegen("pdf-einlagern", "dritter", 4)

	got, err := s.LetzteSummaries("pdf-einlagern", 2)
	if err != nil {
		t.Fatal(err)
	}
	// Die jüngsten 2, chronologisch (älteste zuerst) für den Prompt.
	if len(got) != 2 || got[0] != "zweiter" || got[1] != "dritter" {
		t.Errorf("LetzteSummaries = %v", got)
	}

	if got, err := s.LetzteSummaries("pdf-einlagern", 0); err != nil || got != nil {
		t.Errorf("n=0: %v, %v", got, err)
	}
	if got, err := s.LetzteSummaries("unbekannt", 3); err != nil || len(got) != 0 {
		t.Errorf("unbekannter Auftrag: %v, %v", got, err)
	}
}

func TestOpenLehntNeuereDBAb(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hasenbau.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("PRAGMA user_version = 99"); err != nil {
		t.Fatal(err)
	}
	s.Close()

	if _, err := Open(path); err == nil {
		t.Error("Open muss eine DB mit höherer user_version ablehnen")
	}
}
