// Package rueckkanal gibt den Hasen zwei Werkzeuge, mit denen sie
// strukturiert in die Bau-Datenbank schreiben: notiz(text) und
// summary(text) (PLAN.md §8, Phase 2). Strukturierte Writes statt
// stdout-Parsing — sonst entsteht in drei Wochen ein Regex-Friedhof.
//
// Der Server spricht MCP über stdio und wird von opencode als
// Kindprozess gestartet (`hasenbau mcp`, Eintrag in der Bau-Config).
// Deshalb gehört stdout dem Protokoll: Logs immer nach stderr.
//
// # Wer ruft da an?
//
// opencode reicht an MCP-Tools keinen Session-Kontext durch — der
// Aufruf trägt nur Name und Argumente (verifiziert an opencode
// 1.15.13). Der Rückkanal erkennt den Lauf deshalb daran, dass genau
// einer aktiv ist. Bei keinem oder mehreren bekommt der Hase einen
// Fehler; geraten wird nie, sonst landet eine Summary am falschen Lauf.
// Die Summary geht dann nicht verloren: der Runner trägt beim
// Lauf-Ende die letzte Assistant-Message als Fallback ein (§5).
package backchannel

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/Moritz-Dorn/Hasenbau/internal/store"
)

// Store ist die Schreibsicht des Rückkanals; *store.Store erfüllt sie.
type Store interface {
	ActiveLauf() (*store.Lauf, error)
	WriteNote(lauf int64, text string) error
	WriteSummary(lauf int64, text string) error
}

const (
	// Name ist der Server-Name; opencode stellt ihn den Werkzeugen
	// voran, der Hase sieht sie also als `hasenbau_notiz` bzw.
	// `hasenbau_summary`.
	Name = "hasenbau"

	anleitung = `Der Rückkanal des Hasenbaus. Halte fest, was nur du weißt: ` +
		`summary für die eine Zeile, was dieser Lauf getan hat (der nächste ` +
		`Lauf desselben Auftrags bekommt sie als Kontext), notiz für ` +
		`Beobachtungen unterwegs.`
)

// Server baut den MCP-Server mit notiz, summary und werkzeug_wunsch.
//
// bauRoot und wunschRaum brauchen nur letzteres: ein Wunsch wird als
// Datei abgelegt, nicht als DB-Zeile. Das ist Absicht — der Raum ist
// der Eingang des Schmieds, und ein watch-Trigger darauf braucht keinen
// neuen Mechanismus (§2, §7: die Warteschlange ist das Dateisystem).
// Ist wunschRaum leer, bleibt das Werkzeug aus: ein Bau ohne Wunsch-Raum
// soll dem Hasen keinen Briefkasten zeigen, den niemand leert.
func Server(st Store, version, bauRoot, wunschRaum string) *server.MCPServer {
	s := server.NewMCPServer(Name, version,
		server.WithInstructions(anleitung),
		// Ein Panic im Handler darf nicht den Prozess reißen — der
		// Hase soll einen Fehler sehen und weiterarbeiten können.
		server.WithRecovery(),
	)

	s.AddTool(mcp.NewTool("notiz",
		mcp.WithDescription("Hält eine Beobachtung zum laufenden Lauf fest — etwas, "+
			"das später jemand wissen sollte, aber nicht in die Summary gehört. "+
			"Beliebig oft aufrufbar."),
		mcp.WithString("text",
			mcp.Required(),
			mcp.Description("Die Notiz, in ganzen Sätzen."),
		),
	), handler(st, "notiert", func(st Store, lauf int64, text string) error {
		return st.WriteNote(lauf, text)
	}))

	s.AddTool(mcp.NewTool("summary",
		mcp.WithDescription("Meldet in einer Zeile, was dieser Lauf getan hat. "+
			"Genau einmal am Ende des Laufs aufrufen; ein weiterer Aufruf "+
			"korrigiert die Zeile. Der nächste Lauf desselben Auftrags "+
			"bekommt sie als Kontext."),
		mcp.WithString("text",
			mcp.Required(),
			mcp.Description("Eine Zeile: was ist passiert."),
		),
	), handler(st, "Summary gesetzt", func(st Store, lauf int64, text string) error {
		return st.WriteSummary(lauf, text)
	}))

	if wunschRaum != "" {
		s.AddTool(werkzeugWunschTool(), wunschHandler(st, bauRoot, wunschRaum))
	}

	return s
}

