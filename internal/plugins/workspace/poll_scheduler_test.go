package workspace

import (
	"errors"
	"testing"

	"github.com/marcus/sidecar/internal/tty"
)

func TestAgentCaptureResultRejectedAfterSchedulerReset(t *testing.T) {
	buffer := tty.NewOutputBuffer(10)
	buffer.Update("new project")
	p := &Plugin{
		worktrees: []*Worktree{{
			Name:   "shared",
			Status: StatusActive,
			Agent:  &Agent{OutputBuf: buffer},
		}},
	}
	oldGeneration := p.pollScheduler.Invalidate(agentPollKey("shared"))
	p.pollScheduler.Reset()

	_, cmd := p.Update(AgentOutputMsg{
		WorkspaceName: "shared",
		Generation:    oldGeneration,
		Output:        "old project",
		Status:        StatusDone,
	})

	if cmd != nil {
		t.Fatal("stale capture scheduled a continuation")
	}
	if got := buffer.String(); got != "new project" {
		t.Fatalf("stale same-name capture mutated new buffer: %q", got)
	}
	if got := p.worktrees[0].Status; got != StatusActive {
		t.Fatalf("stale same-name capture changed status to %v", got)
	}
}

func TestAgentRetryRejectedAfterInvalidation(t *testing.T) {
	p := &Plugin{
		worktrees: []*Worktree{{
			Name:  "work",
			Agent: &Agent{OutputBuf: tty.NewOutputBuffer(10)},
		}},
	}
	scheduled := p.scheduleAgentPoll("work", 0)
	retry := scheduled().(pollAgentMsg)
	if retry.Generation == 0 {
		t.Fatal("scheduled retry owner used generation zero")
	}
	p.pollScheduler.Invalidate(agentPollKey("work"))

	_, cmd := p.Update(retry)
	if cmd != nil {
		t.Fatal("retry from invalidated capture owner started a new capture")
	}
}

func TestStaleShellCaptureCannotMutateOrContinue(t *testing.T) {
	buffer := tty.NewOutputBuffer(10)
	buffer.Update("current shell")
	shell := &ShellSession{
		TmuxName: "shared",
		Agent:    &Agent{OutputBuf: buffer},
	}
	p := &Plugin{shells: []*ShellSession{shell}}
	oldGeneration := p.pollScheduler.Invalidate(shellPollKey("shared"))
	p.pollScheduler.Invalidate(shellPollKey("shared"))

	_, cmd := p.Update(ShellOutputMsg{
		TmuxName:   "shared",
		Generation: oldGeneration,
		Output:     "stale shell",
	})

	if cmd != nil {
		t.Fatal("stale shell capture scheduled a continuation")
	}
	if got := buffer.String(); got != "current shell" {
		t.Fatalf("stale shell capture mutated buffer: %q", got)
	}
}

func TestTransientShellCaptureErrorPreservesLastGoodOutput(t *testing.T) {
	buffer := tty.NewOutputBuffer(10)
	buffer.Update("last good screen")
	shell := &ShellSession{
		TmuxName: "shell",
		Agent:    &Agent{OutputBuf: buffer},
	}
	p := &Plugin{shells: []*ShellSession{shell}}
	generation := p.pollScheduler.Invalidate(shellPollKey("shell"))

	_, cmd := p.Update(ShellOutputMsg{
		TmuxName:   "shell",
		Generation: generation,
		Err:        errors.New("capture timed out"),
	})

	if got := buffer.String(); got != "last good screen" {
		t.Fatalf("transient capture error cleared buffer: %q", got)
	}
	if cmd == nil {
		t.Fatal("transient capture error did not schedule retry")
	}
	if p.pollScheduler.IsCurrent(shellPollKey("shell"), generation) {
		t.Fatal("retry did not receive a fresh generation")
	}
}
