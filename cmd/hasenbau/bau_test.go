package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Hasenbau-ha0.6: Die beiden Befunde, die `describe bau` zeigen muss —
// ein Bau ohne Git-Commit und ein Rückkanal-Eintrag auf ein
// verschwundenes Binary. Beide sieht man dem Bau sonst nicht an.
func TestDescribeBauMeldetDieStillenFehler(t *testing.T) {
	bau := t.TempDir()
	conf := filepath.Join(bau, ".opencode-home", "opencode", "opencode.json")
	if err := os.MkdirAll(filepath.Dir(conf), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(conf, []byte(`{"plugin":[],"mcp":{"hasenbau":{"type":"local",`+
		`"command":["/gibt/es/nicht/hasenbau","-bau",".","mcp"],"enabled":true}}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	var out, errw strings.Builder
	if code := run([]string{"-bau", bau, "describe", "bau"}, &out, &errw); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errw.String())
	}
	got := out.String()
	for _, muss := range []string{
		"no .git", "Raum permissions", // ohne Commit greifen sie nicht (§11.5)
		"/gibt/es/nicht/hasenbau", "does not exist",
		"CHECK", "to look into",
	} {
		if !strings.Contains(got, muss) {
			t.Errorf("Diagnose ohne %q:\n%s", muss, got)
		}
	}
}

// Ein vollständiger Bau meldet nichts — sonst gewöhnt sich jeder an
// gelbe Zeilen und liest sie nicht mehr.
func TestDescribeBauSchweigtWennAllesStimmt(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("ohne git nicht prüfbar")
	}
	bau := filepath.Join(t.TempDir(), "bau")
	var out, errw strings.Builder
	if code := run([]string{"init", bau}, &out, &errw); code != 0 {
		t.Fatalf("init: exit %d, stderr %q", code, errw.String())
	}
	// Nichts von Hand nachtragen: `init` legt auch den Rückkanal-Eintrag
	// an, und er zeigt auf dieses Test-Binary — also auf ein Binary, das
	// es gibt.
	out.Reset()
	errw.Reset()
	if code := run([]string{"-bau", bau, "describe", "bau"}, &out, &errw); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errw.String())
	}
	got := out.String()
	if strings.Contains(got, "CHECK") {
		t.Errorf("frischer Bau meldet etwas:\n%s", got)
	}
	if !strings.Contains(got, "Nothing to do") {
		t.Errorf("Ausgabe:\n%s", got)
	}
}

// `hasenbau init unterverzeichnis` darf keinen relativen Pfad in den
// Rückkanal-Eintrag schreiben: opencode startet `hasenbau -bau <root>
// mcp` mit einem CWD, das nicht das des Aufrufers ist, und relativ
// aufgelöst zeigte der Eintrag dann irgendwohin.
func TestInitMachtDenBauPfadAbsolut(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("ohne git nicht prüfbar")
	}
	t.Chdir(t.TempDir()) // stellt das alte CWD am Testende wieder her

	var out, errw strings.Builder
	if code := run([]string{"init", "relativer-bau"}, &out, &errw); code != 0 {
		t.Fatalf("init: exit %d, stderr %q", code, errw.String())
	}
	roh, err := os.ReadFile(filepath.Join("relativer-bau", ".opencode-home", "opencode", "opencode.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(roh), `"relativer-bau"`) {
		t.Errorf("relativer Bau-Pfad im Rückkanal-Eintrag:\n%s", roh)
	}
}

// status ist das Dashboard und prüft NICHTS — der Unterschied zu
// describe bau ist der ganze Sinn der Aufteilung.
func TestStatusZeigtOhneZuPruefen(t *testing.T) {
	bau := t.TempDir()
	laufMitNotizen(t, bau).Close()

	var out, errw strings.Builder
	if code := run([]string{"-bau", bau, "status"}, &out, &errw); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errw.String())
	}
	got := out.String()
	for _, muss := range []string{"Bau:", "Läufe:", "The most recent Läufe", "describe bau"} {
		if !strings.Contains(got, muss) {
			t.Errorf("Dashboard ohne %q:\n%s", muss, got)
		}
	}
	// Dieser Bau hat weder .git noch Bau-Config — status sagt trotzdem
	// kein Wort dazu.
	for _, darfNicht := range []string{"CHECK", "no .git", "missing"} {
		if strings.Contains(got, darfNicht) {
			t.Errorf("status prüft (%q):\n%s", darfNicht, got)
		}
	}
}

func TestDescribeBauMitArgument(t *testing.T) {
	var out, errw strings.Builder
	if code := run([]string{"-bau", t.TempDir(), "describe", "bau", "zuviel"}, &out, &errw); code != 2 {
		t.Errorf("exit %d, erwartet 2", code)
	}
}

// TestFixStelltGeloeschteVorlagenWiederHer: der Sinn des Befehls in
// einem Satz — wer den Baumeister löscht, bekommt ihn samt Auftrag
// zurück. Gelöscht wird hier beides, damit der Test nicht schon grün
// ist, wenn nur eine Hälfte wiederkommt.
func TestFixStelltGeloeschteVorlagenWiederHer(t *testing.T) {
	bauDir := t.TempDir()
	var out, errw strings.Builder
	if code := run([]string{"-bau", bauDir, "init", bauDir}, &out, &errw); code != 0 {
		t.Fatalf("init: %d, %s", code, errw.String())
	}

	auftrag := filepath.Join(bauDir, "auftraege", "baumeister.md")
	hase := filepath.Join(bauDir, "hasen", "baumeister.md")
	for _, f := range []string{auftrag, hase} {
		if _, err := os.Stat(f); err != nil {
			t.Fatalf("init hat %s nicht angelegt: %v", filepath.Base(f), err)
		}
		if err := os.Remove(f); err != nil {
			t.Fatal(err)
		}
	}

	out.Reset()
	errw.Reset()
	if code := run([]string{"-bau", bauDir, "fix"}, &out, &errw); code != 0 {
		t.Fatalf("fix: %d, %s", code, errw.String())
	}
	for _, f := range []string{auftrag, hase} {
		if _, err := os.Stat(f); err != nil {
			t.Errorf("fix hat %s nicht wiederhergestellt: %v", filepath.Base(f), err)
		}
	}
	if !strings.Contains(out.String(), "auftraege/baumeister.md") {
		t.Errorf("fix meldet nicht, was es ergänzt hat:\n%s", out.String())
	}
}

// TestFixAendertBestehendesNicht: fix ist eine Ergänzung, keine
// Rücksetzung. Ein Bau, in dem jemand den Baumeister angepasst hat,
// darf ihn nicht verlieren.
func TestFixAendertBestehendesNicht(t *testing.T) {
	bauDir := t.TempDir()
	var out, errw strings.Builder
	if code := run([]string{"-bau", bauDir, "init", bauDir}, &out, &errw); code != 0 {
		t.Fatalf("init: %d, %s", code, errw.String())
	}
	auftrag := filepath.Join(bauDir, "auftraege", "baumeister.md")
	eigen, err := os.ReadFile(auftrag)
	if err != nil {
		t.Fatal(err)
	}
	geaendert := strings.Replace(string(eigen), "hase_timeout: 60m", "hase_timeout: 90m", 1)
	if geaendert == string(eigen) {
		t.Fatal("Testaufbau: hase_timeout nicht in der Vorlage")
	}
	if err := os.WriteFile(auftrag, []byte(geaendert), 0o644); err != nil {
		t.Fatal(err)
	}

	if code := run([]string{"-bau", bauDir, "fix"}, &out, &errw); code != 0 {
		t.Fatalf("fix: %d, %s", code, errw.String())
	}
	nachher, err := os.ReadFile(auftrag)
	if err != nil {
		t.Fatal(err)
	}
	if string(nachher) != geaendert {
		t.Error("fix hat die angepasste Datei überschrieben")
	}
}

// TestFixOhneBau: in einem leeren Verzeichnis gibt es nichts zu
// reparieren — dort gehört der Nutzer zu `init` geschickt, nicht mit
// einem halben Bau beglückt.
func TestFixOhneBau(t *testing.T) {
	leer := t.TempDir()
	var out, errw strings.Builder
	if code := run([]string{"-bau", leer, "fix"}, &out, &errw); code == 0 {
		t.Error("fix in leerem Verzeichnis muss scheitern")
	}
	if !strings.Contains(errw.String(), "init") {
		t.Errorf("Fehler nennt init nicht: %s", errw.String())
	}
	if _, err := os.Stat(filepath.Join(leer, "hasenbau.yaml")); err == nil {
		t.Error("fix hat im leeren Verzeichnis einen Bau angelegt")
	}
}

// TestFixStelltWaechterWiederHer: der Sandbox-Wächter ist eine Vorlage
// wie der Baumeister — und sein Fehlen ist gefährlicher als dessen,
// weil sein Schweigen als „niemand hat es versucht" gelesen wird.
func TestFixStelltWaechterWiederHer(t *testing.T) {
	bauDir := t.TempDir()
	var out, errw strings.Builder
	if code := run([]string{"-bau", bauDir, "init", bauDir}, &out, &errw); code != 0 {
		t.Fatalf("init: %d, %s", code, errw.String())
	}
	waechter := filepath.Join(bauDir, ".opencode-home", "opencode", "plugin", "hasenbau.js")
	if _, err := os.Stat(waechter); err != nil {
		t.Fatalf("init hat den Wächter nicht angelegt: %v", err)
	}
	if err := os.Remove(waechter); err != nil {
		t.Fatal(err)
	}

	// Ohne ihn muss die Diagnose anschlagen — sonst wäre die Lücke
	// unsichtbar, und genau das ist der Fall, den es zu vermeiden gilt.
	out.Reset()
	run([]string{"-bau", bauDir, "describe", "bau"}, &out, &errw)
	if !strings.Contains(out.String(), "Sandbox guard") || !strings.Contains(out.String(), "missing") {
		t.Errorf("Diagnose meldet den fehlenden Wächter nicht:\n%s", out.String())
	}

	out.Reset()
	if code := run([]string{"-bau", bauDir, "fix"}, &out, &errw); code != 0 {
		t.Fatalf("fix: %d, %s", code, errw.String())
	}
	if _, err := os.Stat(waechter); err != nil {
		t.Errorf("fix hat den Wächter nicht wiederhergestellt: %v", err)
	}
}

// TestVeralteterWaechterWirdGemeldetUndErsetzt: der leisere Fall von
// Hasenbau-uei. Die Datei fehlt nicht, sie ist nur alt — und dann
// meldet der Wächter zwar noch, hat aber die Zusagen nicht, die seither
// dazugekommen sind (Review-Gate der Werkzeuge, Sandkasten samt
// Raum-Grenze). Gemessen an ~/SRC/meinHasenbau: 72 Zeilen gegen 359,
// und nichts sagte es.
func TestVeralteterWaechterWirdGemeldetUndErsetzt(t *testing.T) {
	bauDir := t.TempDir()
	var out, errw strings.Builder
	if code := run([]string{"-bau", bauDir, "init", bauDir}, &out, &errw); code != 0 {
		t.Fatalf("init: %d, %s", code, errw.String())
	}
	waechter := filepath.Join(bauDir, ".opencode-home", "opencode", "plugin", "hasenbau.js")
	ausgeliefert, err := os.ReadFile(waechter)
	if err != nil {
		t.Fatal(err)
	}
	const alt = "// alte Fassung ohne Review-Gate\nexport const SandboxWaechter = async () => ({})\n"
	if err := os.WriteFile(waechter, []byte(alt), 0o644); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	run([]string{"-bau", bauDir, "describe", "bau"}, &out, &errw)
	if !strings.Contains(out.String(), "Sandbox guard") || !strings.Contains(out.String(), "outdated") {
		t.Errorf("Diagnose meldet den veralteten Wächter nicht:\n%s", out.String())
	}

	out.Reset()
	if code := run([]string{"-bau", bauDir, "fix"}, &out, &errw); code != 0 {
		t.Fatalf("fix: %d, %s", code, errw.String())
	}
	nachher, err := os.ReadFile(waechter)
	if err != nil {
		t.Fatal(err)
	}
	if string(nachher) != string(ausgeliefert) {
		t.Error("fix hat die alte Fassung stehen lassen")
	}
	// Und es muss dabeistehen: ein Befehl, der eine Sicherheitsdatei
	// tauscht und „nichts zu tun" meldet, verbirgt genau das, was dieser
	// Bead sichtbar machen soll.
	if !strings.Contains(out.String(), "replaced") {
		t.Errorf("fix schweigt über den Tausch:\n%s", out.String())
	}
	if strings.Contains(out.String(), "nothing to do") {
		t.Errorf("fix meldet trotz Tausch, es sei nichts zu tun:\n%s", out.String())
	}
}

// TestStartZiehtDasPluginNach: der Weg, auf dem eine Härtung einen
// bestehenden Bau OHNE Zutun erreicht. `fix` zu tippen ist die Ausnahme;
// die Regel ist, dass Daemon-Start und Lauf über loadAndGenerate gehen —
// dieselbe Runde, in der auch die Agenten und die Raum-Grenzen
// entstehen. Läge das Plugin nicht darin, hinge die Zusage weiter daran,
// dass jemand an sie denkt.
func TestStartZiehtDasPluginNach(t *testing.T) {
	bauDir := t.TempDir()
	var out, errw strings.Builder
	if code := run([]string{"-bau", bauDir, "init", bauDir}, &out, &errw); code != 0 {
		t.Fatalf("init: %d, %s", code, errw.String())
	}
	waechter := filepath.Join(bauDir, ".opencode-home", "opencode", "plugin", "hasenbau.js")
	ausgeliefert, err := os.ReadFile(waechter)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(waechter, []byte("// alte Fassung\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := loadAndGenerate(bauDir); err != nil {
		t.Fatalf("loadAndGenerate: %v", err)
	}
	nachher, err := os.ReadFile(waechter)
	if err != nil {
		t.Fatal(err)
	}
	if string(nachher) != string(ausgeliefert) {
		t.Error("der Start hat die alte Fassung stehen lassen")
	}
}

// TestWaechterOhneEintragWirdGemeldet: die Datei allein nützt nichts —
// ohne den plugin:-Eintrag lädt opencode sie nie. Ein Wächter, der
// daliegt und schweigt, sähe aus wie einer, der nichts zu melden hat.
func TestWaechterOhneEintragWirdGemeldet(t *testing.T) {
	bauDir := t.TempDir()
	var out, errw strings.Builder
	if code := run([]string{"-bau", bauDir, "init", bauDir}, &out, &errw); code != 0 {
		t.Fatalf("init: %d, %s", code, errw.String())
	}
	cfg := filepath.Join(bauDir, ".opencode-home", "opencode", "opencode.json")
	roh, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ohne := strings.Replace(string(roh), `"./plugin/hasenbau.js"`, "", 1)
	if ohne == string(roh) {
		t.Fatal("Testaufbau: kein plugin-Eintrag in der Config")
	}
	if err := os.WriteFile(cfg, []byte(ohne), 0o644); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	run([]string{"-bau", bauDir, "describe", "bau"}, &out, &errw)
	if !strings.Contains(out.String(), "not listed in the plugin") {
		t.Errorf("Diagnose meldet den fehlenden Eintrag nicht:\n%s", out.String())
	}
}
