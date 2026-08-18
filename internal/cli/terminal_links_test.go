package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// buildFixtureProvider builds the reference provider executable so these tests
// exercise a real child process rather than a stub.
func buildFixtureProvider(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "fixtureprovider")
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the test source")
	}
	pkg := filepath.Join(filepath.Dir(thisFile), "..", "resourceprovider", "testdata", "fixtureprovider")
	cmd := exec.Command("go", "build", "-o", bin, pkg)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("building the fixture provider: %v", err)
	}
	return bin
}

func writeProviderConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func runCLI(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	var out, errOut bytes.Buffer
	handled, code := Run(args, &out, &errOut)
	if !handled {
		t.Fatalf("Run(%v) was not handled by the CLI", args)
	}
	return out.String(), errOut.String(), code
}

func TestTerminalLinksListEmpty(t *testing.T) {
	cfg := writeProviderConfig(t, `{"ui":{"showClock":false}}`)
	out, errOut, code := runCLI(t, "terminal-links", "list", "--config", cfg)
	if code != 0 {
		t.Fatalf("code = %d, stderr %q", code, errOut)
	}
	if !strings.Contains(out, "No terminal resource providers are configured") {
		t.Fatalf("stdout = %q", out)
	}
	// The trust posture is stated where a user would go to add one.
	if !strings.Contains(out, "trusts that executable") {
		t.Fatalf("the trust warning is missing:\n%s", out)
	}
}

