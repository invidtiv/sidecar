package overview

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/uirequest"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

func TestOverview_UIRequestPendingView(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv("SIDECAR_ISOLATED_STATE", "1")

	m := &Model{
		catalog: map[string]workspaceinventory.Workspace{
			"ws-1": {
				ID:       "ws-1",
				TmuxName: "sidecar-sh-sidecar-1",
			},
		},
	}

	req := uirequest.Request{
		ID:        "req-ov-1",
		Action:    uirequest.ActionOpen,
		CreatedAt: time.Now().UTC(),
		TTLMs:     5000,
		Origin: uirequest.Origin{
			TmuxSession: "sidecar-sh-sidecar-1",
		},
		Target: uirequest.Target{
			Kind:  uirequest.TargetKindFile,
			Value: "README.md",
		},
	}

	// When not selected, queues and acks StatusQueued
	cmd := m.handleUIRequest(req)
	if cmd != nil {
		t.Errorf("expected nil cmd for unselected workspace, got %v", cmd)
	}

	badge, hasBadge := m.pendingViewBadge("sidecar-sh-sidecar-1")
	if !hasBadge || badge == "" {
		t.Errorf("expected pending view badge on ws-1, got %q, %v", badge, hasBadge)
	}

	acks, err := uirequest.ReadAcks(filepath.Join(stateHome, "sidecar"), req.ID, req.Action)
	if err != nil {
		t.Fatalf("ReadAcks error: %v", err)
	}
	if len(acks) != 1 || acks[0].Status != uirequest.StatusQueued {
		t.Fatalf("expected 1 queued ack, got %+v", acks)
	}
}

func TestOverview_WorktreeRenameRepaintsSharedCatalog(t *testing.T) {
	path := workspaceinventory.CanonicalPath(t.TempDir())
	m := New(workspaceinventory.Collector{})
	m.projects = []Project{{Name: "sidecar", Path: path}}
	key := projectKey(m.projects[0])
	workspace := workspaceinventory.Workspace{
		ID: key + ":worktree:" + path, ProjectKey: key, ProjectName: "sidecar",
		Kind: workspaceinventory.KindWorktree, Path: path, Key: path, Name: "panes",
	}
	m.results[key] = workspaceinventory.ProjectResult{ProjectKey: key, Workspaces: []workspaceinventory.Workspace{workspace}}
	m.syncBoard()

	m.handleUIRequest(uirequest.Request{
		Action: uirequest.ActionRenameWorktree,
		Origin: uirequest.Origin{WorkDir: path},
		Target: uirequest.Target{Kind: uirequest.TargetKindWorktree, Value: "pane handle polish"},
	})
	if got := m.results[key].Workspaces[0].Name; got != "pane handle polish" {
		t.Fatalf("result name = %q", got)
	}
	if got := m.catalog[workspace.ID].Name; got != "pane handle polish" {
		t.Fatalf("catalog name = %q", got)
	}
}

func TestOverview_ShellRenameRepaintsSharedCatalog(t *testing.T) {
	path := workspaceinventory.CanonicalPath(t.TempDir())
	m := New(workspaceinventory.Collector{})
	m.projects = []Project{{Name: "sidecar", Path: path}}
	key := projectKey(m.projects[0])
	workspace := workspaceinventory.Workspace{
		ID: key + ":shell:sidecar-sh-sidecar-1", ProjectKey: key, ProjectName: "sidecar",
		Kind: workspaceinventory.KindShell, TmuxName: "sidecar-sh-sidecar-1", Name: "stale shell",
	}
	m.results[key] = workspaceinventory.ProjectResult{ProjectKey: key, Workspaces: []workspaceinventory.Workspace{workspace}}
	m.syncBoard()

	m.handleUIRequest(uirequest.Request{
		Action: uirequest.ActionRenameShell,
		Origin: uirequest.Origin{TmuxSession: "sidecar-sh-sidecar-1"},
		Target: uirequest.Target{Kind: uirequest.TargetKindShell, Value: "active task context"},
	})
	if got := m.results[key].Workspaces[0].Name; got != "active task context" {
		t.Fatalf("result name = %q", got)
	}
	if got := m.catalog[workspace.ID].Name; got != "active task context" {
		t.Fatalf("catalog name = %q", got)
	}
}

func TestOverview_PendingDiffLastWriteWins(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv("SIDECAR_ISOLATED_STATE", "1")

	m := &Model{
		catalog: map[string]workspaceinventory.Workspace{
			"ws-1": {ID: "ws-1", TmuxName: "sidecar-sh-sidecar-1"},
		},
	}
	first := uirequest.Request{
		ID: "req-ov-diff-1", Action: uirequest.ActionOpen,
		CreatedAt: time.Now().UTC(), TTLMs: 5000,
		Origin: uirequest.Origin{TmuxSession: "sidecar-sh-sidecar-1"},
		Target: uirequest.Target{Kind: uirequest.TargetKindDiff, Value: "wt"},
	}
	second := first
	second.ID = "req-ov-diff-2"
	second.Target.Value = "c:def5678"

	m.handleUIRequest(first)
	m.handleUIRequest(second)
	pv := m.pendingViews["sidecar-sh-sidecar-1"]
	if pv == nil || pv.Target.Value != "c:def5678" {
		t.Fatalf("pending = %+v, want last-write-wins c:def5678", pv)
	}
	if len(m.pendingViews) != 1 {
		t.Fatalf("pending slots = %d, want one", len(m.pendingViews))
	}
}
