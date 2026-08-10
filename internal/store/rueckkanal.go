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
	ErrKeinAktiverLauf = errors.New("store: kein aktiver Lauf")
	ErrMehrdeutig      = errors.New("store: mehrere aktive Läufe")
)

// AktiverLauf liefert den einzigen Lauf mit status='laeuft'. Bei keinem
// oder mehreren Treffern kommt ErrKeinAktiverLauf bzw. ErrMehrdeutig —
// letzterer mit den Kandidaten im Text.
//
// Zeilen, deren Wirt nicht mehr lebt, zählen nicht mit (verwaist.go):
// sie gehören zu keinem laufenden Hasen, können den Aufrufer also auch
// nicht meinen. So bleibt der Rückkanal auch in der Lücke zwischen
// einem Absturz und dem nächsten Aufräumen benutzbar.
func (s *Store) AktiverLauf() (*Lauf, error) {
	rows, err := s.db.Query(`
		SELECT id, auftrag, "trigger", COALESCE(ausloeser,''), gestartet,
		       pid, pid_gestartet
		FROM laeufe WHERE status = 'laeuft' ORDER BY gestartet, id`)
	if err != nil {
		return nil, fmt.Errorf("store: aktive Läufe lesen: %w", err)
	}
	defer rows.Close()

	var aktive []Lauf
	for rows.Next() {
		var l Lauf
		var pid sql.NullInt64
		var pidGestartet sql.NullTime
		if err := rows.Scan(&l.ID, &l.Auftrag, &l.Trigger, &l.Ausloeser,
			&l.Gestartet, &pid, &pidGestartet); err != nil {
			return nil, fmt.Errorf("store: aktiven Lauf scannen: %w", err)
		}
		if verwaist(pid, pidGestartet) {
			continue
		}
		l.Status = "laeuft"
		aktive = append(aktive, l)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	switch len(aktive) {
	case 1:
		return &aktive[0], nil
	case 0:
		return nil, ErrKeinAktiverLauf
	default:
		var kandidaten []string
		for _, l := range aktive {
			kandidaten = append(kandidaten, fmt.Sprintf("%d (%s, seit %s)",
				l.ID, l.Auftrag, l.Gestartet.Local().Format("2006-01-02 15:04")))
		}
		return nil, fmt.Errorf("%w: %s", ErrMehrdeutig, strings.Join(kandidaten, ", "))
	}
}

// NotizSchreibe hängt eine Notiz des Hasen an den Lauf.
func (s *Store) NotizSchreibe(lauf int64, text string) error {
	if _, err := s.db.Exec(
		`INSERT INTO notizen (lauf, geschrieben, text) VALUES (?, ?, ?)`,
		lauf, time.Now().UTC(), text); err != nil {
		return fmt.Errorf("store: Notiz zu Lauf %d: %w", lauf, err)
	}
	return nil
}

// SummaryZeile presst einen Text in eine Summary-Zeile (§5: „eine
// Zeile: was ist passiert") und kappt Ausschweifungen. Der Store hält
// die Invariante für beide Wege — Rückkanal wie Fallback.
func SummaryZeile(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	const max = 500
	if runen := []rune(s); len(runen) > max {
		return string(runen[:max]) + "…"
	}
	return s
}

// SummarySchreibe setzt die Summary eines laufenden Laufs. Der Hase
// darf sich korrigieren — der letzte Aufruf gewinnt; LaufBeende
// überschreibt eine so gesetzte Summary nicht mehr (§5).
func (s *Store) SummarySchreibe(lauf int64, text string) error {
	res, err := s.db.Exec(`UPDATE laeufe SET summary = ? WHERE id = ?`, SummaryZeile(text), lauf)
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

// Notiz ist eine Notiz des Hasen zu einem Lauf.
type Notiz struct {
	Geschrieben time.Time
	Text        string
}

// Notizen liefert die Notizen eines Laufs in Schreibreihenfolge.
func (s *Store) Notizen(lauf int64) ([]Notiz, error) {
	rows, err := s.db.Query(
		`SELECT geschrieben, text FROM notizen WHERE lauf = ? ORDER BY id`, lauf)
	if err != nil {
		return nil, fmt.Errorf("store: Notizen zu Lauf %d lesen: %w", lauf, err)
	}
	defer rows.Close()

	var out []Notiz
	for rows.Next() {
		var n Notiz
		if err := rows.Scan(&n.Geschrieben, &n.Text); err != nil {
			return nil, fmt.Errorf("store: Notiz scannen: %w", err)
		}
		out = append(out, n)
	}
	return out, rows.Err()
}
