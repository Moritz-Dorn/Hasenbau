package hase

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Moritz-Dorn/Hasenbau/internal/auftrag"
)

var aktualisiere = flag.Bool("update", false, "Golden-Dateien neu schreiben")

// quellen sind die beiden Wurzeln mit ausgelieferten Definitionen:
// beispiele/ ist Demo-Material zum Kopieren, internal/bau/vorlagen/ ist
// das, was `hasenbau init` in jeden Bau schreibt. Beide gehören in die
// Golden-Prüfung — als der Baumeister umzog und nur eine Wurzel geprüft
// wurde, blieb der Test grün und sicherte ihn nicht mehr.
func quellen() []string {
	return []string{
		filepath.Join("..", "..", "beispiele"),
		filepath.Join("..", "bau", "vorlagen"),
	}
}

// TestBeispieleGenerierenGolden hält den Agenten fest, der aus den
// ausgelieferten Dateien entsteht. Kein Selbstzweck: die Permissions
// dieses Agenten sind die einzige harte Garantie, dass der Baumeister
// nichts scharf schalten kann (PLAN.md §8/§10). Wer
// internal/bau/vorlagen/auftraege/baumeister.md ändert, sieht hier
// sofort, was er dem Hasen damit erlaubt.
func TestBeispieleGenerierenGolden(t *testing.T) {
	type quelle struct {
		root string
		a    *auftrag.Auftrag
	}
	var alle []quelle
	for _, root := range quellen() {
		// Load parst alle Aufträge und prüft, dass ihre Hasen existieren
		// — die Definitionen bleiben damit ladbar, nicht nur lesbar.
		auftraege, err := auftrag.Load(root)
		if err != nil {
			t.Fatal(err)
		}
		for _, a := range auftraege {
			alle = append(alle, quelle{root, a})
		}
	}
	if len(alle) < 2 {
		t.Fatalf("nur %d Auftrag/Aufträge gefunden — eine Wurzel ist leer oder falsch verdrahtet", len(alle))
	}

	// Beide Bau-Lagen: ohne `requests:`-Raum bekommt der Hase den
	// Absatz über `hasenbau_tool_request` nicht, weil es das Werkzeug
	// dann nicht gibt (Hasenbau-2lq). Nur eine Lage zu prüfen hiesse,
	// die andere ungesichert zu lassen — genau der Fehler, der beim
	// Umzug des Baumeisters schon einmal passiert ist.
	lagen := []struct {
		suffix string
		o      Optionen
	}{
		{".golden.md", Optionen{}},
		{".requests.golden.md", Optionen{ToolRequests: true}},
	}

	for _, q := range alle {
		for _, lage := range lagen {
			root, a, lage := q.root, q.a, lage
			t.Run(a.Name+lage.suffix, func(t *testing.T) {
				tpl, err := Lade(root, a.Hase)
				if err != nil {
					t.Fatal(err)
				}
				got, err := Generiere(a, tpl, lage.o)
				if err != nil {
					t.Fatal(err)
				}
				golden := filepath.Join("testdata", a.Name+"__"+a.Hase+lage.suffix)
				if *aktualisiere {
					if err := os.MkdirAll("testdata", 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(golden, got, 0o644); err != nil {
						t.Fatal(err)
					}
					return
				}
				want, err := os.ReadFile(golden)
				if err != nil {
					t.Fatalf("%v — mit `go test ./internal/hase/ -update` neu schreiben", err)
				}
				if string(got) != string(want) {
					t.Errorf("generierter Agent weicht ab:\n--- got ---\n%s\n--- want ---\n%s", got, want)
				}
			})
		}
	}
}

// TestSchmiedSchreibtNurInDenEntwurfsraum: der Schmied schreibt Code,
// der später IM SERVER-PROZESS läuft — außerhalb der Sandbox, in der
// die Hasen sitzen. Zwischen dem, was ein Modell geschrieben hat, und
// dem, was ein Hase rufen darf, muss deshalb ein Mensch stehen. Das
// Schreibrecht auf tools/entwurf/ und nirgends sonst ist die technische
// Hälfte dieser Zusage; die andere ist, dass tools/entwurf/ nicht
// registriert wird (internal/bau/tools.go).
func TestSchmiedSchreibtNurInDenEntwurfsraum(t *testing.T) {
	root := filepath.Join("..", "bau", "vorlagen")
	auftraege, err := auftrag.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	var schmied *auftrag.Auftrag
	for _, a := range auftraege {
		if a.Name == "schmied" {
			schmied = a
		}
	}
	if schmied == nil {
		t.Fatal("internal/bau/vorlagen/auftraege/schmied.md fehlt")
	}
	tpl, err := Lade(root, schmied.Hase)
	if err != nil {
		t.Fatal(err)
	}
	roh, err := Generiere(schmied, tpl, Optionen{})
	if err != nil {
		t.Fatal(err)
	}
	agent := string(roh)

	var allows []string
	for _, zeile := range strings.Split(agent, "\n") {
		if strings.HasSuffix(strings.TrimSpace(zeile), ": allow") {
			allows = append(allows, strings.TrimSpace(zeile))
		}
	}
	erwartet := []string{`"raeume/schmiede/work/**": allow`, `"tools/entwurf/**": allow`}
	if strings.Join(allows, "|") != strings.Join(erwartet, "|") {
		t.Errorf("allow-Regeln = %v, erwartet %v", allows, erwartet)
	}
	// tools/ selbst ist die Freigabe-Stufe des Menschen. Schriebe der
	// Schmied dorthin, wäre der Lauf, der ein Werkzeug entwirft, zugleich
	// der, der es scharf schaltet.
	for _, a := range allows {
		if strings.Contains(a, `"tools/**"`) || strings.Contains(a, `"tools/*.`) {
			t.Errorf("Schreibrecht auf das freigegebene tools/: %q", a)
		}
	}

	// Er muss die Regel gegen Universalwerkzeuge im Prompt tragen: sein
	// Skript läuft außerhalb der Sandbox, ein Interpreter wäre die
	// Hintertür, die der Wächter gerade zugemacht hat.
	for _, satz := range []string{"Ein Werkzeug, eine Aufgabe", "Interpreter"} {
		if !strings.Contains(agent, satz) {
			t.Errorf("Schmied-Prompt ohne %q — genau das ist schon gewünscht worden", satz)
		}
	}
}

// TestBaumeisterDarfNichtsScharfSchalten prüft die Garantie direkt,
// nicht nur über den Golden-Vergleich: ein Golden-Diff könnte man
// gedankenlos mit -update wegwischen, diese Zusicherungen nicht.
func TestBaumeisterDarfNichtsScharfSchalten(t *testing.T) {
	root := filepath.Join("..", "bau", "vorlagen")
	auftraege, err := auftrag.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	var baumeister *auftrag.Auftrag
	for _, a := range auftraege {
		if a.Name == "baumeister" {
			baumeister = a
		}
	}
	if baumeister == nil {
		t.Fatal("internal/bau/vorlagen/auftraege/baumeister.md fehlt")
	}
	tpl, err := Lade(root, baumeister.Hase)
	if err != nil {
		t.Fatal(err)
	}
	roh, err := Generiere(baumeister, tpl, Optionen{})
	if err != nil {
		t.Fatal(err)
	}
	agent := string(roh)

	// Die erste edit-Regel verbietet alles; erst danach kommen die
	// Ausnahmen (opencode wertet last-match aus, §11.5).
	edit := agent[strings.Index(agent, "  edit:"):]
	if erste := strings.SplitN(edit, "\n", 3)[1]; !strings.Contains(erste, `"*": deny`) {
		t.Errorf("erste edit-Regel ist %q, erwartet \"*\": deny", erste)
	}

	// Erlaubt ist genau der work- und der out-Raum — sonst nichts.
	var allows []string
	for _, zeile := range strings.Split(agent, "\n") {
		if strings.HasSuffix(strings.TrimSpace(zeile), ": allow") {
			allows = append(allows, strings.TrimSpace(zeile))
		}
	}
	erwartet := []string{`"raeume/baumeister/work/**": allow`, `"gaenge/entwurf/**": allow`}
	if strings.Join(allows, "|") != strings.Join(erwartet, "|") {
		t.Errorf("allow-Regeln = %v, erwartet %v", allows, erwartet)
	}

	// Was der Baumeister niemals anfassen darf: die Aufträge (sonst
	// könnte er sich selbst scharf schalten), die Hasen-Templates, die
	// Server-Config und die produktiven Gänge.
	for _, verboten := range []string{"auftraege", "hasen/", ".opencode-home", `"gaenge/**"`, "hasenbau.yaml"} {
		for _, a := range allows {
			if strings.Contains(a, verboten) {
				t.Errorf("Schreibrecht auf %s: %q", verboten, a)
			}
		}
	}

	// Kein Werkzeug, mit dem er den Bau von außen verändern könnte.
	for _, deny := range []string{"bash: deny", "webfetch: deny", "websearch: deny", "external_directory: deny"} {
		if !strings.Contains(agent, deny) {
			t.Errorf("%q fehlt im generierten Agenten", deny)
		}
	}
}
