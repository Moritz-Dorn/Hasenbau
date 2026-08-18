# Werkzeuge: vom Entwurf zur Freigabe

[English](../tools.md) · **Deutsch**. Übersetzung; maßgeblich ist die englische Fassung.

Neben den Gängen, die vor dem Hasen laufen, gibt es Werkzeuge, die er
während seines Laufs ruft. Sie liegen als Skript plus Manifest unter
`tools/` und werden vom Bau-Plugin beim Server-Start registriert;
geschrieben hat sie der Schmied, freigegeben ein Mensch.

Die Freigabe ist zweistufig: erst wandert die Datei aus `tools/entwurf/`
nach `tools/`, dann nennt ein Auftrag sie in seinem `tools:`. Ohne
Eintrag bekommt ein Hase kein Werkzeug. Ein neu gebautes soll nicht
dadurch bei allen landen, dass niemand es verboten hat. `hasenbau get
tools` zeigt, was es gibt und wer es rufen darf.

## Warum überhaupt drei Stufen

Ein Entwurf ist Code, den ein Modell geschrieben und niemand gelesen
hat. Der Schmied hat wie jeder Hase kein `bash` und kann sein Skript kein
einziges Mal ausführen, er liefert also Ungetestetes. Der erste echte
Schmied-Lauf zeigte das: tadelloses Manifest, plausibel aussehendes
Python, und beim ersten Aufruf ein Absturz.

Deshalb geht ein Werkzeug durch drei Stufen, und jede setzt die vorige
voraus:

```bash
hasenbau tool review --next        # lesen und verantworten
hasenbau tool test <name> --…      # ausführen und zeigen, was kommt
hasenbau tool release <name>       # Ausgabe bestätigen, nach tools/ verschieben
```

`--next` nimmt den nächsten **ungelesenen** Entwurf. Ein bereits
gelesener bleibt in `get tools -drafts` stehen, bis er freigegeben ist —
er wartet auf den Probelauf, nicht auf eine zweite Lesung. Wer ihn
trotzdem noch einmal lesen will, nennt ihn beim Namen:
`hasenbau tool review <name>`.

## Die Zustände

