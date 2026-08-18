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

	"github.com/Moritz-Dorn/Hasenbau/internal/auftrag"
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
		checkWaechter(root),
		checkHasenbauYAML(root),
		checkWerkzeuge(root),
	}
}

// checkWerkzeuge prüft die zwei Stellen, an denen die Werkzeug-Kette
// still reißt (Hasenbau-hcs).
//
// Erstens ein kaputtes Manifest: die Go-Seite verweigert dann das
// Generieren der Agenten, das Plugin überspringt nur dieses eine
// Werkzeug — der Bau läuft also weiter und ein Hase bekommt sein
// Werkzeug nicht. Zweitens die Kopplung zwischen `requests:` und dem
// input-Raum des Schmied-Auftrags: passen sie nicht zusammen, wartet
// der Schmied an einem Briefkasten, in den niemand einwirft. Beides
// sieht man dem Bau nicht an.
func checkWerkzeuge(root string) Check {
	const name = "Werkzeuge"
	alle, err := LadeTools(root)
	if err != nil {
		return Check{Name: name, Detail: err.Error(),
			Hint: "solange das Manifest kaputt ist, generiert `hasenbau daemon` keine Agenten"}
	}
	var frei, entwuerfe int
	var lahm []string
	for _, t := range alle {
		if t.Entwurf {
			entwuerfe++
			continue
		}
		frei++
		if !t.Einsatzbereit() {
			lahm = append(lahm, fmt.Sprintf("%s (%s)", t.Name, t.Zustand))
		}
	}
	detail := fmt.Sprintf("%d freigegeben, %d im Entwurf", frei, entwuerfe)

	// Ein freigegebenes Werkzeug, das nicht mehr einsatzbereit ist, ist
	// der stillste Fall von allen: es liegt in tools/, `get tools`
	// führt es, ein Auftrag nennt es womöglich — und trotzdem bekommt
	// es kein Hase, weil das Plugin es beim Start aussortiert. Wer das
	// nicht sucht, findet es nie.
	if len(lahm) > 0 {
		return Check{Name: name, Detail: detail,
			Hint: "freigegeben, aber nicht einsatzbereit: " + strings.Join(lahm, ", ") + "\n" +
				"                       Das Plugin registriert sie nicht. `hasenbau tool review <name>`, dann erneut testen"}
	}

	// Ohne bwrap registriert das Plugin gar kein Werkzeug (Hasenbau-9w6):
	// im Betrieb läuft ein Werkzeug im Server-Prozess, und ohne
	// Sandkasten hätte es mehr Rechte als der Hase, der es ruft. Das ist
	// fail-closed und deshalb richtig — aber es steht nur im Server-Log,
	// und dort sucht niemand, der sich wundert, wo sein Werkzeug ist.
	if frei > 0 {
		if _, err := exec.LookPath("bwrap"); err != nil {
			return Check{Name: name, Detail: detail,
				Hint: "bwrap fehlt — das Plugin registriert deshalb KEIN Werkzeug.\n" +
					"                       Ein Werkzeug läuft im Server-Prozess; ohne Sandkasten dürfte es mehr\n" +
					"                       als der Hase, der es ruft. Installieren (bubblewrap) oder ohne Werkzeuge fahren"}
		}
	}

	// Die Kopplung: der Schmied beobachtet einen input-Raum, die Hasen
	// werfen in den `requests:`-Raum ein. Nichts hält die beiden
	// synchron, und ein Schmied, der am falschen Briefkasten wartet,
	// sieht aus wie einer, der nie etwas zu tun bekommt.
	if hinweis := schmiedEingang(root); hinweis != "" {
		return Check{Name: name, Detail: detail, Hint: hinweis}
	}
	if entwuerfe > 0 {
		return Check{Name: name, OK: true, Detail: detail,
			Hint: "Ein Entwurf ist Code, den ein Modell geschrieben und niemand gelesen hat.\n" +
				"                       ERST LESEN. Dann `hasenbau tool test <name> …` — der Befehl FÜHRT AUS,\n" +
				"                       mit deinen Rechten; er findet Fehler, keine Absichten. Zuletzt nach " + ToolsDir + "/ verschieben"}
	}
	return Check{Name: name, OK: true, Detail: detail}
}

