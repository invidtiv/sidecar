package agentactivity

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestCodexPrecedenceAndProcessGate(t *testing.T) {
	ob := Observation{Agent: "codex", CurrentCommand: "node", PaneTitle: "⠼ Action Required", Screen: "• Working (2s • esc to interrupt)\nAllow command?"}
	got := DetectCodex(ob)
	// Phase 2: evidence was `codex.title.blocked`, now `osc_title_blocked`.
	// Reason: engine semantics. Upstream's title blocker is priority 1100 and
	// the title spinner 1050, so a title carrying both a spinner frame and
	// "Action Required" still resolves blocked — the same precedence the Go
	// table got from file order, now stated as a number.
	//
	// The screen also grew its bullet: upstream's `screen_working_fallback` is
	// column-anchored on `^[•◦]\s+Working \(`, which is what real Codex paints,
	// where Sidecar's rule matched the phrase anywhere. The line without a
	// bullet was never a screen Codex produces.
	if got.State != StateBlocked || got.Evidence != "osc_title_blocked" {
		t.Fatalf("got %+v", got)
	}
	ob.CurrentCommand = "zsh"
	if got := DetectCodex(ob); got.State != StateUnknown || got.Evidence != "codex.process-mismatch" {
		t.Fatalf("process mismatch got %+v", got)
	}
}

func TestIdentifyLivePaneOwner(t *testing.T) {
	orig := lookUpAgentAlias
	lookUpAgentAlias = func() string { return "" }
	t.Cleanup(func() { lookUpAgentAlias = orig })

	tests := []struct {
		name    string
		command string
		screen  string
		want    string
	}{
		{"Claude version executable", "2.1.220", "", "claude"},
		{"Codex command", "codex", "", "codex"},
		{"Cursor command", "cursor-agent", "", "cursor"},
		{"Cursor windows launcher", "cursor-agent.cmd", "", "cursor"},
		{"returned to shell", "zsh", "Action Required", "shell"},
		{"shared runtime ANSI Claude UI", "node", "\x1b[2m────────────────\x1b[0m\n\x1b[36m❯ \x1b[0m\n────────────────\n  ⏸ manual mode on · ? for shortcuts", "claude"},
		{"shared runtime Codex UI", "node", "• Working (2s • esc to interrupt)\n› edit the file", "codex"},
		{"agent alias with Cursor chrome", "agent", "Cursor Agent\n~/proj · main\nPlan, search, build anything", "cursor"},
		{"agent alias without Cursor chrome is not Cursor", "agent", "ordinary output", ""},
		{"agent follow-up composer is not identity", "agent", "→ Add a follow-up\nctrl+c to stop", ""},
		{"node with Cursor approval chrome is not Cursor", "node", "Write to this file?\nProceed (y)\nreject & propose changes", ""},
		{"node with Cursor Agent header is not Cursor", "node", "Cursor Agent\n~/proj · main\n→ Plan, search, build anything", ""},
		{"prompt glyphs in transcript are not identity", "node", "example output:\n❯ \nthen later:\n› ", ""},
		{"provider mention is not identity", "node", "I recommend OpenAI Codex here", ""},
		{"shared runtime ambiguous", "node", "ordinary output", ""},
		{"stop hint alone is not Cursor", "node", "ctrl+c to stop\nmore output", ""},
		{"follow-up alone is not Cursor", "agent", "→ Add a follow-up", ""},
		{"write-file question alone is not Cursor", "node", "Should I write to this file?", ""},
		{"Codex approval is Codex not Cursor", "node", "Would you like to run the following command?\n› 1. Yes, proceed (y)", "codex"},
		{"Grok footer on shared runtime is Grok", "node", "Run /doctor for details and fixes.\nEnter:send  │  Shift+Tab:mode  │  Ctrl+x:shortcuts", "grok"},
		{"Grok footer on agent alias is Grok not Cursor", "agent", "Enter:send | Shift+Tab:mode | Ctrl+x:shortcuts", "grok"},
		{"conversation about Cursor is not Cursor", "node", "will you check the cursor shell detection?\nRun this command? maybe", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Identify(Observation{CurrentCommand: tt.command, Screen: tt.screen}); got != tt.want {
				t.Fatalf("Identify() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestIdentifyAgentAliasResolvesLikeHerdrSymlink(t *testing.T) {
	orig := lookUpAgentAlias
	lookUpAgentAlias = func() string { return "cursor" }
	t.Cleanup(func() { lookUpAgentAlias = orig })
	if got := Identify(Observation{CurrentCommand: "agent", Screen: "ordinary output"}); got != "cursor" {
		t.Fatalf("Identify() = %q, want cursor from resolved agent alias", got)
	}
	if got := Identify(Observation{CurrentCommand: "node", Screen: "ordinary output"}); got != "" {
		t.Fatalf("Identify(node) = %q, resolved alias must not leak onto node", got)
	}
}

func TestForegroundProcessIdentityDisambiguatesCursorNode(t *testing.T) {
	ob := Observation{
		Agent: "cursor", CurrentCommand: "node", ProcessIdentity: "cursor",
		Screen: "Run Everything\napproval mode",
	}
	if got := Identify(ob); got != "cursor" {
		t.Fatalf("Identify() = %q, want cursor from foreground process identity", got)
	}
	result := DetectCursor(ob)
	if result.State != StateIdle || result.Evidence != "cursor.known-live-fallback" {
		t.Fatalf("DetectCursor() = %+v, want known live Cursor fallback", result)
	}

	ob.ProcessIdentity = ""
	if got := Identify(ob); got != "" {
		t.Fatalf("bare node Identify() = %q, want ambiguous", got)
	}
	if result := DetectCursor(ob); result.State != StateUnknown || result.Evidence != "cursor.process-mismatch" {
		t.Fatalf("bare node DetectCursor() = %+v, want process mismatch", result)
	}
}

func TestArgv0IdentityResolvesCursorAgentSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "cursor-agent")
	link := filepath.Join(dir, "agent")
	if err := os.WriteFile(target, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if got := identifyArgv0(link); got != "cursor" {
		t.Fatalf("identifyArgv0(%q) = %q, want cursor", link, got)
	}
}

func TestTrackerProcessChangeAcceptsInitialIdleImmediately(t *testing.T) {
	now := time.Unix(100, 0)
	tracker := Tracker{State: StateWorking, Evidence: "codex.screen.working"}
	tracker.ResetForProcessChange(now)
	changed := tracker.Apply(Result{State: StateIdle, Evidence: "claude.screen.idle"}, now)
	if !changed || tracker.State != StateIdle || tracker.DisplayState() != "idle" {
		t.Fatalf("initial idle after process change = %#v, changed=%v", tracker, changed)
	}
}

func TestTrackerExplicitVisibleIdleDoesNotNeedAnotherOutputEvent(t *testing.T) {
	now := time.Unix(200, 0)
	tracker := Tracker{State: StateWorking, Evidence: "claude.screen.working"}
	result := Result{State: StateIdle, Evidence: "claude.screen.idle", VisibleIdle: true}
	if !tracker.Apply(result, now) || tracker.State != StateIdle {
		t.Fatalf("explicit visible idle did not settle immediately: %#v", tracker)
	}
}

func TestTrackerVisibleIdleEvidenceChangeDoesNotManufactureDone(t *testing.T) {
	now := time.Unix(300, 0)
	tracker := Tracker{State: StateIdle, Evidence: "claude.screen.resolved-idle", Seen: true}
	result := Result{State: StateIdle, Evidence: "claude.screen.idle", VisibleIdle: true}
	if tracker.Apply(result, now) || tracker.DisplayState() != "idle" || !tracker.Seen {
		t.Fatalf("idle evidence change mutated acknowledged idle: %#v", tracker)
	}
}

func TestRealCodexFixtures(t *testing.T) {
	tests := []struct {
		file string
		want State
		skip bool
	}{
		{"startup_idle.txt", StateIdle, false},
		{"working.txt", StateWorking, false},
		{"tool_execution.txt", StateWorking, false},
		{"background_terminal.txt", StateWorking, false},
		{"blocked.txt", StateBlocked, false},
		{"interrupted.txt", StateIdle, false},
		{"completed.txt", StateIdle, false},
		{"transcript_viewer.txt", StateUnknown, true},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("testdata", "codex", tt.file))
			if err != nil {
				t.Fatal(err)
			}
			fields := strings.SplitN(string(data), "screen:\n", 2)
			if len(fields) != 2 {
				t.Fatal("fixture missing screen")
			}
			var title, command string
			for _, line := range strings.Split(fields[0], "\n") {
				if strings.HasPrefix(line, "pane_title: ") {
					title = strings.TrimPrefix(line, "pane_title: ")
				}
				if strings.HasPrefix(line, "pane_current_command: ") {
					command = strings.TrimPrefix(line, "pane_current_command: ")
				}
			}
			got := DetectCodex(Observation{Agent: "codex", Screen: fields[1], PaneTitle: title, CurrentCommand: command})
			if got.State != tt.want || got.SkipStateUpdate != tt.skip {
				t.Fatalf("got %+v", got)
			}
		})
	}
}

