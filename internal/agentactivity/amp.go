package agentactivity

// Amp's screen rules are Herdr's, executed from the vendored
// `manifests/upstream/amp.toml` by the manifest engine (Phase 2 of
// docs/plans/active/herdr-detection-parity.md). This file is what remains that
// is Sidecar's: the process gate.
//
// The Go rule table is gone and upstream carries all six of its rules under its
// own ids, with the same patterns: the plugin-confirmation title blocker, the
// braille title spinner, the approval footer, the `╰ … thinking/streaming/running
// tools/waiting ─` status line, the "esc to cancel" hint, and the " - amp - "
// idle title. What Sidecar got from rule order — a blocked title beating a
// working title beating an idle title — upstream states as priorities 1100,
// 1050 and 50, and it adds the `not` gate on the idle title that Sidecar spelled
// as a rule-level exclusion.

// DetectAmp classifies an Amp pane. The process gate runs first and refuses
// before any manifest is evaluated; everything after it is upstream's.
func DetectAmp(ob Observation) Result {
	if ob.Agent != "amp" {
		return Result{State: StateUnknown, Evidence: "amp.process-mismatch"}
	}
	return DetectManifestResult(ob)
}

func oneOf(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if value == candidate {
			return true
		}
	}
	return false
}

// ampProcess is the process gate for Amp. Named for the reason on piProcess.
func ampProcess(command string) bool { return oneOf(command, "amp", "amp-local") }
