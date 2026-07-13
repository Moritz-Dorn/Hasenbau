// Package scheduler feuert cron-Trigger (PLAN.md §2, §6). Er plant nur —
// ausgeführt wird über den übergebenen Callback, und die Overlap-Sperre
// ist die trigger-übergreifende lauf.Sperre.
package scheduler

import (
	"fmt"

	"github.com/Moritz-Dorn/Hasenbau/internal/auftrag"
	"github.com/Moritz-Dorn/Hasenbau/internal/lauf"
	"github.com/robfig/cron/v3"
)

// AusfuehrFunc führt einen Lauf aus. Sie läuft in der cron-Goroutine;
// die Sperre ist beim Aufruf bereits belegt und wird danach freigegeben.
type AusfuehrFunc func(a *auftrag.Auftrag)

type Scheduler struct {
	cron *cron.Cron
	logf func(format string, args ...any)
}

// New registriert alle Aufträge mit cron-Trigger. Aufträge mit
// watch-Trigger gehören zum Watcher und werden ignoriert.
func New(auftraege []*auftrag.Auftrag, sperre *lauf.Sperre, ausfuehren AusfuehrFunc, logf func(format string, args ...any)) (*Scheduler, error) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	s := &Scheduler{cron: cron.New(), logf: logf}

	for _, a := range auftraege {
		if a.Trigger.Cron == "" {
			continue
		}
		a := a
		_, err := s.cron.AddFunc(a.Trigger.Cron, func() {
			if !sperre.Belege(a.Name) {
				s.logf("scheduler: auftrag %s läuft noch — Tick übersprungen", a.Name)
				return
			}
			defer sperre.GibFrei(a.Name)
			ausfuehren(a)
		})
		if err != nil {
			// Parse validiert den Ausdruck bereits — das hier wäre eine
			// Diskrepanz zwischen Parser und Scheduler.
			return nil, fmt.Errorf("scheduler: auftrag %s: cron %q: %w", a.Name, a.Trigger.Cron, err)
		}
	}
	return s, nil
}

// Start beginnt zu planen (eigene Goroutine von robfig/cron).
func (s *Scheduler) Start() { s.cron.Start() }

// Stop hält die Planung an und blockiert, bis laufende Jobs fertig sind.
func (s *Scheduler) Stop() {
	<-s.cron.Stop().Done()
}
