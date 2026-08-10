---
# Der Baumeister ist ein ganz normaler Auftrag — sein Material sind
# keine PDFs, sondern Läufe. Gestartet wird er auf Zuruf:
#   hasenbau baumeister <lauf-id|auftrag>
trigger:
  manual: true

gaenge:
  # $INPUT ist hier die Lauf-ID, kein Pfad. Die run-Zeile muss in
  # einfache Anführungszeichen, weil sie mit einem doppelten beginnt.
  - name: trace-ziehen
    run: '"$HASENBAU" dig "$INPUT" > "$WORK/trace.md"'
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
  - file: $WORK/trace.md
---

Im Kontext unten steht der Trace eines einzelnen Laufs: was der Hase
vorhatte, was er tat, was fehlschlug.

Finde darin den deterministischen Teil — die Schritte, die bei jedem
Lauf dieses Auftrags gleich abgelaufen wären — und schreib daraus
**einen** Gang-Entwurf in deinen out-Raum.

Bleibt nichts Deterministisches übrig, schreib keine Datei und sag in
deiner Summary, warum.
