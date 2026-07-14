# Hasenbau — Agent-Instruktionen

Ein Go-Daemon, der `opencode` headless orchestriert: zeitgesteuerte und
dateigetriggerte Aufträge, deterministische Vorverarbeitung (Gänge) vor
dem LLM-Schritt (Hase). Lokal, eine Maschine — kein Remote, keine Web-UI.

**`PLAN.md` ist der Spec.** Bei Design-Fragen zuerst dort nachsehen:
Architektur §2, Isolation §3, Layout §4, Datenmodell §5, Auftragsformat §6,
Phasen §8, offene Punkte §11. Beads (`bd ready`, `bd show`) trägt den
Arbeitsstand und die Befund-Notizen; PLAN.md trägt das Warum. Beides lesen,
bevor gebaut wird. Spike-Ergebnisse werden in PLAN.md §11 eingepflegt.

## Build & Test

```bash
go build ./...
go vet ./...
go test ./...        # Integrationstests skippen sich ohne opencode im PATH
```

`-race` braucht cgo und gcc (fehlt derzeit im Nix-Profil).

Der End-zu-End-Smoke-Test kostet einen echten LLM-Call und ist deshalb
doppelt gegated:

```bash
HASENBAU_SMOKE=1 HASENBAU_SMOKE_MODEL=scc/kit.glm-5.2-753b \
  go test ./internal/opencode/ -run TestSmokePromptRoundtrip -v
```

Test-Bau für manuelle Experimente: `~/SRC/meinHasenbau` (außerhalb des
Repos, siehe Leckage-Abschnitt).

## Git-Workflow

Wer einen Bead schließt, committet auch die zugehörigen Änderungen —
im selben Arbeitsgang, nicht „später":

- Ein Commit pro logischer Einheit (Implementierung, Tests, Doku/PLAN.md,
  Beads-Export dürfen getrennte Commits sein). Bead-ID in die
  Commit-Message, wenn der Commit einen Bead abschließt.
- Vorher Quality-Gates: `go vet ./...` und `go test ./...`.
- **Push weiterhin nur auf ausdrückliche Aufforderung.**
- Eigenheit: Der Beads-pre-commit-Hook exportiert `.beads/issues.jsonl`
  *während* des Commits — bleibt die Datei danach modified, gehört sie
  per `git add .beads/ && git commit --amend --no-edit` in den letzten
  Commit gefaltet.

## Vokabular (verbindlich, PLAN.md §1)

| Begriff | Bedeutung |
|---|---|
| **Bau** | Root-Verzeichnis des Systems (Config, Räume, State) |
| **Raum** | Benanntes Verzeichnis im Materialfluss |
| **Hase** | Template in `hasen/`; der Daemon generiert daraus den opencode-Agenten pro Auftrag×Hase, Permissions aus den Räumen des Auftrags (PLAN.md §6) |
| **Gang** | Deterministisches Skript, läuft vor dem Hasen. Kein LLM |
| **Auftrag** | Trigger + Gänge + Hase + Räume (Job-Definition) |
| **Lauf** | Eine Ausführung eines Auftrags |
| **Baumeister** | Sonder-Hase, verdichtet Traces zu Gängen (Phase 2) |

Domänen-Ebene deutsch, Infrastruktur englisch (`Store`, `Scheduler`,
`Watcher`, `Client`, `Runner`, `Supervisor`). Keine Mischformen, kein
`Job` statt `Auftrag`.

Sprachlich strikt trennen: **Hasenbau** ist dieses Projekt/Programm,
ein **Bau** ist eine mit `hasenbau init` erzeugte Instanz (eigenes
Git-Repo, ohne AGENTS.md/CLAUDE.md). „Der Hasenbau" meint nie einen Bau.

## ⚠️ AGENTS.md-Leckage (PLAN.md §3)

Dieses Repo baut ein System, das selbst opencode-Agents startet — und
diese lesen `AGENTS.md`. Deshalb zwei harte Regeln:

- Test-Baue liegen **immer außerhalb des Repos**. Vereinbarter Pfad:
  `~/SRC/meinHasenbau`. Nie unter `testdata/`.
- Das CWD eines gespawnten opencode-Servers liegt **immer im Bau**,
  nie im Projekt-Root. Sonst liest der Hase diese Datei und fängt an,
  Beads-Issues zu filen, statt PDFs einzusortieren.

## Zwei Datenbanken — nicht verwechseln (PLAN.md §9)

Beads trackt, wie der Hasenbau *gebaut* wird. Die SQLite unter
`state/hasenbau.db` (PLAN.md §5) trackt, was der Hasenbau *tut*.

## Non-Interactive Shell Commands

