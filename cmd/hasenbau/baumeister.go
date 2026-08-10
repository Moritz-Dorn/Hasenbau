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

	"github.com/Moritz-Dorn/Hasenbau/internal/bau"
	"github.com/Moritz-Dorn/Hasenbau/internal/store"
)

func cmdBaumeister(root string, args []string, out, errw io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(errw, "Aufruf: hasenbau baumeister <lauf-id|auftrag>")
		return 2
	}
	logger := log.New(errw, "", log.LstdFlags)

	conf, err := bau.LoadConfig(root)
	if err != nil {
		logger.Print(err)
		return 1
	}
	if conf.Baumeister == "" {
		fmt.Fprintf(errw, "hasenbau baumeister: %s nennt keinen baumeister:\n"+
			"  Vorlage kopieren (beispiele/auftraege/baumeister.md, beispiele/hasen/baumeister.md),\n"+
			"  dann `baumeister: <auftrag>` in %s eintragen.\n", bau.ConfigFile, bau.ConfigFile)
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
		fmt.Fprintf(errw, "hasenbau baumeister: der Auftrag %s braucht einen Raum mit Rolle out —\n"+
			"  nur daraus entsteht das Schreibrecht des Baumeisters (PLAN.md §6).\n", ziel.Name)
		return 1
	}

	// Vor dem Server auflösen: ein Tippfehler soll kein opencode starten.
	quelle, err := targetLauf(k.Store, args[0])
	if err != nil {
		fmt.Fprintf(errw, "hasenbau baumeister: %v\n", err)
		return 1
	}

	vorher, err := draftSnapshot(root, raum)
	if err != nil {
		logger.Print(err)
		return 1
	}

	fmt.Fprintf(out, "Baumeister %s auf Lauf %d (%s, %s, %s)\n",
		ziel.Name, quelle.ID, quelle.Auftrag, quelle.Trigger, quelle.Status)
	if err := k.StartServer(); err != nil {
		logger.Print(err)
		return 1
	}

	laufFehler := k.Runner.Execute(k.Ctx, ziel, "manual", strconv.FormatInt(quelle.ID, 10))
	if laufFehler != nil {
		logger.Print(laufFehler)
	}

	// Bericht auch nach einem Fehlschlag: der Hase kann geschrieben
	// haben, bevor etwas anderes schiefging.
	nachher, err := draftSnapshot(root, raum)
	if err != nil {
		logger.Print(err)
		return 1
	}
	neu := reportDrafts(out, raum, vorher, nachher)
	for _, meldung := range checkDrafts(root, neu) {
		fmt.Fprintf(out, "  %s\n", meldung)
	}
	if len(neu) > 0 {
		fmt.Fprintf(out, "\nNICHT AKTIVIERT — der Hasenbau trägt nie selbst etwas in einen Auftrag ein\n"+
			"(PLAN.md §8/§10). Lies das Skript, dann trag den Gang selbst ein; der\n"+
			"Vorschlag steht im Kopf der Datei.\n")
	}
	fmt.Fprintf(out, "\nDer Trace, aus dem er gearbeitet hat: `hasenbau dig %d`.\n", quelle.ID)

	if laufFehler != nil {
		return 1
	}
	return 0
}

// targetLauf löst das Argument auf: reine Ziffern sind eine Lauf-ID,
// alles andere ein Auftragsname (dann der jüngste Lauf mit Session).
// Zurück kommt immer ein echter Lauf — damit ist das $INPUT des
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
		return nil, fmt.Errorf("lauf %d (%s, %s) hat keine Session — es gibt nichts zu verdichten", l.ID, l.Auftrag, l.Status)
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
		fmt.Fprintln(w, "  unverändert — der Baumeister hat nichts geschrieben (warum, sagt seine Summary)")
		return nil
	}
	for _, p := range neu {
		fmt.Fprintf(w, "  neu        %s (%d Bytes)\n", p, nachher[p].Size)
	}
	for _, p := range geaendert {
		fmt.Fprintf(w, "  GEÄNDERT   %s (%d → %d Bytes) — hier lag schon ein Draft\n",
			p, vorher[p].Size, nachher[p].Size)
	}
	if unveraendert := len(nachher) - len(neu) - len(geaendert); unveraendert > 0 {
		fmt.Fprintf(w, "  unverändert: %d Datei(en)\n", unveraendert)
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
			meldungen = append(meldungen, fmt.Sprintf("SYNTAXFEHLER in %s: %s", rel, strings.TrimSpace(zeilen[len(zeilen)-1])))
		} else {
			meldungen = append(meldungen, fmt.Sprintf("Syntax von %s ist in Ordnung (ausgeführt wurde nichts)", rel))
		}
	}
	return meldungen
}
