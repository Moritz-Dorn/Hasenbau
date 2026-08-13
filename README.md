# Hasenbau 🐇

Ein Daemon, der [opencode](https://opencode.ai) headless orchestriert:
zeitgesteuerte und dateigetriggerte Agenten-Aufträge mit deterministischer
Vorverarbeitung — lokal, ein Binary, kein Cloud-Dienst.

**Status: Phase 0 (Fundament), Phase 1 (Aufträge) und Phase 2
(Verdichtung und Rückkanal) sind fertig** — der Referenz-Auftrag
`pdf-einlagern` läuft Ende-zu-Ende, `hasenbau dig` zieht Traces, der
MCP-Rückkanal steht, und der Baumeister verdichtet einen Lauf zu einem
Gang-Entwurf (siehe [`beispiele/`](beispiele/)). `hasenbau findings`
rechnet inzwischen über viele Läufe hinweg. `PLAN.md` ist der Spec.

## Die Idee

1. **Trigger statt Chat.** Aufgaben starten, weil eine Datei in einem
   überwachten Raum landet oder ein Cron-Zeitpunkt erreicht ist — nicht,
   weil jemand tippt.
2. **Deterministisches vor Probabilistischem.** Bevor ein Agent („Hase")
   Material sieht, laufen „Gänge" — normale Skripte ohne LLM. Der Hase
   bekommt aufbereitetes Markdown, nie das rohe PDF.
3. **Verdichtung.** Ein Hase, der bei jedem Lauf dieselben Tool-Calls
   macht, ist ein Interpreter, der jedes Mal neu kompiliert. Der Hasenbau
   loggt die Traces; `hasenbau dig` + der Baumeister-Hase machen daraus
   deterministische Gänge. Aktiviert wird ein generierter Gang **nie
   automatisch**.

## Architektur

```
systemd → hasenbau (ein Go-Binary)
            ├── Scheduler   cron-Trigger
            ├── Watcher     Datei-Trigger (fsnotify)
            ├── Runner      Gänge, dann der Hase
            ├── Store       SQLite (WAL, kein cgo)
            └── Supervisor ──spawnt──> opencode serve (127.0.0.1, Child)
```

Der opencode-Server läuft mit isolierter Config (`XDG_CONFIG_HOME` zeigt
in den Bau): keine Plugins und Hooks aus der Alltags-Config, aber geteilte
Credentials (`auth.json` via `XDG_DATA_HOME`).

## Vokabular

| Begriff | Bedeutung |
|---|---|
| **Bau** | Root-Verzeichnis des Systems |
| **Raum** | Verzeichnis im Materialfluss (`laderampe/`, `lager/`, `archiv/`, `quarantaene/`) |
| **Gang** | Deterministisches Skript, läuft vor dem Hasen |
| **Hase** | Template in `hasen/`; daraus generiert der Daemon pro Auftrag×Hase einen opencode-Agenten — Permissions kommen aus den Räumen des Auftrags |
| **Auftrag** | Trigger + Gänge + Hase + Räume |
| **Lauf** | Eine Ausführung eines Auftrags |

Ein „Bau" ist eine mit `hasenbau init` erzeugte Instanz — nicht dieses
Repo. Sessions ankern immer am Bau-Root; die Hasen sehen nur die Räume
ihres Auftrags.

## Der erste Bau

Sechs Schritte bis zum ersten Lauf. Der Bau liegt **außerhalb dieses
Repos**: ein Hase liest die `AGENTS.md` in seinem Arbeitsverzeichnis, und
die hier gehört zum Bauen des Hasenbaus, nicht zum Einsortieren von PDFs.

**1. Bau anlegen.**

```bash
go build -o ~/bin/hasenbau ./cmd/hasenbau
hasenbau init ~/meinbau
```

Das legt das Layout an, schreibt den Baumeister (Auftrag und Hase)
sowie den Sandbox-Wächter hinein, macht den Bau zu einem Git-Repo mit
Root-Commit und trägt den Rückkanal in die Bau-Config ein. Den Root-Commit braucht
opencode: ohne ihn bekommt der Bau keine eigene Projekt-ID, und die
Raum-Permissions der Hasen greifen nicht.

Jeder weitere Befehl braucht den Bau: entweder `-bau ~/meinbau` **vor**
dem Unterbefehl oder einmal `cd ~/meinbau`, denn der Vorgabewert ist das
aktuelle Verzeichnis. Die Beispiele unten setzen das `cd` voraus.

**2. Provider eintragen.** Ein Bau bringt seine Provider selbst mit;
`auth.json` teilt nur die Schlüssel, nicht die Definitionen. Das Gerüst
gehört von Hand in den `provider:`-Block von
`.opencode-home/opencode/opencode.json`:

```json
"provider": {
  "scc": {
    "npm": "@ai-sdk/openai-compatible",
    "name": "SCC KI Toolbox",
    "options": {"baseURL": "https://beispiel.invalid/api/v1"}
  }
}
```

Die Modell-Liste holt danach `hasenbau provider fetch scc` am Endpoint —
Diff anzeigen, dann auf Zuruf schreiben. `hasenbau get provider` sagt,
was der Bau kennt und was holbar ist.

**3. Den Referenz-Auftrag übernehmen.** Statt bei Null anzufangen:

```bash
cp -f <hasenbau-repo>/beispiele/auftraege/pdf-einlagern.md auftraege/
cp -f <hasenbau-repo>/beispiele/hasen/archivar.md          hasen/
cp -f <hasenbau-repo>/beispiele/gaenge/pdf_to_md.py        gaenge/
```

Zwei Handgriffe bleiben: `pdftotext` (poppler) muss im PATH sein, und das
`model:` in `hasen/archivar.md` muss auf ein Modell zeigen, das der Bau
aus Schritt 2 kennt.

**4. Nachsehen, ob alles steht.**

```bash
hasenbau describe bau
```

Genau ein Punkt darf hier offen sein:

```
PRÜFEN  Agenten        nicht generiert: pdf-einlagern__archivar
                       → der nächste Daemon- oder Lauf-Start schreibt sie
```

Das ist kein Fehler, sondern die Reihenfolge: den opencode-Agenten
erzeugt der Hasenbau aus Template und Auftrag, wenn er die Definitionen
lädt. Alles andere — Layout, Git-Commit, Bau-Config, Rückkanal-Binary —
muss `ok` sein, denn das sind die Dinge, die man sonst erst an einem
Lauf merkt, der komisch aussieht.

**5. Der erste Lauf, von Hand.** Erst einen gezielten Lauf, dann die
Trigger — so sieht man den Fehler am Auftrag und nicht am Daemon:

```bash
mkdir -p raeume/laderampe/sources
cp -f ~/irgendwas.pdf raeume/laderampe/sources/
hasenbau lauf pdf-einlagern raeume/laderampe/sources/irgendwas.pdf
```

Das `mkdir` ist nur beim allerersten Mal nötig: welche Räume es gibt,
sagt der Auftrag, und angelegt werden sie beim Lauf — `raeume/` ist nach
`init` deshalb leer. Das zweite Argument ist der Auslöser — bei diesem
watch-Auftrag also die Datei, die sonst der Watcher gefunden hätte
(`$TRIGGER_FILE`). Gänge laufen mit dem Bau-Root als
Arbeitsverzeichnis, der Pfad ist deshalb Bau-relativ.

**6. Ansehen, was passiert ist.**

```bash
hasenbau get laeufe            # eine Zeile je Lauf: Status, Dauer, Kosten
hasenbau describe lauf <id>    # Notizen aus dem Rückkanal, Fehler, Tool-Calls
```

Ging der Lauf schief, steht der Grund in `describe lauf` und das
`$WORK`-Verzeichnis bleibt absichtlich liegen — mit dem Log jedes Gangs
darin. `describe bau` zählt solche Reste ab da als offenen Punkt: sie
sind Nachlass zum Ansehen.

Erst wenn das stimmt, lohnt der Daemon — der macht dasselbe, nur ohne
dass jemand danebensteht.

## Im Alltag: starten und stoppen

`hasenbau daemon` schaltet die Trigger scharf (cron + watch) und läuft
**im Vordergrund**, mit dem Log auf stderr:

```bash
cd ~/meinbau
hasenbau daemon
```

Beendet wird er mit Ctrl-C oder `SIGTERM`; beides ist derselbe Weg, er
meldet `sauber beendet` und geht mit 0. Ein Lauf, der dabei mitten in der
Arbeit ist, wird als `aborted` geschlossen — sein `$WORK`-Verzeichnis
bleibt liegen, `describe bau` erinnert später daran.

**Definitionen liest der Daemon beim Start.** Wer an `auftraege/`,
`hasen/` oder `hasenbau.yaml` etwas ändert, startet ihn neu; Material in
den Räumen ist davon nicht betroffen, das ist ja der Trigger.

**Ein `hasenbau lauf` daneben ist erlaubt** und der normale Weg, einen
einzelnen Auftrag zu prüfen, während der Daemon läuft: er startet seinen
eigenen opencode-Server auf einem eigenen Port, und die SQLite teilen
sich beide im WAL-Modus. Der Auftrags-Deckel (`throttle:`) gilt für ihn
nicht — wer selbst davorsteht, wartet nicht auf das nächste Fenster —,
gezählt wird sein Lauf trotzdem.

Wird der Prozess hart abgeschossen (`kill -9`, Stromausfall), bleiben
Läufe als `running` in der Datenbank stehen. Der nächste Start von
`daemon` oder `lauf` räumt sie ab: geprüft wird, ob der Wirt-Prozess noch
lebt, tote Zeilen werden als `aborted` mit Grund geschlossen und stehen
im Log. Ein gleichzeitig laufender zweiter Hasenbau bleibt unangetastet.

Dauerhaft läuft er als systemd-User-Unit. `opencode` muss im PATH der
Unit stehen — der Daemon startet es als Kind-Prozess:

```ini
# ~/.config/systemd/user/hasenbau.service
[Unit]
Description=Hasenbau
After=network.target

[Service]
ExecStart=%h/bin/hasenbau -bau %h/meinbau daemon
Environment=PATH=%h/.opencode/bin:/usr/local/bin:/usr/bin:/bin
Restart=on-failure

[Install]
WantedBy=default.target
```

```bash
systemctl --user enable --now hasenbau    # starten, und beim Login mit
systemctl --user stop hasenbau            # stoppen (SIGTERM, sauberes Ende)
journalctl --user -u hasenbau -f          # zusehen
hasenbau status                           # was liegt hier, was ist passiert
```

## Benutzen

Alle Befehle im Überblick:

```bash
hasenbau init <bau>        # Bau anlegen (Git-Repo, isolierte Config, Rückkanal, Baumeister)
hasenbau fix               # ergänzen, was einem bestehenden Bau fehlt
hasenbau new hase <name>   # Template-Gerüst anlegen, kommentiert
hasenbau new auftrag <name> -hase <hase>   # Auftrags-Gerüst anlegen
hasenbau daemon            # Trigger scharf schalten (cron + watch)
hasenbau lauf <auftrag>    # Auftrag manuell triggern
hasenbau get auftraege     # was der Bau kennt
hasenbau get hasen         # Templates, Modelle, wer sie benutzt
hasenbau get gaenge        # Gang-Skripte, wer sie ruft, offene Entwürfe
hasenbau get tools         # Schmied-Werkzeuge, ihre Argumente, wer sie rufen darf
hasenbau get laeufe        # Historie
hasenbau describe bau             # Diagnose: ist dieser Bau in Ordnung?
hasenbau describe auftrag <name>  # Trigger, Gänge, Räume, Schreibrechte
hasenbau describe hase <name>     # effektive Permissions je Auftrag
hasenbau describe gang <datei>    # Zweck und alle Aufträge, die ihn rufen
hasenbau describe lauf <id>       # ein Lauf mit Notizen, Fehlern, Kosten
hasenbau describe provider <id>   # Endpoint, Schlüssel, und die Modelle des Baus
hasenbau dig <ziel>     # Material für den Baumeister: <lauf-id> oder <auftrag>#<n>
hasenbau findings <auftrag>  # was sich über die Läufe rechnen lässt (kein Modell)
hasenbau baumeister [-finding N] <ziel>  # Baumeister ansetzen
hasenbau get provider      # welche Provider kennt der Bau, welche sind holbar
hasenbau provider fetch <id>  # Modell-Liste beim Provider-Endpoint holen
hasenbau status            # Dashboard: was liegt hier, was ist passiert
```

Der Referenz-Auftrag zum Übernehmen liegt in [`beispiele/`](beispiele/).

Jeder Hase bekommt Werkzeuge, mit denen er selbst in die Bau-Datenbank
schreibt: `hasenbau_summary` für die eine Zeile, was der Lauf getan hat
(der nächste Lauf desselben Auftrags bekommt sie als Kontext), und
`hasenbau_notiz` für Beobachtungen unterwegs — sie stehen später in
`hasenbau dig`. Ist in `hasenbau.yaml` ein `requests:`-Raum gesetzt,
kommt `hasenbau_tool_request` dazu: damit fordert ein Hase ein
Werkzeug an, das ihm für seine Aufgabe fehlt, statt sich einen Weg an
seinen Grenzen vorbei zu suchen. Der Wunsch landet als Datei unter
`<requests>/tools/` — dem künftigen Eingang des Schmieds. Ohne den
Eintrag bleibt das Werkzeug aus, und der Hase wird auch im Prompt nicht
darauf verwiesen; `hasenbau describe bau` sagt, woran der Bau gerade
ist. Dahinter steckt ein MCP-Server, den opencode als
`hasenbau mcp` startet; eingetragen wird er von `hasenbau init`, und
jeder Daemon- oder Lauf-Start korrigiert den Eintrag auf das gerade
laufende Binary und sagt es im Log — auch nach einem Rebuild an einen
anderen Pfad.

`hasenbau baumeister <lauf-id|auftrag>` setzt den Baumeister auf einen
Lauf an: er liest dessen Trace und schreibt daraus einen Gang-Entwurf
nach `gaenge/entwurf/`. Mit `-finding <n>` bekommt er stattdessen einen
Befund aus `hasenbau findings` — dann steht schon gerechnet da, welche
Argument-Position über die Läufe variiert, und er muss es nicht aus
einem einzelnen Trace raten. Der Baumeister ist dabei kein Sonderfall im
Code, sondern selbst ein Auftrag mit einem Hasen — sein Material sind
nur Läufe statt PDFs, und sein Gang ist `hasenbau dig`. Sein
Schreibrecht entsteht ausschließlich aus dem `out`-Raum seines
Auftrags; auf `auftraege/` hat er keines. **Ein Entwurf wird nie
automatisch aktiviert** — der Nutzer liest ihn und trägt den Gang
selbst ein. Aus einem einzelnen Trace ist nicht sicher zu erkennen, was
Parameter und was Konstante war; ein Entwurf ist deshalb eine
Gesprächsgrundlage, kein fertiger Gang.

Wer 200 PDFs auf einmal ablegt, will sie selten alle sofort verarbeitet
haben. `throttle: {max: 5, per: 1h}` deckelt einen Auftrag auf fünf
Läufe je rollender Stunde; der Rest wartet in `sources/`, denn die
Warteschlange ist das Dateisystem und übersteht damit jeden Neustart.
Abgearbeitet wird der älteste Input zuerst — je Auftrag von genau einem
Arbeiter, nacheinander.
Gezählt wird aus der Lauf-Historie statt aus einem Zähler im Speicher —
sonst bekäme ausgerechnet ein Crash-Loop nach jedem Neustart frisches
Budget. Gescheiterte Läufe zählen mit: gekostet haben sie trotzdem.
`hasenbau lauf` umgeht den Deckel, zählt aber mit.

`between: "22:00-06:00"` kommt dazu, wenn die Arbeit nur nachts laufen
soll — Ortszeit, über Mitternacht erlaubt. Es begrenzt nur den *Start*:
ein Lauf, der um 05:55 beginnt, läuft zu Ende. Und es verschiebt nur,
statt zu deckeln; wer beides will, setzt beides.

Gedrosselte Aufträge stehen in `hasenbau status` mit ihrem Rückstau und
dem frühesten nächsten Lauf — ein Deckel, den man nicht sieht, ist von
einem hängenden Daemon nicht zu unterscheiden:

```
Gedrosselt (1)
  pdf-einlagern  5 Läufe je 1h, nur 22:00-06:00
                 195 Dateien im Eingang, nächster Lauf frühestens 22:00 (in 8h44m)
```

Darüber steht ein Bau-weiter Deckel: `throttle: {max: 20, per: 1h}` in
`hasenbau.yaml` gilt über **alle** Aufträge zusammen. Der Deckel je
Auftrag schützt einen Auftrag vor sich selbst, dieser das Budget vor
allen — zehn Aufträge mit je 5/h sind 50/h. Er zählt auch cron-Läufe
mit, denn deren Kosten sind dieselben; `hasenbau lauf` wird weiterhin
durchgelassen und mitgezählt.

Ein Auftrag mit `monitored: true` im Frontmatter wird routinemäßig
beurteilt: seine Befunde stehen dann in `hasenbau status`, ohne dass
jemand danach fragt. Das Flag steuert nur die Meldung — aufgezeichnet
wird bei jedem Auftrag alles, und `hasenbau findings <auftrag>` rechnet
auch über die, die es nicht setzen. Wer es später nachträgt, bekommt die
Historie mitgeliefert.

Neben den Gängen, die **vor** dem Hasen laufen, gibt es Werkzeuge, die
er **während** seines Laufs ruft. Sie liegen als Skript plus Manifest
unter `tools/` und werden vom Bau-Plugin beim Server-Start registriert;
geschrieben hat sie der Schmied, freigegeben ein Mensch. Die Freigabe
ist zweistufig: erst wandert die Datei aus `tools/entwurf/` nach
`tools/`, dann nennt ein Auftrag sie in seinem `tools:`. Ohne Eintrag
bekommt ein Hase kein Werkzeug — ein neu gebautes soll nicht dadurch
bei allen landen, dass niemand es verboten hat. `hasenbau get tools`
zeigt, was es gibt und wer es rufen darf.

Jeder generierte Agent bekommt dieselben sechs Verbote, unabhängig vom
Template: `bash`, `webfetch`, `websearch`, `external_directory`, `task`
und `question` stehen als `deny` in seinem `permission:`-Block. Das ist
kein Verbieten, sondern ein Entziehen — die Werkzeuge tauchen in der
Liste des Modells gar nicht erst auf, und der Hase sucht deshalb keinen
Weg um sie herum. `task` ist dabei das wichtigste: ein Subagent wäre ein
eigener Agent und erbte weder die Permissions noch die Raum-Grenzen.
`hasenbau describe hase <name>` zeigt, was für einen Hasen in einem
konkreten Auftrag herauskommt.

Ein Hasen-Template kann Hintergrundwissen anfordern: `knows_hasenbau:
true` bindet eine mitgelieferte Einführung in den Hasenbau ein (Begriffe,
Ablauf, Trace-Aufbau, Grenzen), `knowledge: [pfade]` eigene Dateien aus
dem Bau. Beides landet im generierten Agenten und gilt damit nur für diesen
Hasen — anders als `instructions` in der opencode.json, das
Workspace-weit für *jeden* Agenten gilt. Die Einführung steckt bewusst im
Binary statt im Bau: so passt sie immer zur installierten Version, statt
als veraltete Kopie mitzulaufen.

Die lesenden Befehle folgen dem Vorbild von `kubectl`: **`get`** zeigt
eine Zeile pro Objekt (`get laeufe`, `get lauf <id>`, `get provider`),
**`describe`** ein Objekt im Detail samt allem, was der Hasenbau darüber
weiß — bei einem Lauf also auch die Notizen aus dem Rückkanal. `describe`
ist dabei kein `cat`: Dateien werden nie im Volltext ausgegeben, wohl
aber ihr Pfad genannt. `new` legt ein Objekt an. Die Verben zum
Auslösen — `lauf`, `baumeister`, `daemon` — bleiben davon unberührt.

Zwei Fragen, zwei Befehle: **`describe bau`** prüft (Layout, Git-Commit,
Bau-Config, Rückkanal-Binary, generierte Agenten, liegengebliebene
$WORK-Reste), **`status`** zeigt nur. Am meisten wert sind dabei die zwei
unauffälligsten Prüfungen: der Root-Commit von oben und der
Rückkanal-Eintrag — zeigt der auf ein verschwundenes Binary, nimmt er den
Hasen still ihre Werkzeuge weg.

Dass ein Bau seine custom Provider selbst mitbringt, folgt aus der
Isolation (PLAN.md §3): `auth.json` teilt die Schlüssel, die
Definitionen bleiben im Bau. Deshalb der handgepflegte
`provider:`-Block aus Schritt 2 oben, und deshalb schreibt `hasenbau
provider fetch` die Modell-Liste nie automatisch, sondern zeigt erst den
Diff.

## Build & Test

```bash
go build ./...
go test ./...    # Integrationstests skippen sich ohne opencode im PATH
```

## Nicht-Ziele

Kein Remote/Multi-Host, keine Web-UI, kein eigenes Agent-Framework,
keine automatische Aktivierung generierter Gänge.

## Mehr

- [`PLAN.md`](PLAN.md) — der vollständige Implementierungsplan (Spec)
- [`AGENTS.md`](AGENTS.md) — Instruktionen für AI-Agents, die hier mitbauen
- Issue-Tracking: [beads](https://github.com/gastownhall/beads) (`bd ready`)
