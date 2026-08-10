package store

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func neuerStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "state", "hasenbau.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestAktiverLauf(t *testing.T) {
	s := neuerStore(t)

	if _, err := s.ActiveLauf(); !errors.Is(err, ErrNoActiveLauf) {
		t.Errorf("ohne Lauf: err = %v, erwartet ErrNoActiveLauf", err)
	}

	id, err := s.StartLauf("pdf-einlagern", "watch", "raeume/laderampe/sources/a.pdf")
	if err != nil {
		t.Fatal(err)
	}
	l, err := s.ActiveLauf()
	if err != nil {
		t.Fatal(err)
	}
	if l.ID != id || l.Auftrag != "pdf-einlagern" {
		t.Errorf("aktiver Lauf = %+v, erwartet ID %d", l, id)
	}

	// Zweiter Auftrag parallel: der Rückkanal darf jetzt nicht raten.
	zweite, err := s.StartLauf("tagesbericht", "cron", "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.ActiveLauf()
	if !errors.Is(err, ErrAmbiguous) {
		t.Fatalf("bei zwei Läufen: err = %v, erwartet ErrAmbiguous", err)
	}
	// Die Kandidaten stehen im Text — sonst sieht niemand die
	// verwaiste Zeile eines abgestürzten Daemons.
	if !strings.Contains(err.Error(), "pdf-einlagern") || !strings.Contains(err.Error(), "tagesbericht") {
		t.Errorf("Kandidaten fehlen in %q", err)
	}

	// Endet einer, ist der andere wieder eindeutig.
	if err := s.EndLauf(zweite, LaufResult{Status: "ok"}); err != nil {
		t.Fatal(err)
	}
	l, err = s.ActiveLauf()
	if err != nil || l.ID != id {
		t.Errorf("nach Ende des zweiten: %+v, %v", l, err)
	}
}

func TestNotizenSchreibenUndLesen(t *testing.T) {
	s := neuerStore(t)
	id, err := s.StartLauf("pdf-einlagern", "manual", "")
	if err != nil {
		t.Fatal(err)
	}

	for _, text := range []string{"Rechnung ohne Datum", "Scan war schief"} {
		if err := s.WriteNote(id, text); err != nil {
			t.Fatal(err)
		}
	}
	notizen, err := s.Notes(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(notizen) != 2 || notizen[0].Text != "Rechnung ohne Datum" || notizen[1].Text != "Scan war schief" {
		t.Fatalf("Notizen = %+v", notizen)
	}
	if notizen[0].Written.IsZero() {
		t.Error("Zeitstempel fehlt")
	}

	// Notizen hängen am Lauf, nicht am Auftrag.
	andere, err := s.StartLauf("tagesbericht", "cron", "")
	if err != nil {
		t.Fatal(err)
	}
	if n, err := s.Notes(andere); err != nil || len(n) != 0 {
		t.Errorf("fremder Lauf hat Notizen: %+v, %v", n, err)
	}
}

// Der Rückkanal gewinnt gegen den Fallback: was der Hase selbst
// gemeldet hat, überschreibt EndLauf nicht (§5, Bead-Kriterium).
func TestSummaryVomHasenSchlaegtFallback(t *testing.T) {
	s := neuerStore(t)
	id, err := s.StartLauf("pdf-einlagern", "manual", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.WriteSummary(id, "3 Rechnungen einsortiert, eine ohne Datum"); err != nil {
		t.Fatal(err)
	}
	if err := s.EndLauf(id, LaufResult{
		Status:  "ok",
		Summary: "Ich habe die Dateien verarbeitet. Sag Bescheid, wenn ich helfen kann!",
	}); err != nil {
		t.Fatal(err)
	}

	l, err := s.LaufByID(id)
	if err != nil {
		t.Fatal(err)
	}
	if l.Summary != "3 Rechnungen einsortiert, eine ohne Datum" {
		t.Errorf("Summary = %q — der Fallback hat den Rückkanal überschrieben", l.Summary)
	}
}

// Ohne summary()-Aufruf bleibt der Fallback funktionsfähig.
func TestOhneRueckkanalGreiftFallback(t *testing.T) {
	s := neuerStore(t)
	id, err := s.StartLauf("pdf-einlagern", "manual", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.EndLauf(id, LaufResult{Status: "ok", Summary: "Alles  einsortiert.\nFertig."}); err != nil {
		t.Fatal(err)
	}
	l, err := s.LaufByID(id)
	if err != nil {
		t.Fatal(err)
	}
	if l.Summary != "Alles einsortiert. Fertig." {
		t.Errorf("Summary = %q", l.Summary)
	}
}

func TestSummarySchreibePresstInEineZeile(t *testing.T) {
	s := neuerStore(t)
	id, err := s.StartLauf("pdf-einlagern", "manual", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.WriteSummary(id, "Zeile eins\n\tZeile   zwei\n"); err != nil {
		t.Fatal(err)
	}
	l, err := s.LaufByID(id)
	if err != nil {
		t.Fatal(err)
	}
	if l.Summary != "Zeile eins Zeile zwei" {
		t.Errorf("Summary = %q", l.Summary)
	}

	// Der letzte Aufruf gewinnt — der Hase darf sich korrigieren.
	if err := s.WriteSummary(id, "Doch nur zwei Dateien"); err != nil {
		t.Fatal(err)
	}
	l, _ = s.LaufByID(id)
	if l.Summary != "Doch nur zwei Dateien" {
		t.Errorf("Korrektur nicht übernommen: %q", l.Summary)
	}

	if err := s.WriteSummary(9999, "nirgendwo"); err == nil {
		t.Error("Summary auf unbekannten Lauf blieb ohne Fehler")
	}
}

func TestSummaryLineKapptAusschweifungen(t *testing.T) {
	lang := strings.Repeat("wort ", 200)
	got := SummaryLine(lang)
	if runen := []rune(got); len(runen) != 501 || !strings.HasSuffix(got, "…") {
		t.Errorf("Länge = %d, Ende = %q", len([]rune(got)), got[len(got)-3:])
	}
}
