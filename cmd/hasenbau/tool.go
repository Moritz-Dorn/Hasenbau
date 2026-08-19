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
// SEIT Hasenbau-lnk gibt es doch einen Vergleich — aber gegen etwas
// anderes, als hier einmal ausgeschlossen wurde. Der Einwand von damals
// steht und ist nicht widerlegt: die ERWARTUNG DES LESERS lässt sich
// nicht ins Manifest schreiben, und ein Test, der nur fragt „stürzt es
// ab?", meldet nach der ersten Korrektur grün.
//
// Verglichen wird deshalb nicht mit dem Urteil des Lesers, sondern mit
// der VORHERSAGE DES SCHMIEDS (`example.expect`). Das ist eine schwächere
// Aussage und genau deshalb tragfähig: sie bindet niemanden außer den,
// der sie gemacht hat. Weicht die Ausgabe ab, hat sich das Modell über
// sein eigenes Skript geirrt — das darf eine Maschine feststellen, und
// es macht `invalid`. Stimmt sie überein, ist damit NICHTS bewiesen:
// Vorhersage und Skript stammen aus derselben Feder. Der Zustand bleibt
// `hypothetical`, das Urteil fällt weiterhin ein Mensch bei `release`.
//
// Der praktische Anlass war eine Frage von Moritz, auf die es keine gute
// Antwort gab: welche Datei soll ein Mensch beim Probelauf eigentlich
// angeben? Das weiß nur der Hase, der das Werkzeug angefordert hat, und
// der steht beim Review nicht daneben.
//
// WAS DIESER BEFEHL NICHT IST: eine Sicherheitsprüfung. Er FÜHRT das
// Skript AUS. Seit Hasenbau-9w6 tut er das im Sandkasten (probelauf.go:
// kein Netz, Bau nur lesbar, kein $HOME, Zeitlimit), und das ist eine
// echte Grenze — aber keine Prüfung. Er findet Fehler, keine Absichten,
// und das freigegebene Werkzeug läuft später ungesandboxed im
// Server-Prozess.
//
// Das ist keine Spitzfindigkeit, denn der Weg dorthin ist real: ein
// Hase liest fremdes Material (eine PDF, eine Notiz), darin stehen
// eingeschleuste Anweisungen, er stellt daraufhin einen Werkzeug-Wunsch,
// und der Schmied baut, was im Wunsch steht. Am Ende dieser Kette liegt
// Python, das im Server-Prozess laufen soll.
//
// Die Reihenfolge lautet deshalb LESEN, dann probieren, dann freigeben —
// nicht umgekehrt. Ein Befehl, der das Ausführen bequem macht, verführt
// dazu, das Lesen zu überspringen; dagegen hilft nur, es überall
// hinzuschreiben, wo jemand vorbeikommt (hier, in `describe bau`, im
// README und in der Ausgabe des Befehls selbst). Der Sandkasten nimmt
// dem Versäumnis inzwischen die Spitze, ersetzt das Lesen aber nicht:
// er hält den Probelauf klein, nicht den Betrieb.
package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Moritz-Dorn/Hasenbau/internal/bau"
	"github.com/Moritz-Dorn/Hasenbau/internal/store"
)

const toolUsage = `Usage: hasenbau tool <verb> …

  review [<name>|--next]        read it and take responsibility
  test <name> [--<arg> <val>]   run it once and show what came out;
                                without arguments it takes the Schmied's
                                example and compares against its prediction
        [-` + probeSandboxFlag + `]           without a sandbox, under real conditions
  release <name>                move it to ` + bau.ToolsDir + `/
  state <name>                  may it be registered? (exit 0/1; the Bau
                                plugin asks this — not meant for hand use)

The three verbs are an order, not a choice. A tool passes through them
in this sequence, and each step requires the previous one:

  generated → review → hypothetical → test → hypothetical → release → actual
  written,              claimed;                 with evidence;         a human found
  unread                a trial run              the trial run          the output to
                        is still missing         is evidence, not       be right
                                                 a verdict

A FAILED trial run refutes and makes it invalid. So does one that runs
through but returns something other than the Schmied predicted — exit 0
can mean "it did the wrong thing". A passing one does NOT make it
actual: the prediction and the script come from the same model. Whether
the output matches reality is something only a human sees.

` + "`test`" + ` requires a review because it RUNS the script. It does so in a
sandbox — no network, the Bau read-only, no $HOME — but it finds
mistakes, not intentions: against malicious code it is not the defence,
it is the execution. That is why a human reads it first.

If it fails INSIDE the sandbox, the command asks: the crash may come
from the tool or from its boundaries, and no machine sees that
difference. Without an answer — in a script, without a terminal — the
state stays as it was. A verdict without asking exists only under real
conditions: ` + "`-" + probeSandboxFlag + "`" + `.

What the Bau knows is shown by ` + "`hasenbau get tools`" + `,
what is waiting for review by ` + "`hasenbau get tools -drafts`" + `.
`

func cmdTool(root string, args []string, in io.Reader, out, errw io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(errw, toolUsage)
		return 2
	}
	switch args[0] {
	case "test":
		if len(args) < 2 {
			fmt.Fprint(errw, toolUsage)
			return 2
		}
		return toolTest(root, args[1], args[2:], in, out, errw)
	case "review":
		return toolReview(root, args[1:], in, out, errw)
	case "state":
		if len(args) != 2 {
			fmt.Fprintln(errw, "Usage: hasenbau tool state <name>")
			return 2
		}
		return toolState(root, args[1], out, errw)
	case "release":
		fs := flag.NewFlagSet("tool release", flag.ContinueOnError)
		fs.SetOutput(errw)
		ja := fs.Bool("yes", false, "skip the confirmation (for scripts)")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		if fs.NArg() != 1 {
			fmt.Fprintln(errw, "Usage: hasenbau tool release [-yes] <name>")
			return 2
		}
		return toolRelease(root, fs.Arg(0), *ja, in, out, errw)
	default:
		fmt.Fprintf(errw, "hasenbau tool: unknown verb %q\n\n%s", args[0], toolUsage)
		return 2
	}
}

