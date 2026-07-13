package auftrag

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// beispiel ist der Referenz-Auftrag aus PLAN.md §6, wörtlich.
const beispiel = `---
trigger:
  watch: raeume/laderampe/sources/*.pdf
  debounce: 5s

gaenge:
  - name: pdf-zu-markdown
    run: gaenge/pdf_to_md.py "$INPUT" --out "$WORK/extrakt.md"
    timeout: 120s

hase: archivar
cwd: raeume/laderampe/

raeume:
  input: raeume/laderampe/sources/
  work:  raeume/laderampe/work/
  out:   raeume/lager/
  done:  raeume/archiv/

kontext:
  - datei: $WORK/extrakt.md
  - letzte_summaries: 3

nachher:
  - move: $INPUT -> raeume/archiv/
---

Der extrahierte Text liegt in ` + "`$WORK/extrakt.md`" + `.
Fasse ihn zusammen, vergib Tags, und lege ihn strukturiert in ` + "`lager/`" + ` ab.
Dateiname: YYYY-MM-DD-<slug>.md
`

func TestParseBeispielAusPlan(t *testing.T) {
	a, err := Parse("pdf-einlagern", []byte(beispiel))
	if err != nil {
		t.Fatal(err)
	}

	if a.Name != "pdf-einlagern" {
		t.Errorf("Name = %q", a.Name)
	}
	if a.Trigger.Watch != "raeume/laderampe/sources/*.pdf" || a.Trigger.Cron != "" {
		t.Errorf("Trigger = %+v", a.Trigger)
	}
	if a.Trigger.Debounce != 5*time.Second {
		t.Errorf("Debounce = %v", a.Trigger.Debounce)
	}
	if len(a.Gaenge) != 1 {
		t.Fatalf("Gaenge = %+v", a.Gaenge)
	}
	g := a.Gaenge[0]
	if g.Name != "pdf-zu-markdown" || g.Timeout != 120*time.Second || !strings.Contains(g.Run, "pdf_to_md.py") {
		t.Errorf("Gang = %+v", g)
	}
	if a.Hase != "archivar" || a.CWD != "raeume/laderampe/" {
		t.Errorf("Hase/CWD = %q/%q", a.Hase, a.CWD)
	}
	if len(a.Raeume) != 4 || a.Raeume["out"] != "raeume/lager/" {
		t.Errorf("Raeume = %+v", a.Raeume)
	}
	if len(a.Kontext) != 2 || a.Kontext[0].Datei != "$WORK/extrakt.md" || a.Kontext[1].LetzteSummaries != 3 {
		t.Errorf("Kontext = %+v", a.Kontext)
	}
	if len(a.Nachher) != 1 || a.Nachher[0] != (Nachher{Aktion: "move", Von: "$INPUT", Nach: "raeume/archiv/"}) {
		t.Errorf("Nachher = %+v", a.Nachher)
	}
	if !strings.HasPrefix(a.Body, "Der extrahierte Text") || strings.HasPrefix(a.Body, "\n") {
		t.Errorf("Body = %q", a.Body)
	}
}

