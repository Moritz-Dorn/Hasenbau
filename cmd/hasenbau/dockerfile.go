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
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"unicode/utf8"

	"github.com/Moritz-Dorn/Hasenbau/internal/bau"
	"github.com/Moritz-Dorn/Hasenbau/internal/provider"
)

// DockerfileName ist der Ort im Bau. Ein fester Name, kein Argument:
// Docker sucht ihn so, und zwei Dockerfiles in einem Bau wären eine
// Frage, die niemand stellen wollte.
const DockerfileName = "Dockerfile"

// ComposeName ist die zweite Datei. Sie erklärt keine neue Sache,
// sondern macht die drei Flags aus dem Kopf des Dockerfiles zu
// Konfiguration — was man einmal aufschreibt, tippt man nicht jedesmal
// falsch.
const ComposeName = "docker-compose.yml"

// dienst ist der Name des Compose-Dienstes und damit das, was hinter
// `docker compose run --rm …` steht.
const dienst = "hasenbau"

// goFallback greift, wenn runtime.Version() nicht wie `go1.25.5`
// aussieht (Tip-Builds melden Commit-Hashes).
const goFallback = "1.25"

// containerBau ist der Ort, an den der Bau gehängt wird. Er steht an
// drei Stellen der erzeugten Datei (WORKDIR, safe.directory, die
// Beispielzeilen) und muss überall derselbe sein — ein Bau an einem
// anderen Pfad als safe.directory ist wieder der Fall, den der
// Eintrag gerade verhindern soll.
const containerBau = "/bau"