func toolTest(root, name string, rest []string, in io.Reader, out, errw io.Writer) int {
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
		fmt.Fprintf(errw, "hasenbau tool test: no tool %q\n%s", name, vorhandene(werkzeuge))
		return 1
	}

	// Das Gate: getestet werden darf NUR, was ein gültiges Review trägt
	// — `hypothetical` (gelesen, noch nicht gezeigt) und `actual`
	// (gelesen und gezeigt). Alles andere wird abgewiesen, und zwar mit
	// dem Grund, denn jeder der drei Fälle braucht einen anderen
	// nächsten Schritt.
	if gefunden.Zustand != bau.Hypothetical && gefunden.Zustand != bau.Actual {
		fmt.Fprintf(errw, "hasenbau tool test: %s is %s — %s\n",
			name, gefunden.Zustand, gefunden.Zustand.Erklaerung())
		switch gefunden.Zustand {
		case bau.Outdated:
			fmt.Fprintf(errw, "The review of %s applies to different content than what is there now.\n", gefunden.Review.By)
		case bau.Invalid:
			// Ein widerlegter Anspruch wird nicht durch Wiederholen
			// wahr. Wer erneut testen will, soll erst wieder lesen —
			// und wer das Skript repariert, kommt ohnehin über
			// `outdated` hierher zurück.
			fmt.Fprintln(errw, "A trial run has already refuted the claim.")
			fmt.Fprintln(errw, "Running it again does not make it true: read first, then show.")
		default:
			if gefunden.Review.Fehler != "" {
				fmt.Fprintf(errw, "There is a review block, but it is unusable: %s\n", gefunden.Review.Fehler)
				fmt.Fprintln(errw, "A block nobody wrote comes from the Schmied — that is a finding.")
			}
			fmt.Fprintln(errw, "This command RUNS THE SCRIPT. Read it first.")
		}
		fmt.Fprintf(errw, "\n  hasenbau tool review %s\n", name)
		return 1
	}

	// `-no-sandbox` gehört dem Befehl, nicht dem Werkzeug. Die
	// Doppelstrich-Form wird nur geschluckt, wenn das Werkzeug nicht
	// selbst ein Argument dieses Namens führt — sonst nähme der Befehl
	// dem Werkzeug einen Namen weg, den dessen Manifest vergeben hat.
	eigenes := false
	for _, a := range gefunden.Args {
		if a.Name == probeSandboxFlag {
			eigenes = true
		}
	}
	sandkasten := true
	var gefiltert []string
	for _, r := range rest {
		if r == "-"+probeSandboxFlag || (!eigenes && r == "--"+probeSandboxFlag) {
			sandkasten = false
			continue
		}
		gefiltert = append(gefiltert, r)
	}

	// Ohne Argumente greift das Beispiel des Schmieds — und nur dann wird
	// verglichen. Wer selbst Argumente setzt, prüft etwas anderes als das
	// Vorhergesagte; ein Vergleich mit `expect` wäre dort sinnlos.
	//
	// Das Beispiel ist die Antwort auf eine Frage, die ein Mensch beim
	// Review nicht beantworten kann: WELCHE Datei gehört hier hinein? Der
	// Einzige, der das weiß, ist der Hase, der das Werkzeug angefordert
	// hat, und der steht beim Review nicht daneben.
	var argv []string
	vergleichen := false
	if len(gefiltert) == 0 && gefunden.Beispiel != nil {
		argv = gefunden.BeispielArgv()
		vergleichen = true
	} else {
		werte, code := parseToolArgs(gefunden, gefiltert, errw)
		if code != 0 {
			return code
		}
		for _, a := range gefunden.Args {
			wert, da := werte[a.Name]
			if !da {
				continue
			}
			argv = append(argv, "--"+a.Name, wert)
		}
	}

	// argv statt einer Shell-Zeile: die Werte kommen hier zwar von einem
	// Menschen, aber im Ernstfall von einem Modell — und das Plugin ruft
	// das Skript ebenso auf. Ein Testlauf, der anders aufruft als der
	// Ernstfall, prüft das Falsche.
	skript := filepath.Join(root, gefunden.Skript)
	ctx, abbruch := probeKontext(sandkasten)
	defer abbruch()
	cmd, kasten := probeCommand(ctx, root, skript, argv, sandkasten)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	fmt.Fprintf(out, "%s  (%s, %d lines)\n", gefunden.Name, gefunden.Skript, zeilen(skript))
	if gefunden.Entwurf {
		// Der Hinweis steht VOR der Ausführung, nicht danach: wer ihn
		// erst im Ergebnis liest, hat das Skript schon laufen lassen.
		fmt.Fprintln(out, "Draft — written by a model, not released yet.")
		fmt.Fprintln(out, "This command RUNS it. It finds mistakes, not intentions —")
		fmt.Fprintln(out, "read the script before you try it.")
	}
	// Unter welchen Bedingungen gelaufen wurde, gehört ÜBER die Ausgabe
	// und nicht darunter: es entscheidet, was sie bedeutet.
	if kasten.active {
		fmt.Fprintf(out, "Sandbox: no network, the Bau read-only, no $HOME, time limit %s.\n", probeTimeout)
	} else {
		fmt.Fprintf(out, "WITHOUT A SANDBOX, with your rights — %s.\n", kasten.reason)
	}
	fmt.Fprintf(out, "Call: %s %s\n", gefunden.Skript, strings.Join(argv, " "))
	if vergleichen {
		fmt.Fprintf(out, "Example by the Schmied — it predicts: %s\n", einzeiler(gefunden.Beispiel.Erwartet))
	}
	fmt.Fprintln(out)

	runErr := cmd.Run()
	exitCode := 0
	switch e := runErr.(type) {
	case nil:
	case *exec.ExitError:
		exitCode = e.ExitCode()
	default:
		// Nicht ausführbar, Shebang fehlt, Datei weg — das ist ein
		// Befund über das Werkzeug, kein Fehler des Hasenbaus.
		fmt.Fprintf(out, "not executable: %v\n", runErr)
		fmt.Fprintln(out, "\nLike this a Hase would never get to see the tool.")
		return 1
	}

	schreibeKanal(out, "stdout", stdout.String())
	schreibeKanal(out, "stderr", stderr.String())
	fmt.Fprintf(out, "\nExit code %d — ", exitCode)
	if exitCode == 0 {
		fmt.Fprintln(out, "the Hase would get stdout as the result.")
	} else {
		fmt.Fprintln(out, "the Hase would get a tool error with the text from stderr.")
	}

	// Ein Fehlschlag IM SANDKASTEN klassifiziert NICHT. `invalid` heißt
	// „der Probelauf hat die Behauptung widerlegt" — widerlegt hat er
	// sie aber nur unter den Bedingungen des Ernstfalls. Bei
	// geschlossenem Netz und nur lesbarem Bau kann derselbe Exit-Code
	// vom Sandkasten kommen, und diesen Unterschied sieht keine
	// Maschine: das Skript scheitert in beiden Fällen mit 1.
	//
	// Hier falsch zu klassifizieren wäre besonders teuer, denn `invalid`
	// sperrt auch das erneute Testen — ein Werkzeug, das nur schreiben
	// wollte, käme ohne neues Review nicht mehr aus dem Zustand heraus.
	// Also dasselbe Prinzip wie bei `actual`: der Lauf ist ein Beleg,
	// das Urteil fällt woanders.
	if kasten.active && exitCode != 0 {
		fmt.Fprintln(out)
		switch {
		case ctx.Err() != nil:
			// Zeitlimit und geplatzter Sandkasten sagen nichts über das
			// Werkzeug — hier gibt es nichts zu fragen.
			fmt.Fprintf(out, "Aborted: the time limit of %s has expired.\n", probeTimeout)
		case probeSandboxHatVersagt(stderr.String()):
			fmt.Fprintln(out, "It was not the script that failed but the sandbox itself")
			fmt.Fprintln(out, "(see the bwrap line on stderr) — the tool did not run.")
		default:
			// Die Frage geht an den Menschen, weil nur er sie beantworten
			// kann: auf stderr steht, woran es lag, und ein „Permission
			// denied" heißt hier etwas anderes als ein TypeError. Bleibt
			// die Antwort aus — kein Terminal, ein Skript, eine
			// Pipeline —, wird NICHT klassifiziert. Das ist die sichere
			// Richtung: ein zu Unrecht gesetztes `invalid` sperrt auch
			// das erneute Testen.
			fmt.Fprintln(out, "It failed inside the sandbox. That may be the tool")
			fmt.Fprintln(out, "or its boundaries: no network, no write rights, no $HOME.")
			fmt.Fprintln(out, "No machine sees the difference — it is on stderr above.")
			fmt.Fprint(out, "\nWas it the tool? [y/N] ")
			antwort, _ := bufio.NewReader(in).ReadString('\n')
			switch strings.ToLower(strings.TrimSpace(antwort)) {
			case "j", "ja", "y", "yes":
				zustand, err := vermerkeProbelauf(root, gefunden, argv, exitCode, "")
				if err != nil {
					fmt.Fprintf(errw, "trial run not recorded: %v\n", err)
					return 1
				}
				fmt.Fprintf(out, "\nState: %s — %s\n", zustand, zustand.Erklaerung())
				fmt.Fprintln(out, "No Hase gets it while that stays the case.")
				return 1
			}
		}
		fmt.Fprintf(out, "\nState: %s — unchanged.\n", gefunden.Zustand)
		fmt.Fprintln(out, "Nothing is refuted by that. For a verdict from the machine,")
		fmt.Fprintln(out, "read the script and run under real conditions:")
		fmt.Fprintf(out, "\n  hasenbau tool test %s -%s %s\n", gefunden.Name, probeSandboxFlag, strings.Join(argv, " "))
		return 1
	}

	// Der Vergleich mit der Vorhersage. Er ist die einzige Stelle, an der
	// eine Maschine über die AUSGABE urteilen darf — und sie darf es nur
	// in eine Richtung: eine Abweichung widerlegt, was der Schmied
	// behauptet hat. Eine Übereinstimmung bestätigt nichts, denn
	// Vorhersage und Skript stammen vom selben Modell.
	expect := ""
	if vergleichen && exitCode == 0 {
		if strings.TrimSpace(stdout.String()) == strings.TrimSpace(gefunden.Beispiel.Erwartet) {
			expect = bau.ExpectMatch
		} else {
			expect = bau.ExpectMismatch
		}
		fmt.Fprintln(out)
		if expect == bau.ExpectMismatch {
			fmt.Fprintln(out, "NOT what the Schmied predicted:")
			fmt.Fprintf(out, "  expected: %s\n", einzeiler(gefunden.Beispiel.Erwartet))
			fmt.Fprintf(out, "  got:      %s\n", einzeiler(stdout.String()))
			fmt.Fprintln(out, "Exit 0 means it ran — this means it did the wrong thing.")
		} else {
			fmt.Fprintln(out, "Matches what the Schmied predicted.")
			fmt.Fprintln(out, "He could not run it, so he had to know it — that is the point.")
		}
	}

	// Der Probelauf KLASSIFIZIERT: er trägt sich in den Review-Block
	// ein und macht daraus `actual` oder `invalid`. Genau so verlangt es
	// die Intentionssemantik — durch Verifikation, nicht durch Setzen.
	zustand, err := vermerkeProbelauf(root, gefunden, argv, exitCode, expect)
	if err != nil {
		fmt.Fprintf(errw, "trial run not recorded: %v\n", err)
		return 1
	}
	fmt.Fprintf(out, "\nState: %s — %s\n", zustand, zustand.Erklaerung())
	switch zustand {
	case bau.Hypothetical:
		// Bewusst KEIN actual. Der Probelauf zeigt, dass es lief, nicht
		// dass es stimmt — das Urteil über die Ausgabe fällt beim
		// Freigeben, und zwar ein Mensch.
		fmt.Fprintln(out, "The trial run is evidence, not a verdict: it shows that it ran,")
		fmt.Fprintln(out, "not that the output above is right. That is your call.")
		if gefunden.Entwurf {
			fmt.Fprintf(out, "\n  hasenbau tool release %s   — if the output is right\n", gefunden.Name)
		}
	case bau.Invalid:
		fmt.Fprintln(out, "No Hase gets it while that stays the case.")
	}
	// Der Exit des BEFEHLS folgt dem Zustand, nicht dem des Skripts.
	// Beides fällt sonst auseinander, sobald eine Vorhersage widerlegt
	// wird: das Skript endet mit 0, das Werkzeug ist trotzdem `invalid`,
	// und wer `tool test` in einem Skript aufruft, hielte das für Erfolg.
	if exitCode != 0 || zustand == bau.Invalid {
		return 1
	}
	return 0
}