Der Zustand dazwischen heißt nach der Intentionssemantik des IRS
([ValIntent](https://github.com/KIT-IRS/Intent-Semantics)):
`generated` (geschrieben, ungelesen) → `hypothetical` (behauptet, nicht
bestätigt) → `actual` (ein Mensch hat die Ausgabe für richtig befunden).
Ein gescheiterter Probelauf macht `invalid`, eine Änderung nach dem
Review `outdated`. Klassifiziert wird durch Verifikation; `actual` kann
sich niemand selbst geben.

Der Probelauf zählt nur in eine Richtung: scheitert er, widerlegt er das
Review (`invalid`); besteht er, bestätigt er es nicht. Exit 0 heißt „es
lief", nicht „es stimmt", und ob 24 die richtige Zeilenzahl war, sieht
kein Exit-Code. Deshalb bleibt ein bestandener Probelauf `hypothetical`,
und `release` fragt, bevor es verschiebt: War die Ausgabe richtig? Wer
bejaht, steht mit Namen in der Datei.

## Der Review-Block

`review` schreibt einen Block in den Kopf des Skripts: wer gelesen hat,
was er glaubt, dass es tut, warum er es für unbedenklich hält, dazu einen
Hash über das Skript ohne diesen Block. Probelauf und Freigabe tragen
ihre Zeilen nach:

```python
#!/usr/bin/env python3
# hasenbau-review: 1
# reviewed-by: Moritz Dorn
# reviewed-at: 2026-08-13T14:02:11+02:00
# body-sha256: 9f2c…
# does: zählt die Zeilen einer Datei und gibt die Zahl auf stdout aus
# safe-because: liest nur den übergebenen Pfad, schreibt nichts, kein Netz
# verified-at: 2026-08-13T14:05:30+02:00
# verified-with: --pfad eingang/probe.txt
# verified-exit: 0
# released-by: Moritz Dorn
# released-at: 2026-08-13T14:06:02+02:00
# valintent: actual
# hasenbau-review-end
import argparse
```

Damit ist das Review an genau den Inhalt gebunden, der gelesen wurde:
eine Zeile geändert, und es gilt nicht mehr, auch dem Reviewer selbst
gegenüber. Bei jedem Server-Start fragt das Plugin den Hasenbau
zu jedem Werkzeug (`hasenbau tool state <name>`) und registriert nur,
was als freigegeben zurückkommt. Die Regel steht an einer Stelle — im
Binary —, und das Plugin tut, was der Exit-Code sagt.

`#` und `//` sind beide erlaubt, innerhalb eines Blocks aber nur eines
davon, und `hasenbau-review-end` muss ihn abschließen. Beide Regeln
stammen aus je einem Fehlschlag: eine `//`-Zeile in einem Bash-Skript ist
ein ausführbarer Pfad und kein Kommentar, und ohne Schlusszeile schluckt
der Block den ersten Kommentar des Skripts und macht sein eigenes Review
sofort ungültig.

Die Zeile `valintent:` gibt nur Auskunft. Der Zustand wird jedes Mal neu
ausgerechnet: wer eine freigegebene Datei ändert, hinterlässt darin ein
`actual`, das nicht mehr stimmt; `describe tool` nennt die Abweichung,
und der Daemon zieht die Zeile beim Start nach, nur diese eine, der Hash
bleibt unangetastet.

Der Block ist ein Format und lässt sich von Hand oder mit einer GUI
erzeugen; der Hasenbau prüft die Eigenschaft, nie die Herkunft. Ein
Review beweist er damit nicht: ein Block mit richtig gerechnetem Hash
kommt durch, auch mit `reviewed-by: niemand`. Er verhindert stilles
Abdriften und erzwingt einen Namen.

## Der Probelauf

`test` führt das Skript aus. Bösartigen Code hält er nicht auf, er
startet ihn, und genau deshalb verlangt er vorher ein Review.

Die Kette dahinter ist real. Ein Hase liest fremdes Material, darin
stehen eingeschleuste Anweisungen, er stellt daraufhin einen
Werkzeug-Wunsch, und der Schmied baut, was im Wunsch steht; am Ende liegt
Python, das im Server-Prozess laufen soll. Die einzige Stelle, an der das
auffällt, ist ein Mensch, der den Entwurf liest. Dafür ist er kurz und
steht in einer Datei.

Der Probelauf läuft deshalb in einem Sandkasten (`bwrap`): kein Netz, der
Bau nur lesbar, kein `$HOME`, Zeitlimit eine Minute. So fällt auch auf,
wenn ein Werkzeug heimlich nach draußen telefoniert. Fehlt `bwrap`, läuft
es wie ohne, und der Befehl sagt das laut, statt es zu verschweigen.

Ein Sandkasten ändert das Ergebnis: ein Werkzeug, das legitim in einen
Raum schreibt, scheitert darin. Dafür gibt es `-no-sandbox`, den Lauf
unter Ernstfall-Bedingungen. Ein Fehlschlag im Sandkasten widerlegt
deshalb nicht von selbst, er kann vom Werkzeug kommen oder von dessen
Grenzen. Der Befehl fragt dann: Lag es am Werkzeug? Wer bejaht, setzt
`invalid`; ohne Antwort bleibt der Zustand, wie er war.

## Die Grenze im Betrieb

Im Betrieb gilt eine zweite, engere Grenze: ein Werkzeug darf nie mehr
als der Hase, der es ruft. Es sieht die Räume seines Auftrags, liest,
was der Hase liest, und schreibt, was er schreibt (`work` und `out`).
Sonst nichts: kein fremder Raum, nichts außerhalb des Baus, kein Netz.
Die Grenze rechnet der Hasenbau beim Laden der Aufträge aus, angewendet
wird sie vom Bau-Plugin.

Fehlt `bwrap`, wird im Betrieb gar kein Werkzeug registriert, anders als
beim Probelauf, wo ein Mensch die Warnung liest. Ein fehlendes Werkzeug
fällt auf, eine fehlende Grenze nicht; `hasenbau describe bau` meldet den
Fall.
