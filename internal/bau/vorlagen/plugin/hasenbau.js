// Der Sandbox-Wächter des Hasenbaus (Hasenbau-d2p).
//
// Ausgeliefert von `hasenbau init`/`fix`. Änderungen bleiben stehen,
// aber eine gelöschte Datei wird wiederhergestellt.
//
// Er beantwortet die Frage, die dem `tools:`-Block im generierten
// Agenten fehlt: greift der überhaupt? Ein Hase hat die bash-Sperre
// einmal umgangen, indem er über `task` einen Subagenten startete — der
// erbt weder Permissions noch Raum-Grenzen. Der Agent bekommt solche
// Werkzeuge deshalb entzogen; ob das wirkt, ist an keinem Endpoint
// ablesbar. Hier ist es ablesbar: dieser Hook läuft im Server-Prozess
// und sieht jeden Aufruf, egal welcher Agent ihn stellt.
//
// Die Regel steht NICHT hier. Das Plugin meldet an das Binary und tut,
// was zurückkommt — sonst läge die Sandbox-Semantik in einer
// opencode-spezifischen Datei, und beim Wechsel des Backends (PLAN §6)
// müsste man sie nachbauen.
import { readFileSync } from "node:fs"
import { join } from "node:path"

const VERDAECHTIG = new Set(["task", "bash", "webfetch", "websearch"])

// Den Binary-Pfad aus der Bau-Config lesen statt auf den PATH zu
// hoffen: `hasenbau mcp` ist eine Selbstreferenz auf genau das Binary,
// das diesen Bau fährt, und EnsureMCP hält den Eintrag bei jedem Start
// aktuell. Ein Pfad aus dem PATH wäre ein anderer Hasenbau — oder
// keiner (Hasenbau-2nq/08u).
function binaryPfad(bau) {
  try {
    const cfg = JSON.parse(readFileSync(join(bau, ".opencode-home/opencode/opencode.json"), "utf8"))
    const cmd = cfg?.mcp?.hasenbau?.command
    if (Array.isArray(cmd) && cmd.length > 0) return cmd[0]
  } catch {}
  return "hasenbau"
}

export const SandboxWaechter = async ({ $, directory }) => {
  const hasenbau = binaryPfad(directory)

  return {
    "tool.execute.before": async (input, output) => {
      if (!VERDAECHTIG.has(input.tool)) return

      // Argumente mitgeben: „bash gerufen" ist eine Warnung, „bash mit
      // python3 sync.py" ist ein Werkzeug-Wunsch und sagt, was zu bauen
      // wäre.
      let args = ""
      try {
        args = JSON.stringify(output.args ?? {})
      } catch {
        args = "<nicht serialisierbar>"
      }

      // nothrow: ein Wächter, der Läufe umbringt, wird abgeschaltet —
      // und misst dann gar nichts mehr. Fällt die Meldung aus, läuft
      // der Aufruf durch, und die Lücke steht im Server-Log.
      const r = await $`${hasenbau} -bau ${directory} sandbox-vorfall --tool ${input.tool} --session ${input.sessionID} --args ${args}`
        .quiet()
        .nothrow()

      // Exit 3 heißt abweisen. Der Text ist für den Hasen bestimmt: er
      // kommt bei ihm als Fehler des Werkzeugs an und ist dort lesbar
      // (verifiziert am 2026-08-12 an einem echten Lauf).
      if (r.exitCode === 3) {
        throw new Error(r.stdout.toString().trim() || "Dieses Werkzeug führt aus deiner Sandbox heraus.")
      }
      if (r.exitCode !== 0) {
        console.error("hasenbau: Sandbox-Vorfall nicht gemeldet (exit " + r.exitCode + "): " + r.stderr.toString().trim())
      }
    },
  }
}
