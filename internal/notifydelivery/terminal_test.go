package notifydelivery

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/term"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/notify"
	"github.com/marcus/sidecar/internal/termnotify"
)

// terminalSink is the injected writer under test. It records complete writes,
// so a test can assert the exact bytes that would reach a real terminal.
type terminalSink struct {
	mu      sync.Mutex
	writes  []string
	flushes int
	err     error
	flushEr error
}

func (s *terminalSink) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return 0, s.err
	}
	s.writes = append(s.writes, string(p))
	return len(p), nil
}

func (s *terminalSink) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.flushes++
	return s.flushEr
}

func (s *terminalSink) written() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.writes...)
}

func terminalEnv(values map[string]string) func(string) string {
	return func(name string) string { return values[name] }
}

func newTerminalNative(t *testing.T, selected config.TerminalNotifier, env map[string]string) (NativeNotifier, *terminalSink) {
	t.Helper()
	sink := &terminalSink{}
	notifier := NewTerminalNative(TerminalOptions{
		Getenv:   terminalEnv(env),
		Selected: func() config.TerminalNotifier { return selected },
		Write:    sink.Write,
		Flush:    sink.Flush,
	})
	return notifier, sink
}

func TestTerminalNativeProbe(t *testing.T) {
	tests := []struct {
		name         string
		selected     config.TerminalNotifier
		env          map[string]string
		wantProvider string
		wantReason   string
	}{
		{
			name:         "off is the default and says how to turn it on",
			selected:     config.TerminalNotifierOff,
			env:          map[string]string{"TERM": "xterm-kitty"},
			wantProvider: "",
			wantReason:   "notifications.ssh.terminal",
		},
		{
			name:       "auto with nothing to go on stays unavailable",
			selected:   config.TerminalNotifierAuto,
			env:        map[string]string{"TERM": "xterm-256color"},
			wantReason: "no supported terminal was identified",
		},
		{
			name:       "auto refuses an unsupported terminal rather than falling back",
			selected:   config.TerminalNotifierAuto,
			env:        map[string]string{"TERM_PROGRAM": "Apple_Terminal"},
			wantReason: "no supported terminal was identified",
		},
		{
			name:         "auto recognises a forwarded TERM",
			selected:     config.TerminalNotifierAuto,
			env:          map[string]string{"TERM": "xterm-kitty"},
			wantProvider: "terminal:kitty",
			wantReason:   "detected from TERM",
		},
		{
			// The case the fixed choice exists for: SSH has dropped every
			// marker, so only an explicit selection can work.
			name:         "a fixed choice works with no markers at all",
			selected:     config.TerminalNotifierGhostty,
			env:          map[string]string{"TERM": "xterm-256color"},
			wantProvider: "terminal:ghostty",
			wantReason:   "best effort",
		},
		{
			name:         "a fixed choice overrides a contradicting marker",
			selected:     config.TerminalNotifierWezTerm,
			env:          map[string]string{"TERM": "xterm-kitty"},
			wantProvider: "terminal:wezterm",
		},
		{
			name:         "inside tmux status names the passthrough setting",
			selected:     config.TerminalNotifierITerm2,
			env:          map[string]string{"TMUX": "/tmp/tmux-501/default,1,0"},
			wantProvider: "terminal:iterm2",
			wantReason:   "allow-passthrough",
		},
		{
			// config.ValidateNotifications refuses this before a save, so it
			// can only arrive from a hand-edited config.json.
			name:       "a name with no encoder is refused",
			selected:   config.TerminalNotifier("alacritty"),
			env:        map[string]string{"TERM": "alacritty"},
			wantReason: "has no Sidecar encoder",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			notifier, _ := newTerminalNative(t, tt.selected, tt.env)
			got := notifier.Probe(context.Background())
			if got.Available != (tt.wantProvider != "") {
				t.Fatalf("Probe() = %+v, want available=%v", got, tt.wantProvider != "")
			}
			if tt.wantProvider != "" && got.Provider != tt.wantProvider {
				t.Errorf("Probe().Provider = %q, want %q", got.Provider, tt.wantProvider)
			}
			if tt.wantReason != "" && !strings.Contains(got.Reason, tt.wantReason) {
				t.Errorf("Probe().Reason = %q, want it to contain %q", got.Reason, tt.wantReason)
			}
		})
	}
}

