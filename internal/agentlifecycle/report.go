package agentlifecycle

import (
	"time"

	"github.com/marcus/sidecar/internal/agentactivity"
)

// SchemaVersion is the wire version of every record and diagnostic this
// package defines: [Report], [Explanation], and [IntegrationStatus] share it
// so a reader that understands one understands all three.
//
// Bump it only for a change a version-1 reader could misread. Adding an
// optional field that an old reader may ignore is not such a change; removing
// a field, renaming one, narrowing an enum, or altering what an existing value
// means is. The store keeps the version on every appended line so a fold can
// skip records from a future writer rather than misinterpret them.
const SchemaVersion = 1

// Kind distinguishes what a report asserts. Splitting these out rather than
// inventing extra steady-state lanes keeps [State] to the three lanes the rest
// of Sidecar already reasons about.
type Kind string

const (
	// KindState asserts the run's current lane. It carries a State.
	KindState Kind = "state"
	// KindSession establishes or rotates the provider session identity for a
	// run without asserting a lane. A session-identity-only integration emits
	// only these, which is exactly why it never earns lifecycle authority.
	KindSession Kind = "session"
	// KindEnd asserts that the run finished and carries an Outcome. It clears
	// lifecycle authority; process liveness still confirms the run really ended
	// before any surface calls the pane orphaned or failed.
	KindEnd Kind = "end"
	// KindRelease surrenders authority without claiming an outcome — the hook
	// is being uninstalled, disabled, or has detected it can no longer observe
	// the run truthfully.
	KindRelease Kind = "release"
)

// Kinds is the frozen, ordered report-kind vocabulary.
func Kinds() []Kind { return []Kind{KindState, KindSession, KindEnd, KindRelease} }

// Outcome is the bounded terminal result a KindEnd report may carry.
//
// It is deliberately not a fourth lane. A finished run's *lane* is idle; the
// outcome is separate evidence the shared status projection may use for health,
// and it is the only place a provider gets to say a turn failed rather than
// merely stopped.
type Outcome string

const (
	OutcomeCompleted Outcome = "completed"
	OutcomeCancelled Outcome = "cancelled"
	OutcomeFailed    Outcome = "failed"
	// OutcomeUnknown is the honest answer when a provider's end event cannot
	// distinguish success from failure. It must stay available: forcing such a
	// provider to guess "completed" would launder missing evidence into a
	// health claim.
	OutcomeUnknown Outcome = "unknown"
)

// Outcomes is the frozen, ordered outcome vocabulary.
func Outcomes() []Outcome {
	return []Outcome{OutcomeCompleted, OutcomeCancelled, OutcomeFailed, OutcomeUnknown}
}

// ReportStates is the frozen, ordered set of lanes a lifecycle report may
// assert. It is a strict subset of [agentactivity.State]: a report may never
// assert "unknown", because a source that does not know something must stay
// silent and let screen detection answer rather than actively reporting
// ignorance and displacing better evidence.
func ReportStates() []agentactivity.State {
	return []agentactivity.State{
		agentactivity.StateWorking,
		agentactivity.StateBlocked,
		agentactivity.StateIdle,
	}
}

// Identity binds a report, and the arbitration that consumes it, to exactly
// one live agent run. Every field is derived by Sidecar from the managed-shell
// environment, the tmux context, and the hook process ancestry. None of it is
// selectable through provider input: that is what stops a hook from reporting
// for another pane, host, server, or run.
type Identity struct {
	// Host identifies the machine that owns the pane. Reports never cross
	// hosts; a registered remote resolves its own state locally.
	Host string `json:"host"`
	// ServerIncarnation namespaces everything by a specific tmux server
	// lifetime. Without it a recycled %pane identifier after a server restart
	// would silently inherit the previous occupant's authority.
	ServerIncarnation string `json:"serverIncarnation"`
	// PaneID is the tmux pane identifier, e.g. "%7".
	PaneID string `json:"paneId"`
	// Provider is the catalog agent kind, e.g. "opencode".
	Provider string `json:"provider"`
	// RunID is the Sidecar-assigned identifier for one agent run in this pane.
	RunID string `json:"runId"`
	// ProcessGeneration pins the run to a specific provider process. A restart
	// in the same pane produces a new generation, so a late report from the
	// previous process cannot regain authority.
	ProcessGeneration string `json:"processGeneration"`
	// SessionFingerprint is an optional host-salted digest of the provider's
	// own session identifier. The salted digest is all the lifecycle store ever
	// retains; the exact reference, where session restore needs it, travels
	// separately to the agentsession-owned shell binding.
	SessionFingerprint string `json:"sessionFingerprint,omitempty"`
}

