package agentactivity

// OpenCode's screen rules are Herdr's, executed from the vendored
// `manifests/upstream/opencode.toml` by the manifest engine (Phase 2 of
// docs/plans/active/herdr-detection-parity.md). This file is what remains that
// is Sidecar's: the process gate.
//
// The Go rule table that used to live here is gone, and nothing was lost. Its
// three rules were transcribed from this very manifest at version 2026.06.10.1
// and upstream still carries all three: `permission_required` (the `△
// Permission required` banner and the dismiss/confirm footer pair),
// `interrupt_hint_working` (the four interrupt hints), and
// `progress_bar_working` (four or more progress blocks). No overlay is needed.
//
// One region widened. Sidecar read the bottom twelve or eighteen lines;
// upstream reads `whole_recent`, which is the whole read window — the pane's own
// height, twenty-four when tmux cannot report it. A permission banner sitting
// fifteen rows up now reaches the rule, which is the direction that matters: the
// window is already bounded to what the pane can display, so nothing scrolled
// away can win.

// DetectOpenCode classifies an OpenCode pane. The process gate runs first and
// refuses before any manifest is evaluated; everything after it is upstream's.
func DetectOpenCode(ob Observation) Result {
	if ob.Agent != "opencode" {
		return Result{State: StateUnknown, Evidence: "opencode.process-mismatch"}
	}
	return DetectManifestResult(ob)
}

// openCodeProcess is the process gate for OpenCode. Named for the reason on
// piProcess.
func openCodeProcess(command string) bool { return oneOf(command, "opencode", "open-code") }
