package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/agentcontrol"
	"github.com/marcus/sidecar/internal/shellstate"
)

func agentFixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "agentactivity", "testdata", "codex", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func runAgentCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	handled, code := Run(append([]string{"--enable-feature=agent_control"}, args...), &out, &errOut)
	if !handled {
		t.Fatalf("Run(%v) was not handled", args)
	}
	return code, out.String(), errOut.String()
}

// TestAgentPromptSendsWaitsAndReportsUnderOnePinnedTarget covers the happy path
// of the two verbs that write: the prompt lands, the wait settles, and both
// answer with the same Agent shape list/get/start use.
func TestAgentPromptSendsWaitsAndReportsUnderOnePinnedTarget(t *testing.T) {
	screens := []string{agentFixture(t, "startup_idle.txt"), agentFixture(t, "working.txt"), agentFixture(t, "completed.txt")}
	targetProject(t)
	terminal := &cliAgentTerminal{launched: true, screens: screens}
	useCLIAgentTerminal(t, terminal)

	code, out, errOut := runAgentCLI(t, "agent", "prompt", "sidecar-sh-demo-2", "Review the current diff.", "--wait", "--timeout", "10s", "--json")
	if code != 0 || errOut != "" {
		t.Fatalf("prompt = %d stdout=%q stderr=%q", code, out, errOut)
	}
	var agent agentcontrol.Agent
	if err := json.Unmarshal([]byte(out), &agent); err != nil {
		t.Fatalf("prompt JSON %q: %v", out, err)
	}
	if agent.Target.Session != "sidecar-sh-demo-2" || agent.Target.PaneID != "%11" || agent.Agent.Kind != "codex" {
		t.Fatalf("prompt agent = %+v", agent)
	}
	settled := agent.Agent.Status == agentcontrol.StatusDone || agent.Agent.Status == agentcontrol.StatusIdle || agent.Agent.Status == agentcontrol.StatusBlocked
	if !settled {
		t.Fatalf("prompt returned at %s, which is not a settled state", agent.Agent.Status)
	}
	if terminal.inspects < 3 {
		t.Fatalf("inspected %d times; --wait returned without observing the turn", terminal.inspects)
	}
	if len(terminal.submitted) != 1 || terminal.submitted[0] != "Review the current diff." {
		t.Fatalf("submitted = %q", terminal.submitted)
	}
}

func TestAgentPromptUsesTheCurrentShellWhenNoTargetIsNamed(t *testing.T) {
	screens := []string{agentFixture(t, "startup_idle.txt"), agentFixture(t, "working.txt")}
	targetProject(t)
	terminal := &cliAgentTerminal{launched: true, screens: screens}
	useCLIAgentTerminal(t, terminal)
	t.Setenv(shellstate.SessionEnv, "sidecar-sh-demo-1")

	code, out, errOut := runAgentCLI(t, "agent", "prompt", "look at the failing test", "--json")
	if code != 0 || errOut != "" {
		t.Fatalf("prompt = %d stdout=%q stderr=%q", code, out, errOut)
	}
	var agent agentcontrol.Agent
	if err := json.Unmarshal([]byte(out), &agent); err != nil {
		t.Fatal(err)
	}
	if agent.Target.Session != "sidecar-sh-demo-1" {
		t.Fatalf("target = %+v, want the current shell", agent.Target)
	}
	if len(terminal.submitted) != 1 || terminal.submitted[0] != "look at the failing test" {
		t.Fatalf("submitted = %q; the single positional is the prompt, not a target", terminal.submitted)
	}
}

func TestAgentPromptRefusesABlockedTargetWithoutWritingBytes(t *testing.T) {
	blocked := agentFixture(t, "blocked.txt")
	targetProject(t)
	terminal := &cliAgentTerminal{launched: true, screen: blocked}
	useCLIAgentTerminal(t, terminal)

	code, out, errOut := runAgentCLI(t, "agent", "prompt", "sidecar-sh-demo-2", "do it anyway", "--json")
	if code != 5 || out != "" || !strings.Contains(errOut, `"code":"agent_blocked"`) {
		t.Fatalf("blocked prompt = %d stdout=%q stderr=%q", code, out, errOut)
	}
	if len(terminal.submitted) != 0 {
		t.Fatalf("a refusal wrote %q", terminal.submitted)
	}
}

func TestAgentWaitAndPromptRefuseAnImplicitTimeout(t *testing.T) {
	idle := agentFixture(t, "startup_idle.txt")
	targetProject(t)
	terminal := &cliAgentTerminal{launched: true, screen: idle}
	useCLIAgentTerminal(t, terminal)

	for _, args := range [][]string{
		{"agent", "wait", "sidecar-sh-demo-2", "--json"},
		{"agent", "prompt", "sidecar-sh-demo-2", "go", "--wait", "--json"},
	} {
		code, out, errOut := runAgentCLI(t, args...)
		if code != 2 || out != "" || !strings.Contains(errOut, "timeout") {
			t.Fatalf("%v = %d stdout=%q stderr=%q; want a usage error", args, code, out, errOut)
		}
	}
	if len(terminal.submitted) != 0 {
		t.Fatalf("a usage error wrote %q", terminal.submitted)
	}

	// --timeout and --until without --wait are equally a usage error: silently
	// ignoring them would let a caller believe it waited.
	if code, _, _ := runAgentCLI(t, "agent", "prompt", "sidecar-sh-demo-2", "go", "--timeout", "1s"); code != 2 {
		t.Fatalf("--timeout without --wait = %d, want 2", code)
	}
}