// einzeiler macht eine Ausgabe in einer Zeile zitierbar. Mehrzeiliges
// wird nach der ersten Zeile abgeschnitten — die volle Ausgabe steht
// ohnehin darüber, hier geht es nur um den Vergleich.
func einzeiler(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "(empty)"
	}
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strconv.Quote(s[:i]) + " …"
	}
	return strconv.Quote(s)
}

// vermerkeProbelauf schreibt das Ergebnis in den Review-Block. Der Hash
// bleibt dabei unberührt — er läuft über den Body OHNE Block, ein
// Eintrag im Block macht das Review also nicht ungültig.
func vermerkeProbelauf(root string, t *bau.Tool, argv []string, exitCode int, expect string) (bau.Zustand, error) {
	pfad := filepath.Join(root, t.Skript)
	roh, err := os.ReadFile(pfad)
	if err != nil {
		return "", err
	}
	review, body := bau.LiesReview(roh)
	review.VerifiedAt = bau.JetztStempel()
	review.VerifiedWith = strings.Join(argv, " ")
	review.VerifiedExit = &exitCode
	review.VerifiedExpect = expect

	neu := bau.SchreibeReviewBlock(review, body)
	info, err := os.Stat(pfad)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(pfad, []byte(neu), info.Mode().Perm()); err != nil {
		return "", err
	}
	// Den Manifest-Hash frisch rechnen statt den aus dem Block zu
	// nehmen: sonst verglichen wir ihn mit sich selbst und bekämen nie
	// `outdated` zu sehen.
	manifestRoh, err := os.ReadFile(filepath.Join(root, t.Manifest))
	if err != nil {
		return "", err
	}
	return bau.LeiteZustandAb(review, body, bau.ManifestHash(string(manifestRoh))), nil
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
			fmt.Fprintf(errw, "hasenbau tool test: %q — expected --<arg> <value>\n%s", arg, manifestArgs(t))
			return nil, 2
		}
		name := strings.TrimPrefix(arg, "--")
		if gleich := strings.SplitN(name, "=", 2); len(gleich) == 2 {
			name = gleich[0]
			if !bekannt[name] {
				fmt.Fprintf(errw, "hasenbau tool test: the manifest does not know %q\n%s", name, manifestArgs(t))
				return nil, 2
			}
			werte[name] = gleich[1]
			continue
		}
		if !bekannt[name] {
			fmt.Fprintf(errw, "hasenbau tool test: the manifest does not know %q\n%s", name, manifestArgs(t))
			return nil, 2
		}
		if i+1 >= len(rest) {
			fmt.Fprintf(errw, "hasenbau tool test: --%s without a value\n", name)
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
		fmt.Fprintf(errw, "hasenbau tool test: required argument missing: %s\n%s",
			strings.Join(fehlend, ", "), manifestArgs(t))
		return nil, 2
	}
	return werte, 0
}

