// diagnose.go beantwortet eine Frage: **ist dieser Bau in Ordnung?**
// (Hasenbau-ha0.6)
//
// Nicht „was liegt hier" — das ist `status`. Hier stehen die Prüfungen,
// und zwar die, deren Fehlschlag man sonst nicht merkt: ein Bau ohne
// Git-Commit bekommt bei opencode keine eigene Projekt-ID, und ohne die
// greifen die Raum-Permissions nicht (§11.5). Ein Rückkanal-Eintrag auf
// ein verschwundenes Binary nimmt den Hasen still ihre Werkzeuge weg
// (Hasenbau-2nq/08u). Beides sieht man dem Bau nicht an, und beides
// merkt man erst an einem Lauf, der komisch aussieht.
package bau

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Check ist eine einzelne Prüfung. Hint steht nur da, wenn etwas zu tun
// ist — eine bestandene Prüfung braucht keinen Rat.
type Check struct {
	Name   string
	OK     bool
	Detail string
	Hint   string
}

// Diagnose prüft, was der Bau über sich selbst wissen kann. Die
// Reihenfolge ist die Ausgabe-Reihenfolge und geht von außen nach
// innen: Layout, Git, Config, Rückkanal.
func Diagnose(root string) []Check {
	return []Check{
		checkLayout(root),
		checkGit(root),
		checkOpencodeConfig(root),
		checkMCP(root),
		checkHasenbauYAML(root),
	}
}

func checkLayout(root string) Check {
	var fehlt []string
	for _, d := range dirs {
		if fi, err := os.Stat(filepath.Join(root, d)); err != nil || !fi.IsDir() {
			fehlt = append(fehlt, d+"/")
		}
	}
	for f := range files {
		if _, err := os.Stat(filepath.Join(root, f)); err != nil {
			fehlt = append(fehlt, f)
		}
	}
	sort.Strings(fehlt)
	if len(fehlt) > 0 {
		return Check{Name: "Layout", Detail: "fehlt: " + strings.Join(fehlt, ", "),
			Hint: "`hasenbau init " + root + "` legt Fehlendes nach, ohne Vorhandenes anzufassen"}
	}
	return Check{Name: "Layout", OK: true, Detail: fmt.Sprintf("%d Verzeichnisse, %d Dateien", len(dirs), len(files))}
}

// checkGit ist die wichtigste Prüfung und die unauffälligste: ohne
// Commit hat der Bau bei opencode keine eigene Projekt-ID, und die
// Permissions der Hasen ankern dann woanders (§11.5).
func checkGit(root string) Check {
	if _, err := os.Stat(filepath.Join(root, ".git")); errors.Is(err, fs.ErrNotExist) {
		return Check{Name: "Git-Repo", Detail: "kein .git",
			Hint: "`git init` im Bau, dann committen — ohne eigene Projekt-ID greifen die Raum-Permissions nicht (PLAN.md §11.5)"}
	}
	if _, err := exec.LookPath("git"); err != nil {
		return Check{Name: "Git-Repo", Detail: "git nicht im PATH — nicht prüfbar"}
	}
	cmd := exec.Command("git", "-C", root, "rev-parse", "--verify", "-q", "HEAD")
	roh, err := cmd.Output()
	if err != nil {
		return Check{Name: "Git-Repo", Detail: "Repo ohne Commit",
			Hint: "`git add -A && git commit` — erst der Root-Commit gibt dem Bau seine Projekt-ID (PLAN.md §11.5)"}
	}
	commit := strings.TrimSpace(string(roh))
	if len(commit) > 12 {
		commit = commit[:12]
	}
	return Check{Name: "Git-Repo", OK: true, Detail: "HEAD " + commit}
}

