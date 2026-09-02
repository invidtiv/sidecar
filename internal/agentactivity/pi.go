package agentactivity

// Pi's screen rules are Herdr's, executed from the vendored
// `manifests/upstream/pi.toml` by the manifest engine (Phase 2 of
// docs/plans/active/herdr-detection-parity.md). This file is what remains that
// is Sidecar's: the process gate.
//
// Nothing was lost and nothing was gained. Upstream's pi.toml has exactly one
// rule, `working_literal` ("Working..." anywhere in the read window), and
// Sidecar's table was a transcription of it. Pi has never been available for a
// live capture on the evidence machine, so both sides of this cutover are the
// same manifest read twice.
//
// One region widened, as everywhere else: Sidecar read the bottom twelve lines
// and upstream reads `whole_recent`, the whole read window.

// DetectPi classifies a Pi pane. The process gate runs first and refuses before
// any manifest is evaluated; everything after it is upstream's.
func DetectPi(ob Observation) Result {
	if ob.Agent != "pi" {
		return Result{State: StateUnknown, Evidence: "pi.process-mismatch"}
	}
	return DetectManifestResult(ob)
}

// piProcess is the process gate for Pi. It is named rather than inlined so
// the manifest engine can apply exactly the same refusal without going through
// a Go rule table; see manifest_detect.go.
func piProcess(command string) bool { return command == "pi" }
