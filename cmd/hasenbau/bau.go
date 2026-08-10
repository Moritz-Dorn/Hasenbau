// bau.go: `hasenbau describe bau` — die Diagnose (Hasenbau-ha0.6).
//
// Zwei verschiedene Fragen, deshalb zwei Befehle. Hier: **ist dieser
// Bau in Ordnung?** Bei `status`: was liegt hier und was ist passiert.
// Der Unterschied ist nicht kosmetisch — eine Diagnose darf Fehler
// melden und tut das laut, ein Dashboard soll nur zeigen.
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/Moritz-Dorn/Hasenbau/internal/auftrag"
	"github.com/Moritz-Dorn/Hasenbau/internal/bau"
	"github.com/Moritz-Dorn/Hasenbau/internal/hase"
	"github.com/Moritz-Dorn/Hasenbau/internal/store"
)

func describeBau(root string, args []string, out, errw io.Writer) int {
	if len(args) != 0 {
		fmt.Fprintln(errw, "Aufruf: hasenbau describe bau")
		return 2
	}

	checks := bau.Diagnose(root)
	checks = append(checks, checkAgenten(root), checkWorkReste(root, laufendeLaeufe(root)))

	a := newSection(out)
	a.field("Bau", "%s", root)
	a.done()
	fmt.Fprintln(out)

	w := newSection(out)
	var offen int
	for _, c := range checks {
		zeichen := "ok  "
		if !c.OK {
			zeichen = "PRÜFEN"
			offen++
		}
		w.field(zeichen, "%-14s %s", c.Name, c.Detail)
		if c.Hint != "" {
			w.field("", "%-14s → %s", "", c.Hint)
		}
	}
	w.done()

	fmt.Fprintln(out)
	if offen == 0 {
		fmt.Fprintln(out, "Nichts zu tun.")
		return 0
	}
	fmt.Fprintf(out, "%d Punkt(e) zum Nachsehen. Was hier steht, merkt man sonst erst\n"+
		"an einem Lauf, der komisch aussieht.\n", offen)
	// Kein Exit-Code ≠ 0: die Diagnose ist eine Auskunft, kein Test.
	// Ein Bau ohne Baumeister-Eintrag ist nicht kaputt, er kann nur
	// eines nicht.
	return 0
}

// checkAgenten prüft, ob zu jedem Auftrag der generierte Agent liegt.
// Fehlt einer, promptet der Runner gegen einen Namen, den opencode
// nicht kennt — das fällt erst im Lauf auf.
func checkAgenten(root string) bau.Check {
	auftraege, err := auftrag.Load(root)
	if err != nil {
		return bau.Check{Name: "Definitionen", Detail: kurzeZeile(err.Error()),
			Hint: "solange die nicht laden, geht kein Lauf und kein `get`"}
	}
	if len(auftraege) == 0 {
		return bau.Check{Name: "Agenten", OK: true, Detail: "keine Aufträge",
			Hint: "`hasenbau new auftrag <name> -hase <hase>` legt einen an"}
	}
	var fehlt []string
	for _, a := range auftraege {
		pfad := filepath.Join(root, ".opencode-home", "opencode", "agents", hase.AgentName(a)+".md")
		if _, err := os.Stat(pfad); err != nil {
			fehlt = append(fehlt, hase.AgentName(a))
		}
	}
	if len(fehlt) > 0 {
		sort.Strings(fehlt)
		return bau.Check{Name: "Agenten", Detail: "nicht generiert: " + strings.Join(fehlt, ", "),
			Hint: "der nächste Daemon- oder Lauf-Start schreibt sie"}
	}
	return bau.Check{Name: "Agenten", OK: true, Detail: fmt.Sprintf("%d generiert", len(auftraege))}
}

// laufendeLaeufe sammelt die IDs der Läufe, die gerade arbeiten. Ihre
// $WORK-Verzeichnisse sind kein Nachlass, sondern in Benutzung.
func laufendeLaeufe(root string) map[string]bool {
	aktiv := map[string]bool{}
	st, err := store.Open(dbPath(root))
	if err != nil {
		return aktiv
	}
	defer st.Close()
	laeufe, err := st.RecentLaeufe(100)
	if err != nil {
		return aktiv
	}
	for _, l := range laeufe {
		if l.Status == "running" {
			aktiv[strconv.FormatInt(l.ID, 10)] = true
		}
	}
	return aktiv
}

// laufIDAusWork liest die Lauf-ID aus einem $WORK-Namen der Form
// <zeitstempel>-<id> (runner.Execute baut ihn so).
func laufIDAusWork(name string) string {
	if i := strings.LastIndex(name, "-"); i >= 0 {
		return name[i+1:]
	}
	return ""
}

// checkWorkReste findet liegengebliebene $WORK-Verzeichnisse. Der
// Runner räumt sie nach einem geglückten Lauf weg und lässt sie nach
// einem gescheiterten absichtlich stehen (§6) — sie sind also kein
// Fehler, sondern Nachlass, den irgendwann jemand ansehen sollte. Was
// zu einem laufenden Lauf gehört, ist keiner.
func checkWorkReste(root string, laufend map[string]bool) bau.Check {
	auftraege, err := auftrag.Load(root)
	if err != nil {
		return bau.Check{Name: "$WORK-Reste", OK: true, Detail: "nicht prüfbar (Definitionen laden nicht)"}
	}
	raeume := map[string]bool{}
	for _, a := range auftraege {
		if w, ok := a.Raeume["work"]; ok {
			raeume[strings.TrimSuffix(w, "/")] = true
		}
	}
	var reste []string
	for r := range raeume {
		eintraege, err := os.ReadDir(filepath.Join(root, r))
		if err != nil {
			continue
		}
		for _, e := range eintraege {
			if e.IsDir() && !laufend[laufIDAusWork(e.Name())] {
				reste = append(reste, filepath.Join(r, e.Name()))
			}
		}
	}
	if len(reste) == 0 {
		return bau.Check{Name: "$WORK-Reste", OK: true, Detail: "keine"}
	}
	sort.Strings(reste)
	zeige := reste
	if len(zeige) > 3 {
		zeige = zeige[:3]
	}
	detail := fmt.Sprintf("%d Verzeichnis(se): %s", len(reste), strings.Join(zeige, ", "))
	if len(zeige) < len(reste) {
		detail += ", …"
	}
	return bau.Check{Name: "$WORK-Reste", Detail: detail,
		Hint: "Nachlass gescheiterter Läufe — ansehen, dann löschen (`hasenbau get laeufe` sagt, welche)"}
}

func kurzeZeile(s string) string {
	s = strings.TrimSpace(strings.SplitN(s, "\n", 2)[0])
	if len(s) > 100 {
		return s[:100] + "…"
	}
	return s
}
