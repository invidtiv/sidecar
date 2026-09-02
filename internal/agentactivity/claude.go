package agentactivity

// Claude Code's screen rules are Herdr's, executed from the vendored
// `manifests/upstream/claude.toml` by the manifest engine (Phase 2 of
// docs/plans/active/herdr-detection-parity.md). This file is what remains that
// is Sidecar's: the process gate.
//
// The Go rule table that used to live here is gone. What it knew is now either
// upstream's — the braille title spinner is `osc_title_working`, which also
// covers the half-circle frames (U+25D0–U+25D3) Claude Code 2.1.228 switched to
// and Sidecar's hand-written pattern never learned — or an overlay rule in
// `manifests/sidecar/claude.toml`, which carries the one thing upstream is
// narrower on: an "Esc to close" overlay footer retains the prior state instead
// of reading as idle.

// DetectClaude classifies a Claude Code pane. The process gate runs first and
// refuses before any manifest is evaluated; everything after it is upstream's.
func DetectClaude(ob Observation) Result {
	if ob.Agent != "claude" {
		return Result{State: StateUnknown, Evidence: "claude.process-mismatch"}
	}
	result, _ := DetectManifest(ob)
	return result
}

// claudeProcess is Sidecar's refusal, and it is stricter than Herdr's: Herdr
// evaluates claude.toml against whatever is on a pane it believes is Claude,
// while Sidecar declines unless the foreground command is Claude itself or a
// permitted runtime wrapper. Claude renames its own process to its version
// string, which is why claudeVersionArgv0 is here.
func claudeProcess(command string) bool {
	return command == "claude" || command == "node" || command == "bun" ||
		claudeVersionArgv0(command)
}
