package agentactivity

import "regexp"

var openCodeRules = []Rule{
	// Compatibility safety markers only: OpenCode was unavailable for capture.
	{ID: "opencode.overlay-or-interruption.retain", State: StateUnknown, Region: RegionCurrent, LastN: 12, Skip: true, Regexp: regexp.MustCompile(`(?ims)(\b(?:help|settings)\b.*(?:esc to close|q to quit)|(?:esc to close|q to quit).*\b(?:help|settings)\b|^\s*(?:turn )?interrupted\b)`)},
	// Compatibility rules from Herdr opencode manifest 2026.06.10.1.
	{ID: "opencode.screen.blocked", State: StateBlocked, Region: RegionCurrent, LastN: 18, Regexp: regexp.MustCompile(`(?is)(△ Permission required|esc dismiss.*enter (?:confirm|submit|toggle).*(?:↑↓ select|⇆ tab))`)},
	{ID: "opencode.screen.interrupt-working", State: StateWorking, Region: RegionCurrent, LastN: 12, Regexp: regexp.MustCompile(`(?im)(esc to interrupt|ctrl\+c to interrupt|press esc to interrupt|^.*opencode.*esc (?:again to )?interrupt)`)},
	{ID: "opencode.screen.progress-working", State: StateWorking, Region: RegionCurrent, LastN: 12, Regexp: regexp.MustCompile(`(?:■|⬝){4,}`)},
}

func DetectOpenCode(ob Observation) Result {
	if ob.Agent != "opencode" || !oneOf(ob.CurrentCommand, "opencode", "open-code") {
		return Result{State: StateUnknown, Evidence: "opencode.process-mismatch"}
	}
	result := Evaluate(ob, openCodeRules)
	if result.State == StateUnknown && !result.SkipStateUpdate {
		return Result{State: StateIdle, Evidence: "opencode.known-live-fallback"}
	}
	return result
}
