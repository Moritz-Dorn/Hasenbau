// Package store persistiert, was der Hasenbau tut: Läufe,
// Auftrags-Zustand, Notizen der Hasen und den Idempotenz-Backstop
// (PLAN.md §5). SQLite im WAL-Modus, pure Go (modernc.org/sqlite,
// kein cgo). Bewusst klein —
// vier Tabellen, nicht dreißig. Nicht verwechseln mit Beads (§9):
// Beads trackt, wie der Hasenbau gebaut wird.
package store

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// migrations laufen der Reihe nach; PRAGMA user_version merkt sich den
// Stand. Bestehende Einträge nie ändern — nur hinten anhängen.
var migrations = [][]string{
	{
		// "trigger" ist ein SQLite-Schlüsselwort und muss überall
		// gequotet werden — auch in späteren Queries.
		`CREATE TABLE laeufe (
			id            INTEGER PRIMARY KEY,
			auftrag       TEXT NOT NULL,
			"trigger"     TEXT NOT NULL,     -- 'cron' | 'watch' | 'manuell'
			ausloeser     TEXT,              -- z.B. der Pfad der auslösenden Datei
			gestartet     TIMESTAMP NOT NULL,
			beendet       TIMESTAMP,
			status        TEXT NOT NULL,     -- 'laeuft' | 'ok' | 'fehler' | 'abgebrochen'
			session_id    TEXT,              -- opencode-Session, für Nachforschung
			summary       TEXT,              -- eine Zeile: was ist passiert
			output_pfad   TEXT,
			fehler        TEXT,
			tokens_in     INTEGER,
			tokens_out    INTEGER,
			kosten_cent   INTEGER
		)`,
		`CREATE INDEX idx_laeufe_auftrag ON laeufe(auftrag, gestartet DESC)`,
		`CREATE TABLE auftrag_state (
			auftrag       TEXT PRIMARY KEY,
			letzter_lauf  TIMESTAMP,
			letzter_ok    TIMESTAMP,
			fehler_serie  INTEGER NOT NULL DEFAULT 0
		)`,
		// Idempotenz-Backstop: der eigentliche Mechanismus ist der Move
		// nach archiv/ — das hier fängt nur fehlgeschlagene Moves ab (§7).
		`CREATE TABLE gesehen (
			auftrag       TEXT NOT NULL,
			quelle_hash   TEXT NOT NULL,     -- sha256 des Trigger-Inputs
			gesehen_am    TIMESTAMP NOT NULL,
			PRIMARY KEY (auftrag, quelle_hash)
		)`,
	},
	{
		// Notizen des Hasen über den Rückkanal (§8, Phase 2). Anders als
		// die Summary (eine Zeile pro Lauf, in laeufe) beliebig viele
		// pro Lauf — deshalb eine eigene Tabelle statt einer Spalte.
		`CREATE TABLE notizen (
			id            INTEGER PRIMARY KEY,
			lauf          INTEGER NOT NULL REFERENCES laeufe(id),
			geschrieben   TIMESTAMP NOT NULL,
			text          TEXT NOT NULL
		)`,
		`CREATE INDEX idx_notizen_lauf ON notizen(lauf, id)`,
	},
}

// Store ist die Datenbank des Baus (state/hasenbau.db).
type Store struct {
	db *sql.DB
}

// Open öffnet (oder erzeugt) die Datenbank, aktiviert WAL und bringt
// das Schema auf den aktuellen Stand. Fehlende Elternverzeichnisse
// werden angelegt.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("store: Verzeichnis anlegen: %w", err)
	}
	dsn := "file:" + url.PathEscape(path) +
		"?_pragma=journal_mode(WAL)" +
		"&_pragma=busy_timeout(5000)" +
		"&_pragma=synchronous(NORMAL)" +
		"&_pragma=foreign_keys(ON)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: öffnen: %w", err)
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// migrate führt alle noch nicht angewandten Migrationen aus, jede in
// einer eigenen Transaktion. Idempotent über PRAGMA user_version.
func (s *Store) migrate() error {
	var version int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("store: user_version lesen: %w", err)
	}
	if version > len(migrations) {
		return fmt.Errorf("store: Datenbank ist neuer als der Code (user_version %d > %d)", version, len(migrations))
	}
	for i := version; i < len(migrations); i++ {
		tx, err := s.db.Begin()
		if err != nil {
			return fmt.Errorf("store: Migration %d: %w", i+1, err)
		}
		for _, stmt := range migrations[i] {
			if _, err := tx.Exec(stmt); err != nil {
				tx.Rollback()
				return fmt.Errorf("store: Migration %d: %w", i+1, err)
			}
		}
		if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", i+1)); err != nil {
			tx.Rollback()
			return fmt.Errorf("store: Migration %d: user_version setzen: %w", i+1, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("store: Migration %d: %w", i+1, err)
		}
	}
	return nil
}

// Close schließt die Datenbank.
func (s *Store) Close() error {
	return s.db.Close()
}
