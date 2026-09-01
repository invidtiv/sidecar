package agentactivity

var piRules = []Rule{
	// Compatibility rule from Herdr pi manifest 2026.06.10.1. Pi was not
	// available for a real capture on the evidence machine.
	{ID: "pi.screen.working", State: StateWorking, Region: RegionCurrent, LastN: 12, Contains: []string{"Working..."}},
}

func DetectPi(ob Observation) Result {
	if ob.Agent != "pi" || !piProcess(ob.CurrentCommand) {
		return Result{State: StateUnknown, Evidence: "pi.process-mismatch"}
	}
	result := Evaluate(ob, piRules)
	if result.State == StateUnknown && !result.SkipStateUpdate {
		return Result{State: StateIdle, Evidence: "pi.known-live-fallback", FallbackIdle: true}
	}
	return result
}

// piProcess is the process gate for Pi. It is named rather than inlined so
// the manifest engine can apply exactly the same refusal without going through
// the Go rule table; see manifest_detect.go.
func piProcess(command string) bool { return command == "pi" }
