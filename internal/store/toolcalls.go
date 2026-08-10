// toolcalls.go: die Tool-Aufrufe eines Laufs, normalisiert zum Rechnen
// (PLAN.md §8 Phase 2, Hasenbau-4cx.1).
//
// Der Trace daneben bleibt das Protokoll zum *Lesen* — er trägt auch
// reasoning und Text, und er gehört als Ganzes dem Baumeister. Diese
// Tabelle ist dasselbe Material zum *Rechnen*: welche Folge kommt in
// wie vielen Läufen vor, welche Argument-Position variiert, wo scheitern
// Aufrufe an Permissions. Aus einem einzelnen Trace ist prinzipiell
// nicht entscheidbar, was Parameter und was Konstante war (§8); die
// Antwort darauf ist nicht ein besserer Prompt, sondern mehr Material.
//
// Der Store kennt das Trace-Format weiterhin nicht (Schichtgrenze §2):
// Zeilen kommen fertig herein, und für den Nachzug alter Läufe reicht
// der Aufrufer eine Parse-Funktion durch.
package store

import (
	"database/sql"
	"fmt"
	"strings"
)

// ToolCall ist ein Tool-Aufruf in Ausführungsreihenfolge.
type ToolCall struct {
	Nr         int    // Position im Lauf, ab 1
	Tool       string // 'read', 'write', 'hasenbau_summary', …
	Args       string // JSON der Argumente, vollständig
	Status     string // completed | error | …
	Error      string // Begründung bei status='error'
	DurationMs int64  // 0 = der Trace hatte keine Zeiten
}

// LaufTools sind die Aufrufe eines Laufs samt seiner Signatur.
type LaufTools struct {
	Lauf      int64
	Signature string
	Calls     []ToolCall
}

// Signature ist die geordnete Tool-Folge, z.B.
// 'read>write>hasenbau_summary'.
//
// Fehlversuche stehen mit drin. Sie gehören zur Wahrheit über den Lauf,
// und wer sie beim Vergleichen nicht will, filtert die Zeilen — aus
// einer Signatur, aus der sie schon herausgerechnet sind, bekommt sie
// niemand zurück.
func Signature(calls []ToolCall) string {
	namen := make([]string, 0, len(calls))
	for _, c := range calls {
		namen = append(namen, c.Tool)
	}
	return strings.Join(namen, ">")
}

// WriteToolCalls ersetzt die Aufrufe eines Laufs und setzt seine
// Signatur — beides in einer Transaktion, damit die Spalte nie etwas
// anderes behauptet als die Zeilen.
//
// Ein Lauf ganz ohne Tool-Calls bekommt eine leere Signatur, nicht
// NULL: „hat nichts angefasst" ist ein Befund, „wurde nie ausgewertet"
// ist ein anderer, und die beiden zu unterscheiden ist der Sinn der
// Spalte.
func (s *Store) WriteToolCalls(lauf int64, calls []ToolCall) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("store: Tool-Calls zu Lauf %d: %w", lauf, err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM tool_calls WHERE lauf = ?`, lauf); err != nil {
		return fmt.Errorf("store: Tool-Calls zu Lauf %d ersetzen: %w", lauf, err)
	}
	for i, c := range calls {
		nr := c.Nr
		if nr == 0 {
			nr = i + 1
		}
		var dauer any
		if c.DurationMs > 0 {
			dauer = c.DurationMs
		}
		if _, err := tx.Exec(`
			INSERT INTO tool_calls (lauf, nr, tool, args_json, status, error, duration_ms)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			lauf, nr, c.Tool, c.Args, c.Status, nullIfEmpty(c.Error), dauer); err != nil {
			return fmt.Errorf("store: Tool-Call %d zu Lauf %d: %w", nr, lauf, err)
		}
	}
	if _, err := tx.Exec(`UPDATE laeufe SET tool_signature = ? WHERE id = ?`,
		Signature(calls), lauf); err != nil {
		return fmt.Errorf("store: Signatur zu Lauf %d: %w", lauf, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: Tool-Calls zu Lauf %d: %w", lauf, err)
	}
	return nil
}

