// plugin_js_test.go sichert die Stelle ab, an der dieselbe Regel
// zweimal steht: die Ableitung des Werkzeug-Zustands in Go
// (LeiteZustandAb) und in JavaScript im Bau-Plugin (reviewPruefung).
//
// Die Doppelung ist nicht Nachlässigkeit — das Plugin läuft im
// opencode-Prozess und kann den Hasenbau nicht rufen. Sie ist aber
// nachweislich gedriftet: bis 2026-08-13 registrierte die JS-Seite
// bereits ein Werkzeug, das nur den Probelauf bestanden hatte, während
// Go längst die Freigabe verlangte. Aufgefallen ist das beim
// Doku-Nachziehen, nicht durch einen Test (Hasenbau-7or).
//
// Getestet wird deshalb DIFFERENTIELL: dieselben Skripte durch beide
// Fassungen, und die Erwartung ist nicht hingeschrieben, sondern das,
// was die Go-Seite ausrechnet. Ein neuer Zustand, der nur auf einer
// Seite ankommt, fällt damit auf, ohne dass jemand diese Tabelle pflegt.
//
// Die JS-Fassung braucht eine Runtime, und in dieser Umgebung liegt
// keine im PATH — wohl aber im opencode-Binary: es ist bun-kompiliert,
// und BUN_BE_BUN=1 macht es zur bun-CLI (gemessen 2026-08-18, Bun
// 1.3.14). Das Gate ist damit exec.LookPath("opencode"), dieselbe
// Bedingung wie bei den übrigen Integrationstests. Spielt das Binary
// nicht mit, wird geskippt — das ist ein anderer Fall als „Regel
// verletzt" und darf nicht als Erfolg durchgehen.
package bau

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// jsHarness ruft reviewPruefung aus dem Plugin für jedes Skript auf.
// Die Funktion wird aus der Vorlage HERAUSGESCHNITTEN statt importiert:
// exportieren ließe sie sich nicht gefahrlos, denn opencode behandelt
// jeden benannten Export eines Plugins als Plugin und riefe sie mit dem
// PluginInput auf.
const jsHarness = `import { createHash } from "node:crypto"
import { readFileSync } from "node:fs"

%FUNKTION%

const faelle = JSON.parse(readFileSync(process.env.FAELLE, "utf8"))
console.log(JSON.stringify(faelle.map((f) => reviewPruefung(f))))
`

// schneideFunktion holt eine Top-Level-Funktion aus dem Plugin. Endet an
// der ersten schließenden Klammer in Spalte 0 — das ist der Stil der
// Datei, und trifft es nicht zu, schlägt der Test fehl statt zu raten.
func schneideFunktion(t *testing.T, quelle, name string) string {
	t.Helper()
	kopf := "\nfunction " + name + "("
	start := strings.Index(quelle, kopf)
	if start < 0 {
		t.Fatalf("%s steht nicht mehr in der Plugin-Vorlage — Test anpassen, nicht löschen", name)
	}
	rest := quelle[start+1:]
	ende := strings.Index(rest, "\n}\n")
	if ende < 0 {
		t.Fatalf("Ende von %s nicht gefunden", name)
	}
	return rest[:ende+len("\n}\n")]
}

