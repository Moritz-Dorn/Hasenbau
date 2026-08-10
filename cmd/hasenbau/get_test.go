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
		"ID", "ENDPOINT", "MODELLE", "AKTIV", "SCHLÜSSEL",
		"scc", "https://beispiel.invalid/api/v1",
		"hasenbau provider fetch <id>",
	} {
		if !strings.Contains(got, muss) {
			t.Errorf("Ausgabe ohne %q:\n%s", muss, got)
		}
	}
	// Der Schlüssel selbst darf nie in der Ausgabe stehen.
	if strings.Contains(got, `"k"`) || strings.Contains(got, "key") {
		t.Errorf("Schlüssel in der Ausgabe:\n%s", got)
	}
	// Die Fixture hat ein Modell und keinen enabled_providers-Eintrag.
	if !strings.Contains(got, "nein") {
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
	if !strings.Contains(got, "Ohne Endpoint:") || !strings.Contains(got, "Ohne Schlüssel:") {
		t.Errorf("Erklärung fehlt:\n%s", got)
	}
}

func TestGetOhneUndMitUnbekannterRessource(t *testing.T) {
	var out, errw strings.Builder
	if code := run([]string{"-bau", t.TempDir(), "get"}, &out, &errw); code != 2 {
		t.Errorf("ohne Ressource: exit %d, erwartet 2", code)
	}
	if !strings.Contains(errw.String(), "Ressourcen:") {
		t.Errorf("Hilfe fehlt: %q", errw.String())
	}
	errw.Reset()
	if code := run([]string{"-bau", t.TempDir(), "get", "hasen"}, &out, &errw); code != 2 {
		t.Errorf("unbekannte Ressource: exit %d, erwartet 2", code)
	}
	if !strings.Contains(errw.String(), "unbekannte Ressource") {
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
	id, err := st.LaufBeginne("notiz-einlagern", "watch", "raeume/laderampe/sources/a.txt")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.NotizSchreibe(id, "31. Februar existiert nicht"); err != nil {
		t.Fatal(err)
	}
	if err := st.NotizSchreibe(id, "zweite Beobachtung"); err != nil {
		t.Fatal(err)
	}
	if err := st.LaufBeende(id, store.LaufErgebnis{
		Status: "ok", SessionID: "ses_t", Summary: "Notiz abgelegt",
		TokensIn: 2100, TokensOut: 360, KostenCent: 12,
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
	for _, muss := range []string{"ID", "KOSTEN", "notiz-einlagern", "watch", "12 ct", "Notiz abgelegt"} {
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
		"Lauf", "Auftrag", "notiz-einlagern", "Auslöser", "raeume/laderampe/sources/a.txt",
		"Session", "ses_t", "2100 ein, 360 aus", "12 ct", "Notiz abgelegt",
		"Notizen des Hasen", "31. Februar existiert nicht", "zweite Beobachtung",
		"hasenbau graben 1",
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
	id, err := st.LaufBeginne("pdf-einlagern", "watch", "kaputt.pdf")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.LaufBeende(id, store.LaufErgebnis{
		Status: "fehler",
		Fehler: "gang pdf-zu-markdown: exit 1\n(log: raeume/laderampe/work/…/gang.log)",
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
	if !strings.Contains(got, "Kein Trace") {
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
