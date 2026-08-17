# Befehle

Die vollständige Referenz. Die Kurzfassung steht im
[README](../README.md#befehle); jeder Befehl kennt außerdem
`hasenbau <befehl>` ohne Argumente als Hilfe.

Jeder Befehl braucht den Bau: entweder `-bau ~/meinbau` **vor** dem
Unterbefehl oder einmal `cd ~/meinbau`, denn der Vorgabewert ist das
aktuelle Verzeichnis.

## Anlegen und ergänzen

```bash
hasenbau init <bau>        # Bau anlegen (Git-Repo, isolierte Config, Rückkanal, Sonder-Hasen)
hasenbau fix               # ergänzen, was einem bestehenden Bau fehlt
hasenbau new hase <name>   # Template-Gerüst anlegen, kommentiert
hasenbau new auftrag <name> -hase <hase>   # Auftrags-Gerüst anlegen
```

`init` und `fix` sind nicht-destruktiv und idempotent: vorhandene
Dateien bleiben unangetastet.

## Auslösen

```bash
hasenbau daemon                  # Trigger scharf schalten (cron + watch)
hasenbau lauf <auftrag> [datei]  # Auftrag manuell triggern
hasenbau baumeister [-finding N] <ziel>   # Baumeister ansetzen
```

Das zweite Argument von `lauf` ist der Auslöser — bei einem
watch-Auftrag die Datei, die sonst der Watcher gefunden hätte
(`$TRIGGER_FILE`). Gänge laufen mit dem Bau-Root als
Arbeitsverzeichnis, der Pfad ist deshalb Bau-relativ.

## Nachsehen

```bash
hasenbau get auftraege     # was der Bau kennt
hasenbau get hasen         # Templates, Modelle, wer sie benutzt
hasenbau get gaenge        # Gang-Skripte, wer sie ruft, offene Entwürfe
hasenbau get tools             # freigegebene Werkzeuge und wer sie rufen darf
hasenbau get tools -entwuerfe  # was auf Review wartet
hasenbau get laeufe [-n N] # Historie
hasenbau get lauf <id>     # ein Lauf, eine Zeile
hasenbau get provider      # welche Provider kennt der Bau, welche sind holbar
```

```bash
hasenbau describe bau             # Diagnose: ist dieser Bau in Ordnung?
hasenbau describe auftrag <name>  # Trigger, Gänge, Räume, Schreibrechte
hasenbau describe hase <name>     # effektive Permissions je Auftrag
hasenbau describe gang <datei>    # Zweck und alle Aufträge, die ihn rufen
hasenbau describe tool <name>     # Zustand, Review, wer es rufen darf
hasenbau describe lauf <id>       # ein Lauf mit Notizen, Fehlern, Kosten
hasenbau describe provider <id>   # Endpoint, Schlüssel, und die Modelle des Baus
```

```bash
hasenbau status            # Dashboard: was liegt hier, was ist passiert
```

### Die Verben

Die lesenden Befehle folgen dem Vorbild von `kubectl`: **`get`** zeigt
eine Zeile pro Objekt, **`describe`** ein Objekt im Detail samt allem,
was der Hasenbau darüber weiß — bei einem Lauf also auch die Notizen aus
dem Rückkanal. `describe` ist dabei kein `cat`: Dateien werden nie im
Volltext ausgegeben, wohl aber ihr Pfad genannt. `new` legt ein Objekt
an. Die Verben zum Auslösen — `lauf`, `baumeister`, `daemon` — bleiben
davon unberührt.

Zwei Fragen, zwei Befehle: **`describe bau`** prüft (Layout, Git-Commit,
Bau-Config, Rückkanal-Binary, generierte Agenten, liegengebliebene
`$WORK`-Reste), **`status`** zeigt nur. Am meisten wert sind dabei die
zwei unauffälligsten Prüfungen: der Root-Commit und der
Rückkanal-Eintrag — zeigt der auf ein verschwundenes Binary, nimmt er
den Hasen still ihre Werkzeuge weg.

## Verdichten

```bash
hasenbau dig [-json] <ziel>  # Material für den Baumeister: <lauf-id> oder <auftrag>#<n>
hasenbau findings <auftrag>  # was sich über die Läufe rechnen lässt (kein Modell)
```

Ausführlich in [Verdichtung](verdichtung.md).

## Werkzeuge freigeben

```bash
hasenbau tool review [<name>|--next]       # lesen und verantworten
hasenbau tool test <name> --<arg> <wert>   # im Sandkasten ausführen und zeigen, was kommt
hasenbau tool test <name> -no-sandbox …    # dasselbe unter Ernstfall-Bedingungen
hasenbau tool release <name>               # Ausgabe bestätigen und freigeben (macht actual)
```

Die drei Verben sind eine Reihenfolge, keine Auswahl — warum, steht in
[Werkzeuge](werkzeuge.md).

## Provider

```bash
hasenbau provider fetch <id>  # Modell-Liste beim Provider-Endpoint holen
```

Geschrieben wird nie automatisch: erst zeigt der Befehl den Diff.
Warum ein Bau seine Provider überhaupt selbst mitbringt, steht in
[Architektur](architektur.md#provider-im-bau).

## Nicht von Hand

```bash
hasenbau mcp               # Rückkanal über stdio (startet opencode selbst)
hasenbau sandbox-vorfall   # meldet einen Werkzeug-Aufruf aus der Sandbox heraus
```

Beide sind Selbstaufrufe: `mcp` startet opencode, `sandbox-vorfall`
ruft der Wächter im opencode-Server.
