// Re-instantiation harness for the bundled Pi asset.
//
// It pins one property that only shows up across two instantiations, which is
// exactly the case no other harness here creates: Pi can replace this extension
// mid-run without emitting another agent_start, and the reports the replacement
// instance sends have to be as acceptable to Sidecar's store as the first
// instance's were.
//
// This file replaces an earlier sequence-harness.mjs that proved a much weaker
// thing -- that a second instance's first sequence exceeded a first instance's
// last -- because the asset no longer holds a sequence at all. Both counters it
// tried dropped reports. Opening at zero dropped the replacement instance's,
// since the run key survives a reload (lifecycleenv derives RunID from the
// process generation and the session fingerprint, and a reload changes neither)
// and the store rejects anything at or below the run's high-water mark. Seeding
// at `Date.now() * 1000`, which is what upstream does over a socket that bounds
// nothing, dropped EVERY report: Sidecar's store caps the field at
// MaxSequence = 1 << 40 and the seed is about 1600x over it. Both were silent,
// because reports spawn with stdio "ignore" and their exit codes are never read.
//
// So the asset omits --seq and the store assigns under the lock it already
// holds, and what this harness asserts is that the omission holds across a
// reload: two instances, and no argv from either carries a sequence to restart
// or to overflow. The Go test additionally feeds the recorded argv through the
// real report path, which is where "the store accepts it" is proved.
//
// Usage: reinstantiate-harness.mjs <stub-path> <argv-dir>

import { writeFileSync, chmodSync, readFileSync, readdirSync, mkdirSync } from "node:fs"
import { join } from "node:path"

const [stub, argvDir] = process.argv.slice(2)
if (!stub || !argvDir) {
  console.error("usage: reinstantiate-harness.mjs <stub-path> <argv-dir>")
  process.exit(2)
}
mkdirSync(argvDir, { recursive: true })

// The stub records one file per report process, named by its arrival order, with
// the complete argv one element per line. One file per process, so nothing has
// to interleave into a shared file to be read back, and an element containing a
// space cannot be mistaken for two.
writeFileSync(
  stub,
  `#!/bin/sh
n=1
while [ -e "$SIDECAR_ARGV_DIR/$n" ]; do n=$((n+1)); done
printf '%s\\n' "$@" > "$SIDECAR_ARGV_DIR/$n"
`,
  "utf8",
)
chmodSync(stub, 0o755)

process.env.SIDECAR_MANAGED_SHELL = "1"
process.env.SIDECAR_BIN = stub
process.env.SIDECAR_ARGV_DIR = argvDir

const { default: install } = await import("./sidecar-lifecycle.js")
if (typeof install !== "function") {
  console.error("the asset's default export is not a function; Pi would drop the module silently")
  process.exit(2)
}

const ctx = {
  mode: "tui",
  isIdle: () => false,
  sessionManager: {
    getSessionFile: () => "/tmp/pi-reinstantiate.jsonl",
    getSessionId: () => "pi-reinstantiate",
  },
}

// Files are named by arrival order, so sorting numerically gives the order the
// processes were spawned in. The asset's queue is serialized, so that order is
// also the order they completed in.
const recorded = () =>
  readdirSync(argvDir)
    .map(Number)
    .filter((n) => !Number.isNaN(n))
    .sort((a, b) => a - b)
    .map((n) => readFileSync(join(argvDir, String(n)), "utf8").split("\n").filter(Boolean))

// One instance, one session_start with reason "reload". That branch forces a
// report even though the lane did not change -- it is the reload-recovery
// publish -- and it is precisely the report a restarted counter used to drop.
// Each instance therefore emits a binding and a state report: two processes.
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

await drive(2)
const first = recorded()
await drive(4)
const all = recorded()

process.stdout.write(JSON.stringify({ first, second: all.slice(first.length) }))
