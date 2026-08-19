# Tools: from draft to release

**English** · [Deutsch](de/tools.md)

Besides the Gänge, which run before the Hase, there are tools that it
calls during its Lauf. Each one is a **folder** — the folder name is the
tool name — and it is registered by the Bau plugin when the server
starts; the Schmied wrote it, a human released it.

```
tools/
  drafts/zeilen_zaehlen/      what the Schmied wrote, never registered
    tool.json                 the manifest
    zeilen_zaehlen.py         the script
    example/probe.txt         material for the one trial run
  released/                   only what is in here reaches a Hase
```

The release is two-stage: first the folder moves from `tools/drafts/` to
`tools/released/`, then an Auftrag names it in its `tools:`. Without that
entry a Hase gets no tool. A newly built one should not end up with
everybody just because nobody forbade it. `hasenbau get tools` shows what
exists and who may call it.

## Why three stages at all

A draft is code that a model wrote and nobody read. The Schmied, like
every Hase, has no `bash` and cannot run its script even once, so what it
delivers is untested. The first real Schmied Lauf showed exactly that: an
impeccable manifest, plausible looking Python, and a crash on the first
call.

Hence a tool goes through three stages, and each requires the previous
one:

```bash
hasenbau tool review --next        # read it and take responsibility
hasenbau tool test <name>          # run its example and check the prediction
hasenbau tool release <name>       # confirm the output, move it to released/
```

`--next` takes the next **unread** draft. One that has already been read
stays in `get tools -drafts` until it is released — it is waiting for the
trial run, not for another reading. To read it again anyway, name it:
`hasenbau tool review <name>`.

## The example is the Schmied's prediction

`tool test` without arguments runs the example the Schmied put in the
folder and compares the output against `example.expect` in the manifest.
That answers a question a reviewer cannot answer alone: *which file goes
in here?* Only the Hase that asked for the tool knows, and it is not in
the room.

```json
"example": {"args": {"datei": "example/probe.txt"}, "expect": "3"}
```

A mismatch makes the tool `invalid` even when the script exits 0 — it ran
and did the wrong thing, which means the model was wrong about its own
code. A match proves nothing: the prediction and the script come from the
same model. It stays `hypothetical` until a human confirms the output at
`release`.

The Schmied can predict at all because it may **not** run its script.
Whoever can try things out may guess and patch; whoever cannot has to
understand what they wrote. Paths inside `example` are relative to the
tool folder, so they survive the move to `released/`.

## The states

The state in between is named after the intention semantics of the IRS
([ValIntent](https://github.com/KIT-IRS/Intent-Semantics)):
`generated` (written, unread) → `hypothetical` (claimed, not confirmed)
→ `actual` (a human found the output to be right). A failed test run
makes it `invalid`, a change after the review makes it `outdated`.
Classification happens through verification; nobody can hand themselves
`actual`.

The test run only counts in one direction: if it fails, it refutes the
review (`invalid`); if it passes, it does not confirm it. Exit 0 means
"it ran", not "it is right", and no exit code can see whether 24 was the
correct line count. A run that contradicts the Schmied's `expect` refutes
as well, even at exit 0 — that is the same direction, not a new one. So a
passing test run stays `hypothetical`, and `release` asks before it moves
anything: was the output right? Whoever says yes is named in the file.

## The review block

`review` writes a block into the head of the script: who read it, what
they believe it does, why they consider it harmless, plus a hash over the
script without that block. Test run and release add their lines
afterwards:

```python
#!/usr/bin/env python3
# hasenbau-review: 1
# reviewed-by: Moritz Dorn
# reviewed-at: 2026-08-13T14:02:11+02:00
# body-sha256: 9f2c…
# does: zählt die Zeilen einer Datei und gibt die Zahl auf stdout aus
# safe-because: liest nur den übergebenen Pfad, schreibt nichts, kein Netz
# verified-at: 2026-08-13T14:05:30+02:00
# verified-with: --pfad example/probe.txt
# verified-exit: 0
# verified-expect: match
# released-by: Moritz Dorn
# released-at: 2026-08-13T14:06:02+02:00
# valintent: actual
# hasenbau-review-end
import argparse
```

This binds the review to exactly the content that was read: change one
line and it no longer holds, not even towards the reviewer themselves.
At every server start the plugin asks Hasenbau about each tool
(`hasenbau tool state <name>`) and registers only what comes back as
released. The rule lives in one place — the binary — and the plugin does
what the exit code says.

`#` and `//` are both allowed, but only one of them within a block, and
`hasenbau-review-end` has to close it. Both rules come from one failure
each: a `//` line in a bash script is an executable path and not a
comment, and without the closing line the block swallows the first
comment of the script and invalidates its own review immediately.

The `valintent:` line is informational only. The state is recomputed
every time: whoever changes a released file leaves an `actual` in it that
is no longer true; `describe tool` names the discrepancy, and the daemon
corrects the line at startup, only that one line, leaving the hash
untouched.

The block is a format and can be produced by hand or with a GUI;
Hasenbau checks the property, never the origin. It does not prove a
review: a block with a correctly computed hash gets through even with
`reviewed-by: niemand`. What it prevents is silent drift, and it forces a
name.

## The test run

`test` executes the script. It does not stop malicious code, it starts
it, and that is exactly why it demands a review first.

The chain behind this is real. A Hase reads foreign material, that
material contains injected instructions, the Hase files a tool request as
a result, and the Schmied builds whatever the request says; at the end
there is Python meant to run in the server process. The only place where
this is noticed is a human reading the draft. That is what it is short
for, and why it sits in a file.

The test run therefore happens in a sandbox (`bwrap`): no network, the
Bau readable only, no `$HOME`, a one minute time limit. That also makes
it apparent when a tool secretly phones home. If `bwrap` is missing, it
runs as if there were none, and the command says so out loud instead of
keeping quiet about it.

A sandbox changes the result: a tool that legitimately writes into a Raum
fails inside it. That is what `-no-sandbox` is for, the run under
production conditions. A failure inside the sandbox therefore does not
refute by itself; it can come from the tool or from its boundaries. The
command then asks: was it the tool? Whoever says yes sets `invalid`;
without an answer the state stays as it was.

## The boundary in operation

In operation a second, tighter boundary applies: a tool never gets more
than the Hase calling it. It sees the Räume of its Auftrag, reads what
the Hase reads, and writes what the Hase writes (`work` and `out`).
Nothing else: no foreign Raum, nothing outside the Bau, no network.
Hasenbau computes the boundary when it loads the Aufträge; the Bau plugin
applies it.

If `bwrap` is missing, no tool is registered in operation at all, unlike
in the test run, where a human reads the warning. A missing tool is
noticed, a missing boundary is not; `hasenbau describe bau` reports the
case.
