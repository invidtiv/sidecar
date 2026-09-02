// Ordering harness for the bundled OpenCode asset.
//
// The asset's serialization — report N+1 is not spawned until report N's
// process has exited — was the fix for a defect that silently dropped the
// terminal `end` report in two live runs out of three: sequences were assigned
// in order and delivered out of order, and the store correctly rejected the
// loser. Until now that fix was proven only by running against a real provider,
// which means a regression would have reached a user before it reached a test.
//
// This drives the REAL plugin factory — the same `event` and `dispose` handlers
// OpenCode calls — against a stub binary whose processes are rigged to exit in
// the opposite order to the one they were started in. The first report sleeps
// longest and the last sleeps least, so:
//
//   serialized  -> the stub is invoked and exits 1, 2, 3, 4
//   concurrent  -> they all start at once and exit 3, 2, 1, 4
//
// The two outcomes are therefore distinguishable by the recorded order alone,
// with no timing assertion to go flaky on a loaded machine.
//
// It also reports how long dispose took, which pins a second defect this
// harness found: the bounding timer in dispose was never cleared, so a pending
// timer kept Node's event loop alive and held the host process open for the
// full five second budget after every report had already landed.
//
// And it records each report's COMPLETE argv, keyed by its sequence, because
// the order alone says nothing about whether the argv is one the Sidecar CLI
// would accept. That gap is what let a Pi asset ship an argv carrying a --seq
// about 1600x over the store's MaxSequence: it passed every offline test and
// every report was rejected at runtime, silently, because reports spawn with
// stdio "ignore". TestBundledAssetsSpawnArgvTheShippedCLIAccepts in internal/cli
// consumes what this records and pushes it through the real flag parser,
// validator and store.
//
// Usage: ordering-harness.mjs <stub-path> <order-log-path> <argv-dir>

import { writeFileSync, chmodSync, readFileSync, existsSync, mkdirSync } from "node:fs"
import { join } from "node:path"

const [stub, orderLog, argvDir] = process.argv.slice(2)
if (!stub || !orderLog || !argvDir) {
  console.error("usage: ordering-harness.mjs <stub-path> <order-log-path> <argv-dir>")
  process.exit(2)
}
mkdirSync(argvDir, { recursive: true })

// The stub stands in for the Sidecar binary. It writes its complete argv, one
// element per line, to a file named for its sequence, sleeps for a duration
// chosen to invert the completion order, then records that it ran.
writeFileSync(
  stub,
  `#!/bin/sh
seq=""
prev=""
for a in "$@"; do
  if [ "$prev" = "--seq" ]; then seq="$a"; fi
  prev="$a"
done
printf '%s\\n' "$@" > "$SIDECAR_ARGV_DIR/$seq"
case "$seq" in
  1) sleep 0.6 ;;
  2) sleep 0.3 ;;
  3) sleep 0.1 ;;
  *) sleep 0.05 ;;
esac
echo "$seq" >> "$SIDECAR_ORDER_LOG"
`,
  "utf8",
)
chmodSync(stub, 0o755)

process.env.SIDECAR_MANAGED_SHELL = "1"
process.env.SIDECAR_BIN = stub
process.env.SIDECAR_ORDER_LOG = orderLog
process.env.SIDECAR_ARGV_DIR = argvDir

const { SidecarLifecycle } = await import("./sidecar-lifecycle.js")
const plugin = await SidecarLifecycle()
if (!plugin || typeof plugin.event !== "function") {
  console.error("the asset did not return a plugin; it would not load in OpenCode either")
  process.exit(2)
}

// Three reports from the bus, then a fourth from dispose. They are fed with no
// awaiting between them, which is exactly how OpenCode delivers a burst and is
// the condition the race needed.
const bus = [
  { type: "session.created", properties: { info: { id: "s1" } } },
  { type: "session.status", properties: { info: { status: { type: "busy" } } } },
  { type: "session.error", properties: { error: { name: "MessageAbortedError" } } },
]
for (const event of bus) plugin.event({ event })

const started = Date.now()
await plugin.dispose()
const elapsedMs = Date.now() - started

const recorded = existsSync(orderLog)
  ? readFileSync(orderLog, "utf8").trim().split("\n").filter(Boolean)
  : []
const argv = {}
for (const seq of recorded) {
  const path = join(argvDir, seq)
  argv[seq] = existsSync(path) ? readFileSync(path, "utf8").split("\n").filter(Boolean) : []
}
process.stdout.write(JSON.stringify({ order: recorded, argv, elapsedMs }))
