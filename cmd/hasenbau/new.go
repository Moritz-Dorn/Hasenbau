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
# Auftrag %[1]s — trigger, Gänge, Hase, Räume (PLAN.md §6).
# Created by ` + "`hasenbau new`" + `. These comments may go.

# Exactly one kind of trigger. The other two stay commented out.
trigger:
  manual: true                     # only on request: hasenbau lauf %[1]s
  # watch: "*.txt"                 # ONLY the pattern — the input lives
  #                                # below as raeume: input:, and what is
  #                                # watched is the sum of the two. Flat;
  #                                # "**/*.txt" takes subdirectories
  #                                # along (zero or more levels).
  #                                # One Lauf per file: material the Hase
  #                                # is only meant to read alongside must
  #                                # not match the pattern.
  # debounce: 5s                   # watch only: quiet before touching it
  # cron: "0 7 * * *"              # five fields, no seconds — see below

# Cron: five fields, always quoted. A bare */15 is not valid YAML —
# an alias begins there.
#
#   minute  hour    day-of-month  month          day-of-week
#   0-59    0-23    1-31          1-12, JAN-DEC  0-6, SUN-SAT (0 = Sunday)
#
# Per field: * everything, , list, - range, / step.
#
#   cron: "0 7 * * *"          daily at 07:00
#   cron: "*/15 * * * *"       every 15 minutes
#   cron: "0 7 * * MON-FRI"    weekdays at 07:00
#   cron: "0 9,17 * * *"       09:00 and 17:00
#   cron: "30 3 1 * *"         on the first of each month, 03:30
#   cron: "@daily"             likewise @hourly @weekly @monthly @yearly
#   cron: "@every 90m"         counted from daemon start, not on the hour
#
# The machine's local time applies; "CRON_TZ=UTC 0 2 * * *" changes that.
# If day-of-month AND day-of-week are both set, it fires on both — "0 7
# 13 * FRI" means every thirteenth OR every Friday. Missed ticks are not
# caught up (unlike the input of a watch Auftrag), and if the Auftrag is
# still running at the next tick, that tick is dropped.

# Deterministic preprocessing, runs BEFORE the Hase. No model, no
# judgement. Exactly these five names are substituted — every other
# $UPPERCASE name is a hard error, $HOME included:
#
#   $BAU           root of the Bau, that is this directory
#   $TRIGGER_FILE  the triggering file — bound for watch ONLY
#   $TRIGGER_ARG   the argument of: hasenbau lauf %[1]s <arg>
#                  — bound for manual ONLY, free text, not a path
#   $WORK          scratch of this Lauf, created fresh per Lauf;
#                  bound only with a Raum of role work
#   $RAUM_<role>   a Raum from below, role spelled exactly as written
#                  there: $RAUM_out, not $RAUM_OUT
#   $HASENBAU      path of the running binary, for Gänge that call
#                  Hasenbau itself
#
# Substitution is textual, before the shell sees the line — so quote
# paths, they may contain spaces.
# gaenge:
#   - name: extrakt
#     run: gaenge/mein_gang.py "$TRIGGER_FILE" --out "$WORK/extrakt.md"
#     timeout: 120s

hase: %[2]s

# Time limit of the LLM step. Without it the default applies (30m).
# hase_timeout: 60m

# Cap: at most this many Läufe per rolling window. Counted from the Lauf
# history, not from a counter — so the cap survives a restart. Both
# fields or neither. ` + "`hasenbau lauf`" + ` bypasses it but still counts.
#
# between additionally limits the time of day at which a Lauf may START
# (local time, across midnight allowed); a running one is never cut off.
# On its own it only shifts — it does not cap.
# throttle:
#   max: 5
#   per: 1h
#   between: "22:00-06:00"

# Report routinely: then the findings of this Auftrag appear in
# ` + "`hasenbau status`" + `. This steers the reporting only — everything is
# recorded anyway, and ` + "`hasenbau findings %[1]s`" + ` computes without it.
# monitored: true

# Schmied tools the Hase may call in THIS Auftrag. Without an entry it
# gets none: the default is nothing, not everything. Named is the file
# the folder name under tools/released/; if there is none, the Auftrag does
# not load. What the Bau has is shown by ` + "`hasenbau get tools`" + `.
# tools:
#   - zeilen_zaehlen

# The roles: input is the entrance and MANDATORY for a watch trigger —
# that is where the watcher looks, and the pattern above is relative to
# it. For cron and manual it is the search space that Gänge address via
# $RAUM_input. work is the scratch of this Lauf ($WORK), out the
# destination, done the archive of the raw material, quarantine whatever
# went wrong. The Hase gets write rights EXCLUSIVELY for work and out —
# its permissions come from those, not from its role.
raeume:
  # input: raeume/eingang/
  work: raeume/werkstatt/
  out: raeume/lager/

# What goes into the prompt. Files are read, last_summaries fetches the
# reports of the most recent Läufe of this Auftrag.
# context:
#   - file: $WORK/extrakt.md
#   - last_summaries: 3

# Cleaning up after a successful Lauf — and only after a successful one.
# Exactly one action per step, worked through in the order written here;
# the first error aborts.
#
#   move:   FROM -> TO   moves
#   copy:   FROM -> TO   copies, the original stays
#   delete: PATH         deletes a file, without asking
#
# If TO ends in a slash or is an existing directory, the file keeps its
# name; missing target directories are created. If a file of the same
# name is already there, the new one gets a timestamp in front — nothing
# is ever overwritten. $BAU is off limits here (absolute), and
# $TRIGGER_ARG is not a path — whatever a manual Auftrag moves here has
# to come from a Raum.
# after:
#   - move: $TRIGGER_FILE -> raeume/archiv/        # clear the trigger away
#   - copy: $WORK/bericht.md -> raeume/lager/      # keep the result
#   - delete: raeume/werkstatt/zwischenstand.json  # throw the rest away
---

This is where you say what the Hase should do — this text is the prompt
core and goes into the prompt unchanged. Write it as an instruction to a
person who knows the Bau but not this case: what is there, what should
become of it, how do they know they are done.
`, name, haseName)
}

// haseGeruest ist das kommentierte Hasen-Skelett. Was der Rückkanal
// ist, steht bewusst nicht drin: den Absatz hängt der Generator an
// jeden Agenten selbst an (§8 Phase 2) — hier wäre er Doppelung, die
// beim nächsten Wortlaut-Wechsel veraltet.
func haseGeruest(name string) string {
	return fmt.Sprintf(`---
# Hase %[1]s — a template, not an opencode agent (PLAN.md §6). From it
# Hasenbau generates one agent per Auftrag.
# Created by ` + "`hasenbau new`" + `. These comments may go.
description: %[1]s — what this Hase does

# Provider/model as in the Bau config. Without it opencode chooses.
# model: scc/kit.deepseek-v4-flash-0731
# temperature: 0.2

# Attaches the shipped knowledge about Hasenbau to the prompt: the
# vocabulary, how a Lauf proceeds, how to read a trace, the boundaries.
knows_hasenbau: true

# Your own texts from the Bau, Bau-relative, globs allowed.
# knowledge:
#   - wissen/ablage-regeln.md

# RESTRICTIONS ONLY. allow and ask come from the Räume of the Auftrag; a
# template may narrow rights, never widen them.
# permission:
#   read:
#     "*.env": deny
---

You are %[1]s. Write the role here: who the Hase is, how it works, what
it holds to, and where it would rather do nothing than guess.

The Auftrag tells it what to do in this one Lauf — this is who it is
across all Aufträge.
`, name)
}
