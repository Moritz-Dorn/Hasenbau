# Drosseln: Deckel, Nachtfenster, Reihenfolge

[English](../throttling.md) · **Deutsch**. Übersetzung; maßgeblich ist die englische Fassung.

Wer 200 PDFs auf einmal ablegt, will sie selten alle sofort verarbeitet
haben.

## Der Deckel je Auftrag

`throttle: {max: 5, per: 1h}` im Auftrags-Frontmatter deckelt einen
Auftrag auf fünf Läufe je rollender Stunde; der Rest wartet in
`sources/`, denn die Warteschlange ist das Dateisystem und übersteht
damit jeden Neustart. Abgearbeitet wird der älteste Input zuerst, je
Auftrag von genau einem Arbeiter, nacheinander.

Gezählt wird aus der Lauf-Historie statt aus einem Zähler im Speicher.
Sonst bekäme ausgerechnet ein Crash-Loop nach jedem Neustart frisches
Budget. Gescheiterte Läufe zählen mit: gekostet haben sie trotzdem.
`hasenbau lauf` umgeht den Deckel, zählt aber mit.

## Nur nachts

`between: "22:00-06:00"` kommt dazu, wenn die Arbeit nur nachts laufen
soll, in Ortszeit und über Mitternacht erlaubt. Es begrenzt allein den
Start: ein Lauf, der um 05:55 beginnt, läuft zu Ende. Und es verschiebt,
statt zu deckeln; wer beides will, setzt beides.

## Sichtbar

Gedrosselte Aufträge stehen in `hasenbau status` mit ihrem Rückstau und
dem frühesten nächsten Lauf. Ein Deckel, den man nicht sieht, ist von
einem hängenden Daemon nicht zu unterscheiden:

```
Gedrosselt (1)
  pdf-einlagern  5 Läufe je 1h, nur 22:00-06:00
                 195 Dateien im Eingang, nächster Lauf frühestens 22:00 (in 8h44m)
```

## Der Deckel über allem

Darüber steht ein Bau-weiter Deckel: `throttle: {max: 20, per: 1h}` in
`hasenbau.yaml` gilt über alle Aufträge zusammen. Der Deckel je Auftrag
schützt einen Auftrag vor sich selbst, dieser das Budget vor allen, denn
zehn Aufträge mit je 5/h sind 50/h. Er zählt auch cron-Läufe mit, deren
Kosten sind dieselben; `hasenbau lauf` wird weiterhin durchgelassen und
mitgezählt.
