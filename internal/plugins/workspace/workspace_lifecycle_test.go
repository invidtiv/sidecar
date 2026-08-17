package workspace

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/app"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/tty"
)

type lifecycleFakeExecCommand struct {
	runs    atomic.Int32
	started chan struct{}
	release chan struct{}
}

func (c *lifecycleFakeExecCommand) Run() error {
	c.runs.Add(1)
	if c.started != nil {
		close(c.started)
	}
	if c.release != nil {
		<-c.release
	}
	return nil
}
func (*lifecycleFakeExecCommand) SetStdin(io.Reader)  {}
func (*lifecycleFakeExecCommand) SetStdout(io.Writer) {}
func (*lifecycleFakeExecCommand) SetStderr(io.Writer) {}

func TestHiddenWorkspaceRejectsQueuedPollCaptureResizeAndResizeCompletion(t *testing.T) {
	p := newTerminalEmbeddingTestPlugin()
	p.width, p.height = 100, 30
	p.sidebarVisible = false
	buf := tty.NewOutputBuffer(20)
	buf.Update("before hide")
	wt := &Worktree{
		Key: "work", Name: "work", Status: StatusActive,
		Agent: &Agent{TmuxSession: "sidecar-ws-work", TmuxPane: "%71", OutputBuf: buf},
	}
	p.worktrees = []*Worktree{wt}
	p.agents = map[string]*Agent{wt.IdentityKey(): wt.Agent}

	poll := p.scheduleAgentPoll(wt.IdentityKey(), 0)
	if poll == nil {
		t.Fatal("visible project did not arm semantic poll")
	}
	tick := poll().(pollAgentMsg)
	resize := p.resizeTmuxTargetCmd("%71")
	if resize == nil {
		t.Fatal("visible project did not construct resize")
	}

	p.SetFocused(false)
	if got := resize(); got != nil {
		t.Fatalf("stale resize completed after hide: %T", got)
	}
	if _, cmd := p.update(tick); cmd != nil {
		t.Fatal("stale poll tick captured after hide")
	}
	p.update(AgentOutputMsg{
		WorkspaceName: wt.IdentityKey(), Generation: tick.Generation,
		Output: "after hide", Status: StatusThinking,
	})
	if got := wt.Agent.OutputBuf.String(); got != "before hide" || wt.Status != StatusActive {
		t.Fatalf("hidden capture mutated state: output=%q status=%s", got, wt.Status)
	}
	if _, cmd := p.update(paneResizedMsg{}); cmd != nil {
		t.Fatal("late resize completion restarted hidden polling")
	}
}

func TestWorkspaceHideDrainsInFlightGeometryAndExcludesStaleCommand(t *testing.T) {
	originalQuery := workspaceQueryPaneSize
	originalTouch := workspaceTouchGeometryLease
	started := make(chan struct{})
	release := make(chan struct{})
	var queries atomic.Int32
	var touches atomic.Int32
	var wantWidth, wantHeight int
	workspaceQueryPaneSize = func(string) (int, int, bool) {
		if queries.Add(1) == 1 {
			close(started)
			<-release
		}
		return wantWidth, wantHeight, true
	}
	workspaceTouchGeometryLease = func(string) { touches.Add(1) }
	t.Cleanup(func() {
		workspaceQueryPaneSize = originalQuery
		workspaceTouchGeometryLease = originalTouch
	})

	p := newTerminalEmbeddingTestPlugin()
	p.width, p.height = 100, 30
	p.sidebarVisible = false
	wantWidth, wantHeight = p.calculatePreviewDimensions()
	wantWidth = p.terminalContentWidth(wantWidth)
	inFlight := p.resizeTmuxTargetCmd("%71-geometry-drain")
	stale := p.resizeTmuxTargetCmd("%71-geometry-drain")
	if inFlight == nil || stale == nil {
		t.Fatal("visible workspace did not construct geometry commands")
	}
	cmdDone := make(chan tea.Msg, 1)
	go func() { cmdDone <- inFlight() }()
	<-started
	originalBeforeDeactivate := workspaceBeforeDeactivate
	hideEntered := make(chan struct{})
	workspaceBeforeDeactivate = func() { close(hideEntered) }
	t.Cleanup(func() { workspaceBeforeDeactivate = originalBeforeDeactivate })
	hidden := make(chan struct{})
	go func() {
		p.SetFocused(false)
		close(hidden)
	}()
	<-hideEntered
	select {
	case <-hidden:
		t.Fatal("hide returned while an old ownership geometry effect was in flight")
	default:
	}
	close(release)
	select {
	case <-cmdDone:
	case <-time.After(time.Second):
		t.Fatal("in-flight workspace geometry did not drain")
	}
	select {
	case <-hidden:
	case <-time.After(time.Second):
		t.Fatal("workspace hide did not return after geometry drained")
	}
	if got := stale(); got != nil {
		t.Fatalf("stale geometry command returned %T after hide", got)
	}
	if queries.Load() != 1 || touches.Load() != 1 {
		t.Fatalf("old geometry began after hide: queries=%d touches=%d", queries.Load(), touches.Load())
	}
}

