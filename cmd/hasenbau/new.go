// new.go: das anlegende Verb der CLI (Hasenbau-ha0.8).
//
// `get` zeigt Zeilen, `describe` zeigt ein Objekt, `new` legt eines an.
// Das Frontmatter-Schema kennt der Hasenbau — der Mensch soll es nicht
// auswendig können müssen. Deshalb entsteht hier kein leeres Blatt,
// sondern ein Gerüst, das seine Felder selbst erklärt: Pflichtfelder
// ausgefüllt, alles Optionale auskommentiert daneben.
//
// Zwei Regeln: Der Befehl überschreibt nie (eine handgeschriebene
// Datei zu ersetzen bleibt eine bewusste Handlung), und er prüft, was
// er geschrieben hat — ein Gerüst, das nicht lädt, wäre ein Fehler des
// Hasenbaus, nicht des Nutzers.
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Moritz-Dorn/Hasenbau/internal/auftrag"
	"github.com/Moritz-Dorn/Hasenbau/internal/hase"
)

const newUsage = `Aufruf: hasenbau new <ressource> <name>

Ressourcen:
  auftrag <name> -hase <hase>   Gerüst für auftraege/<name>.md
  hase <name>                   Gerüst für hasen/<name>.md

Vorhandene Dateien bleiben unangetastet.
`

func cmdNew(root string, args []string, out, errw io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(errw, newUsage)
		return 2
	}
	switch args[0] {
	case "auftrag":
		return newAuftrag(root, args[1:], out, errw)
	case "hase":
		return newHase(root, args[1:], out, errw)
	default:
		fmt.Fprintf(errw, "hasenbau new: unbekannte Ressource %q\n\n%s", args[0], newUsage)
		return 2
	}
}

func newAuftrag(root string, args []string, out, errw io.Writer) int {
	name, haseName, err := parseNewAuftragArgs(args)
	if err != nil {
		fmt.Fprintf(errw, "hasenbau new auftrag: %v\n\n%s", err, newUsage)
		return 2
	}

	if err := auftrag.ValidName(name); err != nil {
		fmt.Fprintf(errw, "hasenbau new auftrag: %v\n", err)
		return 2
	}
	// Der Hase muss existieren, bevor der Auftrag ihn nennt: `Load`
	// prüft das für ALLE Aufträge auf einmal — ein Auftrag mit
	// unbekanntem Hasen legt sonst den ganzen Bau lahm, auch `get` und
	// `describe`. Lieber hier scheitern als dort.
	if code := requireHase(root, haseName, errw); code != 0 {
		return code
	}

	inhalt := auftragGeruest(name, haseName)
	if _, err := auftrag.Parse(name, []byte(inhalt)); err != nil {
		fmt.Fprintf(errw, "hasenbau new auftrag: das Gerüst lädt nicht — das ist ein Fehler im Hasenbau: %v\n", err)
		return 1
	}
	rel := filepath.Join("auftraege", name+".md")
	if code := writeNew(root, rel, inhalt, errw); code != 0 {
		return code
	}

	fmt.Fprintf(out, "angelegt: %s\n\n", rel)
	fmt.Fprintf(out, "Als Nächstes:\n"+
		"  1. Den Markdown-Teil schreiben — er ist der Prompt-Kern, nicht Deko.\n"+
		"  2. Trigger wählen: das Gerüst steht auf manual, watch und cron liegen auskommentiert daneben.\n"+
		"  3. Räume prüfen. Anlegen muss sie niemand, das tut der erste Lauf.\n"+
		"  4. hasenbau describe auftrag %s  — zeigt, was der Bau daraus liest.\n"+
		"  5. hasenbau lauf %s  — einmal von Hand auslösen.\n", name, name)
	return 0
}

func newHase(root string, args []string, out, errw io.Writer) int {
	if len(args) != 1 {
		fmt.Fprint(errw, newUsage)
		return 2
	}
	name := args[0]
	if err := auftrag.ValidName(name); err != nil {
		fmt.Fprintf(errw, "hasenbau new hase: %v\n", err)
		return 2
	}

	rel := filepath.Join("hasen", name+".md")
	if code := writeNew(root, rel, haseGeruest(name), errw); code != 0 {
		return code
	}
	// Erst nach dem Schreiben prüfbar: Lade liest aus dem Bau, weil
	// `knowledge:`-Globs sich gegen den Bau auflösen. Lädt es nicht,
	// bleibt kein Torso liegen.
	if _, err := hase.Lade(root, name); err != nil {
		os.Remove(filepath.Join(root, rel))
		fmt.Fprintf(errw, "hasenbau new hase: das Gerüst lädt nicht — das ist ein Fehler im Hasenbau: %v\n", err)
		return 1
	}

	fmt.Fprintf(out, "angelegt: %s\n\n", rel)
	fmt.Fprintf(out, "Als Nächstes:\n"+
		"  1. Die Rolle schreiben — wer der Hase ist und was er tun soll.\n"+
		"  2. model: setzen, falls nicht der Vorgabe-Provider gelten soll.\n"+
		"  3. hasenbau new auftrag <name> -hase %s  — ein Hase ohne Auftrag läuft nie.\n"+
		"  4. hasenbau describe hase %s  — zeigt die effektiven Permissions je Auftrag.\n", name, name)
	return 0
}

