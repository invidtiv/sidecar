package cli

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/mobileproto"
	"github.com/marcus/sidecar/internal/testenv"
	"github.com/marcus/sidecar/internal/uiapi"
	"github.com/marcus/sidecar/internal/workspaceops"
	"github.com/marcus/sidecar/internal/workspacewire"
)

func TestWorkspaceMutationRefreshesCachedWorktreesAndInactiveShellsImmediately(t *testing.T) {
	testenv.RequireTmux(t)
	_, stateDir := setupIsolatedCLI(t)
	project := t.TempDir()
	initGitRepoOnMain(t, project)
	data, _ := json.Marshal(map[string]any{"projects": map[string]any{"list": []config.ProjectConfig{{Name: "Proof", Path: project}}}})
	if err := os.MkdirAll(filepath.Dir(config.ConfigPath()), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.ConfigPath(), data, 0600); err != nil {
		t.Fatal(err)
	}
	b, err := newMobileBackend(context.Background(), Env{StateDir: stateDir})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	// Keep age and server/config fences stable: only a mutation invalidation
	// may retire the snapshot used by the immediate post-operation refetch.
	b.catalog.now = func() time.Time { return time.Unix(1000, 0) }
	b.catalog.fence = func(context.Context) (catalogFence, error) {
		return catalogFence{configGeneration: "proof", tmuxServer: "proof"}, nil
	}
	key := projKey(stateDir, project)
	mutate := func(command uiapi.WorkspaceCommand) json.RawMessage {
		t.Helper()
		data, exit, err := b.WorkspaceOperation(context.Background(), key, command)
		if err != nil || exit != 0 {
			t.Fatalf("%s: %d %v %s", command.Operation, exit, err, data)
		}
		return data
	}
	read := func() workspacewire.Workspace {
		t.Helper()
		result, err := b.Workspace(context.Background(), key, "", mobileproto.CatalogQuery{})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	var shell workspacewire.ShellCreated
	if err := json.Unmarshal(mutate(uiapi.WorkspaceCommand{Operation: "shells/create", Name: "Reopen proof"}), &shell); err != nil {
		t.Fatal(err)
	}
	_ = read()
	var plan workspaceops.WorktreePlan
	if err := json.Unmarshal(mutate(uiapi.WorkspaceCommand{Operation: "worktrees/plan", Name: "Visible feature", Base: "main"}), &plan); err != nil {
		t.Fatal(err)
	}
	mutate(uiapi.WorkspaceCommand{Operation: "worktrees/create", Name: "Visible feature", Base: "main", Confirm: true, ExpectSourceOID: plan.SourceOID})
	found := false
	for _, section := range read().Catalog.Sections {
		for _, row := range section.Rows {
			if row.DisplayName == "Visible feature" && row.WorkspaceKind == "worktree" {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("immediate workspace refetch kept the pre-create worktree catalog")
	}
	if err := exec.Command("tmux", "kill-session", "-t", "="+shell.Shell.Session).Run(); err != nil {
		t.Fatal(err)
	}
	b.invalidateCatalog()
	_ = read()
	mutate(uiapi.WorkspaceCommand{Operation: "shells/start", Target: shell.Shell.Session})
	found = false
	for _, section := range read().Catalog.Sections {
		for _, row := range section.Rows {
			if row.Session == shell.Shell.Session && row.AttachmentReady {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("immediate workspace refetch kept the inactive shell catalog")
	}
}

func TestFailedWorkspaceMutationRetiresSharedCollection(t *testing.T) {
	stateDir, _ := targetProject(t)
	h := newShareHarness()
	b := &mobileBackend{env: Env{StateDir: stateDir}, catalog: h.catalog}
	if h.get(t) != 1 {
		t.Fatal("initial collection")
	}
	_, exit, err := b.WorkspaceOperation(context.Background(), "demo", uiapi.WorkspaceCommand{Operation: "shells/start", Target: "missing"})
	if exit == 0 || err == nil {
		t.Fatal("missing shell accepted")
	}
	if h.get(t) != 2 {
		t.Fatal("failed command left a potentially partial shared collection cached")
	}
}