// schmiedEingang liefert einen Hinweis, wenn Wunsch-Raum und
// Schmied-Eingang auseinanderlaufen — sonst den leeren String. Kein
// Schmied-Auftrag ist kein Fehler: nicht jeder Bau will einen.
func schmiedEingang(root string) string {
	conf, err := LoadConfig(root)
	if err != nil || conf.Requests == "" {
		return ""
	}
	auftraege, err := auftrag.Load(root)
	if err != nil {
		return "" // Ladefehler meldet die CLI ohnehin, und zwar genauer
	}
	soll := filepath.Join(conf.Requests, "tools") + "/"
	for _, a := range auftraege {
		if a.Hase != "schmied" {
			continue
		}
		ist := a.Raeume["input"]
		if ist == "" || filepath.Clean(ist) == filepath.Clean(soll) {
			return ""
		}
		return fmt.Sprintf("Auftrag %s beobachtet %s, die Wünsche landen aber in %s — der Schmied bekommt sie nie",
			a.Name, ist, soll)
	}
	return ""
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

// checkWaechter prüft den Sandbox-Wächter (Hasenbau-d2p). Er MUSS
// geprüft werden, und zwar aus demselben Grund, aus dem es ihn gibt:
// sein Schweigen wird als Nachweis gelesen („kein Hase hat es
// versucht"). Ein Wächter, der gar nicht geladen ist, schweigt ebenso —
// und dann heißt Stille nichts mehr. Dieselbe Falle wie beim Rückkanal,
// dessen Eintrag fünf Tage lang auf ein verschwundenes Binary zeigte
// (Hasenbau-2nq/08u).
func checkWaechter(root string) Check {
	const name = "Sandbox-Wächter"
	if _, err := os.Stat(filepath.Join(root, PluginDatei)); err != nil {
		return Check{Name: name, Detail: PluginDatei + " fehlt",
			Hint: "`hasenbau fix` legt ihn wieder an"}
	}
	// Veraltet ist der leisere, aber gefährlichere Fall: die Datei liegt
	// da, der Wächter meldet auch etwas — nur fehlen ihr die Zusagen, die
	// seither dazugekommen sind (Review-Gate, Werkzeug-Sandkasten). Ein
	// Bau von 2026-07 trug 72 Zeilen gegen 359 und sagte nichts dazu
	// (Hasenbau-uei).
	if aktuell, err := PluginAktuell(root); err == nil && !aktuell {
		return Check{Name: name, Detail: "veraltet — nicht die Fassung dieses Binaries",
			Hint: "`hasenbau fix` oder der nächste Daemon-Start ersetzt ihn"}
	}
	eingetragen, err := PluginEingetragen(root)
	if err != nil {
		return Check{Name: name, Detail: "Bau-Config unlesbar"}
	}
	if !eingetragen {
		return Check{Name: name, Detail: "liegt da, steht aber nicht im plugin:-Block",
			Hint: "so lädt opencode ihn nicht — `hasenbau fix` trägt ihn ein"}
	}
	conf, err := LoadConfig(root)
	if err == nil && conf.Sandbox == SandboxWarn {
		return Check{Name: name, OK: true, Detail: "aktiv, sandbox: warn (Aufrufe werden gemeldet, nicht abgewiesen)"}
	}
	return Check{Name: name, OK: true, Detail: "aktiv, sandbox: deny"}
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
	if conf.Throttle.An() {
		teile = append(teile, fmt.Sprintf("bau-deckel: %d je %s", conf.Throttle.Max, conf.Throttle.Per))
	}
	// Der Wunsch-Raum entscheidet, ob die Hasen `hasenbau_tool_request`
	// überhaupt zu sehen bekommen — und ob der generierte Prompt sie
	// darauf verweist. Ohne diese Zeile sieht ein Bau mit und ohne ihn
	// in der Diagnose identisch aus (Hasenbau-2lq).
	if conf.Requests != "" {
		teile = append(teile, "requests: "+conf.Requests)
	} else {
		teile = append(teile, "kein requests-Raum (Hasen können kein Werkzeug anfordern)")
	}
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
