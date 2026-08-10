package store

import (
	"database/sql"
	"errors"
	"os/exec"
	"runtime"
	"testing"
	"time"

	"github.com/Moritz-Dorn/Hasenbau/internal/process"
)

// toterWirt liefert die PID und Startzeit eines Prozesses, den es nicht
// mehr gibt — die Leiche, die ein SIGKILL hinterlässt.
func toterWirt(t *testing.T) (int, time.Time) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skipf("ohne /proc kein Lebendkriterium (GOOS=%s)", runtime.GOOS)
	}
	cmd := exec.Command("/bin/sh", "-c", "exit 0")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	if process.Alive(pid, time.Now().UTC()) {
		t.Fatalf("PID %d lebt noch — Test taugt nicht", pid)
	}
	return pid, time.Now().UTC()
}

// setzeWirt schiebt einer Zeile einen fremden Wirt unter; anders kommt
// ein Test nicht an den Zustand nach einem harten Abbruch.
func setzeWirt(t *testing.T, s *Store, id int64, pid any, start any) {
	t.Helper()
	if _, err := s.db.Exec(
		`UPDATE laeufe SET pid = ?, pid_started = ? WHERE id = ?`, pid, start, id); err != nil {
		t.Fatal(err)
	}
}

func TestLaeufeAufraeumenSchontLebendeUndRaeumtLeichen(t *testing.T) {
	s := neuerStore(t)

	// Der eigene Prozess hält diesen Lauf — er läuft wirklich.
	lebend, err := s.StartLauf("pdf-einlagern", "watch", "raeume/laderampe/sources/a.pdf")
	if err != nil {
		t.Fatal(err)
	}
	// Diesen hier hielt ein Prozess, den es nicht mehr gibt.
	leiche, err := s.StartLauf("tagesbericht", "cron", "")
	if err != nil {
		t.Fatal(err)
	}
	totePID, toteStart := toterWirt(t)
	setzeWirt(t, s, leiche, totePID, toteStart)

	aufgeraeumt, err := s.CleanupLaeufe()
	if err != nil {
		t.Fatal(err)
	}
	if len(aufgeraeumt) != 1 || aufgeraeumt[0].ID != leiche {
		t.Fatalf("aufgeräumt = %+v, erwartet nur Lauf %d", aufgeraeumt, leiche)
	}
	if aufgeraeumt[0].Ended == nil || aufgeraeumt[0].Status != "aborted" {
		t.Errorf("aufgeräumter Lauf = %+v, erwartet abgebrochen mit beendet", aufgeraeumt[0])
	}

	tot, err := s.LaufByID(leiche)
	if err != nil {
		t.Fatal(err)
	}
	if tot.Status != "aborted" || tot.Ended == nil || tot.Error == "" {
		t.Errorf("Leiche in der DB = %+v, erwartet abgebrochen mit Grund", tot)
	}
	weiter, err := s.LaufByID(lebend)
	if err != nil {
		t.Fatal(err)
	}
	if weiter.Status != "running" {
		t.Errorf("lebender Lauf = %q, erwartet running", weiter.Status)
	}

	// Der Rückkanal ist danach wieder eindeutig (Kernpunkt §11.7).
	l, err := s.ActiveLauf()
	if err != nil || l.ID != lebend {
		t.Errorf("ActiveLauf = %+v, %v — erwartet Lauf %d", l, err, lebend)
	}

	// Zweiter Durchgang: nichts mehr zu tun, nichts kaputt.
	nochmal, err := s.CleanupLaeufe()
	if err != nil || len(nochmal) != 0 {
		t.Errorf("zweiter Durchgang: %+v, %v", nochmal, err)
	}
}

func TestAufraeumenZaehltFehlerSerieHoch(t *testing.T) {
	s := neuerStore(t)

	id, err := s.StartLauf("tagesbericht", "cron", "")
	if err != nil {
		t.Fatal(err)
	}
	totePID, toteStart := toterWirt(t)
	setzeWirt(t, s, id, totePID, toteStart)
	if _, err := s.CleanupLaeufe(); err != nil {
		t.Fatal(err)
	}

	states, err := s.AuftragStates()
	if err != nil {
		t.Fatal(err)
	}
	if len(states) != 1 || states[0].ErrorStreak != 1 {
		t.Errorf("Auftrag-Zustand = %+v, erwartet Fehlerserie 1", states)
	}
}

func TestZeileOhneWirtGiltAlsVerwaist(t *testing.T) {
	s := neuerStore(t)

	// Eine Zeile aus der Zeit vor den Wirt-Spalten.
	id, err := s.StartLauf("pdf-einlagern", "manual", "")
	if err != nil {
		t.Fatal(err)
	}
	setzeWirt(t, s, id, nil, nil)

	if _, err := s.ActiveLauf(); !errors.Is(err, ErrNoActiveLauf) {
		t.Errorf("Zeile ohne Wirt: ActiveLauf err = %v, erwartet ErrNoActiveLauf", err)
	}
	aufgeraeumt, err := s.CleanupLaeufe()
	if err != nil {
		t.Fatal(err)
	}
	if len(aufgeraeumt) != 1 || aufgeraeumt[0].ID != id {
		t.Fatalf("aufgeräumt = %+v, erwartet Lauf %d", aufgeraeumt, id)
	}
}

func TestAktiverLaufIgnoriertLeichen(t *testing.T) {
	s := neuerStore(t)

	leiche, err := s.StartLauf("tagesbericht", "cron", "")
	if err != nil {
		t.Fatal(err)
	}
	totePID, toteStart := toterWirt(t)
	setzeWirt(t, s, leiche, totePID, toteStart)
	lebend, err := s.StartLauf("pdf-einlagern", "watch", "a.pdf")
	if err != nil {
		t.Fatal(err)
	}

	// Ohne Aufräumen: die Leiche darf den Rückkanal nicht mehrdeutig
	// machen — sie gehört zu keinem laufenden Hasen.
	l, err := s.ActiveLauf()
	if err != nil {
		t.Fatalf("ActiveLauf mit Leiche in der DB: %v", err)
	}
	if l.ID != lebend {
		t.Errorf("ActiveLauf = %d, erwartet %d", l.ID, lebend)
	}
}

func TestLaufBeginneSchreibtDenWirt(t *testing.T) {
	s := neuerStore(t)

	id, err := s.StartLauf("pdf-einlagern", "manual", "")
	if err != nil {
		t.Fatal(err)
	}
	var pid int
	var start sql.NullTime
	if err := s.db.QueryRow(
		`SELECT pid, pid_started FROM laeufe WHERE id = ?`, id).Scan(&pid, &start); err != nil {
		t.Fatal(err)
	}
	ichPID, ichStart := process.Self()
	if pid != ichPID {
		t.Errorf("pid in der Zeile = %d, erwartet %d", pid, ichPID)
	}
	if ichStart.IsZero() {
		return // Plattform ohne Startzeit: NULL ist richtig
	}
	// Die Startzeit muss den Weg durch die DB überstehen — sonst hielte
	// der Vergleich in process.Alive jeden eigenen Lauf für recycelt.
	if !start.Valid || !start.Time.Equal(ichStart) {
		t.Errorf("pid_gestartet in der Zeile = %v, erwartet %s", start, ichStart)
	}
}
