// review.go trägt den Zustand eines Schmied-Werkzeugs (Hasenbau-9w6).
//
// Der Zustand wird ABGELEITET, nicht gespeichert: aus dem Review-Block
// im Skript, einem Hash über den Rest der Datei und dem vermerkten
// Probelauf. Das ist Absicht — so kann ihn jedes Werkzeug ausrechnen,
// eine GUI ebenso wie dieser Code, und niemand muss ihn pflegen.
//
// Benannt sind die Zustände nach der Intentionssemantik des IRS
// (ValIntent, irs.kit.edu/concept-store/IntentionSemantics). Der Satz,
// auf den es dabei ankommt, steht in der Definition von `hypothetical`:
// **klassifiziert wird durch Verifikation, nicht durch Setzen.** Genau
// daran war die erste Fassung dieses Features vorbeigelaufen — sie
// setzte „geprüft", indem jemand einen Befehl tippte.
//
//	generated     Der Schmied hat geschrieben, niemand hat gelesen.
//	hypothetical  Ein Mensch behauptet, was es tut und warum es
//	              unbedenklich ist — behauptet, nicht gezeigt
//	              („assumed" in der ValIntent-Liste).
//	actual        Ein Probelauf hat das Verhalten gezeigt.
//	invalid       Der Probelauf hat die Behauptung widerlegt.
//	outdated      War geprüft, dann hat jemand die Datei geändert.
//
// `outdated` fällt mit der Hash-Bindung zusammen, und das ist kein
// Zufall: die Definition lautet „war actual, Re-Verifikation
// fehlgeschlagen", und genau das ist ein Skript, dessen Body nicht mehr
// zu dem passt, was jemand gelesen hat.
package bau

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Zustand ist ein ValIntent-Wert.
type Zustand string

const (
	Generated    Zustand = "generated"
	Hypothetical Zustand = "hypothetical"
	Actual       Zustand = "actual"
	Invalid      Zustand = "invalid"
	Outdated     Zustand = "outdated"
)

// Erklaerung sagt in einem Halbsatz, was der Zustand für dieses Werkzeug
// bedeutet — die ValIntent-Namen sind präzise, aber nicht
// selbsterklärend, und in einer Tabelle steht sonst nur ein Fremdwort.
func (z Zustand) Erklaerung() string {
	switch z {
	case Actual:
		return "gelesen und im Probelauf gezeigt"
	case Hypothetical:
		return "gelesen, aber nicht ausgeführt"
	case Invalid:
		return "der Probelauf ist gescheitert"
	case Outdated:
		return "seit dem Review geändert — Review gilt nicht mehr"
	default:
		return "vom Schmied geschrieben, ungelesen"
	}
}

// ReviewMarke ist die erste Zeile des Blocks und zugleich sein
// Erkennungszeichen.
const ReviewMarke = "hasenbau-review"

// ReviewVersion ist die Formatversion. Sie steht im Block, damit ein
// späteres Format alte Blöcke nicht still falsch liest.
const ReviewVersion = "1"

// Review ist der geparste Block. Die Feldnamen im Block sind englisch
// wie alle Formatschlüssel (PLAN §1); der Freitext darin ist deutsch.
type Review struct {
	By     string // reviewed-by
	At     string // reviewed-at
	Hash   string // body-sha256, über das Skript OHNE den Block
	Does   string // does — was der Reviewer glaubt, dass es tut
	Safe   string // safe-because — warum er es für unbedenklich hält
	Rest   string // was sonst noch im Block stand
	Fehler string // gefüllt, wenn der Block da, aber unbrauchbar ist

	// Verifikation, vom Probelauf eingetragen. Leer heißt: nur
	// behauptet, nicht gezeigt.
	VerifiedAt   string
	VerifiedWith string
	VerifiedExit *int
}

// ReviewFelder sind die Pflichtfelder. Fehlt eines, ist der Block
// unbrauchbar — und das Werkzeug gilt als ungelesen.
var ReviewFelder = []string{"reviewed-by", "reviewed-at", "body-sha256", "does", "safe-because"}

// LiesReview zerlegt ein Skript in Review-Block und Body.
//
// Ein kaputter Block ist KEIN Ladefehler, sondern zählt wie kein Block:
// der Zustand fällt auf `generated` zurück und das Werkzeug gilt als
// ungelesen. Das ist die sichere Richtung. Ein harter Fehler wäre es
// nicht: den Block schreibt im Zweifel ein Modell, und ein Modell
// könnte damit den ganzen Bau lahmlegen.
func LiesReview(skript []byte) (Review, string) {
	zeilen := strings.Split(string(skript), "\n")
	start, ende := -1, -1
	for i, z := range zeilen {
		t := strings.TrimSpace(z)
		if !strings.HasPrefix(t, "#") {
			// Der Block steht zusammenhängend im Kopf. Nach der ersten
			// Nicht-Kommentarzeile hinter dem Start ist er zu Ende.
			if start >= 0 && ende < 0 {
				ende = i
			}
			continue
		}
		feld := strings.TrimSpace(strings.TrimPrefix(t, "#"))
		if start < 0 && strings.HasPrefix(feld, ReviewMarke+":") {
			start = i
		}
	}
	if start < 0 {
		return Review{}, string(skript)
	}
	if ende < 0 {
		ende = len(zeilen)
	}

	// Body ist alles ohne den Block — darüber läuft der Hash. So bleibt
	// er stabil, wenn im Block etwas dazukommt (etwa der Probelauf).
	body := append(append([]string{}, zeilen[:start]...), zeilen[ende:]...)

	r := parseReviewBlock(zeilen[start:ende])
	return r, strings.Join(body, "\n")
}

