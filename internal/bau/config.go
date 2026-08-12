// config.go: hasenbau.yaml, die Config des Baus (PLAN.md §4). Sie wächst
// mit den Phasen — deshalb lehnt der Decoder Unbekanntes ab: ein
// verschriebener Schlüssel darf nicht still wirkungslos bleiben.
package bau

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// ConfigFile ist der Ort der Bau-Config, Bau-relativ.
const ConfigFile = "hasenbau.yaml"

// Config ist die geparste hasenbau.yaml.
type Config struct {
	// LogLevel ist geparst, aber noch ohne Konsumenten: der Daemon
	// loggt über log.New ohne Level. Der Schlüssel steht seit jeher im
	// Skelett; ihn hier stillschweigend zu schlucken wäre gelogen.
	LogLevel string

	// Baumeister ist der Auftrag, den `hasenbau baumeister` startet.
	// Leer heißt: keiner gesetzt. Im Bau dürfen mehrere Baumeister-
	// Varianten unter hasen/ liegen — welche zum Zug kommt, entscheidet
	// der hier benannte Auftrag über sein hase:-Feld.
	Baumeister string

	// Requests ist der Raum, in den das Werkzeug hasenbau_tool_request
	// die Wünsche der Hasen legt — der Eingang des Schmieds. Bau-weit
	// und nicht je Auftrag, weil das Werkzeug allen Hasen gehört. Leer
	// heißt: kein Wunsch-Raum, dann bekommt kein Hase das Werkzeug zu
	// sehen. Ein Briefkasten, den niemand leert, ist schlimmer als
	// keiner (Hasenbau-hcs).
	Requests string

	// Sandbox sagt, was der Wächter tut, wenn ein Hase ein Werkzeug
	// ruft, das ihn aus seiner Sandbox führen würde (Hasenbau-d2p):
	// "deny" weist den Aufruf ab, "warn" lässt ihn durch und meldet
	// ihn nur. Gemeldet wird in beiden Fällen. Vorgabe ist "deny" —
	// wer lockert, tut es sichtbar.
	Sandbox string

	// Throttle ist der Bau-weite Deckel über ALLE Aufträge
	// (Hasenbau-cvf). Der Deckel je Auftrag (§6) schützt einen Auftrag
	// vor sich selbst; dieser schützt das Budget: zehn Aufträge mit je
	// fünf Läufen je Stunde sind fünfzig. Der Nullwert heißt „kein
	// Bau-weiter Deckel".
	Throttle Throttle
}

// Throttle ist die Bau-weite Rate: höchstens Max Läufe je Per, rollend.
// Bewusst nur Zahl und Fenster und nicht das ganze throttle: aus §6 —
// ein Bau-weites Tageszeitfenster wäre eine andere Aussage („der ganze
// Bau ruht tagsüber") und ist nicht gefragt.
type Throttle struct {
	Max int
	Per time.Duration
}

// An sagt, ob ein Bau-weiter Deckel gesetzt ist.
func (t Throttle) An() bool { return t.Max > 0 }

type configFields struct {
	LogLevel   *string `yaml:"log_level"`
	Baumeister *string `yaml:"baumeister"`
	Sandbox    *string `yaml:"sandbox"`
	Requests   *string `yaml:"requests"`
	Throttle   *struct {
		Max int     `yaml:"max"`
		Per *string `yaml:"per"`
	} `yaml:"throttle"`
}

var logLevel = map[string]bool{"debug": true, "info": true, "warn": true, "error": true}

// Sandbox-Modi (Hasenbau-d2p).
const (
	SandboxDeny = "deny" // Aufruf abweisen; der Hase bekommt den Grund zu lesen
	SandboxWarn = "warn" // Aufruf durchlassen, aber melden
)

var sandboxModus = map[string]bool{SandboxDeny: true, SandboxWarn: true}

// namePattern gilt für Auftrags- und Hasen-Namen (§6). Hier dupliziert
// statt importiert: internal/auftrag hängt an dieser Config nicht, und
// umgekehrt soll bau nicht das halbe Auftragsformat hereinziehen.
var namePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

