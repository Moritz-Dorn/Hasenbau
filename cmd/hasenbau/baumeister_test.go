package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Moritz-Dorn/Hasenbau/internal/auftrag"
	"github.com/Moritz-Dorn/Hasenbau/internal/lauf"
	"github.com/Moritz-Dorn/Hasenbau/internal/runner"
	"github.com/Moritz-Dorn/Hasenbau/internal/store"
)

// baueBinary übersetzt den Hasenbau für Tests, die ihn als
// Unterprozess brauchen — genau das tut der Gang des Baumeisters.
func baueBinary(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("kein go im PATH")
	}
	pfad := filepath.Join(t.TempDir(), "hasenbau")
	cmd := exec.Command("go", "build", "-o", pfad, ".")
	if aus, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("hasenbau bauen: %v\n%s", err, aus)
	}
	return pfad
}

// TestBaumeisterGangZiehtTrace fährt den Gang des ausgelieferten
// Baumeister-Auftrags gegen eine echte DB — ohne opencode und ohne
// Modell. Damit ist alles bis zum LLM-Schritt abgedeckt: die Gang-Zeile
// aus dem Beispiel, $HASENBAU als Unterprozess, die Umleitung nach
// $WORK und dass graben seinen Trace aus der Bau-DB nimmt.
func TestBaumeisterGangZiehtTrace(t *testing.T) {
	binaer := baueBinary(t)
	root := t.TempDir()

	st, err := store.Open(dbPath(root))
	if err != nil {
		t.Fatal(err)
	}
	id, err := st.StartLauf("notiz-einlagern", "watch", "raeume/laderampe/sources/notiz.txt")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.EndLauf(id, store.LaufResult{Status: "ok", SessionID: "ses_t", Summary: "abgelegt"}); err != nil {
		t.Fatal(err)
	}
	roh := []byte(`{"session_id":"ses_t","schritte":[` +
		`{"art":"tool","rolle":"assistant","tool":"read","status":"completed",` +
		`"input":"{\"filePath\":\"raeume/laderampe/sources/notiz.txt\"}"}]}`)
	if err := st.WriteTrace(id, "ses_t", roh); err != nil {
		t.Fatal(err)
	}
	st.Close()

	// Der ausgelieferte Auftrag, unverändert — der Test prüft die
	// Beispiel-Datei, nicht eine Testkopie davon.
	src, err := os.ReadFile(filepath.Join("..", "..", "beispiele", "auftraege", "baumeister.md"))
	if err != nil {
		t.Fatal(err)
	}
	a, err := auftrag.Parse("baumeister", src)
	if err != nil {
		t.Fatal(err)
	}

	u, err := lauf.Neue(root, a, "lauf-001", "1")
	if err != nil {
		t.Fatal(err)
	}
	u.Hasenbau = binaer // im echten Lauf das laufende Binary

	if _, err := runner.FuehreGaengeAus(context.Background(), u, a, time.Minute); err != nil {
		log, _ := os.ReadFile(filepath.Join(root, u.Work, "gang-trace-ziehen.log"))
		t.Fatalf("Gang gescheitert: %v\nlog: %s", err, log)
	}

	trace, err := os.ReadFile(filepath.Join(root, u.Work, "trace.md"))
	if err != nil {
		t.Fatalf("$WORK/trace.md fehlt: %v", err)
	}
	for _, muss := range []string{"Trace Lauf 1", "notiz-einlagern", "[tool read — completed]", "notiz.txt"} {
		if !strings.Contains(string(trace), muss) {
			t.Errorf("Trace ohne %q:\n%s", muss, trace)
		}
	}
}

