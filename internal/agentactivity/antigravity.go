package agentactivity

// Antigravity's screen rules are Herdr's, executed from the vendored
// `manifests/upstream/antigravity.toml` by the manifest engine (Phase 2 of
// docs/plans/active/herdr-detection-parity.md), with two Sidecar rules layered
// over them in `manifests/sidecar/antigravity.toml` plus the RE2 rewrite of
// upstream's `spinner_working`. This file is what remains that is Sidecar's:
// the process gate, and the fallback exception below.
//
// Upstream has three rules and Sidecar's captures match none of them, so both
// the trust prompt and the working footer are overlay rules with a fixture
// each. Two rules from the Go table were dropped instead of carried:
//
//   - `antigravity.overlay.viewer` and `antigravity.overlay.retain` retained
//     the prior state on "conversation history", "transcript", "esc to close"
//     or "select a model". Neither had a fixture, and nothing in
//     testdata/antigravity shows the CLI rendering any of those overlays; both
//     were copies of Claude's rules written before the provider was harvested
//     (see testdata/antigravity/availability.txt, which records that no runtime
//     states were captured in Phase 1). A retain rule fires on a screen and then
//     holds the badge for SkipRetentionCap with no way to tell it is wrong, so a
//     speculative one is worse than none. If a capture of either overlay turns
//     up, it is one rule in the overlay file.
//   - the wider half of `antigravity.screen.blocked`, which alternated over
//     "Proceed", "Amend" and "enter.*Confirm" as well as the trust question.
//     Any one of those words anywhere in the last twenty-four lines called a
//     pane blocked; "Proceed" is a word an agent writes about its own plan.

// DetectAntigravity classifies an Antigravity pane. The process gate runs first
// and refuses before any manifest is evaluated; everything after it is
// upstream's, plus the two overlay rules.
func DetectAntigravity(ob Observation) Result {
	if ob.Agent != "antigravity" {
		return Result{State: StateUnknown, Evidence: "antigravity.process-mismatch"}
	}
	return DetectManifestResult(ob)
}

func antigravityProcess(command string) bool {
	return command == "agy" || command == "antigravity" || command == "node" || command == "bun"
}
