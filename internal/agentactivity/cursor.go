package agentactivity

import (
	"regexp"
	"strings"
)

// Cursor Agent CLI activity evidence.
//
// Provenance (no live Cursor subscription required for this harvest):
//   - Herdr screen manifest 2026.08.03.1 (bundled + herdr.dev remote).
//   - Static string harvest of cursor-cli 2026.08.04-aaa8809
//     (agent-cli-package.tar.gz index/chunks; process never launched).
//
// Cursor's launcher is a bash wrapper that re-execs its bundled node with
// `exec -a "$0"`, so pane_current_command is typically `cursor-agent` (or
// `agent` when that is the install name / symlink). Bare `agent` is also
// used by other tools (e.g. Grok), so Identify only accepts it with Cursor
// screen chrome, and DetectCursor only accepts shared runtimes when the
// observation is already labeled cursor.
//
// Status authority is the live bottom buffer. Session hooks report identity
// only (Herdr's model); do not reintroduce SQLite mtime as activity.
var (
	// Spinner + activity verb. Marketing demos use past-tense + duration
	// ("⬢ Thought 3s"); the interactive UI uses Thinking / Working… /
	// Running… / Searching / Finding / Listing next to a ⬡/⬢ or braille frame.
	cursorSpinnerWorking = regexp.MustCompile(`(?m)^\s*(?:⬡|⬢|[⠀-⣿]+)\s+(?:\p{L}+\w*ing\b|\p{L}+(?:\s+\d+\s+\w+){0,4}\s+\d+s?\b|Thinking|Working|Running|Searching|Finding|Listing|Composing|Planning|Summarizing)\b`)
	// Explicit tool/turn chrome without requiring the spinner glyph (the
	// glyph can land on a different line after capture-pane reflow).
	cursorScreenWorking = regexp.MustCompile(`(?im)ctrl\+c to stop|waiting for subagent to finish|running subagent|running command|running:\s+\S|working…|working\.\.\.|starting…|starting\.\.\.|^\s*Running…\s*$|^\s*Thinking\s*$|^\s*Working…\s*$`)
	// Live background work suffix on the status line. "N background tasks"
	// alone is a *Finished* completion group title in the 2026.08.04 CLI and
	// must not flip a pane to working.
	cursorBackgroundWorking = regexp.MustCompile(`(?i)\(\s*background\s*\)`)
	// Approval prompts. Herdr's write/command rules plus additional 2026.08.04
	// operations (delete, web, MCP, edit, mode switch, decision, sudo).
	cursorWriteBlocked = regexp.MustCompile(`(?is)write to this file\?.*(proceed \(y\)|reject & propose changes|esc or n or p|add write\(|add to allowlist)`)
	cursorDeleteBlocked = regexp.MustCompile(`(?is)delete this file\?.*(delete \(y\)|keep \(n\)|reject & propose|esc or n)`)
	// Shell/MCP body: prompt phrase near action chrome. Kept separate from
	// line-anchored `(y)` forms so "Run Everything" menu chrome cannot win
	// (Herdr fae0b236).
	cursorShellBlocked = regexp.MustCompile(`(?is)(waiting for approval|run this command(?: outside the sandbox)?\?|run this mcp tool\?).{0,240}(run \(once\)|run outside sandbox \(once\)|skip \(esc or n\)|skip & tell the agent|reject & propose|allowlist)`)
	cursorShellBlockedLine = regexp.MustCompile(`(?im)^\s*(?:→\s*)?run .*\(y\)|^\s*allow .*\(y\)|\(y\) \(enter\)|keep \(n\)|skip \(esc or n\)`)
	cursorWebBlocked = regexp.MustCompile(`(?is)(allow this web (?:search|fetch)\?|proceed with this edit\?).{0,160}(allow search|fetch \(y\)|proceed \(y\)|skip \(esc or n\)|reject)`)
	cursorDecisionBlocked = regexp.MustCompile(`(?im)waiting for decision \(y/n/p\)|approve mode switch \(y/n\)|waiting for confirmation|enter password\.\.\.`)
)

