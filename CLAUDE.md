@AGENTS.md

## Claude Code

- SDK-Aufrufe (`internal/opencode/`) nie aus dem Gedächtnis erweitern —
  die Signaturen sind Stainless-generiert. Verifizierter Stand in
  PLAN.md §11.2; bei Neuem im Modul-Cache nachlesen
  (`go env GOMODCACHE`/github.com/sst/opencode-sdk-go@…) oder die
  OpenAPI-Spec unter `<server>/doc` ziehen.
- Git: Bead geschlossen ⇒ zugehörige Commits gehören dazu (siehe
  Git-Workflow in AGENTS.md). Pushen nur auf ausdrückliche Aufforderung.
