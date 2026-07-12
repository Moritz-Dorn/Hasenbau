// Package bau legt das Verzeichnis-Layout eines Baus an (PLAN.md §4).
// Ein Bau ist selbst-enthaltend: Config, Räume und State liegen darin.
package bau

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// opencodeJSON ist die minimale, explizite Server-Config (§3): keine
// Plugins, kein Erbe der Alltags-Config. Der provider:-Block ist das
// Gerüst für eigene Definitionen — auth.json teilt nur Credentials,
// custom Provider brauchen ihre Definition hier (Befund Hasenbau-7ya).
const opencodeJSON = `{
  "$schema": "https://opencode.ai/config.json",
  "plugin": [],
  "provider": {}
}
`

const hasenbauYAML = `# Hasenbau — Daemon-Config (PLAN.md §4).
# Die Felder wachsen mit den Phasen; Unbekanntes wird abgelehnt.
log_level: info
`

// dirs sind die Verzeichnisse eines frischen Baus. raeume/ bleibt leer:
// Räume benennt der Auftrag, der Daemon legt fehlende an (§4).
var dirs = []string{
	".opencode-home/opencode/agents",
	".opencode-home/opencode/skills",
	"auftraege",
	"gaenge",
	"raeume",
	"state",
}

// files sind die Dateien eines frischen Baus. Bestehende Dateien werden
// nie überschrieben — Init ist idempotent und nicht-destruktiv.
var files = map[string]string{
	"hasenbau.yaml": hasenbauYAML,
	".opencode-home/opencode/opencode.json": opencodeJSON,
}

// Init legt das Layout unter root an. Vorhandenes bleibt unangetastet;
// zurückgegeben werden die tatsächlich neu angelegten Pfade (relativ).
func Init(root string) ([]string, error) {
	if fi, err := os.Stat(root); err == nil && !fi.IsDir() {
		return nil, fmt.Errorf("bau: %s existiert und ist kein Verzeichnis", root)
	}

	var created []string
	for _, d := range dirs {
		abs := filepath.Join(root, d)
		if _, err := os.Stat(abs); errors.Is(err, fs.ErrNotExist) {
			created = append(created, d+"/")
		}
		if err := os.MkdirAll(abs, 0o755); err != nil {
			return created, fmt.Errorf("bau: %s anlegen: %w", d, err)
		}
	}
	for rel, content := range files {
		abs := filepath.Join(root, rel)
		f, err := os.OpenFile(abs, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if errors.Is(err, fs.ErrExist) {
			continue // nie überschreiben
		}
		if err != nil {
			return created, fmt.Errorf("bau: %s anlegen: %w", rel, err)
		}
		if _, err := f.WriteString(content); err != nil {
			f.Close()
			return created, fmt.Errorf("bau: %s schreiben: %w", rel, err)
		}
		if err := f.Close(); err != nil {
			return created, fmt.Errorf("bau: %s schließen: %w", rel, err)
		}
		created = append(created, rel)
	}
	return created, nil
}
