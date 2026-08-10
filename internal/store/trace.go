// trace.go: der Verlauf eines Laufs, abgelegt beim Lauf-Ende
// (PLAN.md §5, §8 Phase 2). Der Runner holt die Session-Messages
// ohnehin schon für Summary, Tokens und Kosten — der Trace fällt dabei
// ab und kostet keinen zweiten Aufruf.
//
// Warum überhaupt in der Bau-DB, wo `graben` ihn doch beim Server holen
// kann: der Baumeister zieht seinen Trace in einem Gang. Der müsste
// sonst `hasenbau graben` rufen und damit einen zweiten opencode-Server
// starten — der bei einem Gang-Timeout verwaist zurückbliebe, weil der
// Kill nur die Prozessgruppe der Gang-Shell trifft (§2: „hängt nie als
// offener Endpoint herum"). Mit der Zeile hier braucht `graben` gar
// keinen Server mehr.
//
// Der Store nimmt rohes JSON und kennt das Trace-Format nicht — das
// gehört internal/opencode (Schichtgrenze §2).
package store

import (
	"database/sql"
	"fmt"
	"time"
)

// TraceSchreibe legt den Trace eines Laufs ab. Ein zweiter Aufruf
// ersetzt den ersten: der Lazy-Backfill in `graben` darf eine bereits
// vorhandene Zeile überschreiben, ohne vorher zu fragen.
func (s *Store) TraceSchreibe(lauf int64, sessionID string, roh []byte) error {
	if sessionID == "" {
		return fmt.Errorf("store: Trace zu Lauf %d ohne Session-ID", lauf)
	}
	if _, err := s.db.Exec(`
		INSERT INTO trace (lauf, session_id, json, geschrieben) VALUES (?, ?, ?, ?)
		ON CONFLICT (lauf) DO UPDATE SET
			session_id = excluded.session_id,
			json = excluded.json,
			geschrieben = excluded.geschrieben`,
		lauf, sessionID, string(roh), time.Now().UTC()); err != nil {
		return fmt.Errorf("store: Trace zu Lauf %d: %w", lauf, err)
	}
	return nil
}

// TraceLies liefert den abgelegten Trace. da=false heißt: für diesen
// Lauf wurde keiner aufgezeichnet (Altlauf oder gescheitert vor dem
// Hasen) — dann bleibt der Weg über den Server.
func (s *Store) TraceLies(lauf int64) (roh []byte, da bool, err error) {
	var j string
	switch err := s.db.QueryRow(`SELECT json FROM trace WHERE lauf = ?`, lauf).Scan(&j); {
	case err == sql.ErrNoRows:
		return nil, false, nil
	case err != nil:
		return nil, false, fmt.Errorf("store: Trace zu Lauf %d lesen: %w", lauf, err)
	}
	return []byte(j), true, nil
}
