//go:build !linux

package process

import "time"

// Ohne /proc gibt es kein verlässliches Lebendkriterium: kill(pid, 0)
// beantwortet nur, ob *irgendein* Prozess die PID hält, und trifft nach
// einem Recycling den Falschen. Der Hasenbau räumt dann lieber nicht auf
// — verwaiste Zeilen bleiben stehen, statt einen lebenden Lauf zu
// schließen (siehe Lebt).
const startTimeAvailable = false

func startTime(int) (time.Time, bool) { return time.Time{}, false }
