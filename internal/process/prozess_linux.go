//go:build linux

package process

import (
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const startTimeAvailable = true

// ticksPerSecond ist USER_HZ: /proc/<pid>/stat rechnet in diesen Ticks.
// Der Kernel meldet sie über die proc-Schnittstelle unabhängig von
// seiner internen HZ immer als 100; sysconf(_SC_CLK_TCK) bräuchte cgo,
// das der Hasenbau bewusst nicht hat (§ store: pure Go).
const ticksPerSecond = 100

// bootTime ist der Nullpunkt für Feld 22 aus /proc/<pid>/stat und
// ändert sich über die Prozess-Lebensdauer nicht.
var bootTime = sync.OnceValues(func() (time.Time, bool) {
	roh, err := os.ReadFile("/proc/stat")
	if err != nil {
		return time.Time{}, false
	}
	for _, zeile := range strings.Split(string(roh), "\n") {
		rest, ok := strings.CutPrefix(zeile, "btime ")
		if !ok {
			continue
		}
		sek, err := strconv.ParseInt(strings.TrimSpace(rest), 10, 64)
		if err != nil {
			return time.Time{}, false
		}
		return time.Unix(sek, 0).UTC(), true
	}
	return time.Time{}, false
})

// startTime liest die Startzeit des Prozesses aus /proc/<pid>/stat
// (Feld 22: Ticks seit Boot). false heißt: diesen Prozess gibt es nicht
// mehr. Ein Zombie zählt dabei als tot — sein Lauf läuft nicht mehr, er
// wartet nur darauf, dass jemand ihn abholt.
func startTime(pid int) (time.Time, bool) {
	roh, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return time.Time{}, false
	}
	// Feld 2 (comm) steht in Klammern und darf Leerzeichen und
	// Klammern enthalten — deshalb erst ab der letzten ')' zerlegen.
	// felder[0] ist dann Feld 3 (state), Feld 22 also felder[19].
	zu := strings.LastIndexByte(string(roh), ')')
	if zu < 0 {
		return time.Time{}, false
	}
	felder := strings.Fields(string(roh)[zu+1:])
	if len(felder) < 20 || felder[0] == "Z" {
		return time.Time{}, false
	}
	ticks, err := strconv.ParseInt(felder[19], 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	boot, ok := bootTime()
	if !ok {
		return time.Time{}, false
	}
	return boot.Add(time.Duration(ticks) * time.Second / ticksPerSecond), true
}
