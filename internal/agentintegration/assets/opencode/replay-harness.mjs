// Behavioral test harness for the bundled OpenCode asset.
//
// Reads a sanitized trace .tsv on argv, drives it through the asset's REAL
// mapping, and prints the ordered argv list each report would have produced as
// JSON. The Go test compares that against what OpenCodeHandler produces from
// the same trace.
//
// This exists because the previous asset<->handler test was a substring check,
// which let the shipped JavaScript and the Go mirror diverge on two behaviors
// that mattered -- an absent `ended` latch and a different session.created
// rule -- and neither showed up until the asset was run against a live
// provider. Comparing recorded output is the only version of this test that
// can actually fail for the right reason.
//
// The spawn boundary is not stubbed so much as not reached: mapEvent and
// buildArgs are pure and are what the runtime path itself calls, so this
// exercises the same code rather than a copy of it.

import { readFileSync } from "node:fs"
import { buildArgs, mapEvent, newState } from "./sidecar-lifecycle.js"

const tracePath = process.argv[2]
if (!tracePath) {
  console.error("usage: replay-harness.mjs <trace.tsv>")
  process.exit(2)
}

// Columns: offset_ms, kind, type, status, tool, session-id present,
// parent-id present, [error class name]. The traces record only whether a
// session id was present, never its value, so a stable synthetic id stands in
// -- which is exactly what the Go replay does, so the two stay comparable.
const SYNTHETIC_SESSION = "s1"

const state = newState()
let seq = 0
const emitted = []

for (const line of readFileSync(tracePath, "utf8").trim().split("\n")) {
  const cols = line.split("\t")
  if (cols.length < 7) {
    console.error(`malformed trace row: ${line}`)
    process.exit(2)
  }
  const ev = {
    type: cols[2],
    status: cols[3] === "-" ? "" : cols[3],
    errorName: cols.length > 7 && cols[7] !== "-" ? cols[7] : "",
    sessionID: cols[5] !== "-" ? SYNTHETIC_SESSION : "",
  }
  for (const action of mapEvent(state, ev)) {
    seq += 1
    emitted.push(buildArgs(action, seq, state.session))
  }
}

process.stdout.write(JSON.stringify(emitted))
