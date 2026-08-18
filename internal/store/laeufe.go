package store

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/Moritz-Dorn/Hasenbau/internal/process"
)

// Lauf ist eine Ausführung eines Auftrags (PLAN.md §1, §5).
type Lauf struct {
	ID        int64
	Auftrag   string
	Trigger   string // 'cron' | 'watch' | 'manual'
	Input     string
	Started   time.Time
	Ended     *time.Time
	Status    string // 'running' | 'ok' | 'failed' | 'aborted'
	SessionID string
	Summary   string
	Error     string
	TokensIn  int64
	TokensOut int64
	CostCent  int64

	// ToolSignature ist die geordnete Tool-Folge des Laufs, z.B.
	// 'read>write>hasenbau_summary'. Leer heißt zweierlei: der Lauf hat
	// nichts angefasst, oder er wurde nie ausgewertet — zu unterscheiden
	// über tool_calls (Hasenbau-4cx.1).
	ToolSignature string
}

// StartLauf legt die laeufe-Zeile an (status 'running') und stempelt
// auftrag_state.last_lauf. Die ID identifiziert den Lauf bis zum
// EndLauf. Mit in die Zeile geht der Wirt — der Prozess, der den Lauf
// hält: nur an ihm ist später zu erkennen, ob die Zeile noch lebt
// (verwaist.go).
func (s *Store) StartLauf(auftrag, trigger, input string) (int64, error) {
	switch trigger {
	case "cron", "watch", "manual":
	default:
		return 0, fmt.Errorf("store: unbekannter Trigger %q", trigger)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("store: Lauf beginnen: %w", err)
	}
	defer tx.Rollback()

	now := time.Now().UTC()
	pid, pidStarted := process.Self()
	res, err := tx.Exec(
		`INSERT INTO laeufe (auftrag, "trigger", input, started, status, pid, pid_started)
		 VALUES (?, ?, ?, ?, 'running', ?, ?)`,
		auftrag, trigger, input, now, pid, nullTime(pidStarted))
	if err != nil {
		return 0, fmt.Errorf("store: Lauf beginnen: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("store: Lauf beginnen: %w", err)
	}
	if _, err := tx.Exec(
		`INSERT INTO auftrag_state (auftrag, last_lauf) VALUES (?, ?)
		 ON CONFLICT (auftrag) DO UPDATE SET last_lauf = excluded.last_lauf`,
		auftrag, now); err != nil {
		return 0, fmt.Errorf("store: Auftrag-Zustand stempeln: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("store: Lauf beginnen: %w", err)
	}
	return id, nil
}

// LaufResult ist der Endzustand eines Laufs für EndLauf.
type LaufResult struct {
	Status    string // 'ok' | 'failed' | 'aborted'
	SessionID string
	Summary   string
	Error     string
	TokensIn  int64
	TokensOut int64
	CostCent  int64

	// ToolSignature ist die geordnete Tool-Folge des Laufs, z.B.
	// 'read>write>hasenbau_summary'. Leer heißt zweierlei: der Lauf hat
	// nichts angefasst, oder er wurde nie ausgewertet — zu unterscheiden
	// über tool_calls (Hasenbau-4cx.1).
	ToolSignature string
}

// EndLauf schreibt den Endzustand und pflegt auftrag_state:
// ok setzt last_ok und die Fehlerserie zurück, alles andere zählt
// sie hoch.
//
// e.Summary ist nur der Fallback (letzte Assistant-Message): hat der
// Hase seine Summary schon über den Rückkanal geschrieben, bleibt sie
// stehen (§5, §8 Phase 2).
func (s *Store) EndLauf(id int64, e LaufResult) error {
	switch e.Status {
	case "ok", "failed", "aborted":
	default:
		return fmt.Errorf("store: unbekannter Endstatus %q", e.Status)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("store: Lauf beenden: %w", err)
	}
	defer tx.Rollback()

	var auftrag string
	if err := tx.QueryRow(`SELECT auftrag FROM laeufe WHERE id = ?`, id).Scan(&auftrag); err != nil {
		return fmt.Errorf("store: Lauf %d beenden: %w", id, err)
	}
	now := time.Now().UTC()
	if _, err := tx.Exec(
		`UPDATE laeufe SET ended = ?, status = ?, session_id = ?,
		        summary = CASE WHEN summary IS NULL OR summary = '' THEN ? ELSE summary END,
		        error = ?, tokens_in = ?, tokens_out = ?, cost_cent = ?
		 WHERE id = ?`,
		now, e.Status, e.SessionID, SummaryLine(e.Summary), e.Error,
		e.TokensIn, e.TokensOut, e.CostCent, id); err != nil {
		return fmt.Errorf("store: Lauf %d beenden: %w", id, err)
	}
	if e.Status == "ok" {
		_, err = tx.Exec(
			`UPDATE auftrag_state SET last_ok = ?, error_streak = 0 WHERE auftrag = ?`,
			now, auftrag)
	} else {
		_, err = tx.Exec(
			`UPDATE auftrag_state SET error_streak = error_streak + 1 WHERE auftrag = ?`,
			auftrag)
	}
	if err != nil {
		return fmt.Errorf("store: Auftrag-Zustand pflegen: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: Lauf %d beenden: %w", id, err)
	}
	return nil
}

// LaufByID liefert einen einzelnen Lauf — z.B. für graben, das über
// die session_id an den Trace kommt (§8 Phase 2).
func (s *Store) LaufByID(id int64) (*Lauf, error) {
	var l Lauf
	var ended sql.NullTime
	err := s.db.QueryRow(`
		SELECT id, auftrag, "trigger", COALESCE(input,''), started,
		       ended, status, COALESCE(session_id,''), COALESCE(summary,''),
		       COALESCE(error,''), COALESCE(tokens_in,0), COALESCE(tokens_out,0),
		       COALESCE(cost_cent,0), COALESCE(tool_signature,'')
		FROM laeufe WHERE id = ?`, id).Scan(
		&l.ID, &l.Auftrag, &l.Trigger, &l.Input, &l.Started, &ended,
		&l.Status, &l.SessionID, &l.Summary, &l.Error, &l.TokensIn,
		&l.TokensOut, &l.CostCent, &l.ToolSignature)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("store: no Lauf with ID %d", id)
	}
	if err != nil {
		return nil, fmt.Errorf("store: Lauf %d lesen: %w", id, err)
	}
	if ended.Valid {
		t := ended.Time
		l.Ended = &t
	}
	return &l, nil
}

// LastLaufByAuftrag liefert den jüngsten Lauf eines Auftrags, der
// eine Session hat — nur an so einem hängt ein Trace, und nur damit
// kann der Baumeister etwas anfangen (§8 Phase 2).
func (s *Store) LastLaufByAuftrag(auftrag string) (*Lauf, error) {
	var id int64
	err := s.db.QueryRow(`
		SELECT id FROM laeufe
		WHERE auftrag = ? AND session_id IS NOT NULL AND session_id != ''
		ORDER BY started DESC, id DESC LIMIT 1`, auftrag).Scan(&id)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("store: no Lauf of Auftrag %q with a session", auftrag)
	}
	if err != nil {
		return nil, fmt.Errorf("store: letzten Lauf von %q lesen: %w", auftrag, err)
	}
	return s.LaufByID(id)
}