// werkzeugWunschTool ist der Weg des Hasen zu einem Werkzeug, das er
// nicht hat. Vier Felder statt eines Freitexts, und ein zweites
// Werkzeug, das erst das Format erklärt, gibt es bewusst nicht: das
// Schema steht dem Modell ohnehin vor jedem Aufruf, und ein Roundtrip
// „hol dir die Anleitung" wird übersprungen — dann käme der eigentliche
// Aufruf falsch, obwohl die Anleitung danebenstand.
//
// Die Beschreibung besteht auf der AUFGABE, und das ist nicht Kosmetik:
// im ersten echten Lauf wünschte sich der Hase „ein Shell-Werkzeug, das
// beliebige Kommandos ausführt" — also genau die Umgehung, die der
// Sandbox-Wächter gerade verhindert hatte. Ein Kanal, der solche
// Wünsche entgegennimmt, baut die Hintertür nur langsamer.
//
// Wer die Sache baut, steht hier nicht: der Hase muss den Schmied nicht
// kennen, nur wissen, dass er anfragen darf.
func werkzeugWunschTool() mcp.Tool {
	return mcp.NewTool("werkzeug_wunsch",
		mcp.WithDescription("Fordert ein Werkzeug für DEINE AUFGABE an — eines, das "+
			"genau eine Sache tut. Beschreib die Aufgabe, nicht das Mittel: "+
			"\"Dateien nach Typ einsortieren\" ist ein Wunsch, \"eine Shell\" ist "+
			"keiner und wird abgelehnt. Der Wunsch wird geprüft und gebaut; in "+
			"diesem Lauf bekommst du das Werkzeug nicht mehr. Beispiel: zweck "+
			"\"120 Vorlagen nach Typ in Unterordner verteilen\", eingabe \"ein "+
			"Verzeichnis mit Vorlagen\", ausgabe \"Dateien je Typ einsortiert, plus "+
			"CSV mit dem, was kopiert wurde\"."),
		mcp.WithString("zweck", mcp.Required(),
			mcp.Description("Die Aufgabe in einem Satz — was getan werden soll, nicht womit.")),
		mcp.WithString("eingabe", mcp.Required(),
			mcp.Description("Was hineingeht.")),
		mcp.WithString("ausgabe", mcp.Required(),
			mcp.Description("Was herauskommen soll.")),
		mcp.WithString("versuch",
			mcp.Description("Was du stattdessen versucht hast und warum es nicht ging.")),
	)
}

// wunschHandler legt den Wunsch als Datei in den Wunsch-Raum und
// vermerkt ihn am Lauf. Beides, weil es zwei Leser hat: der Raum ist
// der Eingang des Schmieds, die Notiz macht im Trace sichtbar, dass der
// Hase gefragt hat — sonst sähe der Baumeister nur einen Lauf, der
// nichts zustande brachte.
func wunschHandler(st Store, bauRoot, wunschRaum string) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		zweck, err := req.RequireString("zweck")
		if err != nil {
			return mcp.NewToolResultError("zweck fehlt: " + err.Error()), nil
		}
		eingabe, err := req.RequireString("eingabe")
		if err != nil {
			return mcp.NewToolResultError("eingabe fehlt: " + err.Error()), nil
		}
		ausgabe, err := req.RequireString("ausgabe")
		if err != nil {
			return mcp.NewToolResultError("ausgabe fehlt: " + err.Error()), nil
		}
		versuch := req.GetString("versuch", "")

		l, fehler := aktiverLauf(st)
		if fehler != nil {
			return fehler, nil
		}

		rel, schreibfehler := schreibeWunsch(bauRoot, wunschRaum, l, zweck, eingabe, ausgabe, versuch)
		if schreibfehler != nil {
			return mcp.NewToolResultError("Wunsch nicht abgelegt: " + schreibfehler.Error()), nil
		}
		// Die Notiz ist Beiwerk: liegt der Wunsch, ist er angekommen.
		_ = st.WriteNote(l.ID, "Werkzeug-Wunsch abgelegt ("+rel+"): "+zweck)

		return mcp.NewToolResultText("Wunsch abgelegt in " + rel + " (Lauf " +
			fmt.Sprint(l.ID) + "). Er wird geprüft und gebaut — in diesem Lauf " +
			"hast du das Werkzeug noch nicht. Arbeite ohne es weiter oder melde, " +
			"dass die Aufgabe so nicht lösbar ist."), nil
	}
}

