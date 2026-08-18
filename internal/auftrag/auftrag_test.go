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
  watch: "*.pdf"
  debounce: 5s

gaenge:
  - name: pdf-zu-markdown
    run: gaenge/pdf_to_md.py "$TRIGGER_FILE" --out "$WORK/extrakt.md"
    timeout: 120s

hase: archivar

raeume:
  input: raeume/laderampe/sources/
  work:  raeume/laderampe/work/
  out:   raeume/lager/
  done:  raeume/archiv/
  quarantine: raeume/quarantaene/

context:
  - file: $WORK/extrakt.md
  - last_summaries: 3

after:
  - move: $TRIGGER_FILE -> raeume/archiv/
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
	if a.Trigger.Watch != "*.pdf" || a.Trigger.Cron != "" {
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
	if a.Hase != "archivar" {
		t.Errorf("Hase = %q", a.Hase)
	}
	if len(a.Raeume) != 5 || a.Raeume["out"] != "raeume/lager/" || a.Raeume["quarantine"] != "raeume/quarantaene/" {
		t.Errorf("Raeume = %+v", a.Raeume)
	}
	if len(a.Context) != 2 || a.Context[0].File != "$WORK/extrakt.md" || a.Context[1].LastSummaries != 3 {
		t.Errorf("Kontext = %+v", a.Context)
	}
	if len(a.After) != 1 || a.After[0] != (After{Action: "move", From: "$TRIGGER_FILE", To: "raeume/archiv/"}) {
		t.Errorf("Nachher = %+v", a.After)
	}
	if !strings.HasPrefix(a.Body, "Der extrahierte Text") || strings.HasPrefix(a.Body, "\n") {
		t.Errorf("Body = %q", a.Body)
	}
}

