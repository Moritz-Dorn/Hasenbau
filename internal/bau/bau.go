// Package bau legt das Verzeichnis-Layout eines Baus an (PLAN.md §4).
// Ein Bau ist selbst-enthaltend: Config, Räume und State liegen darin.
package bau

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// OpencodeConfig ist der Ort der Bau-eigenen Server-Config, Bau-relativ
// (§3: XDG_CONFIG_HOME zeigt auf .opencode-home/, opencode sucht darin
// sein opencode/-Unterverzeichnis).
const OpencodeConfig = ".opencode-home/opencode/opencode.json"

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

// MCPEintrag ist der Schlüssel des Rückkanals im mcp:-Block der
// Bau-Config (PLAN.md §8, Phase 2). opencode stellt ihn den Werkzeugen
// voran — der Hase sieht `hasenbau_notiz` und `hasenbau_summary`
// (verifiziert an einem echten Lauf, opencode 1.15.13).
const MCPEintrag = "hasenbau"

const hasenbauYAML = `# Hasenbau — Daemon-Config (PLAN.md §4).
# Die Felder wachsen mit den Phasen; Unbekanntes wird abgelehnt.
log_level: info

# Welcher Auftrag ` + "`hasenbau baumeister`" + ` startet. Vorlage zum
# Kopieren: beispiele/auftraege/baumeister.md + beispiele/hasen/baumeister.md
# baumeister: baumeister
`

// gitIgnore: Der Bau versioniert Definitionen (Aufträge, Hasen, Gänge,
// Config), nicht Laufzeit-Material und nicht generierte Agenten.
const gitIgnore = `state/
raeume/
.opencode-home/opencode/agents/
`

// dirs sind die Verzeichnisse eines frischen Baus. raeume/ bleibt leer:
// Räume benennt der Auftrag, der Daemon legt fehlende an (§4).
var dirs = []string{
	".opencode-home/opencode/agents",
	".opencode-home/opencode/skills",
	"auftraege",
	"gaenge",
	"hasen",
	"raeume",
	"state",
}

// files sind die Dateien eines frischen Baus. Bestehende Dateien werden
// nie überschrieben — Init ist idempotent und nicht-destruktiv.
var files = map[string]string{
	"hasenbau.yaml": hasenbauYAML,
	".gitignore":    gitIgnore,
	OpencodeConfig:  opencodeJSON,
}

// Init legt das Layout unter root an. Vorhandenes bleibt unangetastet;
// zurückgegeben werden die tatsächlich neu angelegten Pfade (relativ).
//
// exe ist der Pfad des laufenden Binaries — er wandert in den
// Rückkanal-Eintrag der Bau-Config (§8). Ihn hier schon zu setzen ist
// kein Vorgriff auf den Daemon-Start: eine Bau-Config ohne `mcp.hasenbau`
// ist unvollständig, `describe bau` meldet sie als offenen Punkt, und ein
// frischer Bau soll nicht mit einem Befund auf die Welt kommen. Der
// Eintrag entsteht vor dem Root-Commit, damit er darin steht statt den
// Bau ab dem ersten Lauf schmutzig zu machen.
func Init(root, exe string) ([]string, error) {
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

	// Nach den Dateien (die Config muss liegen) und vor dem Commit.
	if _, err := EnsureMCP(root, exe); err != nil {
		return created, err
	}

	initialisiert, err := gitSicherstellen(root)
	if err != nil {
		return created, err
	}
	if initialisiert {
		created = append(created, ".git/")
	}
	return created, nil
}

// gitSicherstellen macht den Bau zu einem Git-Repo mit mindestens einem
// Commit (PLAN.md §11.5): ohne Git ist das opencode-Projekt „global" mit
// worktree=/ — Raum-Permissions der Hasen matchen nie, und
// „always"-Approvals des Alltags-opencode lecken über die geteilte
// Projekt-ID in die Läufe.
func gitSicherstellen(root string) (bool, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return false, fmt.Errorf("bau: git nicht gefunden — ein Bau muss ein Git-Repo sein (PLAN.md §11.5)")
	}

	// Auf root/.git prüfen, nicht per rev-parse: das würde bei einem Bau
	// innerhalb eines fremden Repos dessen Parent finden und Init überspringen.
	var initialisiert bool
	if _, err := os.Stat(filepath.Join(root, ".git")); errors.Is(err, fs.ErrNotExist) {
		if err := gitLauf(root, "init", "-q"); err != nil {
			return false, err
		}
		initialisiert = true
	}

	// Mindestens ein Commit: erst damit bekommt der Bau bei opencode eine
	// eigene Projekt-ID (Hash des Root-Commits).
	if gitLauf(root, "rev-parse", "--verify", "-q", "HEAD") == nil {
		return initialisiert, nil
	}
	if err := gitLauf(root, "add", "-A"); err != nil {
		return initialisiert, err
	}
	if err := gitLauf(root,
		"-c", "user.name=hasenbau", "-c", "user.email=hasenbau@localhost",
		"-c", "commit.gpgsign=false",
		"commit", "-q", "-m", "hasenbau init"); err != nil {
		return initialisiert, err
	}
	return true, nil
}

func gitLauf(root string, args ...string) error {
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("bau: git %s: %w: %s", args[0], err, strings.TrimSpace(string(out)))
	}
	return nil
}
