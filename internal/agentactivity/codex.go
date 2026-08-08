package agentactivity

import "regexp"

var (
	brailleSpinner = regexp.MustCompile(`[⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏]`)
	codexRules     = []Rule{
		{ID: "codex.title.blocked", State: StateBlocked, Region: RegionTitle, Contains: []string{"Action Required"}},
		{ID: "codex.screen.blocked", State: StateBlocked, Region: RegionLastLines, LastN: 18, Regexp: regexp.MustCompile(`(?i)(Action Required|Would you like to run|Press enter to confirm|Allow command)`)},
		{ID: "codex.viewer.retain", State: StateUnknown, Region: RegionScreen, Regexp: regexp.MustCompile(`(?m)(/ T R A N S C R I P T /|q to quit\s+esc to edit prev)`), Skip: true},
		{ID: "codex.title.working", State: StateWorking, Region: RegionTitle, Regexp: brailleSpinner},
		{ID: "codex.screen.working", State: StateWorking, Region: RegionLastLines, LastN: 12, Regexp: regexp.MustCompile(`Working \(.*esc to interrupt\)`)},
		{ID: "codex.screen.idle", State: StateIdle, Region: RegionLastLines, LastN: 8, Regexp: regexp.MustCompile(`(?m)^\s*›(?:\s|$)`)},
	}
)

// DetectCodex requires live process identity before evaluating Codex-owned UI.
// tmux commonly reports "node" for the npm-installed Codex launcher.
func DetectCodex(ob Observation) Result {
	if ob.Agent != "codex" || !codexProcess(ob.CurrentCommand) {
		return Result{State: StateUnknown, Evidence: "codex.process-mismatch"}
	}
	result := Evaluate(ob, codexRules)
	if result.State == StateUnknown && !result.SkipStateUpdate {
		return Result{State: StateIdle, Evidence: "codex.known-live-fallback"}
	}
	return result
}

func codexProcess(command string) bool {
	switch command {
	case "codex", "node", "bun", "codex-cli":
		return true
	default:
		return false
	}
}
