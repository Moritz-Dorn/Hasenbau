package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Moritz-Dorn/Hasenbau/internal/bau"
)

// antwort ist die Form, die die KI-Toolbox liefert (Befund 2026-08-04):
// {"data":[…]} mit id, name, connection_type und info.meta.description.
const antwort = `{"data":[
  {"id":"kit.glm-5.2","name":"GLM 5.2","connection_type":"local",
   "info":{"meta":{"description":"Grosses Modell\nHost: KIT"}}},
  {"id":"azure.gpt-5","name":"GPT-5","connection_type":"external"},
  {"id":"ohne-namen"}
]}`

func testServer(t *testing.T, body string, status int) (*httptest.Server, *string) {
	t.Helper()
	var gesehen string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gesehen = r.Header.Get("Authorization")
		if r.URL.Path != "/models" {
			t.Errorf("Pfad %q, erwartet /models", r.URL.Path)
		}
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	t.Cleanup(s.Close)
	return s, &gesehen
}

func TestHoleParstUndSortiert(t *testing.T) {
	s, auth := testServer(t, antwort, 200)

	// Trailing Slash in der baseURL darf keinen Doppel-Slash geben.
	modelle, err := Fetch(context.Background(), s.URL+"/", "geheim")
	if err != nil {
		t.Fatal(err)
	}
	if *auth != "Bearer geheim" {
		t.Errorf("Authorization %q", *auth)
	}
	if len(modelle) != 3 {
		t.Fatalf("%d Modelle, erwartet 3", len(modelle))
	}
	if modelle[0].ID != "azure.gpt-5" {
		t.Errorf("nicht nach ID sortiert: %v", modelle)
	}
	glm := modelle[1]
	if glm.Name != "GLM 5.2" || glm.Connection != "local" {
		t.Errorf("Felder falsch: %+v", glm)
	}
	if glm.Note != "Grosses Modell" {
		t.Errorf("Notiz ist nicht die erste Zeile: %q", glm.Note)
	}
	// Ohne name fällt der Anzeigename auf die ID zurück.
	if modelle[2].Name != "ohne-namen" {
		t.Errorf("Fallback auf ID fehlt: %+v", modelle[2])
	}
}

func TestHoleAkzeptiertNackteListe(t *testing.T) {
	s, _ := testServer(t, `[{"id":"a"},{"id":"b"}]`, 200)
	modelle, err := Fetch(context.Background(), s.URL, "k")
	if err != nil {
		t.Fatal(err)
	}
	if len(modelle) != 2 {
		t.Errorf("%d Modelle, erwartet 2", len(modelle))
	}
}

func TestHoleFehlerpfade(t *testing.T) {
	// HTTP-Error: Status und Body-Auszug gehören in die Meldung, sonst
	// rätselt der Nutzer über einen abgelaufenen Key.
	s, _ := testServer(t, `{"detail":"Not authenticated"}`, 401)
	_, err := Fetch(context.Background(), s.URL, "falsch")
	if err == nil || !strings.Contains(err.Error(), "401") || !strings.Contains(err.Error(), "Not authenticated") {
		t.Errorf("unbrauchbarer Error: %v", err)
	}

	// Leere Liste ist kein stiller Erfolg — sonst löscht der Merge alles.
	s2, _ := testServer(t, `{"data":[]}`, 200)
	if _, err := Fetch(context.Background(), s2.URL, "k"); err == nil {
		t.Error("leere Modell-Liste muss ein Fehler sein")
	}

	// Müll statt JSON.
	s3, _ := testServer(t, `<html>proxy</html>`, 200)
	if _, err := Fetch(context.Background(), s3.URL, "k"); err == nil {
		t.Error("unparsbare Antwort muss ein Fehler sein")
	}
}