// Report is one appended lifecycle record.
//
// Field ordering here is the JSON field ordering pinned by the contract
// fixtures, so a reordering is a visible test failure rather than a silent
// wire change.
type Report struct {
	SchemaVersion int    `json:"schemaVersion"`
	ID            string `json:"id"`
	Kind          Kind   `json:"kind"`

	Identity Identity `json:"identity"`

	// Source is the integration that produced this report, e.g.
	// "sidecar.opencode.plugin". It is distinct from Provider because one
	// provider may over time have more than one integration shape, and
	// authority is granted to a source at a version, never to a provider name.
	Source string `json:"source"`
	// SourceVersion is the installed integration asset version.
	SourceVersion string `json:"sourceVersion"`

	// Sequence is strictly increasing within
	// (ServerIncarnation, PaneID, Source, RunID). It is what makes replay
	// idempotent and out-of-order delivery rejectable without a clock.
	Sequence uint64 `json:"sequence"`

	// State is set for KindState reports and empty otherwise.
	State agentactivity.State `json:"state,omitempty"`
	// Outcome is set for KindEnd reports and empty otherwise.
	Outcome Outcome `json:"outcome,omitempty"`

	// ObservedAt is when the provider event happened, as the hook saw it. It is
	// checked for skew against the receiver's clock but never trusted for
	// ordering — Sequence does that.
	ObservedAt time.Time `json:"observedAt"`

	// Reason is a bounded code from the frozen [Reasons] allowlist describing
	// which provider event produced this report.
	Reason ReasonCode `json:"reason,omitempty"`
	// Detail is a short sanitized diagnostic string. It is the only free-form
	// field in the record and it must never carry prompt text, response text,
	// tool arguments or results, environment values, credentials, or paths
	// outside Sidecar's own state tree.
	Detail string `json:"detail,omitempty"`
}

// Key is the scope within which [Report.Sequence] must strictly increase.
type Key struct {
	ServerIncarnation string `json:"serverIncarnation"`
	PaneID            string `json:"paneId"`
	Source            string `json:"source"`
	RunID             string `json:"runId"`
}

// Key returns the sequence scope this report belongs to.
func (r Report) Key() Key {
	return Key{
		ServerIncarnation: r.Identity.ServerIncarnation,
		PaneID:            r.Identity.PaneID,
		Source:            r.Source,
		RunID:             r.Identity.RunID,
	}
}

// ReasonCode names the provider event behind a report. The vocabulary is
// provider-neutral on purpose: an adapter translates its native event name
// into one of these, so nothing downstream branches on a vendor.
type ReasonCode string

const (
	ReasonTurnStart          ReasonCode = "turn_start"
	ReasonToolUse            ReasonCode = "tool_use"
	ReasonPermissionRequest  ReasonCode = "permission_request"
	ReasonQuestion           ReasonCode = "question"
	ReasonPermissionResolved ReasonCode = "permission_resolved"
	ReasonTurnComplete       ReasonCode = "turn_complete"
	ReasonCancelled          ReasonCode = "cancelled"
	ReasonSessionStart       ReasonCode = "session_start"
	ReasonSessionChange      ReasonCode = "session_change"
	ReasonSubagentStart      ReasonCode = "subagent_start"
	ReasonSubagentStop       ReasonCode = "subagent_stop"
	ReasonCompaction         ReasonCode = "compaction"
	ReasonProcessExit        ReasonCode = "process_exit"
	ReasonIntegrationRemoved ReasonCode = "integration_removed"
	ReasonProviderError      ReasonCode = "provider_error"
	ReasonUnspecified        ReasonCode = "unspecified"
)

// Reasons is the frozen, ordered reason-code allowlist. A report carrying a
// code outside this set is rejected rather than stored, which is what keeps an
// adapter from smuggling free text through the reason field.
func Reasons() []ReasonCode {
	return []ReasonCode{
		ReasonTurnStart,
		ReasonToolUse,
		ReasonPermissionRequest,
		ReasonQuestion,
		ReasonPermissionResolved,
		ReasonTurnComplete,
		ReasonCancelled,
		ReasonSessionStart,
		ReasonSessionChange,
		ReasonSubagentStart,
		ReasonSubagentStop,
		ReasonCompaction,
		ReasonProcessExit,
		ReasonIntegrationRemoved,
		ReasonProviderError,
		ReasonUnspecified,
	}
}
