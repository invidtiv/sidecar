package agentactivity

import (
	"regexp"
	"strings"
)

// Grok detection follows Herdr's grok.toml (2026.07.16.2, Grok Build 0.2.101
// evidence) adapted to Sidecar's ordered-rule engine and tmux capture path.
//
// Live Grok emits OSC 0 titles: idle is "grok" or "<session> - grok"; during a
// turn the title gains a braille spinner and activity text. Working turns also
// render a status line above the prompt with a braille frame and trailing
// [stop] chip, plus Esc:cancel in the footer next to shortcuts. Idle footers
// keep Ctrl+x:shortcuts (and sometimes Ctrl+.:shortcuts) without Esc:cancel.
//
// OSC 9;4 progress is deferred: tmux does not expose the payload after
// consumption. Startup splash draws braille in the screen, so working rules
// never treat a bare spinner glyph as busy — they anchor on [stop] or the
// title/footer differential.
//
// Screen rules use RegionCurrent so historical Thinking…/[stop] lines scrolled
// off the live bottom cannot stick the tracker on working after the turn ends.
// A clear idle footer also beats a sticky braille title (observed false working).
//
// Parked main turns with background subagents restore an idle-looking title and
// footer (Ctrl+x:shortcuts, no Esc:cancel) while a status row still says
// "N subagent still running · send a message to interrupt". That row must
// outrank title/footer idle or Overview reports false "done".

var (
	// Idle title form: "grok" or "<session> - grok". Braille is checked in
	// grokTitleIsIdle — RE2 has no lookaround.
	grokTitleIdleForm = regexp.MustCompile(`(?i)(?:^| - )grok$`)
	grokBraille       = regexp.MustCompile(`[\x{2800}-\x{28FF}]`)
	// Live status line: braille + … + [stop], as in Herdr spinner_status_working.
	// Also accept Thinking… and Waiting on… from current Grok Build chrome.
	grokSpinnerStop = regexp.MustCompile(`(?im)(^\s*[\x{2801}-\x{28FF}]\s.*\[stop\]\s*$|Thinking…|Waiting on |background tasks?:\s*[1-9])`)
	// Working footer: cancel hint together with shortcuts (Ctrl+x live; Ctrl+. in Herdr).
	grokFooterWorking = regexp.MustCompile(`(?is)(?:esc:cancel|esc to (?:interrupt|cancel)).*(?:ctrl\+[x.]:shortcuts)|(?:ctrl\+[x.]:shortcuts).*(?:esc:cancel|esc to (?:interrupt|cancel))`)
	grokFooterShortcuts = regexp.MustCompile(`(?i)ctrl\+[x.]:shortcuts`)
	grokFooterCancel    = regexp.MustCompile(`(?i)(esc:cancel|esc to (?:interrupt|cancel)|ctrl\+c:cancel)`)
	// Prompt box alone (older captures / interrupted fixtures).
	grokPromptIdle = regexp.MustCompile(`(?m)^\s*│ ❯\s+│`)
	// Background task chip on the first non-empty row (Herdr background_work_chip).
	grokBackgroundChip = regexp.MustCompile(`(?m)\A(?:\s*\n)*[^\n]*[⋅:⸬⁙.·]\s+[1-9][0-9]*\s+│`)
	// Parked turn with live background work (Grok 1.0 status row above prompt).
	// Must run before title-idle: title/footer look idle while this is present.
	grokBackgroundRunning = regexp.MustCompile(`(?i)(\d+\s+subagents?\s+still\s+running|send a message to interrupt)`)
	// Option / ask-user dialogs (┃-guttered choices).
	grokOptionDialog = regexp.MustCompile(`(?m)^\s*┃\s+[0-9a-z]+\s+\([●○]\)\s`)
	// Legacy pre-0.2 tool wait chrome.
	grokLegacyToolWorking = regexp.MustCompile(`(?im)(ctrl\+c:cancel.*ctrl\+enter:interject|ctrl\+enter:interject.*ctrl\+c:cancel|^\s*[\x{2801}-\x{28FF}]\s+(Run|Read|Search|List)\b)`)
)

