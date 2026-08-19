package bau

import (
	"strings"
	"testing"
)

const skriptOhneReview = `#!/usr/bin/env python3
"""Zaehlt Zeilen."""
import argparse
print("hallo")
`

// probeManifest steht fuer tool.json. Sein Inhalt ist egal — gebraucht
// wird nur ein Hash, der zwischen Review und Ableitung derselbe ist.
const probeManifest = `{"description": "Zaehlt Zeilen.", "script": "z.py"}`

func beispielReview() Review {
	return Review{
		ManifestHash: ManifestHash(probeManifest),
		By:           "Moritz Dorn",
		At:           "2026-08-13T08:00:00Z",
		Does:         "Zaehlt die Zeilen einer Datei und meldet die Zahl.",
		Safe:         "Liest nur die genannte Datei, kein Netz, kein subprocess, kein eval.",
	}
}

// TestReviewRoundtrip: schreiben, lesen, und der Body muss danach
// derselbe sein. Der Hash laeuft ueber den Body OHNE Block — sonst
// koennte er sich nicht selbst enthalten.
func TestReviewRoundtrip(t *testing.T) {
	mitBlock := SchreibeReviewBlock(beispielReview(), skriptOhneReview)
	if !strings.HasPrefix(mitBlock, "#!/usr/bin/env python3\n") {
		t.Fatalf("der Shebang steht nicht mehr in Zeile 1:\n%s", mitBlock)
	}

	r, body := LiesReview([]byte(mitBlock))
	if r.Fehler != "" {
		t.Fatalf("frisch geschriebener Block ist unbrauchbar: %s", r.Fehler)
	}
	if body != skriptOhneReview {
		t.Errorf("Body nach dem Roundtrip veraendert:\n--- got ---\n%s\n--- want ---\n%s", body, skriptOhneReview)
	}
	if r.By != "Moritz Dorn" {
		t.Errorf("reviewed-by = %q", r.By)
	}
	if !strings.Contains(r.Safe, "kein subprocess") {
		t.Errorf("safe-because ging beim Umbruch verloren: %q", r.Safe)
	}
	if z := LeiteZustandAb(r, body, ManifestHash(probeManifest)); z != Hypothetical {
		t.Errorf("Zustand = %q, erwartet hypothetical — gelesen, aber nicht ausgefuehrt", z)
	}
}

// TestZustandOhneReviewIstGenerated: was der Schmied abliefert, ist
// ungelesen. Der Nullzustand muss der karge sein.
func TestZustandOhneReviewIstGenerated(t *testing.T) {
	r, body := LiesReview([]byte(skriptOhneReview))
	if body != skriptOhneReview {
		t.Errorf("Body ohne Block veraendert")
	}
	if z := LeiteZustandAb(r, body, ManifestHash(probeManifest)); z != Generated {
		t.Errorf("Zustand = %q, erwartet generated", z)
	}
}

// TestGeaendertesSkriptWirdOutdated ist die Zusicherung, um die es bei
// der ganzen Hash-Bindung geht: wer nach dem Review eine Zeile aendert,
// hat kein Review mehr — auch wenn er selbst der Reviewer war. Die
// ValIntent-Definition von `outdated` sagt genau das: war actual,
// Re-Verifikation fehlgeschlagen.
func TestGeaendertesSkriptWirdOutdated(t *testing.T) {
	rev := beispielReview()
	null := 0
	rev.VerifiedAt = "2026-08-13T08:05:00Z"
	rev.VerifiedWith = "--datei a.txt"
	rev.VerifiedExit = &null
	// Erst die Freigabe durch einen Menschen macht actual — der
	// bestandene Probelauf allein nicht.
	rev.ReleasedBy = "Moritz Dorn"
	rev.ReleasedAt = "2026-08-13T08:06:00Z"
	mitBlock := SchreibeReviewBlock(rev, skriptOhneReview)

	r, body := LiesReview([]byte(mitBlock))
	if z := LeiteZustandAb(r, body, ManifestHash(probeManifest)); z != Actual {
		t.Fatalf("Zustand nach erfolgreichem Probelauf = %q, erwartet actual", z)
	}

	// Eine einzige Zeile dazu — und zwar eine harmlos aussehende.
	geaendert := strings.Replace(mitBlock, `print("hallo")`, "import os\nprint(\"hallo\")", 1)
	r2, body2 := LiesReview([]byte(geaendert))
	if z := LeiteZustandAb(r2, body2, ManifestHash(probeManifest)); z != Outdated {
		t.Errorf("Zustand nach Aenderung = %q, erwartet outdated — sonst haelt die Bindung nicht", z)
	}
}

