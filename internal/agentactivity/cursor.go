package agentactivity

import (
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// Cursor Agent CLI activity evidence.
//
// Provenance (no live Cursor subscription required for this harvest):
//   - Herdr screen manifest 2026.08.03.1 (bundled + herdr.dev remote).
//   - Static string harvest of cursor-cli 2026.08.04-aaa8809
//     (agent-cli-package.tar.gz index/chunks; process never launched).
//
// Cursor's launcher is a bash wrapper that re-execs its bundled node with
// `exec -a "$0"`. tmux may therefore report `cursor-agent`, `agent`, or the
// shared runtime `node`. Identity follows Herdr: process name or a foreground
// argv[0] whose `agent` symlink resolves to cursor-agent, not activity phrases.
// A bare node remains ambiguous; only ProcessIdentity can make it Cursor.
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
	cursorWriteBlocked  = regexp.MustCompile(`(?is)write to this file\?.*(proceed \(y\)|reject & propose changes|esc or n or p|add write\(|add to allowlist)`)
	cursorDeleteBlocked = regexp.MustCompile(`(?is)delete this file\?.*(delete \(y\)|keep \(n\)|reject & propose|esc or n)`)
	// Shell/MCP body: prompt phrase near action chrome. Kept separate from
	// line-anchored `(y)` forms so "Run Everything" menu chrome cannot win
	// (Herdr fae0b236).
	cursorShellBlocked     = regexp.MustCompile(`(?is)(waiting for approval|run this command(?: outside the sandbox)?\?|run this mcp tool\?).{0,240}(run \(once\)|run outside sandbox \(once\)|skip \(esc or n\)|skip & tell the agent|reject & propose|allowlist)`)
	cursorShellBlockedLine = regexp.MustCompile(`(?im)^\s*(?:→\s*)?run .*\(y\)|^\s*allow .*\(y\)|\(y\) \(enter\)|keep \(n\)|skip \(esc or n\)`)
	cursorWebBlocked       = regexp.MustCompile(`(?is)(allow this web (?:search|fetch)\?|proceed with this edit\?).{0,160}(allow search|fetch \(y\)|proceed \(y\)|skip \(esc or n\)|reject)`)
	cursorDecisionBlocked  = regexp.MustCompile(`(?im)waiting for decision \(y/n/p\)|approve mode switch \(y/n\)|waiting for confirmation|enter password\.\.\.`)
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

// cursorScreenIdentity is a last-resort claim for the `agent` comm name
// when PATH does not resolve the binary. Herdr identifies Cursor from the
// process / symlink target only (never from screen). These phrases are the
// header/tagline, not activity hints that Codex and Grok also print.
var cursorScreenIdentity = regexp.MustCompile(`(?is)(Cursor Agent|Plan, search, build anything)`)

// lookUpAgentAlias reports the provider for a bare `agent` comm name by
// resolving $PATH once, matching Herdr's cursor-agent symlink test. Empty
// means "not Cursor." Tests replace this so Identify is not host-dependent.
var lookUpAgentAlias = lookupCursorAgentAlias

var (
	agentAliasOnce sync.Once
	agentAliasID   string
)

func lookupCursorAgentAlias() string {
	agentAliasOnce.Do(func() {
		path, err := exec.LookPath("agent")
		if err != nil {
			return
		}
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			resolved = path
		}
		switch strings.ToLower(filepath.Base(resolved)) {
		case "cursor-agent", "cursor-agent.cmd", "cursor":
			agentAliasID = "cursor"
		}
	})
	return agentAliasID
}

func DetectCursor(ob Observation) Result {
	if ob.Agent != "cursor" || (!cursorProcess(ob.CurrentCommand) && ob.ProcessIdentity != "cursor") {
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
	case "cursor-agent", "cursor", "cursor-agent.cmd", "agent":
		return true
	default:
		return false
	}
}
