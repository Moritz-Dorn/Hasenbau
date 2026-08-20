package provider

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mitAuth legt eine auth.json an und biegt XDG_DATA_HOME darauf um.
func mitAuth(t *testing.T, inhalt string) {
	t.Helper()
	daten := t.TempDir()
	t.Setenv("XDG_DATA_HOME", daten)
	if err := os.MkdirAll(filepath.Join(daten, "opencode"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(daten, "opencode", "auth.json"), []byte(inhalt), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Der Kern von Hasenbau-a88: opencode nimmt options.apiKey VOR auth.json
// (provider.ts — `if (options["apiKey"] === undefined && provider.key)`),
// und der Hasenbau muss denselben Weg gehen. Täte er es nicht, meldete
// `provider fetch` einen anderen Schlüssel als den, mit dem die Läufe
// tatsächlich laufen — der schlimmste Fall, weil er nicht auffällt.
func TestConfigSchluesselGewinntGegenAuthJSON(t *testing.T) {
	mitAuth(t, `{"scc":{"type":"api","key":"aus-auth-json"}}`)
	t.Setenv("SCC_KEY", "aus-der-umgebung")

	root := schreibeConfig(t, `{
  "provider": {"scc": {"options": {"baseURL": "https://x.invalid", "apiKey": "{env:SCC_KEY}"}}}
}`)
	conf, err := LoadConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	key, err := conf.Key("scc")
	if err != nil {
		t.Fatal(err)
	}
	if key != "aus-der-umgebung" {
		t.Errorf("Key %q — options.apiKey muss gewinnen, so wie bei opencode", key)
	}

	// Und ohne options.apiKey bleibt es beim alten Weg.
	root2 := schreibeConfig(t, `{
  "provider": {"scc": {"options": {"baseURL": "https://x.invalid"}}}
}`)
	conf2, err := LoadConfig(root2)
	if err != nil {
		t.Fatal(err)
	}
	key, err = conf2.Key("scc")
	if err != nil {
		t.Fatal(err)
	}
	if key != "aus-auth-json" {
		t.Errorf("Key %q — ohne options.apiKey gilt auth.json", key)
	}
}

// {file:…} ist der empfohlene Weg im Container. Drei Eigenschaften
// davon sind an opencodes config/variable.ts abgelesen und müssen hier
// genauso gelten: ~/ wird expandiert, der Inhalt wird getrimmt, und eine
// fehlende Datei ist ein Fehler und kein leerer Schlüssel.
func TestSchluesselAusDatei(t *testing.T) {
	mitAuth(t, `{}`)
	home := t.TempDir()
	t.Setenv("HOME", home)
	geheim := filepath.Join(home, "geheim")
	// Mit Zeilenumbruch, so wie `echo` ihn schreibt.
	if err := os.WriteFile(geheim, []byte("sk-aus-der-datei\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	root := schreibeConfig(t, `{
  "provider": {"scc": {"options": {"apiKey": "{file:~/geheim}"}}}
}`)
	conf, err := LoadConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	key, err := conf.Key("scc")
	if err != nil {
		t.Fatal(err)
	}
	if key != "sk-aus-der-datei" {
		t.Errorf("Key %q — ~/ muss expandiert und der Inhalt getrimmt werden", key)
	}

	// Fehlende Datei: ein Fehler, der den Pfad nennt. Ein leerer
	// Schlüssel wäre hier das Schlimmste — er ginge als Bearer hinaus.
	root2 := schreibeConfig(t, `{
  "provider": {"scc": {"options": {"apiKey": "{file:/gibt/es/nicht}"}}}
}`)
	conf2, _ := LoadConfig(root2)
	_, err = conf2.Key("scc")
	if err == nil || !strings.Contains(err.Error(), "/gibt/es/nicht") {
		t.Errorf("unbrauchbarer Error: %v", err)
	}
}

// Eine nicht gesetzte Umgebungsvariable wird bei opencode zum LEEREN
// String, nicht zum Fehler. Der Hasenbau ahmt das nach — und fängt das
// Ergebnis ab, statt einen leeren Bearer zu schicken.
func TestLeererSchluesselIstEinFehler(t *testing.T) {
	mitAuth(t, `{}`)
	os.Unsetenv("GIBTS_NICHT")
	root := schreibeConfig(t, `{
  "provider": {"scc": {"options": {"apiKey": "{env:GIBTS_NICHT}"}}}
}`)
	conf, _ := LoadConfig(root)
	_, err := conf.Key("scc")
	if err == nil || !strings.Contains(err.Error(), "empty") {
		t.Errorf("unbrauchbarer Error: %v", err)
	}
}

// Ein Präfix, das opencode nicht kennt, bliebe dort als Literal stehen
// und ginge als Schlüssel über die Leitung. Hier ist es ein Fehler:
// etwas, das wie ein Platzhalter aussieht, ist keiner.
func TestUnbekannterPlatzhalterIstEinFehler(t *testing.T) {
	mitAuth(t, `{}`)
	root := schreibeConfig(t, `{
  "provider": {"scc": {"options": {"apiKey": "{vault:prod/scc}"}}}
}`)
	conf, _ := LoadConfig(root)
	_, err := conf.Key("scc")
	if err == nil || !strings.Contains(err.Error(), "{env:NAME}") {
		t.Errorf("unbrauchbarer Error: %v", err)
	}
}

// Ein Schlüssel im Klartext ist nicht empfohlen (PLAN §3), aber gültig —
// opencode nähme ihn ebenso, und ihn hier abzulehnen hieße, ein
// funktionierendes Setup für kaputt zu erklären.
func TestKlartextSchluesselWirdGenommen(t *testing.T) {
	mitAuth(t, `{}`)
	root := schreibeConfig(t, `{
  "provider": {"scc": {"options": {"apiKey": "sk-im-klartext"}}}
}`)
	conf, _ := LoadConfig(root)
	key, err := conf.Key("scc")
	if err != nil {
		t.Fatal(err)
	}
	if key != "sk-im-klartext" {
		t.Errorf("Key %q", key)
	}
}

// KeySource ist das, was `get provider` und `describe provider` zeigen:
// der WEG, nie der Schlüssel. Der interessante Zustand ist der dritte —
// konfiguriert, aber liefert nichts. Von außen sieht so ein Bau aus wie
// einer, dem nichts fehlt.
func TestKeySourceNenntDenWegUndNieDenSchluessel(t *testing.T) {
	mitAuth(t, `{}`)
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.WriteFile(filepath.Join(home, "da"), []byte("sk-geheim"), 0o600); err != nil {
		t.Fatal(err)
	}

	root := schreibeConfig(t, `{
  "provider": {
    "ausdatei":  {"options": {"apiKey": "{file:~/da}"}},
    "kaputt":    {"options": {"apiKey": "{file:~/fehlt}"}},
    "ausauth":   {"options": {"baseURL": "https://x.invalid"}},
    "ohnealles": {"options": {"baseURL": "https://y.invalid"}}
  }
}`)
	conf, err := LoadConfig(root)
	if err != nil {
		t.Fatal(err)
	}

	faelle := []struct {
		id     string
		inAuth bool
		via    string
		ok     bool
		label  string
	}{
		{"ausdatei", false, "options.apiKey", true, "options.apiKey"},
		{"kaputt", false, "options.apiKey", false, "options.apiKey (broken)"},
		{"ausauth", true, "auth.json", true, "auth.json"},
		{"ohnealles", false, "", false, "—"},
	}
	for _, f := range faelle {
		q := conf.KeySource(f.id, f.inAuth)
		if q.Via != f.via || q.OK() != f.ok || q.Label() != f.label {
			t.Errorf("%s: via=%q ok=%v label=%q — erwartet via=%q ok=%v label=%q",
				f.id, q.Via, q.OK(), q.Label(), f.via, f.ok, f.label)
		}
		// Die Zusage, die diese Struktur überhaupt trägt.
		if strings.Contains(q.Ref, "sk-geheim") || strings.Contains(q.Label(), "sk-geheim") {
			t.Errorf("%s: der Schlüssel steht in der Auskunft", f.id)
		}
	}
}
