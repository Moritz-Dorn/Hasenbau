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
und DB-Tabellen. Die Domänen-Ebene ist deutsch, die Infrastruktur-Ebene
englisch (`Store`, `Scheduler`, `Watcher`, `Client`, `Runner`).
Keine Mischformen wie `HasenStore`.

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
├── hasenbau.yaml            # Daemon-Config: Defaults, Sinks, Log-Level
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
│   └── pdf_to_md.py
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

SQLite, WAL-Modus. Bewusst klein — vier Tabellen, nicht dreißig.
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
  kosten_cent   INTEGER
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

Gelesen werden sie von `hasenbau graben`: was der Hase selbst für
erwähnenswert hielt, steht dort über dem Trace.

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

gaenge:                            # deterministisch, läuft VOR dem Hasen
  - name: pdf-zu-markdown
    run: gaenge/pdf_to_md.py "$INPUT" --out "$WORK/extrakt.md"
    timeout: 120s

hase: archivar                     # → Template hasen/archivar.md

raeume:
  input: raeume/laderampe/sources/
  work:  raeume/laderampe/work/
  out:   raeume/lager/
  done:  raeume/archiv/
  quarantaene: raeume/quarantaene/ # Gang-Fehler ⇒ Input landet hier (§7)

kontext:                           # Push: was kommt in den Prompt
  - datei: $WORK/extrakt.md
  - letzte_summaries: 3

nachher:
  - move: $INPUT -> raeume/archiv/
---

Der extrahierte Text liegt in `$WORK/extrakt.md`.
Fasse ihn zusammen, vergib Tags, und lege ihn strukturiert in `lager/` ab.
Dateiname: `YYYY-MM-DD-<slug>.md`
```

Der Hase sieht das PDF nie — er kriegt Markdown. Das ist der Punkt.

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

### Variablen im Auftrag

| Variable | Bedeutung |
|---|---|
| `$BAU` | Root des Baus |
| `$INPUT` | Die auslösende Datei (nur bei `watch`) |
| `$WORK` | Scratch-Verzeichnis dieses Laufs, wird pro Lauf angelegt |
| `$RAUM_<name>` | Pfad des unter `raeume:` benannten Raums |

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
er riss im Spike bei langen Läufen ab. Danach liefert `Session.Messages`
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
2. `hasenbau graben <lauf-id>` zieht den Trace der Session
   (`session.messages()` → Tool-Call-Parts mit Namen und Argumenten;
   strukturiert, kein Log-Parsing) ✅ *(2026-07-14, Hasenbau-2qy:
   `graben [-json]`, Zugriffsweg §11.3 — der Trace enthält auch die
   reasoning-Parts, also die Absicht des Hasen)*
3. Der **Baumeister** (ein Hase mit Schreibrecht auf `gaenge/`) bekommt den
   Trace und schreibt daraus ein Skript
4. Der Nutzer liest das Skript und trägt es selbst in den Auftrag ein

**Die harte Stelle:** Ein Trace ist konkret, ein Gang muss generisch sein.
Der Trace sagt `read("sources/rechnung-2026-03.pdf")`, der Gang muss
`read("$INPUT")` sagen. Diese Generalisierung — was ist Parameter, was
Konstante, was war ein Fehlversuch, den der Hase danach korrigiert hat —
ist selbst eine Modell-Aufgabe und geht regelmäßig daneben.

**Deshalb, nicht verhandelbar:**

> Ein gegrabener Gang wird **nie automatisch scharf geschaltet.** Der
> Baumeister schreibt das Skript nach `gaenge/`, der Nutzer liest es, der
> Nutzer trägt es ein. Sonst entsteht ein System, das sich selbst
> umschreibt und dessen Fehlverhalten drei Läufe später in einem
> generierten Skript steckt, das nie jemand gelesen hat.

**Rückkanal:** Ein kleiner MCP-Server (in Go, `mark3labs/mcp-go`), der den
Hasen die Tools `notiz(text)` und `summary(text)` gibt und damit direkt in
die SQLite schreibt. Strukturierte Writes statt stdout-Parsing — sonst
entsteht in drei Wochen ein Regex-Friedhof. ✅ *(2026-08-05,
Hasenbau-ekm: `hasenbau mcp` über stdio, von opencode gestartet;
Eintrag `mcp.hasenbau` in der Bau-Config, den der Daemon bei jedem
Server-Start auf das laufende Binary setzt. Der generierte Agent bringt
den Absatz mit, der die Werkzeuge erklärt — ohne ihn ruft sie kein Hase.
Zur Lauf-Zuordnung §11.7)*

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
     längst toter Server-Instanzen — `hasenbau graben` funktioniert
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
     watch), ist der Rückkanal für beide vorübergehend zu. Dasselbe
     gilt nach einem Daemon-Absturz, solange verwaiste
     `laeuft`-Zeilen in der DB stehen (Hasenbau-c6i). Verworfene
     Alternativen: ein MCP-Eintrag pro Auftrag (exakt, aber der Daemon
     müsste die user-eigene `opencode.json` pro Auftrag pflegen, N
     Subprozesse, N×2 Werkzeuge in jeder Werkzeugliste plus
     `tools:`-Filter im generierten Agenten) und die Lauf-ID im Prompt
     (das Modell müsste sie fehlerfrei abschreiben).
   - **Werkzeugnamen:** opencode stellt den Server-Namen voran, der
     Hase sieht `hasenbau_notiz` und `hasenbau_summary` (intern lautet
     der Schlüssel `hasenbau:notiz`, am Modell kommt der Unterstrich
     an — im echten Lauf bestätigt).
