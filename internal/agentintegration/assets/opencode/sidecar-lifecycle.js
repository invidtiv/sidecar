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
// WHY session.status IS THE PRIMARY SIGNAL
//
// session.status is state-shaped: every emission re-asserts ground truth as
// busy or idle, so a dropped or reordered event is corrected by the next one
// rather than leaving a pane stuck. That property is why OpenCode was chosen as
// the first full-lifecycle provider.
//
// It does NOT extend to the blocked lane. permission.asked/permission.replied
// are transition-shaped, so a dropped permission.asked will not self-correct.
// The handler compensates by treating any later busy or idle assertion as
// clearing the blocked lane, which bounds how long a missed unblock can last.
//
// CANCELLATION
//
// OpenCode has no dedicated cancel event. A user interrupt arrives as
// session.error carrying error.name === "MessageAbortedError", followed by
// session.status idle. A provider failure emits the identical shape with a
// different error name, so the name is the only discriminator and is read
// explicitly rather than assumed.
//
// FAILING OPEN
//
// Every report is fire-and-forget and every error is swallowed. Outside a
// Sidecar-managed shell the command exits 0 and does nothing. Nothing in this
// file may delay, block, or change what OpenCode does.

import { spawn } from "node:child_process"

const SOURCE = "sidecar.opencode.plugin"
const PROVIDER = "opencode"
const VERSION = "1"

// The binary Sidecar published into this shell. Absent means we are not in a
// Sidecar-managed shell, and the whole plugin becomes a no-op with no
// subprocess ever spawned.
const BIN = process.env.SIDECAR_BIN
const MANAGED = process.env.SIDECAR_MANAGED_SHELL === "1"

export const SidecarLifecycle = async () => {
  if (!MANAGED || !BIN) return {}

  let seq = 0
  let sessionID = ""
  let lane = ""
  let blocked = false

  // Direct argv, never a shell string. Nothing from OpenCode is interpolated
  // into a command line; every value is a separate argv element and every one
  // of them is a bounded enum or an identifier Sidecar re-validates.
  const send = (args) => {
    try {
      const child = spawn(BIN, args, { stdio: "ignore", detached: false })
      child.on("error", () => {})
      child.unref?.()
    } catch (e) {
      /* a reporting failure must never surface to the agent */
    }
  }

  const base = (verb) => {
    seq += 1
    const args = ["agent", verb, "--source", SOURCE, "--provider", PROVIDER, "--seq", String(seq)]
    if (sessionID) args.push("--session-id", sessionID)
    return args
  }

  const report = (state, reason) => {
    // Suppress an exact repeat of the lane we last reported. OpenCode emits
    // session.status busy several times per turn, and each one would otherwise
    // be a process spawn that tells Sidecar nothing new.
    if (state === lane) return
    lane = state
    const args = base("report")
    args.push("--state", state, "--reason", reason)
    send(args)
  }

  const end = (outcome, reason) => {
    lane = ""
    const args = base("end")
    args.push("--outcome", outcome, "--reason", reason)
    send(args)
  }

  return {
    event: async ({ event }) => {
      const type = event?.type
      const props = event?.properties || {}

      switch (type) {
        case "session.created": {
          const id = props.info?.id || props.sessionID
          if (id && id !== sessionID) {
            sessionID = id
            // A session change reanchors the run. Reporting it as a session
            // record rather than a lane keeps identity and state separate.
            send(base("report").concat(["--state", "idle", "--reason", "session_start"]))
            lane = "idle"
          }
          return
        }

        case "session.status": {
          const status = props.status?.type || props.info?.status?.type
          if (status === "busy") {
            // Any positive busy assertion clears a blocked lane. This is the
            // compensation for permission events being transition-shaped: a
            // missed permission.replied cannot strand the pane on blocked for
            // longer than the next status emission.
            blocked = false
            report("working", "turn_start")
          } else if (status === "idle") {
            blocked = false
            report("idle", "turn_complete")
          }
          return
        }

        case "permission.asked": {
          blocked = true
          report("blocked", "permission_request")
          return
        }

        case "permission.replied": {
          if (!blocked) return
          blocked = false
          report("working", "permission_resolved")
          return
        }

        case "session.error": {
          // The one place the error name matters. Cancellation and failure are
          // the same event shape on this bus and differ only here.
          const name = props.error?.name || props.info?.error?.name
          if (name === "MessageAbortedError") {
            end("cancelled", "cancelled")
          } else {
            end("failed", "provider_error")
          }
          return
        }

        default:
          return
      }
    },

    // The provider process is going away. Releasing rather than reporting a
    // lane is correct: the run is over and Sidecar should return to ordinary
    // process and screen observation immediately rather than hold a last
    // remembered state.
    dispose: async () => {
      send(base("release").concat(["--reason", "process_exit"]))
    },
  }
}