func parseReviewBlock(block []string) Review {
	werte := map[string]string{}
	var reihenfolge []string
	letztes := ""
	for _, z := range block {
		t := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(z), "#"))
		if t == "" {
			continue
		}
		name, wert, ok := strings.Cut(t, ":")
		// Fortsetzungszeile: eingerückt und ohne eigenen Schlüssel.
		if !ok || strings.HasPrefix(strings.TrimPrefix(strings.TrimSpace(z), "#"), "  ") {
			if letztes != "" {
				werte[letztes] = strings.TrimSpace(werte[letztes] + " " + t)
				continue
			}
			continue
		}
		name = strings.TrimSpace(name)
		if _, da := werte[name]; !da {
			reihenfolge = append(reihenfolge, name)
		}
		werte[name] = strings.TrimSpace(wert)
		letztes = name
	}

	r := Review{
		By:           werte["reviewed-by"],
		At:           werte["reviewed-at"],
		Hash:         werte["body-sha256"],
		Does:         werte["does"],
		Safe:         werte["safe-because"],
		VerifiedAt:   werte["verified-at"],
		VerifiedWith: werte["verified-with"],
	}
	if v, da := werte["verified-exit"]; da {
		if n, err := strconv.Atoi(v); err == nil {
			r.VerifiedExit = &n
		}
	}
	if v := werte[ReviewMarke]; v != ReviewVersion {
		r.Fehler = fmt.Sprintf("%s: %q — diese Fassung kennt nur Version %s", ReviewMarke, v, ReviewVersion)
		return r
	}
	var fehlend []string
	for _, f := range ReviewFelder {
		if strings.TrimSpace(werte[f]) == "" {
			fehlend = append(fehlend, f)
		}
	}
	if len(fehlend) > 0 {
		r.Fehler = "unvollständig, es fehlt: " + strings.Join(fehlend, ", ")
	}
	_ = reihenfolge
	return r
}

// BodyHash ist der Hash über das Skript ohne Review-Block. Er bindet
// das Review an genau den Inhalt, den jemand gelesen hat: eine Zeile
// geändert, und das Review gilt nicht mehr — auch dem Reviewer selbst
// gegenüber.
func BodyHash(body string) string {
	summe := sha256.Sum256([]byte(body))
	return hex.EncodeToString(summe[:])
}

// LeiteZustandAb rechnet den ValIntent-Wert aus Block und Body aus.
func LeiteZustandAb(r Review, body string) Zustand {
	if r.Hash == "" || r.Fehler != "" {
		return Generated
	}
	if r.Hash != BodyHash(body) {
		return Outdated
	}
	if r.VerifiedExit == nil {
		return Hypothetical
	}
	if *r.VerifiedExit != 0 {
		return Invalid
	}
	return Actual
}

// SchreibeReviewBlock baut den Block. Er wird IMMER hier erzeugt und
// nie aus einem Entwurf übernommen: lernte der Schmied, dass ein Block
// ein Werkzeug freigabefähig macht, schriebe er einen.
func SchreibeReviewBlock(r Review, body string) string {
	var b strings.Builder
	// Der Shebang muss die erste Zeile bleiben, sonst startet das
	// Skript nicht.
	rest := body
	shebang := ""
	if strings.HasPrefix(body, "#!") {
		zeile, danach, _ := strings.Cut(body, "\n")
		shebang, rest = zeile+"\n", danach
	}
	b.WriteString(shebang)
	fmt.Fprintf(&b, "# %s: %s\n", ReviewMarke, ReviewVersion)
	fmt.Fprintf(&b, "# reviewed-by: %s\n", einzeilig(r.By))
	fmt.Fprintf(&b, "# reviewed-at: %s\n", einzeilig(r.At))
	fmt.Fprintf(&b, "# body-sha256: %s\n", BodyHash(body))
	schreibeFeld(&b, "does", r.Does)
	schreibeFeld(&b, "safe-because", r.Safe)
	if r.VerifiedAt != "" {
		fmt.Fprintf(&b, "# verified-at: %s\n", einzeilig(r.VerifiedAt))
		fmt.Fprintf(&b, "# verified-with: %s\n", einzeilig(r.VerifiedWith))
		if r.VerifiedExit != nil {
			fmt.Fprintf(&b, "# verified-exit: %d\n", *r.VerifiedExit)
		}
	}
	b.WriteString(rest)
	return b.String()
}

// schreibeFeld bricht langen Freitext um und rückt Fortsetzungen ein —
// ein Review, das man nicht lesen kann, ist keines.
func schreibeFeld(b *strings.Builder, name, wert string) {
	worte := strings.Fields(wert)
	if len(worte) == 0 {
		fmt.Fprintf(b, "# %s:\n", name)
		return
	}
	zeile := "# " + name + ":"
	for _, w := range worte {
		if len(zeile)+1+len(w) > 72 {
			b.WriteString(zeile + "\n")
			zeile = "#   "
		}
		zeile += " " + w
	}
	b.WriteString(strings.TrimRight(zeile, " ") + "\n")
}

func einzeilig(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// JetztStempel ist der Zeitstempel im Block — UTC und RFC3339, damit
// zwei Rechner dasselbe schreiben.
func JetztStempel() string {
	return time.Now().UTC().Format(time.RFC3339)
}
