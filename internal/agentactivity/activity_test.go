package agentactivity

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCodexPrecedenceAndProcessGate(t *testing.T) {
	ob := Observation{Agent: "codex", CurrentCommand: "node", PaneTitle: "⠼ Action Required", Screen: "Working (2s • esc to interrupt)\nAllow command?"}
	got := DetectCodex(ob)
	if got.State != StateBlocked || got.Evidence != "codex.title.blocked" {
		t.Fatalf("got %+v", got)
	}
	ob.CurrentCommand = "zsh"
	if got := DetectCodex(ob); got.State != StateUnknown || got.Evidence != "codex.process-mismatch" {
		t.Fatalf("process mismatch got %+v", got)
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
	}
	return ob
}

func TestRealPhase2ProviderFixtures(t *testing.T) {
	tests := []struct {
		agent, file, evidence string
		want                  State
		skip                  bool
	}{
		{"claude", "idle.txt", "claude.screen.idle", StateIdle, false},
		{"claude", "working.txt", "claude.title.working", StateWorking, false},
		{"claude", "blocked.txt", "claude.screen.blocked", StateBlocked, false},
		{"claude", "interrupted.txt", "claude.screen.idle", StateIdle, false},
		{"claude", "overlay.txt", "claude.overlay.retain", StateUnknown, true},
		{"grok", "idle.txt", "grok.screen.idle", StateIdle, false},
		{"grok", "working.txt", "grok.title.working", StateWorking, false},
		{"grok", "interrupted.txt", "grok.screen.idle", StateIdle, false},
		{"grok", "overlay.txt", "grok.overlay.retain", StateUnknown, true},
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
		{"cursor working", Observation{Agent: "cursor", CurrentCommand: "cursor-agent", Screen: "ctrl+c to stop"}, StateWorking, "cursor.screen.stop-working"},
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

func TestExpandedProviderOverlayAndInterruptionRetainPastIdleDebounce(t *testing.T) {
	tests := []struct {
		agent, command, screen string
	}{
		{"pi", "pi", "Settings\nEsc to close"},
		{"copilot", "copilot", "Transcript viewer\nq to quit"},
		{"cursor", "cursor-agent", "History\nEsc to close"},
		{"opencode", "opencode", "Help\nq to quit"},
		{"amp", "amp", "Turn interrupted"},
	}
	for _, tt := range tests {
		t.Run(tt.agent, func(t *testing.T) {
			result := Detect(Observation{Agent: tt.agent, CurrentCommand: tt.command, Screen: tt.screen})
			if !result.SkipStateUpdate || result.Evidence != tt.agent+".overlay-or-interruption.retain" {
				t.Fatalf("retain result = %+v", result)
			}
			now := time.Unix(400, 0)
			tracker := Tracker{State: StateWorking, Evidence: tt.agent + ".working"}
			for _, elapsed := range []time.Duration{IdleDebounce + time.Millisecond, 2*IdleDebounce + time.Second} {
				if tracker.Apply(result, now.Add(elapsed)) {
					t.Fatalf("retain changed tracker at %s: %+v", elapsed, tracker)
				}
			}
			if tracker.State != StateWorking || tracker.DisplayState() != "working" {
				t.Fatalf("retain fabricated completion: %+v display=%q", tracker, tracker.DisplayState())
			}
		})
	}
}

func TestExpandedPerProviderFixtures(t *testing.T) {
	tests := []struct {
		agent, file, evidence string
		want                  State
		skip                  bool
	}{
		{"pi", "working_compatibility.txt", "pi.screen.working", StateWorking, false},
		{"pi", "overlay_compatibility.txt", "pi.overlay-or-interruption.retain", StateUnknown, true},
		{"pi", "interrupted_compatibility.txt", "pi.overlay-or-interruption.retain", StateUnknown, true},
		{"pi", "false_positive.txt", "pi.process-mismatch", StateUnknown, false},
		{"copilot", "blocked_compatibility.txt", "copilot.screen.blocked", StateBlocked, false},
		{"copilot", "overlay_compatibility.txt", "copilot.overlay-or-interruption.retain", StateUnknown, true},
		{"copilot", "interrupted_compatibility.txt", "copilot.overlay-or-interruption.retain", StateUnknown, true},
		{"copilot", "false_positive.txt", "copilot.process-mismatch", StateUnknown, false},
		{"cursor", "blocked_compatibility.txt", "cursor.screen.write-blocked", StateBlocked, false},
		{"cursor", "overlay_compatibility.txt", "cursor.overlay-or-interruption.retain", StateUnknown, true},
		{"cursor", "interrupted_compatibility.txt", "cursor.overlay-or-interruption.retain", StateUnknown, true},
		{"cursor", "false_positive.txt", "cursor.process-mismatch", StateUnknown, false},
		{"opencode", "blocked_compatibility.txt", "opencode.screen.blocked", StateBlocked, false},
		{"opencode", "overlay_compatibility.txt", "opencode.overlay-or-interruption.retain", StateUnknown, true},
		{"opencode", "interrupted_compatibility.txt", "opencode.overlay-or-interruption.retain", StateUnknown, true},
		{"opencode", "false_positive.txt", "opencode.process-mismatch", StateUnknown, false},
		{"amp", "title_compatibility.txt", "amp.title.plugin-blocked", StateBlocked, false},
		{"amp", "overlay_compatibility.txt", "amp.overlay-or-interruption.retain", StateUnknown, true},
		{"amp", "interrupted_compatibility.txt", "amp.overlay-or-interruption.retain", StateUnknown, true},
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

func TestExpandedProviderTransientMarkersMustBeCurrentAndSpecific(t *testing.T) {
	for _, tt := range []struct{ agent, command string }{
		{"pi", "pi"}, {"copilot", "copilot"}, {"cursor", "cursor-agent"}, {"opencode", "opencode"}, {"amp", "amp"},
	} {
		t.Run(tt.agent, func(t *testing.T) {
			for name, screen := range map[string]string{
				"label without UI hint":      "Please update settings for the project",
				"hint without overlay label": "Press esc to close this issue",
				"stale overlay":              "Settings\nEsc to close\n" + strings.Repeat("current idle\n", 30),
				"stale interruption":         "Turn interrupted\n" + strings.Repeat("current idle\n", 30),
			} {
				got := Detect(Observation{Agent: tt.agent, CurrentCommand: tt.command, Screen: screen})
				if got.SkipStateUpdate || got.State != StateIdle {
					t.Fatalf("%s got %+v", name, got)
				}
			}
		})
	}
}

func TestGrokUsesTitleMetadataWithoutOSCProgress(t *testing.T) {
	got := DetectGrok(Observation{Agent: "grok", CurrentCommand: "grok-1.0.0-maco", PaneTitle: "repo - grok"})
	if got.State != StateIdle || got.Evidence != "grok.title.idle" {
		t.Fatalf("got %+v", got)
	}
	got = DetectGrok(Observation{Agent: "grok", CurrentCommand: "grok-1.0.0-maco", PaneTitle: "⠼ Working - grok"})
	if got.State != StateWorking || got.Evidence != "grok.title.working" {
		t.Fatalf("got %+v", got)
	}
}

func TestAntigravityFallbackStillDebouncesIdle(t *testing.T) {
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
	claude := DetectClaude(Observation{
		Agent: "claude", CurrentCommand: "2.1.220",
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
	tests := []struct {
		name string
		got  Result
	}{
		{
			name: "Claude resolved question remains in scrollback",
			got: DetectClaude(Observation{
				Agent: "claude", CurrentCommand: "2.1.220",
				Screen: "Which option?\nEnter to select · ↑/↓ to navigate · Esc to cancel\nanswered: Alpha\n❯ " + padding,
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
		name string
		ob   Observation
		want State
		skip bool
	}{
		{"title working", Observation{Agent: "codex", CurrentCommand: "node", PaneTitle: "⠼ repo"}, StateWorking, false},
		{"screen working", Observation{Agent: "codex", CurrentCommand: "codex", Screen: "Working (2s • esc to interrupt)"}, StateWorking, false},
		{"idle composer", Observation{Agent: "codex", CurrentCommand: "node", Screen: "\n› \n  gpt-5"}, StateIdle, false},
		{"viewer", Observation{Agent: "codex", CurrentCommand: "node", Screen: "/ T R A N S C R I P T /\nq to quit   esc to edit prev"}, StateUnknown, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DetectCodex(tt.ob)
			if got.State != tt.want || got.SkipStateUpdate != tt.skip {
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