// zeilen ist der billigste Hinweis darauf, wie viel da zu lesen ist —
// acht Zeilen überfliegt man, achthundert nicht, und der Unterschied
// gehört vor die Ausführung.
func zeilen(pfad string) int {
	roh, err := os.ReadFile(pfad)
	if err != nil {
		return 0
	}
	return bytes.Count(roh, []byte("\n"))
}

func manifestArgs(t *bau.Tool) string {
	if len(t.Args) == 0 {
		return "The manifest names no arguments.\n"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "According to the manifest (%s):\n", t.Manifest)
	for _, a := range t.Args {
		pflicht := "optional"
		if a.Pflicht {
			pflicht = "required"
		}
		fmt.Fprintf(&b, "  --%s <%s>  %s (%s)\n", a.Name, a.Typ, a.Beschreibung, pflicht)
	}
	return b.String()
}

func vorhandene(werkzeuge []bau.Tool) string {
	if len(werkzeuge) == 0 {
		return "The Bau has no tools.\n"
	}
	var namen []string
	for _, t := range werkzeuge {
		if t.Entwurf {
			namen = append(namen, t.Name+" (draft)")
			continue
		}
		namen = append(namen, t.Name)
	}
	return "Available: " + strings.Join(namen, ", ") + "\n"
}

