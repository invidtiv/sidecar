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

func TestAPIStatusReportsServedUIDir(t *testing.T) {
	state := apiStateTree(t, t.TempDir())
	ui := t.TempDir()
	if err := os.WriteFile(ui+"/index.html", []byte("UI"), 0o644); err != nil {
		t.Fatal(err)
	}
	server, err := uiapi.Start(uiapi.Options{StateDir: state, Port: 0, Backend: staticAPIBackend{}, UIDir: ui})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	for _, args := range [][]string{{"api", "status"}, {"api", "status", "--json"}} {
		code, out, stderr := runAPICLI(t, args...)
		if code != 0 || !strings.Contains(out, ui) {
			t.Fatalf("%v: %d %s %s", args, code, out, stderr)
		}
		if len(args) == 3 && !strings.Contains(out, `"ui_dir":`) {
			t.Fatalf("missing field: %s", out)
		}
	}
}

func TestAPICommandUsageErrors(t *testing.T) {
	apiStateTree(t, t.TempDir())
	for _, args := range [][]string{
		{"api", "pair"},
		{"api", "pair", "--list", "--origin", "http://a.example"},
		{"api", "pair", "--revoke-sessions", "--list"},
		{"api", "pair", "--revoke-sessions", "--revoke", "http://a.example"},
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
	code, stdout, stderr = runAPICLI(t, "api", "pair", "--revoke-sessions", "--origin", server.BrowserURL(), "--json")
	var sessions uiapi.SessionRevocation
	if code != 0 || json.Unmarshal([]byte(stdout), &sessions) != nil || sessions.Origin != server.BrowserURL() || sessions.Revoked != 0 {
		t.Fatalf("revoke-sessions --origin: %d %q %q", code, stdout, stderr)
	}
	if code, stdout, _ = runAPICLI(t, "api", "pair", "--revoke-sessions"); code != 0 || !strings.Contains(stdout, "Signed out 0 browser session(s) on every origin") {
		t.Fatalf("revoke-sessions: %d %q", code, stdout)
	}

	code, stdout, _ = runAPICLI(t, "api", "open", "--print", "--path", "/s/x")
	if code != 0 || !strings.HasPrefix(stdout, server.BrowserURL()+"/pair#code=") || !strings.Contains(stdout, "next=%2Fs%2Fx") {
		t.Fatalf("open --print: %d %q", code, stdout)
	}
	opened := ""
	previous := apiOpenBrowser
	apiOpenBrowser = func(url string) error { opened = url; return nil }
	t.Cleanup(func() { apiOpenBrowser = previous })
	if code, _, _ = runAPICLI(t, "api", "open"); code != 0 || !strings.HasPrefix(opened, server.BrowserURL()+"/pair#code=") {
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
	cfg := `{"api":{"tailnetLogins":["a@example.com"," b@example.com "],"tailnetHTTPSPort":7861}}`
	if err := os.WriteFile(config.ConfigPath(), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	options, err = apiTailnetOptions(context.Background(), 7862)
	if err != nil || strings.Join(options.Logins, ",") != "a@example.com,b@example.com" || options.Port != 7862 || options.HTTPSPort != 7861 {
		t.Fatalf("configured options = %+v, %v", options, err)
	}
	if err := os.WriteFile(config.ConfigPath(), []byte(`{"api":{"tailnetHTTPSPort":-1}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := apiTailnetOptions(context.Background(), 0); err == nil || !strings.Contains(err.Error(), "tailnetHTTPSPort") {
		t.Fatalf("invalid port: %v", err)
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

func TestTailnetHintWarnsOnlyForTheLoopbackPort(t *testing.T) {
	tailnet := &uiapi.TailnetOptions{Host: "node.example.ts.net", Logins: []string{"owner@example.com"}}
	var out bytes.Buffer
	printTailnetHint(uiapi.Endpoint{TailnetTCP: "127.0.0.1:7862"}, tailnet, &out)
	if !strings.Contains(out.String(), "tailscale serve --bg http://127.0.0.1:7862") || !strings.Contains(out.String(), "any local process or OS user") {
		t.Fatalf("port hint = %q", out.String())
	}
	out.Reset()
	printTailnetHint(uiapi.Endpoint{TailnetSocket: "/tmp/s/tailnet.sock"}, tailnet, &out)
	if !strings.Contains(out.String(), "unix:/tmp/s/tailnet.sock") || strings.Contains(out.String(), "Warning") {
		t.Fatalf("socket hint = %q", out.String())
	}
}

func TestDedicatedTailnetHTTPSPortHint(t *testing.T) {
	tailnet := &uiapi.TailnetOptions{Host: "node.example.ts.net", Logins: []string{"owner@example.com"}, HTTPSPort: 7861}
	var out bytes.Buffer
	printTailnetHint(uiapi.Endpoint{TailnetSocket: "/tmp/s/tailnet.sock"}, tailnet, &out)
	if !strings.Contains(out.String(), "https://node.example.ts.net:7861") || !strings.Contains(out.String(), "tailscale serve --bg --https=7861 unix:/tmp/s/tailnet.sock") {
		t.Fatalf("hint = %q", out.String())
	}
}

func TestAPIPairContentScopeCLI(t *testing.T) {
	stateDir := apiStateTree(t, t.TempDir())
	server, err := uiapi.Start(uiapi.Options{StateDir: stateDir, Port: 0, Backend: staticAPIBackend{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	code, stdout, stderr := runAPICLI(t, "api", "pair", "--origin", "http://content.example", "--scopes", "content:read", "--json")
	if code != 0 {
		t.Fatalf("scoped pairing: %d %s", code, stderr)
	}
	var registration uiapi.OriginRegistration
	if err = json.Unmarshal([]byte(stdout), &registration); err != nil {
		t.Fatal(err)
	}
	if len(registration.Scopes) != 1 || registration.Scopes[0] != uiapi.ScopeContentRead {
		t.Fatalf("scopes: %+v", registration.Scopes)
	}
	code, _, _ = runAPICLI(t, "api", "pair", "--list", "--scopes", "content:read")
	if code != 2 {
		t.Fatalf("scopes accepted without pairing: %d", code)
	}
}

func TestAPIPairCombinedScopeSpellings(t *testing.T) {
	stateDir := apiStateTree(t, t.TempDir())
	server, err := uiapi.Start(uiapi.Options{StateDir: stateDir, Port: 0, Backend: staticAPIBackend{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	for _, flags := range [][]string{
		{"--scope", "workspace:write", "--scope", "content:read"},
		{"--scopes", "workspace:write, content:read"},
		{"--scope", "workspace:write", "--scopes", "content:read,workspace:write"},
	} {
		args := append([]string{"api", "pair", "--origin", "http://combined.example", "--json"}, flags...)
		code, stdout, stderr := runAPICLI(t, args...)
		if code != 0 {
			t.Fatalf("pair %v: %d %s", flags, code, stderr)
		}
		var registration uiapi.OriginRegistration
		if err := json.Unmarshal([]byte(stdout), &registration); err != nil {
			t.Fatal(err)
		}
		if strings.Join(registration.Scopes, ",") != "content:read,workspace:write" {
			t.Fatalf("scopes: %+v", registration.Scopes)
		}
	}
}

func TestAPIOpenProxyPrint(t *testing.T) {
	state := apiStateTree(t, t.TempDir())
	const origin = "https://node.example.ts.net:7861"
	server, err := uiapi.Start(uiapi.Options{StateDir: state, Port: 0, Backend: staticAPIBackend{}, BrowserProxyOrigin: origin})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	code, out, stderr := runAPICLI(t, "api", "open", "--proxy", "--print", "--path", "/s/x")
	if code != 0 || !strings.HasPrefix(out, origin+"/pair#code=") || !strings.Contains(out, "next=%2Fs%2Fx") {
		t.Fatalf("proxy open: %d %s", code, stderr)
	}
	code, out, _ = runAPICLI(t, "api", "open", "--print")
	if code != 0 || !strings.HasPrefix(out, server.BrowserURL()+"/pair#code=") {
		t.Fatal("default local pairing origin changed")
	}
}

func TestAPIOpenProxyNeedsConfiguredOrigin(t *testing.T) {
	state := apiStateTree(t, t.TempDir())
	server, err := uiapi.Start(uiapi.Options{StateDir: state, Port: 0, Backend: staticAPIBackend{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	code, out, stderr := runAPICLI(t, "api", "open", "--proxy", "--print")
	if code != 1 || out != "" || !strings.Contains(stderr, "browserProxyOrigin") {
		t.Fatalf("unconfigured proxy: %d %s", code, stderr)
	}
}