func TestApplicationBlurDoesNotRevokeVisibleWorkspaceOwnership(t *testing.T) {
	p := newTerminalEmbeddingTestPlugin()
	p.worktrees = []*Worktree{{
		Key: "work", Name: "work",
		Agent: &Agent{TmuxSession: "sidecar-ws-work", TmuxPane: "%72"},
	}}
	model := openTestTerminal(t, p, workspaceTerminalPrimary, workspaceTerminalTarget{
		Session: "sidecar-ws-work", Pane: "%72", Source: "agent", SourceID: "work",
	})
	ownership := p.currentTerminalOwnership()

	_, _ = p.Update(tea.BlurMsg{})
	if p.applicationFocused || !p.focused {
		t.Fatalf("blur conflated app focus with surface visibility: app=%v surface=%v", p.applicationFocused, p.focused)
	}
	if !model.IsActive() || p.currentTerminalOwnership() != ownership {
		t.Fatal("app blur revoked the visible project's terminal ownership")
	}
}

func TestPluginFocusRequestsInventoryRefreshForReturnedProject(t *testing.T) {
	p := New()
	p.autoShellChecked = true
	p.SetFocused(true)
	_, cmd := p.Update(app.PluginFocusedMsg{})
	if cmd == nil {
		t.Fatal("focus produced no reconciliation command")
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		if len(batch) == 0 {
			t.Fatal("focus produced an empty reconciliation batch")
		}
		msg = batch[0]()
	}
	if _, ok := msg.(RefreshMsg); !ok {
		t.Fatalf("first focus reconciliation = %T, want RefreshMsg", msg)
	}
}

func TestReinitNormalizesVisibilityBeforeFocusedProjectReactivates(t *testing.T) {
	p := New()
	p.SetFocused(true)
	root := t.TempDir()
	if err := p.Init(&plugin.Context{Epoch: 2, WorkDir: root, ProjectRoot: root}); err != nil {
		t.Fatal(err)
	}
	if p.focused || p.currentTerminalOwnership() != 0 {
		t.Fatal("Init retained the previous project's terminal ownership")
	}
	p.SetFocused(true)
	if !p.focused || p.currentTerminalOwnership() == 0 {
		t.Fatal("focused project did not reactivate ownership after Init")
	}
}