// schreibeKanal zeigt einen Ausgabekanal so, dass leer auch als leer zu
// erkennen ist — ein Werkzeug, das schweigend Exit 0 meldet, ist ein
// Befund.
func schreibeKanal(out io.Writer, name, inhalt string) {
	if strings.TrimSpace(inhalt) == "" {
		fmt.Fprintf(out, "%s: (empty)\n", name)
		return
	}
	fmt.Fprintf(out, "%s:\n", name)
	for _, zeile := range strings.Split(strings.TrimRight(inhalt, "\n"), "\n") {
		fmt.Fprintf(out, "  %s\n", zeile)
	}
}

// toolReview führt durch das Lesen und schreibt danach den Block.
//
// Der Befehl ist NUR eine bequeme Art, ein Artefakt herzustellen — das
// Format steht in PLAN §4 und lässt sich von Hand oder mit einer GUI
// erzeugen. Der Hasenbau prüft überall nur die Eigenschaft (Block
// vollständig, Hash passt), nie die Herkunft.
func toolReview(root string, args []string, in io.Reader, out, errw io.Writer) int {
	werkzeuge, err := bau.LadeTools(root)
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	// Am ZEIGER hängt der Abbruch, nicht am Code: waehleFuerReview hat
	// drei Ausgänge, aber nur zwei Sorten Rückgabe — "nichts wartet" ist
	// kein Fehler und kommt als (nil, 0). Wer hier `code != 0` prüft,
	// läuft mit einem nil-Zeiger weiter (Hasenbau-4rh).
	t, code := waehleFuerReview(werkzeuge, args, out, errw)
	if t == nil {
		return code
	}

	zeigeHerkunft(root, t, out)
	fmt.Fprintf(out, "\n%s — %s\n", t.Name, t.Beschreibung)
	fmt.Fprint(out, manifestArgs(t))
	fmt.Fprintf(out, "\nScript: %s (%d lines)\n", t.Skript, t.Zeilen)

	// Ein Entwurf, der schon einen Block trägt, ist ein Befund: den hat
	// kein Mensch gesetzt, also hat der Schmied das Format nachgeahmt.
	if t.Review.By != "" || t.Review.Fehler != "" {
		fmt.Fprintln(out, "\nCAUTION: this draft already carries a review block.")
		fmt.Fprintln(out, "Nobody wrote it by hand — the model is imitating the format.")
		fmt.Fprintln(out, "It will be replaced and does not count; read especially carefully.")
	}

	pfad := filepath.Join(root, t.Skript)
	if code := oeffneEditor(pfad, out, errw); code != 0 {
		return code
	}

	// Nach dem Editor neu lesen: der Reviewer darf korrigieren, und der
	// Hash muss auf den Stand zeigen, den er am Ende gelesen hat.
	roh, err := os.ReadFile(pfad)
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	_, body := bau.LiesReview(roh)

	leser := bufio.NewReader(in)
	name := frage(leser, out, "Your name", gitName(root))
	does := frage(leser, out, "What does the script do? (in your own words)", "")
	safe := frage(leser, out, "Why is it harmless?", "")
	if strings.TrimSpace(name) == "" || strings.TrimSpace(does) == "" || strings.TrimSpace(safe) == "" {
		fmt.Fprintln(errw, "\naborted: all three answers are required.")
		fmt.Fprintln(errw, "The third is the real one — it cannot be answered")
		fmt.Fprintln(errw, "without having looked.")
		return 1
	}

	// Das Manifest gehört zum Gelesenen: `description` sagt einem Modell,
	// wofür es das Werkzeug ruft, `example` trägt die Vorhersage des
	// Schmieds. Ohne diesen Hash wäre der Block unvollständig, und das
	// Werkzeug bliebe nach seinem eigenen Review `generated`.
	manifestRoh, err := os.ReadFile(filepath.Join(root, t.Manifest))
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	review := bau.Review{
		By: name, At: bau.JetztStempel(), Does: does, Safe: safe,
		ManifestHash: bau.ManifestHash(string(manifestRoh)),
	}
	info, err := os.Stat(pfad)
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	if err := os.WriteFile(pfad, []byte(bau.SchreibeReviewBlock(review, body)), info.Mode().Perm()); err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}

	fmt.Fprintf(out, "\n%s is now %s — %s\n", t.Name, bau.Hypothetical, bau.Hypothetical.Erklaerung())
	fmt.Fprintf(out, "Next, show that it does what you say:\n  hasenbau tool test %s", t.Name)
	for _, a := range t.Args {
		if a.Pflicht {
			fmt.Fprintf(out, " --%s <%s>", a.Name, a.Typ)
		}
	}
	fmt.Fprintln(out)
	return 0
}

// waehleFuerReview löst <name> oder --next auf. --next nimmt den
// ältesten Entwurf, der noch niemanden gefunden hat — eine Arbeitsliste
// braucht einen Anfang, sonst schaut man sie gar nicht erst an.
func waehleFuerReview(werkzeuge []bau.Tool, args []string, out, errw io.Writer) (*bau.Tool, int) {
	naechster := len(args) == 1 && (args[0] == "--next" || args[0] == "-next")
	if !naechster && len(args) != 1 {
		fmt.Fprintln(errw, "Usage: hasenbau tool review <name>|--next")
		return nil, 2
	}
	if naechster {
		for i := range werkzeuge {
			if werkzeuge[i].Zustand == bau.Generated || werkzeuge[i].Zustand == bau.Outdated {
				return &werkzeuge[i], 0
			}
		}
		fmt.Fprintln(out, "Nothing is waiting for review.")
		return nil, 0
	}
	for i := range werkzeuge {
		if werkzeuge[i].Name == args[0] {
			return &werkzeuge[i], 0
		}
	}
	fmt.Fprintf(errw, "hasenbau tool review: no tool %q\n%s", args[0], vorhandene(werkzeuge))
	return nil, 1
}

