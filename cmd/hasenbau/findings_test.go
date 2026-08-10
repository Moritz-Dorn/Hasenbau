package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Moritz-Dorn/Hasenbau/internal/store"
)

// Hasenbau-4cx.2: findings läuft ohne opencode-Server und ohne Modell.
func TestFindingsOhneServer(t *testing.T) {
	bau := t.TempDir()
	st, err := store.Open(filepath.Join(bau, "state", "hasenbau.db"))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		id, err := st.StartLauf("pdf-einlagern", "watch", fmt.Sprintf("sources/%d.pdf", i))
		if err != nil {
			t.Fatal(err)
		}
		if err := st.EndLauf(id, store.LaufResult{Status: "ok", SessionID: "ses"}); err != nil {
			t.Fatal(err)
		}
		if err := st.WriteToolCalls(id, []store.ToolCall{
			{Tool: "read", Args: fmt.Sprintf(`{"path":"sources/%d.pdf"}`, i), Status: "completed"},
			{Tool: "write", Args: `{"path":"raeume/lager/x.md"}`, Status: "completed"},
		}); err != nil {
			t.Fatal(err)
		}
	}
	st.Close()

	var out, errw strings.Builder
	if code := run([]string{"-bau", bau, "findings", "pdf-einlagern"}, &out, &errw); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errw.String())
	}
	for _, muss := range []string{"read → write", "in 3 von 3", "VARIIERT"} {
		if !strings.Contains(out.String(), muss) {
			t.Errorf("Ausgabe ohne %q:\n%s", muss, out.String())
		}
	}

	// -json für den Gang-Einsatz.
	out.Reset()
	if code := run([]string{"-bau", bau, "findings", "-json", "pdf-einlagern"}, &out, &errw); code != 0 {
		t.Fatalf("json: exit %d, stderr %q", code, errw.String())
	}
	var report struct {
		Auftrag  string `json:"auftrag"`
		Laeufe   int    `json:"laeufe"`
		Findings []struct {
			Kind   string  `json:"kind"`
			Laeufe []int64 `json:"laeufe"`
			Steps  []struct {
				Tool   string `json:"tool"`
				Varies bool   `json:"varies"`
			} `json:"steps"`
		} `json:"findings"`
	}
	if err := json.Unmarshal([]byte(out.String()), &report); err != nil {
		t.Fatalf("json unlesbar: %v\n%s", err, out.String())
	}
	if report.Auftrag != "pdf-einlagern" || report.Laeufe != 3 {
		t.Errorf("report = %+v", report)
	}
	if len(report.Findings) == 0 || len(report.Findings[0].Laeufe) != 3 {
		t.Errorf("Befund ohne Lauf-Bezug: %+v", report.Findings)
	}
	if s := report.Findings[0].Steps; len(s) != 2 || !s[0].Varies || s[1].Varies {
		t.Errorf("Schritte = %+v", s)
	}
}

func TestFindingsFehlerpfade(t *testing.T) {
	var out, errw strings.Builder
	if code := run([]string{"-bau", t.TempDir(), "findings"}, &out, &errw); code != 2 {
		t.Errorf("ohne Auftrag: exit %d, erwartet 2", code)
	}
	// Ein unbekannter Auftrag ist kein Fehler: er hat eben keine Läufe.
	out.Reset()
	if code := run([]string{"-bau", t.TempDir(), "findings", "gibtsnicht"}, &out, &errw); code != 0 {
		t.Errorf("exit %d, erwartet 0", code)
	}
	if !strings.Contains(out.String(), "Keine ausgewerteten Läufe") {
		t.Errorf("Ausgabe: %q", out.String())
	}
}
