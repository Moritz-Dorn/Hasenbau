package findings

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Moritz-Dorn/Hasenbau/internal/store"
)

// lauf baut einen ausgewerteten Lauf aus Paaren tool/args.
func lauf(id int64, paare ...string) store.LaufTools {
	lt := store.LaufTools{Lauf: id}
	for i := 0; i+1 < len(paare); i += 2 {
		lt.Calls = append(lt.Calls, store.ToolCall{
			Nr: len(lt.Calls) + 1, Tool: paare[i], Args: paare[i+1], Status: "completed",
		})
	}
	lt.Signature = store.Signature(lt.Calls)
	return lt
}

// Der Kern von Hasenbau-4cx.2 und die Antwort auf die harte Stelle aus
// PLAN.md §8: was über die Läufe variiert, ist der Parameter.
func TestGangKandidatTrenntParameterVonKonstante(t *testing.T) {
	history := []store.LaufTools{
		lauf(3, "read", `{"path":"sources/c.pdf"}`, "write", `{"path":"lager/x.md"}`),
		lauf(2, "read", `{"path":"sources/b.pdf"}`, "write", `{"path":"lager/x.md"}`),
		lauf(1, "read", `{"path":"sources/a.pdf"}`, "write", `{"path":"lager/x.md"}`),
	}

	r := Analyze("pdf-einlagern", history, nil)
	if len(r.Findings) == 0 || r.Findings[0].Kind != KindGang {
		t.Fatalf("kein Gang-Kandidat: %+v", r.Findings)
	}
	f := r.Findings[0]
	if !strings.Contains(f.Title, "in 3 von 3") {
		t.Errorf("Titel = %q", f.Title)
	}
	if len(f.Laeufe) != 3 {
		t.Errorf("Läufe = %v", f.Laeufe)
	}
	if len(f.Steps) != 2 {
		t.Fatalf("Schritte = %+v", f.Steps)
	}
	if !f.Steps[0].Varies || f.Steps[0].Distinct != 3 {
		t.Errorf("Position 1 muss variieren: %+v", f.Steps[0])
	}
	if f.Steps[1].Varies {
		t.Errorf("Position 2 muss konstant sein: %+v", f.Steps[1])
	}
	if !strings.Contains(f.Detail, "1 (read)") {
		t.Errorf("Detail nennt den Parameter nicht: %q", f.Detail)
	}
}

// Zwei Läufe sind kein Muster, sondern ein Zufall zu zweit.
func TestGangKandidatBrauchtGenugMaterial(t *testing.T) {
	history := []store.LaufTools{
		lauf(2, "read", "{}", "write", "{}"),
		lauf(1, "read", "{}", "write", "{}"),
	}
	r := Analyze("a", history, nil)
	for _, f := range r.Findings {
		if f.Kind == KindGang {
			t.Errorf("Gang-Kandidat aus 2 Läufen: %+v", f)
		}
	}
}

// Das häufigste Muster gewinnt gegen das längste: ein langes Muster in
// drei von zwanzig Läufen trägt keine Generalisierung.
func TestGangKandidatBevorzugtHaeufigkeit(t *testing.T) {
	var history []store.LaufTools
	for i := int64(1); i <= 5; i++ {
		history = append(history, lauf(i, "read", "{}", "write", "{}"))
	}
	// Drei Läufe mit einer längeren Folge, die die kurze enthält.
	for i := int64(6); i <= 8; i++ {
		history = append(history, lauf(i, "glob", "{}", "read", "{}", "write", "{}", "edit", "{}"))
	}

	r := Analyze("a", history, nil)
	f := r.Findings[0]
	if f.Kind != KindGang {
		t.Fatalf("kein Gang-Kandidat: %+v", f)
	}
	// read→write kommt in allen acht vor, glob→read→write→edit in drei.
	if !strings.Contains(f.Title, "in 8 von 8") {
		t.Errorf("Titel = %q — erwartet das Muster aus allen Läufen", f.Title)
	}
}

func TestPermissionReibungGruppiert(t *testing.T) {
	mach := func(id int64, pfad string) store.LaufTools {
		return store.LaufTools{Lauf: id, Calls: []store.ToolCall{
			{Nr: 1, Tool: "write", Args: fmt.Sprintf(`{"path":%q}`, pfad),
				Status: "error", Error: "permission denied: edit"},
		}}
	}
	history := []store.LaufTools{mach(3, "raeume/geheim/a"), mach(2, "raeume/geheim/b"), mach(1, "raeume/geheim/a")}

	r := Analyze("a", history, nil)
	var f *Finding
	for i := range r.Findings {
		if r.Findings[i].Kind == KindPermission {
			f = &r.Findings[i]
		}
	}
	if f == nil {
		t.Fatalf("keine Permission-Reibung: %+v", r.Findings)
	}
	if !strings.Contains(f.Title, "3x in 3 Läufen") {
		t.Errorf("Titel = %q", f.Title)
	}
	if !strings.Contains(f.Detail, "permission denied") {
		t.Errorf("Detail = %q", f.Detail)
	}
	// Die Zielpfade zeigen, wo der Raum falsch geschnitten ist.
	if len(f.Samples) != 2 {
		t.Errorf("Zielpfade = %v", f.Samples)
	}
	if len(f.Laeufe) != 3 {
		t.Errorf("Läufe = %v", f.Laeufe)
	}
}