// zeigeHerkunft beantwortet die Frage, ohne die man einen Entwurf nicht
// beurteilen kann: warum gibt es das Ding überhaupt?
//
// Abgeleitet statt gespeichert — der Lauf, in dessen Zeitfenster die
// Datei geschrieben wurde, ist der, der sie geschrieben hat. Findet sich
// keiner, wird das gesagt und nicht geraten.
func zeigeHerkunft(root string, t *bau.Tool, out io.Writer) {
	info, err := os.Stat(filepath.Join(root, t.Skript))
	if err != nil {
		return
	}
	st, err := store.Open(dbPath(root))
	if err != nil {
		return
	}
	defer st.Close()
	laeufe, err := st.RecentLaeufe(200)
	if err != nil {
		return
	}
	for _, l := range laeufe {
		if l.Ended == nil || info.ModTime().Before(l.Started) || info.ModTime().After(l.Ended.Add(2*time.Second)) {
			continue
		}
		ausloeser := l.Input
		if ausloeser == "" {
			ausloeser = "—"
		}
		fmt.Fprintf(out, "Origin: Lauf %d (%s), triggered by %s\n", l.ID, l.Auftrag, ausloeser)
		if l.Summary != "" {
			fmt.Fprintf(out, "The Schmied says: %s\n", oneLine(l.Summary))
		}
		if l.Input != "" {
			fmt.Fprintf(out, "The request is in %s — read it before you read the script.\n", l.Input)
		}
		return
	}
	fmt.Fprintln(out, "Origin: no Lauf found whose time window matches the file.")
	fmt.Fprintln(out, "Either someone touched the file later, or it did not come from a Lauf.")
}

// oeffneEditor ist der erzwungene Blick ins Skript. Ohne $EDITOR wird
// nicht geraten, sondern gesagt, was zu tun ist — ein Befehl, der
// stillschweigend nichts öffnet, hätte den Zweck verfehlt.
func oeffneEditor(pfad string, out, errw io.Writer) int {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = os.Getenv("VISUAL")
	}
	if editor == "" {
		fmt.Fprintf(errw, "\n$EDITOR is not set. Read the script, then call again:\n  %s\n", pfad)
		return 1
	}
	fmt.Fprintf(out, "\n%s is opening %s …\n", editor, pfad)
	cmd := exec.Command(editor, pfad)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(errw, "Editor: %v\n", err)
		return 1
	}
	return 0
}

func frage(leser *bufio.Reader, out io.Writer, text, vorgabe string) string {
	if vorgabe != "" {
		fmt.Fprintf(out, "\n%s [%s]: ", text, vorgabe)
	} else {
		fmt.Fprintf(out, "\n%s\n> ", text)
	}
	antwort, _ := leser.ReadString('\n')
	antwort = strings.TrimSpace(antwort)
	if antwort == "" {
		return vorgabe
	}
	return antwort
}

// gitName holt den Vorgabe-Namen aus der Git-Config des Baus. Wer
// reviewt, steht mit Namen dafür ein; der Name soll derselbe sein, der
// auch am Commit steht.
func gitName(root string) string {
	cmd := exec.Command("git", "-C", root, "config", "user.name")
	roh, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(roh))
}

