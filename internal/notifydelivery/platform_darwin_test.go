//go:build darwin

package notifydelivery

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/marcus/sidecar/internal/notify"
)

type runnerCall struct {
	name string
	args []string
}

type fakeRunner struct {
	mu       sync.Mutex
	paths    map[string]string
	runError map[string]error
	calls    []runnerCall
}

func (r *fakeRunner) LookPath(name string) (string, error) {
	if path := r.paths[name]; path != "" {
		return path, nil
	}
	return "", errors.New("not found")
}
func (r *fakeRunner) Run(_ context.Context, name string, args ...string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, runnerCall{name: name, args: append([]string(nil), args...)})
	key := name
	if len(args) > 0 {
		key += " " + args[0]
	}
	return r.runError[key]
}

func TestDarwinNativePrefersTerminalNotifierWithSafeArgv(t *testing.T) {
	runner := &fakeRunner{paths: map[string]string{
		terminalNotifierProvider: "/opt/homebrew/bin/terminal-notifier",
		"/usr/bin/osascript":     "/usr/bin/osascript",
	}, runError: map[string]error{}}
	provider := NewPlatformNative(runner)
	message := Message{
		Title: "[title] $(touch nope)", Body: "-message; rm -rf /", Severity: notify.SeverityError,
		Group: "sidecar-group", ActivationBundleID: "com.apple.Terminal",
	}
	receipt, err := provider.Deliver(context.Background(), message)
	if err != nil || !receipt.Delivered || receipt.Provider != terminalNotifierProvider {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
	if len(runner.calls) != 2 || runner.calls[0].args[0] != "-version" {
		t.Fatalf("calls=%+v", runner.calls)
	}
	call := runner.calls[1]
	joined := strings.Join(call.args, "\x00")
	for _, forbidden := range []string{"-execute", "-open", "-sender", "-ignoreDnD", "-urgency"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("unsafe/unsupported option %q in %#v", forbidden, call.args)
		}
	}
	if call.args[1] != `\[title] $(touch nope)` || call.args[3] != " -message; rm -rf /" {
		t.Fatalf("provider-specific values = %#v", call.args)
	}
	if !containsArgPair(call.args, "-group", "sidecar-group") || !containsArgPair(call.args, "-activate", "com.apple.Terminal") || !containsArgPair(call.args, "-subtitle", "Session ended") {
		t.Fatalf("missing owned grouping, activation, or severity mapping: %#v", call.args)
	}
}

func TestDarwinNativeFallsBackToConstantOsascript(t *testing.T) {
	runner := &fakeRunner{paths: map[string]string{
		terminalNotifierProvider: "/bin/terminal-notifier",
		"/usr/bin/osascript":     "/usr/bin/osascript",
	}, runError: map[string]error{"/bin/terminal-notifier -version": errors.New("bad install")}}
	provider := NewPlatformNative(runner)
	title := `dangerous " & do shell script "touch /tmp/nope"`
	body := "$(touch /tmp/nope); `whoami`"
	receipt, err := provider.Deliver(context.Background(), Message{Title: title, Body: body})
	if err != nil || receipt.Provider != osaScriptProvider || !receipt.Delivered {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
	call := runner.calls[len(runner.calls)-1]
	if call.name != "/usr/bin/osascript" || len(call.args) != 5 || call.args[0] != "-e" || call.args[1] != osascriptNotificationScript || call.args[2] != "--" || call.args[3] != title || call.args[4] != body {
		t.Fatalf("osascript call = %#v", call)
	}
	if strings.Contains(osascriptNotificationScript, title) || strings.Contains(osascriptNotificationScript, body) || strings.Contains(osascriptNotificationScript, "do shell script") {
		t.Fatal("user text was interpolated into AppleScript")
	}
	if err := provider.Remove(context.Background(), "sidecar-group"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("osascript removal = %v, want unsupported", err)
	}
}

func TestDarwinNativeRemovesStableGroup(t *testing.T) {
	runner := &fakeRunner{paths: map[string]string{terminalNotifierProvider: "/bin/terminal-notifier"}, runError: map[string]error{}}
	provider := NewPlatformNative(runner)
	if err := provider.Remove(context.Background(), "sidecar-stable"); err != nil {
		t.Fatal(err)
	}
	call := runner.calls[len(runner.calls)-1]
	if call.name != "/bin/terminal-notifier" || !containsArgPair(call.args, "-remove", "sidecar-stable") {
		t.Fatalf("remove call = %#v", call)
	}
}

func TestDarwinAFPlayUsesMaterializedPathAsOneArg(t *testing.T) {
	runner := &fakeRunner{paths: map[string]string{"/usr/bin/afplay": "/usr/bin/afplay"}, runError: map[string]error{}}
	cache := fakeCache{path: "/tmp/a cue; still one arg.wav"}
	player := NewPlatformSound(runner, cache)
	receipt, err := player.Play(context.Background(), CueAttention)
	if err != nil || !receipt.Delivered {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
	call := runner.calls[len(runner.calls)-1]
	if len(call.args) != 1 || call.args[0] != cache.path {
		t.Fatalf("afplay args = %#v", call.args)
	}
}

type fakeCache struct{ path string }

func (c fakeCache) Materialize(Cue) (string, error) { return c.path, nil }

func containsArgPair(args []string, key, value string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == key && args[i+1] == value {
			return true
		}
	}
	return false
}
