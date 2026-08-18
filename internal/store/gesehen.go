// gesehen.go: Idempotenz-Backstop (§7). Der eigentliche Mechanismus ist
// der Move nach archiv/ — diese Tabelle fängt fehlgeschlagene Moves ab.
package store

import (
	"fmt"
	"time"
)

// IsSeen prüft, ob dieser Input (sha256) für den Auftrag schon
// erfolgreich verarbeitet wurde.
func (s *Store) IsSeen(auftrag, hash string) (bool, error) {
	var n int
	err := s.db.QueryRow(
		`SELECT count(*) FROM seen WHERE auftrag = ? AND source_hash = ?`,
		auftrag, hash,
	).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("store: checking already-seen: %w", err)
	}
	return n > 0, nil
}

// MarkSeen registriert einen erfolgreich verarbeiteten Input.
// Idempotent: derselbe Hash darf mehrfach gemeldet werden.
func (s *Store) MarkSeen(auftrag, hash string) error {
	_, err := s.db.Exec(
		`INSERT INTO seen (auftrag, source_hash, seen_at) VALUES (?, ?, ?)
		 ON CONFLICT (auftrag, source_hash) DO NOTHING`,
		auftrag, hash, time.Now().UTC(),
	)
	if err != nil {
		return fmt.Errorf("store: gesehen merken: %w", err)
	}
	return nil
}
