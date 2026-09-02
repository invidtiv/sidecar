package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/agentactivity/manifest"
	"github.com/marcus/sidecar/internal/agentactivity/manifests"
	"github.com/marcus/sidecar/internal/agentlifecycle"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/shellstate"
)

func runLifecycleCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	handled, code := Run(args, &out, &errOut)
	if !handled {
		t.Fatalf("Run(%v) was not handled", args)
	}
	return code, out.String(), errOut.String()
}

// TestLifecycleHooksNoOpOutsideAManagedShell is the fail-open rule, which is
// the single most important property of these three commands.
//
// A provider hook fires on every lifecycle event whether or not the user is
// running inside Sidecar. If reporting outside a managed shell were an error,
// every OpenCode user with the plugin installed would see Sidecar's complaints
// in their own terminal, and a non-zero exit could change what the provider
// does. So the answer is: exit 0, say nothing, record nothing.
func TestLifecycleHooksNoOpOutsideAManagedShell(t *testing.T) {
	// Explicitly unset rather than assumed: the test binary may be running
	// inside a real Sidecar shell.
	t.Setenv(shellstate.ManagedEnv, "")

	for _, args := range [][]string{
		{"agent", "report", "--state", "working", "--source", "s", "--provider", "opencode", "--seq", "1"},
		{"agent", "end", "--outcome", "completed", "--source", "s", "--provider", "opencode", "--seq", "2"},
		{"agent", "release", "--source", "s", "--provider", "opencode", "--seq", "3"},
	} {
		t.Run(args[1], func(t *testing.T) {
			code, out, errOut := runLifecycleCLI(t, args...)
			if code != 0 {
				t.Fatalf("exit %d, want 0 (stderr: %s)", code, errOut)
			}
			if out != "" {
				t.Fatalf("wrote to stdout in human mode: %q", out)
			}
			if errOut != "" {
				t.Fatalf("wrote to stderr: %q", errOut)
			}
		})
	}
}

