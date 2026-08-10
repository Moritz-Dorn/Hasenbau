//go:build !linux

package prozess

import "time"

// Ohne /proc gibt es kein verlässliches Lebendkriterium: kill(pid, 0)
// beantwortet nur, ob *irgendein* Prozess die PID hält, und trifft nach
// einem Recycling den Falschen. Der Hasenbau räumt dann lieber nicht auf
// — verwaiste Zeilen bleiben stehen, statt einen lebenden Lauf zu
// schließen (siehe Lebt).
const startZeitVerfuegbar = false

func startZeit(int) (time.Time, bool) { return time.Time{}, false }
