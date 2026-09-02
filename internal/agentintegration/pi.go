package agentintegration

import (
	_ "embed"
	"regexp"

	"github.com/marcus/sidecar/internal/agentactivity"
	"github.com/marcus/sidecar/internal/agentlifecycle"
)

// The Pi integration.
//
// Same split as OpenCode, for the same reason: the handler below is a Go mirror
// of the state machine inside assets/pi/sidecar-lifecycle.js, kept deliberately
// separate from the shipped JavaScript so the mapping can be replayed in an
// ordinary test. A bug in "which event becomes which lane" is then a failing
// assertion rather than something discovered when a pane freezes on someone's
// machine. The two are held together by TestBundledPiAssetBehavesLikeTheHandler,
// which drives both over the same fixtures and requires identical ordered argv.
//
// The provider half is ported from Herdr's pi integration at version 8 and is
// kept verbatim in behavior; the deliberate differences are named in the asset
// and recorded in portedfrom.go.

// Pi integration identity.
const (
	PiProvider = "pi"
	PiSource   = "sidecar.pi.extension"

	// PiAssetVersion is the bundled asset's version. Authority is granted to a
	// source at a version, so this changing is what makes an installed copy
	// "outdated" rather than merely different.
	//
	// Bump it whenever assets/pi/sidecar-lifecycle.js changes, once a release
	// has shipped `agent integration install pi`. Until then the bytes may be
	// revised in place, because there is no earlier copy of version 1 anywhere
	// to be misread. See the same note on OpenCodeAssetVersion, and
	// asset_golden_test.go, which states the whole bump order.
	PiAssetVersion = "1"

	// PiAssetName is the filename the asset is installed as.
	//
	// Pi's loader takes any bare *.ts or *.js file in an extension directory
	// (isExtensionFile, dist/core/extensions/loader.js:528-530), so the
	// extension is free and .js is chosen: node cannot import a .ts module, and
	// the equivalence harness has to run the shipped file itself.
	PiAssetName = "sidecar-lifecycle.js"
)

//go:embed assets/pi/sidecar-lifecycle.js
var piAsset string

// PiAsset returns the bundled extension source.
func PiAsset() string { return piAsset }

// PiEvent is one flattened Pi extension event.
//
// It carries only what the mapping reads: the event's own reason, and the three
// fields of `ctx` the asset touches. No message content ever reaches this type,
// so no handler bug can leak any.
type PiEvent struct {
	// Type is the Pi event name: session_start, agent_start, agent_settled, or
	// the synthetic "blocked" for the event-bus channel.
	Type string
	// Reason is session_start's own reason: startup, reload, new, resume, fork.
	Reason string
	// Mode is ctx.mode. The TUI gate is on this and not on ctx.hasUI, because
	// an RPC session reports hasUI true while being headless.
	Mode string
	// Idle is ctx.isIdle(), and it is a tri-state on purpose. Pi's isIdle may be
	// absent, and both guards that read it distinguish "not idle" from "unknown":
	// unknown must neither start a turn nor complete one.
	Idle *bool
	// SessionPath and SessionID are ctx.sessionManager's two answers.
	SessionPath string
	SessionID   string
	// BlockedActive and BlockedLabel are the payload of the blocked channel.
	BlockedActive bool
	BlockedLabel  string
}

// PiAction is one report the handler wants made.
type PiAction struct {
	Kind   agentlifecycle.Kind
	State  agentactivity.State
	Reason agentlifecycle.ReasonCode
	// SessionPath and SessionID are the reference a KindSession action binds.
	// Exactly one is set: a path identifies the transcript a restore would
	// resume, which an id alone does not, so a path wins when both are known.
	SessionPath string
	SessionID   string
}

// PiHandler maps Pi's extension events onto lifecycle reports.
//
// The zero value is ready. Every field is upstream's, because the ladder they
// feed is upstream's.
type PiHandler struct {
	// rootSession latches once a TUI session_start has been seen. Until then
	// nothing is reported, which is what keeps an RPC or print-mode invocation
	// from claiming a pane it is not on screen in.
	rootSession  bool
	agentActive  bool
	blockedCount int
	// blockedMessage is carried but never transmitted. It participates in the
	// repeat-suppression comparison exactly as upstream's does, which is why it
	// is here; putting an unbounded label from another extension on the wire is
	// a separate decision, and the answer to it is no.
	blockedMessage string
	lastState      agentactivity.State
	lastMessage    string
	sessionPath    string
	sessionID      string
}

