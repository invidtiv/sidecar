package agentactivity

// Copilot's screen rules are Herdr's, executed from the vendored
// `manifests/upstream/github-copilot.toml` by the manifest engine (Phase 2 of
// docs/plans/active/herdr-detection-parity.md). This file is what remains that
// is Sidecar's: the process gate.
//
// Copilot is the one provider whose Sidecar family name and Herdr manifest file
// name differ — the manifest is `github-copilot.toml` and its own id is
// "copilot" — which is what ManifestAgentID exists for.
//
// The Go rule table is gone and upstream is a superset of it. Sidecar's blocked
// rule was one regex requiring a cancel hint and a confirm hint in either order;
// upstream's `selection_blocker` is the same conjunction written as two `any`
// groups, and it knows two spellings Sidecar did not ("esc cancel", "enter
// accept"). Sidecar's working rule is upstream's `working_cancel_hint`, again
// with an extra spelling. Upstream also carries `background_agents_working`,
// which Sidecar had no equivalent for at all: a pane parked on "◎ Waiting for
// background agents" used to read idle and announce a completed turn.

// DetectCopilot classifies a Copilot pane. The process gate runs first and
// refuses before any manifest is evaluated; everything after it is upstream's.
func DetectCopilot(ob Observation) Result {
	if ob.Agent != "copilot" {
		return Result{State: StateUnknown, Evidence: "copilot.process-mismatch"}
	}
	return DetectManifestResult(ob)
}

// copilotProcess is the process gate for Copilot. Named for the reason on
// piProcess.
func copilotProcess(command string) bool {
	return oneOf(command, "copilot", "github-copilot", "ghcs")
}
