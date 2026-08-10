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
	{
		// Wer hält den Lauf? Ohne den Wirt ist nach einem harten
		// Abbruch nicht zu unterscheiden, ob eine 'laeuft'-Zeile einem
		// lebenden Prozess gehört oder eine Leiche ist (verwaist.go).
		// Die PID allein reicht nicht — sie wird recycelt.
		`ALTER TABLE laeufe ADD COLUMN pid INTEGER`,
		`ALTER TABLE laeufe ADD COLUMN pid_gestartet TIMESTAMP`,
	},
	{
		// Der Verlauf eines Laufs, beim Lauf-Ende abgelegt (trace.go).
		// json ist bewusst opak: das Format gehört internal/opencode.
		`CREATE TABLE trace (
			lauf          INTEGER PRIMARY KEY REFERENCES laeufe(id),
			session_id    TEXT NOT NULL,
			json          TEXT NOT NULL,
			geschrieben   TIMESTAMP NOT NULL
		)`,
	},
	{
		// Alles, was kein Domänen-Eigenname ist, wird englisch (PLAN.md
		// §1). Die Eigennamen bleiben: laeufe, lauf, auftrag, gaenge,
		// raeume. Die Migrationen darüber bleiben unangetastet — der
		// Stand von damals ist der Stand von damals.
		`ALTER TABLE laeufe RENAME COLUMN ausloeser TO input`,
		`ALTER TABLE laeufe RENAME COLUMN gestartet TO started`,
		`ALTER TABLE laeufe RENAME COLUMN beendet TO ended`,
		`ALTER TABLE laeufe RENAME COLUMN fehler TO error`,
		`ALTER TABLE laeufe RENAME COLUMN output_pfad TO output_path`,
		`ALTER TABLE laeufe RENAME COLUMN kosten_cent TO cost_cent`,
		`ALTER TABLE laeufe RENAME COLUMN pid_gestartet TO pid_started`,
		`ALTER TABLE auftrag_state RENAME COLUMN letzter_lauf TO last_lauf`,
		`ALTER TABLE auftrag_state RENAME COLUMN letzter_ok TO last_ok`,
		`ALTER TABLE auftrag_state RENAME COLUMN fehler_serie TO error_streak`,
		`ALTER TABLE notizen RENAME TO notes`,
		`ALTER TABLE notes RENAME COLUMN geschrieben TO written`,
		`ALTER TABLE gesehen RENAME TO seen`,
		`ALTER TABLE seen RENAME COLUMN quelle_hash TO source_hash`,
		`ALTER TABLE seen RENAME COLUMN gesehen_am TO seen_at`,
		`ALTER TABLE trace RENAME COLUMN geschrieben TO written`,
		`DROP INDEX idx_notizen_lauf`,
		`CREATE INDEX idx_notes_lauf ON notes(lauf, id)`,

		// Auch die Zustandswerte selbst — sonst stünde in der Spalte
		// weiter Deutsch, und der Code müsste beides kennen.
		`UPDATE laeufe SET status = 'running' WHERE status = 'laeuft'`,
		`UPDATE laeufe SET status = 'failed' WHERE status = 'fehler'`,
		`UPDATE laeufe SET status = 'aborted' WHERE status = 'abgebrochen'`,
		`UPDATE laeufe SET "trigger" = 'manual' WHERE "trigger" = 'manuell'`,

		// Die Trace-JSON-Schlüssel wandern mit (art→kind, rolle→role,
		// fehler→error, ende→end, schritte→steps). Bestehende Zeilen
		// wären danach unlesbar; sie fliegen raus, und `hasenbau dig`
		// holt den Trace bei Bedarf wieder vom Server.
		`DELETE FROM trace`,
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
