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
hasenbau new dockerfile    # a Dockerfile with everything Hasenbau needs
```

`init` and `fix` are non-destructive and idempotent: existing files are
left untouched. So is `new`, for all three resources.

`new dockerfile` is the one resource without a name: the file is always
called `Dockerfile`.

## In a container

`hasenbau new dockerfile` writes the recipe that the Install section of
the README describes in prose. What goes in is what **Hasenbau** calls —
`opencode`, `git`, `bwrap`, `sh`, `python3` — plus `ca-certificates` and
`tzdata`. What your Gänge call is not in there: Hasenbau does not know
those and will not guess, so the file ends with a marked block for you.

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

Do not take `describe bau` as proof for the first one. It checks whether
`bwrap` is *installed*, not whether it can do its job — measured in a
container, it reported `ok  Tools` while `bwrap` answered `No permissions
to create new namespace`. Until that check is sharper, ask `bwrap` itself:

```bash
docker run --rm --security-opt seccomp=unconfined --entrypoint bwrap meinbau \
  --ro-bind / / --unshare-all --die-with-parent -- /bin/true
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

```bash
docker run --rm -v "$PWD":/bau \
  --mount type=bind,src="$HOME/.secrets/<id>-key",dst=/run/secrets/<id>-key,ro \
  meinbau daemon
```

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

One asymmetry to expect: `hasenbau provider fetch` and `get provider`
read `auth.json` directly. On the mounted-file route they report
`no auth.json` while your Läufe run fine. Fetch the model list on the
host — it is versioned Bau content anyway.

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
