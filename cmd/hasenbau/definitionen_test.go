package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Moritz-Dorn/Hasenbau/internal/auftrag"
	"github.com/Moritz-Dorn/Hasenbau/internal/hase"
)

// baueDefinitionsBau legt einen Bau mit einem Hasen und zwei Aufträgen
// an — genug, um die Querbezüge zu prüfen, die describe zeigt.
func baueDefinitionsBau(t *testing.T) string {
	t.Helper()
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

	schreibe("hasen/archivar.md", "---\ndescription: Archivar\nmodel: scc/kit.x\n---\nSortiere ein.\n")
	schreibe("hasen/ungenutzt.md", "---\nmodel: scc/kit.y\n---\nNiemand ruft mich.\n")
	schreibe("auftraege/pdf-einlagern.md", `---
trigger:
  watch: raeume/laderampe/sources/*.pdf
  debounce: 5s
gaenge:
  - name: pdf-zu-markdown
    run: python3 gaenge/pdf_to_md.py "$INPUT" --out "$WORK/extrakt.md"
    timeout: 120s
hase: archivar
monitored: true
raeume:
  input: raeume/laderampe/sources/
  work:  raeume/laderampe/work/
  out:   raeume/lager/
context:
  - file: $WORK/extrakt.md
  - last_summaries: 3
after:
  - move: $INPUT -> raeume/archiv/
---
Fasse zusammen.
`)
	schreibe("auftraege/tagesbericht.md", `---
trigger:
  cron: "0 7 * * *"
hase: archivar
raeume:
  out: raeume/berichte/
---
Berichte.
`)
	schreibe("gaenge/pdf_to_md.py", "#!/usr/bin/env python3\n")
	return root
}

func TestGetAuftraege(t *testing.T) {
	root := baueDefinitionsBau(t)
	var out, errw strings.Builder
	if code := run([]string{"-bau", root, "get", "auftraege"}, &out, &errw); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errw.String())
	}
	got := out.String()
	for _, muss := range []string{
		"NAME", "TRIGGER", "HASE", "pdf-einlagern", "watch raeume/laderampe/sources/*.pdf",
		"tagesbericht", "cron 0 7 * * *", "archivar",
	} {
		if !strings.Contains(got, muss) {
			t.Errorf("Ausgabe ohne %q:\n%s", muss, got)
		}
	}
}

func TestGetHasen(t *testing.T) {
	root := baueDefinitionsBau(t)
	var out, errw strings.Builder
	if code := run([]string{"-bau", root, "get", "hasen"}, &out, &errw); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errw.String())
	}
	got := out.String()
	if !strings.Contains(got, "archivar") || !strings.Contains(got, "pdf-einlagern, tagesbericht") {
		t.Errorf("Nutzer-Spalte fehlt:\n%s", got)
	}
	// Ein Hase, den kein Auftrag benutzt, ist ein Befund für sich.
	if !strings.Contains(got, "ungenutzt") {
		t.Errorf("ungenutzter Hase fehlt:\n%s", got)
	}
}

func TestDescribeAuftrag(t *testing.T) {
	root := baueDefinitionsBau(t)
	var out, errw strings.Builder
	if code := run([]string{"-bau", root, "describe", "auftrag", "pdf-einlagern"}, &out, &errw); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errw.String())
	}
	got := out.String()
	for _, muss := range []string{
		"Agent pdf-einlagern__archivar",
		"pdf-zu-markdown", "2m0s", "gaenge/pdf_to_md.py  vorhanden",
		"→ Schreibrecht des Hasen",
		"letzten 3 Summaries", "move $INPUT -> raeume/archiv/",
		"Prompt-Kern", "Noch nie gelaufen",
	} {
		if !strings.Contains(got, muss) {
			t.Errorf("Ausgabe ohne %q:\n%s", muss, got)
		}
	}
	// Der Body wird nicht ausgegeben — describe ist kein cat.
	if strings.Contains(got, "Fasse zusammen.") {
		t.Errorf("describe gibt den Body aus:\n%s", got)
	}
	// input hat kein Schreibrecht, out schon.
	zeile := zeileMit(got, "input ")
	if strings.Contains(zeile, "Schreibrecht") {
		t.Errorf("input als Schreibraum markiert: %q", zeile)
	}

	errw.Reset()
	if code := run([]string{"-bau", root, "describe", "auftrag", "gibtsnicht"}, &out, &errw); code != 1 {
		t.Errorf("unbekannter Auftrag: exit %d, erwartet 1", code)
	}
}

