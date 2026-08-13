// Das Bau-Plugin des Hasenbaus. Es tut zwei Dinge, und beide gehen nur
// hier, weil dieser Code im SERVER-Prozess läuft:
//
//   1. Sandbox-Wächter (Hasenbau-d2p). Der Hook `tool.execute.before`
//      sieht jeden Werkzeug-Aufruf jedes Agenten — auch den eines
//      Subagenten, und unabhängig davon, was ein Agent über sich selbst
//      behauptet. Er beantwortet damit die Frage, die dem
//      permission-Block des generierten Agenten fehlt: greift der
//      überhaupt?
//   2. Schmied-Werkzeuge (Hasenbau-hcs). Beim Start liest es die
//      Manifeste unter tools/ und registriert daraus je ein Werkzeug.
//      Dessen `execute` läuft ebenfalls im Server-Prozess und darf
//      deshalb ein Skript starten, obwohl der Hase kein `bash` hat —
//      die Permissions gelten dem Agenten, nicht dem Server. Genau das
//      ist der legale Weg aus Hasenbau-wiu, und genau deshalb ist die
//      menschliche Freigabe hier die einzige Grenze.
//
// Ausgeliefert von `hasenbau init`/`fix`. Änderungen bleiben stehen,
// aber eine gelöschte Datei wird wiederhergestellt.
//
// Die Regeln stehen NICHT hier. Das Plugin meldet an das Binary und tut,
// was zurückkommt — sonst läge die Sandbox-Semantik in einer
// opencode-spezifischen Datei, und beim Wechsel des Backends (PLAN §6)
// müsste man sie nachbauen. Aus demselben Grund erfindet es an den
// Manifesten nichts: was ein Werkzeug ist, entscheidet der Hasenbau.
import { readFileSync, readdirSync } from "node:fs"
import { join } from "node:path"
import { tool } from "@opencode-ai/plugin"

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

// ladeWerkzeuge liest die Manifeste unter tools/ und baut daraus die
// Werkzeug-Definitionen. Ein kaputtes Manifest wird ÜBERSPRUNGEN und
// laut gemeldet, nicht geworfen: ein Plugin, das beim Laden wirft, ist
// still weg — und mit ihm der Sandbox-Wächter oben. Ein Werkzeug zu
// verlieren ist ärgerlich, den Wächter zu verlieren ist gefährlich.
//
// Dass ein fehlendes Werkzeug trotzdem auffällt, sichert die Go-Seite:
// `hasenbau` liest dieselben Manifeste beim Generieren der Agenten und
// verweigert dort den Start (internal/bau/tools.go). Diese Datei ist
// die nachsichtige Hälfte eines Paares, dessen andere Hälfte streng ist.
function ladeWerkzeuge(bau, $) {
  const dir = join(bau, "tools")
  let dateien = []
  try {
    dateien = readdirSync(dir).filter((f) => f.endsWith(".json")).sort()
  } catch {
    return {} // kein tools/ — der Bau hat schlicht keine Werkzeuge
  }

  const werkzeuge = {}
  for (const datei of dateien) {
    const name = datei.slice(0, -".json".length)
    try {
      const m = JSON.parse(readFileSync(join(dir, datei), "utf8"))
      if (!m?.description || !m?.script) throw new Error("description oder script fehlt")

      // Argumente aus dem Manifest. Die drei Typen sind dieselben, die
      // die Go-Seite zulässt; alles andere hat sie schon abgelehnt.
      const args = {}
      for (const a of m.args ?? []) {
        const basis =
          a.type === "number" ? tool.schema.number() : a.type === "boolean" ? tool.schema.boolean() : tool.schema.string()
        const beschrieben = basis.describe(a.description ?? "")
        args[a.name] = a.required ? beschrieben : beschrieben.optional()
      }

      werkzeuge[name] = tool({
        description: m.description,
        args,
        async execute(eingabe) {
          // argv statt `sh -c`: die Werte kommen aus einem Modell, und
          // eine Shell dazwischen wäre genau die Lücke, die der Wächter
          // nebenan zumacht. Skripte liegen neben ihrem Manifest.
          const argv = []
          for (const a of m.args ?? []) {
            const wert = eingabe?.[a.name]
            if (wert === undefined || wert === null) continue
            argv.push("--" + a.name, String(wert))
          }
          const skript = join(dir, m.script)
          const r = await $`${skript} ${argv}`.cwd(bau).quiet().nothrow()
          const aus = r.stdout.toString().trim()
          if (r.exitCode !== 0) {
            // Der Fehlertext kommt beim Modell an und ist dort
            // verwertbar (gemessen 2026-08-12) — also das Nützliche
            // hineinschreiben, nicht nur den Exit-Code.
            const fehler = r.stderr.toString().trim() || aus
            throw new Error(`${name} scheiterte (exit ${r.exitCode}): ${fehler}`)
          }
          return aus
        },
      })
    } catch (e) {
      console.error(`hasenbau: Werkzeug ${name} uebersprungen — ${e?.message ?? e}`)
    }
  }
  return werkzeuge
}

export const SandboxWaechter = async ({ $, directory }) => {
  const hasenbau = binaryPfad(directory)

  return {
    tool: ladeWerkzeuge(directory, $),

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