var cursorRules = []Rule{
	// --- blocked (high priority) ---
	// Compatibility: Herdr write_file_approval 2026.08.03.1
	{ID: "cursor.screen.write-blocked", State: StateBlocked, Region: RegionCurrent, LastN: 10, Regexp: cursorWriteBlocked},
	// 2026.08.04 CLI: Delete this file? / Keep (n) / Delete (y)
	{ID: "cursor.screen.delete-blocked", State: StateBlocked, Region: RegionCurrent, LastN: 10, Regexp: cursorDeleteBlocked},
	// Compatibility: Herdr approval_prompt body + MCP / sandbox variants.
	{ID: "cursor.screen.approval-blocked", State: StateBlocked, Region: RegionCurrent, LastN: 18, Regexp: cursorShellBlocked},
	// 2026.08.04 CLI: web search/fetch and edit confirmation (before the
	// generic line-anchored skip/allow forms, which also appear here).
	{ID: "cursor.screen.web-edit-blocked", State: StateBlocked, Region: RegionCurrent, LastN: 12, Regexp: cursorWebBlocked},
	// Decision overlays and sudo password banner.
	{ID: "cursor.screen.decision-blocked", State: StateBlocked, Region: RegionCurrent, LastN: 10, Regexp: cursorDecisionBlocked},
	// Compatibility: Herdr line-anchored allow/run (y) controls. Last among
	// blockers so specific prompt shapes keep their evidence IDs.
	{ID: "cursor.screen.approval-line-blocked", State: StateBlocked, Region: RegionCurrent, LastN: 18, Regexp: cursorShellBlockedLine},

	// --- working ---
	// Compatibility: Herdr stop_hint_working. Also the strongest live signal:
	// the composer rightPlaceholder is "ctrl+c to stop" only while generating.
	{ID: "cursor.screen.stop-working", State: StateWorking, Region: RegionCurrent, LastN: 8, Contains: []string{"ctrl+c to stop"}},
	// Live background status suffix, not the Finished completion group.
	{ID: "cursor.screen.background-working", State: StateWorking, Region: RegionCurrent, LastN: 6, Regexp: cursorBackgroundWorking, Not: []string{"Finished"}},
	// Compatibility: Herdr spinner_working, expanded for past-tense demos and
	// explicit Thinking/Working/Running labels.
	{ID: "cursor.screen.spinner-working", State: StateWorking, Region: RegionCurrent, LastN: 10, Regexp: cursorSpinnerWorking},
	// Tool-step chrome without a spinner on the same capture line.
	{ID: "cursor.screen.activity-working", State: StateWorking, Region: RegionCurrent, LastN: 10, Regexp: cursorScreenWorking},
}

// cursorScreenIdentity is distinctive live chrome, not session-file residue.
// Used only when the process name is a shared runtime (node/agent). Keep this
// stricter than activity rules: a false identity steals the pane from another
// tool that also uses `agent` or `node`.
var cursorScreenIdentity = regexp.MustCompile(`(?is)(Cursor Agent|Plan, search, build anything|Add a follow-up|Reject & propose changes|Write to this file\?|Delete this file\?|ctrl\+c to stop|Waiting for decision \(y/n/p\)|Waiting for approval|Run this command\?|Run this MCP tool\?|Allow this web (?:search|fetch)\?|Proceed with this edit\?)`)

func DetectCursor(ob Observation) Result {
	if ob.Agent != "cursor" || !cursorProcess(ob.CurrentCommand) {
		return Result{State: StateUnknown, Evidence: "cursor.process-mismatch"}
	}
	result := Evaluate(ob, cursorRules)
	if result.State == StateUnknown && !result.SkipStateUpdate {
		return Result{State: StateIdle, Evidence: "cursor.known-live-fallback", FallbackIdle: true}
	}
	return result
}

func cursorProcess(command string) bool {
	switch strings.ToLower(strings.TrimSpace(command)) {
	case "cursor-agent", "cursor", "cursor-agent.cmd", "agent", "node":
		return true
	default:
		return false
	}
}
