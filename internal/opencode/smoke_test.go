package opencode

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sdk "github.com/sst/opencode-sdk-go"

	"github.com/Moritz-Dorn/Hasenbau/internal/supervisor"
)

// TestSmokePromptRoundtrip ist der Phase-0-Smoke-Test (Hasenbau-7ya):
// Supervisor hoch, Session anlegen, Prompt schicken, Antwort holen.
// Kostet einen echten LLM-Call und braucht Provider-Credentials
// (auth.json wird via XDG_DATA_HOME geteilt, §3) — deshalb doppelt
// gegated: HASENBAU_SMOKE=1 und HASENBAU_SMOKE_MODEL=provider/model.
func TestSmokePromptRoundtrip(t *testing.T) {
	if os.Getenv("HASENBAU_SMOKE") != "1" {
		t.Skip("kostet einen LLM-Call — mit HASENBAU_SMOKE=1 aktivieren")
	}
	if _, err := exec.LookPath("opencode"); err != nil {
		t.Skip("opencode nicht im PATH")
	}
	model, ok := SplitModel(os.Getenv("HASENBAU_SMOKE_MODEL"))
	if !ok {
		t.Skip("HASENBAU_SMOKE_MODEL=provider/model setzen — die isolierte Config hat bewusst kein Default-Modell")
	}

	bau := t.TempDir()
	confDir := filepath.Join(bau, ".opencode-home", "opencode")
	if err := os.MkdirAll(confDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Befund Hasenbau-7ya: auth.json teilt nur Credentials. Custom
	// Provider (wie "scc") sind Config, nicht Auth — ohne ihre Definition
	// antwortet der Server mit 500. Für den Smoke-Test übernehmen wir
	// die Provider-Definition explizit aus der Alltags-Config; ein
	// echter Bau versioniert sie in seiner eigenen opencode.json.
	conf := map[string]any{
		"$schema": "https://opencode.ai/config.json",
		"plugin":  []string{},
	}
	if home, err := os.UserConfigDir(); err == nil {
		if raw, err := os.ReadFile(filepath.Join(home, "opencode", "opencode.json")); err == nil {
			var user struct {
				Provider         map[string]any `json:"provider"`
				EnabledProviders []string       `json:"enabled_providers"`
			}
			if json.Unmarshal(raw, &user) == nil && user.Provider != nil {
				conf["provider"] = user.Provider
				conf["enabled_providers"] = user.EnabledProviders
			}
		}
	}
	raw, err := json.Marshal(conf)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(confDir, "opencode.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}

	sup, err := supervisor.New(supervisor.Config{BauDir: bau, Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if err := sup.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer sup.Stop()

	client := New(sup.BaseURL())
	sess, err := client.Session.New(ctx, sdk.SessionNewParams{
		Title: sdk.F("hasenbau-smoke"),
	})
	if err != nil {
		t.Fatalf("Session anlegen: %v", err)
	}
	t.Logf("Session %s", sess.ID)

	res, err := client.Session.Prompt(ctx, sess.ID, sdk.SessionPromptParams{
		Parts: sdk.F([]sdk.SessionPromptParamsPartUnion{
			TextPart("Antworte mit genau einem Wort: PONG"),
		}),
		Model: sdk.F(model),
	})
	if err != nil {
		t.Fatalf("Prompt: %v", err)
	}

	answer := AnswerText(res.Parts)
	t.Logf("Antwort: %q — Modell %s/%s, Tokens in/out %.0f/%.0f, Kosten $%.6f",
		answer, res.Info.ProviderID, res.Info.ModelID,
		res.Info.Tokens.Input, res.Info.Tokens.Output, res.Info.Cost)

	if res.Info.Role != sdk.AssistantMessageRoleAssistant {
		t.Errorf("unerwartete Rolle %q", res.Info.Role)
	}
	if !strings.Contains(strings.ToUpper(answer), "PONG") {
		t.Errorf("Antwort enthält kein PONG: %q", answer)
	}
}

func TestSplitModel(t *testing.T) {
	m, ok := SplitModel("scc/kit.glm-5.2-753b")
	if !ok || m.ProviderID.Value != "scc" || m.ModelID.Value != "kit.glm-5.2-753b" {
		t.Errorf("SplitModel: %+v ok=%v", m, ok)
	}
	if _, ok := SplitModel("ohne-slash"); ok {
		t.Error("SplitModel ohne Slash muss fehlschlagen")
	}
	if _, ok := SplitModel("provider/"); ok {
		t.Error("SplitModel mit leerem Modell muss fehlschlagen")
	}
}
