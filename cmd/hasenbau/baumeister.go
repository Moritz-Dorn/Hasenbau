// baumeister.go: `hasenbau baumeister <ziel>` setzt den Baumeister auf
// einen Lauf an (PLAN.md §8, Phase 2).
//
// Der Befehl ist bewusst dünn. Der Baumeister ist kein Sonderpfad im
// Code, sondern ein ganz normaler Auftrag mit einem ganz normalen
// Hasen — sein Material sind nur keine PDFs, sondern Läufe, und seine
// deterministische Vorverarbeitung ist ein Gang wie jeder andere. Was
// hier passiert: den Auftrag aus hasenbau.yaml holen, das Ziel zu einer
// Lauf-ID auflösen, den Lauf fahren und hinterher sagen, was in den
// out-Raum geschrieben wurde.
//
// Was hier NICHT passiert: aktivieren. Der Baumeister hat auf
// auftraege/ kein Schreibrecht (die Permissions des generierten Agenten
// kommen aus den Räumen, §6), und dieser Befehl trägt nichts ein.
// PLAN.md §8/§10: ein gegrabener Gang wird nie automatisch scharf
// geschaltet.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Moritz-Dorn/Hasenbau/internal/auftrag"
	"github.com/Moritz-Dorn/Hasenbau/internal/bau"
	"github.com/Moritz-Dorn/Hasenbau/internal/store"
)

func cmdBaumeister(root string, args []string, out, errw io.Writer) int {
	fs := flag.NewFlagSet("baumeister", flag.ContinueOnError)
	fs.SetOutput(errw)
	// Stufe 2 (Hasenbau-4cx.4): nicht ein Trace, sondern ein gerechneter
	// Befund über N Läufe. Die Nummer kommt aus `hasenbau findings`.
	befund := fs.Int("finding", 0, "Nummer eines Befunds aus `hasenbau findings <auftrag>`")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(errw, "Usage: hasenbau baumeister [-finding N] <lauf-id|auftrag>")
		return 2
	}
	args = fs.Args()
	logger := log.New(errw, "", log.LstdFlags)

	conf, err := bau.LoadConfig(root)
	if err != nil {
		logger.Print(err)
		return 1
	}
	if conf.Baumeister == "" {
		fmt.Fprintf(errw, "hasenbau baumeister: %s names no baumeister:\n"+
			"  copy the templates (beispiele/auftraege/baumeister.md, beispiele/hasen/baumeister.md),\n"+
			"  then set `baumeister: <auftrag>` in %s.\n", bau.ConfigFile, bau.ConfigFile)
		return 1
	}

	k, err := openLaufContext(root, logger)
	if err != nil {
		logger.Print(err)
		return 1
	}
	defer k.Close()

	ziel, err := k.Auftrag(conf.Baumeister)
	if err != nil {
		fmt.Fprintf(errw, "hasenbau baumeister: %s nennt %q — %v\n", bau.ConfigFile, conf.Baumeister, err)
		return 1
	}
	raum := ziel.Raeume["out"]
	if raum == "" {
		fmt.Fprintf(errw, "hasenbau baumeister: Auftrag %s needs a Raum with role out —\n"+
			"  that is the only source of the Baumeister's write right (PLAN.md §6).\n", ziel.Name)
		return 1
	}

	// Vor dem Server auflösen: ein Tippfehler soll kein opencode starten.
	material, err := resolveMaterial(k.Store, args[0], *befund)
	if err != nil {
		fmt.Fprintf(errw, "hasenbau baumeister: %v\n", err)
		return 1
	}

	vorher, err := draftSnapshot(root, raum)
	if err != nil {
		logger.Print(err)
		return 1
	}

	fmt.Fprintf(out, "Baumeister %s auf %s\n", ziel.Name, material.Kopf)
	if err := k.StartServer(); err != nil {
		logger.Print(err)
		return 1
	}

	// Der Runner loggt den Fehlschlag mit Grund, und reportLauf sagt
	// unten, was in der DB steht — hier nichts nachdrucken (Hasenbau-vwr).
	laufID, laufFehler := k.Runner.Execute(k.Ctx, ziel, "manual", material.Input)

	// Bericht auch nach einem Fehlschlag: der Hase kann geschrieben
	// haben, bevor etwas anderes schiefging.
	nachher, err := draftSnapshot(root, raum)
	if err != nil {
		logger.Print(err)
		return 1
	}
	reportLauf(out, k.Store, laufID)
	neu := reportDrafts(out, raum, vorher, nachher)
	for _, meldung := range checkDrafts(root, neu) {
		fmt.Fprintf(out, "  %s\n", meldung)
	}
	if len(neu) > 0 {
		fmt.Fprintf(out, "\nNOT ACTIVATED — Hasenbau never enters anything into an Auftrag itself\n"+
			"(PLAN.md §8/§10). Read the script, then register the Gang yourself; the\n"+
			"proposal is in the head of the file.\n")
	}
	fmt.Fprintf(out, "\nThe material it worked from: `hasenbau dig %s`.\n", material.Input)

	if laufFehler != nil {
		return 1
	}
	return 0
}

