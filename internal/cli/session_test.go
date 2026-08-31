package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/agentsession"
	"github.com/marcus/sidecar/internal/sessionrestore"
	"github.com/marcus/sidecar/internal/shellstate"
)

// seedRestoreManifest writes a project whose shells were confirmed live in a
// tmux server that is not the one running now, which is the state a cold
// restore starts from.
func seedRestoreManifest(t *testing.T, stateDir string, defs ...shellstate.Definition) string {
	t.Helper()
	workDir := t.TempDir()
	writeProjectMeta(t, stateDir, "harness", workDir)
	for i := range defs {
		if defs[i].WorkDir == "" {
			defs[i].WorkDir = workDir
		}
	}
	path := filepath.Join(stateDir, "projects", "harness", "shells.json")
	body, err := json.Marshal(map[string]any{"version": shellstate.CurrentVersion, "shells": defs})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func restorableShell(name string) shellstate.Definition {
	return shellstate.Definition{
		TmuxName:    name,
		DisplayName: name,
		CreatedAt:   time.Now().Add(-time.Hour),
		Restore: &shellstate.RestoreState{
			Eligible:       true,
			LastSeenServer: "pid=999999", // a server that is certainly not running
		},
	}
}

func withBoundAgent(def shellstate.Definition, kind, value string) shellstate.Definition {
	def.AgentType = kind
	def.Agent = &shellstate.AgentBinding{
		Kind: kind,
		Session: &agentsession.Ref{
			Kind: agentsession.RefID, Value: value,
			Source: agentsession.OfficialSourceFor(kind), Reported: true,
			ReportedAt: time.Now(),
		},
	}
	return def
}

func TestSessionStatusIsReadOnlyAndParses(t *testing.T) {
	_, stateDir := setupIsolatedCLI(t)
	manifest := seedRestoreManifest(t, stateDir, restorableShell("sidecar-sh-harness-1"))
	before, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := runCLI(t, "session", "status", "--json")
	if code != 0 {
		t.Fatalf("code %d stderr %q", code, stderr)
	}
	var doc struct {
		ServerChanged bool `json:"serverChanged"`
		Steps         []struct {
			Session string `json:"session"`
			Action  string `json:"action"`
			Reason  string `json:"reason"`
		} `json:"steps"`
	}
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("status --json is not valid JSON: %v\n%s", err, stdout)
	}
	if len(doc.Steps) != 1 || doc.Steps[0].Session != "sidecar-sh-harness-1" {
		t.Fatalf("unexpected steps: %+v", doc.Steps)
	}
	if doc.Steps[0].Action != string(sessionrestore.ActionRecreateShell) {
		t.Errorf("action %q, want recreate-shell", doc.Steps[0].Action)
	}

	after, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("session status wrote to the shell manifest; it must be read-only")
	}
}

// TestSessionStatusHumanAndJSONAgree is the surface-parity check: the sentence a
// human reads and the document a script parses must describe the same plan.
func TestSessionStatusHumanAndJSONAgree(t *testing.T) {
	_, stateDir := setupIsolatedCLI(t)
	seedRestoreManifest(t, stateDir,
		restorableShell("sidecar-sh-harness-1"),
		restorableShell("sidecar-sh-harness-2"),
	)

	human, _, code := runCLI(t, "session", "status")
	if code != 0 {
		t.Fatalf("human status code %d", code)
	}
	jsonOut, _, code := runCLI(t, "session", "status", "--json")
	if code != 0 {
		t.Fatalf("json status code %d", code)
	}
	var doc struct {
		Steps []struct {
			Name   string `json:"name"`
			Action string `json:"action"`
		} `json:"steps"`
	}
	if err := json.Unmarshal([]byte(jsonOut), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Steps) != 2 {
		t.Fatalf("want 2 steps, got %d", len(doc.Steps))
	}
	for _, step := range doc.Steps {
		if !strings.Contains(human, step.Name) {
			t.Errorf("human output omits %q that JSON reports", step.Name)
		}
		if !strings.Contains(human, step.Action) {
			t.Errorf("human output omits action %q", step.Action)
		}
	}
}

// TestSessionRestoreDryRunCreatesNothing is the reviewability property: a plan
// can be read before anything is done to the machine.
func TestSessionRestoreDryRunCreatesNothing(t *testing.T) {
	_, stateDir := setupIsolatedCLI(t)
	manifest := seedRestoreManifest(t, stateDir, restorableShell("sidecar-sh-harness-1"))
	before, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := runCLI(t, "session", "restore", "--dry-run")
	if code != 0 {
		t.Fatalf("code %d stderr %q", code, stderr)
	}
	if !strings.Contains(stdout, "dry run: nothing was created") {
		t.Errorf("a dry run must say so plainly: %q", stdout)
	}
	after, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("a dry run wrote to the shell manifest")
	}
}

// TestSessionRestoreRefusesAnUnconfirmedResume is the ask-policy contract at the
// CLI boundary. Silently doing less than asked is how a user comes to believe a
// conversation was resumed when it was not, so this is a refusal with an exit
// code rather than a quiet downgrade to shells-only.
func TestSessionRestoreRefusesAnUnconfirmedResume(t *testing.T) {
	_, stateDir := setupIsolatedCLI(t)
	seedRestoreManifest(t, stateDir,
		withBoundAgent(restorableShell("sidecar-sh-harness-1"), "codex", "sess-abc"))

	_, stderr, code := runCLI(t, "session", "restore", "--agents")
	if code != exitInputRejected {
		t.Fatalf("code %d, want %d; stderr %q", code, exitInputRejected, stderr)
	}
	if !strings.Contains(stderr, "--yes") {
		t.Errorf("the refusal must name the flag that resolves it: %q", stderr)
	}
}

