// tool.go: `hasenbau tool test <name> [--arg wert …]` (Hasenbau-kf5).
//
// Der Befehl schließt die Lücke, die der erste echte Schmied-Lauf
// gezeigt hat. Der Schmied lieferte ein Werkzeug samt tadellosem
// Manifest, und nichts in der Kette merkte, dass es nicht läuft: das
// Manifest war gültig, `get tools` führte es klaglos, die Diagnose
// meldete „1 im Entwurf". Erst der Aufruf gegen eine echte Datei brachte
// einen TypeError zutage — und nach dessen Behebung einen Parser, der
// reale PDFs nicht liest.
//
// Die Ursache steckt im Zuschnitt: der Schmied hat `bash: deny` wie
// jeder Hase und kann sein Skript kein einziges Mal ausführen. Wir
// verlangen Code und nehmen zugleich jede Möglichkeit, ihn zu
// probieren. Bis das anders ist, gehört der eine Probelauf dorthin, wo
// ohnehin ein Mensch steht: an die Freigabe.
//
// Bewusst KEIN Vergleich gegen eine erwartete Ausgabe. Ein Test, der
// nur fragt „stürzt es ab?", hätte im Fall oben nach der ersten
// Korrektur grün gemeldet — die Erwartung ist das Urteil des Lesers,
// und das lässt sich nicht ins Manifest schreiben. Der Befehl zeigt
// deshalb Exit-Code, stdout und stderr und überlässt den Schluss dem
// Menschen.
package main

import (
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Moritz-Dorn/Hasenbau/internal/bau"
)

const toolUsage = `Aufruf: hasenbau tool test <name> [--<arg> <wert> …]

Führt ein Werkzeug einmal aus und zeigt, was zurückkam. Gesucht wird
zuerst unter ` + bau.ToolsEntwurfDir + `/, dann unter ` + bau.ToolsDir + `/ —
ein Entwurf ist ungeprüfter Code, und genau der gehört probiert, bevor
ihn jemand freigibt.

  hasenbau tool test pdf_seiten_zaehlen --pdf raeume/eingang/a.pdf

Was der Bau kennt, zeigt ` + "`hasenbau get tools`" + `.
`

func cmdTool(root string, args []string, out, errw io.Writer) int {
	if len(args) == 0 || args[0] != "test" {
		fmt.Fprint(errw, toolUsage)
		return 2
	}
	if len(args) < 2 {
		fmt.Fprint(errw, toolUsage)
		return 2
	}
	return toolTest(root, args[1], args[2:], out, errw)
}

func toolTest(root, name string, rest []string, out, errw io.Writer) int {
	werkzeuge, err := bau.LadeTools(root)
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	// Der Entwurf hat Vorrang: liegt derselbe Name zweimal, will man den
	// prüfen, der noch nicht freigegeben ist.
	var gefunden *bau.Tool
	for i := range werkzeuge {
		if werkzeuge[i].Name != name {
			continue
		}
		if gefunden == nil || werkzeuge[i].Entwurf {
			gefunden = &werkzeuge[i]
		}
	}
	if gefunden == nil {
		fmt.Fprintf(errw, "hasenbau tool test: kein Werkzeug %q\n%s", name, vorhandene(werkzeuge))
		return 1
	}

	werte, code := parseToolArgs(gefunden, rest, errw)
	if code != 0 {
		return code
	}

	// argv statt einer Shell-Zeile: die Werte kommen hier zwar von einem
	// Menschen, aber im Ernstfall von einem Modell — und das Plugin ruft
	// das Skript ebenso auf. Ein Testlauf, der anders aufruft als der
	// Ernstfall, prüft das Falsche.
	var argv []string
	for _, a := range gefunden.Args {
		wert, da := werte[a.Name]
		if !da {
			continue
		}
		argv = append(argv, "--"+a.Name, wert)
	}
	skript := filepath.Join(root, gefunden.Skript)
	cmd := exec.Command(skript, argv...)
	cmd.Dir = root
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	fmt.Fprintf(out, "%s  (%s)\n", gefunden.Name, gefunden.Skript)
	if gefunden.Entwurf {
		fmt.Fprintln(out, "Entwurf — noch nicht freigegeben.")
	}
	fmt.Fprintf(out, "Aufruf: %s %s\n\n", gefunden.Skript, strings.Join(argv, " "))

	runErr := cmd.Run()
	exitCode := 0
	switch e := runErr.(type) {
	case nil:
	case *exec.ExitError:
		exitCode = e.ExitCode()
	default:
		// Nicht ausführbar, Shebang fehlt, Datei weg — das ist ein
		// Befund über das Werkzeug, kein Fehler des Hasenbaus.
		fmt.Fprintf(out, "nicht ausführbar: %v\n", runErr)
		fmt.Fprintln(out, "\nSo bekäme ein Hase das Werkzeug nie zu sehen.")
		return 1
	}

	schreibeKanal(out, "stdout", stdout.String())
	schreibeKanal(out, "stderr", stderr.String())
	fmt.Fprintf(out, "\nExit-Code %d — ", exitCode)
	if exitCode == 0 {
		fmt.Fprintln(out, "der Hase bekäme stdout als Ergebnis.")
		fmt.Fprintln(out, "Ob das Ergebnis STIMMT, sagt dir kein Befehl: das ist der Teil,")
		fmt.Fprintf(out, "für den die Freigabe eine Code-Review ist und kein Durchwinken.\n")
	} else {
		fmt.Fprintln(out, "der Hase bekäme einen Werkzeug-Fehler mit dem Text aus stderr.")
	}
	if exitCode != 0 {
		return 1
	}
	return 0
}

