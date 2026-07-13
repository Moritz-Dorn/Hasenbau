// gesehen.go: Idempotenz-Backstop (§7). Der eigentliche Mechanismus ist
// der Move nach archiv/ — diese Tabelle fängt fehlgeschlagene Moves ab.
package store

import (
	"fmt"
	"time"
)

// IstGesehen prüft, ob dieser Input (sha256) für den Auftrag schon
// erfolgreich verarbeitet wurde.
func (s *Store) IstGesehen(auftrag, hash string) (bool, error) {
	var n int
	err := s.db.QueryRow(
		`SELECT count(*) FROM gesehen WHERE auftrag = ? AND quelle_hash = ?`,
		auftrag, hash,
	).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("store: gesehen prüfen: %w", err)
	}
	return n > 0, nil
}

// MerkeGesehen registriert einen erfolgreich verarbeiteten Input.
// Idempotent: derselbe Hash darf mehrfach gemeldet werden.
func (s *Store) MerkeGesehen(auftrag, hash string) error {
	_, err := s.db.Exec(
		`INSERT INTO gesehen (auftrag, quelle_hash, gesehen_am) VALUES (?, ?, ?)
		 ON CONFLICT (auftrag, quelle_hash) DO NOTHING`,
		auftrag, hash, time.Now().UTC(),
	)
	if err != nil {
		return fmt.Errorf("store: gesehen merken: %w", err)
	}
	return nil
}
