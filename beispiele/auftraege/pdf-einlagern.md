---
trigger:
  # Das Muster allein — der Eingang steht unten als raeume: input:,
  # beobachtet wird die Summe aus beidem.
  watch: "*.pdf"
  debounce: 5s

gaenge:
  - name: pdf-zu-markdown
    run: python3 gaenge/pdf_to_md.py "$TRIGGER_FILE" --out "$WORK/extrakt.md"
    timeout: 120s

hase: archivar

# Befunde dieses Auftrags stehen in `hasenbau status`. Steuert nur die
# Meldung — `hasenbau findings pdf-einlagern` rechnet auch ohne.
monitored: true

raeume:
  input: raeume/laderampe/sources/
  work:  raeume/laderampe/work/
  out:   raeume/lager/
  done:  raeume/archiv/
  quarantine: raeume/quarantaene/

context:
  - file: $WORK/extrakt.md
  - last_summaries: 3

after:
  - move: $TRIGGER_FILE -> raeume/archiv/
---

Der extrahierte Text eines PDFs liegt im Kontext unten (aus
`$WORK/extrakt.md`). Fasse ihn zusammen, vergib Tags, und lege ihn
strukturiert in `raeume/lager/` ab.

Dateiname: `YYYY-MM-DD-<slug>.md` — das Datum steht als „Extrahiert
am:" im Kopf des Extrakts.

Aufbau der Datei: Titelzeile, dann eine Zeile `Tags: tag1, tag2, …`,
dann eine kurze Zusammenfassung, dann der Volltext.

Antworte am Ende mit genau einer Zeile: was du wo abgelegt hast.
