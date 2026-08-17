# Examples: the reference Auftrag pdf-einlagern

**English** · [Deutsch](README.de.md)

The end-to-end proof for phase 1 (PLAN.md §6, §8; Hasenbau-z0u): a PDF
lands in `laderampe/sources/` → the Gang `pdf_to_md.py` extracts it
deterministically into `$WORK/extrakt.md` → the Hase `archivar`
summarises it, assigns tags and files `YYYY-MM-DD-<slug>.md` into
`lager/` → `nachher:` moves the PDF into `archiv/`. The Hase never sees
the PDF, it gets Markdown, and that is the point.

Taking it over into a Bau (the Bau lives outside this repository, see the
AGENTS.md leakage section in PLAN.md §3):

```bash
hasenbau init <bau>
cp -f beispiele/auftraege/pdf-einlagern.md <bau>/auftraege/
cp -f beispiele/hasen/archivar.md          <bau>/hasen/
cp -f beispiele/gaenge/pdf_to_md.py        <bau>/gaenge/
```

`pdf_to_md.py` needs `pdftotext` (poppler) in the PATH. Adjust the
`model:` in the Hase template to the Bau's own provider config.

## The Baumeister

The Baumeister no longer lives here, it lives in the binary: `hasenbau
init` writes the Auftrag and the Hase into every Bau, and `hasenbau fix`
restores them when they are missing. The source is
`internal/bau/vorlagen/`. So nobody has to copy anything any more:

```bash
hasenbau -bau <bau> baumeister 8               # trace of Lauf 8
hasenbau -bau <bau> baumeister pdf-einlagern   # last Lauf of the Auftrag
```

It is not a special case in the code but exactly such an Auftrag, except
that its material is Läufe instead of PDFs. Its Gang pulls the trace
(`hasenbau dig`), its Hase distills it into a draft Gang in
`gaenge/entwurf/`. Whoever does not want it changes its trigger or
empties the Auftrag; deleting the file does not help, `fix` creates it
again.

Its write permission comes exclusively from `raeume: out:`, which becomes
the only allowed `edit` pattern of the generated agent (PLAN.md §6).
Change the out Raum and you change what the Baumeister may touch;
`internal/hase` has a golden test for that which checks both roots, this
directory and `internal/bau/vorlagen/`. `gaenge/entwurf/` rather than
`gaenge/`, so that no Lauf can overwrite a Gang that is in use.

Several Baumeister variants may sit next to each other in `hasen/`
(`baumeister-streng.md`, `baumeister-python.md`); `hasenbau.yaml` names
exactly one Auftrag, and its `hase:` decides which variant runs.

A draft is a draft: Hasenbau never enters anything into an Auftrag itself
(§8/§10). Read it, check it, enter it yourself; the suggested `run:` line
is in the head of the script.