func TestHiddenWorkspaceSkipsQueuedWatchedWheelFinalEffect(t *testing.T) {
	tty.WaitForPendingSends()
	t.Cleanup(tty.WaitForPendingSends)
	originalSendWheel := workspaceSendSGRWheel
	var sends atomic.Int32
	workspaceSendSGRWheel = func(string, bool, int, int, int) error {
		sends.Add(1)
		return nil
	}
	t.Cleanup(func() { workspaceSendSGRWheel = originalSendWheel })

	target := "sidecar-sh-wheel-stale"
	blockerEntered := make(chan struct{})
	releaseBlocker := make(chan struct{})
	_ = tty.SendOrdered(target, func() error {
		close(blockerEntered)
		<-releaseBlocker
		return nil
	})
	<-blockerEntered

	p := New()
	p.SetFocused(true)
	p.shellSelected = true
	p.shells = []*ShellSession{{
		TmuxName: target,
		Agent:    &Agent{TmuxSession: target, OutputBuf: tty.NewOutputBuffer(20)},
	}}
	if cmd := p.sendTerminalWheelNotches(false, true, 2, 3, 1); cmd == nil {
		t.Fatal("visible watched pane did not queue wheel input")
	}
	p.SetFocused(false)
	close(releaseBlocker)
	tty.WaitForPendingSends()
	if sends.Load() != 0 {
		t.Fatalf("queued watched wheel began %d final effect(s) after hide", sends.Load())
	}
}

func TestQueuedAttachAfterHideDoesNotExecOrRetainGeometryHold(t *testing.T) {
	enableWorkspaceFeature(t, features.TmuxFullAttach.Name)
	originalExec := workspaceExec
	originalFactory := newWorkspaceAttachCommand
	originalRelease := workspaceReleaseGeometryHold
	fake := &lifecycleFakeExecCommand{}
	var releases atomic.Int32
	newWorkspaceAttachCommand = func(*exec.Cmd) tea.ExecCommand { return fake }
	workspaceExec = func(command tea.ExecCommand, callback tea.ExecCallback) tea.Cmd {
		return func() tea.Msg { return callback(command.Run()) }
	}
	workspaceReleaseGeometryHold = func(string) { releases.Add(1) }
	t.Cleanup(func() {
		workspaceExec = originalExec
		newWorkspaceAttachCommand = originalFactory
		workspaceReleaseGeometryHold = originalRelease
	})

	p := New()
	p.SetFocused(true)
	completed := false
	cmd := p.attachWithResize("%attach-stale", "attach-stale", "stale", func(error) tea.Msg {
		completed = true
		return nil
	})
	if cmd == nil {
		t.Fatal("visible workspace did not construct attach command")
	}
	p.SetFocused(false)
	if msg := cmd(); msg != nil {
		t.Fatalf("stale attach returned %T", msg)
	}
	if fake.runs.Load() != 0 || completed {
		t.Fatalf("stale attach launched: runs=%d completed=%v", fake.runs.Load(), completed)
	}
	if releases.Load() != 0 {
		t.Fatalf("stale attach released an unowned geometry hold %d time(s)", releases.Load())
	}
}

