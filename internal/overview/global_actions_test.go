package overview

import (
	"testing"

	"github.com/marcus/sidecar/internal/workspaceinventory"
)

func hasCommand(m *Model, id string) bool {
	for _, command := range m.Commands() {
		if command.ID == id {
			return true
		}
	}
	return false
}

func TestGlobalShellDeleteApplicabilityAndTargetedExecution(t *testing.T) {
	m := catalogModel(t)
	m.workspaces.SelectID("s2")
	if !hasCommand(m, "delete-shell") || hasCommand(m, "merge-workflow") {
		t.Fatalf("shell commands = %#v", m.Commands())
	}
	original := deleteManagedShell
	defer func() { deleteManagedShell = original }()
	var root, session string
	deleteManagedShell = func(projectRoot, sessionName, namespace string) error {
		root, session = projectRoot, sessionName
		return nil
	}
	m.OpenDeleteSelectedShell()
	cmd := m.applyDeleteAction(globalDeleteConfirmID)
	msg := cmd().(globalShellDeletedMsg)
	if msg.Project.Key != "sidecar" || root != "/tmp/sidecar" || session != "sidecar-sh-1" {
		t.Fatalf("delete target leaked: msg=%+v root=%q session=%q", msg, root, session)
	}
	if refresh := m.Update(msg); refresh == nil || m.DeleteOpen() {
		t.Fatalf("successful delete did not close and target-refresh: refresh=%v open=%v", refresh, m.DeleteOpen())
	}

	m.workspaces.SelectID("s1")
	if hasCommand(m, "delete-shell") {
		t.Fatal("worktree advertised shell delete")
	}
}

func TestGlobalMergeApplicabilityUsesSharedRefusalAndExistingWorkflow(t *testing.T) {
	m := catalogModel(t)
	path := t.TempDir()
	result := m.results["sidecar"]
	for i := range result.Workspaces {
		if result.Workspaces[i].ID == "s1" {
			result.Workspaces[i].Path = path
			result.Workspaces[i].ProjectRoot = path
		}
	}
	m.results["sidecar"] = result
	m.syncBoard()
	m.workspaces.SelectID("s1")
	if !hasCommand(m, "merge-workflow") {
		t.Fatalf("safe worktree did not advertise Merge: %#v", m.Commands())
	}
	msg := m.StartSelectedMerge()().(NavigateMsg)
	if msg.Action != "merge" || msg.Workspace.ID != "s1" {
		t.Fatalf("merge navigation = %+v", msg)
	}

	workspace := m.catalog["s1"]
	workspace.IsMain = true
	m.catalog["s1"] = workspace
	if mergeRefusal(workspace) == "" {
		t.Fatal("main worktree was not refused")
	}
	if cmd := m.StartSelectedMerge(); cmd != nil {
		t.Fatal("refused main worktree started merge")
	}

	m.workspaces.SelectID("s2")
	if hasCommand(m, "merge-workflow") || mergeRefusal(workspaceinventory.Workspace{Kind: workspaceinventory.KindShell}) == "" {
		t.Fatal("shell advertised merge")
	}
}
