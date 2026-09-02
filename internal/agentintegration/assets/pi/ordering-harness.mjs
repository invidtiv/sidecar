// Runtime harness for the bundled Pi asset.
//
// Everything pinned here is invisible to a test that drives the pure mapping,
// and the whole runtime half of the asset sits in that blind spot: the replay
// harness calls mapEvent and buildArgs directly, so it never touches readCtx,
// never touches the subscriptions, and would pass unchanged if `pi.on`
// registered "agent_started", if readCtx read getSessionId where it means
// getSessionFile, or if it stopped reading isIdle at all. Each of those is a
// silent failure in production -- the extension installs, loads, and reports
// the wrong thing or nothing. So this harness installs the real factory against
// a stub Pi, drives real events through it, and reports three things:
//
//   - the event names the asset actually subscribed to, on both registries;
//   - the exact argv every report process was spawned with;
//   - the order those processes completed in.
//
// ORDERING
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
//   serialized  -> session, working-session_change, blocked-..., working-...
//   concurrent  -> that list reversed
//
// The two outcomes are distinguishable by the recorded order alone, with no
// timing assertion to go flaky on a loaded machine.
//
// The stub labels itself from the REPORT'S OWN CONTENT -- the verb, then the
// state and reason -- rather than from its sequence number. That is not
// cosmetic. The asset seeds its sequence counter from the clock (see reportSeq),
// so sequence values are large and unpredictable and cannot key a sleep or name
// a file; content can, and labelling by content also makes the recorded order
// assert the mapping instead of only the ordering.
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

// The stub stands in for the Sidecar binary. It labels itself from its own argv
// -- "session" for the binding verb, "<state>-<reason>" for a state report --
// writes its complete argv one element per line to a file named for that label,
// sleeps for a duration chosen to invert the completion order, then records that
// it ran. One file per label, so nothing has to interleave into a shared file to
// be read back.
writeFileSync(
  stub,
  `#!/bin/sh
label=""
state=""
reason=""
prev=""
for a in "$@"; do
  if [ "$a" = "report-session" ]; then label="session"; fi
  case "$prev" in
    --state) state="$a" ;;
    --reason) reason="$a" ;;
  esac
  prev="$a"
done
if [ -z "$label" ]; then label="$state-$reason"; fi
printf '%s\\n' "$@" > "$SIDECAR_ARGV_DIR/$label"
case "$label" in
  session) sleep 0.6 ;;
  working-session_change) sleep 0.45 ;;
  blocked-permission_request) sleep 0.3 ;;
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
process.env.SIDECAR_ARGV_DIR = argvDir

const { default: install } = await import("./sidecar-lifecycle.js")
if (typeof install !== "function") {
  console.error("the asset's default export is not a function; Pi would drop the module silently")
  process.exit(2)
}

// The same shape upstream's own test harness builds: Pi's typed listener
// registry plus the untyped string-keyed event bus. Both record which names were
// registered, because a subscription to a name Pi never emits is a whole
// extension that does nothing and says nothing.
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

// The ctx is deliberately the shape Pi hands a listener, with the two session
// accessors returning DIFFERENT values and only one of them a path. A readCtx
// that swapped getSessionFile for getSessionId would then bind by id instead of
// by path, and the recorded argv says which one happened.
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

const order = recorded()
const argv = {}
for (const label of order) {
  const path = join(argvDir, label)
  argv[label] = existsSync(path) ? readFileSync(path, "utf8").split("\n").filter(Boolean) : []
}

process.stdout.write(
  JSON.stringify({
    order,
    argv,
    events: [...handlers.keys()],
    busEvents: [...eventHandlers.keys()],
    elapsedMs: Date.now() - started,
  }),
)
