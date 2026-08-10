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

// MCPUpdate sagt, was EnsureMCP an der Bau-Config geändert hat.
type MCPUpdate struct {
	Written  bool   // die Datei wurde geschrieben
	Previous string // der bisherige Binary-Pfad; "" wenn der Eintrag neu ist
}

// EnsureMCP hält den Rückkanal-Eintrag im mcp:-Block der Bau-Config auf
// dem laufenden Binary und meldet, was es getan hat.
//
// Der Eintrag ist eine Selbstreferenz: `hasenbau mcp` ist der Hasenbau
// selbst über stdio, nicht ein mitgeliefertes fremdes Programm. Damit
// beschreibt der Eintrag nicht „ein Werkzeug", sondern „das Binary, das
// diesen Bau fährt" — und das wechselt bei jedem Rebuild. Ein einmal
// eingetragener Pfad veraltet deshalb zwangsläufig; im Test-Bau zeigte
// er fünf Tage lang auf einen Wegwerf-Build unter /tmp und riss dem
// Baumeister still die Werkzeuge weg (Hasenbau-2nq, Hasenbau-08u).
//
// Angefasst wird ausschließlich das erste Element von command:, also
// der Binary-Pfad. Zusatz-Argumente, env, type und enabled bleiben
// stehen — wer den Eintrag von Hand erweitert, behält seine Erweiterung,
// nur eben nicht das veraltete Binary. Alles außerhalb des Eintrags
// fasst die Funktion nicht an (Reihenfolge und Einrückung normalisiert
// das Schreiben allerdings, wie bei `provider fetch`).
func EnsureMCP(root, exe string) (MCPUpdate, error) {
	pfad := filepath.Join(root, OpencodeConfig)
	b, err := os.ReadFile(pfad)
	if errors.Is(err, fs.ErrNotExist) {
		return MCPUpdate{}, fmt.Errorf("bau: keine Bau-Config unter %s — `hasenbau init` läuft lassen", pfad)
	}
	if err != nil {
		return MCPUpdate{}, fmt.Errorf("bau: %s lesen: %w", pfad, err)
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber() // Zahlen unverfälscht durchreichen
	roh := map[string]any{}
	if err := dec.Decode(&roh); err != nil {
		return MCPUpdate{}, fmt.Errorf("bau: %s parsen: %w", pfad, err)
	}

	mcp, ok := roh["mcp"].(map[string]any)
	if !ok {
		if _, belegt := roh["mcp"]; belegt {
			return MCPUpdate{}, fmt.Errorf("bau: %s: mcp ist kein Objekt", pfad)
		}
		mcp = map[string]any{}
	}

	update, err := setzeBinary(mcp, root, exe)
	if err != nil {
		return MCPUpdate{}, fmt.Errorf("bau: %s: %v", pfad, err)
	}
	if !update.Written {
		return update, nil
	}
	roh["mcp"] = mcp

	neu, err := json.MarshalIndent(roh, "", "  ")
	if err != nil {
		return MCPUpdate{}, fmt.Errorf("bau: %s serialisieren: %w", pfad, err)
	}
	if err := os.WriteFile(pfad, append(neu, '\n'), 0o644); err != nil {
		return MCPUpdate{}, fmt.Errorf("bau: %s schreiben: %w", pfad, err)
	}
	return update, nil
}

// setzeBinary bringt mcp[MCPEintrag] auf das laufende Binary und sagt,
// ob sich dabei etwas geändert hat. Fehlt der Eintrag oder ist seine
// command:-Zeile unbrauchbar, entsteht er neu in kanonischer Form.
func setzeBinary(mcp map[string]any, root, exe string) (MCPUpdate, error) {
	eintrag, ok := mcp[MCPEintrag].(map[string]any)
	if !ok {
		if _, belegt := mcp[MCPEintrag]; belegt {
			return MCPUpdate{}, fmt.Errorf("mcp.%s ist kein Objekt", MCPEintrag)
		}
		mcp[MCPEintrag] = kanonisch(root, exe)
		return MCPUpdate{Written: true}, nil
	}

	befehl, _ := eintrag["command"].([]any)
	if len(befehl) == 0 {
		// Ein Eintrag ohne aufrufbaren Befehl ist keine Handarbeit,
		// die man schonen müsste — er ist kaputt.
		eintrag["command"] = kanonisch(root, exe)["command"]
		return MCPUpdate{Written: true}, nil
	}
	alt, _ := befehl[0].(string)
	if alt == exe {
		return MCPUpdate{}, nil
	}
	befehl[0] = exe
	eintrag["command"] = befehl
	return MCPUpdate{Written: true, Previous: alt}, nil
}

// kanonisch ist der Eintrag, wie ihn der Hasenbau von sich aus schreibt.
func kanonisch(root, exe string) map[string]any {
	return map[string]any{
		"type":    "local",
		"command": []any{exe, "-bau", root, "mcp"},
		"enabled": true,
	}
}
