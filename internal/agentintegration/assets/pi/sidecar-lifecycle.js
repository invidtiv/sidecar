// sidecar-integration: id=sidecar.pi.extension schema=1 version=1
//
// The line above is what makes this file Sidecar's. The installer identifies an
// asset it may replace or remove by that marker and by nothing else -- not by
// its name, and not by where it sits. A file called sidecar-lifecycle.js
// without the marker is somebody else's, and Sidecar refuses to touch it.
//
// Sidecar lifecycle integration for Pi.
//
// WHY THIS IS .js AND NOT .ts
//
// Herdr's upstream asset is TypeScript, and Pi loads it happily: its extension
// loader accepts a bare `.ts` or `.js` file in an extension directory
// (isExtensionFile, dist/core/extensions/loader.js:528-530) and imports both
// through jiti, so either extension is ESM by the time Pi sees it.
//
// `node` is not jiti. It cannot import a `.ts` module at all, and the harness
// pattern that keeps this file and its Go mirror from drifting -- run the
// asset's own pure mapping under node, replay a fixture through it, compare the
// argv element for element -- requires exactly that. Shipping .ts would mean
// vendoring a TypeScript-capable runner into the test path to buy nothing Pi
// can tell apart. So this is .js on purpose. Do not "fix" it back.
//
// WHAT THIS SENDS
//
// Lanes, a bounded reason code, a monotonically increasing sequence, and -- when
// Pi supplies one -- the conversation this pane is on, as the session file's
// path or Pi's own session id. It never sends prompt text, response text, tool
// arguments or results, file contents, or environment values. There is no code
// path here that reads message content.
//
// STRUCTURE
//
// The event->report mapping is a pure function (`mapEvent`) over an explicit
// state object, exported so it can be driven directly by tests, with the argv
// builder (`buildArgs`) beside it. The runtime half -- spawning, ordering,
// timeouts -- is separate and touches no mapping logic. This split is what makes
// `TestBundledPiAssetBehavesLikeTheHandler` possible: it runs THIS function
// against the checked-in fixtures and requires the same ordered reports the Go
// handler produces. The OpenCode pair drifted once before that test existed, in
// two behaviors that only showed up against a live provider.
//
// PROVENANCE
//
// The provider half -- which Pi event means which lane, and every guard around
// it -- is ported from Herdr's pi integration at HERDR_INTEGRATION_VERSION=8
// (internal/agentintegration/upstream/pi/herdr-agent-state.ts) and is kept
// verbatim in behavior. The transport half is Sidecar's own. See
// internal/agentintegration/portedfrom.go for the recorded provenance, and the
// notes below for the four places this deliberately differs.

import { spawn } from "node:child_process"

const SOURCE = "sidecar.pi.extension"
const PROVIDER = "pi"

// VERSION is the bundled asset version, carried on every state report because
// authority is granted to a source at a version, never to a source forever.
const VERSION = "1"

// REPORT_TIMEOUT_MS bounds one report subprocess. A hung `sidecar agent report`
// -- a wedged lock, a stalled filesystem -- must not stall the queue behind it
// for the rest of the session, so it is killed and the queue moves on.
const REPORT_TIMEOUT_MS = 5000