// schreibeConfig legt eine Bau-Config an, ohne bau.Init (das würde ein
// Git-Repo anlegen — hier geht es nur um die JSON).
func schreibeConfig(t *testing.T, inhalt string) string {
	t.Helper()
	root := t.TempDir()
	pfad := filepath.Join(root, bau.OpencodeConfig)
	if err := os.MkdirAll(filepath.Dir(pfad), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pfad, []byte(inhalt), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

const gerüst = `{
  "$schema": "https://opencode.ai/config.json",
  "plugin": [],
  "instructions": ["MARKER"],
  "provider": {
    "scc": {
      "npm": "@ai-sdk/openai-compatible",
      "name": "SCC KI Toolbox",
      "options": {"baseURL": "https://beispiel.invalid/api/v1", "modify_params": true},
      "models": {
        "kit.glm-5.2": {"name": "Alter Name", "limit": {"context": 128000}},
        "azure.o3": {"name": "o3"}
      }
    }
  }
}
`

func TestMergeSchreibtNurModelleUndEnabled(t *testing.T) {
	root := schreibeConfig(t, gerüst)
	conf, err := LoadConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	url, err := conf.BaseURL("scc")
	if err != nil {
		t.Fatal(err)
	}
	if url != "https://beispiel.invalid/api/v1" {
		t.Errorf("baseURL %q", url)
	}

	ae := conf.Merge("scc", []Model{
		{ID: "kit.glm-5.2", Name: "GLM 5.2", Connection: "local"},
		{ID: "kit.neu", Name: "Neu"},
	})
	if len(ae.Neu) != 1 || ae.Neu[0].ID != "kit.neu" {
		t.Errorf("Neu falsch: %+v", ae.Neu)
	}
	if len(ae.Weg) != 1 || ae.Weg[0] != "azure.o3" {
		t.Errorf("Weg falsch: %+v", ae.Weg)
	}
	if len(ae.Umbenannt) != 1 || ae.Umbenannt[0].Alt != "Alter Name" {
		t.Errorf("Umbenannt falsch: %+v", ae.Umbenannt)
	}
	if !ae.EnabledErgaenzt {
		t.Error("enabled_providers muss ergänzt werden")
	}
	if ae.Empty() {
		t.Error("Change darf nicht leer sein")
	}

	if err := conf.Write(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, bau.OpencodeConfig))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Schema           string   `json:"$schema"`
		Plugin           []string `json:"plugin"`
		Instructions     []string `json:"instructions"`
		EnabledProviders []string `json:"enabled_providers"`
		Provider         map[string]struct {
			NPM     string `json:"npm"`
			Name    string `json:"name"`
			Options struct {
				BaseURL      string `json:"baseURL"`
				ModifyParams bool   `json:"modify_params"`
			} `json:"options"`
			Models map[string]struct {
				Name  string `json:"name"`
				Limit struct {
					Context int `json:"context"`
				} `json:"limit"`
			} `json:"models"`
		} `json:"provider"`
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}

	// Der Rest der Config bleibt unangetastet (Akzeptanzkriterium).
	if got.Schema == "" || got.Plugin == nil || len(got.Instructions) != 1 || got.Instructions[0] != "MARKER" {
		t.Errorf("Rest der Config verändert: %s", b)
	}
	scc := got.Provider["scc"]
	if scc.NPM == "" || scc.Name == "" || scc.Options.BaseURL == "" || !scc.Options.ModifyParams {
		t.Errorf("Gerüst verändert: %s", b)
	}
	if len(got.EnabledProviders) != 1 || got.EnabledProviders[0] != "scc" {
		t.Errorf("enabled_providers falsch: %v", got.EnabledProviders)
	}

	// models: gespiegelt — Zusatzfelder bestehender Einträge überleben.
	if len(scc.Models) != 2 {
		t.Errorf("%d Modelle, erwartet 2: %s", len(scc.Models), b)
	}
	if _, weg := scc.Models["azure.o3"]; weg {
		t.Error("azure.o3 hätte rausfallen müssen")
	}
	glm := scc.Models["kit.glm-5.2"]
	if glm.Name != "GLM 5.2" {
		t.Errorf("name nicht aktualisiert: %+v", glm)
	}
	if glm.Limit.Context != 128000 {
		t.Errorf("Zusatzfeld limit verloren: %+v", glm)
	}
	// connection_type ist Diff-Beiwerk, nicht Config.
	if strings.Contains(string(b), "connection_type") {
		t.Errorf("connection_type gehört nicht in die Config: %s", b)
	}
}

func TestMergeIdempotent(t *testing.T) {
	root := schreibeConfig(t, gerüst)
	modelle := []Model{
		{ID: "kit.glm-5.2", Name: "Alter Name"},
		{ID: "azure.o3", Name: "o3"},
	}
	conf, err := LoadConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	conf.Merge("scc", modelle)
	if err := conf.Write(); err != nil {
		t.Fatal(err)
	}

	// Zweiter Lauf gegen dieselbe Liste: nichts zu tun.
	conf2, err := LoadConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	if ae := conf2.Merge("scc", modelle); !ae.Empty() {
		t.Errorf("zweiter Merge nicht leer: %s", ae.Report())
	}
}

