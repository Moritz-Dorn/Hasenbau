package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Moritz-Dorn/Hasenbau/internal/store"
)

func TestGetProvider(t *testing.T) {
	root := baueProviderBau(t, "https://beispiel.invalid/api/v1")

	var out, errw strings.Builder
	if code := run([]string{"-bau", root, "get", "provider"}, &out, &errw); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errw.String())
	}
	got := out.String()
	for _, muss := range []string{
		"ID", "ENDPOINT", "MODELS", "ACTIVE", "KEY",
		"scc", "https://beispiel.invalid/api/v1",
		"hasenbau provider fetch <id>",
	} {
		if !strings.Contains(got, muss) {
			t.Errorf("Ausgabe ohne %q:\n%s", muss, got)
		}
	}
	// Der Schlüssel selbst darf nie in der Ausgabe stehen. Geprüft wird
	// auf den Wert und auf das JSON-Feld — nicht auf das Wort „key",
	// seit die Ausgabe englisch redet und es dort erklärend vorkommt.
	if strings.Contains(got, `"k"`) || strings.Contains(got, `"key":`) {
		t.Errorf("Schlüssel in der Ausgabe:\n%s", got)
	}
	// Die Fixture hat ein Modell und keinen enabled_providers-Eintrag.
	if !strings.Contains(got, "no") {
		t.Errorf("nicht-aktiver Provider nicht als solcher erkennbar:\n%s", got)
	}
}

// TestGetProviderOhneSchluesselUndOhneEndpoint: genau die beiden Fälle,
// in denen `provider fetch` nicht funktionieren kann — die Ausgabe muss
// das vorher sagen.
func TestGetProviderOhneSchluesselUndOhneEndpoint(t *testing.T) {
	root := baueProviderBau(t, "https://beispiel.invalid/api/v1")

	// Ein eingebauter Provider ohne baseURL, dazu eine ID, die nur in
	// enabled_providers steht (Tippfehler-Fall), und keine auth.json.
	conf := filepath.Join(root, ".opencode-home", "opencode", "opencode.json")
	roh := map[string]any{}
	b, err := os.ReadFile(conf)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &roh); err != nil {
		t.Fatal(err)
	}
	roh["provider"].(map[string]any)["eingebaut"] = map[string]any{"npm": "x"}
	roh["enabled_providers"] = []any{"scc", "verschrieben"}
	neu, err := json.Marshal(roh)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(conf, neu, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_DATA_HOME", t.TempDir()) // keine auth.json

	var out, errw strings.Builder
	if code := run([]string{"-bau", root, "get", "provider"}, &out, &errw); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errw.String())
	}
	got := out.String()
	if !strings.Contains(got, "eingebaut") || !strings.Contains(got, "—") {
		t.Errorf("Provider ohne Endpoint fehlt oder ist nicht markiert:\n%s", got)
	}
	// Eine ID nur in enabled_providers ist ein stiller Konfigurationsfehler.
	if !strings.Contains(got, "verschrieben") {
		t.Errorf("ID nur in enabled_providers fehlt:\n%s", got)
	}
	if !strings.Contains(got, "Without an endpoint:") || !strings.Contains(got, "Without a key:") {
		t.Errorf("Erklärung fehlt:\n%s", got)
	}
}

func TestGetOhneUndMitUnbekannterRessource(t *testing.T) {
	var out, errw strings.Builder
	if code := run([]string{"-bau", t.TempDir(), "get"}, &out, &errw); code != 2 {
		t.Errorf("ohne Ressource: exit %d, erwartet 2", code)
	}
	if !strings.Contains(errw.String(), "Resources:") {
		t.Errorf("Hilfe fehlt: %q", errw.String())
	}
	errw.Reset()
	if code := run([]string{"-bau", t.TempDir(), "get", "karotten"}, &out, &errw); code != 2 {
		t.Errorf("unbekannte Ressource: exit %d, erwartet 2", code)
	}
	if !strings.Contains(errw.String(), "unknown resource") {
		t.Errorf("Meldung: %q", errw.String())
	}
}

func TestGetProviderOhneBauConfig(t *testing.T) {
	var out, errw strings.Builder
	if code := run([]string{"-bau", t.TempDir(), "get", "provider"}, &out, &errw); code != 1 {
		t.Errorf("exit %d, erwartet 1", code)
	}
	if !strings.Contains(errw.String(), "hasenbau init") {
		t.Errorf("Meldung sagt nicht, was zu tun ist: %q", errw.String())
	}
}

