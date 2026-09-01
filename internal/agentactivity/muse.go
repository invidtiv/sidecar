package agentactivity

import (
	"regexp"
	"strings"
)

// Muse Spark CLI activity evidence.
//
// Harvested from Muse Code 1.0.1 live tmux captures (2026-08-31,
// darwin/arm64, echo provider with --echo-delay-ms to expose working
// transitions). Pane title drives a braille spinner while a turn is
// active (U+2800–U+28FF block), and the screen shows a status line
// with "Thinking (Ns · esc to interrupt)" or "Working…". Idle is
// marked by the composer prompt "⟩" at the bottom with no cancel
// hint. These patterns are provider-owned and not shared with
// Codex/Claude/Grok spinners, even where they appear similar.

var (
	museTitleWorking = regexp.MustCompile(`^[\x{2800}-\x{28FF}]\s`)

	museSpinnerWorking = regexp.MustCompile(`(?i)Thinking|Working`)

	museRules = []Rule{
		// Blocked / approval: provider-owned permission prompts.
		{
			ID:     "muse.screen.blocked",
			State:  StateBlocked,
			Region: RegionCurrent,
			LastN:  24,
			Regexp: regexp.MustCompile(`(?im)(Do you want to proceed\?|Allow command\?|Would you like to run|Proceed\?|Yes.*No|Enter to confirm.*Esc to cancel|Allow.*\?|approve)`),
		},
		// Overlays & modals — retain prior state.
		{
			ID:     "muse.overlay.retain",
			State:  StateUnknown,
			Region: RegionLastLines,
			LastN:  24,
			Regexp: regexp.MustCompile(`(?im)(esc to close|model picker|select.*model)`),
			Skip:   true,
		},
		// Title spinner: braille frame + space + project name while working.
		{
			ID:     "muse.title.working",
			State:  StateWorking,
			Region: RegionTitle,
			Regexp: museTitleWorking,
		},
		// Screen working: Thinking / Working with cancel hint or spinner glyph.
		{
			ID:     "muse.screen.working",
			State:  StateWorking,
			Region: RegionCurrent,
			LastN:  16,
			Regexp: museSpinnerWorking,
		},
		{
			ID:     "muse.screen.cancel-working",
			State:  StateWorking,
			Region: RegionCurrent,
			LastN:  16,
			Regexp: regexp.MustCompile(`(?i)esc to interrupt`),
		},
		{
			ID:     "muse.screen.thinking-glyph",
			State:  StateWorking,
			Region: RegionCurrent,
			LastN:  16,
			Regexp: regexp.MustCompile(`◈\s*Thinking`),
		},
		// Explicit idle: composer prompt at bottom without cancel hint.
		{
			ID:     "muse.screen.idle",
			State:  StateIdle,
			Region: RegionLastLines,
			LastN:  12,
			Regexp: regexp.MustCompile(`(?m)^⟩(?:\s| |$)`),
			Not:    []string{"esc to interrupt"},
		},
	}
)

func DetectMuse(ob Observation) Result {
	if ob.Agent != "muse" || !museProcess(ob.CurrentCommand) {
		return Result{State: StateUnknown, Evidence: "muse.process-mismatch"}
	}
	result := Evaluate(ob, museRules)
	if result.State == StateUnknown && !result.SkipStateUpdate {
		return Result{State: StateIdle, Evidence: "muse.known-live-fallback", FallbackIdle: true}
	}
	return result
}

func museProcess(command string) bool {
	return command == "muse" || strings.HasPrefix(command, "muse-")
}
