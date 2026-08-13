// get.go: das lesende Verb der CLI (Hasenbau-ha0). `hasenbau get
// <ressource>` zeigt, was der Bau kennt — angelehnt an kubectl, damit
// die Befehle einem Schema folgen statt einzeln zu wachsen. Die Verben
// zum Auslösen (`lauf`, `baumeister`, `daemon`) bleiben davon unberührt.
package main

import (
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/Moritz-Dorn/Hasenbau/internal/auftrag"
	"github.com/Moritz-Dorn/Hasenbau/internal/bau"
	"github.com/Moritz-Dorn/Hasenbau/internal/hase"
	"github.com/Moritz-Dorn/Hasenbau/internal/provider"
	"github.com/Moritz-Dorn/Hasenbau/internal/store"
)

const getUsage = `Aufruf: hasenbau get <ressource>

Ressourcen:
  auftraege       die Aufträge des Baus
  hasen           die Hasen-Templates
  gaenge          die Gang-Skripte und wer sie benutzt
  tools           die Schmied-Werkzeuge und wer sie rufen darf
  laeufe [-n N]   die letzten Läufe
  lauf <id>       ein Lauf (Details: hasenbau describe lauf <id>)
  provider        Provider der Bau-Config: Endpoint, Modelle, Schlüssel
`

func cmdGet(root string, args []string, out, errw io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(errw, getUsage)
		return 2
	}
	switch args[0] {
	case "provider", "providers":
		return getProvider(root, args[1:], out, errw)
	case "laeufe":
		return getLaeufe(root, args[1:], out, errw)
	case "lauf":
		return getLauf(root, args[1:], out, errw)
	case "auftraege", "auftrag":
		return getAuftraege(root, args[1:], out, errw)
	case "hasen", "hase":
		return getHasen(root, args[1:], out, errw)
	case "gaenge", "gang":
		return getGaenge(root, args[1:], out, errw)
	case "tools", "tool":
		return getTools(root, args[1:], out, errw)
	default:
		fmt.Fprintf(errw, "hasenbau get: unbekannte Ressource %q\n\n%s", args[0], getUsage)
		return 2
	}
}

func getLaeufe(root string, args []string, out, errw io.Writer) int {
	fs := flag.NewFlagSet("get laeufe", flag.ContinueOnError)
	fs.SetOutput(errw)
	n := fs.Int("n", 20, "Anzahl Läufe")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(errw, "Aufruf: hasenbau get laeufe [-n N]")
		return 2
	}

	st, err := store.Open(dbPath(root))
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	defer st.Close()

	laeufe, err := st.RecentLaeufe(*n)
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	if len(laeufe) == 0 {
		fmt.Fprintln(out, "keine Läufe")
		return 0
	}
	writeLaufTable(out, laeufe)
	return 0
}

func getLauf(root string, args []string, out, errw io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(errw, "Aufruf: hasenbau get lauf <id>")
		return 2
	}
	st, l, code := openLauf(root, args[0], errw)
	if st != nil {
		defer st.Close()
	}
	if code != 0 {
		return code
	}
	writeLaufTable(out, []store.Lauf{*l})
	return 0
}

// openLauf öffnet die DB und löst eine Lauf-ID auf — der gemeinsame
// Vorspann von `get lauf` und `describe lauf`.
func openLauf(root, arg string, errw io.Writer) (*store.Store, *store.Lauf, int) {
	id, err := strconv.ParseInt(arg, 10, 64)
	if err != nil {
		fmt.Fprintf(errw, "hasenbau: ungültige Lauf-ID %q\n", arg)
		return nil, nil, 2
	}
	st, err := store.Open(dbPath(root))
	if err != nil {
		fmt.Fprintln(errw, err)
		return nil, nil, 1
	}
	l, err := st.LaufByID(id)
	if err != nil {
		fmt.Fprintf(errw, "hasenbau: %v\n", err)
		return st, nil, 1
	}
	return st, l, 0
}

func writeLaufTable(out io.Writer, laeufe []store.Lauf) {
	w := tabwriter.NewWriter(out, 2, 4, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tAUFTRAG\tTRIGGER\tSTATUS\tGESTARTET\tDAUER\tKOSTEN\tSUMMARY")
	for _, l := range laeufe {
		summary := l.Summary
		if l.Status == "fehler" && l.Error != "" {
			summary = l.Error
		}
		fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			l.ID, l.Auftrag, l.Trigger, l.Status,
			l.Started.Local().Format("2006-01-02 15:04"),
			laufDuration(l), cost(l.CostCent), truncate(oneLine(summary), 70))
	}
	w.Flush()
}