func checkOpencodeConfig(root string) Check {
	pfad := filepath.Join(root, OpencodeConfig)
	roh, err := os.ReadFile(pfad)
	if err != nil {
		return Check{Name: "Bau-Config", Detail: OpencodeConfig + " fehlt",
			Hint: "`hasenbau init` legt sie an — ohne sie liefe der Server mit der Alltags-Config (PLAN.md §3)"}
	}
	var m map[string]any
	if err := json.NewDecoder(bytes.NewReader(roh)).Decode(&m); err != nil {
		return Check{Name: "Bau-Config", Detail: "unlesbar: " + err.Error(),
			Hint: "JSON reparieren — opencode startet damit nicht"}
	}
	var teile []string
	if p, ok := m["plugin"].([]any); ok && len(p) == 0 {
		teile = append(teile, "plugin: [] (keine geerbten)")
	}
	if e, ok := m["enabled_providers"].([]any); ok {
		teile = append(teile, fmt.Sprintf("%d aktive Provider", len(e)))
	}
	if len(teile) == 0 {
		teile = append(teile, "lesbar")
	}
	return Check{Name: "Bau-Config", OK: true, Detail: strings.Join(teile, ", ")}
}

// checkMCP ist der Befund aus Hasenbau-2nq: der Eintrag zeigte fünf
// Tage lang auf ein Wegwerf-Binary unter /tmp, und niemandem fiel es
// auf, weil ein Lauf ohne Rückkanal normal aussieht.
func checkMCP(root string) Check {
	pfad := filepath.Join(root, OpencodeConfig)
	roh, err := os.ReadFile(pfad)
	if err != nil {
		return Check{Name: "Rückkanal", Detail: "keine Bau-Config"}
	}
	var m map[string]any
	if err := json.Unmarshal(roh, &m); err != nil {
		return Check{Name: "Rückkanal", Detail: "Bau-Config unlesbar"}
	}
	mcp, _ := m["mcp"].(map[string]any)
	eintrag, ok := mcp[MCPEintrag].(map[string]any)
	if !ok {
		return Check{Name: "Rückkanal", Detail: "kein mcp." + MCPEintrag,
			Hint: "der nächste Daemon- oder Lauf-Start trägt ihn ein"}
	}
	if an, da := eintrag["enabled"].(bool); da && !an {
		return Check{Name: "Rückkanal", Detail: "enabled: false",
			Hint: "so bekommt kein Hase hasenbau_summary und hasenbau_notiz"}
	}
	befehl, _ := eintrag["command"].([]any)
	if len(befehl) == 0 {
		return Check{Name: "Rückkanal", Detail: "Eintrag ohne command",
			Hint: "der nächste Start setzt ihn kanonisch neu"}
	}
	binary, _ := befehl[0].(string)
	if _, err := os.Stat(binary); err != nil {
		return Check{Name: "Rückkanal", Detail: "zeigt auf " + binary + " — das gibt es nicht (mehr)",
			Hint: "der nächste Daemon- oder Lauf-Start korrigiert den Pfad auf das laufende Binary"}
	}
	return Check{Name: "Rückkanal", OK: true, Detail: binary}
}

func checkHasenbauYAML(root string) Check {
	if _, err := os.Stat(filepath.Join(root, ConfigFile)); err != nil {
		return Check{Name: ConfigFile, Detail: "fehlt — es gelten die Vorgaben"}
	}
	conf, err := LoadConfig(root)
	if err != nil {
		return Check{Name: ConfigFile, Detail: err.Error(), Hint: "YAML reparieren"}
	}
	teile := []string{"log_level: " + orDash(conf.LogLevel)}
	if conf.Baumeister == "" {
		return Check{Name: ConfigFile, OK: true, Detail: strings.Join(teile, ", ") + ", kein baumeister",
			Hint: "ohne `baumeister:` läuft `hasenbau baumeister` nicht"}
	}
	teile = append(teile, "baumeister: "+conf.Baumeister)
	return Check{Name: ConfigFile, OK: true, Detail: strings.Join(teile, ", ")}
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