func TestCodexBackgroundTerminalRequiresCurrentRunningChrome(t *testing.T) {
	tests := []struct {
		name   string
		screen string
		want   State
	}{
		{
			name:   "plural background terminals",
			screen: "• Waiting for background terminal (10s • esc to interrupt)\n  └ 2 background terminals running · /ps to view · /stop to close\n\n› next prompt",
			want:   StateWorking,
		},
		{
			name:   "historical completed wait",
			screen: "• Waited for background terminal · make release\n\n› next prompt",
			want:   StateIdle,
		},
		{
			name:   "generic long lived helper",
			screen: "background terminal running · /ps to view · /stop to close\n\n› next prompt",
			want:   StateIdle,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DetectCodex(Observation{Agent: "codex", CurrentCommand: "codex", PaneTitle: "tasks", Screen: tt.screen})
			if got.State != tt.want {
				t.Fatalf("got %+v, want %s", got, tt.want)
			}
		})
	}
}

func readObservationFixture(t *testing.T, agent, file string) Observation {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", agent, file))
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.SplitN(string(data), "screen:\n", 2)
	if len(fields) != 2 {
		t.Fatal("fixture missing screen")
	}
	ob := Observation{Agent: agent, Screen: fields[1]}
	for _, line := range strings.Split(fields[0], "\n") {
		if strings.HasPrefix(line, "pane_title: ") {
			ob.PaneTitle = strings.TrimPrefix(line, "pane_title: ")
		}
		if strings.HasPrefix(line, "pane_current_command: ") {
			ob.CurrentCommand = strings.TrimPrefix(line, "pane_current_command: ")
		}
		// pane_height is the manifest engine's read window. A fixture that
		// omits it is read at Herdr's own 24-row fallback, which is what every
		// fixture minted before the manifest cutover assumes.
		if strings.HasPrefix(line, "pane_height: ") {
			ob.PaneHeight, _ = strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "pane_height: ")))
		}
	}
	return ob
}

