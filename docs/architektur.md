# Architektur und Isolation

## Die Idee

1. Trigger statt Chat. Aufgaben starten, weil eine Datei in einem
   überwachten Raum landet oder ein Cron-Zeitpunkt erreicht ist, nicht,
   weil jemand tippt.
2. Deterministisches vor Probabilistischem. Bevor ein Hase Material
   sieht, laufen Gänge, also normale Skripte ohne LLM. Der Hase bekommt
   aufbereitetes Markdown, nie das rohe PDF.
3. Verdichtung. Ein Hase, der bei jedem Lauf dieselben Tool-Calls macht,
   ist ein Interpreter, der jedes Mal neu kompiliert. Der Hasenbau loggt
   die Traces; `hasenbau dig` und der Baumeister machen daraus
   deterministische Gänge. Aktiviert wird ein generierter Gang nie
   automatisch.

Nicht dazu gehören: Remote und Multi-Host, eine Web-UI, ein eigenes
Agent-Framework.

## Die Teile

```
systemd → hasenbau (ein Go-Binary)
            ├── Scheduler   cron-Trigger
            ├── Watcher     Datei-Trigger (fsnotify)
            ├── Runner      Gänge, dann der Hase
            ├── Store       SQLite (WAL, kein cgo)
            └── Supervisor ──spawnt──> opencode serve (127.0.0.1, Child)
```

Der vollständige Implementierungsplan steht in [`PLAN.md`](../PLAN.md):
Architektur §2, Isolation §3, Layout §4, Datenmodell §5,
Auftragsformat §6.

## Isolation

Der opencode-Server läuft mit isolierter Config (`XDG_CONFIG_HOME` zeigt
in den Bau): keine Plugins und Hooks aus der Alltags-Config, aber
geteilte Credentials (`auth.json` via `XDG_DATA_HOME`).

Sessions ankern immer am Bau-Root; die Hasen sehen nur die Räume ihres
Auftrags. Das ist auch der Grund, warum `hasenbau init` einen Root-Commit
anlegt: ohne ihn bekommt der Bau keine eigene Projekt-ID, und die
Raum-Permissions greifen nicht.

## Provider im Bau

Dass ein Bau seine custom Provider selbst mitbringt, folgt aus derselben
Isolation: `auth.json` teilt die Schlüssel, die Definitionen bleiben im
Bau. Deshalb der handgepflegte `provider:`-Block in
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

Die Modell-Liste holt danach `hasenbau provider fetch scc` am Endpoint.
Geschrieben wird sie nie automatisch; erst zeigt der Befehl den Diff.
`hasenbau get provider` sagt, was der Bau kennt und was holbar ist.

## Als systemd-Unit

`opencode` muss im PATH der Unit stehen, der Daemon startet es als
Kind-Prozess:

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

Gestoppt wird mit `systemctl --user stop hasenbau`. Das ist `SIGTERM` und
damit derselbe saubere Weg wie Ctrl-C im Vordergrund.

## Zwei Datenbanken

Die SQLite unter `state/hasenbau.db` trackt, was der Hasenbau tut
(PLAN.md §5). Beads trackt, wie der Hasenbau gebaut wird, also die
Entwicklung dieses Repos; mit einem laufenden Bau hat es nichts zu tun.
