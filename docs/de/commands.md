# Befehle

[English](../commands.md) · **Deutsch**. Übersetzung; maßgeblich ist die englische Fassung.

Die vollständige Referenz. Die Kurzfassung steht im
[README](../../README.de.md#befehle); jeder Befehl kennt außerdem
`hasenbau <befehl>` ohne Argumente als Hilfe.

Jeder Befehl braucht den Bau: entweder `-bau ~/meinbau` vor dem
Unterbefehl oder einmal `cd ~/meinbau`, denn der Vorgabewert ist das
aktuelle Verzeichnis.

## Anlegen und ergänzen

```bash
hasenbau init <bau>        # Bau anlegen (Git-Repo, isolierte Config, Rückkanal, Sonder-Hasen)
hasenbau fix               # ergänzen, was einem bestehenden Bau fehlt
hasenbau new hase <name>   # Template-Gerüst anlegen, kommentiert
hasenbau new auftrag <name> -hase <hase>   # Auftrags-Gerüst anlegen
hasenbau new dockerfile    # Dockerfile und docker-compose.yml, lauffähig
```

`init` und `fix` sind nicht-destruktiv und idempotent: vorhandene
Dateien bleiben unangetastet. `new` ebenso, für alle drei Ressourcen.

`new dockerfile` ist die eine Ressource ohne Namen — und die eine, die
zwei Dateien schreibt: `Dockerfile` und `docker-compose.yml`. Was schon
dasteht, bleibt liegen, während die andere trotzdem entsteht; wer das
Dockerfile von Hand angefasst hat, bekommt die Compose-Datei also
weiterhin.

## Im Container

`hasenbau new dockerfile` schreibt das Rezept, das der Install-Abschnitt
des README als Prosa beschreibt — dazu ein `docker-compose.yml`, das es
laufen lässt:

```bash
hasenbau new dockerfile
docker compose run --rm hasenbau describe bau   # prüfen, bevor scharf geschaltet wird
docker compose up -d                            # der Daemon
docker compose logs -f
docker compose run --rm hasenbau lauf <auftrag> # ein Auftrag von Hand
```

Ins Image kommt, was **Hasenbau** ruft — `opencode`, `git`, `bwrap`,
`sh`, `python3` — dazu `ca-certificates` und `tzdata`. Was die eigenen
Gänge rufen, steht nicht drin: das kennt der Hasenbau nicht und rät es
auch nicht, dafür endet die Datei mit einem markierten Block.

In der Compose-Datei hören die Flags auf, Fußnoten zu sein: sie
deklariert `security_opt: [seccomp:unconfined]`, den Bau als
Bind-Mount, `TZ: ${TZ:-Europe/Berlin}`, `restart: unless-stopped` (was
`Restart=always` für die systemd-Unit ist) und `init: true` — hasenbau
ist dort PID 1 und startet opencode sowie jeden Gang, also muss jemand
verwaiste Kinder einsammeln.

Eines lohnt sich einmalig: `build: .` schickt dieses ganze Verzeichnis
als Kontext an den Docker-Daemon, `archiv/` eingeschlossen, obwohl das
Dockerfile nichts daraus kopiert. Ein `.dockerignore` mit einem
einzelnen `*` reduziert das auf nichts, und der Build läuft trotzdem —
gemessen wurden aus 200 MB Kontext 42 Byte.

Zwei Dinge liest sie aus dem Bau, statt sie anzunehmen: die Provider-IDs
aus `.opencode-home/opencode/opencode.json`, damit die
Credential-Kommentare die eigenen Provider nennen, und die Fassung des
`opencode` im PATH, auf die die Installer-Zeile gepinnt wird.

Drei der `docker run`-Flags im erzeugten Kopf sind nicht optional, und
jedes scheitert, ohne es zu sagen:

| Flag | Ohne es |
|---|---|
| `--security-opt seccomp=unconfined` | `bwrap` bekommt keinen User-Namespace, das Plugin registriert **kein** Werkzeug — nur der Daemon-Log erwähnt es |
| der Schlüssel (unten) | der Provider weist ab, der Lauf endet als `failed` |
| `-e TZ=…` | cron-Trigger laufen in UTC, ein `0 10 * * *` feuert um zwölf |

Für das erste ist `describe bau` kein Beleg. Es prüft, ob `bwrap`
*installiert* ist, nicht ob es seine Aufgabe erfüllen kann — im
Container gemessen meldete es `ok  Tools`, während `bwrap` selbst mit
`No permissions to create new namespace` antwortete. Solange dieser
Check nicht schärfer ist, fragt man `bwrap` direkt:

```bash
docker run --rm --security-opt seccomp=unconfined --entrypoint bwrap meinbau \
  --ro-bind / / --unshare-all --die-with-parent -- /bin/true
```

Eines nimmt das Image bereits ab: der Bau kommt als Mount und gehört
dem Nutzer auf dem Host, nicht root im Container. Git würde ihn deshalb
nicht ansehen (`dubious ownership`), und `describe bau` meldete dann
einen Bau `without a commit`, der in Wahrheit welche hat — kein
sichtbarer Commit heißt keine Projekt-ID und keine Raum-Permissions
(PLAN.md §11.5). Das erzeugte Dockerfile trägt den passenden
`safe.directory`-Eintrag ein.

### Credentials im Container

Nie einen Schlüssel ins Dockerfile. `COPY` und `ENV` landen in den
Image-Layern und bleiben über `docker history` lesbar; sie in einem
späteren Layer zu entfernen, entfernt sie nicht.

`auth.json` braucht der Container gar nicht. `options.apiKey` versteht
`{file:PFAD}` und `{env:VAR}`, der Schlüssel kann also als eingehängte
Datei kommen, während die Bau-Config schlüssellos bleibt:

```json
"provider": {"<id>": {"options": {"apiKey": "{file:/run/secrets/<id>-key}"}}}
```

Die Compose-Datei deklariert das als Secret, zu tippen ist also nichts:

```yaml
secrets:
  <id>-key:
    file: ${HOME}/.secrets/<id>-key
```

Zwei Eigenschaften davon, beide gemessen: die Datei erscheint drinnen
als `/run/secrets/<id>-key` und **übernimmt die Rechte der Host-Datei
unverändert** — ein `chmod 600` bleibt also erhalten. Und fehlt die
Datei, hält `docker compose up` mit `secret file … does not exist` an,
statt zu starten. Das ist Absicht: ein Bau ohne Schlüssel bringt keinen
Lauf zustande, und ein Container, der startet und dann jeden Lauf
scheitern lässt, ist schlimmer als einer, der sich weigert.

**Verschlüsselung hilft *im* Container nicht** — der Prozess braucht den
Klartext in dem Moment, in dem er den HTTPS-Call macht. Überall sonst
hilft sie, und das Muster ist werkzeugunabhängig: verschlüsselt auf dem
Host liegen lassen, für den Lauf in den Speicher (ein tmpfs)
entschlüsseln, das einhängen und danach schreddern. Mit
[age](https://age-encryption.org) als austauschbarem Beispiel:

```bash
age -d -i ~/.age/key ~/.secrets/scc-key.age > /run/user/$UID/scc-key
docker run --rm -v "$PWD":/bau \
  --mount type=bind,src=/run/user/$UID/scc-key,dst=/run/secrets/scc-key,ro \
  meinbau daemon
shred -u /run/user/$UID/scc-key
```

`pass`, `sops`, `gpg` oder ein systemd-Credential passen in dieselbe
Form.

Zwei Alternativen, jede mit ihrem Preis. Das ganze Datenverzeichnis zu
teilen (`-v "$HOME/.local/share/opencode":/root/.local/share/opencode`)
teilt auch `opencode.db`, `storage/` und `log/` mit dem
Alltags-opencode — es ist aber der einzige Weg, auf dem ein
**OAuth**-Login im Container erneuerbar bleibt, denn solche Einträge
werden neu geschrieben. Eine Umgebungsvariable (`{env:…}` plus
`--env-file`) ist am schnellsten getippt und am weitesten offen: der
Schlüssel steht in `docker inspect` und in der Umgebung jedes Prozesses
im Container, auch der eines Schmied-Werkzeugs.

Eine Asymmetrie ist zu erwarten: `hasenbau provider fetch` und
`get provider` lesen `auth.json` direkt. Auf dem Datei-Weg melden sie
`no auth.json`, während die Läufe einwandfrei laufen. Die Modell-Liste
also auf dem Host holen — sie ist ohnehin versionierter Bau-Inhalt.

## Auslösen

```bash
hasenbau daemon                  # Trigger scharf schalten (cron + watch)
hasenbau lauf <auftrag> [datei]  # Auftrag manuell triggern
hasenbau baumeister [-finding N] <ziel>   # Baumeister ansetzen
```

Das zweite Argument von `lauf` ist der Auslöser, bei einem watch-Auftrag
also die Datei, die sonst der Watcher gefunden hätte (`$TRIGGER_FILE`).
Gänge laufen mit dem Bau-Root als Arbeitsverzeichnis, der Pfad ist
deshalb Bau-relativ.

## Nachsehen

```bash
hasenbau get auftraege     # was der Bau kennt
hasenbau get hasen         # Templates, Modelle, wer sie benutzt
hasenbau get gaenge        # Gang-Skripte, wer sie ruft, offene Entwürfe
hasenbau get tools          # freigegebene Werkzeuge und wer sie rufen darf
hasenbau get tools -drafts  # was auf Review wartet
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

Die lesenden Befehle folgen dem Vorbild von `kubectl`. `get` zeigt eine
Zeile pro Objekt, `describe` ein Objekt im Detail samt allem, was der
Hasenbau darüber weiß, bei einem Lauf also auch die Notizen aus dem
Rückkanal. `describe` ist kein `cat`: Dateien werden nie im Volltext
ausgegeben, wohl aber ihr Pfad genannt. `new` legt ein Objekt an. Die
Verben zum Auslösen (`lauf`, `baumeister`, `daemon`) bleiben davon
unberührt.

`describe bau` prüft: Layout, Git-Commit, Bau-Config, Rückkanal-Binary,
generierte Agenten, liegengebliebene `$WORK`-Reste. `status` zeigt nur.
Die beiden unauffälligen Prüfungen tragen am weitesten, der Root-Commit
und der Rückkanal-Eintrag: zeigt der auf ein verschwundenes Binary, nimmt
er den Hasen still ihre Werkzeuge weg.

## Verdichten

```bash
hasenbau dig [-json] <ziel>  # Material für den Baumeister: <lauf-id> oder <auftrag>#<n>
hasenbau findings <auftrag>  # was sich über die Läufe rechnen lässt (kein Modell)
```

Ausführlich in [Verdichtung](distillation.md).

## Werkzeuge freigeben

```bash
hasenbau tool review [<name>|--next]       # lesen und verantworten
hasenbau tool test <name> --<arg> <wert>   # im Sandkasten ausführen und zeigen, was kommt
hasenbau tool test <name> -no-sandbox …    # dasselbe unter Ernstfall-Bedingungen
hasenbau tool release <name>               # Ausgabe bestätigen und freigeben (macht actual)
```

Die drei Verben laufen in dieser Reihenfolge, jeder Schritt setzt den
vorigen voraus. Warum, steht in [Werkzeuge](tools.md).

## Provider

```bash
hasenbau provider fetch <id>  # Modell-Liste beim Provider-Endpoint holen
```

Geschrieben wird nie automatisch: erst zeigt der Befehl den Diff.
Warum ein Bau seine Provider überhaupt selbst mitbringt, steht in
[Architektur](architecture.md#provider-im-bau).

## Nicht von Hand

```bash
hasenbau mcp               # Rückkanal über stdio (startet opencode selbst)
hasenbau sandbox-incident   # meldet einen Werkzeug-Aufruf aus der Sandbox heraus
```

Beide sind Selbstaufrufe: `mcp` startet opencode, `sandbox-incident`
ruft der Wächter im opencode-Server.
