// Package findings rechnet über die Läufe eines Auftrags und schlägt
// vor, was sich lohnen könnte (PLAN.md §8 Phase 2, Hasenbau-4cx.2).
//
// Kein Modell. Das Anschauen kostet nichts, ist reproduzierbar, und
// jeder Befund ist auf konkrete Läufe zurückführbar — die Intelligenz
// steckt im Trigger-Layer, nicht im Modell (§8). Was hier herauskommt,
// sind Kandidaten für einen Menschen, keine Beschlüsse: der Baumeister
// arbeitet später genau den aus, den jemand ausgewählt hat.
//
// Der Kern ist die Antwort auf die harte Stelle aus §8: aus EINEM Trace
// ist nicht entscheidbar, was Parameter und was Konstante war. Über N
// Läufe schon — was variiert, ist der Parameter.
package findings

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Moritz-Dorn/Hasenbau/internal/store"
)

// Kind unterscheidet die Befundarten. Englisch wie alle Datenwerte
// (§1); die Überschriften in der Ausgabe sind Prosa und deutsch.
const (
	KindGang       = "gang-candidate"
	KindPermission = "permission-friction"
	KindCost       = "cost-and-duration"
)

// Finding ist ein einzelner Vorschlag. Laeufe nennt die Läufe, auf
// denen er beruht — ohne die wäre er eine Behauptung.
type Finding struct {
	Kind    string   `json:"kind"`
	Title   string   `json:"title"`
	Detail  string   `json:"detail"`
	Laeufe  []int64  `json:"laeufe"`
	Steps   []Step   `json:"steps,omitempty"`   // nur bei gang-candidate
	Samples []string `json:"samples,omitempty"` // Beispielzeilen, gekürzt
}

// Step ist eine Position im gefundenen Muster: welches Werkzeug, und
// ob seine Argumente über die Läufe variieren.
type Step struct {
	Nr       int      `json:"nr"`
	Tool     string   `json:"tool"`
	Varies   bool     `json:"varies"`
	Distinct int      `json:"distinct"`
	Examples []string `json:"examples,omitempty"`
}

// Report ist das Ergebnis für einen Auftrag.
type Report struct {
	Auftrag  string    `json:"auftrag"`
	Laeufe   int       `json:"laeufe"`   // ausgewertete Läufe
	Findings []Finding `json:"findings"` // nummeriert in der Ausgabe
}

// minLaeufe ist die Untergrenze für ein Muster. Zwei Läufe sind kein
// Muster, sondern ein Zufall zu zweit — und die ganze Idee ist, dass
// mehr Material die Generalisierung trägt.
const minLaeufe = 3

// Analyze rechnet über die Aufrufe und die Lauf-Zeilen eines Auftrags.
// Beide beschreiben dieselben Läufe; laeufe trägt Dauer und Kosten,
// history die Werkzeuge.
func Analyze(auftrag string, history []store.LaufTools, laeufe []store.Lauf) Report {
	r := Report{Auftrag: auftrag, Laeufe: len(history)}
	if f, ok := gangCandidate(history); ok {
		r.Findings = append(r.Findings, f)
	}
	r.Findings = append(r.Findings, permissionFriction(history)...)
	if f, ok := costAndDuration(history, laeufe); ok {
		r.Findings = append(r.Findings, f)
	}
	return r
}

