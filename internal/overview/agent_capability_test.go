package overview

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/marcus/sidecar/internal/workspaceops"
)

func failingGlobalProvider(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	marker := filepath.Join(dir, "probe-cwd")
	script := "#!/bin/sh\nprintf '%s' \"$PWD\" > '" + marker + "'\nexit 2\n"
	if err := os.WriteFile(filepath.Join(dir, "codex"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return marker
}

func TestGlobalOpaqueOverrideDoesNotProbeUnusedProvider(t *testing.T) {
	marker := failingGlobalProvider(t)
	got, err := globalAgentLaunchArgvInDir(t.TempDir(), "codex", map[string]string{"codex": "custom-agent --flag"}, false, "custom-agent --flag", nil)
	if err != nil || !reflect.DeepEqual(got, []string{"sh", "-lc", "custom-agent --flag"}) {
		t.Fatalf("opaque launch = %v, %v", got, err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("unused provider was probed: %v", err)
	}
}

func TestGlobalWorktreeCapabilityProbeRunsInCommandAtDestination(t *testing.T) {
	marker := failingGlobalProvider(t)
	destination, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m := catalogModel(t)
	plan := &workspaceops.WorktreePlan{AgentType: "codex"}
	record := &workspaceops.WorktreeRecord{Path: destination, Name: "created"}
	cmd := m.launchCreatedWorktree(Project{Path: destination}, plan, record)
	if cmd == nil {
		t.Fatal("missing launch command")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("command construction probed provider: %v", err)
	}
	msg := cmd().(globalWorkspaceLaunchedMsg)
	if msg.Err == nil {
		t.Fatal("failed capability probe must refuse before creating a session")
	}
	got, err := os.ReadFile(marker)
	if err != nil || string(got) != destination {
		t.Fatalf("probe cwd = %q, %v; want %q", got, err, destination)
	}
}
