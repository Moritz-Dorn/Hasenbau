package hase

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Moritz-Dorn/Hasenbau/internal/auftrag"
)

const templateArchivar = `---
description: Sortiert Dokumente ins Lager ein
model: scc/kit.glm-5.2-753b
temperature: 0.2
permission:
  edit:
    "raeume/lager/geheim/**": deny
  read:
    "*.env": deny
---
Du bist der Archivar. Fasse zusammen, vergib Tags, lege strukturiert ab.
`

func schreibeTemplate(t *testing.T, root, name, inhalt string) {
	t.Helper()
	dir := filepath.Join(root, "hasen")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".md"), []byte(inhalt), 0o644); err != nil {
		t.Fatal(err)
	}
}

func beispielAuftrag(t *testing.T) *auftrag.Auftrag {
	t.Helper()
	return &auftrag.Auftrag{
		Name: "pdf-einlagern",
		Hase: "archivar",
		Raeume: map[string]string{
			"input": "raeume/laderampe/sources/",
			"work":  "raeume/laderampe/work/",
			"out":   "raeume/lager/",
			"done":  "raeume/archiv/",
		},
	}
}

func TestLadeUndGeneriere(t *testing.T) {
	root := t.TempDir()
	schreibeTemplate(t, root, "archivar", templateArchivar)

	tpl, err := Lade(root, "archivar")
	if err != nil {
		t.Fatal(err)
	}
	if tpl.Model != "scc/kit.glm-5.2-753b" || tpl.Temperature == nil || *tpl.Temperature != 0.2 {
		t.Errorf("Template = %+v", tpl)
	}

	inhalt, err := Generiere(beispielAuftrag(t), tpl)
	if err != nil {
		t.Fatal(err)
	}
	text := string(inhalt)

	// Reihenfolge trägt die Semantik: deny-Basis, Auftrags-Allows,
	// Template-Denies zuletzt (letzte matchende Regel gewinnt, §11.5).
	erwartet := []string{
		`"*": deny`,
		`"raeume/laderampe/work/**": allow`,
		`"raeume/lager/**": allow`,
		`"raeume/lager/geheim/**": deny`,
		"bash: deny",
		"webfetch: deny",
		"websearch: deny",
		"external_directory: deny",
		`"*.env": deny`,
		"Du bist der Archivar.",
		// Der Rückkanal hängt hinter dem Template-Prompt (§8, Phase 2).
		"## Rückkanal",
		"`hasenbau_summary`",
		"`hasenbau_notiz`",
	}
	pos := -1
	for _, e := range erwartet {
		i := strings.Index(text, e)
		if i < 0 {
			t.Fatalf("generierter Agent enthält nicht %q:\n%s", e, text)
		}
		if i < pos {
			t.Fatalf("%q steht vor dem Vorgänger — Reihenfolge falsch:\n%s", e, text)
		}
		pos = i
	}
	if !strings.Contains(text, "mode: primary") {
		t.Error("mode: primary fehlt")
	}

	// Deterministisch: gleicher Input ⇒ gleiche Bytes.
	nochmal, err := Generiere(beispielAuftrag(t), tpl)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(inhalt, nochmal) {
		t.Error("Generiere ist nicht deterministisch")
	}
}

func TestGeneriereOhneSchreibRaeume(t *testing.T) {
	root := t.TempDir()
	schreibeTemplate(t, root, "melder", "---\ndescription: Meldet nur\n---\nBerichte.\n")
	tpl, err := Lade(root, "melder")
	if err != nil {
		t.Fatal(err)
	}
	a := &auftrag.Auftrag{Name: "morgenpost", Hase: "melder"}
	inhalt, err := Generiere(a, tpl)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(inhalt), `"*": deny`) || strings.Contains(string(inhalt), "allow") {
		t.Errorf("Hase ohne Schreib-Räume darf nirgends schreiben:\n%s", inhalt)
	}
}

