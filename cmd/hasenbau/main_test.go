package main

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Moritz-Dorn/Hasenbau/internal/store"
)

func TestUnbekannterBefehlUndUsage(t *testing.T) {
	var out, errw strings.Builder
	if code := run([]string{"quatsch"}, &out, &errw); code != 2 {
		t.Errorf("exit %d, erwartet 2", code)
	}
	if !strings.Contains(errw.String(), "unbekannter Befehl") {
		t.Errorf("Fehlermeldung fehlt: %q", errw.String())
	}
	errw.Reset()
	if code := run(nil, &out, &errw); code != 2 || !strings.Contains(errw.String(), "Befehle:") {
		t.Errorf("ohne Argumente: exit %d, usage %q", code, errw.String())
	}
}

func TestLaufUnbekannterAuftrag(t *testing.T) {
	// Leerer Bau: der Auftrag existiert nicht — sauberer Fehler, bevor
	// irgendein Server gestartet wird.
	var out, errw strings.Builder
	if code := run([]string{"-bau", t.TempDir(), "lauf", "pdf-einlagern"}, &out, &errw); code != 1 {
		t.Errorf("exit %d, erwartet 1", code)
	}
	if !strings.Contains(errw.String(), "unbekannter Auftrag") {
		t.Errorf("Fehlermeldung fehlt: %q", errw.String())
	}
}

func TestGrabenFehlerpfade(t *testing.T) {
	bau := t.TempDir()

	// Unbekannter Lauf: klarer Fehler, bevor irgendein Server startet.
	var out, errw strings.Builder
	if code := run([]string{"-bau", bau, "graben", "7"}, &out, &errw); code != 1 {
		t.Errorf("unbekannter Lauf: exit %d, erwartet 1", code)
	}
	if !strings.Contains(errw.String(), "kein Lauf mit ID 7") {
		t.Errorf("Fehlermeldung fehlt: %q", errw.String())
	}

	// Lauf ohne Session (Gang scheiterte vor dem Hasen): klarer Fehler.
	st, err := store.Open(filepath.Join(bau, "state", "hasenbau.db"))
	if err != nil {
		t.Fatal(err)
	}
	id, err := st.LaufBeginne("pdf-einlagern", "watch", "kaputt.pdf")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.LaufBeende(id, store.LaufErgebnis{Status: "fehler", Fehler: "gang kaputt"}); err != nil {
		t.Fatal(err)
	}
	st.Close()

	errw.Reset()
	if code := run([]string{"-bau", bau, "graben", "1"}, &out, &errw); code != 1 {
		t.Errorf("Lauf ohne Session: exit %d, erwartet 1", code)
	}
	if !strings.Contains(errw.String(), "hat keine Session") {
		t.Errorf("Fehlermeldung fehlt: %q", errw.String())
	}

	// Kaputte Lauf-ID.
	errw.Reset()
	if code := run([]string{"-bau", bau, "graben", "vier"}, &out, &errw); code != 2 {
		t.Errorf("ungültige ID: exit %d, erwartet 2", code)
	}
}

// TestGrabenAusDerDBOhneServer: seit die Läufe ihren Trace ablegen,
// braucht graben keinen opencode mehr. Der leere PATH ist der Beweis —
// käme der Weg über den Server, fände sich kein Binary.
func TestGrabenAusDerDBOhneServer(t *testing.T) {
	bau := t.TempDir()
	dbFile := filepath.Join(bau, "state", "hasenbau.db")

	st, err := store.Open(dbFile)
	if err != nil {
		t.Fatal(err)
	}
	id, err := st.LaufBeginne("notiz-einlagern", "manuell", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.LaufBeende(id, store.LaufErgebnis{Status: "ok", SessionID: "ses_t", Summary: "abgelegt"}); err != nil {
		t.Fatal(err)
	}
	roh := []byte(`{"session_id":"ses_t","schritte":[` +
		`{"art":"reasoning","rolle":"assistant","text":"Erst lesen."},` +
		`{"art":"tool","rolle":"assistant","tool":"write","status":"completed","input":"{\"filePath\":\"raeume/lager/x.md\"}"}]}`)
	if err := st.TraceSchreibe(id, "ses_t", roh); err != nil {
		t.Fatal(err)
	}
	st.Close()

	t.Setenv("PATH", "")
	var out, errw strings.Builder
	if code := run([]string{"-bau", bau, "graben", "1"}, &out, &errw); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errw.String())
	}
	for _, muss := range []string{"Trace Lauf 1", "notiz-einlagern", "[tool write — completed]", "raeume/lager/x.md"} {
		if !strings.Contains(out.String(), muss) {
			t.Errorf("Ausgabe ohne %q:\n%s", muss, out.String())
		}
	}
}

