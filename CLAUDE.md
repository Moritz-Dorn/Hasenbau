@AGENTS.md

## Claude Code

- **Zuerst `bd prime` lesen — ganz.** Der SessionStart-Hook ruft es
  automatisch, aber seine Ausgabe ist größer als das, was im Kontext
  landet: sie wird als „Output too large" in eine Datei ausgelagert,
  und die Vorschau zeigt rund 2 von 12 KB. Diese Datei mit `Read`
  öffnen, bevor die Arbeit beginnt — dort stehen die Projekt-Memories.
  Am 2026-08-12 unterblieb das, und die Folgen standen alle in den
  ungelesenen zwei Dritteln: vier Testläufe auf einem externen Modell
  gegen ein erschöpftes Budget, ein per `timeout` gekillter Lauf, und
  ein deutscher Formatschlüssel, den eine Rückfrage verhindert hätte.
- SDK-Aufrufe (`internal/opencode/`) nie aus dem Gedächtnis erweitern —
  die Signaturen sind Stainless-generiert. Verifizierter Stand in
  PLAN.md §11.2; bei Neuem im Modul-Cache nachlesen
  (`go env GOMODCACHE`/github.com/sst/opencode-sdk-go@…) oder die
  OpenAPI-Spec unter `<server>/doc` ziehen.
- Dasselbe gilt für opencode selbst: Plugin- und Tool-API im lokalen
  Paket nachlesen (`~/.opencode/node_modules/@opencode-ai/…`), nicht
  raten. Und was ein Agent *tatsächlich* darf, ist über die HTTP-API
  nur begrenzt messbar — siehe das Memory
  `opencode-werkzeugliste-nicht-je-agent-messbar`, bevor eine Messung
  als Beweis gilt.
- Git: Bead geschlossen ⇒ zugehörige Commits gehören dazu (siehe
  Git-Workflow in AGENTS.md). Pushen nur auf ausdrückliche Aufforderung.
