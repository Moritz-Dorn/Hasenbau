package rueckkanal

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/Moritz-Dorn/Hasenbau/internal/store"
)

// fakeStore steht für die Bau-Datenbank: aktiv ist der Lauf, den
// AktiverLauf meldet, fehler das, was sie stattdessen zurückgibt.
type fakeStore struct {
	aktiv         *store.Lauf
	fehler        error
	notizen       []string
	summaries     []string
	schreibFehler error
}

func (f *fakeStore) AktiverLauf() (*store.Lauf, error) {
	if f.fehler != nil {
		return nil, f.fehler
	}
	return f.aktiv, nil
}

func (f *fakeStore) NotizSchreibe(lauf int64, text string) error {
	if f.schreibFehler != nil {
		return f.schreibFehler
	}
	f.notizen = append(f.notizen, text)
	return nil
}

func (f *fakeStore) SummarySchreibe(lauf int64, text string) error {
	if f.schreibFehler != nil {
		return f.schreibFehler
	}
	f.summaries = append(f.summaries, text)
	return nil
}

// verbinde hängt einen In-Process-Client an den Rückkanal — derselbe
// Weg, den opencode über stdio nimmt, nur ohne Prozessgrenze.
func verbinde(t *testing.T, st Store) (*client.Client, context.Context) {
	t.Helper()
	c, err := client.NewInProcessClient(Server(st, "test"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	if err := c.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Initialize(ctx, mcp.InitializeRequest{
		Params: mcp.InitializeParams{
			ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
			ClientInfo:      mcp.Implementation{Name: "test", Version: "test"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	return c, ctx
}

func rufe(t *testing.T, c *client.Client, ctx context.Context, name, text string) *mcp.CallToolResult {
	t.Helper()
	res, err := c.CallTool(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{Name: name, Arguments: map[string]any{"text": text}},
	})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return res
}

func text(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

func TestWerkzeugeStehenBereit(t *testing.T) {
	c, ctx := verbinde(t, &fakeStore{aktiv: &store.Lauf{ID: 7, Auftrag: "pdf-einlagern"}})

	res, err := c.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	gefunden := map[string]bool{}
	for _, w := range res.Tools {
		gefunden[w.Name] = true
		if w.Description == "" {
			t.Errorf("Werkzeug %s ohne Beschreibung — der Hase soll wissen, wofür es da ist", w.Name)
		}
	}
	if !gefunden["notiz"] || !gefunden["summary"] {
		t.Errorf("Werkzeuge = %v, erwartet notiz und summary", gefunden)
	}
}

func TestSchreibtAnDenAktivenLauf(t *testing.T) {
	f := &fakeStore{aktiv: &store.Lauf{ID: 7, Auftrag: "pdf-einlagern"}}
	c, ctx := verbinde(t, f)

	res := rufe(t, c, ctx, "notiz", "Rechnung ohne Datum")
	if res.IsError {
		t.Fatalf("notiz meldet Fehler: %s", text(t, res))
	}
	// Die Quittung nennt den Lauf — der Hase soll sehen, wo es landete.
	if antwort := text(t, res); !strings.Contains(antwort, "Lauf 7") || !strings.Contains(antwort, "pdf-einlagern") {
		t.Errorf("Quittung = %q", antwort)
	}

	if res := rufe(t, c, ctx, "summary", "3 Rechnungen einsortiert"); res.IsError {
		t.Fatalf("summary meldet Fehler: %s", text(t, res))
	}
	if len(f.notizen) != 1 || f.notizen[0] != "Rechnung ohne Datum" {
		t.Errorf("Notizen = %v", f.notizen)
	}
	if len(f.summaries) != 1 || f.summaries[0] != "3 Rechnungen einsortiert" {
		t.Errorf("Summaries = %v", f.summaries)
	}
}

// Ohne eindeutigen Lauf wird nichts geschrieben — und der Hase erfährt
// warum, statt einen Protokollfehler zu sehen.
func TestOhneEindeutigenLaufWirdNichtsGeschrieben(t *testing.T) {
	faelle := []struct {
		name    string
		fehler  error
		erwarte string
	}{
		{"kein Lauf", store.ErrKeinAktiverLauf, "kein Lauf aktiv"},
		{"mehrdeutig", store.ErrMehrdeutig, "mehrere Läufe"},
	}
	for _, f := range faelle {
		t.Run(f.name, func(t *testing.T) {
			fake := &fakeStore{fehler: f.fehler}
			c, ctx := verbinde(t, fake)

			for _, werkzeug := range []string{"notiz", "summary"} {
				res := rufe(t, c, ctx, werkzeug, "irgendwas")
				if !res.IsError {
					t.Errorf("%s: kein Fehler gemeldet", werkzeug)
				}
				if antwort := text(t, res); !strings.Contains(antwort, f.erwarte) {
					t.Errorf("%s: Antwort = %q, erwartet %q darin", werkzeug, antwort, f.erwarte)
				}
			}
			if len(fake.notizen) != 0 || len(fake.summaries) != 0 {
				t.Errorf("trotzdem geschrieben: %v, %v", fake.notizen, fake.summaries)
			}
		})
	}
}

func TestLeererTextWirdAbgelehnt(t *testing.T) {
	f := &fakeStore{aktiv: &store.Lauf{ID: 7, Auftrag: "pdf-einlagern"}}
	c, ctx := verbinde(t, f)

	if res := rufe(t, c, ctx, "summary", "   \n"); !res.IsError {
		t.Error("leerer Text wurde angenommen")
	}
	if len(f.summaries) != 0 {
		t.Errorf("Summaries = %v", f.summaries)
	}
}

func TestSchreibfehlerGehtAnDenHasen(t *testing.T) {
	f := &fakeStore{
		aktiv:         &store.Lauf{ID: 7, Auftrag: "pdf-einlagern"},
		schreibFehler: errSchreib,
	}
	c, ctx := verbinde(t, f)

	res := rufe(t, c, ctx, "notiz", "Rechnung ohne Datum")
	if !res.IsError || !strings.Contains(text(t, res), "Datenbank zu") {
		t.Errorf("Antwort = %q (IsError=%v)", text(t, res), res.IsError)
	}
}

var errSchreib = errString("store: Datenbank zu")

type errString string

func (e errString) Error() string { return string(e) }