// Handle returns the actions one event should produce, in order. Most events
// produce none.
func (h *PiHandler) Handle(ev PiEvent) []PiAction {
	var actions []PiAction

	switch ev.Type {
	case "session_start":
		// TUI only. RPC, JSON and print modes are headless, and RPC reports
		// hasUI true, so mode is the reliable gate. Upstream has a fixture for
		// exactly this and it is translated as rpc-session-is-ignored.tsv.
		if ev.Mode != "tui" {
			return nil
		}
		h.rootSession = true
		h.updateSessionRef(ev)
		// The binding goes first, and the asset's serialized queue makes that
		// mean "the binding process has exited before the state report is
		// spawned". That is the translation of upstream's awaited session
		// report.
		actions = append(actions, h.sessionActions()...)
		// A reload can replace the extension mid-run without emitting another
		// agent_start, so the run's true state is read back rather than assumed
		// idle. Explicitly false, not "not true": unknown is not working.
		h.agentActive = ev.Idle != nil && !*ev.Idle
		return append(actions, h.publish(piSessionStartReason(ev.Reason), true)...)

	case "agent_start":
		if !h.rootSession {
			return nil
		}
		h.updateSessionRef(ev)
		// Upstream re-asserts the binding on every turn. Kept: it is what
		// recovers a binding Sidecar lost to a restart mid-session.
		actions = append(actions, h.sessionActions()...)
		h.agentActive = true
		return append(actions, h.publish(agentlifecycle.ReasonTurnStart, false)...)

	case "agent_settled":
		// Turn completion is agent_settled and never agent_end: agent_end means
		// "this attempt stopped" and Pi can follow it with an automatic retry or
		// a compaction. The idle guard discards a stale settlement, and it is
		// "not true" rather than "false" because an absent isIdle is unknown and
		// unknown must not close a turn.
		if !h.rootSession || ev.Idle == nil || !*ev.Idle {
			return nil
		}
		h.agentActive = false
		return h.publish(agentlifecycle.ReasonTurnComplete, false)

	case "blocked":
		// Unreachable against every released Pi: nothing publishes the channel
		// this listens on, because Pi ships no permission system. The branch is
		// kept because it costs one comparison, upstream's own fixture drives it,
		// and the capability entry records it as structurally unreachable rather
		// than merely untraced.
		if !h.rootSession {
			return nil
		}
		if !ev.BlockedActive {
			if h.blockedCount > 0 {
				h.blockedCount--
			}
			if h.blockedCount == 0 {
				h.blockedMessage = ""
			}
			return h.publish(agentlifecycle.ReasonPermissionResolved, false)
		}
		h.blockedCount++
		h.blockedMessage = ev.BlockedLabel
		return h.publish(agentlifecycle.ReasonPermissionRequest, false)
	}
	return nil
}

// desiredState is upstream's ladder, unchanged: blocked outranks working,
// working outranks idle. The ordering is load-bearing rather than stylistic -- a
// settlement arriving while a block is outstanding must not report idle -- and
// blocked-outranks-settle.tsv drives exactly that.
func (h *PiHandler) desiredState() (agentactivity.State, string) {
	switch {
	case h.blockedCount > 0:
		return agentactivity.StateBlocked, h.blockedMessage
	case h.agentActive:
		return agentactivity.StateWorking, ""
	default:
		return agentactivity.StateIdle, ""
	}
}

// publish reports the desired lane unless it is an exact repeat.
//
// force exists for one caller: session_start re-asserts the lane even when it
// has not changed, because a reload replaces the extension mid-run and Sidecar
// has no record of what the previous instance reported.
func (h *PiHandler) publish(reason agentlifecycle.ReasonCode, force bool) []PiAction {
	state, message := h.desiredState()
	if !force && state == h.lastState && message == h.lastMessage {
		return nil
	}
	h.lastState, h.lastMessage = state, message
	return []PiAction{{Kind: agentlifecycle.KindState, State: state, Reason: reason}}
}