func TestGetLaeufeUndStatus(t *testing.T) {
	bau := t.TempDir()

	var out, errw strings.Builder
	if code := run([]string{"-bau", bau, "get", "laeufe"}, &out, &errw); code != 0 {
		t.Fatalf("get laeufe (leer): exit %d, stderr %q", code, errw.String())
	}
	if !strings.Contains(out.String(), "keine Läufe") {
		t.Errorf("Leer-Ausgabe: %q", out.String())
	}

	// Einen Lauf direkt einfügen — hier zählt nur der Lesepfad.
	seed(t, filepath.Join(bau, "state", "hasenbau.db"))

	out.Reset()
	if code := run([]string{"-bau", bau, "get", "laeufe"}, &out, &errw); code != 0 {
		t.Fatalf("get laeufe: exit %d, stderr %q", code, errw.String())
	}
	if !strings.Contains(out.String(), "pdf-einlagern") || !strings.Contains(out.String(), "watch") {
		t.Errorf("Lauf fehlt in Ausgabe: %q", out.String())
	}

	out.Reset()
	if code := run([]string{"-bau", bau, "status"}, &out, &errw); code != 0 {
		t.Fatalf("status: exit %d, stderr %q", code, errw.String())
	}
	got := out.String()
	if !strings.Contains(got, "1 gesamt") || !strings.Contains(got, "1 ok") {
		t.Errorf("Status-Zähler fehlen: %q", got)
	}
	if !strings.Contains(got, "FEHLERSERIE") || !strings.Contains(got, "pdf-einlagern") {
		t.Errorf("Auftrag-Zustand fehlt: %q", got)
	}
}

