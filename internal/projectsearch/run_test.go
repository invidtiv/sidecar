package projectsearch

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// The command Run hands back runs on the Bubble Tea runtime's goroutine while
// the update loop keeps typing into the same State. Everything the run needs is
// copied out before the command is returned, so the goroutine reads nothing the
// loop is writing. Without that copy this test fails under -race: the goroutine
// reads state.Query (and buildRipgrepArgs reads every option) while the loop
// below rewrites them.
func TestRunTouchesNoSharedStateFromItsGoroutine(t *testing.T) {
	state := NewState()
	state.Query = "alpha"

	cmd := Run(t.TempDir(), state, 1)
	if cmd == nil {
		t.Fatal("Run produced no command")
	}

	start := make(chan struct{})
	done := make(chan tea.Msg, 1)
	go func() {
		<-start
		done <- cmd()
	}()

	close(start)
	for i := 0; i < 200; i++ {
		state.Query += "x"
		state.UseRegex = !state.UseRegex
		state.CaseSensitive = !state.CaseSensitive
		state.WholeWord = !state.WholeWord
	}

	select {
	case <-done:
	case <-time.After(searchTimeout):
		t.Fatal("the run never finished")
	}
}

// The query the run reports on is the one it was issued for, not whatever has
// been typed since.
func TestRunSnapshotsTheQuery(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	state := NewState()
	state.Query = "alpha"
	cmd := Run(root, state, 1)

	// The user keeps typing before the runtime gets to the command.
	state.Query = "beta"

	msg, ok := cmd().(ResultsMsg)
	if !ok {
		t.Fatalf("run produced %T, want ResultsMsg", cmd())
	}
	if msg.Error != nil {
		t.Skipf("ripgrep unavailable: %v", msg.Error)
	}
	if len(msg.Results) != 1 || len(msg.Results[0].Matches) != 1 {
		t.Fatalf("results = %#v, want the one line matching the snapshotted query", msg.Results)
	}
	if got := msg.Results[0].Matches[0].LineText; got != "alpha" {
		t.Fatalf("matched %q, want the line for the query the run was issued with", got)
	}
}

// Two runs inside one epoch can finish in either order: the debounced run for
// "foo" is slow, and the immediate re-run alt+r issues is not. The newest run
// wins whichever lands last.
func TestResultsFromASupersededRunAreDropped(t *testing.T) {
	s := New(t.TempDir(), 7)

	s.State.Query = "foo"
	s.State.DebounceVersion = 1
	// The debounce fires: run one.
	if cmd := s.Update(DebounceMsg{Version: 1, Query: "foo"}); cmd == nil {
		t.Fatal("the debounce tick issued no run")
	}
	first := s.State.RunToken

	// alt+r re-runs immediately: run two.
	if cmd := s.ToggleOption(&s.State.UseRegex); cmd == nil {
		t.Fatal("toggling regex issued no run")
	}
	second := s.State.RunToken
	if second == first {
		t.Fatalf("both runs claimed token %d; they cannot be told apart", first)
	}

	stale := []SearchFileResult{{Path: "stale.go", Matches: []SearchMatch{{LineNo: 1, LineText: "stale"}}}}
	fresh := []SearchFileResult{{Path: "fresh.go", Matches: []SearchMatch{{LineNo: 2, LineText: "fresh"}}}}

	// Out of order: the newer run lands first, then the slower older one.
	s.Update(ResultsMsg{Epoch: 7, Run: second, Results: fresh})
	s.Update(ResultsMsg{Epoch: 7, Run: first, Results: stale})

	if len(s.State.Results) != 1 || s.State.Results[0].Path != "fresh.go" {
		t.Fatalf("results = %#v, want only the newest run's", s.State.Results)
	}
	if s.State.IsSearching {
		t.Fatal("the newest run's results left the search still marked in flight")
	}

	// And the ordinary order still applies the newest run.
	s.Apply(ResultsMsg{Epoch: 7, Run: second, Results: stale})
	if len(s.State.Results) != 1 || s.State.Results[0].Path != "stale.go" {
		t.Fatalf("a result for the newest run was dropped: %#v", s.State.Results)
	}
}