// TestLifecycleHookJSONNoOpIsStillStructured checks that --json callers get a
// parseable answer for the no-op, because an integration's own diagnostics
// need to distinguish "not applicable" from "failed".
func TestLifecycleHookJSONNoOpIsStillStructured(t *testing.T) {
	t.Setenv(shellstate.ManagedEnv, "")

	code, out, errOut := runLifecycleCLI(t,
		"agent", "report", "--state", "idle", "--source", "s", "--provider", "opencode", "--seq", "1", "--json")
	if code != 0 {
		t.Fatalf("exit %d (stderr: %s)", code, errOut)
	}
	if errOut != "" {
		t.Fatalf("stderr not empty with --json: %q", errOut)
	}
	var res struct {
		SchemaVersion int    `json:"schemaVersion"`
		Accepted      bool   `json:"accepted"`
		Managed       bool   `json:"managed"`
		Note          string `json:"note"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("stdout is not one JSON document: %q (%v)", out, err)
	}
	if res.Managed || res.Accepted {
		t.Fatalf("no-op reported as managed/accepted: %+v", res)
	}
	if res.Note == "" {
		t.Fatal("no-op carried no explanation")
	}
}

// TestLifecycleUsageErrorsComeBeforeAnythingElse pins the ordering the agent
// family already uses: a mistyped command line is a usage error, never
// something that looks like a lifecycle refusal the integration caused.
func TestLifecycleUsageErrorsComeBeforeAnythingElse(t *testing.T) {
	t.Setenv(shellstate.ManagedEnv, "")

	tests := []struct {
		name string
		args []string
		want string
	}{
		{"report without a state", []string{"agent", "report", "--source", "s", "--provider", "p", "--seq", "1"}, "--state is required"},
		{"report with a bad state", []string{"agent", "report", "--state", "thinking", "--source", "s", "--provider", "p", "--seq", "1"}, "must be working, blocked, or idle"},
		{"report without a source", []string{"agent", "report", "--state", "idle", "--provider", "p", "--seq", "1"}, "--source is required"},
		{"report without a provider", []string{"agent", "report", "--state", "idle", "--source", "s", "--seq", "1"}, "--provider is required"},
		{"a non-numeric sequence", []string{"agent", "report", "--state", "idle", "--source", "s", "--provider", "p", "--seq", "x"}, "--seq must be"},
		{"end without an outcome", []string{"agent", "end", "--source", "s", "--provider", "p", "--seq", "1"}, "--outcome is required"},
		{"end with a bad outcome", []string{"agent", "end", "--outcome", "exploded", "--source", "s", "--provider", "p", "--seq", "1"}, "--outcome must be"},
		{"a reason outside the allowlist", []string{"agent", "report", "--state", "idle", "--source", "s", "--provider", "p", "--seq", "1", "--reason", "vibes"}, "not in the frozen allowlist"},
		{"an unknown flag", []string{"agent", "report", "--state", "idle", "--source", "s", "--provider", "p", "--seq", "1", "--nope"}, "unknown flag"},
		{"a state on end", []string{"agent", "end", "--outcome", "completed", "--state", "idle", "--source", "s", "--provider", "p", "--seq", "1"}, "belongs to sidecar agent report"},
		{"an outcome on report", []string{"agent", "report", "--state", "idle", "--outcome", "completed", "--source", "s", "--provider", "p", "--seq", "1"}, "belongs to sidecar agent end"},
		{"a state on release", []string{"agent", "release", "--state", "idle", "--source", "s", "--provider", "p", "--seq", "1"}, "asserts neither a state nor an outcome"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			code, out, errOut := runLifecycleCLI(t, tc.args...)
			if code != 2 {
				t.Fatalf("exit %d, want 2 (usage)", code)
			}
			if out != "" {
				t.Fatalf("usage error wrote to stdout: %q", out)
			}
			if !strings.Contains(errOut, tc.want) {
				t.Fatalf("stderr %q does not mention %q", errOut, tc.want)
			}
		})
	}
}

// TestLifecycleHooksCannotSelectAnotherPane is the security property stated as
// a test: there is no flag through which provider input could choose a
// different host, server, pane, or run. If someone adds one, this fails.
func TestLifecycleHooksCannotSelectAnotherPane(t *testing.T) {
	t.Setenv(shellstate.ManagedEnv, "")

	forbidden := []string{"--pane", "--host", "--server", "--run", "--run-id", "--process-generation", "--pane-id"}
	for _, name := range []string{"report", "end", "release"} {
		cmd := RootCommand().FindSubcommand("agent").FindSubcommand(name)
		if cmd == nil {
			t.Fatalf("agent %s is not registered", name)
		}
		for _, f := range cmd.Flags {
			for _, bad := range forbidden {
				if f.Name == bad {
					t.Fatalf("agent %s accepts %s; identity must never be selectable through input", name, bad)
				}
			}
		}
	}
}

// TestExplainIsReadOnlyAndResolvesOtherShellsByName covers the three things
// that make explain safe to run anywhere: it never writes, it answers about
// another managed shell by resolving it through the shell inventory, and it
// refuses plainly when the name is not one Sidecar owns rather than answering
// about the wrong pane.
func TestExplainIsReadOnlyAndResolvesOtherShellsByName(t *testing.T) {
	t.Setenv(shellstate.ManagedEnv, "")

	code, out, _ := runLifecycleCLI(t, "agent", "explain", "--json")
	if code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
	var res struct {
		Managed bool   `json:"managed"`
		Note    string `json:"note"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("stdout is not one JSON document: %q", out)
	}
	if res.Managed {
		t.Fatal("reported managed outside a managed shell")
	}

	// An unregistered name is refused by naming what Sidecar actually owns,
	// rather than by describing a pane it guessed at.
	code, _, errOut := runLifecycleCLI(t, "agent", "explain", "--shell", "other")
	if code != exitInputRejected {
		t.Fatalf("exit %d, want %d", code, exitInputRejected)
	}
	if !strings.Contains(errOut, "no registered Sidecar shell named") || !strings.Contains(errOut, "shell list") {
		t.Fatalf("stderr = %q", errOut)
	}

	code, _, errOut = runLifecycleCLI(t, "agent", "explain", "--current", "--shell", "other")
	if code != 2 {
		t.Fatalf("exit %d, want 2 for two conflicting targets (stderr: %s)", code, errOut)
	}
}

