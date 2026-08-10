// trace.go: zieht den Verlauf einer Session und normalisiert ihn zum
// Baumeister-Input (§8 Phase 2, verifiziert in §11.3): Absicht
// (reasoning), Taten (tool, volle Argumente) und Fehlversuche
// (status=error) in Ausführungsreihenfolge — strukturiert, kein
// Log-Parsing.
package opencode

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	sdk "github.com/sst/opencode-sdk-go"
)

// Trace ist der aufbereitete Verlauf einer Session.
type Trace struct {
	SessionID string      `json:"session_id"`
	Steps     []TraceStep `json:"steps"`
}

// TraceStep ist ein Ereignis in Ausführungsreihenfolge. Kind folgt
// dem opencode-Vokabular der Parts: "text", "reasoning", "tool",
// "patch". step-start/-finish und Snapshots sind Rauschen und fehlen.
type TraceStep struct {
	Kind   string     `json:"kind"`
	Role   string     `json:"role"` // user | assistant
	Text   string     `json:"text,omitempty"`
	Tool   string     `json:"tool,omitempty"`
	CallID string     `json:"call_id,omitempty"`
	Status string     `json:"status,omitempty"` // completed | error | …
	Input  string     `json:"input,omitempty"`  // JSON der Argumente, vollständig
	Output string     `json:"output,omitempty"`
	Error  string     `json:"error,omitempty"`
	Start  *time.Time `json:"start,omitempty"` // nur tool, aus State.Time
	End    *time.Time `json:"end,omitempty"`
}

// FetchTrace holt die Messages der Session und normalisiert sie. Die
// Reihenfolge der Parts ist die Ausführungsreihenfolge (§11.3).
func FetchTrace(ctx context.Context, client *sdk.Client, sessionID string) (*Trace, error) {
	msgs, err := client.Session.Messages(ctx, sessionID, sdk.SessionMessagesParams{})
	if err != nil {
		return nil, fmt.Errorf("trace der Session %s: %w", sessionID, err)
	}
	return TraceFrom(sessionID, *msgs), nil
}

// TraceFrom normalisiert bereits geholte Messages — derselbe Weg wie
// FetchTrace, nur ohne HTTP. Der Runner hat die Messages am Lauf-Ende
// ohnehin in der Hand und legt den Trace damit ab, ohne ihn ein
// zweites Mal zu holen.
func TraceFrom(sessionID string, msgs []sdk.SessionMessagesResponse) *Trace {
	t := &Trace{SessionID: sessionID}
	for _, m := range msgs {
		rolle := string(m.Info.Role)
		for _, p := range m.Parts {
			switch u := p.AsUnion().(type) {
			case sdk.TextPart:
				if s := strings.TrimSpace(u.Text); s != "" {
					t.Steps = append(t.Steps, TraceStep{Kind: "text", Role: rolle, Text: s})
				}
			case sdk.ReasoningPart:
				if s := strings.TrimSpace(u.Text); s != "" {
					t.Steps = append(t.Steps, TraceStep{Kind: "reasoning", Role: rolle, Text: s})
				}
			case sdk.ToolPart:
				input, err := json.Marshal(u.State.Input)
				if err != nil {
					input = []byte(fmt.Sprintf("%q", fmt.Sprint(u.State.Input)))
				}
				s := TraceStep{
					Kind:   "tool",
					Role:   rolle,
					Tool:   u.Tool,
					CallID: u.CallID,
					Status: string(u.State.Status),
					Input:  string(input),
					Output: u.State.Output,
					Error:  u.State.Error,
				}
				s.Start, s.End = toolTimes(u.State.Time)
				t.Steps = append(t.Steps, s)
			default:
				if p.Type == sdk.PartTypePatch {
					t.Steps = append(t.Steps, TraceStep{Kind: "patch", Role: rolle})
				}
			}
		}
	}
	return t
}

// Truncated liefert eine Kopie, deren Ausgaben und Fehlertexte bei max
// Bytes gekappt sind. Was in die Bau-DB geht, ist Baumeister-Input,
// kein Archiv: für die Verdichtung zählen Werkzeug, Argumente und
// Status — nicht der Volltext einer gelesenen Datei. Der Live-Weg über
// den Server bleibt ungekürzt.
func (t *Trace) Truncated(max int) *Trace {
	kopie := &Trace{SessionID: t.SessionID, Steps: make([]TraceStep, len(t.Steps))}
	copy(kopie.Steps, t.Steps)
	for i := range kopie.Steps {
		s := &kopie.Steps[i]
		s.Output = truncateField(s.Output, max)
		s.Error = truncateField(s.Error, max)
		s.Input = truncateField(s.Input, max)
	}
	return kopie
}

// truncateField schneidet auf max Bytes und sagt, dass es das getan hat —
// stilles Abschneiden würde der Baumeister für die ganze Wahrheit
// halten. Schnitt an einer Runen-Grenze, damit gültiges UTF-8 bleibt.
func truncateField(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	schnitt := max
	for schnitt > 0 && !utf8.RuneStart(s[schnitt]) {
		schnitt--
	}
	return s[:schnitt] + fmt.Sprintf("… (gekürzt, %d Bytes insgesamt)", len(s))
}

// toolTimes liest start/end (Unix-ms) aus dem Time-Union des
// Tool-States — je nach Status kann eines oder beides fehlen.
func toolTimes(zeit interface{}) (*time.Time, *time.Time) {
	roh, err := json.Marshal(zeit)
	if err != nil {
		return nil, nil
	}
	var z struct {
		Start float64 `json:"start"`
		End   float64 `json:"end"`
	}
	if json.Unmarshal(roh, &z) != nil {
		return nil, nil
	}
	ms := func(v float64) *time.Time {
		if v == 0 {
			return nil
		}
		t := time.UnixMilli(int64(v)).UTC()
		return &t
	}
	return ms(z.Start), ms(z.End)
}

// Markdown rendert den Trace als Baumeister-Input: nummerierte
// Steps, Tool-Argumente vollständig (der Baumeister muss daraus
// generalisieren — was ist Parameter, was Konstante), Fehlversuche
// deutlich markiert.
func (t *Trace) Markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Trace der Session %s\n", t.SessionID)
	for i, s := range t.Steps {
		switch s.Kind {
		case "tool":
			marker := s.Status
			if s.Status == "error" {
				marker = "FEHLVERSUCH"
			}
			fmt.Fprintf(&b, "\n%d. [tool %s — %s]", i+1, s.Tool, marker)
			if s.Start != nil && s.End != nil {
				fmt.Fprintf(&b, " (%s, %s)", s.Start.Format("15:04:05"), s.End.Sub(*s.Start).Round(time.Millisecond))
			}
			b.WriteString("\n")
			fmt.Fprintf(&b, "   input: %s\n", s.Input)
			if s.Output != "" {
				fmt.Fprintf(&b, "   output: %s\n", indent(s.Output))
			}
			if s.Error != "" {
				fmt.Fprintf(&b, "   fehler: %s\n", indent(s.Error))
			}
		case "patch":
			fmt.Fprintf(&b, "\n%d. [patch]\n", i+1)
		default:
			fmt.Fprintf(&b, "\n%d. [%s, %s]\n   %s\n", i+1, s.Kind, s.Role, indent(s.Text))
		}
	}
	return b.String()
}

// indent hält mehrzeilige Inhalte unter dem Schritt eingerückt.
func indent(s string) string {
	return strings.ReplaceAll(strings.TrimSpace(s), "\n", "\n   ")
}