// Blocked, overlay, and background-work outrank title/footer idle.
var grokPriorityRules = []Rule{
	{ID: "grok.title.blocked", State: StateBlocked, Region: RegionTitle, Contains: []string{"Action Required"}},
	{ID: "grok.screen.option-blocked", State: StateBlocked, Region: RegionCurrent, LastN: 28, Regexp: grokOptionDialog},
	{ID: "grok.screen.blocked", State: StateBlocked, Region: RegionCurrent, LastN: 22, Regexp: regexp.MustCompile(`(?im)(Action Required|Would you like to|Allow .*\?|Enter to confirm|↑/↓.*(?:select|navigate)|:select.*ctrl\+o:yolo|tab:scrollback.*shift\+x:dismiss|yes, proceed.*no, reject)`)},
	// "transcript"/"conversation history" are ordinary words in a turn, so the
	// viewer needs its own chrome alongside them; a retained state never expires.
	{ID: "grok.overlay.viewer", State: StateUnknown, Region: RegionCurrent, LastN: 6, Regexp: regexp.MustCompile(`(?im)(transcript|conversation history)`), Any: [][]string{
		{"esc", "close"},
		{"↑↓"},
		{"scroll"},
	}, Skip: true},
	{ID: "grok.overlay.retain", State: StateUnknown, Region: RegionCurrent, LastN: 24, Regexp: regexp.MustCompile(`(?im)(esc to close|resume session)`), Skip: true},
	// Before title idle: parked main turn with background subagents still live.
	{ID: "grok.screen.background-running", State: StateWorking, Region: RegionCurrent, LastN: 14, Regexp: grokBackgroundRunning},
	{ID: "grok.screen.background-chip", State: StateWorking, Region: RegionScreen, Regexp: grokBackgroundChip},
}

// Working chrome — evaluated before title idle so a parked-looking title
// cannot mask a live status line (Waiting on… / [stop] / Esc:cancel).
var grokWorkingChromeRules = []Rule{
	{ID: "grok.screen.working", State: StateWorking, Region: RegionCurrent, LastN: 16, Regexp: grokSpinnerStop},
	{ID: "grok.footer.working", State: StateWorking, Region: RegionCurrent, LastN: 4, Regexp: grokFooterWorking},
	{ID: "grok.screen.legacy-tool-working", State: StateWorking, Region: RegionCurrent, LastN: 16, Regexp: grokLegacyToolWorking},
}

var grokIdleChromeRules = []Rule{
	{ID: "grok.screen.idle", State: StateIdle, Region: RegionCurrent, LastN: 10, Regexp: grokPromptIdle},
}

func DetectGrok(ob Observation) Result {
	if ob.Agent != "grok" || !grokProcess(ob.CurrentCommand) {
		return Result{State: StateUnknown, Evidence: "grok.process-mismatch"}
	}

	if result := Evaluate(ob, grokPriorityRules); result.Evidence != "no-match" {
		return annotateGrok(result)
	}

	// Live working chrome before any idle title/footer decision.
	if result := Evaluate(ob, grokWorkingChromeRules); result.Evidence != "no-match" {
		// Clear idle footer beats residual Thinking…/[stop] still in the
		// current-bottom window after the turn settled (false sticky working).
		if footer := regionText(ob, Rule{Region: RegionCurrent, LastN: 4}); grokFooterIsIdle(footer) {
			return Result{State: StateIdle, Evidence: "grok.footer.idle", VisibleIdle: true}
		}
		return annotateGrok(result)
	}

	// Strict idle title (bare "grok" / "<session> - grok", no braille).
	if grokTitleIsIdle(ob.PaneTitle) {
		return Result{State: StateIdle, Evidence: "grok.title.idle", VisibleIdle: true}
	}

	// Idle footer (shortcuts, no cancel) without idle title form.
	if footer := regionText(ob, Rule{Region: RegionCurrent, LastN: 4}); grokFooterIsIdle(footer) {
		return Result{State: StateIdle, Evidence: "grok.footer.idle", VisibleIdle: true}
	}

	if result := Evaluate(ob, grokIdleChromeRules); result.Evidence != "no-match" {
		return annotateGrok(result)
	}

	// Non-idle non-empty title (spinner + activity) after screen/footer.
	if strings.TrimSpace(ob.PaneTitle) != "" {
		return Result{State: StateWorking, Evidence: "grok.title.working", VisibleWorking: true}
	}

	// Live process, no strong evidence: establish idle quietly. Must not
	// manufacture "done" after a working turn (same policy as pi/cursor/amp).
	return Result{State: StateIdle, Evidence: "grok.known-live-fallback", FallbackIdle: true}
}

func annotateGrok(result Result) Result {
	switch result.State {
	case StateIdle:
		result.VisibleIdle = !result.SkipStateUpdate
	case StateWorking:
		result.VisibleWorking = !result.SkipStateUpdate
	case StateBlocked:
		result.VisibleBlocker = !result.SkipStateUpdate
	}
	return result
}

func grokTitleIsIdle(title string) bool {
	title = strings.TrimSpace(title)
	if title == "" || !grokTitleIdleForm.MatchString(title) {
		return false
	}
	return !grokBraille.MatchString(title)
}

func grokFooterIsIdle(footer string) bool {
	if !grokFooterShortcuts.MatchString(footer) {
		return false
	}
	return !grokFooterCancel.MatchString(footer)
}

func grokProcess(command string) bool {
	return command == "grok" || command == "node" || command == "bun" || strings.HasPrefix(command, "grok-")
}
