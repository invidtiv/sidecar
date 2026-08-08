package agentactivity

import "regexp"

var cursorRules = []Rule{
	// Compatibility rules from Herdr cursor manifest 2026.08.03.1.
	{ID: "cursor.screen.write-blocked", State: StateBlocked, Region: RegionCurrent, LastN: 8, Regexp: regexp.MustCompile(`(?is)write to this file\?.*proceed \(y\).*(reject & propose changes|esc or n or p|add write\()`)},
	{ID: "cursor.screen.approval-blocked", State: StateBlocked, Region: RegionCurrent, LastN: 16, Regexp: regexp.MustCompile(`(?im)(waiting for approval.*run this command\?.*(run \(once\) \(y\)|skip \(esc or n\))|\(y\) \(enter\)|^\s*allow .*\(y\)|keep \(n\)|skip \(esc or n\)|^\s*(?:→\s*)?run .*\(y\))`)},
	{ID: "cursor.screen.stop-working", State: StateWorking, Region: RegionCurrent, LastN: 6, Contains: []string{"ctrl+c to stop"}},
	{ID: "cursor.screen.background-working", State: StateWorking, Region: RegionCurrent, LastN: 5, Regexp: regexp.MustCompile(`(?i)[1-9][0-9]* background tasks?`)},
	{ID: "cursor.screen.spinner-working", State: StateWorking, Region: RegionCurrent, LastN: 8, Regexp: regexp.MustCompile(`(?m)^\s*(?:⬡|⬢|[⠀-⣿]+)\s+[[:alpha:]]+\w*ing\b`)},
}

func DetectCursor(ob Observation) Result {
	if ob.Agent != "cursor" || !oneOf(ob.CurrentCommand, "cursor-agent", "cursor") {
		return Result{State: StateUnknown, Evidence: "cursor.process-mismatch"}
	}
	result := Evaluate(ob, cursorRules)
	if result.State == StateUnknown {
		return Result{State: StateIdle, Evidence: "cursor.known-live-fallback"}
	}
	return result
}
