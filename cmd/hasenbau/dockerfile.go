// dockerfile.go beantwortet die Frage, die das README bisher als
// Fließtext beantwortet hat: **was muss installiert sein, damit ein Bau
// läuft?** (Hasenbau-sss)
//
// Die Liste ist deshalb interessant, weil ihr Fehlen still ist. Ohne
// `bwrap` registriert das Plugin kein einziges Werkzeug — fail-closed,
// und nur der Daemon-Log sagt es. Ohne Git-Commit bekommt der Bau keine
// Projekt-ID und die Raum-Permissions greifen nicht (§11.5). Ohne
// `tzdata` nimmt `cron.New()` die Zeitzone des Containers, und das ist
// UTC: ein `0 10 * * *` feuert dann um zwölf.
//
// Grenze, und sie ist bewusst: hier steht nur, was **Hasenbau** ruft.
// Was der Bau ruft — `pdftotext` für einen Gang, ein MCP-Server —
// kennt der Hasenbau nicht und rät es auch nicht. Dafür trägt die
// erzeugte Datei einen markierten Block am Ende.
package main

import (
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"unicode/utf8"

	"github.com/Moritz-Dorn/Hasenbau/internal/provider"
)

// DockerfileName ist der Ort im Bau. Ein fester Name, kein Argument:
// Docker sucht ihn so, und zwei Dockerfiles in einem Bau wären eine
// Frage, die niemand stellen wollte.
const DockerfileName = "Dockerfile"

// goFallback greift, wenn runtime.Version() nicht wie `go1.25.5`
// aussieht (Tip-Builds melden Commit-Hashes).
const goFallback = "1.25"

// containerBau ist der Ort, an den der Bau gehängt wird. Er steht an
// drei Stellen der erzeugten Datei (WORKDIR, safe.directory, die
// Beispielzeilen) und muss überall derselbe sein — ein Bau an einem
// anderen Pfad als safe.directory ist wieder der Fall, den der
// Eintrag gerade verhindern soll.
const containerBau = "/bau"

// fremdprogramm ist ein Programm, das der Hasenbau ruft, samt dem
// Debian-Paket, das es mitbringt, und dem Grund.
//
// Diese Liste ist die einzige Fassung — dockerfile_test.go liest die
// `exec`-Aufrufe des Quellbaums und verlangt, dass jeder gefundene
// Befehl hier steht. Wer später ein `jq` einbaut, bekommt einen roten
// Test statt eines Images, in dem es fehlt.
type fremdprogramm struct {
	// Befehl ist der Name im PATH, so wie der Code ihn ruft. Leer:
	// kein direkter Aufruf, das Paket wird trotzdem gebraucht.
	Befehl string
	// Paket ist das Debian-Paket. Leer: in der Basis enthalten oder
	// mit eigenem Installer (opencode).
	Paket string
	// Grund steht als Kommentar in der erzeugten Datei. Englisch —
	// die Datei geht in einen Bau (AGENTS.md, Hasenbau-tzl).
	Grund string
}

var fremdprogramme = []fremdprogramm{
	{Befehl: "opencode", Grund: "the server the daemon starts; installed below, it brings its own installer"},
	{Befehl: "git", Paket: "git", Grund: "a Bau is a Git repo — without a root commit opencode gives it no project ID and the Raum permissions do not bite"},
	{Befehl: "bwrap", Paket: "bubblewrap", Grund: "the sandbox around every Schmied tool. Missing it, the plugin registers NO tool at all"},
	{Befehl: "sh", Grund: "every Gang runs as `sh -c`, and dash is already in the base image"},
	{Befehl: "python3", Paket: "python3", Grund: "the Baumeister checks the syntax of the Gänge it writes"},
	{Paket: "ca-certificates", Grund: "HTTPS to your model provider"},
	{Paket: "tzdata", Grund: "without it cron triggers run in UTC, so `0 10 * * *` fires at 12"},
	{Paket: "curl", Grund: "only for the opencode installer below"},
	{Paket: "tar", Grund: "the opencode installer unpacks a .tar.gz on Linux"},
}