// The trust prompt is the only upstream rule reading `top_non_empty_lines`, so
// it is the only one whose verdict depends on where the read window starts. It
// is also a rule Sidecar had no equivalent for: before the cutover a first-run
// Codex pane sat on an unanswered trust prompt reading idle, because the
// composer glyph rule saw the `›` on the option line.
func TestCodexTrustDirectoryPromptReadsFromTheTopOfTheWindow(t *testing.T) {
	ob := readObservationFixture(t, "codex", "trust_directory.txt")
	if ob.PaneHeight != 40 {
		t.Fatalf("fixture pane_height = %d, want the realistic 40 the header records", ob.PaneHeight)
	}

	// At the pane's own height, and at the 24-row fallback a pane whose height
	// tmux could not report falls back to. The prompt is eleven rows, so both
	// windows start at the same line and both must read blocked.
	for _, rows := range []int{ob.PaneHeight, 0} {
		probe := ob
		probe.PaneHeight = rows
		got := DetectCodex(probe)
		if got.State != StateBlocked || got.Evidence != "trust_directory" || !got.VisibleBlocker {
			t.Fatalf("pane_height %d: got %+v, want blocked/trust_directory", rows, got)
		}
	}

	// The window bound is real, and this is what it costs. Push the prompt up
	// with a turn's worth of prior output and read it at the 24-row fallback:
	// the header scrolls out of the top of the window, `\A> You are in` no
	// longer anchors, and the trust rule stops matching. The pane is still
	// blocked — the confirm hint reaches `live_strong_blocker` at priority 900 —
	// which is the graceful direction for this to degrade in.
	scrolled := ob
	scrolled.PaneHeight = 24
	scrolled.Screen = strings.Repeat("• Ran a command\n", 30) + ob.Screen
	got := DetectCodex(scrolled)
	if got.Evidence == "trust_directory" {
		t.Fatal("the trust rule matched a header outside the read window; the bound is not being applied")
	}
	if got.State != StateBlocked || got.Evidence != "live_strong_blocker" {
		t.Fatalf("scrolled trust prompt got %+v, want blocked/live_strong_blocker", got)
	}
}

func TestRealPhase2ProviderFixtures(t *testing.T) {
	tests := []struct {
		agent, file, evidence string
		want                  State
		skip                  bool
	}{
		// Claude's six fixtures after the Phase 2 cutover. Every verdict is
		// unchanged; every evidence string is now the Herdr rule id that
		// produced it. Per fixture, old → new and why:
		//
		//   idle.txt              claude.screen.idle    → live_prompt_box
		//   interrupted.txt       claude.screen.idle    → live_prompt_box
		//     Upstream rule better. Sidecar matched `^❯` in the last 12 lines
		//     and excluded two literals; upstream reads only the body of the
		//     prompt *box* (the lines between its two horizontal rules), so a
		//     resolved form still in the scrollback cannot reach the rule at
		//     all rather than being excluded literal by literal.
		//   working.txt           claude.title.working  → osc_title_working
		//   background-agents.txt claude.title.working  → osc_title_working
		//     Upstream rule better. Same braille class, plus the half-circle
		//     frames (U+25D0–U+25D3) Claude Code 2.1.228 switched to, which
		//     Sidecar's pattern never learned. See working_halfcircle.txt.
		//   blocked.txt           claude.screen.blocked → live_blocked_form
		//     Upstream rule better. Sidecar alternated over a list of phrases
		//     anywhere in the last 24 lines; upstream requires "esc to cancel"
		//     *and* a confirm-or-select hint *and*, for the select shape, a
		//     navigation hint, read below the last horizontal rule. The
		//     AskUserQuestion form this fixture captured satisfies all three.
		//   overlay.txt           claude.overlay.retain → sidecar.overlay_retain
		//     Sidecar-only behaviour preserved through the overlay. Upstream's
		//     `model_picker_menu` is gated on literals this rendering does not
		//     carry, so upstream alone reads the model picker as idle and the
		//     tracker would call that a completed turn. See
		//     manifests/sidecar/claude.toml.
		{"claude", "idle.txt", "live_prompt_box", StateIdle, false},
		{"claude", "working.txt", "osc_title_working", StateWorking, false},
		{"claude", "background-agents.txt", "osc_title_working", StateWorking, false},
		{"claude", "working_halfcircle.txt", "osc_title_working", StateWorking, false},
		{"claude", "blocked.txt", "live_blocked_form", StateBlocked, false},
		{"claude", "interrupted.txt", "live_prompt_box", StateIdle, false},
		{"claude", "overlay.txt", "sidecar.overlay_retain", StateUnknown, true},
		{"grok", "idle.txt", "grok.title.idle", StateIdle, false},
		{"grok", "working.txt", "grok.screen.working", StateWorking, false},
		{"grok", "interrupted.txt", "grok.title.idle", StateIdle, false},
		{"grok", "overlay.txt", "grok.overlay.retain", StateUnknown, true},
		{"grok", "stale_working_scrollback.txt", "grok.footer.idle", StateIdle, false},
		{"grok", "background_subagent.txt", "grok.screen.background-running", StateWorking, false},
		{"antigravity", "blocked.txt", "antigravity.screen.blocked", StateBlocked, false},
		{"antigravity", "working.txt", "antigravity.screen.working", StateWorking, false},
		{"antigravity", "idle_fallback.txt", "antigravity.known-live-fallback", StateIdle, false},
		{"antigravity", "interrupted.txt", "antigravity.known-live-fallback", StateIdle, false},
	}
	for _, tt := range tests {
		t.Run(tt.agent+"/"+tt.file, func(t *testing.T) {
			got := Detect(readObservationFixture(t, tt.agent, tt.file))
			if got.State != tt.want || got.Evidence != tt.evidence || got.SkipStateUpdate != tt.skip {
				t.Fatalf("got %+v", got)
			}
		})
	}
}

func TestPhase2ProviderProcessGates(t *testing.T) {
	for _, agent := range []string{"claude", "grok", "antigravity"} {
		got := Detect(Observation{Agent: agent, CurrentCommand: "zsh", Screen: "Action Required\nGenerating..."})
		if got.State != StateUnknown || !strings.HasSuffix(got.Evidence, ".process-mismatch") {
			t.Fatalf("%s mismatch got %+v", agent, got)
		}
	}
}

