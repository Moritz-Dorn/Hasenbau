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

// PluginErgebnis sagt, was SchreibePlugin vorgefunden hat.
type PluginErgebnis int

const (
	// PluginUnveraendert: die Datei im Bau war schon die ausgelieferte.
	PluginUnveraendert PluginErgebnis = iota
	// PluginAngelegt: sie fehlte.
	PluginAngelegt
	// PluginErsetzt: dort stand etwas anderes — eine ältere Fassung oder
	// eine Änderung von Hand. Der Aufrufer soll das melden, sonst
	// verschwindet die Änderung von jemandem lautlos.
	PluginErsetzt
)

// SchreibePlugin bringt das Bau-Plugin auf den Stand des laufenden
// Binaries. Anders als die Vorlagen des Baus (hasenbau.yaml, die
// Sonder-Hasen) wird diese Datei ÜBERSCHRIEBEN, und das ist Absicht:
//
// Sie ist kein Material für den Menschen, sondern ein generiertes
// Artefakt wie die Agenten unter agents/ — und sie trägt seit
// Hasenbau-9w6 Sicherheitslogik, die niemand von Hand pflegt (das
// Review-Gate der Schmied-Werkzeuge, den Sandkasten samt Raum-Grenze,
// den Wächter selbst). Bliebe sie stehen wie eine Vorlage, hätte ein
// Bau von 2026-07 diese Zusagen für immer nicht, während PLAN und
// README sie behaupten — gemessen an ~/SRC/meinHasenbau, das 72 Zeilen
// gegen 359 trug (Hasenbau-uei).
//
// Wer eigene Hooks will, legt ein eigenes Plugin DANEBEN: das
// plugin/-Verzeichnis gehört weiter dem Bau, nur diese eine Datei
// nicht.
//
// Geschrieben wird nur bei Abweichung, und verglichen wird der Inhalt,
// nicht eine Versionsmarke. Eine Marke müsste jemand hochzählen und
// vergäße es; der Inhalt lügt nicht. Derselbe Gedanke wie beim
// Review-Block der Werkzeuge: an den Inhalt gebunden, nicht an die
// Herkunft.
func SchreibePlugin(root string) (PluginErgebnis, error) {
	pfad := filepath.Join(root, PluginDatei)
	alt, err := os.ReadFile(pfad)
	switch {
	case err == nil && string(alt) == sandboxWaechter:
		return PluginUnveraendert, nil
	case err != nil && !errors.Is(err, fs.ErrNotExist):
		return PluginUnveraendert, fmt.Errorf("bau: %s lesen: %w", PluginDatei, err)
	}
	if err := os.MkdirAll(filepath.Dir(pfad), 0o755); err != nil {
		return PluginUnveraendert, fmt.Errorf("bau: %s anlegen: %w", filepath.Dir(PluginDatei), err)
	}
	if err := os.WriteFile(pfad, []byte(sandboxWaechter), 0o644); err != nil {
		return PluginUnveraendert, fmt.Errorf("bau: %s schreiben: %w", PluginDatei, err)
	}
	if alt == nil {
		return PluginAngelegt, nil
	}
	return PluginErsetzt, nil
}

// PluginAktuell sagt, ob im Bau die Fassung dieses Binaries liegt — für
// die Diagnose, die nichts schreiben darf. Fehlt die Datei, ist sie
// nicht aktuell; das meldet checkWaechter aber als eigenen, lauteren
// Befund.
func PluginAktuell(root string) (bool, error) {
	b, err := os.ReadFile(filepath.Join(root, PluginDatei))
	if err != nil {
		return false, err
	}
	return string(b) == sandboxWaechter, nil
}

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
		return false, fmt.Errorf("bau: no Bau config at %s — run `hasenbau init`", pfad)
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
