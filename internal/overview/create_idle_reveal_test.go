package overview

import (
	"testing"

	"github.com/marcus/sidecar/internal/uirequest"
	"github.com/marcus/sidecar/internal/workspaceinventory"
	"github.com/marcus/sidecar/internal/workspaceops"
)

// Creating a worktree used to flip showIdleWorktrees, which dumped every
// inactive checkout across every project into Sessions. The new row is
// revealed by path; neighbors stay hidden. The CLI sends a session name
// even for --no-launch, so Session is not liveness.

func TestCreateWorktreeWithSessionDoesNotUnhideIdleNeighbors(t *testing.T) {
	m := catalogModel(t)
	addIdleNeighbor(t, m)

	focus := true
	path := workspaceinventory.CanonicalPath("/tmp/sidecar-created")
	m.applyCreateWorktreeRequest(uirequest.Request{}, uirequest.CreatePayload{
		Kind: uirequest.CreateKindWorktree, Session: "sidecar-ws-created",
		DisplayName: "created", Focus: &focus, Path: path, Branch: "created",
	}, m.projects[0], "sidecar")

	if m.showIdleWorktrees {
		t.Fatal("creating a session-backed worktree turned on showIdleWorktrees")
	}
	assertCreatedVisibleIdleHidden(t, m, path)
	selected, ok := m.SelectedWorkspace()
	if !ok || selected.Path != path {
		t.Fatalf("selected = %+v ok=%v, want the created worktree", selected, ok)
	}
	if selected.Live {
		t.Fatal("CLI session name is not liveness; --no-launch sends one too")
	}
}

func TestCreateIdleWorktreeRevealsOnlyTheTarget(t *testing.T) {
	m := catalogModel(t)
	addIdleNeighbor(t, m)

	focus := true
	path := workspaceinventory.CanonicalPath("/tmp/sidecar-idle")
	m.applyCreateWorktreeRequest(uirequest.Request{}, uirequest.CreatePayload{
		// The CLI always fills Session, including --no-launch.
		Kind: uirequest.CreateKindWorktree, Session: "sidecar-ws-idle-new",
		DisplayName: "idle-new", Focus: &focus, Path: path, Branch: "idle-new",
	}, m.projects[0], "sidecar")

	if m.showIdleWorktrees {
		t.Fatal("creating an idle worktree turned on showIdleWorktrees")
	}
	assertCreatedVisibleIdleHidden(t, m, path)
	selected, ok := m.SelectedWorkspace()
	if !ok || selected.Path != path {
		t.Fatalf("selected = %+v ok=%v, want the created idle worktree", selected, ok)
	}
	if selected.Live {
		t.Fatal("a no-session create was marked live")
	}
}

func TestLaunchedWorktreeDoesNotUnhideIdleNeighbors(t *testing.T) {
	m := catalogModel(t)
	addIdleNeighbor(t, m)
	path := workspaceinventory.CanonicalPath("/tmp/sidecar-launched")
	result := m.results["sidecar"]
	result.Workspaces = append(result.Workspaces, workspaceinventory.Workspace{
		ID: "launched", ProjectKey: "sidecar", ProjectName: "sidecar",
		Kind: workspaceinventory.KindWorktree, Name: "launched", Path: path, Branch: "launched",
	})
	m.results["sidecar"] = result
	m.syncBoard()
	if _, ok := visibleByID(m)["launched"]; ok {
		t.Fatal("premise: the not-yet-revealed idle create is hidden")
	}

	record := &workspaceops.WorktreeRecord{Path: path, Name: "launched", Branch: "launched"}
	m.Update(globalWorkspaceLaunchedMsg{Project: m.projects[0], Record: record})
	if m.showIdleWorktrees {
		t.Fatal("launching a worktree turned on showIdleWorktrees")
	}
	m.syncBoard()
	assertCreatedVisibleIdleHidden(t, m, path)
	if got := m.workspaces.SelectedID(); got != "launched" {
		t.Fatalf("selected = %q, want the launched worktree", got)
	}
}

func TestRemoteWorktreeCreateDoesNotUnhideIdleNeighbors(t *testing.T) {
	m, _ := remoteCreateModel(t)
	m.results["sidecar"] = workspaceinventory.ProjectResult{ProjectKey: "sidecar", Workspaces: []workspaceinventory.Workspace{
		{ID: "s3", ProjectKey: "sidecar", ProjectName: "sidecar", Kind: workspaceinventory.KindWorktree, Name: "dormant", Path: "/tmp/sidecar-dormant", Branch: "old", Plain: true},
	}}
	created := "/home/me/api-feature"
	results := m.hostResults["mac-mini"]
	if len(results) == 0 {
		t.Fatal("premise: remote snapshot is present")
	}
	results[0].Workspaces = append(results[0].Workspaces,
		workspaceinventory.Workspace{
			ID: "remote-created", HostID: "mac-mini", ProjectKey: remoteProjectKey(),
			Kind: workspaceinventory.KindWorktree, Name: "feature", Path: created,
		},
		workspaceinventory.Workspace{
			ID: "remote-old", HostID: "mac-mini", ProjectKey: remoteProjectKey(),
			Kind: workspaceinventory.KindWorktree, Name: "old", Path: "/home/me/api-old",
		},
	)
	m.hostResults["mac-mini"] = results
	m.syncBoard()
	if _, ok := visibleByID(m)["remote-created"]; ok {
		t.Fatal("premise: remote idle rows are hidden")
	}

	m.Update(globalWorktreeCreatedMsg{
		remoteReply: remoteReply{HostID: "mac-mini"},
		RemotePath:  created,
	})
	if m.showIdleWorktrees {
		t.Fatal("a remote worktree create turned on showIdleWorktrees")
	}
	m.syncBoard()
	vis := visibleByID(m)
	if _, ok := vis["remote-created"]; !ok {
		t.Fatal("the created remote worktree stayed hidden")
	}
	if _, ok := vis["remote-old"]; ok {
		t.Fatal("an unrelated remote idle worktree became visible")
	}
	if _, ok := vis["s3"]; ok {
		t.Fatal("a local idle worktree became visible")
	}
}

func addIdleNeighbor(t *testing.T, m *Model) {
	t.Helper()
	if m.showIdleWorktrees {
		t.Fatal("premise: hide-idle is the default")
	}
	result := m.results["sidecar"]
	result.Workspaces = append(result.Workspaces, workspaceinventory.Workspace{
		ID: "idle-neighbor", ProjectKey: "sidecar", ProjectName: "sidecar",
		Kind: workspaceinventory.KindWorktree, Name: "other-idle", Path: "/tmp/sidecar-other-idle", Branch: "other", Plain: true,
	})
	m.results["sidecar"] = result
	m.syncBoard()
	if _, ok := visibleByID(m)["s3"]; ok {
		t.Fatal("premise: fixture idle worktree is hidden")
	}
	if _, ok := visibleByID(m)["idle-neighbor"]; ok {
		t.Fatal("premise: extra idle worktree is hidden")
	}
}

func assertCreatedVisibleIdleHidden(t *testing.T, m *Model, createdPath string) {
	t.Helper()
	vis := visibleByID(m)
	if _, ok := vis["s3"]; ok {
		t.Fatal("creating a worktree unhid the fixture idle row")
	}
	if _, ok := vis["idle-neighbor"]; ok {
		t.Fatal("creating a worktree unhid an unrelated idle worktree")
	}
	for _, item := range vis {
		ws, ok := m.catalog[item.ID]
		if ok && ws.Path == createdPath {
			return
		}
	}
	t.Fatalf("created worktree %q is not visible", createdPath)
}
