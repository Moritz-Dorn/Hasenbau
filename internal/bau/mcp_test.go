package bau

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func leseConfig(t *testing.T, root string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, OpencodeConfig))
	if err != nil {
		t.Fatal(err)
	}
	var roh map[string]any
	if err := json.Unmarshal(b, &roh); err != nil {
		t.Fatal(err)
	}
	return roh
}

func TestMCPSicherstellen(t *testing.T) {
	root := t.TempDir()
	if _, err := Init(root); err != nil {
		t.Fatal(err)
	}

	update, err := EnsureMCP(root, "/opt/hasenbau")
	if err != nil {
		t.Fatal(err)
	}
	if !update.Written {
		t.Fatal("frischer Bau: nichts geschrieben")
	}
	if update.Previous != "" {
		t.Errorf("neuer Eintrag hat keinen Vorgänger, gemeldet: %q", update.Previous)
	}

	roh := leseConfig(t, root)
	eintrag, ok := roh["mcp"].(map[string]any)[MCPEintrag].(map[string]any)
	if !ok {
		t.Fatalf("kein mcp.%s in %v", MCPEintrag, roh)
	}
	if eintrag["type"] != "local" || eintrag["enabled"] != true {
		t.Errorf("Eintrag = %v", eintrag)
	}
	befehl, _ := eintrag["command"].([]any)
	if len(befehl) != 4 || befehl[0] != "/opt/hasenbau" || befehl[1] != "-bau" ||
		befehl[2] != root || befehl[3] != "mcp" {
		t.Errorf("command = %v", befehl)
	}
	// Der Rest der Bau-Config bleibt stehen (§3).
	if _, da := roh["plugin"]; !da {
		t.Error("plugin: [] ist verschwunden")
	}
	if roh["$schema"] == nil {
		t.Error("$schema ist verschwunden")
	}

	// Zweiter Aufruf mit demselben Binary ist ein No-op.
	update, err = EnsureMCP(root, "/opt/hasenbau")
	if err != nil {
		t.Fatal(err)
	}
	if update.Written {
		t.Error("zweiter Aufruf hat erneut geschrieben")
	}
}

// Der Kern von Hasenbau-2nq: Der Eintrag ist eine Selbstreferenz auf
// das Binary, das den Bau fährt — zeigt er woandershin, wird er
// korrigiert, und der alte Pfad kommt zurück, damit es im Log auffällt.
func TestMCPSicherstellenKorrigiertVeraltetesBinary(t *testing.T) {
	root := t.TempDir()
	if _, err := Init(root); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureMCP(root, "/tmp/wegwerf/hasenbau"); err != nil {
		t.Fatal(err)
	}

	update, err := EnsureMCP(root, "/opt/hasenbau")
	if err != nil {
		t.Fatal(err)
	}
	if !update.Written {
		t.Fatal("veralteter Pfad blieb stehen")
	}
	if update.Previous != "/tmp/wegwerf/hasenbau" {
		t.Errorf("Vorgänger = %q", update.Previous)
	}
	eintrag := leseConfig(t, root)["mcp"].(map[string]any)[MCPEintrag].(map[string]any)
	if befehl := eintrag["command"].([]any); befehl[0] != "/opt/hasenbau" {
		t.Errorf("command = %v", befehl)
	}
}

// Korrigiert wird der Binary-Pfad, sonst nichts: Zusatz-Argumente und
// eigene Felder überleben.
func TestMCPSicherstellenLaesstHandarbeitStehen(t *testing.T) {
	root := t.TempDir()
	if _, err := Init(root); err != nil {
		t.Fatal(err)
	}
	pfad := filepath.Join(root, OpencodeConfig)
	if err := os.WriteFile(pfad, []byte(`{
  "plugin": [],
  "mcp": {"hasenbau": {"type": "local", "enabled": false,
    "command": ["/eigener/pfad", "-bau", "/anderswo", "mcp"],
    "environment": {"HASENBAU_DEBUG": "1"}}}
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	update, err := EnsureMCP(root, "/opt/hasenbau")
	if err != nil {
		t.Fatal(err)
	}
	if !update.Written || update.Previous != "/eigener/pfad" {
		t.Fatalf("update = %+v", update)
	}
	eintrag := leseConfig(t, root)["mcp"].(map[string]any)[MCPEintrag].(map[string]any)
	befehl := eintrag["command"].([]any)
	if len(befehl) != 4 || befehl[0] != "/opt/hasenbau" || befehl[2] != "/anderswo" {
		t.Errorf("nur das Binary sollte sich ändern: command = %v", befehl)
	}
	if eintrag["enabled"] != false {
		t.Errorf("enabled wurde angefasst: %v", eintrag["enabled"])
	}
	if _, da := eintrag["environment"]; !da {
		t.Error("environment ist verschwunden")
	}
}

// Ein Eintrag ohne aufrufbaren Befehl ist keine Handarbeit, sondern
// kaputt — er entsteht kanonisch neu.
func TestMCPSicherstellenRepariertLeerenBefehl(t *testing.T) {
	root := t.TempDir()
	if _, err := Init(root); err != nil {
		t.Fatal(err)
	}
	pfad := filepath.Join(root, OpencodeConfig)
	if err := os.WriteFile(pfad, []byte(`{"mcp": {"hasenbau": {"type": "local"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	update, err := EnsureMCP(root, "/opt/hasenbau")
	if err != nil {
		t.Fatal(err)
	}
	if !update.Written {
		t.Fatal("kaputter Eintrag blieb stehen")
	}
	eintrag := leseConfig(t, root)["mcp"].(map[string]any)[MCPEintrag].(map[string]any)
	befehl, _ := eintrag["command"].([]any)
	if len(befehl) != 4 || befehl[0] != "/opt/hasenbau" || befehl[2] != root {
		t.Errorf("command = %v", befehl)
	}
}

// Fremde mcp-Server bleiben unangetastet.
func TestMCPSicherstellenNebenFremdenServern(t *testing.T) {
	root := t.TempDir()
	if _, err := Init(root); err != nil {
		t.Fatal(err)
	}
	pfad := filepath.Join(root, OpencodeConfig)
	if err := os.WriteFile(pfad, []byte(`{
  "mcp": {"fremd": {"type": "remote", "url": "https://example.invalid/mcp"}}
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := EnsureMCP(root, "/opt/hasenbau"); err != nil {
		t.Fatal(err)
	}
	mcp := leseConfig(t, root)["mcp"].(map[string]any)
	if _, da := mcp["fremd"]; !da {
		t.Error("fremder MCP-Server ist verschwunden")
	}
	if _, da := mcp[MCPEintrag]; !da {
		t.Error("Rückkanal fehlt")
	}
}

func TestMCPSicherstellenOhneConfig(t *testing.T) {
	if _, err := EnsureMCP(t.TempDir(), "/opt/hasenbau"); err == nil {
		t.Error("ohne Bau-Config kein Fehler")
	}
}