func TestTemplateDenyVerdraengtBasisAllow(t *testing.T) {
	root := t.TempDir()
	schreibeTemplate(t, root, "archivar", `---
permission:
  edit:
    "raeume/lager/**": deny
---
Prompt.
`)
	tpl, err := Lade(root, "archivar")
	if err != nil {
		t.Fatal(err)
	}
	inhalt, err := Generiere(beispielAuftrag(t), tpl)
	if err != nil {
		t.Fatal(err)
	}
	text := string(inhalt)
	if strings.Contains(text, `"raeume/lager/**": allow`) {
		t.Errorf("Allow muss dem Template-Deny weichen (doppelter YAML-Schlüssel):\n%s", text)
	}
	if !strings.Contains(text, `"raeume/lager/**": deny`) {
		t.Errorf("Template-Deny fehlt:\n%s", text)
	}
}

func TestLadeLehntAllowUndAskAb(t *testing.T) {
	for _, f := range []struct{ name, permission string }{
		{"allow skalar", "bash: allow"},
		{"ask skalar", "webfetch: ask"},
		{"allow pattern", "edit:\n    \"raeume/extra/**\": allow"},
	} {
		t.Run(f.name, func(t *testing.T) {
			root := t.TempDir()
			schreibeTemplate(t, root, "schummler", "---\npermission:\n  "+f.permission+"\n---\nPrompt.\n")
			_, err := Lade(root, "schummler")
			if err == nil {
				t.Fatal("Template mit allow/ask muss scheitern")
			}
			if !strings.Contains(err.Error(), "nur deny") {
				t.Errorf("Fehler = %q", err)
			}
		})
	}
}

func TestLadeLehntUnbekannteFelderAb(t *testing.T) {
	root := t.TempDir()
	schreibeTemplate(t, root, "moechtegern", "---\ndescription: x\nmode: subagent\n---\nPrompt.\n")
	_, err := Lade(root, "moechtegern")
	if err == nil || !strings.Contains(err.Error(), "mode") {
		t.Errorf("unbekanntes Feld muss abgelehnt werden, Fehler = %v", err)
	}
}

func TestSchreibeAgent(t *testing.T) {
	root := t.TempDir()
	schreibeTemplate(t, root, "archivar", templateArchivar)
	tpl, err := Lade(root, "archivar")
	if err != nil {
		t.Fatal(err)
	}

	a := beispielAuftrag(t)
	rel, err := SchreibeAgent(root, a, tpl)
	if err != nil {
		t.Fatal(err)
	}
	if rel != filepath.Join(".opencode-home", "opencode", "agents", "pdf-einlagern__archivar.md") {
		t.Errorf("rel = %q", rel)
	}
	if AgentName(a) != "pdf-einlagern__archivar" {
		t.Errorf("AgentName = %q", AgentName(a))
	}
	if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
		t.Errorf("Agent-Datei fehlt: %v", err)
	}
}

func TestGeneriereLehntFalschesTemplateAb(t *testing.T) {
	root := t.TempDir()
	schreibeTemplate(t, root, "melder", "---\n---\nPrompt.\n")
	tpl, err := Lade(root, "melder")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Generiere(beispielAuftrag(t), tpl); err == nil {
		t.Error("Auftrag mit fremdem Hasen muss scheitern")
	}
}

