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

	inhalt, err := Generiere(beispielAuftrag(t), tpl, Optionen{})
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
		// Der Rückkanal steht zweimal: kurz VOR dem Template-Prompt,
		// ausführlich dahinter. Sonst steht er in einem langen Agenten
		// mitten im Text und geht verloren (§8 Phase 2, Hasenbau-ifg).
		"**Dein Lauf endet mit einem Werkzeug-Aufruf",
		"Du bist der Archivar.",
		"## Rückkanal",
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
	if n := strings.Count(text, "hasenbau_summary"); n < 2 {
		t.Errorf("hasenbau_summary steht %dx im Agenten, erwartet mindestens 2 (Anfang und Ende)", n)
	}

	// Deterministisch: gleicher Input ⇒ gleiche Bytes.
	nochmal, err := Generiere(beispielAuftrag(t), tpl, Optionen{})
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
	inhalt, err := Generiere(a, tpl, Optionen{})
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
	inhalt, err := Generiere(beispielAuftrag(t), tpl, Optionen{})
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
	rel, err := SchreibeAgent(root, a, tpl, Optionen{})
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
	if _, err := Generiere(beispielAuftrag(t), tpl, Optionen{}); err == nil {
		t.Error("Auftrag mit fremdem Hasen muss scheitern")
	}
}

func TestKenntHasenbauBindetMitgeliefertesWissenEin(t *testing.T) {
	root := t.TempDir()
	schreibeTemplate(t, root, "baumeister", "---\nknows_hasenbau: true\n---\nDu bist der Baumeister.\n")

	tpl, err := Lade(root, "baumeister")
	if err != nil {
		t.Fatal(err)
	}
	if len(tpl.Knowledge) != 1 || tpl.Knowledge[0].Origin != "Der Hasenbau" {
		t.Fatalf("Wissen = %+v", tpl.Knowledge)
	}
	// Der Text kommt aus dem Binary — im Bau liegt keine Datei dafür.
	if !strings.Contains(tpl.Knowledge[0].Text, "Bau") || len(tpl.Knowledge[0].Text) < 500 {
		t.Errorf("mitgeliefertes Wissen sieht leer aus: %q", kurzText(tpl.Knowledge[0].Text))
	}

	a := beispielAuftrag(t)
	a.Hase = "baumeister"
	roh, err := Generiere(a, tpl, Optionen{})
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
	if len(tpl.Knowledge) != 0 {
		t.Errorf("Wissen ohne Feld: %+v", tpl.Knowledge)
	}
	roh, err := Generiere(beispielAuftrag(t), tpl, Optionen{})
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
		"---\nknowledge:\n  - doku/haus.md\n  - doku/[ab].md\n---\nSortiere ein.\n")

	tpl, err := Lade(root, "archivar")
	if err != nil {
		t.Fatal(err)
	}
	// Reihenfolge: die Einträge in Template-Reihenfolge, Glob-Treffer
	// sortiert — sonst wechselt der generierte Agent bei jedem Lauf.
	var herkunft []string
	for _, w := range tpl.Knowledge {
		herkunft = append(herkunft, w.Origin)
	}
	if strings.Join(herkunft, ",") != "doku/haus.md,doku/a.md,doku/b.md" {
		t.Errorf("Herkunft = %v", herkunft)
	}

	roh, err := Generiere(beispielAuftrag(t), tpl, Optionen{})
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
			schreibeTemplate(t, root, "archivar", "---\nknowledge:\n"+f.wissen+"\n---\nSortiere ein.\n")
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