// TestDescribeAuftragFehlendeGangDatei: eine run:-Zeile, die ins Leere
// zeigt, ist ein Fehler, den man erst im Lauf merkt — hier vorher.
func TestDescribeAuftragFehlendeGangDatei(t *testing.T) {
	root := baueDefinitionsBau(t)
	if err := os.Remove(filepath.Join(root, "gaenge", "pdf_to_md.py")); err != nil {
		t.Fatal(err)
	}
	var out, errw strings.Builder
	if code := run([]string{"-bau", root, "describe", "auftrag", "pdf-einlagern"}, &out, &errw); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out.String(), "gaenge/pdf_to_md.py  FEHLT") {
		t.Errorf("fehlende Gang-Datei nicht gemeldet:\n%s", out.String())
	}
}

func TestDescribeHaseZeigtEffektivePermissions(t *testing.T) {
	root := baueDefinitionsBau(t)
	var out, errw strings.Builder
	if code := run([]string{"-bau", root, "describe", "hase", "archivar"}, &out, &errw); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errw.String())
	}
	got := out.String()
	// Der Kern: derselbe Hase, zwei Aufträge, verschiedene Rechte.
	if !strings.Contains(got, "In Auftrag pdf-einlagern") || !strings.Contains(got, "In Auftrag tagesbericht") {
		t.Fatalf("nicht beide Aufträge:\n%s", got)
	}
	if !strings.Contains(got, `"raeume/laderampe/work/**": allow`) ||
		!strings.Contains(got, `"raeume/berichte/**": allow`) {
		t.Errorf("effektive edit-Regeln fehlen:\n%s", got)
	}
	if !strings.Contains(got, `"*": deny`) || !strings.Contains(got, "bash: deny") {
		t.Errorf("Grund-Regeln fehlen:\n%s", got)
	}
	if strings.Contains(got, "Sortiere ein.") {
		t.Errorf("describe gibt den Prompt aus:\n%s", got)
	}

	out.Reset()
	if code := run([]string{"-bau", root, "describe", "hase", "ungenutzt"}, &out, &errw); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out.String(), "Kein Auftrag benutzt diesen Hasen") {
		t.Errorf("ungenutzter Hase nicht erklärt:\n%s", out.String())
	}
}

// TestSchreibRaeumeStimmtMitGenerierungUeberein: die Anzeige „→
// Schreibrecht" ist eine Behauptung über hase.Generiere. Weicht die
// Liste ab, lügt describe — deshalb hier gegeneinander geprüft.
func TestSchreibRaeumeStimmtMitGenerierungUeberein(t *testing.T) {
	rollen := map[string]string{
		"work": "raeume/w/", "out": "raeume/o/",
		"input": "raeume/i/", "done": "raeume/d/", "quarantine": "raeume/q/",
	}
	a := &auftrag.Auftrag{
		Name: "test", Hase: "h", Raeume: rollen,
		Trigger: auftrag.Trigger{Manual: true}, Body: "x",
	}
	t.Setenv("HOME", t.TempDir()) // hase.Generiere fasst nichts an, aber sicher ist sicher
	roh, err := hase.Generiere(a, &hase.Template{Name: "h", Prompt: "x"})
	if err != nil {
		t.Fatal(err)
	}
	agent := string(roh)
	for rolle, pfad := range rollen {
		erlaubt := strings.Contains(agent, `"`+strings.TrimSuffix(pfad, "/")+`/**": allow`)
		if erlaubt != grantsWrite(rolle) {
			t.Errorf("Rolle %q: Generierung erlaubt=%v, grantsWrite=%v",
				rolle, erlaubt, grantsWrite(rolle))
		}
	}
}

