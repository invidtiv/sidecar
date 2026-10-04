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