func TestAgentWaitRejectsAnUnknownState(t *testing.T) {
	targetProject(t)
	code, _, errOut := runAgentCLI(t, "agent", "wait", "sidecar-sh-demo-2", "--until", "settled", "--timeout", "1s")
	if code != 2 || !strings.Contains(errOut, "unknown status") {
		t.Fatalf("code = %d stderr = %q", code, errOut)
	}
}

func TestAgentReadPassesTheSourceThroughAndPrintsTheText(t *testing.T) {
	idle := agentFixture(t, "startup_idle.txt")
	targetProject(t)
	terminal := &cliAgentTerminal{launched: true, screen: idle}
	useCLIAgentTerminal(t, terminal)

	code, out, errOut := runAgentCLI(t, "agent", "read", "sidecar-sh-demo-2", "--source", "recent-unwrapped", "--lines", "120")
	if code != 0 || errOut != "" || !strings.Contains(out, "recent-unwrapped capture") {
		t.Fatalf("read = %d stdout=%q stderr=%q", code, out, errOut)
	}
	if len(terminal.captured) != 1 || terminal.captured[0].Source != agentcontrol.SourceRecentUnwrapped || terminal.captured[0].Lines != 120 {
		t.Fatalf("capture request = %+v", terminal.captured)
	}

	code, out, errOut = runAgentCLI(t, "agent", "read", "sidecar-sh-demo-2", "--json")
	if code != 0 || errOut != "" {
		t.Fatalf("read json = %d stderr=%q", code, errOut)
	}
	var result agentcontrol.ReadResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	if result.Source != agentcontrol.SourceVisible || result.Kind != "codex" || result.Target.Session != "sidecar-sh-demo-2" {
		t.Fatalf("read result = %+v", result)
	}

	code, _, errOut = runAgentCLI(t, "agent", "read", "sidecar-sh-demo-2", "--source", "screenshot")
	if code != 2 || !strings.Contains(errOut, "unknown source") {
		t.Fatalf("unknown source = %d stderr=%q", code, errOut)
	}
}

func TestAgentReadTranscriptIsUnavailableUntilM3(t *testing.T) {
	idle := agentFixture(t, "startup_idle.txt")
	targetProject(t)
	terminal := &cliAgentTerminal{launched: true, screen: idle}
	useCLIAgentTerminal(t, terminal)

	code, out, errOut := runAgentCLI(t, "agent", "read", "sidecar-sh-demo-2", "--source", "transcript", "--json")
	if code != 5 || out != "" || !strings.Contains(errOut, `"code":"transcript_unavailable"`) {
		t.Fatalf("transcript read = %d stdout=%q stderr=%q", code, out, errOut)
	}
	if len(terminal.captured) != 0 {
		t.Fatal("an unavailable transcript fell back to scraping the terminal")
	}
}

func TestAgentSendKeysValidatesBeforeItResolvesOrWrites(t *testing.T) {
	blocked := agentFixture(t, "blocked.txt")
	targetProject(t)
	terminal := &cliAgentTerminal{launched: true, screen: blocked}
	useCLIAgentTerminal(t, terminal)

	code, out, errOut := runAgentCLI(t, "agent", "send-keys", "sidecar-sh-demo-2", "down", "enter", "--json")
	if code != 0 || errOut != "" {
		t.Fatalf("send-keys = %d stdout=%q stderr=%q", code, out, errOut)
	}
	if strings.Join(terminal.keys, ",") != "down,enter" {
		t.Fatalf("keys = %q", terminal.keys)
	}

	terminal.keys = nil
	code, out, errOut = runAgentCLI(t, "agent", "send-keys", "sidecar-sh-demo-2", "down", "cmd+q")
	if code != 2 || out != "" || !strings.Contains(errOut, "cmd+q") {
		t.Fatalf("bad key = %d stdout=%q stderr=%q; want a usage error naming the key", code, out, errOut)
	}
	if len(terminal.keys) != 0 {
		t.Fatalf("one bad key still wrote %q", terminal.keys)
	}

	// One positional is a key for the current shell, not a target.
	t.Setenv(shellstate.SessionEnv, "sidecar-sh-demo-1")
	terminal.keys = nil
	code, _, errOut = runAgentCLI(t, "agent", "send-keys", "esc", "--json")
	if code != 0 || errOut != "" {
		t.Fatalf("current-shell send-keys = %d stderr=%q", code, errOut)
	}
	if strings.Join(terminal.keys, ",") != "esc" {
		t.Fatalf("keys = %q", terminal.keys)
	}
}

func TestAgentInteractVerbsAreDiscoverableWhileTheFeatureIsDisabled(t *testing.T) {
	setupIsolatedCLI(t)
	for _, verb := range []string{"prompt", "wait", "read", "send-keys"} {
		var out, errOut bytes.Buffer
		handled, code := Run([]string{"agent", verb, "--help"}, &out, &errOut)
		if !handled || code != 0 || errOut.Len() != 0 || !strings.Contains(out.String(), "sidecar agent "+verb) {
			t.Fatalf("%s help = handled=%v code=%d stderr=%q", verb, handled, code, errOut.String())
		}
	}
	for _, args := range [][]string{
		{"agent", "prompt", "sidecar-sh-demo-2", "go", "--json"},
		{"agent", "wait", "sidecar-sh-demo-2", "--timeout", "1s", "--json"},
		{"agent", "read", "sidecar-sh-demo-2", "--json"},
		{"agent", "send-keys", "sidecar-sh-demo-2", "enter", "--json"},
	} {
		var out, errOut bytes.Buffer
		handled, code := Run(append([]string{"--disable-feature=agent_control"}, args...), &out, &errOut)
		if !handled || code != 5 || !strings.Contains(errOut.String(), `"code":"feature_disabled"`) {
			t.Fatalf("%v while disabled = handled=%v code=%d stderr=%q", args, handled, code, errOut.String())
		}
	}
}