func TestParseCronTrigger(t *testing.T) {
	src := `---
trigger:
  cron: "0 7 * * *"
hase: melder
---
Guten Morgen.
`
	a, err := Parse("morgenpost", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if a.Trigger.Cron != "0 7 * * *" || a.Trigger.Watch != "" {
		t.Errorf("Trigger = %+v", a.Trigger)
	}
}

// ersetze baut Varianten des Beispiel-Auftrags für Fehlerfälle.
func ersetze(t *testing.T, alt, neu string) string {
	t.Helper()
	if !strings.Contains(beispiel, alt) {
		t.Fatalf("Testaufbau: %q nicht im Beispiel", alt)
	}
	return strings.Replace(beispiel, alt, neu, 1)
}

func TestParseFehler(t *testing.T) {
	faelle := []struct {
		name    string
		src     string
		erwarte string // Substring der Fehlermeldung
	}{
		{"kein Frontmatter", "kein frontmatter", "muss mit \"---\" beginnen"},
		{"Frontmatter offen", "---\nhase: x\nkein Ende", "nicht geschlossen"},
		{"Trigger fehlt", ersetze(t, "trigger:\n  watch: raeume/laderampe/sources/*.pdf\n  debounce: 5s", "trigger:"), "trigger fehlt"},
		{"watch und cron", ersetze(t, "  debounce: 5s", "  cron: \"0 7 * * *\""), "schließen sich aus"},
		{"ungültiger Cron", ersetze(t, "watch: raeume/laderampe/sources/*.pdf\n  debounce: 5s", "cron: \"99 99 * * *\""), "ungültiger cron-Ausdruck"},
		{"debounce bei cron", ersetze(t, "watch: raeume/laderampe/sources/*.pdf", "cron: \"0 7 * * *\""), "debounce gilt nur für watch"},
		{"hase fehlt", ersetze(t, "hase: archivar\n", ""), "hase fehlt"},
		{"unbekanntes Feld", ersetze(t, "hase: archivar", "hase: archivar\nfarbe: braun"), "farbe"},
		{"body fehlt", beispiel[:strings.LastIndex(beispiel, "---")+4], "body fehlt"},
		{"cwd absolut", ersetze(t, "cwd: raeume/laderampe/", "cwd: /etc"), "nicht absolut"},
		{"raum verlässt Bau", ersetze(t, "out:   raeume/lager/", "out:   ../draussen/"), "darf den Bau nicht verlassen"},
		{"watch absolut", ersetze(t, "watch: raeume/laderampe/sources/*.pdf", "watch: /tmp/*.pdf"), "nicht absolut"},
		{"gang ohne run", ersetze(t, "    run: gaenge/pdf_to_md.py \"$INPUT\" --out \"$WORK/extrakt.md\"\n", ""), "run fehlt"},
		{"ungültige Dauer", ersetze(t, "timeout: 120s", "timeout: zwei Minuten"), "ungültige Dauer"},
		{"kontext leer", ersetze(t, "- datei: $WORK/extrakt.md", "- {}"), "braucht datei oder letzte_summaries"},
		{"kontext doppelt", ersetze(t, "- datei: $WORK/extrakt.md", "- {datei: x, letzte_summaries: 2}"), "schließen sich aus"},
		{"summaries null", ersetze(t, "letzte_summaries: 3", "letzte_summaries: 0"), "muss > 0 sein"},
		{"nachher ohne Pfeil", ersetze(t, "- move: $INPUT -> raeume/archiv/", "- move: $INPUT raeume/archiv/"), "VON -> NACH"},
		{"nachher unbekannt", ersetze(t, "- move: $INPUT -> raeume/archiv/", "- verbrenne: $INPUT"), "unbekannte Aktion"},
		{"hase ungültig", ersetze(t, "hase: archivar", "hase: archi/var"), "ungültiger Hasen-Name"},
	}

	for _, f := range faelle {
		t.Run(f.name, func(t *testing.T) {
			_, err := Parse("pdf-einlagern", []byte(f.src))
			if err == nil {
				t.Fatal("Fehler erwartet, bekam nil")
			}
			if !strings.Contains(err.Error(), f.erwarte) {
				t.Errorf("Fehler %q enthält nicht %q", err, f.erwarte)
			}
			if !strings.Contains(err.Error(), "pdf-einlagern") && !strings.Contains(f.name, "Name") {
				t.Errorf("Fehler %q nennt den Auftrag nicht", err)
			}
		})
	}
}

func TestParseLehntUngueltigenNamenAb(t *testing.T) {
	if _, err := Parse("../boese", []byte(beispiel)); err == nil {
		t.Error("Pfad-Traversal im Namen muss fehlschlagen")
	}
}

func TestLoad(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"auftraege", "hasen"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "auftraege", "pdf-einlagern.md"), []byte(beispiel), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "hasen", "archivar.md"), []byte("---\ndescription: Archivar\n---\nDu sortierst ein.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	auftraege, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(auftraege) != 1 || auftraege[0].Name != "pdf-einlagern" {
		t.Fatalf("auftraege = %+v", auftraege)
	}
}

func TestLoadUnbekannterHase(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"auftraege", "hasen"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "auftraege", "pdf-einlagern.md"), []byte(beispiel), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(root)
	if err == nil {
		t.Fatal("unbekannter Hase muss Load scheitern lassen")
	}
	if !strings.Contains(err.Error(), "unbekannter Hase") || !strings.Contains(err.Error(), "archivar") {
		t.Errorf("Fehler = %q", err)
	}
}

func TestLoadLeererBau(t *testing.T) {
	root := t.TempDir()
	auftraege, err := Load(root)
	if err != nil || len(auftraege) != 0 {
		t.Errorf("leerer Bau: auftraege=%v err=%v", auftraege, err)
	}
}
