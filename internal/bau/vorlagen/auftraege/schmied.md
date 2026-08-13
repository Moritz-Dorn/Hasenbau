---
# Der Schmied ist ein ganz normaler Auftrag — sein Material sind die
# Werkzeug-Wünsche der Hasen. Jeder Wunsch löst einen eigenen Lauf aus.
#
# ACHTUNG, eine Kopplung, die niemand für dich prüft: der input-Raum
# unten muss der `requests:`-Raum aus hasenbau.yaml sein, plus `tools/`.
# Wer den einen umhängt, hängt den anderen mit um — sonst wartet der
# Schmied an einem Briefkasten, in den niemand einwirft.
trigger:
  watch: "*.md"
  debounce: 5s

hase: schmied

# Ein Werkzeug zu entwerfen ist Schreibarbeit, keine Sortierarbeit.
hase_timeout: 30m

raeume:
  input: raeume/wuensche/tools/
  work: raeume/schmiede/work/
  # Das Schreibrecht des Schmieds entsteht ausschließlich hier: der
  # out-Raum wird zum einzigen erlaubten edit-Pattern des generierten
  # Agenten (PLAN.md §6). entwurf/ und nicht tools/, damit kein Lauf ein
  # benutztes Werkzeug überschreiben kann — und damit zwischen dem, was
  # ein Modell geschrieben hat, und dem, was ein Hase rufen darf, ein
  # Mensch steht.
  out: tools/entwurf/

context:
  - file: $TRIGGER_FILE
---

Im Kontext unten steht ein Werkzeug-Wunsch. Bau daraus ein Skript und
sein Manifest, beide in deinen out-Raum, beide mit demselben Namen.

Prüfe zuerst, ob der Wunsch überhaupt ein Werkzeug beschreibt: eine
Aufgabe, eine Eingabe, eine Ausgabe. Verlangt er einen Interpreter oder
ist er zu vage, um ihn ohne Raten zu bauen, schreib nichts und sag in
deiner Summary, warum — und was du stattdessen bräuchtest.

Melde am Ende in einer Zeile, wie das Werkzeug heißt und was es tut,
oder warum du keines gebaut hast.
