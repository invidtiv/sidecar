package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/apiservice"
	"github.com/marcus/sidecar/internal/uiapi"
)

type fakeAPIManager struct {
	status  apiservice.Status
	calls   []string
	failure error
}

func (f *fakeAPIManager) Install(context.Context) error {
	f.calls = append(f.calls, "install")
	if f.failure != nil {
		return f.failure
	}
	f.status.Installed = true
	f.status.Loaded = true
	return nil
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
	for _, args := range [][]string{{"nope"}, {"status", "--unknown"}, {"install", "unexpected"}} {
		if code, _, _ := runAPICLI(t, append([]string{"api", "service"}, args...)...); code != 2 {
			t.Fatalf("usage: %v => %d", args, code)
		}
	}
	if len(fake.calls) != 0 {
		t.Fatalf("usage called manager: %v", fake.calls)
	}
}
func TestAPIServiceRefusesForegroundServerAndReportsManagedVersion(t *testing.T) {
	state := apiStateTree(t, t.TempDir())
	fake := fakeAPIService(t)
	server, err := uiapi.Start(uiapi.Options{StateDir: state, Port: 0, Backend: staticAPIBackend{}, Version: "managed-test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	if code, _, stderr := runAPICLI(t, "api", "service", "install"); code != 1 || !strings.Contains(stderr, "outside this service") {
		t.Fatalf("foreground refused: %d %s", code, stderr)
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
