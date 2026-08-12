package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Moritz-Dorn/Hasenbau/internal/store"
)

// probeBau legt einen Bau an und gibt seinen Pfad zurück.
func probeBau(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	var out, errw strings.Builder
	if code := run([]string{"-bau", dir, "init", dir}, &out, &errw); code != 0 {
		t.Fatalf("init: %d, %s", code, errw.String())
	}
	return dir
}

// laufendenLaufAnlegen simuliert, was der Runner tut, bevor der Hase
// arbeitet: eine Zeile mit status = 'running'.
func laufendenLaufAnlegen(t *testing.T, root string) int64 {
	t.Helper()
	st, err := store.Open(dbPath(root))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	id, err := st.StartLauf("lesetest", "manual", "")
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// TestSandboxVorfallWeistAbUndVerbucht: der Normalfall. Exit 3 sagt dem
// Wächter „abweisen", der Text auf stdout geht als Fehler des Werkzeugs
// an den Hasen — verifiziert an einem echten Lauf am 2026-08-12, dort
// hat der Hase daraufhin den Rückkanal benutzt statt einen anderen Weg
// zu suchen. Deshalb prüft der Test auch, dass der Text den Weg nennt
// und nicht nur „verboten" sagt.
func TestSandboxVorfallWeistAbUndVerbucht(t *testing.T) {
	root := probeBau(t)
	lauf := laufendenLaufAnlegen(t, root)

	var out, errw strings.Builder
	code := run([]string{"-bau", root, "sandbox-vorfall",
		"--tool", "task", "--session", "ses_x", "--args", `{"prompt":"mach was"}`}, &out, &errw)
	if code != 3 {
		t.Fatalf("Exit = %d, erwartet 3 (abweisen); stderr: %s", code, errw.String())
	}
	if !strings.Contains(out.String(), "hasenbau_werkzeug_wunsch") {
		t.Errorf("Abweisung nennt den Weg nicht:\n%s", out.String())
	}

	notizen := notizenLesen(t, root, lauf)
	if len(notizen) != 1 {
		t.Fatalf("Notizen = %v", notizen)
	}
	for _, muss := range []string{`"task"`, "abgewiesen", "mach was", "ses_x"} {
		if !strings.Contains(notizen[0], muss) {
			t.Errorf("Notiz enthält %q nicht: %s", muss, notizen[0])
		}
	}
}

// TestSandboxVorfallWarnLaesstDurch: das Flag aus hasenbau.yaml. Gemeldet
// wird auch hier — der Unterschied ist nur, ob der Aufruf stattfindet.
func TestSandboxVorfallWarnLaesstDurch(t *testing.T) {
	root := probeBau(t)
	schreibeYAML(t, root, "log_level: info\nsandbox: warn\n")
	lauf := laufendenLaufAnlegen(t, root)

	var out, errw strings.Builder
	if code := run([]string{"-bau", root, "sandbox-vorfall", "--tool", "bash"}, &out, &errw); code != 0 {
		t.Fatalf("Exit = %d, erwartet 0 (durchlassen); stderr: %s", code, errw.String())
	}
	if out.String() != "" {
		t.Errorf("warn schreibt einen Abweisungstext: %q", out.String())
	}
	notizen := notizenLesen(t, root, lauf)
	if len(notizen) != 1 || !strings.Contains(notizen[0], "durchgelassen") {
		t.Errorf("Notiz fehlt oder falsch: %v", notizen)
	}
}

// TestSandboxVorfallOhneLauf: kein aktiver Lauf heißt, dass da kein Hase
// war. Das darf nicht still durchgehen — im Bau soll niemand unbemerkt
// an der Sandbox rütteln. Abgewiesen wird trotzdem: die Grenze hängt
// nicht daran, ob sich die Meldung zuordnen lässt.
func TestSandboxVorfallOhneLauf(t *testing.T) {
	root := probeBau(t)

	var out, errw strings.Builder
	if code := run([]string{"-bau", root, "sandbox-vorfall", "--tool", "task"}, &out, &errw); code != 3 {
		t.Fatalf("Exit = %d, erwartet 3", code)
	}
	if !strings.Contains(errw.String(), "ohne laufenden Lauf") {
		t.Errorf("Vorfall ohne Lauf bleibt still: %q", errw.String())
	}
}

func notizenLesen(t *testing.T, root string, lauf int64) []string {
	t.Helper()
	st, err := store.Open(dbPath(root))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	notizen, err := st.Notes(lauf)
	if err != nil {
		t.Fatal(err)
	}
	var texte []string
	for _, n := range notizen {
		texte = append(texte, n.Text)
	}
	return texte
}

func schreibeYAML(t *testing.T, root, inhalt string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "hasenbau.yaml"), []byte(inhalt), 0o644); err != nil {
		t.Fatal(err)
	}
}
