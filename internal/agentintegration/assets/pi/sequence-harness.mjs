// Sequence-seeding harness for the bundled Pi asset.
//
// It pins one property that only shows up across two instantiations, which is
// exactly the case no other test here creates: a second instance's first
// sequence must be greater than a first instance's last.
//
// Why that matters is in the asset, beside reportSeq. Pi can replace this
// extension mid-run without emitting another agent_start; the run key survives
// that, because RunID is derived from the process generation and the session
// fingerprint and a reload changes neither; and Sidecar's store rejects any
// sequence at or below the run's high-water mark. A counter that restarted at
// zero would have every report from the replacement instance dropped in silence
// -- reports are spawned with stdio: "ignore" and their exit codes are never
// read -- starting with the forced reload-recovery report itself.
//
// The harness installs the factory twice against two stub Pis, drives one
// session_start through each, and reports the sequence each report was spawned
// with, plus the wall clock reading taken before the module was imported. The Go
// test uses that reading to prove the seeding is from the clock and not merely
// monotonic: a counter left at zero would produce 1 and 2 here, which are
// monotonic and still wrong.
//
// Usage: sequence-harness.mjs <stub-path> <seq-log-path>

import { writeFileSync, chmodSync, readFileSync, existsSync } from "node:fs"

const [stub, seqLog] = process.argv.slice(2)
if (!stub || !seqLog) {
  console.error("usage: sequence-harness.mjs <stub-path> <seq-log-path>")
  process.exit(2)
}

// The stub records the --seq of every state report and nothing else. The session
// binding carries no --seq and is deliberately not recorded: it is not part of
// the sequenced stream.
writeFileSync(
  stub,
  `#!/bin/sh
prev=""
for a in "$@"; do
  if [ "$prev" = "--seq" ]; then echo "$a" >> "$SIDECAR_SEQ_LOG"; fi
  prev="$a"
done
`,
  "utf8",
)
chmodSync(stub, 0o755)

process.env.SIDECAR_MANAGED_SHELL = "1"
process.env.SIDECAR_BIN = stub
process.env.SIDECAR_SEQ_LOG = seqLog

// Taken before the import, because the module reads the clock at import time and
// the assertion is that the first sequence is at or above this floor.
const startedAtMs = Date.now()

const { default: install } = await import("./sidecar-lifecycle.js")
if (typeof install !== "function") {
  console.error("the asset's default export is not a function; Pi would drop the module silently")
  process.exit(2)
}

const ctx = {
  mode: "tui",
  isIdle: () => false,
  sessionManager: {
    getSessionFile: () => "/tmp/pi-seq.jsonl",
    getSessionId: () => "pi-seq",
  },
}

const recorded = () =>
  existsSync(seqLog) ? readFileSync(seqLog, "utf8").trim().split("\n").filter(Boolean) : []

// One instance, one turn. session_start forces a report even though the lane did
// not change, which is the reload-recovery publish, and it is the report a
// restarted counter would have dropped.
const drive = async (want) => {
  const handlers = new Map()
  install({
    on(event, handler) {
      handlers.set(event, handler)
    },
    events: { on: () => () => {} },
  })
  handlers.get("session_start")({ reason: "reload" }, ctx)
  const deadline = Date.now() + 30_000
  while (Date.now() < deadline && recorded().length < want) {
    await new Promise((resolve) => setTimeout(resolve, 25))
  }
}

await drive(1)
await drive(2)

process.stdout.write(JSON.stringify({ startedAtMs, seqs: recorded().map(Number) }))
