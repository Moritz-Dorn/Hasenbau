# Hasenbau 🐇: the rabbits work at night, you read up in the morning

[![license](https://img.shields.io/badge/license-EUPL--1.2-blue?style=flat-square)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.25%2B-00ADD8?style=flat-square&logo=go&logoColor=white)](go.mod)
[![platform](https://img.shields.io/badge/platform-Linux-lightgrey?style=flat-square)](#install)

**English** · [Deutsch](README.de.md)

A daemon that orchestrates [opencode](https://opencode.ai) headless:
scheduled and file-triggered agent jobs with deterministic
preprocessing, local, one binary, no cloud service. For recurring work
that nobody should have to sit through.

An Auftrag (job) is a file. This one waits for PDFs, converts them to
Markdown without a model, and only hands the result to the Hase (the
agent):

```yaml
trigger:  {watch: "*.pdf", debounce: 5s}
gaenge:   [{name: pdf-zu-markdown, run: 'python3 gaenge/pdf_to_md.py "$TRIGGER_FILE" --out "$WORK/extrakt.md"'}]
hase:     archivar
raeume:   {input: raeume/laderampe/sources/, work: raeume/laderampe/work/, out: raeume/lager/}
context:  [{file: $WORK/extrakt.md}, {last_summaries: 3}]
after:    [{move: $TRIGGER_FILE -> raeume/archiv/}]
```

The full version lives in
[`beispiele/`](beispiele/auftraege/pdf-einlagern.md) and runs
end to end. How the parts fit together is in
[docs/architecture.md](docs/architecture.md), the reasoning in
[`PLAN.md`](PLAN.md).

The vocabulary stays German, in this document as well as in the code and
in the CLI: Bau, Raum, Hase, Gang, Auftrag, Lauf. There is a glossary
[below](#vocabulary). Everything around those words is English —
`hasenbau get laeufe` is spelled that way and prints its table in
English, so the sample output in this README is verbatim.

## Install

`opencode` has to be in the PATH, `bwrap` (bubblewrap) for the tool
sandbox, `pdftotext` (poppler) for the reference Auftrag.

```bash
go install github.com/Moritz-Dorn/Hasenbau/cmd/hasenbau@latest
```

That puts the binary in `$(go env GOPATH)/bin`, usually `~/go/bin` —
that directory has to be in the PATH.

Once you have a Bau, `hasenbau new dockerfile` writes a `Dockerfile`
into it that carries all of this: the programs above, plus the ones
whose absence is silent rather than loud — without `bwrap` no Schmied
tool is registered at all, without `tzdata` your cron triggers run in
UTC. It installs what **Hasenbau** needs; what your Gänge call is a
marked block at the end, for you to fill in. Details in
[Commands](docs/commands.md#in-a-container).

## Your first Bau

A Bau lives outside this repository: a Hase reads the `AGENTS.md` in its
working directory, and the one here is about building Hasenbau, not
about filing PDFs.

### 1. Create the Bau

```bash
hasenbau init ~/meinbau
cd ~/meinbau
```

That creates the layout, writes the two special Hasen (Baumeister and
Schmied, each as an Auftrag and a Hase) plus the sandbox guard, turns the
Bau into a Git repository with a root commit, and registers the back
channel in the Bau config. Every further command needs the Bau: either
`-bau ~/meinbau` in front of the subcommand or one `cd`, since the
default is the current directory.

The guard is the one file in the Bau that Hasenbau keeps for itself: it
is rewritten from the binary whenever it differs, so an upgrade of
Hasenbau reaches an existing Bau as well. Everything else stays as you
left it. Details in [Architecture](docs/architecture.md).

### 2. Register a provider

A Bau brings its own providers; `auth.json` only shares the keys, not the
definitions. The scaffold goes into the `provider:` block of
`.opencode-home/opencode/opencode.json` by hand. Template and reasoning
are in
[docs/architecture.md](docs/architecture.md#providers-in-a-bau); the
model list is then fetched by `hasenbau provider fetch <id>`.

### 3. Take over the reference Auftrag

```bash
cp -f <hasenbau-repo>/beispiele/auftraege/pdf-einlagern.md auftraege/
cp -f <hasenbau-repo>/beispiele/hasen/archivar.md          hasen/
cp -f <hasenbau-repo>/beispiele/gaenge/pdf_to_md.py        gaenge/
```

One thing is left to do by hand: the `model:` in `hasen/archivar.md` has
to point at a model the Bau knows about from step 2.

### 4. Check that everything stands

```bash
hasenbau describe bau
```

Exactly one item may be open here:

```
CHECK   Agents         not generated: pdf-einlagern__archivar
                       → the next daemon or Lauf start writes them
```

That is the order of things: Hasenbau generates the agents when it loads
the definitions. Layout, Git commit, Bau config and back channel binary
have to be `ok`; those are the things you otherwise only notice from a
Lauf that looks odd.

### 5. The first Lauf, by hand

A targeted Lauf first, triggers afterwards, so that you see the mistake
in the Auftrag rather than in the daemon:

```bash
mkdir -p raeume/laderampe/sources
cp -f ~/irgendwas.pdf raeume/laderampe/sources/
hasenbau lauf pdf-einlagern raeume/laderampe/sources/irgendwas.pdf
```

The `mkdir` is only needed the very first time: `raeume/` is empty after
`init`, and a Lauf creates what the Auftrag names. The second argument is
the trigger, meaning the file the watcher would otherwise have found
(`$TRIGGER_FILE`), relative to the Bau like the working directory of the
Gänge.

### 6. Look at what happened

```bash
hasenbau get laeufe            # one line per Lauf: status, duration, cost
hasenbau describe lauf <id>    # notes from the back channel, errors, tool calls
```

If a Lauf went wrong, the reason is in `describe lauf`, and the `$WORK`
directory is left behind on purpose, with the log of every Gang in it.
From then on `describe bau` counts such leftovers as an open item: they
are remains to look at.

## Vocabulary

| Term | Meaning |
|---|---|
| Bau | Root directory of the system |
| Raum | Directory in the material flow (`laderampe/`, `lager/`, `archiv/`, `quarantaene/`) |
| Gang | Deterministic script, runs before the Hase. No LLM |
| Hase | Template in `hasen/`; from it the daemon generates one opencode agent per Auftrag×Hase, permissions come from the Räume of the Auftrag |
| Auftrag | Trigger + Gänge + Hase + Räume |
| Lauf | One execution of an Auftrag |

A "Bau" is an instance created by `hasenbau init`, not this repository.

## Day to day

`hasenbau daemon` arms the triggers (cron + watch) and runs in the
foreground, with the log on stderr. It stops on Ctrl-C or `SIGTERM`,
reports `shut down cleanly` and exits with 0. A Lauf that is in the middle
of its work is closed as `aborted`; its `$WORK` directory stays, and
`describe bau` reminds you of it later.

The daemon reads the definitions at startup. Change anything in
`auftraege/`, `hasen/` or `hasenbau.yaml` and you restart it; material in
the Räume is unaffected, since that is the trigger.

A watch Auftrag fires **one Lauf per file**, and its pattern is how you
steer that: `watch: "*.pdf"` sees the input Raum flat, `watch:
"**/*.pdf"` takes subdirectories along as well, and material that a Hase
is only meant to read alongside the trigger must not match the pattern —
otherwise it gets a Lauf of its own
([docs/architecture.md](docs/architecture.md#what-a-watch-auftrag-sees)).

A `hasenbau lauf` alongside it is allowed and the normal way to check a
single Auftrag: it starts its own opencode server on its own port, and
both share the SQLite database in WAL mode.

If the process is killed hard (`kill -9`, power loss), Läufe are left
behind as `running`. The next start clears them out: if the host process
is gone, the row is closed as `aborted` with a reason and shows up in the
log. A second Hasenbau running at the same time is left alone.

For permanent operation it runs as a systemd user unit, template in
[docs/architecture.md](docs/architecture.md#as-a-systemd-unit).
`opencode` has to be in the PATH of the unit, since the daemon starts it
as a child process.

```bash
systemctl --user enable --now hasenbau    # start, and at login too
journalctl --user -u hasenbau -f          # watch
hasenbau status                           # what is here, what happened
```

## Boundaries

Every generated agent gets the same six prohibitions, regardless of the
template: `bash`, `webfetch`, `websearch`, `external_directory`, `task`
and `question` are `deny` in its `permission:` block. That way they never
show up in the model's tool list in the first place, and the Hase does
not go looking for a way around them.

What it gets instead is a back channel: `hasenbau_summary` for the one
line about what the Lauf did, `hasenbau_notiz` for observations along the
way, and `hasenbau_tool_request`, with which it asks for a missing tool
instead of looking for a path around its boundaries
([docs/hasen.md](docs/hasen.md)).

Out of such a request the Schmied builds a tool that a Hase calls during
its Lauf. A draft is code that a model wrote and nobody read, hence three
stages, each requiring the previous one:

```bash
hasenbau tool review --next        # read it and take responsibility
hasenbau tool test <name>          # run its example in the sandbox
hasenbau tool release <name>       # confirm the output and release it
```

A tool is a folder, and the Schmied puts an example into it along with
the output it predicts — it may not run its own script, so it has to
know. `tool test` fares that example: a mismatch refutes even at exit 0,
a match confirms nothing, because prediction and script come from the
same model. How `generated → hypothetical → actual`
comes about, and why a tool in operation never gets more than the Hase
calling it: [docs/tools.md](docs/tools.md).

## Throttling and distilling

`throttle: {max: 5, per: 1h}` caps an Auftrag at five Läufe per rolling
hour; `between: "22:00-06:00"` moves the work into the night. The backlog
waits in the file system and survives any restart, oldest input first. In
`hasenbau.yaml` the same cap applies across all Aufträge together
([docs/throttling.md](docs/throttling.md)).

A Hase that makes the same tool calls on every Lauf is an interpreter
recompiling every time. `hasenbau findings <auftrag>` works that out from
the Läufe without asking a model; the Baumeister turns it into a draft
Gang. A generated Gang is never activated automatically
([docs/distillation.md](docs/distillation.md)).

## Commands

| | |
|---|---|
| `init`, `fix`, `new` | create a Bau, complete it, write scaffolds |
| `daemon`, `lauf`, `baumeister` | trigger |
| `get <resource>` | one line per object |
| `describe <resource>` | one object in detail, `describe bau` as a diagnosis |
| `status` | what is here, what happened |
| `dig`, `findings` | material and findings for distillation |
| `tool review\|test\|release` | release a tool |
| `provider fetch` | fetch the model list from the endpoint |

Complete, with all resources and flags:
[docs/commands.md](docs/commands.md).

## Development

```bash
go build ./...
go vet ./...
go test ./...    # integration tests skip themselves without opencode in the PATH
```

Agents working on this repository read [`AGENTS.md`](AGENTS.md); the spec
is [`PLAN.md`](PLAN.md), issue tracking runs on
[beads](https://github.com/gastownhall/beads) (`bd ready`). Both are
German, like the rest of the internal documentation; the German mirror of
this README is [README.de.md](README.de.md), the German docs are in
[docs/de/](docs/de/).

## License

[EUPL-1.2](LICENSE).
