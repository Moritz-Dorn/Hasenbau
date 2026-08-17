# Beispiele — der Referenz-Auftrag pdf-einlagern

[English](README.md) · **Deutsch**. Übersetzung; maßgeblich ist die englische Fassung.

Der Ende-zu-Ende-Beweis für Phase 1 (PLAN.md §6, §8; Hasenbau-z0u):
PDF landet in `laderampe/sources/` → Gang `pdf_to_md.py` extrahiert
deterministisch nach `$WORK/extrakt.md` → Hase `archivar` fasst
zusammen, vergibt Tags und legt `YYYY-MM-DD-<slug>.md` in `lager/`
ab → `nachher:` verschiebt das PDF nach `archiv/`. Der Hase sieht das
PDF nie — er kriegt Markdown; das ist der Punkt.

In einen Bau übernehmen (der Bau liegt **außerhalb** dieses Repos,
siehe AGENTS.md-Leckage in PLAN.md §3):

```bash
hasenbau init <bau>
cp -f beispiele/auftraege/pdf-einlagern.md <bau>/auftraege/
cp -f beispiele/hasen/archivar.md          <bau>/hasen/
cp -f beispiele/gaenge/pdf_to_md.py        <bau>/gaenge/
```

`pdf_to_md.py` braucht `pdftotext` (poppler) im PATH. Das `model:` im
Hasen-Template an die eigene Provider-Config des Baus anpassen.

## Der Baumeister

Der Baumeister liegt **nicht mehr hier**, sondern im Binary:
`hasenbau init` schreibt Auftrag und Hase in jeden Bau, und
`hasenbau fix` stellt sie wieder her, wenn sie fehlen. Die Quelle ist
`internal/bau/vorlagen/`. Kopieren muss also niemand mehr:

```bash
hasenbau -bau <bau> baumeister 8               # Trace von Lauf 8
hasenbau -bau <bau> baumeister pdf-einlagern   # letzter Lauf des Auftrags
```

Er ist kein Sonderfall im Code, sondern genau so ein Auftrag — nur dass
sein Material Läufe sind statt PDFs. Sein Gang zieht den Trace
(`hasenbau dig`), sein Hase verdichtet ihn zu einem Gang-Entwurf in
`gaenge/entwurf/`. Wer ihn nicht will, stellt seinen Trigger um oder
leert den Auftrag — die Datei zu löschen hilft nicht, `fix` legt sie
wieder an.

Sein Schreibrecht entsteht **ausschließlich** aus `raeume: out:` —
daraus wird das einzige erlaubte `edit`-Pattern des generierten Agenten
(PLAN.md §6). Wer den out-Raum ändert, ändert damit, was der Baumeister
anfassen darf; `internal/hase` hat dafür einen Golden-Test, der beide
Wurzeln prüft — dieses Verzeichnis und `internal/bau/vorlagen/`.
`gaenge/entwurf/` statt `gaenge/`, damit kein Lauf einen benutzten Gang
überschreiben kann.

Mehrere Baumeister-Varianten dürfen nebeneinander in `hasen/` liegen
(`baumeister-streng.md`, `baumeister-python.md`) — `hasenbau.yaml`
benennt genau einen Auftrag, und dessen `hase:` entscheidet, welche
Variante läuft.

Ein Entwurf ist ein Entwurf: der Hasenbau trägt nie selbst etwas in
einen Auftrag ein (§8/§10). Lesen, prüfen, selbst eintragen — die
vorgeschlagene `run:`-Zeile steht im Kopf des Skripts.
