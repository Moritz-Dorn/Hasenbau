package runner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Moritz-Dorn/Hasenbau/internal/auftrag"
	"github.com/Moritz-Dorn/Hasenbau/internal/lauf"
)

func testAuftrag(gaenge ...auftrag.Gang) *auftrag.Auftrag {
	return &auftrag.Auftrag{
		Name:    "test",
		Hase:    "archivar",
		Trigger: auftrag.Trigger{Watch: "raeume/eingang/*.txt"},
		Gaenge:  gaenge,
		Raeume: map[string]string{
			"input":      "raeume/eingang/",
			"work":       "raeume/work/",
			"quarantine": "raeume/quarantaene/",
		},
	}
}

func testUmgebung(t *testing.T, a *auftrag.Auftrag) *lauf.Environment {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "raeume/eingang"), 0o755); err != nil {
		t.Fatal(err)
	}
	input := "raeume/eingang/doc.txt"
	if err := os.WriteFile(filepath.Join(root, input), []byte("material"), 0o644); err != nil {
		t.Fatal(err)
	}
	u, err := lauf.Neue(root, a, "lauf-001", input)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestGaengeSequenziellMitLogs(t *testing.T) {
	a := testAuftrag(
		auftrag.Gang{Name: "eins", Run: `echo "sehe $TRIGGER_FILE"`},
		auftrag.Gang{Name: "zwei", Run: `cat $TRIGGER_FILE > $WORK/kopie.txt`},
	)
	u := testUmgebung(t, a)

	logs, err := RunGaenge(context.Background(), u, a, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 2 {
		t.Fatalf("logs = %v", logs)
	}

	raw, err := os.ReadFile(filepath.Join(u.Bau, logs[0]))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "sehe raeume/eingang/doc.txt") {
		t.Errorf("log eins = %q", raw)
	}
	kopie, err := os.ReadFile(filepath.Join(u.Bau, u.Work, "kopie.txt"))
	if err != nil || string(kopie) != "material" {
		t.Errorf("Gang zwei lief nicht mit CWD=Bau: %q, %v", kopie, err)
	}
}

func TestGangFehlerBrichtAbUndQuarantaene(t *testing.T) {
	a := testAuftrag(
		auftrag.Gang{Name: "kaputt", Run: `echo "au weia" >&2; exit 3`},
		auftrag.Gang{Name: "nie", Run: `touch $WORK/nie.txt`},
	)
	u := testUmgebung(t, a)

	logs, err := RunGaenge(context.Background(), u, a, time.Minute)
	var gf *GangError
	if !errors.As(err, &gf) {
		t.Fatalf("erwartete GangError, bekam %v", err)
	}
	if gf.Gang != "kaputt" || gf.Grund != "exit 3" {
		t.Errorf("GangError = %+v", gf)
	}

	// stderr steht im Log, auch im Fehlerfall.
	raw, _ := os.ReadFile(filepath.Join(u.Bau, logs[0]))
	if !strings.Contains(string(raw), "au weia") {
		t.Errorf("stderr fehlt im Log: %q", raw)
	}
	// Der zweite Gang lief nie.
	if _, err := os.Stat(filepath.Join(u.Bau, u.Work, "nie.txt")); err == nil {
		t.Error("Gang nach Fehler wurde trotzdem ausgeführt")
	}
	// Input in quarantaene/, nicht am Ursprung, nirgendwo ein archiv/.
	if gf.Quarantaene == "" || !strings.HasPrefix(gf.Quarantaene, "raeume/quarantaene/") {
		t.Errorf("Quarantaene = %q", gf.Quarantaene)
	}
	if _, err := os.Stat(filepath.Join(u.Bau, gf.Quarantaene)); err != nil {
		t.Errorf("Input nicht in Quarantäne: %v", err)
	}
	if _, err := os.Stat(filepath.Join(u.Bau, u.TriggerFile)); err == nil {
		t.Error("Input liegt noch am Ursprung, obwohl verschoben gemeldet")
	}
}