// laufMitNotizen legt einen abgeschlossenen Lauf samt Rückkanal-Notizen
// an — die Vorlage für get/describe lauf.
func laufMitNotizen(t *testing.T, bau string) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(bau, "state", "hasenbau.db"))
	if err != nil {
		t.Fatal(err)
	}
	id, err := st.StartLauf("notiz-einlagern", "watch", "raeume/laderampe/sources/a.txt")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.WriteNote(id, "31. Februar gibt es nicht"); err != nil {
		t.Fatal(err)
	}
	if err := st.WriteNote(id, "zweite Beobachtung"); err != nil {
		t.Fatal(err)
	}
	if err := st.EndLauf(id, store.LaufResult{
		Status: "ok", SessionID: "ses_t", Summary: "Notiz abgelegt",
		TokensIn: 2100, TokensOut: 360, CostCent: 12,
	}); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestGetLauf(t *testing.T) {
	bau := t.TempDir()
	laufMitNotizen(t, bau).Close()

	var out, errw strings.Builder
	if code := run([]string{"-bau", bau, "get", "lauf", "1"}, &out, &errw); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errw.String())
	}
	got := out.String()
	for _, muss := range []string{"ID", "COST", "notiz-einlagern", "watch", "12 ct", "Notiz abgelegt"} {
		if !strings.Contains(got, muss) {
			t.Errorf("Ausgabe ohne %q:\n%s", muss, got)
		}
	}
	// get bleibt eine Zeile: die Notizen gehören zu describe.
	if strings.Contains(got, "31. Februar") {
		t.Errorf("get zeigt Notizen:\n%s", got)
	}

	errw.Reset()
	if code := run([]string{"-bau", bau, "get", "lauf", "99"}, &out, &errw); code != 1 {
		t.Errorf("unbekannter Lauf: exit %d, erwartet 1", code)
	}
	errw.Reset()
	if code := run([]string{"-bau", bau, "get", "lauf", "acht"}, &out, &errw); code != 2 {
		t.Errorf("ungültige ID: exit %d, erwartet 2", code)
	}
}

func TestDescribeLauf(t *testing.T) {
	bau := t.TempDir()
	laufMitNotizen(t, bau).Close()

	var out, errw strings.Builder
	if code := run([]string{"-bau", bau, "describe", "lauf", "1"}, &out, &errw); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errw.String())
	}
	got := out.String()
	for _, muss := range []string{
		"Lauf", "Auftrag", "notiz-einlagern", "Trigger file", "raeume/laderampe/sources/a.txt",
		"Session", "ses_t", "2100 in, 360 out", "12 ct", "Notiz abgelegt",
		"Notes from the Hase", "31. Februar gibt es nicht", "zweite Beobachtung",
		"hasenbau dig 1",
	} {
		if !strings.Contains(got, muss) {
			t.Errorf("Ausgabe ohne %q:\n%s", muss, got)
		}
	}
}

// TestDescribeLaufOhneSessionUndMitFehler: der Fall, für den man den
// Befehl überhaupt aufruft — was ist schiefgegangen?
func TestDescribeLaufOhneSessionUndMitFehler(t *testing.T) {
	bau := t.TempDir()
	st, err := store.Open(filepath.Join(bau, "state", "hasenbau.db"))
	if err != nil {
		t.Fatal(err)
	}
	id, err := st.StartLauf("pdf-einlagern", "watch", "kaputt.pdf")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.EndLauf(id, store.LaufResult{
		Status: "failed",
		Error:  "gang pdf-zu-markdown: exit 1\n(log: raeume/laderampe/work/…/gang.log)",
	}); err != nil {
		t.Fatal(err)
	}
	st.Close()

	var out, errw strings.Builder
	if code := run([]string{"-bau", bau, "describe", "lauf", "1"}, &out, &errw); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errw.String())
	}
	got := out.String()
	// Der Fehler steht vollständig da, auch mehrzeilig.
	if !strings.Contains(got, "gang pdf-zu-markdown: exit 1") || !strings.Contains(got, "gang.log") {
		t.Errorf("Fehler nicht vollständig:\n%s", got)
	}
	if !strings.Contains(got, "No trace") {
		t.Errorf("fehlende Session nicht erklärt:\n%s", got)
	}
}

