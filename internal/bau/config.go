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

	"gopkg.in/yaml.v3"
)

// ConfigDatei ist der Ort der Bau-Config, Bau-relativ.
const ConfigDatei = "hasenbau.yaml"

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
}

type configDaten struct {
	LogLevel   *string `yaml:"log_level"`
	Baumeister *string `yaml:"baumeister"`
}

var logLevel = map[string]bool{"debug": true, "info": true, "warn": true, "error": true}

// namePattern gilt für Auftrags- und Hasen-Namen (§6). Hier dupliziert
// statt importiert: internal/auftrag hängt an dieser Config nicht, und
// umgekehrt soll bau nicht das halbe Auftragsformat hereinziehen.
var namePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

// LadeConfig liest <root>/hasenbau.yaml. Fehlt die Datei, gelten die
// Defaults — sie wird von `hasenbau init` angelegt, und ihr Fehlen ist
// kein Grund, den Daemon nicht zu starten.
func LadeConfig(root string) (*Config, error) {
	c := &Config{LogLevel: "info"}
	pfad := filepath.Join(root, ConfigDatei)
	src, err := os.ReadFile(pfad)
	if errors.Is(err, fs.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return nil, fmt.Errorf("bau: %s lesen: %w", ConfigDatei, err)
	}

	dec := yaml.NewDecoder(bytes.NewReader(src))
	dec.KnownFields(true)
	var d configDaten
	if err := dec.Decode(&d); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("bau: %s: %v (erlaubt: log_level, baumeister)", ConfigDatei, err)
	}

	if d.LogLevel != nil {
		if !logLevel[*d.LogLevel] {
			return nil, fmt.Errorf("bau: %s: log_level %q (erlaubt: debug, info, warn, error)", ConfigDatei, *d.LogLevel)
		}
		c.LogLevel = *d.LogLevel
	}
	if d.Baumeister != nil {
		if !namePattern.MatchString(*d.Baumeister) {
			return nil, fmt.Errorf("bau: %s: baumeister %q ist kein gültiger Auftrags-Name (erlaubt: Buchstaben, Ziffern, . _ -)", ConfigDatei, *d.Baumeister)
		}
		c.Baumeister = *d.Baumeister
	}
	// Ob es den Auftrag gibt, prüft nicht der Parser, sondern der
	// Befehl — der kann besser sagen, was zu tun ist.
	return c, nil
}
