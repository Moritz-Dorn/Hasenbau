package bau

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// werkzeugAnlegen schreibt Skript und Manifest nebeneinander.
func werkzeugAnlegen(t *testing.T, root, dir, name, manifest string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, dir, name+".py"), []byte("#!/usr/bin/env python3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, dir, name+".json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
}

const manifestOK = `{
  "description": "Zaehlt Zeilen.",
  "script": "zaehlen.py",
  "args": [{"name": "datei", "type": "string", "description": "Pfad", "required": true}]
}`

// TestLadeToolsTrenntEntwurfVonFreigegeben: was unter tools/entwurf/
// liegt, hat der Schmied geschrieben und kein Mensch angesehen — es
// wird nicht registriert und darf deshalb auch nicht in der Liste
// stehen, aus der der Generator seine Verbote baut.
func TestLadeToolsTrenntEntwurfVonFreigegeben(t *testing.T) {
	root := t.TempDir()
	werkzeugAnlegen(t, root, ToolsDir, "zaehlen", manifestOK)
	werkzeugAnlegen(t, root, ToolsEntwurfDir, "zaehlen", manifestOK)

	alle, err := LadeTools(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(alle) != 2 {
		t.Fatalf("%d Werkzeuge, erwartet 2", len(alle))
	}
	if alle[0].Entwurf || !alle[1].Entwurf {
		t.Errorf("Reihenfolge/Entwurf falsch: %+v", alle)
	}

	namen, bereit, err := ToolNamen(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(namen) != 1 || namen[0] != "zaehlen" {
		t.Errorf("namen = %v, erwartet genau [zaehlen] — ein Entwurf zaehlt nicht mit", namen)
	}
	// Ungelesen heisst nicht einsatzbereit: das Skript traegt keinen
	// Review-Block, ist also `generated`.
	if len(bereit) != 0 {
		t.Errorf("bereit = %v, erwartet keines — niemand hat es gelesen", bereit)
	}
}

// TestLadeToolsLehntKaputteManifesteAb: ein Manifest, das nicht stimmt,
// wird nicht stillschweigend uebergangen. Ein uebersprungenes Werkzeug
// hiesse, dass ein Hase es ohne Grund nicht bekommt — und das sieht im
// Lauf wie ein Modellfehler aus.
func TestLadeToolsLehntKaputteManifesteAb(t *testing.T) {
	faelle := map[string]struct{ manifest, erwartet string }{
		"kein JSON":            {`{nope`, "manifest"},
		"description fehlt":    {`{"script": "zaehlen.py"}`, "description fehlt"},
		"script fehlt":         {`{"description": "x"}`, "script fehlt"},
		"unbekanntes Feld":     {`{"description": "x", "script": "zaehlen.py", "farbe": "rot"}`, "manifest"},
		"arg ohne Typ":         {`{"description": "x", "script": "zaehlen.py", "args": [{"name": "a", "description": "b"}]}`, "erlaubt sind string, number, boolean"},
		"arg ohne description": {`{"description": "x", "script": "zaehlen.py", "args": [{"name": "a", "type": "string"}]}`, "description fehlt"},
		// Das Skript laeuft im SERVER-Prozess, nicht in der Sandbox des
		// Hasen. Ein Pfad im script-Feld waere ein Weg aus dem Bau
		// heraus, und zwar an allen Permissions vorbei.
		"script mit Pfad": {`{"description": "x", "script": "../../etc/passwd"}`, "enthält einen Pfad"},
	}
	for name, f := range faelle {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			werkzeugAnlegen(t, root, ToolsDir, "zaehlen", f.manifest)
			_, err := LadeTools(root)
			if err == nil {
				t.Fatalf("kein Fehler — %s wurde geschluckt", name)
			}
			if !strings.Contains(err.Error(), f.erwartet) {
				t.Errorf("Fehler %q nennt %q nicht", err, f.erwartet)
			}
		})
	}
}

// TestLadeToolsBrauchtDasSkript: ein Manifest ohne Skript ist kein
// halbes Werkzeug, sondern keines.
func TestLadeToolsBrauchtDasSkript(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ToolsDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ToolsDir, "zaehlen.json"), []byte(manifestOK), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LadeTools(root)
	if err == nil || !strings.Contains(err.Error(), "fehlt") {
		t.Fatalf("Fehler = %v, erwartet einen Hinweis auf das fehlende Skript", err)
	}
}

// TestLadeToolsOhneVerzeichnis: ein Bau ohne Werkzeuge ist der
// Normalfall und kein Fehler.
func TestLadeToolsOhneVerzeichnis(t *testing.T) {
	alle, err := LadeTools(t.TempDir())
	if err != nil {
		t.Fatalf("leerer Bau ist ein Fehler: %v", err)
	}
	if len(alle) != 0 {
		t.Errorf("%d Werkzeuge in einem leeren Bau", len(alle))
	}
}

// TestDiagnoseMeldetSchmiedAmFalschenBriefkasten: der Schmied
// beobachtet einen input-Raum, die Hasen werfen in den requests:-Raum
// ein. Nichts haelt die beiden synchron — und ein Schmied, der am
// falschen Briefkasten wartet, sieht aus wie einer, der nie etwas zu
// tun bekommt (Hasenbau-hcs).
func TestDiagnoseMeldetSchmiedAmFalschenBriefkasten(t *testing.T) {
	werkzeugCheck := func(root string) Check {
		for _, c := range Diagnose(root) {
			if c.Name == "Werkzeuge" {
				return c
			}
		}
		t.Fatal("kein Werkzeuge-Check in der Diagnose")
		return Check{}
	}

	bauMitSchmied := func(t *testing.T, requests, input string) string {
		t.Helper()
		root := t.TempDir()
		for _, d := range []string{"auftraege", "hasen"} {
			if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		schreib := func(rel, inhalt string) {
			if err := os.WriteFile(filepath.Join(root, rel), []byte(inhalt), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		schreib(ConfigFile, "requests: "+requests+"\n")
		schreib("hasen/schmied.md", "---\ndescription: Schmied\n---\nDu bist der Schmied.\n")
		schreib("auftraege/schmied.md", "---\ntrigger:\n  watch: \"*.md\"\nhase: schmied\nraeume:\n  input: "+input+"\n  out: tools/entwurf/\n---\nBau ein Werkzeug.\n")
		return root
	}

	t.Run("passt zusammen", func(t *testing.T) {
		root := bauMitSchmied(t, "raeume/wuensche/", "raeume/wuensche/tools/")
		if c := werkzeugCheck(root); !c.OK || c.Hint != "" {
			t.Errorf("stimmiger Bau wird bemaengelt: %+v", c)
		}
	})

	t.Run("laeuft auseinander", func(t *testing.T) {
		root := bauMitSchmied(t, "raeume/anders/", "raeume/wuensche/tools/")
		c := werkzeugCheck(root)
		if c.OK {
			t.Errorf("auseinanderlaufende Raeume gelten als in Ordnung: %+v", c)
		}
		if !strings.Contains(c.Hint, "raeume/anders/tools/") {
			t.Errorf("Hinweis %q nennt den erwarteten Raum nicht", c.Hint)
		}
	})
}
