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
	const name = "Tools"
	alle, err := LadeTools(root)
	if err != nil {
		return Check{Name: name, Detail: err.Error(),
			Hint: "while the manifest is broken, `hasenbau daemon` generates no agents"}
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
	detail := fmt.Sprintf("%d released, %d in draft", frei, entwuerfe)

	// Das alte Layout (Hasenbau-lnk): Werkzeuge lagen als Dateipaar
	// direkt unter tools/ bzw. tools/entwurf/. Ein Bau von damals wird
	// hier sonst STILL leer — LadeTools findet nichts, `get tools` zeigt
	// eine leere Tabelle, und nichts sagt, warum. Das ist genau der
	// Fehler, den dieser Bau sonst überall vermeidet.
	if alt := altesToolLayout(root); len(alt) > 0 {
		return Check{Name: name, Detail: detail,
			Hint: "old layout found: " + strings.Join(alt, ", ") + "\n" +
				"                       A tool is a folder now: tools/drafts/<name>/ with tool.json and the script,\n" +
				"                       released ones in tools/released/<name>/. Move them, then rename the manifest"}
	}

	// Ein freigegebenes Werkzeug, das nicht mehr einsatzbereit ist, ist
	// der stillste Fall von allen: es liegt in tools/, `get tools`
	// führt es, ein Auftrag nennt es womöglich — und trotzdem bekommt
	// es kein Hase, weil das Plugin es beim Start aussortiert. Wer das
	// nicht sucht, findet es nie.
	if len(lahm) > 0 {
		return Check{Name: name, Detail: detail,
			Hint: "released but not ready for use: " + strings.Join(lahm, ", ") + "\n" +
				"                       The plugin does not register them. `hasenbau tool review <name>`, then test again"}
	}

	// Ohne bwrap registriert das Plugin gar kein Werkzeug (Hasenbau-9w6):
	// im Betrieb läuft ein Werkzeug im Server-Prozess, und ohne
	// Sandkasten hätte es mehr Rechte als der Hase, der es ruft. Das ist
	// fail-closed und deshalb richtig — aber es steht nur im Server-Log,
	// und dort sucht niemand, der sich wundert, wo sein Werkzeug ist.
	if frei > 0 {
		if _, err := exec.LookPath("bwrap"); err != nil {
			return Check{Name: name, Detail: detail,
				Hint: "bwrap is missing — the plugin therefore registers NO tool.\n" +
					"                       A tool runs in the server process; without a sandbox it could do more\n" +
					"                       than the Hase calling it. Install it (bubblewrap) or run without tools"}
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
			Hint: "A draft is code a model wrote and nobody read.\n" +
				"                       READ IT FIRST. Then `hasenbau tool test <name> …` — that command RUNS it,\n" +
				"                       with your rights; it finds mistakes, not intentions. Finally move it to " + ToolsDir + "/"}
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
		return fmt.Sprintf("Auftrag %s watches %s, but the requests land in %s — the Schmied never gets them",
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
		return Check{Name: "Layout", Detail: "missing: " + strings.Join(fehlt, ", "),
			Hint: "`hasenbau init " + root + "` legt Fehlendes nach, ohne Vorhandenes anzufassen"}
	}
	return Check{Name: "Layout", OK: true, Detail: fmt.Sprintf("%d directories, %d files", len(dirs), len(files))}
}

// checkGit ist die wichtigste Prüfung und die unauffälligste: ohne
// Commit hat der Bau bei opencode keine eigene Projekt-ID, und die
// Permissions der Hasen ankern dann woanders (§11.5).
func checkGit(root string) Check {
	if _, err := os.Stat(filepath.Join(root, ".git")); errors.Is(err, fs.ErrNotExist) {
		return Check{Name: "Git repo", Detail: "no .git",
			Hint: "`git init` in the Bau, then commit — without its own project ID the Raum permissions do not take effect (PLAN.md §11.5)"}
	}
	if _, err := exec.LookPath("git"); err != nil {
		return Check{Name: "Git repo", Detail: "git not in PATH — cannot check"}
	}
	cmd := exec.Command("git", "-C", root, "rev-parse", "--verify", "-q", "HEAD")
	roh, err := cmd.Output()
	if err != nil {
		return Check{Name: "Git repo", Detail: "repo without a commit",
			Hint: "`git add -A && git commit` — only the root commit gives the Bau its project ID (PLAN.md §11.5)"}
	}
	commit := strings.TrimSpace(string(roh))
	if len(commit) > 12 {
		commit = commit[:12]
	}
	return Check{Name: "Git repo", OK: true, Detail: "HEAD " + commit}
}

func checkOpencodeConfig(root string) Check {
	pfad := filepath.Join(root, OpencodeConfig)
	roh, err := os.ReadFile(pfad)
	if err != nil {
		return Check{Name: "Bau config", Detail: OpencodeConfig + " missing",
			Hint: "`hasenbau init` creates it — without it the server would run with your everyday config (PLAN.md §3)"}
	}
	var m map[string]any
	if err := json.NewDecoder(bytes.NewReader(roh)).Decode(&m); err != nil {
		return Check{Name: "Bau config", Detail: "unreadable: " + err.Error(),
			Hint: "fix the JSON — opencode will not start with this"}
	}
	var teile []string
	if p, ok := m["plugin"].([]any); ok && len(p) == 0 {
		teile = append(teile, "plugin: [] (none inherited)")
	}
	if e, ok := m["enabled_providers"].([]any); ok {
		teile = append(teile, fmt.Sprintf("%d aktive Provider", len(e)))
	}
	if len(teile) == 0 {
		teile = append(teile, "readable")
	}
	return Check{Name: "Bau config", OK: true, Detail: strings.Join(teile, ", ")}
}

// checkMCP ist der Befund aus Hasenbau-2nq: der Eintrag zeigte fünf
// Tage lang auf ein Wegwerf-Binary unter /tmp, und niemandem fiel es
// auf, weil ein Lauf ohne Rückkanal normal aussieht.
func checkMCP(root string) Check {
	pfad := filepath.Join(root, OpencodeConfig)
	roh, err := os.ReadFile(pfad)
	if err != nil {
		return Check{Name: "Back channel", Detail: "no Bau config"}
	}
	var m map[string]any
	if err := json.Unmarshal(roh, &m); err != nil {
		return Check{Name: "Back channel", Detail: "Bau config unreadable"}
	}
	mcp, _ := m["mcp"].(map[string]any)
	eintrag, ok := mcp[MCPEintrag].(map[string]any)
	if !ok {
		return Check{Name: "Back channel", Detail: "no mcp." + MCPEintrag,
			Hint: "the next daemon or Lauf start writes it"}
	}
	if an, da := eintrag["enabled"].(bool); da && !an {
		return Check{Name: "Back channel", Detail: "enabled: false",
			Hint: "this way no Hase gets hasenbau_summary and hasenbau_notiz"}
	}
	befehl, _ := eintrag["command"].([]any)
	if len(befehl) == 0 {
		return Check{Name: "Back channel", Detail: "entry without command",
			Hint: "the next start rewrites it canonically"}
	}
	binary, _ := befehl[0].(string)
	if _, err := os.Stat(binary); err != nil {
		return Check{Name: "Back channel", Detail: "points at " + binary + " — that does not exist (any more)",
			Hint: "the next daemon or Lauf start corrects the path to the running binary"}
	}
	return Check{Name: "Back channel", OK: true, Detail: binary}
}

// checkWaechter prüft den Sandbox-Wächter (Hasenbau-d2p). Er MUSS
// geprüft werden, und zwar aus demselben Grund, aus dem es ihn gibt:
// sein Schweigen wird als Nachweis gelesen („kein Hase hat es
// versucht"). Ein Wächter, der gar nicht geladen ist, schweigt ebenso —
// und dann heißt Stille nichts mehr. Dieselbe Falle wie beim Rückkanal,
// dessen Eintrag fünf Tage lang auf ein verschwundenes Binary zeigte
// (Hasenbau-2nq/08u).
func checkWaechter(root string) Check {
	const name = "Sandbox guard"
	if _, err := os.Stat(filepath.Join(root, PluginDatei)); err != nil {
		return Check{Name: name, Detail: PluginDatei + " missing",
			Hint: "`hasenbau fix` creates it again"}
	}
	// Veraltet ist der leisere, aber gefährlichere Fall: die Datei liegt
	// da, der Wächter meldet auch etwas — nur fehlen ihr die Zusagen, die
	// seither dazugekommen sind (Review-Gate, Werkzeug-Sandkasten). Ein
	// Bau von 2026-07 trug 72 Zeilen gegen 359 und sagte nichts dazu
	// (Hasenbau-uei).
	if aktuell, err := PluginAktuell(root); err == nil && !aktuell {
		return Check{Name: name, Detail: "outdated — not the version of this binary",
			Hint: "`hasenbau fix` or the next daemon start replaces it"}
	}
	eingetragen, err := PluginEingetragen(root)
	if err != nil {
		return Check{Name: name, Detail: "Bau config unreadable"}
	}
	if !eingetragen {
		return Check{Name: name, Detail: "present, but not listed in the plugin: block",
			Hint: "opencode will not load it like this — `hasenbau fix` adds it"}
	}
	detail := "active, sandbox: deny"
	conf, err := LoadConfig(root)
	if err == nil && conf.Sandbox == SandboxWarn {
		detail = "active, sandbox: warn (calls are reported, not refused)"
	}
	// Ein Bau, der vor Hasenbau-uei angelegt wurde, hat die Datei in
	// seinem Git — und eine .gitignore-Zeile holt eine getrackte Datei
	// nicht mehr ein. Seither wird sie bei jedem Start neu geschrieben,
	// steht also nach jedem Upgrade als geänderte Datei da, ohne dass
	// jemand sie angefasst hätte.
	//
	// Gemeldet und nicht repariert: `git rm --cached` griffe in die
	// Historie eines fremden Repos, und das tut der Hasenbau nicht
	// (Hasenbau-610). Der Check bleibt deshalb OK — es ist Rauschen,
	// kein Defekt.
	if pluginGetrackt(root) {
		return Check{Name: name, OK: true, Detail: detail,
			Hint: "generated, but tracked in your Bau's git — `git rm --cached " + PluginDatei + "`\n" +
				"                       otherwise it shows up as a change after every upgrade"}
	}
	return Check{Name: name, OK: true, Detail: detail}
}

// pluginGetrackt sagt, ob die generierte Datei im Git des Baus liegt.
// Ohne git im PATH und ohne Repo ist die Antwort nein — dann gibt es
// nichts zu melden, und checkGit sagt ohnehin schon, was fehlt.
func pluginGetrackt(root string) bool {
	if _, err := exec.LookPath("git"); err != nil {
		return false
	}
	cmd := exec.Command("git", "-C", root, "ls-files", "--error-unmatch", filepath.ToSlash(PluginDatei))
	return cmd.Run() == nil
}

func checkHasenbauYAML(root string) Check {
	if _, err := os.Stat(filepath.Join(root, ConfigFile)); err != nil {
		return Check{Name: ConfigFile, Detail: "missing — the defaults apply"}
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
		teile = append(teile, "no requests Raum (Hasen cannot ask for a tool)")
	}
	if conf.Baumeister == "" {
		return Check{Name: ConfigFile, OK: true, Detail: strings.Join(teile, ", ") + ", no baumeister",
			Hint: "without `baumeister:` the command `hasenbau baumeister` does not run"}
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

// altesToolLayout findet Reste der Fassung vor Hasenbau-lnk: Manifeste,
// die als lose Datei unter tools/ oder tools/entwurf/ liegen statt in
// einem Werkzeug-Ordner. Zurück kommen die Pfade, die es zu verschieben
// gilt — nichts wird angefasst, denn was ein Werkzeug ist, entscheidet
// hier niemand automatisch.
func altesToolLayout(root string) []string {
	var gefunden []string
	for _, dir := range []string{"tools", "tools/entwurf"} {
		treffer, err := filepath.Glob(filepath.Join(root, dir, "*.json"))
		if err != nil {
			continue
		}
		for _, pfad := range treffer {
			gefunden = append(gefunden, filepath.Join(dir, filepath.Base(pfad)))
		}
	}
	sort.Strings(gefunden)
	return gefunden
}