func newDockerfile(root string, args []string, out, errw io.Writer) int {
	if len(args) != 0 {
		fmt.Fprintf(errw, "hasenbau new dockerfile: takes no name — the file is always %s\n\n%s",
			DockerfileName, newUsage)
		return 2
	}

	fassung := opencodeFassung()
	befund := providerBefund(root)
	if code := writeNew(root, DockerfileName, dockerfileGeruest(fassung, befund), errw); code != 0 {
		return code
	}

	fmt.Fprintf(out, "created: %s\n\n", DockerfileName)
	if fassung != "" {
		fmt.Fprintf(out, "opencode pinned to %s — the version in your PATH.\n", fassung)
	} else {
		fmt.Fprint(out, "opencode is not in your PATH, so the installer line is unpinned.\n"+
			"Set OPENCODE_VERSION yourself if you want a reproducible image.\n")
	}
	fmt.Fprint(out, "\nNext:\n"+
		"  1. Read it. Three `docker run` flags in the header are not optional,\n"+
		"     and each of them fails quietly when it is missing.\n"+
		"  2. Add what YOUR Gänge call — the marked block at the end.\n"+
		"  3. docker build -t meinbau - < "+DockerfileName+"\n"+
		"  4. docker run --rm -v \"$PWD\":"+containerBau+" meinbau describe bau\n")
	return 0
}

// opencodeFassung liest die Version des opencode im PATH. Leer heißt:
// keins da oder es antwortet anders als erwartet — dann bleibt die
// Installer-Zeile ungepinnt, und der Befehl sagt das auch. Raten wäre
// hier schlimmer als nicht pinnen: eine falsche Version stünde
// unauffällig in der Datei.
func opencodeFassung() string {
	roh, err := exec.Command("opencode", "--version").Output()
	if err != nil {
		return ""
	}
	v := strings.TrimSpace(string(roh))
	if !regexp.MustCompile(`^\d+\.\d+\.\d+`).MatchString(v) {
		return ""
	}
	return v
}

// goFassung ist die Go-Minor-Version dieses Binaries — sie wird zum
// Tag des Builder-Images. Damit altert die erzeugte Datei nicht
// schneller als der Hasenbau selbst.
func goFassung() string {
	m := regexp.MustCompile(`^go(\d+\.\d+)`).FindStringSubmatch(runtime.Version())
	if m == nil {
		return goFallback
	}
	return m[1]
}

// provider Befund ist, was der Generator über die Provider DIESES Baus
// weiß. Beispiel ist die ID, mit der die Credential-Kommentare
// geschrieben werden — kein fest verdrahteter Name, denn welcher
// Provider ein Bau benutzt, weiß nur der Bau.
type providerbefund struct {
	Zeile    string // "scc (custom), github-copilot (built-in)" oder leer
	Beispiel string // ID für die Beispielpfade; "<provider-id>", wenn keiner da ist
}

func providerBefund(root string) providerbefund {
	befund := providerbefund{Beispiel: "<provider-id>"}
	conf, err := provider.LoadConfig(root)
	if err != nil {
		return befund
	}
	var teile []string
	var erster, ersterCustom string
	for _, e := range conf.List() {
		art := "built-in"
		if e.BaseURL != "" {
			art = "custom"
			if ersterCustom == "" {
				ersterCustom = e.ID
			}
		}
		if erster == "" {
			erster = e.ID
		}
		teile = append(teile, e.ID+" ("+art+")")
	}
	// Ein custom Provider ist der interessantere Fall: nur er hat den
	// handgepflegten options-Block, in den der Schlüssel-Verweis
	// gehört. Deshalb gewinnt der erste davon.
	switch {
	case ersterCustom != "":
		befund.Beispiel = ersterCustom
	case erster != "":
		befund.Beispiel = erster
	}
	befund.Zeile = strings.Join(teile, ", ")
	return befund
}