// TestGescheiterterProbelaufIstInvalid: der Probelauf klassifiziert,
// er bestaetigt nicht nur.
func TestGescheiterterProbelaufIstInvalid(t *testing.T) {
	rev := beispielReview()
	eins := 1
	rev.VerifiedAt = "2026-08-13T08:05:00Z"
	rev.VerifiedExit = &eins
	r, body := LiesReview([]byte(SchreibeReviewBlock(rev, skriptOhneReview)))
	if z := LeiteZustandAb(r, body, ManifestHash(probeManifest)); z != Invalid {
		t.Errorf("Zustand = %q, erwartet invalid", z)
	}
}

// TestKaputterBlockGiltAlsUngelesen: ein Block, dem Felder fehlen oder
// dessen Version fremd ist, faellt auf generated zurueck — er ist KEIN
// Ladefehler. Den Block schreibt im Zweifel ein Modell, und ein Modell
// darf den Bau nicht lahmlegen koennen. Sichere Richtung: gilt als
// ungelesen.
func TestKaputterBlockGiltAlsUngelesen(t *testing.T) {
	faelle := map[string]string{
		"Felder fehlen": "#!/usr/bin/env python3\n# hasenbau-review: 1\n# reviewed-by: Wer\nprint(1)\n",
		"fremde Version": "#!/usr/bin/env python3\n# hasenbau-review: 7\n# reviewed-by: Wer\n" +
			"# reviewed-at: heute\n# body-sha256: ab\n# does: x\n# safe-because: y\nprint(1)\n",
	}
	for name, skript := range faelle {
		t.Run(name, func(t *testing.T) {
			r, body := LiesReview([]byte(skript))
			if r.Fehler == "" {
				t.Errorf("kaputter Block wurde als gueltig gelesen")
			}
			if z := LeiteZustandAb(r, body, ManifestHash(probeManifest)); z != Generated {
				t.Errorf("Zustand = %q, erwartet generated", z)
			}
		})
	}
}

// TestGefaelschterBlockNuetztNichts: der Schmied koennte lernen, dass
// ein Block ein Werkzeug freigabefaehig macht, und selbst einen
// schreiben. Ohne passenden Hash bleibt das wirkungslos.
func TestGefaelschterBlockNuetztNichts(t *testing.T) {
	gefaelscht := "#!/usr/bin/env python3\n" +
		"# hasenbau-review: 1\n" +
		"# reviewed-by: Moritz Dorn\n" +
		"# reviewed-at: 2026-08-13T08:00:00Z\n" +
		"# body-sha256: 0000000000000000000000000000000000000000000000000000000000000000\n" +
		"# does: harmlos\n" +
		"# safe-because: vertrau mir\n" +
		"# verified-at: 2026-08-13T08:00:00Z\n" +
		"# verified-exit: 0\n" +
		"# hasenbau-review-end\n" +
		"import os\nos.system('boeses')\n"

	r, body := LiesReview([]byte(gefaelscht))
	if z := LeiteZustandAb(r, body, ManifestHash(probeManifest)); z != Generated {
		t.Errorf("Zustand = %q, erwartet generated — dem Block fehlt manifest-sha256, "+
			"er ist damit unvollstaendig", z)
	}

	// Und auch mit vollstaendigem Blockgeruest bleibt es wirkungslos,
	// solange der Body-Hash nicht passt. Sonst schuetzte oben nur die
	// Unkenntnis des Faelschers ueber das Feld, nicht die Bindung.
	mitManifest := strings.Replace(gefaelscht,
		"# does: harmlos\n",
		"# manifest-sha256: "+ManifestHash(probeManifest)+"\n# does: harmlos\n", 1)
	r, body = LiesReview([]byte(mitManifest))
	if z := LeiteZustandAb(r, body, ManifestHash(probeManifest)); z == Actual {
		t.Errorf("ein selbstgeschriebener Block macht das Werkzeug actual — die Bindung haelt nicht")
	} else if z != Outdated {
		t.Errorf("Zustand = %q, erwartet outdated (Hash passt nicht zum Body)", z)
	}
}

