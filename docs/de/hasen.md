# Der Hase: Werkzeuge, Grenzen, Wissen

[English](../hasen.md) · **Deutsch**. Übersetzung; maßgeblich ist die englische Fassung.

Ein Hase ist ein Template in `hasen/`. Der Daemon generiert daraus pro
Auftrag×Hase einen opencode-Agenten; die Permissions kommen aus den
Räumen des Auftrags, nicht aus dem Template. `hasenbau describe hase
<name>` zeigt, was für einen Hasen in einem konkreten Auftrag
herauskommt.

Werkzeuge, die er während eines Laufs ruft, kommen vom Schmied und gehen
durch eine dreistufige Freigabe: [Werkzeuge](tools.md).

## Der Rückkanal

Jeder Hase bekommt Werkzeuge, mit denen er selbst in die Bau-Datenbank
schreibt:

- `hasenbau_summary` schreibt die eine Zeile, was der Lauf getan hat.
  Der nächste Lauf desselben Auftrags bekommt sie als Kontext.
- `hasenbau_notiz` hält Beobachtungen unterwegs fest; sie stehen später
  in `hasenbau dig`.
- `hasenbau_tool_request` gibt es nur, wenn in `hasenbau.yaml` ein
  `requests:`-Raum gesetzt ist. Damit fordert ein Hase ein Werkzeug an,
  das ihm für seine Aufgabe fehlt, statt sich einen Weg an seinen
  Grenzen vorbei zu suchen. Der Wunsch landet als Datei unter
  `<requests>/tools/`, dem künftigen Eingang des Schmieds. Ohne den
  Eintrag bleibt das Werkzeug aus, und der Hase wird auch im Prompt
  nicht darauf verwiesen; `hasenbau describe bau` sagt, woran der Bau
  gerade ist.

Dahinter steckt ein MCP-Server, den opencode als `hasenbau mcp` startet.
Eingetragen wird er von `hasenbau init`, und jeder Daemon- oder
Lauf-Start korrigiert den Eintrag auf das gerade laufende Binary und sagt
es im Log, auch nach einem Rebuild an einen anderen Pfad.

## Die sechs Verbote

Jeder generierte Agent bekommt dieselben sechs Verbote, unabhängig vom
Template: `bash`, `webfetch`, `websearch`, `external_directory`, `task`
und `question` stehen als `deny` in seinem `permission:`-Block.

Sie tauchen in der Werkzeugliste des Modells damit gar nicht erst auf,
und der Hase sucht keinen Weg um sie herum. `task` wiegt am schwersten:
ein Subagent wäre ein eigener Agent und erbte weder die Permissions noch
die Raum-Grenzen.

## Wissen

Ein Hasen-Template kann Hintergrundwissen anfordern:

- `knows_hasenbau: true` bindet eine mitgelieferte Einführung in den
  Hasenbau ein: Begriffe, Ablauf, Trace-Aufbau, Grenzen.
- `knowledge: [pfade]` bindet eigene Dateien aus dem Bau ein.

Beides landet im generierten Agenten und gilt damit nur für diesen
Hasen. `instructions` in der `opencode.json` gilt dagegen
Workspace-weit für jeden Agenten. Die Einführung steckt bewusst im Binary
statt im Bau: so passt sie immer zur installierten Version, statt als
veraltete Kopie mitzulaufen.
