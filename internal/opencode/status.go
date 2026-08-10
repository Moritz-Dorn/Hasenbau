// status.go fragt ab, was der Event-Stream nur *meldet*: ob der Server
// eine Session gerade noch bearbeitet.
//
// Der Unterschied ist der Grund für diese Datei. `session.idle` ist ein
// flüchtiges Ereignis — wer im falschen Moment die Verbindung verliert,
// bekommt es nie wieder zu sehen, und ein Lauf wartet dann bis zum
// Timeout auf etwas, das längst passiert ist (Hasenbau-0f4). Ein
// Zustand lässt sich dagegen jederzeit nachfragen.
package opencode

import (
	"context"
	"fmt"

	sdk "github.com/sst/opencode-sdk-go"
)

// sessionStatus ist der Zustand einer Session. Bekannte Typen sind
// idle, busy und retry (opencode 1.15.x, Schema SessionStatus unter
// <server>/doc); fertige Sessions verschwinden aus der Antwort.
type sessionStatus struct {
	Type string `json:"type"`
}

// SessionBusy sagt, ob der Server die Session noch bearbeitet.
//
// Alles außer `idle` gilt als beschäftigt — `retry` heißt, dass der
// Server es gerade nochmal versucht, und das ist kein Lauf-Ende. Eine
// Session, die gar nicht mehr auftaucht, ist fertig.
func SessionBusy(ctx context.Context, client *sdk.Client, sessionID string) (bool, error) {
	var status map[string]sessionStatus
	if err := client.Get(ctx, "session/status", nil, &status); err != nil {
		return false, fmt.Errorf("session/status: %w", err)
	}
	s, da := status[sessionID]
	return da && s.Type != "idle", nil
}