// parseNewAuftragArgs liest Namen und -hase, egal in welcher
// Reihenfolge. Von Hand statt mit flag.FlagSet: das Paket hört beim
// ersten Nicht-Flag auf, und `new auftrag notizen -hase sortierer` ist
// genau die Reihenfolge, die man tippt (kubectl-Schema, Hasenbau-ha0).
func parseNewAuftragArgs(args []string) (name, haseName string, err error) {
	var frei []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-hase" || arg == "--hase":
			if i+1 >= len(args) {
				return "", "", fmt.Errorf("-hase ohne Wert")
			}
			haseName = args[i+1]
			i++
		case strings.HasPrefix(arg, "-hase="), strings.HasPrefix(arg, "--hase="):
			haseName = arg[strings.Index(arg, "=")+1:]
		case strings.HasPrefix(arg, "-"):
			return "", "", fmt.Errorf("unbekanntes Flag %q (es gibt nur -hase)", arg)
		default:
			frei = append(frei, arg)
		}
	}
	switch len(frei) {
	case 1:
		return frei[0], haseName, nil
	case 0:
		return "", "", fmt.Errorf("der Name fehlt")
	default:
		return "", "", fmt.Errorf("mehr als ein Name: %s", strings.Join(frei, ", "))
	}
}

// requireHase besteht darauf, dass der genannte Hase existiert, und
// zählt sonst auf, was es stattdessen gibt.
func requireHase(root, name string, errw io.Writer) int {
	vorhanden := existingHasen(root)
	if name == "" {
		fmt.Fprintf(errw, "hasenbau new auftrag: -hase fehlt — jeder Auftrag braucht einen Hasen.\n%s", hasenHinweis(vorhanden))
		return 2
	}
	if err := auftrag.ValidName(name); err != nil {
		fmt.Fprintf(errw, "hasenbau new auftrag: -hase: %v\n", err)
		return 2
	}
	for _, h := range vorhanden {
		if h == name {
			return 0
		}
	}
	fmt.Fprintf(errw, "hasenbau new auftrag: kein Template hasen/%s.md.\n%s", name, hasenHinweis(vorhanden))
	return 1
}

func hasenHinweis(vorhanden []string) string {
	if len(vorhanden) == 0 {
		return "  Der Bau hat noch keine Hasen — `hasenbau new hase <name>` legt einen an.\n"
	}
	return fmt.Sprintf("  Vorhanden: %s\n  Neu: `hasenbau new hase <name>`\n", strings.Join(vorhanden, ", "))
}

func existingHasen(root string) []string {
	treffer, err := filepath.Glob(filepath.Join(root, "hasen", "*.md"))
	if err != nil {
		return nil
	}
	var namen []string
	for _, t := range treffer {
		namen = append(namen, strings.TrimSuffix(filepath.Base(t), ".md"))
	}
	sort.Strings(namen)
	return namen
}

// writeNew legt die Datei an — und nur, wenn es sie noch nicht gibt.
func writeNew(root, rel, inhalt string, errw io.Writer) int {
	abs := filepath.Join(root, rel)
	if _, err := os.Stat(abs); err == nil {
		fmt.Fprintf(errw, "hasenbau new: %s gibt es schon — anderer Name, oder die Datei selbst bearbeiten.\n", rel)
		return 1
	} else if !os.IsNotExist(err) {
		fmt.Fprintf(errw, "hasenbau new: %s: %v\n", rel, err)
		return 1
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		fmt.Fprintf(errw, "hasenbau new: %v\n", err)
		return 1
	}
	// O_EXCL statt eines zweiten Stat: zwischen Prüfen und Schreiben
	// kann jemand dieselbe Datei anlegen.
	f, err := os.OpenFile(abs, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if os.IsExist(err) {
			fmt.Fprintf(errw, "hasenbau new: %s gibt es schon — anderer Name, oder die Datei selbst bearbeiten.\n", rel)
		} else {
			fmt.Fprintf(errw, "hasenbau new: %s schreiben: %v\n", rel, err)
		}
		return 1
	}
	defer f.Close()
	if _, err := io.WriteString(f, inhalt); err != nil {
		fmt.Fprintf(errw, "hasenbau new: %s schreiben: %v\n", rel, err)
		return 1
	}
	return 0
}