func laufDuration(l store.Lauf) string {
	if l.Ended == nil {
		return "läuft"
	}
	return l.Ended.Sub(l.Started).Round(time.Second).String()
}

// kosten zeigt kosten_cent so, wie es gemeint ist: Cent, und bei einem
// lokalen Modell schlicht nichts.
func cost(cent int64) string {
	if cent == 0 {
		return "—"
	}
	return strconv.FormatInt(cent, 10) + " ct"
}

// oneLine hält eine Tabellenzelle oneLine — die Summary hält der
// Store zwar auf einer Zeile (§5), der Fehlertext eines Laufs nicht.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// truncate hält eine Tabelle lesbar. Eine Summary darf 500 Zeichen lang
// sein (§5), und wenn sie aus dem Fallback stammt, ist sie es auch —
// vollständig steht sie in `describe lauf`.
func truncate(s string, max int) string {
	runen := []rune(s)
	if len(runen) <= max {
		return s
	}
	return strings.TrimRight(string(runen[:max]), " ") + "…"
}

// getProvider beantwortet die Frage, die `provider fetch <id>` stellt,
// aber nirgends beantwortet: welche IDs kennt dieser Bau, und welche
// davon lassen sich überhaupt holen?
func getProvider(root string, args []string, out, errw io.Writer) int {
	if len(args) != 0 {
		fmt.Fprintln(errw, "Aufruf: hasenbau get provider")
		return 2
	}
	conf, err := provider.LoadConfig(root)
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	eintraege := conf.List()
	if len(eintraege) == 0 {
		fmt.Fprintf(out, "keine Provider in %s\n", conf.Pfad)
		fmt.Fprintln(out, "Das Gerüst (npm, options.baseURL) gehört handgepflegt in den provider:-Block (PLAN.md §3).")
		return 0
	}

	// Der Schlüssel selbst bleibt, wo er ist — hier zählt nur, ob es
	// einen gibt.
	schluessel, err := provider.KeyIDs()
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}

	w := tabwriter.NewWriter(out, 2, 4, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tENDPOINT\tMODELLE\tAKTIV\tSCHLÜSSEL")
	var ohneEndpoint, ohneSchluessel bool
	for _, e := range eintraege {
		endpoint := e.BaseURL
		if endpoint == "" {
			endpoint = "—"
			ohneEndpoint = true
		}
		if !schluessel[e.ID] {
			ohneSchluessel = true
		}
		fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\n",
			e.ID, endpoint, e.Modelle, yesNo(e.Aktiv), yesNo(schluessel[e.ID]))
	}
	w.Flush()

	fmt.Fprintf(out, "\n%s\n", conf.Pfad)
	fmt.Fprintln(out, "Holen lässt sich, was Endpoint und Schlüssel hat: hasenbau provider fetch <id>")
	if ohneEndpoint {
		fmt.Fprintln(out, "Ohne Endpoint: eingebauter Provider (seine Modelle kennt opencode aus models.dev)")
		fmt.Fprintln(out, "oder das Gerüst im provider:-Block ist unvollständig.")
	}
	if ohneSchluessel {
		fmt.Fprintf(out, "Ohne Schlüssel: in %s anmelden (`opencode auth login`) — die Datei teilen\n", provider.AuthPath())
		fmt.Fprintln(out, "sich Bau und Alltags-opencode (PLAN.md §3).")
	}
	return 0
}

func yesNo(b bool) string {
	if b {
		return "ja"
	}
	return "nein"
}

