package agentactivity

import "regexp"

var ampRules = []Rule{
	// Compatibility rules from Herdr amp manifest 2026.07.09.1.
	{ID: "amp.title.plugin-blocked", State: StateBlocked, Region: RegionTitle, Contains: []string{"Plugin confirmation needed"}},
	{ID: "amp.title.working", State: StateWorking, Region: RegionTitle, Regexp: regexp.MustCompile(`^[⠀-⣿] `)},
	{ID: "amp.screen.blocked", State: StateBlocked, Region: RegionCurrent, LastN: 18, Regexp: regexp.MustCompile(`(?is)(waiting for approval|invoke tool|run this command\?|allow editing file:|allow creating file:|confirm tool call|approve.*(?:allow all for this session|allow all for every session|allow file for every session|deny with feedback))`)},
	{ID: "amp.screen.footer-working", State: StateWorking, Region: RegionCurrent, LastN: 5, Regexp: regexp.MustCompile(`(?im)^\s*╰\s+\S+\s+(thinking|streaming|running tools|waiting)\s+─`)},
	{ID: "amp.screen.cancel-working", State: StateWorking, Region: RegionCurrent, LastN: 10, Contains: []string{"esc to cancel"}},
	{ID: "amp.title.idle", State: StateIdle, Region: RegionTitle, Contains: []string{" - amp - "}, Not: []string{"Plugin confirmation needed"}},
}

func DetectAmp(ob Observation) Result {
	if ob.Agent != "amp" || !oneOf(ob.CurrentCommand, "amp", "amp-local") {
		return Result{State: StateUnknown, Evidence: "amp.process-mismatch"}
	}
	result := Evaluate(ob, ampRules)
	if result.State == StateUnknown {
		return Result{State: StateIdle, Evidence: "amp.known-live-fallback"}
	}
	return result
}

func oneOf(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if value == candidate {
			return true
		}
	}
	return false
}