// BLOCKED_CHANNEL is the untyped event-bus channel a cooperating extension
// would publish a block on.
//
// Upstream listens on "herdr:blocked". Sidecar listens on its own namespace
// instead, and that is the one deliberate rename in the provider half. Herdr's
// channel is Herdr's protocol: if Sidecar consumed it, a machine with both
// projects installed would have one project's approval protocol driving the
// other project's lane, which is precisely the identity collision the parity
// plan's first decision refuses ("claiming to be Herdr ... buys one agent today
// and a real identity collision whenever Sidecar and Herdr are nested").
//
// NOTHING EMITS EITHER CHANNEL, and the branch below is therefore unreachable
// against every released Pi. The evidence, so this reads as deliberate rather
// than aspirational: the string "herdr" does not appear anywhere in Pi 0.84.3's
// shipped bundle; Pi's ExtensionAPI.on enumerates thirty events and not one of
// them is a permission, approval, or prompt event (types.d.ts:892-928); and
// Herdr's own four occurrences of "herdr:blocked" are two listeners and two test
// drivers, with no publisher anywhere in its tree. Pi ships no permission system
// at all, so there is nothing to be blocked on. The ladder keeps its blocked
// branch anyway because it costs one comparison, upstream's own fixture drives
// it directly, and an extension-to-extension protocol is the only shape a Pi
// block could ever have. The capability entry records `blocked_on_request` and
// `unblocked` as structurally unreachable rather than merely untraced.
const BLOCKED_CHANNEL = "sidecar:blocked"

// newState returns the mapping's initial state.
//
// The field names are upstream's, because the ladder they feed is upstream's.
// `lastState`/`lastMessage` exist only to suppress an exact repeat, and
// `blockedMessage` is carried purely so that suppression behaves identically --
// it is deliberately never transmitted. See buildArgs.
function newState() {
  return {
    rootSession: false,
    agentActive: false,
    blockedCount: 0,
    blockedMessage: undefined,
    lastState: undefined,
    lastMessage: undefined,
    sessionPath: undefined,
    sessionId: undefined,
  }
}

// isAbsoluteSessionPath reports whether a session file path may be reported.
//
// This is the one genuine upstream bug the port fixes. Herdr's Pi asset accepts
// a session path only when it `startsWith("/")` (upstream asset lines 76-78),
// which silently discards every Windows path and leaves those sessions bound by
// id or not at all. Herdr's OMP variant of the same asset fixed exactly this and
// has a test for it (upstream/herdr-agent-state.test.ts:232-241); the Pi variant
// never received the fix. Sidecar takes the fixed form.
//
// Sidecar's own `agent report-session --path` re-validates and additionally
// requires the path to sit inside one of the provider's approved store roots, so
// this is a filter on what is worth sending, not a security boundary.
function isAbsoluteSessionPath(value) {
  if (typeof value !== "string" || value.length === 0) return false
  if (value.startsWith("/")) return true
  // C:\... and C:/..., the two shapes a Windows absolute path arrives in.
  return /^[A-Za-z]:[\\/]/.test(value)
}

