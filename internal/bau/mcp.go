package bau

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// MCPSicherstellen trägt den Rückkanal in den mcp:-Block der Bau-Config
// ein, falls er dort fehlt, und meldet, ob geschrieben wurde.
//
// Das läuft bei jedem Server-Start, nicht nur bei `init`: Der Eintrag
// zeigt auf das laufende Binary, und ältere Baue kennen ihn noch nicht.
// Ein vorhandener Eintrag bleibt unangetastet — wer ihn von Hand
// anpasst, behält ihn. Alles andere in der Datei fasst die Funktion
// nicht an (Reihenfolge und Einrückung normalisiert das Schreiben
// allerdings, wie bei `provider fetch`).
func MCPSicherstellen(root, exe string) (bool, error) {
	pfad := filepath.Join(root, OpencodeConfig)
	b, err := os.ReadFile(pfad)
	if errors.Is(err, fs.ErrNotExist) {
		return false, fmt.Errorf("bau: keine Bau-Config unter %s — `hasenbau init` läuft lassen", pfad)
	}
	if err != nil {
		return false, fmt.Errorf("bau: %s lesen: %w", pfad, err)
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber() // Zahlen unverfälscht durchreichen
	roh := map[string]any{}
	if err := dec.Decode(&roh); err != nil {
		return false, fmt.Errorf("bau: %s parsen: %w", pfad, err)
	}

	mcp, ok := roh["mcp"].(map[string]any)
	if !ok {
		if _, belegt := roh["mcp"]; belegt {
			return false, fmt.Errorf("bau: %s: mcp ist kein Objekt", pfad)
		}
		mcp = map[string]any{}
	}
	if _, da := mcp[MCPEintrag]; da {
		return false, nil
	}

	mcp[MCPEintrag] = map[string]any{
		"type":    "local",
		"command": []any{exe, "-bau", root, "mcp"},
		"enabled": true,
	}
	roh["mcp"] = mcp

	neu, err := json.MarshalIndent(roh, "", "  ")
	if err != nil {
		return false, fmt.Errorf("bau: %s serialisieren: %w", pfad, err)
	}
	if err := os.WriteFile(pfad, append(neu, '\n'), 0o644); err != nil {
		return false, fmt.Errorf("bau: %s schreiben: %w", pfad, err)
	}
	return true, nil
}
