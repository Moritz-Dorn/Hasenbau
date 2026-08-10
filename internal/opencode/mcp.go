// mcp.go fragt den Server, wie es den MCP-Servern des Baus geht.
//
// Nötig, weil ein MCP-Client, der nicht hochkommt, vollkommen still
// ist: opencode startet trotzdem, loggt nichts auf stdout, und der
// Hase bekommt die Werkzeuge einfach nicht. Er erzählt seine Summary
// dann als Fließtext, und der Lauf sieht von außen erfolgreich aus
// (Hasenbau-08u, Lauf 10 im Test-Bau).
package opencode

import (
	"context"
	"fmt"

	sdk "github.com/sst/opencode-sdk-go"
)

// MCPState ist der Zustand eines MCP-Servers. Status ist einer von
// connected, disabled, failed, needs_auth, needs_client_registration;
// Error tragen nur die beiden letzten Fehlerfälle (opencode 1.15.x,
// Schema MCPStatus unter <server>/doc).
type MCPState struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// MCPConnected ist der einzige Zustand, in dem die Werkzeuge eines
// MCP-Servers tatsächlich beim Hasen ankommen.
const MCPConnected = "connected"

// MCPStatus liefert den Zustand aller konfigurierten MCP-Server, je
// Config-Schlüssel. SDK v0.19.2 kennt keinen MCP-Service; Client.Get
// ist der dokumentierte Weg für solche Endpoints (wie bei
// DisposeInstance).
func MCPStatus(ctx context.Context, client *sdk.Client) (map[string]MCPState, error) {
	var status map[string]MCPState
	if err := client.Get(ctx, "mcp", nil, &status); err != nil {
		return nil, fmt.Errorf("mcp: %w", err)
	}
	return status, nil
}