// toolRelease ist der dritte Schritt und der einzige, der etwas
// verschiebt. Er verlangt `actual` — gelesen UND gezeigt.
func toolRelease(root, name string, ja bool, in io.Reader, out, errw io.Writer) int {
	werkzeuge, err := bau.LadeTools(root)
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	// Gesucht wird über den NAMEN, nicht über den Ablageort. Der Entwurf
	// hat Vorrang, wenn es beide gibt — aber ein Werkzeug, das schon in
	// released/ liegt, muss ebenfalls freigegeben werden können.
	//
	// Der Grund steht in PLAN §12: release ist nicht das Verschieben,
	// sondern die FRAGE; das Verschieben ist die Folge. Wer den Vorgang
	// an den Ablageort bindet, sperrt genau den Fall aus, der ihn am
	// nötigsten hat — ein neues Review ersetzt den released-by-Eintrag,
	// und das Werkzeug steht dann in released/ auf `hypothetical`, für
	// keinen Hasen sichtbar und ohne Weg zurück (Hasenbau-sng).
	var t *bau.Tool
	for i := range werkzeuge {
		if werkzeuge[i].Name != name {
			continue
		}
		if t == nil || werkzeuge[i].Entwurf {
			t = &werkzeuge[i]
		}
	}
	if t == nil {
		fmt.Fprintf(errw, "hasenbau tool release: no tool %q\n%s", name, vorhandene(werkzeuge))
		return 1
	}
	if t.Zustand != bau.Hypothetical {
		fmt.Fprintf(errw, "hasenbau tool release: %s is %s — %s\n", name, t.Zustand, t.Zustand.Erklaerung())
		switch t.Zustand {
		case bau.Invalid:
			fmt.Fprintln(errw, "\nThe trial run failed. Fix it first, then read it again.")
		case bau.Actual:
			fmt.Fprintln(errw, "\nIt is released already.")
		default:
			fmt.Fprintf(errw, "\n  hasenbau tool review %s\n", name)
		}
		return 1
	}
	// Ein Probelauf muss stattgefunden haben — sonst gäbe es nichts, was
	// der Mensch beurteilen könnte.
	if t.Review.VerifiedExit == nil {
		fmt.Fprintf(errw, "hasenbau tool release: %s has never run.\n", name)
		fmt.Fprintf(errw, "Releasing means confirming the output is right — so there has to be one.\n")
		fmt.Fprintf(errw, "\n  hasenbau tool test %s", name)
		for _, a := range t.Args {
			if a.Pflicht {
				fmt.Fprintf(errw, " --%s <%s>", a.Name, a.Typ)
			}
		}
		fmt.Fprintln(errw)
		return 1
	}

	// Der eigentliche Verifikationsakt: nicht das Verschieben, sondern
	// das Urteil über die Ausgabe. `actual` heißt „entspricht der
	// Realität", und das kann nur ein Mensch feststellen.
	fmt.Fprintf(out, "%s (%s, %d lines)\n", t.Name, t.Skript, t.Zeilen)
	fmt.Fprintf(out, "read by %s: %s\n", t.Review.By, t.Review.Does)
	fmt.Fprintf(out, "trial run on %s with %s, exit 0\n", t.Review.VerifiedAt, t.Review.VerifiedWith)
	if !ja {
		fmt.Fprintf(out, "\nWas the output of that trial run right? [y/N] ")
		antwort, _ := bufio.NewReader(in).ReadString('\n')
		switch strings.ToLower(strings.TrimSpace(antwort)) {
		case "j", "ja", "y", "yes":
		default:
			if t.Entwurf {
				fmt.Fprintln(out, "aborted, nothing moved")
			} else {
				fmt.Fprintln(out, "aborted, nothing confirmed — it stays hypothetical")
			}
			return 0
		}
	}
	freigeber := gitName(root)
	if freigeber == "" {
		freigeber = "unknown"
	}

	// Erst das Urteil in die Datei, dann verschieben: bricht das
	// Verschieben ab, steht der Freigeber wenigstens im Entwurf und
	// niemand muss noch einmal lesen.
	if err := vermerkeFreigabe(root, t, freigeber); err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	// Der ganze ORDNER wandert, nicht Datei für Datei: das Beispiel geht
	// mit, und damit bleibt der Probelauf nach der Freigabe fahrbar —
	// gerade dann, wenn ein Werkzeug später `outdated` wird und jemand
	// wissen will, was es einmal getan hat.
	ziel := filepath.Join(bau.ToolsDir, t.Name)
	if t.Entwurf {
		if _, err := os.Stat(filepath.Join(root, ziel)); err == nil {
			fmt.Fprintf(errw, "hasenbau tool release: %s is already there — nothing moved\n", ziel)
			return 1
		}
		if err := os.MkdirAll(filepath.Join(root, bau.ToolsDir), 0o755); err != nil {
			fmt.Fprintln(errw, err)
			return 1
		}
		if err := os.Rename(filepath.Join(root, t.Ordner), filepath.Join(root, ziel)); err != nil {
			fmt.Fprintln(errw, err)
			return 1
		}
		fmt.Fprintf(out, "moved: %s → %s\n", t.Ordner, ziel)
	} else {
		// Schon am Ziel — dann war dies eine reine Bestätigung, und die
		// ist der eigentliche Vorgang. Sagen muss man es trotzdem, sonst
		// wirkt der Befehl folgenlos.
		fmt.Fprintf(out, "already in %s — confirmed, nothing moved\n", ziel)
	}
	fmt.Fprintf(out, "\n%s is %s — %s\n", name, bau.Actual, bau.Actual.Erklaerung())
	fmt.Fprintf(out, "It gets registered at the next server start —\n")
	fmt.Fprintln(out, "and only if the hash still matches the review then.")
	fmt.Fprintf(out, "A Hase only gets it once an Auftrag names it in its `tools:`.\n")
	return 0
}

