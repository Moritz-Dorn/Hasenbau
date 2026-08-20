// key.go beantwortet eine Frage, die länger nur halb beantwortet
// war: **woher kommt der API-Key eines Providers?** (Hasenbau-a88)
//
// Bis hierher hieß die Antwort „aus der geteilten auth.json", und das
// war die halbe Wahrheit. opencode versteht in `options.apiKey` auch
// `{env:VAR}` und `{file:PFAD}` — der empfohlene Weg im Container, weil
// die Bau-Config damit schlüssellos bleibt (§3). Wer so fährt, bekam von
// `hasenbau provider fetch` bis dahin „no auth.json" zu lesen, während
// seine Läufe tadellos liefen. Ein Befehl, der Fehlanzeige meldet,
// obwohl alles geht, verschiebt die Arbeit nur zum Menschen.
//
// DIE REIHENFOLGE IST NACHGELESEN, NICHT GERATEN (opencode dev,
// packages/opencode/src/provider/provider.ts):
//
//	if (options["apiKey"] === undefined && provider.key) options["apiKey"] = provider.key
//
// Also: `options.apiKey` gewinnt, `provider.key` — aus Umgebungsvariablen
// und auth.json — ist der Rückfall. Genau so steht es hier. Ebenfalls
// abgelesen (src/config/variable.ts): nur `env` und `file` sind gültige
// Präfixe, `~/` wird expandiert, der Dateiinhalt wird **getrimmt**, und
// eine nicht gesetzte Umgebungsvariable wird zum leeren String statt zum
// Fehler.
package provider

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// placeholder erkennt {env:NAME} und {file:PFAD}. Andere Präfixe kennt
// opencode nicht; sie blieben dort als Literal stehen und gingen als
// Schlüssel über die Leitung — hier werden sie zum Fehler, denn ein
// Schlüssel, der wie ein Platzhalter aussieht, ist keiner.
var placeholder = regexp.MustCompile(`\{(env|file):([^}]*)\}`)

// KeySource sagt, WOHER der Schlüssel eines Providers käme — ohne ihn
// herauszugeben. Für `get provider` und `describe provider`: die Frage
// dort ist nicht „wie lautet er", sondern „gibt es einen, und auf
// welchem Weg".
type KeySource struct {
	// Via ist der Weg: "options.apiKey" oder "auth.json". Leer heißt,
	// dass es keinen gibt.
	Via string
	// Ref ist die Referenz aus der Config ("{file:/run/secrets/…}") —
	// nie der Schlüssel selbst. Bei auth.json leer.
	Ref string
	// Err ist gesetzt, wenn der Weg konfiguriert ist, aber nichts
	// liefert: die Datei fehlt, die Variable ist leer. Das ist der
	// interessanteste Zustand von allen, weil er von außen wie ein
	// funktionierender Bau aussieht.
	Err error
}

// OK sagt, ob auf diesem Weg wirklich ein Schlüssel ankommt.
func (s KeySource) OK() bool { return s.Via != "" && s.Err == nil }

// Label ist die kurze Fassung für eine Tabellenspalte.
func (s KeySource) Label() string {
	switch {
	case s.Via == "":
		return "—"
	case s.Err != nil:
		return s.Via + " (broken)"
	default:
		return s.Via
	}
}

// KeySource ermittelt den Weg. inAuth ist, was KeyIDs über auth.json
// weiß — der Aufrufer hat es meist schon, und die Datei zweimal je
// Provider zu lesen wäre Verschwendung.
func (c *Config) KeySource(id string, inAuth bool) KeySource {
	if ref, ok := c.apiKeyRef(id); ok {
		s := KeySource{Via: "options.apiKey", Ref: ref}
		if _, err := resolve(ref); err != nil {
			s.Err = err
		}
		return s
	}
	if inAuth {
		return KeySource{Via: "auth.json"}
	}
	return KeySource{}
}

// Key liefert den Schlüssel auf demselben Weg, den opencode geht:
// options.apiKey gewinnt, auth.json ist der Rückfall.
func (c *Config) Key(id string) (string, error) {
	if ref, ok := c.apiKeyRef(id); ok {
		wert, err := resolve(ref)
		if err != nil {
			return "", err
		}
		if wert == "" {
			return "", fmt.Errorf("provider: options.apiKey of %q in %s resolves to an empty string (%s) — "+
				"opencode would send that as the key", id, c.Pfad, ref)
		}
		return wert, nil
	}
	return keyFromAuth(id)
}

// apiKeyRef liest options.apiKey roh — mit Platzhaltern, so wie es
// dasteht. ok=false heißt: der Provider setzt es nicht, dann gilt
// auth.json.
func (c *Config) apiKeyRef(id string) (string, bool) {
	p, ok := c.provider()[id].(map[string]any)
	if !ok {
		return "", false
	}
	opts, _ := p["options"].(map[string]any)
	ref, _ := opts["apiKey"].(string)
	return ref, ref != ""
}

// resolve ersetzt die Platzhalter. Ein Wert ohne Platzhalter kommt
// unverändert zurück — dann steht der Schlüssel im Klartext in der
// Bau-Config, was PLAN §3 nicht empfiehlt, aber auch nicht verbietet;
// opencode nähme ihn ebenso.
func resolve(ref string) (string, error) {
	// Erst prüfen, ob etwas wie ein Platzhalter aussieht, ohne einer zu
	// sein: `{vault:…}` bliebe bei opencode als Literal stehen und ginge
	// als Schlüssel hinaus. Das ist nie gewollt.
	if i := strings.IndexByte(ref, '{'); i >= 0 && !placeholder.MatchString(ref[i:]) {
		return "", fmt.Errorf("provider: %q is not a placeholder opencode knows — only {env:NAME} and {file:PATH}", ref)
	}

	var fehler error
	wert := placeholder.ReplaceAllStringFunc(ref, func(treffer string) string {
		teile := placeholder.FindStringSubmatch(treffer)
		art, arg := teile[1], teile[2]
		if art == "env" {
			// Nicht gesetzt wird zum leeren String, nicht zum Fehler —
			// so macht es opencode (config/variable.ts). Der Aufrufer
			// merkt es daran, dass am Ende nichts übrig bleibt.
			return os.Getenv(arg)
		}
		pfad, err := expandTilde(arg)
		if err != nil {
			fehler = err
			return ""
		}
		roh, err := os.ReadFile(pfad)
		if err != nil {
			fehler = fmt.Errorf("provider: {file:%s}: %w", arg, err)
			return ""
		}
		// opencode trimmt ebenfalls — ein abschließender Zeilenumbruch
		// aus `echo` darf keinen Schlüssel unbrauchbar machen.
		return strings.TrimSpace(string(roh))
	})
	if fehler != nil {
		return "", fehler
	}
	return wert, nil
}

// expandTilde macht aus `~/x` einen absoluten Pfad — auch das nach
// opencodes Vorbild, dessen Doku genau diese Schreibweise zeigt.
func expandTilde(pfad string) (string, error) {
	if !strings.HasPrefix(pfad, "~/") {
		return pfad, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("provider: %q: kein Home-Verzeichnis: %w", pfad, err)
	}
	return filepath.Join(home, pfad[2:]), nil
}
