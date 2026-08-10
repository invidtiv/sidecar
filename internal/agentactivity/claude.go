package agentactivity

import "regexp"

// Claude Code drives its terminal title as a liveness signal: a braille frame
// while a turn or a background agent is running, ✳ once everything has
// finished. It cycles the whole Braille block rather than the ten-frame dots
// set the other providers use, so it needs its own patterns — matching only
// the shared set let frames like U+2810 fall through to the prompt-box idle
// rule while subagents were still working, which the tracker then reported as
// a completed turn. Both are anchored so braille elsewhere in a title, or a
// task name that merely starts with one, cannot pass for a spinner.
var (
	claudeTitleWorking = regexp.MustCompile(`^[\x{2800}-\x{28FF}]\s`)
	claudeTitleIdle    = regexp.MustCompile(`^\x{2733}\s`)
)

var claudeRules = []Rule{
	{ID: "claude.screen.resolved-idle", State: StateIdle, Region: RegionCurrent, LastN: 24, Regexp: regexp.MustCompile(`(?ims)(Enter to (?:confirm|select).*Esc to cancel|↑/↓ to navigate).*^❯(?:\s| )*$`)},
	{ID: "claude.screen.blocked", State: StateBlocked, Region: RegionCurrent, LastN: 24, Regexp: regexp.MustCompile(`(?im)(Do you want to proceed\?|Allow .*\?|Yes, allow|Yes, and don't ask again|Enter to (?:confirm|select).*Esc to cancel|↑/↓ to navigate)`)},
	// The transcript viewer is gated on its own chrome. Matching a bare
	// "transcript" anywhere in the window froze the badge whenever a turn
	// merely discussed one, and a retained state has no expiry of its own.
	{ID: "claude.overlay.transcript", State: StateUnknown, Region: RegionCurrent, LastN: 6, Contains: []string{"showing detailed transcript"}, Any: [][]string{
		{"ctrl+o", "to toggle"},
		{"ctrl+e", "show all"},
		{"ctrl+e", "collapse"},
		{"↑↓ scroll"},
		{"? for shortcuts"},
	}, Skip: true},
	{ID: "claude.overlay.retain", State: StateUnknown, Region: RegionLastLines, LastN: 24, Regexp: regexp.MustCompile(`(?im)(esc to close|Enter to select.*Esc to cancel|model picker)`), Skip: true},
	{ID: "claude.title.working", State: StateWorking, Region: RegionTitle, Regexp: claudeTitleWorking},
	{ID: "claude.screen.working", State: StateWorking, Region: RegionLastLines, LastN: 16, Regexp: regexp.MustCompile(`(?im)(esc to interrupt|esc to cancel|Thinking…|Churning…|Working…|Running…)`)},
	{ID: "claude.screen.idle", State: StateIdle, Region: RegionLastLines, LastN: 12, Regexp: regexp.MustCompile(`(?m)^❯(?:\s| |$)`), Not: []string{"esc to interrupt", "esc to cancel"}},
	{ID: "claude.title.idle", State: StateIdle, Region: RegionTitle, Regexp: claudeTitleIdle},
}

func DetectClaude(ob Observation) Result {
	if ob.Agent != "claude" || !claudeProcess(ob.CurrentCommand) {
		return Result{State: StateUnknown, Evidence: "claude.process-mismatch"}
	}
	result := Evaluate(ob, claudeRules)
	if result.State == StateUnknown && !result.SkipStateUpdate {
		return Result{State: StateIdle, Evidence: "claude.known-live-fallback", FallbackIdle: true}
	}
	return result
}

func claudeProcess(command string) bool {
	return command == "claude" || command == "node" || command == "bun" ||
		semanticVersionCommand.MatchString(command)
}
