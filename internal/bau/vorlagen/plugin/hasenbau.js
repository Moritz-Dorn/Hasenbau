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
// GENERIERT vom Hasenbau — nicht von Hand ändern. Die Datei wird bei
// jedem Start aus dem Binary geschrieben, sobald sie abweicht; eine
// Änderung daran ist beim nächsten Lauf weg. Das ist Absicht: was hier
// steht, ist eine Zusage des Hasenbaus (Review-Gate, Sandkasten,
// Wächter), keine Einstellung des Baus. Wer eigene Hooks oder Werkzeuge
// will, legt ein EIGENES Plugin daneben und trägt es im plugin:-Block
// der Bau-Config ein — das Verzeichnis gehört weiterhin dem Bau.
//
// Die Regeln stehen NICHT hier. Das Plugin meldet an das Binary und tut,
// was zurückkommt — sonst läge die Sandbox-Semantik in einer
// opencode-spezifischen Datei, und beim Wechsel des Backends (PLAN §6)
// müsste man sie nachbauen. Aus demselben Grund erfindet es an den
// Manifesten nichts: was ein Werkzeug ist, entscheidet der Hasenbau.
import { readFileSync, readdirSync } from "node:fs"
import { createHash } from "node:crypto"
import { join } from "node:path"
import { tool } from "@opencode-ai/plugin"

const VERDAECHTIG = new Set(["task", "bash", "webfetch", "websearch"])

// Der Sandkasten um ein Werkzeug im BETRIEB (Hasenbau-9w6, Stufe 2).
//
// Ein Schmied-Werkzeug laeuft hier im Server-Prozess und damit
// ausserhalb jeder Hasen-Sandbox. Ohne Grenze waere es der bequemste Weg
// aus den Raum-Rechten heraus: der Hase darf raeume/eingang nicht
// schreiben, sein Werkzeug schon. Deshalb gilt EIN WERKZEUG DARF NIE
// MEHR ALS DER HASE, DER ES RUFT — und die Grenze kommt aus derselben
// Quelle wie dessen Permissions, den Raeumen seines Auftrags.
//
// Ausgerechnet hat sie der Hasenbau (internal/hase/grenze.go) und in
// hasenbau-raeume.json neben die generierten Agenten gelegt. Hier steht
// nur die Anwendung.
const GRENZEN_DATEI = ".opencode-home/opencode/hasenbau-raeume.json"
const WERKZEUG_ZEITLIMIT = "60s"

function ladeGrenzen(bau) {
  try {
    const roh = JSON.parse(readFileSync(join(bau, GRENZEN_DATEI), "utf8"))
    const nach = {}
    for (const g of roh) nach[g.agent] = g
    return nach
  } catch {
    // Keine Datei heisst: keine Grenze bekannt. Das ist NICHT dasselbe
    // wie "keine noetig" — der Aufrufer weist dann ab.
    return null
  }
}

// SYSTEM sind die Verzeichnisse, ohne die kein Interpreter startet. Der
// Sandkasten baut von NICHTS aus auf und blendet nur diese ein — anders
// als der Probelauf (cmd/hasenbau/probelauf.go), der das Dateisystem
// lesend stehen laesst.
//
// Der Unterschied ist Absicht und folgt daraus, WER laeuft: beim
// Probelauf ein Mensch auf seiner eigenen Maschine, hier ein Werkzeug im
// Namen eines Hasen — und der hat `external_directory: deny`, sieht also
// nichts ausserhalb seines Baus. Ein `--ro-bind / /` gaebe seinem
// Werkzeug den Rest der Platte zu lesen, und damit mehr als ihm selbst.
//
// -try, weil nicht jede Maschine jedes davon hat (auf NixOS traegt
// /nix/store fast alles).
const SYSTEM = ["/nix", "/usr", "/bin", "/lib", "/lib64", "/etc"]

