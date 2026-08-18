// verwaist.go: die Leichen eines hart abgebrochenen Prozesses —
// laeufe-Zeilen, die für immer auf status='running' stehen bleiben.
// Folgen: `hasenbau status` zählt falsch, und der Rückkanal findet
// keinen eindeutigen aktiven Lauf mehr, schreibt also gar nicht
// (PLAN.md §11.7).
//
// „Beim Start alles auf 'running' als abgebrochen markieren" wäre zu
// grob: ein paralleles `hasenbau lauf` ist ausdrücklich erlaubt
// (eigener Server, geteilte DB im WAL-Modus) und würde mit abgeräumt.
// Das Kriterium ist deshalb der Wirt — der Prozess, der den Lauf hält.
// Jede Zeile trägt seine PID und dessen Startzeit (die PID allein wird
// recycelt), und abgeräumt wird nur, wo diese Inkarnation nachweislich
// tot ist.
package store

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/Moritz-Dorn/Hasenbau/internal/process"
)

// nullTime macht aus einer Nullzeit ein SQL NULL — die Startzeit des
// Wirts kennt nicht jede Plattform.
func nullTime(t time.Time) sql.NullTime {
	return sql.NullTime{Time: t, Valid: !t.IsZero()}
}

// orphaned entscheidet über eine 'running'-Zeile: gehört sie einem
// lebenden Prozess oder ist sie eine Leiche?
func orphaned(pid sql.NullInt64, pidStarted sql.NullTime) bool {
	if !pid.Valid {
		// Zeile aus der Zeit vor der Wirt-Spalte: nicht zuzuordnen,
		// also aufräumen. Seit der Migration trägt jeder lebende Lauf
		// seine PID.
		return true
	}
	return !process.Alive(int(pid.Int64), pidStarted.Time)
}

// CleanupLaeufe schließt die Läufe ab, deren Wirt gestorben ist:
// status 'aborted', der Grund in error. Zurück kommen die
// aufgeräumten Läufe — beim Daemon-Start gehört jeder in eine Log-Zeile,
// sonst verschwindet ein abgestürzter Lauf lautlos.
//
// beendet wird auf jetzt gesetzt, nicht auf den (unbekannten)
// Todeszeitpunkt: die Dauer einer solchen Zeile ist die Zeit bis zum
// Aufräumen, nicht die Laufzeit. Wer es genauer braucht, liest error.
//
// Aufzurufen beim Start jedes Prozesses, der selbst Läufe anlegt
// (daemon, lauf) — nie mittendrin: ein gerade beginnender Lauf einer
// anderen Inkarnation soll seine Zeile behalten.
func (s *Store) CleanupLaeufe() ([]Lauf, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("store: cleaning up orphaned Läufe: %w", err)
	}
	defer tx.Rollback()

	rows, err := tx.Query(`
		SELECT id, auftrag, "trigger", COALESCE(input,''), started,
		       pid, pid_started
		FROM laeufe WHERE status = 'running' ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("store: looking for orphaned Läufe: %w", err)
	}
	var leichen []Lauf
	for rows.Next() {
		var l Lauf
		var pid sql.NullInt64
		var pidStarted sql.NullTime
		if err := rows.Scan(&l.ID, &l.Auftrag, &l.Trigger, &l.Input,
			&l.Started, &pid, &pidStarted); err != nil {
			rows.Close()
			return nil, fmt.Errorf("store: verwaisten Lauf scannen: %w", err)
		}
		if !orphaned(pid, pidStarted) {
			continue
		}
		l.Status = "aborted"
		if pid.Valid {
			l.Error = fmt.Sprintf("process %d died without ending the Lauf — cleaned up at startup", pid.Int64)
		} else {
			l.Error = "Lauf without a host in the row (older binary) — cleaned up at startup"
		}
		leichen = append(leichen, l)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: looking for orphaned Läufe: %w", err)
	}

	now := time.Now().UTC()
	for i, l := range leichen {
		// status='running' in der WHERE-Klausel: hat der Lauf sich
		// zwischen Query und Update selbst beendet, gewinnt er.
		if _, err := tx.Exec(
			`UPDATE laeufe SET ended = ?, status = 'aborted', error = ?
			 WHERE id = ? AND status = 'running'`,
			now, l.Error, l.ID); err != nil {
			return nil, fmt.Errorf("store: cleaning up Lauf %d: %w", l.ID, err)
		}
		// Wie bei jedem nicht-ok-Ende (EndLauf): der Lauf ist
		// gescheitert, die Serie zählt hoch.
		if _, err := tx.Exec(
			`UPDATE auftrag_state SET error_streak = error_streak + 1 WHERE auftrag = ?`,
			l.Auftrag); err != nil {
			return nil, fmt.Errorf("store: Auftrag-Zustand pflegen: %w", err)
		}
		leichen[i].Ended = &now
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("store: cleaning up orphaned Läufe: %w", err)
	}
	return leichen, nil
}
