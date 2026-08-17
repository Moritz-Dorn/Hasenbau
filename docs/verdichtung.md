# Verdichtung: aus Läufen werden Gänge

Ein Hase, der bei jedem Lauf dieselben Tool-Calls macht, ist ein
Interpreter, der jedes Mal neu kompiliert. Der Hasenbau loggt die
Traces; daraus wird deterministischer Code.

## Was sich ohne Modell rechnen lässt

```bash
hasenbau dig [-json] <ziel>  # Material: <lauf-id> oder <auftrag>#<n>
hasenbau findings <auftrag>  # Gang-Kandidaten, Reibung, Ausreißer
```

`findings` ruft kein Modell. Es rechnet über die Läufe eines Auftrags:
welche Tool-Calls wiederkehren, welche Argument-Position variiert, wo
Läufe reiben.

Ein Auftrag mit `monitored: true` im Frontmatter wird routinemäßig
beurteilt: seine Befunde stehen dann in `hasenbau status`, ohne dass
jemand danach fragt. Das Flag steuert nur die Meldung — aufgezeichnet
wird bei jedem Auftrag alles, und `hasenbau findings <auftrag>` rechnet
auch über die, die es nicht setzen. Wer es später nachträgt, bekommt die
Historie mitgeliefert.

## Der Baumeister

```bash
hasenbau baumeister <lauf-id|auftrag>   # aus einem Trace
hasenbau baumeister -finding <n> <ziel> # aus einem gerechneten Befund
```

`hasenbau baumeister` setzt den Baumeister auf einen Lauf an: er liest
dessen Trace und schreibt daraus einen Gang-Entwurf nach
`gaenge/entwurf/`. Mit `-finding <n>` bekommt er stattdessen einen
Befund aus `hasenbau findings` — dann steht schon gerechnet da, welche
Argument-Position über die Läufe variiert, und er muss es nicht aus
einem einzelnen Trace raten.

Der Baumeister ist dabei kein Sonderfall im Code, sondern selbst ein
Auftrag mit einem Hasen — sein Material sind nur Läufe statt PDFs, und
sein Gang ist `hasenbau dig`. Sein Schreibrecht entsteht ausschließlich
aus dem `out`-Raum seines Auftrags; auf `auftraege/` hat er keines.

**Ein Entwurf wird nie automatisch aktiviert** — der Nutzer liest ihn
und trägt den Gang selbst ein. Aus einem einzelnen Trace ist nicht
sicher zu erkennen, was Parameter und was Konstante war; ein Entwurf ist
deshalb eine Gesprächsgrundlage, kein fertiger Gang.
