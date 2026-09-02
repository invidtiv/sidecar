package agentactivity

import (
	"regexp"
	"strings"
)

// Grok's screen rules are Herdr's, executed from the vendored
// `manifests/upstream/grok.toml` (2026.07.16.2, engine 3) by the manifest
// engine, with four Sidecar rules layered over them in
// `manifests/sidecar/grok.toml`. This file is what remains that is Sidecar's:
// the process gate and the identity fallback Identify uses.
//
// The Go probe that used to live here was the most hand-tuned in the package —
// three ordered rule tables plus a bespoke resolver that ran footer chrome
// before any title decision. Upstream carries the same findings and states them
// as priorities: `osc_title_blocked` is the Action Required title,
// `option_dialog_blocked` the ┃-guttered choice list, `background_work_chip_working`
// the pinned top-row chip, `spinner_status_working` the braille+[stop] status
// line, `waiting_tool_working` the pre-0.2 tool chrome, `osc_title_idle` and
// `prompt_hints_idle` the two idle shapes. What upstream does not have — the
// footer outranking a lagging title in both directions, the parked turn with
// live subagents, the resume overlay — is in the overlay, each with a fixture.
//
// Two rules were dropped outright rather than carried:
//
//   - `grok.overlay.viewer` retained state whenever "transcript" or
//     "conversation history" appeared beside viewer chrome. It never had a
//     fixture, and no capture in testdata/grok shows Grok rendering such a
//     viewer at all; it was a copy of Claude's rule. A retain rule that fires on
//     a screen the provider may not paint freezes a badge for SkipRetentionCap
//     on nothing, which is worse than the gap. TestViewerWordsWithoutChromeDoNotRetain
//     still covers the direction that matters: the words alone never retain.
//   - `grok.screen.blocked` alternated over "Would you like to", "Allow .*?",
//     "Enter to confirm" and "↑/↓ select" anywhere in the last twenty-two lines,
//     any one of which was enough. Upstream's four blockers each require a
//     corroborating pair from the same footer, which is what stops a turn that
//     merely quotes one of those phrases from reading as an unanswered prompt.
//     The narrowing is the point; no fixture depended on the wider form.
//
// Two working phrases also went: `Thinking…` and `Waiting on ` matched anywhere
// in the current-bottom window, where upstream's `spinner_status_working`
// requires the braille frame *and* the trailing [stop] chip on one line. A
// finished turn's last status line still says "Thinking…" for as long as it
// stays on screen, which is precisely the residual-working case
// TestGrokIdleFooterBeatsResidualThinkingInViewport was written for.

// grokScreenIdentity is distinctive footer/composer chrome. It is used when
// pane_current_command is a shared runtime (node/agent) so a Grok pane cannot be
// claimed by Cursor, and it is identity only — never activity.
var grokScreenIdentity = regexp.MustCompile(`(?is)(Ctrl\+x:shortcuts|Enter:send.{0,60}Shift\+Tab:mode|Esc:cancel.{0,60}Ctrl\+x:shortcuts)`)

// DetectGrok classifies a Grok pane. The process gate runs first and refuses
// before any manifest is evaluated; everything after it is upstream's, plus the
// four overlay rules.
func DetectGrok(ob Observation) Result {
	if ob.Agent != "grok" {
		return Result{State: StateUnknown, Evidence: "grok.process-mismatch"}
	}
	return DetectManifestResult(ob)
}

func grokProcess(command string) bool {
	return command == "grok" || command == "node" || command == "bun" || strings.HasPrefix(command, "grok-")
}
