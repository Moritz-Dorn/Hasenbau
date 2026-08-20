package bau

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// aufruf findet `exec.LookPath("x")`, `exec.Command("x", …)` und
// `exec.CommandContext(ctx, "x", …)`.
//
// Das führende `ctx,` ist bewusst das EINZIGE, was vor dem String
// stehen darf. Ließe man einen beliebigen Bezeichner zu, fände der
// Ausdruck in `exec.Command(s.cfg.Binary, "serve")` das Wort "serve"
// und meldete ein Programm, das es nicht gibt. Der Preis ist die
// Gegenrichtung: ein Aufruf, dessen Programm in einer Variablen steckt,
// wird hier nicht gesehen — genau deshalb steht `opencode` in
// ExternalPrograms mit einem Kommentar statt durch diesen Fund.
var aufruf = regexp.MustCompile(`exec\.(?:LookPath|Command|CommandContext)\((?:ctx,\s*)?"([^"]+)"`)

// Der Test, um dessentwillen die Liste überhaupt an einer Stelle steht:
// Was der Hasenbau wirklich ruft, muss im Dockerfile ankommen und in der
// Diagnose geprüft werden. Die Kopplung pflegt sonst niemand — ein neues
// `exec.Command("jq", …)` fiele erst im Container auf, und dort als
// Gang-Fehler, nicht als fehlendes Paket.
func TestListeKenntJedesGerufeneProgramm(t *testing.T) {
	bekannt := map[string]bool{}
	for _, p := range ExternalPrograms {
		if p.Command != "" {
			bekannt[p.Command] = true
		}
	}

	gefunden := map[string][]string{}
	for _, dir := range []string{"../../internal", "../../cmd"} {
		err := filepath.WalkDir(dir, func(pfad string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(pfad, ".go") {
				return err
			}
			// Tests sind ausgenommen: das Image führt das Produkt aus,
			// nicht die Suite. Ein `go` oder `docker` aus einem Test
			// gehört nicht in ein Bau-Image.
			if strings.HasSuffix(pfad, "_test.go") {
				return nil
			}
			roh, err := os.ReadFile(pfad)
			if err != nil {
				return err
			}
			for _, m := range aufruf.FindAllStringSubmatch(string(roh), -1) {
				gefunden[m[1]] = append(gefunden[m[1]], pfad)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("%s durchsuchen: %v", dir, err)
		}
	}

	if len(gefunden) == 0 {
		t.Fatal("kein einziger exec-Aufruf gefunden — der Ausdruck oder die Pfade stimmen nicht mehr")
	}
	for befehl, orte := range gefunden {
		if !bekannt[befehl] {
			t.Errorf("%q wird gerufen (%s), steht aber nicht in ExternalPrograms — "+
				"das erzeugte Dockerfile installiert es nicht und `describe bau` prüft es nicht",
				befehl, strings.Join(orte, ", "))
		}
	}
}

// Jeder Eintrag trägt seinen Grund. Ein Paket ohne Grund wäre eine
// Zeile, die beim nächsten Aufräumen fällt, weil niemand mehr weiß,
// wofür sie da war.
func TestJedesProgrammHatEinenGrund(t *testing.T) {
	for _, p := range ExternalPrograms {
		if p.Command == "" && p.Package == "" {
			t.Error("Eintrag ohne Befehl und ohne Paket")
		}
		if p.Why == "" {
			t.Errorf("%q ohne Grund", p.Command+p.Package)
		}
	}
}

// Der Kern von Hasenbau-nbk: der Check fragt bwrap, statt es zu zählen.
// Beide Ausgänge müssen sich unterscheiden lassen — sonst ist die
// Prüfung wieder die alte.
func TestProgrammeCheckFragtBwrapStattEsZuZaehlen(t *testing.T) {
	c := checkPrograms()

	ok, grund := BwrapWorks()
	switch {
	case !ok && grund == "not in PATH":
		// Kein bwrap: dann muss es unter den fehlenden stehen.
		if c.OK || !strings.Contains(c.Detail, "bwrap") {
			t.Errorf("bwrap fehlt, der Check sagt aber: %+v", c)
		}
	case !ok:
		// Da, aber kann nichts — der Fall, den LookPath nicht sieht.
		if c.OK {
			t.Errorf("bwrap kann keinen Namespace (%q), der Check meldet trotzdem ok", grund)
		}
		if !strings.Contains(c.Hint, grund) {
			t.Errorf("der Hinweis gibt bwraps eigene Auskunft nicht weiter: %q", c.Hint)
		}
	default:
		if !c.OK {
			t.Errorf("alles da und bwrap läuft, der Check meldet trotzdem: %+v", c)
		}
		// Sichtbar machen, DASS gefragt wurde — sonst liest sich ein
		// grüner Check wie das alte Vorhandensein.
		if !strings.Contains(c.Detail, "probed") {
			t.Errorf("dem Detail sieht man den Probelauf nicht an: %q", c.Detail)
		}
	}
}

// Fehlt ein Programm, muss dastehen WOZU es gebraucht wird. Ein
// "missing: python3" ohne Folge ist eine Meldung, die man wegklickt.
func TestFehlendesProgrammNenntSeineFolge(t *testing.T) {
	pfad := t.TempDir() // leerer PATH: hier liegt nichts
	t.Setenv("PATH", pfad)

	c := checkPrograms()
	if c.OK {
		t.Fatal("leerer PATH, und der Check ist trotzdem grün")
	}
	for _, p := range ExternalPrograms {
		if p.Command == "" {
			continue
		}
		if !strings.Contains(c.Detail, p.Command) {
			t.Errorf("%q fehlt in der Aufzählung: %q", p.Command, c.Detail)
		}
		if !strings.Contains(c.Hint, p.Why) {
			t.Errorf("der Grund für %q steht nicht im Hinweis", p.Command)
		}
	}
}
