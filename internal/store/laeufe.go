package store

import (
	"database/sql"
	"fmt"
	"time"
)

// Lauf ist eine Ausführung eines Auftrags (PLAN.md §1, §5).
type Lauf struct {
	ID        int64
	Auftrag   string
	Trigger   string // 'cron' | 'watch' | 'manuell'
	Ausloeser string
	Gestartet time.Time
	Beendet   *time.Time
	Status    string // 'laeuft' | 'ok' | 'fehler' | 'abgebrochen'
	SessionID string
	Summary   string
	Fehler    string
	TokensIn  int64
	TokensOut int64
}

// LetzteLaeufe liefert die jüngsten Läufe, neueste zuerst.
func (s *Store) LetzteLaeufe(limit int) ([]Lauf, error) {
	rows, err := s.db.Query(`
		SELECT id, auftrag, "trigger", COALESCE(ausloeser,''), gestartet,
		       beendet, status, COALESCE(session_id,''), COALESCE(summary,''),
		       COALESCE(fehler,''), COALESCE(tokens_in,0), COALESCE(tokens_out,0)
		FROM laeufe ORDER BY gestartet DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("store: Läufe lesen: %w", err)
	}
	defer rows.Close()

	var out []Lauf
	for rows.Next() {
		var l Lauf
		var beendet sql.NullTime
		if err := rows.Scan(&l.ID, &l.Auftrag, &l.Trigger, &l.Ausloeser,
			&l.Gestartet, &beendet, &l.Status, &l.SessionID, &l.Summary,
			&l.Fehler, &l.TokensIn, &l.TokensOut); err != nil {
			return nil, fmt.Errorf("store: Lauf scannen: %w", err)
		}
		if beendet.Valid {
			t := beendet.Time
			l.Beendet = &t
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// StatusZaehler liefert die Anzahl Läufe je Status.
func (s *Store) StatusZaehler() (map[string]int, error) {
	rows, err := s.db.Query(`SELECT status, count(*) FROM laeufe GROUP BY status`)
	if err != nil {
		return nil, fmt.Errorf("store: Status zählen: %w", err)
	}
	defer rows.Close()

	counts := map[string]int{}
	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			return nil, err
		}
		counts[status] = n
	}
	return counts, rows.Err()
}

// AuftragState ist der aggregierte Zustand eines Auftrags (§5).
type AuftragState struct {
	Auftrag     string
	LetzterLauf *time.Time
	LetzterOk   *time.Time
	FehlerSerie int
}

// AuftragStates liefert den Zustand aller bekannten Aufträge.
func (s *Store) AuftragStates() ([]AuftragState, error) {
	rows, err := s.db.Query(`
		SELECT auftrag, letzter_lauf, letzter_ok, fehler_serie
		FROM auftrag_state ORDER BY auftrag`)
	if err != nil {
		return nil, fmt.Errorf("store: Auftrag-Zustände lesen: %w", err)
	}
	defer rows.Close()

	var out []AuftragState
	for rows.Next() {
		var a AuftragState
		var lauf, ok sql.NullTime
		if err := rows.Scan(&a.Auftrag, &lauf, &ok, &a.FehlerSerie); err != nil {
			return nil, err
		}
		if lauf.Valid {
			t := lauf.Time
			a.LetzterLauf = &t
		}
		if ok.Valid {
			t := ok.Time
			a.LetzterOk = &t
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
