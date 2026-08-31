// Package agentintegration owns Sidecar's bundled provider integrations: the
// assets that are installed beside a provider, and the handlers that define
// what those assets are supposed to do.
//
// The handler is a pure state machine, deliberately separate from the shipped
// JavaScript that runs inside OpenCode. Keeping a Go mirror of the mapping is
// what lets the recorded traces be replayed against it in an ordinary test: a
// bug in "which event becomes which lane" is then a failing assertion rather
// than something discovered when a pane freezes on someone's machine. The
// asset and the handler are held together by TestBundledAssetMatchesTheHandler.
package agentintegration

import (
	_ "embed"
	"strconv"
	"strings"

	"github.com/marcus/sidecar/internal/agentactivity"
	"github.com/marcus/sidecar/internal/agentlifecycle"
)

// OpenCode integration identity.
const (
	OpenCodeProvider = "opencode"
	OpenCodeSource   = "sidecar.opencode.plugin"

	// OpenCodeAssetVersion is the bundled asset's version. Authority is granted
	// to a source at a version, so this changing is what makes an installed
	// copy "outdated" rather than merely different.
	OpenCodeAssetVersion = "1"

	// OpenCodeAssetName is the filename the asset is installed as.
	OpenCodeAssetName = "sidecar-lifecycle.js"
)

//go:embed assets/opencode/sidecar-lifecycle.js
var openCodeAsset string

// OpenCodeAsset returns the bundled plugin source.
func OpenCodeAsset() string { return openCodeAsset }

// OpenCodeOwnedDir is the single plugin directory Sidecar installs into,
// relative to the OpenCode config directory.
//
// OpenCodeConflictDir is the *other* directory OpenCode also loads. Both
// `plugin/` and `plugins/` are read, and an asset present in both fires every
// event twice — which would double every sequence number and make ordering
// meaningless. Sidecar therefore owns exactly one and treats a copy in the
// other as damage to be reported, never as a second install to keep in step.
const (
	OpenCodeOwnedDir    = "plugin"
	OpenCodeConflictDir = "plugins"
)

// OpenCodeEvent is one sanitized event from OpenCode's plugin bus.
//
// It carries only what the mapping needs, which is the same set the trace
// fixtures record: no message content ever reaches this type, so no handler
// bug can leak any.
type OpenCodeEvent struct {
	// Type is the bus event or hook name, e.g. "session.status".
	Type string
	// Status is the session.status discriminator, e.g. `{"type":"busy"}`.
	Status string
	// ErrorName is the error class name on session.error. It is the only thing
	// separating a cancelled turn from a failed one.
	ErrorName string
	// SessionID is the provider session identifier the event carried, if any.
	//
	// It is read rather than merely recorded: session.created fires on a new
	// id, which covers both the first session and a rotation mid-process. An
	// earlier version carried only a "has a session" boolean that nothing ever
	// consulted, which let this handler and the shipped asset disagree about
	// when a baseline lane is established.
	SessionID string
}

// OpenCodeAction is one report the handler wants made.
type OpenCodeAction struct {
	Kind    agentlifecycle.Kind
	State   agentactivity.State
	Outcome agentlifecycle.Outcome
	Reason  agentlifecycle.ReasonCode
}

// OpenCodeHandler maps OpenCode's event stream onto lifecycle reports.
//
// The zero value is ready. It is a state machine rather than a pure function
// per event because two of the mappings are stateful: a repeated lane must not
// be re-reported, and permission.replied means nothing unless a
// permission.asked preceded it.
type OpenCodeHandler struct {
	lane    agentactivity.State
	blocked bool
	session string
	// ended latches after a terminal outcome so a trailing status event cannot
	// resurrect a run that already reported its end.
	//
	// This is not defensive: the recorded traces show session.error is
	// immediately followed by session.status idle, so without the latch the
	// trailing idle supersedes the end report and a cancelled or failed turn is
	// announced to the user as a clean completion.
	ended bool
}