// sandkastenArgv sperrt einen Werkzeug-Aufruf auf die Raeume des
// rufenden Hasen ein.
//
// Die Reihenfolge traegt die Bedeutung: bwrap wendet Mounts von links
// nach rechts an, ein spaeterer ueberdeckt einen frueheren. Die
// Schreib-Bindungen kommen deshalb ZULETZT — ein Raum, der in beiden
// Listen steht, ist am Ende schreibbar.
function sandkastenArgv(bau, grenze, skript, argv) {
  const flags = []
  for (const dir of SYSTEM) flags.push("--ro-bind-try", dir, dir)
  flags.push("--dev", "/dev", "--proc", "/proc", "--tmpfs", "/tmp")
  // Das Skript selbst: ohne tools/ gaebe es nichts auszufuehren. Lesend
  // — ein Werkzeug, das sich selbst umschreibt, waere ein Weg an der
  // Freigabe vorbei.
  flags.push("--ro-bind", join(bau, "tools"), join(bau, "tools"))
  for (const raum of grenze.read ?? []) flags.push("--ro-bind-try", join(bau, raum), join(bau, raum))
  for (const raum of grenze.write ?? []) flags.push("--bind-try", join(bau, raum), join(bau, raum))
  flags.push(
    "--unshare-all",
    "--die-with-parent",
    "--new-session",
    "--setenv", "HOME", "/tmp",
    "--chdir", bau,
    "--",
    skript,
    ...argv,
  )
  return flags
}

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
// reviewPruefung trennt den Review-Block vom Body und vergleicht den
// Hash. Der Block ist die Zusage eines Menschen, GENAU DIESEN Inhalt
// gelesen zu haben; passt der Hash nicht, wurde danach geändert und die
// Zusage gilt nicht mehr (ValIntent `outdated`).
//
// Das ist die Stelle, an der die Bindung wirksam wird: ein Werkzeug,
// dessen Skript nach dem Review getauscht wurde, kommt hier nicht durch
// und wird keinem Hasen angeboten.
function reviewPruefung(quelle) {
  // `#` fuer Python und Bash, `//` fuer TypeScript — der Block gehoert
  // an den Code, nicht an eine Sprache.
  const kommentar = (z) => {
    const t = z.trim()
    for (const zeichen of ["//", "#"]) {
      if (t.startsWith(zeichen)) return t.slice(zeichen.length).trim()
    }
    return null
  }

  // Der Block ist HOMOGEN: nur Zeilen mit demselben Kommentarzeichen
  // wie die Markenzeile gehoeren dazu. Ohne das liesse sich Code am
  // Hash vorbeischmuggeln — in einem Bash-Skript ist `//bin/sh -c …`
  // kein Kommentar, sondern ein ausfuehrbarer Pfad (gemessen
  // 2026-08-13, Hasenbau-9w6).
  const zeilen = quelle.split("\n")
  let start = -1
  let ende = -1
  let zeichen = ""
  let abgeschlossen = false
  for (let i = 0; i < zeilen.length; i++) {
    if (start < 0) {
      const feld = kommentar(zeilen[i])
      if (feld !== null && feld.startsWith("hasenbau-review:")) {
        start = i
        zeichen = zeilen[i].trim().startsWith("//") ? "//" : "#"
      }
      continue
    }
    if (ende >= 0) continue
    if (!zeilen[i].trim().startsWith(zeichen)) {
      ende = i
      continue
    }
    // Ausdrueckliche Grenze: ohne sie muesste man das Ende raten, und
    // das verschluckt Kommentare, die ohnehin schon im Skript standen.
    if (kommentar(zeilen[i]).startsWith("hasenbau-review-end")) {
      ende = i + 1
      abgeschlossen = true
    }
  }
  if (start < 0) return { ok: false, grund: "kein Review — ungelesen" }
  if (!abgeschlossen) return { ok: false, grund: "Review ohne Schlusszeile" }
  if (ende < 0) ende = zeilen.length

  const block = zeilen.slice(start, ende).join("\n")
  const body = zeilen.slice(0, start).concat(zeilen.slice(ende)).join("\n")
  const soll = /^\s*(?:#|\/\/)\s*body-sha256:\s*([0-9a-f]{64})\s*$/m.exec(block)?.[1]
  if (!soll) return { ok: false, grund: "Review ohne body-sha256" }

  const ist = createHash("sha256").update(body).digest("hex")
  if (ist !== soll) return { ok: false, grund: "seit dem Review geaendert (outdated)" }

  // Ab hier dieselbe Ableitung wie LeiteZustandAb auf der Go-Seite
  // (internal/bau/review.go) — beide Stellen muessen denselben Zustand
  // ausrechnen, sonst registriert das Plugin, was der Generator verbietet.
  const exit = /^\s*(?:#|\/\/)\s*verified-exit:\s*(-?\d+)\s*$/m.exec(block)?.[1]
  if (exit !== undefined && exit !== "0") {
    return { ok: false, grund: `Probelauf gescheitert, exit ${exit} (invalid)` }
  }
  // Ein bestandener Probelauf reicht NICHT: Exit 0 heisst „es lief", nicht
  // „es stimmt". Registriert wird nur, was ein Mensch bei `tool release`
  // fuer richtig befunden hat — `released-by` ist dessen Spur (actual).
  if (!/^\s*(?:#|\/\/)\s*released-by:\s*\S/m.test(block)) {
    return { ok: false, grund: "nicht freigegeben — niemand hat die Ausgabe bestaetigt (hypothetical)" }
  }
  return { ok: true }
}

async function ladeWerkzeuge(bau, $) {
  const dir = join(bau, "tools")
  let dateien = []
  try {
    dateien = readdirSync(dir).filter((f) => f.endsWith(".json")).sort()
  } catch {
    return {} // kein tools/ — der Bau hat schlicht keine Werkzeuge
  }
  if (dateien.length === 0) return {}

  // FAIL-CLOSED, und hier anders als beim Probelauf: dort steht ein
  // Mensch am Terminal und liest eine Warnung, im Betrieb liest sie
  // niemand. Ohne bwrap oder ohne bekannte Grenzen wird deshalb GAR NICHT
  // registriert — ein fehlendes Werkzeug faellt auf, eine fehlende
  // Grenze nicht.
  const hatBwrap = (await $`bwrap --version`.quiet().nothrow()).exitCode === 0
  if (!hatBwrap) {
    console.error(
      `hasenbau: KEIN Schmied-Werkzeug registriert (${dateien.length} vorhanden) — bwrap fehlt.\n` +
        "  Ein Werkzeug laeuft im Server-Prozess; ohne Sandkasten haette es mehr Rechte als der Hase, der es ruft.",
    )
    return {}
  }
  // Das Zeitlimit haengt an `timeout` (coreutils) und wird ANDERS
  // behandelt als der Sandkasten: fehlt es, laeuft das Werkzeug trotzdem,
  // nur ohne Deckel. Es schuetzt gegen Haenger, nicht gegen Absichten —
  // und ein Haenger faellt spaetestens am Zeitlimit des Auftrags auf.
  const hatTimeout = (await $`timeout --version`.quiet().nothrow()).exitCode === 0
  const vorspann = hatTimeout ? ["timeout", WERKZEUG_ZEITLIMIT, "bwrap"] : ["bwrap"]
  if (!hatTimeout) {
    console.error("hasenbau: Werkzeuge laufen ohne Zeitlimit — timeout (coreutils) fehlt.")
  }

  const grenzen = ladeGrenzen(bau)
  if (grenzen === null) {
    console.error(
      `hasenbau: KEIN Schmied-Werkzeug registriert (${dateien.length} vorhanden) — ${GRENZEN_DATEI} fehlt oder ist unlesbar.\n` +
        "  Sie entsteht beim Laden der Auftraege; ohne sie ist unbekannt, welche Raeume ein Werkzeug sehen darf.",
    )
    return {}
  }

  const werkzeuge = {}
  for (const datei of dateien) {
    const name = datei.slice(0, -".json".length)
    try {
      const m = JSON.parse(readFileSync(join(dir, datei), "utf8"))
      if (!m?.description || !m?.script) throw new Error("description oder script fehlt")

      // Vor allem anderen: hat ein Mensch genau diesen Inhalt gelesen?
      const pruefung = reviewPruefung(readFileSync(join(dir, m.script), "utf8"))
      if (!pruefung.ok) {
        console.error(`hasenbau: Werkzeug ${name} NICHT registriert — ${pruefung.grund}`)
        continue
      }

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
        async execute(eingabe, ctx) {
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

          // Wer ruft, entscheidet, was das Werkzeug sehen darf. Ist der
          // Agent unbekannt — ein Subagent, ein handgeschriebener Agent,
          // ein Aufruf ausserhalb eines Auftrags —, gibt es keine
          // Grenze, die man ableiten koennte. Dann wird abgewiesen, und
          // der Text geht an das Modell (er kommt dort an, gemessen
          // 2026-08-12).
          const grenze = grenzen[ctx?.agent]
          if (!grenze) {
            throw new Error(
              `${name} nicht ausgefuehrt: fuer den Agenten ${ctx?.agent ?? "?"} ist keine Raum-Grenze hinterlegt. ` +
                "Ein Werkzeug darf nie mehr als der Hase, der es ruft — ohne bekannte Raeume laeuft es nicht.",
            )
          }
          const befehl = [...vorspann, ...sandkastenArgv(bau, grenze, skript, argv)]
          const r = await $`${befehl[0]} ${befehl.slice(1)}`.cwd(bau).quiet().nothrow()
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
    tool: await ladeWerkzeuge(directory, $),

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