// dockerfileGeruest baut den Text. Englisch, weil die Datei in einen
// Bau geht (AGENTS.md), und kommentiert, weil eine Zeile, deren Fehlen
// still wirkt, ihren Grund neben sich tragen muss.
func dockerfileGeruest(opencodeVersion string, p providerbefund) string {
	var b strings.Builder

	b.WriteString("# Dockerfile — written by `hasenbau new dockerfile`.\n" +
		"#\n" +
		"# In here is what HASENBAU needs. What YOUR Bau needs — the programs\n" +
		"# your Gänge call, an MCP server, a model CLI — is not: Hasenbau does\n" +
		"# not know them and does not guess. The marked block at the end is\n" +
		"# yours.\n" +
		"#\n" +
		"#   docker build -t meinbau - < Dockerfile\n" +
		"#\n" +
		"# Nothing is copied from the build context, so do not send one:\n" +
		"# `- < Dockerfile` keeps your Bau out of the daemon.\n" +
		"#\n" +
		"#   docker run --rm -it \\\n" +
		"#     --security-opt seccomp=unconfined \\\n" +
		"#     -v \"$PWD\":" + containerBau + " \\\n" +
		"#     --mount type=bind,src=\"$HOME/.secrets/" + p.Beispiel + "-key\",dst=/run/secrets/" + p.Beispiel + "-key,ro \\\n" +
		"#     -e TZ=Europe/Berlin \\\n" +
		"#     meinbau daemon\n" +
		"#\n" +
		"# Three of those are not optional, and each one fails quietly:\n" +
		"#\n" +
		"#   seccomp=unconfined  bwrap needs unprivileged user namespaces. Without\n" +
		"#                       them the plugin registers NO tool — the Hasen simply\n" +
		"#                       do not get them, and only the daemon log says so.\n" +
		"#   the secret mount    see Credentials below.\n" +
		"#   TZ                  cron triggers run in the container's timezone, and\n" +
		"#                       that is UTC unless you name one. Write it out —\n" +
		"#                       reading it off the host is not portable, and an\n" +
		"#                       empty TZ is UTC again without saying so.\n" +
		"\n")

	b.WriteString(credentialsBlock(p))

	b.WriteString("\n" +
		"# ---------------------------------------------------------------------\n" +
		"# Build\n" +
		"# ---------------------------------------------------------------------\n" +
		"\n" +
		"FROM golang:" + goFassung() + "-bookworm AS build\n" +
		"# modernc.org/sqlite is pure Go, so there is no cgo and the binary is\n" +
		"# static — that is what makes the slim image below enough.\n" +
		"ENV CGO_ENABLED=0\n" +
		"RUN go install github.com/Moritz-Dorn/Hasenbau/cmd/hasenbau@latest\n" +
		"\n" +
		"# opencode is a bun-compiled binary and wants glibc — do not swap this\n" +
		"# for Alpine without testing it.\n" +
		"FROM debian:stable-slim\n" +
		"\n")

	b.WriteString(paketBlock())

	b.WriteString("\nCOPY --from=build /go/bin/hasenbau /usr/local/bin/hasenbau\n\n")

	if opencodeVersion != "" {
		b.WriteString("# Pinned to the opencode that ran on the machine which wrote this\n" +
			"# file. A Bau is only reproducible if its server is.\n" +
			"ENV OPENCODE_VERSION=" + opencodeVersion + "\n" +
			"RUN curl -fsSL https://opencode.ai/install | VERSION=$OPENCODE_VERSION bash\n")
	} else {
		b.WriteString("# Unpinned: there was no opencode in the PATH when this file was\n" +
			"# written, so nothing could be read off. Set a version here — a Bau\n" +
			"# is only reproducible if its server is.\n" +
			"# ENV OPENCODE_VERSION=1.15.13\n" +
			"RUN curl -fsSL https://opencode.ai/install | bash\n")
	}

	b.WriteString("ENV PATH=/root/.opencode/bin:$PATH\n" +
		"\n" +
		"# The Bau arrives as a mount and belongs to the user on the host, not\n" +
		"# to root in here. Without this line git refuses to look at it\n" +
		"# (\"dubious ownership\") and Hasenbau then reports a Bau \"without a\n" +
		"# commit\" — while the real one has plenty. That is not cosmetic: no\n" +
		"# commit visible means no project ID, and the Raum permissions do not\n" +
		"# take effect (PLAN.md §11.5).\n" +
		"RUN git config --global --add safe.directory " + containerBau + "\n" +
		"\n" +
		"WORKDIR " + containerBau + "\n" +
		"ENTRYPOINT [\"hasenbau\"]\n" +
		"CMD [\"daemon\"]\n" +
		"\n")

	b.WriteString(eigenerBlock())
	return b.String()
}