func getAuftraege(root string, args []string, out, errw io.Writer) int {
	if len(args) != 0 {
		fmt.Fprintln(errw, "Aufruf: hasenbau get auftraege")
		return 2
	}
	auftraege, err := loadDefinitions(root)
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	if len(auftraege) == 0 {
		fmt.Fprintln(out, "keine Aufträge unter auftraege/")
		return 0
	}

	st, err := store.Open(dbPath(root))
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	defer st.Close()
	states, err := st.AuftragStates()
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	stand := map[string]store.AuftragState{}
	for _, s := range states {
		stand[s.Auftrag] = s
	}

	w := tabwriter.NewWriter(out, 2, 4, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tTRIGGER\tHASE\tGÄNGE\tRÄUME\tLETZTER LAUF\tFEHLERSERIE")
	for _, a := range auftraege {
		s := stand[a.Name]
		letzter := "—"
		if s.LastLauf != nil {
			letzter = s.LastLauf.Local().Format("2006-01-02 15:04")
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%d\t%s\t%d\n",
			a.Name, triggerShort(a), a.Hase,
			len(a.Gaenge), len(a.Raeume), letzter, s.ErrorStreak)
	}
	w.Flush()
	return 0
}

// triggerShort fasst den Trigger für eine Tabellenzelle zusammen.
// Bei watch steht dort der effektive Glob, nicht das Muster allein:
// wer die Übersicht liest, will wissen, WO beobachtet wird, und der
// Raum steht nicht in derselben Zeile (Hasenbau-d6d).
func triggerShort(a *auftrag.Auftrag) string {
	switch a.Trigger.Kind() {
	case auftrag.TriggerCron:
		return "cron " + a.Trigger.Cron
	case auftrag.TriggerManual:
		return "manual"
	default:
		return "watch " + a.WatchGlob()
	}
}

func getHasen(root string, args []string, out, errw io.Writer) int {
	if len(args) != 0 {
		fmt.Fprintln(errw, "Aufruf: hasenbau get hasen")
		return 2
	}
	namen, err := hasenNames(root)
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	if len(namen) == 0 {
		fmt.Fprintln(out, "keine Hasen unter hasen/")
		return 0
	}
	// Ohne die Aufträge fehlt die Spalte, die den Hasen erst einordnet —
	// aber ein kaputter Auftrag soll `get hasen` nicht unbrauchbar
	// machen. Deshalb weiter mit leerer Liste und einem Hinweis.
	auftraege, ladefehler := loadDefinitions(root)

	w := tabwriter.NewWriter(out, 2, 4, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tMODELL\tTEMPERATUR\tDENIES\tBENUTZT VON")
	for _, name := range namen {
		t, err := hase.Lade(root, name)
		if err != nil {
			fmt.Fprintf(w, "%s\tKAPUTT: %s\t\t\t\n", name, oneLine(err.Error()))
			continue
		}
		modell := t.Model
		if modell == "" {
			modell = "— (opencode-Default)"
		}
		temp := "—"
		if t.Temperature != nil {
			temp = fmt.Sprintf("%g", *t.Temperature)
		}
		var von []string
		for _, a := range users(auftraege, name) {
			von = append(von, a.Name)
		}
		benutzt := strings.Join(von, ", ")
		if benutzt == "" {
			benutzt = "—"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\n", name, modell, temp, len(t.Denies), benutzt)
	}
	w.Flush()
	if ladefehler != nil {
		fmt.Fprintf(out, "\nSpalte BENUTZT VON unvollständig: %v\n", ladefehler)
	}
	return 0
}

func getGaenge(root string, args []string, out, errw io.Writer) int {
	if len(args) != 0 {
		fmt.Fprintln(errw, "Aufruf: hasenbau get gaenge")
		return 2
	}
	auftraege, ladefehler := loadDefinitions(root)
	gaenge, err := collectGaenge(root, auftraege)
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	if len(gaenge) == 0 {
		fmt.Fprintln(out, "keine Gänge unter gaenge/")
		return 0
	}

	w := tabwriter.NewWriter(out, 2, 4, 2, ' ', 0)
	fmt.Fprintln(w, "DATEI\tGRÖSSE\tBENUTZT VON")
	for _, g := range gaenge {
		var von []string
		for _, b := range g.Uses {
			von = append(von, b.Auftrag+"/"+b.Gang)
		}
		benutzt := strings.Join(von, ", ")
		groesse := fmt.Sprintf("%d B", g.Size)
		switch {
		case len(g.Uses) == 0 && g.Draft:
			benutzt = "—  (Draft, nicht eingetragen)"
		case len(g.Uses) == 0:
			benutzt = "—"
		}
		if g.Size == 0 && len(g.Uses) > 0 && !exists(root, g.Path) {
			groesse = "FEHLT"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", g.Path, groesse, benutzt)
	}
	w.Flush()
	if ladefehler != nil {
		fmt.Fprintf(out, "\nSpalte BENUTZT VON unvollständig: %v\n", ladefehler)
	}
	return 0
}

// getTools zeigt die Schmied-Werkzeuge und, ebenso wichtig, WER sie
// rufen darf (Hasenbau-hcs). Die Freigabe ist zweistufig — ein Mensch
// verschiebt die Datei aus tools/entwurf/ nach tools/, und ein Auftrag
// nennt sie in seinem `tools:`. Ein Werkzeug, das die erste Stufe
// genommen hat und die zweite nicht, sieht kein Hase; ohne diese Spalte
// merkt man das erst an einem Lauf.
func getTools(root string, args []string, out, errw io.Writer) int {
	fs := flag.NewFlagSet("get tools", flag.ContinueOnError)
	fs.SetOutput(errw)
	entwuerfe := fs.Bool("entwuerfe", false, "die Review-Queue statt der freigegebenen Werkzeuge")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(errw, "Aufruf: hasenbau get tools [-entwuerfe]")
		return 2
	}
	werkzeuge, err := bau.LadeTools(root)
	if err != nil {
		fmt.Fprintln(errw, err)
		return 1
	}
	if *entwuerfe {
		return getToolEntwuerfe(werkzeuge, out)
	}
	return getToolFreigegeben(root, werkzeuge, out)
}

// getToolFreigegeben beantwortet die Betriebsfrage: was dürfen meine
// Hasen benutzen? Entwürfe stehen hier bewusst nicht — die sind eine
// Arbeitsliste für einen Menschen und keine Systemeigenschaft.
func getToolFreigegeben(root string, werkzeuge []bau.Tool, out io.Writer) int {
	auftraege, ladefehler := loadDefinitions(root)
	nutzer := map[string][]string{}
	for _, a := range auftraege {
		for _, w := range a.Tools {
			nutzer[w] = append(nutzer[w], a.Name)
		}
	}

	var offen int
	w := tabwriter.NewWriter(out, 2, 4, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tZUSTAND\tARGUMENTE\tFREIGEGEBEN FÜR")
	for _, t := range werkzeuge {
		if t.Entwurf {
			offen++
			continue
		}
		frei := strings.Join(nutzer[t.Name], ", ")
		switch {
		case !t.Einsatzbereit():
			// Ein Auftrag darf es nennen — bekommen tut es der Hase
			// trotzdem nicht. Das gehört in dieselbe Zeile, sonst sucht
			// man den Fehler im Modell.
			frei = "—  (" + t.Zustand.Erklaerung() + ")"
		case frei == "":
			frei = "—  (kein Auftrag nennt es)"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", t.Name, t.Zustand, argSpalte(t), frei)
	}
	w.Flush()
	if ladefehler != nil {
		fmt.Fprintf(out, "\nSpalte FREIGEGEBEN FÜR unvollständig: %v\n", ladefehler)
	}
	if offen > 0 {
		fmt.Fprintf(out, "\n%d Entwurf/Entwürfe warten auf Review: hasenbau get tools -entwuerfe\n", offen)
	}
	return 0
}

// getToolEntwuerfe ist die Arbeitsliste: was hat der Schmied
// geschrieben, das noch niemand gelesen hat?
func getToolEntwuerfe(werkzeuge []bau.Tool, out io.Writer) int {
	var zeilen int
	w := tabwriter.NewWriter(out, 2, 4, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tZUSTAND\tZEILEN\tGELESEN VON")
	for _, t := range werkzeuge {
		if !t.Entwurf {
			continue
		}
		zeilen++
		von := t.Review.By
		if von == "" {
			von = "—  (" + t.Zustand.Erklaerung() + ")"
		} else if t.Zustand != bau.Actual {
			von = von + "  (" + t.Zustand.Erklaerung() + ")"
		}
		fmt.Fprintf(w, "%s\t%s\t%d\t%s\n", t.Name, t.Zustand, t.Zeilen, von)
	}
	w.Flush()
	if zeilen == 0 {
		fmt.Fprintln(out, "Nichts wartet auf Review.")
		return 0
	}
	fmt.Fprintln(out, "\nEin Entwurf ist Code, den ein Modell geschrieben und niemand gelesen hat.")
	fmt.Fprintln(out, "  hasenbau tool review --next")
	return 0
}

func argSpalte(t bau.Tool) string {
	var args []string
	for _, a := range t.Args {
		if a.Pflicht {
			args = append(args, a.Name)
			continue
		}
		args = append(args, "["+a.Name+"]")
	}
	if len(args) == 0 {
		return "—"
	}
	return strings.Join(args, " ")
}
