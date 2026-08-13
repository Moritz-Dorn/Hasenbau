package hase

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Moritz-Dorn/Hasenbau/internal/auftrag"
)

func auftragMitRaeumen(name, hase string, raeume map[string]string) *auftrag.Auftrag {
	return &auftrag.Auftrag{Name: name, Hase: hase, Raeume: raeume}
}

// TestGrenzeIstDieDesHasen: die Raum-Grenze eines Werkzeugs wird aus
// denselben Rollen abgeleitet wie das Schreibrecht des Hasen (§6).
// Schriebe sie jemand eigenstaendig auf, waere sie beim naechsten
// Rollen-Wechsel still falsch — und ein Werkzeug haette mehr Rechte als
// der Hase, der es ruft.
func TestGrenzeIstDieDesHasen(t *testing.T) {
	a := auftragMitRaeumen("pdf-einlagern", "sortierer", map[string]string{
		"input": "raeume/laderampe",
		"work":  "raeume/werkbank",
		"out":   "raeume/lager",
		"done":  "raeume/archiv",
	})
	g := GrenzeVon(a)

	if g.Agent != "pdf-einlagern__sortierer" {
		t.Errorf("Agent = %q", g.Agent)
	}
	// Lesen: alle Raeume des Auftrags.
	for _, erwartet := range []string{"raeume/laderampe", "raeume/werkbank", "raeume/lager", "raeume/archiv"} {
		if !enthaelt(g.Read, erwartet) {
			t.Errorf("Read enthaelt %q nicht: %v", erwartet, g.Read)
		}
	}
	// Schreiben: NUR work und out — dieselben Rollen wie beim Agenten.
	if len(g.Write) != 2 || !enthaelt(g.Write, "raeume/werkbank") || !enthaelt(g.Write, "raeume/lager") {
		t.Errorf("Write = %v, erwartet werkbank und lager", g.Write)
	}
	// done gehoert dem Runner, nicht dem Hasen — und damit auch keinem
	// seiner Werkzeuge.
	if enthaelt(g.Write, "raeume/archiv") {
		t.Error("der done-Raum ist schreibbar — den bedient der Runner, nicht der Hase")
	}
	// Was man schreiben darf, muss man lesen duerfen: sonst muesste die
	// anwendende Seite die Listen zusammenrechnen.
	for _, w := range g.Write {
		if !enthaelt(g.Read, w) {
			t.Errorf("%q ist schreibbar, aber nicht lesbar: %v", w, g.Read)
		}
	}
}

// TestGrenzenDateiIstStabilUndVollstaendig: die Datei wird bei jedem
// Laden neu geschrieben. Ein geloeschter Auftrag darf keine Grenze
// hinterlassen, die noch jemanden hereinlaesst, und die Reihenfolge muss
// stabil sein — sonst rauscht sie durch jedes git diff des Baus.
func TestGrenzenDateiIstStabilUndVollstaendig(t *testing.T) {
	root := t.TempDir()
	zwei := []*auftrag.Auftrag{
		auftragMitRaeumen("zweiter", "hase", map[string]string{"input": "raeume/b", "out": "raeume/b-out"}),
		auftragMitRaeumen("erster", "hase", map[string]string{"input": "raeume/a", "out": "raeume/a-out"}),
	}
	if err := SchreibeGrenzen(root, zwei); err != nil {
		t.Fatal(err)
	}
	roh, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(GrenzenDatei)))
	if err != nil {
		t.Fatal(err)
	}
	var grenzen []Grenze
	if err := json.Unmarshal(roh, &grenzen); err != nil {
		t.Fatalf("die Datei ist kein gueltiges JSON: %v\n%s", err, roh)
	}
	if len(grenzen) != 2 || grenzen[0].Agent != "erster__hase" {
		t.Fatalf("Reihenfolge nicht stabil: %+v", grenzen)
	}

	// Ein Auftrag faellt weg: seine Grenze muss verschwinden.
	if err := SchreibeGrenzen(root, zwei[1:]); err != nil {
		t.Fatal(err)
	}
	roh, err = os.ReadFile(filepath.Join(root, filepath.FromSlash(GrenzenDatei)))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(roh), "zweiter__hase") {
		t.Errorf("die Grenze eines geloeschten Auftrags steht noch da:\n%s", roh)
	}
}

func enthaelt(liste []string, s string) bool {
	for _, e := range liste {
		if e == s {
			return true
		}
	}
	return false
}
