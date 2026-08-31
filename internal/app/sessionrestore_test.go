package app

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/sessionrestore"
)

func restoreConfig(recreate bool, mode string) *config.Config {
	cfg := &config.Config{}
	cfg.Plugins.Workspace.SessionRestore = config.SessionRestoreConfig{
		RecreateShells: recreate,
		ResumeAgents:   mode,
	}
	return cfg
}

// TestRestoreDoesNoWorkBeforeTheFirstReadyFrame is the startup-latency
// invariant as behavior rather than as a comment.
//
// The plan requires that startup tracing show no tmux spawn, provider-store
// walk, or restore write before `first ready frame`. Reading a trace proves it
// once, on one machine; this proves it every time the suite runs, and it fails
// if someone later hoists a manifest read above the gate for convenience.
func TestRestoreDoesNoWorkBeforeTheFirstReadyFrame(t *testing.T) {
	gate := make(chan struct{})
	var worked atomic.Bool

	prevGate, prevWork := sessionRestoreGate, sessionRestoreWork
	t.Cleanup(func() { sessionRestoreGate, sessionRestoreWork = prevGate, prevWork })
	sessionRestoreGate = func() <-chan struct{} { return gate }
	sessionRestoreWork = func(context.Context, sessionrestore.Config) tea.Msg {
		worked.Store(true)
		return SessionRestoredMsg{}
	}

	cmd := restoreSessionsCmd(restoreConfig(true, "ask"))
	if cmd == nil {
		t.Fatal("an enabled configuration must schedule a restore")
	}

	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()

	// Give the command a real opportunity to misbehave. If any work happens
	// here, it happened before the first ready frame.
	time.Sleep(50 * time.Millisecond)
	if worked.Load() {
		t.Fatal("the restore did work before the first ready frame")
	}
	select {
	case <-done:
		t.Fatal("the restore returned before the first ready frame")
	default:
	}

	close(gate)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the restore never ran after the first ready frame")
	}
	if !worked.Load() {
		t.Fatal("the restore never did its work")
	}
}

// TestInitSchedulesNoBlockingRestore is a regression test for a hang.
//
// The restore command parks on the first-ready-frame latch, which only closes
// when View renders a model with real dimensions. Returned from Init, that
// command hung every caller which runs Init's commands synchronously — which is
// what collectMsgs does, and what an embedder could reasonably do. The restore
// is now started from the first WindowSizeMsg instead, so it exists only inside
// a program that is actually driving a UI and can therefore close the latch.
//
// The test drives Init's commands to completion with a deadline. It is the
// deadline that is the assertion: any Init command that waits on the latch will
// never return.
func TestInitSchedulesNoBlockingRestore(t *testing.T) {
	m, _ := scopeBaselineModel(t, "git")

	done := make(chan struct{})
	go func() {
		defer close(done)
		collectMsgs(m.Init())
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("an Init command blocked; nothing returned from Init may wait on the first ready frame")
	}
}

// TestRestoreIsFreeWhenDisabled pins that a user who has turned restore off pays
// nothing at all: no command, so no goroutine, no manifest read, no tmux call.
func TestRestoreIsFreeWhenDisabled(t *testing.T) {
	if cmd := restoreSessionsCmd(restoreConfig(false, "off")); cmd != nil {
		t.Fatal("a fully disabled restore must schedule no command")
	}
	if cmd := restoreSessionsCmd(nil); cmd != nil {
		t.Fatal("a nil config must schedule no command")
	}
	// recreateShells off schedules nothing even with resumeAgents=auto, because
	// the planner refuses to recreate before it considers an agent, so no shell
	// in that configuration can host a resume. This is the case that hung the
	// app suite: a zero-value config has RecreateShells false and an empty
	// resumeAgents that parses to "ask", and the earlier guard read that
	// combination as work to do, parking a goroutine on the first-frame latch
	// that a synchronous test command-runner then waited on forever.
	if cmd := restoreSessionsCmd(restoreConfig(false, "auto")); cmd != nil {
		t.Fatal("recreateShells=false must schedule nothing, whatever resumeAgents says")
	}
	if cmd := restoreSessionsCmd(&config.Config{}); cmd != nil {
		t.Fatal("a zero-value config must schedule no restore")
	}
}