// ToolCalls liefert die Aufrufe eines Laufs in Reihenfolge.
func (s *Store) ToolCalls(lauf int64) ([]ToolCall, error) {
	rows, err := s.db.Query(`
		SELECT nr, tool, args_json, status, COALESCE(error,''), COALESCE(duration_ms,0)
		FROM tool_calls WHERE lauf = ? ORDER BY nr`, lauf)
	if err != nil {
		return nil, fmt.Errorf("store: Tool-Calls zu Lauf %d lesen: %w", lauf, err)
	}
	defer rows.Close()
	return scanToolCalls(rows)
}

// ToolCallHistory liefert die jüngsten ausgewerteten Läufe eines
// Auftrags mit ihren Aufrufen, neueste zuerst — das Material für
// „gleiche Folge in N von M Läufen" und „Argumente je Position"
// (Hasenbau-4cx.2). Läufe ohne Auswertung fehlen; sie würden jede
// Statistik verwässern, ohne etwas beizutragen.
func (s *Store) ToolCallHistory(auftrag string, limit int) ([]LaufTools, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(`
		SELECT id, COALESCE(tool_signature,'')
		FROM laeufe
		WHERE auftrag = ? AND tool_signature IS NOT NULL
		ORDER BY started DESC, id DESC LIMIT ?`, auftrag, limit)
	if err != nil {
		return nil, fmt.Errorf("store: Signaturen von %q lesen: %w", auftrag, err)
	}
	var out []LaufTools
	for rows.Next() {
		var lt LaufTools
		if err := rows.Scan(&lt.Lauf, &lt.Signature); err != nil {
			rows.Close()
			return nil, fmt.Errorf("store: Signaturen von %q lesen: %w", auftrag, err)
		}
		out = append(out, lt)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("store: Signaturen von %q lesen: %w", auftrag, err)
	}
	rows.Close()

	for i := range out {
		calls, err := s.ToolCalls(out[i].Lauf)
		if err != nil {
			return nil, err
		}
		out[i].Calls = calls
	}
	return out, nil
}

// TraceParser macht aus rohem Trace-JSON die Aufrufe eines Laufs. Der
// Aufrufer reicht ihn durch — so bleibt das Trace-Format bei
// internal/opencode und nicht hier.
type TraceParser func(roh []byte) ([]ToolCall, error)

// BackfillToolCalls zieht die Aufrufe der Läufe nach, die einen Trace
// haben, aber noch keine Zeilen. Zurück kommt die Zahl der
// nachgezogenen Läufe.
//
// Kein Marker, keine Einmal-Migration: die Auswahl ist die Bedingung.
// Damit ist der Nachzug idempotent, überlebt einen Abbruch mittendrin
// und zieht auch Läufe nach, deren Trace erst später über `dig`
// entstanden ist.
func (s *Store) BackfillToolCalls(parse TraceParser) (int, error) {
	rows, err := s.db.Query(`
		SELECT t.lauf, t.json FROM trace t
		LEFT JOIN laeufe l ON l.id = t.lauf
		WHERE l.tool_signature IS NULL
		ORDER BY t.lauf`)
	if err != nil {
		return 0, fmt.Errorf("store: Läufe ohne Tool-Calls suchen: %w", err)
	}
	type offen struct {
		lauf int64
		roh  string
	}
	var todo []offen
	for rows.Next() {
		var o offen
		if err := rows.Scan(&o.lauf, &o.roh); err != nil {
			rows.Close()
			return 0, fmt.Errorf("store: Läufe ohne Tool-Calls suchen: %w", err)
		}
		todo = append(todo, o)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, fmt.Errorf("store: Läufe ohne Tool-Calls suchen: %w", err)
	}
	rows.Close()

	var nachgezogen int
	for _, o := range todo {
		calls, err := parse([]byte(o.roh))
		if err != nil {
			// Ein unlesbarer Trace ist kein Grund, den Rest liegen zu
			// lassen — und kein Grund, den Aufrufer scheitern zu lassen.
			continue
		}
		if err := s.WriteToolCalls(o.lauf, calls); err != nil {
			return nachgezogen, err
		}
		nachgezogen++
	}
	return nachgezogen, nil
}

func scanToolCalls(rows *sql.Rows) ([]ToolCall, error) {
	var out []ToolCall
	for rows.Next() {
		var c ToolCall
		if err := rows.Scan(&c.Nr, &c.Tool, &c.Args, &c.Status, &c.Error, &c.DurationMs); err != nil {
			return nil, fmt.Errorf("store: Tool-Call lesen: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: Tool-Calls lesen: %w", err)
	}
	return out, nil
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