// Hasenbau-4cx.3: monitored steuert die Meldung, nicht die Erfassung —
// und fehlt es, wird eben nicht gemeldet.
func TestParseMonitored(t *testing.T) {
	if a, err := Parse("pdf-einlagern", []byte(beispiel)); err != nil {
		t.Fatal(err)
	} else if a.Monitored {
		t.Errorf("ohne Feld überwacht: %+v", a.Monitored)
	}

	src := `---
trigger:
  cron: "0 7 * * *"
hase: melder
monitored: true
---
Guten Morgen.
`
	a, err := Parse("morgenpost", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if !a.Monitored {
		t.Error("monitored: true nicht übernommen")
	}
}

// Hasenbau-do0.2: Der Deckel braucht beide Hälften. Eine Zahl ohne
// Fenster ist keine Rate, ein Fenster ohne Zahl deckelt nichts — beides
// sieht aus wie eine Drossel und ist keine.
func TestParseThrottle(t *testing.T) {
	kopf := func(block string) []byte {
		return []byte("---\ntrigger:\n  watch: \"*.pdf\"\nhase: archivar\nraeume:\n  input: raeume/eingang/\n" + block + "---\nTu was.\n")
	}

	a, err := Parse("einlagern", kopf("throttle:\n  max: 5\n  per: 1h\n"))
	if err != nil {
		t.Fatal(err)
	}
	if a.Throttle.Max != 5 || a.Throttle.Per != time.Hour {
		t.Errorf("Throttle = %+v", a.Throttle)
	}
	if !a.Throttle.An() || a.Throttle.String() != "5 Läufe per 1h" {
		t.Errorf("An=%v String=%q", a.Throttle.An(), a.Throttle)
	}

	// Ohne den Block ist der Auftrag ungedrosselt — der Nullwert trägt das.
	ohne, err := Parse("einlagern", kopf(""))
	if err != nil {
		t.Fatal(err)
	}
	if ohne.Throttle.An() || ohne.Throttle.String() != "ungedrosselt" {
		t.Errorf("ohne throttle: %+v", ohne.Throttle)
	}

	fehlerfaelle := []struct{ block, erwartet string }{
		{"throttle:\n  max: 5\n", "max without per"},
		{"throttle:\n  per: 1h\n", "per without max"},
		{"throttle:\n  max: 5\n  per: 0s\n", "per: 0 is not a window"},
		{"throttle:\n  max: -1\n  per: 1h\n", "max has to be > 0"},
		{"throttle:\n  max: 0\n", "empty"},
	}
	for _, f := range fehlerfaelle {
		_, err := Parse("einlagern", kopf(f.block))
		if err == nil {
			t.Errorf("%q: kein Fehler", f.block)
			continue
		}
		if !strings.Contains(err.Error(), f.erwartet) {
			t.Errorf("%q: Fehler %q enthält nicht %q", f.block, err, f.erwartet)
		}
	}
}

// Hasenbau-do0.3: Das Tagesfenster. Gerechnet wird gegen übergebene
// Zeitpunkte, nie gegen die Wanduhr — sonst hinge der Test daran, zu
// welcher Tageszeit er läuft (vgl. Hasenbau-eav).
func TestWindowContainsUndUntil(t *testing.T) {
	nachts := Window{From: 22 * 60, To: 6 * 60} // über Mitternacht
	tags := Window{From: 9 * 60, To: 17 * 60}   // normal
	um := func(h, m int) time.Time {
		return time.Date(2026, 3, 10, h, m, 0, 0, time.UTC)
	}

	faelle := []struct {
		name   string
		w      Window
		t      time.Time
		drin   bool
		warten time.Duration
	}{
		{"nachts mittendrin", nachts, um(23, 30), true, 0},
		{"nachts nach Mitternacht", nachts, um(2, 0), true, 0},
		{"nachts am Anfang", nachts, um(22, 0), true, 0},
		{"nachts am Ende ist draußen", nachts, um(6, 0), false, 16 * time.Hour},
		{"nachts kurz davor", nachts, um(21, 30), false, 30 * time.Minute},
		{"nachts am Nachmittag", nachts, um(14, 0), false, 8 * time.Hour},
		{"tags mittendrin", tags, um(12, 0), true, 0},
		{"tags am Anfang", tags, um(9, 0), true, 0},
		{"tags am Ende ist draußen", tags, um(17, 0), false, 16 * time.Hour},
		{"tags davor", tags, um(7, 30), false, 90 * time.Minute},
	}
	for _, f := range faelle {
		if drin := f.w.Contains(f.t); drin != f.drin {
			t.Errorf("%s: Contains = %v, erwartet %v", f.name, drin, f.drin)
		}
		if warten := f.w.Until(f.t); warten != f.warten {
			t.Errorf("%s: Until = %v, erwartet %v", f.name, warten, f.warten)
		}
	}

	if !nachts.UeberMitternacht() || tags.UeberMitternacht() {
		t.Error("UeberMitternacht falsch")
	}
	if nachts.String() != "22:00-06:00" {
		t.Errorf("String = %q", nachts.String())
	}
}

// Zeitumstellung: die Nacht auf den 29.03.2026 ist in Berlin 23 Stunden
// lang. Wer bis zum Öffnen einfach 24h addiert, landet eine Stunde
// daneben — deshalb rechnet Until über time.Date.
func TestWindowUeberstehtDieZeitumstellung(t *testing.T) {
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Skip("ohne tzdata nicht prüfbar")
	}
	w := Window{From: 22 * 60, To: 6 * 60}
	// Samstag 28.03. um 07:00 — das Fenster öffnet am selben Abend 22:00.
	// Dazwischen liegt keine Umstellung, aber der Tag danach ist kurz.
	vor := time.Date(2026, 3, 28, 7, 0, 0, 0, berlin)
	if got := w.Until(vor); got != 15*time.Hour {
		t.Errorf("Until = %v, erwartet 15h", got)
	}
	// Sonntag 29.03. um 07:00, nach der Umstellung auf Sommerzeit:
	// bis 22:00 Ortszeit sind es 15 Stunden Wanduhr.
	nach := time.Date(2026, 3, 29, 7, 0, 0, 0, berlin)
	if got := w.Until(nach); got != 15*time.Hour {
		t.Errorf("Until nach der Umstellung = %v, erwartet 15h", got)
	}
}

