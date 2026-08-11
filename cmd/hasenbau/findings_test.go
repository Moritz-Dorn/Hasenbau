package main

import (
	"encoding/json"
	"fmt"
	"os"
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

// Hasenbau-4cx.3: `monitored: true` bringt die Befunde eines Auftrags
// ungefragt in den Status — und sonst nichts. Der nicht überwachte
// Auftrag bleibt vollständig analysierbar, er wird nur nicht gemeldet.
func TestStatusMeldetNurUeberwachteAuftraege(t *testing.T) {
	root := baueDefinitionsBau(t) // pdf-einlagern überwacht, tagesbericht nicht
	st, err := store.Open(filepath.Join(root, "state", "hasenbau.db"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"pdf-einlagern", "tagesbericht"} {
		for i := 0; i < 3; i++ {
			id, err := st.StartLauf(name, "watch", fmt.Sprintf("sources/%d.pdf", i))
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
	}
	st.Close()

	var out, errw strings.Builder
	if code := run([]string{"-bau", root, "status"}, &out, &errw); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errw.String())
	}
	got := out.String()
	for _, muss := range []string{
		"Überwacht  (1 von 2 Aufträgen)",
		"pdf-einlagern", "2 Befunde über 3 Läufe", "read → write",
		"hasenbau findings <auftrag>",
	} {
		if !strings.Contains(got, muss) {
			t.Errorf("Status ohne %q:\n%s", muss, got)
		}
	}
	// Nur im Abschnitt prüfen: in der Auftrags-Tabelle darüber steht
	// tagesbericht zu Recht.
	abschnitt := got[strings.Index(got, "Überwacht  ("):]
	if i := strings.Index(abschnitt, "\nDie letzten Läufe"); i >= 0 {
		abschnitt = abschnitt[:i]
	}
	if strings.Contains(abschnitt, "tagesbericht") {
		t.Errorf("nicht überwachter Auftrag wird gemeldet:\n%s", abschnitt)
	}

	// Erfasst wird trotzdem alles: derselbe Auftrag, von Hand gefragt.
	out.Reset()
	if code := run([]string{"-bau", root, "findings", "tagesbericht"}, &out, &errw); code != 0 {
		t.Fatalf("findings tagesbericht: exit %d, stderr %q", code, errw.String())
	}
	for _, muss := range []string{"Befunde zu tagesbericht", "3 ausgewertete Läufe", "read → write"} {
		if !strings.Contains(out.String(), muss) {
			t.Errorf("nicht überwachter Auftrag nicht analysierbar (%q):\n%s", muss, out.String())
		}
	}
}

// Ohne überwachten Auftrag schweigt der Status dazu — ein leerer
// Abschnitt wäre eine Zeile, die niemand mehr liest.
func TestStatusOhneUeberwachungSchweigt(t *testing.T) {
	bau := t.TempDir()
	laufMitNotizen(t, bau).Close()

	var out, errw strings.Builder
	if code := run([]string{"-bau", bau, "status"}, &out, &errw); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errw.String())
	}
	if strings.Contains(out.String(), "Überwacht") {
		t.Errorf("Abschnitt ohne überwachte Aufträge:\n%s", out.String())
	}
}

