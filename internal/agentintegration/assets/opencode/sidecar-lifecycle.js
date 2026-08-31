// Sidecar lifecycle integration for OpenCode.
//
// Translates OpenCode's own plugin events into `sidecar agent report`
// invocations so Sidecar knows what an agent is doing without reading its
// screen. It reports lifecycle facts only: it never notifies, plays a sound,
// emits a terminal escape, or chooses delivery policy.
//
// WHAT THIS SENDS
//
// Lanes, a terminal outcome, a bounded reason code, a monotonically increasing
// sequence, and -- when OpenCode supplies one -- the session id, of which
// Sidecar retains only a host-salted digest. It never sends prompt text,
// response text, tool arguments or results, file paths, environment values, or
// credentials. There is no code path here that reads message content.
//
// STRUCTURE
//
// The event->report mapping is a pure function (`mapEvent`) over an explicit
// state object, exported so it can be driven directly by tests. The runtime
// half -- spawning, ordering, timeouts -- is separate and touches no mapping
// logic. This split exists because the shipped asset and Sidecar's Go mirror of
// the same state machine drifted once already, and a substring check did not
// catch it; `TestBundledAssetBehavesLikeTheHandler` now runs THIS function
// against the recorded traces and requires the same ordered reports the Go
// handler produces.
//
// WHY session.status IS THE PRIMARY SIGNAL
//
// session.status is state-shaped: every emission re-asserts ground truth as
// busy or idle, so a dropped or reordered event is corrected by the next one
// rather than leaving a pane stuck.
//
// It does NOT extend to the blocked lane. permission.asked/permission.replied
// are transition-shaped, so a dropped permission.asked will not self-correct.
// The mapping compensates by treating any later busy or idle assertion as
// clearing the blocked lane, which bounds how long a missed unblock can last.
//
// CANCELLATION, AND WHY THE LATCH EXISTS
//
// OpenCode has no dedicated cancel event. A user interrupt arrives as
// session.error carrying error.name === "MessageAbortedError", followed by a
// provider failure's identical shape with a different name -- so the name is
// the only discriminator and is read explicitly.
//
// The recorded traces show session.error is IMMEDIATELY followed by
// session.status idle. Without a latch that trailing idle supersedes the end
// report, and under full authority a cancelled or failed turn is announced to
// the user as a clean completion. `ended` latches the run closed so nothing
// after a terminal outcome can reopen it.
//
// ORDERING
//
// Reports are serialized. Each one is a subprocess that takes an exclusive lock
// on Sidecar's append-only store, and the store enforces a strictly increasing
// sequence per run. Spawning them concurrently means sequences are assigned in
// order but delivered out of order, and the store correctly rejects the loser --
// which was observed silently dropping the terminal `end` report in two runs
// out of three. So report N+1 is not spawned until report N's process has
// exited. The queue stays fail-open: a failed or timed-out report is swallowed
// and never blocks the next event.
//
// FAILING OPEN
//
// Outside a Sidecar-managed shell nothing is spawned at all. Nothing in this
// file may delay, block, or change what OpenCode does.

import { spawn } from "node:child_process"

const SOURCE = "sidecar.opencode.plugin"
const PROVIDER = "opencode"
const ABORTED_ERROR = "MessageAbortedError"

// VERSION is the bundled asset version, carried on every report because
// authority is granted to a source at a version, never to a source forever.
const VERSION = "1"

// REPORT_TIMEOUT_MS bounds one report subprocess. A hung `sidecar agent report`
// -- a wedged lock, a stalled filesystem -- must not stall the queue behind it
// for the rest of the session, so it is killed and the queue moves on.
const REPORT_TIMEOUT_MS = 5000

// newState returns the mapping's initial state.
function newState() {
  return { lane: "", blocked: false, ended: false, session: "" }
}

// mapEvent is the pure event->reports mapping.
//
// It mutates `st` and returns zero or more actions, each
// {kind, state?, outcome?, reason}. It performs no I/O and knows nothing about
// subprocesses, which is what makes it directly comparable with the Go handler.
function mapEvent(st, ev) {
  const type = ev.type
  const lane = (state, reason) => {
    // Suppress an exact repeat of the lane already reported. OpenCode emits
    // session.status busy several times per turn, and each repeat would
    // otherwise be a process spawn and a consumed sequence number that told
    // Sidecar nothing new.
    if (st.lane === state) return []
    st.lane = state
    return [{ kind: "report", state, reason }]
  }

  switch (type) {
    case "session.created": {
      // Fires on a genuinely new session id, which covers both the first
      // session and a rotation mid-process. A rotation is a new run, so the
      // baseline lane is re-established.
      const id = ev.sessionID || ""
      if (id === st.session) return []
      st.session = id
      st.ended = false
      st.blocked = false
      st.lane = "idle"
      return [{ kind: "report", state: "idle", reason: "session_start" }]
    }

    case "session.status": {
      if (st.ended) return []
      const status = ev.status || ""
      if (status.includes('"busy"')) {
        // A positive busy assertion clears the blocked lane. This is the
        // deliberate compensation for the blocked lane being transition-shaped:
        // a dropped permission.replied would otherwise strand the pane on
        // blocked for the rest of the run.
        st.blocked = false
        return lane("working", "turn_start")
      }
      if (status.includes('"idle"')) {
        st.blocked = false
        return lane("idle", "turn_complete")
      }
      return []
    }

    case "permission.asked": {
      if (st.ended) return []
      st.blocked = true
      return lane("blocked", "permission_request")
    }

    case "permission.replied": {
      if (st.ended || !st.blocked) return []
      st.blocked = false
      return lane("working", "permission_resolved")
    }

    case "session.error": {
      if (st.ended) return []
      st.ended = true
      st.lane = ""
      const name = ev.errorName || ""
      if (name === ABORTED_ERROR) {
        return [{ kind: "end", outcome: "cancelled", reason: "cancelled" }]
      }
      return [{ kind: "end", outcome: "failed", reason: "provider_error" }]
    }

    case "dispose": {
      // The provider is going away. Releasing rather than asserting a lane is
      // correct: the run is over, and Sidecar must return to ordinary process
      // and screen observation immediately instead of holding a remembered
      // state that nothing will ever update again.
      return [{ kind: "release", reason: "process_exit" }]
    }

    default:
      return []
  }
}

