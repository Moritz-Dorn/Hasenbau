# Hasenbau — Implementierungsplan

Ein Daemon, der `opencode` headless orchestriert. Zeitgesteuerte und
dateigetriggerte Aufgaben, mit einer Kontext-Schicht davor und
deterministischen Vorverarbeitungsschritten, die aus Agent-Sessions
heraus verdichtet werden können.

Zielsprache: **Go**. Zielplattform: **lokal** (eine Maschine, gleicher
Rechner wie das interaktive opencode des Nutzers). Remote/Multi-Host ist
explizit kein Ziel.

---

## 1. Vokabular

Diese Begriffe sind verbindlich für Package-Namen, Typen, Verzeichnisse
und DB-Tabellen — und sie sind **die einzigen deutschen Wörter im
Code**, samt Plural (`laeufe`, `gaenge`, `raeume`, `auftraege`). Alles
andere ist englisch: Bezeichner, Formatschlüssel, DB-Spalten und -Werte,
Paketnamen.

Zusammengesetzte Namen mischen deshalb bewusst: **englisches Verb,
deutsches Domänen-Nomen** — `StartLauf`, `EndLauf`, `LaufByID`,
`RecentLaeufe`, `RunGaenge`, `last_lauf`. Die frühere Regel „keine
Mischformen" ist damit hinfällig; sie war nicht durchzuhalten, sobald
ein Verb auf ein Domänen-Nomen trifft (entschieden 2026-08-10).

Prosa bleibt deutsch: Kommentare, dieses Dokument, README, die
Meldungstexte der CLI und Testfunktionsnamen. Die Zustandswerte eines
Laufs (`running`, `ok`, `failed`, `aborted`) sind Daten, keine Prosa —
englisch in der DB und in der Ausgabe.

| Begriff | Technisch |
|---|---|
| **Bau** | Root-Verzeichnis. Das gesamte System, inkl. Config, Räume, State. |
| **Raum** | Benanntes Verzeichnis mit einer Rolle im Materialfluss. Kein Typsystem — nur Name und Pfad. |
| **Hase** | Ein Template in `hasen/` (Markdown + Frontmatter, opencode-Feldvokabular): Prompt, Modell, optionale Zusatz-Einschränkungen. Der probabilistische Teil. Daraus generiert der Daemon pro Auftrag×Hase einen opencode-Agenten — Permissions kommen aus den Räumen des Auftrags, nie aus dem Template. |
| **Gang** | Deterministisches Skript. Transformiert Material, bevor ein Hase es sieht. Kein LLM. |
| **Auftrag** | Trigger + Gänge + Hase + Räume. Die Job-Definition. |
| **Lauf** | Eine Ausführung eines Auftrags. Hat Status, Dauer, Output, Summary. |
| **Baumeister** | Sonder-Hase mit Schreibrecht auf `gaenge/`. Verdichtet Tool-Traces zu Gängen (Phase 2). |

---

## 2. Architektur

```
  systemd
     │
     ▼
  hasenbau (ein Go-Binary, ein Prozess)
     ├── Scheduler      cron-Trigger
     ├── Watcher        Datei-Trigger (fsnotify)
     ├── Runner         führt Gänge aus, dann den Hasen
     ├── Store          SQLite
     └── Supervisor ──spawnt──> `opencode serve` (Child-Prozess)
                                       ▲
                        HTTP/SSE ──────┘
                        (sst/opencode-sdk-go)
```

Die CLI desselben Binaries folgt dem `kubectl`-Schema (§12).

Ein Prozess, ein Binary, `Restart=always`. Der opencode-Server ist ein
Kind des Daemons und stirbt mit ihm — er hängt nie als offener Endpoint
herum. Der Daemon bindet ihn an `127.0.0.1` auf einem freien Port
(Port 0 anfragen und den zugewiesenen lesen, um Kollisionen mit dem
interaktiven opencode des Nutzers auszuschließen).

### Abhängigkeiten

- `github.com/sst/opencode-sdk-go` — offizieller Client, Stainless-generiert
- `github.com/robfig/cron/v3` — Scheduler
- `modernc.org/sqlite` — pure Go, **kein cgo** (sonst nervt Cross-Compile)
- `github.com/fsnotify/fsnotify` — Datei-Trigger
- `gopkg.in/yaml.v3` — Frontmatter

---

## 3. Isolation gegen die Alltags-Config

**Das ist keine Kür, das ist die Grundlage für Reproduzierbarkeit.**

`opencode serve` lädt dieselbe Config-Kaskade wie das interaktive
opencode. Plugins sind dabei serverseitig (Hooks wie
`tool.execute.before`, Event-Subscriptions, Custom Tools) — sie liefen
also in den Automation-Läufen mit. Ein Notification-Plugin würde nachts
um drei feuern; ein Hook könnte das Verhalten von Aufträgen still
verändern. Und weil Config-Arrays (`plugin`, `instructions`) beim Mergen
konkateniert und nicht überschrieben werden, lassen sich Plugins durch
eine Override-Config nicht abschalten. Nur addieren.

Lösung: Der Daemon startet `opencode serve` mit umgebogenem
`XDG_CONFIG_HOME`.

```
XDG_CONFIG_HOME = <bau>/.opencode-home    # eigene, minimale Config. Keine Plugins.
XDG_DATA_HOME   = geerbt vom Nutzer        # → auth.json wird geteilt
```

Damit teilt der Bau die Provider-Credentials mit dem täglichen opencode,
aber sonst nichts. Die Automation-Config ist explizit und versioniert.

`plugin: []` richtet sich dabei gegen die **geerbten** Plugins der
Alltags-Instanz, nicht gegen Plugins an sich: Eigene, im Bau
versionierte Plugins bleiben eine Option (z. B. später Echtzeit-Hooks
wie `tool.execute.before` oder Telemetrie) — sie wären dann Teil der
expliziten Automation-Config, kein stilles Erbe.

**Befund aus dem Smoke-Test (2026-07-12):** auth.json teilt nur die
Schlüssel. *Custom* Provider-Definitionen (`provider:`-Block, z.B. der
KIT-Provider `scc`) sind Config — ohne sie in der Bau-eigenen
`opencode.json` quittiert der Server Prompts mit 500. Ein Bau muss den
`provider:`-Block (und `enabled_providers`) also selbst mitbringen;
`hasenbau init` sollte dafür ein Gerüst anlegen.

Gegen die Drift der Modell-Liste: `hasenbau provider fetch <id>` holt sie
beim Provider selbst — `GET <baseURL>/models`, Schlüssel aus der
geteilten auth.json — und schreibt die `models:`-Map neu (Diff anzeigen,
dann schreiben). **Nie automatisch** beim Server-Start, sonst wäre die
Isolation still unterlaufen. Keys landen nie in der Bau-Config; die
teilt auth.json ohnehin.

Bewusst *nicht* aus der Alltags-Config kopieren: die ist selbst nur ein
handgepflegter Schnappschuss und driftet genauso. Belegt am 2026-08-04 —
Bau-Config und Alltags-Config des `scc`-Providers wichen gegenseitig ab
(`mokrates`/`oktobunny` nur im Bau, `kit.deepseek-v4-flash-0731` nur im
Alltag), während der Endpoint 35 Modelle mit `id`, `name`,
`connection_type` und Capabilities liefert. Ein Sync würde einen
veralteten Stand in den nächsten kopieren; der Fetch fragt die Quelle.

Arbeitsteilung: Das Gerüst (`npm`, `name`, `options.baseURL`) ist
handgepflegt und Voraussetzung des Fetch — ohne `baseURL` kein Endpoint.
Der Fetch füllt nur `models:` und ergänzt die ID in
`enabled_providers`. Betroffen sind ausschließlich *custom* Provider;
eingebaute (anthropic, openai …) zieht opencode aus models.dev, da
genügen `enabled_providers` und auth.json.

> ✅ **Verifiziert (2026-07-11, opencode 1.15.13):** opencode folgt
> `XDG_CONFIG_HOME` strikt. Test: Server mit umgebogenem
> `XDG_CONFIG_HOME` gestartet; `GET /config` zeigt `plugin: []` und
> den Marker aus der isolierten Config, Schlüssel der Alltags-Config
> (`model`, `enabled_providers`) und deren Agents fehlen. Kein
> Fallback nötig. **Pfad-Detail:** Die Config liegt unter
> `$XDG_CONFIG_HOME/opencode/opencode.json` — der Bau braucht also
> ein `opencode/`-Unterverzeichnis in `.opencode-home/` (§4).

### Die zweite Leckage: AGENTS.md

Weniger offensichtlich, dafür verwirrender. Dieses Projekt ist ein System,
das opencode-Agents startet — und es *wird selbst* von einem opencode-artigen
Agent gebaut. Beide Seiten lesen `AGENTS.md`.

Liegt ein Test-Bau im Repo und startet der Daemon dort `opencode serve` mit
CWD im Repo, dann liest **der Archivar-Hase die Entwickler-`AGENTS.md`** —
inklusive Sätzen wie "nutze `bd` für Issue-Tracking". Der Hase fängt dann an,
Beads-Issues zu filen, während er eigentlich ein PDF einsortieren soll.