// Hasenbau-do0.4: Ein Deckel, den man nicht sieht, ist von einem
// hängenden Daemon nicht zu unterscheiden. Wer 200 Dateien ablegt und
// abends nachsieht, muss erkennen können: es staut sich, und planmäßig.
func TestStatusZeigtDenRueckstauGedrosselterAuftraege(t *testing.T) {
	root := t.TempDir()
	schreibe := func(rel, inhalt string) {
		pfad := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(pfad), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(pfad, []byte(inhalt), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	schreibe("hasen/archivar.md", "---\nmodel: scc/kit.x\n---\nSortiere ein.\n")
	schreibe("auftraege/pdf-einlagern.md", `---
trigger:
  watch: raeume/eingang/*.pdf
hase: archivar
throttle:
  max: 2
  per: 1h
raeume:
  out: raeume/lager/
---
Lege ab.
`)
	schreibe("auftraege/ungedrosselt.md", `---
trigger:
  cron: "0 7 * * *"
hase: archivar
raeume:
  out: raeume/lager/
---
Berichte.
`)
	for i := 0; i < 7; i++ {
		schreibe(fmt.Sprintf("raeume/eingang/%d.pdf", i), "material")
	}

	// Das Fenster ist voll: zwei Läufe in der letzten Stunde.
	st, err := store.Open(filepath.Join(root, "state", "hasenbau.db"))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		id, err := st.StartLauf("pdf-einlagern", "watch", "x.pdf")
		if err != nil {
			t.Fatal(err)
		}
		if err := st.EndLauf(id, store.LaufResult{Status: "ok"}); err != nil {
			t.Fatal(err)
		}
	}
	st.Close()

	var out, errw strings.Builder
	if code := run([]string{"-bau", root, "status"}, &out, &errw); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errw.String())
	}
	got := out.String()
	for _, muss := range []string{
		"\nGedrosselt (1)",
		"pdf-einlagern", "2 Läufe je 1h",
		"7 Dateien im Eingang",
		"nächster Lauf frühestens",
	} {
		if !strings.Contains(got, muss) {
			t.Errorf("Status ohne %q:\n%s", muss, got)
		}
	}
	// Der ungedrosselte Auftrag gehört nicht in den Abschnitt.
	abschnitt := got[strings.Index(got, "\nGedrosselt ("):]
	if i := strings.Index(abschnitt, "\nÜberwacht"); i >= 0 {
		abschnitt = abschnitt[:i]
	}
	if strings.Contains(abschnitt, "ungedrosselt") {
		t.Errorf("ungedrosselter Auftrag im Abschnitt:\n%s", abschnitt)
	}

	// Und ohne gedrosselten Auftrag schweigt der Status dazu.
	if err := os.Remove(filepath.Join(root, "auftraege", "pdf-einlagern.md")); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if code := run([]string{"-bau", root, "status"}, &out, &errw); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errw.String())
	}
	// Auf die Abschnitts-Überschrift prüfen, nicht auf das Wort: der
	// Temp-Pfad dieses Tests trägt den Testnamen und damit selbst ein
	// „Gedrosselt" — die Zeile „Bau: …" reichte als Fehlalarm.
	if strings.Contains(out.String(), "\nGedrosselt (") {
		t.Errorf("Abschnitt ohne gedrosselte Aufträge:\n%s", out.String())
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

func TestSelektorParsen(t *testing.T) {
	faelle := []struct {
		s       string
		auftrag string
		nr      int
		ok      bool
	}{
		{"pdf-einlagern#2", "pdf-einlagern", 2, true},
		{"a#1", "a", 1, true},
		{"12", "", 0, false},              // Lauf-ID
		{"pdf-einlagern", "", 0, false},   // nur der Auftrag
		{"pdf-einlagern#0", "", 0, false}, // Befunde zählen ab 1
		{"pdf-einlagern#x", "", 0, false},
		{"mit/slash#1", "", 0, false},
		{"#1", "", 0, false},
	}
	for _, f := range faelle {
		sel, ok := parseSelector(f.s)
		if ok != f.ok {
			t.Errorf("parseSelector(%q): ok=%v, erwartet %v", f.s, ok, f.ok)
			continue
		}
		if ok && (sel.Auftrag != f.auftrag || sel.Nr != f.nr) {
			t.Errorf("parseSelector(%q) = %+v", f.s, sel)
		}
		if ok && sel.String() != f.s {
			t.Errorf("String() = %q, erwartet %q", sel.String(), f.s)
		}
	}
}

// Hasenbau-4cx.4: `dig <auftrag>#<n>` liefert das Material für Stufe 2
// — den gerechneten Befund und die Traces, auf denen er beruht.
func TestDigBefundLiefertBefundUndTraces(t *testing.T) {
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
		roh := []byte(fmt.Sprintf(`{"session_id":"ses","steps":[{"kind":"tool","role":"assistant",`+
			`"tool":"read","status":"completed","input":"{\"path\":\"sources/%d.pdf\"}"}]}`, i))
		if err := st.WriteTrace(id, "ses", roh); err != nil {
			t.Fatal(err)
		}
	}
	st.Close()

	var out, errw strings.Builder
	if code := run([]string{"-bau", bau, "dig", "pdf-einlagern#1"}, &out, &errw); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errw.String())
	}
	got := out.String()
	for _, muss := range []string{
		"# Befund 1 zu pdf-einlagern", "deterministisch, ohne Modell",
		"read → write", "VARIIERT",
		"Die Läufe, auf denen der Befund beruht", "## Lauf 3", "[tool read — completed]",
	} {
		if !strings.Contains(got, muss) {
			t.Errorf("Material ohne %q:\n%s", muss, got)
		}
	}
	// Der Schlusssatz an den Menschen gehört nicht ins Material des Hasen.
	if strings.Contains(got, "Nichts davon ist beschlossen") {
		t.Errorf("Listen-Schlusssatz im Einzelbefund:\n%s", got)
	}

	// Eine Nummer, die es nicht gibt, sagt was es gibt.
	errw.Reset()
	if code := run([]string{"-bau", bau, "dig", "pdf-einlagern#99"}, &out, &errw); code != 1 {
		t.Errorf("exit %d, erwartet 1", code)
	}
	if !strings.Contains(errw.String(), "hasenbau findings pdf-einlagern") {
		t.Errorf("Meldung ohne Wegweiser: %q", errw.String())
	}
}
