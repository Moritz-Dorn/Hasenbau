# Throttling: caps, night window, order

**English** · [Deutsch](de/throttling.md)

Whoever drops 200 PDFs at once rarely wants all of them processed right
away.

## The cap per Auftrag

`throttle: {max: 5, per: 1h}` in the Auftrag frontmatter caps an Auftrag
at five Läufe per rolling hour; the rest waits in `sources/`, because the
queue is the file system and therefore survives any restart. The oldest
input is processed first, per Auftrag by exactly one worker, one after
another.

The count comes from the Lauf history rather than from a counter in
memory. Otherwise a crash loop of all things would get fresh budget after
every restart. Failed Läufe count too: they cost something anyway.
`hasenbau lauf` bypasses the cap but is counted.

## Only at night

`between: "22:00-06:00"` is added when the work should only run at night,
in local time and allowed to span midnight. It restricts the start only:
a Lauf that begins at 05:55 runs to its end. And it postpones rather than
caps; whoever wants both sets both.

## Visible

Throttled Aufträge appear in `hasenbau status` with their backlog and the
earliest next Lauf. A cap you cannot see is indistinguishable from a
hanging daemon:

```
Gedrosselt (1)
  pdf-einlagern  5 Läufe je 1h, nur 22:00-06:00
                 195 Dateien im Eingang, nächster Lauf frühestens 22:00 (in 8h44m)
```

## The cap above everything

Above that sits a Bau-wide cap: `throttle: {max: 20, per: 1h}` in
`hasenbau.yaml` applies across all Aufträge together. The per-Auftrag cap
protects an Auftrag from itself, this one protects the budget from all of
them, since ten Aufträge at 5/h each are 50/h. It counts cron Läufe as
well, their cost being the same; `hasenbau lauf` is still let through and
still counted.
