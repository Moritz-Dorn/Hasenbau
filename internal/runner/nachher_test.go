package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Moritz-Dorn/Hasenbau/internal/auftrag"
)

func TestNachherMoveInVerzeichnis(t *testing.T) {
	a := testAuftrag()
	a.Raeume["done"] = "raeume/archiv/"
	a.After = []auftrag.After{{Action: "move", From: "$TRIGGER_FILE", To: "raeume/archiv/"}}
	u := testUmgebung(t, a)

	if err := RunAfter(u, a); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(u.Bau, "raeume/archiv/doc.txt")); err != nil {
		t.Errorf("Input nicht im Archiv: %v", err)
	}
	if _, err := os.Stat(filepath.Join(u.Bau, u.TriggerFile)); err == nil {
		t.Error("Input liegt noch am Ursprung")
	}
}

func TestNachherMoveKollisionUeberschreibtNie(t *testing.T) {
	a := testAuftrag()
	a.After = []auftrag.After{{Action: "move", From: "$TRIGGER_FILE", To: "raeume/archiv/"}}
	u := testUmgebung(t, a)

	if err := os.MkdirAll(filepath.Join(u.Bau, "raeume/archiv"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(u.Bau, "raeume/archiv/doc.txt"), []byte("alt"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := RunAfter(u, a); err != nil {
		t.Fatal(err)
	}
	alt, err := os.ReadFile(filepath.Join(u.Bau, "raeume/archiv/doc.txt"))
	if err != nil || string(alt) != "alt" {
		t.Errorf("Bestand überschrieben: %q, %v", alt, err)
	}
	eintraege, err := os.ReadDir(filepath.Join(u.Bau, "raeume/archiv"))
	if err != nil || len(eintraege) != 2 {
		t.Errorf("erwartete 2 Dateien im Archiv, fand %d", len(eintraege))
	}
}

func TestNachherCopyUndDelete(t *testing.T) {
	a := testAuftrag()
	a.After = []auftrag.After{
		{Action: "copy", From: "$TRIGGER_FILE", To: "raeume/work/sicherung.txt"},
		{Action: "delete", From: "$TRIGGER_FILE"},
	}
	u := testUmgebung(t, a)

	if err := RunAfter(u, a); err != nil {
		t.Fatal(err)
	}
	kopie, err := os.ReadFile(filepath.Join(u.Bau, "raeume/work/sicherung.txt"))
	if err != nil || string(kopie) != "material" {
		t.Errorf("Kopie = %q, %v", kopie, err)
	}
	if _, err := os.Stat(filepath.Join(u.Bau, u.TriggerFile)); err == nil {
		t.Error("delete hat den Input nicht entfernt")
	}
}

func TestNachherBleibtImBau(t *testing.T) {
	faelle := []auftrag.After{
		{Action: "move", From: "$TRIGGER_FILE", To: "../draussen/"},
		{Action: "delete", From: "/etc/passwd"},
		{Action: "copy", From: "$TRIGGER_FILE", To: "$BAU/raeume/archiv/"}, // $BAU ist absolut ⇒ tabu
	}
	for _, n := range faelle {
		a := testAuftrag()
		a.After = []auftrag.After{n}
		u := testUmgebung(t, a)
		if err := RunAfter(u, a); err == nil {
			t.Errorf("%s %s -> %s: muss scheitern", n.Action, n.From, n.To)
		}
	}
}

func TestNachherFehlendeQuelle(t *testing.T) {
	a := testAuftrag()
	a.After = []auftrag.After{{Action: "move", From: "raeume/eingang/gibtsnicht.txt", To: "raeume/archiv/"}}
	u := testUmgebung(t, a)
	if err := RunAfter(u, a); err == nil || !strings.Contains(err.Error(), "gibtsnicht") {
		t.Errorf("fehlende Quelle: %v", err)
	}
}