// RecentLaeufe liefert die jüngsten Läufe, neueste zuerst.
func (s *Store) RecentLaeufe(limit int) ([]Lauf, error) {
	rows, err := s.db.Query(`
		SELECT id, auftrag, "trigger", COALESCE(input,''), started,
		       ended, status, COALESCE(session_id,''), COALESCE(summary,''),
		       COALESCE(error,''), COALESCE(tokens_in,0), COALESCE(tokens_out,0),
		       COALESCE(cost_cent,0)
		FROM laeufe ORDER BY started DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("store: reading Läufe: %w", err)
	}
	defer rows.Close()
	return scanLaeufe(rows)
}

// scanLaeufe liest die Spaltenfolge, die sich die Lauf-Abfragen teilen.
func scanLaeufe(rows *sql.Rows) ([]Lauf, error) {
	var out []Lauf
	for rows.Next() {
		var l Lauf
		var ended sql.NullTime
		if err := rows.Scan(&l.ID, &l.Auftrag, &l.Trigger, &l.Input,
			&l.Started, &ended, &l.Status, &l.SessionID, &l.Summary,
			&l.Error, &l.TokensIn, &l.TokensOut, &l.CostCent); err != nil {
			return nil, fmt.Errorf("store: Lauf scannen: %w", err)
		}
		if ended.Valid {
			t := ended.Time
			l.Ended = &t
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// RecentLaeufeByAuftrag liefert die jüngsten Läufe eines Auftrags,
// neueste zuerst — anders als LastLaufByAuftrag auch die ohne
// Session, denn gerade die gescheiterten sind interessant.
func (s *Store) RecentLaeufeByAuftrag(auftrag string, n int) ([]Lauf, error) {
	rows, err := s.db.Query(`
		SELECT id, auftrag, "trigger", COALESCE(input,''), started,
		       ended, status, COALESCE(session_id,''), COALESCE(summary,''),
		       COALESCE(error,''), COALESCE(tokens_in,0), COALESCE(tokens_out,0),
		       COALESCE(cost_cent,0)
		FROM laeufe WHERE auftrag = ?
		ORDER BY started DESC, id DESC LIMIT ?`, auftrag, n)
	if err != nil {
		return nil, fmt.Errorf("store: reading Läufe of %q: %w", auftrag, err)
	}
	defer rows.Close()
	return scanLaeufe(rows)
}

// LaeufeSince liefert die Startzeitpunkte aller Läufe eines Auftrags
// seit `since`, älteste zuerst — die Grundlage des Deckels aus §6
// (Hasenbau-do0.2).
//
// Alle, unabhängig vom Status: ein Lauf, der scheitert, hat das Modell
// trotzdem gekostet. Ein Deckel, der nur `ok` zählt, wäre bei einem
// kaputten Auftrag gar keiner — der bekäme endlos frisches Budget.
func (s *Store) LaeufeSince(auftrag string, since time.Time) ([]time.Time, error) {
	rows, err := s.db.Query(`
		SELECT started FROM laeufe
		WHERE auftrag = ? AND started > ?
		ORDER BY started ASC`, auftrag, since.UTC())
	if err != nil {
		return nil, fmt.Errorf("store: reading Läufe of %q since %s: %w", auftrag, since, err)
	}
	defer rows.Close()

	var out []time.Time
	for rows.Next() {
		var t time.Time
		if err := rows.Scan(&t); err != nil {
			return nil, fmt.Errorf("store: Startzeit scannen: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// LaeufeSinceAll ist LaeufeSince ohne Auftrags-Filter: die Startzeiten
// ALLER Läufe des Baus seit `since`, älteste zuerst. Grundlage des
// Bau-weiten Deckels (Hasenbau-cvf) — der schützt nicht einen Auftrag
// vor sich selbst, sondern das Budget vor allen zusammen.
func (s *Store) LaeufeSinceAll(since time.Time) ([]time.Time, error) {
	rows, err := s.db.Query(`
		SELECT started FROM laeufe
		WHERE started > ?
		ORDER BY started ASC`, since.UTC())
	if err != nil {
		return nil, fmt.Errorf("store: reading Läufe since %s: %w", since, err)
	}
	defer rows.Close()

	var out []time.Time
	for rows.Next() {
		var t time.Time
		if err := rows.Scan(&t); err != nil {
			return nil, fmt.Errorf("store: Startzeit scannen: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// RecentSummaries liefert die jüngsten nicht-leeren Summaries eines
// Auftrags in chronologischer Reihenfolge (älteste zuerst) — so wandern
// sie direkt in den Prompt (§6, Kontext-Schicht).
func (s *Store) RecentSummaries(auftrag string, n int) ([]string, error) {
	if n <= 0 {
		return nil, nil
	}
	rows, err := s.db.Query(`
		SELECT summary FROM laeufe
		WHERE auftrag = ? AND summary IS NOT NULL AND summary != ''
		ORDER BY started DESC, id DESC LIMIT ?`, auftrag, n)
	if err != nil {
		return nil, fmt.Errorf("store: Summaries lesen: %w", err)
	}
	defer rows.Close()

	var neueste []string
	for rows.Next() {
		var summary string
		if err := rows.Scan(&summary); err != nil {
			return nil, fmt.Errorf("store: Summary scannen: %w", err)
		}
		neueste = append(neueste, summary)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i, j := 0, len(neueste)-1; i < j; i, j = i+1, j-1 {
		neueste[i], neueste[j] = neueste[j], neueste[i]
	}
	return neueste, nil
}

// StatusCounts liefert die Anzahl Läufe je Status.
func (s *Store) StatusCounts() (map[string]int, error) {
	rows, err := s.db.Query(`SELECT status, count(*) FROM laeufe GROUP BY status`)
	if err != nil {
		return nil, fmt.Errorf("store: counting status: %w", err)
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
	LastLauf    *time.Time
	LastOk      *time.Time
	ErrorStreak int
}

// AuftragStates liefert den Zustand aller bekannten Aufträge.
func (s *Store) AuftragStates() ([]AuftragState, error) {
	rows, err := s.db.Query(`
		SELECT auftrag, last_lauf, last_ok, error_streak
		FROM auftrag_state ORDER BY auftrag`)
	if err != nil {
		return nil, fmt.Errorf("store: reading Auftrag states: %w", err)
	}
	defer rows.Close()

	var out []AuftragState
	for rows.Next() {
		var a AuftragState
		var lauf, ok sql.NullTime
		if err := rows.Scan(&a.Auftrag, &lauf, &ok, &a.ErrorStreak); err != nil {
			return nil, err
		}
		if lauf.Valid {
			t := lauf.Time
			a.LastLauf = &t
		}
		if ok.Valid {
			t := ok.Time
			a.LastOk = &t
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
