// prompt.go setzt den Prompt eines Laufs zusammen (§6, Ablauf 4):
// Auftrags-Body, dann die kontext:-Quellen in Auftrags-Reihenfolge.
// Die Summaries vergangener Läufe sind der Kern der Kontext-Schicht.
package runner

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Moritz-Dorn/Hasenbau/internal/auftrag"
	"github.com/Moritz-Dorn/Hasenbau/internal/lauf"
)

// SummaryQuelle liefert die letzten nicht-leeren Summaries eines
// Auftrags, älteste zuerst. *store.Store erfüllt das Interface.
type SummaryQuelle interface {
	RecentSummaries(auftrag string, n int) ([]string, error)
}

// BauePrompt baut den Prompt-Text. Fehlende Kontext-Dateien sind ein
// Fehler vor dem LLM-Call — nie ein stiller Lücken-Prompt.
func BauePrompt(u *lauf.Umgebung, a *auftrag.Auftrag, quelle SummaryQuelle) (string, error) {
	var b strings.Builder
	b.WriteString(a.Body)

	for i, k := range a.Context {
		if k.File != "" {
			pfad, err := u.Ersetze(k.File)
			if err != nil {
				return "", fmt.Errorf("kontext %d: %w", i+1, err)
			}
			if err := auftrag.BauRelative(pfad); err != nil {
				return "", fmt.Errorf("kontext %d: %w", i+1, err)
			}
			inhalt, err := os.ReadFile(filepath.Join(u.Bau, pfad))
			if err != nil {
				return "", fmt.Errorf("kontext %d: Datei %s fehlt — Gang nicht gelaufen? (%w)", i+1, pfad, err)
			}
			fmt.Fprintf(&b, "\n\n## Kontext: %s\n\n%s", pfad, strings.TrimRight(string(inhalt), "\n"))
			continue
		}

		summaries, err := quelle.RecentSummaries(a.Name, k.LastSummaries)
		if err != nil {
			return "", fmt.Errorf("kontext %d: %w", i+1, err)
		}
		if len(summaries) == 0 {
			continue // erster Lauf — es gibt noch nichts zu erzählen
		}
		b.WriteString("\n\n## Die letzten Läufe dieses Auftrags\n")
		for _, s := range summaries {
			fmt.Fprintf(&b, "\n- %s", s)
		}
	}

	b.WriteString("\n")
	return b.String(), nil
}
