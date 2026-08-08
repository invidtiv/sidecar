package agentactivity

import (
	"regexp"
	"strings"
)

var grokRules = []Rule{
	{ID: "grok.title.blocked", State: StateBlocked, Region: RegionTitle, Contains: []string{"Action Required"}},
	{ID: "grok.screen.blocked", State: StateBlocked, Region: RegionLastLines, LastN: 22, Regexp: regexp.MustCompile(`(?im)(Action Required|Would you like to|Allow .*\?|Enter to confirm|↑/↓.*(?:select|navigate))`)},
	{ID: "grok.overlay.retain", State: StateUnknown, Region: RegionLastLines, LastN: 24, Regexp: regexp.MustCompile(`(?im)(esc to close|resume session|transcript|conversation history)`), Skip: true},
	{ID: "grok.title.working", State: StateWorking, Region: RegionTitle, Regexp: brailleSpinner},
	{ID: "grok.screen.working", State: StateWorking, Region: RegionLastLines, LastN: 16, Regexp: regexp.MustCompile(`(?im)(\[stop\]|esc to (?:interrupt|cancel)|background tasks?:\s*[1-9]|Thinking…)`)},
	{ID: "grok.screen.idle", State: StateIdle, Region: RegionLastLines, LastN: 10, Regexp: regexp.MustCompile(`(?m)^\s*│ ❯\s+│|Ctrl\+x.*shortcuts`)},
}

func DetectGrok(ob Observation) Result {
	if ob.Agent != "grok" || !grokProcess(ob.CurrentCommand) {
		return Result{State: StateUnknown, Evidence: "grok.process-mismatch"}
	}
	result := Evaluate(ob, grokRules)
	if result.State == StateUnknown && !result.SkipStateUpdate && strings.HasSuffix(strings.ToLower(ob.PaneTitle), "grok") {
		return Result{State: StateIdle, Evidence: "grok.title.idle", VisibleIdle: true}
	}
	if result.State == StateUnknown && !result.SkipStateUpdate {
		return Result{State: StateIdle, Evidence: "grok.known-live-fallback", FallbackIdle: true}
	}
	return result
}

func grokProcess(command string) bool {
	return command == "grok" || command == "node" || command == "bun" || strings.HasPrefix(command, "grok-")
}
