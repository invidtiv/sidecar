package resourceprovider

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/resource"
)

func TestCommandProviderDescribe(t *testing.T) {
	p := newFixtureProvider(t, "fixture")
	desc, err := p.Describe(context.Background())
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if desc.Info.Kind != "fixture" || desc.Info.Name != "Fixture" {
		t.Fatalf("info = %+v", desc.Info)
	}
	if desc.Info.DocsURL != "https://example.test/sidecar-fixture/setup" {
		t.Fatalf("docsUrl = %q", desc.Info.DocsURL)
	}
	if len(desc.Matchers) != 2 || desc.Matchers[0].ID != "issue-key" {
		t.Fatalf("matchers = %+v", desc.Matchers)
	}
}

func TestCommandProviderResolve(t *testing.T) {
	p := newFixtureProvider(t, "fixture")
	doc, err := p.Resolve(context.Background(), resource.Reference{Instance: "fixture", Matcher: "issue-key", Locator: "CASH-1245"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if doc.Identity != "CASH-1245" {
		t.Fatalf("identity = %q", doc.Identity)
	}
	if doc.SourceURL != "https://fixture.example.test/browse/CASH-1245" {
		t.Fatalf("sourceUrl = %q", doc.SourceURL)
	}
	if doc.Status == nil || doc.Status.Tone != resource.ToneInfo {
		t.Fatalf("status = %+v", doc.Status)
	}
	if doc.FreshFor != time.Minute {
		t.Fatalf("freshFor = %s", doc.FreshFor)
	}
	if doc.UpdatedAt.IsZero() {
		t.Fatal("updatedAt was dropped")
	}
}

func TestCommandProviderRejectsForeignReference(t *testing.T) {
	p := newFixtureProvider(t, "fixture")
	_, err := p.Resolve(context.Background(), resource.Reference{Instance: "someone-else", Matcher: "m", Locator: "X-1"})
	var terr *TransportError
	if !errors.As(err, &terr) || terr.Reason != ReasonInvalidRequest {
		t.Fatalf("err = %v", err)
	}
}

func TestCommandProviderTypedFailure(t *testing.T) {
	p := newFixtureProvider(t, "fixture", "-mode=error-response")
	_, err := p.Resolve(context.Background(), resource.Reference{Instance: "fixture", Matcher: "issue-key", Locator: "CASH-1"})
	var rerr *resource.Error
	if !errors.As(err, &rerr) {
		t.Fatalf("err = %v (%T)", err, err)
	}
	if rerr.Code != resource.CodeUnauthorized || rerr.Retryable {
		t.Fatalf("error = %+v", rerr)
	}
	if rerr.SetupHint != "run fixtureprovider configure" {
		t.Fatalf("setupHint = %q", rerr.SetupHint)
	}
	// A typed failure exits 0 and is a service failure, not a transport one.
	var terr *TransportError
	if errors.As(err, &terr) {
		t.Fatalf("typed failure misreported as transport: %v", terr)
	}
}

// The hostile matrix. Every case must fail boundedly, with the documented
// reason, and leave no child behind.
func TestCommandProviderHostileCases(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		method  string
		reason  TransportReason
		timeout time.Duration
	}{
		{name: "malformed json", args: []string{"-mode=malformed"}, method: "describe", reason: ReasonMalformed},
		{name: "no output", args: []string{"-mode=no-output"}, method: "describe", reason: ReasonMalformed},
		{name: "extra stdout object", args: []string{"-mode=extra-stdout"}, method: "describe", reason: ReasonExtraOutput},
		{name: "trailing log line", args: []string{"-mode=trailing-log"}, method: "describe", reason: ReasonExtraOutput},
		{name: "oversize response", args: []string{"-mode=oversize"}, method: "resolve", reason: ReasonOversize},
		{name: "incompatible protocol", args: []string{"-mode=bad-protocol"}, method: "describe", reason: ReasonProtocol},
		{name: "missing protocol", args: []string{"-mode=no-protocol"}, method: "describe", reason: ReasonProtocol},
		{name: "crash", args: []string{"-mode=crash"}, method: "describe", reason: ReasonExit},
		{name: "crash after output", args: []string{"-mode=crash-after-output"}, method: "describe", reason: ReasonExit},
		{name: "duplicate matcher ids", args: []string{"-mode=dup-matchers"}, method: "describe", reason: ReasonInvalidDescribe},
		{name: "invalid re2", args: []string{"-mode=invalid-re2"}, method: "describe", reason: ReasonInvalidDescribe},
		{name: "too many matchers", args: []string{"-mode=too-many-matchers"}, method: "describe", reason: ReasonInvalidDescribe},
		{name: "hang past the timeout", args: []string{"-mode=hang"}, method: "describe", reason: ReasonTimeout, timeout: 400 * time.Millisecond},
		{name: "missing command", args: nil, method: "describe", reason: ReasonSpawn},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			argv := append([]string{fixtureBin}, tc.args...)
			if tc.name == "missing command" {
				argv = []string{filepath.Join(t.TempDir(), "no-such-provider")}
			}
			p, err := NewCommandProvider(CommandConfig{
				Instance:       "fixture",
				Argv:           argv,
				Dir:            t.TempDir(),
				HostEnv:        os.Environ(),
				ResolveTimeout: tc.timeout,
			})
			if err != nil {
				t.Fatalf("NewCommandProvider: %v", err)
			}
			if tc.timeout > 0 {
				p.describeTimeout = tc.timeout
			}

			started := time.Now()
			if tc.method == "describe" {
				_, err = p.Describe(context.Background())
			} else {
				_, err = p.Resolve(context.Background(), resource.Reference{Instance: "fixture", Matcher: "issue-key", Locator: "CASH-1"})
			}
			elapsed := time.Since(started)

			var terr *TransportError
			if !errors.As(err, &terr) {
				t.Fatalf("expected a transport error, got %v (%T)", err, err)
			}
			if terr.Reason != tc.reason {
				t.Fatalf("reason = %q, want %q", terr.Reason, tc.reason)
			}
			if elapsed > 10*time.Second {
				t.Fatalf("failure was not bounded: took %s", elapsed)
			}
			// A transport error carries host-authored text only.
			if strings.Contains(terr.Error(), "noise") {
				t.Fatalf("transport error leaked provider output: %q", terr.Error())
			}
		})
	}
}