func TestKenntHasenbauBindetMitgeliefertesWissenEin(t *testing.T) {
	root := t.TempDir()
	schreibeTemplate(t, root, "baumeister", "---\nkennt_hasenbau: true\n---\nDu bist der Baumeister.\n")

	tpl, err := Lade(root, "baumeister")
	if err != nil {
		t.Fatal(err)
	}
	if len(tpl.Wissen) != 1 || tpl.Wissen[0].Herkunft != "Der Hasenbau" {
		t.Fatalf("Wissen = %+v", tpl.Wissen)
	}
	// Der Text kommt aus dem Binary — im Bau liegt keine Datei dafür.
	if !strings.Contains(tpl.Wissen[0].Text, "Bau") || len(tpl.Wissen[0].Text) < 500 {
		t.Errorf("mitgeliefertes Wissen sieht leer aus: %q", kurzText(tpl.Wissen[0].Text))
	}

	a := beispielAuftrag(t)
	a.Hase = "baumeister"
	roh, err := Generiere(a, tpl)
	if err != nil {
		t.Fatal(err)
	}
	agent := string(roh)
	if !strings.Contains(agent, "## Wissen: Der Hasenbau") {
		t.Errorf("Wissen fehlt im Agenten:\n%s", agent)
	}
	// Reihenfolge: erst die Rolle, dann das Nachschlagewerk, dann der
	// Rückkanal.
	rolle := strings.Index(agent, "Du bist der Baumeister.")
	wissen := strings.Index(agent, "## Wissen: Der Hasenbau")
	kanal := strings.Index(agent, "## Rückkanal")
	if !(rolle < wissen && wissen < kanal) {
		t.Errorf("Reihenfolge falsch: Rolle %d, Wissen %d, Rückkanal %d", rolle, wissen, kanal)
	}
}

func TestOhneWissenFelderKeinZusatz(t *testing.T) {
	root := t.TempDir()
	schreibeTemplate(t, root, "archivar", templateArchivar)
	tpl, err := Lade(root, "archivar")
	if err != nil {
		t.Fatal(err)
	}
	if len(tpl.Wissen) != 0 {
		t.Errorf("Wissen ohne Feld: %+v", tpl.Wissen)
	}
	roh, err := Generiere(beispielAuftrag(t), tpl)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(roh), "## Wissen") {
		t.Errorf("Wissen-Abschnitt ohne Anforderung:\n%s", roh)
	}
}

func TestWissenAusEigenenDateien(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "doku"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, inhalt := range map[string]string{
		"doku/haus.md": "Im Haus wird nicht gerannt.",
		"doku/a.md":    "Erstens.",
		"doku/b.md":    "Zweitens.",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(inhalt), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	schreibeTemplate(t, root, "archivar",
		"---\nwissen:\n  - doku/haus.md\n  - doku/[ab].md\n---\nSortiere ein.\n")

	tpl, err := Lade(root, "archivar")
	if err != nil {
		t.Fatal(err)
	}
	// Reihenfolge: die Einträge in Template-Reihenfolge, Glob-Treffer
	// sortiert — sonst wechselt der generierte Agent bei jedem Lauf.
	var herkunft []string
	for _, w := range tpl.Wissen {
		herkunft = append(herkunft, w.Herkunft)
	}
	if strings.Join(herkunft, ",") != "doku/haus.md,doku/a.md,doku/b.md" {
		t.Errorf("Herkunft = %v", herkunft)
	}

	roh, err := Generiere(beispielAuftrag(t), tpl)
	if err != nil {
		t.Fatal(err)
	}
	for _, muss := range []string{"## Wissen: doku/haus.md", "Im Haus wird nicht gerannt.", "Zweitens."} {
		if !strings.Contains(string(roh), muss) {
			t.Errorf("Agent ohne %q", muss)
		}
	}
}

func TestWissenFehlerpfade(t *testing.T) {
	root := t.TempDir()
	faelle := []struct {
		name    string
		wissen  string
		erwarte string
	}{
		{"Datei fehlt", "  - doku/gibtsnicht.md", "keine Datei gefunden"},
		{"verlässt den Bau", "  - ../geheim.md", "darf den Bau nicht verlassen"},
		{"absolut", "  - /etc/passwd", "nicht absolut"},
	}
	for _, f := range faelle {
		t.Run(f.name, func(t *testing.T) {
			schreibeTemplate(t, root, "archivar", "---\nwissen:\n"+f.wissen+"\n---\nSortiere ein.\n")
			_, err := Lade(root, "archivar")
			if err == nil {
				t.Fatal("Fehler erwartet")
			}
			if !strings.Contains(err.Error(), f.erwarte) {
				t.Errorf("Fehler %q enthält nicht %q", err, f.erwarte)
			}
		})
	}
}

func kurzText(s string) string {
	if len(s) > 80 {
		return s[:80] + "…"
	}
	return s
}
