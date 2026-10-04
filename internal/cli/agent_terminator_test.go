package cli

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func TestAgentArgsTerminatorKeepsFlagLikeTextLiteral(t *testing.T) {
	for _, args := range [][]string{
		{"--project", "proof", "--json", "--", "managed", "--json"},
		{"--project", "proof", "--json", "--", "managed", "--help"},
		{"--project", "proof", "--json", "--", "managed", "-"},
	} {
		var out, stderr bytes.Buffer
		flags, exit := parseAgentArgs(Env{Stdout: &out, Stderr: &stderr}, args, "help", optWait|optTimeout)
		if exit != -1 || !flags.json || flags.project != "proof" || !reflect.DeepEqual(flags.positional, args[len(args)-2:]) {
			t.Fatalf("args=%v flags=%+v exit=%d stderr=%s", args, flags, exit, stderr.String())
		}
		if out.Len() != 0 {
			t.Fatalf("literal help requested help: %s", out.String())
		}
	}
}
func TestAgentPromptTerminatorReachesReceiptWithoutInterpretingText(t *testing.T) {
	_, stateDir := setupIsolatedCLI(t)
	var out, stderr bytes.Buffer
	exit := runAgentPrompt(Env{StateDir: stateDir, Stdout: &out, Stderr: &stderr, FeatureOverrides: map[string]bool{"agent_control": false}}, []string{"--project", "proof", "--json", "--", "managed", "--help"})
	if exit != 5 || !strings.Contains(stderr.String(), `"not_submitted"`) || !strings.Contains(stderr.String(), `"session":"managed"`) || strings.Contains(stderr.String(), "unknown option") {
		t.Fatalf("exit=%d stdout=%s stderr=%s", exit, out.String(), stderr.String())
	}
}

// Every verb using the common parser keeps its option contract before --,
// then accepts the remaining arguments literally. start has its own parser,
// where -- continues to delimit provider argv; lifecycle/integration verbs do
// not call parseAgentArgs.
func TestAgentTerminatorAcrossCommonVerbOptions(t *testing.T) {
	cases := []struct {
		name    string
		allowed agentOpt
		args    []string
	}{
		{"list", optIncludeSession, []string{"--include-session-ref"}},
		{"get", optIncludeSession, []string{"--include-session-ref"}},
		{"prompt", optWait | optUntil | optTimeout, []string{"--wait", "--until", "done", "--timeout", "1s"}},
		{"wait", optUntil | optTimeout, []string{"--until", "idle", "--timeout", "1s"}},
		{"read", optSource | optLines | optANSI, []string{"--source", "recent-unwrapped", "--lines", "3", "--ansi"}},
		{"send-keys", 0, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out, stderr bytes.Buffer
			args := append([]string{"--project", "proof", "--json"}, tc.args...)
			args = append(args, "--", "managed", "--host=elsewhere", "--help")
			f, code := parseAgentArgs(Env{Stdout: &out, Stderr: &stderr}, args, "help", tc.allowed)
			if code != -1 || f.host != "" || f.project != "proof" || !f.json || out.Len() != 0 || !reflect.DeepEqual(f.positional, []string{"managed", "--host=elsewhere", "--help"}) {
				t.Fatalf("flags=%+v exit=%d stderr=%s", f, code, stderr.String())
			}
		})
	}
}