// bunAusOpencode liefert den Befehl, mit dem sich JavaScript ausführen
// lässt, oder skippt. Zwei verschiedene Gründe, und sie werden
// auseinandergehalten: kein opencode im PATH (wie bei jedem
// Integrationstest hier) oder ein opencode, das nicht mehr
// bun-kompiliert ist (dann trägt die Voraussetzung dieses Tests nicht
// mehr, und das soll jemand lesen).
func bunAusOpencode(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath("opencode")
	if err != nil {
		t.Skip("ohne opencode im PATH nicht prüfbar")
	}
	probe := filepath.Join(t.TempDir(), "probe.js")
	if err := os.WriteFile(probe, []byte("process.stdout.write('ja')\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, probe)
	cmd.Env = append(os.Environ(), "BUN_BE_BUN=1")
	aus, err := cmd.Output()
	if err != nil || string(aus) != "ja" {
		t.Skipf("opencode führt kein JavaScript aus (BUN_BE_BUN, %v) — "+
			"die JS-Seite des Zustands bleibt ungeprüft, siehe Hasenbau-7or", err)
	}
	return bin
}

// jsErgebnis ist, was reviewPruefung zurückgibt.
type jsErgebnis struct {
	OK    bool   `json:"ok"`
	Grund string `json:"grund"`
}

func TestPluginUndGoLeitenDenselbenZustandAb(t *testing.T) {
	bin := bunAusOpencode(t)

	const body = "#!/usr/bin/env python3\nimport sys\nprint(len(sys.argv))\n"
	basis := Review{
		By:        "moritz",
		At:        "2026-08-18T10:00:00Z",
		Does:      "zaehlt Argumente",
		Safe:      "liest nichts, schreibt nichts",
		Kommentar: "#",
	}
	mit := func(f func(*Review)) string {
		r := basis
		f(&r)
		return SchreibeReviewBlock(r, body)
	}
	null, eins := 0, 1

	faelle := []struct {
		name string
		// grund ist das, was in der Begründung der JS-Seite vorkommen
		// muss, wenn sie ablehnt. Der Zustand selbst wird NICHT
		// hingeschrieben — den rechnet die Go-Seite aus.
		grund   string
		skript  string
		gelesen bool // Erwartung nur zur Lesbarkeit des Fehlertexts
	}{
		{name: "kein Review", grund: "ungelesen", skript: body},
		{
			name:   "Review ohne Schlusszeile",
			grund:  "Schlusszeile",
			skript: strings.Replace(mit(func(*Review) {}), "# "+ReviewEnde+"\n", "", 1),
		},
		{name: "nur gelesen", grund: "hypothetical", skript: mit(func(*Review) {})},
		{
			// Der gedriftete Fall: bestandener Probelauf, aber niemand hat
			// die Ausgabe bestätigt. Exit 0 heißt „es lief", nicht „es
			// stimmt" — hier registrierte die JS-Seite einmal, was Go
			// verbot.
			name:  "Probelauf bestanden, nicht freigegeben",
			grund: "hypothetical",
			skript: mit(func(r *Review) {
				r.VerifiedAt, r.VerifiedWith, r.VerifiedExit = "2026-08-18T10:05:00Z", "probe.txt", &null
			}),
		},
		{
			name:  "Probelauf gescheitert",
			grund: "invalid",
			skript: mit(func(r *Review) {
				r.VerifiedAt, r.VerifiedWith, r.VerifiedExit = "2026-08-18T10:05:00Z", "probe.txt", &eins
			}),
		},
		{
			name:    "freigegeben",
			gelesen: true,
			skript: mit(func(r *Review) {
				r.VerifiedAt, r.VerifiedWith, r.VerifiedExit = "2026-08-18T10:05:00Z", "probe.txt", &null
				r.ReleasedBy, r.ReleasedAt = "moritz", "2026-08-18T10:10:00Z"
			}),
		},
		{
			name:  "freigegeben, danach geaendert",
			grund: "outdated",
			skript: mit(func(r *Review) {
				r.ReleasedBy, r.ReleasedAt = "moritz", "2026-08-18T10:10:00Z"
			}) + "print('nachtraeglich')\n",
		},
		{
			// Die Injektion aus Hasenbau-9w6: in einem `#`-Block ist
			// `//bin/sh -c …` kein Kommentar, sondern ein ausführbarer
			// Pfad. Steht sie INNERHALB des Blocks, beendet sie ihn — die
			// Schlusszeile liegt dann außerhalb, der Block ist unbrauchbar
			// und das Werkzeug gilt als ungelesen.
			name:  "//-Zeile im #-Block beendet ihn",
			grund: "Schlusszeile",
			skript: strings.Replace(
				mit(func(r *Review) { r.ReleasedBy, r.ReleasedAt = "moritz", "2026-08-18T10:10:00Z" }),
				"# "+ReviewEnde+"\n",
				"//bin/sh -c 'echo INJIZIERT'\n# "+ReviewEnde+"\n", 1),
		},
		{
			// Und die Form, in der sie damals durchkam: direkt UNTER dem
			// Block. Dort war sie einmal ungehasht, das Review blieb gültig
			// und der Probelauf führte sie aus (gemessen 2026-08-13). Heute
			// gehört sie zum Body, fällt unter den Hash und widerlegt ihn.
			name:  "//-Zeile direkt unter dem Block",
			grund: "outdated",
			skript: mit(func(r *Review) {
				r.ReleasedBy, r.ReleasedAt = "moritz", "2026-08-18T10:10:00Z"
			}) + "//bin/sh -c 'echo INJIZIERT'\n",
		},
	}

	// Die JS-Seite in einem Rutsch: ein Prozessstart, nicht acht.
	tmp := t.TempDir()
	quellen := make([]string, len(faelle))
	for i, f := range faelle {
		quellen[i] = f.skript
	}
	roh, err := json.Marshal(quellen)
	if err != nil {
		t.Fatal(err)
	}
	faelleDatei := filepath.Join(tmp, "faelle.json")
	if err := os.WriteFile(faelleDatei, roh, 0o644); err != nil {
		t.Fatal(err)
	}
	harness := filepath.Join(tmp, "harness.js")
	quelltext := strings.Replace(jsHarness, "%FUNKTION%", schneideFunktion(t, sandboxWaechter, "reviewPruefung"), 1)
	if err := os.WriteFile(harness, []byte(quelltext), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, harness)
	cmd.Env = append(os.Environ(), "BUN_BE_BUN=1", "FAELLE="+faelleDatei)
	aus, err := cmd.Output()
	if err != nil {
		t.Fatalf("Harness: %v\n%s", err, aus)
	}
	var js []jsErgebnis
	if err := json.Unmarshal(aus, &js); err != nil {
		t.Fatalf("Ausgabe des Harness unlesbar: %v\n%s", err, aus)
	}
	if len(js) != len(faelle) {
		t.Fatalf("%d Ergebnisse für %d Fälle", len(js), len(faelle))
	}

	for i, f := range faelle {
		r, rumpf := LiesReview([]byte(f.skript))
		zustand := LeiteZustandAb(r, rumpf)
		// Registriert wird genau, was `actual` ist. Das ist die eine
		// Zusage, an der die Freigabe hängt: ein Werkzeug läuft im
		// Server-Prozess, und der Mensch bei `tool release` ist die
		// einzige Instanz, die die Ausgabe für richtig befunden hat.
		soll := zustand == Actual
		if js[i].OK != soll {
			t.Errorf("%s: Go sagt %s (registrieren: %v), das Plugin sagt ok=%v (%s)",
				f.name, zustand, soll, js[i].OK, js[i].Grund)
			continue
		}
		if soll != f.gelesen {
			t.Errorf("%s: Go leitet %s ab — der Fall meint etwas anderes, als er heißt", f.name, zustand)
		}
		if !soll && !strings.Contains(js[i].Grund, f.grund) {
			t.Errorf("%s: Go sagt %s, die Begründung des Plugins nennt das nicht: %q",
				f.name, zustand, js[i].Grund)
		}
	}
}
