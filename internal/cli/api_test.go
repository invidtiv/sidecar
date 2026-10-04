package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/mobileproto"
	"github.com/marcus/sidecar/internal/uiapi"
)

type staticAPIBackend struct{}

func (staticAPIBackend) Sessions(context.Context, mobileproto.CatalogQuery) (mobileproto.CatalogSnapshot, error) {
	return mobileproto.CatalogSnapshot{}, nil
}

func (staticAPIBackend) ServeTerminal(_ context.Context, input io.Reader, _ io.Writer) error {
	_, err := io.Copy(io.Discard, input)
	return err
}

func runAPICLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	handled, code := Run(args, &out, &errOut)
	if !handled {
		t.Fatalf("Run(%v) not handled", args)
	}
	return code, out.String(), errOut.String()
}

func TestAPICommandsWithoutAServer(t *testing.T) {
	apiStateTree(t, t.TempDir())
	for _, args := range [][]string{{"api", "status"}, {"api", "open", "--print"}, {"api", "pair", "--list"}} {
		code, _, stderr := runAPICLI(t, args...)
		if code != 1 || !strings.Contains(stderr, "sidecar api serve") {
			t.Fatalf("%v: code %d stderr %q", args, code, stderr)
		}
	}
}

func TestAPICommandUsageErrors(t *testing.T) {
	apiStateTree(t, t.TempDir())
	for _, args := range [][]string{
		{"api", "pair"},
		{"api", "pair", "--list", "--origin", "http://a.example"},
		{"api", "serve", "--port", "nope"},
		{"api", "serve", "--port", "70000"},
		{"api", "serve", "--tailnet-port", "0"},
		{"api", "status", "--bogus"},
		{"api", "open", "--path"},
		{"api", "nonsense"},
	} {
		if code, _, _ := runAPICLI(t, args...); code != 2 {
			t.Fatalf("%v: code %d, want 2", args, code)
		}
	}
	if code, stdout, _ := runAPICLI(t, "api", "--help"); code != 0 || !strings.Contains(stdout, "sidecar api <command>") {
		t.Fatalf("api --help: %d %q", code, stdout)
	}
}

func TestAPICommandsAgainstARunningServer(t *testing.T) {
	stateDir := apiStateTree(t, t.TempDir())
	server, err := uiapi.Start(uiapi.Options{StateDir: stateDir, Port: 0, Backend: staticAPIBackend{}, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })

	code, stdout, stderr := runAPICLI(t, "api", "status", "--json")
	if code != 0 {
		t.Fatalf("status --json: %d %s", code, stderr)
	}
	var viaHTTP json.RawMessage
	if err := uiapi.NewLocalClientForSocket(server.Endpoint()).Do(context.Background(), http.MethodGet, "/api/v0/status", nil, &viaHTTP); err != nil {
		t.Fatal(err)
	}
	if stdout != string(viaHTTP) {
		t.Fatalf("status --json is not the route document:\ncli  %s\nhttp %s", stdout, viaHTTP)
	}
	if code, stdout, _ = runAPICLI(t, "api", "status"); code != 0 || !strings.Contains(stdout, "browser") {
		t.Fatalf("status: %d %q", code, stdout)
	}

	code, stdout, stderr = runAPICLI(t, "api", "pair", "--origin", "HTTP://App.Example:5173", "--json")
	if code != 0 {
		t.Fatalf("pair: %d %s", code, stderr)
	}
	var registration uiapi.OriginRegistration
	if err := json.Unmarshal([]byte(stdout), &registration); err != nil || registration.Origin != "http://app.example:5173" || registration.Token == "" {
		t.Fatalf("pair = %q (%v)", stdout, err)
	}
	if code, stdout, _ = runAPICLI(t, "api", "pair", "--list", "--json"); code != 0 || !strings.Contains(stdout, "http://app.example:5173") || strings.Contains(stdout, registration.Token) {
		t.Fatalf("pair --list: %d %q", code, stdout)
	}
	if code, _, stderr = runAPICLI(t, "api", "pair", "--origin", "ftp://nope"); code != 1 || !strings.Contains(stderr, "http or https") {
		t.Fatalf("bad origin: %d %q", code, stderr)
	}
	if code, stdout, _ = runAPICLI(t, "api", "pair", "--revoke", "http://app.example:5173"); code != 0 || !strings.Contains(stdout, "Revoked") {
		t.Fatalf("revoke: %d %q", code, stdout)
	}
	if code, _, stderr = runAPICLI(t, "api", "pair", "--revoke", "http://app.example:5173"); code != 1 || !strings.Contains(stderr, "not paired") {
		t.Fatalf("second revoke: %d %q", code, stderr)
	}

	code, stdout, _ = runAPICLI(t, "api", "open", "--print", "--path", "/s/x")
	if code != 0 || !strings.HasPrefix(stdout, server.BrowserURL()+"/pair?code=") || !strings.Contains(stdout, "next=%2Fs%2Fx") {
		t.Fatalf("open --print: %d %q", code, stdout)
	}
	opened := ""
	previous := apiOpenBrowser
	apiOpenBrowser = func(url string) error { opened = url; return nil }
	t.Cleanup(func() { apiOpenBrowser = previous })
	if code, _, _ = runAPICLI(t, "api", "open"); code != 0 || !strings.HasPrefix(opened, server.BrowserURL()+"/pair?code=") {
		t.Fatalf("open: %d opened %q", code, opened)
	}
}

func TestAPITailnetOptionsDefaultToTheNodeOwner(t *testing.T) {
	apiStateTree(t, t.TempDir())
	previous := apiTailnetIdentity
	t.Cleanup(func() { apiTailnetIdentity = previous })
	apiTailnetIdentity = func(context.Context) (uiapi.TailnetIdentity, error) {
		return uiapi.TailnetIdentity{Host: "node.example.ts.net", OwnerLogin: "owner@example.com"}, nil
	}
	options, err := apiTailnetOptions(context.Background(), 0)
	if err != nil || options.Host != "node.example.ts.net" || strings.Join(options.Logins, ",") != "owner@example.com" {
		t.Fatalf("options = %+v, %v", options, err)
	}
	// Configured logins replace the default.
	cfg := `{"api":{"tailnetLogins":["a@example.com"," b@example.com "]}}`
	if err := os.WriteFile(config.ConfigPath(), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	options, err = apiTailnetOptions(context.Background(), 7862)
	if err != nil || strings.Join(options.Logins, ",") != "a@example.com,b@example.com" || options.Port != 7862 {
		t.Fatalf("configured options = %+v, %v", options, err)
	}
}

func TestParseTailscaleStatusIdentity(t *testing.T) {
	identity, err := uiapi.ParseTailscaleStatus([]byte(`{"Self":{"DNSName":"Aerie.tail53fd54.ts.net.","UserID":42},"User":{"42":{"LoginName":"marcus@example.com"}}}`))
	if err != nil || identity.Host != "aerie.tail53fd54.ts.net" || identity.OwnerLogin != "marcus@example.com" {
		t.Fatalf("identity = %+v, %v", identity, err)
	}
	if _, err := uiapi.ParseTailscaleStatus([]byte(`{"Self":{"DNSName":""}}`)); err == nil {
		t.Fatal("a node without MagicDNS was accepted")
	}
}