func TestExpandedProviderCompatibilityRules(t *testing.T) {
	tests := []struct {
		name     string
		ob       Observation
		want     State
		evidence string
	}{
		{"pi working", Observation{Agent: "pi", CurrentCommand: "pi", Screen: "Working..."}, StateWorking, "pi.screen.working"},
		{"pi idle fallback", Observation{Agent: "pi", CurrentCommand: "pi", Screen: "ready"}, StateIdle, "pi.known-live-fallback"},
		{"copilot blocked wins", Observation{Agent: "copilot", CurrentCommand: "copilot", Screen: "esc to cancel\nenter to confirm"}, StateBlocked, "copilot.screen.blocked"},
		{"copilot cancel working", Observation{Agent: "copilot", CurrentCommand: "copilot", Screen: "esc again to cancel"}, StateWorking, "copilot.screen.working"},
		{"cursor write blocked", Observation{Agent: "cursor", CurrentCommand: "cursor-agent", Screen: "Write to this file?\nProceed (y)\nreject & propose changes"}, StateBlocked, "cursor.screen.write-blocked"},
		{"cursor shell blocked", Observation{Agent: "cursor", CurrentCommand: "cursor-agent", Screen: "Run this command?\nRun (once) (y)\nSkip (esc or n)"}, StateBlocked, "cursor.screen.approval-blocked"},
		{"cursor working", Observation{Agent: "cursor", CurrentCommand: "cursor-agent", Screen: "ctrl+c to stop"}, StateWorking, "cursor.screen.stop-working"},
		{"cursor spinner working", Observation{Agent: "cursor", CurrentCommand: "cursor-agent", Screen: "⬢ Thought 3s\n⬢ Reading 2 files"}, StateWorking, "cursor.screen.spinner-working"},
		{"cursor background working", Observation{Agent: "cursor", CurrentCommand: "cursor-agent", Screen: "12s (background)"}, StateWorking, "cursor.screen.background-working"},
		{"cursor run everything not blocked", Observation{Agent: "cursor", CurrentCommand: "cursor-agent", Screen: "Run Everything\napproval mode"}, StateIdle, "cursor.known-live-fallback"},
		{"opencode blocked", Observation{Agent: "opencode", CurrentCommand: "opencode", Screen: "△ Permission required"}, StateBlocked, "opencode.screen.blocked"},
		{"opencode working", Observation{Agent: "opencode", CurrentCommand: "opencode", Screen: "■■⬝⬝"}, StateWorking, "opencode.screen.progress-working"},
		{"amp title blocked", Observation{Agent: "amp", CurrentCommand: "amp", PaneTitle: "Plugin confirmation needed"}, StateBlocked, "amp.title.plugin-blocked"},
		{"amp title working", Observation{Agent: "amp", CurrentCommand: "amp", PaneTitle: "⠼ repo - amp - task"}, StateWorking, "amp.title.working"},
		{"amp title idle", Observation{Agent: "amp", CurrentCommand: "amp", PaneTitle: "repo - amp - task"}, StateIdle, "amp.title.idle"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Detect(tt.ob)
			if got.State != tt.want || got.Evidence != tt.evidence {
				t.Fatalf("got %+v", got)
			}
		})
	}
}

func TestExpandedProvidersRequirePositiveProcessIdentity(t *testing.T) {
	for _, agent := range []string{"pi", "copilot", "cursor", "opencode", "amp"} {
		got := Detect(Observation{Agent: agent, CurrentCommand: "zsh", PaneTitle: "Plugin confirmation needed", Screen: "Working...\nesc to cancel\nenter to confirm"})
		if got.State != StateUnknown || got.Evidence != agent+".process-mismatch" {
			t.Fatalf("%s mismatch got %+v", agent, got)
		}
	}
}

func TestExpandedProvidersIgnoreHistoricalSignalsOutsideCurrentBottom(t *testing.T) {
	old := "Working...\nesc to cancel\nenter to confirm\n△ Permission required\n" + strings.Repeat("resolved\n", 30)
	for _, ob := range []Observation{
		{Agent: "pi", CurrentCommand: "pi", Screen: old},
		{Agent: "copilot", CurrentCommand: "copilot", Screen: old},
		{Agent: "cursor", CurrentCommand: "cursor-agent", Screen: old},
		{Agent: "opencode", CurrentCommand: "opencode", Screen: old},
		{Agent: "amp", CurrentCommand: "amp", Screen: old},
	} {
		if got := Detect(ob); got.State != StateIdle {
			t.Fatalf("%s historical signal got %+v", ob.Agent, got)
		}
	}
}

func TestExpandedPerProviderFixtures(t *testing.T) {
	tests := []struct {
		agent, file, evidence string
		want                  State
		skip                  bool
	}{
		{"pi", "working_compatibility.txt", "pi.screen.working", StateWorking, false},
		{"pi", "false_positive.txt", "pi.process-mismatch", StateUnknown, false},
		{"copilot", "blocked_compatibility.txt", "copilot.screen.blocked", StateBlocked, false},
		{"copilot", "false_positive.txt", "copilot.process-mismatch", StateUnknown, false},
		{"cursor", "blocked_compatibility.txt", "cursor.screen.write-blocked", StateBlocked, false},
		{"cursor", "blocked_shell.txt", "cursor.screen.approval-blocked", StateBlocked, false},
		{"cursor", "blocked_delete.txt", "cursor.screen.delete-blocked", StateBlocked, false},
		{"cursor", "blocked_web.txt", "cursor.screen.web-edit-blocked", StateBlocked, false},
		{"cursor", "blocked_decision.txt", "cursor.screen.decision-blocked", StateBlocked, false},
		{"cursor", "working_stop.txt", "cursor.screen.stop-working", StateWorking, false},
		{"cursor", "working_spinner.txt", "cursor.screen.spinner-working", StateWorking, false},
		{"cursor", "working_background.txt", "cursor.screen.background-working", StateWorking, false},
		{"cursor", "idle_fallback.txt", "cursor.known-live-fallback", StateIdle, false},
		{"cursor", "false_positive.txt", "cursor.process-mismatch", StateUnknown, false},
		{"cursor", "false_positive_run_everything.txt", "cursor.known-live-fallback", StateIdle, false},
		{"cursor", "false_positive_finished_background.txt", "cursor.known-live-fallback", StateIdle, false},
		{"opencode", "blocked_compatibility.txt", "opencode.screen.blocked", StateBlocked, false},
		{"opencode", "false_positive.txt", "opencode.process-mismatch", StateUnknown, false},
		{"amp", "title_compatibility.txt", "amp.title.plugin-blocked", StateBlocked, false},
		{"amp", "false_positive.txt", "amp.process-mismatch", StateUnknown, false},
	}
	for _, tt := range tests {
		t.Run(tt.agent+"/"+tt.file, func(t *testing.T) {
			got := Detect(readObservationFixture(t, tt.agent, tt.file))
			if got.State != tt.want || got.Evidence != tt.evidence || got.SkipStateUpdate != tt.skip {
				t.Fatalf("got %+v", got)
			}
		})
	}
}