func newDockerfile(root string, args []string, out, errw io.Writer) int {
	if len(args) != 0 {
		fmt.Fprintf(errw, "hasenbau new dockerfile: takes no name — the file is always %s\n\n%s",
			DockerfileName, newUsage)
		return 2
	}

	fassung := opencodeFassung()
	befund := providerBefund(root)

	// Zwei Dateien, eine Sache: das Dockerfile sagt, was drin ist, die
	// Compose-Datei, wie es läuft. Jede wird für sich geschrieben —
	// wer die eine von Hand angefasst hat, soll die andere trotzdem
	// bekommen.
	dateien := []struct{ rel, inhalt string }{
		{DockerfileName, dockerfileGeruest(fassung, befund)},
		{ComposeName, composeGeruest(befund)},
	}
	var geschrieben int
	for _, d := range dateien {
		if _, err := os.Stat(filepath.Join(root, d.rel)); err == nil {
			fmt.Fprintf(errw, "hasenbau new dockerfile: %s already exists — left untouched.\n", d.rel)
			continue
		}
		if code := writeNew(root, d.rel, d.inhalt, errw); code != 0 {
			return code
		}
		fmt.Fprintf(out, "created: %s\n", d.rel)
		geschrieben++
	}
	if geschrieben == 0 {
		// Nichts getan ist hier kein Erfolg: derselbe Exit wie bei den
		// anderen `new`-Ressourcen, wenn die Datei schon dasteht.
		return 1
	}

	fmt.Fprintln(out)
	if fassung != "" {
		fmt.Fprintf(out, "opencode pinned to %s — the version in your PATH.\n", fassung)
	} else {
		fmt.Fprint(out, "opencode is not in your PATH, so the installer line is unpinned.\n"+
			"Set OPENCODE_VERSION yourself if you want a reproducible image.\n")
	}
	fmt.Fprint(out, "\nNext:\n"+
		"  1. Read them. What the Dockerfile header calls not optional, the\n"+
		"     compose file declares — so read the comments once, then use compose.\n"+
		"  2. Add what YOUR Gänge call — the marked block at the end of the Dockerfile.\n"+
		"  3. Put your provider key where "+ComposeName+" expects it, and\n"+
		"     point apiKey at /run/secrets/… — the file says which path.\n"+
		"  4. docker compose run --rm "+dienst+" describe bau\n"+
		"  5. docker compose up -d\n")
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
		"# Everything below is also declared in " + ComposeName + ", which was\n" +
		"# written next to this file. `docker compose up -d` and you are done —\n" +
		"# read this once, then use that.\n" +
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

// composeGeruest schreibt die zweite Datei. Sie erfindet nichts: was
// im Kopf des Dockerfiles als „nicht optional" steht, steht hier als
// Konfiguration — ein Flag, das man aufschreibt, vergisst man nicht.
//
// Alle vier Eigenheiten darin sind am 2026-08-20 an Compose 2.36.0
// gemessen: das Secret landet auf /run/secrets/<name> und ERBT die
// Rechte der Host-Datei; ein `secrets:`-Eintrag auf eine fehlende
// Datei bricht `up` ab; `${TZ:-…}` greift; und `$VAR` in einem
// Compose-String ersetzt Compose, nicht die Shell.
func composeGeruest(p providerbefund) string {
	key := p.Beispiel + "-key"
	return `# ` + ComposeName + ` — written by ` + "`hasenbau new dockerfile`" + `.
#
# The Dockerfile says what is in the image. This says how it runs: the
# three flags its header calls not optional are declared here, so you
# only have to read them once.
#
#   docker compose up -d                             # the daemon, in the background
#   docker compose logs -f                           # what it is doing
#   docker compose down                              # stop it
#   docker compose run --rm ` + dienst + ` describe bau     # check the Bau
#   docker compose run --rm ` + dienst + ` lauf <auftrag>   # one Auftrag by hand
#
# One thing worth doing once: ` + "`build`" + ` sends this whole directory to
# the Docker daemon as context, your archiv/ included — even though the
# Dockerfile copies nothing from it. A .dockerignore containing a single
# ` + "`*`" + ` cuts that to nothing and the build still works. Measured: 200 MB
# of context became 42 bytes.

services:
  ` + dienst + `:
    build: .

    # The daemon is meant to stay up. This is what Restart=always is in
    # the systemd unit (PLAN.md §2).
    restart: unless-stopped

    # hasenbau is PID 1 in here, and it spawns opencode plus every Gang.
    # PID 1 has to reap orphans; init: true puts a reaper in front of it
    # so a Gang whose grandchild outlives it leaves no zombie behind.
    init: true

    # bwrap needs unprivileged user namespaces. Without this it answers
    # "No permissions to create new namespace" — and then the plugin
    # registers NO tool at all, which only the daemon log mentions.
    security_opt:
      - seccomp:unconfined

    volumes:
      # The Bau, read-write: state/hasenbau.db, the Räume and the
      # generated agents all live in it.
      - .:` + containerBau + `

    environment:
      # cron triggers run in this timezone. Unset means UTC, so a
      # ` + "`0 10 * * *`" + ` would fire at 12. Set TZ in your shell or here.
      TZ: ${TZ:-Europe/Berlin}

    secrets:
      - ` + key + `

secrets:
  # Create this file BEFORE the first ` + "`up`" + `, or compose stops with
  #   secret file ... does not exist
  # That is on purpose: a Bau without a key cannot make a Lauf anyway,
  # and a container that starts and then fails every Lauf is worse.
  #
  #   mkdir -p ~/.secrets
  #   printf %s 'your-api-key' > ~/.secrets/` + key + `
  #   chmod 600 ~/.secrets/` + key + `
  #
  # A trailing newline does no harm — opencode trims the file, and so
  # does ` + "`hasenbau provider fetch`" + `. ` + "`printf %s`" + ` just keeps the file
  # honest about what is in it.
  #
  # The permissions of the host file are carried over unchanged, so the
  # chmod above sticks. Inside, the file appears as
  # /run/secrets/` + key + `, which is the path your Bau config
  # points at:
  #
  #   "provider": {"` + p.Beispiel + `": {"options": {
  #      "apiKey": "{file:/run/secrets/` + key + `}"}}}
  ` + key + `:
    file: ${HOME}/.secrets/` + key + `

# Instead of the secret you can share your whole opencode data directory,
# auth.json as usual. It is the only way that keeps an OAuth login
# refreshable in here, and it also shares opencode.db, storage/ and log/
# with your everyday opencode:
#
#     volumes:
#       - .:` + containerBau + `
#       - ${HOME}/.local/share/opencode:/root/.local/share/opencode
`
}

// paketBlock schreibt die apt-Zeile und über jedem Paket den Grund.
// Beides aus derselben Liste: ein Paket ohne Grund gäbe es dann gar
// nicht erst.
func paketBlock() string {
	var b strings.Builder
	b.WriteString("# Every line below is something Hasenbau itself calls or needs.\n")

	breite := 0
	for _, f := range bau.ExternalPrograms {
		if n := len(f.Package); n > breite {
			breite = n
		}
	}
	var pakete []string
	for _, f := range bau.ExternalPrograms {
		name, grund := f.Package, f.Why
		if name == "" {
			// Ohne Paket bleibt der Grund trotzdem stehen: dass `sh`
			// schon da ist und opencode seinen eigenen Weg geht, ist
			// eine Antwort auf eine Frage, die sonst jemand stellt.
			// Dass es NICHT in der apt-Zeile steht, muss dabei
			// dranstehen — sonst liest es sich wie ein Vergessen.
			name, grund = f.Command, "not from apt — "+grund
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
