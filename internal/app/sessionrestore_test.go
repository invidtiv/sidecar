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

// TestRestoreIsFreeWhenDisabled pins that a user who has turned restore off pays
// nothing at all: no command, so no goroutine, no manifest read, no tmux call.
func TestRestoreIsFreeWhenDisabled(t *testing.T) {
	if cmd := restoreSessionsCmd(restoreConfig(false, "off")); cmd != nil {
		t.Fatal("a fully disabled restore must schedule no command")
	}
	if cmd := restoreSessionsCmd(nil); cmd != nil {
		t.Fatal("a nil config must schedule no command")
	}
	// Resume-only is still work worth scheduling.
	if cmd := restoreSessionsCmd(restoreConfig(false, "auto")); cmd == nil {
		t.Fatal("resumeAgents=auto must still schedule a restore")
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