// Handle returns the actions one event should produce, in order. Most events
// produce none.
func (h *OpenCodeHandler) Handle(ev OpenCodeEvent) []OpenCodeAction {
	switch ev.Type {
	case "session.created":
		// Fires on a genuinely new session id, which covers both the first
		// session and a rotation mid-process. A rotation is a new run, so the
		// baseline lane is re-established and the latch is cleared.
		if ev.SessionID == h.session {
			return nil
		}
		h.session = ev.SessionID
		h.ended = false
		h.blocked = false
		h.lane = agentactivity.StateIdle
		return []OpenCodeAction{{
			Kind:   agentlifecycle.KindState,
			State:  agentactivity.StateIdle,
			Reason: agentlifecycle.ReasonSessionStart,
		}}

	case "session.status":
		if h.ended {
			return nil
		}
		switch statusType(ev.Status) {
		case "busy":
			// A positive busy assertion clears the blocked lane. This is the
			// deliberate compensation for the blocked lane being
			// transition-shaped rather than state-shaped: permission.asked and
			// permission.replied do not re-assert ground truth, so a dropped
			// permission.replied would otherwise strand the pane on blocked
			// indefinitely. Bounding that to "until the next status emission"
			// is the best the provider's contract allows.
			h.blocked = false
			return h.lane_(agentactivity.StateWorking, agentlifecycle.ReasonTurnStart)
		case "idle":
			h.blocked = false
			return h.lane_(agentactivity.StateIdle, agentlifecycle.ReasonTurnComplete)
		}
		return nil

	case "permission.asked":
		if h.ended {
			return nil
		}
		h.blocked = true
		return h.lane_(agentactivity.StateBlocked, agentlifecycle.ReasonPermissionRequest)

	case "permission.replied":
		if h.ended || !h.blocked {
			return nil
		}
		h.blocked = false
		return h.lane_(agentactivity.StateWorking, agentlifecycle.ReasonPermissionResolved)

	case "session.error":
		if h.ended {
			return nil
		}
		h.ended = true
		h.lane = ""
		// Cancellation and failure are the same shape on this bus. The bounded
		// error class name is the entire discriminator, which is why it is read
		// explicitly here and recorded as a known gap for any adapter that does
		// not read it.
		if ev.ErrorName == OpenCodeAbortedError {
			return []OpenCodeAction{{
				Kind:    agentlifecycle.KindEnd,
				Outcome: agentlifecycle.OutcomeCancelled,
				Reason:  agentlifecycle.ReasonCancelled,
			}}
		}
		return []OpenCodeAction{{
			Kind:    agentlifecycle.KindEnd,
			Outcome: agentlifecycle.OutcomeFailed,
			Reason:  agentlifecycle.ReasonProviderError,
		}}

	case "dispose":
		// The provider is going away. Releasing rather than asserting a lane is
		// correct: the run is over, and Sidecar must return to ordinary process
		// and screen observation immediately instead of holding a remembered
		// state that nothing will ever update again.
		return []OpenCodeAction{{
			Kind:   agentlifecycle.KindRelease,
			Reason: agentlifecycle.ReasonProcessExit,
		}}
	}
	return nil
}

// OpenCodeAbortedError is the error class name a user interrupt produces.
// Traced on 1.18.25; see docs/reference/agent-lifecycle-capability-matrix.md.
const OpenCodeAbortedError = "MessageAbortedError"

// lane_ reports a lane unless it is the one already reported.
//
// Suppressing repeats is not cosmetic. OpenCode emits session.status busy
// several times per turn, and each repeat would otherwise be a process spawn
// and a consumed sequence number that told Sidecar nothing new.
func (h *OpenCodeHandler) lane_(state agentactivity.State, reason agentlifecycle.ReasonCode) []OpenCodeAction {
	if h.lane == state {
		return nil
	}
	h.lane = state
	return []OpenCodeAction{{Kind: agentlifecycle.KindState, State: state, Reason: reason}}
}

// statusType extracts the discriminator from a `{"type":"busy"}` status value
// without parsing JSON, because the trace fixtures store it verbatim and the
// handler must read exactly what was recorded.
func statusType(status string) string {
	switch {
	case strings.Contains(status, `"busy"`):
		return "busy"
	case strings.Contains(status, `"idle"`):
		return "idle"
	default:
		return ""
	}
}

// ReportArgs builds the exact CLI argv one action becomes.
//
// It mirrors buildArgs in the bundled asset. Both exist because the asset must
// construct argv in JavaScript at runtime, and the equivalence test compares
// the two lists element for element -- so this is the Go statement of the same
// contract, not a convenience wrapper.
func ReportArgs(action OpenCodeAction, seq uint64, sessionID string) []string {
	verb := "report"
	switch action.Kind {
	case agentlifecycle.KindEnd:
		verb = "end"
	case agentlifecycle.KindRelease:
		verb = "release"
	}
	args := []string{
		"agent", verb,
		"--source", OpenCodeSource,
		"--provider", OpenCodeProvider,
		"--seq", strconv.FormatUint(seq, 10),
	}
	if sessionID != "" {
		args = append(args, "--session-id", sessionID)
	}
	if action.Kind == agentlifecycle.KindState {
		args = append(args, "--state", string(action.State))
	}
	if action.Kind == agentlifecycle.KindEnd {
		args = append(args, "--outcome", string(action.Outcome))
	}
	return append(args, "--reason", string(action.Reason))
}

// Session returns the provider session id the handler has adopted, which the
// asset also carries on every report.
func (h *OpenCodeHandler) Session() string { return h.session }