func TestTerminalNativeProbeWithoutAWriter(t *testing.T) {
	notifier := NewTerminalNative(TerminalOptions{
		Getenv:   terminalEnv(map[string]string{"TERM": "xterm-kitty"}),
		Selected: func() config.TerminalNotifier { return config.TerminalNotifierAuto },
	})
	if got := notifier.Probe(context.Background()); got.Available || !strings.Contains(got.Reason, "writer") {
		t.Errorf("Probe() = %+v, want unavailable because nothing can write", got)
	}
}

func TestTerminalNativeDeliverWritesOneSequence(t *testing.T) {
	tests := []struct {
		name     string
		selected config.TerminalNotifier
		env      map[string]string
		want     string
	}{
		{
			name:     "ghostty",
			selected: config.TerminalNotifierGhostty,
			want:     "\x1b]9;Agent needs input: sidecar · main\x07",
		},
		{
			name:     "kitty",
			selected: config.TerminalNotifierKitty,
			want: "\x1b]99;i=ntf-01:d=0:p=title;Agent needs input\x1b\\" +
				"\x1b]99;i=ntf-01:d=1:p=body;sidecar · main\x1b\\",
		},
		{
			name:     "wezterm inside tmux",
			selected: config.TerminalNotifierWezTerm,
			env:      map[string]string{"TMUX": "/tmp/tmux-501/default,1,0"},
			want:     "\x1bPtmux;\x1b\x1b]9;Agent needs input: sidecar · main\x07\x1b\\",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			notifier, sink := newTerminalNative(t, tt.selected, tt.env)
			receipt, err := notifier.Deliver(context.Background(), Message{
				NotificationID: "ntf-01",
				Title:          "Agent needs input",
				Body:           "sidecar · main",
			})
			if err != nil {
				t.Fatalf("Deliver() error = %v", err)
			}
			if !receipt.Delivered || !strings.HasPrefix(receipt.Provider, "terminal:") {
				t.Fatalf("Deliver() receipt = %+v", receipt)
			}
			// One write per delivery: the TUI renderer shares this file
			// descriptor, and a sequence split across writes could interleave.
			if got := sink.written(); len(got) != 1 || got[0] != tt.want {
				t.Fatalf("writes = %q, want exactly [%q]", got, tt.want)
			}
			if sink.flushes != 1 {
				t.Errorf("flushes = %d, want 1", sink.flushes)
			}
		})
	}
}

// Message text has already been sanitized by NativeMessage before a provider
// sees it, but the transport re-sanitizes anyway: a provider that trusts its
// caller is a provider that breaks the day something else calls it.
func TestTerminalNativeDeliverSanitizesAdversarialText(t *testing.T) {
	for _, selected := range []config.TerminalNotifier{
		config.TerminalNotifierGhostty, config.TerminalNotifierITerm2,
		config.TerminalNotifierWezTerm, config.TerminalNotifierKitty,
	} {
		for _, env := range []map[string]string{nil, {"TMUX": "/tmp/tmux-501/default,1,0"}} {
			notifier, sink := newTerminalNative(t, selected, env)
			benign := Message{NotificationID: "ntf-01", Title: "needs input", Body: "sidecar main worktree"}
			adversarial := Message{
				NotificationID: "ntf\x1b]0;pwned\x07-01",
				Title:          "needs\x1b\\ input\x07",
				Body:           "sidecar\nmain\tworktree\x1b]9;pwned\x07",
			}
			for _, message := range []Message{benign, adversarial} {
				if _, err := notifier.Deliver(context.Background(), message); err != nil {
					t.Fatalf("%s: Deliver() error = %v", selected, err)
				}
			}
			written := sink.written()
			if len(written) != 2 {
				t.Fatalf("%s: writes = %q, want two sequences", selected, written)
			}
			// The adversarial message must not have contributed a single
			// framing byte of its own: the same terminal, the same tmux state
			// and the same field count give the same escape and BEL budget.
			for _, framing := range []string{"\x1b", "\x07"} {
				if got, want := strings.Count(written[1], framing), strings.Count(written[0], framing); got != want {
					t.Errorf("%s/tmux=%v: %d %q bytes, want the %d that framing alone needs: %q",
						selected, env != nil, got, framing, want, written[1])
				}
			}
			for _, forbidden := range []string{"\n", "\t", "\x00"} {
				if strings.Contains(written[1], forbidden) {
					t.Errorf("%s: control character %q survived: %q", selected, forbidden, written[1])
				}
			}
		}
	}
}