// Captures are taken with `capture-pane -e`, so styled chrome arrives with SGR
// escapes inline. ESC is not \s, so unless they are stripped every
// column-anchored rule silently fails against a coloured prompt marker — the
// pane reads as no-match and falls through to the known-live fallback.
//
// The manifest engine strips in two places, and both are exercised here:
// manifest.ReadWindow strips the screen, and manifest's resolver strips the
// title, which upstream never has to do because Herdr's osc_title is a decoded
// OSC payload rather than tmux's `#{pane_title}`.
//
// The screens grew real chrome in the Phase 2 cutover. Upstream's idle rules
// read structure — the body of the prompt box, a bulleted status line — not a
// bare glyph anywhere in the tail, so a two-line synthetic screen no longer
// reaches the rule it was written to exercise. Painting the box is what keeps
// the test about escape stripping.
func TestStyledChromeStillMatchesColumnAnchoredRules(t *testing.T) {
	rule := strings.Repeat("─", 56)
	tests := []struct {
		name         string
		ob           Observation
		wantState    State
		wantEvidence string
	}{
		{
			// Old: idle / claude.screen.idle. New: idle / live_prompt_box.
			// Reason: upstream rule better — it reads the prompt box body
			// rather than the last twelve lines.
			name:         "claude coloured prompt marker is idle",
			ob:           Observation{Agent: "claude", CurrentCommand: "2.1.220", Screen: "some output\n" + rule + "\n\x1b[38;5;153m❯\x1b[0m \n" + rule + "\n  ⏸ manual mode on"},
			wantState:    StateIdle,
			wantEvidence: "live_prompt_box",
		},
		{
			// Old: idle / codex.screen.idle from a coloured `›` composer. New:
			// working / screen_working_fallback from a coloured status bullet.
			// Reason: engine semantics — upstream codex.toml has no composer
			// idle rule at all (it reaches idle through osc_title_idle, which
			// reads no screen text and so proves nothing about stripping). Its
			// column-anchored rule is the status line, and that is now what
			// carries the coloured glyph this case exists to strip.
			name:         "codex coloured status bullet is working",
			ob:           Observation{Agent: "codex", CurrentCommand: "codex", Screen: "some output\n\x1b[36m•\x1b[0m Working (2s • esc to interrupt)"},
			wantState:    StateWorking,
			wantEvidence: "screen_working_fallback",
		},
		{
			// Old: working / claude.title.working. New: working /
			// osc_title_working. Reason: upstream rule better (it also covers
			// the half-circle frames). This case is why the engine strips the
			// title: upstream's pattern is anchored at the start of the title,
			// and an unstripped `\x1b[33m` ahead of the glyph makes it a
			// permanent no-match on a provider that colours its spinner.
			name:         "claude styled spinner title is working",
			ob:           Observation{Agent: "claude", CurrentCommand: "2.1.220", PaneTitle: "\x1b[33m⠹\x1b[0m Claude Code", Screen: "output"},
			wantState:    StateWorking,
			wantEvidence: "osc_title_working",
		},
		{
			// Old: blocked / claude.screen.blocked. New: blocked /
			// live_blocked_form. Reason: upstream rule better.
			//
			// PaneHeight is set because the read window is now selected before
			// trailing blanks are trimmed, which is Herdr's order: thirty-two
			// rows of pane do not fit in the 24-row fallback, and the last
			// twenty-four of them are the padding. A real pane showing this form
			// is at least as tall as the form plus its padding, so the fixture
			// says so rather than relying on a trim that reached above the pane.
			name:         "rows of pure escapes count as trailing padding",
			ob:           Observation{Agent: "claude", CurrentCommand: "2.1.220", PaneHeight: 40, Screen: "Which option?\nEnter to select · ↑/↓ to navigate · Esc to cancel" + strings.Repeat("\n\x1b[0m", 30)},
			wantState:    StateBlocked,
			wantEvidence: "live_blocked_form",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Detect(tt.ob)
			if got.State != tt.wantState || got.Evidence != tt.wantEvidence {
				t.Fatalf("got %+v, want %s/%s", got, tt.wantState, tt.wantEvidence)
			}
		})
	}
}

// Retain rules keyed on a bare word froze the badge whenever a turn merely
// discussed one. Viewer chrome must corroborate the word.
func TestViewerWordsWithoutChromeDoNotRetain(t *testing.T) {
	tests := []Observation{
		{Agent: "claude", CurrentCommand: "2.1.220", Screen: "I read the transcript and found the bug\n❯ "},
		{Agent: "grok", CurrentCommand: "grok-1.0.0-maco", Screen: "let me check the conversation history for that\n│ ❯ "},
	}
	for _, ob := range tests {
		t.Run(ob.Agent, func(t *testing.T) {
			if got := Detect(ob); got.SkipStateUpdate {
				t.Fatalf("discussion retained state: %+v", got)
			}
		})
	}
	// The real viewer, chrome and all, still retains.
	viewer := Detect(Observation{
		Agent: "claude", CurrentCommand: "2.1.220",
		Screen: "showing detailed transcript · ctrl+o to toggle",
	})
	// Phase 2: evidence was `claude.overlay.transcript`, now `transcript_viewer`.
	// Reason: upstream rule better — same corroboration list, read from the
	// bottom three non-empty lines rather than the bottom six.
	if !viewer.SkipStateUpdate || viewer.Evidence != "transcript_viewer" {
		t.Fatalf("real transcript viewer not retained: %+v", viewer)
	}
}