// paketBlock schreibt die apt-Zeile und über jedem Paket den Grund.
// Beides aus derselben Liste: ein Paket ohne Grund gäbe es dann gar
// nicht erst.
func paketBlock() string {
	var b strings.Builder
	b.WriteString("# Every line below is something Hasenbau itself calls or needs.\n")

	breite := 0
	for _, f := range fremdprogramme {
		if n := len(f.Paket); n > breite {
			breite = n
		}
	}
	var pakete []string
	for _, f := range fremdprogramme {
		name, grund := f.Paket, f.Grund
		if name == "" {
			// Ohne Paket bleibt der Grund trotzdem stehen: dass `sh`
			// schon da ist und opencode seinen eigenen Weg geht, ist
			// eine Antwort auf eine Frage, die sonst jemand stellt.
			// Dass es NICHT in der apt-Zeile steht, muss dabei
			// dranstehen — sonst liest es sich wie ein Vergessen.
			name, grund = f.Befehl, "not from apt — "+grund
		} else {
			pakete = append(pakete, name)
		}
		b.WriteString(umbrochen("#   ", name, breite, grund))
	}

	b.WriteString("RUN apt-get update \\\n" +
		" && apt-get install -y --no-install-recommends \\\n")
	for _, p := range pakete {
		b.WriteString("      " + p + " \\\n")
	}
	b.WriteString(" && rm -rf /var/lib/apt/lists/*\n")
	return b.String()
}

// umbrochen setzt `name` in eine Spalte und bricht `grund` daneben um,
// Folgezeilen eingerückt. Ohne das wird die längste Begründung zur
// Zeilenlänge der ganzen Datei — und eine Datei, die man seitwärts
// scrollen muss, liest niemand zu Ende.
func umbrochen(prefix, name string, breite int, grund string) string {
	const zeilenlaenge = 74
	kopf := fmt.Sprintf("%s%-*s  ", prefix, breite, name)
	// Folgezeilen bleiben Kommentar und rücken unter die Begründung.
	folge := "#" + strings.Repeat(" ", utf8.RuneCountInString(kopf)-1)

	var b strings.Builder
	b.WriteString(kopf)
	spalte := utf8.RuneCountInString(kopf)
	for i, wort := range strings.Fields(grund) {
		breiteWort := utf8.RuneCountInString(wort)
		switch {
		case i == 0:
		case spalte+1+breiteWort > zeilenlaenge:
			b.WriteString("\n" + folge)
			spalte = utf8.RuneCountInString(folge)
		default:
			b.WriteString(" ")
			spalte++
		}
		b.WriteString(wort)
		spalte += breiteWort
	}
	b.WriteString("\n")
	return b.String()
}

