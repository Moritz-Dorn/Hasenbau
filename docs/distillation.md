# Distillation: Läufe become Gänge

**English** · [Deutsch](de/distillation.md)

A Hase that makes the same tool calls on every Lauf is an interpreter
recompiling every time. Hasenbau logs the traces; out of them comes
deterministic code.

## What can be computed without a model

```bash
hasenbau dig [-json] <ziel>  # material: <lauf-id> or <auftrag>#<n>
hasenbau findings <auftrag>  # Gang candidates, friction, outliers
```

`findings` calls no model. It computes over the Läufe of an Auftrag:
which tool calls recur, which argument position varies, where Läufe
create friction.

An Auftrag with `monitored: true` in its frontmatter is assessed
routinely: its findings then appear in `hasenbau status` without anybody
asking for them. The flag only controls the reporting. Everything is
recorded for every Auftrag, and `hasenbau findings <auftrag>` computes
over the ones that do not set it as well. Whoever adds it later gets the
history along with it.

## The Baumeister

```bash
hasenbau baumeister <lauf-id|auftrag>   # from a trace
hasenbau baumeister -finding <n> <ziel> # from a computed finding
```

`hasenbau baumeister` puts the Baumeister on a Lauf: it reads that Lauf's
trace and writes a draft Gang from it into `gaenge/entwurf/`. With
`-finding <n>` it gets a finding from `hasenbau findings` instead. Then
it is already computed which argument position varies across the Läufe,
and it does not have to guess that from a single trace.

In the code the Baumeister is itself an Auftrag with a Hase. Its material
is Läufe instead of PDFs, its Gang is `hasenbau dig`. Its write
permission comes exclusively from the `out` Raum of its Auftrag; on
`auftraege/` it has none.

A draft is never activated automatically, the user reads it and enters
the Gang themselves. From a single trace there is no reliable way to tell
what was a parameter and what was a constant; a draft is therefore good
as a basis for discussion, not as a finished Gang.