func TestCommandProviderStderrFloodIsCountedAndDiscarded(t *testing.T) {
	p := newFixtureProvider(t, "fixture", "-mode=stderr-flood")
	doc, err := p.Resolve(context.Background(), resource.Reference{Instance: "fixture", Matcher: "issue-key", Locator: "CASH-1"})
	if err != nil {
		t.Fatalf("a noisy provider should still succeed: %v", err)
	}
	if doc.Identity != "CASH-1" {
		t.Fatalf("identity = %q", doc.Identity)
	}
	// Nothing in the document may come from stderr.
	if strings.Contains(doc.Title, "noise") {
		t.Fatalf("stderr reached the document: %q", doc.Title)
	}
}

func TestCommandProviderKillsForkedDescendant(t *testing.T) {
	pidfile := filepath.Join(t.TempDir(), "descendant.pid")
	p := newFixtureProvider(t, "fixture", "-mode=fork-descendant", "-pidfile="+pidfile)
	p.describeTimeout = 700 * time.Millisecond

	started := time.Now()
	_, err := p.Describe(context.Background())
	elapsed := time.Since(started)

	var terr *TransportError
	if !errors.As(err, &terr) || terr.Reason != ReasonTimeout {
		t.Fatalf("expected a timeout, got %v", err)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("the forked descendant held the invocation open for %s", elapsed)
	}

	raw, readErr := os.ReadFile(pidfile)
	if readErr != nil {
		t.Fatalf("the fixture did not record a descendant pid: %v", readErr)
	}
	pid, convErr := strconv.Atoi(strings.TrimSpace(string(raw)))
	if convErr != nil {
		t.Fatalf("bad pid file %q", raw)
	}
	if alive := waitForExit(pid, 3*time.Second); alive {
		t.Fatalf("descendant pid %d survived the process-group kill", pid)
	}
}

