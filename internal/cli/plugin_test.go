package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/features"
)

// pluginTestEnv points the command at a config file of the test's own, so no
// run reads or writes the developer's real configuration.
func pluginTestEnv(t *testing.T, contents string) (Env, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(configPath, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	config.SetTestConfigPath(configPath)
	config.SetTestStateDir(filepath.Join(dir, "state"))
	t.Cleanup(config.ResetTestConfigPath)
	t.Cleanup(config.ResetTestStateDir)
	// runPluginList initializes the feature manager from the config it reads;
	// put it back afterwards so no later test inherits this one's flags.
	t.Cleanup(func() { features.Init(config.Default()) })

	var out, errOut bytes.Buffer
	return Env{Stdout: &out, Stderr: &errOut, StateDir: filepath.Join(dir, "state")}, &out, &errOut
}

func TestPluginListReportsEveryDescriptor(t *testing.T) {
	env, out, errOut := pluginTestEnv(t, `{}`)
	if code := runPluginList(env, []string{"--json"}); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	var result pluginListJSON
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode: %v (%s)", err, out.String())
	}

	want := []string{
		"td-monitor", "git-status", "file-browser", "conversations",
		"workspace-manager", "notes", "tasks",
	}
	if len(result.Plugins) != len(want) {
		t.Fatalf("listed %d plugins, want %d: %+v", len(result.Plugins), len(want), result.Plugins)
	}
	for i, id := range want {
		if result.Plugins[i].ID != id {
			t.Fatalf("plugin %d = %q, want %q", i, result.Plugins[i].ID, id)
		}
		if result.Plugins[i].Class != "embedded" {
			t.Errorf("%s class = %q", id, result.Plugins[i].Class)
		}
		if len(result.Plugins[i].Placements) == 0 {
			t.Errorf("%s reported no placements", id)
		}
	}
	// Scope is the lifecycle answer, and Tasks is the one global plugin today.
	for _, p := range result.Plugins {
		wantScope := "project"
		if p.ID == "tasks" {
			wantScope = "global"
		}
		if p.Scope != wantScope {
			t.Errorf("%s scope = %q, want %q", p.ID, p.Scope, wantScope)
		}
	}
}

// The reported switch is plugins.<id>.enabled, with the deprecated flags
// answering only while the key is absent.
func TestPluginListReadsTheUnifiedSwitch(t *testing.T) {
	enabled := func(t *testing.T, contents, id string) bool {
		t.Helper()
		env, out, errOut := pluginTestEnv(t, contents)
		if code := runPluginList(env, []string{"--json"}); code != 0 {
			t.Fatalf("exit %d: %s", code, errOut.String())
		}
		var result pluginListJSON
		if err := json.Unmarshal(out.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		for _, p := range result.Plugins {
			if p.ID == id {
				return p.Enabled
			}
		}
		t.Fatalf("%q is not listed", id)
		return false
	}

	if enabled(t, `{}`, "tasks") {
		t.Error("tasks is on with neither the key nor the flag set")
	}
	if !enabled(t, `{"features":{"flags":{"tasks_plugin":true}}}`, "tasks") {
		t.Error("the deprecated tasks_plugin flag was ignored with no config key present")
	}
	if enabled(t, `{"features":{"flags":{"tasks_plugin":true}},"plugins":{"tasks":{"enabled":false}}}`, "tasks") {
		t.Error("plugins.tasks.enabled did not outrank the deprecated flag")
	}
	if !enabled(t, `{"plugins":{"tasks":{"enabled":true}}}`, "tasks") {
		t.Error("plugins.tasks.enabled was not read")
	}
	if enabled(t, `{"plugins":{"git-status":{"enabled":false}}}`, "git-status") {
		t.Error("plugins.git-status.enabled was not read")
	}
}

func TestPluginListHumanOutputAndUsageErrors(t *testing.T) {
	env, out, errOut := pluginTestEnv(t, `{}`)
	if code := runPluginList(env, nil); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	text := out.String()
	for _, want := range []string{"td-monitor", "embedded", "project", "global", "tasks", "tab"} {
		if !strings.Contains(text, want) {
			t.Fatalf("human output is missing %q:\n%s", want, text)
		}
	}

	env, _, errOut = pluginTestEnv(t, `{}`)
	if code := runPluginList(env, []string{"--nope"}); code != 2 {
		t.Fatalf("unknown option exit = %d", code)
	}
	if !strings.Contains(errOut.String(), "unknown option") {
		t.Fatalf("unknown option was not explained: %s", errOut.String())
	}

	env, _, errOut = pluginTestEnv(t, `{}`)
	if code := runPluginList(env, []string{"recall"}); code != 2 {
		t.Fatalf("positional argument exit = %d", code)
	}
	if !strings.Contains(errOut.String(), "takes no positional arguments") {
		t.Fatalf("positional argument was not explained: %s", errOut.String())
	}

	env, _, errOut = pluginTestEnv(t, `{}`)
	if code := runPluginRoot(env, []string{"describe"}); code != 2 {
		t.Fatalf("unknown subcommand exit = %d", code)
	}
	if !strings.Contains(errOut.String(), "unknown plugin command") {
		t.Fatalf("unknown subcommand was not explained: %s", errOut.String())
	}
}
