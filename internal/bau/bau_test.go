package bau

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Moritz-Dorn/Hasenbau/internal/supervisor"
)

// TestInitInFremdemRepoLegtEigenesAn: Ein Bau innerhalb eines fremden
// Git-Repos braucht trotzdem sein eigenes — sonst wäre die Projekt-ID
// (und damit die Permission-Isolation, §11.5) die des Parent-Repos.
func TestInitInFremdemRepoLegtEigenesAn(t *testing.T) {
	parent := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false", "commit", "-q", "--allow-empty", "-m", "parent"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", parent}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}

	root := filepath.Join(parent, "bau")
	if _, err := Init(root, "/opt/hasenbau"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, ".git")); err != nil {
		t.Errorf("Bau im fremden Repo bekam kein eigenes .git: %v", err)
	}
}

func TestInitLegtLayoutAn(t *testing.T) {
	root := filepath.Join(t.TempDir(), "neuer-bau")
	created, err := Init(root, "/opt/hasenbau")
	if err != nil {
		t.Fatal(err)
	}
	// +2: .git/ und das Bau-Plugin, das keine Vorlage mehr ist und
	// deshalb nicht in files steht (SchreibePlugin).
	if len(created) != len(dirs)+len(files)+2 {
		t.Errorf("created = %v", created)
	}
	for _, p := range []string{
		"hasenbau.yaml",
		".gitignore",
		".opencode-home/opencode/opencode.json",
		".opencode-home/opencode/agents",
		"auftraege", "gaenge", "hasen", "raeume", "state",
	} {
		if _, err := os.Stat(filepath.Join(root, p)); err != nil {
			t.Errorf("%s fehlt: %v", p, err)
		}
	}

	// Der Bau muss ein Git-Repo mit mindestens einem Commit sein —
	// sonst greifen die Raum-Permissions der Hasen nicht (§11.5).
	if err := exec.Command("git", "-C", root, "rev-parse", "--verify", "-q", "HEAD").Run(); err != nil {
		t.Errorf("Bau hat keinen Git-Commit: %v", err)
	}

	// Der frische Bau muss die Supervisor-Validierung bestehen —
	// init und Supervisor teilen dieselbe Layout-Konvention.
	if _, err := supervisor.New(supervisor.Config{BauDir: root}); err != nil {
		t.Errorf("frischer Bau besteht Supervisor-Validierung nicht: %v", err)
	}
}

// Ein frischer Bau muss den Rückkanal-Eintrag schon haben: ohne ihn
// bekommt kein Hase `hasenbau_summary` und `hasenbau_notiz`, und
// `describe bau` meldet den frischen Bau als PRÜFEN. Der Eintrag gehört
// außerdem in den Root-Commit — käme er erst beim ersten Daemon-Start
// dazu, wäre der Bau ab da schmutzig.
func TestInitTraegtRueckkanalEin(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "neuer-bau")
	// Ein Binary, das es gibt: die Diagnose prüft den Pfad.
	exe := filepath.Join(tmp, "hasenbau")
	if err := os.WriteFile(exe, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Init(root, exe); err != nil {
		t.Fatal(err)
	}

	if c := checkMCP(root); !c.OK {
		t.Errorf("frischer Bau, Rückkanal-Prüfung: %s — %s", c.Detail, c.Hint)
	}
	roh, err := os.ReadFile(filepath.Join(root, OpencodeConfig))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(roh, &m); err != nil {
		t.Fatal(err)
	}
	mcp, _ := m["mcp"].(map[string]any)
	eintrag, ok := mcp[MCPEintrag].(map[string]any)
	if !ok {
		t.Fatalf("kein mcp.%s in %s", MCPEintrag, roh)
	}
	befehl, _ := eintrag["command"].([]any)
	if len(befehl) != 4 || befehl[0] != exe || befehl[2] != root {
		t.Errorf("command = %v", befehl)
	}

	out, err := exec.Command("git", "-C", root, "status", "--porcelain").Output()
	if err != nil {
		t.Fatal(err)
	}
	if len(out) > 0 {
		t.Errorf("frischer Bau ist nicht sauber:\n%s", out)
	}
}

func TestInitIstIdempotentUndNichtDestruktiv(t *testing.T) {
	root := t.TempDir()
	if _, err := Init(root, "/opt/hasenbau"); err != nil {
		t.Fatal(err)
	}

	// Nutzer-Config verändern — ein zweites Init darf sie nicht anfassen.
	conf := filepath.Join(root, ".opencode-home", "opencode", "opencode.json")
	eigen := `{"plugin":[],"provider":{"scc":{}}}`
	if err := os.WriteFile(conf, []byte(eigen), 0o644); err != nil {
		t.Fatal(err)
	}

	created, err := Init(root, "/opt/hasenbau")
	if err != nil {
		t.Fatalf("zweites Init: %v", err)
	}
	if len(created) != 0 {
		t.Errorf("zweites Init legte an: %v", created)
	}
	raw, err := os.ReadFile(conf)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "scc") {
		t.Error("Init hat bestehende opencode.json überschrieben")
	}
}

func TestInitLehntDateiAlsRootAb(t *testing.T) {
	f := filepath.Join(t.TempDir(), "datei")
	if err := os.WriteFile(f, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Init(f, "/opt/hasenbau"); err == nil {
		t.Error("Init auf einer Datei muss fehlschlagen")
	}
}

// TestDiagnoseMeldetRequestsRaum: `describe bau` muss sagen, ob der Bau
// einen Wunsch-Raum hat. Ohne diese Angabe sieht ein Bau mit und ohne
// ihn identisch aus — obwohl der Unterschied ist, ob die Hasen
// `hasenbau_tool_request` überhaupt zu sehen bekommen (Hasenbau-2lq).
func TestDiagnoseMeldetRequestsRaum(t *testing.T) {
	suche := func(checks []Check) string {
		for _, c := range checks {
			if c.Name == ConfigFile {
				return c.Detail
			}
		}
		return ""
	}

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ConfigFile),
		[]byte("log_level: info\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if detail := suche(Diagnose(root)); !strings.Contains(detail, "no requests Raum") {
		t.Errorf("ohne requests-Raum meldet die Diagnose %q — der Hinweis fehlt", detail)
	}

	if err := os.WriteFile(filepath.Join(root, ConfigFile),
		[]byte("log_level: info\nrequests: raeume/wuensche/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if detail := suche(Diagnose(root)); !strings.Contains(detail, "requests: raeume/wuensche/") {
		t.Errorf("mit requests-Raum meldet die Diagnose %q — der Raum fehlt", detail)
	}
}