// mapEvent is the pure event->actions mapping.
//
// It mutates `st` and returns zero or more actions, each {kind, state?, reason?,
// sessionPath?, sessionId?}. It performs no I/O and knows nothing about
// subprocesses, which is what makes it directly comparable with the Go handler.
//
// The event shape is a flattening of what Pi hands a listener: the event's own
// `reason` and the parts of `ctx` the mapping reads. Nothing else from ctx is
// touched.
function mapEvent(st, ev) {
  const actions = []

  // desiredState is upstream's ladder, unchanged: blocked outranks working,
  // working outranks idle. Its ordering is load-bearing -- an agent_settled
  // arriving while a block is outstanding must not report idle -- and upstream's
  // blocked-precedence fixture drives exactly that.
  const desiredState = () => {
    if (st.blockedCount > 0) return { state: "blocked", message: st.blockedMessage }
    if (st.agentActive) return { state: "working", message: undefined }
    return { state: "idle", message: undefined }
  }

  // publishState suppresses an exact repeat unless forced. `force` exists for
  // one caller: session_start re-asserts the lane even when it has not changed,
  // because a reload replaces this extension mid-run and Sidecar has no record
  // of what the previous instance reported.
  const publishState = (reason, force) => {
    const next = desiredState()
    if (!force && next.state === st.lastState && next.message === st.lastMessage) return
    st.lastState = next.state
    st.lastMessage = next.message
    actions.push({ kind: "report", state: next.state, reason })
  }

  // updateSessionRef reads the conversation reference out of ctx, defensively:
  // an extension host that throws from a getter must not take the lane reporting
  // down with it.
  const updateSessionRef = () => {
    const file = ev.sessionPath
    st.sessionPath = isAbsoluteSessionPath(file) ? file : undefined
    const id = ev.sessionId
    st.sessionId = typeof id === "string" && id.length > 0 ? id : undefined
  }

  // reportSession emits the binding action, or nothing when Pi has told us
  // nothing to bind. Path is preferred over id because a path identifies the
  // exact transcript a restore would resume, which an id alone does not.
  const reportSession = () => {
    if (st.sessionPath) {
      actions.push({ kind: "session", sessionPath: st.sessionPath })
      return
    }
    if (st.sessionId) {
      actions.push({ kind: "session", sessionId: st.sessionId })
    }
  }

  switch (ev.type) {
    case "session_start": {
      // TUI ONLY, and the gate is on `mode` rather than `hasUI` for a reason
      // that is invisible from the field names: RPC sessions report
      // hasUI === true (types.d.ts:213-214) while being headless, so hasUI
      // would claim a pane that has no agent on screen. `mode` is the reliable
      // discriminator and upstream has a fixture for exactly this.
      if (ev.mode !== "tui") return actions
      st.rootSession = true
      updateSessionRef()
      // The session binding is emitted BEFORE the first state report and the
      // queue is serialized, so the binding process has exited before the state
      // report is spawned. That is the translation of upstream's `await
      // reportSession(...)`: upstream awaits the socket acknowledgement, Sidecar
      // waits for the subprocess. Upstream's ordering fixture pins it either
      // way, because what it actually asserts is that no state report is sent
      // while the session report is outstanding.
      reportSession()
      // A reload can replace this extension mid-run without emitting another
      // agent_start, so the run's true state is read back from ctx rather than
      // assumed idle. `=== false` and not `!== true`: an absent isIdle means
      // "unknown", and unknown must not be reported as working.
      st.agentActive = ev.idle === false
      publishState(sessionStartReason(ev.reason), true)
      return actions
    }

    case "agent_start": {
      if (!st.rootSession) return actions
      updateSessionRef()
      // Upstream re-asserts the binding on every turn rather than only on
      // session_start, and does it fire-and-forget (`void reportSession()`)
      // while session_start awaits its own. Sidecar's single serialized queue
      // collapses that distinction: both are enqueued, both are delivered in
      // order. The re-assertion itself is kept, because it is what recovers a
      // binding Sidecar lost to a restart mid-session.
      reportSession()
      st.agentActive = true
      publishState("turn_start", false)
      return actions
    }

    case "agent_settled": {
      // TURN COMPLETION IS agent_settled, NEVER agent_end, and there is
      // deliberately no agent_end subscription in this file. agent_end means
      // "this attempt stopped": Pi can follow it with an automatic retry or a
      // compaction (_willRetryAfterAgentEnd, dist/core/agent-session.js:369), so
      // an adapter that reported idle on it would announce a finished turn in
      // the middle of one. agent_settled is documented as firing only once no
      // automatic retry, compaction, or queued continuation will run.
      //
      // The isIdle guard is the second half of the same care. `_emitAgentSettled`
      // clears `_isAgentRunActive` BEFORE it emits (agent-session.js:330-334), so
      // a genuine settlement always observes isIdle() === true; a settlement seen
      // while the run is still active is stale and is discarded. `!== true` and
      // not `=== false`: an absent isIdle is unknown, and unknown must not close
      // a turn.
      if (!st.rootSession || ev.idle !== true) return actions
      st.agentActive = false
      publishState("turn_complete", false)
      return actions
    }

    case "blocked": {
      // Unreachable against every released Pi. See BLOCKED_CHANNEL.
      if (!st.rootSession) return actions
      if (!ev.blockedActive) {
        st.blockedCount = Math.max(0, st.blockedCount - 1)
        if (st.blockedCount === 0) st.blockedMessage = undefined
        publishState("permission_resolved", false)
        return actions
      }
      st.blockedCount += 1
      st.blockedMessage = ev.blockedLabel
      publishState("permission_request", false)
      return actions
    }

    default:
      return actions
  }
}