// reportLauf sagt, was in laeufe steht — nicht, was hier gerade
// durchgelaufen ist.
//
// Ohne das liest sich der Entwurfs-Bericht nach einem Abbruch wie ein
// Erfolg: der Fehler steht auf stderr und weiter oben, die
// freundlichen Zeilen über den out-Raum stehen darunter und kommen
// zuletzt. Genau so hat ein abgebrochener Lauf ausgesehen, der in der
// Datenbank als 'aborted' geführt wurde (Hasenbau-0f4).
func reportLauf(w io.Writer, st *store.Store, laufID int64) {
	if laufID == 0 {
		return // die Zeile kam nie zustande; der Fehler steht schon oben
	}
	l, err := st.LaufByID(laufID)
	if err != nil {
		fmt.Fprintf(w, "\nLauf %d: status not readable — %v\n", laufID, err)
		return
	}
	dauer := ""
	if l.Ended != nil {
		dauer = fmt.Sprintf(", %s", l.Ended.Sub(l.Started).Round(time.Second))
	}
	fmt.Fprintf(w, "\nLauf %d: %s%s\n", l.ID, l.Status, dauer)
	if l.Error != "" {
		fmt.Fprintf(w, "  %s\n", l.Error)
	}
	if l.Status != "ok" {
		fmt.Fprintf(w, "  What follows may therefore be incomplete.\n")
	}
}

// material beschreibt, woran der Baumeister arbeitet.
type material struct {
	Input string // Auslöser des Laufs ($TRIGGER_ARG) — eine Lauf-ID oder ein Befund-Selektor
	Kopf  string // was oben in der Ausgabe steht
}

// resolveMaterial entscheidet zwischen den beiden Stufen des
// Baumeisters (PLAN.md §8): ein einzelner Trace, oder ein gerechneter
// Befund über N Läufe.
//
// Stufe 2 ist die bessere, wo es sie gibt — aus EINEM Trace ist
// prinzipiell nicht entscheidbar, was Parameter und was Konstante war.
// Stufe 1 bleibt trotzdem: solange ein Auftrag zu wenige ausgewertete
// Läufe hat, gibt es keine Befunde, und ein Trace ist mehr als nichts.
func resolveMaterial(st *store.Store, ziel string, befund int) (material, error) {
	if befund <= 0 {
		l, err := targetLauf(st, ziel)
		if err != nil {
			return material{}, err
		}
		return material{
			Input: strconv.FormatInt(l.ID, 10),
			Kopf:  fmt.Sprintf("Lauf %d (%s, %s, %s)", l.ID, l.Auftrag, l.Trigger, l.Status),
		}, nil
	}

	sel := selector{Auftrag: ziel, Nr: befund}
	if auftrag.ValidName(ziel) != nil {
		return material{}, fmt.Errorf("-finding needs an Auftrag, %q is not one", ziel)
	}
	_, f, err := resolveFinding(st, sel, 20)
	if err != nil {
		return material{}, err
	}
	return material{
		Input: sel.String(),
		Kopf: fmt.Sprintf("finding %d of %s: %s (Läufe %s)",
			befund, ziel, f.Title, laufListe(f.Laeufe)),
	}, nil
}

func laufListe(ids []int64) string {
	var s []string
	for _, id := range ids {
		s = append(s, strconv.FormatInt(id, 10))
	}
	if len(s) == 0 {
		return "—"
	}
	return strings.Join(s, ", ")
}

// targetLauf löst das Argument auf: reine Ziffern sind eine Lauf-ID,
// alles andere ein Auftragsname (dann der jüngste Lauf mit Session).
// Zurück kommt immer ein echter Lauf — damit ist der Auslöser des
// Baumeister-Laufs eine Zahl und nie ein Pfad oder Shell-Syntax.
func targetLauf(st *store.Store, ziel string) (*store.Lauf, error) {
	var l *store.Lauf
	var err error
	if id, e := strconv.ParseInt(ziel, 10, 64); e == nil {
		l, err = st.LaufByID(id)
	} else {
		l, err = st.LastLaufByAuftrag(ziel)
	}
	if err != nil {
		return nil, err
	}
	if l.SessionID == "" {
		return nil, fmt.Errorf("Lauf %d (%s, %s) has no session — there is nothing to distil", l.ID, l.Auftrag, l.Status)
	}
	return l, nil
}

