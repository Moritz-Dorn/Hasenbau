package lauf

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Moritz-Dorn/Hasenbau/internal/auftrag"
)

func watchAuftrag() *auftrag.Auftrag {
	return &auftrag.Auftrag{
		Name:    "pdf-einlagern",
		Hase:    "archivar",
		Trigger: auftrag.Trigger{Watch: "raeume/laderampe/sources/*.pdf"},
		Raeume: map[string]string{
			"input": "raeume/laderampe/sources/",
			"work":  "raeume/laderampe/work/",
			"out":   "raeume/lager/",
		},
	}
}

func TestNeueLegtRaeumeUndWorkAn(t *testing.T) {
	root := t.TempDir()
	u, err := Neue(root, watchAuftrag(), "lauf-001", "raeume/laderampe/sources/a.pdf")
	if err != nil {
		t.Fatal(err)
	}

	for _, p := range []string{
		"raeume/laderampe/sources",
		"raeume/laderampe/work/lauf-001",
		"raeume/lager",
	} {
		if fi, err := os.Stat(filepath.Join(root, p)); err != nil || !fi.IsDir() {
			t.Errorf("%s fehlt: %v", p, err)
		}
	}
	if u.Work != filepath.Join("raeume/laderampe/work", "lauf-001") {
		t.Errorf("Work = %q", u.Work)
	}

	// Zweiter Lauf bekommt ein eigenes Scratch.
	u2, err := Neue(root, watchAuftrag(), "lauf-002", "raeume/laderampe/sources/b.pdf")
	if err != nil {
		t.Fatal(err)
	}
	if u2.Work == u.Work {
		t.Errorf("Läufe teilen sich $WORK: %q", u2.Work)
	}
}

func TestNeueValidiert(t *testing.T) {
	root := t.TempDir()
	cronAuftrag := &auftrag.Auftrag{Name: "morgenpost", Hase: "melder", Trigger: auftrag.Trigger{Cron: "0 7 * * *"}}

	if _, err := Neue(root, watchAuftrag(), "lauf-001", ""); err == nil || !strings.Contains(err.Error(), "auslösende Datei fehlt") {
		t.Errorf("watch ohne input: %v", err)
	}
	if _, err := Neue(root, cronAuftrag, "lauf-001", "x.pdf"); err == nil || !strings.Contains(err.Error(), "cron-Trigger mit $INPUT") {
		t.Errorf("cron mit input: %v", err)
	}
	if _, err := Neue(root, watchAuftrag(), "../boese", "x.pdf"); err == nil {
		t.Error("Pfad-Traversal in Lauf-ID muss scheitern")
	}
	if _, err := Neue("relativ/pfad", watchAuftrag(), "lauf-001", "x.pdf"); err == nil {
		t.Error("relativer Bau-Root muss scheitern")
	}
}

// TestManuellBindetInputUndHasenbau: bei manuell ist $INPUT das
// übergebene Argument (optional, kein Pfad), und $HASENBAU zeigt auf
// das laufende Binary — ohne das findet ein Gang den Hasenbau nicht,
// wenn der Daemon mit absolutem Pfad gestartet wurde.
func TestManuellBindetInputUndHasenbau(t *testing.T) {
	root := t.TempDir()
	a := &auftrag.Auftrag{
		Name: "baumeister", Hase: "baumeister",
		Trigger: auftrag.Trigger{Manual: true},
		Raeume:  map[string]string{"work": "raeume/baumeister/work/", "out": "gaenge/entwurf/"},
	}

	// Ohne Argument: erlaubt, $INPUT bleibt ungebunden.
	ohne, err := Neue(root, a, "lauf-001", "")
	if err != nil {
		t.Fatal(err)
	}
	if ohne.TriggerArt != auftrag.TriggerManual {
		t.Errorf("TriggerArt = %q", ohne.TriggerArt)
	}
	if _, err := ohne.Ersetze("graben $INPUT"); err == nil {
		t.Error("$INPUT ohne Argument muss ein Fehler sein")
	}

	u, err := Neue(root, a, "lauf-002", "8")
	if err != nil {
		t.Fatal(err)
	}
	zeile, err := u.Ersetze(`"$HASENBAU" graben "$INPUT" > "$WORK/trace.md"`)
	if err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	erwartet := `"` + exe + `" graben "8" > "` +
		filepath.Join("raeume/baumeister/work", "lauf-002") + `/trace.md"`
	if zeile != erwartet {
		t.Errorf("Ersetze:\n got %q\nwant %q", zeile, erwartet)
	}
}

func TestErsetze(t *testing.T) {
	root := t.TempDir()
	u, err := Neue(root, watchAuftrag(), "lauf-001", "raeume/laderampe/sources/a.pdf")
	if err != nil {
		t.Fatal(err)
	}

	// Die Gang-Zeile aus dem §6-Beispiel.
	zeile, err := u.Ersetze(`gaenge/pdf_to_md.py "$INPUT" --out "$WORK/extrakt.md"`)
	if err != nil {
		t.Fatal(err)
	}
	erwartet := `gaenge/pdf_to_md.py "raeume/laderampe/sources/a.pdf" --out "` +
		filepath.Join("raeume/laderampe/work", "lauf-001") + `/extrakt.md"`
	if zeile != erwartet {
		t.Errorf("Ersetze:\n got %q\nwant %q", zeile, erwartet)
	}

	// $BAU ist absolut, $RAUM_<rolle> kommt aus dem Auftrag.
	s, err := u.Ersetze("$BAU|$RAUM_out")
	if err != nil {
		t.Fatal(err)
	}
	if s != root+"|raeume/lager/" {
		t.Errorf("Ersetze = %q", s)
	}
}

func TestErsetzeFehler(t *testing.T) {
	root := t.TempDir()
	u, err := Neue(root, watchAuftrag(), "lauf-001", "raeume/laderampe/sources/a.pdf")
	if err != nil {
		t.Fatal(err)
	}

	faelle := []struct{ eingabe, erwarte string }{
		{"echo $HOME", "unbekannte Variable $HOME"},
		{"cp $RAUM_lager x", `keinen Raum "lager"`},
	}
	for _, f := range faelle {
		if _, err := u.Ersetze(f.eingabe); err == nil || !strings.Contains(err.Error(), f.erwarte) {
			t.Errorf("Ersetze(%q): %v", f.eingabe, err)
		}
	}

	// Kein stilles Leerersetzen: der Fehlerfall liefert leeren String
	// UND einen Fehler, nie den halb substituierten Text.
	s, err := u.Ersetze("$INPUT und $HOME")
	if err == nil || s != "" {
		t.Errorf("teilweise Substitution durchgereicht: %q, %v", s, err)
	}
}

func TestErsetzeUngebundeneVariablen(t *testing.T) {
	root := t.TempDir()
	cronAuftrag := &auftrag.Auftrag{Name: "morgenpost", Hase: "melder", Trigger: auftrag.Trigger{Cron: "0 7 * * *"}}
	u, err := Neue(root, cronAuftrag, "lauf-001", "")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := u.Ersetze("lies $INPUT"); err == nil || !strings.Contains(err.Error(), "$INPUT ist bei watch-Triggern gebunden") {
		t.Errorf("$INPUT bei cron: %v", err)
	}
	if _, err := u.Ersetze("schreib nach $WORK"); err == nil || !strings.Contains(err.Error(), "Rolle work") {
		t.Errorf("$WORK ohne work-Raum: %v", err)
	}
}