func TestWorkspaceHideDrainsAttachAndAttachReleasesAcquiredHold(t *testing.T) {
	enableWorkspaceFeature(t, features.TmuxFullAttach.Name)
	originalExec := workspaceExec
	originalFactory := newWorkspaceAttachCommand
	originalHold := workspaceHoldGeometryLease
	originalRelease := workspaceReleaseGeometryHold
	originalResize := workspaceResizeTmuxPane
	fake := &lifecycleFakeExecCommand{started: make(chan struct{}), release: make(chan struct{})}
	var holds, releases atomic.Int32
	newWorkspaceAttachCommand = func(*exec.Cmd) tea.ExecCommand { return fake }
	workspaceExec = func(command tea.ExecCommand, callback tea.ExecCallback) tea.Cmd {
		return func() tea.Msg { return callback(command.Run()) }
	}
	workspaceHoldGeometryLease = func(string) { holds.Add(1) }
	workspaceReleaseGeometryHold = func(string) { releases.Add(1) }
	workspaceResizeTmuxPane = func(string, int, int) {}
	t.Cleanup(func() {
		workspaceExec = originalExec
		newWorkspaceAttachCommand = originalFactory
		workspaceHoldGeometryLease = originalHold
		workspaceReleaseGeometryHold = originalRelease
		workspaceResizeTmuxPane = originalResize
	})

	p := New()
	p.width, p.height = 100, 30
	p.SetFocused(true)
	cmd := p.attachWithResize("%attach-live", "attach-live", "live", func(error) tea.Msg { return nil })
	attachDone := make(chan tea.Msg, 1)
	go func() { attachDone <- cmd() }()
	select {
	case <-fake.started:
	case <-time.After(time.Second):
		t.Fatal("attach did not reach leased exec boundary")
	}
	originalBoundary := workspaceBeforeDeactivate
	hideEntered := make(chan struct{})
	workspaceBeforeDeactivate = func() { close(hideEntered) }
	t.Cleanup(func() { workspaceBeforeDeactivate = originalBoundary })
	hidden := make(chan struct{})
	go func() {
		p.SetFocused(false)
		close(hidden)
	}()
	<-hideEntered
	select {
	case <-hidden:
		t.Fatal("hide returned while attach held terminal ownership")
	default:
	}
	close(fake.release)
	select {
	case <-attachDone:
	case <-time.After(time.Second):
		t.Fatal("attach did not drain")
	}
	select {
	case <-hidden:
	case <-time.After(time.Second):
		t.Fatal("hide did not return after attach drained")
	}
	if fake.runs.Load() != 1 || holds.Load() != 1 || releases.Load() != 1 {
		t.Fatalf("attach lifecycle runs=%d holds=%d releases=%d", fake.runs.Load(), holds.Load(), releases.Load())
	}
}

func TestRefreshReconcilesNewSessionWithoutReplacingExistingAgent(t *testing.T) {
	root := t.TempDir()
	installReconcileTmux(t, "sidecar-ws-old", "sidecar-ws-my-feature")

	p := New()
	p.ctx = &plugin.Context{Epoch: 9, WorkDir: root, ProjectRoot: root}
	p.operationCtx, p.operationCancel = context.WithCancel(context.Background())
	t.Cleanup(p.operationCancel)
	p.SetFocused(true)
	p.initialReconnectDone = true
	p.worktreesLoaded, p.stateRestored = true, true
	existingBuffer := tty.NewOutputBuffer(20)
	existingBuffer.Update("preserve me")
	existing := &Agent{TmuxSession: "sidecar-ws-old", TmuxPane: "%40", OutputBuf: existingBuffer}
	p.agents = map[string]*Agent{"old": existing}
	p.worktrees = []*Worktree{{Key: "old", Name: "old", Path: filepath.Join(root, "old"), Agent: existing}}

	refreshedOld := &Worktree{Key: "old", Name: "old", Path: filepath.Join(root, "old")}
	refreshedNew := &Worktree{Key: "new", Name: "My_Feature", Path: filepath.Join(root, "My_Feature"), ChosenAgentType: AgentClaude}
	_, cmd := p.update(RefreshDoneMsg{
		OperationScope: OperationScope{Epoch: 9},
		Worktrees:      []*Worktree{refreshedOld, refreshedNew},
	})
	var reconciled reconnectedAgentsMsg
	var found bool
	messages := collectMsgs(cmd)
	for _, msg := range messages {
		if value, ok := msg.(reconnectedAgentsMsg); ok {
			reconciled, found = value, true
		}
	}
	if !found {
		t.Fatalf("post-initial refresh did not reconcile existing tmux sessions: cmd=%T messages=%#v", cmd, messages)
	}
	p.update(reconciled)

	if refreshedOld.Agent != existing || refreshedOld.Agent.OutputBuf != existingBuffer || refreshedOld.Agent.OutputBuf.String() != "preserve me" {
		t.Fatal("incremental reconciliation replaced the already-bound agent or its buffer")
	}
	if refreshedNew.Agent == nil || refreshedNew.Agent.TmuxSession != "sidecar-ws-my-feature" {
		t.Fatalf("mixed-case worktree did not bind the canonical global-created session: %#v", refreshedNew.Agent)
	}
	if reconciled.StartValidation {
		t.Fatal("incremental refresh started a duplicate validation chain")
	}
}

