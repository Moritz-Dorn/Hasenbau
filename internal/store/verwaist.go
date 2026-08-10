// verwaist.go: die Leichen eines hart abgebrochenen Prozesses —
// laeufe-Zeilen, die für immer auf status='laeuft' stehen bleiben.
// Folgen: `hasenbau status` zählt falsch, und der Rückkanal findet
// keinen eindeutigen aktiven Lauf mehr, schreibt also gar nicht
// (PLAN.md §11.7).
//
// „Beim Start alles auf 'laeuft' als abgebrochen markieren" wäre zu
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

	"github.com/Moritz-Dorn/Hasenbau/internal/prozess"
)

// nullZeit macht aus einer Nullzeit ein SQL NULL — die Startzeit des
// Wirts kennt nicht jede Plattform.
func nullZeit(t time.Time) sql.NullTime {
	return sql.NullTime{Time: t, Valid: !t.IsZero()}
}

// verwaist entscheidet über eine 'laeuft'-Zeile: gehört sie einem
// lebenden Prozess oder ist sie eine Leiche?
func verwaist(pid sql.NullInt64, pidGestartet sql.NullTime) bool {
	if !pid.Valid {
		// Zeile aus der Zeit vor der Wirt-Spalte: nicht zuzuordnen,
		// also aufräumen. Seit der Migration trägt jeder lebende Lauf
		// seine PID.
		return true
	}
	return !prozess.Lebt(int(pid.Int64), pidGestartet.Time)
}

// LaeufeAufraeumen schließt die Läufe ab, deren Wirt gestorben ist:
// status 'abgebrochen', der Grund in fehler. Zurück kommen die
// aufgeräumten Läufe — beim Daemon-Start gehört jeder in eine Log-Zeile,
// sonst verschwindet ein abgestürzter Lauf lautlos.
//
// beendet wird auf jetzt gesetzt, nicht auf den (unbekannten)
// Todeszeitpunkt: die Dauer einer solchen Zeile ist die Zeit bis zum
// Aufräumen, nicht die Laufzeit. Wer es genauer braucht, liest fehler.
//
// Aufzurufen beim Start jedes Prozesses, der selbst Läufe anlegt
// (daemon, lauf) — nie mittendrin: ein gerade beginnender Lauf einer
// anderen Inkarnation soll seine Zeile behalten.
func (s *Store) LaeufeAufraeumen() ([]Lauf, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("store: verwaiste Läufe aufräumen: %w", err)
	}
	defer tx.Rollback()

	rows, err := tx.Query(`
		SELECT id, auftrag, "trigger", COALESCE(ausloeser,''), gestartet,
		       pid, pid_gestartet
		FROM laeufe WHERE status = 'laeuft' ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("store: verwaiste Läufe suchen: %w", err)
	}
	var leichen []Lauf
	for rows.Next() {
		var l Lauf
		var pid sql.NullInt64
		var pidGestartet sql.NullTime
		if err := rows.Scan(&l.ID, &l.Auftrag, &l.Trigger, &l.Ausloeser,
			&l.Gestartet, &pid, &pidGestartet); err != nil {
			rows.Close()
			return nil, fmt.Errorf("store: verwaisten Lauf scannen: %w", err)
		}
		if !verwaist(pid, pidGestartet) {
			continue
		}
		l.Status = "abgebrochen"
		if pid.Valid {
			l.Fehler = fmt.Sprintf("Prozess %d ist gestorben, ohne den Lauf zu beenden — beim Start aufgeräumt", pid.Int64)
		} else {
			l.Fehler = "Lauf ohne Wirt in der Zeile (älteres Binary) — beim Start aufgeräumt"
		}
		leichen = append(leichen, l)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: verwaiste Läufe suchen: %w", err)
	}

	now := time.Now().UTC()
	for i, l := range leichen {
		// status='laeuft' in der WHERE-Klausel: hat der Lauf sich
		// zwischen Query und Update selbst beendet, gewinnt er.
		if _, err := tx.Exec(
			`UPDATE laeufe SET beendet = ?, status = 'abgebrochen', fehler = ?
			 WHERE id = ? AND status = 'laeuft'`,
			now, l.Fehler, l.ID); err != nil {
			return nil, fmt.Errorf("store: Lauf %d aufräumen: %w", l.ID, err)
		}
		// Wie bei jedem nicht-ok-Ende (LaufBeende): der Lauf ist
		// gescheitert, die Serie zählt hoch.
		if _, err := tx.Exec(
			`UPDATE auftrag_state SET fehler_serie = fehler_serie + 1 WHERE auftrag = ?`,
			l.Auftrag); err != nil {
			return nil, fmt.Errorf("store: Auftrag-Zustand pflegen: %w", err)
		}
		leichen[i].Beendet = &now
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("store: verwaiste Läufe aufräumen: %w", err)
	}
	return leichen, nil
}
