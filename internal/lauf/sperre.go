// lock.go: Die Overlap-Lock pro Auftrag (§6, Ablauf 1). Sie gilt
// trigger-übergreifend — Scheduler, Watcher und manuelle Läufe teilen
// sich dieselbe Instanz. Wer sie nicht bekommt, überspringt den Lauf
// (kein Queueing: der nächste Trigger kommt von selbst).
package lauf

import "sync"

// Lock verhindert, dass ein Auftrag doppelt läuft.
type Lock struct {
	mu     sync.Mutex
	active map[string]bool
}

func NewLock() *Lock {
	return &Lock{active: map[string]bool{}}
}

// Acquire reserviert den Auftrag für einen Lauf. false = läuft bereits.
func (s *Lock) Acquire(auftrag string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active[auftrag] {
		return false
	}
	s.active[auftrag] = true
	return true
}

// Release beendet die Reservierung.
func (s *Lock) Release(auftrag string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.active, auftrag)
}