// TestGenerierterAgentSchaltetTaskAb nagelt Hasenbau-wiu auf der Ebene
// fest, die ohne LLM-Lauf prüfbar ist: `task` steht als deny im
// permission-Block der generierten Datei.
//
// Seit Hasenbau-8fd steht es dort und nicht mehr in einem eigenen
// `tools:`-Block. Beide Wege entziehen dem Modell das Werkzeug —
// gemessen, nicht angenommen —, und `tools` ist laut opencode-Doku
// deprecated. Ein zweiter Mechanismus für dieselbe Aussage wäre eine
// Stelle mehr, an der die beiden auseinanderlaufen können.
//
// Was dieser Test NICHT zeigt: dass opencode das Feld auch auswertet.
// Über die Endpoints ist das nicht messbar (/agent gibt die Werkzeuge
// nicht zurück, /experimental/tool antwortet je Provider und Modell,
// nicht je Agent). Gezeigt hat es ein echter Lauf mit Gegenprobe; die
// Grenze, die unabhängig davon hält, ist der Sandbox-Wächter.
func TestGenerierterAgentSchaltetTaskAb(t *testing.T) {
	a := &auftrag.Auftrag{
		Name: "einlagern", Hase: "archivar",
		Trigger: auftrag.Trigger{Watch: "*.pdf"},
		Raeume:  map[string]string{"input": "raeume/eingang/", "work": "raeume/werkstatt/"},
		Body:    "Tu was.",
	}
	roh, err := Generiere(a, &Template{Name: "archivar", Prompt: "Du bist der Archivar."}, Optionen{})
	if err != nil {
		t.Fatal(err)
	}
	agent := string(roh)

	kopf, _, ok := strings.Cut(strings.TrimPrefix(agent, "---\n"), "\n---\n")
	if !ok {
		t.Fatalf("kein Frontmatter im generierten Agenten:\n%s", agent)
	}
	for _, name := range []string{"task", "bash", "webfetch", "websearch", "question", "external_directory"} {
		if !strings.Contains(kopf, "\n  "+name+": deny") {
			t.Errorf("permission-Block verbietet %q nicht:\n%s", name, kopf)
		}
	}
	// Das deprecatete Feld darf nicht zurückkommen: zwei Quellen für
	// dieselbe Aussage laufen irgendwann auseinander.
	if strings.Contains(kopf, "\ntools:") {
		t.Errorf("tools:-Block ist wieder da — die Sperre gehört in permission: (Hasenbau-8fd):\n%s", kopf)
	}
	// Der Rückkanal darf nicht mitabgeschaltet werden: seine Werkzeuge
	// registriert opencode erst zur Laufzeit über MCP, eine Whitelist
	// hätte sie still mitgenommen.
	if strings.Contains(kopf, "hasenbau_") {
		t.Errorf("das Frontmatter fasst den Rückkanal an:\n%s", kopf)
	}
}

