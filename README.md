# Hasenbau 🐇

Ein Daemon, der [opencode](https://opencode.ai) headless orchestriert:
zeitgesteuerte und dateigetriggerte Agenten-Aufträge mit deterministischer
Vorverarbeitung — lokal, ein Binary, kein Cloud-Dienst.

**Status: frühe Entwicklung** (Phase 0 — Fundament). `PLAN.md` ist der Spec.

## Die Idee

1. **Trigger statt Chat.** Aufgaben starten, weil eine Datei in einem
   überwachten Raum landet oder ein Cron-Zeitpunkt erreicht ist — nicht,
   weil jemand tippt.
2. **Deterministisches vor Probabilistischem.** Bevor ein Agent („Hase")
   Material sieht, laufen „Gänge" — normale Skripte ohne LLM. Der Hase
   bekommt aufbereitetes Markdown, nie das rohe PDF.
3. **Verdichtung.** Ein Hase, der bei jedem Lauf dieselben Tool-Calls
   macht, ist ein Interpreter, der jedes Mal neu kompiliert. Der Hasenbau
   loggt die Traces; `hasenbau graben` + der Baumeister-Hase machen daraus
   deterministische Gänge. Aktiviert wird ein generierter Gang **nie
   automatisch** — der Nutzer liest ihn und trägt ihn selbst ein.

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
| **Hase** | opencode-Agent + Working Directory + Permissions |
| **Auftrag** | Trigger + Gänge + Hase + Räume |
| **Lauf** | Eine Ausführung eines Auftrags |

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