func TestTerminalNativeDeliverRefusesWhenUnavailable(t *testing.T) {
	for _, tt := range []struct {
		name     string
		selected config.TerminalNotifier
		env      map[string]string
	}{
		{"off", config.TerminalNotifierOff, map[string]string{"TERM": "xterm-kitty"}},
		{"auto with no marker", config.TerminalNotifierAuto, map[string]string{"TERM": "xterm-256color"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			notifier, sink := newTerminalNative(t, tt.selected, tt.env)
			receipt, err := notifier.Deliver(context.Background(), Message{NotificationID: "ntf-01", Title: "hi"})
			if err == nil || receipt.Delivered {
				t.Fatalf("Deliver() = %+v, %v, want a refusal", receipt, err)
			}
			if len(sink.written()) != 0 {
				t.Errorf("an unavailable transport wrote %q", sink.written())
			}
		})
	}
}

func TestTerminalNativeReportsWriterFailure(t *testing.T) {
	sink := &terminalSink{err: errors.New("broken pipe")}
	notifier := NewTerminalNative(TerminalOptions{
		Getenv:   terminalEnv(nil),
		Selected: func() config.TerminalNotifier { return config.TerminalNotifierGhostty },
		Write:    sink.Write,
		Flush:    sink.Flush,
	})
	receipt, err := notifier.Deliver(context.Background(), Message{NotificationID: "ntf-01", Title: "hi"})
	if err == nil || receipt.Delivered {
		t.Fatalf("Deliver() = %+v, %v, want the write failure reported", receipt, err)
	}
	if sink.flushes != 0 {
		t.Errorf("flushed after a failed write")
	}
}

func TestTerminalNativeReportsFlushFailure(t *testing.T) {
	sink := &terminalSink{flushEr: errors.New("device not configured")}
	notifier := NewTerminalNative(TerminalOptions{
		Getenv:   terminalEnv(nil),
		Selected: func() config.TerminalNotifier { return config.TerminalNotifierGhostty },
		Write:    sink.Write,
		Flush:    sink.Flush,
	})
	receipt, err := notifier.Deliver(context.Background(), Message{NotificationID: "ntf-01", Title: "hi"})
	// A sequence that was written but never reached the terminal is not a
	// delivery, and the status must not claim it was one.
	if err == nil || receipt.Delivered {
		t.Fatalf("Deliver() = %+v, %v, want the flush failure reported", receipt, err)
	}
}

// The transport promises no removal, and says so through the sentinel
// Service.Remove already treats as "nothing to do".
func TestTerminalNativeCannotRemove(t *testing.T) {
	notifier, _ := newTerminalNative(t, config.TerminalNotifierGhostty, nil)
	if err := notifier.Remove(context.Background(), "sidecar-group"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("Remove() = %v, want ErrUnsupported", err)
	}
}

func TestNativeWithTerminalRoutesByLocality(t *testing.T) {
	local := &fakeNative{capability: Capability{Available: true, Provider: "native-fake"}}
	terminal, sink := newTerminalNative(t, config.TerminalNotifierGhostty, nil)
	message := Message{NotificationID: "ntf-01", Title: "Agent needs input"}

	localOnly := NewNativeWithTerminal(local, terminal, terminalEnv(nil))
	if got := localOnly.Probe(context.Background()); got.Provider != "native-fake" {
		t.Fatalf("local Probe() = %+v, want the desktop provider", got)
	}
	if _, err := localOnly.Deliver(context.Background(), message); err != nil {
		t.Fatalf("local Deliver() error = %v", err)
	}
	if len(local.delivered) != 1 || len(sink.written()) != 0 {
		t.Fatalf("local delivery used the terminal transport: desktop=%d terminal=%q", len(local.delivered), sink.written())
	}

	remote := NewNativeWithTerminal(local, terminal, terminalEnv(map[string]string{"SSH_TTY": "/dev/ttys004"}))
	if got := remote.Probe(context.Background()); got.Provider != "terminal:ghostty" {
		t.Fatalf("remote Probe() = %+v, want the terminal transport", got)
	}
	if _, err := remote.Deliver(context.Background(), message); err != nil {
		t.Fatalf("remote Deliver() error = %v", err)
	}
	if len(local.delivered) != 1 {
		t.Fatalf("remote delivery reached the desktop provider %d times", len(local.delivered))
	}
	if got := sink.written(); len(got) != 1 {
		t.Fatalf("remote writes = %q, want one sequence", got)
	}
}

