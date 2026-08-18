// probelauf.go sperrt den Probelauf eines Schmied-Werkzeugs ein
// (Hasenbau-9w6).
//
// NICHT zu verwechseln mit `sandbox.go` daneben: dort geht es um den
// Sandbox-Wächter im Server-Prozess, der einem HASEN auf die Finger
// sieht (PLAN §6, Hasenbau-d2p). Hier geht es um die Bedingungen, unter
// denen ein MENSCH ein noch ungeprüftes Skript einmal laufen lässt.
//
// Der Anlass ist eine Frage von Moritz: „wenn das malicious code ist,
// dann wird tool test den ja ausführen, was haben wir damit gewonnen?"
// Die ehrliche Antwort war: nichts. `tool test` ist gegen bösartigen
// Code keine Abwehr, sondern die Ausführung — und der Weg dorthin ist
// real (ein Hase liest fremdes Material, darin steht ein
// eingeschleuster Auftrag, er stellt einen Werkzeug-Wunsch, der Schmied
// baut, was im Wunsch steht).
//
// Was der Sandkasten leistet: er nimmt dem Probelauf das Netz, den Rest
// des Dateisystems und jedes Schreibrecht. Damit fällt auch auf, wenn
// ein Werkzeug heimlich nach draußen telefoniert — das ist etwas
// anderes als „es stürzt nicht ab".
//
// Was er NICHT leistet: das freigegebene Werkzeug läuft im Betrieb
// weiterhin ungesandboxed im opencode-Prozess. Ein Sandkasten um die
// Probe schützt die Probe, nicht den Betrieb; letzteres ist ein eigener
// Entwurf zu PLAN §3 und offen.
//
// Und er hat einen Preis, der in PLAN §4 steht und hier den Schalter
// begründet: ein Sandkasten ändert das Ergebnis. Ein Werkzeug, das
// legitim schreibt oder eine Datei außerhalb des Baus liest, scheitert
// darin — und dann prüft man etwas anderes als den Ernstfall.
package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// probeSandboxFlag schaltet den Sandkasten ab. Englisch wie jeder
// Formatschlüssel (AGENTS.md §1) — die acht deutschen Begriffe sind
// Domänenvokabular, ein Schalter ist keines.
const probeSandboxFlag = "no-sandbox"

// probeTimeout begrenzt den Probelauf. Ein Werkzeug wird von einem Hasen
// mitten in dessen Lauf gerufen und soll antworten, nicht rechnen; was
// hier länger braucht, ist im Ernstfall ohnehin ein Problem.
//
// Das Limit gilt NUR im Sandkasten. Der Lauf mit `-no-sandbox` bleibt,
// wie er war — er ist der Ernstfall, und den zu beschneiden hieße, ihn
// zu verfälschen.
const probeTimeout = 60 * time.Second

// probeSandbox sagt, unter welchen Bedingungen der Probelauf lief.
// `reason` ist gefüllt, wenn kein Sandkasten aktiv ist — dieser Fall
// muss laut sein, nicht still: wer glaubt, eingesperrt zu haben, und es
// nicht hat, ist schlechter dran als wer es weiß.
type probeSandbox struct {
	active bool
	reason string
}

// probeCommand baut den Aufruf für den Probelauf.
//
// Ohne `bwrap` im PATH läuft das Skript wie bisher — der Hasenbau hängt
// an wenigen Abhängigkeiten, und eine fehlende soll ihn nicht
// unbenutzbar machen. Der Aufrufer muss den Zustand dann aber melden.
func probeCommand(ctx context.Context, root, skript string, argv []string, erlaubt bool) (*exec.Cmd, probeSandbox) {
	blank := func(grund string) (*exec.Cmd, probeSandbox) {
		cmd := exec.Command(skript, argv...)
		cmd.Dir = root
		return cmd, probeSandbox{active: false, reason: grund}
	}
	if !erlaubt {
		return blank("mit -" + probeSandboxFlag + " abgeschaltet")
	}
	bwrap, err := exec.LookPath("bwrap")
	if err != nil {
		return blank("bwrap is not in the PATH")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return blank("the Bau path could not be resolved: " + err.Error())
	}

	// Die Reihenfolge trägt die Bedeutung: bwrap wendet die Mounts von
	// links nach rechts an, und ein späterer überdeckt einen früheren.
	// Erst also das ganze Dateisystem lesend, dann $HOME wegwerfen —
	// und ZULETZT den Bau wieder sichtbar machen. Nur so bleibt er
	// lesbar, obwohl er meistens unter $HOME liegt (verifiziert
	// 2026-08-13 an einem Bau unter ~/SRC).
	flags := []string{
		"--ro-bind", "/", "/",
		"--dev", "/dev",
		"--proc", "/proc",
		"--tmpfs", "/tmp",
	}
	if home := os.Getenv("HOME"); home != "" && home != "/" {
		flags = append(flags, "--tmpfs", home)
	}
	flags = append(flags,
		"--ro-bind", abs, abs,
		// --unshare-all nimmt auch das Netz. Genau das ist der Teil,
		// den ein Blick aufs Skript am ehesten übersieht.
		"--unshare-all",
		// Stirbt der Hasenbau, stirbt der Probelauf mit — ein Werkzeug
		// soll den Befehl nicht überleben.
		"--die-with-parent",
		// Eigene Session: sonst könnte das Skript über TIOCSTI Zeichen
		// in das Terminal schieben, aus dem es gerufen wurde.
		"--new-session",
		// HOME zeigt in den tmpfs. Ein Interpreter, der einen
		// Cache-Pfad braucht, findet einen — er ist nur nach dem Lauf
		// weg.
		"--setenv", "HOME", "/tmp",
		"--chdir", abs,
		"--",
	)
	flags = append(flags, skript)
	flags = append(flags, argv...)

	cmd := exec.CommandContext(ctx, bwrap, flags...)
	cmd.Dir = root
	return cmd, probeSandbox{active: true}
}

// probeKontext liefert den Kontext für den Probelauf: mit Zeitlimit im
// Sandkasten, ohne außerhalb.
func probeKontext(erlaubt bool) (context.Context, context.CancelFunc) {
	if !erlaubt {
		return context.WithCancel(context.Background())
	}
	return context.WithTimeout(context.Background(), probeTimeout)
}

// probeSandboxHatVersagt erkennt, dass nicht das Skript gescheitert ist,
// sondern der Sandkasten selbst — fehlende User-Namespaces etwa. bwrap
// meldet das mit einem eigenen Präfix auf stderr, und der Unterschied
// gehört in die Ausgabe: im einen Fall weiß man etwas über das Werkzeug,
// im anderen nur etwas über die Maschine.
func probeSandboxHatVersagt(stderr string) bool {
	for _, z := range strings.Split(stderr, "\n") {
		if strings.HasPrefix(strings.TrimSpace(z), "bwrap:") {
			return true
		}
	}
	return false
}
