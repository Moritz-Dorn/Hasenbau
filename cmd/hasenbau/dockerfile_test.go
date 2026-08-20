package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
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
// fremdprogramme mit einem Kommentar statt durch diesen Fund.
var aufruf = regexp.MustCompile(`exec\.(?:LookPath|Command|CommandContext)\((?:ctx,\s*)?"([^"]+)"`)

// Der Test, um dessentwillen die Liste im Code steht: Was der Hasenbau
// wirklich ruft, muss im Dockerfile ankommen. Die Kopplung pflegt sonst
// niemand — ein neues `exec.Command("jq", …)` fiele erst im Container
// auf, und dort als Gang-Fehler, nicht als fehlendes Paket.
func TestDockerfileKenntJedesGerufeneProgramm(t *testing.T) {
	bekannt := map[string]bool{}
	for _, f := range fremdprogramme {
		if f.Befehl != "" {
			bekannt[f.Befehl] = true
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
			t.Errorf("%q wird gerufen (%s), steht aber nicht in fremdprogramme — "+
				"das erzeugte Dockerfile installiert es also nicht",
				befehl, strings.Join(orte, ", "))
		}
	}
}

// Jeder Eintrag trägt seinen Grund, und jedes Paket landet in der
// apt-Zeile. Ein Paket ohne Grund wäre eine Zeile, die beim nächsten
// Aufräumen fällt, weil niemand mehr weiß, wofür sie da war.
func TestDockerfileInstalliertJedesPaketMitGrund(t *testing.T) {
	text := dockerfileGeruest("1.2.3", providerbefund{Beispiel: "<provider-id>"})
	for _, f := range fremdprogramme {
		if f.Grund == "" {
			t.Errorf("%q ohne Grund", f.Befehl+f.Paket)
		}
		if f.Paket == "" {
			continue
		}
		if !strings.Contains(text, "\n      "+f.Paket+" \\\n") {
			t.Errorf("Paket %q fehlt in der apt-Zeile", f.Paket)
		}
	}
}

// Das Versprechen der Datei: kein Schlüssel darin. COPY und ENV landen
// in den Layern und sind mit `docker history` lesbar — was hier
// hineinkäme, bekäme man nicht wieder heraus.
func TestDockerfileTraegtKeinenSchluessel(t *testing.T) {
	text := dockerfileGeruest("1.2.3", providerbefund{Zeile: "scc (custom)", Beispiel: "scc"})

	for _, zeile := range strings.Split(text, "\n") {
		nackt := strings.TrimSpace(zeile)
		if strings.HasPrefix(nackt, "#") {
			continue // Kommentare erklären die Wege, sie gehen keinen
		}
		if strings.Contains(nackt, "auth.json") {
			t.Errorf("wirksame Zeile fasst auth.json an: %q", zeile)
		}
		if regexp.MustCompile(`^(ENV|ARG)\s+\w*(KEY|TOKEN|SECRET)`).MatchString(nackt) {
			t.Errorf("wirksame Zeile setzt ein Geheimnis: %q", zeile)
		}
	}
	// Und der Weg, der stattdessen gilt, muss dastehen.
	if !strings.Contains(text, "{file:/run/secrets/scc-key}") {
		t.Error("der empfohlene Weg über eine Secret-Datei fehlt")
	}
}

// Die Provider-IDs kommen aus dem Bau. Ein fest verdrahteter Name wäre
// hier falsch: welchen Provider ein Bau benutzt, weiß nur der Bau.
func TestDockerfileNenntDieProviderDesBaus(t *testing.T) {
	bau := t.TempDir()
	schreibeBauConfig(t, bau, `{
	  "provider": {
	    "meinprovider": {"options": {"baseURL": "https://beispiel.invalid/v1"}},
	    "anthropic": {}
	  },
	  "enabled_providers": ["meinprovider", "anthropic"]
	}`)

	text := dockerfileGeruest("1.2.3", providerBefund(bau))
	if !strings.Contains(text, "meinprovider (custom)") || !strings.Contains(text, "anthropic (built-in)") {
		t.Errorf("Provider des Baus fehlen im Text:\n%s", text)
	}
	// Der custom Provider gewinnt als Beispiel: nur er hat den
	// options-Block, in den der Schlüssel-Verweis gehört.
	if !strings.Contains(text, "{file:/run/secrets/meinprovider-key}") {
		t.Error("der custom Provider ist nicht das Beispiel")
	}
	if strings.Contains(text, "<provider-id>") {
		t.Error("Platzhalter steht noch drin, obwohl der Bau Provider kennt")
	}
}

// Der leere Fall darf nicht in eine leere Zeile münden: ein Bau ohne
// Provider ist ein Bau vor Schritt 2, und das gehört gesagt.
func TestDockerfileOhneProviderSagtWasFehlt(t *testing.T) {
	text := dockerfileGeruest("1.2.3", providerBefund(t.TempDir()))
	if !strings.Contains(text, "does not define a provider yet") {
		t.Errorf("kein Hinweis auf den fehlenden Provider:\n%s", text)
	}
	if !strings.Contains(text, "{file:/run/secrets/<provider-id>-key}") {
		t.Error("Platzhalter fehlt")
	}
}