// parseToolArgs liest `--name wert` gegen das Manifest. Ein unbekannter
// Name ist ein Fehler und kein Durchreichen: sonst prüft man das
// Werkzeug mit einer Eingabe, die ihm ein Hase nie schicken könnte.
func parseToolArgs(t *bau.Tool, rest []string, errw io.Writer) (map[string]string, int) {
	bekannt := map[string]bool{}
	for _, a := range t.Args {
		bekannt[a.Name] = true
	}
	werte := map[string]string{}
	for i := 0; i < len(rest); i++ {
		arg := rest[i]
		if !strings.HasPrefix(arg, "--") {
			fmt.Fprintf(errw, "hasenbau tool test: %q — erwartet --<arg> <wert>\n%s", arg, manifestArgs(t))
			return nil, 2
		}
		name := strings.TrimPrefix(arg, "--")
		if gleich := strings.SplitN(name, "=", 2); len(gleich) == 2 {
			name = gleich[0]
			if !bekannt[name] {
				fmt.Fprintf(errw, "hasenbau tool test: %q kennt das Manifest nicht\n%s", name, manifestArgs(t))
				return nil, 2
			}
			werte[name] = gleich[1]
			continue
		}
		if !bekannt[name] {
			fmt.Fprintf(errw, "hasenbau tool test: %q kennt das Manifest nicht\n%s", name, manifestArgs(t))
			return nil, 2
		}
		if i+1 >= len(rest) {
			fmt.Fprintf(errw, "hasenbau tool test: --%s ohne Wert\n", name)
			return nil, 2
		}
		werte[name] = rest[i+1]
		i++
	}
	var fehlend []string
	for _, a := range t.Args {
		if a.Pflicht {
			if _, da := werte[a.Name]; !da {
				fehlend = append(fehlend, "--"+a.Name)
			}
		}
	}
	if len(fehlend) > 0 {
		sort.Strings(fehlend)
		fmt.Fprintf(errw, "hasenbau tool test: Pflichtargument fehlt: %s\n%s",
			strings.Join(fehlend, ", "), manifestArgs(t))
		return nil, 2
	}
	return werte, 0
}

func manifestArgs(t *bau.Tool) string {
	if len(t.Args) == 0 {
		return "Das Manifest nennt keine Argumente.\n"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Laut Manifest (%s):\n", t.Manifest)
	for _, a := range t.Args {
		pflicht := "optional"
		if a.Pflicht {
			pflicht = "Pflicht"
		}
		fmt.Fprintf(&b, "  --%s <%s>  %s (%s)\n", a.Name, a.Typ, a.Beschreibung, pflicht)
	}
	return b.String()
}

func vorhandene(werkzeuge []bau.Tool) string {
	if len(werkzeuge) == 0 {
		return "Der Bau hat keine Werkzeuge.\n"
	}
	var namen []string
	for _, t := range werkzeuge {
		if t.Entwurf {
			namen = append(namen, t.Name+" (Entwurf)")
			continue
		}
		namen = append(namen, t.Name)
	}
	return "Vorhanden: " + strings.Join(namen, ", ") + "\n"
}

// schreibeKanal zeigt einen Ausgabekanal so, dass leer auch als leer zu
// erkennen ist — ein Werkzeug, das schweigend Exit 0 meldet, ist ein
// Befund.
func schreibeKanal(out io.Writer, name, inhalt string) {
	if strings.TrimSpace(inhalt) == "" {
		fmt.Fprintf(out, "%s: (leer)\n", name)
		return
	}
	fmt.Fprintf(out, "%s:\n", name)
	for _, zeile := range strings.Split(strings.TrimRight(inhalt, "\n"), "\n") {
		fmt.Fprintf(out, "  %s\n", zeile)
	}
}