// TestLifecycleCommandsAreRegisteredAlphabetically guards the slice order the
// generated CLI doc and RenderHelp both render in, so a new subcommand cannot
// quietly reorder the reference.
func TestLifecycleCommandsAreRegisteredAlphabetically(t *testing.T) {
	agent := RootCommand().FindSubcommand("agent")
	var names []string
	for _, c := range agent.Sub {
		names = append(names, c.Name)
	}
	for i := 1; i < len(names); i++ {
		if names[i-1] > names[i] {
			t.Fatalf("agent subcommands are not alphabetical: %v", names)
		}
	}
	for _, want := range []string{"report", "end", "release", "explain"} {
		if agent.FindSubcommand(want) == nil {
			t.Fatalf("agent %s is not registered", want)
		}
	}
}

// TestASequenceIsOptionalSoAPerEventHookCanReport pins the flag contract that
// makes a multi-process integration possible at all. Requiring --seq assumed
// the reporter is one long-lived process holding a counter, which is true of
// OpenCode's plugin and false of Codex's and Claude Code's hooks: those run a
// fresh process per event with nothing to count with. Omitting it now asks the
// store to assign the next sequence under the lock it already takes.
func TestASequenceIsOptionalSoAPerEventHookCanReport(t *testing.T) {
	t.Setenv(shellstate.ManagedEnv, "")

	for _, args := range [][]string{
		{"agent", "report", "--state", "working", "--source", "s", "--provider", "codex"},
		{"agent", "end", "--outcome", "cancelled", "--source", "s", "--provider", "codex"},
		{"agent", "release", "--source", "s", "--provider", "codex"},
	} {
		t.Run(args[1], func(t *testing.T) {
			code, _, errOut := runLifecycleCLI(t, args...)
			if code == 2 {
				t.Fatalf("omitting --seq was still a usage error: %s", errOut)
			}
			if code != 0 {
				t.Fatalf("exit %d (stderr: %s)", code, errOut)
			}
		})
	}
}

// TestLiveExplainTextPrintsTheManifestWarning is the half of "a refused or
// degraded override must be visible" that runs on a live pane. The --file path
// already prints the warning and is covered by
// TestExplainFileReportsARefusedOverride; this is the path a user actually
// reaches from a pane that is badged wrong, and until it prints the warning the
// news arrives only in --json.
func TestLiveExplainTextPrintsTheManifestWarning(t *testing.T) {
	var out bytes.Buffer
	writeExplanationText(Env{Stdout: &out}, agentlifecycle.Explanation{
		State: "idle",
		ScreenExplain: &manifest.Explain{
			Agent:          "cursor",
			ManifestSource: "bundled cursor 2026.08.29.1 + sidecar overlay",
			Warning:        "ignored override /tmp/cursor.toml because it is invalid: bad",
		},
	})
	text := out.String()
	if !strings.Contains(text, "warning") || !strings.Contains(text, "/tmp/cursor.toml") {
		t.Fatalf("live explain text does not carry the warning:\n%s", text)
	}
}

// `sidecar agent manifests`. The property under test is parity between the two
// output forms: the JSON is the interface an agent reads, and a column that
// exists only in the table is a column an agent cannot see.

