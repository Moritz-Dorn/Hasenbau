# Hasenbau 🐇: die Hasen arbeiten nachts, du liest morgens nach

[![license](https://img.shields.io/badge/license-EUPL--1.2-blue?style=flat-square)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.25%2B-00ADD8?style=flat-square&logo=go&logoColor=white)](go.mod)
[![Plattform](https://img.shields.io/badge/Plattform-Linux-lightgrey?style=flat-square)](#install)

[English](README.md) · **Deutsch**. Übersetzung; maßgeblich ist die englische Fassung.

Ein Daemon, der [opencode](https://opencode.ai) headless orchestriert:
zeitgesteuerte und dateigetriggerte Agenten-Aufträge mit deterministischer
Vorverarbeitung, lokal, ein Binary, kein Cloud-Dienst. Für wiederkehrende
Arbeit, bei der niemand danebensitzen soll.

Ein Auftrag ist eine Datei. Diese hier wartet auf PDFs, wandelt sie ohne
Modell nach Markdown und gibt erst das Ergebnis an den Hasen:

```yaml
trigger:  {watch: "*.pdf", debounce: 5s}
gaenge:   [{name: pdf-zu-markdown, run: 'python3 gaenge/pdf_to_md.py "$TRIGGER_FILE" --out "$WORK/extrakt.md"'}]
hase:     archivar
raeume:   {input: raeume/laderampe/sources/, work: raeume/laderampe/work/, out: raeume/lager/}
context:  [{file: $WORK/extrakt.md}, {last_summaries: 3}]
after:    [{move: $TRIGGER_FILE -> raeume/archiv/}]
```

Ungekürzt liegt er in
[`beispiele/`](beispiele/auftraege/pdf-einlagern.md) und läuft
Ende-zu-Ende. Wie die Teile zusammenstecken, steht in
[docs/de/architecture.md](docs/de/architecture.md), das Warum in
[`PLAN.md`](PLAN.md).

## Install

`opencode` muss im PATH sein, `bwrap` (bubblewrap) für die
Werkzeug-Sandbox, `pdftotext` (poppler) für den Referenz-Auftrag.

```bash
go install github.com/Moritz-Dorn/Hasenbau/cmd/hasenbau@latest
```

Die Binärdatei landet in `$(go env GOPATH)/bin`, meist `~/go/bin` —
dieses Verzeichnis muss im PATH liegen.

## Der erste Bau

Der Bau liegt außerhalb dieses Repos: ein Hase liest die `AGENTS.md` in
seinem Arbeitsverzeichnis, und die hier gehört zum Bauen des Hasenbaus,
nicht zum Einsortieren von PDFs.

### 1. Bau anlegen

```bash
hasenbau init ~/meinbau
cd ~/meinbau
```

Das legt das Layout an, schreibt die beiden Sonder-Hasen (Baumeister und
Schmied, je als Auftrag und Hase) sowie den Sandbox-Wächter hinein, macht
den Bau zu einem Git-Repo mit Root-Commit und trägt den Rückkanal in die
Bau-Config ein. Jeder weitere Befehl braucht den Bau: entweder `-bau
~/meinbau` vor dem Unterbefehl oder einmal `cd`, denn der Vorgabewert ist
das aktuelle Verzeichnis.

Der Wächter ist die eine Datei im Bau, die der Hasenbau für sich behält:
sie wird aus dem Binary neu geschrieben, sobald sie abweicht — ein
Upgrade des Hasenbaus erreicht damit auch einen bestehenden Bau. Alles
andere bleibt, wie du es hinterlassen hast. Näheres in
[Architektur](docs/de/architecture.md).

### 2. Provider eintragen

Ein Bau bringt seine Provider selbst mit; `auth.json` teilt nur die
Schlüssel, nicht die Definitionen. Das Gerüst gehört von Hand in den
`provider:`-Block von `.opencode-home/opencode/opencode.json`. Vorlage
und Begründung stehen in
[docs/de/architecture.md](docs/de/architecture.md#provider-im-bau), die
Modell-Liste holt danach `hasenbau provider fetch <id>`.

### 3. Den Referenz-Auftrag übernehmen

```bash
cp -f <hasenbau-repo>/beispiele/auftraege/pdf-einlagern.md auftraege/
cp -f <hasenbau-repo>/beispiele/hasen/archivar.md          hasen/
cp -f <hasenbau-repo>/beispiele/gaenge/pdf_to_md.py        gaenge/
```

Ein Handgriff bleibt: das `model:` in `hasen/archivar.md` muss auf ein
Modell zeigen, das der Bau aus Schritt 2 kennt.

### 4. Nachsehen, ob alles steht

```bash
hasenbau describe bau
```

Genau ein Punkt darf hier offen sein:

```
PRÜFEN  Agenten        nicht generiert: pdf-einlagern__archivar
                       → der nächste Daemon- oder Lauf-Start schreibt sie
```

Das ist die Reihenfolge: den Agenten erzeugt der Hasenbau, wenn er die
Definitionen lädt. Layout, Git-Commit, Bau-Config und Rückkanal-Binary
müssen `ok` sein; das sind die Dinge, die man sonst erst an einem Lauf
merkt, der komisch aussieht.

### 5. Der erste Lauf, von Hand

Erst einen gezielten Lauf, dann die Trigger, so sieht man den Fehler am
Auftrag und nicht am Daemon:

```bash
mkdir -p raeume/laderampe/sources
cp -f ~/irgendwas.pdf raeume/laderampe/sources/
hasenbau lauf pdf-einlagern raeume/laderampe/sources/irgendwas.pdf
```

Das `mkdir` ist nur beim allerersten Mal nötig: `raeume/` ist nach `init`
leer, angelegt wird beim Lauf, was der Auftrag nennt. Das zweite Argument
ist der Auslöser, also die Datei, die sonst der Watcher gefunden hätte
(`$TRIGGER_FILE`), Bau-relativ wie das Arbeitsverzeichnis der Gänge.

### 6. Ansehen, was passiert ist

```bash
hasenbau get laeufe            # eine Zeile je Lauf: Status, Dauer, Kosten
hasenbau describe lauf <id>    # Notizen aus dem Rückkanal, Fehler, Tool-Calls
```

Ging der Lauf schief, steht der Grund in `describe lauf`, und das
`$WORK`-Verzeichnis bleibt absichtlich liegen, mit dem Log jedes Gangs
darin. `describe bau` zählt solche Reste ab da als offenen Punkt: sie
sind Nachlass zum Ansehen.

## Begriffe

| Begriff | Bedeutung |
|---|---|
| Bau | Root-Verzeichnis des Systems |
| Raum | Verzeichnis im Materialfluss (`laderampe/`, `lager/`, `archiv/`, `quarantaene/`) |
| Gang | Deterministisches Skript, läuft vor dem Hasen. Kein LLM |
| Hase | Template in `hasen/`; daraus generiert der Daemon pro Auftrag×Hase einen opencode-Agenten, Permissions kommen aus den Räumen des Auftrags |
| Auftrag | Trigger + Gänge + Hase + Räume |
| Lauf | Eine Ausführung eines Auftrags |

Ein „Bau" ist eine mit `hasenbau init` erzeugte Instanz, nicht dieses
Repo.

## Im Alltag

`hasenbau daemon` schaltet die Trigger scharf (cron + watch) und läuft im
Vordergrund, mit dem Log auf stderr. Beendet wird er mit Ctrl-C oder
`SIGTERM`; er meldet `sauber beendet` und geht mit 0. Ein Lauf, der dabei
mitten in der Arbeit ist, wird als `aborted` geschlossen; sein
`$WORK`-Verzeichnis bleibt liegen, `describe bau` erinnert später daran.

Definitionen liest der Daemon beim Start. Wer an `auftraege/`, `hasen/`
oder `hasenbau.yaml` etwas ändert, startet ihn neu; Material in den
Räumen ist davon nicht betroffen, das ist ja der Trigger.

Ein `hasenbau lauf` daneben ist erlaubt und der normale Weg, einen
einzelnen Auftrag zu prüfen: er startet seinen eigenen opencode-Server
auf eigenem Port, die SQLite teilen sich beide im WAL-Modus.

Wird der Prozess hart abgeschossen (`kill -9`, Stromausfall), bleiben
Läufe als `running` stehen. Der nächste Start räumt sie ab: lebt der
Wirt-Prozess nicht mehr, wird die Zeile als `aborted` mit Grund
geschlossen und steht im Log. Ein gleichzeitig laufender zweiter Hasenbau
bleibt unangetastet.

Dauerhaft läuft er als systemd-User-Unit, Vorlage in
[docs/de/architecture.md](docs/de/architecture.md#als-systemd-unit). `opencode`
muss im PATH der Unit stehen, der Daemon startet es als Kind-Prozess.

```bash
systemctl --user enable --now hasenbau    # starten, und beim Login mit
journalctl --user -u hasenbau -f          # zusehen
hasenbau status                           # was liegt hier, was ist passiert
```

## Grenzen

Jeder generierte Agent bekommt dieselben sechs Verbote, unabhängig vom
Template: `bash`, `webfetch`, `websearch`, `external_directory`, `task`
und `question` stehen als `deny` in seinem `permission:`-Block. Sie
tauchen in der Werkzeugliste des Modells damit gar nicht erst auf, und
der Hase sucht keinen Weg um sie herum.

Was er stattdessen bekommt, ist ein Rückkanal: `hasenbau_summary` für die
eine Zeile, was der Lauf getan hat, `hasenbau_notiz` für Beobachtungen
unterwegs, und `hasenbau_tool_request`, mit dem er ein fehlendes Werkzeug
anfordert, statt sich einen Weg an seinen Grenzen vorbei zu suchen
([docs/de/hasen.md](docs/de/hasen.md)).

Aus so einem Wunsch baut der Schmied ein Werkzeug, das ein Hase während
seines Laufs ruft. Ein Entwurf ist Code, den ein Modell geschrieben und
niemand gelesen hat, deshalb drei Stufen, jede setzt die vorige voraus:

```bash
hasenbau tool review --next        # lesen und verantworten
hasenbau tool test <name> --…      # im Sandkasten ausführen und zeigen, was kommt
hasenbau tool release <name>       # Ausgabe bestätigen und freigeben
```

Ein gescheiterter Probelauf widerlegt, ein bestandener bestätigt nicht:
Exit 0 heißt „es lief", nicht „es stimmt". Wie daraus `generated →
hypothetical → actual` wird und warum ein Werkzeug im Betrieb nie mehr
darf als der Hase, der es ruft: [docs/de/tools.md](docs/de/tools.md).

## Drosseln und verdichten

`throttle: {max: 5, per: 1h}` deckelt einen Auftrag auf fünf Läufe je
rollender Stunde; `between: "22:00-06:00"` verschiebt die Arbeit in die
Nacht. Der Rückstau wartet im Dateisystem und übersteht jeden Neustart,
der älteste Input zuerst. In `hasenbau.yaml` gilt derselbe Deckel über
alle Aufträge zusammen ([docs/de/throttling.md](docs/de/throttling.md)).

Ein Hase, der bei jedem Lauf dieselben Tool-Calls macht, ist ein
Interpreter, der jedes Mal neu kompiliert. `hasenbau findings <auftrag>`
rechnet das aus den Läufen aus, ohne ein Modell zu fragen; der Baumeister
macht daraus einen Gang-Entwurf. Aktiviert wird ein generierter Gang nie
automatisch ([docs/de/distillation.md](docs/de/distillation.md)).

## Befehle

| | |
|---|---|
| `init`, `fix`, `new` | Bau anlegen, ergänzen, Gerüste schreiben |
| `daemon`, `lauf`, `baumeister` | auslösen |
| `get <ressource>` | eine Zeile pro Objekt |
| `describe <ressource>` | ein Objekt im Detail, `describe bau` als Diagnose |
| `status` | was liegt hier, was ist passiert |
| `dig`, `findings` | Material und Befunde für die Verdichtung |
| `tool review\|test\|release` | ein Werkzeug freigeben |
| `provider fetch` | Modell-Liste beim Endpoint holen |

Vollständig mit allen Ressourcen und Flags:
[docs/de/commands.md](docs/de/commands.md).

## Development

```bash
go build ./...
go vet ./...
go test ./...    # Integrationstests skippen sich ohne opencode im PATH
```

Mitbauende Agents lesen [`AGENTS.md`](AGENTS.md); der Spec steht in
[`PLAN.md`](PLAN.md), Issue-Tracking läuft über
[beads](https://github.com/gastownhall/beads) (`bd ready`).

## License

[EUPL-1.2](LICENSE).
