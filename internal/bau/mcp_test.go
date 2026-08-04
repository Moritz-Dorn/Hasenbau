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

	geschrieben, err := MCPSicherstellen(root, "/opt/hasenbau")
	if err != nil {
		t.Fatal(err)
	}
	if !geschrieben {
		t.Fatal("frischer Bau: nichts geschrieben")
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

	// Zweiter Aufruf ist ein No-op.
	geschrieben, err = MCPSicherstellen(root, "/opt/hasenbau")
	if err != nil {
		t.Fatal(err)
	}
	if geschrieben {
		t.Error("zweiter Aufruf hat erneut geschrieben")
	}
}

// Wer den Eintrag von Hand anpasst, behält ihn — auch wenn das Binary
// inzwischen woanders liegt.
func TestMCPSicherstellenLaesstHandarbeitStehen(t *testing.T) {
	root := t.TempDir()
	if _, err := Init(root); err != nil {
		t.Fatal(err)
	}
	pfad := filepath.Join(root, OpencodeConfig)
	if err := os.WriteFile(pfad, []byte(`{
  "plugin": [],
  "mcp": {"hasenbau": {"type": "local", "command": ["/eigener/pfad", "mcp"]}}
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	geschrieben, err := MCPSicherstellen(root, "/opt/hasenbau")
	if err != nil {
		t.Fatal(err)
	}
	if geschrieben {
		t.Fatal("vorhandener Eintrag wurde überschrieben")
	}
	eintrag := leseConfig(t, root)["mcp"].(map[string]any)[MCPEintrag].(map[string]any)
	if befehl := eintrag["command"].([]any); befehl[0] != "/eigener/pfad" {
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

	if _, err := MCPSicherstellen(root, "/opt/hasenbau"); err != nil {
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
	if _, err := MCPSicherstellen(t.TempDir(), "/opt/hasenbau"); err == nil {
		t.Error("ohne Bau-Config kein Fehler")
	}
}