// TestToolRequestAbsatzHaengtAmRequestsRaum hält die Invariante aus
// Hasenbau-2lq fest: der Prompt verweist genau dann auf
// `hasenbau_tool_request`, wenn es das Werkzeug auch gibt.
//
// Warum das mehr ist als Kosmetik: der Absatz ist die Stelle, die einem
// Hasen den legalen Weg zeigt, wenn ihm etwas fehlt. Zeigt er ins
// Leere, steht der Hase in der Lage aus Hasenbau-wiu — Aufgabe
// unlösbar, kein angebotener Ausweg —, und dort nahm er den Umweg über
// einen Subagenten. Angeboten wird das Werkzeug nur bei gesetztem
// `requests:` (cmd/hasenbau: wunschRaum aus cfg.Requests); gemessen am
// 2026-08-12 liefert `tools/list` ohne den Schlüssel nur notiz und
// summary.
func TestToolRequestAbsatzHaengtAmRequestsRaum(t *testing.T) {
	a := &auftrag.Auftrag{
		Name: "einlagern", Hase: "archivar",
		Trigger: auftrag.Trigger{Watch: "*.pdf"},
		Raeume:  map[string]string{"input": "raeume/eingang/", "work": "raeume/werkstatt/"},
		Body:    "Tu was.",
	}
	tpl := &Template{Name: "archivar", Prompt: "Du bist der Archivar."}

	ohne, err := Generiere(a, tpl, Optionen{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(ohne), "hasenbau_tool_request") {
		t.Errorf("ohne requests-Raum verweist der Prompt trotzdem auf hasenbau_tool_request:\n%s", ohne)
	}

	mit, err := Generiere(a, tpl, Optionen{ToolRequests: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mit), "hasenbau_tool_request") {
		t.Errorf("mit requests-Raum fehlt der Verweis auf hasenbau_tool_request:\n%s", mit)
	}

	// Der übrige Rückkanal bleibt in beiden Lagen stehen — abgeschaltet
	// wird nur der eine Absatz, nicht die Abschlusshandlung des Laufs.
	for _, roh := range [][]byte{ohne, mit} {
		for _, pflicht := range []string{"hasenbau_summary", "hasenbau_notiz"} {
			if !strings.Contains(string(roh), pflicht) {
				t.Errorf("%s fehlt im generierten Agenten", pflicht)
			}
		}
	}
}

// TestWerkzeugFreigabeIstEinschlussliste hält die Zusicherung aus
// Hasenbau-hcs fest: ein Schmied-Werkzeug bekommt nur, wer es im
// Auftrag nennt.
//
// Die Freigabe entsteht über ein VERBOT der übrigen, und das ist kein
// Umweg, sondern nötig: das Bau-Plugin registriert seine Werkzeuge beim
// Server, sichtbar sind sie damit zunächst für jeden Agenten. Gemessen
// am 2026-08-12 — ein plugin-registriertes Werkzeug stand in der
// Werkzeugliste eines Hasen, der nie davon gehört hatte.
func TestWerkzeugFreigabeIstEinschlussliste(t *testing.T) {
	basis := func(tools []string) *auftrag.Auftrag {
		return &auftrag.Auftrag{
			Name: "einlagern", Hase: "archivar",
			Trigger: auftrag.Trigger{Manual: true},
			Raeume:  map[string]string{"work": "raeume/werkstatt/"},
			Tools:   tools,
			Body:    "Tu was.",
		}
	}
	tpl := &Template{Name: "archivar", Prompt: "Du bist der Archivar."}
	imBau := Optionen{
		Tools:       []string{"exif_lesen", "zeilen_zaehlen"},
		ToolsBereit: []string{"exif_lesen", "zeilen_zaehlen"},
	}

	// Genannt: kein Verbot. Nicht genannt: Verbot.
	roh, err := Generiere(basis([]string{"zeilen_zaehlen"}), tpl, imBau)
	if err != nil {
		t.Fatal(err)
	}
	agent := string(roh)
	if strings.Contains(agent, "zeilen_zaehlen: deny") {
		t.Errorf("freigegebenes Werkzeug wird trotzdem verboten:\n%s", agent)
	}
	if !strings.Contains(agent, "exif_lesen: deny") {
		t.Errorf("nicht freigegebenes Werkzeug wird nicht verboten:\n%s", agent)
	}

	// Ohne `tools:` bekommt der Hase keines — die Vorgabe ist nichts,
	// nicht alles.
	roh, err = Generiere(basis(nil), tpl, imBau)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range imBau.Tools {
		if !strings.Contains(string(roh), name+": deny") {
			t.Errorf("ohne tools: fehlt das Verbot für %q:\n%s", name, roh)
		}
	}

	// Ein Werkzeug, das es nicht gibt, ist ein Ladefehler. Sonst merkt
	// man den Tippfehler erst an einem Lauf, in dem der Hase sagt, er
	// habe das Werkzeug nicht — und das sieht aus wie ein Modellfehler.
	_, err = Generiere(basis([]string{"gibtsnicht"}), tpl, imBau)
	if err == nil {
		t.Error("ein unbekanntes Werkzeug im Auftrag ist kein Fehler — der Tippfehler bliebe still")
	} else if !strings.Contains(err.Error(), "gibtsnicht") {
		t.Errorf("Fehler %q nennt das Werkzeug nicht", err)
	}

	// Ein Bau ohne Werkzeuge erzeugt keine leeren Verbote.
	roh, err = Generiere(basis(nil), tpl, Optionen{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(roh), ": deny\n  : deny") {
		t.Errorf("leerer Verbots-Eintrag im Agenten:\n%s", roh)
	}
}

// TestNurEinsatzbereiteWerkzeugeWerdenFreigegeben: ein Auftrag kann ein
// Werkzeug nennen, das niemand gelesen hat oder das seit dem Review
// geaendert wurde. Genannt ist nicht gelesen — es bleibt verboten
// (ValIntent: nur `actual` ist einsatzbereit, Hasenbau-9w6).
//
// Ohne diese Regel waere die ganze Hash-Bindung wirkungslos: wer nach
// dem Review eine Zeile aendert, bekaeme das Werkzeug weiterhin, weil
// sein Name ja im Auftrag steht.
func TestNurEinsatzbereiteWerkzeugeWerdenFreigegeben(t *testing.T) {
	a := &auftrag.Auftrag{
		Name: "einlagern", Hase: "archivar",
		Trigger: auftrag.Trigger{Manual: true},
		Raeume:  map[string]string{"work": "raeume/werkstatt/"},
		Tools:   []string{"geprueft", "geaendert"},
		Body:    "Tu was.",
	}
	tpl := &Template{Name: "archivar", Prompt: "Du bist der Archivar."}

	roh, err := Generiere(a, tpl, Optionen{
		Tools:       []string{"geprueft", "geaendert"},
		ToolsBereit: []string{"geprueft"}, // `geaendert` ist outdated
	})
	if err != nil {
		t.Fatal(err)
	}
	agent := string(roh)
	if strings.Contains(agent, "geprueft: deny") {
		t.Errorf("das einsatzbereite Werkzeug wird verboten:\n%s", agent)
	}
	if !strings.Contains(agent, "geaendert: deny") {
		t.Errorf("ein Werkzeug ohne gueltiges Review wird trotz Nennung freigegeben:\n%s", agent)
	}
}
