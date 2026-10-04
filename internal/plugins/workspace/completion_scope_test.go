package workspace

import (
	"bytes"
	"context"
	"errors"
	"github.com/marcus/sidecar/internal/agentcontrol"
	"github.com/marcus/sidecar/internal/workspaceinventory"
	"os"
	"path/filepath"
	"testing"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/tmuxenv"
)

func bindCompletionProject(t *testing.T, p *Plugin, root string, epoch uint64) *ShellManifest {
	t.Helper()
	if err := p.Init(&plugin.Context{Epoch: epoch, WorkDir: root, ProjectRoot: root, Config: config.Default()}); err != nil {
		t.Fatal(err)
	}
	manifest, err := LoadShellManifest(filepath.Join(root, "shells.json"))
	if err != nil {
		t.Fatal(err)
	}
	p.shellManifest = manifest
	return manifest
}

// Deterministic reproduction: deliver A's completed create after Init binds B.
// No tmux operation is needed to expose the erroneous AddShell in Update.
func TestCompletionFromPreviousProjectDoesNotWriteNewManifest(t *testing.T) {
	p := New()
	oldRoot, newRoot := t.TempDir(), t.TempDir()
	bindCompletionProject(t, p, oldRoot, 1)
	completion := ShellCreatedMsg{OperationScope: p.completionScope(), SessionName: "sidecar-sh-old-project-1", DisplayName: "Old project shell"}
	manifest := bindCompletionProject(t, p, newRoot, 2)
	_, cmd := p.Update(completion)
	if manifest.FindShell(completion.SessionName) != nil || len(p.shells) != 0 {
		t.Fatal("stale shell completion wrote the next project's manifest or sidebar")
	}
	if _, err := os.Stat(manifest.Path()); !os.IsNotExist(err) {
		t.Fatalf("stale completion wrote a durable file: %v", err)
	}
	if cmd != nil {
		t.Fatal("stale create scheduled a poll, launch, prefill, or resize")
	}
}

func TestCompletionSiblingsDoNotAlterReplacementProject(t *testing.T) {
	late := errors.New("late failure")
	constructors := []func(OperationScope) tea.Msg{
		func(s OperationScope) tea.Msg { return ShellCreatedMsg{OperationScope: s, Err: late} },
		func(s OperationScope) tea.Msg { return ShellKilledMsg{OperationScope: s, SessionName: "collision"} },
		func(s OperationScope) tea.Msg { return ShellSessionDeadMsg{OperationScope: s, TmuxName: "collision"} },
		func(s OperationScope) tea.Msg {
			return RenameShellDoneMsg{OperationScope: s, TmuxName: "collision", NewName: "old project"}
		},
		func(s OperationScope) tea.Msg {
			return ShellAgentStartedMsg{OperationScope: s, TmuxName: "collision", AgentType: AgentClaude}
		},
		func(s OperationScope) tea.Msg { return ShellAgentErrorMsg{OperationScope: s, Err: late} },
		func(s OperationScope) tea.Msg {
			return shellResumeInjectedMsg{OperationScope: s, TmuxSession: "collision"}
		},
		func(s OperationScope) tea.Msg { return shellResumeErrorMsg{OperationScope: s, Err: late} },
		func(s OperationScope) tea.Msg {
			return shellResumeResolvedMsg{OperationScope: s, ResumeArgv: []string{"codex", "resume", "old"}}
		},
		func(s OperationScope) tea.Msg { return worktreeResumeCreatedMsg{OperationScope: s, Err: late} },
		func(s OperationScope) tea.Msg { return TermPanelSessionCreatedMsg{OperationScope: s, Err: late} },
		func(s OperationScope) tea.Msg { return TermPanelSeedFailedMsg{OperationScope: s, Err: late} },
		func(s OperationScope) tea.Msg { return ApproveResultMsg{OperationScope: s, Err: late} },
		func(s OperationScope) tea.Msg { return RejectResultMsg{OperationScope: s, Err: late} },
		func(s OperationScope) tea.Msg { return TmuxAttachFinishedMsg{OperationScope: s, Err: late} },
		func(s OperationScope) tea.Msg { return docSearchMsg{OperationScope: s, LeafID: 1} },
		func(s OperationScope) tea.Msg { return WorkDirDeletedMsg{OperationScope: s, MainWorktreePath: "/old"} },
	}
	for _, constructor := range constructors {
		p := New()
		oldRoot, nextRoot := t.TempDir(), t.TempDir()
		bindCompletionProject(t, p, oldRoot, 1)
		msg := constructor(p.completionScope())
		manifest := bindCompletionProject(t, p, nextRoot, 2)
		if err := manifest.AddShell(ShellDefinition{TmuxName: "collision", DisplayName: "Next project", WorkDir: nextRoot}); err != nil {
			t.Fatal(err)
		}
		p.shells = []*ShellSession{{TmuxName: "collision", Name: "Next project", WorkDir: nextRoot}}
		p.pendingPrefillCmd = "next project's prefill"
		before, err := os.ReadFile(manifest.Path())
		if err != nil {
			t.Fatal(err)
		}
		revision := manifest.Revision()
		_, cmd := p.Update(msg)
		after, err := os.ReadFile(manifest.Path())
		if err != nil {
			t.Fatal(err)
		}
		if cmd != nil || !bytes.Equal(before, after) || manifest.Revision() != revision || len(p.shells) != 1 || p.shells[0].Name != "Next project" || p.shells[0].ChosenAgent != AgentNone || p.pendingPrefillCmd != "next project's prefill" || p.viewMode != ViewModeList {
			t.Fatalf("%T changed or scheduled work in the next project", msg)
		}
	}
}

