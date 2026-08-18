# Architecture and isolation

**English** · [Deutsch](de/architecture.md)

## The idea

1. Triggers instead of chat. Tasks start because a file lands in a
   watched Raum or a cron time is reached, not because somebody is
   typing.
2. Deterministic before probabilistic. Before a Hase sees any material,
   the Gänge run, meaning ordinary scripts without an LLM. The Hase gets
   prepared Markdown, never the raw PDF.
3. Distillation. A Hase that makes the same tool calls on every Lauf is
   an interpreter recompiling every time. Hasenbau logs the traces;
   `hasenbau dig` and the Baumeister turn them into deterministic Gänge.
   A generated Gang is never activated automatically.

Not part of it: remote and multi-host, a web UI, an agent framework of
its own.

## The parts

```
systemd → hasenbau (one Go binary)
            ├── Scheduler   cron triggers
            ├── Watcher     file triggers (fsnotify)
            ├── Runner      Gänge, then the Hase
            ├── Store       SQLite (WAL, no cgo)
            └── Supervisor ──spawns──> opencode serve (127.0.0.1, child)
```

The full implementation plan is in [`PLAN.md`](../PLAN.md), in German:
architecture §2, isolation §3, layout §4, data model §5, Auftrag format
§6.

## What a watch Auftrag sees

`watch:` carries the pattern alone; the directory it applies to is the
Auftrag's `input:` Raum. Flat by default: `watch: "*.pdf"` sees the
Räum's own files and nothing below them.

A double star turns that on for subdirectories whose names nobody has to
know in advance:

```yaml
trigger: {watch: "*.pdf"}       # the Raum itself
trigger: {watch: "**/*.pdf"}    # the Raum and everything below it
```

The double star stands for zero or more directories, so `**/*.pdf` also
matches a PDF lying directly in the Raum — you do not lose the flat case
by opting in. A pattern that names its subdirectory (`scans/*.pdf`) stays
flat and keeps costing exactly one inotify watch.

**One Lauf per file.** The pattern is the control: material that a Hase
is meant to read along with the trigger must **not** match it. With
`watch: "*.pdf"` the JSON files next to the PDF set nothing off; write
`watch: "*"` and every attachment gets a Lauf of its own. The
already-seen backstop does not save you here — it knows triggers, not
companions.

A Hase **may** read what lies next to the trigger (`read`, `glob` and
`list` are open, only `bash`, `webfetch`, `websearch` and
`external_directory` are denied). It just does not learn of it by
itself. If you want it read, say so in the body of the Auftrag, as prose
without a path: "next to the PDF there are two JSON files in the input
folder". The path is already under `raeume:` and does not belong there
twice.

Limits worth knowing before pointing `**` at a large tree: every watched
directory costs one inotify watch (`fs.inotify.max_user_watches`),
symlinks are not followed (a link pointing at an ancestor would be a
loop, one pointing out of the Bau a window into foreign directories),
and `{a,b}` is read as an alternative — a file whose name contains
braces is no longer matched literally.

## Isolation

The opencode server runs with an isolated config (`XDG_CONFIG_HOME`
points into the Bau): no plugins and hooks from the everyday config, but
shared credentials (`auth.json` via `XDG_DATA_HOME`).

Sessions are always anchored at the Bau root; the Hasen only see the
Räume of their Auftrag. That is also why `hasenbau init` creates a root
commit: without it the Bau gets no project ID of its own, and the Raum
permissions do not take effect.

### The Bau plugin is generated

One file in the Bau is not yours:
`.opencode-home/opencode/plugin/hasenbau.js`. It carries the sandbox
guard and the gate that only registers released tools, and Hasenbau
rewrites it from the binary whenever it differs — on every daemon start,
every Lauf and every `hasenbau fix`. So upgrading Hasenbau upgrades the
Bau, and an edit of your own is gone with the next Lauf. `hasenbau
describe bau` reports an outdated file; a replaced one is named in the
output.

Everything else `init` writes is yours and is never overwritten:
`hasenbau.yaml`, the special Hasen, your Aufträge. Own plugins belong
**next to** it as separate files, entered in the `plugin:` block of the
Bau config — the directory stays yours, only that one file does not.

A Bau created before that change has the file in its git, and a
`.gitignore` line does not catch up with a tracked file. It then shows
up as a change after every upgrade, without anyone having touched it.
`describe bau` says so and names the way out (`git rm --cached …`); it
does not do it for you, since that would reach into the history of your
repository.

## Providers in a Bau

That a Bau brings its own custom providers follows from the same
isolation: `auth.json` shares the keys, the definitions stay in the Bau.
Hence the hand-maintained `provider:` block in
`.opencode-home/opencode/opencode.json`:

```json
"provider": {
  "scc": {
    "npm": "@ai-sdk/openai-compatible",
    "name": "SCC KI Toolbox",
    "options": {"baseURL": "https://beispiel.invalid/api/v1"}
  }
}
```

The model list is then fetched from the endpoint by `hasenbau provider
fetch scc`. It is never written automatically; the command shows the diff
first. `hasenbau get provider` says what the Bau knows and what can be
fetched.

## As a systemd unit

`opencode` has to be in the PATH of the unit, since the daemon starts it
as a child process:

```ini
# ~/.config/systemd/user/hasenbau.service
[Unit]
Description=Hasenbau
After=network.target

[Service]
ExecStart=%h/go/bin/hasenbau -bau %h/meinbau daemon
Environment=PATH=%h/.opencode/bin:/usr/local/bin:/usr/bin:/bin
Restart=on-failure

[Install]
WantedBy=default.target
```

Stopping is `systemctl --user stop hasenbau`. That is `SIGTERM` and
therefore the same clean path as Ctrl-C in the foreground.

## Two databases

The SQLite database under `state/hasenbau.db` tracks what Hasenbau does
(PLAN.md §5). Beads tracks how Hasenbau is built, meaning the development
of this repository; it has nothing to do with a running Bau.