func TestGangFehlerOhneQuarantaeneRaum(t *testing.T) {
	a := testAuftrag(auftrag.Gang{Name: "kaputt", Run: "exit 1"})
	delete(a.Raeume, "quarantine")
	u := testUmgebung(t, a)

	_, err := RunGaenge(context.Background(), u, a, time.Minute)
	var gf *GangError
	if !errors.As(err, &gf) {
		t.Fatalf("erwartete GangError, bekam %v", err)
	}
	if gf.Quarantaene != "" {
		t.Errorf("Quarantaene = %q ohne quarantaene-Raum", gf.Quarantaene)
	}
	// §7: Der Input bleibt dann am Ursprung — niemals weg.
	if _, err := os.Stat(filepath.Join(u.Bau, u.TriggerFile)); err != nil {
		t.Errorf("Input verschwunden: %v", err)
	}
}

// TestManuellKeineQuarantaene: bei manual ist der Auslöser ein Argument,
// kein Pfad. Selbst wenn zufällig eine gleichnamige Datei im Bau liegt,
// darf ein Gang-Fehler sie nicht wegtragen.
func TestManuellKeineQuarantaene(t *testing.T) {
	a := &auftrag.Auftrag{
		Name: "baumeister", Hase: "baumeister",
		Trigger: auftrag.Trigger{Manual: true},
		Gaenge:  []auftrag.Gang{{Name: "kaputt", Run: "exit 1"}},
		Raeume: map[string]string{
			"work":       "raeume/work/",
			"quarantine": "raeume/quarantaene/",
		},
	}
	root := t.TempDir()
	// Die Falle: eine Datei, die genauso heißt wie das Argument.
	if err := os.WriteFile(filepath.Join(root, "8"), []byte("unbeteiligt"), 0o644); err != nil {
		t.Fatal(err)
	}
	u, err := lauf.Neue(root, a, "lauf-001", "8")
	if err != nil {
		t.Fatal(err)
	}

	_, err = RunGaenge(context.Background(), u, a, time.Minute)
	var gf *GangError
	if !errors.As(err, &gf) {
		t.Fatalf("erwartete GangError, bekam %v", err)
	}
	if gf.Quarantaene != "" {
		t.Errorf("Quarantaene = %q bei manuell-Trigger", gf.Quarantaene)
	}
	if _, err := os.Stat(filepath.Join(root, "8")); err != nil {
		t.Errorf("unbeteiligte Datei wurde weggetragen: %v", err)
	}
}

func TestGangTimeoutKilltProzessgruppe(t *testing.T) {
	a := testAuftrag(auftrag.Gang{Name: "schlaefer", Run: "sleep 30", Timeout: 200 * time.Millisecond})
	u := testUmgebung(t, a)

	start := time.Now()
	_, err := RunGaenge(context.Background(), u, a, time.Minute)
	dauer := time.Since(start)

	var gf *GangError
	if !errors.As(err, &gf) {
		t.Fatalf("erwartete GangError, bekam %v", err)
	}
	if !strings.Contains(gf.Grund, "timeout nach") {
		t.Errorf("Grund = %q", gf.Grund)
	}
	if dauer > 5*time.Second {
		t.Errorf("Timeout griff nicht: Lauf dauerte %v", dauer)
	}
}

func TestGaengeOhneWorkRaum(t *testing.T) {
	a := testAuftrag(auftrag.Gang{Name: "eins", Run: "true"})
	delete(a.Raeume, "work")
	u := testUmgebung(t, a)

	if _, err := RunGaenge(context.Background(), u, a, time.Minute); err == nil || !strings.Contains(err.Error(), "role work") {
		t.Errorf("ohne work-Raum: %v", err)
	}
}

func TestKeineGaengeKeinWork(t *testing.T) {
	a := testAuftrag()
	delete(a.Raeume, "work")
	u := testUmgebung(t, a)
	if logs, err := RunGaenge(context.Background(), u, a, time.Minute); err != nil || logs != nil {
		t.Errorf("ohne Gänge: logs=%v err=%v", logs, err)
	}
}
