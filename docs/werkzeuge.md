# Werkzeuge: vom Entwurf zur Freigabe

Neben den Gängen, die **vor** dem Hasen laufen, gibt es Werkzeuge, die
er **während** seines Laufs ruft. Sie liegen als Skript plus Manifest
unter `tools/` und werden vom Bau-Plugin beim Server-Start registriert;
geschrieben hat sie der Schmied, freigegeben ein Mensch.

Die Freigabe ist zweistufig: erst wandert die Datei aus `tools/entwurf/`
nach `tools/`, dann nennt ein Auftrag sie in seinem `tools:`. Ohne
Eintrag bekommt ein Hase kein Werkzeug — ein neu gebautes soll nicht
dadurch bei allen landen, dass niemand es verboten hat. `hasenbau get
tools` zeigt, was es gibt und wer es rufen darf.

## Warum überhaupt drei Stufen

**Ein Entwurf ist Code, den ein Modell geschrieben und niemand gelesen
hat.** Der Schmied hat wie jeder Hase kein `bash` und kann sein Skript
kein einziges Mal ausführen — er liefert also Ungetestetes. Der erste
echte Schmied-Lauf zeigte das: tadelloses Manifest, plausibel
aussehendes Python, und beim ersten Aufruf ein Absturz.

Deshalb geht ein Werkzeug durch drei Stufen, und jede setzt die vorige
voraus:

```bash
hasenbau tool review --next        # lesen und verantworten
hasenbau tool test <name> --…      # ausführen und zeigen, was kommt
hasenbau tool release <name>       # Ausgabe bestätigen, nach tools/ verschieben
```

## Die Zustände

Der Zustand dazwischen heißt nach der Intentionssemantik des IRS
([ValIntent](https://github.com/KIT-IRS/Intent-Semantics)):
`generated` (geschrieben, ungelesen) → `hypothetical` (behauptet, nicht
bestätigt) → `actual` (ein Mensch hat die Ausgabe für richtig
befunden). Ein gescheiterter Probelauf macht `invalid`, eine Änderung
nach dem Review `outdated`. Klassifiziert wird durch Verifikation, nicht
durch Setzen — man kann sich `actual` nicht geben.

Dabei zählt der Probelauf **in nur eine Richtung**: scheitert er,
widerlegt er das Review (`invalid`); besteht er, bestätigt er es nicht.
Exit 0 heißt „es lief", nicht „es stimmt" — ob 24 die richtige
Zeilenzahl war, sieht kein Exit-Code. Deshalb bleibt ein bestandener
Probelauf `hypothetical`, und `release` fragt, bevor es verschiebt: *War
die Ausgabe richtig?* Wer bejaht, steht mit Namen in der Datei.

## Der Review-Block

`review` schreibt einen Block in den Kopf des Skripts: wer gelesen hat,
was er glaubt, dass es tut, warum er es für unbedenklich hält — und
einen Hash über das Skript **ohne** diesen Block. Probelauf und Freigabe
tragen ihre Zeilen nach:

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
gegenüber. Das Plugin prüft den Hash bei jedem Server-Start und
registriert nur, was durchgeht.

`#` und `//` sind beide erlaubt, aber innerhalb eines Blocks nur
**eines** — gemischt wird nicht, und `hasenbau-review-end` muss ihn
abschließen. Beide Regeln haben einen Grund, den man einmal gesehen
haben muss: eine `//`-Zeile in einem Bash-Skript ist kein Kommentar,
sondern ein ausführbarer Pfad, und ohne Schlusszeile schluckt der Block
den ersten Kommentar des Skripts und macht sein eigenes Review sofort
ungültig.

Die Zeile `valintent:` ist **Auskunft, nicht Wahrheit**: der Zustand
wird jedes Mal neu ausgerechnet. Wer eine freigegebene Datei ändert,
hinterlässt darin ein `actual`, das nicht mehr stimmt; `describe tool`
nennt die Abweichung, und der Daemon zieht die Zeile beim Start nach —
nur diese eine, der Hash bleibt unangetastet.

Der Block ist ein Format, kein Befehl — er lässt sich von Hand oder mit
einer GUI erzeugen; der Hasenbau prüft nur die Eigenschaft, nie die
Herkunft. Das heißt auch: **er beweist kein Review.** Ein Block mit
richtig gerechnetem Hash kommt durch, auch mit `reviewed-by: niemand`.
Was er verhindert, ist stilles Abdriften, und was er erzwingt, ist ein
Name — mehr nicht.

## Der Probelauf

`test` ist **keine Sicherheitsprüfung**: er führt das Skript aus und ist
gegen bösartigen Code nicht die Abwehr, sondern die Ausführung. Genau
deshalb verlangt er vorher ein Review — gelesen wird zuerst.

Das ist kein hypothetischer Einwand. Ein Hase liest fremdes Material,
darin stehen eingeschleuste Anweisungen, er stellt daraufhin einen
Werkzeug-Wunsch, und der Schmied baut, was im Wunsch steht — am Ende
dieser Kette liegt Python, das im Server-Prozess laufen soll. Die
einzige Stelle, an der das auffällt, ist ein Mensch, der den Entwurf
liest. Dafür ist er kurz und in einer Datei.

Weil eine Grenze besser ist als eine Ermahnung, läuft der Probelauf in
einem Sandkasten (`bwrap`): **kein Netz, der Bau nur lesbar, kein
`$HOME`, Zeitlimit eine Minute.** So fällt auch auf, wenn ein Werkzeug
heimlich nach draußen telefoniert. Fehlt `bwrap`, läuft es wie ohne —
und der Befehl sagt das laut, statt es zu verschweigen.

Ein Sandkasten ändert allerdings das Ergebnis: ein Werkzeug, das legitim
in einen Raum schreibt, scheitert darin. Dafür gibt es `-no-sandbox`,
den Lauf unter Ernstfall-Bedingungen. Und deshalb widerlegt ein
Fehlschlag im Sandkasten nicht von selbst — er kann vom Werkzeug kommen
oder von dessen Grenzen. Der Befehl fragt dann: *Lag es am Werkzeug?*
Wer bejaht, setzt `invalid`; ohne Antwort bleibt der Zustand, wie er
war.

## Die Grenze im Betrieb

Im Betrieb gilt eine zweite, engere Grenze: **ein Werkzeug darf nie mehr
als der Hase, der es ruft.** Es sieht die Räume seines Auftrags —
lesen, was der Hase liest, schreiben, was er schreibt (`work` und
`out`) — und sonst nichts: kein fremder Raum, nichts außerhalb des
Baus, kein Netz. Die Grenze rechnet der Hasenbau beim Laden der Aufträge
aus, angewendet wird sie vom Bau-Plugin.

Fehlt `bwrap`, wird im Betrieb **kein** Werkzeug registriert — anders
als beim Probelauf, wo ein Mensch die Warnung liest. Ein fehlendes
Werkzeug fällt auf, eine fehlende Grenze nicht; `hasenbau describe bau`
meldet den Fall.
