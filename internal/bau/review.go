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
//	              unbedenklich ist — behauptet, nicht bestätigt
//	              („assumed" in der ValIntent-Liste). Ein bestandener
//	              Probelauf ändert daran nichts: er ist Beleg, nicht
//	              Urteil.
//	actual        Ein Mensch hat die Ausgabe gesehen und für richtig
//	              befunden — „verifiziert und entspricht der Realität".
//	invalid       Der Probelauf hat die Behauptung widerlegt.
//	outdated      War geprüft, dann hat jemand die Datei geändert.
//
// Die Asymmetrie zwischen `invalid` und `actual` ist Absicht: ein
// Fehlschlag WIDERLEGT (das kann eine Maschine feststellen), ein Erfolg
// BESTÄTIGT NICHT. Exit 0 heißt „es lief", nicht „es stimmt" — ob 24
// die richtige Zeilenzahl war, sieht nur ein Mensch. Diese Fassung
// stammt von Moritz; die vorige setzte `actual` beim Probelauf und
// verwechselte damit Beleg und Urteil.
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
		return "gelesen, gelaufen, und ein Mensch hat die Ausgabe für richtig befunden"
	case Hypothetical:
		return "gelesen, aber noch nicht als richtig bestätigt"
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

	// ReleasedBy und ReleasedAt hält fest, wer die Ausgabe des
	// Probelaufs für richtig befunden und das Werkzeug freigegeben hat.
	//
	// Erst das macht `actual`. Ein bestandener Probelauf allein tut es
	// NICHT: Exit 0 heißt „es lief", nicht „es stimmt", und `actual`
	// heißt nach der Intentionssemantik „verifiziert und entspricht der
	// Realität". Ob 24 die richtige Zeilenzahl war, kann kein Exit-Code
	// wissen — das sieht nur ein Mensch.
	ReleasedBy string
	ReleasedAt string

	// ValIntent ist der Zustand, wie er beim Schreiben des Blocks
	// galt — für Menschen und fremde Werkzeuge, die die Datei lesen,
	// ohne zu rechnen.
	//
	// Er ist AUSKUNFT, nicht Wahrheit. Maßgeblich bleibt der abgeleitete
	// Zustand: sonst könnte man sich `actual` in die Datei schreiben,
	// und genau das schließt die Intentionssemantik aus — klassifiziert
	// wird durch Verifikation, nicht durch Setzen. `outdated` liesse
	// sich ohnehin nie eintragen, weil es erst durch eine spätere
	// Änderung entsteht.
	ValIntent string

	// Kommentar ist das Zeichen, mit dem der Block geschrieben war
	// ("#" oder "//"). Leer heißt: es gab keinen Block.
	Kommentar string
}