func sshEnv(extra map[string]string) func(string) string {
	values := map[string]string{"SSH_CONNECTION": "client 123 host 22"}
	for k, v := range extra {
		values[k] = v
	}
	return terminalEnv(values)
}

// The whole point of the transport: inside SSH it is the one native provider
// that runs, and it never lets a sound or a desktop provider run with it.
func TestServiceDeliversThroughTheTerminalTransportInsideSSH(t *testing.T) {
	now := time.Now().UTC()
	desktop := &fakeNative{capability: Capability{Available: true, Provider: "native-fake"}}
	sound := &fakeSound{capability: Capability{Available: true, Provider: "sound-fake"}}
	terminal, sink := newTerminalNative(t, config.TerminalNotifierGhostty, nil)
	service := NewService(ServiceOptions{
		Native: NewNativeWithTerminal(desktop, terminal, sshEnv(nil)),
		Sound:  sound,
		Ledger: func() (Ledger, error) { return NewMemoryLedger(), nil },
		Config: enabledPolicy, Clock: fixedClock{now: now}, Getenv: sshEnv(nil),
	})

	status := service.Status(context.Background())
	if !status.Remote {
		t.Fatalf("status = %+v, want a remote process", status)
	}
	if !status.Native.Available || status.Native.Provider != "terminal:ghostty" {
		t.Errorf("status.Native = %+v, want the terminal transport", status.Native)
	}
	if status.Sound.Available || status.Sound.Reason != RemoteUnavailableReason {
		t.Errorf("status.Sound = %+v, want the remote refusal", status.Sound)
	}

	n := notify.Notification{
		ID: "ntf-ssh", Source: notify.SourceWaiting, Severity: notify.SeverityWarning,
		Title: "Agent needs input", Body: "sidecar · main", CreatedAt: now,
	}
	result := service.Deliver(context.Background(), Request{Notification: n})
	if !result.Native.Delivered || result.Native.Provider != "terminal:ghostty" {
		t.Fatalf("native result = %+v", result.Native)
	}
	if result.Sound.Attempted || result.Sound.Reason != notify.ReasonUnavailable {
		t.Fatalf("sound result = %+v, want the remote refusal", result.Sound)
	}
	if len(desktop.delivered) != 0 || len(sound.played) != 0 {
		t.Fatalf("a remote host ran a desktop or audio provider: native=%d sound=%d", len(desktop.delivered), len(sound.played))
	}
	want := "\x1b]9;Agent needs input: sidecar · main\x07"
	if got := sink.written(); len(got) != 1 || got[0] != want {
		t.Fatalf("writes = %q, want exactly [%q]", got, want)
	}
}

// Off is the default, and the default must be silent even when everything else
// about the session would allow delivery.
func TestServiceWritesNothingWhenTheTerminalTransportIsOff(t *testing.T) {
	now := time.Now().UTC()
	desktop := &fakeNative{capability: Capability{Available: true, Provider: "native-fake"}}
	sound := &fakeSound{capability: Capability{Available: true, Provider: "sound-fake"}}
	env := sshEnv(map[string]string{"TERM": "xterm-ghostty"})
	terminal, sink := newTerminalNative(t, config.TerminalNotifierOff, map[string]string{"TERM": "xterm-ghostty"})
	service := NewService(ServiceOptions{
		Native: NewNativeWithTerminal(desktop, terminal, env),
		Sound:  sound,
		Ledger: func() (Ledger, error) { return NewMemoryLedger(), nil },
		Config: enabledPolicy, Clock: fixedClock{now: now}, Getenv: env,
	})

	status := service.Status(context.Background())
	if status.Native.Available || !strings.Contains(status.Native.Reason, "notifications.ssh.terminal") {
		t.Errorf("status.Native = %+v, want an honest off answer", status.Native)
	}

	n := notify.Notification{ID: "ntf-off", Source: notify.SourceWaiting, Severity: notify.SeverityWarning, CreatedAt: now}
	result := service.Deliver(context.Background(), Request{Notification: n, ExplicitTest: true})
	if result.Native.Attempted || result.Native.Reason != notify.ReasonUnavailable {
		t.Fatalf("native result = %+v, want an unavailable refusal", result.Native)
	}
	if len(sink.written()) != 0 || len(desktop.delivered) != 0 || len(sound.played) != 0 {
		t.Fatalf("an explicit test wrote something: terminal=%q desktop=%d sound=%d", sink.written(), len(desktop.delivered), len(sound.played))
	}
}