// draftFile ist der Stand einer Datei im out-Raum. Größe und Hash,
// damit auch eine gleich große Änderung auffällt.
type draftFile struct {
	Size int64
	Hash string
}

// draftSnapshot fotografiert einen Raum. Ein fehlendes Verzeichnis ist
// kein Fehler — der Runner legt Räume erst beim Lauf an.
func draftSnapshot(bauRoot, raum string) (map[string]draftFile, error) {
	stand := map[string]draftFile{}
	wurzel := filepath.Join(bauRoot, raum)
	err := filepath.WalkDir(wurzel, func(pfad string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) && pfad == wurzel {
				return nil
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		inhalt, err := os.ReadFile(pfad)
		if err != nil {
			return err
		}
		summe := sha256.Sum256(inhalt)
		rel, err := filepath.Rel(bauRoot, pfad)
		if err != nil {
			return err
		}
		stand[rel] = draftFile{Size: int64(len(inhalt)), Hash: hex.EncodeToString(summe[:8])}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("hasenbau baumeister: Raum %s lesen: %w", raum, err)
	}
	return stand, nil
}

// reportDrafts stellt Vorher und Nachher gegenüber und liefert die
// neuen Dateien zurück. Geänderte zählen nicht dazu: eine bestehende
// Datei zu überschreiben ist ein Befund, kein Ergebnis.
func reportDrafts(w io.Writer, raum string, vorher, nachher map[string]draftFile) []string {
	var neu, geaendert []string
	for pfad, n := range nachher {
		v, gabs := vorher[pfad]
		switch {
		case !gabs:
			neu = append(neu, pfad)
		case v.Hash != n.Hash:
			geaendert = append(geaendert, pfad)
		}
	}
	sort.Strings(neu)
	sort.Strings(geaendert)

	fmt.Fprintf(w, "\n%s:\n", raum)
	if len(neu) == 0 && len(geaendert) == 0 {
		fmt.Fprintln(w, "  unchanged — the Baumeister wrote nothing (its summary says why)")
		return nil
	}
	for _, p := range neu {
		fmt.Fprintf(w, "  new        %s (%d bytes)\n", p, nachher[p].Size)
	}
	for _, p := range geaendert {
		fmt.Fprintf(w, "  CHANGED   %s (%d → %d bytes) — a draft was already here\n",
			p, vorher[p].Size, nachher[p].Size)
	}
	if unveraendert := len(nachher) - len(neu) - len(geaendert); unveraendert > 0 {
		fmt.Fprintf(w, "  unchanged: %d file(s)\n", unveraendert)
	}
	return neu
}

// checkDrafts lässt die Syntax neuer Skripte prüfen. Der Baumeister
// kann das nicht selbst — er hat kein bash (§6, und Hasen-Templates
// dürfen Rechte nur verengen), sein Draft ist also konstruktions-
// bedingt ungetestet. Parsen ist kein Ausführen: weder `ast.parse` noch
// `sh -n` führt eine Zeile des Skripts aus.
//
// ast.parse und nicht py_compile: das legt ein __pycache__/ neben den
// Draft, und der Bau ist ein Git-Repo — im Diff soll der Draft
// stehen, nicht sein Bytecode.
func checkDrafts(bauRoot string, dateien []string) []string {
	const pySyntax = `import ast,sys; ast.parse(open(sys.argv[1],encoding="utf-8").read(), sys.argv[1])`

	var meldungen []string
	for _, rel := range dateien {
		abs := filepath.Join(bauRoot, rel)
		var cmd *exec.Cmd
		switch filepath.Ext(rel) {
		case ".py":
			if pfad, err := exec.LookPath("python3"); err == nil {
				cmd = exec.Command(pfad, "-c", pySyntax, abs)
			}
		case ".sh":
			if pfad, err := exec.LookPath("sh"); err == nil {
				cmd = exec.Command(pfad, "-n", abs)
			}
		}
		if cmd == nil {
			continue
		}
		cmd.Dir = bauRoot
		if ausgabe, err := cmd.CombinedOutput(); err != nil {
			// Die letzte Zeile, nicht die erste: bei Python steht oben
			// der Traceback und unten die eigentliche Meldung.
			zeilen := strings.Split(strings.TrimSpace(string(ausgabe)), "\n")
			meldungen = append(meldungen, fmt.Sprintf("SYNTAX ERROR in %s: %s", rel, strings.TrimSpace(zeilen[len(zeilen)-1])))
		} else {
			meldungen = append(meldungen, fmt.Sprintf("syntax of %s is fine (nothing was executed)", rel))
		}
	}
	return meldungen
}