func TestParseThrottleBetween(t *testing.T) {
	kopf := func(block string) []byte {
		return []byte("---\ntrigger:\n  watch: \"*.pdf\"\nhase: archivar\nraeume:\n  input: raeume/eingang/\n" + block + "---\nTu was.\n")
	}

	a, err := Parse("einlagern", kopf("throttle:\n  between: \"22:00-06:00\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	// Nur ein Fenster, ohne Rate — erlaubt: „nur nachts, so viele wie nötig".
	if a.Throttle.Between == nil || a.Throttle.Between.From != 22*60 || a.Throttle.Between.To != 6*60 {
		t.Fatalf("Between = %+v", a.Throttle.Between)
	}
	if !a.Throttle.An() || a.Throttle.String() != "nur 22:00-06:00" {
		t.Errorf("String = %q", a.Throttle)
	}

	beides, err := Parse("einlagern", kopf("throttle:\n  max: 5\n  per: 1h\n  between: \"22:00-06:00\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if beides.Throttle.String() != "5 Läufe per 1h, nur 22:00-06:00" {
		t.Errorf("String = %q", beides.Throttle)
	}

	fehlerfaelle := []struct{ block, erwartet string }{
		{"throttle:\n  between: \"22:00\"\n", "HH:MM-HH:MM"},
		{"throttle:\n  between: \"25:00-06:00\"\n", "invalid time"},
		{"throttle:\n  between: \"22:00-22:00\"\n", "start and end are the same"},
		{"throttle:\n  between: \"abends-morgens\"\n", "invalid time"},
	}
	for _, f := range fehlerfaelle {
		_, err := Parse("einlagern", kopf(f.block))
		if err == nil {
			t.Errorf("%q: kein Fehler", f.block)
			continue
		}
		if !strings.Contains(err.Error(), f.erwartet) {
			t.Errorf("%q: Fehler %q enthält nicht %q", f.block, err, f.erwartet)
		}
	}
}

// Hasenbau-do0.4: Wait ist die eine Rechnung, die Watcher und Status
// teilen. Reine Funktion, deshalb ohne Uhr und ohne DB prüfbar.
func TestThrottleWait(t *testing.T) {
	um := func(h, m int) time.Time { return time.Date(2026, 3, 10, h, m, 0, 0, time.UTC) }
	starts := func(ts ...time.Time) []time.Time { return ts }

	rate := Throttle{Max: 2, Per: time.Hour}
	nachts := Throttle{Between: &Window{From: 22 * 60, To: 6 * 60}}
	beides := Throttle{Max: 2, Per: time.Hour, Between: &Window{From: 22 * 60, To: 6 * 60}}

	faelle := []struct {
		name   string
		t      Throttle
		jetzt  time.Time
		starts []time.Time
		warten time.Duration
	}{
		{"Rate: Platz frei", rate, um(12, 0), starts(um(11, 30)), 0},
		{"Rate: voll", rate, um(12, 0), starts(um(11, 30), um(11, 45)), 30 * time.Minute},
		// starts sind laut Vertrag nur die Läufe IM Fenster; um 12:31
		// gehört der von 11:30 nicht mehr dazu.
		{"Rate: einer herausgefallen", rate, um(12, 31), starts(um(11, 45)), 0},
		{"Fenster: offen", nachts, um(23, 0), nil, 0},
		{"Fenster: zu", nachts, um(14, 0), nil, 8 * time.Hour},
		{"beides: offen und Platz", beides, um(23, 0), starts(um(22, 30)), 0},
		{"beides: offen, aber voll", beides, um(23, 0), starts(um(22, 30), um(22, 45)), 30 * time.Minute},
		// Der Platz fiele um 06:20 frei — da ist das Fenster schon zu,
		// also gilt der spätere Zeitpunkt: 22:00.
		{"beides: Platz fällt nach Fensterschluss frei", beides, um(5, 50),
			starts(um(5, 20), um(5, 30)), 16*time.Hour + 10*time.Minute},
		{"ungedrosselt", Throttle{}, um(12, 0), nil, 0},
	}
	for _, f := range faelle {
		if got := f.t.Wait(f.jetzt, f.starts); got != f.warten {
			t.Errorf("%s: Wait = %v, erwartet %v", f.name, got, f.warten)
		}
	}

	// Mehr Läufe als Max im Fenster (`hasenbau lauf` umgeht den Deckel):
	// gezählt wird von dem, der als Max-letzter herausfällt.
	if got := rate.Wait(um(12, 0), starts(um(11, 10), um(11, 30), um(11, 45))); got != 30*time.Minute {
		t.Errorf("übervolles Fenster: Wait = %v, erwartet 30m", got)
	}
}

func TestWindowLaenge(t *testing.T) {
	if got := (Window{From: 22 * 60, To: 6 * 60}).Laenge(); got != 8*time.Hour {
		t.Errorf("über Mitternacht: %v", got)
	}
	if got := (Window{From: 9 * 60, To: 17 * 60}).Laenge(); got != 8*time.Hour {
		t.Errorf("normal: %v", got)
	}
}

func TestFormatDuration(t *testing.T) {
	faelle := map[time.Duration]string{
		time.Hour:                    "1h",
		8 * time.Hour:                "8h",
		8*time.Hour + 44*time.Minute: "8h44m",
		30 * time.Minute:             "30m",
		90 * time.Second:             "1m30s",
		10 * time.Second:             "10s",
		2*time.Hour + 30*time.Minute: "2h30m",
		time.Hour + 2*time.Second:    "1h0m2s",
	}
	for d, erwartet := range faelle {
		if got := FormatDuration(d); got != erwartet {
			t.Errorf("FormatDuration(%v) = %q, erwartet %q", d, got, erwartet)
		}
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

func TestParseManuellTrigger(t *testing.T) {
	src := `---
trigger:
  manual: true

gaenge:
  - name: trace-ziehen
    run: '"$HASENBAU" graben "$TRIGGER_ARG" > "$WORK/trace.md"'

hase: baumeister

raeume:
  work: raeume/baumeister/work/
  out:  gaenge/entwurf/

context:
  - file: $WORK/trace.md
---
Verdichte den Trace.
`
	a, err := Parse("baumeister", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if !a.Trigger.Manual || a.Trigger.Watch != "" || a.Trigger.Cron != "" {
		t.Errorf("Trigger = %+v", a.Trigger)
	}
	if a.Trigger.Kind() != TriggerManual {
		t.Errorf("Art() = %q, erwartet %q", a.Trigger.Kind(), TriggerManual)
	}
}

func TestTriggerArt(t *testing.T) {
	faelle := []struct {
		t   Trigger
		art string
	}{
		{Trigger{Watch: "*.pdf"}, TriggerWatch},
		{Trigger{Cron: "0 7 * * *"}, TriggerCron},
		{Trigger{Manual: true}, TriggerManual},
	}
	for _, f := range faelle {
		if got := f.t.Kind(); got != f.art {
			t.Errorf("Art(%+v) = %q, erwartet %q", f.t, got, f.art)
		}
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
		{"kein Frontmatter", "kein frontmatter", "must start with \"---\""},
		{"Frontmatter offen", "---\nhase: x\nkein Ende", "not closed"},
		{"Trigger fehlt", ersetze(t, "trigger:\n  watch: \"*.pdf\"\n  debounce: 5s", "trigger:"), "trigger missing"},
		{"watch und cron", ersetze(t, "  debounce: 5s", "  cron: \"0 7 * * *\""), "are mutually exclusive"},
		{"ungültiger Cron", ersetze(t, "watch: \"*.pdf\"\n  debounce: 5s", "cron: \"99 99 * * *\""), "invalid cron expression"},
		{"debounce bei cron", ersetze(t, "watch: \"*.pdf\"", "cron: \"0 7 * * *\""), "debounce only applies to watch"},
		{"hase missing", ersetze(t, "hase: archivar\n", ""), "hase missing"},
		{"unbekanntes Feld", ersetze(t, "hase: archivar", "hase: archivar\nfarbe: braun"), "farbe"},
		{"body missing", beispiel[:strings.LastIndex(beispiel, "---")+4], "body missing"},
		{"cwd abgelehnt", ersetze(t, "hase: archivar", "hase: archivar\ncwd: raeume/laderampe/"), "cwd is not supported"},
		{"raum verlässt Bau", ersetze(t, "out:   raeume/lager/", "out:   ../draussen/"), "must not leave the Bau"},
		{"watch absolut", ersetze(t, "watch: \"*.pdf\"", "watch: /tmp/*.pdf"), "has to be relative to the input Raum"},
		{"gang ohne run", ersetze(t, "    run: gaenge/pdf_to_md.py \"$TRIGGER_FILE\" --out \"$WORK/extrakt.md\"\n", ""), "run missing"},
		{"invalid duration", ersetze(t, "timeout: 120s", "timeout: zwei Minuten"), "invalid duration"},
		{"kontext leer", ersetze(t, "- file: $WORK/extrakt.md", "- {}"), "needs file or last_summaries"},
		{"kontext doppelt", ersetze(t, "- file: $WORK/extrakt.md", "- {file: x, last_summaries: 2}"), "are mutually exclusive"},
		{"summaries null", ersetze(t, "last_summaries: 3", "last_summaries: 0"), "has to be > 0"},
		{"nachher ohne Pfeil", ersetze(t, "- move: $TRIGGER_FILE -> raeume/archiv/", "- move: $TRIGGER_FILE raeume/archiv/"), "FROM -> TO"},
		{"nachher unbekannt", ersetze(t, "- move: $TRIGGER_FILE -> raeume/archiv/", "- verbrenne: $TRIGGER_FILE"), "unbekannte Aktion"},
		{"hase ungültig", ersetze(t, "hase: archivar", "hase: archi/var"), "hase: invalid name"},
		{"hase_timeout null", ersetze(t, "hase: archivar", "hase: archivar\nhase_timeout: 0s"), "0 is not a time limit"},
		{"hase_timeout negativ", ersetze(t, "hase: archivar", "hase: archivar\nhase_timeout: -5m"), "negative Dauer"},
		{"hase_timeout kaputt", ersetze(t, "hase: archivar", "hase: archivar\nhase_timeout: eine Stunde"), "invalid duration"},
		{"watch und manuell", ersetze(t, "  debounce: 5s", "  manual: true"), "are mutually exclusive"},
		{"debounce bei manuell", ersetze(t, "watch: \"*.pdf\"", "manual: true"), "debounce only applies to watch"},
		// Der Auslöser muss zur Trigger-Art passen: bei manual gibt es
		// $TRIGGER_FILE nicht, dort heißt er $TRIGGER_ARG (Hasenbau-d6d).
		{"manual mit $TRIGGER_FILE", ersetze(t,
			"trigger:\n  watch: \"*.pdf\"\n  debounce: 5s",
			"trigger:\n  manual: true"), "$TRIGGER_FILE is not bound for a manual Auftrag"},
		{"watch ohne input-Raum", ersetze(t, "  input: raeume/laderampe/sources/\n", ""), "watch trigger without Raum"},
		{"watch mit Bau-Pfad", ersetze(t, "watch: \"*.pdf\"", "watch: raeume/laderampe/sources/*.pdf"), "looks like a Bau-relative path"},
		// Platzhalter im Verzeichnis-Anteil sind seit Hasenbau-5xv erlaubt
		// (siehe TestWatchRekursiv). Abgelehnt wird jetzt, was der Matcher
		// nicht lesen kann — beim Laden, nicht als Trigger, der nie feuert.
		{"watch mit kaputtem Muster", ersetze(t, "watch: \"*.pdf\"", "watch: \"[a-.pdf\""), "is not a valid glob pattern"},
		{"watch mit .. im Muster", ersetze(t, "watch: \"*.pdf\"", "watch: \"../*.pdf\""), "must not leave the input Raum"},
		{"$INPUT gibt es nicht mehr", ersetze(t, "$TRIGGER_FILE -> raeume/archiv/", "$INPUT -> raeume/archiv/"), "$INPUT is now called $TRIGGER_FILE"},
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

// TestAusloeserVariableGehoertZurTriggerArt nagelt die Symmetrie fest:
// gebunden ist genau der Name, den die Trigger-Art hergibt. Früher war
// das eine Sonderregel für manuell-Aufträge („$INPUT ist hier kein
// Pfad"), jetzt folgt es daraus, dass die Datei-Variable bei manual gar
// nicht existiert (Hasenbau-d6d).
func TestAusloeserVariableGehoertZurTriggerArt(t *testing.T) {
	src := func(trigger, variable string) []byte {
		return []byte(`---
trigger:
` + trigger + `
hase: baumeister
raeume:
  input: raeume/eingang/
context:
  - file: ` + variable + `
---
Lies das.
`)
	}

	faelle := []struct {
		name     string
		trigger  string
		variable string
		erwarte  string // leer = muss laden
	}{
		{"watch bindet TRIGGER_FILE", "  watch: \"*.pdf\"", "$TRIGGER_FILE", ""},
		{"manual bindet TRIGGER_ARG", "  manual: true", "$TRIGGER_ARG", ""},
		{"watch kennt TRIGGER_ARG nicht", "  watch: \"*.pdf\"", "$TRIGGER_ARG",
			"$TRIGGER_ARG is not bound for a watch Auftrag, use $TRIGGER_FILE here"},
		{"manual kennt TRIGGER_FILE nicht", "  manual: true", "$TRIGGER_FILE",
			"$TRIGGER_FILE is not bound for a manual Auftrag, use $TRIGGER_ARG here"},
		{"cron bindet keines", "  cron: \"0 7 * * *\"", "$TRIGGER_FILE",
			"cron has no trigger file"},
		{"$INPUT bekommt den Wegweiser", "  watch: \"*.pdf\"", "$INPUT",
			"$INPUT is now called $TRIGGER_FILE"},
	}
	for _, f := range faelle {
		t.Run(f.name, func(t *testing.T) {
			_, err := Parse("auftrag", src(f.trigger, f.variable))
			if f.erwarte == "" {
				if err != nil {
					t.Fatalf("muss laden, bekam: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("Fehler erwartet, bekam nil")
			}
			if !strings.Contains(err.Error(), f.erwarte) {
				t.Errorf("Fehler %q enthält nicht %q", err, f.erwarte)
			}
		})
	}
}

// TestWatchGlobSetztRaumUndMusterZusammen: der Eingang steht genau
// einmal im Auftrag, nämlich als Raum — beobachtet wird die Summe.
func TestWatchGlobSetztRaumUndMusterZusammen(t *testing.T) {
	a, err := Parse("pdf-einlagern", []byte(beispiel))
	if err != nil {
		t.Fatal(err)
	}
	if got := a.WatchGlob(); got != "raeume/laderampe/sources/*.pdf" {
		t.Errorf("WatchGlob() = %q", got)
	}

	// Ein Unterverzeichnis im Muster bleibt erlaubt, solange sein Name
	// feststeht — nur Platzhalter darin sind es nicht (Hasenbau-5xv).
	mit := strings.Replace(beispiel, `watch: "*.pdf"`, `watch: "scans/*.pdf"`, 1)
	b, err := Parse("pdf-einlagern", []byte(mit))
	if err != nil {
		t.Fatal(err)
	}
	if got := b.WatchGlob(); got != "raeume/laderampe/sources/scans/*.pdf" {
		t.Errorf("WatchGlob() mit Unterverzeichnis = %q", got)
	}

	// Kein watch-Trigger ⇒ kein Glob, auch wenn ein input-Raum da ist.
	c, err := Parse("morgenpost", []byte("---\ntrigger:\n  cron: \"0 7 * * *\"\nhase: melder\nraeume:\n  input: raeume/eingang/\n---\nMoin.\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := c.WatchGlob(); got != "" {
		t.Errorf("WatchGlob() bei cron = %q, erwartet leer", got)
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
	if !strings.Contains(err.Error(), "unknown Hase") || !strings.Contains(err.Error(), "archivar") {
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

// Hasenbau-uh0: Das Zeitlimit des LLM-Schritts gehört dem Auftrag.
// Ohne Angabe bleibt es 0 — dann entscheidet der Runner.
func TestParseHaseTimeout(t *testing.T) {
	mit := ersetze(t, "hase: archivar", "hase: archivar\nhase_timeout: 90m")
	a, err := Parse("pdf-einlagern", []byte(mit))
	if err != nil {
		t.Fatal(err)
	}
	if a.HaseTimeout != 90*time.Minute {
		t.Errorf("HaseTimeout = %v, erwartet 90m", a.HaseTimeout)
	}

	ohne, err := Parse("pdf-einlagern", []byte(beispiel))
	if err != nil {
		t.Fatal(err)
	}
	if ohne.HaseTimeout != 0 {
		t.Errorf("ohne Angabe: HaseTimeout = %v, erwartet 0", ohne.HaseTimeout)
	}
}

// TestToolsFreigabeImFrontmatter: `tools:` nennt die Schmied-Werkzeuge,
// die der Hase in diesem Auftrag rufen darf (Hasenbau-hcs). Ein Name
// wandert von hier in den permission-Block des generierten Agenten und
// in einen Dateinamen unter tools/ — ein Tippfehler soll deshalb beim
// Laden auffallen und nicht an einem Lauf, in dem der Hase sagt, er
// habe das Werkzeug nicht.
func TestToolsFreigabeImFrontmatter(t *testing.T) {
	kopf := func(tools string) []byte {
		return []byte("---\ntrigger:\n  manual: true\nhase: archivar\n" + tools + "---\nTu was.\n")
	}

	a, err := Parse("einlagern", kopf("tools:\n  - zeilen_zaehlen\n  - exif_lesen\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Tools) != 2 || a.Tools[0] != "zeilen_zaehlen" || a.Tools[1] != "exif_lesen" {
		t.Errorf("Tools = %v", a.Tools)
	}

	// Ohne den Schlüssel: keine Werkzeuge. Die Vorgabe ist nichts,
	// nicht alles — ein neu gebautes Werkzeug soll nicht dadurch bei
	// jedem Hasen landen, dass niemand es verboten hat.
	a, err = Parse("einlagern", kopf(""))
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Tools) != 0 {
		t.Errorf("ohne tools: sind es %v, erwartet keine", a.Tools)
	}

	for name, eingabe := range map[string]string{
		"ungültiger Name": "tools:\n  - \"pfad/raus\"\n",
		"doppelt":         "tools:\n  - zaehlen\n  - zaehlen\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse("einlagern", kopf(eingabe)); err == nil {
				t.Errorf("%s wurde angenommen", name)
			}
		})
	}
}
