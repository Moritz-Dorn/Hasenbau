// sperre.go: Die Overlap-Sperre pro Auftrag (§6, Ablauf 1). Sie gilt
// trigger-übergreifend — Scheduler, Watcher und manuelle Läufe teilen
// sich dieselbe Instanz. Wer sie nicht bekommt, überspringt den Lauf
// (kein Queueing: der nächste Trigger kommt von selbst).
package lauf

import "sync"

// Sperre verhindert, dass ein Auftrag doppelt läuft.
type Sperre struct {
	mu    sync.Mutex
	aktiv map[string]bool
}

func NeueSperre() *Sperre {
	return &Sperre{aktiv: map[string]bool{}}
}

// Belege reserviert den Auftrag für einen Lauf. false = läuft bereits.
func (s *Sperre) Belege(auftrag string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.aktiv[auftrag] {
		return false
	}
	s.aktiv[auftrag] = true
	return true
}

// GibFrei beendet die Reservierung.
func (s *Sperre) GibFrei(auftrag string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.aktiv, auftrag)
}
