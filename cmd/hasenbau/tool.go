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
	"strings"
	"time"

	"github.com/Moritz-Dorn/Hasenbau/internal/bau"
	"github.com/Moritz-Dorn/Hasenbau/internal/store"
)

const toolUsage = `Aufruf: hasenbau tool <verb> …

  review [<name>|--next]        lesen und verantworten
  test <name> [--<arg> <wert>]  einmal ausführen und zeigen, was kam
        [-` + probeSandboxFlag + `]           ohne Sandkasten, unter Ernstfall-Bedingungen
  release <name>                nach ` + bau.ToolsDir + `/ verschieben

Die drei Verben sind eine Reihenfolge, keine Auswahl. Ein Werkzeug
durchläuft sie in dieser Folge, und jeder Schritt setzt den vorigen
voraus:

  generated → review → hypothetical → test → hypothetical → release → actual
  geschrieben,          behauptet;                 mit Beleg;             ein Mensch
  ungelesen             ein Probelauf              der Probelauf          hat die Ausgabe
                        fehlt noch                 belegt, er urteilt     für richtig
                                                   nicht                  befunden

Ein FEHLGESCHLAGENER Probelauf widerlegt und macht invalid. Ein
bestandener macht NICHT actual: Exit 0 heißt „es lief", nicht „es
stimmt". Ob die Ausgabe der Realität entspricht, sieht nur ein Mensch.

` + "`test`" + ` verlangt ein Review, weil er das Skript AUSFÜHRT. Er tut das im
Sandkasten — kein Netz, Bau nur lesbar, kein $HOME —, aber er findet
Fehler, keine Absichten: gegen bösartigen Code ist er nicht die Abwehr,
sondern die Ausführung. Deshalb liest ein Mensch zuerst.

Scheitert er IM Sandkasten, fragt der Befehl nach: der Absturz kann vom
Werkzeug kommen oder von dessen Grenzen, und diesen Unterschied sieht
keine Maschine. Ohne Antwort — im Skript, ohne Terminal — bleibt der
Zustand, wie er war. Ein Urteil ohne Rückfrage gibt es nur unter
Ernstfall-Bedingungen: ` + "`-" + probeSandboxFlag + "`" + `.

Was der Bau kennt, zeigt ` + "`hasenbau get tools`" + `,
was auf Review wartet ` + "`hasenbau get tools -entwuerfe`" + `.
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
	case "release":
		fs := flag.NewFlagSet("tool release", flag.ContinueOnError)
		fs.SetOutput(errw)
		ja := fs.Bool("ja", false, "die Rückfrage überspringen (für Skripte)")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		if fs.NArg() != 1 {
			fmt.Fprintln(errw, "Aufruf: hasenbau tool release [-ja] <name>")
			return 2
		}
		return toolRelease(root, fs.Arg(0), *ja, in, out, errw)
	default:
		fmt.Fprintf(errw, "hasenbau tool: unbekanntes Verb %q\n\n%s", args[0], toolUsage)
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
		fmt.Fprintf(errw, "hasenbau tool test: kein Werkzeug %q\n%s", name, vorhandene(werkzeuge))
		return 1
	}

	// Das Gate: getestet werden darf NUR, was ein gültiges Review trägt
	// — `hypothetical` (gelesen, noch nicht gezeigt) und `actual`
	// (gelesen und gezeigt). Alles andere wird abgewiesen, und zwar mit
	// dem Grund, denn jeder der drei Fälle braucht einen anderen
	// nächsten Schritt.
	if gefunden.Zustand != bau.Hypothetical && gefunden.Zustand != bau.Actual {
		fmt.Fprintf(errw, "hasenbau tool test: %s ist %s — %s\n",
			name, gefunden.Zustand, gefunden.Zustand.Erklaerung())
		switch gefunden.Zustand {
		case bau.Outdated:
			fmt.Fprintf(errw, "Das Review von %s gilt für einen anderen Inhalt als den, der jetzt dasteht.\n", gefunden.Review.By)
		case bau.Invalid:
			// Ein widerlegter Anspruch wird nicht durch Wiederholen
			// wahr. Wer erneut testen will, soll erst wieder lesen —
			// und wer das Skript repariert, kommt ohnehin über
			// `outdated` hierher zurück.
			fmt.Fprintln(errw, "Ein Probelauf hat die Behauptung bereits widerlegt.")
			fmt.Fprintln(errw, "Nochmal laufen lassen macht sie nicht wahr: erst lesen, dann zeigen.")
		default:
			if gefunden.Review.Fehler != "" {
				fmt.Fprintf(errw, "Es liegt zwar ein Review-Block vor, aber er ist unbrauchbar: %s\n", gefunden.Review.Fehler)
				fmt.Fprintln(errw, "Ein Block, den niemand gesetzt hat, kommt vom Schmied — das ist ein Befund.")
			}
			fmt.Fprintln(errw, "Dieser Befehl FÜHRT DAS SKRIPT AUS. Lies es zuerst.")
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

	werte, code := parseToolArgs(gefunden, gefiltert, errw)
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
	ctx, abbruch := probeKontext(sandkasten)
	defer abbruch()
	cmd, kasten := probeCommand(ctx, root, skript, argv, sandkasten)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	fmt.Fprintf(out, "%s  (%s, %d Zeilen)\n", gefunden.Name, gefunden.Skript, zeilen(skript))
	if gefunden.Entwurf {
		// Der Hinweis steht VOR der Ausführung, nicht danach: wer ihn
		// erst im Ergebnis liest, hat das Skript schon laufen lassen.
		fmt.Fprintln(out, "Entwurf — von einem Modell geschrieben, noch nicht freigegeben.")
		fmt.Fprintln(out, "Dieser Befehl FÜHRT IHN AUS. Er findet Fehler, keine Absichten —")
		fmt.Fprintln(out, "lies das Skript, bevor du es probierst.")
	}
	// Unter welchen Bedingungen gelaufen wurde, gehört ÜBER die Ausgabe
	// und nicht darunter: es entscheidet, was sie bedeutet.
	if kasten.active {
		fmt.Fprintf(out, "Sandkasten: kein Netz, der Bau nur lesbar, kein $HOME, Zeitlimit %s.\n", probeTimeout)
	} else {
		fmt.Fprintf(out, "OHNE SANDKASTEN, mit deinen Rechten — %s.\n", kasten.reason)
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
	} else {
		fmt.Fprintln(out, "der Hase bekäme einen Werkzeug-Fehler mit dem Text aus stderr.")
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
			fmt.Fprintf(out, "Abgebrochen: das Zeitlimit von %s ist abgelaufen.\n", probeTimeout)
		case probeSandboxHatVersagt(stderr.String()):
			fmt.Fprintln(out, "Nicht das Skript ist gescheitert, sondern der Sandkasten selbst")
			fmt.Fprintln(out, "(siehe die bwrap-Zeile auf stderr) — gelaufen ist das Werkzeug nicht.")
		default:
			// Die Frage geht an den Menschen, weil nur er sie beantworten
			// kann: auf stderr steht, woran es lag, und ein „Permission
			// denied" heißt hier etwas anderes als ein TypeError. Bleibt
			// die Antwort aus — kein Terminal, ein Skript, eine
			// Pipeline —, wird NICHT klassifiziert. Das ist die sichere
			// Richtung: ein zu Unrecht gesetztes `invalid` sperrt auch
			// das erneute Testen.
			fmt.Fprintln(out, "Gescheitert ist es im Sandkasten. Das kann am Werkzeug liegen")
			fmt.Fprintln(out, "oder an dessen Grenzen: kein Netz, kein Schreibrecht, kein $HOME.")
			fmt.Fprintln(out, "Den Unterschied sieht keine Maschine — er steht oben auf stderr.")
			fmt.Fprint(out, "\nLag es am Werkzeug? [j/N] ")
			antwort, _ := bufio.NewReader(in).ReadString('\n')
			switch strings.ToLower(strings.TrimSpace(antwort)) {
			case "j", "ja", "y", "yes":
				zustand, err := vermerkeProbelauf(root, gefunden, argv, exitCode)
				if err != nil {
					fmt.Fprintf(errw, "Probelauf nicht vermerkt: %v\n", err)
					return 1
				}
				fmt.Fprintf(out, "\nZustand: %s — %s\n", zustand, zustand.Erklaerung())
				fmt.Fprintln(out, "Kein Hase bekommt es, solange das so bleibt.")
				return 1
			}
		}
		fmt.Fprintf(out, "\nZustand: %s — unverändert.\n", gefunden.Zustand)
		fmt.Fprintln(out, "Widerlegt ist damit nichts. Wer ein Urteil der Maschine will,")
		fmt.Fprintln(out, "liest das Skript und läuft unter den Bedingungen des Ernstfalls:")
		fmt.Fprintf(out, "\n  hasenbau tool test %s -%s %s\n", gefunden.Name, probeSandboxFlag, strings.Join(argv, " "))
		return 1
	}

	// Der Probelauf KLASSIFIZIERT: er trägt sich in den Review-Block
	// ein und macht daraus `actual` oder `invalid`. Genau so verlangt es
	// die Intentionssemantik — durch Verifikation, nicht durch Setzen.
	zustand, err := vermerkeProbelauf(root, gefunden, argv, exitCode)
	if err != nil {
		fmt.Fprintf(errw, "Probelauf nicht vermerkt: %v\n", err)
		return 1
	}
	fmt.Fprintf(out, "\nZustand: %s — %s\n", zustand, zustand.Erklaerung())
	switch zustand {
	case bau.Hypothetical:
		// Bewusst KEIN actual. Der Probelauf zeigt, dass es lief, nicht
		// dass es stimmt — das Urteil über die Ausgabe fällt beim
		// Freigeben, und zwar ein Mensch.
		fmt.Fprintln(out, "Der Probelauf ist ein Beleg, kein Urteil: er zeigt, dass es lief,")
		fmt.Fprintln(out, "nicht dass die Ausgabe oben richtig ist. Das entscheidest du.")
		if gefunden.Entwurf {
			fmt.Fprintf(out, "\n  hasenbau tool release %s   — wenn die Ausgabe stimmt\n", gefunden.Name)
		}
	case bau.Invalid:
		fmt.Fprintln(out, "Kein Hase bekommt es, solange das so bleibt.")
	}
	if exitCode != 0 {
		return 1
	}
	return 0
}

// vermerkeProbelauf schreibt das Ergebnis in den Review-Block. Der Hash
// bleibt dabei unberührt — er läuft über den Body OHNE Block, ein
// Eintrag im Block macht das Review also nicht ungültig.
func vermerkeProbelauf(root string, t *bau.Tool, argv []string, exitCode int) (bau.Zustand, error) {
	pfad := filepath.Join(root, t.Skript)
	roh, err := os.ReadFile(pfad)
	if err != nil {
		return "", err
	}
	review, body := bau.LiesReview(roh)
	review.VerifiedAt = bau.JetztStempel()
	review.VerifiedWith = strings.Join(argv, " ")
	review.VerifiedExit = &exitCode

	neu := bau.SchreibeReviewBlock(review, body)
	info, err := os.Stat(pfad)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(pfad, []byte(neu), info.Mode().Perm()); err != nil {
		return "", err
	}
	return bau.LeiteZustandAb(review, body), nil
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
	t, code := waehleFuerReview(werkzeuge, args, out, errw)
	if code != 0 {
		return code
	}

	zeigeHerkunft(root, t, out)
	fmt.Fprintf(out, "\n%s — %s\n", t.Name, t.Beschreibung)
	fmt.Fprint(out, manifestArgs(t))
	fmt.Fprintf(out, "\nSkript: %s (%d Zeilen)\n", t.Skript, t.Zeilen)

	// Ein Entwurf, der schon einen Block trägt, ist ein Befund: den hat
	// kein Mensch gesetzt, also hat der Schmied das Format nachgeahmt.
	if t.Review.By != "" || t.Review.Fehler != "" {
		fmt.Fprintln(out, "\nACHTUNG: dieser Entwurf trägt bereits einen Review-Block.")
		fmt.Fprintln(out, "Gesetzt hat ihn niemand von Hand — das Modell ahmt das Format nach.")
		fmt.Fprintln(out, "Er wird ersetzt und zählt nicht; lies besonders genau.")
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
	name := frage(leser, out, "Dein Name", gitName(root))
	does := frage(leser, out, "Was tut das Skript? (in deinen Worten)", "")
	safe := frage(leser, out, "Warum ist es unbedenklich?", "")
	if strings.TrimSpace(name) == "" || strings.TrimSpace(does) == "" || strings.TrimSpace(safe) == "" {
		fmt.Fprintln(errw, "\nabgebrochen: alle drei Angaben sind Pflicht.")
		fmt.Fprintln(errw, "Die dritte ist die eigentliche — man kann sie nicht beantworten,")
		fmt.Fprintln(errw, "ohne hingesehen zu haben.")
		return 1
	}

	review := bau.Review{By: name, At: bau.JetztStempel(), Does: does, Safe: safe}
	info, err := os.Stat(pfad)
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	if err := os.WriteFile(pfad, []byte(bau.SchreibeReviewBlock(review, body)), info.Mode().Perm()); err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}

	fmt.Fprintf(out, "\n%s ist jetzt %s — %s\n", t.Name, bau.Hypothetical, bau.Hypothetical.Erklaerung())
	fmt.Fprintf(out, "Als Nächstes zeigen, dass es tut, was du sagst:\n  hasenbau tool test %s", t.Name)
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
		fmt.Fprintln(errw, "Aufruf: hasenbau tool review <name>|--next")
		return nil, 2
	}
	if naechster {
		for i := range werkzeuge {
			if werkzeuge[i].Zustand == bau.Generated || werkzeuge[i].Zustand == bau.Outdated {
				return &werkzeuge[i], 0
			}
		}
		fmt.Fprintln(out, "Nichts wartet auf Review.")
		return nil, 0
	}
	for i := range werkzeuge {
		if werkzeuge[i].Name == args[0] {
			return &werkzeuge[i], 0
		}
	}
	fmt.Fprintf(errw, "hasenbau tool review: kein Werkzeug %q\n%s", args[0], vorhandene(werkzeuge))
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
		fmt.Fprintf(out, "Herkunft: Lauf %d (%s), ausgelöst von %s\n", l.ID, l.Auftrag, ausloeser)
		if l.Summary != "" {
			fmt.Fprintf(out, "Der Schmied sagt: %s\n", oneLine(l.Summary))
		}
		if l.Input != "" {
			fmt.Fprintf(out, "Der Wunsch steht in %s — lies ihn, bevor du das Skript liest.\n", l.Input)
		}
		return
	}
	fmt.Fprintln(out, "Herkunft: kein Lauf gefunden, dessen Zeitfenster zur Datei passt.")
	fmt.Fprintln(out, "Entweder hat jemand die Datei später angefasst, oder sie kam nicht aus einem Lauf.")
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
		fmt.Fprintf(errw, "\n$EDITOR ist nicht gesetzt. Lies das Skript und ruf danach erneut:\n  %s\n", pfad)
		return 1
	}
	fmt.Fprintf(out, "\n%s öffnet %s …\n", editor, pfad)
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
	var t *bau.Tool
	for i := range werkzeuge {
		if werkzeuge[i].Name == name && werkzeuge[i].Entwurf {
			t = &werkzeuge[i]
		}
	}
	if t == nil {
		fmt.Fprintf(errw, "hasenbau tool release: kein Entwurf %q\n%s", name, vorhandene(werkzeuge))
		return 1
	}
	if t.Zustand != bau.Hypothetical {
		fmt.Fprintf(errw, "hasenbau tool release: %s ist %s — %s\n", name, t.Zustand, t.Zustand.Erklaerung())
		switch t.Zustand {
		case bau.Invalid:
			fmt.Fprintln(errw, "\nDer Probelauf ist gescheitert. Erst reparieren, dann neu lesen.")
		case bau.Actual:
			fmt.Fprintln(errw, "\nEs ist bereits freigegeben.")
		default:
			fmt.Fprintf(errw, "\n  hasenbau tool review %s\n", name)
		}
		return 1
	}
	// Ein Probelauf muss stattgefunden haben — sonst gäbe es nichts, was
	// der Mensch beurteilen könnte.
	if t.Review.VerifiedExit == nil {
		fmt.Fprintf(errw, "hasenbau tool release: %s ist nie gelaufen.\n", name)
		fmt.Fprintf(errw, "Freigeben heißt zu bestätigen, dass die Ausgabe stimmt — dafür muss es eine geben.\n")
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
	fmt.Fprintf(out, "%s (%s, %d Zeilen)\n", t.Name, t.Skript, t.Zeilen)
	fmt.Fprintf(out, "gelesen von %s: %s\n", t.Review.By, t.Review.Does)
	fmt.Fprintf(out, "Probelauf am %s mit %s, Exit 0\n", t.Review.VerifiedAt, t.Review.VerifiedWith)
	if !ja {
		fmt.Fprintf(out, "\nWar die Ausgabe dieses Probelaufs richtig? [j/N] ")
		antwort, _ := bufio.NewReader(in).ReadString('\n')
		switch strings.ToLower(strings.TrimSpace(antwort)) {
		case "j", "ja", "y", "yes":
		default:
			fmt.Fprintln(out, "abgebrochen, nichts verschoben")
			return 0
		}
	}
	freigeber := gitName(root)
	if freigeber == "" {
		freigeber = "unbekannt"
	}

	// Erst das Urteil in die Datei, dann verschieben: bricht das
	// Verschieben ab, steht der Freigeber wenigstens im Entwurf und
	// niemand muss noch einmal lesen.
	if err := vermerkeFreigabe(root, t, freigeber); err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	for _, quelle := range []string{t.Skript, t.Manifest} {
		ziel := filepath.Join(bau.ToolsDir, filepath.Base(quelle))
		if _, err := os.Stat(filepath.Join(root, ziel)); err == nil {
			fmt.Fprintf(errw, "hasenbau tool release: %s liegt schon da — nichts verschoben\n", ziel)
			return 1
		}
		if err := os.Rename(filepath.Join(root, quelle), filepath.Join(root, ziel)); err != nil {
			fmt.Fprintln(errw, err)
			return 1
		}
		fmt.Fprintf(out, "verschoben: %s → %s\n", quelle, ziel)
	}
	fmt.Fprintf(out, "\n%s ist %s — %s\n", name, bau.Actual, bau.Actual.Erklaerung())
	fmt.Fprintf(out, "Es wird beim nächsten Server-Start registriert —\n")
	fmt.Fprintln(out, "und nur dann, wenn der Hash dann noch zum Review passt.")
	fmt.Fprintf(out, "Bekommen tut es ein Hase erst, wenn ein Auftrag es in seinem `tools:` nennt.\n")
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
		fmt.Fprintf(errw, "hasenbau describe tool: kein Werkzeug %q\n%s", name, vorhandene(werkzeuge))
		return 1
	}

	ab := newSection(out)
	ab.field("Werkzeug", "%s", t.Name)
	ab.field("Zustand", "%s — %s", t.Zustand, t.Zustand.Erklaerung())
	// Die Abweichung ist selbst ein Befund, und zwar der wichtigste:
	// `valintent:` in der Datei kann nur so frisch sein wie der letzte
	// Schreibvorgang, und `outdated` steht dort nie. Wer bloß die Datei
	// öffnet, liest also womöglich „actual" über einem Werkzeug, das
	// niemandem mehr zur Verfügung steht.
	if t.Review.ValIntent != "" && bau.Zustand(t.Review.ValIntent) != t.Zustand {
		ab.field("", "im Block steht %q — die Datei wurde seit dem letzten", t.Review.ValIntent)
		ab.field("", "Schreiben geändert; gilt der abgeleitete Wert oben")
	}
	ort := bau.ToolsDir + "/ (freigegeben)"
	if t.Entwurf {
		ort = bau.ToolsEntwurfDir + "/ (nicht freigegeben)"
	}
	ab.field("Ort", "%s", ort)
	ab.field("Skript", "%s (%d Zeilen)", t.Skript, t.Zeilen)
	ab.field("Manifest", "%s", t.Manifest)
	ab.done()

	// Die description ist sicherheitsrelevant: sie entscheidet, WANN ein
	// Hase das Werkzeug ruft. Deshalb steht sie hier wörtlich.
	fmt.Fprintf(out, "\nWas das Modell liest\n  %s\n", t.Beschreibung)
	if len(t.Args) > 0 {
		fmt.Fprintln(out, "\nArgumente")
		for _, a := range t.Args {
			pflicht := "optional"
			if a.Pflicht {
				pflicht = "Pflicht"
			}
			fmt.Fprintf(out, "  --%s <%s>  %s (%s)\n", a.Name, a.Typ, a.Beschreibung, pflicht)
		}
	}

	fmt.Fprintln(out, "\nReview")
	switch {
	case t.Review.Fehler != "":
		fmt.Fprintf(out, "  unbrauchbar: %s\n", t.Review.Fehler)
		fmt.Fprintln(out, "  Gilt als ungelesen. Ein Block, den kein Mensch gesetzt hat,")
		fmt.Fprintln(out, "  kommt vom Schmied — das ist ein Befund.")
	case t.Review.By == "":
		fmt.Fprintln(out, "  keines — niemand hat dieses Skript gelesen.")
	default:
		fmt.Fprintf(out, "  gelesen von  %s am %s\n", t.Review.By, t.Review.At)
		fmt.Fprintf(out, "  tut angeblich  %s\n", t.Review.Does)
		fmt.Fprintf(out, "  unbedenklich weil  %s\n", t.Review.Safe)
		if t.Zustand == bau.Outdated {
			fmt.Fprintln(out, "  ABER: das Skript wurde seither geändert. Das Review gilt für")
			fmt.Fprintln(out, "  einen anderen Inhalt als den, der jetzt dasteht.")
		}
	}
	if t.Review.VerifiedAt != "" {
		ausgang := "gescheitert"
		if t.Review.VerifiedExit != nil && *t.Review.VerifiedExit == 0 {
			ausgang = "bestanden"
		}
		fmt.Fprintf(out, "  Probelauf  %s am %s (%s)\n", ausgang, t.Review.VerifiedAt, t.Review.VerifiedWith)
	} else if t.Review.By != "" {
		fmt.Fprintln(out, "  Probelauf  keiner — behauptet, nicht gezeigt")
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
	fmt.Fprintln(out, "\nFreigegeben für")
	switch {
	case ladefehler != nil:
		fmt.Fprintf(out, "  nicht ermittelbar: %v\n", ladefehler)
	case len(nennen) == 0:
		fmt.Fprintln(out, "  keinen Auftrag — kein Hase bekommt es zu sehen")
	default:
		fmt.Fprintf(out, "  %s\n", strings.Join(nennen, ", "))
		if !t.Einsatzbereit() {
			fmt.Fprintf(out, "  ABER: %s — genannt ist nicht einsatzbereit, der Hase bekommt es nicht.\n", t.Zustand)
		}
	}
	fmt.Fprintf(out, "\nLesen: hasenbau tool review %s\n", t.Name)
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