// describeTool zeigt ein Werkzeug im Detail — aber NICHT sein Skript.
// `describe` ist kein `cat` (PLAN §12), und hier wäre es sogar
// schädlich: ein Skript im Terminal überflogen zu haben, fühlt sich wie
// Lesen an. Wer lesen will, nimmt `tool review`.
func describeTool(root, name string, out, errw io.Writer) int {
	werkzeuge, err := bau.LadeTools(root)
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	var t *bau.Tool
	for i := range werkzeuge {
		if werkzeuge[i].Name == name {
			if t == nil || werkzeuge[i].Entwurf {
				t = &werkzeuge[i]
			}
		}
	}
	if t == nil {
		fmt.Fprintf(errw, "hasenbau describe tool: no tool %q\n%s", name, vorhandene(werkzeuge))
		return 1
	}

	ab := newSection(out)
	ab.field("Tool", "%s", t.Name)
	ab.field("State", "%s — %s", t.Zustand, t.Zustand.Erklaerung())
	// Die Abweichung ist selbst ein Befund, und zwar der wichtigste:
	// `valintent:` in der Datei kann nur so frisch sein wie der letzte
	// Schreibvorgang, und `outdated` steht dort nie. Wer bloß die Datei
	// öffnet, liest also womöglich „actual" über einem Werkzeug, das
	// niemandem mehr zur Verfügung steht.
	if t.Review.ValIntent != "" && bau.Zustand(t.Review.ValIntent) != t.Zustand {
		ab.field("", "the block says %q — the file was changed since it was last", t.Review.ValIntent)
		ab.field("", "written; the derived value above is what counts")
	}
	ort := bau.ToolsDir + "/ (released)"
	if t.Entwurf {
		ort = bau.ToolsEntwurfDir + "/ (not released)"
	}
	ab.field("Ort", "%s", ort)
	ab.field("Script", "%s (%d lines)", t.Skript, t.Zeilen)
	ab.field("Manifest", "%s", t.Manifest)
	ab.done()

	// Die description ist sicherheitsrelevant: sie entscheidet, WANN ein
	// Hase das Werkzeug ruft. Deshalb steht sie hier wörtlich.
	fmt.Fprintf(out, "\nWhat the model reads\n  %s\n", t.Beschreibung)
	if len(t.Args) > 0 {
		fmt.Fprintln(out, "\nArguments")
		for _, a := range t.Args {
			pflicht := "optional"
			if a.Pflicht {
				pflicht = "required"
			}
			fmt.Fprintf(out, "  --%s <%s>  %s (%s)\n", a.Name, a.Typ, a.Beschreibung, pflicht)
		}
	}

	fmt.Fprintln(out, "\nReview")
	switch {
	case t.Review.Fehler != "":
		fmt.Fprintf(out, "  unusable: %s\n", t.Review.Fehler)
		fmt.Fprintln(out, "  Counts as unread. A block no human wrote")
		fmt.Fprintln(out, "  comes from the Schmied — that is a finding.")
	case t.Review.By == "":
		fmt.Fprintln(out, "  none — nobody has read this script.")
	default:
		fmt.Fprintf(out, "  read by  %s on %s\n", t.Review.By, t.Review.At)
		fmt.Fprintf(out, "  supposedly does  %s\n", t.Review.Does)
		fmt.Fprintf(out, "  harmless because  %s\n", t.Review.Safe)
		if t.Zustand == bau.Outdated {
			fmt.Fprintln(out, "  BUT: the script has changed since. The review applies to")
			fmt.Fprintln(out, "  different content than what is there now.")
		}
	}
	if t.Review.VerifiedAt != "" {
		ausgang := "failed"
		if t.Review.VerifiedExit != nil && *t.Review.VerifiedExit == 0 {
			ausgang = "passed"
		}
		fmt.Fprintf(out, "  Trial run  %s on %s (%s)\n", ausgang, t.Review.VerifiedAt, t.Review.VerifiedWith)
	} else if t.Review.By != "" {
		fmt.Fprintln(out, "  Trial run  none — claimed, not shown")
	}

	auftraege, ladefehler := loadDefinitions(root)
	var nennen []string
	for _, a := range auftraege {
		for _, w := range a.Tools {
			if w == t.Name {
				nennen = append(nennen, a.Name)
			}
		}
	}
	fmt.Fprintln(out, "\nReleased for")
	switch {
	case ladefehler != nil:
		fmt.Fprintf(out, "  cannot be determined: %v\n", ladefehler)
	case len(nennen) == 0:
		fmt.Fprintln(out, "  no Auftrag — no Hase gets to see it")
	default:
		fmt.Fprintf(out, "  %s\n", strings.Join(nennen, ", "))
		if !t.Einsatzbereit() {
			fmt.Fprintf(out, "  BUT: %s — named is not ready for use, the Hase does not get it.\n", t.Zustand)
		}
	}
	fmt.Fprintf(out, "\nRead it: hasenbau tool review %s\n", t.Name)
	return 0
}

// vermerkeFreigabe trägt ein, wer die Ausgabe für richtig befunden hat.
// Genau dieser Eintrag macht `actual` — nicht das Verschieben und nicht
// der Exit-Code des Probelaufs.
func vermerkeFreigabe(root string, t *bau.Tool, wer string) error {
	pfad := filepath.Join(root, t.Skript)
	roh, err := os.ReadFile(pfad)
	if err != nil {
		return err
	}
	review, body := bau.LiesReview(roh)
	review.ReleasedBy = wer
	review.ReleasedAt = bau.JetztStempel()
	info, err := os.Stat(pfad)
	if err != nil {
		return err
	}
	return os.WriteFile(pfad, []byte(bau.SchreibeReviewBlock(review, body)), info.Mode().Perm())
}

// toolState beantwortet genau eine Frage, und zwar für eine MASCHINE:
// darf dieses Werkzeug registriert werden?
//
// Es gibt diesen Befehl, weil das Bau-Plugin die Antwort braucht und die
// Regel nicht kennen soll. PLAN §3 sagt das über dieses Plugin
// ausdrücklich — „die Regel steht NICHT hier, das Plugin meldet an das
// Binary und tut, was zurückkommt" —, und für den Sandbox-Wächter galt
// es von Anfang an; die Review-Prüfung rechnete sie dagegen selbst nach
// und ist genau deshalb einmal abgedriftet (Hasenbau-7or/cko).
//
// Der Exit-Code trägt die Antwort, damit ein Aufrufer nichts parsen
// muss: 0 = registrieren, 1 = nicht, 2 = die Frage ergibt keinen Sinn.
// Auf stdout steht der Grund, in einer Zeile — er landet im Server-Log
// des Baus, wo sonst niemand erführe, warum ein Werkzeug fehlt.
func toolState(root, name string, out, errw io.Writer) int {
	werkzeuge, err := bau.LadeTools(root)
	if err != nil {
		fmt.Fprintln(errw, err)
		return 2
	}
	// Freigegebenes hat hier Vorrang, anders als bei `test`: gefragt wird
	// nach dem Werkzeug, das ein Hase bekäme, und das liegt in tools/.
	var gefunden *bau.Tool
	for i := range werkzeuge {
		if werkzeuge[i].Name != name {
			continue
		}
		if gefunden == nil || !werkzeuge[i].Entwurf {
			gefunden = &werkzeuge[i]
		}
	}
	if gefunden == nil {
		fmt.Fprintf(errw, "hasenbau tool state: no tool %q\n", name)
		return 2
	}
	if !gefunden.Einsatzbereit() {
		grund := string(gefunden.Zustand) + " — " + gefunden.Zustand.Erklaerung()
		if gefunden.Entwurf {
			grund = "draft, not released"
		}
		fmt.Fprintln(out, grund)
		return 1
	}
	fmt.Fprintln(out, string(gefunden.Zustand))
	return 0
}
