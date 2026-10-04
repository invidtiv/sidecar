package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/agentcontrol"
	"github.com/marcus/sidecar/internal/testenv"
)

type failedCreateTerminal struct{ cliAgentTerminal }

func (*failedCreateTerminal) Launch(context.Context, agentcontrol.Snapshot, []string) error {
	return &agentcontrol.Error{Code: agentcontrol.ErrStartFailed, Message: "fixture provider failed to start", Err: errors.New("fixture exit")}
}

func TestCreateJSONReportsProviderFailureWithCreatedIdentity(t *testing.T) {
	for _, kind := range []string{"shell", "worktree"} {
		t.Run(kind, func(t *testing.T) {
			testenv.ProviderHelp(t, "codex", "usage: codex (older standalone CLI)")
			createRepoProject(t)
			previous := newAgentTerminal
			newAgentTerminal = func() agentcontrol.Terminal { return &failedCreateTerminal{} }
			t.Cleanup(func() { newAgentTerminal = previous })
			args := []string{"--enable-feature=agent_control", "create", kind, "--project", "demo", "--agent", "codex", "--json", "--wait", "0"}
			if kind == "worktree" {
				args = append(args, "failed-provider")
			}
			var out, errOut bytes.Buffer
			handled, code := Run(args, &out, &errOut)
			var result struct {
				Shell      createShellInfo `json:"shell"`
				AgentStart struct {
					Kind   string              `json:"kind"`
					Status string              `json:"status"`
					Error  *agentcontrol.Error `json:"error"`
				} `json:"agent_start"`
			}
			if err := json.Unmarshal(out.Bytes(), &result); err != nil {
				t.Fatalf("JSON: %v (%s)", err, &out)
			}
			t.Cleanup(func() { _ = exec.Command("tmux", "kill-session", "-t", result.Shell.Session).Run() })
			if !handled || code != 1 || result.Shell.Session == "" || result.AgentStart.Kind != "codex" || result.AgentStart.Status != "failed" || result.AgentStart.Error == nil || result.AgentStart.Error.Code != agentcontrol.ErrStartFailed || !strings.Contains(errOut.String(), "fixture provider failed") {
				t.Fatalf("create %s: handled=%v code=%d result=%+v stderr=%s", kind, handled, code, result, &errOut)
			}
		})
	}
}

func TestCreateAgentStartResultStates(t *testing.T) {
	if got := createdAgentStart("", false, nil); got != nil {
		t.Fatalf("no agent = %+v", got)
	}
	for _, tc := range []struct {
		requested bool
		err       error
		status    string
		code      agentcontrol.ErrorCode
	}{
		{false, nil, "not_started", ""},
		{true, nil, "ready", ""},
		{true, errors.New("unknown launch failure"), "failed", agentcontrol.ErrTransport},
	} {
		got := createdAgentStart("codex", tc.requested, tc.err)
		if got.Kind != "codex" || got.Status != tc.status {
			t.Fatalf("result = %+v", got)
		}
		if tc.code != "" && (got.Error == nil || got.Error.Code != tc.code) {
			t.Fatalf("error = %+v", got.Error)
		}
	}
}
