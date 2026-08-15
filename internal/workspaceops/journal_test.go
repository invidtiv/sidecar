package workspaceops

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadPendingCreationMigratesLiteralV1PluginJournal(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	root := filepath.Join(t.TempDir(), "repo")
	worktree := filepath.Join(filepath.Dir(root), "feature")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(worktree, 0755); err != nil {
		t.Fatal(err)
	}
	plan := &WorktreePlan{RepoKey: "repo-key", OperationID: "old-operation", MainWorktree: root, Path: worktree}
	path, err := PendingCreationPath(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	literal := fmt.Sprintf(`{
  "version": 1,
  "repoKey": "repo-key",
  "operationId": "old-operation",
  "plan": {"RepoKey":"repo-key","OperationID":"old-operation","MainWorktree":%q,"Path":%q,"Branch":"feature","AgentType":"codex"},
  "worktree": {"Key":"wt-key","RepoKey":"repo-key","Name":"Feature","Path":%q,"Branch":"feature","HEADOID":"abc123","Agent":{"TmuxSession":"must-not-migrate"}}
}`, root, worktree, worktree)
	if err := os.WriteFile(path, []byte(literal), 0644); err != nil {
		t.Fatal(err)
	}
	journal, err := LoadPendingCreation(context.Background(), root, []WorktreeRecord{{Key: "wt-key", Path: worktree, HEADOID: "abc123"}}, "repo-key")
	if err != nil {
		t.Fatal(err)
	}
	if journal == nil || journal.Version != 1 || journal.Plan.AgentType != "codex" || journal.Worktree.Path != worktree {
		t.Fatalf("migrated journal = %+v", journal)
	}
}
