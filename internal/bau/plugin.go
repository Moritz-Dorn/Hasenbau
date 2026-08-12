// plugin.go trägt den Sandbox-Wächter in den plugin:-Block der
// Bau-Config ein (Hasenbau-d2p). Aufbau und Haltung wie mcp.go: nur der
// eigene Eintrag wird angefasst, alles andere bleibt stehen.
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

// PluginDatei ist der Ort des Wächters, Bau-relativ. Er liegt im
// Config-Home neben den generierten Agenten — von dort lädt opencode
// ihn (verifiziert an 1.15.13).
const PluginDatei = ".opencode-home/opencode/plugin/hasenbau.js"

// PluginEintrag ist der Pfad, wie er im plugin:-Array steht: relativ
// zum opencode-Config-Verzeichnis, nicht zum Bau. Ein absoluter Pfad
// täte es auch, wäre aber nach jedem Verschieben des Baus falsch.
const PluginEintrag = "./plugin/hasenbau.js"

// EnsurePlugin stellt sicher, dass der Wächter im plugin:-Block steht.
// Rückgabe true = die Config wurde geschrieben.
//
// Anders als beim Rückkanal steht hier kein Binary-Pfad, der veralten
// könnte — der Eintrag ist relativ und damit stabil. Den Binary-Pfad
// holt sich das Plugin zur Laufzeit aus dem mcp:-Block, den EnsureMCP
// aktuell hält.
func EnsurePlugin(root string) (bool, error) {
	pfad := filepath.Join(root, OpencodeConfig)
	b, err := os.ReadFile(pfad)
	if errors.Is(err, fs.ErrNotExist) {
		return false, fmt.Errorf("bau: keine Bau-Config unter %s — `hasenbau init` läuft lassen", pfad)
	}
	if err != nil {
		return false, fmt.Errorf("bau: %s lesen: %w", pfad, err)
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	roh := map[string]any{}
	if err := dec.Decode(&roh); err != nil {
		return false, fmt.Errorf("bau: %s parsen: %w", pfad, err)
	}

	liste, ok := roh["plugin"].([]any)
	if !ok {
		if _, belegt := roh["plugin"]; belegt {
			return false, fmt.Errorf("bau: %s: plugin ist keine Liste", pfad)
		}
		liste = []any{}
	}
	for _, e := range liste {
		if s, _ := e.(string); s == PluginEintrag {
			return false, nil
		}
	}
	roh["plugin"] = append(liste, PluginEintrag)

	neu, err := json.MarshalIndent(roh, "", "  ")
	if err != nil {
		return false, fmt.Errorf("bau: %s serialisieren: %w", pfad, err)
	}
	if err := os.WriteFile(pfad, append(neu, '\n'), 0o644); err != nil {
		return false, fmt.Errorf("bau: %s schreiben: %w", pfad, err)
	}
	return true, nil
}

// PluginEingetragen sagt, ob der Wächter im plugin:-Block steht — für
// die Diagnose, die nichts schreiben darf.
func PluginEingetragen(root string) (bool, error) {
	b, err := os.ReadFile(filepath.Join(root, OpencodeConfig))
	if err != nil {
		return false, err
	}
	var roh struct {
		Plugin []string `json:"plugin"`
	}
	if err := json.Unmarshal(b, &roh); err != nil {
		return false, err
	}
	for _, e := range roh.Plugin {
		if e == PluginEintrag {
			return true, nil
		}
	}
	return false, nil
}
