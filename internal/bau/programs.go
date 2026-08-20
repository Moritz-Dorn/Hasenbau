// programs.go hält die Liste der Programme, die der Hasenbau ruft, ohne
// sie mitzubringen (Hasenbau-nbk, PLAN.md §2).
//
// Sie steht hier und nicht in cmd/, weil zwei sehr verschiedene Dinge
// sie brauchen: `hasenbau new dockerfile` schreibt daraus ein
// Container-Rezept, und `describe bau` prüft daraus, ob die Programme
// da sind. Zwei Fassungen derselben Liste wären die Doppelung, gegen
// die PLAN §3 schon einmal geschrieben hat.
//
// Die Bezeichner sind englisch (AGENTS.md §1); deutsch bleibt die
// Prosa. Why ist die Ausnahme, die keine ist: der Text geht in ein
// erzeugtes Dockerfile und in eine CLI-Ausgabe, beides englisch.
package bau

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// ExternalProgram ist ein Programm, das der Hasenbau ruft, samt dem
// Debian-Paket, das es mitbringt, und dem Grund.
//
// Diese Liste ist die einzige Fassung — programs_test.go liest die
// `exec`-Aufrufe des Quellbaums und verlangt, dass jeder gefundene
// Befehl hier steht. Wer später ein `jq` einbaut, bekommt einen roten
// Test statt eines Images, in dem es fehlt.
type ExternalProgram struct {
	// Command ist der Name im PATH, so wie der Code ihn ruft. Leer:
	// kein direkter Aufruf, das Paket wird trotzdem gebraucht.
	Command string
	// Package ist das Debian-Paket. Leer: in der Basis enthalten oder
	// mit eigenem Installer (opencode).
	Package string
	// Why steht als Kommentar im erzeugten Dockerfile und als Hinweis
	// in der Diagnose. Englisch, siehe Paket-Kommentar.
	Why string
}

// ExternalPrograms ist die Liste. Reihenfolge ist Ausgabe-Reihenfolge.
var ExternalPrograms = []ExternalProgram{
	{Command: "opencode", Why: "the server the daemon starts; installed below, it brings its own installer"},
	{Command: "git", Package: "git", Why: "a Bau is a Git repo — without a root commit opencode gives it no project ID and the Raum permissions do not bite"},
	{Command: "bwrap", Package: "bubblewrap", Why: "the sandbox around every Schmied tool. Missing it, the plugin registers NO tool at all"},
	{Command: "sh", Why: "every Gang runs as `sh -c`, and dash is already in the base image"},
	{Command: "python3", Package: "python3", Why: "the Baumeister checks the syntax of the Gänge it writes"},
	{Package: "ca-certificates", Why: "HTTPS to your model provider"},
	{Package: "tzdata", Why: "without it cron triggers run in UTC, so `0 10 * * *` fires at 12"},
	{Package: "curl", Why: "only for the opencode installer below"},
	{Package: "tar", Why: "the opencode installer unpacks a .tar.gz on Linux"},
}

// bwrapProbeTimeout deckelt den Probelauf. Er dauert normalerweise
// Millisekunden; hängt er, ist das selbst ein Befund und darf `describe
// bau` trotzdem nicht anhalten.
const bwrapProbeTimeout = 3 * time.Second

// BwrapWorks beantwortet die Frage, die LookPath NICHT beantwortet:
// kann bwrap hier einen Namespace aufmachen?
//
// Der Unterschied ist keine Feinheit. In einem Container ohne
// `--security-opt seccomp=unconfined` liegt das Binary da und scheitert
// trotzdem an jedem Aufruf — gemessen am 2026-08-20 (Hasenbau-sss), und
// `describe bau` meldete damals `ok Tools`. Vorhandensein ist ein Beleg,
// kein Urteil; dieselbe Lehre wie beim Rückkanal (Hasenbau-2nq/08u) und
// bei ValIntent.
//
// Zurück kommt der Grund als Text, leer heißt „geht". `/bin/sh` und
// nicht `/bin/true`: nicht jedes System hat letzteres (NixOS zum
// Beispiel), und `sh` steht ohnehin auf der Liste oben.
func BwrapWorks() (ok bool, grund string) {
	if _, err := exec.LookPath("bwrap"); err != nil {
		return false, "not in PATH"
	}
	ctx, cancel := context.WithTimeout(context.Background(), bwrapProbeTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bwrap",
		"--ro-bind", "/", "/", "--unshare-all", "--die-with-parent",
		"--", "/bin/sh", "-c", "exit 0")
	roh, err := cmd.CombinedOutput()
	if err == nil {
		return true, ""
	}
	if ctx.Err() != nil {
		return false, "the probe run did not finish within " + bwrapProbeTimeout.String()
	}
	// bwraps eigener Text ist die brauchbarste Auskunft, die es hier
	// gibt („No permissions to create new namespace, likely because
	// the kernel does not allow non-privileged user namespaces").
	// Weitergeben statt zusammenfassen.
	if text := strings.TrimSpace(string(roh)); text != "" {
		return false, firstLine(text)
	}
	return false, err.Error()
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
