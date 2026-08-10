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
	"strings"

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

// Server baut den MCP-Server mit notiz und summary.
func Server(st Store, version string) *server.MCPServer {
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

	return s
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
func Serve(st Store, version string) error {
	if err := server.ServeStdio(Server(st, version)); err != nil {
		return fmt.Errorf("rueckkanal: %w", err)
	}
	return nil
}
