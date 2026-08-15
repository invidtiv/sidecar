package workspaceops

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/tdroot"
)

func TestRunConfiguredSetupPreservesWorktreeEnvAndTDRoot(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	main := t.TempDir()
	worktree := t.TempDir()
	if err := os.WriteFile(filepath.Join(main, ".worktree-env"), []byte("CUSTOM_FLAG=from-file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(main, "setup.sh"), []byte("printf '%s' \"$CUSTOM_FLAG\" > setup-result\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	plan := &WorktreePlan{SourceWorktree: main, MainWorktree: main, Path: worktree, RunHook: true, HookPath: "setup.sh"}
	outcomes := RunConfiguredSetup(context.Background(), plan)
	for _, outcome := range outcomes {
		if outcome.Err != nil {
			t.Fatalf("setup outcome %+v", outcome)
		}
	}
	got, err := os.ReadFile(filepath.Join(worktree, "setup-result"))
	if err != nil || string(got) != "from-file" {
		t.Fatalf("hook environment = %q, err=%v", got, err)
	}
	if gotRoot := tdroot.ResolveTDRoot(main); filepath.Clean(gotRoot) != filepath.Clean(main) {
		t.Fatalf("td root = %q, want %q; outcomes=%+v", gotRoot, main, outcomes)
	}
	found := false
	for _, outcome := range outcomes {
		found = found || outcome.Kind == "td-root" && strings.Contains(outcome.Action, ".td-root")
	}
	if !found {
		t.Fatalf("shared setup did not report td-root: %+v", outcomes)
	}
}
