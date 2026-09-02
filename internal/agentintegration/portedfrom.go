package agentintegration

// Where a Sidecar integration asset came from.
//
// Every asset Sidecar ships has an upstream counterpart in Herdr's
// src/integration/assets, and the valuable half of that counterpart is
// provider-specific knowledge: which hook or plugin event means working,
// blocked, idle, or a session reference, and the ordering guards around it. The
// transport half is Sidecar's own. So the maintenance question after a Herdr
// sync is never "what does this file do" but "what changed since the version we
// wrote ours against", and that question needs a recorded answer.
//
// This is that answer, and it is Go data rather than a comment in each asset for
// two reasons. The sync tool has to read it without parsing a header out of five
// different comment syntaxes, and the Claude and Codex assets are not files at
// all: they are hook entries built as Go values in claude_install.go and
// codex_install.go, so there is no header to put it in. See
// docs/plans/active/herdr-detection-parity.md, Phase 6.
//
// A record here is a claim about provenance, so it is either established from
// evidence or it is UnknownPortedVersion. It is never a guess: with "unknown"
// the sync report shows the whole current upstream file, which is the correct
// amount of work when nobody can say what the port was reviewed against.

// UnknownPortedVersion is the Version of a Sidecar asset whose upstream
// starting point could not be established. The sync report then renders the
// whole upstream file rather than a diff.
const UnknownPortedVersion = "unknown"

// PortedFrom records the Herdr integration version one Sidecar asset was
// written against.
type PortedFrom struct {
	// Provider is the Sidecar provider id, matching Adapter.Provider().
	Provider string `json:"provider"`
	// UpstreamID is the Herdr agent id the upstream assets are vendored under,
	// which is what UpstreamLock.Provider is keyed by.
	UpstreamID string `json:"upstream_id"`
	// UpstreamDir is the directory name under Herdr's src/integration/assets.
	UpstreamDir string `json:"upstream_dir"`
	// Version is the HERDR_INTEGRATION_VERSION the port was written against, as
	// a string so UnknownPortedVersion is expressible in the same field.
	Version string `json:"version"`
	// Commit is the Herdr commit carrying that version, when one is known. The
	// sync tool reads the upstream files at this commit to diff them against
	// what it just vendored, so a byte change that upstream did not bump a
	// version for is still visible.
	Commit string `json:"commit,omitempty"`
	// Evidence says where Version and Commit come from. It is prose because the
	// provenance of a port is prose; the point is that a reader can check it.
	Evidence string `json:"evidence"`
}

// herdrInspectedCommit is the Herdr commit
// docs/plans/active/notification-agent-lifecycle-hooks.md records as inspected
// while Sidecar's three integrations were written: "Herdr commit 4a3b04f5... was
// inspected for the report/release command contract, per-source sequence
// handling, hook authority resolver, capability allowlist, and provider assets."
// It is dated 2026-08-30 03:03 +0300, one day before the commits that added
// assets/opencode/sidecar-lifecycle.js, claude_install.go and codex_install.go.
const herdrInspectedCommit = "4a3b04f59ba3b7d8a15cea187b23e1e80c343b0c"

// portedFrom is the recorded provenance of every Sidecar integration asset.
//
// Adding an adapter adds a row here, and TestEveryAdapterRecordsWhatItWasPortedFrom
// is what says so.
var portedFrom = []PortedFrom{
	{
		Provider:    OpenCodeProvider,
		UpstreamID:  "opencode",
		UpstreamDir: "opencode",
		Version:     "10",
		Commit:      herdrInspectedCommit,
		Evidence: "Herdr's opencode assets are at HERDR_INTEGRATION_VERSION=10 at the commit the " +
			"lifecycle plan records as inspected for provider assets, one day before " +
			"assets/opencode/sidecar-lifecycle.js was written. The event mapping itself was derived " +
			"from Sidecar's own traces of OpenCode 1.18.25, so this names what the port was reviewed " +
			"against rather than transcribed from.",
	},
	{
		Provider:    CodexProvider,
		UpstreamID:  "codex",
		UpstreamDir: "codex",
		Version:     "8",
		Commit:      herdrInspectedCommit,
		Evidence: "Herdr's codex asset is at HERDR_INTEGRATION_VERSION=8 at that commit, and it is the " +
			"same shape Sidecar's adapter installs: a single session-identity SessionStart hook. " +
			"Corroborated by codex_install_test.go, whose trusted-hash vector is a live codex-cli " +
			"0.151.0 trust record for Herdr's own herdr-agent-state.sh session hook.",
	},
	{
		Provider:    ClaudeProvider,
		UpstreamID:  "claude",
		UpstreamDir: "claude",
		Version:     "9",
		Commit:      herdrInspectedCommit,
		Evidence: "Herdr's claude asset is at HERDR_INTEGRATION_VERSION=9 at that commit. Sidecar's " +
			"entry is session-identity only for the reason the lifecycle plan records from the same " +
			"inspection: Herdr removed its own Claude lifecycle hook set, and tracing Claude Code " +
			"2.1.220 reproduced both halves of its stated reason.",
	},
}

// PortedFromRecords returns the provenance of every Sidecar integration asset.
//
// The slice is copied, because it is data the sync tool ranges over and a
// caller that sorted it in place would reorder the package's own table.
func PortedFromRecords() []PortedFrom {
	return append([]PortedFrom(nil), portedFrom...)
}

// PortedFromProvider returns one provider's record.
func PortedFromProvider(provider string) (PortedFrom, bool) {
	for _, record := range portedFrom {
		if record.Provider == provider {
			return record, true
		}
	}
	return PortedFrom{}, false
}