// gangCandidate sucht die längste Werkzeug-Folge, die in möglichst
// vielen Läufen an einem Stück vorkommt, und prüft für jede Position,
// ob ihre Argumente variieren.
//
// Zusammenhängend und nicht als Teilfolge mit Lücken: ein Gang führt
// Schritte hintereinander aus, und was dazwischen passierte, gehört
// zum Muster oder es ist keines.
func gangCandidate(history []store.LaufTools) (Finding, bool) {
	if len(history) < minLaeufe {
		return Finding{}, false
	}
	folgen := make([][]store.ToolCall, 0, len(history))
	for _, h := range history {
		folgen = append(folgen, h.Calls)
	}

	best, bestLaeufe := []string(nil), 0
	zaehlung := map[string]map[int]bool{} // n-Gramm → Läufe, in denen es vorkommt
	for i, calls := range folgen {
		gesehen := map[string]bool{}
		for start := range calls {
			for ende := start + 1; ende <= len(calls); ende++ {
				gramm := namen(calls[start:ende])
				key := strings.Join(gramm, ">")
				if gesehen[key] {
					continue
				}
				gesehen[key] = true
				if zaehlung[key] == nil {
					zaehlung[key] = map[int]bool{}
				}
				zaehlung[key][i] = true
			}
		}
	}
	// Über sortierte Schlüssel, nicht über die Map: bei Gleichstand
	// entschiede sonst die Iterationsreihenfolge, und zwei Aufrufe
	// lieferten verschiedene Befunde. Reproduzierbar zu sein ist der
	// halbe Sinn der Übung.
	keys := make([]string, 0, len(zaehlung))
	for key := range zaehlung {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		laeufe := zaehlung[key]
		n := len(strings.Split(key, ">"))
		if n < 2 || len(laeufe) < minLaeufe {
			continue
		}
		// Mehr Läufe schlägt länger: ein Muster, das überall vorkommt,
		// trägt eine Generalisierung, ein langes in drei Läufen nicht.
		// Bei völligem Gleichstand gewinnt der alphabetisch erste — eine
		// willkürliche, aber stabile Regel.
		if len(laeufe) > bestLaeufe || (len(laeufe) == bestLaeufe && n > len(best)) {
			best, bestLaeufe = strings.Split(key, ">"), len(laeufe)
		}
	}
	if best == nil {
		return Finding{}, false
	}

	f := Finding{
		Kind:  KindGang,
		Title: fmt.Sprintf("%s — in %d von %d Läufen", strings.Join(best, " → "), bestLaeufe, len(history)),
	}
	// Argumente je Position einsammeln, aus dem ersten Vorkommen je Lauf.
	proPosition := make([]map[string]bool, len(best))
	for i := range proPosition {
		proPosition[i] = map[string]bool{}
	}
	for i, calls := range folgen {
		start, ok := findeFolge(calls, best)
		if !ok {
			continue
		}
		f.Laeufe = append(f.Laeufe, history[i].Lauf)
		for j := range best {
			proPosition[j][calls[start+j].Args] = true
		}
	}
	var variabel []string
	for j, tool := range best {
		s := Step{Nr: j + 1, Tool: tool, Distinct: len(proPosition[j])}
		s.Varies = s.Distinct > 1
		s.Examples = beispiele(proPosition[j], 3)
		if s.Varies {
			variabel = append(variabel, fmt.Sprintf("%d (%s)", s.Nr, s.Tool))
		}
		f.Steps = append(f.Steps, s)
	}
	switch len(variabel) {
	case 0:
		f.Detail = "Alle Argumente sind über die Läufe konstant — das Muster hängt an nichts, was der Lauf mitbringt. Ein Gang daraus braucht keine Parameter."
	default:
		f.Detail = fmt.Sprintf("Variabel sind die Positionen %s — das sind die Parameter. Alles andere war über die Läufe konstant und gehört als Konstante ins Skript.",
			strings.Join(variabel, ", "))
	}
	return f, true
}

// permissionFriction sammelt gescheiterte Aufrufe. Wiederkehrende
// Fehler derselben Art zeigen, wo die Räume falsch geschnitten sind —
// der Hase greift dann jedes Mal dorthin, wo er nicht darf.
func permissionFriction(history []store.LaufTools) []Finding {
	type gruppe struct {
		tool, grund string
		ziele       map[string]bool
		laeufe      map[int64]bool
		anzahl      int
	}
	gruppen := map[string]*gruppe{}
	for _, h := range history {
		for _, c := range h.Calls {
			if c.Status != "error" {
				continue
			}
			grund := kurz(c.Error, 120)
			key := c.Tool + "\x00" + grund
			g := gruppen[key]
			if g == nil {
				g = &gruppe{tool: c.Tool, grund: grund, ziele: map[string]bool{}, laeufe: map[int64]bool{}}
				gruppen[key] = g
			}
			g.anzahl++
			g.laeufe[h.Lauf] = true
			if z := ziel(c.Args); z != "" {
				g.ziele[z] = true
			}
		}
	}

	var out []Finding
	for _, g := range gruppen {
		f := Finding{
			Kind:   KindPermission,
			Title:  fmt.Sprintf("%s scheitert wiederholt (%dx in %d Läufen)", g.tool, g.anzahl, len(g.laeufe)),
			Detail: g.grund,
			Laeufe: sortiert(g.laeufe),
		}
		if ziele := beispiele(g.ziele, 5); len(ziele) > 0 {
			f.Samples = ziele
		}
		out = append(out, f)
	}
	// Häufigstes zuerst; bei Gleichstand alphabetisch, damit die
	// Ausgabe zwischen zwei Aufrufen dieselbe bleibt.
	sort.Slice(out, func(i, j int) bool {
		if len(out[i].Laeufe) != len(out[j].Laeufe) {
			return len(out[i].Laeufe) > len(out[j].Laeufe)
		}
		return out[i].Title < out[j].Title
	})
	return out
}

