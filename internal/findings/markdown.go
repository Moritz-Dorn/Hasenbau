package findings

import (
	"fmt"
	"strings"
)

// Markdown rendert den Report als nummerierte Liste — die
// Gesprächsgrundlage für den Menschen und zugleich der Kontext, den ein
// Baumeister-Lauf bekommt, wenn jemand einen Befund ausarbeiten lässt.
//
// Nummeriert, weil die Nummer der Griff ist: „arbeite 2 aus" ist der
// nächste Satz, den jemand sagen will.
func (r Report) Markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Befunde zu %s\n\n", r.Auftrag)
	if r.Laeufe == 0 {
		fmt.Fprintf(&b, "Keine ausgewerteten Läufe. Ein Lauf zählt erst, wenn seine\n"+
			"Werkzeuge aufgezeichnet sind — bei Altläufen holt `hasenbau dig <id>`\n"+
			"den Trace nach, danach zieht der nächste Start die Aufrufe mit.\n")
		return b.String()
	}
	if r.Laeufe == 1 {
		b.WriteString("Grundlage: ein einziger ausgewerteter Lauf — daraus ist nicht\n" +
			"entscheidbar, was Parameter und was Konstante war (PLAN.md §8).\n\n")
	} else {
		fmt.Fprintf(&b, "Grundlage: %d ausgewertete Läufe.\n\n", r.Laeufe)
	}
	if len(r.Findings) == 0 {
		fmt.Fprintf(&b, "Nichts gefunden. Das ist ein Ergebnis: kein wiederkehrendes\n"+
			"Muster, keine Reibung, keine Ausreißer.\n")
		return b.String()
	}

	for i, f := range r.Findings {
		b.WriteString(f.Markdown(i + 1))
		b.WriteString("\n")
	}
	b.WriteString("Nichts davon ist beschlossen. Wer einen Befund ausarbeiten lassen\n" +
		"will, setzt den Baumeister auf einen der genannten Läufe an; er\n" +
		"schreibt einen Entwurf, eingetragen wird er von Hand (PLAN.md §8/§10).\n")
	return b.String()
}

// Markdown rendert einen einzelnen Befund unter seiner Nummer. Eigene
// Methode, weil der Baumeister genau einen bekommt (Hasenbau-4cx.4) —
// ohne die Liste drumherum und ohne den Schlusssatz, der an den
// Menschen gerichtet ist.
func (f Finding) Markdown(nr int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## %d. %s\n\n%s\n", nr, f.Title, kopfzeile(f))
	if f.Detail != "" {
		fmt.Fprintf(&b, "\n%s\n", f.Detail)
	}
	if len(f.Steps) > 0 {
		b.WriteString("\n")
		for _, s := range f.Steps {
			art := "konstant"
			if s.Varies {
				art = "VARIIERT"
			}
			fmt.Fprintf(&b, "%d. `%s` — %s (%d verschiedene Argumente)\n", s.Nr, s.Tool, art, s.Distinct)
			for _, e := range s.Examples {
				fmt.Fprintf(&b, "   - `%s`\n", e)
			}
		}
	}
	if len(f.Samples) > 0 && len(f.Steps) == 0 {
		b.WriteString("\n")
		for _, s := range f.Samples {
			fmt.Fprintf(&b, "- `%s`\n", s)
		}
	}
	return b.String()
}

// kopfzeile nennt die Läufe, auf denen ein Befund beruht — ohne die
// wäre er eine Behauptung.
func kopfzeile(f Finding) string {
	if len(f.Laeufe) == 0 {
		return "_ohne Lauf-Bezug_"
	}
	var ids []string
	for _, l := range f.Laeufe {
		ids = append(ids, fmt.Sprintf("%d", l))
	}
	if len(ids) > 8 {
		ids = append(ids[:8], "…")
	}
	return "Läufe: " + strings.Join(ids, ", ")
}