func TestZielLauf(t *testing.T) {
	st, err := store.Open(dbPath(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// Ein Lauf ohne Session (vor dem Hasen gescheitert) und zwei mit.
	ohne, _ := st.StartLauf("pdf-einlagern", "watch", "a.pdf")
	if err := st.EndLauf(ohne, store.LaufResult{Status: "failed", Error: "gang kaputt"}); err != nil {
		t.Fatal(err)
	}
	alt, _ := st.StartLauf("pdf-einlagern", "watch", "b.pdf")
	if err := st.EndLauf(alt, store.LaufResult{Status: "ok", SessionID: "ses_alt"}); err != nil {
		t.Fatal(err)
	}
	neu, _ := st.StartLauf("pdf-einlagern", "watch", "c.pdf")
	if err := st.EndLauf(neu, store.LaufResult{Status: "ok", SessionID: "ses_neu"}); err != nil {
		t.Fatal(err)
	}

	l, err := zielLauf(st, "2")
	if err != nil || l.ID != alt {
		t.Errorf("Lauf-ID als Ziel: %+v, %v", l, err)
	}
	// Auftragsname: der jüngste Lauf mit Session gewinnt.
	l, err = zielLauf(st, "pdf-einlagern")
	if err != nil || l.ID != neu {
		t.Errorf("Auftrag als Ziel: %+v, %v", l, err)
	}
	// Ein Lauf ohne Session hat nichts zu verdichten.
	if _, err := zielLauf(st, "1"); err == nil || !strings.Contains(err.Error(), "keine Session") {
		t.Errorf("Lauf ohne Session: %v", err)
	}
	if _, err := zielLauf(st, "tagesbericht"); err == nil {
		t.Error("unbekannter Auftrag muss scheitern")
	}
}

func TestBerichteEntwurf(t *testing.T) {
	vorher := map[string]entwurfDatei{
		"gaenge/entwurf/alt.py":   {Groesse: 10, Hash: "aa"},
		"gaenge/entwurf/bleib.py": {Groesse: 5, Hash: "bb"},
	}
	nachher := map[string]entwurfDatei{
		"gaenge/entwurf/alt.py":   {Groesse: 12, Hash: "cc"},
		"gaenge/entwurf/bleib.py": {Groesse: 5, Hash: "bb"},
		"gaenge/entwurf/neu.py":   {Groesse: 42, Hash: "dd"},
	}

	var b strings.Builder
	neu := berichteEntwurf(&b, "gaenge/entwurf/", vorher, nachher)
	if len(neu) != 1 || neu[0] != "gaenge/entwurf/neu.py" {
		t.Errorf("neu = %v", neu)
	}
	for _, muss := range []string{"neu        gaenge/entwurf/neu.py (42 Bytes)", "GEÄNDERT   gaenge/entwurf/alt.py", "unverändert: 1 Datei"} {
		if !strings.Contains(b.String(), muss) {
			t.Errorf("Bericht ohne %q:\n%s", muss, b.String())
		}
	}

	// Nichts geschrieben: das muss dastehen, nicht bloß fehlen.
	b.Reset()
	if neu := berichteEntwurf(&b, "gaenge/entwurf/", vorher, vorher); neu != nil {
		t.Errorf("neu = %v ohne Änderung", neu)
	}
	if !strings.Contains(b.String(), "unverändert — der Baumeister hat nichts geschrieben") {
		t.Errorf("Bericht: %s", b.String())
	}
}

func TestEntwurfStandFehlenderRaum(t *testing.T) {
	// Räume legt der Runner erst beim Lauf an — vorher ist der Raum
	// schlicht leer, kein Fehler.
	stand, err := entwurfStand(t.TempDir(), "gaenge/entwurf/")
	if err != nil || len(stand) != 0 {
		t.Errorf("stand = %v, err = %v", stand, err)
	}
}

func TestPruefeEntwurfFindetSyntaxfehler(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("kein python3 im PATH")
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "gaenge", "entwurf"), 0o755); err != nil {
		t.Fatal(err)
	}
	gut := "gaenge/entwurf/gut.py"
	kaputt := "gaenge/entwurf/kaputt.py"
	if err := os.WriteFile(filepath.Join(root, gut), []byte("import sys\nprint(sys.argv)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, kaputt), []byte("def kaputt(\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	meldungen := strings.Join(pruefeEntwurf(root, []string{gut, kaputt}), "\n")
	if !strings.Contains(meldungen, "Syntax von "+gut+" ist in Ordnung") {
		t.Errorf("gutes Skript: %s", meldungen)
	}
	if !strings.Contains(meldungen, "SYNTAXFEHLER in "+kaputt) {
		t.Errorf("kaputtes Skript: %s", meldungen)
	}
	// Die Meldung muss den Fehler nennen, nicht die Traceback-Kopfzeile.
	if !strings.Contains(meldungen, "SyntaxError") {
		t.Errorf("Meldung ohne den eigentlichen Fehler: %s", meldungen)
	}
	// Und die Prüfung darf nichts im Bau hinterlassen — er ist ein
	// Git-Repo, im Diff soll der Entwurf stehen, nicht sein Bytecode.
	eintraege, err := os.ReadDir(filepath.Join(root, "gaenge", "entwurf"))
	if err != nil {
		t.Fatal(err)
	}
	if len(eintraege) != 2 {
		var namen []string
		for _, e := range eintraege {
			namen = append(namen, e.Name())
		}
		t.Errorf("die Syntaxprüfung hat Spuren hinterlassen: %v", namen)
	}
}

func TestBaumeisterOhneConfigEintrag(t *testing.T) {
	var out, errw strings.Builder
	if code := cmdBaumeister(t.TempDir(), []string{"1"}, &out, &errw); code != 1 {
		t.Errorf("exit %d, erwartet 1", code)
	}
	if !strings.Contains(errw.String(), "nennt keinen baumeister:") {
		t.Errorf("Meldung: %q", errw.String())
	}
}
