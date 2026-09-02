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

// piProcess is Pi's no-identity process gate — what Sidecar will accept from
// `pane_current_command` alone. It is named rather than inlined so the manifest
// engine can apply exactly the same refusal without going through a Go rule
// table; see manifest_detect.go.
//
// It deliberately does not allow a bare `node`, even though Pi 0.84.3 installs
// as a `#!/usr/bin/env node` shim and that shim is precisely what made
// `sidecar agent start --kind pi` time out. Allowing it would trade one refusal
// for a worse claim: upstream's pi.toml has a single rule, `working_literal`,
// which is the literal "Working..." anywhere in the read window, so a `node`
// allowance would let Pi's manifest report *any* Node pane that ever prints
// that word as working, and let every Pi pane's fallback report a Node pane as
// idle. What reaches a Pi shim instead is processGate's identity rule, on the
// evidence of the pane's own foreground argv.
//
// The residual is stated rather than hidden: on a platform with no
// process-identity adapter nothing resolves, so a shim-installed Pi pane is
// still refused there. That is a missing badge on those platforms, which is the
// side of this trade Sidecar has taken everywhere else in this gate.
func piProcess(command string) bool { return command == "pi" }