// credentialsBlock ist der Teil, der falsch zu machen teuer ist. Er
// steht deshalb weit oben und sagt zuerst, was man NICHT tun soll.
func credentialsBlock(p providerbefund) string {
	var b strings.Builder
	b.WriteString("# ---------------------------------------------------------------------\n" +
		"# Credentials\n" +
		"# ---------------------------------------------------------------------\n" +
		"#\n" +
		"# Never put a key in this file. COPY and ENV end up in the image layers\n" +
		"# and stay readable with `docker history` — removing them later does not\n" +
		"# remove them.\n" +
		"#\n" +
		"# opencode does not need auth.json at all: options.apiKey understands\n" +
		"# {file:PATH} and {env:VAR}. So the key can arrive as a mounted file and\n" +
		"# your Bau config stays free of secrets — which is what it promises\n" +
		"# anyway (PLAN.md §3).\n" +
		"#\n")
	if p.Zeile != "" {
		b.WriteString("# Your Bau knows: " + p.Zeile + "\n#\n")
	} else {
		b.WriteString("# Your Bau does not define a provider yet — see step 2 of the README.\n" +
			"# Until then <provider-id> below is a placeholder.\n#\n")
	}
	b.WriteString("# In .opencode-home/opencode/opencode.json:\n" +
		"#\n" +
		"#   \"provider\": {\"" + p.Beispiel + "\": {\"options\": {\n" +
		"#      \"apiKey\": \"{file:/run/secrets/" + p.Beispiel + "-key}\"}}}\n" +
		"#\n" +
		"# Two other ways, each with its price:\n" +
		"#\n" +
		"#   A  Share the whole data directory, auth.json as usual:\n" +
		"#        -v \"$HOME/.local/share/opencode\":/root/.local/share/opencode\n" +
		"#      That also shares opencode.db, storage/, log/ and tool-output/ with\n" +
		"#      your everyday opencode. It is the only way that keeps an OAuth\n" +
		"#      login refreshable inside the container — those entries get\n" +
		"#      rewritten, which a single-file mount does not survive.\n" +
		"#\n" +
		"#   B  An environment variable, \"apiKey\": \"{env:" + strings.ToUpper(strings.NewReplacer("-", "_", ".", "_").Replace(p.Beispiel)) + "_API_KEY}\":\n" +
		"#        --env-file ~/.secrets/bau.env\n" +
		"#      The key is then in `docker inspect` and in the environment of every\n" +
		"#      process in the container, a Schmied tool included.\n" +
		"#\n" +
		"# `hasenbau provider fetch` and `get provider` read auth.json directly.\n" +
		"# With the mounted-file route they report \"no auth.json\" while the Läufe\n" +
		"# run fine — fetch the model list on the host, it is versioned Bau\n" +
		"# content anyway.\n")
	return b.String()
}

// eigenerBlock ist die Stelle, an der der Nutzer weitermacht. Er steht
// am Ende, weil eine RUN-Zeile hier keine der Zeilen darüber
// invalidiert — und er ist kommentiert, weil ein Bau ohne eigene
// Abhängigkeiten sonst eine kaputte Zeile erbte.
func eigenerBlock() string {
	return "# ---------------------------------------------------------------------\n" +
		"# What YOUR Bau needs\n" +
		"# ---------------------------------------------------------------------\n" +
		"#\n" +
		"# Hasenbau knows its own dependencies, not the ones your Gänge call. A\n" +
		"# Gang that misses its program fails, and its input goes to quarantaene/\n" +
		"# rather than through (PLAN.md §7). So it belongs here:\n" +
		"#\n" +
		"# RUN apt-get update \\\n" +
		"#  && apt-get install -y --no-install-recommends \\\n" +
		"#       poppler-utils \\\n" +
		"#  && rm -rf /var/lib/apt/lists/*\n" +
		"#\n" +
		"# (poppler-utils brings pdftotext, which the reference Gang pdf_to_md.py\n" +
		"# calls. Yours will want something else.)\n" +
		"#\n" +
		"# An MCP server that runs through npx needs Node:\n" +
		"#\n" +
		"# RUN apt-get update \\\n" +
		"#  && apt-get install -y --no-install-recommends nodejs npm \\\n" +
		"#  && rm -rf /var/lib/apt/lists/*\n"
}
