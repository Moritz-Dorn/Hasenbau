// toolcalls.go: die Brücke zwischen Trace und Tabelle (Hasenbau-4cx.1).
//
// Hier, weil der Runner als einziger beide Seiten kennt: das
// Trace-Format gehört internal/opencode, die Zeilen gehören
// internal/store, und keines der beiden soll das andere importieren
// müssen (Schichtgrenze §2).
package runner

import (
	"encoding/json"
	"fmt"

	"github.com/Moritz-Dorn/Hasenbau/internal/opencode"
	"github.com/Moritz-Dorn/Hasenbau/internal/store"
)

// toolCalls zieht die Aufrufe aus einem Trace, in
// Ausführungsreihenfolge und durchnummeriert.
//
// Genommen wird der Trace, nicht der Event-Stream: der Stream meldet
// denselben Aufruf dreimal (pending, running, completed) und kann
// Ereignisse verlieren, wenn die Verbindung abreißt. Der Trace ist der
// Stand am Ende — je Aufruf eine Zeile, mit den endgültigen Argumenten.
func toolCalls(t *opencode.Trace) []store.ToolCall {
	if t == nil {
		return nil
	}
	var calls []store.ToolCall
	for _, s := range t.Steps {
		if s.Kind != "tool" {
			continue
		}
		c := store.ToolCall{
			Nr:     len(calls) + 1,
			Tool:   s.Tool,
			Args:   s.Input,
			Status: s.Status,
			Error:  s.Error,
		}
		if s.Start != nil && s.End != nil {
			if ms := s.End.Sub(*s.Start).Milliseconds(); ms > 0 {
				c.DurationMs = ms
			}
		}
		calls = append(calls, c)
	}
	return calls
}

// ToolCallsFromTrace parst abgelegtes Trace-JSON zu Tool-Call-Zeilen —
// die Parse-Funktion für store.BackfillToolCalls.
func ToolCallsFromTrace(roh []byte) ([]store.ToolCall, error) {
	var t opencode.Trace
	if err := json.Unmarshal(roh, &t); err != nil {
		return nil, fmt.Errorf("trace parsen: %w", err)
	}
	return toolCalls(&t), nil
}
