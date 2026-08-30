// Package agentlifecycle owns the provider-neutral contract for agent
// lifecycle reports and the pure arbitration that decides whether such a
// report, rather than screen observation, authors a pane's activity state.
//
// # What lives here and what does not
//
// This package is deliberately transport-free and side-effect-free. It has no
// CLI, Bubble Tea, tmux, provider-configuration, filesystem, or notification
// dependency, and it must keep none. Its only Sidecar dependency is
// [agentactivity], because the resolver's whole job is to return that
// package's [agentactivity.Result] — introducing a parallel state vocabulary
// here would create exactly the second classifier the M6 plan forbids.
//
// The JSONL store, the `sidecar agent report` command, the provider adapters,
// the bundled integration assets, and the workspace/inventory call sites are
// Phase B and Phase C. They depend on this package; this package never learns
// about them.
//
// # A report is evidence, not a verdict
//
// Provider hook input is untrusted local data. A [Report] records what a
// provider's own lifecycle event claimed, bound to the host, tmux server
// incarnation, pane, provider process generation, and agent run it was
// observed against. It never carries prompt text, response text, tool
// arguments, environment values, credentials, or arbitrary provider paths: the
// only free-form field is [Report.Detail], which is bounded and expected to be
// a sanitized diagnostic string.
//
// Whether that evidence wins is [Resolve]'s decision alone, and it turns on
// the source's proved capability [Tier], the freshness of the report, and
// whether every identity field still matches the live pane. Any discontinuity
// — process exit, pane replacement, server restart, session rotation, explicit
// release, or simple staleness — fails toward current screen and process
// detection. It never fails toward a remembered `blocked` or `idle` lane,
// because a stale guess is worse than an honest fallback.
//
// # Authority is versioned, not permanent
//
// Authority belongs to a specific integration source at a specific asset
// version observed against a specific run, not to a provider name forever. A
// [Capability] records the tier a source earned, the provider version range
// that tier was proved against, which [Transition] values real traces covered,
// and what the known gaps are. An unproved or newer provider version starts
// advisory rather than inheriting full authority. See
// docs/reference/agent-lifecycle-capability-matrix.md for the recorded
// evidence behind each shipped tier.
package agentlifecycle
