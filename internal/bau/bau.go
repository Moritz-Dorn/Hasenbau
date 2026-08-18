// Package bau legt das Verzeichnis-Layout eines Baus an (PLAN.md §4).
// Ein Bau ist selbst-enthaltend: Config, Räume und State liegen darin.
package bau

import (
	_ "embed"
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

# Welcher Auftrag ` + "`hasenbau baumeister`" + ` startet. Auftrag und Hase
# legt ` + "`hasenbau init`" + ` mit an; ` + "`hasenbau fix`" + ` stellt sie wieder her,
# wenn sie fehlen.
baumeister: baumeister

# Was der Sandbox-Wächter tut, wenn ein Hase ein Werkzeug ruft, das ihn
# aus seiner Sandbox führen würde (task, bash, webfetch, websearch):
#   deny  Aufruf abweisen — der Hase bekommt den Grund zu lesen (Vorgabe)
#   warn  Aufruf durchlassen und nur melden
# Gemeldet wird in beiden Fällen, als Notiz am Lauf.
# sandbox: warn

# Wohin ` + "`hasenbau_tool_request`" + ` die Wünsche der Hasen legt; die
# Werkzeug-Wünsche landen darin unter ` + "`tools/`" + `, damit dort später
# andere Arten danebenliegen können. Ohne diesen Eintrag bekommt kein
# Hase das Werkzeug zu sehen — und der generierte Prompt verweist ihn
# dann auch nicht darauf. Ein Briefkasten, den niemand leert, ist
# schlimmer als keiner; ein Wegweiser auf einen, den es nicht gibt,
# erst recht.
#
# Steht hier ein Raum, muss der input-Raum des Schmied-Auftrags derselbe
# sein plus ` + "`tools/`" + `. ` + "`hasenbau describe bau`" + ` prüft das.
requests: raeume/wuensche/
`

// Die Vorlagen der Sonder-Hasen liegen im Binary, nicht in beispiele/:
// ein frischer Bau soll den Baumeister haben, ohne dass jemand Dateien
// kopiert, und `hasenbau fix` kann sie nur zurückschreiben, wenn er sie
// bei sich trägt. Wer sie nicht will, stellt den Trigger auf manual
// oder leert den Auftrag — löschen hilft nicht, fix legt sie wieder an.
var (
	//go:embed vorlagen/auftraege/baumeister.md
	auftragBaumeister string
	//go:embed vorlagen/hasen/baumeister.md
	haseBaumeister string
	//go:embed vorlagen/auftraege/schmied.md
	auftragSchmied string
	//go:embed vorlagen/hasen/schmied.md
	haseSchmied string
	//go:embed vorlagen/plugin/hasenbau.js
	sandboxWaechter string
)

// gitIgnore: Der Bau versioniert Definitionen (Aufträge, Hasen, Gänge,
// Config), nicht Laufzeit-Material und nicht generierte Agenten. Das
// Bau-Plugin steht aus demselben Grund dabei: es ist generiert, nicht
// geschrieben (SchreibePlugin). Eigene Plugins daneben gehören dem Bau
// und bleiben versioniert — deshalb die eine Datei, nicht das
// Verzeichnis.
const gitIgnore = `state/
raeume/
.opencode-home/opencode/agents/
.opencode-home/opencode/plugin/hasenbau.js
`

// dirs sind die Verzeichnisse eines frischen Baus. raeume/ bleibt leer:
// Räume benennt der Auftrag, der Daemon legt fehlende an (§4).
var dirs = []string{
	".opencode-home/opencode/agents",
	".opencode-home/opencode/plugin",
	".opencode-home/opencode/skills",
	"auftraege",
	"gaenge",
	"hasen",
	"raeume",
	"state",
	// tools/ liegt neben gaenge/ und nicht unter raeume/: ein Werkzeug
	// ist kein Material, es fließt nicht durch den Bau. entwurf/ hat
	// dieselbe Bedeutung wie bei den Gängen — was dort liegt, hat ein
	// Sonder-Hase geschrieben und noch kein Mensch angesehen.
	ToolsDir,
	ToolsEntwurfDir,
}

// files sind die Dateien eines frischen Baus. Bestehende Dateien werden
// nie überschrieben — Init ist idempotent und nicht-destruktiv.
var files = map[string]string{
	"hasenbau.yaml":           hasenbauYAML,
	".gitignore":              gitIgnore,
	OpencodeConfig:            opencodeJSON,
	"auftraege/baumeister.md": auftragBaumeister,
	"hasen/baumeister.md":     haseBaumeister,
	"auftraege/schmied.md":    auftragSchmied,
	"hasen/schmied.md":        haseSchmied,
	// Das Bau-Plugin steht bewusst NICHT hier: es ist keine Vorlage,
	// sondern ein Artefakt, und SchreibePlugin hält es aktuell.
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
	if _, err := EnsurePlugin(root); err != nil {
		return created, err
	}
	// Das Plugin selbst: hier, damit ein frischer Bau vollständig ist,
	// ohne dass je ein Daemon lief. Gemeldet wird nur das Anlegen — eine
	// ersetzte Fassung ist keine Ergänzung, und wer sie sehen will,
	// bekommt sie beim Start (loadAndGenerate) und von `describe bau`.
	if erg, err := SchreibePlugin(root); err != nil {
		return created, err
	} else if erg == PluginAngelegt {
		created = append(created, PluginDatei)
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