// updateSessionRef adopts the conversation reference the event carried.
func (h *PiHandler) updateSessionRef(ev PiEvent) {
	h.sessionPath = ""
	if piAbsoluteSessionPath(ev.SessionPath) {
		h.sessionPath = ev.SessionPath
	}
	h.sessionID = ev.SessionID
}

// sessionActions emits the binding, or nothing when Pi has told us nothing to
// bind.
func (h *PiHandler) sessionActions() []PiAction {
	switch {
	case h.sessionPath != "":
		return []PiAction{{Kind: agentlifecycle.KindSession, SessionPath: h.sessionPath}}
	case h.sessionID != "":
		return []PiAction{{Kind: agentlifecycle.KindSession, SessionID: h.sessionID}}
	}
	return nil
}

// piWindowsAbsolutePath matches the two shapes a Windows absolute path arrives
// in, C:\... and C:/....
var piWindowsAbsolutePath = regexp.MustCompile(`^[A-Za-z]:[\\/]`)

// piAbsoluteSessionPath reports whether a session file path is worth reporting.
//
// This is the one genuine upstream bug the port fixes. Herdr's Pi asset accepts
// a session path only when it starts with "/", which silently discards every
// Windows path; its OMP variant fixed exactly this and has a test for it, and
// the Pi variant never received the fix.
func piAbsoluteSessionPath(path string) bool {
	if path == "" {
		return false
	}
	if path[0] == '/' {
		return true
	}
	return piWindowsAbsolutePath.MatchString(path)
}

// piSessionStartReason maps Pi's session_start reason onto Sidecar's frozen
// reason vocabulary.
//
// Upstream carries the raw reason as a session_start_source field on its own
// session report. Sidecar's `agent report-session` has no such field and its
// reason codes are an allowlist, so the fact lands on the forced state report
// instead: "startup" is a process that has just begun, and Pi's four other
// reasons are all a live process swapping which conversation it is on, which is
// what session_change means. An absent or unrecognised reason takes the
// conservative reading.
func piSessionStartReason(reason string) agentlifecycle.ReasonCode {
	switch reason {
	case "new", "reload", "resume", "fork":
		return agentlifecycle.ReasonSessionChange
	default:
		return agentlifecycle.ReasonSessionStart
	}
}

// PiReportArgs builds the exact CLI argv one action becomes.
//
// It mirrors buildArgs in the bundled asset. Both exist because the asset must
// construct argv in JavaScript at runtime, and the equivalence test compares the
// two lists element for element -- so this is the Go statement of the same
// contract, not a convenience wrapper.
//
// NEITHER VERB CARRIES --seq, and there is no sequence parameter to pass.
// `agent report-session` never had the flag. `agent report` has it and it is
// omitted, which is what its own help names as the right thing for a per-event
// hook process to do: the store assigns under the exclusive lock it already
// takes for the append (lifecyclestore.AppendNext), which is the only place the
// read and the write are atomic.
//
// The asset held a counter twice and dropped reports both times -- opening at
// zero dropped a reloaded instance's reports, and seeding at Date.now()*1000
// dropped every report by exceeding MaxSequence -- and both were silent because
// reports spawn with stdio "ignore". The asset's buildArgs carries the full
// account. What matters here is that this mirror has no sequence to drift on.
//
// The blocked label is deliberately absent from every argv: it is unbounded text
// authored by another extension, and nothing but lanes, bounded codes and
// conversation identifiers goes over this wire.
func PiReportArgs(action PiAction, sessionID string) []string {
	if action.Kind == agentlifecycle.KindSession {
		args := []string{"agent", "report-session", "--kind", PiProvider, "--source", PiSource}
		switch {
		case action.SessionPath != "":
			args = append(args, "--path", action.SessionPath)
		case action.SessionID != "":
			args = append(args, "--id", action.SessionID)
		}
		return args
	}
	args := []string{
		"agent", "report",
		"--source", PiSource,
		"--source-version", PiAssetVersion,
		"--provider", PiProvider,
	}
	if sessionID != "" {
		args = append(args, "--session-id", sessionID)
	}
	args = append(args, "--state", string(action.State))
	return append(args, "--reason", string(action.Reason))
}

// Session returns the provider session id the handler has adopted, which the
// asset also carries on every state report.
func (h *PiHandler) Session() string { return h.sessionID }
