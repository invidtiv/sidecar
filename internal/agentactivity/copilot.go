package agentactivity

import "regexp"

var copilotRules = []Rule{
	// Compatibility rules from Herdr copilot manifest 2026.07.07.1.
	{ID: "copilot.screen.blocked", State: StateBlocked, Region: RegionCurrent, LastN: 12, Regexp: regexp.MustCompile(`(?is)((esc (?:to )?cancel).*?(enter (?:to )?(?:select|confirm|submit|accept))|(enter (?:to )?(?:select|confirm|submit|accept)).*?(esc (?:to )?cancel))`)},
	{ID: "copilot.screen.working", State: StateWorking, Region: RegionCurrent, LastN: 8, Regexp: regexp.MustCompile(`(?i)esc (?:to cancel|cancel|again to cancel|interrupt)`)},
}

func DetectCopilot(ob Observation) Result {
	if ob.Agent != "copilot" || !copilotProcess(ob.CurrentCommand) {
		return Result{State: StateUnknown, Evidence: "copilot.process-mismatch"}
	}
	result := Evaluate(ob, copilotRules)
	if result.State == StateUnknown && !result.SkipStateUpdate {
		return Result{State: StateIdle, Evidence: "copilot.known-live-fallback", FallbackIdle: true}
	}
	return result
}

// copilotProcess is the process gate for Copilot. Named for the reason on
// piProcess.
func copilotProcess(command string) bool {
	return oneOf(command, "copilot", "github-copilot", "ghcs")
}