// waitForExit reports whether pid is still alive after the deadline. Signal 0
// is a liveness probe, not a kill.
func waitForExit(pid int, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); err != nil {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
	return syscall.Kill(pid, 0) == nil
}

func TestCommandProviderCancellationKillsTheChild(t *testing.T) {
	p := newFixtureProvider(t, "fixture", "-mode=hang")
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()
	started := time.Now()
	_, err := p.Describe(ctx)
	if time.Since(started) > 5*time.Second {
		t.Fatalf("cancellation did not stop the child")
	}
	var terr *TransportError
	if !errors.As(err, &terr) || terr.Reason != ReasonCanceled {
		t.Fatalf("expected cancellation, got %v", err)
	}
}

func TestCommandProviderHostileDocumentIsSanitized(t *testing.T) {
	p := newFixtureProvider(t, "fixture", "-mode=hostile-document")
	doc, err := p.Resolve(context.Background(), resource.Reference{Instance: "fixture", Matcher: "issue-key", Locator: "CASH-1"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if strings.ContainsAny(doc.Title+doc.Identity, "\x1b\x07") {
		t.Fatalf("escape sequence survived: %q", doc.Title)
	}
	if doc.SourceURL != "" {
		t.Fatalf("javascript: sourceUrl survived: %q", doc.SourceURL)
	}
	if len(doc.Fields) > resource.MaxFields {
		t.Fatalf("field count = %d", len(doc.Fields))
	}
	if doc.Status.Tone != resource.ToneNeutral {
		t.Fatalf("tone = %q", doc.Status.Tone)
	}
	if doc.Body.Format != resource.FormatText {
		t.Fatalf("format = %q", doc.Body.Format)
	}
	if !doc.UpdatedAt.IsZero() {
		t.Fatalf("unparseable timestamp survived")
	}
	if doc.FreshFor != resource.MaxFreshFor {
		t.Fatalf("freshFor = %s", doc.FreshFor)
	}
}

func TestCommandProviderUnknownErrorCodeCoerces(t *testing.T) {
	p := newFixtureProvider(t, "fixture", "-mode=unknown-code")
	_, err := p.Describe(context.Background())
	var rerr *resource.Error
	if !errors.As(err, &rerr) || rerr.Code != resource.CodeInternal {
		t.Fatalf("err = %v", err)
	}
}

// The child runs with no shell, a neutral working directory, and only the
// documented environment. Asking the fixture to report its own execution
// context is how that is proven from the outside.
func TestCommandProviderExecutionEnvironment(t *testing.T) {
	dir := t.TempDir()
	p, err := NewCommandProvider(CommandConfig{
		Instance: "fixture",
		Argv:     []string{fixtureBin, "-mode=env-report"},
		Dir:      dir,
		PassEnv:  []string{"FIXTURE_TOKEN"},
		HostEnv: []string{
			"PATH=/usr/bin",
			"HOME=/home/test",
			"LANG=en_US.UTF-8",
			"LC_TIME=en_GB.UTF-8",
			"HTTPS_PROXY=http://proxy.test:8080",
			"FIXTURE_TOKEN=s3cret",
			"AWS_SECRET_ACCESS_KEY=must-not-leak",
			"TMUX=/private/tmp/tmux-501/default,1,0",
			"SHELL=/bin/zsh",
		},
	})
	if err != nil {
		t.Fatalf("NewCommandProvider: %v", err)
	}
	doc, err := p.Resolve(context.Background(), resource.Reference{Instance: "fixture", Matcher: "m", Locator: "X-1"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	report := doc.Body.Text

	wantEnv := []string{"PATH=/usr/bin", "HOME=/home/test", "LANG=en_US.UTF-8", "LC_TIME=en_GB.UTF-8", "HTTPS_PROXY=http://proxy.test:8080", "FIXTURE_TOKEN=s3cret"}
	for _, want := range wantEnv {
		if !strings.Contains(report, want) {
			t.Fatalf("child environment missing %q:\n%s", want, report)
		}
	}
	for _, forbidden := range []string{"AWS_SECRET_ACCESS_KEY", "TMUX=", "SHELL="} {
		if strings.Contains(report, forbidden) {
			t.Fatalf("child environment inherited %q:\n%s", forbidden, report)
		}
	}

	// The working directory is the neutral one it was given, resolved through
	// symlinks (macOS /var -> /private/var).
	resolved, _ := filepath.EvalSymlinks(dir)
	if !strings.Contains(report, "cwd="+dir) && !strings.Contains(report, "cwd="+resolved) {
		t.Fatalf("child ran in the wrong directory:\n%s", report)
	}
	// argv reached the child verbatim, which means no shell interpreted it.
	if !strings.Contains(report, "argv="+fixtureBin+" -mode=env-report") {
		t.Fatalf("argv was not passed through:\n%s", report)
	}
}

// The request Sidecar writes and the responses it accepts must match the
// published fixtures byte for byte, field for field.
func TestProtocolGoldens(t *testing.T) {
	t.Run("describe request", func(t *testing.T) {
		req := Request{
			Protocol: resource.Protocol,
			Method:   MethodDescribe,
			Instance: "jira-work",
			Host:     &HostInfo{Name: "sidecar", Version: "0.0.0"},
		}
		assertMatchesGolden(t, "describe-request.json", req)
	})

	t.Run("resolve request", func(t *testing.T) {
		req := Request{
			Protocol: resource.Protocol,
			Method:   MethodResolve,
			Instance: "jira-work",
			Params:   &ResolveParams{Matcher: "issue-key", Locator: "CASH-1245"},
		}
		assertMatchesGolden(t, "resolve-request.json", req)
	})

	t.Run("describe response", func(t *testing.T) {
		resp := decodeGolden(t, "describe-response.json")
		desc, err := ValidateDescription("jira-work", resp.Provider, resp.Matchers)
		if err != nil {
			t.Fatalf("ValidateDescription: %v", err)
		}
		if desc.Info.Kind != "jira" || len(desc.Matchers) != 1 || desc.Matchers[0].Priority != 100 {
			t.Fatalf("description = %+v", desc)
		}
	})

	t.Run("resolve response", func(t *testing.T) {
		resp := decodeGolden(t, "resolve-response.json")
		doc, rerr := resource.SanitizeDocument(resp.Resource)
		if rerr != nil {
			t.Fatalf("SanitizeDocument: %v", rerr)
		}
		if doc.Identity != "CASH-1245" || doc.Status.Tone != resource.ToneInfo || len(doc.Fields) != 3 {
			t.Fatalf("document = %+v", doc)
		}
	})

	t.Run("error response", func(t *testing.T) {
		resp := decodeGolden(t, "error-response.json")
		e := resource.SanitizeError(resp.Error)
		if e.Code != resource.CodeUnauthorized || e.Retryable {
			t.Fatalf("error = %+v", e)
		}
	})
}

func decodeGolden(t *testing.T, name string) *Response {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "protocol", name))
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	resp, reason, detail := decodeResponse(raw)
	if reason != "" {
		t.Fatalf("golden %s rejected: %s (%s)", name, reason, detail)
	}
	return resp
}

func assertMatchesGolden(t *testing.T, name string, value any) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "protocol", name))
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	var want, got any
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatalf("golden %s is not JSON: %v", name, err)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatalf("re-decode: %v", err)
	}
	wantJSON, _ := json.MarshalIndent(want, "", "  ")
	gotJSON, _ := json.MarshalIndent(got, "", "  ")
	if string(wantJSON) != string(gotJSON) {
		t.Fatalf("request does not match %s:\nwant %s\ngot  %s", name, wantJSON, gotJSON)
	}
}