// sessionStartReason maps Pi's session_start reason onto Sidecar's frozen reason
// vocabulary.
//
// Upstream carries the raw reason as a `session_start_source` field on its own
// session report. Sidecar's `agent report-session` has no such field and its
// reason codes are a frozen allowlist, so the fact lands on the forced state
// report instead: "startup" is a process that has just begun, and Pi's four
// other reasons -- new, reload, resume and fork -- are all a live process
// swapping which conversation it is on, which is what session_change means.
// An absent or unrecognised reason takes the conservative reading.
function sessionStartReason(reason) {
  switch (reason) {
    case "new":
    case "reload":
    case "resume":
    case "fork":
      return "session_change"
    default:
      return "session_start"
  }
}

// buildArgs turns one action into the exact argv for the Sidecar CLI.
//
// Direct argv, never a shell string: nothing from Pi is interpolated into a
// command line, and every value is either a bounded enum or an identifier
// Sidecar re-validates on the way in.
//
// A state report carries a sequence; a session binding does not, because
// `agent report-session` has no --seq flag -- it is a binding rather than a
// point in an ordered stream. The queue therefore consumes a sequence number
// only for the verbs that carry one, which keeps the state stream's sequence
// gapless.
//
// The blocked label is deliberately absent from every argv. It is unbounded text
// authored by another extension, `--detail` would put it into Sidecar's store,
// and this file's rule is that nothing but lanes, bounded codes and conversation
// identifiers goes over the wire. It is still carried in the mapping's state,
// because upstream compares it when suppressing a repeat and the port keeps that
// behavior byte for byte.
function buildArgs(action, seq, sessionId) {
  if (action.kind === "session") {
    const args = ["agent", "report-session", "--kind", PROVIDER, "--source", SOURCE]
    if (action.sessionPath) args.push("--path", action.sessionPath)
    else if (action.sessionId) args.push("--id", action.sessionId)
    return args
  }
  const args = [
    "agent", "report",
    "--source", SOURCE,
    "--source-version", VERSION,
    "--provider", PROVIDER,
    "--seq", String(seq),
  ]
  if (sessionId) args.push("--session-id", sessionId)
  args.push("--state", action.state)
  args.push("--reason", action.reason)
  return args
}

// carriesSequence reports whether an action's verb takes --seq.
function carriesSequence(action) {
  return action.kind !== "session"
}