func TestReconnectAliasCollisionWithBoundWorktreeCannotDoubleBindSession(t *testing.T) {
	root := t.TempDir()
	installReconcileTmux(t, "sidecar-ws-feature")
	p := New()
	p.ctx = &plugin.Context{Epoch: 10, WorkDir: root, ProjectRoot: root}
	p.SetFocused(true)
	scope := OperationScope{Epoch: 10, OperationID: "refresh-collision"}
	p.refreshOperationID = scope.OperationID
	existing := &Agent{TmuxSession: "sidecar-ws-feature", TmuxPane: "%40", OutputBuf: tty.NewOutputBuffer(20)}
	bound := &Worktree{Key: "bound", Name: "feature", Path: filepath.Join(root, "a", "feature"), Agent: existing}
	unbound := &Worktree{Key: "unbound", Name: "feature", Path: filepath.Join(root, "b", "feature")}
	p.worktrees = []*Worktree{bound, unbound}
	p.agents = map[string]*Agent{bound.IdentityKey(): existing}

	msg := p.reconnectAgents(scope, false, p.currentTerminalOwnership())()
	result, ok := msg.(reconnectedAgentsMsg)
	if !ok {
		t.Fatalf("reconnect result = %T", msg)
	}
	if len(result.Agents) != 0 {
		t.Fatalf("ambiguous bound/unbound aliases produced reconnects: %#v", result.Agents)
	}
	p.update(result)
	if bound.Agent != existing || unbound.Agent != nil || len(p.agents) != 1 {
		t.Fatalf("collision double-bound session: bound=%#v unbound=%#v agents=%#v", bound.Agent, unbound.Agent, p.agents)
	}
}

func TestReconnectPrefersCanonicalSessionWhenLegacyAliasIsListedFirst(t *testing.T) {
	root := t.TempDir()
	legacy := "sidecar-ws-My_Feature"
	canonical := "sidecar-ws-my-feature"
	installReconcileTmux(t, legacy, canonical)
	p := New()
	p.ctx = &plugin.Context{Epoch: 11, WorkDir: root, ProjectRoot: root}
	p.SetFocused(true)
	scope := OperationScope{Epoch: 11, OperationID: "refresh-canonical"}
	p.refreshOperationID = scope.OperationID
	wt := &Worktree{Key: "feature", Name: "My_Feature", Path: filepath.Join(root, "My_Feature")}
	p.worktrees = []*Worktree{wt}

	msg := p.reconnectAgents(scope, false, p.currentTerminalOwnership())()
	result, ok := msg.(reconnectedAgentsMsg)
	if !ok {
		t.Fatalf("reconnect result = %T", msg)
	}
	if len(result.Agents) != 1 || result.Agents[0].Agent.TmuxSession != canonical {
		t.Fatalf("reconnect agents = %#v, want only canonical %q", result.Agents, canonical)
	}
	p.update(result)
	if wt.Agent == nil || wt.Agent.TmuxSession != canonical {
		t.Fatalf("bound agent = %#v, want canonical %q", wt.Agent, canonical)
	}
}

