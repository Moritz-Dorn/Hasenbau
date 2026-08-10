package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Fehler der Lauf-Auflösung für den Rückkanal (PLAN.md §8, Phase 2).
// opencode reicht an MCP-Tools keinen Session-Kontext durch — der
// Rückkanal erkennt den aufrufenden Lauf deshalb daran, dass genau
// einer läuft. Beide Fälle sind für den Hasen sichtbare Fehler, nie
// ein geratener Schreibvorgang.
var (
	ErrNoActiveLauf = errors.New("store: kein aktiver Lauf")
	ErrAmbiguous    = errors.New("store: mehrere aktive Läufe")
)

// ActiveLauf liefert den einzigen Lauf mit status='running'. Bei keinem
// oder mehreren Treffern kommt ErrNoActiveLauf bzw. ErrAmbiguous —
// letzterer mit den Kandidaten im Text.
//
// Zeilen, deren Wirt nicht mehr lebt, zählen nicht mit (verwaist.go):
// sie gehören zu keinem laufenden Hasen, können den Aufrufer also auch
// nicht meinen. So bleibt der Rückkanal auch in der Lücke zwischen
// einem Absturz und dem nächsten Aufräumen benutzbar.
func (s *Store) ActiveLauf() (*Lauf, error) {
	rows, err := s.db.Query(`
		SELECT id, auftrag, "trigger", COALESCE(input,''), started,
		       pid, pid_started
		FROM laeufe WHERE status = 'running' ORDER BY started, id`)
	if err != nil {
		return nil, fmt.Errorf("store: aktive Läufe lesen: %w", err)
	}
	defer rows.Close()

	var aktive []Lauf
	for rows.Next() {
		var l Lauf
		var pid sql.NullInt64
		var pidStarted sql.NullTime
		if err := rows.Scan(&l.ID, &l.Auftrag, &l.Trigger, &l.Input,
			&l.Started, &pid, &pidStarted); err != nil {
			return nil, fmt.Errorf("store: aktiven Lauf scannen: %w", err)
		}
		if orphaned(pid, pidStarted) {
			continue
		}
		l.Status = "running"
		aktive = append(aktive, l)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	switch len(aktive) {
	case 1:
		return &aktive[0], nil
	case 0:
		return nil, ErrNoActiveLauf
	default:
		var kandidaten []string
		for _, l := range aktive {
			kandidaten = append(kandidaten, fmt.Sprintf("%d (%s, seit %s)",
				l.ID, l.Auftrag, l.Started.Local().Format("2006-01-02 15:04")))
		}
		return nil, fmt.Errorf("%w: %s", ErrAmbiguous, strings.Join(kandidaten, ", "))
	}
}

// WriteNote hängt eine Notiz des Hasen an den Lauf.
func (s *Store) WriteNote(lauf int64, text string) error {
	if _, err := s.db.Exec(
		`INSERT INTO notes (lauf, written, text) VALUES (?, ?, ?)`,
		lauf, time.Now().UTC(), text); err != nil {
		return fmt.Errorf("store: Notiz zu Lauf %d: %w", lauf, err)
	}
	return nil
}

// SummaryLine presst einen Text in eine Summary-Zeile (§5: „eine
// Zeile: was ist passiert") und kappt Ausschweifungen. Der Store hält
// die Invariante für beide Wege — Rückkanal wie Fallback.
func SummaryLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	const max = 500
	if runen := []rune(s); len(runen) > max {
		return string(runen[:max]) + "…"
	}
	return s
}

// WriteSummary setzt die Summary eines laufenden Laufs. Der Hase
// darf sich korrigieren — der letzte Aufruf gewinnt; LaufBeende
// überschreibt eine so gesetzte Summary nicht mehr (§5).
func (s *Store) WriteSummary(lauf int64, text string) error {
	res, err := s.db.Exec(`UPDATE laeufe SET summary = ? WHERE id = ?`, SummaryLine(text), lauf)
	if err != nil {
		return fmt.Errorf("store: Summary zu Lauf %d: %w", lauf, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: Summary zu Lauf %d: %w", lauf, err)
	}
	if n == 0 {
		return fmt.Errorf("store: kein Lauf mit ID %d", lauf)
	}
	return nil
}

// Note ist eine Notiz des Hasen zu einem Lauf.
type Note struct {
	Written time.Time
	Text    string
}

// Notes liefert die Notizen eines Laufs in Schreibreihenfolge.
func (s *Store) Notes(lauf int64) ([]Note, error) {
	rows, err := s.db.Query(
		`SELECT written, text FROM notes WHERE lauf = ? ORDER BY id`, lauf)
	if err != nil {
		return nil, fmt.Errorf("store: Notizen zu Lauf %d lesen: %w", lauf, err)
	}
	defer rows.Close()

	var out []Note
	for rows.Next() {
		var n Note
		if err := rows.Scan(&n.Written, &n.Text); err != nil {
			return nil, fmt.Errorf("store: Notiz scannen: %w", err)
		}
		out = append(out, n)
	}
	return out, rows.Err()
}