Zwei Gegenmaßnahmen, beide umsetzen:

- Der Test-Bau liegt **außerhalb des Repos** (z.B. `~/hasenbau-test/`),
  nicht unter `testdata/`.
- Der Daemon setzt das CWD des Servers **immer innerhalb des Baus**, niemals
  im Projekt-Root. Das ist eine harte Invariante, kein Default.

---

## 4. Verzeichnis-Layout

Der Bau ist selbst-enthaltend. Alles, was das System ausmacht, liegt darin.

```
bau/
├── hasenbau.yaml            # Bau-Config: log_level, baumeister
├── .opencode-home/          # XDG_CONFIG_HOME des Servers
│   └── opencode/            #   opencode erwartet dieses Unterverzeichnis
│       ├── opencode.json    #   minimal, explizit, plugin: []
│       ├── agents/          #   GENERIERT pro Auftrag×Hase — nie von Hand pflegen
│       │   └── pdf-einlagern__archivar.md
│       └── skills/
├── hasen/                   # Hasen-Templates (Quelle der Wahrheit, Nutzer pflegt hier)
│   ├── archivar.md
│   └── baumeister.md
├── auftraege/               # Auftrags-Definitionen (Markdown + YAML-Frontmatter)
│   └── pdf-einlagern.md
├── gaenge/                  # deterministische Skripte
│   ├── pdf_to_md.py
│   └── entwurf/             #   was der Baumeister schreibt — nie aktiv (§8)
├── raeume/                  # der eigentliche Materialfluss
│   ├── laderampe/
│   │   ├── sources/         #   Drop-Zone
│   │   └── work/            #   Scratch pro Lauf
│   ├── lager/               #   Ergebnisse
│   ├── archiv/              #   verarbeitetes Rohmaterial
│   └── quarantaene/         #   was schiefging
└── state/
    └── hasenbau.db          # SQLite
```

Räume sind nicht hartkodiert. Der Auftrag benennt sie; der Daemon legt
fehlende an. `laderampe/`, `lager/` usw. sind Konvention, kein Vertrag.

`hasenbau.yaml` ist bewusst dünn: `log_level` (noch ohne Konsumenten —
der Daemon loggt levelfrei) und `baumeister`, der Auftrag, den
`hasenbau baumeister` startet. Unbekannte Schlüssel sind ein Fehler; ein
verschriebener darf nicht still wirkungslos bleiben.

Der Baumeister zeigt eine Dehnung des Raum-Begriffs: sein `out`-Raum ist
`gaenge/entwurf/`, also **Code als Material**. Das ist gewollt — nur aus
den Räumen entsteht sein Schreibrecht (§6), und so steht es an derselben
Stelle wie jede andere Rechtevergabe. Der Preis ist begrifflich, und das
Review-Gate ist genau deshalb da: `gaenge/` ist versioniert, jeder
Entwurf steht im `git diff` des Baus, und aktiviert wird er nie
automatisch (§8, §10).

Der Bau ist außerdem ein **Git-Repo mit mindestens einem Commit** — nicht
zur Versionierung, sondern weil opencode daran Projekt-Identität und
`worktree` festmacht; ohne Git greifen die Raum-Permissions der Hasen
nicht (§11.5). `hasenbau init` legt das an. Versioniert werden nur
Definitionen: `raeume/`, `state/` und die generierten Agenten stehen in
der `.gitignore`.

Räume dürfen ihrerseits **eigene Git-Repos** sein (Änderungen
nachvollziehen, In-place-Transformationen) — fürs Bau-Repo unsichtbar
und für die Permissions egal, solange Sessions am Server-CWD (= Bau-Root)
hängen. Der Runner darf Sessions deshalb nie mit einem Verzeichnis
innerhalb eines Raums anlegen, sonst verschöbe sich der Worktree-Anker
auf das Raum-Repo. **Entschieden (2026-07-14, Hasenbau-q4y):** Sessions
ankern immer am Bau-Root; das Auftragsformat kennt kein `cwd:` — der
Parser lehnt es mit klarer Meldung ab, kein stilles No-op.

---

## 5. Datenmodell

SQLite, WAL-Modus. Bewusst klein — sechs Tabellen, nicht dreißig.
(DuckDB wäre hier falsch: OLAP-Engine für einen OLTP-Workload aus vielen
kleinen Writes und Punkt-Reads. Falls später Analysen über viele Läufe
gebraucht werden, kann DuckDB direkt auf diese SQLite-Datei querien.)

```sql
CREATE TABLE laeufe (
  id            INTEGER PRIMARY KEY,
  auftrag       TEXT NOT NULL,
  trigger       TEXT NOT NULL,     -- 'cron' | 'watch' | 'manuell'
  ausloeser     TEXT,              -- z.B. der Pfad der Datei, die es auslöste
  gestartet     TIMESTAMP NOT NULL,
  beendet       TIMESTAMP,
  status        TEXT NOT NULL,     -- 'laeuft' | 'ok' | 'fehler' | 'abgebrochen'
  session_id    TEXT,              -- opencode-Session, für Nachforschung
  summary       TEXT,              -- eine Zeile: was ist passiert
  output_pfad   TEXT,
  fehler        TEXT,
  tokens_in     INTEGER,
  tokens_out    INTEGER,
  kosten_cent   INTEGER,
  pid           INTEGER,           -- Wirt: der Prozess, der den Lauf hält
  pid_gestartet TIMESTAMP          -- dessen Startzeit (gegen PID-Recycling)
);
CREATE INDEX idx_laeufe_auftrag ON laeufe(auftrag, gestartet DESC);

CREATE TABLE auftrag_state (
  auftrag       TEXT PRIMARY KEY,
  letzter_lauf  TIMESTAMP,
  letzter_ok    TIMESTAMP,
  fehler_serie  INTEGER NOT NULL DEFAULT 0
);

-- Idempotenz-Backstop. Der eigentliche Mechanismus ist der Move nach
-- archiv/ — das hier fängt nur den Fall ab, dass der Move fehlschlug.
CREATE TABLE gesehen (
  auftrag       TEXT NOT NULL,
  quelle_hash   TEXT NOT NULL,     -- sha256 des Trigger-Inputs
  gesehen_am    TIMESTAMP NOT NULL,
  PRIMARY KEY (auftrag, quelle_hash)
);
```

`summary` ist der Kern der Kontext-Schicht: Der nächste Lauf desselben
Auftrags bekommt die letzten N Summaries in den Prompt. Der Hase schreibt
sie am Ende selbst über den Rückkanal (§8, Phase 2); ruft er das Tool
nicht auf, extrahiert der Runner sie als Fallback aus der letzten
Assistant-Message. Die Ein-Zeilen-Invariante hält der Store, damit sie
für beide Wege gilt.

`pid`/`pid_gestartet` tragen den **Wirt** eines Laufs: den Prozess, der
ihn hält. Stirbt der hart (SIGKILL), bliebe seine Zeile sonst für immer
auf `status='laeuft'` — `hasenbau status` zählt dann falsch, und der
Rückkanal findet keinen eindeutigen aktiven Lauf mehr (§11.7). „Beim
Start alles auf `laeuft` als abgebrochen markieren" wäre zu grob: ein
paralleles `hasenbau lauf` ist ausdrücklich erlaubt (eigener Server,
geteilte DB im WAL-Modus) und würde mit abgeräumt. Also entscheidet der
Wirt — die PID allein reicht nicht, sie wird recycelt, erst mit der
Startzeit ist eine Prozess-Inkarnation identifiziert (`/proc/<pid>/stat`
Feld 22 plus `btime`; ein Zombie zählt als tot). Jeder Prozess, der
selbst Läufe anlegt (`daemon`, `lauf`), räumt beim Start die Zeilen ab,
deren Wirt nachweislich nicht mehr lebt: Status `abgebrochen`, Grund in
`fehler`, `beendet` = Zeitpunkt des Aufräumens (der Todeszeitpunkt ist
nicht bekannt). Im Zweifel wird nichts abgeräumt — eine verwaiste Zeile
ist ärgerlich, ein abgeräumter lebender Lauf ist Datenverlust. Auf
Plattformen ohne `/proc` gibt es kein Kriterium; dort bleibt es beim
Zweifel. Unabhängig davon zählen tote Wirte für den Rückkanal nicht als
aktiver Lauf, auch bevor jemand aufgeräumt hat (Hasenbau-c6i).

Dazu kommt eine vierte Tabelle für den Rückkanal — die Summary ist eine
pro Lauf und steht deshalb in `laeufe`, Notizen sind beliebig viele:

```sql
CREATE TABLE notizen (
  id            INTEGER PRIMARY KEY,
  lauf          INTEGER NOT NULL REFERENCES laeufe(id),
  geschrieben   TIMESTAMP NOT NULL,
  text          TEXT NOT NULL
);
CREATE INDEX idx_notizen_lauf ON notizen(lauf, id);
```

