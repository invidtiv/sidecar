package agentactivity

import "regexp"

var copilotRules = []Rule{
	// Compatibility rules from Herdr copilot manifest 2026.07.07.1.
	{ID: "copilot.screen.blocked", State: StateBlocked, Region: RegionCurrent, LastN: 12, Regexp: regexp.MustCompile(`(?is)((esc (?:to )?cancel).*?(enter (?:to )?(?:select|confirm|submit|accept))|(enter (?:to )?(?:select|confirm|submit|accept)).*?(esc (?:to )?cancel))`)},
	{ID: "copilot.screen.working", State: StateWorking, Region: RegionCurrent, LastN: 8, Regexp: regexp.MustCompile(`(?i)esc (?:to cancel|cancel|again to cancel|interrupt)`)},
}

func DetectCopilot(ob Observation) Result {
	if ob.Agent != "copilot" || !oneOf(ob.CurrentCommand, "copilot", "github-copilot", "ghcs") {
		return Result{State: StateUnknown, Evidence: "copilot.process-mismatch"}
	}
	result := Evaluate(ob, copilotRules)
	if result.State == StateUnknown && !result.SkipStateUpdate {
		return Result{State: StateIdle, Evidence: "copilot.known-live-fallback", FallbackIdle: true}
	}
	return result
}
