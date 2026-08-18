---
trigger:
  # The pattern alone — the input lives below as raeume: input:, and
  # what gets watched is the sum of the two.
  watch: "*.pdf"
  debounce: 5s

gaenge:
  - name: pdf-zu-markdown
    run: python3 gaenge/pdf_to_md.py "$TRIGGER_FILE" --out "$WORK/extrakt.md"
    timeout: 120s

hase: archivar

# Findings of this Auftrag appear in `hasenbau status`. This only steers
# the reporting — `hasenbau findings pdf-einlagern` computes them anyway.
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

The extracted text of a PDF is in the context below (from
`$WORK/extrakt.md`). Summarise it, assign tags, and file it in a
structured form under `raeume/lager/`.

File name: `YYYY-MM-DD-<slug>.md` — the date is in the head of the
extract, as "Extracted on:".

Layout of the file: a title line, then one line `Tags: tag1, tag2, …`,
then a short summary, then the full text.

End your answer with exactly one line: what you filed and where.