// TestSessionRestoreJSONRefusalIsStructured keeps the machine surface honest:
// the same refusal a human reads is a parseable code on stderr, with stdout
// left clean.
func TestSessionRestoreJSONRefusalIsStructured(t *testing.T) {
	_, stateDir := setupIsolatedCLI(t)
	seedRestoreManifest(t, stateDir,
		withBoundAgent(restorableShell("sidecar-sh-harness-1"), "codex", "sess-abc"))

	stdout, stderr, code := runCLI(t, "session", "restore", "--agents", "--json")
	if code != exitInputRejected {
		t.Fatalf("code %d, want %d", code, exitInputRejected)
	}
	if strings.TrimSpace(stdout) != "" {
		t.Errorf("stdout must stay clean for a parser: %q", stdout)
	}
	var doc struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(stderr), &doc); err != nil {
		t.Fatalf("the refusal is not JSON: %v\n%s", err, stderr)
	}
	if doc.Error.Code != "confirmation_required" {
		t.Errorf("error code %q", doc.Error.Code)
	}
}

// TestSessionStatusNeverPrintsTheSessionValue is M3's redaction rule applied to
// the surface most likely to be pasted into an issue.
func TestSessionStatusNeverPrintsTheSessionValue(t *testing.T) {
	const secret = "01a05614-0ca7-dead-beef-000000000000"
	_, stateDir := setupIsolatedCLI(t)
	seedRestoreManifest(t, stateDir,
		withBoundAgent(restorableShell("sidecar-sh-harness-1"), "codex", secret))

	for _, args := range [][]string{
		{"session", "status"},
		{"session", "status", "--json"},
		{"session", "restore", "--dry-run", "--json"},
	} {
		stdout, stderr, _ := runCLI(t, args...)
		if strings.Contains(stdout+stderr, secret) {
			t.Fatalf("%v leaked the conversation identifier", args)
		}
	}
}

func TestSessionPolicyRoundTrips(t *testing.T) {
	_, stateDir := setupIsolatedCLI(t)
	seedRestoreManifest(t, stateDir, restorableShell("sidecar-sh-harness-1"))

	if _, stderr, code := runCLI(t, "session", "policy", "sidecar-sh-harness-1", "--resume"); code != 0 {
		t.Fatalf("set: code %d stderr %q", code, stderr)
	}
	stdout, stderr, code := runCLI(t, "session", "policy", "sidecar-sh-harness-1", "--json")
	if code != 0 {
		t.Fatalf("read: code %d stderr %q", code, stderr)
	}
	var doc struct {
		Policy string `json:"policy"`
	}
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Policy != string(agentsession.PolicyResume) {
		t.Fatalf("policy %q, want resume", doc.Policy)
	}
}

// TestSessionPolicyRefusesTwoPolicies keeps the flag set unambiguous rather than
// silently letting the last one win.
func TestSessionPolicyRefusesTwoPolicies(t *testing.T) {
	_, stateDir := setupIsolatedCLI(t)
	seedRestoreManifest(t, stateDir, restorableShell("sidecar-sh-harness-1"))

	_, stderr, code := runCLI(t, "session", "policy", "sidecar-sh-harness-1", "--resume", "--never")
	if code != 2 {
		t.Fatalf("code %d, want 2 (usage); stderr %q", code, stderr)
	}
}

// TestSessionPolicyAffectsThePlan proves the per-shell policy is not merely
// stored: it changes what a restore would do.
func TestSessionPolicyAffectsThePlan(t *testing.T) {
	_, stateDir := setupIsolatedCLI(t)
	seedRestoreManifest(t, stateDir, restorableShell("sidecar-sh-harness-1"))

	if _, stderr, code := runCLI(t, "session", "policy", "sidecar-sh-harness-1", "--never"); code != 0 {
		t.Fatalf("set never: code %d stderr %q", code, stderr)
	}
	stdout, _, code := runCLI(t, "session", "status", "--json")
	if code != 0 {
		t.Fatalf("status code %d", code)
	}
	var doc struct {
		Steps []struct {
			Action string `json:"action"`
			Reason string `json:"reason"`
		} `json:"steps"`
	}
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Steps) != 1 || doc.Steps[0].Action != string(sessionrestore.ActionSkip) {
		t.Fatalf("a never policy must skip the shell: %+v", doc.Steps)
	}
	if doc.Steps[0].Reason != string(sessionrestore.ReasonPolicyNever) {
		t.Errorf("reason %q, want policy_never", doc.Steps[0].Reason)
	}
}

func TestSessionUnknownOptionsAreUsageErrors(t *testing.T) {
	setupIsolatedCLI(t)
	for _, args := range [][]string{
		{"session", "status", "--nope"},
		{"session", "restore", "--nope"},
		{"session", "policy", "--nope"},
		{"session", "nonsense"},
	} {
		if _, _, code := runCLI(t, args...); code != 2 {
			t.Errorf("%v exited %d, want 2", args, code)
		}
	}
}

func TestSessionHelpIsAvailableEverywhere(t *testing.T) {
	setupIsolatedCLI(t)
	for _, args := range [][]string{
		{"session"},
		{"session", "--help"},
		{"session", "status", "--help"},
		{"session", "restore", "--help"},
		{"session", "policy", "--help"},
	} {
		stdout, stderr, code := runCLI(t, args...)
		if code != 0 {
			t.Errorf("%v exited %d (%s)", args, code, stderr)
		}
		if strings.TrimSpace(stdout) == "" {
			t.Errorf("%v printed no help", args)
		}
	}
}