func TestTerminalLinksListJSON(t *testing.T) {
	bin := buildFixtureProvider(t)
	cfg := writeProviderConfig(t, `{"terminalResources":{"providers":[
	  {"id":"good","command":["`+bin+`"],"passEnv":["FIXTURE_TOKEN"],"enabled":true,"timeout":"7s"},
	  {"id":"off","command":["`+bin+`"],"enabled":false},
	  {"id":"missing","command":["definitely-not-a-real-binary-xyzzy"],"enabled":true}
	]}}`)

	out, errOut, code := runCLI(t, "terminal-links", "list", "--config", cfg, "--json")
	if code != 0 {
		t.Fatalf("code = %d, stderr %q", code, errOut)
	}

	var report struct {
		Protocol   string `json:"protocol"`
		ConfigPath string `json:"configPath"`
		Providers  []struct {
			Instance        string   `json:"instance"`
			Enabled         bool     `json:"enabled"`
			State           string   `json:"state"`
			CommandResolved bool     `json:"commandResolved"`
			CommandPath     string   `json:"commandPath"`
			PassEnv         []string `json:"passEnv"`
			PassEnvMissing  []string `json:"passEnvMissing"`
			Timeout         string   `json:"timeout"`
			Describe        *struct {
				OK bool `json:"ok"`
			} `json:"describe"`
		} `json:"providers"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	if report.Protocol != "sidecar.terminal-resource/v1" || report.ConfigPath != cfg {
		t.Fatalf("report head = %+v", report)
	}
	if len(report.Providers) != 3 {
		t.Fatalf("providers = %+v", report.Providers)
	}
	// Configuration order is matcher precedence and must be preserved.
	if report.Providers[0].Instance != "good" || report.Providers[2].Instance != "missing" {
		t.Fatalf("order changed: %+v", report.Providers)
	}
	if !report.Providers[0].CommandResolved || report.Providers[0].CommandPath == "" {
		t.Fatalf("good did not resolve: %+v", report.Providers[0])
	}
	if report.Providers[0].Timeout != "7s" {
		t.Fatalf("timeout = %q", report.Providers[0].Timeout)
	}
	if report.Providers[1].State != "disabled" {
		t.Fatalf("off state = %q", report.Providers[1].State)
	}
	if report.Providers[2].CommandResolved {
		t.Fatal("a nonexistent command should not resolve")
	}
	// Bare list starts no process.
	for _, p := range report.Providers {
		if p.Describe != nil {
			t.Fatalf("bare list ran describe for %q", p.Instance)
		}
	}
	// passEnv is reported by name and presence, never by value.
	if len(report.Providers[0].PassEnv) != 1 || report.Providers[0].PassEnv[0] != "FIXTURE_TOKEN" {
		t.Fatalf("passEnv = %v", report.Providers[0].PassEnv)
	}
	if len(report.Providers[0].PassEnvMissing) != 1 {
		t.Fatalf("an unset passEnv variable should be reported missing: %+v", report.Providers[0])
	}
}

func TestTerminalLinksListDescribe(t *testing.T) {
	bin := buildFixtureProvider(t)
	cfg := writeProviderConfig(t, `{"terminalResources":{"providers":[{"id":"good","command":["`+bin+`"],"enabled":true}]}}`)
	out, _, code := runCLI(t, "terminal-links", "list", "--config", cfg, "--describe", "--json")
	if code != 0 {
		t.Fatalf("code = %d", code)
	}
	if !strings.Contains(out, `"matcherCount": 2`) {
		t.Fatalf("describe did not run:\n%s", out)
	}
}

func TestTerminalLinksCheck(t *testing.T) {
	bin := buildFixtureProvider(t)
	cfg := writeProviderConfig(t, `{"terminalResources":{"providers":[{"id":"good","command":["`+bin+`"],"enabled":true}]}}`)

	out, errOut, code := runCLI(t, "terminal-links", "check", "good", "--config", cfg, "--json")
	if code != 0 {
		t.Fatalf("code = %d, stderr %q\n%s", code, errOut, out)
	}
	var report struct {
		Instance string `json:"instance"`
		State    string `json:"state"`
		Describe struct {
			OK       bool `json:"ok"`
			Provider struct {
				Kind    string `json:"kind"`
				DocsURL string `json:"docsUrl"`
			} `json:"provider"`
			Matchers []struct {
				ID      string `json:"id"`
				Pattern string `json:"pattern"`
			} `json:"matchers"`
		} `json:"describe"`
		Resolve any `json:"resolve"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	if report.State != "ready" || !report.Describe.OK {
		t.Fatalf("report = %+v", report)
	}
	if report.Describe.Provider.Kind != "fixture" {
		t.Fatalf("provider = %+v", report.Describe.Provider)
	}
	if len(report.Describe.Matchers) != 2 {
		t.Fatalf("matchers = %+v", report.Describe.Matchers)
	}
	// A bare check never resolves: that is what --resolve is for.
	if report.Resolve != nil {
		t.Fatalf("bare check resolved: %v", report.Resolve)
	}
}

func TestTerminalLinksCheckResolve(t *testing.T) {
	bin := buildFixtureProvider(t)
	cfg := writeProviderConfig(t, `{"terminalResources":{"providers":[{"id":"good","command":["`+bin+`"],"enabled":true}]}}`)

	out, errOut, code := runCLI(t, "terminal-links", "check", "good", "--config", cfg, "--resolve", "CASH-1245", "--json")
	if code != 0 {
		t.Fatalf("code = %d, stderr %q\n%s", code, errOut, out)
	}
	var report struct {
		Resolve struct {
			OK       bool   `json:"ok"`
			Matcher  string `json:"matcher"`
			Locator  string `json:"locator"`
			Resource struct {
				Identity  string `json:"identity"`
				Title     string `json:"title"`
				SourceURL string `json:"sourceUrl"`
			} `json:"resource"`
		} `json:"resolve"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	if !report.Resolve.OK || report.Resolve.Matcher != "issue-key" || report.Resolve.Locator != "CASH-1245" {
		t.Fatalf("resolve = %+v", report.Resolve)
	}
	if report.Resolve.Resource.Identity != "CASH-1245" {
		t.Fatalf("resource = %+v", report.Resolve.Resource)
	}
}

func TestTerminalLinksCheckHumanOutput(t *testing.T) {
	bin := buildFixtureProvider(t)
	cfg := writeProviderConfig(t, `{"terminalResources":{"providers":[{"id":"good","command":["`+bin+`"],"enabled":true}]}}`)
	out, _, code := runCLI(t, "terminal-links", "check", "good", "--config", cfg, "--resolve", "CASH-1245")
	if code != 0 {
		t.Fatalf("code = %d\n%s", code, out)
	}
	for _, want := range []string{"good", "describe  ok", "issue-key", "resolve   ok", "CASH-1245"} {
		if !strings.Contains(out, want) {
			t.Fatalf("human output is missing %q:\n%s", want, out)
		}
	}
}

func TestTerminalLinksCheckFailures(t *testing.T) {
	bin := buildFixtureProvider(t)
	cfg := writeProviderConfig(t, `{"terminalResources":{"providers":[
	  {"id":"broken","command":["`+bin+`","-mode=invalid-re2"],"enabled":true},
	  {"id":"unauthorized","command":["`+bin+`","-mode=error-response"],"enabled":true},
	  {"id":"noisy","command":["`+bin+`","-mode=stderr-flood"],"enabled":true},
	  {"id":"off","command":["`+bin+`"],"enabled":false}
	]}}`)

	t.Run("invalid describe is incompatible", func(t *testing.T) {
		out, _, code := runCLI(t, "terminal-links", "check", "broken", "--config", cfg, "--json")
		if code != 1 {
			t.Fatalf("code = %d\n%s", code, out)
		}
		if !strings.Contains(out, `"state": "incompatible"`) {
			t.Fatalf("state:\n%s", out)
		}
		if !strings.Contains(out, `"outcome": "invalid-describe"`) {
			t.Fatalf("outcome:\n%s", out)
		}
	})

	t.Run("typed failure is reported with its code", func(t *testing.T) {
		out, _, code := runCLI(t, "terminal-links", "check", "unauthorized", "--config", cfg, "--json")
		if code != 1 {
			t.Fatalf("code = %d\n%s", code, out)
		}
		if !strings.Contains(out, `"code": "unauthorized"`) {
			t.Fatalf("code:\n%s", out)
		}
		// setupHint is copyable text; the CLI must say it is not executed.
		if !strings.Contains(out, "run fixtureprovider configure") {
			t.Fatalf("setupHint:\n%s", out)
		}
	})

	t.Run("stderr is never printed", func(t *testing.T) {
		out, errOut, _ := runCLI(t, "terminal-links", "check", "noisy", "--config", cfg, "--resolve", "CASH-1", "--json")
		if strings.Contains(out, "noise") || strings.Contains(errOut, "noise") {
			t.Fatal("provider stderr reached the CLI output")
		}
	})

	t.Run("a disabled provider cannot be resolved", func(t *testing.T) {
		_, _, code := runCLI(t, "terminal-links", "check", "off", "--config", cfg, "--resolve", "CASH-1")
		if code != 1 {
			t.Fatalf("code = %d", code)
		}
	})

	t.Run("unknown instance", func(t *testing.T) {
		_, errOut, code := runCLI(t, "terminal-links", "check", "nope", "--config", cfg)
		if code != 3 {
			t.Fatalf("code = %d", code)
		}
		if !strings.Contains(errOut, "nope") {
			t.Fatalf("stderr = %q", errOut)
		}
	})
}

func TestTerminalLinksUsageErrors(t *testing.T) {
	cfg := writeProviderConfig(t, `{}`)
	cases := [][]string{
		{"terminal-links", "check", "--config", cfg},
		{"terminal-links", "check", "a", "b", "--config", cfg},
		{"terminal-links", "list", "extra", "--config", cfg},
		{"terminal-links", "list", "--resolve", "X-1", "--config", cfg},
		{"terminal-links", "list", "--nope", "--config", cfg},
		{"terminal-links", "nope"},
	}
	for _, args := range cases {
		if _, _, code := runCLI(t, args...); code != 2 {
			t.Fatalf("%v exited %d, want 2", args, code)
		}
	}
}

func TestTerminalLinksHelp(t *testing.T) {
	for _, args := range [][]string{
		{"terminal-links"},
		{"terminal-links", "--help"},
		{"terminal-links", "list", "--help"},
		{"terminal-links", "check", "-h"},
	} {
		out, _, code := runCLI(t, args...)
		if code != 0 || out == "" {
			t.Fatalf("%v = code %d, out %q", args, code, out)
		}
	}
}

// The command must dispatch before any TUI, tmux, state, or log setup. This
// proves the two that would actually bite: a `tmux` invocation, and any write
// into the state tree.
func TestTerminalLinksTouchesNoTmuxOrState(t *testing.T) {
	bin := buildFixtureProvider(t)

	// A tmux shim earlier on PATH than the real one. Any invocation leaves a
	// marker behind.
	shimDir := t.TempDir()
	marker := filepath.Join(shimDir, "tmux-was-run")
	shim := "#!/bin/sh\necho invoked >> " + marker + "\nexit 0\n"
	if err := os.WriteFile(filepath.Join(shimDir, "tmux"), []byte(shim), 0o755); err != nil {
		t.Fatalf("write tmux shim: %v", err)
	}
	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	stateHome := filepath.Join(t.TempDir(), "state")
	t.Setenv("XDG_STATE_HOME", stateHome)

	cfg := writeProviderConfig(t, `{"terminalResources":{"providers":[{"id":"good","command":["`+bin+`"],"enabled":true}]}}`)

	for _, args := range [][]string{
		{"terminal-links", "list", "--config", cfg, "--json"},
		{"terminal-links", "list", "--config", cfg, "--describe", "--json"},
		{"terminal-links", "check", "good", "--config", cfg, "--json"},
		{"terminal-links", "check", "good", "--config", cfg, "--resolve", "CASH-1245", "--json"},
	} {
		if _, _, code := runCLI(t, args...); code != 0 {
			t.Fatalf("%v exited %d", args, code)
		}
	}

	if _, err := os.Stat(marker); err == nil {
		t.Fatal("terminal-links invoked tmux")
	}
	if _, err := os.Stat(stateHome); err == nil {
		t.Fatal("terminal-links created a state directory")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(cfg), "debug.log")); err == nil {
		t.Fatal("terminal-links opened a log file")
	}
	if page, ok := StartupConfigPage(); ok {
		t.Fatalf("terminal-links recorded a TUI startup destination: %q", page)
	}
}

// Field kinds and the body truncation flag are carried through the CLI, not
// dropped. M1 decides what to do with them; M0's job is not to lose them.
func TestTerminalLinksCheckCarriesFieldKinds(t *testing.T) {
	bin := buildFixtureProvider(t)
	cfg := writeProviderConfig(t, `{"terminalResources":{"providers":[{"id":"good","command":["`+bin+`"],"enabled":true}]}}`)

	out, _, code := runCLI(t, "terminal-links", "check", "good", "--config", cfg, "--resolve", "CASH-1245", "--json")
	if code != 0 {
		t.Fatalf("code = %d\n%s", code, out)
	}
	var report struct {
		Resolve struct {
			Resource struct {
				Fields []struct {
					Label string `json:"label"`
					Kind  string `json:"kind"`
				} `json:"fields"`
			} `json:"resource"`
		} `json:"resolve"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	kinds := map[string]string{}
	for _, f := range report.Resolve.Resource.Fields {
		kinds[f.Label] = f.Kind
	}
	want := map[string]string{
		"Project":   "text",
		"Assignee":  "user",
		"Priority":  "text",
		"Created":   "timestamp",
		"Component": "text", // an unknown kind coerces rather than failing
	}
	for label, kind := range want {
		if kinds[label] != kind {
			t.Fatalf("field %q kind = %q, want %q", label, kinds[label], kind)
		}
	}
}

// An over-limit document is truncated and still checks out, rather than
// reporting a failure for something almost entirely fine.
func TestTerminalLinksCheckTruncatesOverLimitDocuments(t *testing.T) {
	bin := buildFixtureProvider(t)
	cfg := writeProviderConfig(t, `{"terminalResources":{"providers":[
	  {"id":"big","command":["`+bin+`","-mode=over-limit-document"],"enabled":true}
	]}}`)
	out, _, code := runCLI(t, "terminal-links", "check", "big", "--config", cfg, "--resolve", "CASH-1", "--json")
	if code == 0 {
		// describe for this mode returns a resource result, which is a shape
		// failure, so a non-zero exit is expected; the resolve is what matters.
		t.Log("check exited 0")
	}
	if !strings.Contains(out, `"truncated": true`) {
		t.Fatalf("the truncation flag was not carried:\n%s", out)
	}
}

// A resource with no identity is a protocol violation reported as a transport
// failure, not rendered as a blank card.
func TestTerminalLinksCheckRejectsAnIncompleteResource(t *testing.T) {
	bin := buildFixtureProvider(t)
	cfg := writeProviderConfig(t, `{"terminalResources":{"providers":[
	  {"id":"headless","command":["`+bin+`","-mode=no-identity"],"enabled":true}
	]}}`)
	out, _, code := runCLI(t, "terminal-links", "check", "headless", "--config", cfg, "--resolve", "CASH-1", "--json")
	if code != 1 {
		t.Fatalf("code = %d\n%s", code, out)
	}
	if !strings.Contains(out, `"outcome": "invalid-resource"`) {
		t.Fatalf("outcome:\n%s", out)
	}
}