// TestVerwaisteZeileWirdBeimStartAufgeraeumt bildet den Fall nach, für
// den es das Kriterium gibt: der Daemon starb mitten im Lauf, seine
// Zeile steht noch auf 'laeuft'. Der nächste Prozess, der selbst Läufe
// anlegt, muss sie schließen — hier `lauf`, weil es dafür (anders als
// `daemon`) keinen opencode-Server braucht.
func TestVerwaisteZeileWirdBeimStartAufgeraeumt(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skipf("ohne /proc kein Lebendkriterium (GOOS=%s)", runtime.GOOS)
	}
	bau := t.TempDir()
	dbFile := filepath.Join(bau, "state", "hasenbau.db")
	seed(t, dbFile)

	// Ein Prozess, den es nicht mehr gibt — die Leiche des Daemons.
	cmd := exec.Command("/bin/sh", "-c", "exit 0")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	totePID := cmd.Process.Pid
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open("sqlite", "file:"+dbFile)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO laeufe (auftrag, "trigger", gestartet, status, pid, pid_gestartet)
		VALUES ('tagesbericht', 'cron', datetime('now','-1 hour'), 'laeuft', ?, datetime('now','-1 hour'))`,
		totePID); err != nil {
		t.Fatal(err)
	}
	db.Close()

	var out, errw strings.Builder
	// Der Auftrag existiert nicht — aufgeräumt wird trotzdem, es
	// passiert vor allem anderen.
	if code := run([]string{"-bau", bau, "lauf", "tagesbericht"}, &out, &errw); code != 1 {
		t.Fatalf("exit %d, erwartet 1 (unbekannter Auftrag), stderr %q", code, errw.String())
	}
	if !strings.Contains(errw.String(), "aufgeräumt") {
		t.Errorf("keine Log-Zeile zum aufgeräumten Lauf: %q", errw.String())
	}

	out.Reset()
	if code := run([]string{"-bau", bau, "status"}, &out, &errw); code != 0 {
		t.Fatalf("status: exit %d", code)
	}
	if strings.Contains(out.String(), "laeuft") {
		t.Errorf("Status zählt immer noch einen laufenden Lauf: %q", out.String())
	}
	if !strings.Contains(out.String(), "1 abgebrochen") {
		t.Errorf("abgebrochener Lauf fehlt im Status: %q", out.String())
	}
}

func seed(t *testing.T, dbFile string) {
	t.Helper()
	st, err := store.Open(dbFile) // legt Schema an
	if err != nil {
		t.Fatal(err)
	}
	st.Close()

	db, err := sql.Open("sqlite", "file:"+dbFile)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`
		INSERT INTO laeufe (auftrag, "trigger", gestartet, beendet, status, summary)
		VALUES ('pdf-einlagern', 'watch', datetime('now','-2 minutes'), datetime('now'), 'ok', 'Rechnung einsortiert')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO auftrag_state (auftrag, letzter_lauf, letzter_ok, fehler_serie)
		VALUES ('pdf-einlagern', datetime('now'), datetime('now'), 0)`); err != nil {
		t.Fatal(err)
	}
}

// baueProviderBau legt einen Bau mit Gerüst-Config und eine geteilte
// auth.json an; der Endpoint zeigt auf einen Test-Server.
func baueProviderBau(t *testing.T, endpoint string) string {
	t.Helper()
	root := t.TempDir()
	conf := filepath.Join(root, ".opencode-home", "opencode", "opencode.json")
	if err := os.MkdirAll(filepath.Dir(conf), 0o755); err != nil {
		t.Fatal(err)
	}
	inhalt := `{"plugin":[],"provider":{"scc":{"npm":"@ai-sdk/openai-compatible",` +
		`"options":{"baseURL":"` + endpoint + `"},"models":{"alt":{"name":"Alt"}}}}}`
	if err := os.WriteFile(conf, []byte(inhalt), 0o644); err != nil {
		t.Fatal(err)
	}

	daten := t.TempDir()
	t.Setenv("XDG_DATA_HOME", daten)
	if err := os.MkdirAll(filepath.Join(daten, "opencode"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(daten, "opencode", "auth.json"),
		[]byte(`{"scc":{"type":"api","key":"k"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestProviderFetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":[{"id":"neu","name":"Neu","connection_type":"local"}]}`))
	}))
	defer srv.Close()
	root := baueProviderBau(t, srv.URL)
	conf := filepath.Join(root, ".opencode-home", "opencode", "opencode.json")
	vorher, err := os.ReadFile(conf)
	if err != nil {
		t.Fatal(err)
	}

	// Ohne Bestätigung wird nichts geschrieben.
	var out, errw strings.Builder
	if code := cmdProvider(root, []string{"fetch", "scc"}, strings.NewReader("n\n"), &out, &errw); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errw.String())
	}
	if !strings.Contains(out.String(), "+ neu") || !strings.Contains(out.String(), "- alt") {
		t.Errorf("Diff fehlt: %q", out.String())
	}
	if !strings.Contains(out.String(), "local") {
		t.Errorf("connection_type gehört in den Diff: %q", out.String())
	}
	if !strings.Contains(out.String(), "abgebrochen") {
		t.Errorf("Abbruch nicht gemeldet: %q", out.String())
	}
	nachher, err := os.ReadFile(conf)
	if err != nil {
		t.Fatal(err)
	}
	if string(vorher) != string(nachher) {
		t.Errorf("ohne Bestätigung geschrieben:\n%s", nachher)
	}

	// Mit -yes schreiben, danach ist der Bau auf Stand.
	out.Reset()
	if code := cmdProvider(root, []string{"fetch", "-yes", "scc"}, strings.NewReader(""), &out, &errw); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errw.String())
	}
	if !strings.Contains(out.String(), "geschrieben") {
		t.Errorf("Schreiben nicht gemeldet: %q", out.String())
	}
	b, err := os.ReadFile(conf)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"neu"`) || strings.Contains(string(b), `"alt"`) {
		t.Errorf("models nicht gespiegelt:\n%s", b)
	}

	out.Reset()
	if code := cmdProvider(root, []string{"fetch", "scc"}, strings.NewReader(""), &out, &errw); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errw.String())
	}
	if !strings.Contains(out.String(), "auf Stand") {
		t.Errorf("zweiter Lauf ist nicht idempotent: %q", out.String())
	}
}

func TestProviderAufrufFehler(t *testing.T) {
	var out, errw strings.Builder
	for _, args := range [][]string{nil, {"sync", "scc"}, {"fetch"}, {"fetch", "a", "b"}} {
		errw.Reset()
		if code := cmdProvider(t.TempDir(), args, strings.NewReader(""), &out, &errw); code != 2 {
			t.Errorf("%v: exit %d, erwartet 2", args, code)
		}
		if !strings.Contains(errw.String(), "provider fetch") {
			t.Errorf("%v: Aufruf-Hinweis fehlt: %q", args, errw.String())
		}
	}
}
