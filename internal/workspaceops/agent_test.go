package workspaceops

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeTmuxRunner struct {
	calls             [][]string
	sessionExists     bool
	failSendSubstring string
}

func (r *fakeTmuxRunner) Run(_ context.Context, args ...string) ([]byte, error) {
	r.calls = append(r.calls, append([]string(nil), args...))
	if len(args) > 0 && args[0] == "has-session" {
		if r.sessionExists {
			return nil, nil
		}
		return nil, errors.New("missing")
	}
	if len(args) > 0 && args[0] == "new-session" {
		r.sessionExists = true
	}
	if len(args) > 0 && args[0] == "kill-session" {
		r.sessionExists = false
	}
	if len(args) > 3 && args[0] == "send-keys" && r.failSendSubstring != "" && strings.Contains(args[3], r.failSendSubstring) {
		return []byte("send failed"), errors.New("send failed")
	}
	if len(args) > 0 && args[0] == "list-panes" {
		return []byte("%7\n"), nil
	}
	return nil, nil
}

func TestLaunchWorktreeSessionCleansUpFailedNewSessionBeforeRetry(t *testing.T) {
	for _, failCommand := range []string{"TD_SESSION_ID", "CUSTOM_FLAG", "td start"} {
		t.Run(failCommand, func(t *testing.T) {
			runner := &fakeTmuxRunner{failSendSubstring: failCommand}
			spec := AgentLaunchSpec{
				SessionName: "sidecar-ws-topic", WorkDir: "/tmp/topic", AgentCommand: "codex-custom", TaskID: "td-123",
				Env: map[string]string{"CUSTOM_FLAG": "from-file"}, StartAgent: true,
			}
			if _, err := LaunchWorktreeSessionWithRunner(context.Background(), spec, runner); err == nil {
				t.Fatalf("%s setup failure unexpectedly succeeded", failCommand)
			}
			if runner.sessionExists {
				t.Fatal("failed launch left the newly created session behind")
			}
			if got := strings.Join(flattenCalls(runner.calls), "\n"); !strings.Contains(got, "kill-session -t sidecar-ws-topic") {
				t.Fatalf("failed launch did not clean up session:\n%s", got)
			}

			runner.failSendSubstring = ""
			result, err := LaunchWorktreeSessionWithRunner(context.Background(), spec, runner)
			if err != nil {
				t.Fatalf("retry launch: %v", err)
			}
			if result.Reconnected {
				t.Fatal("retry falsely reconnected to the failed launch session")
			}
		})
	}
}

func flattenCalls(calls [][]string) []string {
	flat := make([]string, 0, len(calls))
	for _, call := range calls {
		flat = append(flat, strings.Join(call, " "))
	}
	return flat
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

func TestAgentSkipFlag(t *testing.T) {
	if got := AgentSkipFlag("codex"); got != "--dangerously-bypass-approvals-and-sandbox" {
		t.Fatalf("codex skip flag = %q", got)
	}
	if got := AgentSkipFlag("claude"); got != "--dangerously-skip-permissions" {
		t.Fatalf("claude skip flag = %q", got)
	}
	if got := AgentSkipFlag("copilot"); got != "" {
		t.Fatalf("copilot skip flag = %q, want empty", got)
	}
	if got := AgentSkipFlag(""); got != "" {
		t.Fatalf("empty agent skip flag = %q, want empty", got)
	}
	if got := AgentSkipFlag("nonesuch"); got != "" {
		t.Fatalf("unknown agent skip flag = %q, want empty", got)
	}
}
