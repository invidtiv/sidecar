package cli

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func assertCLIJSONFixture(t *testing.T, path string, value any) {
	t.Helper()
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(got)) != strings.TrimSpace(string(want)) {
		t.Fatalf("compatibility fixture drift in %s\n--- got ---\n%s\n--- want ---\n%s", path, got, want)
	}
}

func TestM0AgentDiscoveryCompatibilityFixture(t *testing.T) {
	rendered := RenderAgents(RootCommand())
	data, err := os.ReadFile("testdata/herdr-m0/sidecar-agents-lines.txt")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if !strings.Contains(rendered, line) {
			t.Fatalf("sidecar agents lost %q:\n%s", line, rendered)
		}
	}
}

func TestM0CreateJSONCompatibilityFixtures(t *testing.T) {
	assertCLIJSONFixture(t, "testdata/herdr-m0/create-shell.json", createShellResult{Shell: createShellInfo{DisplayName: "reviewer", Session: "sidecar-sh-sidecar-4", WorkDir: "/repo"}, Acked: true, Surface: "workspace", Placement: "workspace"})
	assertCLIJSONFixture(t, "testdata/herdr-m0/create-worktree.json", createWorktreeResult{Shell: createShellInfo{DisplayName: "fix auth", Session: "sidecar-ws-fix-auth", WorkDir: "/repo-fix-auth"}, Path: "/repo-fix-auth", Branch: "fix-auth", Setup: []createSetupOutcome{}, Placement: "workspace"})
}