// UnterordnerTools ist die Ablage der Werkzeug-Wünsche im Wunsch-Raum.
// Ein Unterordner statt des Raums selbst, damit dort später andere
// Arten von Wünschen daneben liegen können, ohne dass ein Schmied, der
// auf `tools/` schaut, sie mitliest.
const UnterordnerTools = "tools"

// schreibeWunsch legt die Datei an und liefert ihren Bau-relativen Pfad.
func schreibeWunsch(bauRoot, wunschRaum string, l *store.Lauf, zweck, eingabe, ausgabe, versuch string) (string, error) {
	wunschRaum = filepath.Join(wunschRaum, UnterordnerTools)
	dir := filepath.Join(bauRoot, wunschRaum)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	name := fmt.Sprintf("%s-lauf%d.md", time.Now().UTC().Format("20060102-150405"), l.ID)

	var b strings.Builder
	fmt.Fprintf(&b, "# Werkzeug-Wunsch aus Lauf %d (%s)\n\n", l.ID, l.Auftrag)
	fmt.Fprintf(&b, "- **Zweck:** %s\n", zweck)
	fmt.Fprintf(&b, "- **Eingabe:** %s\n", eingabe)
	fmt.Fprintf(&b, "- **Ausgabe:** %s\n", ausgabe)
	if strings.TrimSpace(versuch) != "" {
		fmt.Fprintf(&b, "- **Versucht:** %s\n", versuch)
	}
	fmt.Fprintf(&b, "\nGestellt am %s.\n", time.Now().UTC().Format(time.RFC3339))

	if err := os.WriteFile(filepath.Join(dir, name), []byte(b.String()), 0o644); err != nil {
		return "", err
	}
	return filepath.Join(wunschRaum, name), nil
}

// aktiverLauf löst den Lauf auf. Im Fehlerfall kommt das fertige
// Tool-Ergebnis zurück statt eines Go-Fehlers — dieselben Meldungen wie
// in handler, damit ein Hase nicht zwei Sprachen für dasselbe Problem
// lernt.
func aktiverLauf(st Store) (*store.Lauf, *mcp.CallToolResult) {
	l, err := st.ActiveLauf()
	switch {
	case errors.Is(err, store.ErrNoActiveLauf):
		return nil, mcp.NewToolResultError("kein Lauf aktiv — der Rückkanal " +
			"schreibt nur während eines Laufs.")
	case errors.Is(err, store.ErrAmbiguous):
		return nil, mcp.NewToolResultError("mehrere Läufe gleichzeitig aktiv, " +
			"der Rückkanal kann den richtigen nicht bestimmen. Nichts geschrieben.")
	case err != nil:
		return nil, mcp.NewToolResultError(err.Error())
	}
	return l, nil
}

// handler baut den Tool-Handler: Argument prüfen, Lauf auflösen,
// schreiben. Fehler gehen als Tool-Fehler an den Hasen zurück, nicht
// als Protokollfehler — er soll sie lesen und darauf reagieren können.
func handler(st Store, getan string, schreibe func(Store, int64, string) error) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		text, err := req.RequireString("text")
		if err != nil {
			return mcp.NewToolResultError("text fehlt: " + err.Error()), nil
		}
		if strings.TrimSpace(text) == "" {
			return mcp.NewToolResultError("text ist leer"), nil
		}

		l, err := st.ActiveLauf()
		switch {
		case errors.Is(err, store.ErrNoActiveLauf):
			return mcp.NewToolResultError("kein Lauf aktiv — der Rückkanal " +
				"schreibt nur während eines Laufs."), nil
		case errors.Is(err, store.ErrAmbiguous):
			return mcp.NewToolResultError("mehrere Läufe gleichzeitig aktiv, " +
				"der Rückkanal kann den richtigen nicht bestimmen: " +
				strings.TrimPrefix(err.Error(), "store: ") +
				". Nichts geschrieben — sag es in deiner Antwort."), nil
		case err != nil:
			return mcp.NewToolResultError(err.Error()), nil
		}

		if err := schreibe(st, l.ID, text); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("%s (Lauf %d, Auftrag %s)", getan, l.ID, l.Auftrag)), nil
	}
}

// Serve betreibt den Server über stdio, bis stdin schließt.
func Serve(st Store, version, bauRoot, wunschRaum string) error {
	if err := server.ServeStdio(Server(st, version, bauRoot, wunschRaum)); err != nil {
		return fmt.Errorf("rueckkanal: %w", err)
	}
	return nil
}