// Ohne opencode im PATH wird nicht geraten — die Zeile bleibt ungepinnt
// und sagt, warum. Eine falsche Version stünde sonst unauffällig da.
func TestDockerfileOhneOpencodePinntNicht(t *testing.T) {
	mit := dockerfileGeruest("1.15.13", providerbefund{Beispiel: "p"})
	if !strings.Contains(mit, "ENV OPENCODE_VERSION=1.15.13") {
		t.Error("gepinnte Fassung fehlt")
	}
	ohne := dockerfileGeruest("", providerbefund{Beispiel: "p"})
	if strings.Contains(ohne, "\nENV OPENCODE_VERSION=") {
		t.Error("ohne bekannte Fassung darf keine gesetzt werden")
	}
	if !strings.Contains(ohne, "# ENV OPENCODE_VERSION=") {
		t.Error("der Hinweis, selbst zu pinnen, fehlt")
	}
}

// Zwei Fallen, beide am 2026-08-20 im echten Container GEMESSEN und
// nicht angenommen (Hasenbau-sss):
//
//	bwrap ohne --security-opt seccomp=unconfined:
//	  "No permissions to create new namespace" — und damit kein Werkzeug.
//	git auf dem gemounteten Bau ohne safe.directory:
//	  "detected dubious ownership", woraufhin `describe bau` einen Bau
//	  "without a commit" meldet, der in Wahrheit welche hat.
//
// Beide Zeilen sehen aus wie Beiwerk und sind es nicht. Dieser Test ist
// dagegen, dass eine davon beim Aufräumen fällt.
func TestDockerfileHatDieBeidenGemessenenZeilen(t *testing.T) {
	text := dockerfileGeruest("1.2.3", providerbefund{Beispiel: "p"})

	if !strings.Contains(text, "RUN git config --global --add safe.directory "+containerBau+"\n") {
		t.Error("safe.directory fehlt — git sieht den gemounteten Bau dann nicht an")
	}
	if !strings.Contains(text, "seccomp=unconfined") {
		t.Error("der Hinweis auf die Namespace-Erlaubnis fehlt")
	}
	// Der Bau liegt an drei Stellen, und sie müssen übereinstimmen:
	// ein safe.directory auf einem anderen Pfad wirkt gar nicht.
	if !strings.Contains(text, "WORKDIR "+containerBau+"\n") ||
		!strings.Contains(text, "\"$PWD\":"+containerBau+" ") {
		t.Errorf("der Mount-Pfad %q steht nicht überall gleich", containerBau)
	}
}

// Die Compose-Datei muss YAML SEIN, nicht nur so aussehen. Der
// Platzhalter-Fall ist der interessante: `<provider-id>-key` als
// Schlüssel steht in einer Sprache, in der spitze Klammern sonst etwas
// bedeuten könnten.
func TestComposeIstGueltigesYAML(t *testing.T) {
	for _, p := range []providerbefund{
		{Beispiel: "<provider-id>"},
		{Zeile: "scc (custom)", Beispiel: "scc"},
	} {
		var doc struct {
			Services map[string]struct {
				Build       string            `yaml:"build"`
				Restart     string            `yaml:"restart"`
				Init        bool              `yaml:"init"`
				SecurityOpt []string          `yaml:"security_opt"`
				Volumes     []string          `yaml:"volumes"`
				Environment map[string]string `yaml:"environment"`
				Secrets     []string          `yaml:"secrets"`
			} `yaml:"services"`
			Secrets map[string]struct {
				File string `yaml:"file"`
			} `yaml:"secrets"`
		}
		text := composeGeruest(p)
		if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
			t.Fatalf("Beispiel %q: kein gültiges YAML: %v\n%s", p.Beispiel, err, text)
		}

		s, da := doc.Services[dienst]
		if !da {
			t.Fatalf("Beispiel %q: kein Dienst %q", p.Beispiel, dienst)
		}
		// Die drei Zusagen, die im Kopf des Dockerfiles als "nicht
		// optional" stehen — hier als Konfiguration.
		if len(s.SecurityOpt) != 1 || s.SecurityOpt[0] != "seccomp:unconfined" {
			t.Errorf("security_opt: %v — ohne das registriert das Plugin kein Werkzeug", s.SecurityOpt)
		}
		if s.Environment["TZ"] == "" {
			t.Error("kein TZ — die cron-Trigger liefen in UTC")
		}
		if len(s.Volumes) != 1 || !strings.HasSuffix(s.Volumes[0], ":"+containerBau) {
			t.Errorf("volumes: %v — der Bau muss auf %s liegen", s.Volumes, containerBau)
		}

		// Das Secret muss an beiden Enden denselben Namen tragen, sonst
		// meldet compose einen undefinierten Verweis.
		key := p.Beispiel + "-key"
		if len(s.Secrets) != 1 || s.Secrets[0] != key {
			t.Errorf("Dienst verlangt %v, erwartet [%s]", s.Secrets, key)
		}
		if _, da := doc.Secrets[key]; !da {
			t.Errorf("Secret %q ist nicht deklariert (deklariert: %v)", key, doc.Secrets)
		}
		// Der Pfad, auf den das Werkzeug im Bau zeigt, muss im Text
		// stehen — sonst rät der Nutzer, wohin apiKey zeigen soll.
		if !strings.Contains(text, "{file:/run/secrets/"+key+"}") {
			t.Error("der apiKey-Pfad fehlt")
		}
	}
}