// LoadConfig liest <root>/hasenbau.yaml. Fehlt die Datei, gelten die
// Defaults — sie wird von `hasenbau init` angelegt, und ihr Fehlen ist
// kein Grund, den Daemon nicht zu starten.
func LoadConfig(root string) (*Config, error) {
	c := &Config{LogLevel: "info", Sandbox: SandboxDeny}
	pfad := filepath.Join(root, ConfigFile)
	src, err := os.ReadFile(pfad)
	if errors.Is(err, fs.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return nil, fmt.Errorf("bau: %s lesen: %w", ConfigFile, err)
	}

	dec := yaml.NewDecoder(bytes.NewReader(src))
	dec.KnownFields(true)
	var d configFields
	if err := dec.Decode(&d); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("bau: %s: %v (erlaubt: log_level, baumeister, sandbox, requests, throttle)", ConfigFile, err)
	}

	if d.LogLevel != nil {
		if !logLevel[*d.LogLevel] {
			return nil, fmt.Errorf("bau: %s: log_level %q (erlaubt: debug, info, warn, error)", ConfigFile, *d.LogLevel)
		}
		c.LogLevel = *d.LogLevel
	}
	if d.Baumeister != nil {
		if !namePattern.MatchString(*d.Baumeister) {
			return nil, fmt.Errorf("bau: %s: baumeister %q ist kein gültiger Auftrags-Name (erlaubt: Buchstaben, Ziffern, . _ -)", ConfigFile, *d.Baumeister)
		}
		c.Baumeister = *d.Baumeister
	}
	if d.Sandbox != nil {
		if !sandboxModus[*d.Sandbox] {
			return nil, fmt.Errorf("bau: %s: sandbox %q (erlaubt: %s, %s)", ConfigFile, *d.Sandbox, SandboxDeny, SandboxWarn)
		}
		c.Sandbox = *d.Sandbox
	}
	if d.Requests != nil {
		w := strings.TrimSpace(*d.Requests)
		if filepath.IsAbs(w) || strings.Contains(w, "..") {
			return nil, fmt.Errorf("bau: %s: requests %q muss ein Bau-relativer Raum sein", ConfigFile, w)
		}
		c.Requests = w
	}
	// Dieselbe Regel wie beim Deckel je Auftrag (§6): beide Hälften oder
	// keine. Eine Zahl ohne Fenster ist keine Rate, ein Fenster ohne
	// Zahl deckelt nichts — und beides sieht aus wie eine Drossel.
	if d.Throttle != nil {
		t := d.Throttle
		switch {
		case t.Max < 0:
			return nil, fmt.Errorf("bau: %s: throttle.max muss > 0 sein (throttle: weglassen für ungedrosselt)", ConfigFile)
		case t.Max > 0 && t.Per == nil:
			return nil, fmt.Errorf("bau: %s: throttle.max ohne per — höchstens %d Läufe je … was?", ConfigFile, t.Max)
		case t.Max == 0 && t.Per != nil:
			return nil, fmt.Errorf("bau: %s: throttle.per ohne max — das Fenster deckelt nichts", ConfigFile)
		case t.Max == 0 && t.Per == nil:
			return nil, fmt.Errorf("bau: %s: throttle ist leer — Feld weglassen für ungedrosselt", ConfigFile)
		}
		per, err := time.ParseDuration(*t.Per)
		if err != nil {
			return nil, fmt.Errorf("bau: %s: throttle.per %q ist keine Dauer wie \"1h\": %v", ConfigFile, *t.Per, err)
		}
		if per <= 0 {
			return nil, fmt.Errorf("bau: %s: throttle.per %q ist kein Fenster", ConfigFile, *t.Per)
		}
		c.Throttle = Throttle{Max: t.Max, Per: per}
	}

	// Ob es den Auftrag gibt, prüft nicht der Parser, sondern der
	// Befehl — der kann besser sagen, was zu tun ist.
	return c, nil
}
