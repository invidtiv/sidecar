package agentactivity

import "regexp"

var antigravityRules = []Rule{
	{ID: "antigravity.screen.resolved-idle", State: StateIdle, Region: RegionCurrent, LastN: 24, Regexp: regexp.MustCompile(`(?ims)(Generating\.\.\.|esc to cancel).*\? for shortcuts`)},
	{ID: "antigravity.screen.blocked", State: StateBlocked, Region: RegionCurrent, LastN: 24, Regexp: regexp.MustCompile(`(?im)(requesting permission for:|Do you trust the contents of this project\?|Yes, I trust this folder|Proceed|Amend|enter.*Confirm)`)},
	{ID: "antigravity.overlay.retain", State: StateUnknown, Region: RegionLastLines, LastN: 24, Regexp: regexp.MustCompile(`(?im)(esc to close|conversation history|transcript|select a model)`), Skip: true},
	{ID: "antigravity.screen.working", State: StateWorking, Region: RegionCurrent, LastN: 24, Regexp: regexp.MustCompile(`(?im)(Generating\.\.\.|esc to cancel|background tasks?:\s*[1-9]|[⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏⣽].*(?:ing|running|working))`)},
}

func DetectAntigravity(ob Observation) Result {
	if ob.Agent != "antigravity" || !antigravityProcess(ob.CurrentCommand) {
		return Result{State: StateUnknown, Evidence: "antigravity.process-mismatch"}
	}
	result := Evaluate(ob, antigravityRules)
	if result.State == StateUnknown && !result.SkipStateUpdate {
		return Result{State: StateIdle, Evidence: "antigravity.known-live-fallback", FallbackIdle: true}
	}
	return result
}

func antigravityProcess(command string) bool {
	return command == "agy" || command == "antigravity" || command == "node" || command == "bun"
}
