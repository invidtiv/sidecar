package workspaceops

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeTmuxRunner struct{ calls [][]string }

func (r *fakeTmuxRunner) Run(_ context.Context, args ...string) ([]byte, error) {
	r.calls = append(r.calls, append([]string(nil), args...))
	if len(args) > 0 && args[0] == "has-session" {
		return nil, errors.New("missing")
	}
	if len(args) > 0 && args[0] == "list-panes" {
		return []byte("%7\n"), nil
	}
	return nil, nil
}

func TestLaunchWorktreeSessionRunsEnvironmentTaskAndAgent(t *testing.T) {
	runner := &fakeTmuxRunner{}
	result, err := LaunchWorktreeSessionWithRunner(context.Background(), AgentLaunchSpec{
		SessionName: "sidecar-ws-topic", WorkDir: "/tmp/topic", AgentCommand: "codex-custom", TaskID: "td-123",
		Env: map[string]string{"CUSTOM_FLAG": "from-file"}, StartAgent: true,
	}, runner)
	if err != nil || result.PaneID != "%7" {
		t.Fatalf("launch result=%+v err=%v", result, err)
	}
	joined := ""
	for _, call := range runner.calls {
		joined += strings.Join(call, " ") + "\n"
	}
	for _, want := range []string{"new-session -d -s sidecar-ws-topic -c /tmp/topic", "CUSTOM_FLAG", "td start", "codex-custom"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("launch calls missing %q:\n%s", want, joined)
		}
	}
}

func TestResolveAgentCommandUsesConfiguredOverrideAndSkipFlag(t *testing.T) {
	got := ResolveAgentCommand(t.TempDir(), "codex", map[string]string{"codex": "codex-custom"}, true)
	if got != "codex-custom --dangerously-bypass-approvals-and-sandbox" {
		t.Fatalf("resolved command = %q", got)
	}
}
