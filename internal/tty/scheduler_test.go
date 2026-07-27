package tty

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

type schedulerTestMsg struct {
	Generation int
}

func TestKeyedSchedulerScopesAndInvalidatesIndependently(t *testing.T) {
	var scheduler KeyedScheduler
	agent := "agent:shared"
	shell := "shell:shared"

	agentCmd := scheduler.Schedule(agent, 0, func(generation int) tea.Msg {
		return schedulerTestMsg{Generation: generation}
	})
	scheduler.Invalidate(shell)
	if !scheduler.IsCurrent(agent, 0) {
		t.Fatal("invalidating shell changed agent generation")
	}
	scheduler.Invalidate(agent)
	if scheduler.IsCurrent(agent, 0) {
		t.Fatal("stale agent generation remained current")
	}
	if got := agentCmd().(schedulerTestMsg).Generation; got != 0 {
		t.Fatalf("scheduled command token = %d, want captured generation 0", got)
	}
}

func TestKeyedSchedulerResetRejectsOutstandingTokens(t *testing.T) {
	var scheduler KeyedScheduler
	key := "panel"
	scheduler.Invalidate(key)
	old := scheduler.Current(key)
	scheduler.Reset()

	if scheduler.IsCurrent(key, old) {
		t.Fatal("reset left old generation current")
	}
	cmd := scheduler.Schedule(key, time.Nanosecond, func(generation int) tea.Msg {
		return schedulerTestMsg{Generation: generation}
	})
	if got := cmd().(schedulerTestMsg).Generation; got != scheduler.Current(key) {
		t.Fatalf("generation after reset = %d, want %d", got, scheduler.Current(key))
	}
}
