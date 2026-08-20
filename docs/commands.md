# Commands

**English** · [Deutsch](de/commands.md)

The full reference. The short version is in the
[README](../README.md#commands); every command also takes
`hasenbau <command>` without arguments as help.

Every command needs the Bau: either `-bau ~/meinbau` in front of the
subcommand or one `cd ~/meinbau`, since the default is the current
directory.

## Creating and completing

```bash
hasenbau init <bau>        # create a Bau (Git repo, isolated config, back channel, special Hasen)
hasenbau fix               # add what an existing Bau is missing
hasenbau new hase <name>   # write a template scaffold, commented
hasenbau new auftrag <name> -hase <hase>   # write an Auftrag scaffold
hasenbau new dockerfile    # Dockerfile and docker-compose.yml, ready to run
```

`init` and `fix` are non-destructive and idempotent: existing files are
left untouched. So is `new`, for all three resources.

`new dockerfile` is the one resource without a name, and the one that
writes two files: `Dockerfile` and `docker-compose.yml`. Either one that
already exists is left alone while the other is still written — so after
editing the Dockerfile by hand you can still get the compose file.

## In a container

`hasenbau new dockerfile` writes the recipe that the Install section of
the README describes in prose, plus a `docker-compose.yml` that runs it:

```bash
hasenbau new dockerfile
docker compose run --rm hasenbau describe bau   # check before arming
docker compose up -d                            # the daemon
docker compose logs -f
docker compose run --rm hasenbau lauf <auftrag> # one Auftrag by hand
```

What goes into the image is what **Hasenbau** calls — `opencode`, `git`,
`bwrap`, `sh`, `python3` — plus `ca-certificates` and `tzdata`. What your
Gänge call is not in there: Hasenbau does not know those and will not
guess, so the file ends with a marked block for you.

The compose file is where the flags stop being footnotes: it declares
`security_opt: [seccomp:unconfined]`, the Bau as a bind mount,
`TZ: ${TZ:-Europe/Berlin}`, `restart: unless-stopped` (what
`Restart=always` is for the systemd unit) and `init: true` — hasenbau is
PID 1 in there and spawns opencode plus every Gang, so something has to
reap orphans.

One thing worth doing once: `build: .` sends this whole directory to the
Docker daemon as context, `archiv/` included, although the Dockerfile
copies nothing from it. A `.dockerignore` holding a single `*` cuts that
to nothing and the build still works — measured, 200 MB of context became
42 bytes.

Two things it reads off your Bau rather than assuming: the provider IDs
from `.opencode-home/opencode/opencode.json`, so the credential comments
name your providers, and the version of the `opencode` in your PATH,
which the installer line is pinned to.

Three of the `docker run` flags in the generated header are not optional,
and each one fails without saying so:

| Flag | Without it |
|---|---|
| `--security-opt seccomp=unconfined` | `bwrap` cannot open a user namespace, so the plugin registers **no** tool at all — only the daemon log mentions it |
| the key (below) | the model provider refuses, and the Lauf ends as `failed` |
| `-e TZ=…` | cron triggers run in UTC, so `0 10 * * *` fires at 12 |

`describe bau` is proof for the first one: its `Programs` check does not
ask whether `bwrap` is installed but whether it can open a namespace, by
running it. In a container without the flag it says so, and passes on
what `bwrap` answered:

```
CHECK  Programs       all there, but bwrap cannot open a namespace
                      → bwrap says: No permissions to create new namespace, …
                        Until that is fixed the plugin registers NO tool
```

One thing the image already handles: the Bau arrives as a mount and
belongs to the host user, not to root inside. Git would refuse to look at
it (`dubious ownership`), and `describe bau` would then report a Bau
`without a commit` that in fact has plenty — no commit visible means no
project ID and no Raum permissions (PLAN.md §11.5). The generated
Dockerfile writes the matching `safe.directory` entry.

### Credentials in a container

Never put a key in the Dockerfile. `COPY` and `ENV` end up in the image
layers and stay readable with `docker history`; removing them in a later
layer does not remove them.

You do not need `auth.json` in the container at all. `options.apiKey`
understands `{file:PATH}` and `{env:VAR}`, so the key can arrive as a
mounted file while your Bau config stays free of secrets:

```json
"provider": {"<id>": {"options": {"apiKey": "{file:/run/secrets/<id>-key}"}}}
```

The compose file declares that as a secret, so there is nothing to type:

```yaml
secrets:
  <id>-key:
    file: ${HOME}/.secrets/<id>-key
```

Two properties of that, both measured: the file appears inside as
`/run/secrets/<id>-key` **with the permissions of the host file carried
over unchanged** — so `chmod 600` it and it stays that way. And if the
file does not exist, `docker compose up` stops with `secret file … does
not exist` instead of starting. That is on purpose: a Bau without a key
cannot make a Lauf, and a container that starts and then fails every Lauf
is worse than one that refuses.

**Encrypting the key does not help *inside* the container** — the
process needs the plaintext at the moment it makes the HTTPS call. What
it does help with is everywhere else, and the pattern is
tool-independent: keep it encrypted on the host, decrypt it into memory
(a tmpfs) for the run, mount that, and shred it afterwards. With
[age](https://age-encryption.org) as an interchangeable example:

```bash
age -d -i ~/.age/key ~/.secrets/scc-key.age > /run/user/$UID/scc-key
docker run --rm -v "$PWD":/bau \
  --mount type=bind,src=/run/user/$UID/scc-key,dst=/run/secrets/scc-key,ro \
  meinbau daemon
shred -u /run/user/$UID/scc-key
```

`pass`, `sops`, `gpg` or a systemd credential all fit the same shape.

Two alternatives, each with its price. Sharing the whole data directory
(`-v "$HOME/.local/share/opencode":/root/.local/share/opencode`) also
shares `opencode.db`, `storage/` and `log/` with your everyday opencode —
but it is the only way that keeps an **OAuth** login refreshable inside
the container, since those entries get rewritten. An environment variable
(`{env:…}` plus `--env-file`) is the easiest to type and the widest open:
the key is in `docker inspect` and in the environment of every process in
the container, a Schmied tool included.

`hasenbau provider fetch` and `get provider` follow the same route, in
the same order opencode does: `options.apiKey` wins, `auth.json` is the
fallback. So `get provider` names the route instead of answering
yes-or-no:

```
ID   ENDPOINT                     MODELS  ACTIVE  KEY
scc  https://…/api/v1             35      yes     options.apiKey
```

A third value is the one worth knowing about: `options.apiKey (broken)`
means the route is configured but delivers nothing — the secret is not
mounted, the variable is empty. Without that, such a Bau looks from the
outside exactly like one that is fine. `hasenbau describe provider <id>`
says what is wrong, and it never prints the key itself.

## Triggering

```bash
hasenbau daemon                  # arm the triggers (cron + watch)
hasenbau lauf <auftrag> [datei]  # trigger an Auftrag by hand
hasenbau baumeister [-finding N] <ziel>   # put the Baumeister to work
```

The second argument of `lauf` is the trigger, for a watch Auftrag the
file the watcher would otherwise have found (`$TRIGGER_FILE`). Gänge run
with the Bau root as their working directory, so the path is relative to
the Bau.

## Looking

```bash
hasenbau get auftraege     # what the Bau knows
hasenbau get hasen         # templates, models, who uses them
hasenbau get gaenge        # Gang scripts, who calls them, open drafts
hasenbau get tools          # released tools and who may call them
hasenbau get tools -drafts  # what is waiting for review
hasenbau get laeufe [-n N] # history
hasenbau get lauf <id>     # one Lauf, one line
hasenbau get provider      # which providers the Bau knows, which are fetchable
```

```bash
hasenbau describe bau             # diagnosis: is this Bau in order?
hasenbau describe auftrag <name>  # trigger, Gänge, Räume, write permissions
hasenbau describe hase <name>     # effective permissions per Auftrag
hasenbau describe gang <datei>    # purpose and every Auftrag that calls it
hasenbau describe tool <name>     # state, review, who may call it
hasenbau describe lauf <id>       # one Lauf with notes, errors, cost
hasenbau describe provider <id>   # endpoint, key, and the models of the Bau
```

```bash
hasenbau status            # dashboard: what is here, what happened
```

### The verbs

The reading commands follow the example of `kubectl`. `get` shows one
line per object, `describe` one object in detail together with everything
Hasenbau knows about it, so for a Lauf that includes the notes from the
back channel. `describe` is not a `cat`: files are never printed in full,
though their path is named. `new` creates an object. The triggering verbs
(`lauf`, `baumeister`, `daemon`) are unaffected by this.

`describe bau` checks: layout, Git commit, Bau config, back channel
binary, generated agents, leftover `$WORK` directories. `status` only
shows. The two inconspicuous checks carry the furthest, the root commit
and the back channel entry: if the latter points at a binary that is
gone, it quietly takes the tools away from the Hasen.

## Distilling

```bash
hasenbau dig [-json] <ziel>  # material for the Baumeister: <lauf-id> or <auftrag>#<n>
hasenbau findings <auftrag>  # what can be computed over the Läufe (no model)
```

In detail in [distillation](distillation.md).

## Releasing tools

```bash
hasenbau tool review [<name>|--next]       # read it and take responsibility
hasenbau tool test <name> --<arg> <wert>   # run it in the sandbox and show what comes out
hasenbau tool test <name> -no-sandbox …    # the same under production conditions
hasenbau tool release <name>               # confirm the output and release it (makes it actual)
```

The three verbs run in this order, and each step requires the previous
one. Why, is in [tools](tools.md).

## Providers

```bash
hasenbau provider fetch <id>  # fetch the model list from the provider endpoint
```

Nothing is ever written automatically: the command shows the diff first.
Why a Bau brings its own providers at all is in
[architecture](architecture.md#providers-in-a-bau).

## Not by hand

```bash
hasenbau mcp               # back channel over stdio (started by opencode itself)
hasenbau sandbox-incident   # reports a tool call from inside the sandbox
```

Both are self-calls: `mcp` is started by opencode, `sandbox-incident` by
the guard in the opencode server.
