package backchannel

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/Moritz-Dorn/Hasenbau/internal/store"
)

// fakeStore steht für die Bau-Datenbank: aktiv ist der Lauf, den
// ActiveLauf meldet, fehler das, was sie stattdessen zurückgibt.
type fakeStore struct {
	aktiv         *store.Lauf
	fehler        error
	notizen       []string
	summaries     []string
	schreibFehler error
}

func (f *fakeStore) ActiveLauf() (*store.Lauf, error) {
	if f.fehler != nil {
		return nil, f.fehler
	}
	return f.aktiv, nil
}

func (f *fakeStore) WriteNote(lauf int64, text string) error {
	if f.schreibFehler != nil {
		return f.schreibFehler
	}
	f.notizen = append(f.notizen, text)
	return nil
}

func (f *fakeStore) WriteSummary(lauf int64, text string) error {
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
	return verbindeMitRaum(t, st, "", "")
}

// verbindeMitRaum ist dasselbe mit Wunsch-Raum — ohne ihn bietet der
// Server tool_request gar nicht an.
func verbindeMitRaum(t *testing.T, st Store, bauRoot, wunschRaum string) (*client.Client, context.Context) {
	t.Helper()
	c, err := client.NewInProcessClient(Server(st, "test", bauRoot, wunschRaum))
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
		t.Fatalf("notiz meldet Error: %s", text(t, res))
	}
	// Die Quittung nennt den Lauf — der Hase soll sehen, wo es landete.
	if antwort := text(t, res); !strings.Contains(antwort, "Lauf 7") || !strings.Contains(antwort, "pdf-einlagern") {
		t.Errorf("Quittung = %q", antwort)
	}

	if res := rufe(t, c, ctx, "summary", "3 Rechnungen einsortiert"); res.IsError {
		t.Fatalf("summary meldet Error: %s", text(t, res))
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
		{"kein Lauf", store.ErrNoActiveLauf, "kein Lauf aktiv"},
		{"mehrdeutig", store.ErrAmbiguous, "mehrere Läufe"},
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

// TestWerkzeugWunschLegtDateiUndNotizAn: der Wunsch hat zwei Leser. Die
// Datei im Wunsch-Raum ist der Eingang des Schmieds — ein watch-Trigger
// darauf braucht keinen neuen Mechanismus. Die Notiz macht im Trace
// sichtbar, dass der Hase gefragt hat; ohne sie sähe der Baumeister nur
// einen Lauf, der nichts zustande brachte.
func TestToolRequestLegtDateiUndNotizAn(t *testing.T) {
	bauRoot := t.TempDir()
	st := &fakeStore{aktiv: &store.Lauf{ID: 7, Auftrag: "einlagern"}}
	c, ctx := verbindeMitRaum(t, st, bauRoot, "raeume/wuensche/")

	res, err := c.CallTool(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "tool_request",
			Arguments: map[string]any{
				"zweck":   "120 Vorlagen nach Typ verteilen",
				"eingabe": "ein Verzeichnis mit Vorlagen",
				"ausgabe": "Dateien je Typ einsortiert, plus CSV",
				"versuch": "von Hand kopiert, nach 20 abgebrochen",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("Fehler: %s", text(t, res))
	}

	// tools/ und nicht der Raum selbst: dort sollen später andere
	// Wunsch-Arten danebenliegen können.
	dateien, err := filepath.Glob(filepath.Join(bauRoot, "raeume/wuensche/tools", "*.md"))
	if err != nil || len(dateien) != 1 {
		t.Fatalf("Wunsch-Dateien = %v (%v)", dateien, err)
	}
	inhalt, err := os.ReadFile(dateien[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, muss := range []string{"Lauf 7", "einlagern", "120 Vorlagen", "ein Verzeichnis", "plus CSV", "nach 20 abgebrochen"} {
		if !strings.Contains(string(inhalt), muss) {
			t.Errorf("Wunsch-Datei enthält %q nicht:\n%s", muss, inhalt)
		}
	}
	if len(st.notizen) != 1 || !strings.Contains(st.notizen[0], "120 Vorlagen") {
		t.Errorf("Notiz am Lauf fehlt: %v", st.notizen)
	}
	// Der Hase muss erfahren, dass er das Werkzeug JETZT nicht bekommt —
	// sonst wartet er darauf oder hält die Aufgabe für gelöst.
	if !strings.Contains(text(t, res), "in diesem Lauf") {
		t.Errorf("Antwort sagt nicht, dass das Werkzeug jetzt fehlt: %s", text(t, res))
	}
}

// TestWerkzeugWunschOhneRaumGibtEsNicht: ein Briefkasten, den niemand
// leert, ist schlimmer als keiner. Ohne Wunsch-Raum darf der Hase das
// Werkzeug nicht einmal sehen.
func TestToolRequestOhneRaumGibtEsNicht(t *testing.T) {
	st := &fakeStore{aktiv: &store.Lauf{ID: 1, Auftrag: "x"}}
	c, ctx := verbinde(t, st)

	liste, err := c.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range liste.Tools {
		if w.Name == "tool_request" {
			t.Error("tool_request wird ohne Wunsch-Raum angeboten")
		}
	}
}