func TestReconnectFromOlderRefreshCannotBindIntoNewerInventory(t *testing.T) {
	root := t.TempDir()
	p := New()
	p.ctx = &plugin.Context{Epoch: 12, WorkDir: root, ProjectRoot: root}
	p.operationCtx, p.operationCancel = context.WithCancel(context.Background())
	t.Cleanup(p.operationCancel)
	p.SetFocused(true)
	p.initialReconnectDone = true
	p.worktreesLoaded, p.stateRestored = true, true
	ownership := p.currentTerminalOwnership()

	firstScope := OperationScope{Epoch: 12, OperationID: "refresh-1"}
	p.refreshOperationID = firstScope.OperationID
	first := &Worktree{Key: "feature", Name: "feature", Path: filepath.Join(root, "feature")}
	p.update(RefreshDoneMsg{OperationScope: firstScope, Worktrees: []*Worktree{first}})
	late := reconnectedAgentsMsg{
		OperationScope: firstScope,
		Ownership:      ownership,
		Agents: []reconnectedAgent{{
			WorktreeKey: first.IdentityKey(),
			Agent:       &Agent{TmuxSession: "sidecar-ws-feature", TmuxPane: "%51", OutputBuf: tty.NewOutputBuffer(20)},
		}},
	}

	secondScope := OperationScope{Epoch: 12, OperationID: "refresh-2"}
	p.refreshOperationID = secondScope.OperationID
	second := &Worktree{Key: "feature", Name: "feature", Path: filepath.Join(root, "feature")}
	p.update(RefreshDoneMsg{OperationScope: secondScope, Worktrees: []*Worktree{second}})
	p.update(late)
	if second.Agent != nil || len(p.agents) != 0 {
		t.Fatalf("refresh-1 reconnect bound into refresh-2 inventory: agent=%#v agents=%#v", second.Agent, p.agents)
	}

	currentAgent := &Agent{TmuxSession: "sidecar-ws-feature", TmuxPane: "%52", OutputBuf: tty.NewOutputBuffer(20)}
	p.update(reconnectedAgentsMsg{
		OperationScope: secondScope,
		Ownership:      ownership,
		Agents:         []reconnectedAgent{{WorktreeKey: second.IdentityKey(), Agent: currentAgent}},
	})
	if second.Agent != currentAgent {
		t.Fatal("current refresh reconnect was rejected with the stale result")
	}
}

func TestSupersededStartupReconnectLeavesValidationForCurrentRefresh(t *testing.T) {
	root := t.TempDir()
	p := New()
	p.ctx = &plugin.Context{Epoch: 13, WorkDir: root, ProjectRoot: root}
	p.operationCtx, p.operationCancel = context.WithCancel(context.Background())
	t.Cleanup(p.operationCancel)
	p.SetFocused(true)
	p.worktreesLoaded, p.stateRestored = true, true
	ownership := p.currentTerminalOwnership()

	firstScope := OperationScope{Epoch: 13, OperationID: "startup-refresh-1"}
	p.refreshOperationID = firstScope.OperationID
	p.update(RefreshDoneMsg{OperationScope: firstScope, Worktrees: []*Worktree{{Key: "feature", Path: filepath.Join(root, "feature")}}})
	if p.initialReconnectDone {
		t.Fatal("refresh completion consumed validation intent before reconnect applied")
	}

	secondScope := OperationScope{Epoch: 13, OperationID: "startup-refresh-2"}
	p.refreshOperationID = secondScope.OperationID
	p.update(RefreshDoneMsg{OperationScope: secondScope, Worktrees: []*Worktree{{Key: "feature", Path: filepath.Join(root, "feature")}}})
	p.update(reconnectedAgentsMsg{OperationScope: firstScope, Ownership: ownership, StartValidation: true})
	if p.initialReconnectDone || p.sessionValidationScheduled {
		t.Fatal("superseded reconnect consumed or scheduled startup validation")
	}

	p.update(reconnectedAgentsMsg{OperationScope: secondScope, Ownership: ownership, StartValidation: true})
	if !p.initialReconnectDone || !p.sessionValidationScheduled {
		t.Fatalf("current reconnect did not arm validation: done=%v scheduled=%v", p.initialReconnectDone, p.sessionValidationScheduled)
	}
	// Prove the intent is consumed once: if the same result is delivered again
	// after the scheduled flag is cleared, it must not create another chain.
	p.sessionValidationScheduled = false
	p.update(reconnectedAgentsMsg{OperationScope: secondScope, Ownership: ownership, StartValidation: true})
	if p.sessionValidationScheduled {
		t.Fatal("duplicate current reconnect armed a second validation chain")
	}
}

