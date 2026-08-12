// sandbox.go: `hasenbau sandbox-vorfall` — die Gegenstelle des
// Sandbox-Wächters (Hasenbau-d2p).
//
// Der Wächter ist ein opencode-Plugin und sitzt im Server-Prozess; er
// sieht jeden Werkzeug-Aufruf, auch den eines Subagenten. Entscheiden
// darf er nichts: er meldet hierher und tut, was zurückkommt. Damit
// bleibt die Sandbox-Regel im Hasenbau und überlebt einen Wechsel des
// Backends (PLAN.md §6) — nachzubauen wäre dann ein dünner Hook, keine
// Semantik.
//
// Exit-Codes sind die Antwort an den Wächter:
//
//	3  abweisen. Was auf stdout steht, bekommt der Hase als Fehlertext
//	   seines Werkzeugs zu lesen (verifiziert 2026-08-12: der Text
//	   kommt beim Modell an und ist dort verwertbar).
//	0  durchlassen. Gemeldet wurde trotzdem.
//	1  der Vorfall konnte nicht verbucht werden — der Wächter loggt das
//	   und lässt durch. Ein Wächter, der Läufe umbringt, wird
//	   abgeschaltet und misst dann gar nichts mehr.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/Moritz-Dorn/Hasenbau/internal/bau"
	"github.com/Moritz-Dorn/Hasenbau/internal/store"
)

// abweisungsText ist, was der Hase zu lesen bekommt. Er nennt den Grund
// und den Weg — eine Ablehnung ohne Alternative schickt ihn nur auf die
// Suche nach dem nächsten Schlupfloch, und genau so ist dieser Bead
// entstanden.
const abweisungsText = `Dieses Werkzeug führt aus der Sandbox heraus, die der Hasenbau dir gegeben hat, und ist deshalb gesperrt.
Wenn du es für deine Aufgabe brauchst: beschreib über den Rückkanal, was dir fehlt (hasenbau_notiz). Daraus wird ein geprüftes Werkzeug — niemand baut sich hier selbst eines.
Was du jetzt tun kannst: die Aufgabe mit deinen Datei-Werkzeugen lösen, oder melden, dass sie so nicht lösbar ist.`

func cmdSandboxVorfall(root string, args []string, out, errw io.Writer) int {
	fs := flag.NewFlagSet("sandbox-vorfall", flag.ContinueOnError)
	fs.SetOutput(errw)
	tool := fs.String("tool", "", "Name des gerufenen Werkzeugs")
	session := fs.String("session", "", "opencode-Session des Aufrufs")
	argsJSON := fs.String("args", "", "Argumente des Aufrufs (JSON)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *tool == "" {
		fmt.Fprintln(errw, "hasenbau sandbox-vorfall: -tool fehlt")
		return 1
	}

	cfg, err := bau.LoadConfig(root)
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	abweisen := cfg.Sandbox != bau.SandboxWarn

	// Verbucht wird am laufenden Lauf. Welcher das ist, weiß der
	// Hasenbau selbst — der Hook trägt keinen Agent-Namen, und raten
	// wäre schlimmer als nichts (dieselbe Haltung wie im Rückkanal).
	// Scheitert das Verbuchen, ändert es an der Entscheidung nichts:
	// die Sandbox hängt nicht daran, dass die Notiz ankommt.
	if err := verbucheVorfall(root, *tool, *session, *argsJSON, abweisen, errw); err != nil {
		fmt.Fprintf(errw, "hasenbau sandbox-vorfall: %v\n", err)
		if abweisen {
			fmt.Fprint(out, abweisungsText)
			return 3
		}
		return 1
	}

	if abweisen {
		fmt.Fprint(out, abweisungsText)
		return 3
	}
	return 0
}

func verbucheVorfall(root, tool, session, argsJSON string, abweisen bool, errw io.Writer) error {
	st, err := store.Open(dbPath(root))
	if err != nil {
		return err
	}
	defer st.Close()

	text := vorfallText(tool, session, argsJSON, abweisen)
	lauf, err := st.ActiveLauf()
	switch {
	case errors.Is(err, store.ErrNoActiveLauf):
		// Kein Lauf heißt: das war kein Hase. Trotzdem hörbar machen —
		// im Bau soll niemand unbemerkt an der Sandbox rütteln.
		fmt.Fprintf(errw, "hasenbau: Sandbox-Vorfall ohne laufenden Lauf — %s\n", text)
		return nil
	case errors.Is(err, store.ErrAmbiguous):
		// Mehrere Läufe: nicht raten, wem der Aufruf gehört (dieselbe
		// Haltung wie im Rückkanal). Verloren geht die Meldung nicht.
		fmt.Fprintf(errw, "hasenbau: Sandbox-Vorfall nicht zuzuordnen (%v) — %s\n", err, text)
		return nil
	case err != nil:
		return err
	}
	return st.WriteNote(lauf.ID, text)
}

func vorfallText(tool, session, argsJSON string, abweisen bool) string {
	was := "durchgelassen (sandbox: warn)"
	if abweisen {
		was = "abgewiesen"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Sandbox: Werkzeug %q %s", tool, was)
	if argsJSON != "" && argsJSON != "{}" {
		fmt.Fprintf(&b, " — Argumente: %s", gekuerzt(argsJSON, 500))
	}
	if session != "" {
		fmt.Fprintf(&b, " [session %s]", session)
	}
	return b.String()
}

// gekuerzt hält eine Notiz lesbar: die Argumente eines bash-Aufrufs
// können ein ganzes Skript sein.
func gekuerzt(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "… (gekürzt)"
}