// KommentarZeichen sind die Formen, in denen ein Review-Block stehen
// darf. Python und Bash nutzen `#`, TypeScript und JavaScript `//` —
// der Block gehört an den Code, nicht an eine Sprache.
var KommentarZeichen = []string{"#", "//"}

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
	zeichen := ""
	for i, z := range zeilen {
		inhalt, ist := kommentarInhalt(z)
		if !ist {
			// Der Block steht zusammenhängend im Kopf. Nach der ersten
			// Nicht-Kommentarzeile hinter dem Start ist er zu Ende.
			if start >= 0 && ende < 0 {
				ende = i
			}
			continue
		}
		if start < 0 && strings.HasPrefix(inhalt, ReviewMarke+":") {
			start = i
			zeichen = kommentarZeichen(z)
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
	r.Kommentar = zeichen
	return r, strings.Join(body, "\n")
}

// kommentarInhalt liefert den Text hinter dem Kommentarzeichen.
func kommentarInhalt(zeile string) (string, bool) {
	t := strings.TrimSpace(zeile)
	for _, z := range KommentarZeichen {
		if strings.HasPrefix(t, z) {
			return strings.TrimSpace(strings.TrimPrefix(t, z)), true
		}
	}
	return "", false
}

func kommentarZeichen(zeile string) string {
	t := strings.TrimSpace(zeile)
	// Längere Zeichen zuerst prüfen, sonst schluckt "#" nie und "//"
	// würde bei einem Präfix-Vergleich verloren gehen.
	for _, z := range []string{"//", "#"} {
		if strings.HasPrefix(t, z) {
			return z
		}
	}
	return "#"
}

func parseReviewBlock(block []string) Review {
	werte := map[string]string{}
	var reihenfolge []string
	letztes := ""
	for _, z := range block {
		t, _ := kommentarInhalt(z)
		if t == "" {
			continue
		}
		name, wert, ok := strings.Cut(t, ":")
		// Fortsetzungszeile: eingerückt und ohne eigenen Schlüssel.
		if !ok || eingerueckt(z) {
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
		ReleasedBy:   werte["released-by"],
		ReleasedAt:   werte["released-at"],
		ValIntent:    werte["valintent"],
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
// LeiteZustandAb rechnet den ValIntent-Wert aus Block und Body aus.
//
// Die Reihenfolge trägt die Bedeutung, und die Asymmetrie in der Mitte
// ist der Kern: ein FEHLGESCHLAGENER Probelauf widerlegt die Behauptung
// des Reviewers (`invalid`) — ein bestandener bestätigt sie NICHT. Exit
// 0 heißt „es lief", nicht „es stimmt". Ob die Ausgabe der Realität
// entspricht, sieht nur ein Mensch, und der sagt es beim Freigeben.
func LeiteZustandAb(r Review, body string) Zustand {
	if r.Hash == "" || r.Fehler != "" {
		return Generated
	}
	if r.Hash != BodyHash(body) {
		return Outdated
	}
	if r.VerifiedExit != nil && *r.VerifiedExit != 0 {
		return Invalid
	}
	if r.ReleasedBy != "" {
		return Actual
	}
	return Hypothetical
}

// SchreibeReviewBlock baut den Block. Er wird IMMER hier erzeugt und
// nie aus einem Entwurf übernommen: lernte der Schmied, dass ein Block
// ein Werkzeug freigabefähig macht, schriebe er einen.
func SchreibeReviewBlock(r Review, body string) string {
	k := r.Kommentar
	if k == "" {
		k = "#"
	}
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
	fmt.Fprintf(&b, "%s %s: %s\n", k, ReviewMarke, ReviewVersion)
	fmt.Fprintf(&b, "%s reviewed-by: %s\n", k, einzeilig(r.By))
	fmt.Fprintf(&b, "%s reviewed-at: %s\n", k, einzeilig(r.At))
	fmt.Fprintf(&b, "%s body-sha256: %s\n", k, BodyHash(body))
	schreibeFeld(&b, k, "does", r.Does)
	schreibeFeld(&b, k, "safe-because", r.Safe)
	if r.VerifiedAt != "" {
		fmt.Fprintf(&b, "%s verified-at: %s\n", k, einzeilig(r.VerifiedAt))
		fmt.Fprintf(&b, "%s verified-with: %s\n", k, einzeilig(r.VerifiedWith))
		if r.VerifiedExit != nil {
			fmt.Fprintf(&b, "%s verified-exit: %d\n", k, *r.VerifiedExit)
		}
	}
	if r.ReleasedBy != "" {
		fmt.Fprintf(&b, "%s released-by: %s\n", k, einzeilig(r.ReleasedBy))
		fmt.Fprintf(&b, "%s released-at: %s\n", k, einzeilig(r.ReleasedAt))
	}
	// Zuletzt der Zustand, der sich aus allem darüber ergibt. Er steht
	// bewusst am Ende: er ist die Zusammenfassung der Belege, nicht ihr
	// Ersatz, und wer ihn liest, hat sie vorher gesehen.
	//
	// Gerechnet wird über den frisch gesetzten Hash, nicht über den, der
	// in r stand — sonst stünde hier immer `generated`.
	geschrieben := r
	geschrieben.Hash = BodyHash(body)
	geschrieben.Fehler = ""
	fmt.Fprintf(&b, "%s valintent: %s\n", k, LeiteZustandAb(geschrieben, body))
	b.WriteString(rest)
	return b.String()
}

// schreibeFeld bricht langen Freitext um und rückt Fortsetzungen ein —
// ein Review, das man nicht lesen kann, ist keines.
func schreibeFeld(b *strings.Builder, k, name, wert string) {
	worte := strings.Fields(wert)
	if len(worte) == 0 {
		fmt.Fprintf(b, "%s %s:\n", k, name)
		return
	}
	zeile := k + " " + name + ":"
	for _, w := range worte {
		if len(zeile)+1+len(w) > 72 {
			b.WriteString(zeile + "\n")
			zeile = k + "   "
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

// eingerueckt erkennt eine Fortsetzungszeile: hinter dem
// Kommentarzeichen stehen Leerzeichen, bevor Text kommt.
func eingerueckt(zeile string) bool {
	t := strings.TrimSpace(zeile)
	for _, z := range []string{"//", "#"} {
		if strings.HasPrefix(t, z) {
			return strings.HasPrefix(strings.TrimPrefix(t, z), "  ")
		}
	}
	return false
}