func TestCompletionScopeChecksPathsAndReturnVisitEpoch(t *testing.T) {
	p := New()
	root := t.TempDir()
	bindCompletionProject(t, p, root, 1)
	scope := p.completionScope()
	if !p.scopeMatches(scope) {
		t.Fatal("current scope refused")
	}
	bindCompletionProject(t, p, t.TempDir(), 1)
	if p.scopeMatches(scope) {
		t.Fatal("same epoch at a different project accepted")
	}
	bindCompletionProject(t, p, root, 3)
	if p.scopeMatches(scope) {
		t.Fatal("completion from an earlier visit accepted")
	}
}

func TestDelayedRenameRetainsOriginalManifest(t *testing.T) {
	p := New()
	oldRoot, nextRoot := t.TempDir(), t.TempDir()
	old := bindCompletionProject(t, p, oldRoot, 1)
	if err := old.AddShell(ShellDefinition{TmuxName: "collision", DisplayName: "Original", WorkDir: oldRoot, Namespace: tmuxenv.Namespace()}); err != nil {
		t.Fatal(err)
	}
	p.renameShellSession = &ShellSession{TmuxName: "collision", Name: "Original", WorkDir: oldRoot}
	p.renameShellInput = textinput.New()
	p.renameShellInput.SetValue("Renamed original")
	cmd := p.executeRenameShell()
	next := bindCompletionProject(t, p, nextRoot, 2)
	if err := next.AddShell(ShellDefinition{TmuxName: "collision", DisplayName: "Next", WorkDir: nextRoot, Namespace: tmuxenv.Namespace()}); err != nil {
		t.Fatal(err)
	}
	msg := cmd().(RenameShellDoneMsg)
	if msg.Err != nil {
		t.Fatal(msg.Err)
	}
	p.Update(msg)
	if old.FindShell("collision").DisplayName != "Renamed original" || next.FindShell("collision").DisplayName != "Next" {
		t.Fatal("delayed rename used the replacement project's manifest")
	}
}

func TestDelayedShellAgentStartRetainsOriginalContext(t *testing.T) {
	p := New()
	oldRoot, nextRoot := t.TempDir(), t.TempDir()
	bindCompletionProject(t, p, oldRoot, 1)
	p.shells = []*ShellSession{{Name: "Original agent shell", TmuxName: "collision"}}
	oldWait, oldStart := waitWorkspaceShellReady, startWorkspaceAgent
	t.Cleanup(func() { waitWorkspaceShellReady, startWorkspaceAgent = oldWait, oldStart })
	waitWorkspaceShellReady = func(_ context.Context, target agentcontrol.Target, _ time.Duration) (agentcontrol.Snapshot, error) {
		return agentcontrol.Snapshot{Target: target}, nil
	}
	var request agentcontrol.StartRequest
	startWorkspaceAgent = func(_ context.Context, req agentcontrol.StartRequest) (agentcontrol.Agent, error) {
		request = req
		return agentcontrol.Agent{Target: req.Target}, nil
	}
	cmd := p.startAgentInShell("collision", AgentClaude, false)
	bindCompletionProject(t, p, nextRoot, 2)
	p.shells = []*ShellSession{{Name: "Next agent shell", TmuxName: "collision"}}
	msg, ok := cmd().(ShellAgentStartedMsg)
	if !ok || request.Target.Project != workspaceinventory.CanonicalPath(oldRoot) || request.Target.Name != "Original agent shell" || msg.Epoch != 1 || msg.ProjectRoot != oldRoot {
		t.Fatalf("delayed launch changed owner: request=%+v completion=%+v", request, msg)
	}
	p.Update(msg)
	if p.shells[0].ChosenAgent != AgentNone {
		t.Fatal("old launch changed the next project's agent")
	}
}

func TestStaleEpochStoreDiscoveryDoesNotPopulateNextProject(t *testing.T) {
	p := New()
	bindCompletionProject(t, p, t.TempDir(), 2)
	_, cmd := p.Update(tdStoreResolvedMsg{Epoch: 1, Root: "old-root"})
	if cmd != nil || len(p.tdStoreTargets) != 0 {
		t.Fatal("old td-store discovery populated the new live-watch set")
	}
}