// Retention has no natural end: an overlay left open, or chrome that never
// clears, would otherwise assert a confident badge forever.
func TestSkipRetentionExpiresIntoUnknown(t *testing.T) {
	now := time.Unix(500, 0)
	tracker := Tracker{State: StateWorking, Evidence: "claude.title.working"}
	retain := Result{State: StateUnknown, Evidence: "claude.overlay.transcript", SkipStateUpdate: true}

	if tracker.Apply(retain, now) || tracker.State != StateWorking {
		t.Fatalf("retention did not hold prior state: %+v", tracker)
	}
	if tracker.Apply(retain, now.Add(SkipRetentionCap-time.Second)) || tracker.State != StateWorking {
		t.Fatalf("retention expired early: %+v", tracker)
	}
	if !tracker.Apply(retain, now.Add(SkipRetentionCap+time.Second)) {
		t.Fatal("retention never expired")
	}
	if tracker.State != StateUnknown {
		t.Fatalf("expired retention should admit unknown, got %+v", tracker)
	}
	if tracker.DisplayState() == "done" {
		t.Fatal("expired retention manufactured done")
	}

	// A live observation in between clears the retention clock.
	tracker = Tracker{State: StateWorking, Evidence: "claude.title.working"}
	tracker.Apply(retain, now)
	tracker.Apply(Result{State: StateWorking, Evidence: "claude.title.working"}, now.Add(time.Second))
	if tracker.Apply(retain, now.Add(SkipRetentionCap)); tracker.State != StateWorking {
		t.Fatalf("clock not reset by live observation: %+v", tracker)
	}
}

func TestKnownLiveFallbackIdleNeverCreatesUnseenDone(t *testing.T) {
	tests := []Observation{
		// Claude and Codex both own explicit idle rules, so reaching the
		// fallback means the chrome went unrecognised — not that a turn ended.
		{Agent: "claude", CurrentCommand: "2.1.220", Screen: "unmatched"},
		{Agent: "codex", CurrentCommand: "codex", Screen: "unmatched"},
		{Agent: "pi", CurrentCommand: "pi", Screen: "unmatched"},
		{Agent: "copilot", CurrentCommand: "copilot", Screen: "unmatched"},
		{Agent: "cursor", CurrentCommand: "cursor-agent", Screen: "unmatched"},
		{Agent: "opencode", CurrentCommand: "opencode", Screen: "unmatched"},
		{Agent: "amp", CurrentCommand: "amp", Screen: "unmatched"},
		// Grok live process with no strong chrome must not manufacture "done".
		{Agent: "grok", CurrentCommand: "grok-1.0.0-maco", Screen: "unmatched"},
	}
	for _, ob := range tests {
		for _, prior := range []State{StateWorking, StateBlocked} {
			t.Run(ob.Agent+"/"+string(prior), func(t *testing.T) {
				result := Detect(ob)
				if result.State != StateIdle || !result.FallbackIdle {
					t.Fatalf("fallback result = %+v", result)
				}
				now := time.Unix(400, 0)
				tracker := Tracker{State: prior, Evidence: ob.Agent + ".prior"}
				if tracker.Apply(result, now) {
					t.Fatal("fallback bypassed debounce")
				}
				if !tracker.Apply(result, now.Add(IdleDebounce)) {
					t.Fatal("fallback did not establish idle")
				}
				if tracker.DisplayState() != "idle" || !tracker.Seen {
					t.Fatalf("fallback manufactured done: %+v display=%q", tracker, tracker.DisplayState())
				}
			})
		}
	}
}

func TestExplicitIdleEvidenceStillCreatesDone(t *testing.T) {
	now := time.Unix(500, 0)
	tracker := Tracker{State: StateWorking, Evidence: "working"}
	explicit := Result{State: StateIdle, Evidence: "provider.explicit-idle"}
	tracker.Apply(explicit, now)
	tracker.Apply(explicit, now.Add(IdleDebounce))
	if tracker.DisplayState() != "done" {
		t.Fatalf("explicit idle display=%q tracker=%+v", tracker.DisplayState(), tracker)
	}
}

func TestGrokUsesTitleMetadataWithoutOSCProgress(t *testing.T) {
	got := DetectGrok(Observation{Agent: "grok", CurrentCommand: "grok-1.0.0-maco", PaneTitle: "repo - grok"})
	if got.State != StateIdle || got.Evidence != "grok.title.idle" || !got.VisibleIdle {
		t.Fatalf("got %+v", got)
	}
	// Busy title alone (no idle footer) still means working — OSC 9;4 is unavailable via tmux.
	got = DetectGrok(Observation{Agent: "grok", CurrentCommand: "grok-1.0.0-maco", PaneTitle: "⠼ Working - grok"})
	if got.State != StateWorking || got.Evidence != "grok.title.working" {
		t.Fatalf("got %+v", got)
	}
}

func TestGrokIdleFooterBeatsStickyBrailleTitle(t *testing.T) {
	// Observed false-working: title still has a braille frame after the turn
	// ends while the footer is clearly idle (Ctrl+x:shortcuts, no Esc:cancel).
	got := DetectGrok(Observation{
		Agent: "grok", CurrentCommand: "grok",
		PaneTitle: "⠼ session title - grok",
		Screen:    "Enter:send  │  Shift+Tab:mode  │  Ctrl+x:shortcuts\n",
	})
	if got.State != StateIdle || got.Evidence != "grok.footer.idle" {
		t.Fatalf("sticky title should lose to idle footer: %+v", got)
	}
}

func TestGrokBackgroundSubagentBeatsIdleTitleAndFooter(t *testing.T) {
	// Live repro: main turn parked, "1 subagent still running", footer idle —
	// must not classify idle/done while background work is live.
	got := DetectGrok(Observation{
		Agent: "grok", CurrentCommand: "grok",
		PaneTitle: "repo - grok",
		Screen: "" +
			"○ 1 subagent still running · send a message to interrupt\n" +
			"│ ❯ │\n" +
			"Shift+Tab:mode  │  Ctrl+x:shortcuts\n",
	})
	if got.State != StateWorking || got.Evidence != "grok.screen.background-running" {
		t.Fatalf("background subagent got %+v", got)
	}
}