func TestReconnectIsScopedAcrossHideAndProjectReinit(t *testing.T) {
	root := t.TempDir()
	installReconcileTmux(t, "sidecar-ws-feature")
	logPath := filepath.Join(t.TempDir(), "tmux.log")
	t.Setenv("TMUX_RECONCILE_LOG", logPath)

	newPlugin := func(epoch uint64) (*Plugin, *Worktree) {
		p := New()
		p.ctx = &plugin.Context{Epoch: epoch, WorkDir: root, ProjectRoot: root}
		p.SetFocused(true)
		wt := &Worktree{Key: "feature", Name: "feature", Path: filepath.Join(root, "feature")}
		p.worktrees = []*Worktree{wt}
		return p, wt
	}

	t.Run("hidden command never lists sessions", func(t *testing.T) {
		p, _ := newPlugin(3)
		ownership := p.currentTerminalOwnership()
		cmd := p.reconnectAgents(OperationScope{Epoch: 3, OperationID: "refresh-3"}, false, ownership)
		p.SetFocused(false)
		if msg := cmd(); msg != nil {
			t.Fatalf("hidden reconnect command returned %T", msg)
		}
		if data, err := os.ReadFile(logPath); err == nil && len(data) != 0 {
			t.Fatalf("hidden reconnect reached tmux: %q", data)
		} else if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	})

	t.Run("late result after hide is inert", func(t *testing.T) {
		_ = os.Remove(logPath)
		p, wt := newPlugin(4)
		ownership := p.currentTerminalOwnership()
		msg := p.reconnectAgents(OperationScope{Epoch: 4, OperationID: "refresh-4"}, false, ownership)()
		result, ok := msg.(reconnectedAgentsMsg)
		if !ok || len(result.Agents) != 1 {
			t.Fatalf("active reconnect result = %#v", msg)
		}
		p.SetFocused(false)
		p.update(result)
		if wt.Agent != nil {
			t.Fatal("late reconnect result rebound an agent after hide")
		}
	})

	t.Run("prior project result is rejected after reinit", func(t *testing.T) {
		_ = os.Remove(logPath)
		p, _ := newPlugin(5)
		ownership := p.currentTerminalOwnership()
		msg := p.reconnectAgents(OperationScope{Epoch: 5, OperationID: "refresh-5"}, false, ownership)()
		result, ok := msg.(reconnectedAgentsMsg)
		if !ok || len(result.Agents) != 1 {
			t.Fatalf("active reconnect result = %#v", msg)
		}
		if err := p.Init(&plugin.Context{Epoch: 6, WorkDir: root, ProjectRoot: root}); err != nil {
			t.Fatal(err)
		}
		p.SetFocused(true)
		fresh := &Worktree{Key: "feature", Name: "feature", Path: filepath.Join(root, "feature")}
		p.worktrees = []*Worktree{fresh}
		p.update(result)
		if fresh.Agent != nil {
			t.Fatal("prior-project reconnect result rebound an agent after reinit")
		}
	})
}

func installReconcileTmux(t *testing.T, sessions ...string) {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "tmux")
	body := "#!/bin/sh\n" +
		"if [ -n \"$TMUX_RECONCILE_LOG\" ]; then printf '%s\\n' \"$1\" >> \"$TMUX_RECONCILE_LOG\"; fi\n" +
		"if [ \"$1\" = \"list-sessions\" ]; then\n" +
		"  printf '%s\\n' \"$TMUX_RECONCILE_SESSIONS\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = \"display-message\" ]; then printf '%%41\\n'; exit 0; fi\n" +
		"exit 0\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("TMUX_RECONCILE_SESSIONS", strings.Join(sessions, "\n"))
}
