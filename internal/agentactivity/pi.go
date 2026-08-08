package agentactivity

var piRules = []Rule{
	// Compatibility rule from Herdr pi manifest 2026.06.10.1. Pi was not
	// available for a real capture on the evidence machine.
	{ID: "pi.screen.working", State: StateWorking, Region: RegionCurrent, LastN: 12, Contains: []string{"Working..."}},
}

func DetectPi(ob Observation) Result {
	if ob.Agent != "pi" || ob.CurrentCommand != "pi" {
		return Result{State: StateUnknown, Evidence: "pi.process-mismatch"}
	}
	result := Evaluate(ob, piRules)
	if result.State == StateUnknown {
		return Result{State: StateIdle, Evidence: "pi.known-live-fallback"}
	}
	return result
}