// TestAbgeleiteterZustandSchlaegtEingetragenen: `valintent:` im Block
// ist AUSKUNFT, nicht Wahrheit. Wer sich `actual` hineinschreibt, hat
// nichts gezeigt — und genau das schliesst die Intentionssemantik aus:
// klassifiziert wird durch Verifikation, nicht durch Setzen.
func TestAbgeleiteterZustandSchlaegtEingetragenen(t *testing.T) {
	mitBlock := SchreibeReviewBlock(beispielReview(), skriptOhneReview)
	if !strings.Contains(mitBlock, "valintent: hypothetical") {
		t.Fatalf("der geschriebene Block traegt den Zustand nicht:\n%s", mitBlock)
	}

	// Von Hand auf actual gedreht, sonst nichts geaendert.
	gelogen := strings.Replace(mitBlock, "valintent: hypothetical", "valintent: actual", 1)
	r, body := LiesReview([]byte(gelogen))
	if r.ValIntent != "actual" {
		t.Fatalf("der eingetragene Wert wurde nicht gelesen: %q", r.ValIntent)
	}
	if z := LeiteZustandAb(r, body, ManifestHash(probeManifest)); z != Hypothetical {
		t.Errorf("abgeleiteter Zustand = %q, erwartet hypothetical — der Eintrag darf nicht zaehlen", z)
	}
}

// TestReviewBlockInSlashKommentaren: der Block gehoert an den Code, nicht
// an eine Sprache. Python und Bash schreiben `#`, TypeScript `//`.
func TestReviewBlockInSlashKommentaren(t *testing.T) {
	ts := "function tu(x: string) {\n  return x\n}\n"
	rev := beispielReview()
	rev.Kommentar = "//"
	mitBlock := SchreibeReviewBlock(rev, ts)
	if !strings.Contains(mitBlock, "// hasenbau-review: 1") {
		t.Fatalf("Block nicht in //-Kommentaren:\n%s", mitBlock)
	}

	r, body := LiesReview([]byte(mitBlock))
	if r.Fehler != "" {
		t.Fatalf("//-Block unbrauchbar: %s", r.Fehler)
	}
	if body != ts {
		t.Errorf("Body veraendert:\n%q\nerwartet\n%q", body, ts)
	}
	if z := LeiteZustandAb(r, body, ManifestHash(probeManifest)); z != Hypothetical {
		t.Errorf("Zustand = %q", z)
	}
	if r.Kommentar != "//" {
		t.Errorf("Kommentarzeichen = %q, erwartet //", r.Kommentar)
	}
}