func TestAgentManifestsListsEveryVendoredAgentInBothForms(t *testing.T) {
	config.SetTestConfigPath(filepath.Join(t.TempDir(), "config.json"))
	t.Cleanup(config.ResetTestConfigPath)
	config.SetTestStateDir(t.TempDir())
	t.Cleanup(config.ResetTestStateDir)

	agents, err := manifests.Agents()
	if err != nil {
		t.Fatal(err)
	}

	code, out, errOut := runLifecycleCLI(t, "agent", "manifests", "--json")
	if code != 0 {
		t.Fatalf("exit %d (stderr: %s)", code, errOut)
	}
	var res manifestsResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("output is not the manifests record: %v\n%s", err, out)
	}
	if len(res.Agents) != len(agents) {
		t.Fatalf("reported %d agents, want %d", len(res.Agents), len(agents))
	}
	if res.RemoteManifests != config.RemoteManifestsOff {
		t.Fatalf("remoteManifests = %q, want %q by default", res.RemoteManifests, config.RemoteManifestsOff)
	}
	if res.CatalogURL != "" {
		t.Fatalf("catalogUrl = %q with fetching off", res.CatalogURL)
	}
	for _, row := range res.Agents {
		if row.Error != "" {
			t.Fatalf("%s: %s", row.Agent, row.Error)
		}
		if row.ActiveSource != string(manifests.KindBundled) {
			t.Fatalf("%s active source = %q, want %q", row.Agent, row.ActiveSource, manifests.KindBundled)
		}
		if row.ActiveVersion == "" || row.VendoredVersion != row.ActiveVersion {
			t.Fatalf("%s versions = active %q vendored %q", row.Agent, row.ActiveVersion, row.VendoredVersion)
		}
		if row.CachedRemoteVersion != "" {
			t.Fatalf("%s reports a cached remote version with fetching off", row.Agent)
		}
		if row.OverlayApplied != manifests.HasOverlay(row.Agent) {
			t.Fatalf("%s overlayApplied = %v, want %v", row.Agent, row.OverlayApplied, manifests.HasOverlay(row.Agent))
		}
	}

	// Every fact the table shows must also be in the JSON, which is checked by
	// looking for each row's own values in the text form.
	code, text, errOut := runLifecycleCLI(t, "agent", "manifests")
	if code != 0 {
		t.Fatalf("exit %d (stderr: %s)", code, errOut)
	}
	if !strings.Contains(text, "remote manifests  off") {
		t.Fatalf("text form does not report the setting:\n%s", text)
	}
	for _, row := range res.Agents {
		if !strings.Contains(text, row.Agent) || !strings.Contains(text, row.ActiveVersion) {
			t.Fatalf("text form is missing %s %s:\n%s", row.Agent, row.ActiveVersion, text)
		}
	}
}

// TestAgentManifestsReportsARefusedSetting: the config loader warns to the log
// and keeps the default, and the log is not somewhere anyone looks. This is
// where a user finds out their setting did nothing.
func TestAgentManifestsReportsARefusedSetting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"detection":{"remoteManifests":"yes please"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	config.SetTestConfigPath(path)
	t.Cleanup(config.ResetTestConfigPath)
	config.SetTestStateDir(t.TempDir())
	t.Cleanup(config.ResetTestStateDir)

	code, out, errOut := runLifecycleCLI(t, "agent", "manifests", "--json")
	if code != 0 {
		t.Fatalf("exit %d (stderr: %s)", code, errOut)
	}
	var res manifestsResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatal(err)
	}
	// The loader replaced the value, so the effective setting is off and there
	// is no catalog. That is the safe direction and the table says so.
	if res.RemoteManifests != config.RemoteManifestsOff || res.CatalogURL != "" {
		t.Fatalf("a refused value did not resolve to off: %+v", res)
	}
}

func TestAgentManifestsRejectsUnknownFlags(t *testing.T) {
	code, _, errOut := runLifecycleCLI(t, "agent", "manifests", "--fetch")
	if code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	if !strings.Contains(errOut, "--fetch") {
		t.Fatalf("stderr does not name the flag: %s", errOut)
	}
}
