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
	SessionID string         `json:"session_id"`
	Schritte  []TraceSchritt `json:"schritte"`
}

// TraceSchritt ist ein Ereignis in Ausführungsreihenfolge. Art folgt
// dem opencode-Vokabular der Parts: "text", "reasoning", "tool",
// "patch". step-start/-finish und Snapshots sind Rauschen und fehlen.
type TraceSchritt struct {
	Art    string     `json:"art"`
	Rolle  string     `json:"rolle"` // user | assistant
	Text   string     `json:"text,omitempty"`
	Tool   string     `json:"tool,omitempty"`
	CallID string     `json:"call_id,omitempty"`
	Status string     `json:"status,omitempty"` // completed | error | …
	Input  string     `json:"input,omitempty"`  // JSON der Argumente, vollständig
	Output string     `json:"output,omitempty"`
	Fehler string     `json:"fehler,omitempty"`
	Start  *time.Time `json:"start,omitempty"` // nur tool, aus State.Time
	Ende   *time.Time `json:"ende,omitempty"`
}

// ZieheTrace holt die Messages der Session und normalisiert sie. Die
// Reihenfolge der Parts ist die Ausführungsreihenfolge (§11.3).
func ZieheTrace(ctx context.Context, client *sdk.Client, sessionID string) (*Trace, error) {
	msgs, err := client.Session.Messages(ctx, sessionID, sdk.SessionMessagesParams{})
	if err != nil {
		return nil, fmt.Errorf("trace der Session %s: %w", sessionID, err)
	}
	return TraceAus(sessionID, *msgs), nil
}

// TraceAus normalisiert bereits geholte Messages — derselbe Weg wie
// ZieheTrace, nur ohne HTTP. Der Runner hat die Messages am Lauf-Ende
// ohnehin in der Hand und legt den Trace damit ab, ohne ihn ein
// zweites Mal zu holen.
func TraceAus(sessionID string, msgs []sdk.SessionMessagesResponse) *Trace {
	t := &Trace{SessionID: sessionID}
	for _, m := range msgs {
		rolle := string(m.Info.Role)
		for _, p := range m.Parts {
			switch u := p.AsUnion().(type) {
			case sdk.TextPart:
				if s := strings.TrimSpace(u.Text); s != "" {
					t.Schritte = append(t.Schritte, TraceSchritt{Art: "text", Rolle: rolle, Text: s})
				}
			case sdk.ReasoningPart:
				if s := strings.TrimSpace(u.Text); s != "" {
					t.Schritte = append(t.Schritte, TraceSchritt{Art: "reasoning", Rolle: rolle, Text: s})
				}
			case sdk.ToolPart:
				input, err := json.Marshal(u.State.Input)
				if err != nil {
					input = []byte(fmt.Sprintf("%q", fmt.Sprint(u.State.Input)))
				}
				s := TraceSchritt{
					Art:    "tool",
					Rolle:  rolle,
					Tool:   u.Tool,
					CallID: u.CallID,
					Status: string(u.State.Status),
					Input:  string(input),
					Output: u.State.Output,
					Fehler: u.State.Error,
				}
				s.Start, s.Ende = toolZeiten(u.State.Time)
				t.Schritte = append(t.Schritte, s)
			default:
				if p.Type == sdk.PartTypePatch {
					t.Schritte = append(t.Schritte, TraceSchritt{Art: "patch", Rolle: rolle})
				}
			}
		}
	}
	return t
}

// Gekuerzt liefert eine Kopie, deren Ausgaben und Fehlertexte bei max
// Bytes gekappt sind. Was in die Bau-DB geht, ist Baumeister-Input,
// kein Archiv: für die Verdichtung zählen Werkzeug, Argumente und
// Status — nicht der Volltext einer gelesenen Datei. Der Live-Weg über
// den Server bleibt ungekürzt.
func (t *Trace) Gekuerzt(max int) *Trace {
	kopie := &Trace{SessionID: t.SessionID, Schritte: make([]TraceSchritt, len(t.Schritte))}
	copy(kopie.Schritte, t.Schritte)
	for i := range kopie.Schritte {
		s := &kopie.Schritte[i]
		s.Output = kappe(s.Output, max)
		s.Fehler = kappe(s.Fehler, max)
		s.Input = kappe(s.Input, max)
	}
	return kopie
}

// kappe schneidet auf max Bytes und sagt, dass es das getan hat —
// stilles Abschneiden würde der Baumeister für die ganze Wahrheit
// halten. Schnitt an einer Runen-Grenze, damit gültiges UTF-8 bleibt.
func kappe(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	schnitt := max
	for schnitt > 0 && !utf8.RuneStart(s[schnitt]) {
		schnitt--
	}
	return s[:schnitt] + fmt.Sprintf("… (gekürzt, %d Bytes insgesamt)", len(s))
}

// toolZeiten liest start/end (Unix-ms) aus dem Time-Union des
// Tool-States — je nach Status kann eines oder beides fehlen.
func toolZeiten(zeit interface{}) (*time.Time, *time.Time) {
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
// Schritte, Tool-Argumente vollständig (der Baumeister muss daraus
// generalisieren — was ist Parameter, was Konstante), Fehlversuche
// deutlich markiert.
func (t *Trace) Markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Trace der Session %s\n", t.SessionID)
	for i, s := range t.Schritte {
		switch s.Art {
		case "tool":
			marker := s.Status
			if s.Status == "error" {
				marker = "FEHLVERSUCH"
			}
			fmt.Fprintf(&b, "\n%d. [tool %s — %s]", i+1, s.Tool, marker)
			if s.Start != nil && s.Ende != nil {
				fmt.Fprintf(&b, " (%s, %s)", s.Start.Format("15:04:05"), s.Ende.Sub(*s.Start).Round(time.Millisecond))
			}
			b.WriteString("\n")
			fmt.Fprintf(&b, "   input: %s\n", s.Input)
			if s.Output != "" {
				fmt.Fprintf(&b, "   output: %s\n", einruecken(s.Output))
			}
			if s.Fehler != "" {
				fmt.Fprintf(&b, "   fehler: %s\n", einruecken(s.Fehler))
			}
		case "patch":
			fmt.Fprintf(&b, "\n%d. [patch]\n", i+1)
		default:
			fmt.Fprintf(&b, "\n%d. [%s, %s]\n   %s\n", i+1, s.Art, s.Rolle, einruecken(s.Text))
		}
	}
	return b.String()
}

// einruecken hält mehrzeilige Inhalte unter dem Schritt eingerückt.
func einruecken(s string) string {
	return strings.ReplaceAll(strings.TrimSpace(s), "\n", "\n   ")
}
