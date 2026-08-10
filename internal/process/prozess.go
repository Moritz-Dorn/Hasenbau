// Package prozess beantwortet die eine Frage, die eine verwaiste
// 'running'-Zeile von einem lebenden Lauf trennt: Läuft der Prozess, der
// diesen Lauf hält, überhaupt noch? (PLAN.md §5, Hasenbau-c6i)
//
// Die PID allein reicht dafür nicht — sie wird recycelt, und dann hielte
// irgendein fremdes Programm eine Leiche am Leben. Erst PID plus
// Startzeit identifiziert eine Prozess-Inkarnation.
package process

import (
	"os"
	"sync"
	"time"
)

// startTolerance ist die Toleranz beim Vergleich zweier Startzeiten.
// Die Startzeit wird aus Boot-Zeit plus Ticks gerechnet und ist deshalb
// grob sekundengenau, nicht exakt. Zum Erkennen einer recycelten PID
// reicht das: der Nachfolger startet Sekunden bis Tage später.
const startTolerance = 2 * time.Second

var self = sync.OnceValues(func() (int, time.Time) {
	pid := os.Getpid()
	start, _ := startTime(pid) // Nullzeit, wo die Plattform sie nicht hergibt
	return pid, start
})

// Self liefert PID und Startzeit des eigenen Prozesses — das, was ein
// Lauf über seinen Wirt in die Datenbank schreibt. Die Startzeit ist die
// Nullzeit, wenn die Plattform sie nicht hergibt.
func Self() (int, time.Time) { return self() }

// Alive sagt, ob die Prozess-Inkarnation (pid, start) noch läuft. Eine
// Nullzeit als start heißt „Startzeit unbekannt" — dann entscheidet die
// bloße Existenz der PID.
//
// Im Zweifel true: eine verwaiste Zeile, die eine Runde länger steht,
// ist ärgerlich — ein abgeräumter lebender Lauf ist Datenverlust.
func Alive(pid int, start time.Time) bool {
	if pid <= 0 {
		return false
	}
	if !startTimeAvailable {
		// Ohne Startzeit gibt es kein verlässliches Kriterium: die
		// bloße PID beantwortet nur, ob *irgendein* Prozess sie hält.
		return true
	}
	ist, ok := startTime(pid)
	if !ok {
		return false // kein solcher Prozess mehr
	}
	if start.IsZero() {
		return true
	}
	return ist.Sub(start).Abs() <= startTolerance
}
