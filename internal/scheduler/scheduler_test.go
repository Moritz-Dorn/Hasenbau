package scheduler

import (
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Moritz-Dorn/Hasenbau/internal/auftrag"
	"github.com/Moritz-Dorn/Hasenbau/internal/lauf"
)

func cronAuftrag(name, ausdruck string) *auftrag.Auftrag {
	return &auftrag.Auftrag{Name: name, Hase: "melder", Trigger: auftrag.Trigger{Cron: ausdruck}}
}

func TestCronFeuert(t *testing.T) {
	var zaehler atomic.Int32
	s, err := New(
		[]*auftrag.Auftrag{cronAuftrag("tick", "@every 1s")},
		lauf.NewLock(),
		func(a *auftrag.Auftrag) { zaehler.Add(1) },
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	s.Start()
	defer s.Stop()

	frist := time.Now().Add(5 * time.Second)
	for zaehler.Load() == 0 && time.Now().Before(frist) {
		time.Sleep(50 * time.Millisecond)
	}
	if zaehler.Load() == 0 {
		t.Fatal("cron-Trigger feuerte nicht")
	}
}

func TestSperreVerhindertOverlapUndLoggt(t *testing.T) {
	var (
		zaehler atomic.Int32
		mu      sync.Mutex
		logs    []string
	)
	blockiere := make(chan struct{})
	lock := lauf.NewLock()

	s, err := New(
		[]*auftrag.Auftrag{cronAuftrag("langsam", "@every 1s")},
		lock,
		func(a *auftrag.Auftrag) {
			zaehler.Add(1)
			<-blockiere // hält die Sperre über mehrere Ticks
		},
		func(format string, args ...any) {
			mu.Lock()
			logs = append(logs, format)
			mu.Unlock()
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	s.Start()

	// Warten, bis mindestens ein weiterer Tick übersprungen wurde.
	frist := time.Now().Add(6 * time.Second)
	for time.Now().Before(frist) {
		mu.Lock()
		n := len(logs)
		mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if got := zaehler.Load(); got != 1 {
		t.Errorf("Auftrag lief %d-mal parallel", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(logs) == 0 || !strings.Contains(logs[0], "übersprungen") {
		t.Errorf("übersprungener Tick nicht geloggt: %v", logs)
	}
	close(blockiere)
	s.Stop()
}

func TestSperreIstTriggerUebergreifend(t *testing.T) {
	lock := lauf.NewLock()
	if !lock.Acquire("pdf-einlagern") {
		t.Fatal("frische Sperre muss belegbar sein")
	}
	// Ein Watcher- oder manueller Lauf desselben Auftrags muss abprallen.
	if lock.Acquire("pdf-einlagern") {
		t.Error("Doppel-Belegung möglich")
	}
	if !lock.Acquire("anderer-auftrag") {
		t.Error("fremder Auftrag fälschlich blockiert")
	}
	lock.Release("pdf-einlagern")
	if !lock.Acquire("pdf-einlagern") {
		t.Error("nach GibFrei nicht wieder belegbar")
	}
}

func TestWatchAuftraegeWerdenIgnoriert(t *testing.T) {
	s, err := New(
		[]*auftrag.Auftrag{{Name: "w", Hase: "h", Trigger: auftrag.Trigger{Watch: "raeume/x/*.pdf"}}},
		lauf.NewLock(),
		func(a *auftrag.Auftrag) { t.Error("watch-Auftrag im Scheduler gefeuert") },
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	s.Start()
	time.Sleep(100 * time.Millisecond)
	s.Stop()
}