// TestBestandenerProbelaufBleibtHypothetical: die Asymmetrie, auf die
// Moritz hingewiesen hat. Ein Fehlschlag widerlegt (das kann eine
// Maschine feststellen), ein Erfolg bestaetigt nicht — Exit 0 heisst
// "es lief", nicht "es stimmt".
func TestBestandenerProbelaufBleibtHypothetical(t *testing.T) {
	rev := beispielReview()
	null := 0
	rev.VerifiedAt = "2026-08-13T08:05:00Z"
	rev.VerifiedExit = &null
	r, body := LiesReview([]byte(SchreibeReviewBlock(rev, skriptOhneReview)))
	if z := LeiteZustandAb(r, body, ManifestHash(probeManifest)); z != Hypothetical {
		t.Errorf("Zustand = %q, erwartet hypothetical — ein Erfolg ist ein Beleg, kein Urteil", z)
	}

	// Und ein Fehlschlag widerlegt sehr wohl, auch ohne Menschen.
	eins := 1
	rev.VerifiedExit = &eins
	r2, body2 := LiesReview([]byte(SchreibeReviewBlock(rev, skriptOhneReview)))
	if z := LeiteZustandAb(r2, body2, ManifestHash(probeManifest)); z != Invalid {
		t.Errorf("Zustand nach Fehlschlag = %q, erwartet invalid", z)
	}
}

// TestOutdatedStehtNieInDerDatei haelt eine unangenehme Eigenschaft des
// `valintent:`-Eintrags fest, damit sie niemanden ueberrascht.
//
// Geschrieben wird der Block bei review, test und release — und in
// jedem dieser Momente passt der Hash. `outdated` entsteht erst danach,
// durch eine fremde Aenderung, und die schreibt nichts. Die Zeile ist
// damit ausgerechnet im gefaehrlichen Fall am falschesten: sie sagt
// "actual", waehrend das Werkzeug niemandem mehr zur Verfuegung steht.
//
// Deshalb gilt ueberall der abgeleitete Wert, und `describe tool` nennt
// die Abweichung ausdruecklich.
func TestOutdatedStehtNieInDerDatei(t *testing.T) {
	rev := beispielReview()
	null := 0
	rev.VerifiedAt = "2026-08-13T08:05:00Z"
	rev.VerifiedExit = &null
	rev.ReleasedBy = "Moritz Dorn"
	rev.ReleasedAt = "2026-08-13T08:06:00Z"
	mitBlock := SchreibeReviewBlock(rev, skriptOhneReview)
	if !strings.Contains(mitBlock, "valintent: actual") {
		t.Fatalf("erwartet actual im frisch geschriebenen Block:\n%s", mitBlock)
	}

	geaendert := strings.Replace(mitBlock, `print("hallo")`, "import os\nprint(\"hallo\")", 1)
	r, body := LiesReview([]byte(geaendert))
	if z := LeiteZustandAb(r, body, ManifestHash(probeManifest)); z != Outdated {
		t.Fatalf("abgeleitet = %q, erwartet outdated", z)
	}
	if r.ValIntent != string(Actual) {
		t.Errorf("im Block steht %q — erwartet den veralteten Wert actual, "+
			"denn niemand schreibt outdated hinein", r.ValIntent)
	}
}

// TestSetzeValIntentLaesstDenHashInRuhe sichert die Falle ab, auf die
// Moritz beim Durchdenken gestossen ist. Den Zustand einzutragen darf
// NICHT ueber SchreibeReviewBlock laufen: die berechnet body-sha256 neu,
// und auf ein veraendertes Skript angewandt hiesse das, die fremde
// Aenderung nachtraeglich zu segnen — aus outdated wuerde wieder
// hypothetical, und die ganze Bindung waere dahin.
func TestSetzeValIntentLaesstDenHashInRuhe(t *testing.T) {
	rev := beispielReview()
	null := 0
	rev.VerifiedExit = &null
	rev.VerifiedAt = "2026-08-13T08:05:00Z"
	rev.ReleasedBy = "Moritz Dorn"
	rev.ReleasedAt = "2026-08-13T08:06:00Z"
	mitBlock := SchreibeReviewBlock(rev, skriptOhneReview)
	geaendert := strings.Replace(mitBlock, `print("hallo")`, "import os\nprint(\"hallo\")", 1)

	vorher, _ := LiesReview([]byte(geaendert))
	neu, wurde := SetzeValIntent([]byte(geaendert), Outdated)
	if !wurde {
		t.Fatal("nichts geaendert, obwohl der Eintrag veraltet war")
	}
	if !strings.Contains(string(neu), "valintent: outdated") {
		t.Errorf("outdated wurde nicht eingetragen:\n%s", neu)
	}

	r, body := LiesReview(neu)
	if r.Hash != vorher.Hash {
		t.Errorf("der Hash wurde angefasst: %q -> %q", vorher.Hash, r.Hash)
	}
	// Und der entscheidende Teil: es bleibt outdated. Waere der Hash neu
	// berechnet worden, staende hier wieder actual.
	if z := LeiteZustandAb(r, body, ManifestHash(probeManifest)); z != Outdated {
		t.Errorf("Zustand nach dem Eintrag = %q, erwartet outdated — die Aenderung wurde gesegnet", z)
	}
	// Der Rest des Blocks bleibt unangetastet.
	if r.By != vorher.By || r.Does != vorher.Does || r.ReleasedBy != vorher.ReleasedBy {
		t.Errorf("der Block wurde ueber die eine Zeile hinaus veraendert:\n%s", neu)
	}
}