func TestGrokBusyChromeBeatsIdleTitleForm(t *testing.T) {
	// Title already cleared to idle form while Esc:cancel + status still show.
	got := DetectGrok(Observation{
		Agent: "grok", CurrentCommand: "grok",
		PaneTitle: "repo - grok",
		Screen: "" +
			"⠧ Waiting on subagent… 2.8s [stop]\n" +
			"Esc:cancel  │  Ctrl+x:shortcuts\n",
	})
	if got.State != StateWorking {
		t.Fatalf("busy chrome under idle title got %+v", got)
	}
}

func TestGrokIdleFooterBeatsResidualThinkingInViewport(t *testing.T) {
	got := DetectGrok(Observation{
		Agent: "grok", CurrentCommand: "grok",
		PaneTitle: "⠼ leftover - grok",
		Screen: "" +
			"Thinking… [stop]\n" +
			"Enter:send  │  Shift+Tab:mode  │  Ctrl+x:shortcuts\n",
	})
	if got.State != StateIdle || got.Evidence != "grok.footer.idle" {
		t.Fatalf("residual Thinking with idle footer got %+v", got)
	}
}

func TestGrokExplicitIdleAfterWorkingCreatesDone(t *testing.T) {
	now := time.Unix(600, 0)
	tracker := Tracker{State: StateWorking, Evidence: "grok.screen.working"}
	idle := DetectGrok(Observation{
		Agent: "grok", CurrentCommand: "grok",
		PaneTitle: "session - grok",
		Screen:    "│ ❯ │\nCtrl+x:shortcuts\n",
	})
	if idle.State != StateIdle || idle.FallbackIdle {
		t.Fatalf("want explicit idle, got %+v", idle)
	}
	// Explicit idle is VisibleIdle, so it may publish on the first Apply; the
	// second tick covers the debounce path without assuming which one landed.
	tracker.Apply(idle, now)
	if tracker.State != StateIdle {
		tracker.Apply(idle, now.Add(IdleDebounce))
	}
	if tracker.DisplayState() != "done" {
		t.Fatalf("explicit idle after working display=%q tracker=%+v evidence=%s", tracker.DisplayState(), tracker, idle.Evidence)
	}
}

func TestGrokFallbackIdleAfterWorkingStaysQuiet(t *testing.T) {
	now := time.Unix(700, 0)
	tracker := Tracker{State: StateWorking, Evidence: "grok.screen.working"}
	fallback := DetectGrok(Observation{Agent: "grok", CurrentCommand: "grok", Screen: "no chrome"})
	if !fallback.FallbackIdle {
		t.Fatalf("want FallbackIdle, got %+v", fallback)
	}
	tracker.Apply(fallback, now)
	tracker.Apply(fallback, now.Add(IdleDebounce))
	if tracker.DisplayState() != "idle" || !tracker.Seen {
		t.Fatalf("fallback manufactured done: display=%q tracker=%+v", tracker.DisplayState(), tracker)
	}
}

func TestRealAntigravityCompletedFallbackStillCreatesUnseenDone(t *testing.T) {
	result := DetectAntigravity(readObservationFixture(t, "antigravity", "idle_fallback.txt"))
	var tracker Tracker
	now := time.Unix(200, 0)
	tracker.Apply(Result{State: StateWorking, Evidence: "antigravity.screen.working"}, now)
	if tracker.Apply(result, now.Add(time.Second)) {
		t.Fatal("fallback idle published without debounce")
	}
	if !tracker.Apply(result, now.Add(time.Second+IdleDebounce)) || tracker.DisplayState() != "done" {
		t.Fatalf("fallback not published after debounce: %+v", tracker)
	}
}

func TestPhase2FullScreenFormsSurviveTallPanePadding(t *testing.T) {
	padding := strings.Repeat("\n", 30)
	// Every observation here carries the pane's height, because the read window
	// is the last PaneHeight rows *before* trailing blanks are trimmed. A pane
	// tall enough to leave thirty blank rows below a two-line form is a 40-row
	// pane, and saying 24 would describe a pane on which the form has scrolled
	// off the top — a different screen with a different correct answer.
	claude := DetectClaude(Observation{
		Agent: "claude", CurrentCommand: "2.1.220", PaneHeight: 40,
		Screen: "Which option?\nEnter to select · ↑/↓ to navigate · Esc to cancel" + padding,
	})
	if claude.State != StateBlocked {
		t.Fatalf("Claude tall-pane blocker got %+v", claude)
	}
	antigravity := DetectAntigravity(Observation{
		Agent: "antigravity", CurrentCommand: "agy",
		Screen: "Do you trust the contents of this project?\nYes, I trust this folder" + padding,
	})
	if antigravity.State != StateBlocked {
		t.Fatalf("Antigravity tall-pane blocker got %+v", antigravity)
	}
	antigravity = DetectAntigravity(Observation{
		Agent: "antigravity", CurrentCommand: "agy",
		Screen: "⣽  Generating...\nesc to cancel" + padding,
	})
	if antigravity.State != StateWorking {
		t.Fatalf("Antigravity tall-pane working got %+v", antigravity)
	}
}

