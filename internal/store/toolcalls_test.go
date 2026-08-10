package store

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func storeMitLauf(t *testing.T, auftrag string, n int) (*Store, []int64) {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "state", "hasenbau.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	var ids []int64
	for i := 0; i < n; i++ {
		id, err := st.StartLauf(auftrag, "manual", fmt.Sprintf("in-%d", i))
		if err != nil {
			t.Fatal(err)
		}
		if err := st.EndLauf(id, LaufResult{Status: "ok", SessionID: fmt.Sprintf("ses_%d", i)}); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	return st, ids
}

func TestToolCallsSchreibenUndLesen(t *testing.T) {
	st, ids := storeMitLauf(t, "pdf-einlagern", 1)

	calls := []ToolCall{
		{Tool: "read", Args: `{"path":"a.md"}`, Status: "completed", DurationMs: 12},
		{Tool: "write", Args: `{"path":"b.md"}`, Status: "error", Error: "permission denied"},
		{Tool: "hasenbau_summary", Args: `{"text":"fertig"}`, Status: "completed"},
	}
	if err := st.WriteToolCalls(ids[0], calls); err != nil {
		t.Fatal(err)
	}

	zurueck, err := st.ToolCalls(ids[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(zurueck) != 3 {
		t.Fatalf("%d Zeilen, erwartet 3", len(zurueck))
	}
	if zurueck[0].Nr != 1 || zurueck[2].Nr != 3 {
		t.Errorf("Nummerierung: %+v", zurueck)
	}
	if zurueck[1].Error != "permission denied" || zurueck[1].Status != "error" {
		t.Errorf("Fehlversuch ging verloren: %+v", zurueck[1])
	}
	if zurueck[0].DurationMs != 12 || zurueck[2].DurationMs != 0 {
		t.Errorf("Dauer: %+v", zurueck)
	}

	// Die Signatur steht in laeufe und passt zu den Zeilen.
	l, err := st.LaufByID(ids[0])
	if err != nil {
		t.Fatal(err)
	}
	if l.ToolSignature != "read>write>hasenbau_summary" {
		t.Errorf("Signatur = %q", l.ToolSignature)
	}
}

// Ein zweiter Aufruf ersetzt — sonst verdoppelte ein Backfill über
// einen schon ausgewerteten Lauf dessen Zeilen.
func TestToolCallsZweiterAufrufErsetzt(t *testing.T) {
	st, ids := storeMitLauf(t, "a", 1)
	if err := st.WriteToolCalls(ids[0], []ToolCall{{Tool: "read", Args: "{}", Status: "completed"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.WriteToolCalls(ids[0], []ToolCall{{Tool: "glob", Args: "{}", Status: "completed"}}); err != nil {
		t.Fatal(err)
	}
	zurueck, _ := st.ToolCalls(ids[0])
	if len(zurueck) != 1 || zurueck[0].Tool != "glob" {
		t.Errorf("Zeilen = %+v", zurueck)
	}
	l, _ := st.LaufByID(ids[0])
	if l.ToolSignature != "glob" {
		t.Errorf("Signatur = %q", l.ToolSignature)
	}
}

// „Hat nichts angefasst" ist ein Befund und muss von „nie ausgewertet"
// unterscheidbar bleiben.
func TestToolCallsLeererLaufBekommtLeereSignatur(t *testing.T) {
	st, ids := storeMitLauf(t, "a", 2)
	if err := st.WriteToolCalls(ids[0], nil); err != nil {
		t.Fatal(err)
	}

	verlauf, err := st.ToolCallHistory("a", 10)
	if err != nil {
		t.Fatal(err)
	}
	// Nur der ausgewertete Lauf taucht auf, der andere nicht.
	if len(verlauf) != 1 || verlauf[0].Lauf != ids[0] || verlauf[0].Signature != "" {
		t.Errorf("Verlauf = %+v", verlauf)
	}
}

// Das Akzeptanzkriterium von Hasenbau-4cx.1: „gleiche Folge in N von M
// Läufen" und „Argumente je Position" ohne opencode.
func TestToolCallHistoryTraegtDieAnalyse(t *testing.T) {
	st, ids := storeMitLauf(t, "pdf-einlagern", 3)
	folge := func(datei string) []ToolCall {
		return []ToolCall{
			{Tool: "read", Args: fmt.Sprintf(`{"path":%q}`, datei), Status: "completed"},
			{Tool: "write", Args: `{"path":"raeume/lager/x.md"}`, Status: "completed"},
		}
	}
	for i, id := range ids {
		if err := st.WriteToolCalls(id, folge(fmt.Sprintf("sources/%d.pdf", i))); err != nil {
			t.Fatal(err)
		}
	}

	verlauf, err := st.ToolCallHistory("pdf-einlagern", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(verlauf) != 3 {
		t.Fatalf("%d Läufe, erwartet 3", len(verlauf))
	}
	// Neueste zuerst.
	if verlauf[0].Lauf != ids[2] {
		t.Errorf("Reihenfolge: %d zuerst", verlauf[0].Lauf)
	}

	// „Gleiche Folge in N von M": ein String-Vergleich.
	gleich := 0
	for _, v := range verlauf {
		if v.Signature == "read>write" {
			gleich++
		}
	}
	if gleich != 3 {
		t.Errorf("gleiche Folge in %d von 3", gleich)
	}

	// „Argumente je Position": Position 1 variiert, Position 2 nicht —
	// genau die Unterscheidung Parameter/Konstante.
	pos1, pos2 := map[string]bool{}, map[string]bool{}
	for _, v := range verlauf {
		pos1[v.Calls[0].Args] = true
		pos2[v.Calls[1].Args] = true
	}
	if len(pos1) != 3 {
		t.Errorf("Position 1 sollte variieren: %v", pos1)
	}
	if len(pos2) != 1 {
		t.Errorf("Position 2 sollte konstant sein: %v", pos2)
	}
}

func TestBackfillZiehtNachUndIstIdempotent(t *testing.T) {
	st, ids := storeMitLauf(t, "a", 2)

	// Zwei Läufe mit Trace, keiner mit Tool-Calls.
	for i, id := range ids {
		roh, _ := json.Marshal(map[string]any{"tools": []string{"read", "write"}, "i": i})
		if err := st.WriteTrace(id, fmt.Sprintf("ses_%d", i), roh); err != nil {
			t.Fatal(err)
		}
	}
	parse := func(roh []byte) ([]ToolCall, error) {
		var v struct {
			Tools []string `json:"tools"`
		}
		if err := json.Unmarshal(roh, &v); err != nil {
			return nil, err
		}
		var calls []ToolCall
		for _, tool := range v.Tools {
			calls = append(calls, ToolCall{Tool: tool, Args: "{}", Status: "completed"})
		}
		return calls, nil
	}

	n, err := st.BackfillToolCalls(parse)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("%d nachgezogen, erwartet 2", n)
	}
	l, _ := st.LaufByID(ids[0])
	if l.ToolSignature != "read>write" {
		t.Errorf("Signatur = %q", l.ToolSignature)
	}

	// Zweiter Durchlauf findet nichts mehr — die Auswahl ist die
	// Bedingung, kein Marker.
	n, err = st.BackfillToolCalls(parse)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("zweiter Durchlauf zog %d nach", n)
	}
}

// Ein unlesbarer Trace darf den Rest nicht aufhalten.
func TestBackfillUeberspringtKaputteTraces(t *testing.T) {
	st, ids := storeMitLauf(t, "a", 2)
	if err := st.WriteTrace(ids[0], "ses_0", []byte("kein json")); err != nil {
		t.Fatal(err)
	}
	if err := st.WriteTrace(ids[1], "ses_1", []byte(`{"ok":true}`)); err != nil {
		t.Fatal(err)
	}
	parse := func(roh []byte) ([]ToolCall, error) {
		var v map[string]any
		if err := json.Unmarshal(roh, &v); err != nil {
			return nil, err
		}
		return []ToolCall{{Tool: "read", Args: "{}", Status: "completed"}}, nil
	}

	n, err := st.BackfillToolCalls(parse)
	if err != nil {
		t.Fatalf("kaputter Trace hat den Nachzug abgebrochen: %v", err)
	}
	if n != 1 {
		t.Errorf("%d nachgezogen, erwartet 1", n)
	}
}

func TestSignaturLeerUndEinzeln(t *testing.T) {
	if s := Signature(nil); s != "" {
		t.Errorf("leer = %q", s)
	}
	if s := Signature([]ToolCall{{Tool: "read"}}); s != "read" {
		t.Errorf("einzeln = %q", s)
	}
	if s := Signature([]ToolCall{{Tool: "a"}, {Tool: "b"}}); !strings.Contains(s, ">") {
		t.Errorf("Trenner fehlt: %q", s)
	}
}