// A terminal that reports unavailable must stay visibly unavailable: no BEL,
// no generic escape, nothing on the wire.
func TestServiceStaysSilentWhenAutoCannotIdentifyTheTerminal(t *testing.T) {
	now := time.Now().UTC()
	env := sshEnv(map[string]string{"TERM": "xterm-256color"})
	terminal, sink := newTerminalNative(t, config.TerminalNotifierAuto, map[string]string{"TERM": "xterm-256color"})
	service := NewService(ServiceOptions{
		Native: NewNativeWithTerminal(nil, terminal, env),
		Ledger: func() (Ledger, error) { return NewMemoryLedger(), nil },
		Config: enabledPolicy, Clock: fixedClock{now: now}, Getenv: env,
	})
	n := notify.Notification{ID: "ntf-auto", Source: notify.SourceWaiting, Severity: notify.SeverityWarning, CreatedAt: now}
	result := service.Deliver(context.Background(), Request{Notification: n, ExplicitTest: true})
	if result.Native.Attempted || result.Native.Reason != notify.ReasonUnavailable {
		t.Fatalf("native result = %+v", result.Native)
	}
	if len(sink.written()) != 0 {
		t.Fatalf("an unidentified terminal received %q", sink.written())
	}
}

// The transport emits only through its injected writer — it never reaches for
// a stream of its own. That is what lets a host choose where the bytes go, and
// it is why structured CLI output on stdout cannot receive them: the CLI's
// writer is standard error, and the TUI's is the renderer.
//
// This asserts the injection property. Which stream the default writer picks
// is a separate claim, proven by TestStderrTerminalWriterRefusesANonTerminal.
func TestTerminalTransportEmitsOnlyThroughItsInjectedWriter(t *testing.T) {
	notifier, sink := newTerminalNative(t, config.TerminalNotifierKitty, nil)
	if _, err := notifier.Deliver(context.Background(), Message{NotificationID: "ntf-01", Title: "hi"}); err != nil {
		t.Fatalf("Deliver() error = %v", err)
	}
	if len(sink.written()) != 1 {
		t.Fatalf("writes = %q, want the injected writer to have received everything", sink.written())
	}
}

// The default writer refuses a standard error that is not a terminal, which is
// what keeps a sequence out of a redirected log file or a pipe. Under `go
// test` stderr is not a terminal, so this exercises the refusal directly.
func TestStderrTerminalWriterRefusesANonTerminal(t *testing.T) {
	if term.IsTerminal(int(os.Stderr.Fd())) {
		t.Skip("standard error is a terminal in this environment")
	}
	n, err := StderrTerminalWriter([]byte("\x1b]9;hi\x07"))
	if err == nil {
		t.Fatal("a non-terminal standard error accepted a notification sequence")
	}
	if n != 0 {
		t.Fatalf("wrote %d bytes to a non-terminal standard error", n)
	}
}

func TestSupportedTerminalsMatchTheConfigVocabulary(t *testing.T) {
	configured := []config.TerminalNotifier{
		config.TerminalNotifierGhostty, config.TerminalNotifierITerm2,
		config.TerminalNotifierWezTerm, config.TerminalNotifierKitty,
	}
	encoders := termnotify.Supported()
	if len(configured) != len(encoders) {
		t.Fatalf("%d configurable terminals, %d encoders", len(configured), len(encoders))
	}
	for i, name := range configured {
		if string(name) != string(encoders[i]) {
			t.Errorf("config %q has no matching encoder (%q)", name, encoders[i])
		}
	}
}