func TestAlterLaeufeBefehlWeistWeiter(t *testing.T) {
	var out, errw strings.Builder
	if code := run([]string{"-bau", t.TempDir(), "laeufe"}, &out, &errw); code != 2 {
		t.Errorf("exit %d, erwartet 2", code)
	}
	if !strings.Contains(errw.String(), "get laeufe") {
		t.Errorf("Wegweiser fehlt: %q", errw.String())
	}
}

// Hasenbau-uh0: `describe auftrag` nennt das Zeitlimit des LLM-Schritts
// und sagt dazu, ob es aus dem Auftrag kommt oder die Vorgabe ist.
func TestDescribeAuftragZeigtZeitlimit(t *testing.T) {
	bau := t.TempDir()
	if err := os.MkdirAll(filepath.Join(bau, "auftraege"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(bau, "hasen"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bau, "hasen", "testhase.md"),
		[]byte("---\ndescription: t\n---\nTu was.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	schreibe := func(name, timeoutZeile string) {
		src := "---\ntrigger:\n  manual: true\nhase: testhase\n" + timeoutZeile +
			"raeume:\n  work: raeume/w/\n---\nMach.\n"
		if err := os.WriteFile(filepath.Join(bau, "auftraege", name+".md"), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	schreibe("mit", "hase_timeout: 90m\n")
	schreibe("ohne", "")

	var out, errw strings.Builder
	if code := run([]string{"-bau", bau, "describe", "auftrag", "mit"}, &out, &errw); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errw.String())
	}
	// Gekürzte Schreibweise seit Hasenbau-do0.4: „1h30m", nicht „1h30m0s".
	if !strings.Contains(out.String(), "1h30m") || !strings.Contains(out.String(), "hase_timeout") {
		t.Errorf("Zeitlimit des Auftrags fehlt:\n%s", out.String())
	}

	out.Reset()
	errw.Reset()
	if code := run([]string{"-bau", bau, "describe", "auftrag", "ohne"}, &out, &errw); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errw.String())
	}
	if !strings.Contains(out.String(), "30m ") || !strings.Contains(out.String(), "default") {
		t.Errorf("Vorgabe fehlt:\n%s", out.String())
	}
}

// Hasenbau-ha0.7: Der praktische Nutzen von `describe provider` ist der
// model:-String für ein Hasen-Template — und dass der Schlüssel nicht
// dabei ist.
func TestDescribeProvider(t *testing.T) {
	root := baueProviderBau(t, "https://beispiel.invalid/api/v1")

	var out, errw strings.Builder
	if code := run([]string{"-bau", root, "describe", "provider", "scc"}, &out, &errw); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errw.String())
	}
	got := out.String()
	for _, muss := range []string{
		"Provider", "scc", "@ai-sdk/openai-compatible",
		"https://beispiel.invalid/api/v1",
		"Key", "Models (1)",
		"scc/alt", "Alt", // genau so gehört es ins Template
	} {
		if !strings.Contains(got, muss) {
			t.Errorf("Ausgabe ohne %q:\n%s", muss, got)
		}
	}
	if strings.Contains(got, `"k"`) {
		t.Errorf("Schlüssel in der Ausgabe:\n%s", got)
	}
	// Die Fixture steht nicht in enabled_providers — das ist der stille
	// Fehler, den der Befehl laut machen soll.
	if !strings.Contains(got, "missing from enabled_providers") {
		t.Errorf("nicht aktivierter Provider nicht erkennbar:\n%s", got)
	}
}

func TestDescribeProviderFehlerpfade(t *testing.T) {
	root := baueProviderBau(t, "https://beispiel.invalid/api/v1")

	var out, errw strings.Builder
	if code := run([]string{"-bau", root, "describe", "provider", "gibtsnicht"}, &out, &errw); code != 1 {
		t.Errorf("exit %d, erwartet 1", code)
	}
	if !strings.Contains(errw.String(), "available: scc") {
		t.Errorf("Meldung zählt nicht auf, was es gibt: %q", errw.String())
	}
	errw.Reset()
	if code := run([]string{"-bau", root, "describe", "provider"}, &out, &errw); code != 2 {
		t.Errorf("ohne ID: exit %d, erwartet 2", code)
	}
}
