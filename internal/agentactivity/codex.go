package agentactivity

// Codex's screen rules are Herdr's, executed from the vendored
// `manifests/upstream/codex.toml` by the manifest engine (Phase 2 of
// docs/plans/active/herdr-detection-parity.md). This file is what remains that
// is Sidecar's: the process gate.
//
// The Go rule table that used to live here is gone. Upstream carries every
// signal it had — the ten-frame dots title spinner is `osc_title_working`, the
// approval prompt is `live_strong_blocker`, the transcript viewer is
// `transcript_viewer` — plus rules Sidecar never had: the trust-directory
// prompt at the top of the buffer, and prompt-marker-relative regions that stop
// a resolved historical prompt in the scrollback from producing a blocker.
//
// Four signals did not survive upstream unchanged, and all four live on in
// `manifests/sidecar/codex.toml` rather than here: a turn parked on a
// background terminal, which upstream does not model at all; the composer-idle
// rule, because upstream reaches idle through `osc_title_idle` and that rule's
// whole matcher is `\S` on a title tmux seeds with the host name, so under
// tmux it is satisfied on essentially every pane and turns "no rule matched"
// into "explicitly, visibly idle"; the `• Working (… esc to interrupt)` status
// line, because upstream reads it three non-empty lines deep and one tool
// call's output pushes it to the fourth; and the approval prompt whose option
// line carries the composer glyph, which empties both of upstream's
// prompt-marker-relative regions. The overlay states the priority each sits at
// and why, and what would make it deletable.

// DetectCodex classifies a Codex pane. The process gate runs first and refuses
// before any manifest is evaluated; everything after it is upstream's. tmux
// commonly reports "node" for the npm-installed Codex launcher, which is why
// the gate is a set rather than an equality.
func DetectCodex(ob Observation) Result {
	if ob.Agent != "codex" {
		return Result{State: StateUnknown, Evidence: "codex.process-mismatch"}
	}
	return DetectManifestResult(ob)
}

func codexProcess(command string) bool {
	switch command {
	case "codex", "node", "bun", "codex-cli":
		return true
	default:
		return false
	}
}
