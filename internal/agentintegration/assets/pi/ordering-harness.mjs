// Ordering harness for the bundled Pi asset.
//
// Two properties are pinned here, and neither can be seen by a test that only
// drives the pure mapping.
//
// The session binding lands before the first state report. Upstream expresses
// that as `await reportSession(...)` inside session_start and has a fixture that
// holds the socket acknowledgement back to prove no state report is sent while
// the binding is outstanding. Sidecar's transport is a subprocess rather than a
// socket, so the same property is "the `agent report-session` process has exited
// before `agent report` is spawned", and it comes from the queue rather than
// from an await.
//
// Reports are serialized. Each one is a subprocess taking an exclusive lock on
// an append-only store that enforces a strictly increasing sequence per run, so
// spawning them concurrently assigns sequences in order and delivers them out of
// order -- and the store correctly rejects the loser. That defect cost OpenCode's
// exit gate two attempts and silently dropped its terminal report in two live
// runs out of three, which is why it is pinned here before Pi ever runs live.
//
// The stub binary is rigged to exit in the opposite order to the one its
// processes are started in: the binding sleeps longest and the last report
// sleeps least, so
//
//   serialized  -> session, 1, 2, 3
//   concurrent  -> 3, 2, 1, session
//
// The two outcomes are distinguishable by the recorded order alone, with no
// timing assertion to go flaky on a loaded machine.
//
// Usage: ordering-harness.mjs <stub-path> <order-log-path>

import { writeFileSync, chmodSync, readFileSync, existsSync } from "node:fs"

const [stub, orderLog] = process.argv.slice(2)
if (!stub || !orderLog) {
  console.error("usage: ordering-harness.mjs <stub-path> <order-log-path>")
  process.exit(2)
}

// The stub stands in for the Sidecar binary. It labels itself from its own argv
// -- "session" for the binding verb, the sequence number for a state report --
// sleeps for a duration chosen to invert the completion order, then records that
// it ran.
writeFileSync(
  stub,
  `#!/bin/sh
label=""
seq=""
prev=""
for a in "$@"; do
  if [ "$a" = "report-session" ]; then label="session"; fi
  if [ "$prev" = "--seq" ]; then seq="$a"; fi
  prev="$a"
done
if [ -z "$label" ]; then label="$seq"; fi
case "$label" in
  session) sleep 0.6 ;;
  1) sleep 0.45 ;;
  2) sleep 0.3 ;;
  *) sleep 0.1 ;;
esac
echo "$label" >> "$SIDECAR_ORDER_LOG"
`,
  "utf8",
)
chmodSync(stub, 0o755)

process.env.SIDECAR_MANAGED_SHELL = "1"
process.env.SIDECAR_BIN = stub
process.env.SIDECAR_ORDER_LOG = orderLog

const { default: install } = await import("./sidecar-lifecycle.js")
if (typeof install !== "function") {
  console.error("the asset's default export is not a function; Pi would drop the module silently")
  process.exit(2)
}

// The same shape upstream's own test harness builds: Pi's typed listener
// registry plus the untyped string-keyed event bus.
const handlers = new Map()
const eventHandlers = new Map()
const pi = {
  on(event, handler) {
    handlers.set(event, handler)
  },
  events: {
    on(event, handler) {
      eventHandlers.set(event, handler)
      return () => {}
    },
  },
}

install(pi)

const ctx = {
  hasUI: true,
  mode: "tui",
  isIdle: () => false,
  sessionManager: {
    getSessionFile: () => "/tmp/pi-order.jsonl",
    getSessionId: () => "pi-order",
  },
}

// Delivered with no awaiting between them, which is exactly how a burst arrives
// and is the condition the race needed. session_start emits the binding and a
// forced working report; the two block events add one report each.
handlers.get("session_start")({ reason: "new" }, ctx)
eventHandlers.get("sidecar:blocked")({ active: true, label: "approval" }, ctx)
eventHandlers.get("sidecar:blocked")({ active: false }, ctx)

const recorded = () =>
  existsSync(orderLog) ? readFileSync(orderLog, "utf8").trim().split("\n").filter(Boolean) : []

const started = Date.now()
const deadline = started + 30_000
while (Date.now() < deadline && recorded().length < 4) {
  await new Promise((resolve) => setTimeout(resolve, 25))
}

process.stdout.write(JSON.stringify({ order: recorded(), elapsedMs: Date.now() - started }))
