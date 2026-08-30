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

// hostRegistryEnv runs the registry verbs against a config file of their own.
// The package's TestMain already moves the state tree; this moves the file the
// verbs write, so no test can reach the developer's own registry.
func hostRegistryEnv(t *testing.T) func(args ...string) (int, string, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"futureRoot":{"keep":true}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	config.SetTestConfigPath(path)
	t.Cleanup(func() { config.SetConfigPath(defaultTestConfigPath) })

	return func(args ...string) (int, string, string) {
		t.Helper()
		var out, errOut bytes.Buffer
		handled, code := Run(append([]string{"host"}, args...), &out, &errOut)
		if !handled {
			t.Fatalf("Run(host %v) was not handled", args)
		}
		return code, out.String(), errOut.String()
	}
}

// The four verbs are one loop: register, list, change, unregister — and each
// answers with the exit code its own failure deserves.
func TestHostRegistryVerbs(t *testing.T) {
	run := hostRegistryEnv(t)

	if code, _, errOut := run("add", "marcusbook", "--id", "book"); code != 0 {
		t.Fatalf("add = %d, stderr %q", code, errOut)
	}
	if code, _, _ := run("add", "elsewhere", "--id", "book"); code != hostExitRejected {
		t.Fatalf("duplicate add = %d, want %d", code, hostExitRejected)
	}
	if code, _, _ := run("add", "elsewhere", "--env", "NOPE"); code != hostExitRejected {
		t.Fatalf("malformed --env = %d, want %d", code, hostExitRejected)
	}
	if code, _, _ := run("set", "nobody", "--disabled"); code != hostExitNotFound {
		t.Fatalf("set on an unknown host = %d, want %d", code, hostExitNotFound)
	}
	if code, _, _ := run("remove", "nobody"); code != hostExitNotFound {
		t.Fatalf("remove of an unknown host = %d, want %d", code, hostExitNotFound)
	}
	if code, _, _ := run("set", "book"); code != 2 {
		t.Fatalf("set with nothing to change = %d, want a usage error", code)
	}

	if code, _, errOut := run("set", "book", "--disabled", "--target", "marcusbook.local"); code != 0 {
		t.Fatalf("set = %d, stderr %q", code, errOut)
	}

	code, out, _ := run("list", "--json")
	if code != 0 {
		t.Fatalf("list = %d", code)
	}
	var listed hostListResult
	if err := json.Unmarshal([]byte(out), &listed); err != nil {
		t.Fatalf("list --json is not JSON: %v\n%s", err, out)
	}
	if len(listed.Hosts) != 1 {
		t.Fatalf("listed %d hosts, want 1: %+v", len(listed.Hosts), listed.Hosts)
	}
	host := listed.Hosts[0]
	if host.ID != "book" || host.Target != "marcusbook.local" || !host.Disabled {
		t.Fatalf("listed host = %+v, want the edited entry", host)
	}
	// A registered host connects to nothing while the flag is off, and the
	// listing has to say so rather than reading as a working setup.
	if listed.FeatureEnabled != features.IsEnabled(features.SidecarRemoteHosts.Name) {
		t.Fatalf("featureEnabled = %v, want the running answer", listed.FeatureEnabled)
	}

	if code, out, _ := run("remove", "book", "--json"); code != 0 || !strings.Contains(out, hostStatusRemoved) {
		t.Fatalf("remove = %d, out %q", code, out)
	}
	if _, out, _ := run("list"); !strings.Contains(out, "No remote hosts registered") {
		t.Fatalf("list after remove = %q", out)
	}
}

// --env replaces the whole list rather than appending, so running the same
// command twice leaves the same host.
func TestHostSetReplacesTheEnvironment(t *testing.T) {
	run := hostRegistryEnv(t)
	if code, _, errOut := run("add", "proof", "--env", "TMUX_TMPDIR=/tmp/a", "--env", "XDG_STATE_HOME=/tmp/b"); code != 0 {
		t.Fatalf("add = %d, stderr %q", code, errOut)
	}
	if code, _, errOut := run("set", "proof", "--env", "TMUX_TMPDIR=/tmp/c"); code != 0 {
		t.Fatalf("set = %d, stderr %q", code, errOut)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Hosts.List[0].Env; len(got) != 1 || got[0] != "TMUX_TMPDIR=/tmp/c" {
		t.Fatalf("env = %v, want the replacement", got)
	}
	// An empty --env is the way to clear the list, so a blank value is a real
	// value here rather than a missing argument.
	if code, _, errOut := run("set", "proof", "--env", ""); code != 0 {
		t.Fatalf("clearing = %d, stderr %q", code, errOut)
	}
	cfg, _ = config.Load()
	if len(cfg.Hosts.List[0].Env) != 0 {
		t.Fatalf("env = %v, want it cleared", cfg.Hosts.List[0].Env)
	}
}

// The CLI and the Configuration page must accept and refuse exactly the same
// entries, which they do by calling one state-free validator. This asserts the
// CLI actually goes through it rather than having grown a second opinion.
func TestHostAddUsesTheSharedValidator(t *testing.T) {
	run := hostRegistryEnv(t)
	if code, _, _ := run("add", "marcusbook", "--id", "my host"); code != hostExitRejected {
		t.Fatalf("a name with a space was accepted by the CLI")
	}
	if message := config.ValidateHost(nil, config.HostConfig{ID: "my host", Target: "marcusbook"}, -1); message == "" {
		t.Fatal("the shared validator accepts a name the CLI refused")
	}
}

// The three writing verbs are gated by the isolation check, which only arms for
// commands marked Mutates. A read is deliberately not gated.
func TestHostRegistryMutationMarkers(t *testing.T) {
	host := RootCommand().FindSubcommand("host")
	for _, name := range []string{"add", "remove", "set"} {
		if sub := host.FindSubcommand(name); sub == nil || !sub.Mutates {
			t.Fatalf("host %s is not marked as mutating state", name)
		}
	}
	if sub := host.FindSubcommand("list"); sub == nil || sub.Mutates {
		t.Fatal("host list is marked as mutating state")
	}
}
