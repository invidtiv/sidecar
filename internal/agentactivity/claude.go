package agentactivity

import "regexp"

var claudeRules = []Rule{
	{ID: "claude.screen.blocked", State: StateBlocked, Region: RegionLastLines, LastN: 22, Regexp: regexp.MustCompile(`(?im)(Do you want to proceed\?|Allow .*\?|Yes, allow|Yes, and don't ask again|Enter to (?:confirm|select).*Esc to cancel|↑/↓ to navigate)`)},
	{ID: "claude.overlay.retain", State: StateUnknown, Region: RegionLastLines, LastN: 24, Regexp: regexp.MustCompile(`(?im)(esc to close|Enter to select.*Esc to cancel|model picker|transcript)`), Skip: true},
	{ID: "claude.title.working", State: StateWorking, Region: RegionTitle, Regexp: brailleSpinner},
	{ID: "claude.screen.working", State: StateWorking, Region: RegionLastLines, LastN: 16, Regexp: regexp.MustCompile(`(?im)(esc to interrupt|esc to cancel|Thinking…|Churning…|Working…|Running…)`)},
	{ID: "claude.screen.idle", State: StateIdle, Region: RegionLastLines, LastN: 12, Regexp: regexp.MustCompile(`(?m)^❯(?:\s| |$)`), Not: []string{"esc to interrupt", "esc to cancel"}},
}

func DetectClaude(ob Observation) Result {
	if ob.Agent != "claude" || !claudeProcess(ob.CurrentCommand) {
		return Result{State: StateUnknown, Evidence: "claude.process-mismatch"}
	}
	result := Evaluate(ob, claudeRules)
	if result.State == StateUnknown && !result.SkipStateUpdate {
		return Result{State: StateIdle, Evidence: "claude.known-live-fallback"}
	}
	return result
}

func claudeProcess(command string) bool {
	return command == "claude" || command == "node" || command == "bun" ||
		regexp.MustCompile(`^\d+\.\d+\.\d+$`).MatchString(command)
}
