// Behavioral test harness for the bundled Pi asset.
//
// Reads a fixture .tsv on argv, drives it through the asset's REAL mapping, and
// prints the ordered argv list each report would have produced as JSON. The Go
// test compares that against what PiHandler produces from the same fixture.
//
// This is the mechanism that has caught real drift between OpenCode's shipped
// JavaScript and its Go mirror -- twice, in behaviors that only appeared against
// a live provider -- and it is why the Pi asset ships as .js: `node` cannot
// import a .ts module, and there is no version of this test that does not run
// the asset itself.
//
// The spawn boundary is not stubbed so much as not reached: mapEvent and
// buildArgs are pure and are what the runtime path itself calls, so this
// exercises the same code rather than a copy of it.

import { readFileSync } from "node:fs"
import SidecarLifecycle from "./sidecar-lifecycle.js"

// The mapping hangs off the factory because Pi drops a module whose default
// export is not a function. See the export-surface note in the asset.
const { buildArgs, carriesSequence, mapEvent, newState } = SidecarLifecycle.internals

const fixturePath = process.argv[2]
if (!fixturePath) {
  console.error("usage: replay-harness.mjs <fixture.tsv>")
  process.exit(2)
}

// Columns: offset_ms, event, reason, mode, idle, session_path, session_id,
// blocked_active, blocked_label. "-" means the field was absent, which for
// `idle` is the tri-state both of the asset's guards depend on: an absent
// isIdle() is unknown, and unknown neither starts nor completes a turn.
const FIELDS = 9

const tri = (value) => (value === "-" ? undefined : value === "true")
const text = (value) => (value === "-" ? undefined : value)

const st = newState()
let seq = 0
const emitted = []

for (const line of readFileSync(fixturePath, "utf8").trim().split("\n")) {
  if (line.startsWith("#") || line.trim() === "") continue
  const cols = line.split("\t")
  if (cols.length !== FIELDS) {
    console.error(`malformed fixture row (${cols.length} columns, want ${FIELDS}): ${line}`)
    process.exit(2)
  }
  const ev = {
    type: cols[1],
    reason: text(cols[2]),
    mode: text(cols[3]),
    idle: tri(cols[4]),
    sessionPath: text(cols[5]),
    sessionId: text(cols[6]),
    blockedActive: tri(cols[7]) === true,
    blockedLabel: text(cols[8]),
  }
  for (const action of mapEvent(st, ev)) {
    if (carriesSequence(action)) seq += 1
    emitted.push(buildArgs(action, seq, st.sessionId))
  }
}

process.stdout.write(JSON.stringify(emitted))