// buildArgs turns one action into the exact argv for the Sidecar CLI.
//
// Direct argv, never a shell string: nothing from OpenCode is interpolated into
// a command line, and every value is either a bounded enum or an identifier
// Sidecar re-validates on the way in.
function buildArgs(action, seq, sessionID) {
  const args = [
    "agent", action.kind,
    "--source", SOURCE,
    "--source-version", VERSION,
    "--provider", PROVIDER,
    "--seq", String(seq),
  ]
  if (sessionID) args.push("--session-id", sessionID)
  if (action.kind === "report") args.push("--state", action.state)
  if (action.kind === "end") args.push("--outcome", action.outcome)
  args.push("--reason", action.reason)
  return args
}

// normalizeEvent flattens an OpenCode bus event into the shape mapEvent reads.
// Only discriminators and identifiers are extracted; no message content is ever
// touched.
function normalizeEvent(event) {
  const props = (event && event.properties) || {}
  const status = props.status || (props.info && props.info.status)
  const error = props.error || (props.info && props.info.error)
  return {
    type: (event && event.type) || "",
    status: status && status.type ? JSON.stringify({ type: status.type }) : "",
    errorName: (error && error.name) || "",
    sessionID: (props.info && props.info.id) || props.sessionID || "",
  }
}

const SidecarLifecycle = async () => {
  const bin = process.env.SIDECAR_BIN
  if (process.env.SIDECAR_MANAGED_SHELL !== "1" || !bin) return {}

  const st = newState()
  let seq = 0

  // runOnce resolves when the report process exits, or when the timeout fires,
  // whichever comes first. It never rejects: a reporting failure is diagnostic
  // and must never surface to the agent.
  const runOnce = (args) =>
    new Promise((resolve) => {
      let done = false
      const finish = () => {
        if (done) return
        done = true
        clearTimeout(timer)
        resolve()
      }
      let child
      const timer = setTimeout(() => {
        try {
          child && child.kill("SIGKILL")
        } catch (e) {
          /* ignore */
        }
        finish()
      }, REPORT_TIMEOUT_MS)
      try {
        child = spawn(bin, args, { stdio: "ignore" })
        child.on("error", finish)
        child.on("exit", finish)
      } catch (e) {
        finish()
      }
    })

  // The serialization point. Sequences are assigned here, in the same order the
  // queue will deliver them, so the store's strictly-increasing contract is
  // satisfied by construction rather than by luck.
  let queue = Promise.resolve()
  const enqueue = (action) => {
    seq += 1
    const args = buildArgs(action, seq, st.session)
    queue = queue.then(() => runOnce(args)).catch(() => {})
    return queue
  }

  const handle = (event) => {
    for (const action of mapEvent(st, normalizeEvent(event))) enqueue(action)
  }

  return {
    event: async ({ event }) => {
      handle(event)
    },

    dispose: async () => {
      for (const action of mapEvent(st, { type: "dispose" })) enqueue(action)
      // Wait for the queue to drain so the release actually lands before the
      // process goes away, but never wait longer than one report's budget: a
      // slow Sidecar must not hold OpenCode's shutdown open.
      await Promise.race([queue, new Promise((r) => setTimeout(r, REPORT_TIMEOUT_MS))])
    },
  }
}

// EXPORT SURFACE -- measured, not assumed.
//
// OpenCode's plugin loader requires EVERY export of a plugin module to be a
// plugin factory. A single non-function export disqualifies the whole module,
// silently: it is imported, and then never called. This was measured against
// 1.18.25 with four probe plugins -- a lone function export loads, a function
// plus a string export does not, a function plus an object export does not, and
// a function carrying its helpers as properties loads.
//
// That is why the pure mapping hangs off the function rather than being
// exported beside it, and why nothing here is `export const`. The failure mode
// is the worst kind: every test passes, the plugin installs cleanly, and it
// reports nothing at all in production.
SidecarLifecycle.internals = {
  newState,
  mapEvent,
  buildArgs,
  normalizeEvent,
  SOURCE,
  PROVIDER,
  VERSION,
  ABORTED_ERROR,
  REPORT_TIMEOUT_MS,
}

export { SidecarLifecycle }
