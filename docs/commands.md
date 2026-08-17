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
```

`init` and `fix` are non-destructive and idempotent: existing files are
left untouched.

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
hasenbau get tools             # released tools and who may call them
hasenbau get tools -entwuerfe  # what is waiting for review
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
hasenbau sandbox-vorfall   # reports a tool call from inside the sandbox
```

Both are self-calls: `mcp` is started by opencode, `sandbox-vorfall` by
the guard in the opencode server.
