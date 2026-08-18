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

const newUsage = `Usage: hasenbau new <resource> <name>

Resources:
  auftrag <name> -hase <hase>   scaffold for auftraege/<name>.md
  hase <name>                   scaffold for hasen/<name>.md

Existing files are left untouched.
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
		fmt.Fprintf(errw, "hasenbau new: unknown resource %q\n\n%s", args[0], newUsage)
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
		fmt.Fprintf(errw, "hasenbau new auftrag: the scaffold does not load — that is a bug in Hasenbau: %v\n", err)
		return 1
	}
	rel := filepath.Join("auftraege", name+".md")
	if code := writeNew(root, rel, inhalt, errw); code != 0 {
		return code
	}

	fmt.Fprintf(out, "created: %s\n\n", rel)
	fmt.Fprintf(out, "Next:\n"+
		"  1. Write the Markdown part — it is the prompt core, not decoration.\n"+
		"  2. Pick a trigger: the scaffold is set to manual, watch and cron sit next to it, commented out.\n"+
		"     With watch the input belongs under raeume: input: — watch: carries only the pattern.\n"+
		"  3. Check the Räume. Nobody has to create them, the first Lauf does that.\n"+
		"  4. hasenbau describe auftrag %s  — shows what the Bau reads from it.\n"+
		"  5. hasenbau lauf %s  — trigger it once by hand.\n", name, name)
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
		fmt.Fprintf(errw, "hasenbau new hase: the scaffold does not load — that is a bug in Hasenbau: %v\n", err)
		return 1
	}

	fmt.Fprintf(out, "created: %s\n\n", rel)
	fmt.Fprintf(out, "Next:\n"+
		"  1. Write the role — who the Hase is and what it should do.\n"+
		"  2. Set model: if the default provider should not apply.\n"+
		"  3. hasenbau new auftrag <name> -hase %s  — a Hase without an Auftrag never runs.\n"+
		"  4. hasenbau describe hase %s  — shows the effective permissions per Auftrag.\n", name, name)
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
				return "", "", fmt.Errorf("-hase without a value")
			}
			haseName = args[i+1]
			i++
		case strings.HasPrefix(arg, "-hase="), strings.HasPrefix(arg, "--hase="):
			haseName = arg[strings.Index(arg, "=")+1:]
		case strings.HasPrefix(arg, "-"):
			return "", "", fmt.Errorf("unknown flag %q (there is only -hase)", arg)
		default:
			frei = append(frei, arg)
		}
	}
	switch len(frei) {
	case 1:
		return frei[0], haseName, nil
	case 0:
		return "", "", fmt.Errorf("the name is missing")
	default:
		return "", "", fmt.Errorf("more than one name: %s", strings.Join(frei, ", "))
	}
}

// requireHase besteht darauf, dass der genannte Hase existiert, und
// zählt sonst auf, was es stattdessen gibt.
func requireHase(root, name string, errw io.Writer) int {
	vorhanden := existingHasen(root)
	if name == "" {
		fmt.Fprintf(errw, "hasenbau new auftrag: -hase is missing — every Auftrag needs a Hase.\n%s", hasenHinweis(vorhanden))
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
	fmt.Fprintf(errw, "hasenbau new auftrag: no template hasen/%s.md.\n%s", name, hasenHinweis(vorhanden))
	return 1
}

func hasenHinweis(vorhanden []string) string {
	if len(vorhanden) == 0 {
		return "  The Bau has no Hasen yet — `hasenbau new hase <name>` creates one.\n"
	}
	return fmt.Sprintf("  Available: %s\n  New: `hasenbau new hase <name>`\n", strings.Join(vorhanden, ", "))
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
		fmt.Fprintf(errw, "hasenbau new: %s already exists — pick another name, or edit the file itself.\n", rel)
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
			fmt.Fprintf(errw, "hasenbau new: %s already exists — pick another name, or edit the file itself.\n", rel)
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
  # watch: "*.txt"                 # NUR das Muster — der Eingang steht
  #                                # unten als raeume: input:, beobachtet
  #                                # wird die Summe aus beidem. Flach;
  #                                # "**/*.txt" nimmt Unterverzeichnisse
  #                                # dazu (null oder mehr Ebenen).
  #                                # Ein Lauf je Datei: Begleitmaterial,
  #                                # das der Hase nur mitlesen soll, darf
  #                                # das Muster nicht matchen.
  # debounce: 5s                   # nur bei watch: Ruhe vor dem Zugriff
  # cron: "0 7 * * *"              # fünf Felder, keine Sekunden — siehe unten

# Cron: fünf Felder, immer in Anführungszeichen. Ein nacktes */15 ist
# kein gültiges YAML, dort beginnt ein Alias.
#
#   Minute  Stunde  Tag-im-Monat  Monat          Wochentag
#   0-59    0-23    1-31          1-12, JAN-DEC  0-6, SUN-SAT (0 = Sonntag)
#
# Je Feld: * alles, , Liste, - Bereich, / Schrittweite.
#
#   cron: "0 7 * * *"          täglich 07:00
#   cron: "*/15 * * * *"       alle 15 Minuten
#   cron: "0 7 * * MON-FRI"    werktags 07:00
#   cron: "0 9,17 * * *"       09:00 und 17:00
#   cron: "30 3 1 * *"         am Ersten jeden Monats, 03:30
#   cron: "@daily"             ebenso @hourly @weekly @monthly @yearly
#   cron: "@every 90m"         ab Daemon-Start gezählt, nicht zur vollen Stunde
#
# Es gilt die Ortszeit der Maschine; "CRON_TZ=UTC 0 2 * * *" stellt das
# um. Sind Tag-im-Monat UND Wochentag gesetzt, feuert es an beiden — "0 7
# 13 * FRI" heißt jeden Dreizehnten ODER jeden Freitag. Verpasste Ticks
# holt niemand nach (anders als beim Eingang eines watch-Auftrags), und
# läuft der Auftrag beim nächsten Tick noch, fällt dieser Tick aus.

# Deterministische Vorverarbeitung, läuft VOR dem Hasen. Kein Modell,
# kein Urteil. Ersetzt werden genau diese fünf Namen — jeder andere
# $GROSS-Name ist ein harter Fehler, auch $HOME:
#
#   $BAU           Root des Baus, also dieses Verzeichnis
#   $TRIGGER_FILE  die auslösende Datei — NUR bei watch gebunden
#   $TRIGGER_ARG   das Argument von: hasenbau lauf %[1]s <arg>
#                  — NUR bei manual gebunden, freier Text, kein Pfad
#   $WORK          Scratch dieses Laufs, frisch pro Lauf angelegt;
#                  gebunden nur mit einem Raum der Rolle work
#   $RAUM_<rolle>  ein Raum von unten, Rolle wörtlich wie dort
#                  geschrieben: $RAUM_out, nicht $RAUM_OUT
#   $HASENBAU      Pfad des laufenden Binaries, für Gänge, die den
#                  Hasenbau selbst rufen
#
# Ersetzt wird textuell, bevor die Shell die Zeile sieht — Pfade also
# in Anführungszeichen setzen, sie können Leerzeichen enthalten.
# gaenge:
#   - name: extrakt
#     run: gaenge/mein_gang.py "$TRIGGER_FILE" --out "$WORK/extrakt.md"
#     timeout: 120s

hase: %[2]s

# Zeitlimit des LLM-Schritts. Ohne Angabe gilt die Vorgabe (30m).
# hase_timeout: 60m

# Deckel: höchstens so viele Läufe je rollendem Fenster. Gezählt wird
# aus der Lauf-Historie, nicht aus einem Zähler — der Deckel übersteht
# also einen Neustart. Beide Felder oder keines. `+"`hasenbau lauf`"+`
# umgeht ihn, zählt aber mit.
#
# between begrenzt zusätzlich die Tageszeit, zu der ein Lauf STARTEN
# darf (Ortszeit, über Mitternacht erlaubt); ein laufender wird nie
# abgeschnitten. Allein verschiebt es nur — es deckelt nicht.
# throttle:
#   max: 5
#   per: 1h
#   between: "22:00-06:00"

# Routinemäßig melden: dann stehen die Befunde dieses Auftrags in
# `+"`hasenbau status`"+`. Das steuert nur die Meldung — erfasst wird
# ohnehin alles, und `+"`hasenbau findings %[1]s`"+` rechnet auch ohne.
# monitored: true

# Schmied-Werkzeuge, die der Hase in DIESEM Auftrag rufen darf. Ohne
# Eintrag bekommt er keines: die Vorgabe ist nichts, nicht alles.
# Genannt wird der Dateiname unter tools/ ohne Endung; steht dort
# keines, lädt der Auftrag nicht. Was es im Bau gibt, zeigt
# `+"`hasenbau get tools`"+`.
# tools:
#   - zeilen_zaehlen

# Die Rollen: input ist der Eingang und bei einem watch-Trigger
# PFLICHT — dort sucht der Watcher, und das Muster oben ist relativ
# dazu. Bei cron und manual ist er der Suchraum, den Gänge über
# $RAUM_input ansprechen. work ist das Scratch dieses Laufs ($WORK),
# out das Ziel, done das Archiv des Rohmaterials, quarantine, was
# schiefging. Schreibrecht bekommt der Hase AUSSCHLIESSLICH für work
# und out — daraus entstehen seine Permissions, nicht aus seiner Rolle.
raeume:
  # input: raeume/eingang/
  work: raeume/werkstatt/
  out: raeume/lager/

# Was in den Prompt kommt. Dateien werden gelesen, last_summaries holt
# die Meldungen der letzten Läufe dieses Auftrags.
# context:
#   - file: $WORK/extrakt.md
#   - last_summaries: 3

# Aufräumen nach einem geglückten Lauf — und nur nach einem geglückten.
# Genau eine Aktion pro Schritt, abgearbeitet in der Reihenfolge, in der
# sie hier stehen; der erste Fehler bricht ab.
#
#   move:   VON -> NACH   verschiebt
#   copy:   VON -> NACH   kopiert, das Original bleibt liegen
#   delete: PFAD          löscht eine Datei, ohne Nachfrage
#
# Endet NACH auf einem Schrägstrich oder ist es ein vorhandenes
# Verzeichnis, behält die Datei ihren Namen; fehlende Zielverzeichnisse
# entstehen von selbst. Liegt dort schon eine gleichnamige Datei,
# bekommt die neue einen Zeitstempel vorangestellt — überschrieben wird
# nie. $BAU ist hier tabu (absolut), und $TRIGGER_ARG ist kein Pfad —
# was in einem manual-Auftrag hier zu verschieben wäre, muss aus einem
# Raum kommen.
# after:
#   - move: $TRIGGER_FILE -> raeume/archiv/     # Auslöser wegräumen
#   - copy: $WORK/bericht.md -> raeume/lager/   # Ergebnis sichern
#   - delete: raeume/werkstatt/zwischenstand.json  # Rest wegwerfen
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
