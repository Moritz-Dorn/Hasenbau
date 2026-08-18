package bau

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// veraltetesPlugin ist eine Fassung, wie sie ein Bau von 2026-07 trägt:
// der Wächter allein, ohne Review-Gate und ohne Werkzeug-Sandkasten.
const veraltetesPlugin = `// Der Sandbox-Wächter des Hasenbaus (alte Fassung).
export const SandboxWaechter = async () => ({})
`

// TestInitZiehtVeraltetesPluginNach ist der Kern von Hasenbau-uei: ein
// bestehender Bau bekam eine neue Fassung des Plugins NIE, weil Init
// nichts überschreibt. Für hasenbau.yaml und die Sonder-Hasen ist das
// richtig; für eine Datei, in der Sicherheitslogik steht, die niemand
// von Hand pflegt, ist es der Weg, auf dem eine Zusage still verfällt.
func TestInitZiehtVeraltetesPluginNach(t *testing.T) {
	root := filepath.Join(t.TempDir(), "bau")
	if _, err := Init(root, "/opt/hasenbau"); err != nil {
		t.Fatal(err)
	}
	pfad := filepath.Join(root, PluginDatei)
	if err := os.WriteFile(pfad, []byte(veraltetesPlugin), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Init(root, "/opt/hasenbau"); err != nil {
		t.Fatal(err)
	}
	nachher, err := os.ReadFile(pfad)
	if err != nil {
		t.Fatal(err)
	}
	if string(nachher) != sandboxWaechter {
		t.Errorf("veraltetes Plugin steht noch da (%d Zeichen statt %d)",
			len(nachher), len(sandboxWaechter))
	}
}

// TestSchreibePluginMeldetWasEsVorfand: der Aufrufer muss „ersetzt" von
// „war schon richtig" unterscheiden können — nur so erfährt jemand,
// dessen Änderung überschrieben wurde, warum sie weg ist.
func TestSchreibePluginMeldetWasEsVorfand(t *testing.T) {
	root := filepath.Join(t.TempDir(), "bau")
	if _, err := Init(root, "/opt/hasenbau"); err != nil {
		t.Fatal(err)
	}
	pfad := filepath.Join(root, PluginDatei)

	if erg, err := SchreibePlugin(root); err != nil || erg != PluginUnveraendert {
		t.Errorf("nach init: erg = %v, err = %v — erwartet unverändert", erg, err)
	}

	if err := os.WriteFile(pfad, []byte(veraltetesPlugin), 0o644); err != nil {
		t.Fatal(err)
	}
	if erg, err := SchreibePlugin(root); err != nil || erg != PluginErsetzt {
		t.Errorf("bei alter Fassung: erg = %v, err = %v — erwartet ersetzt", erg, err)
	}

	if err := os.Remove(pfad); err != nil {
		t.Fatal(err)
	}
	if erg, err := SchreibePlugin(root); err != nil || erg != PluginAngelegt {
		t.Errorf("bei fehlender Datei: erg = %v, err = %v — erwartet angelegt", erg, err)
	}
}

// TestSchreibePluginLaesstEigenePluginsStehen: überschrieben wird die
// eine generierte Datei, nicht das Verzeichnis. Eigene Plugins im Bau
// sind eine bewusste Option (PLAN §3) — wer sie verlöre, bekäme statt
// einer Härtung einen Datenverlust.
func TestSchreibePluginLaesstEigenePluginsStehen(t *testing.T) {
	root := filepath.Join(t.TempDir(), "bau")
	if _, err := Init(root, "/opt/hasenbau"); err != nil {
		t.Fatal(err)
	}
	eigen := filepath.Join(root, filepath.Dir(PluginDatei), "meins.js")
	const inhalt = "export const Meins = async () => ({})\n"
	if err := os.WriteFile(eigen, []byte(inhalt), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := SchreibePlugin(root); err != nil {
		t.Fatal(err)
	}
	nachher, err := os.ReadFile(eigen)
	if err != nil {
		t.Fatalf("eigenes Plugin weg: %v", err)
	}
	if string(nachher) != inhalt {
		t.Error("eigenes Plugin wurde verändert")
	}
}

// TestPluginAktuellSiehtDieAlteFassung deckt die Diagnose ab: „liegt da"
// ist nicht dasselbe wie „ist die Fassung dieses Binaries", und der
// Unterschied ist genau der stille Fall aus Hasenbau-uei.
func TestPluginAktuellSiehtDieAlteFassung(t *testing.T) {
	root := filepath.Join(t.TempDir(), "bau")
	if _, err := Init(root, "/opt/hasenbau"); err != nil {
		t.Fatal(err)
	}
	if aktuell, err := PluginAktuell(root); err != nil || !aktuell {
		t.Errorf("frischer Bau: aktuell = %v, err = %v", aktuell, err)
	}
	if err := os.WriteFile(filepath.Join(root, PluginDatei), []byte(veraltetesPlugin), 0o644); err != nil {
		t.Fatal(err)
	}
	if aktuell, err := PluginAktuell(root); err != nil || aktuell {
		t.Errorf("alte Fassung: aktuell = %v, err = %v", aktuell, err)
	}
}

// TestDiagnoseMeldetGetracktesPlugin: ein Bau, der vor Hasenbau-uei
// angelegt wurde, hat die generierte Datei in seinem Git. Eine
// .gitignore-Zeile holt das nicht mehr ein — und seit die Datei bei
// jedem Start neu geschrieben wird, steht sie nach jedem Upgrade als
// Änderung da, ohne dass jemand sie angefasst hat.
//
// Gemeldet wird das und nicht repariert: `git rm --cached` griffe in
// die Historie eines fremden Repos (Hasenbau-610).
func TestDiagnoseMeldetGetracktesPlugin(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("ohne git nicht prüfbar")
	}
	root := filepath.Join(t.TempDir(), "bau")
	if _, err := Init(root, "/opt/hasenbau"); err != nil {
		t.Fatal(err)
	}
	waechter := func() Check {
		for _, c := range Diagnose(root) {
			if c.Name == "Sandbox guard" {
				return c
			}
		}
		t.Fatal("kein Sandbox-Check in der Diagnose")
		return Check{}
	}

	// Frischer Bau: die Datei ist ignoriert, es gibt nichts zu melden.
	if h := waechter().Hint; strings.Contains(h, "git rm --cached") {
		t.Errorf("frischer Bau bekommt den Hinweis: %q", h)
	}

	// Der alte Zustand: die Datei liegt im Index, trotz .gitignore.
	cmd := exec.Command("git", "-C", root, "add", "-f", PluginDatei)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v: %s", err, out)
	}
	c := waechter()
	if !c.OK {
		t.Errorf("getrackte Datei ist kein Defekt, nur Rauschen: %+v", c)
	}
	if !strings.Contains(c.Hint, "git rm --cached") || !strings.Contains(c.Hint, PluginDatei) {
		t.Errorf("Hinweis nennt den Weg nicht: %q", c.Hint)
	}
}
