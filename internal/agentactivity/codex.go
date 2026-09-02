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
// a resolved historical prompt in the scrollback from producing a blocker. The
// one thing upstream does not model is a turn parked on a background terminal;
// that rule lives on in `manifests/sidecar/codex.toml`.
//
// One consequence is worth naming. Sidecar had an explicit composer-idle rule
// (`^\s*›`); upstream reaches idle through `osc_title_idle`, which asks only
// that the title be non-empty and carry no spinner. Under tmux a pane title is
// effectively always non-empty, so this is the same verdict in practice — but a
// pane whose title is genuinely empty now resolves idle through the fallback
// instead, which is FallbackIdle and therefore cannot announce a completed
// turn. That is the conservative direction, and it is upstream's call to make.

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