func TestGangDateien(t *testing.T) {
	faelle := []struct {
		run  string
		want string
	}{
		{`python3 gaenge/pdf_to_md.py "$INPUT" --out "$WORK/x.md"`, "gaenge/pdf_to_md.py"},
		{`gaenge/x.sh "$INPUT"`, "gaenge/x.sh"},
		{`"gaenge/mit leer.py"`, "gaenge/mit"}, // Grenze der einfachen Regel, bewusst
		{`"$HASENBAU" dig "$INPUT" > "$WORK/trace.md"`, ""},
		{`tr a-z A-Z < "$INPUT"`, ""},
	}
	for _, f := range faelle {
		got := strings.Join(gangFiles(f.run), ",")
		if got != f.want {
			t.Errorf("gangFiles(%q) = %q, erwartet %q", f.run, got, f.want)
		}
	}
}

// zeileMit liefert die erste Zeile, die s enthält.
func zeileMit(text, s string) string {
	for _, z := range strings.Split(text, "\n") {
		if strings.Contains(z, s) {
			return z
		}
	}
	return ""
}

func TestGetUndDescribeGang(t *testing.T) {
	root := baueDefinitionsBau(t)
	// Ein Draft des Baumeisters, den kein Auftrag einträgt.
	if err := os.MkdirAll(filepath.Join(root, "gaenge", "entwurf"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "gaenge", "entwurf", "lager_index.py"),
		[]byte("#!/usr/bin/env python3\n\"\"\"lager_index.py --lager <raum>\n\nStellt einen Index.\n\"\"\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out, errw strings.Builder
	if code := run([]string{"-bau", root, "get", "gaenge"}, &out, &errw); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errw.String())
	}
	got := out.String()
	if !strings.Contains(got, "gaenge/pdf_to_md.py") || !strings.Contains(got, "pdf-einlagern/pdf-zu-markdown") {
		t.Errorf("Benutzung fehlt:\n%s", got)
	}
	if !strings.Contains(got, "Draft, nicht eingetragen") {
		t.Errorf("Draft nicht als solcher markiert:\n%s", got)
	}

	out.Reset()
	if code := run([]string{"-bau", root, "describe", "gang", "pdf_to_md.py"}, &out, &errw); code != 0 {
		t.Fatalf("describe: exit %d, stderr %q", code, errw.String())
	}
	got = out.String()
	for _, muss := range []string{"gaenge/pdf_to_md.py", "Benutzt von", "pdf-einlagern / pdf-zu-markdown", "2m0s"} {
		if !strings.Contains(got, muss) {
			t.Errorf("Ausgabe ohne %q:\n%s", muss, got)
		}
	}

	out.Reset()
	if code := run([]string{"-bau", root, "describe", "gang", "gaenge/entwurf/lager_index.py"}, &out, &errw); code != 0 {
		t.Fatalf("describe Draft: exit %d", code)
	}
	got = out.String()
	if !strings.Contains(got, "Stellt einen Index.") && !strings.Contains(got, "lager_index.py --lager") {
		t.Errorf("Zweck aus dem Docstring fehlt:\n%s", got)
	}
	if !strings.Contains(got, "trag den Gang selbst ein") {
		t.Errorf("Hinweis auf den Einbau fehlt:\n%s", got)
	}
}

// TestGetGaengeToteReferenz: die Datei ist weg, der Auftrag ruft sie
// weiter — der häufigste Fehler beim Umbenennen.
func TestGetGaengeToteReferenz(t *testing.T) {
	root := baueDefinitionsBau(t)
	if err := os.Remove(filepath.Join(root, "gaenge", "pdf_to_md.py")); err != nil {
		t.Fatal(err)
	}
	var out, errw strings.Builder
	if code := run([]string{"-bau", root, "get", "gaenge"}, &out, &errw); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out.String(), "FEHLT") {
		t.Errorf("tote Referenz nicht gemeldet:\n%s", out.String())
	}

	out.Reset()
	if code := run([]string{"-bau", root, "describe", "gang", "gaenge/pdf_to_md.py"}, &out, &errw); code != 0 {
		t.Fatalf("describe: exit %d", code)
	}
	if !strings.Contains(out.String(), "FEHLT") {
		t.Errorf("describe meldet die fehlende Datei nicht:\n%s", out.String())
	}
}