Gelesen werden sie von `hasenbau dig`: was der Hase selbst für
erwähnenswert hielt, steht dort über dem Trace.

Dazu der Verlauf selbst — eine Zeile pro Lauf, geschrieben beim
Lauf-Ende:

```sql
CREATE TABLE trace (
  lauf          INTEGER PRIMARY KEY REFERENCES laeufe(id),
  session_id    TEXT NOT NULL,
  json          TEXT NOT NULL,     -- opencode.Trace, Ausgaben gekappt
  geschrieben   TIMESTAMP NOT NULL
);
```

Der Runner holt `Session.Messages` am Lauf-Ende ohnehin für Summary,
Tokens und Kosten — der Trace fällt dabei ab und kostet keinen zweiten
Aufruf. `json` ist für den Store opak; das Format gehört
`internal/opencode`.

Der Grund ist der Baumeister (§8): der zieht seinen Trace in einem
**Gang**, und der müsste sonst `hasenbau dig` rufen, das einen
zweiten opencode-Server startet — der bei einem Gang-Timeout verwaist
zurückbliebe, weil der Kill nur die Prozessgruppe der Gang-Shell trifft
(§2: „hängt nie als offener Endpoint herum"). Mit der Zeile hier
braucht `dig` gar keinen Server. Fehlt sie (Altläufe), holt `dig`
den Trace wie bisher und trägt sie nach; `-live` erzwingt den
Server-Weg mit ungekürzten Ausgaben. Gekappt wird, was die Verdichtung
nicht braucht: Tool-Ausgaben und Fehlertexte bei 8 KiB, mit Hinweis —
Werkzeug, Argumente und Status bleiben vollständig.

**Derselbe Verlauf noch einmal, normalisiert** — der Trace ist das
Protokoll zum *Lesen*, diese Tabelle dasselbe zum *Rechnen*
(Hasenbau-4cx.1):

```sql
CREATE TABLE tool_calls (
  lauf          INTEGER NOT NULL REFERENCES laeufe(id),
  nr            INTEGER NOT NULL,  -- Position in Ausführungsreihenfolge, ab 1
  tool          TEXT NOT NULL,
  args_json     TEXT NOT NULL,     -- vollständige Argumente, wie aufgerufen
  status        TEXT NOT NULL,     -- completed | error | …
  error         TEXT,              -- Begründung bei status='error'
  duration_ms   INTEGER,
  PRIMARY KEY (lauf, nr)
);
-- dazu in laeufe: tool_signature TEXT  -- 'read>write>hasenbau_summary'
```

Die Redundanz ist der Zweck. Ohne sie wäre jede Frage nach „welche
Position variiert über N Läufe" ein JSON-Scan, und genau diese Frage
ist die deterministische Antwort auf die harte Stelle aus §8: **was
über die Läufe variiert, ist der Parameter; was konstant bleibt, ist
die Konstante.** Aus einem einzelnen Trace ist das prinzipiell nicht
entscheidbar — aus zwanzig schon, und ohne Modell.

Die Signatur in `laeufe` ist ihrerseits redundant zu den Zeilen und
macht den Vergleich zweier Läufe zu einem String-Vergleich statt einem
Join. Fehlversuche stehen mit drin: sie gehören zur Wahrheit über den
Lauf, und wer sie beim Vergleichen nicht will, filtert die Zeilen —
aus einer Signatur, aus der sie schon herausgerechnet sind, bekommt
sie niemand zurück. Leer heißt „hat nichts angefasst", `NULL` heißt
„nie ausgewertet"; das ist nicht dasselbe. Läufe mit Trace, aber ohne
Zeilen zieht der Hasenbau beim Start nach — ohne Marker, die Auswahl
ist die Bedingung und damit idempotent.

---

## 6. Auftrags-Format

Markdown mit YAML-Frontmatter. Bewusst dasselbe Format wie opencode-Agents,
damit es nur eine Formatkategorie im Projekt gibt.

```yaml
---
trigger:
  watch: raeume/laderampe/sources/*.pdf
  debounce: 5s
  # alternativ:  cron: "0 7 * * *"
  # alternativ:  manual: true      # läuft nur auf Zuruf

gaenge:                            # deterministisch, läuft VOR dem Hasen
  - name: pdf-zu-markdown
    run: gaenge/pdf_to_md.py "$INPUT" --out "$WORK/extrakt.md"
    timeout: 120s

hase: archivar                     # → Template hasen/archivar.md
# hase_timeout: 60m                # Zeitlimit des LLM-Schritts; Vorgabe 30m
monitored: true                    # Befunde routinemäßig melden (§8)

raeume:
  input: raeume/laderampe/sources/
  work:  raeume/laderampe/work/
  out:   raeume/lager/
  done:  raeume/archiv/
  quarantine: raeume/quarantaene/ # Gang-Fehler ⇒ Input landet hier (§7)

context:                           # Push: was kommt in den Prompt
  - file: $WORK/extrakt.md
  - last_summaries: 3

after:
  - move: $INPUT -> raeume/archiv/
---

Der extrahierte Text liegt in `$WORK/extrakt.md`.
Fasse ihn zusammen, vergib Tags, und lege ihn strukturiert in `lager/` ab.
Dateiname: `YYYY-MM-DD-<slug>.md`
```

Der Hase sieht das PDF nie — er kriegt Markdown. Das ist der Punkt.

**`hase_timeout:` begrenzt den LLM-Schritt dieses Auftrags** (Vorgabe:
30 Minuten). Nicht zu verwechseln mit dem `timeout:` eines Gangs, das
einen einzelnen Vorverarbeitungs-Schritt begrenzt. Ein Wert pro Auftrag,
weil ein einziger nicht beides sein kann: derselbe Baumeister auf
demselben Trace war einmal nach 12 Minuten fertig und lief einmal nach
30 in die Vorgabe (Hasenbau-uh0) — dazwischen lag eine Denkpause von
17 Minuten ohne einen einzigen Tool-Call. Für `pdf-einlagern` wären
dieselben 30 Minuten dagegen absurd großzügig. `hase_timeout: 0` ist
ein Ladefehler und kein „unbegrenzt": ein Lauf darf lange dauern, nie
für immer.

**`monitored:` steuert die Meldung, nicht die Erfassung.** Aufgezeichnet
wird bei jedem Auftrag alles, und `hasenbau findings <auftrag>` rechnet
über jeden — auch über einen, der das Feld nicht setzt. Wer es setzt,
sagt nur: *diesen* Auftrag will ich ungefragt beurteilt sehen. Seine
Befunde stehen dann in `hasenbau status`, und ein cron-Auftrag kann ihn
regelmäßig durch `hasenbau findings` schicken. Die Trennung ist
Absicht — ein Flag, das mitschneidet, ist eines, bei dem man später
merkt, dass man es hätte setzen sollen; eines, das nur meldet, kann man
jederzeit nachziehen und bekommt die Historie mitgeliefert.

Der Schlüssel ist englisch wie alle Formatschlüssel außer den sieben
Begriffen aus §1 — in der Prosa und in der Ausgabe heißt die Sache
weiter „überwacht".

### Hasen-Templates und generierte Agenten

Die Dateien in `hasen/` sind Templates, keine opencode-Agents. Der Daemon
generiert daraus pro **Auftrag × Hase** einen Agenten unter
`.opencode-home/opencode/agents/<auftrag>__<hase>.md` (z. B.
`pdf-einlagern__archivar.md`); der Runner promptet mit diesem Namen.
Warum generieren statt direkt benutzen:

1. **Permissions pro Auftrag statt pro Agent.** Ein statischer Agent, den
   zwei Aufträge mit verschiedenen Räumen nutzen, hätte in beiden Läufen
   die Vereinigung seiner Rechte. Generiert bekommt jeder Lauf exakt die
   Räume seines Auftrags.
2. **Portabilität.** Die Hasen-Definition gehört Hasenbau, nicht opencode.
   Perspektivisch sind andere Coding-Agents als Backend denkbar; das
   Template-Format bleibt stabil, nur der Generator wechselt.
3. **Injektionspunkt.** Die Generierung ist die Stelle, an der das
   Framework später allen Hasen Prompts oder Tools mitgeben kann (z. B.
   Telemetrie an den Supervisor, Phase ≥ 2). Capability offenhalten,
   jetzt nicht ausgestalten.

Generierungsregel (deterministisch):

1. Basis aus dem Auftrag: `edit: {"*": deny}`, dann `allow` für dessen
   Schreib-Räume (`work`, `out` — nicht `done`, das `nachher: move`
   erledigt der Runner); `bash`, `webfetch`, `websearch`,
   `external_directory`: `deny`.
2. Template-Frontmatter wird durchgereicht (`description`, `model`,
   `temperature`); ein `permission`-Block im Template darf **nur `deny`**
   enthalten und wird *hinter* die Basis gehängt — wegen
   Last-Match-Semantik (§11.5) kann das Template Rechte nur weiter
   einschränken, nie aufweiten. `allow`/`ask` im Template ⇒ Ladefehler.
3. Generiert wird beim Laden der Definitionen, nicht pro Lauf. Danach
   `POST /instance/dispose` (§11.6) — vom Runner gegen aktive Läufe
   serialisiert, denn dispose cancelt laufende Sessions.

### Wissen für einen einzelnen Hasen

`instructions` in der opencode.json wäre der naheliegende Weg,
Hintergrundwissen mitzugeben — aber es geht nicht: **verifiziert am
Schema (opencode.ai/config.json, 2026-08-10)** ist `instructions`
Workspace-weit und gilt für *jeden* Agenten, und ein einzelner Agent hat
kein eigenes `instructions`-Feld (zulässig sind `model`, `variant`,
`temperature`, `top_p`, `prompt`, `disable`, `description`, `mode`,
`hidden`, `options`, `color`, `steps`, `permission`). Der einzige
agentenspezifische Weg ist `prompt` — und den generiert der Hasenbau
ohnehin selbst. Zwei Template-Felder nutzen das:

- **`knows_hasenbau: true`** bindet einen Text ein, der **im Binary**
  liegt: Begriffe, Ablauf eines Laufs, wie ein Trace zu lesen ist,
  Permissions und Grenzen. Nicht im Bau, und das ist der Punkt — eine
  kopierte Datei driftet, sobald der Hasenbau sich ändert, und dann
  erzählt der Hase von einem System, das es so nicht mehr gibt. Wissen
  über das Werkzeug gehört ins Werkzeug (entschieden 2026-08-10).
- **`wissen: [pfade]`** bindet eigene Bau-relative Dateien ein (Globs
  erlaubt). Mehrere Hasen dürfen dieselbe teilen, ohne sie zu
  duplizieren.

Beides wird beim Laden gelesen und beim Generieren mit einer Überschrift
eingebettet, die die Herkunft nennt — sonst ist im Trace nicht zu
erkennen, woher eine Anweisung kam. Reihenfolge im Agenten: erst die
Rolle aus dem Template, dann das Nachschlagewerk, dann der Rückkanal.
Wer die Hausordnung wirklich *jedem* Hasen mitgeben will, hat weiterhin
`instructions` — mit der Konsequenz, dass es dann auch für jeden gilt.

### Variablen im Auftrag

| Variable | Bedeutung |
|---|---|
| `$BAU` | Root des Baus |
| `$INPUT` | Der Auslöser: bei `watch` die auslösende Datei, bei `manuell` das übergebene Argument |
| `$WORK` | Scratch-Verzeichnis dieses Laufs, wird pro Lauf angelegt |
| `$RAUM_<name>` | Pfad des unter `raeume:` benannten Raums |
| `$HASENBAU` | Pfad des laufenden Binaries — für Gänge, die den Hasenbau selbst rufen |

Genau eine Trigger-Art pro Auftrag. `manuell` gibt es, weil nicht jeder
Auftrag auf Material oder die Uhr wartet: der Baumeister wird auf einen
*Lauf* angesetzt (§8). Scheduler und Watcher wählen positiv aus und
ignorieren ihn von selbst.

**`$INPUT` ist nur bei `watch` ein Pfad.** Bei `manuell` ist es das
Argument von der Kommandozeile — freier Text. Deshalb lehnt der Parser
`$INPUT` in `kontext: - datei:` und in `nachher:` für manuell-Aufträge
ab, und die Quarantäne (§7) greift nur bei `watch`: sonst würde ein
Gang-Fehler eine gleichnamige Datei im Bau wegtragen, die mit dem Lauf
nichts zu tun hat. Zu unterscheiden ist das von `laeufe.trigger` (§5) —
ein watch-Auftrag, den `hasenbau lauf` startet, wird dort als `manuell`
verbucht, bleibt aber ein watch-Auftrag.

**Die Variablen werden vor `sh -c` textuell ersetzt — und genau deshalb
ist `$INPUT` das einzige geprüfte Feld.** Textuell zu ersetzen ist
Absicht: nur so ist `$HOME` in einer Gang-Zeile ein harter Fehler statt
einer stillen Expansion. Der Preis ist, dass der Wert unquotiert im
Kommando landet. Für `$BAU`, `$WORK` und `$RAUM_<rolle>` ist das
harmlos — sie stammen aus Dateien, die ein Mensch geschrieben hat, und
wer einen Auftrag schreibt, darf darin ohnehin alles. `$INPUT` ist das
Einzige, was von außen kommt: bei `watch` ist es ein Dateiname aus der
Drop-Zone. Eine Datei namens `x";rm -rf ~;"y.pdf` wäre sonst ein
Kommando. Deshalb lehnt `lauf.Neue` Inputs mit `"`, `'`, `` ` ``, `$`,
`\` und Steuerzeichen ab, bevor irgendein Gang startet
(Hasenbau-bnh). Leerzeichen, Klammern, Umlaute und `&` bleiben erlaubt
— die stehen in echten Dateinamen und sind in `"$INPUT"` harmlos.

### Ablauf eines Laufs

1. Trigger feuert → Lock auf den Auftrag (kein Overlap, ein Auftrag läuft nie doppelt)
2. `$WORK` anlegen
3. Gänge der Reihe nach ausführen. Exit≠0 → Abbruch, Input nach `quarantaene/`, Lauf als `fehler`
4. Prompt bauen: Auftrags-Body + Kontext-Quellen + letzte N Summaries
5. opencode-Session anlegen, Hase als Agent, Prompt schicken, Event-Stream mitlesen
6. `nachher:`-Schritte ausführen
7. Lauf in DB schreiben, `$WORK` aufräumen (oder bei Fehler behalten)

**Schritt 5 im Detail (entschieden 2026-07-14, Hasenbau-q4y):** Der
Prompt läuft asynchron; die Wahrheitsquelle für das Lauf-Ende ist der
Event-Stream — der Daemon hält *ein* SSE-Abo (`GET /event`, Funnel)
und verteilt die Ereignisse pro Session an die Läufe. Fertig ist der
Lauf bei `session.idle` oder wenn der synchrone Call sauber zurückkommt
(was zuerst eintritt); der synchrone Call allein ist kein Endekriterium,
er riss im Spike bei langen Läufen ab.

**Ein dritter Zeuge, weil Ereignisse verloren gehen (Hasenbau-0f4):**
Beide bisherigen Kriterien sind flüchtig. Reißt der Stream im falschen
Moment ab — der Funnel verbindet dann mit Backoff neu —, ist das eine
`session.idle` für immer weg; ist zusätzlich der Prompt-Call
abgerissen, wartet der Lauf bis zum `HaseTimeout` von 30 Minuten auf
etwas, das längst passiert ist. Von außen sieht das aus wie ein
Befehl, der sich nicht beenden will. Deshalb fragt der Runner
zusätzlich `GET /session/status` im 15-Sekunden-Takt: ein *Zustand*
lässt sich nachfragen, ein Ereignis nicht. Akzeptiert wird das Ende
erst nach einem beobachteten Übergang `busy` → nicht mehr `busy` —
sonst gälte eine Session, die noch gar nicht angelaufen ist, sofort
als fertig. Welcher der drei Zeugen den Lauf beendet hat, steht im
Log; ohne diese Zeile ist ein hängender Lauf nicht diagnostizierbar.

Danach liefert `Session.Messages`
Summary (letzte Assistant-Message, bis der MCP-Rückkanal sie ersetzt),
Tokens und Kosten. Zwei Invarianten dazu: der Daemon weiß jederzeit,
wie viele Läufe aktiv sind (Registry im Runner), und `instance/dispose`
(Agent-Reload, §11.6) läuft nie parallel zu einem Lauf — neue Läufe
warten, solange dispose läuft, und umgekehrt. So entstehen keine
dangling Sessions durch weggeworfene Instanz-Caches.

---

## 7. Die drei Fallen bei Watch-Triggern

Diese sind nicht optional, sie erwischen einen sonst am zweiten Tag.

**Partielle Writes.** Ein 40-MB-PDF, das noch kopiert wird, feuert das
fsnotify-Event bereits. Der Watcher muss auf Größenstabilität warten
(zwei Ticks gleiche Größe, dann verarbeiten) — das ist der Zweck von
`debounce`.

**Idempotenz.** Bleibt das PDF nach dem Lauf in `sources/`, wird es beim
nächsten Daemon-Start erneut verarbeitet. Der `move → archiv/` ist der
Idempotenz-Mechanismus, nicht ein DB-Flag. Die `gesehen`-Tabelle ist nur
Backstop für den Fall, dass der Move fehlschlug.

**Fehlerfall.** Crasht ein Gang, muss der Input in `sources/` bleiben
oder nach `quarantaene/` — niemals nach `archiv/`. Sonst verschwindet
Material lautlos.

### Die vierte Falle: der volle Eingang

Wer 200 PDFs auf einmal ablegt, findet die drei oberen alle erfüllt und
trotzdem ein kaputtes System. Ursprünglich bekam jeder Glob-Treffer
seine eigene Goroutine, und alle drehten in der Overlap-Sperre: 200
Goroutinen, von denen 199 im Sekundentakt meldeten, dass sie warten. Sie
taten dasselbe wie einer — nur lauter, und in zufälliger Reihenfolge,
denn Go-Mutexe sind nicht FIFO.

**Ein Arbeiter je Auftrag, ältester Input zuerst** ✅ *(2026-08-11,
Hasenbau-do0.1)*. Die gemeldeten Inputs stehen in einer Menge, der
Arbeiter nimmt sich den mit der ältesten `mtime` — die Reihenfolge, die
ein Mensch erwartet, wenn er einen Stapel abarbeiten lässt. Bei
gleicher `mtime` entscheidet der Pfad, sonst käme derselbe Stapel bei
zwei Läufen verschieden heraus.

Eine Warteschlange braucht es dafür nicht: **das Dateisystem ist die
Warteschlange.** Ein Input bleibt in `sources/`, bis ein geglückter Lauf
ihn per `after: move` wegräumt, und beim Start liest der Glob alles
wieder ein — derselbe Mechanismus, der oben schon die Idempotenz trägt.
Ein Rückstau übersteht damit jeden Neustart, ohne dass irgendwo Zustand
mitgeschrieben würde.

Der Arbeiter ist außerdem die Stelle, an der eine Drossel ansetzen kann:
„jetzt nicht" sagt man einem, nicht zweihundert (Epic Hasenbau-do0).

---

## 8. Phasen

### Phase 0 — Fundament

Ziel: Der Daemon startet, hält einen opencode-Server am Leben, und kann
einen Prompt schicken.

- [x] **XDG-Isolation verifizieren** (siehe §3). Erst danach weiterbauen —
      wenn das nicht geht, ändert sich das Design.
- [x] Supervisor: `opencode serve` als Child spawnen, auf `127.0.0.1`,
      freier Port. Health-Check per Polling auf `/app` oder `/config`.
      Restart bei Crash, Backoff. Sauberer Shutdown (SIGTERM ans Kind,
      keine Zombies).
- [x] SDK-Client gegen den Server verbinden. Session anlegen, Prompt
      schicken, Antwort holen. Ein Smoke-Test, mehr nicht.
- [x] SQLite anlegen, Migrationen.
- [x] CLI-Grundgerüst: `hasenbau daemon`, `hasenbau lauf <auftrag>` (manuell
      triggern), `hasenbau laeufe` (Historie), `hasenbau status`.
- [x] `hasenbau init <pfad>` — legt einen leeren Bau an. Test-Bau damit
      **außerhalb des Repos** erzeugen (siehe §3, AGENTS.md-Leckage).
- [x] Invariante durchsetzen und testen: CWD des Servers liegt immer im Bau.

> ⚠️ **Zu verifizieren:** Die exakten Struct- und Feldnamen des Go-SDK
> (`opencode.SessionNewParams`, `SessionPromptParams`, Part-Unions) gegen
> `pkg.go.dev/github.com/sst/opencode-sdk-go` prüfen. Das ist generierter
> Code — nicht aus dem Gedächtnis raten.
>
> ⚠️ **Zu verifizieren:** Wie ein Agent (aus `agents/*.md`) beim Prompt
> ausgewählt wird — Parameter am Session- oder am Prompt-Call? Siehe
> OpenAPI-Spec unter `<server>/doc`.

### Phase 1 — Aufträge

Ziel: Der PDF-Auftrag läuft Ende-zu-Ende.

- [x] Auftrags-Parser (Frontmatter + Body), Validierung mit klaren Fehlern
- [x] Räume anlegen/auflösen, Variablen-Substitution
- [x] Scheduler (cron) mit Lock pro Auftrag
- [x] Watcher (fsnotify) mit Debounce und Größenstabilität
- [x] Gang-Runner: Subprocess, Timeout, Exit-Code, stdout/stderr in den Lauf
- [x] Prompt-Bau: Body + Kontext-Dateien + letzte N Summaries
- [x] Hase ausführen, Event-Stream mitlesen (Tool-Calls loggen), Ergebnis persistieren
- [x] `nachher:`-Schritte (move, copy, delete)
- [x] Referenz-Auftrag `pdf-einlagern` + Hase `archivar` + Gang `pdf_to_md.py`

Output-Sink in dieser Phase: **Dateien in `lager/`**. Kein Telegram, kein
Matrix, keine Desktop-Notifications. Erst herausfinden, ob die Aufträge
überhaupt Nützliches produzieren; Sinks kommen, wenn die Antwort ja ist.

### Phase 2 — Verdichtung und Rückkanal

Der eigentliche Grund für das Projekt.

**Das Problem:** Ein Hase, der bei jedem Lauf dieselben drei Tool-Calls in
derselben Reihenfolge macht, ist ein Interpreter, der jedes Mal neu
kompiliert. Bezahlt in Tokens, Latenz und Nichtdeterminismus.

**Der Mechanismus:**

1. Tool-Calls werden ohnehin schon aus dem Event-Stream mitgeloggt
   ✅ *(Phase 1, Hasenbau-q4y)*
2. `hasenbau dig <lauf-id>` zieht den Trace der Session
   (`session.messages()` → Tool-Call-Parts mit Namen und Argumenten;
   strukturiert, kein Log-Parsing) ✅ *(2026-07-14, Hasenbau-2qy:
   `dig [-json]`, Zugriffsweg §11.3 — der Trace enthält auch die
   reasoning-Parts, also die Absicht des Hasen)*
3. Der **Baumeister** (ein Hase mit Schreibrecht auf `gaenge/entwurf/`)
   bekommt den Trace und schreibt daraus ein Skript ✅ *(2026-08-10,
   Hasenbau-xfm, siehe unten)*
4. Der Nutzer liest das Skript und trägt es selbst in den Auftrag ein

**Die harte Stelle:** Ein Trace ist konkret, ein Gang muss generisch sein.
Der Trace sagt `read("sources/rechnung-2026-03.pdf")`, der Gang muss
`read("$INPUT")` sagen. Diese Generalisierung — was ist Parameter, was
Konstante, was war ein Fehlversuch, den der Hase danach korrigiert hat —
ist selbst eine Modell-Aufgabe und geht regelmäßig daneben.

**Deshalb, nicht verhandelbar:**

> Ein gegrabener Gang wird **nie automatisch scharf geschaltet.** Der
> Baumeister schreibt das Skript nach `gaenge/entwurf/`, der Nutzer liest
> es, der Nutzer trägt es ein. Sonst entsteht ein System, das sich selbst
> umschreibt und dessen Fehlverhalten drei Läufe später in einem
> generierten Skript steckt, das nie jemand gelesen hat.

**Der Baumeister ist kein Sonderpfad im Code** (entschieden 2026-08-10,
Hasenbau-xfm). Er ist ein ganz normaler Auftrag mit einem ganz normalen
Hasen — sein Material sind nur keine PDFs, sondern Läufe, und seine
deterministische Vorverarbeitung ist ein Gang wie jeder andere:

```yaml
trigger:  {manuell: true}
gaenge:   [{name: trace-ziehen, run: '"$HASENBAU" dig "$INPUT" > "$WORK/trace.md"'}]
hase:     baumeister
raeume:   {work: raeume/baumeister/work/, out: gaenge/entwurf/}
context:  [{datei: $WORK/trace.md}]
```

Damit frisst der Hasenbau sein eigenes Hundefutter: die Verdichtung
läuft durch dieselbe Maschinerie wie alles andere, und die
Vorverarbeitung kann später selbst zu einem Gang verdichtet werden.
`hasenbau baumeister <lauf-id|auftrag>` ist nur ein dünner Befehl
darüber — er schlägt in `hasenbau.yaml` nach, welcher Auftrag der
Baumeister ist, löst das Ziel zu einer Lauf-ID auf (damit `$INPUT` eine
Zahl ist, kein Pfad und keine Shell-Syntax) und berichtet hinterher, was
im out-Raum neu ist.

Drei Dinge tragen dabei die Garantie aus §10, und zwar im Code statt im
Prompt: das Schreibrecht entsteht **ausschließlich** aus `raeume: out:`
(§6) — auf `auftraege/` hat der Baumeister keines und kann sich deshalb
nicht selbst scharf schalten; `gaenge/entwurf/` statt `gaenge/` heißt,
dass kein Lauf einen benutzten Gang überschreiben kann; und ein
Golden-Test in `internal/hase` hält den generierten Agenten der
ausgelieferten Beispiel-Dateien fest, damit eine Änderung an den Räumen
sofort auffällt.

**`hasenbau findings <auftrag>` rechnet, bevor jemand fragt** ✅
*(2026-08-10, Hasenbau-4cx.2)*. Deterministisch, ohne Server und ohne
Modell: das Anschauen kostet nichts, ist reproduzierbar, und jeder
Vorschlag nennt die Läufe, auf denen er beruht. Drei Arten — der
Gang-Kandidat (das häufigste zusammenhängende Werkzeug-Muster, dazu je
Position, ob die Argumente variieren), die Permission-Reibung
(gescheiterte Aufrufe nach Werkzeug, Grund und Zielpfad) und Laufzeit
samt Ausreißern. Ausgabe als nummerierte Markdown-Liste — die Nummer
ist der Griff, „arbeite 2 aus" der nächste Satz — oder `-json` für den
Einsatz in einem Gang.

Häufigkeit schlägt Länge: ein langes Muster in drei von zwanzig Läufen
trägt keine Generalisierung, ein kurzes in allen zwanzig schon. Unter
drei Läufen entsteht gar kein Gang-Kandidat; zwei sind kein Muster,
sondern ein Zufall zu zweit.

Der Name ist englisch wie alles außer den sieben Begriffen aus §1 —
in der Prosa heißt die Sache weiter Befund, so wie `dig` einen Trace
gräbt.

**`monitored: true` bringt die Befunde eines Auftrags von selbst in den
Blick** ✅ *(2026-08-11, Hasenbau-4cx.3)*. `hasenbau status` meldet für
jeden so markierten Auftrag, wie viele Befunde über wie viele Läufe
stehen, und nennt die ersten drei beim Titel; alles weitere holt
`hasenbau findings <auftrag>`. Auch „keine Befunde über 12 Läufe" wird
gemeldet — dass ein Auftrag rund läuft, ist eine Auskunft. Das Flag
entscheidet nur darüber; erfasst und analysierbar bleibt jeder Auftrag
(§6). Vor der Meldung zieht der Status die Aufrufe der Altläufe nach,
damit dort dieselben Zahlen stehen wie unter `findings` — ohne das
zählte ein Lauf mit Trace, aber ohne Aufrufzeilen nicht mit.

**Stufe 2: der Baumeister arbeitet einen Befund aus, keinen Trace** ✅
*(2026-08-10, Hasenbau-4cx.4)*. `hasenbau baumeister -finding <n>
<auftrag>` setzt ihn auf einen ausgewählten Befund an. Der Code ändert
sich dabei kaum — genau dafür ist der Baumeister in Stufe 1 ein ganz
normaler Auftrag geworden: sein Gang ruft weiter `hasenbau dig
"$INPUT"`, nur ist `$INPUT` jetzt entweder eine Lauf-ID oder ein
Befund-Selektor `<auftrag>#<n>`. Ein Gang bekommt genau eine Variable
mit (§6), also trägt der Selektor beides.

`dig` liefert für einen Selektor den gerechneten Befund und darunter
die Traces der Läufe, auf denen er beruht — höchstens die drei
jüngsten. Vier vollständige Traces sind über zweitausend Zeilen, und
der Befund darüber ginge in der Mitte verloren; der gerechnete Befund
*ist* die Verdichtung, die Traces sind Belege. Der Prompt sagt dem
Baumeister dazu den entscheidenden Satz: **glaub den Zahlen mehr als
deinem Eindruck aus den Traces** — was dort `VARIIERT` heißt, ist ein
Parameter, auch wenn es in einem einzelnen Trace konstant aussieht.

Stufe 1 bleibt daneben stehen. Solange ein Auftrag zu wenige
ausgewertete Läufe hat, gibt es keine Befunde — und ein Trace ist mehr
als nichts.

**Bekannte Grenze:** Aus *einem* Trace ist prinzipiell nicht
entscheidbar, was Parameter und was Konstante war. Das Modell antwortet
trotzdem plausibel und schreibt die Eigenheiten des ersten Materials als
Regel fest. Der `Annahmen:`-Block im Skriptkopf und die Anweisung „lieber
nichts schreiben" dämpfen das, heilen es nicht — Stufe 1 liefert
Gesprächsgrundlagen, keine einsatzfähigen Gänge. Die Antwort darauf ist
nicht ein besserer Prompt, sondern mehr Material: der Argument-Diff über
N Läufe sagt empirisch, welche Position variiert — genau das tut
`hasenbau findings` (Epic Hasenbau-4cx).
Zweitens kann der Baumeister sein Skript nicht ausführen — `bash: deny`
ist unbedingt, Templates dürfen Rechte nur verengen. Deshalb prüft
`hasenbau baumeister` neue Entwürfe nach dem Lauf auf Syntax
(`py_compile`, `sh -n`); das ist deterministisch und außerhalb der
Reichweite des Modells.

**Rückkanal:** Ein kleiner MCP-Server (in Go, `mark3labs/mcp-go`), der den
Hasen die Tools `notiz(text)` und `summary(text)` gibt und damit direkt in
die SQLite schreibt. Strukturierte Writes statt stdout-Parsing — sonst
entsteht in drei Wochen ein Regex-Friedhof. ✅ *(2026-08-05,
Hasenbau-ekm: `hasenbau mcp` über stdio, von opencode gestartet;
Eintrag `mcp.hasenbau` in der Bau-Config. Der generierte Agent bringt
den Absatz mit, der die Werkzeuge erklärt — ohne ihn ruft sie kein Hase.
Zur Lauf-Zuordnung §11.7)*

**Der Hinweis steht zweimal im generierten Agenten**, kurz vor dem
Template-Prompt und ausführlich dahinter. Grund: Ein Hase mit langem
Template, dem Hasenbau-Wissen und einem großen Kontext hat die
Anweisung sonst irgendwo in der Mitte stehen, und dort geht sie
verloren. Beobachtet an zwei Läufen desselben Auftrags mit demselben
Modell und nachweislich vorhandenen Werkzeugen: einer rief
`hasenbau_summary` auf, der andere schrieb die Meldung als Fließtext in
seine Antwort (Hasenbau-ifg). Beide Fassungen sagen deshalb denselben
Satz — der Aufruf ist die Abschlusshandlung, kein Text ersetzt ihn.

**Der Eintrag ist eine Selbstreferenz, kein Fremdprodukt.** `hasenbau
mcp` ist der Hasenbau selbst über stdio; `command:` benennt also nicht
„ein Werkzeug", sondern das Binary, das diesen Bau fährt — und das
wechselt bei jedem Rebuild. Ein einmal eingetragener Pfad veraltet
deshalb zwangsläufig: im Test-Bau zeigte er fünf Tage lang auf einen
Wegwerf-Build unter `/tmp` (Hasenbau-2nq). Der Hasenbau setzt darum bei
jedem Server-Start das *erste Element* von `command:` auf das laufende
Binary und sagt im Log, wenn er dabei etwas korrigiert hat. Zusatz-
Argumente, `env`, `type` und `enabled` bleiben stehen — Handarbeit am
Eintrag überlebt, nur das veraltete Binary nicht.

**Ein Rückkanal, der nicht hochkommt, hält den Lauf an.** Scheitert der
MCP-Client, sagt opencode nichts: der Server startet normal, loggt keine
Zeile, und der Hase bekommt die Werkzeuge einfach nicht. Sein Prompt
verspricht sie ihm trotzdem, also schreibt er die Meldung als Fließtext
in seine Antwort — die Summary kommt aus dem Fallback, Notizen entstehen
gar keine, und der Lauf steht als `ok` in der Datenbank. Genau so lief
Lauf 10 im Test-Bau (2026-08-10, Hasenbau-08u); der Hase hielt im
Reasoning selbst fest, dass ihm die `hasenbau_*`-Werkzeuge fehlen.
Deshalb fragt der Hasenbau nach dem Server-Start den Zustand über `/mcp`
ab und lässt weder Daemon noch Einzellauf los, solange der Eintrag nicht
`connected` meldet. Kein Widerspruch zu „der Rückkanal ist ein besserer
Weg, kein Zwang": dass ein Hase `summary()` nicht *ruft*, bleibt seine
Sache — dass die Werkzeuge gar nicht erst ankommen, ist ein Defekt des
Baus.

Die Summary aus dem Rückkanal gewinnt gegen den Fallback: `LaufBeende`
überschreibt eine schon gesetzte Summary nicht. Ruft ein Hase
`summary()` nie auf, bleibt es bei der letzten Assistant-Message — der
Rückkanal ist ein besserer Weg, kein Zwang.

### Phase 3 — Heartbeat

Erst wenn es mehr als eine Trigger-Quelle gibt.

**Der naive Heartbeat ist teuer und falsch:** alle 5 Minuten einen Agenten
aufwecken und fragen, ob was zu tun ist, sind ~290 LLM-Calls pro Tag, von
denen 285 mit "nö" antworten.

Stattdessen zweistufig:
1. **Tick** — deterministisch, Go-Code, kein Modell. Gibt es fällige
   Reminders, neue Dateien, offene Follow-ups aus letzten Läufen?
2. Nur wenn ja → Hase, mit genau dem Kontext, der zum Trigger gehört.

Der Heartbeat ist ein Trigger-Evaluator, kein Agent-Aufruf. Die Intelligenz
steckt im Trigger-Layer, nicht im Modell. `raeume/laderampe/sources/` ist
bereits so ein Trigger — Phase 1 ist der Anfang des Heartbeats.

---

## 9. Arbeitsweise am Projekt

### Beads als Arbeitsliste

Der Plan hat Phasen mit echten Abhängigkeiten — §11.1 blockiert das Design,
§11.3 blockiert Phase 2. Das ist ein DAG, keine Häkchenliste. Beads (`bd`)
bildet genau das ab: Blocker, Ready-Work-Detection, und Arbeit, die unterwegs
entdeckt wird, geht nicht verloren.

```bash
bd init                    # legt .beads/ an, schreibt AGENTS.md
```

Dann: `PLAN.md` in Epics (pro Phase) und Issues übersetzen, Abhängigkeiten
sorgfältig setzen, einmal durchgehen und polieren, **danach** anfangen zu bauen.

**Zwei Datenbanken, nicht verwechseln.** Beads trackt, wie der Hasenbau
*gebaut* wird. Die SQLite aus §5 trackt, was der Hasenbau *tut*. Verschiedene
Lebenszyklen, verschiedene Zielgruppen. Die Begriffsnähe ("Auftrag" hier,
"Issue" dort) lädt zum Zusammenwerfen ein — nicht tun.

### Instruktionsdateien

`AGENTS.md` ist die kanonische Datei (opencode liest sie, `bd init` schreibt
hinein). Claude Code liest `CLAUDE.md`, **nicht** `AGENTS.md` — es gibt keinen
Fallback. Also:

```markdown
<!-- CLAUDE.md -->
@AGENTS.md

## Claude Code
Plan-Mode für Änderungen an `internal/opencode/` — die SDK-Signaturen sind
generiert und werden gerne aus dem Gedächtnis geraten.
```

In `AGENTS.md` gehören: exakte Build-/Test-Befehle, das Vokabular aus §1
(damit niemand `Job` statt `Auftrag` schreibt), der Beads-Workflow, ein
Verweis auf `PLAN.md`, und die AGENTS.md-Leckage aus §3.

Nicht hinein gehört eine Kopie des Plans. Unter 200 Zeilen halten — die Datei
lädt bei jeder Session komplett in den Kontext, und längere Dateien werden
schlechter befolgt.

### Kein Spec-Kit

Bewusste Entscheidung. Spec-Kit ist Spec-as-Source und auf gut verstandene
Neu-Features optimiert; nachträgliches Ändern bestehender Specs ist der
bekannte Schwachpunkt. Dieses Projekt hat fünf offene Punkte, bei denen die
Antwort das Design ändert — die Phasen-Gates würden gegen genau diese
Exploration arbeiten. `PLAN.md` *ist* der Spec.

---

## 10. Nicht-Ziele

Explizit, damit sie nicht durch die Hintertür reinkriechen:

- **Kein Remote/Multi-Host.** Lokal, eine Maschine. Später vielleicht.
- **Keine Web-UI.** CLI und Dateien.
- **Kein eigenes Agent-Framework.** Personas, Tools, Permissions macht
  opencode. Der Hasenbau macht *wann*, *womit* und *wohin*.
- **Kein Langzeit-Gedächtnis / Vektorsuche** in Phase 1–2. Die letzten N
  Summaries reichen erstaunlich weit.
- **Keine automatische Aktivierung generierter Gänge.** Siehe Phase 2.

---

## 11. Offene Punkte

Alles hier ist ungeprüft. Nicht raten — nachschlagen oder ausprobieren.

1. ~~Folgt opencode strikt `XDG_CONFIG_HOME`?~~ **Ja, verifiziert
   2026-07-11 (opencode 1.15.13), siehe §3.** Config-Pfad ist
   `$XDG_CONFIG_HOME/opencode/opencode.json`.
2. ~~Exakte SDK-Signaturen?~~ **Verifiziert 2026-07-12** (SDK v0.19.2
   gegen opencode 1.15.13, Code im Modul-Cache + live `/doc`):
   - Client: `opencode.NewClient(option.WithBaseURL(supervisor.BaseURL()))`
   - Session: `client.Session.New(ctx, SessionNewParams{Title: F(…)})` → `*Session`
   - Prompt: `client.Session.Prompt(ctx, id, SessionPromptParams{Parts:
     F([]SessionPromptParamsPartUnion{TextPartInputParam{Type: F(TextPartInputTypeText),
     Text: F(…)}}), Agent: F("archivar"), Model: F(SessionPromptParamsModel{
     ProviderID, ModelID})})` → `SessionPromptResponse{Info AssistantMessage, Parts []Part}`
   - **Agent-Auswahl hängt am Prompt-Call** (`agent` im Body), nicht an
     der Session. `AssistantMessage` liefert `Cost` und `Tokens` direkt →
     füllt `laeufe.tokens_*`/`kosten_cent`.
   - Event-Stream: `client.Event.ListStreaming(ctx, EventListParams{})` →
     `ssestream.Stream[EventListResponse]` (SSE auf `GET /event`); Typen
     u.a. `message.part.updated`, `session.idle`, `session.error`.
3. ~~Wie kommt man aus einer Session an den vollständigen
   Tool-Call-Trace?~~ **Verifiziert 2026-07-14 (Hasenbau-c8c, gegen
   echte Sessions der z0u/d6p-Läufe):** `Session.Messages(ctx, id, …)`
   liefert den **kompletten Verlauf** — nicht nur Tool-Calls, sondern
   auch `text`-, `reasoning`- (!) und `patch`-Parts. Für den
   Baumeister heißt das: Absicht (reasoning) + Taten (tool) +
   Korrekturen kommen strukturiert aus einer Quelle.

   - **Tool-Parts:** `p.AsUnion().(sdk.ToolPart)` → `Tool`, `CallID`,
     `State.Status` (`completed`/`error`), `State.Input` (volle
     Argumente als JSON), `State.Output`, `State.Error`. Ein
     abgewehrter Schreibversuch (d6p-Session) kommt als
     `status=error` mit der Permission-Begründung in `State.Error` —
     Fehlversuche sind also trivial filterbar.
   - **Reihenfolge:** Parts liegen in Ausführungsreihenfolge im
     Array; `State.Time` trägt zusätzlich `start`/`end` (Unix-ms)
     für exakte Ordnung über Messages hinweg.
   - **Persistenz:** Der Server speichert alles in einer SQLite unter
     XDG_DATA_HOME (`opencode.db`, Tabellen `session`/`message`/
     `part`), getrennt nach `project_id` (Hash des Bau-Root-Commits,
     §11.5). Ein **frisch gestarteter** Server liefert die Sessions
     längst toter Server-Instanzen — `hasenbau dig` funktioniert
     also post-hoc, Tage später. Die DB ist mit dem Alltags-opencode
     geteilt (§3), die Sessions sind es wegen der project_id nicht.
   - **Verworfene Wege:** Kein Writeback-Tool (Selbstauskunft des
     Modells wäre redundant, teuer, lückenhaft — MCP bleibt für
     notiz()/summary(), Dinge, die nur das Modell weiß), kein Plugin
     nötig (Post-hoc-Lesen braucht keins), kein Direktzugriff auf
     `opencode.db` (internes, Drizzle-migriertes Schema — nur
     Notausgang, nicht drauf bauen).
4. ~~Verhält sich `opencode serve` mit `--port 0` sinnvoll?~~ **Verifiziert
   2026-07-11:** `--port 0` heißt „auto" — bevorzugt 4096 (Default), bei
   Belegung ein freier ephemerer Port. Der zugewiesene Port steht auf
   stdout (`opencode server listening on http://127.0.0.1:<port>`) und
   ist dort zu parsen; der Supervisor darf **nie** 4096 annehmen (das
   interaktive opencode des Nutzers kann dort sitzen). Ohne
   `OPENCODE_SERVER_PASSWORD` warnt der Server („unsecured") — für den
   Supervisor ein Passwort setzen oder die Warnung bewusst dokumentieren.
5. ~~Permissions: Wie verhindert man zuverlässig, dass ein Hase außerhalb
   seiner Räume schreibt? (Agent-Frontmatter? `permission`-Config?
   Oder braucht es einen Sandbox-Layer?)~~ **Verifiziert 2026-07-13
   (opencode 1.15.13, Test-Bau, echter Lauf: 1 legaler + 4
   Flucht-Schreibversuche): Agent-Frontmatter-`permission` reicht,
   kein Sandbox-Layer nötig.** Rezept für einen Raum-beschränkten Hasen:

   ```yaml
   permission:
     edit: { "*": deny, "raeume/archiv/**": allow }
     bash: deny
     webfetch: deny
     websearch: deny
     external_directory: deny
   ```

   Mechanik (nachgelesen in `packages/core/src/permission.ts` u. a.,
   Tag v1.15.13):

   - `write`/`edit`/`apply_patch` laufen alle unter der `edit`-Permission.
     Die **letzte** matchende Regel gewinnt (`findLast`). `*` matcht auch
     über `/`-Grenzen hinweg (`**` ist äquivalent zu `*`), Patterns sind
     mit `^…$` verankert.
   - Edit-Patterns matchen gegen `path.relative(worktree, datei)`.
     **Falle:** In einem Verzeichnis ohne Git ist das opencode-Projekt
     „global" und `worktree = "/"` — relative Patterns matchen dann nie
     (praktisch bestätigt: sogar der legale Write wurde verweigert).
     ⇒ **Ein Bau muss ein Git-Repo mit mindestens einem Commit sein.**
     Dann ist `worktree` der Bau-Root und das Projekt bekommt eine
     eigene ID (Hash des Root-Commits). `hasenbau init` muss `git init`
     + Initial-Commit erledigen.
   - Zweiter Grund für den Git-Bau: „always"-Approvals landen in einer
     Permission-Tabelle **pro Projekt-ID** (unter XDG_DATA_HOME — mit dem
     Alltags-opencode geteilt, §3) und werden *nach* dem Agent-Ruleset
     ausgewertet — sie können dessen Deny überstimmen. Alle
     Nicht-Git-Verzeichnisse teilen sich die ID „global"; interaktive
     Approvals des Nutzers würden in die Hasen-Läufe lecken.
   - `bash: deny` (Pauschal-Deny mit Pattern `*`) entfernt das Tool
     komplett aus dem Toolset — der Hase sieht es gar nicht erst.
     `edit` mit Pattern-Ausnahme bleibt sichtbar und wird pro Aufruf
     geprüft. Pfade außerhalb des Worktree fängt zusätzlich
     `external_directory` ab (Default „ask" — explizit auf `deny`
     setzen). opencode hängt eigene Allows hinten an (u. a.
     `/tmp/opencode/*` und sein tool-output-Verzeichnis); akzeptiert.
   - **„ask" hängt im Headless-Betrieb:** Der Tool-Call publiziert
     `permission.asked` (SSE) und wartet auf
     `POST /permission/{requestID}/reply` (`once|always|reject`);
     pending Requests via `GET /permission`. Für Hasen deshalb alles
     explizit `allow`/`deny`, nie `ask`.
   - Testlauf-Ergebnis: Write in den erlaubten Raum OK; Write in einen
     fremden Raum, nach `/tmp` und per `../` aus dem Worktree hinaus
     verweigert (`DeniedError` geht als Tool-Fehler an den Hasen, der
     Lauf bricht nicht ab); `bash` fehlte im Toolset. Keine
     Escape-Datei entstand.
   - **Nachtrag 2026-08-10 (Baumeister, Hasenbau-xfm):** Ein
     erlaubtes `edit`-Pattern muss **nicht** unter `raeume/` liegen.
     Der Baumeister schreibt mit `"gaenge/entwurf/**": allow` in einem
     echten Lauf sauber dorthin — Patterns matchen gegen
     `path.relative(worktree, datei)`, und der Worktree ist der
     Bau-Root, nicht `raeume/`. Damit kann jeder Bau-relative Pfad ein
     Schreibziel sein; die Rollen-Konvention (§4) ist Konvention, keine
     Grenze der Permissions.
6. ~~Agent-Reload: Wie werden generierte Agenten wirksam, ohne den Server
   neu zu starten?~~ **Verifiziert 2026-07-13 (opencode 1.15.13,
   Test-Bau + Quellcode):** Agent-Definitionen werden pro Instanz
   gecached (`InstanceState`; das `agents/**/*.md`-Glob läuft beim
   Config-Laden). **`POST /instance/dispose`** verwirft die Caches, der
   nächste Request lädt neu — praktisch bestätigt: Dateiänderung ohne
   dispose unsichtbar (Negativprobe), nach dispose sofort in `GET /agent`
   sichtbar, kein Restart. opencode nutzt denselben Mechanismus intern
   nach `PATCH /config`. **Aber: dispose ist nicht lauf-sicher.** Der
   `SessionRunState`-Finalizer cancelt alle aktiven Session-Runner
   (`session/run-state.ts`), pending Permission-Asks werden rejected.
   Der Runner muss dispose deshalb gegen aktive Läufe serialisieren
   (kein Lauf aktiv ⇒ dispose; neue Läufe warten solange). Da Agenten
   beim Laden der Definitionen generiert werden (§6), nicht pro Lauf,
   ist dispose ohnehin selten.
7. ~~Rückkanal: Woher weiß der MCP-Server, zu welchem Lauf ein
   `summary()` gehört?~~ **Verifiziert 2026-08-05 (opencode 1.15.13,
   Binary + echter Lauf, Hasenbau-ekm):** **Gar nicht — opencode reicht
   an MCP-Tools keinen Session-Kontext durch.** Der Aufruf ist
   `callTool({name, arguments})`, sonst nichts; lokale Server werden mit
   `cwd` = Projektverzeichnis und `env` = Server-Env + statischem
   `environment`-Block der Config gespawnt. Die Zuordnung muss also aus
   dem Hasenbau kommen.
   - **Entschieden:** ein MCP-Eintrag (`mcp.hasenbau`, stdio,
     `hasenbau -bau <root> mcp`), Ziel ist der **eindeutige** Lauf mit
     `status='laeuft'`. Bei keinem oder mehreren aktiven Läufen
     schreibt der Rückkanal nichts und sagt dem Hasen warum — geraten
     wird nie, sonst landet eine Summary am falschen Lauf. Die Summary
     geht dabei nicht verloren: der Fallback (letzte Assistant-Message)
     trägt sie beim Lauf-Ende ein.
   - **Bekannte Grenze:** Laufen zwei Aufträge gleichzeitig (cron +
     watch), ist der Rückkanal für beide vorübergehend zu. Nach einem
     Daemon-Absturz gilt das **nicht** mehr: Zeilen, deren Wirt tot
     ist, zählen nicht als aktiver Lauf, und beim nächsten Start
     werden sie abgeräumt (§5, erledigt 2026-08-10, Hasenbau-c6i).
     Verworfene
     Alternativen: ein MCP-Eintrag pro Auftrag (exakt, aber der Daemon
     müsste die user-eigene `opencode.json` pro Auftrag pflegen, N
     Subprozesse, N×2 Werkzeuge in jeder Werkzeugliste plus
     `tools:`-Filter im generierten Agenten) und die Lauf-ID im Prompt
     (das Modell müsste sie fehlerfrei abschreiben).
   - **Werkzeugnamen:** opencode stellt den Server-Namen voran, der
     Hase sieht `hasenbau_notiz` und `hasenbau_summary` (intern lautet
     der Schlüssel `hasenbau:notiz`, am Modell kommt der Unterstrich
     an — im echten Lauf bestätigt).

---

## 12. Die CLI

Angelehnt an `kubectl`, entschieden 2026-08-10 (Hasenbau-ha0). Die
Befehle waren gewachsen und mischten Verben mit Substantiven; das hier
ist die Regel, an der sich jeder neue misst.

| Verb | Bedeutung |
|---|---|
| `get <ressource> [name]` | **eine Zeile pro Objekt**, Spalten, listenfähig |
| `describe <ressource> <name>` | **ein Objekt**, gerenderte Abschnitte, inklusive abgeleiteter Information und Querbezüge |
| `new <ressource> <name>` | ein Objekt **anlegen** — kommentiertes Gerüst, nie überschreibend |
| `lauf`, `baumeister`, `daemon`, `dig`, `findings`, `provider fetch`, `init` | **tun** etwas |

**`describe` ist kein `cat`.** Es gibt nie eine Datei im Volltext aus;
wer die will, nimmt `cat` — `describe` nennt ihm den Pfad und die
Zeilenzahl. Bei Aufträgen und Hasen heißt das: Frontmatter gerendert,
plus was der Hasenbau daraus ableitet (effektive Permissions,
Benutzungen, letzte Läufe), aber der Markdown-Body nur als Verweis. Der
Prompt ist Fließtext, dafür ist ein Editor da; sonst wäre `describe` ein
`cat` mit Kopfzeilen.

**Zwei Fragen, zwei Befehle.** `describe bau` **prüft** — Layout,
Git-Commit, Bau-Config, Rückkanal-Binary, generierte Agenten,
liegengebliebene `$WORK`-Reste. `status` **zeigt** nur: was der Bau
kennt, wie viele Läufe es gab, die jüngsten davon. Der Unterschied ist
nicht kosmetisch: ein Dashboard, das mahnt, liest sich niemand mehr
freiwillig an. Die beiden wertvollsten Prüfungen sind die
unauffälligsten — ein Bau ohne Git-Commit bekommt keine eigene
Projekt-ID (§11.5), ein Rückkanal-Eintrag auf ein verschwundenes Binary
nimmt den Hasen still ihre Werkzeuge weg (Hasenbau-2nq/08u). Beides
merkt man sonst erst an einem Lauf, der komisch aussieht.

**Die Sprache folgt §1.** Befehle und Ressourcen sind englisch, außer
den sieben Domänen-Begriffen: `lauf`, `hase`, `gang`, `auftrag`,
`baumeister`, `bau` bleiben deutsch, weil sie die Sache benennen. Alles
andere nicht — `graben` wurde zu `dig`, und die Befund-Analyse heißt
`findings`, obwohl die Prosa weiter von Befunden spricht.

**Ein Substantiv darf zweimal vorkommen.** `hasenbau lauf <auftrag>`
löst aus, `hasenbau get lauf <id>` zeigt an. Das ist bewusst in Kauf
genommen: der `get`-Präfix macht den Unterschied deutlich genug, und
`lauf` als Auslöse-Befehl war zuerst da.
