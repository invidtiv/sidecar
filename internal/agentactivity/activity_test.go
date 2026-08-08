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