func TestBaseURLFehlerpfade(t *testing.T) {
	// Unbekannter Provider: das Gerüst gehört handgepflegt in die Config.
	root := schreibeConfig(t, gerüst)
	conf, err := LoadConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conf.BaseURL("anthropic"); err == nil || !strings.Contains(err.Error(), "scaffold") {
		t.Errorf("unbrauchbarer Error: %v", err)
	}

	// Provider ohne options.baseURL: verständlicher Fehler statt Absturz.
	root2 := schreibeConfig(t, `{"provider":{"scc":{"npm":"x"}}}`)
	conf2, err := LoadConfig(root2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conf2.BaseURL("scc"); err == nil || !strings.Contains(err.Error(), "baseURL") {
		t.Errorf("unbrauchbarer Error: %v", err)
	}

	// Bau ohne Config.
	if _, err := LoadConfig(t.TempDir()); err == nil || !strings.Contains(err.Error(), "hasenbau init") {
		t.Errorf("unbrauchbarer Error: %v", err)
	}
}

func TestSchluesselAusGeteilterAuthJSON(t *testing.T) {
	daten := t.TempDir()
	t.Setenv("XDG_DATA_HOME", daten)
	if err := os.MkdirAll(filepath.Join(daten, "opencode"), 0o755); err != nil {
		t.Fatal(err)
	}
	auth := `{"scc":{"type":"api","key":"s3cr3t"},"github-copilot":{"type":"oauth","access":"tok"}}`
	if err := os.WriteFile(filepath.Join(daten, "opencode", "auth.json"), []byte(auth), 0o600); err != nil {
		t.Fatal(err)
	}

	key, err := Key("scc")
	if err != nil {
		t.Fatal(err)
	}
	if key != "s3cr3t" {
		t.Errorf("Key %q", key)
	}
	// oauth ohne key: klare Meldung statt leerem Bearer.
	if _, err := Key("github-copilot"); err == nil || !strings.Contains(err.Error(), "oauth") {
		t.Errorf("unbrauchbarer Error: %v", err)
	}
	if _, err := Key("gibtsnicht"); err == nil || !strings.Contains(err.Error(), "auth login") {
		t.Errorf("unbrauchbarer Error: %v", err)
	}

	// Ohne auth.json überhaupt.
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	if _, err := Key("scc"); err == nil || !strings.Contains(err.Error(), "auth login") {
		t.Errorf("unbrauchbarer Error: %v", err)
	}
}

func TestListe(t *testing.T) {
	root := schreibeConfig(t, `{
  "provider": {
    "scc": {"options": {"baseURL": "https://beispiel.invalid/api/v1"},
            "models": {"a": {"name": "A"}, "b": {"name": "B"}}},
    "eingebaut": {"npm": "x"}
  },
  "enabled_providers": ["scc", "nur-in-enabled"]
}`)
	conf, err := LoadConfig(root)
	if err != nil {
		t.Fatal(err)
	}

	liste := conf.List()
	// Sortiert, und die ID aus enabled_providers ohne Definition ist
	// dabei — sonst bliebe ein Tippfehler dort unsichtbar.
	var ids []string
	for _, e := range liste {
		ids = append(ids, e.ID)
	}
	if strings.Join(ids, ",") != "eingebaut,nur-in-enabled,scc" {
		t.Fatalf("IDs = %v", ids)
	}

	nach := map[string]Entry{}
	for _, e := range liste {
		nach[e.ID] = e
	}
	if e := nach["scc"]; e.BaseURL != "https://beispiel.invalid/api/v1" || e.Modelle != 2 || !e.Aktiv {
		t.Errorf("scc = %+v", e)
	}
	// Eingebaut: kein Endpoint, keine Modelle, nicht in enabled.
	if e := nach["eingebaut"]; e.BaseURL != "" || e.Modelle != 0 || e.Aktiv {
		t.Errorf("eingebaut = %+v", e)
	}
	if e := nach["nur-in-enabled"]; e.BaseURL != "" || !e.Aktiv {
		t.Errorf("nur-in-enabled = %+v", e)
	}
}

func TestSchluesselIDs(t *testing.T) {
	daten := t.TempDir()
	t.Setenv("XDG_DATA_HOME", daten)

	// Ohne auth.json: leere Menge, kein Fehler — das ist ein Zustand.
	da, err := KeyIDs()
	if err != nil || len(da) != 0 {
		t.Fatalf("ohne auth.json: %v, %v", da, err)
	}

	if err := os.MkdirAll(filepath.Join(daten, "opencode"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(daten, "opencode", "auth.json"),
		[]byte(`{"scc":{"type":"api","key":"geheim"},"oauth-nur":{"type":"oauth"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	da, err = KeyIDs()
	if err != nil {
		t.Fatal(err)
	}
	if !da["scc"] {
		t.Error("scc hat einen Key, wird aber nicht gemeldet")
	}
	// type=oauth ohne key: fetch kann damit nichts anfangen.
	if da["oauth-nur"] {
		t.Error("Eintrag ohne key gilt als Schlüssel")
	}
}