func TestResolvedHistoricalPhase2StateDoesNotOverrideCurrentIdle(t *testing.T) {
	padding := strings.Repeat("\n", 30)
	rule := strings.Repeat("─", 56)
	tests := []struct {
		name string
		got  Result
	}{
		{
			// The Claude screen grew its prompt box in the Phase 2 cutover, and
			// the box *is* the mechanism now. Sidecar bought this behaviour with
			// a dedicated `claude.screen.resolved-idle` rule ordered ahead of
			// the blocker; upstream buys it structurally, by reading the blocked
			// form only below the last horizontal rule, so a resolved form above
			// the composer is out of the region rather than out-competed. A
			// screen with no box at all — which no live Claude paints — would
			// still read the resolved form as blocked, and that is upstream's
			// call, recorded rather than overlaid.
			name: "Claude resolved question remains in scrollback",
			got: DetectClaude(Observation{
				Agent: "claude", CurrentCommand: "2.1.220",
				Screen: "Which option?\nEnter to select · ↑/↓ to navigate · Esc to cancel\nanswered: Alpha\n" +
					rule + "\n❯ \n" + rule + "\n  ⏸ manual mode on · ? for shortcuts" + padding,
			}),
		},
		{
			name: "Antigravity completed generation remains in scrollback",
			got: DetectAntigravity(Observation{
				Agent: "antigravity", CurrentCommand: "agy",
				Screen: "Generating...\nesc to cancel\ndone\n>\n? for shortcuts · Gemini 3.6 Flash · high" + padding,
			}),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got.State != StateIdle {
				t.Fatalf("resolved historical state classified as %+v; want idle", tt.got)
			}
		})
	}
}

func TestCodexWorkingIdleAndViewer(t *testing.T) {
	tests := []struct {
		name     string
		ob       Observation
		want     State
		evidence string
		skip     bool
	}{
		{"title working", Observation{Agent: "codex", CurrentCommand: "node", PaneTitle: "⠼ repo"}, StateWorking, "osc_title_working", false},
		{
			// The screen grew a bullet in the Phase 2 cutover. Upstream's
			// `screen_working_fallback` is column-anchored on `^[•◦]\s+Working
			// \(...esc to interrupt\)` where Sidecar's rule matched the phrase
			// anywhere in the last twelve lines, so a bare "Working (…)" — a
			// line real Codex never paints — no longer matches. Upstream rule
			// better: the anchor is what stops a transcript quoting the phrase
			// from reading as live work.
			"screen working",
			Observation{Agent: "codex", CurrentCommand: "codex", Screen: "• Working (2s • esc to interrupt)"},
			StateWorking, "screen_working_fallback", false,
		},
		{
			// Old: idle / codex.screen.idle. New: idle /
			// codex.known-live-fallback, with FallbackIdle set. Reason: engine
			// semantics. Upstream has no composer rule; it reaches idle through
			// `osc_title_idle`, which asks only that the title be non-empty and
			// carry no spinner, and this observation has no title at all. Under
			// tmux a pane title is effectively always non-empty, so a live pane
			// still lands on the explicit rule; a title-less one now resolves
			// idle conservatively and cannot announce a completed turn.
			"idle composer with no title",
			Observation{Agent: "codex", CurrentCommand: "node", Screen: "\n› \n  gpt-5"},
			StateIdle, "codex.known-live-fallback", false,
		},
		{
			// Same fixture as testdata/codex/transcript_viewer.txt. Upstream's
			// `transcript_viewer` corroborates the banner with all four of the
			// viewer's key hints, where Sidecar's rule took the banner alone or
			// a two-word fragment, so the two-line synthetic no longer reaches
			// it. Upstream rule better.
			"viewer",
			Observation{Agent: "codex", CurrentCommand: "node", Screen: "/ T R A N S C R I P T /\n↑/↓ to scroll   pgup/pgdn to page   home/end to jump\nq to quit   esc to edit prev"},
			StateUnknown, "transcript_viewer", true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DetectCodex(tt.ob)
			if got.State != tt.want || got.Evidence != tt.evidence || got.SkipStateUpdate != tt.skip {
				t.Fatalf("got %+v", got)
			}
		})
	}
}

func TestTrackerDebouncesIdleAndAcknowledgesDone(t *testing.T) {
	now := time.Unix(100, 0)
	var tracker Tracker
	tracker.Apply(Result{State: StateWorking, Evidence: "working"}, now)
	if tracker.DisplayState() != "working" {
		t.Fatal(tracker.DisplayState())
	}
	tracker.Acknowledge() // Viewing work does not acknowledge its future completion.
	if tracker.Apply(Result{State: StateIdle, Evidence: "idle"}, now.Add(time.Second)) {
		t.Fatal("first idle published")
	}
	if !tracker.Apply(Result{State: StateIdle, Evidence: "idle"}, now.Add(time.Second+IdleDebounce)) {
		t.Fatal("idle not published")
	}
	if tracker.DisplayState() != "done" {
		t.Fatal(tracker.DisplayState())
	}
	tracker.Acknowledge()
	if tracker.DisplayState() != "idle" {
		t.Fatal(tracker.DisplayState())
	}
	tracker.Apply(Result{State: StateBlocked, Evidence: "blocked"}, now.Add(2*time.Second))
	tracker.Acknowledge()
	if tracker.DisplayState() != "blocked" {
		t.Fatal(tracker.DisplayState())
	}
}

func TestFallbackIdleIsMarkedInferred(t *testing.T) {
	now := time.Unix(1000, 0)
	var tracker Tracker
	tracker.Apply(Result{State: StateWorking, Evidence: "busy"}, now)
	tracker.Apply(Result{State: StateIdle, Evidence: "silence", FallbackIdle: true, VisibleIdle: true}, now.Add(time.Second))
	if !tracker.IdleInferred {
		t.Fatalf("fallback idle should be marked inferred")
	}
	if tracker.DisplayState() != string(StateIdle) {
		t.Fatalf("fallback idle must not claim done, got %q", tracker.DisplayState())
	}
	tracker.Apply(Result{State: StateWorking, Evidence: "busy again"}, now.Add(2*time.Second))
	if tracker.IdleInferred {
		t.Fatalf("inferred flag should clear when work resumes")
	}
}

func TestSnapshotRoundTrip(t *testing.T) {
	now := time.Unix(2000, 0)
	original := Tracker{State: StateIdle, Evidence: "claude.prompt.idle", ChangedAt: now, IdleInferred: true}
	restored := Restore(original.Snapshot())
	if restored.State != original.State || !restored.ChangedAt.Equal(now) || restored.Seen || !restored.IdleInferred {
		t.Fatalf("Restore(Snapshot()) = %#v, want %#v", restored, original)
	}
}