// auftragGeruest ist das kommentierte Auftrags-Skelett. Es lädt so, wie
// es dasteht — sonst stünde der Nutzer vor einem Fehler, den er nicht
// verursacht hat.
func auftragGeruest(name, haseName string) string {
	return fmt.Sprintf(`---
# Auftrag %[1]s — Trigger, Gänge, Hase, Räume (PLAN.md §6).
# Angelegt von `+"`hasenbau new`"+`. Diese Kommentare dürfen weg.

# Genau eine Trigger-Art. Die anderen beiden bleiben auskommentiert.
trigger:
  manual: true                     # läuft nur auf Zuruf: hasenbau lauf %[1]s
  # watch: raeume/eingang/*.txt    # Glob, Bau-relativ; $INPUT ist dann die Datei
  # debounce: 5s                   # nur bei watch: Ruhe vor dem Zugriff
  # cron: "0 7 * * *"              # Standard-Cron, fünf Felder

# Deterministische Vorverarbeitung, läuft VOR dem Hasen. Kein Modell,
# kein Urteil. Ersetzt werden nur $BAU, $INPUT, $WORK, $RAUM_<rolle>
# und $HASENBAU — jeder andere $GROSS-Name ist ein harter Fehler.
# gaenge:
#   - name: extrakt
#     run: gaenge/mein_gang.py "$INPUT" --out "$WORK/extrakt.md"
#     timeout: 120s

hase: %[2]s

# Zeitlimit des LLM-Schritts. Ohne Angabe gilt die Vorgabe (30m).
# hase_timeout: 60m

# Deckel: höchstens so viele Läufe je rollendem Fenster. Gezählt wird
# aus der Lauf-Historie, nicht aus einem Zähler — der Deckel übersteht
# also einen Neustart. Beide Felder oder keines. `+"`hasenbau lauf`"+`
# umgeht ihn, zählt aber mit.
# throttle:
#   max: 5
#   per: 1h

# Routinemäßig melden: dann stehen die Befunde dieses Auftrags in
# `+"`hasenbau status`"+`. Das steuert nur die Meldung — erfasst wird
# ohnehin alles, und `+"`hasenbau findings %[1]s`"+` rechnet auch ohne.
# monitored: true

# Die Rollen sind Konvention, kein Gesetz: input (Drop-Zone), work
# (Scratch dieses Laufs, $WORK), out (Ziel), done (Archiv des
# Rohmaterials), quarantine (was schiefging). Schreibrecht bekommt der
# Hase AUSSCHLIESSLICH für work und out — daraus entstehen seine
# Permissions, nicht aus seiner Rolle.
raeume:
  work: raeume/werkstatt/
  out: raeume/lager/

# Was in den Prompt kommt. Dateien werden gelesen, last_summaries holt
# die Meldungen der letzten Läufe dieses Auftrags.
# context:
#   - file: $WORK/extrakt.md
#   - last_summaries: 3

# Aufräumen nach einem geglückten Lauf (move, copy, delete).
# after:
#   - move: $INPUT -> raeume/archiv/
---

Hier steht, was der Hase tun soll — dieser Text ist der Prompt-Kern und
landet unverändert im Prompt. Schreib ihn als Auftrag an eine Person,
die den Bau kennt, aber diesen Fall nicht: was liegt vor, was soll
daraus werden, woran erkennt sie, dass sie fertig ist.
`, name, haseName)
}

// haseGeruest ist das kommentierte Hasen-Skelett. Was der Rückkanal
// ist, steht bewusst nicht drin: den Absatz hängt der Generator an
// jeden Agenten selbst an (§8 Phase 2) — hier wäre er Doppelung, die
// beim nächsten Wortlaut-Wechsel veraltet.
func haseGeruest(name string) string {
	return fmt.Sprintf(`---
# Hase %[1]s — ein Template, kein opencode-Agent (PLAN.md §6). Der
# Hasenbau generiert daraus pro Auftrag einen eigenen Agenten.
# Angelegt von `+"`hasenbau new`"+`. Diese Kommentare dürfen weg.
description: %[1]s — was dieser Hase tut

# Provider/Modell wie in der Bau-Config. Ohne Angabe wählt opencode.
# model: scc/kit.deepseek-v4-flash-0731
# temperature: 0.2

# Legt dem Prompt das mitgelieferte Wissen über den Hasenbau bei:
# Begriffe, Ablauf eines Laufs, wie man einen Trace liest, Grenzen.
knows_hasenbau: true

# Eigene Texte aus dem Bau, Bau-relativ, Globs erlaubt.
# knowledge:
#   - wissen/ablage-regeln.md

# NUR Einschränkungen. allow und ask kommen aus den Räumen des
# Auftrags; ein Template darf Rechte verengen, nie aufweiten.
# permission:
#   read:
#     "*.env": deny
---

Du bist %[1]s. Schreib hier die Rolle: wer der Hase ist, wie er
arbeitet, woran er sich hält und wo er lieber nichts tut als zu raten.

Der Auftrag sagt ihm, was in diesem einen Lauf zu tun ist — hier steht,
wer er über alle Aufträge hinweg ist.
`, name)
}
