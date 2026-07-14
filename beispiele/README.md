# Beispiele — der Referenz-Auftrag pdf-einlagern

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
