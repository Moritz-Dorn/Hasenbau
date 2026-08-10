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

// TestBeispieleGenerierenGolden hält den Agenten fest, der aus den
// ausgelieferten Beispiel-Dateien entsteht. Kein Selbstzweck: die
// Permissions dieses Agenten sind die einzige harte Garantie, dass der
// Baumeister nichts scharf schalten kann (PLAN.md §8/§10). Wer
// beispiele/auftraege/baumeister.md ändert, sieht hier sofort, was er
// dem Hasen damit erlaubt.
func TestBeispieleGenerierenGolden(t *testing.T) {
	root := filepath.Join("..", "..", "beispiele")

	// Load parst alle Beispiel-Aufträge und prüft, dass ihre Hasen
	// existieren — die Beispiele bleiben damit ladbar, nicht nur lesbar.
	auftraege, err := auftrag.Load(root)
	if err != nil {
		t.Fatal(err)
	}

	for _, a := range auftraege {
		t.Run(a.Name, func(t *testing.T) {
			tpl, err := Lade(root, a.Hase)
			if err != nil {
				t.Fatal(err)
			}
			got, err := Generiere(a, tpl)
			if err != nil {
				t.Fatal(err)
			}
			golden := filepath.Join("testdata", a.Name+"__"+a.Hase+".golden.md")
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

// TestBaumeisterDarfNichtsScharfSchalten prüft die Garantie direkt,
// nicht nur über den Golden-Vergleich: ein Golden-Diff könnte man
// gedankenlos mit -update wegwischen, diese Zusicherungen nicht.
func TestBaumeisterDarfNichtsScharfSchalten(t *testing.T) {
	root := filepath.Join("..", "..", "beispiele")
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
		t.Fatal("beispiele/auftraege/baumeister.md fehlt")
	}
	tpl, err := Lade(root, baumeister.Hase)
	if err != nil {
		t.Fatal(err)
	}
	roh, err := Generiere(baumeister, tpl)
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