**ALWAYS use non-interactive flags** with file operations to avoid hanging on confirmation prompts.

Shell commands like `cp`, `mv`, and `rm` may be aliased to include `-i` (interactive) mode on some systems, causing the agent to hang indefinitely waiting for y/n input.

**Use these forms instead:**
```bash
# Force overwrite without prompting
cp -f source dest           # NOT: cp source dest
mv -f source dest           # NOT: mv source dest
rm -f file                  # NOT: rm file

# For recursive operations
rm -rf directory            # NOT: rm -r directory
cp -rf source dest          # NOT: cp -r source dest
```

**Other commands that may prompt:**
- `scp` - use `-o BatchMode=yes` for non-interactive
- `ssh` - use `-o BatchMode=yes` to fail instead of prompting
- `apt-get` - use `-y` flag
- `brew` - use `HOMEBREW_NO_AUTO_UPDATE=1` env var

<!-- BEGIN BEADS INTEGRATION v:1 profile:minimal hash:970c3bf2 -->
## Beads Issue Tracker

This project uses **bd (beads)** for issue tracking. Run `bd prime` to see full workflow context and commands.

### Quick Reference

```bash
bd ready              # Find available work
bd show <id>          # View issue details
bd update <id> --claim  # Claim work
bd close <id>         # Complete work
```

### Rules

- Use `bd` for ALL task tracking — do NOT use TodoWrite, TaskCreate, or markdown TODO lists
- Run `bd prime` for detailed command reference and session close protocol
- Use `bd remember` for persistent knowledge — do NOT use MEMORY.md files

**Architecture in one line:** issues live in a local Dolt DB; sync uses `refs/dolt/data` on your git remote; `.beads/issues.jsonl` is a passive export. See https://github.com/gastownhall/beads/blob/main/docs/SYNC_CONCEPTS.md for details and anti-patterns.

## Agent Context Profiles

The managed Beads block is task-tracking guidance, not permission to override repository, user, or orchestrator instructions.

- **Conservative (default)**: Use `bd` for task tracking. Do not run git commits, git pushes, or Dolt remote sync unless explicitly asked. At handoff, report changed files, validation, and suggested next commands.
- **Minimal**: Keep tool instruction files as pointers to `bd prime`; use the same conservative git policy unless active instructions say otherwise.
- **Team-maintainer**: Only when the repository explicitly opts in, agents may close beads, run quality gates, commit, and push as part of session close. A current "do not commit" or "do not push" instruction still wins.

## Session Completion

This protocol applies when ending a Beads implementation workflow. It is subordinate to explicit user, repository, and orchestrator instructions.

1. **File issues for remaining work** - Create beads for anything that needs follow-up
2. **Run quality gates** (if code changed) - Tests, linters, builds
3. **Update issue status** - Close finished work, update in-progress items
4. **Handle git/sync by active profile**:
   ```bash
   # Conservative/minimal/default: report status and proposed commands; wait for approval.
   git status

   # Team-maintainer opt-in only, unless current instructions forbid it:
   git pull --rebase
   bd dolt push
   git push
   git status
   ```
5. **Hand off** - Summarize changes, validation, issue status, and any blocked sync/commit/push step

**Critical rules:**
- Explicit user or orchestrator instructions override this Beads block.
- Do not commit or push without clear authority from the active profile or the current user request.
- If a required sync or push is blocked, stop and report the exact command and error.
<!-- END BEADS INTEGRATION -->

<!-- BEGIN BEADS CODEX SETUP: generated by bd setup codex -->
## Beads Issue Tracker

Use Beads (`bd`) for durable task tracking in repositories that include it. Use the `beads` skill at `.agents/skills/beads/SKILL.md` (project install) or `~/.agents/skills/beads/SKILL.md` (global install) for Beads workflow guidance, then use the `bd` CLI for issue operations.

### Quick Reference

```bash
bd ready                # Find available work
bd show <id>            # View issue details
bd update <id> --claim  # Claim work
bd close <id>           # Complete work
bd prime                # Refresh Beads context
```

### Rules

- Use `bd` for all task tracking; do not create markdown TODO lists.
- Run `bd prime` when Beads context is missing or stale. Codex 0.129.0+ can load Beads context automatically through native hooks; use `/hooks` to inspect or toggle them.
- Keep persistent project memory in Beads via `bd remember`; do not create ad hoc memory files.

**Architecture in one line:** issues live in a local Dolt DB; sync uses `refs/dolt/data` on your git remote; `.beads/issues.jsonl` is a passive export. See https://github.com/gastownhall/beads/blob/main/docs/SYNC_CONCEPTS.md for details and anti-patterns.
<!-- END BEADS CODEX SETUP -->