// TestRestoreCancelsCleanlyBeforeTheFirstFrame covers the shutdown path: a
// process that exits during startup must not leave the restore running.
func TestRestoreCancelsCleanlyBeforeTheFirstFrame(t *testing.T) {
	gate := make(chan struct{})
	defer close(gate)
	var worked atomic.Bool

	prevGate, prevWork := sessionRestoreGate, sessionRestoreWork
	t.Cleanup(func() { sessionRestoreGate, sessionRestoreWork = prevGate, prevWork })
	sessionRestoreGate = func() <-chan struct{} { return gate }
	sessionRestoreWork = func(context.Context, sessionrestore.Config) tea.Msg {
		worked.Store(true)
		return SessionRestoredMsg{}
	}

	cmd := restoreSessionsCmd(restoreConfig(true, "ask"))
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	time.Sleep(20 * time.Millisecond)

	ShutdownSessionRestore()
	select {
	case msg := <-done:
		if msg != nil {
			t.Fatalf("a cancelled restore should produce no message, got %#v", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown did not stop the restore")
	}
	if worked.Load() {
		t.Fatal("a cancelled restore still did its work")
	}
}

// TestRestoreSummaryIsOneGroupedMessage pins the ask-policy presentation: the
// user gets one summary naming what happened and what is waiting, not one
// notification per shell.
func TestRestoreSummaryIsOneGroupedMessage(t *testing.T) {
	step := func(name string) sessionrestore.Step {
		return sessionrestore.Step{Project: "p", Session: "sess-" + name, Name: name}
	}
	msg := SessionRestoredMsg{
		Result: sessionrestore.Result{Outcomes: []sessionrestore.Outcome{
			{Step: step("builder"), Status: sessionrestore.StatusRestored, Detail: "recreated"},
			{Step: step("server"), Status: sessionrestore.StatusRestored, Detail: "recreated"},
			{Step: step("gone"), Status: sessionrestore.StatusRefused, Detail: "its working directory no longer exists"},
		}},
		Pending: []sessionrestore.Step{step("reviewer")},
	}

	title, body, targets := summariseRestore(msg)
	if !strings.Contains(title, "2 shells restored") {
		t.Errorf("title should count restored shells: %q", title)
	}
	if !strings.Contains(title, "1 shell refused") {
		t.Errorf("title should count refusals: %q", title)
	}
	if !strings.Contains(body, "reviewer") || !strings.Contains(body, "--agents --yes") {
		t.Errorf("body must name the pending conversation and how to resume it: %q", body)
	}
	// Under ask, nothing was resumed, so the summary must not claim otherwise.
	if strings.Contains(title, "resumed") {
		t.Errorf("nothing was resumed; the summary must not say so: %q", title)
	}
	if len(targets) != 3 {
		t.Errorf("every acted-on shell should be an activatable target, got %d", len(targets))
	}
}

// TestRestoreSummaryStaysSilentWhenNothingHappened keeps an ordinary restart
// quiet: every shell still running is the common case and deserves no toast.
func TestRestoreSummaryStaysSilentWhenNothingHappened(t *testing.T) {
	msg := SessionRestoredMsg{
		Result: sessionrestore.Result{Outcomes: []sessionrestore.Outcome{
			{Step: sessionrestore.Step{Session: "a"}, Status: sessionrestore.StatusReattached},
			{Step: sessionrestore.Step{Session: "b"}, Status: sessionrestore.StatusSkipped},
		}},
	}
	if title, _, _ := summariseRestore(msg); title != "" {
		t.Fatalf("a restart with nothing to restore should say nothing, got %q", title)
	}
}
