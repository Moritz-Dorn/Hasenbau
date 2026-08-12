---
# Der Baumeister ist ein ganz normaler Auftrag — sein Material sind
# keine PDFs, sondern Läufe. Gestartet wird er auf Zuruf:
#   hasenbau baumeister <lauf-id|auftrag>            ein Trace
#   hasenbau baumeister -finding <n> <auftrag>       ein Befund über N Läufe
trigger:
  manual: true

gaenge:
  # $TRIGGER_ARG ist hier eine Lauf-ID oder ein Befund (<auftrag>#<n>),
  # nie ein Pfad — deshalb trägt er bei manual-Aufträgen auch einen
  # anderen Namen als die auslösende Datei eines watch-Auftrags. Die
  # run-Zeile muss in einfache Anführungszeichen, weil sie mit einem
  # doppelten beginnt.
  - name: material-ziehen
    run: '"$HASENBAU" dig "$TRIGGER_ARG" > "$WORK/material.md"'
    timeout: 60s

hase: baumeister

# Verdichten dauert. Gemessen am selben Trace: einmal 12 Minuten, einmal
# über 30 — dazwischen eine Denkpause von 17 Minuten ohne einen einzigen
# Tool-Call. Die Vorgabe von 30 Minuten ist dafür zu knapp.
hase_timeout: 60m

raeume:
  work: raeume/baumeister/work/
  # Das Schreibrecht des Baumeisters entsteht ausschließlich hier:
  # der out-Raum wird zum einzigen erlaubten edit-Pattern des
  # generierten Agenten (PLAN.md §6). entwurf/ und nicht gaenge/,
  # damit kein Lauf einen benutzten Gang überschreiben kann.
  out: gaenge/entwurf/

context:
  - file: $WORK/material.md
---

Im Kontext unten steht das Material: entweder ein gerechneter Befund
über mehrere Läufe samt den Traces, auf denen er beruht — dann sagen
dir die Zahlen bereits, welche Position variiert — oder der Trace eines
einzelnen Laufs, und dann musst du selbst urteilen.

Finde den deterministischen Teil — die Schritte, die bei jedem Lauf
dieses Auftrags gleich abliefen — und schreib daraus **einen**
Gang-Entwurf in deinen out-Raum.

Bleibt nichts Deterministisches übrig, schreib keine Datei und sag in
deiner Summary, warum.
