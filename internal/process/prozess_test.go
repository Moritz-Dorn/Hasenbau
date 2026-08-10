package process

import (
	"os"
	"os/exec"
	"runtime"
	"testing"
	"time"
)

func TestIchLebt(t *testing.T) {
	pid, start := Self()
	if pid != os.Getpid() {
		t.Errorf("Self() = %d, erwartet %d", pid, os.Getpid())
	}
	if !Alive(pid, start) {
		t.Errorf("der eigene Prozess (%d, %s) gilt als tot", pid, start)
	}
	// Zweiter Aufruf muss dasselbe liefern — die Startzeit ist der
	// Anker, an dem später eine recycelte PID auffliegt.
	pid2, start2 := Self()
	if pid2 != pid || !start2.Equal(start) {
		t.Errorf("Self() ist nicht stabil: (%d, %s) vs. (%d, %s)", pid, start, pid2, start2)
	}
}

func TestLebtLehntUnsinnigePIDAb(t *testing.T) {
	if Alive(0, time.Time{}) || Alive(-1, time.Time{}) {
		t.Error("PID <= 0 gilt als lebend")
	}
}

// nurLinux überspringt, was am /proc-Kriterium hängt: ohne Startzeit
// ist Lebt bewusst pauschal true (prozess_andere.go).
func nurLinux(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skipf("ohne /proc kein Lebendkriterium (GOOS=%s)", runtime.GOOS)
	}
}

func TestToterProzessLebtNicht(t *testing.T) {
	nurLinux(t)

	cmd := exec.Command("/bin/sh", "-c", "exit 0")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	start, ok := startTime(pid)
	if !ok {
		// Das Kind war schneller als wir — dann ist es garantiert tot,
		// und die Startzeit spielt keine Rolle mehr.
		start = time.Now()
	}
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	if Alive(pid, start) {
		t.Errorf("beendetes Kind (PID %d) gilt als lebend", pid)
	}
}

func TestRecyceltePIDGiltNichtAlsLebend(t *testing.T) {
	nurLinux(t)

	// Dieselbe PID, aber eine Inkarnation von vor einer Stunde: genau
	// der Fall, den die PID allein nicht erkennt.
	pid, start := Self()
	if Alive(pid, start.Add(-time.Hour)) {
		t.Error("PID mit fremder Startzeit gilt als lebend")
	}
	// Sekundenbruchteile dürfen dagegen nicht stören — die Startzeit
	// kommt aus Boot-Zeit plus Ticks.
	if !Alive(pid, start.Add(-time.Second)) {
		t.Error("Startzeit-Toleranz greift nicht")
	}
}

// TestZombieGiltAlsTot: der realistische Fall eines SIGKILLs bei
// lebendem Elternprozess. /proc/<pid>/stat gibt es dann noch — der Lauf
// läuft trotzdem nicht mehr.
func TestZombieGiltAlsTot(t *testing.T) {
	nurLinux(t)

	schlaf, err := exec.LookPath("sleep")
	if err != nil {
		t.Skipf("kein sleep im PATH: %v", err)
	}
	cmd := exec.Command(schlaf, "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	start, ok := startTime(pid)
	if !ok {
		t.Fatalf("keine Startzeit für PID %d", pid)
	}
	if !Alive(pid, start) {
		t.Fatalf("laufendes Kind (PID %d) gilt als tot", pid)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	// Kein Wait: das Kind bleibt Zombie, bis der Test es abholt.
	defer cmd.Wait()

	frist := time.Now().Add(5 * time.Second)
	for Alive(pid, start) {
		if time.Now().After(frist) {
			t.Fatalf("Zombie (PID %d) gilt weiter als lebend", pid)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestStartZeitIstPlausibel(t *testing.T) {
	nurLinux(t)

	_, start := Self()
	if start.IsZero() {
		t.Fatal("keine Startzeit für den eigenen Prozess")
	}
	// Der Testprozess ist gerade erst angelaufen; mehr als ein paar
	// Minuten daneben hieße, dass Boot-Zeit oder Ticks nicht stimmen.
	if abstand := time.Since(start); abstand < -startTolerance || abstand > 10*time.Minute {
		t.Errorf("Startzeit %s liegt %s zurück — unplausibel", start, abstand)
	}
}