// SidecarLifecycle is the Pi extension factory.
//
// Pi requires a module's DEFAULT export to be a function `(pi) => void |
// Promise<void>` and drops the module otherwise -- silently, with no error
// anywhere (loader.js:428-432). That is the same trap OpenCode has, and it is
// why this returns nothing, why every export of this module is a function, and
// why the pure mapping hangs off the factory as a property rather than being
// exported beside it.
export default function SidecarLifecycle(pi) {
  // FAILING OPEN. Outside a Sidecar-managed shell nothing is spawned at all and
  // no handler is even registered. Nothing in this file may delay, block, or
  // change what Pi does.
  const bin = process.env.SIDECAR_BIN
  if (process.env.SIDECAR_MANAGED_SHELL !== "1" || !bin) return

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

  // The serialization point, and the second deliberate difference from upstream.
  //
  // Upstream's state queue is a SINGLE SLOT that coalesces: a state enqueued
  // while another is in flight replaces whatever was waiting, so an intermediate
  // lane can be dropped and only the newest is sent. That is the right shape for
  // its transport, a socket write with a 500ms-then-1500ms retry where a slow
  // consumer could otherwise accumulate a backlog of stale lanes.
  //
  // Sidecar serializes without dropping, for three reasons. Each report here is
  // a subprocess against an append-only store that enforces a strictly
  // increasing sequence per run, so ordering is already the constraint and a
  // dropped intermediate is a lane the notification lane never sees at all --
  // "working" swallowed between two idles is a turn that appears never to have
  // happened. The queue cannot grow without bound anyway, because publishState
  // already suppresses exact repeats, so its depth is the number of genuine
  // state changes rather than the event rate. And a coalescing queue's output
  // depends on subprocess timing, which would make the asset and its Go mirror
  // impossible to compare over a fixed fixture -- the one test that has ever
  // caught real drift between them.
  //
  // Sequences are assigned here, in the same order the queue will deliver them,
  // so the store's strictly-increasing contract is satisfied by construction
  // rather than by luck.
  let queue = Promise.resolve()
  const enqueue = (action) => {
    if (carriesSequence(action)) seq += 1
    const args = buildArgs(action, seq, st.sessionId)
    queue = queue.then(() => runOnce(args)).catch(() => {})
    return queue
  }

  const handle = (ev) => {
    for (const action of mapEvent(st, ev)) enqueue(action)
  }

  // readCtx flattens the parts of Pi's ctx the mapping reads. Every read is
  // guarded: a host that throws from a getter must not take reporting down, and
  // an absent isIdle stays undefined rather than collapsing to a boolean, which
  // both guards above depend on.
  const readCtx = (ctx) => {
    let idle
    try {
      idle = ctx?.isIdle?.()
    } catch (e) {
      idle = undefined
    }
    let sessionPath
    try {
      sessionPath = ctx?.sessionManager?.getSessionFile?.()
    } catch (e) {
      sessionPath = undefined
    }
    let sessionId
    try {
      sessionId = ctx?.sessionManager?.getSessionId?.()
    } catch (e) {
      sessionId = undefined
    }
    return { mode: ctx?.mode, idle, sessionPath, sessionId }
  }

  pi.events.on(BLOCKED_CHANNEL, (data) => {
    handle({ type: "blocked", blockedActive: !!data?.active, blockedLabel: data?.label })
  })

  pi.on("session_start", (event, ctx) => {
    handle({ type: "session_start", reason: event?.reason, ...readCtx(ctx) })
  })

  pi.on("agent_start", (_event, ctx) => {
    handle({ type: "agent_start", ...readCtx(ctx) })
  })

  pi.on("agent_settled", (_event, ctx) => {
    handle({ type: "agent_settled", ...readCtx(ctx) })
  })

  // DELIBERATELY NOT SUBSCRIBED.
  //
  // agent_end: see the agent_settled branch. It means "this attempt stopped",
  // and a retry or a compaction can follow it.
  //
  // session_shutdown: it fires with reason "quit", "reload", "new", "resume" or
  // "fork" (types.d.ts:478-483), so three of its five reasons are not an exit at
  // all -- they are the same session swap that produces the session_start this
  // file already handles. Releasing the lane on those would hand a live pane
  // back to screen detection in the middle of a run. Upstream reached the same
  // conclusion and moved exit detection into its own process supervisor; Sidecar
  // already owns process liveness for every provider, so nothing is lost by
  // leaving it there. `process_exit` is therefore NOT claimed in the capability
  // entry.
}

// EXPORT SURFACE.
//
// Pi drops a module whose default export is not a function, without an error,
// so the pure mapping hangs off the factory rather than being exported beside
// it. `exports-harness.mjs` asserts that every export of this module is a
// function and that the default one is the factory, because the failure mode is
// the worst kind: every test passes, the extension installs cleanly, and it
// reports nothing at all.
SidecarLifecycle.internals = {
  newState,
  mapEvent,
  buildArgs,
  carriesSequence,
  isAbsoluteSessionPath,
  sessionStartReason,
  SOURCE,
  PROVIDER,
  VERSION,
  REPORT_TIMEOUT_MS,
  BLOCKED_CHANNEL,
}
