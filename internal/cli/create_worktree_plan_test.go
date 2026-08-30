package cli

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/workspaceops"
)

// treeSnapshot lists every path under root, skipping git's own bookkeeping:
// read-only git commands touch index and log mtimes, and this test is about
// whether anything Sidecar owns was created, not about git's internals.
func treeSnapshot(t *testing.T, root string) []string {
	t.Helper()
	var paths []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		if rel == ".git" || strings.HasPrefix(rel, ".git"+string(filepath.Separator)) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		paths = append(paths, rel)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(paths)
	return paths
}

func planTestRepo(t *testing.T, stateDir string) (repo, cfgPath string) {
	t.Helper()
	cfgPath = filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(cfgPath, []byte(`{
  "plugins": {"workspace": {"worktreeSetup": {"runHook": true, "hookPath": ".worktree-setup.sh", "hookRequired": true}}}
}`), 0644); err != nil {
		t.Fatal(err)
	}
	repo = filepath.Join(t.TempDir(), "repo")
	initGitRepo(t, repo)
	if err := os.WriteFile(filepath.Join(repo, ".worktree-setup.sh"), []byte("#!/bin/bash\necho hi\n"), 0755); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-qm", "hook")
	if resolved, err := filepath.EvalSymlinks(repo); err == nil {
		repo = resolved
	}
	t.Chdir(repo)
	writeProjectMeta(t, stateDir, "demo", repo)
	return repo, cfgPath
}

func TestCreateWorktreePlanEmitsPlanAndMutatesNothing(t *testing.T) {
	_, stateDir := setupIsolatedCLI(t)
	repo, cfgPath := planTestRepo(t, stateDir)
	parent := filepath.Dir(repo)

	beforeParent := treeSnapshot(t, parent)
	beforeState := treeSnapshot(t, stateDir)
	beforeBranches := gitLines(t, repo, "branch", "--format=%(refname:short)")
	beforeWorktrees := gitLines(t, repo, "worktree", "list", "--porcelain")

	var out, errOut bytes.Buffer
	handled, code := Run([]string{"-config", cfgPath, "create", "worktree", "--plan", "--json", "--agent", "claude", "fix auth"}, &out, &errOut)
	if !handled || code != 0 || errOut.Len() != 0 {
		t.Fatalf("--plan = handled %v code %d stderr %q stdout %q", handled, code, errOut.String(), out.String())
	}

	var plan workspaceops.WorktreePlan
	if err := json.Unmarshal(out.Bytes(), &plan); err != nil {
		t.Fatalf("stdout is not one JSON plan: %q: %v", out.String(), err)
	}
	if plan.Branch != "fix-auth" || plan.DisplayName != "fix auth" {
		t.Fatalf("plan branch/name = %+v", plan)
	}
	if plan.Path == "" || filepath.Dir(plan.Path) != parent {
		t.Fatalf("plan path = %q, want a sibling of %q", plan.Path, repo)
	}
	if plan.SourceRef == "" || len(plan.SourceOID) != 40 {
		t.Fatalf("plan source = %q %q", plan.SourceRef, plan.SourceOID)
	}
	if !plan.RunHook || plan.HookPath != ".worktree-setup.sh" || !plan.HookRequired {
		t.Fatalf("plan hook fields = %+v", plan)
	}
	if plan.AgentType != "claude" || plan.RepoKey == "" || plan.MainWorktree != repo {
		t.Fatalf("plan identity = %+v", plan)
	}

	if _, err := os.Lstat(plan.Path); !os.IsNotExist(err) {
		t.Fatalf("--plan created %s (%v)", plan.Path, err)
	}
	if got := treeSnapshot(t, parent); !equalStrings(got, beforeParent) {
		t.Fatalf("--plan changed the checkout's parent:\nbefore %v\nafter  %v", beforeParent, got)
	}
	if got := treeSnapshot(t, stateDir); !equalStrings(got, beforeState) {
		t.Fatalf("--plan changed Sidecar state:\nbefore %v\nafter  %v", beforeState, got)
	}
	if got := gitLines(t, repo, "branch", "--format=%(refname:short)"); !equalStrings(got, beforeBranches) {
		t.Fatalf("--plan created a branch: %v", got)
	}
	if got := gitLines(t, repo, "worktree", "list", "--porcelain"); !equalStrings(got, beforeWorktrees) {
		t.Fatalf("--plan added a worktree: %v", got)
	}
}

func TestCreateWorktreePlanHumanOutput(t *testing.T) {
	_, stateDir := setupIsolatedCLI(t)
	repo, cfgPath := planTestRepo(t, stateDir)

	var out, errOut bytes.Buffer
	handled, code := Run([]string{"-config", cfgPath, "create", "worktree", "--plan", "fix auth"}, &out, &errOut)
	if !handled || code != 0 || errOut.Len() != 0 {
		t.Fatalf("--plan = handled %v code %d stderr %q", handled, code, errOut.String())
	}
	for _, want := range []string{"Branch:  fix-auth", "Path:    " + filepath.Dir(repo), "Source:", "Hook:    .worktree-setup.sh (required)"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("stdout %q is missing %q", out.String(), want)
		}
	}
}

// A plan that cannot be resolved is still a plan-time refusal, not a partial
// creation: the validation exit code is what the confirming caller sees.
func TestCreateWorktreePlanRefusesInvalidPlan(t *testing.T) {
	_, stateDir := setupIsolatedCLI(t)
	repo, cfgPath := planTestRepo(t, stateDir)
	runGit(t, repo, "branch", "taken")

	var out, errOut bytes.Buffer
	handled, code := Run([]string{"-config", cfgPath, "create", "worktree", "--plan", "--json", "taken"}, &out, &errOut)
	if !handled || code != 2 {
		t.Fatalf("existing branch = handled %v code %d stderr %q", handled, code, errOut.String())
	}
	if !strings.Contains(errOut.String(), "already exists") {
		t.Fatalf("stderr = %q", errOut.String())
	}
}

func TestCreateWorktreePlanRefusesLaunchFlags(t *testing.T) {
	for _, args := range [][]string{
		{"create", "worktree", "--plan", "--run", "true", "x"},
		{"create", "worktree", "--plan", "--no-launch", "x"},
	} {
		var out, errOut bytes.Buffer
		handled, code := Run(args, &out, &errOut)
		if !handled || code != 2 {
			t.Fatalf("Run(%v) = handled %v code %d", args, handled, code)
		}
		if !strings.Contains(errOut.String(), "--plan cannot be combined with --run or --no-launch") {
			t.Fatalf("stderr = %q", errOut.String())
		}
	}
}

func gitLines(t *testing.T, dir string, args ...string) []string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	sort.Strings(lines)
	return lines
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