// Ein Aufruf, zwei Dateien — und dieselbe Zusage wie bei den anderen
// `new`-Ressourcen: eine handgeschriebene Datei zu ersetzen bleibt eine
// bewusste Handlung. Die zweite wird trotzdem nachgelegt, denn wer das
// Dockerfile angefasst hat, will deshalb nicht auf die Compose-Datei
// verzichten.
func TestNewDockerfileUeberschreibtNieUndLegtFehlendesNach(t *testing.T) {
	bau := t.TempDir()
	var out, errw strings.Builder
	if code := run([]string{"-bau", bau, "new", "dockerfile"}, &out, &errw); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errw.String())
	}
	for _, name := range []string{DockerfileName, ComposeName} {
		if _, err := os.Stat(filepath.Join(bau, name)); err != nil {
			t.Errorf("%s fehlt: %v", name, err)
		}
		if !strings.Contains(out.String(), "created: "+name) {
			t.Errorf("Ausgabe nennt %s nicht", name)
		}
	}

	// Nur die Compose-Datei löschen: der zweite Aufruf muss sie neu
	// anlegen und das Dockerfile in Ruhe lassen.
	vorher, err := os.ReadFile(filepath.Join(bau, DockerfileName))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(bau, ComposeName)); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errw.Reset()
	if code := run([]string{"-bau", bau, "new", "dockerfile"}, &out, &errw); code != 0 {
		t.Fatalf("zweiter Aufruf: exit %d, stderr %q", code, errw.String())
	}
	if !strings.Contains(out.String(), "created: "+ComposeName) {
		t.Errorf("die fehlende Datei wurde nicht nachgelegt:\n%s", out.String())
	}
	if !strings.Contains(errw.String(), DockerfileName+" already exists") {
		t.Errorf("die vorhandene Datei wurde nicht gemeldet: %q", errw.String())
	}
	nachher, err := os.ReadFile(filepath.Join(bau, DockerfileName))
	if err != nil {
		t.Fatal(err)
	}
	if string(vorher) != string(nachher) {
		t.Error("das vorhandene Dockerfile wurde verändert")
	}

	// Und wenn beide dastehen, ist nichts zu tun — derselbe Exit wie
	// bei den anderen `new`-Ressourcen.
	out.Reset()
	errw.Reset()
	if code := run([]string{"-bau", bau, "new", "dockerfile"}, &out, &errw); code == 0 {
		t.Error("exit 0, obwohl beide Dateien schon dastanden")
	}
}

// `new dockerfile` nimmt keinen Namen — die Datei heißt immer so.
func TestNewDockerfileNimmtKeinenNamen(t *testing.T) {
	var out, errw strings.Builder
	if code := run([]string{"-bau", t.TempDir(), "new", "dockerfile", "meins"}, &out, &errw); code != 2 {
		t.Fatalf("exit %d, erwartet 2", code)
	}
	if !strings.Contains(errw.String(), "takes no name") {
		t.Errorf("Meldung erklärt es nicht: %q", errw.String())
	}
}

// Der ganze Weg einmal durch, samt der Ausgabe, die den nächsten
// Schritt nennt.
func TestNewDockerfileSchreibtUndErklaert(t *testing.T) {
	bau := t.TempDir()
	var out, errw strings.Builder
	if code := run([]string{"-bau", bau, "new", "dockerfile"}, &out, &errw); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errw.String())
	}
	roh, err := os.ReadFile(filepath.Join(bau, DockerfileName))
	if err != nil {
		t.Fatal(err)
	}
	text := string(roh)
	for _, muss := range []string{
		"FROM debian:stable-slim",
		"go install github.com/Moritz-Dorn/Hasenbau/cmd/hasenbau@latest",
		"COPY --from=build /go/bin/hasenbau /usr/local/bin/hasenbau",
		"ENTRYPOINT [\"hasenbau\"]",
		"seccomp=unconfined",  // ohne das kein einziges Werkzeug
		"What YOUR Bau needs", // der Block, der dem Nutzer gehört
	} {
		if !strings.Contains(text, muss) {
			t.Errorf("erzeugte Datei enthält nicht %q", muss)
		}
	}
	if !strings.Contains(out.String(), "created: "+DockerfileName) ||
		!strings.Contains(out.String(), "docker compose up -d") {
		t.Errorf("Ausgabe hilft nicht weiter:\n%s", out.String())
	}
}

func schreibeBauConfig(t *testing.T, bau, inhalt string) {
	t.Helper()
	dir := filepath.Join(bau, ".opencode-home", "opencode")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "opencode.json"), []byte(inhalt), 0o644); err != nil {
		t.Fatal(err)
	}
}
