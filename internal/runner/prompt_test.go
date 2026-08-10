package runner

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Moritz-Dorn/Hasenbau/internal/auftrag"
)

type fakeSummaries struct {
	summaries []string
	gefragt   int
}

func (f *fakeSummaries) RecentSummaries(auftrag string, n int) ([]string, error) {
	f.gefragt = n
	if n < len(f.summaries) {
		return f.summaries[len(f.summaries)-n:], nil
	}
	return f.summaries, nil
}

func TestBauePrompt(t *testing.T) {
	a := testAuftrag()
	a.Body = "Sortiere den Extrakt ein."
	a.Context = []auftrag.Context{
		{File: "$WORK/extrakt.md"},
		{LastSummaries: 2},
	}
	u := testUmgebung(t, a)
	if err := os.WriteFile(filepath.Join(u.Bau, u.Work, "extrakt.md"), []byte("# Rechnung\nBetrag: 42€\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	quelle := &fakeSummaries{summaries: []string{"alt: PDF abgelegt", "neu: Rechnung sortiert"}}
	prompt, err := BauePrompt(u, a, quelle)
	if err != nil {
		t.Fatal(err)
	}

	// Reihenfolge: Body, dann Kontext-Quellen in Auftrags-Reihenfolge.
	erwartet := []string{
		"Sortiere den Extrakt ein.",
		"## Kontext: " + filepath.Join(u.Work, "extrakt.md"),
		"Betrag: 42€",
		"## Die letzten Läufe dieses Auftrags",
		"- alt: PDF abgelegt",
		"- neu: Rechnung sortiert",
	}
	pos := -1
	for _, e := range erwartet {
		i := strings.Index(prompt, e)
		if i < 0 {
			t.Fatalf("Prompt enthält nicht %q:\n%s", e, prompt)
		}
		if i < pos {
			t.Fatalf("%q an falscher Stelle:\n%s", e, prompt)
		}
		pos = i
	}
	if quelle.gefragt != 2 {
		t.Errorf("LastSummaries mit n=%d statt 2 gefragt", quelle.gefragt)
	}
}

func TestBauePromptOhneVergangenheit(t *testing.T) {
	a := testAuftrag()
	a.Body = "Erster Lauf."
	a.Context = []auftrag.Context{{LastSummaries: 3}}
	u := testUmgebung(t, a)

	prompt, err := BauePrompt(u, a, &fakeSummaries{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(prompt, "letzten Läufe") {
		t.Errorf("leere Vergangenheit erzeugt trotzdem eine Sektion:\n%s", prompt)
	}
	if !strings.HasPrefix(prompt, "Erster Lauf.") {
		t.Errorf("Prompt = %q", prompt)
	}
}

func TestBauePromptFehlendeDatei(t *testing.T) {
	a := testAuftrag()
	a.Body = "Body."
	a.Context = []auftrag.Context{{File: "$WORK/extrakt.md"}}
	u := testUmgebung(t, a)

	_, err := BauePrompt(u, a, &fakeSummaries{})
	if err == nil || !strings.Contains(err.Error(), "extrakt.md fehlt") {
		t.Errorf("fehlende Kontext-File: %v", err)
	}
}

func TestBauePromptDateiAusserhalbDesBaus(t *testing.T) {
	a := testAuftrag()
	a.Body = "Body."
	a.Context = []auftrag.Context{{File: "../geheim.txt"}}
	u := testUmgebung(t, a)

	if _, err := BauePrompt(u, a, &fakeSummaries{}); err == nil {
		t.Error("Kontext-Datei außerhalb des Baus muss scheitern")
	}
}

type fehlerQuelle struct{}

func (fehlerQuelle) RecentSummaries(string, int) ([]string, error) {
	return nil, fmt.Errorf("db kaputt")
}

func TestBauePromptSummaryFehler(t *testing.T) {
	a := testAuftrag()
	a.Body = "Body."
	a.Context = []auftrag.Context{{LastSummaries: 1}}
	u := testUmgebung(t, a)

	if _, err := BauePrompt(u, a, fehlerQuelle{}); err == nil || !strings.Contains(err.Error(), "db kaputt") {
		t.Errorf("Summary-Fehler verschluckt: %v", err)
	}
}