// Erasing the query retires the run that is still out, so its results cannot
// repopulate a search the user has just cleared.
func TestClearingTheQueryDropsTheRunInFlight(t *testing.T) {
	s := New(t.TempDir(), 3)
	s.State.Query = "f"
	s.Update(DebounceMsg{Version: 0, Query: "f"})
	run := s.State.RunToken

	s.HandleKey(tea.KeyPressMsg{Code: tea.KeyBackspace})
	if s.State.Query != "" {
		t.Fatalf("backspace left query %q", s.State.Query)
	}

	s.Apply(ResultsMsg{Epoch: 3, Run: run, Results: []SearchFileResult{{Path: "late.go"}}})
	if len(s.State.Results) != 0 {
		t.Fatalf("results for the erased query landed anyway: %#v", s.State.Results)
	}
}

// A hand-built message (a host that does its own staleness filtering, a test)
// carries no run token and is always applied.
func TestUntokenedResultsStillApply(t *testing.T) {
	s := New(t.TempDir(), 0)
	s.State.RunToken = 4
	s.Apply(ResultsMsg{Results: []SearchFileResult{{Path: "hand.go"}}})
	if len(s.State.Results) != 1 {
		t.Fatalf("results = %#v, want the hand-built message applied", s.State.Results)
	}
}

// fakeRipgrep points the runner at a script that outlives any test, so a run
// that is still going can be observed ending early rather than on its own.
func fakeRipgrep(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "slow-rg")
	body := "#!/bin/sh\nsleep 120\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	prev := ripgrepBin
	ripgrepBin = script
	t.Cleanup(func() { ripgrepBin = prev })
}

// Closing the surface kills the ripgrep process it started. Without the
// cancellable context the run holds on to its 30s timeout, and rapid open/close
// leaves one process alive per open.
func TestCloseKillsTheRunningSearch(t *testing.T) {
	fakeRipgrep(t)

	s := New(t.TempDir(), 1)
	s.State.Query = "anything"
	cmd := s.Update(DebounceMsg{Version: 0, Query: "anything"})
	if cmd == nil {
		t.Fatal("the debounce tick issued no run")
	}

	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()

	// Give the process a moment to be running before it is killed.
	time.Sleep(50 * time.Millisecond)
	s.Close()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close left the search process running")
	}
}

// Esc does the same: dismissing the surface is not a reason to keep grepping.
func TestEscKillsTheRunningSearch(t *testing.T) {
	fakeRipgrep(t)

	s := New(t.TempDir(), 1)
	s.State.Query = "anything"
	cmd := s.Update(DebounceMsg{Version: 0, Query: "anything"})
	if cmd == nil {
		t.Fatal("the debounce tick issued no run")
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	time.Sleep(50 * time.Millisecond)

	res, _ := s.HandleKey(tea.KeyPressMsg{Code: tea.KeyEsc})
	if res.Outcome != OutcomeCancelled {
		t.Fatalf("esc gave outcome %v, want cancelled", res.Outcome)
	}

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("esc left the search process running")
	}
}

// A new run supersedes the old one's process as well as its results.
func TestANewRunKillsThePreviousOne(t *testing.T) {
	fakeRipgrep(t)

	s := New(t.TempDir(), 1)
	s.State.Query = "anything"
	first := s.Update(DebounceMsg{Version: 0, Query: "anything"})
	if first == nil {
		t.Fatal("the debounce tick issued no run")
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- first() }()
	time.Sleep(50 * time.Millisecond)

	if cmd := s.ToggleOption(&s.State.UseRegex); cmd == nil {
		t.Fatal("toggling regex issued no run")
	}

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the superseded run kept running")
	}
	s.Close()
}

// RunContext with a dead context never starts a process at all.
func TestRunContextRefusesADeadContext(t *testing.T) {
	fakeRipgrep(t)

	state := NewState()
	state.Query = "anything"
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cmd := RunContext(ctx, t.TempDir(), state, 1)
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()

	select {
	case msg := <-done:
		res, ok := msg.(ResultsMsg)
		if !ok {
			t.Fatalf("run produced %T, want ResultsMsg", msg)
		}
		if res.Error == nil {
			t.Fatal("a cancelled run reported success")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a cancelled run still waited on the process")
	}
}
