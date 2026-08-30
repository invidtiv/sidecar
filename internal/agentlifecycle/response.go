package agentlifecycle

// This file defines the JSON bodies the `sidecar agent report`, `end`,
// `release`, and `explain` commands return. They live beside the record types
// rather than in the CLI package for the same reason the resolver does: the
// shapes are contract, the command is only a transport, and a second caller
// (an HTTP or MCP surface, a remote host) must be able to produce the identical
// body without moving rules out of the core.

// ReportResult is the JSON body of a successful report, end, or release.
//
// It exists because success is not one thing. A record can be stored by a
// source that may author state, stored by a source that may not, or recognised
// as a replay of one already held. An integration author debugging a silent
// hook needs to tell those apart, and an exit code of zero cannot.
type ReportResult struct {
	SchemaVersion int `json:"schemaVersion"`

	// Accepted says how the record was taken.
	Accepted Acceptance `json:"accepted"`
	// Tier is the authority the accepting source may currently exercise. An
	// AcceptedAdvisory result pairs with a tier below full and is the honest
	// answer to "my hook is firing but nothing changes".
	Tier Tier `json:"tier"`
	// TierReason explains a tier lower than the source's nominal claim.
	TierReason FallbackReason `json:"tierReason,omitempty"`

	// ID and Sequence identify the stored record.
	ID       string `json:"id,omitempty"`
	Sequence uint64 `json:"sequence"`
	// Identity is the context the record was bound to, after Sidecar derived it.
	// A hook cannot choose these values, so echoing them back is how an author
	// confirms the report landed on the pane and run they expected.
	Identity Identity `json:"identity"`
}

// Error is a machine-readable failure from a lifecycle command.
type Error struct {
	Code ErrorCode `json:"code"`
	// Message is sanitized, actionable prose. It never quotes provider input
	// back, because provider input is untrusted and may be adversarial.
	Message string `json:"message"`
	// Identity is included when the failure is about context or identity, which
	// are the failures a person cannot diagnose without seeing what Sidecar
	// resolved.
	Identity *Identity `json:"identity,omitempty"`
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return string(e.Code) + ": " + e.Message
}

// ErrorEnvelope wraps an Error so that JSON output has one stable top-level
// shape whether a command succeeded or failed.
type ErrorEnvelope struct {
	Error *Error `json:"error,omitempty"`
}