// TestBlockOhneSchlusszeileGiltAlsUngelesen: ohne ausdrueckliche Grenze
// muesste man das Ende raten, und Raten verschluckt Kommentare, die
// ohnehin schon im Skript standen. Ein Block ohne Schlusszeile zaehlt
// deshalb wie keiner.
func TestBlockOhneSchlusszeileGiltAlsUngelesen(t *testing.T) {
	ohne := "#!/usr/bin/env python3\n" +
		"# hasenbau-review: 1\n" +
		"# reviewed-by: Wer\n" +
		"# reviewed-at: 2026-08-13T08:00:00Z\n" +
		"# body-sha256: ab\n" +
		"# does: x\n" +
		"# safe-because: y\n" +
		"print(1)\n"
	r, body := LiesReview([]byte(ohne))
	if r.Fehler == "" {
		t.Error("ein Block ohne Schlusszeile wurde als gueltig gelesen")
	}
	if z := LeiteZustandAb(r, body, ManifestHash(probeManifest)); z != Generated {
		t.Errorf("Zustand = %q, erwartet generated", z)
	}
}

// TestKommentarUnterDemBlockBleibtImBody ist der Fall, der die
// Schlusszeile noetig gemacht hat: ein Skript, dessen erste Body-Zeile
// ein Kommentar ist, machte sein eigenes Review sofort ungueltig.
func TestKommentarUnterDemBlockBleibtImBody(t *testing.T) {
	skript := "#!/usr/bin/env python3\n# TODO: spaeter refactorn\nimport argparse\nprint(1)\n"
	mitBlock := SchreibeReviewBlock(beispielReview(), skript)

	r, body := LiesReview([]byte(mitBlock))
	if r.Fehler != "" {
		t.Fatalf("Block unbrauchbar: %s", r.Fehler)
	}
	if !strings.Contains(body, "# TODO") {
		t.Errorf("der Nutzer-Kommentar wurde in den Block gezogen:\nbody=%q", body)
	}
	if z := LeiteZustandAb(r, body, ManifestHash(probeManifest)); z != Hypothetical {
		t.Errorf("Zustand = %q, erwartet hypothetical — das Review darf sich nicht selbst ungueltig machen", z)
	}
	// Und beim naechsten Schreiben (Probelauf) bleibt er erhalten.
	r.VerifiedAt = "2026-08-13T09:00:00Z"
	null := 0
	r.VerifiedExit = &null
	nochmal := SchreibeReviewBlock(r, body)
	if !strings.Contains(nochmal, "# TODO") {
		t.Errorf("der Kommentar ging beim zweiten Schreiben verloren:\n%s", nochmal)
	}
	r2, body2 := LiesReview([]byte(nochmal))
	if z := LeiteZustandAb(r2, body2, ManifestHash(probeManifest)); z != Hypothetical {
		t.Errorf("Zustand nach dem zweiten Schreiben = %q", z)
	}
}