func TestKostenUndDauerNenntAusreisser(t *testing.T) {
	start := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	mach := func(id int64, d time.Duration, ct int64) store.Lauf {
		ende := start.Add(d)
		return store.Lauf{ID: id, Started: start, Ended: &ende, CostCent: ct}
	}
	history := []store.LaufTools{
		{Lauf: 3, Calls: []store.ToolCall{{Tool: "read", DurationMs: 900}}},
		{Lauf: 2, Calls: []store.ToolCall{{Tool: "read", DurationMs: 100}}},
		{Lauf: 1, Calls: []store.ToolCall{{Tool: "write", DurationMs: 50}}},
	}
	laeufe := []store.Lauf{mach(3, 30*time.Minute, 5), mach(2, time.Minute, 3), mach(1, time.Minute, 2)}

	r := Analyze("a", history, laeufe)
	var f *Finding
	for i := range r.Findings {
		if r.Findings[i].Kind == KindCost {
			f = &r.Findings[i]
		}
	}
	if f == nil {
		t.Fatal("kein Kosten-Befund")
	}
	if !strings.Contains(f.Title, "Median 1m0s") || !strings.Contains(f.Title, "30m0s") {
		t.Errorf("Titel = %q", f.Title)
	}
	if !strings.Contains(f.Detail, "10 ct") {
		t.Errorf("Kosten fehlen: %q", f.Detail)
	}
	if !strings.Contains(f.Detail, "read") {
		t.Errorf("teuerstes Werkzeug fehlt: %q", f.Detail)
	}
	if len(f.Laeufe) != 1 || f.Laeufe[0] != 3 {
		t.Errorf("der längste Lauf gehört dazu: %v", f.Laeufe)
	}
}

// Jeder Befund nennt die Läufe, auf denen er beruht — sonst ist er
// eine Behauptung (Akzeptanzkriterium).
func TestMarkdownNummeriertUndBelegt(t *testing.T) {
	history := []store.LaufTools{
		lauf(3, "read", `{"path":"a"}`, "write", `{"path":"z"}`),
		lauf(2, "read", `{"path":"b"}`, "write", `{"path":"z"}`),
		lauf(1, "read", `{"path":"c"}`, "write", `{"path":"z"}`),
	}
	md := Analyze("pdf-einlagern", history, nil).Markdown()

	for _, muss := range []string{
		"# Befunde zu pdf-einlagern", "Grundlage: 3 ausgewertete Läufe",
		"## 1. ", "Läufe: 3, 2, 1", "VARIIERT", "konstant",
	} {
		if !strings.Contains(md, muss) {
			t.Errorf("Markdown ohne %q:\n%s", muss, md)
		}
	}
	if !strings.Contains(md, "eingetragen wird er von Hand") {
		t.Errorf("die Garantie aus §8/§10 fehlt:\n%s", md)
	}
}

func TestMarkdownOhneMaterial(t *testing.T) {
	md := Analyze("a", nil, nil).Markdown()
	if !strings.Contains(md, "Keine ausgewerteten Läufe") || !strings.Contains(md, "hasenbau dig") {
		t.Errorf("ohne Material soll der Weg dahin stehen:\n%s", md)
	}

	// Ein einziger Lauf: die Grenze aus §8 gehört dazugesagt.
	md = Analyze("a", []store.LaufTools{lauf(1, "read", "{}")}, nil).Markdown()
	if !strings.Contains(md, "ein einziger") {
		t.Errorf("bei einem Lauf fehlt der Vorbehalt:\n%s", md)
	}
}

func TestZielAusArgumenten(t *testing.T) {
	for _, f := range []struct{ args, will string }{
		{`{"path":"a/b.md"}`, "a/b.md"},
		{`{"filePath":"/tmp/x"}`, "/tmp/x"},
		{`{"pattern":"*.pdf"}`, "*.pdf"},
		{`{"irgendwas":1}`, ""},
		{`kein json`, ""},
	} {
		if got := ziel(f.args); got != f.will {
			t.Errorf("ziel(%q) = %q, erwartet %q", f.args, got, f.will)
		}
	}
}
