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

	for _, tbl := range []string{"laeufe", "auftrag_state", "seen", "notes", "trace"} {
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

	// Zweites Open auf derselben File: Migrationen dürfen nicht erneut
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
		`INSERT INTO laeufe (auftrag, "trigger", started, status) VALUES (?, ?, ?, ?)`,
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
			`INSERT INTO laeufe (auftrag, "trigger", started, status, summary) VALUES (?, ?, ?, ?, ?)`,
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

	got, err := s.RecentSummaries("pdf-einlagern", 2)
	if err != nil {
		t.Fatal(err)
	}
	// Die jüngsten 2, chronologisch (älteste zuerst) für den Prompt.
	if len(got) != 2 || got[0] != "zweiter" || got[1] != "dritter" {
		t.Errorf("LastSummaries = %v", got)
	}

	if got, err := s.RecentSummaries("pdf-einlagern", 0); err != nil || got != nil {
		t.Errorf("n=0: %v, %v", got, err)
	}
	if got, err := s.RecentSummaries("unbekannt", 3); err != nil || len(got) != 0 {
		t.Errorf("unbekannter Auftrag: %v, %v", got, err)
	}
}

func TestGesehenBackstop(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "hasenbau.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if ok, err := s.IsSeen("pdf-einlagern", "abc123"); err != nil || ok {
		t.Errorf("frisch: ok=%v err=%v", ok, err)
	}
	if err := s.MarkSeen("pdf-einlagern", "abc123"); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkSeen("pdf-einlagern", "abc123"); err != nil {
		t.Errorf("MarkSeen nicht idempotent: %v", err)
	}
	if ok, err := s.IsSeen("pdf-einlagern", "abc123"); err != nil || !ok {
		t.Errorf("nach Merken: ok=%v err=%v", ok, err)
	}
	// Hash ist pro Auftrag — ein anderer Auftrag darf denselben Input sehen.
	if ok, err := s.IsSeen("anderer", "abc123"); err != nil || ok {
		t.Errorf("fremder Auftrag: ok=%v err=%v", ok, err)
	}
}

// Hasenbau-do0.2: Grundlage des Deckels. Alle Läufe zählen, auch die
// gescheiterten — ein Lauf, der scheitert, hat das Modell trotzdem
// gekostet, und ein Deckel, der nur ok zählt, wäre bei einem kaputten
// Auftrag gar keiner.
func TestLaeufeSinceZaehltAlleZustaende(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "hasenbau.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	for _, status := range []string{"ok", "failed", "aborted"} {
		id, err := s.StartLauf("pdf-einlagern", "watch", "x.pdf")
		if err != nil {
			t.Fatal(err)
		}
		if err := s.EndLauf(id, LaufResult{Status: status, Error: "egal"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.StartLauf("anderer", "cron", ""); err != nil {
		t.Fatal(err)
	}

	starts, err := s.LaeufeSince("pdf-einlagern", time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(starts) != 3 {
		t.Errorf("%d Läufe im Fenster, erwartet 3 (ok, failed, aborted)", len(starts))
	}
	for i := 1; i < len(starts); i++ {
		if starts[i].Before(starts[i-1]) {
			t.Errorf("nicht aufsteigend: %v vor %v", starts[i], starts[i-1])
		}
	}
	// Ein Fenster in der Zukunft ist leer — der Vergleich ist echt.
	spaet, err := s.LaeufeSince("pdf-einlagern", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(spaet) != 0 {
		t.Errorf("%d Läufe nach dem Fenster", len(spaet))
	}
}

func TestLaufBeginneUndBeende(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "hasenbau.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	id, err := s.StartLauf("pdf-einlagern", "watch", "raeume/laderampe/sources/a.pdf")
	if err != nil {
		t.Fatal(err)
	}

	laeufe, err := s.RecentLaeufe(10)
	if err != nil || len(laeufe) != 1 {
		t.Fatalf("nach Beginne: %v, %v", laeufe, err)
	}
	l := laeufe[0]
	if l.ID != id || l.Status != "running" || l.Ended != nil || l.Input != "raeume/laderampe/sources/a.pdf" {
		t.Errorf("Lauf = %+v", l)
	}
	states, err := s.AuftragStates()
	if err != nil || len(states) != 1 || states[0].LastLauf == nil || states[0].LastOk != nil {
		t.Errorf("nach Beginne: states=%+v err=%v", states, err)
	}

	if err := s.EndLauf(id, LaufResult{
		Status: "ok", SessionID: "ses_1", Summary: "einsortiert",
		TokensIn: 100, TokensOut: 20, CostCent: 3,
	}); err != nil {
		t.Fatal(err)
	}
	laeufe, _ = s.RecentLaeufe(10)
	l = laeufe[0]
	if l.Status != "ok" || l.Ended == nil || l.SessionID != "ses_1" ||
		l.Summary != "einsortiert" || l.TokensIn != 100 || l.TokensOut != 20 || l.CostCent != 3 {
		t.Errorf("Lauf nach Beende = %+v", l)
	}
	states, _ = s.AuftragStates()
	if states[0].LastOk == nil || states[0].ErrorStreak != 0 {
		t.Errorf("nach ok: state=%+v", states[0])
	}

	// Zwei Fehlläufe zählen die Serie hoch; ein ok setzt sie zurück.
	for i := 0; i < 2; i++ {
		id, err := s.StartLauf("pdf-einlagern", "cron", "")
		if err != nil {
			t.Fatal(err)
		}
		if err := s.EndLauf(id, LaufResult{Status: "failed", Error: "gang kaputt"}); err != nil {
			t.Fatal(err)
		}
	}
	states, _ = s.AuftragStates()
	if states[0].ErrorStreak != 2 {
		t.Errorf("ErrorStreak = %d, erwartet 2", states[0].ErrorStreak)
	}
	id, _ = s.StartLauf("pdf-einlagern", "manual", "")
	if err := s.EndLauf(id, LaufResult{Status: "ok"}); err != nil {
		t.Fatal(err)
	}
	states, _ = s.AuftragStates()
	if states[0].ErrorStreak != 0 {
		t.Errorf("ErrorStreak nach ok = %d", states[0].ErrorStreak)
	}
}

func TestLaufValidiert(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "hasenbau.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if _, err := s.StartLauf("x", "gedanke", ""); err == nil {
		t.Error("unbekannter Trigger muss fehlschlagen")
	}
	id, err := s.StartLauf("x", "cron", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.EndLauf(id, LaufResult{Status: "laeuft"}); err == nil {
		t.Error("'running' ist kein Endstatus")
	}
	if err := s.EndLauf(999, LaufResult{Status: "ok"}); err == nil {
		t.Error("unbekannte Lauf-ID muss fehlschlagen")
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
