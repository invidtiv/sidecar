package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/apiservice"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/uiapi"
)

func TestAPIServiceBrokenConfigStillAllowsStatusAndUninstall(t *testing.T) {
	apiStateTree(t, t.TempDir())
	fake := fakeAPIService(t)
	seed := []byte(`{"api":`)
	if err := os.WriteFile(config.ConfigPath(), seed, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"status"}, {"status", "--json"}, {"uninstall"}, {"uninstall", "--json"}} {
		code, out, stderr := runAPICLI(t, append([]string{"api", "service"}, args...)...)
		if code != 0 {
			t.Fatalf("%v: %d %s %s", args, code, out, stderr)
		}
		if args[0] == "status" {
			if len(args) == 1 && (!strings.Contains(out, "UI directory: unknown") || !strings.Contains(out, "load API config")) {
				t.Fatalf("missing config detail: %s", out)
			}
			if len(args) == 2 {
				var status apiservice.Status
				if err := json.Unmarshal([]byte(out), &status); err != nil {
					t.Fatal(err)
				}
				if status.UIDir != nil || status.UIConfigError == "" || !strings.Contains(out, `"ui_dir":null`) {
					t.Fatalf("missing unknown/detail: %s", out)
				}
			}
		} else if strings.Contains(out, "load API config") {
			t.Fatalf("uninstall loaded config: %s", out)
		}
	}
	if strings.Join(fake.calls, ",") != "status,status,uninstall,status,uninstall,status" {
		t.Fatalf("manager calls: %v", fake.calls)
	}
	if code, _, stderr := runAPICLI(t, "api", "service", "install"); code != 1 || !strings.Contains(stderr, "load API config") {
		t.Fatalf("install: %d %s", code, stderr)
	}
	after, err := os.ReadFile(config.ConfigPath())
	if err != nil || string(after) != string(seed) {
		t.Fatalf("changed broken config: %s %v", after, err)
	}
}

type fakeAPIManager struct {
	status        apiservice.Status
	calls         []string
	failure       error
	beforeInstall func()
}

func (f *fakeAPIManager) Install(context.Context) error {
	f.calls = append(f.calls, "install")
	if f.beforeInstall != nil {
		f.beforeInstall()
	}
	if f.failure != nil {
		return f.failure
	}
	f.status.Installed = true
	f.status.Loaded = true
	return nil
}

