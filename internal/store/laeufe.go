package store

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/Moritz-Dorn/Hasenbau/internal/prozess"
)

// Lauf ist eine Ausführung eines Auftrags (PLAN.md §1, §5).
type Lauf struct {
	ID         int64
	Auftrag    string
	Trigger    string // 'cron' | 'watch' | 'manuell'
	Ausloeser  string
	Gestartet  time.Time
	Beendet    *time.Time
	Status     string // 'laeuft' | 'ok' | 'fehler' | 'abgebrochen'
	SessionID  string
	Summary    string
	Fehler     string
	TokensIn   int64
	TokensOut  int64
	KostenCent int64
}

// LaufBeginne legt die laeufe-Zeile an (status 'laeuft') und stempelt
// auftrag_state.letzter_lauf. Die ID identifiziert den Lauf bis zum
// LaufBeende. Mit in die Zeile geht der Wirt — der Prozess, der den Lauf
// hält: nur an ihm ist später zu erkennen, ob die Zeile noch lebt
// (verwaist.go).
func (s *Store) LaufBeginne(auftrag, trigger, ausloeser string) (int64, error) {
	switch trigger {
	case "cron", "watch", "manuell":
	default:
		return 0, fmt.Errorf("store: unbekannter Trigger %q", trigger)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("store: Lauf beginnen: %w", err)
	}
	defer tx.Rollback()

	now := time.Now().UTC()
	pid, pidGestartet := prozess.Ich()
	res, err := tx.Exec(
		`INSERT INTO laeufe (auftrag, "trigger", ausloeser, gestartet, status, pid, pid_gestartet)
		 VALUES (?, ?, ?, ?, 'laeuft', ?, ?)`,
		auftrag, trigger, ausloeser, now, pid, nullZeit(pidGestartet))
	if err != nil {
		return 0, fmt.Errorf("store: Lauf beginnen: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("store: Lauf beginnen: %w", err)
	}
	if _, err := tx.Exec(
		`INSERT INTO auftrag_state (auftrag, letzter_lauf) VALUES (?, ?)
		 ON CONFLICT (auftrag) DO UPDATE SET letzter_lauf = excluded.letzter_lauf`,
		auftrag, now); err != nil {
		return 0, fmt.Errorf("store: Auftrag-Zustand stempeln: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("store: Lauf beginnen: %w", err)
	}
	return id, nil
}

// LaufErgebnis ist der Endzustand eines Laufs für LaufBeende.
type LaufErgebnis struct {
	Status     string // 'ok' | 'fehler' | 'abgebrochen'
	SessionID  string
	Summary    string
	Fehler     string
	TokensIn   int64
	TokensOut  int64
	KostenCent int64
}

// LaufBeende schreibt den Endzustand und pflegt auftrag_state:
// ok setzt letzter_ok und die Fehlerserie zurück, alles andere zählt
// sie hoch.
//
// e.Summary ist nur der Fallback (letzte Assistant-Message): hat der
// Hase seine Summary schon über den Rückkanal geschrieben, bleibt sie
// stehen (§5, §8 Phase 2).
func (s *Store) LaufBeende(id int64, e LaufErgebnis) error {
	switch e.Status {
	case "ok", "fehler", "abgebrochen":
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
		`UPDATE laeufe SET beendet = ?, status = ?, session_id = ?,
		        summary = CASE WHEN summary IS NULL OR summary = '' THEN ? ELSE summary END,
		        fehler = ?, tokens_in = ?, tokens_out = ?, kosten_cent = ?
		 WHERE id = ?`,
		now, e.Status, e.SessionID, SummaryZeile(e.Summary), e.Fehler,
		e.TokensIn, e.TokensOut, e.KostenCent, id); err != nil {
		return fmt.Errorf("store: Lauf %d beenden: %w", id, err)
	}
	if e.Status == "ok" {
		_, err = tx.Exec(
			`UPDATE auftrag_state SET letzter_ok = ?, fehler_serie = 0 WHERE auftrag = ?`,
			now, auftrag)
	} else {
		_, err = tx.Exec(
			`UPDATE auftrag_state SET fehler_serie = fehler_serie + 1 WHERE auftrag = ?`,
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

// LaufNachID liefert einen einzelnen Lauf — z.B. für graben, das über
// die session_id an den Trace kommt (§8 Phase 2).
func (s *Store) LaufNachID(id int64) (*Lauf, error) {
	var l Lauf
	var beendet sql.NullTime
	err := s.db.QueryRow(`
		SELECT id, auftrag, "trigger", COALESCE(ausloeser,''), gestartet,
		       beendet, status, COALESCE(session_id,''), COALESCE(summary,''),
		       COALESCE(fehler,''), COALESCE(tokens_in,0), COALESCE(tokens_out,0),
		       COALESCE(kosten_cent,0)
		FROM laeufe WHERE id = ?`, id).Scan(
		&l.ID, &l.Auftrag, &l.Trigger, &l.Ausloeser, &l.Gestartet, &beendet,
		&l.Status, &l.SessionID, &l.Summary, &l.Fehler, &l.TokensIn,
		&l.TokensOut, &l.KostenCent)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("store: kein Lauf mit ID %d", id)
	}
	if err != nil {
		return nil, fmt.Errorf("store: Lauf %d lesen: %w", id, err)
	}
	if beendet.Valid {
		t := beendet.Time
		l.Beendet = &t
	}
	return &l, nil
}

// LetzterLaufNachAuftrag liefert den jüngsten Lauf eines Auftrags, der
// eine Session hat — nur an so einem hängt ein Trace, und nur damit
// kann der Baumeister etwas anfangen (§8 Phase 2).
func (s *Store) LetzterLaufNachAuftrag(auftrag string) (*Lauf, error) {
	var id int64
	err := s.db.QueryRow(`
		SELECT id FROM laeufe
		WHERE auftrag = ? AND session_id IS NOT NULL AND session_id != ''
		ORDER BY gestartet DESC, id DESC LIMIT 1`, auftrag).Scan(&id)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("store: kein Lauf des Auftrags %q mit Session", auftrag)
	}
	if err != nil {
		return nil, fmt.Errorf("store: letzten Lauf von %q lesen: %w", auftrag, err)
	}
	return s.LaufNachID(id)
}

// LetzteLaeufe liefert die jüngsten Läufe, neueste zuerst.
func (s *Store) LetzteLaeufe(limit int) ([]Lauf, error) {
	rows, err := s.db.Query(`
		SELECT id, auftrag, "trigger", COALESCE(ausloeser,''), gestartet,
		       beendet, status, COALESCE(session_id,''), COALESCE(summary,''),
		       COALESCE(fehler,''), COALESCE(tokens_in,0), COALESCE(tokens_out,0),
		       COALESCE(kosten_cent,0)
		FROM laeufe ORDER BY gestartet DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("store: Läufe lesen: %w", err)
	}
	defer rows.Close()
	return scanLaeufe(rows)
}

// scanLaeufe liest die Spaltenfolge, die sich die Lauf-Abfragen teilen.
func scanLaeufe(rows *sql.Rows) ([]Lauf, error) {
	var out []Lauf
	for rows.Next() {
		var l Lauf
		var beendet sql.NullTime
		if err := rows.Scan(&l.ID, &l.Auftrag, &l.Trigger, &l.Ausloeser,
			&l.Gestartet, &beendet, &l.Status, &l.SessionID, &l.Summary,
			&l.Fehler, &l.TokensIn, &l.TokensOut, &l.KostenCent); err != nil {
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

// LetzteLaeufeNachAuftrag liefert die jüngsten Läufe eines Auftrags,
// neueste zuerst — anders als LetzterLaufNachAuftrag auch die ohne
// Session, denn gerade die gescheiterten sind interessant.
func (s *Store) LetzteLaeufeNachAuftrag(auftrag string, n int) ([]Lauf, error) {
	rows, err := s.db.Query(`
		SELECT id, auftrag, "trigger", COALESCE(ausloeser,''), gestartet,
		       beendet, status, COALESCE(session_id,''), COALESCE(summary,''),
		       COALESCE(fehler,''), COALESCE(tokens_in,0), COALESCE(tokens_out,0),
		       COALESCE(kosten_cent,0)
		FROM laeufe WHERE auftrag = ?
		ORDER BY gestartet DESC, id DESC LIMIT ?`, auftrag, n)
	if err != nil {
		return nil, fmt.Errorf("store: Läufe von %q lesen: %w", auftrag, err)
	}
	defer rows.Close()
	return scanLaeufe(rows)
}

// LetzteSummaries liefert die jüngsten nicht-leeren Summaries eines
// Auftrags in chronologischer Reihenfolge (älteste zuerst) — so wandern
// sie direkt in den Prompt (§6, Kontext-Schicht).
func (s *Store) LetzteSummaries(auftrag string, n int) ([]string, error) {
	if n <= 0 {
		return nil, nil
	}
	rows, err := s.db.Query(`
		SELECT summary FROM laeufe
		WHERE auftrag = ? AND summary IS NOT NULL AND summary != ''
		ORDER BY gestartet DESC, id DESC LIMIT ?`, auftrag, n)
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
