package agentactivity

import "regexp"

var piRules = []Rule{
	// Compatibility safety markers only: Pi was unavailable for capture. A
	// label requires a close hint so ordinary prompt text does not retain.
	{ID: "pi.overlay-or-interruption.retain", State: StateUnknown, Region: RegionCurrent, LastN: 12, Skip: true, Regexp: regexp.MustCompile(`(?ims)(\b(?:settings|help)\b.*(?:esc to close|q to quit)|(?:esc to close|q to quit).*\b(?:settings|help)\b|^\s*(?:turn )?interrupted\b)`)},
	// Compatibility rule from Herdr pi manifest 2026.06.10.1. Pi was not
	// available for a real capture on the evidence machine.
	{ID: "pi.screen.working", State: StateWorking, Region: RegionCurrent, LastN: 12, Contains: []string{"Working..."}},
}

func DetectPi(ob Observation) Result {
	if ob.Agent != "pi" || ob.CurrentCommand != "pi" {
		return Result{State: StateUnknown, Evidence: "pi.process-mismatch"}
	}
	result := Evaluate(ob, piRules)
	if result.State == StateUnknown && !result.SkipStateUpdate {
		return Result{State: StateIdle, Evidence: "pi.known-live-fallback"}
	}
	return result
}