func TestAPIServiceInstallUIAndStatus(t *testing.T) {
	apiStateTree(t, t.TempDir())
	fake := fakeAPIService(t)
	ui := t.TempDir()
	if err := os.WriteFile(filepath.Join(ui, "index.html"), []byte("UI"), 0o644); err != nil {
		t.Fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	relative, err := filepath.Rel(cwd, ui)
	if err != nil {
		t.Fatal(err)
	}
	fake.beforeInstall = func() {
		cfg, err := config.Load()
		if err != nil || cfg.API.UIDir != ui {
			t.Fatalf("config must be saved before install: %+v %v", cfg, err)
		}
	}
	if code, out, stderr := runAPICLI(t, "api", "service", "install", "--ui", relative, "--json"); code != 0 || !strings.Contains(out, `"ui_dir":`+quoteJSON(t, ui)) {
		t.Fatalf("install: %d %s %s", code, out, stderr)
	}
	for _, args := range [][]string{{"status"}, {"status", "--json"}, {"install"}} {
		if code, out, stderr := runAPICLI(t, append([]string{"api", "service"}, args...)...); code != 0 || !strings.Contains(out, ui) {
			t.Fatalf("%v: %d %s %s", args, code, out, stderr)
		}
	}
	fake.beforeInstall = nil
	if code, out, stderr := runAPICLI(t, "api", "service", "install", "--ui", "", "--json"); code != 0 || !strings.Contains(out, `"ui_dir":""`) {
		t.Fatalf("clear: %d %s %s", code, out, stderr)
	}
	cfg, err := config.Load()
	if err != nil || cfg.API.UIDir != "" {
		t.Fatalf("clear config: %+v %v", cfg, err)
	}
}

func TestAPIServiceInvalidUIDoesNotWriteOrInstall(t *testing.T) {
	apiStateTree(t, t.TempDir())
	fake := fakeAPIService(t)
	before, err := os.ReadFile(config.ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	empty := t.TempDir()
	if err := os.Mkdir(filepath.Join(empty, "index.html"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{filepath.Join(t.TempDir(), "missing"), t.TempDir(), empty, config.ConfigPath()} {
		code, _, stderr := runAPICLI(t, "api", "service", "install", "--ui", dir)
		if code != 1 || !strings.Contains(stderr, "build your UI first") {
			t.Fatalf("%s: %d %s", dir, code, stderr)
		}
	}
	after, err := os.ReadFile(config.ConfigPath())
	if err != nil || string(before) != string(after) || len(fake.calls) != 0 {
		t.Fatalf("invalid UI mutated config/manager: %v %v", err, fake.calls)
	}
}
func (f *fakeAPIManager) Uninstall(context.Context) error {
	f.calls = append(f.calls, "uninstall")
	if f.failure != nil {
		return f.failure
	}
	f.status = apiservice.Status{Manager: "fake", Message: "Not installed; run `sidecar api service install`."}
	return nil
}
func (f *fakeAPIManager) Status(context.Context) (apiservice.Status, error) {
	f.calls = append(f.calls, "status")
	return f.status, nil
}
func fakeAPIService(t *testing.T) *fakeAPIManager {
	t.Helper()
	previous := apiServiceManager
	t.Cleanup(func() { apiServiceManager = previous })
	fake := &fakeAPIManager{status: apiservice.Status{Manager: "fake", Message: "Not installed; run `sidecar api service install`."}}
	apiServiceManager = func(Env) (apiservice.Manager, error) { return fake, nil }
	return fake
}
func TestAPIServiceCLIWithFakeAdapter(t *testing.T) {
	apiStateTree(t, t.TempDir())
	fake := fakeAPIService(t)
	for _, args := range [][]string{{"status", "--json"}, {"install", "--json"}, {"uninstall", "--json"}} {
		code, out, errOut := runAPICLI(t, append([]string{"api", "service"}, args...)...)
		if code != 0 || !strings.Contains(out, `"manager":"fake"`) || !strings.Contains(out, `"last_exit":null`) {
			t.Fatalf("%v = %d %s %s", args, code, out, errOut)
		}
	}
	if strings.Join(fake.calls, ",") != "status,status,install,status,uninstall,status" {
		t.Fatalf("calls: %v", fake.calls)
	}
	fake.failure = errors.New("fake manager failed; inspect test log and retry install")
	if code, _, stderr := runAPICLI(t, "api", "service", "install"); code != 1 || !strings.Contains(stderr, "inspect test log") {
		t.Fatalf("failure: %d %s", code, stderr)
	}
}
func TestAPIServiceCLIUsage(t *testing.T) {
	apiStateTree(t, t.TempDir())
	fake := fakeAPIService(t)
	for _, args := range [][]string{{"--help"}, {"status", "--help"}, {"install", "-h"}} {
		if code, out, _ := runAPICLI(t, append([]string{"api", "service"}, args...)...); code != 0 || !strings.Contains(out, "sidecar api service") {
			t.Fatalf("help: %d %s", code, out)
		}
	}
	for _, args := range [][]string{{"nope"}, {"status", "--unknown"}, {"status", "--ui", ""}, {"uninstall", "--ui", ""}, {"install", "--ui"}, {"install", "unexpected"}} {
		if code, _, _ := runAPICLI(t, append([]string{"api", "service"}, args...)...); code != 2 {
			t.Fatalf("usage: %v => %d", args, code)
		}
	}
	if len(fake.calls) != 0 {
		t.Fatalf("usage called manager: %v", fake.calls)
	}
}

func TestAPIServiceUIPreservesSymlinkPath(t *testing.T) {
	apiStateTree(t, t.TempDir())
	fakeAPIService(t)
	build := t.TempDir()
	if err := os.WriteFile(filepath.Join(build, "index.html"), []byte("UI"), 0o644); err != nil {
		t.Fatal(err)
	}
	current := filepath.Join(t.TempDir(), "current")
	if err := os.Symlink(build, current); err != nil {
		t.Fatal(err)
	}
	if code, _, stderr := runAPICLI(t, "api", "service", "install", "--ui", current); code != 0 {
		t.Fatalf("symlink: %d %s", code, stderr)
	}
	cfg, err := config.Load()
	if err != nil || cfg.API.UIDir != current {
		t.Fatalf("resolved away current: %+v %v", cfg, err)
	}
}
func TestAPIServiceRefusesForegroundServerAndReportsManagedVersion(t *testing.T) {
	state := apiStateTree(t, t.TempDir())
	fake := fakeAPIService(t)
	if err := config.SaveAPIUIDir("/tmp/existing-ui"); err != nil {
		t.Fatal(err)
	}
	server, err := uiapi.Start(uiapi.Options{StateDir: state, Port: 0, Backend: staticAPIBackend{}, Version: "managed-test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	if code, _, stderr := runAPICLI(t, "api", "service", "install", "--ui", ""); code != 1 || !strings.Contains(stderr, "outside this service") {
		t.Fatalf("foreground refused: %d %s", code, stderr)
	}
	if cfg, err := config.Load(); err != nil || cfg.API.UIDir != "/tmp/existing-ui" {
		t.Fatalf("foreground refusal changed UI config: %+v %v", cfg, err)
	}
	for _, call := range fake.calls {
		if call == "install" {
			t.Fatal("started manager alongside foreground server")
		}
	}
	fake.status.Running = true
	fake.status.PID = server.Endpoint().PID
	if code, out, stderr := runAPICLI(t, "api", "service", "status", "--json"); code != 0 || !strings.Contains(out, `"version":"managed-test"`) {
		t.Fatalf("version: %d %s %s", code, out, stderr)
	}
	if code, _, stderr := runAPICLI(t, "api", "service", "install"); code != 0 {
		t.Fatalf("managed reinstall refused: %d %s", code, stderr)
	}
	fake.status.PID++
	if code, out, _ := runAPICLI(t, "api", "service", "status", "--json"); code != 0 || !strings.Contains(out, `"version":""`) {
		t.Fatalf("wrong PID version: %d %s", code, out)
	}
}
func TestAPIServiceIsolatedProofCannotReachRealManager(t *testing.T) {
	apiStateTree(t, t.TempDir())
	if code, _, stderr := runAPICLI(t, "api", "service", "install"); code != 1 || !strings.Contains(stderr, "disabled for isolated proofs") {
		t.Fatalf("isolation: %d %s", code, stderr)
	}
}

func TestAPIServiceRefusesForegroundWithUnavailableDiscovery(t *testing.T) {
	for _, discovery := range []string{"missing", "corrupt", "stale"} {
		t.Run(discovery, func(t *testing.T) {
			state := apiStateTree(t, t.TempDir())
			fake := fakeAPIService(t)
			server, err := uiapi.Start(uiapi.Options{StateDir: state, Port: 0, Backend: staticAPIBackend{}})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
			path := uiapi.EndpointPath(state)
			switch discovery {
			case "missing":
				err = os.Remove(path)
			case "corrupt":
				err = os.WriteFile(path, []byte("{"), 0o600)
			case "stale":
				err = os.WriteFile(path, []byte(`{"pid":-1}`), 0o600)
			}
			if err != nil {
				t.Fatal(err)
			}
			if code, _, stderr := runAPICLI(t, "api", "service", "install"); code != 1 || !strings.Contains(stderr, "stop that API process") {
				t.Fatalf("install with %s discovery: %d %s", discovery, code, stderr)
			}
			for _, call := range fake.calls {
				if call == "install" {
					t.Fatal("started manager beside foreground server holding serve.lock")
				}
			}
		})
	}
}

func TestAPIServiceIgnoresUnlockedStaleDiscovery(t *testing.T) {
	state := apiStateTree(t, t.TempDir())
	fakeAPIService(t)
	if err := os.MkdirAll(uiapi.Dir(state), 0o700); err != nil {
		t.Fatal(err)
	}
	// PID reuse after a crash does not turn old discovery into a running server.
	if err := os.WriteFile(uiapi.EndpointPath(state), []byte(fmt.Sprintf(`{"pid":%d}`, os.Getpid())), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, stderr := runAPICLI(t, "api", "service", "install"); code != 0 {
		t.Fatalf("stale endpoint refused: %d %s", code, stderr)
	}
}

// api.tailnet lives in config, so reinstalling the service, whose definition
// no longer carries --tailnet, keeps the Tailnet listener on.
func TestAPIServiceInstallSavesTailnet(t *testing.T) {
	apiStateTree(t, t.TempDir())
	fakeAPIService(t)
	if code, out, stderr := runAPICLI(t, "api", "service", "install", "--tailnet"); code != 0 || !strings.Contains(out, "Tailnet listener: on (api.tailnet)") {
		t.Fatalf("install --tailnet: %d %s %s", code, out, stderr)
	}
	if cfg, err := config.Load(); err != nil || !cfg.API.Tailnet {
		t.Fatalf("api.tailnet not saved: %+v %v", cfg.API, err)
	}
	if code, out, stderr := runAPICLI(t, "api", "service", "install"); code != 0 || !strings.Contains(out, "Tailnet listener: on") {
		t.Fatalf("plain reinstall must keep api.tailnet: %d %s %s", code, out, stderr)
	}
	if code, out, stderr := runAPICLI(t, "api", "service", "install", "--no-tailnet"); code != 0 || !strings.Contains(out, "Tailnet listener: off") {
		t.Fatalf("install --no-tailnet: %d %s %s", code, out, stderr)
	}
	if cfg, err := config.Load(); err != nil || cfg.API.Tailnet {
		t.Fatalf("api.tailnet not cleared: %+v %v", cfg.API, err)
	}
	if code, _, _ := runAPICLI(t, "api", "service", "install", "--tailnet", "--no-tailnet"); code != 2 {
		t.Fatalf("contradictory flags exit = %d, want 2", code)
	}
}

// A definition installed before api.tailnet existed passed --tailnet itself;
// reinstalling carries that into config instead of dropping it.
func TestAPIServiceReinstallCarriesInstalledTailnetFlag(t *testing.T) {
	apiStateTree(t, t.TempDir())
	fake := fakeAPIService(t)
	plist := filepath.Join(t.TempDir(), "com.haplab.sidecar.api.plist")
	if err := os.WriteFile(plist, []byte("<array><string>/bin/sidecar</string><string>api</string><string>serve</string><string>--tailnet</string></array>"), 0o644); err != nil {
		t.Fatal(err)
	}
	fake.status.Installed, fake.status.File = true, plist
	if code, out, stderr := runAPICLI(t, "api", "service", "install"); code != 0 || !strings.Contains(out, "ran with --tailnet") || !strings.Contains(out, "Tailnet listener: on") {
		t.Fatalf("reinstall: %d %s %s", code, out, stderr)
	}
	if cfg, err := config.Load(); err != nil || !cfg.API.Tailnet {
		t.Fatalf("installed --tailnet not carried into api.tailnet: %+v %v", cfg.API, err)
	}
}

func TestDefinitionRunsTailnet(t *testing.T) {
	for text, want := range map[string]bool{
		"<string>serve</string><string>--tailnet</string>":           true,
		"ExecStart=/bin/sidecar api serve --tailnet-mode serve":      true,
		"ExecStart=/bin/sidecar api serve --tailnet-port=7862":       true,
		"<string>serve</string>":                                     false,
		"<key>tailnet</key><string>/state/api/tailnet.sock</string>": false,
	} {
		if got := definitionRunsTailnet([]byte(text)); got != want {
			t.Errorf("definitionRunsTailnet(%q) = %v, want %v", text, got, want)
		}
	}
}