// costAndDuration nennt Median und Ausreißer der Laufzeit und das
// Werkzeug, das die meiste Zeit frisst.
func costAndDuration(history []store.LaufTools, laeufe []store.Lauf) (Finding, bool) {
	var dauern []time.Duration
	var kosten []int64
	ids := map[int64]bool{}
	for _, h := range history {
		ids[h.Lauf] = true
	}
	var langsamster store.Lauf
	for _, l := range laeufe {
		if !ids[l.ID] || l.Ended == nil {
			continue
		}
		d := l.Ended.Sub(l.Started)
		dauern = append(dauern, d)
		kosten = append(kosten, l.CostCent)
		if langsamster.Ended == nil || d > langsamster.Ended.Sub(langsamster.Started) {
			langsamster = l
		}
	}
	if len(dauern) < 2 {
		return Finding{}, false
	}
	sort.Slice(dauern, func(i, j int) bool { return dauern[i] < dauern[j] })
	median := dauern[len(dauern)/2]

	f := Finding{
		Kind:   KindCost,
		Title:  fmt.Sprintf("Laufzeit: Median %s, längster %s", median.Round(time.Second), dauern[len(dauern)-1].Round(time.Second)),
		Laeufe: []int64{langsamster.ID},
	}
	var teile []string
	if faktor := float64(dauern[len(dauern)-1]) / float64(max(median, time.Millisecond)); faktor >= 2 {
		teile = append(teile, fmt.Sprintf("Der längste Lauf (%d) brauchte das %.1f-fache des Medians — bei Modellen ist das normal, bei Gängen wäre es ein Befund.", langsamster.ID, faktor))
	}
	if summe := summeKosten(kosten); summe > 0 {
		teile = append(teile, fmt.Sprintf("Kosten über %d Läufe: %d ct.", len(kosten), summe))
	}
	if tool, ms := teuerstesWerkzeug(history); tool != "" {
		teile = append(teile, fmt.Sprintf("Die meiste Werkzeug-Zeit geht an %s (%s über alle Läufe).", tool, (time.Duration(ms)*time.Millisecond).Round(time.Millisecond)))
	}
	f.Detail = strings.Join(teile, " ")
	if f.Detail == "" {
		f.Detail = "Keine Ausreißer."
	}
	return f, true
}

func teuerstesWerkzeug(history []store.LaufTools) (string, int64) {
	summe := map[string]int64{}
	for _, h := range history {
		for _, c := range h.Calls {
			summe[c.Tool] += c.DurationMs
		}
	}
	var bestTool string
	var bestMs int64
	for tool, ms := range summe {
		if ms > bestMs || (ms == bestMs && tool < bestTool) {
			bestTool, bestMs = tool, ms
		}
	}
	if bestMs == 0 {
		return "", 0
	}
	return bestTool, bestMs
}

// ziel zieht den Pfad aus den Argumenten eines Aufrufs. Die Schlüssel
// unterscheiden sich je Werkzeug; unbekannte Formen liefern "" statt
// zu raten.
func ziel(args string) string {
	var m map[string]any
	if err := json.Unmarshal([]byte(args), &m); err != nil {
		return ""
	}
	for _, k := range []string{"path", "filePath", "file", "pattern", "target"} {
		if v, ok := m[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

func namen(calls []store.ToolCall) []string {
	out := make([]string, 0, len(calls))
	for _, c := range calls {
		out = append(out, c.Tool)
	}
	return out
}

// findeFolge liefert den Index des ersten Vorkommens von muster.
func findeFolge(calls []store.ToolCall, muster []string) (int, bool) {
	for i := 0; i+len(muster) <= len(calls); i++ {
		passt := true
		for j, tool := range muster {
			if calls[i+j].Tool != tool {
				passt = false
				break
			}
		}
		if passt {
			return i, true
		}
	}
	return 0, false
}

func beispiele(menge map[string]bool, n int) []string {
	var out []string
	for k := range menge {
		if k == "" {
			continue
		}
		out = append(out, kurz(k, 100))
	}
	sort.Strings(out)
	if len(out) > n {
		out = out[:n]
	}
	return out
}

func sortiert(menge map[int64]bool) []int64 {
	var out []int64
	for k := range menge {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] > out[j] })
	return out
}

func kurz(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

func summeKosten(kosten []int64) int64 {
	var s int64
	for _, k := range kosten {
		s += k
	}
	return s
}

func max(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}
