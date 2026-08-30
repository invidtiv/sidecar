package tty

import (
	"errors"
	"fmt"
	"os/exec"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func remoteModelWithFakeControl(t *testing.T) (*Model, *ControlManager, *fakeControlChannel) {
	t.Helper()
	factory := newFakeControlFactory()
	manager := newControlManager(factory.create, 0)
	t.Cleanup(manager.Stop)
	model := New(nil)
	backend := newRemoteTerminalBackend(model, manager)
	model.control = controlManagerSource{manager: manager}
	model.input = inBandInputSender{backend: backend}
	model.capture = unavailableCaptureSource{}
	model.remoteBackend = backend
	model.remote = true
	_ = model.Enter("remote-session", "%4")
	t.Cleanup(model.Exit)
	var channel *fakeControlChannel
	waitFor(t, func() bool {
		channel = factory.channel("remote-session")
		return channel != nil
	})
	return model, manager, channel
}

// TestNewControlManagerLocalPathUnchanged is the rollback guarantee stated in
// the plan: adding remote support must leave the local path byte-identical.
// The local constructor must still build its own factory, not route through
// the remote one with a local spawner.
func TestNewControlManagerLocalPathUnchanged(t *testing.T) {
	manager := NewControlManager()
	if manager.factory == nil {
		t.Fatal("local manager has no factory")
	}
	if manager.coalesce == 0 {
		t.Error("local manager lost its capture coalesce window")
	}
	// Identity, not behaviour. Asserting only that the factory errors on an
	// empty session would still pass if NewControlManager were rewritten as
	// NewRemoteControlManager(localSpawner) — which is exactly the change this
	// test exists to forbid, since it would route the local path through the
	// remote one. Comparing the function pointer is the only assertion that
	// actually distinguishes them.
	want := reflect.ValueOf(newProcessControlChannel).Pointer()
	if got := reflect.ValueOf(manager.factory).Pointer(); got != want {
		t.Error("the local manager no longer uses newProcessControlChannel directly; the local path must not route through the remote factory")
	}
	if _, err := manager.factory(""); err == nil {
		t.Error("local factory accepted an empty session")
	}
}

func TestNewRemoteControlManagerUsesTheSpawner(t *testing.T) {
	var asked string
	manager := NewRemoteControlManager(func(session string) *exec.Cmd {
		asked = session
		// `false` exits immediately, so the channel fails to attach without
		// ever contacting a tmux server — which is the point: this test must
		// not touch any tmux, local or remote.
		return exec.Command("false")
	})
	if _, err := manager.factory("proj-claude"); err == nil {
		t.Error("a spawner producing a dead process still yielded a channel")
	}
	if asked != "proj-claude" {
		t.Errorf("spawner asked for %q, want proj-claude", asked)
	}
	if manager.coalesce != NewControlManager().coalesce {
		t.Error("remote manager uses a different coalesce window than local; that is a decision, not an accident — update the comment if intended")
	}
	// The converse of the local test: the remote manager must NOT be the local
	// factory.
	if reflect.ValueOf(manager.factory).Pointer() == reflect.ValueOf(newProcessControlChannel).Pointer() {
		t.Error("remote manager is using the local factory; the spawner would be ignored")
	}
}

func TestRemoteFactoryRejectsMissingSpawner(t *testing.T) {
	if _, err := spawnedControlChannelFactory(nil)("s"); err == nil {
		t.Error("nil spawner accepted")
	}
	got, err := spawnedControlChannelFactory(func(string) *exec.Cmd { return nil })("s")
	if err == nil {
		t.Error("spawner returning nil command accepted")
	}
	if got != nil {
		t.Error("failed factory returned a channel")
	}
}

// TestInBandSendLiteralIsAlwaysHex. In-band there is no argv: the whole line
// goes through tmux's command parser, where semicolons, quotes, backslashes
// and newlines are all live. Hex-encoding every literal avoids re-deriving
// which characters are safe on a control line.
func TestInBandSendLiteralIsAlwaysHex(t *testing.T) {
	for _, text := range []string{
		"plain", "with;semicolon", `with"quote`, "with'quote", `with\backslash`,
		"with $var and `cmd`", "with\ttab",
	} {
		command := InBandSendLiteral("%3", text)
		if !strings.Contains(command, "-H") {
			t.Errorf("%q was not hex-encoded: %s", text, command)
		}
		hex := strings.TrimSpace(strings.SplitN(command, "-H", 2)[1])
		if got := len(strings.Fields(hex)); got != len(text) {
			t.Errorf("%q encoded to %d hex bytes, want %d: %s", text, got, len(text), command)
		}
		// A control command must never contain a newline: the transport
		// rejects multiline commands, and a smuggled newline would be a
		// second command with a callback nobody registered.
		if strings.ContainsAny(command, "\r\n") {
			t.Errorf("%q produced a multiline command: %q", text, command)
		}
	}
}

func TestInBandSendLiteralEncodesMultibyteByByte(t *testing.T) {
	// "✳" is three bytes. send-keys -H takes bytes, not runes.
	command := InBandSendLiteral("%3", "✳")
	hex := strings.Fields(strings.TrimSpace(strings.SplitN(command, "-H", 2)[1]))
	if len(hex) != 3 {
		t.Fatalf("multibyte rune encoded as %d bytes: %v", len(hex), hex)
	}
	if hex[0] != "e2" || hex[1] != "9c" || hex[2] != "b3" {
		t.Errorf("wrong UTF-8 bytes: %v", hex)
	}
}

func TestInBandSendKeysSplitsLiteralFromNamed(t *testing.T) {
	commands := InBandSendKeys("%3",
		KeySpec{Value: "hello", Literal: true},
		KeySpec{Value: "Enter"},
	)
	if len(commands) != 2 {
		t.Fatalf("commands = %d, want 2", len(commands))
	}
	if !strings.Contains(commands[0], "-H") {
		t.Errorf("literal key was not hex-encoded: %s", commands[0])
	}
	if strings.Contains(commands[1], "-H") {
		t.Errorf("named key Enter was hex-encoded, which sends the letters: %s", commands[1])
	}
	if !strings.HasSuffix(commands[1], "Enter") {
		t.Errorf("named key lost its name: %s", commands[1])
	}
	for _, command := range commands {
		if strings.ContainsAny(command, "\r\n") {
			t.Errorf("multiline command: %q", command)
		}
	}
}

// TestInBandSendKeysPreservesOrder is the FIFO property the local send queue
// exists to guarantee (td-8fcd2e). The commands are written to one pipe in one
// write, so their order here is the order tmux executes them in.
func TestInBandSendKeysPreservesOrder(t *testing.T) {
	keys := []KeySpec{
		{Value: "a", Literal: true}, {Value: "b", Literal: true}, {Value: "c", Literal: true},
	}
	commands := InBandSendKeys("%1", keys...)
	for i, want := range []string{"61", "62", "63"} {
		if !strings.HasSuffix(commands[i], want) {
			t.Errorf("command %d = %q, want it to end in %s", i, commands[i], want)
		}
	}
}

func TestRemoteInputUsesTheOpenControlPipeAndSendQueue(t *testing.T) {
	model, _, channel := remoteModelWithFakeControl(t)
	model.remoteInteractive = true
	cmd := model.input.SendKeys(model.Scope(), "%4",
		KeySpec{Value: "a", Literal: true},
		KeySpec{Value: "b", Literal: true},
		KeySpec{Value: "Enter"},
	)
	if cmd == nil {
		t.Fatal("interactive remote input returned no command")
	}
	_ = cmd()

	channel.mu.Lock()
	var sends []string
	for _, command := range channel.commands {
		if strings.HasPrefix(command.text, "send-keys -t %4") {
			sends = append(sends, command.text)
		}
	}
	channel.mu.Unlock()
	if len(sends) != 3 {
		t.Fatalf("in-band sends = %v, want three commands on the existing channel", sends)
	}
	if got := model.remoteBackend.input.Load(); got != 1 {
		t.Fatalf("successful local sends recorded input mark %d times, want 1", got)
	}
	for i, suffix := range []string{"61", "62", "Enter"} {
		if !strings.HasSuffix(sends[i], suffix) {
			t.Fatalf("send %d = %q, want suffix %q", i, sends[i], suffix)
		}
	}
}

func TestRemoteCaptureRangeIsInBandAndAbsolute(t *testing.T) {
	model, _, channel := remoteModelWithFakeControl(t)
	type result struct {
		capture CaptureRange
		err     error
	}
	done := make(chan result, 1)
	go func() {
		capture, err := model.CaptureRange(-50, -1)
		done <- result{capture: capture, err: err}
	}()

	var metadata, capture fakeControlCommand
	waitFor(t, func() bool {
		channel.mu.Lock()
		defer channel.mu.Unlock()
		for i, command := range channel.commands {
			if strings.Contains(command.text, "capture-pane") && strings.Contains(command.text, "-S -50") {
				if i == 0 {
					return false
				}
				metadata, capture = channel.commands[i-1], command
				return true
			}
		}
		return false
	})
	metadata.callback(controlResponse{Lines: []string{"120"}})
	capture.callback(controlResponse{Lines: []string{"old-a", "old-b"}})
	select {
	case got := <-done:
		if got.err != nil {
			t.Fatal(got.err)
		}
		if got.capture.HistorySize != 120 || got.capture.StartLine != 70 || got.capture.Output != "old-a\nold-b" {
			t.Fatalf("capture = %+v", got.capture)
		}
	case <-time.After(time.Second):
		t.Fatal("in-band range capture did not complete")
	}
}

func TestRemoteGeometryClaimReleasesOnBlurAndReclaimsOnFocus(t *testing.T) {
	store := &fakeLeaseStore{}
	model := New(nil)
	model.remote = true
	model.State = &State{Active: true, TargetSession: "remote-session", TargetPane: "%4"}
	model.remoteBackend = &remoteTerminalBackend{model: model}
	model.remoteBackend.lease = newTestKeeper(store, "viewer-1", DefaultLeasePolicy)

	if cmd := model.ActivateInput(); cmd == nil {
		t.Fatal("remote interactive entry did not claim")
	} else {
		_ = cmd()
	}
	if store.sets != 1 || store.current() == "" {
		t.Fatalf("entry lease sets=%d token=%q", store.sets, store.current())
	}
	if cmd := model.SetApplicationFocused(false); cmd == nil {
		t.Fatal("blur did not schedule a release")
	} else {
		_ = cmd()
	}
	if store.clears != 1 || store.current() != "" {
		t.Fatalf("blur lease clears=%d token=%q", store.clears, store.current())
	}
	if cmd := model.SetApplicationFocused(true); cmd == nil {
		t.Fatal("focus did not schedule a reclaim")
	} else {
		_ = cmd()
	}
	if store.sets != 2 || store.current() == "" {
		t.Fatalf("focus lease sets=%d token=%q", store.sets, store.current())
	}
	model.ReleaseInput()
}

func TestQueuedRemoteActivationCannotReclaimAfterInputRelease(t *testing.T) {
	store := &fakeLeaseStore{}
	model := New(nil)
	model.remote = true
	model.State = &State{Active: true, TargetSession: "remote-session", TargetPane: "%4"}
	model.remoteBackend = &remoteTerminalBackend{model: model}
	model.remoteBackend.lease = newTestKeeper(store, "viewer", DefaultLeasePolicy)

	cmd := model.ActivateInput()
	if cmd == nil {
		t.Fatal("remote activation returned no command")
	}
	model.ReleaseInput()
	_ = cmd()
	if store.reads != 0 || store.sets != 0 || store.current() != "" {
		t.Fatalf("late activation reclaimed lease: reads=%d sets=%d token=%q", store.reads, store.sets, store.current())
	}
}

func TestReleaseInputReturnsWhileRemoteActivationReadIsBlocked(t *testing.T) {
	model, _, channel := remoteModelWithFakeControl(t)
	model.Width, model.Height = 80, 24
	activate := model.ActivateInput()
	activated := make(chan struct{})
	go func() {
		_ = activate()
		close(activated)
	}()

	leaseRead := waitForRemoteLeaseRead(t, channel, 0)
	released := make(chan struct{})
	go func() {
		model.ReleaseInput()
		close(released)
	}()
	select {
	case <-released:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("ReleaseInput waited on the blocked remote activation read")
	}
	select {
	case <-activated:
		t.Fatal("activation unexpectedly completed without its remote response")
	default:
	}

	leaseRead.callback(controlResponse{Lines: []string{"remote-session\t"}})
	select {
	case <-activated:
	case <-time.After(time.Second):
		t.Fatal("activation did not drain after its remote response")
	}

	var unset fakeControlCommand
	waitFor(t, func() bool {
		channel.mu.Lock()
		defer channel.mu.Unlock()
		for _, command := range channel.commands {
			if strings.Contains(command.text, "set-option -u") && strings.Contains(command.text, leaseOptionName) {
				unset = command
				return true
			}
		}
		return false
	})
	channel.mu.Lock()
	for _, command := range channel.commands {
		if strings.Contains(command.text, "#{pane_width},#{pane_height}") ||
			strings.Contains(command.text, "resize-window") || strings.Contains(command.text, "resize-pane") {
			channel.mu.Unlock()
			t.Fatalf("invalidated activation issued late geometry command %q", command.text)
		}
	}
	channel.mu.Unlock()
	// Complete the retained clear so this test leaves no lifetime timer behind.
	unset.callback(controlResponse{})
}

func TestReleaseAndExitReturnWhileRemoteLeaseRefreshIsBlocked(t *testing.T) {
	model, _, channel := remoteModelWithFakeControl(t)
	ticker := &manualTicker{}
	ticker.install(model.remoteBackend.lease)
	activate := model.ActivateInput()
	activated := make(chan struct{})
	go func() {
		_ = activate()
		close(activated)
	}()
	initialRead := waitForRemoteLeaseRead(t, channel, 0)
	initialRead.callback(controlResponse{Lines: []string{"remote-session\t"}})
	select {
	case <-activated:
	case <-time.After(time.Second):
		t.Fatal("remote activation did not complete")
	}

	ticker.c <- time.Now()
	refreshRead := waitForRemoteLeaseRead(t, channel, 1)
	released := make(chan struct{})
	go func() {
		model.ReleaseInput()
		close(released)
	}()
	select {
	case <-released:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("ReleaseInput joined the network-blocked lease refresher")
	}
	exited := make(chan struct{})
	go func() {
		model.Exit()
		close(exited)
	}()
	select {
	case <-exited:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("retarget/Exit waited on the network-blocked lease refresher")
	}
	if channel.closeCount() != 0 {
		t.Fatal("last control subscription closed before the pending release could clear its lease")
	}

	refreshRead.callback(controlResponse{Lines: []string{"remote-session\t"}})
	var unset fakeControlCommand
	waitFor(t, func() bool {
		channel.mu.Lock()
		defer channel.mu.Unlock()
		for _, command := range channel.commands {
			if strings.Contains(command.text, "set-option -u") && strings.Contains(command.text, leaseOptionName) {
				unset = command
				return true
			}
		}
		return false
	})
	if channel.closeCount() != 0 {
		t.Fatal("control closed before the eventual lease unset response")
	}
	channel.events <- controlEvent{Kind: controlEventResponse, Callback: unset.callback}
	waitFor(t, func() bool { return channel.closeCount() == 1 })
}

func waitForRemoteLeaseRead(t *testing.T, channel *fakeControlChannel, index int) fakeControlCommand {
	t.Helper()
	var found fakeControlCommand
	waitFor(t, func() bool {
		channel.mu.Lock()
		defer channel.mu.Unlock()
		seen := 0
		for _, command := range channel.commands {
			if !strings.Contains(command.text, "#{session_name}") || !strings.Contains(command.text, leaseOptionName) {
				continue
			}
			if seen == index {
				found = command
				return true
			}
			seen++
		}
		return false
	})
	return found
}

func TestRemoteInteractivePeriodicLeaseRefreshLetsTypingPeerPreempt(t *testing.T) {
	store := &fakeLeaseStore{}
	var clock sync.Mutex
	now := time.Now()
	readNow := func() time.Time {
		clock.Lock()
		defer clock.Unlock()
		return now
	}
	advance := func(d time.Duration) {
		clock.Lock()
		now = now.Add(d)
		clock.Unlock()
	}

	model := New(nil)
	model.remote = true
	model.State = &State{Active: true, TargetSession: "remote-session", TargetPane: "%4"}
	model.remoteBackend = &remoteTerminalBackend{model: model}
	viewer := newLeaseKeeper(store, DefaultLeasePolicy, time.Second)
	viewer.selfID = "viewer"
	viewer.now = readNow
	viewer.lastInput = readNow()
	ticker := &manualTicker{}
	ticker.install(viewer)
	model.remoteBackend.lease = viewer

	peer := newLeaseKeeper(store, DefaultLeasePolicy, time.Second)
	peer.selfID = "remote-human"
	peer.now = readNow
	peer.lastInput = readNow()

	cmd := model.ActivateInput()
	if cmd == nil {
		t.Fatal("remote activation returned no command")
	}
	_ = cmd()
	defer model.ReleaseInput()
	if leaseOwner(store.current()) != "viewer" {
		t.Fatalf("entry lease = %q, want viewer", store.current())
	}

	advance(10 * time.Second)
	for range max(DefaultLeasePolicy.RefreshTicks, 1) {
		before := store.activity()
		ticker.c <- readNow()
		waitForActivity(t, store, before)
		advance(time.Second)
	}
	waitForSets(t, store, 2)
	parsed := parseLeaseToken(store.current())
	if !parsed.idleKnown || parsed.idle < 10*time.Second || store.sets < 2 {
		t.Fatalf("periodic refresh token=%q idle=%s known=%v sets=%d", store.current(), parsed.idle, parsed.idleKnown, store.sets)
	}

	peer.noteInput()
	if !peer.allow("%4") || leaseOwner(store.current()) != "remote-human" {
		t.Fatalf("typing peer did not preempt periodically refreshed idle owner: %q", store.current())
	}
}

func TestRemoteExitRetainsControlUntilLeaseUnsetCompletes(t *testing.T) {
	model, _, channel := remoteModelWithFakeControl(t)
	activate := model.ActivateInput()
	activated := make(chan struct{})
	go func() {
		_ = activate()
		close(activated)
	}()

	var leaseRead fakeControlCommand
	waitFor(t, func() bool {
		channel.mu.Lock()
		defer channel.mu.Unlock()
		for _, command := range channel.commands {
			if strings.Contains(command.text, "#{session_name}") && strings.Contains(command.text, leaseOptionName) {
				leaseRead = command
				return true
			}
		}
		return false
	})
	leaseRead.callback(controlResponse{Lines: []string{"remote-session\t"}})
	select {
	case <-activated:
	case <-time.After(time.Second):
		t.Fatal("remote activation did not complete")
	}

	model.Exit()
	var unset fakeControlCommand
	waitFor(t, func() bool {
		channel.mu.Lock()
		defer channel.mu.Unlock()
		for _, command := range channel.commands {
			if strings.Contains(command.text, "set-option -u") && strings.Contains(command.text, leaseOptionName) {
				unset = command
				return true
			}
		}
		return false
	})
	if channel.closeCount() != 0 {
		t.Fatal("control closed before tmux confirmed the lease unset")
	}
	// Exit has already returned on this goroutine: response waiting belongs to
	// the control manager, not Bubble Tea. Deliver the response through the real
	// ordered actor and prove it is the lifetime barrier for the transport.
	channel.events <- controlEvent{
		Kind:     controlEventResponse,
		Response: controlResponse{},
		Callback: unset.callback,
	}
	waitFor(t, func() bool { return channel.closeCount() == 1 })
}

func TestControlQuoteHandlesEveryQuotingShape(t *testing.T) {
	cases := map[string]string{
		"%3":          "%3", // plain word, left alone
		"proj-claude": "proj-claude",
		"with space":  "'with space'",
		"semi;colon":  "'semi;colon'",
		"it's":        `"it's"`, // cannot single-quote; tmux has no escape inside ''
	}
	for input, want := range cases {
		if got := controlQuote(input); got != want {
			t.Errorf("controlQuote(%q) = %q, want %q", input, got, want)
		}
	}
	if got := controlQuote(""); got != "''" {
		t.Errorf("empty string quoted as %q", got)
	}
}

// TestControlQuoteNeutralisesExpansionInDoubleQuotes. tmux expands $, ` and
// escapes inside double quotes, so a value that had to be double-quoted (it
// contained a single quote) must have those neutralised or a pane name could
// execute something.
func TestControlQuoteNeutralisesExpansionInDoubleQuotes(t *testing.T) {
	got := controlQuote("it's $HOME `id`")
	if !strings.HasPrefix(got, `"`) {
		t.Fatalf("expected double quoting, got %q", got)
	}
	if strings.Contains(got, "$HOME") && !strings.Contains(got, `\$HOME`) {
		t.Errorf("$ not escaped: %q", got)
	}
	if strings.Contains(got, "`id`") && !strings.Contains(got, "\\`id\\`") {
		t.Errorf("backtick not escaped: %q", got)
	}
}

// TestUseLocalControlRestoresTheLocalPath is the fix for a real defect, not a
// symmetry exercise.
//
// The preview reuses ONE tty.Model across row selections. Before this existed,
// a Model that had shown a remote pane stayed remote forever: the next LOCAL
// row was opened by `ssh <host> tmux -C attach-session -t <local session>`,
// which often SUCCEEDS because both machines derive session names the same
// way — painting the other machine's pane into a local workspace's preview,
// offering interactive mode that swallowed every keystroke, and never resizing
// the pane again.
func TestUseLocalControlRestoresTheLocalPath(t *testing.T) {
	model := New(nil)
	localControl, localInput, localCapture := model.control, model.input, model.capture

	model.UseRemoteControl(func(string) *exec.Cmd { return exec.Command("false") })
	if !model.IsRemote() {
		t.Fatal("UseRemoteControl did not mark the model remote")
	}
	if _, inBand := model.input.(inBandInputSender); !inBand {
		t.Error("remote model does not have its host-aware in-band sender")
	}
	if _, unavailable := model.capture.(unavailableCaptureSource); !unavailable {
		t.Error("remote model still has a local capture source")
	}

	model.UseLocalControl()
	if model.IsRemote() {
		t.Fatal("UseLocalControl left the model remote")
	}
	if _, stillRemote := model.input.(inBandInputSender); stillRemote {
		t.Error("input sender was not restored; a local pane would write through the remote host")
	}
	if _, stillUnavailable := model.capture.(unavailableCaptureSource); stillUnavailable {
		t.Error("capture source was not restored; a local pane would lose its fallback")
	}
	if fmt.Sprintf("%T", model.control) != fmt.Sprintf("%T", localControl) {
		t.Errorf("control source = %T, want %T", model.control, localControl)
	}
	if fmt.Sprintf("%T", model.input) != fmt.Sprintf("%T", localInput) {
		t.Errorf("input sender = %T, want %T", model.input, localInput)
	}
	if fmt.Sprintf("%T", model.capture) != fmt.Sprintf("%T", localCapture) {
		t.Errorf("capture source = %T, want %T", model.capture, localCapture)
	}
}

// A watched remote model has no geometry claim and must not move the window.
func TestRemoteModelNeverResizesBeforeInteractiveClaim(t *testing.T) {
	model := New(nil)
	model.UseRemoteControl(func(string) *exec.Cmd { return exec.Command("false") })
	if cmd := model.assertDimensions(); cmd != nil {
		t.Error("a remote model produced a resize command")
	}
}

// Opening a remote pane is a viewer operation. Pane IDs are server-local, so
// even one ambient query/resize here can mutate an unrelated local %N.
func TestRemoteOpenWithDimensionsNeverTouchesLocalGeometry(t *testing.T) {
	model := New(nil)
	model.UseRemoteControl(func(string) *exec.Cmd { return exec.Command("false") })
	model.Width, model.Height = 120, 40

	originalQuery, originalResize := terminalQueryPaneSize, terminalResizePane
	defer func() { terminalQueryPaneSize, terminalResizePane = originalQuery, originalResize }()
	queries, resizes := 0, 0
	terminalQueryPaneSize = func(string) (int, int, bool) { queries++; return 0, 0, false }
	terminalResizePane = func(string, int, int) { resizes++ }

	_ = model.Open(Target{Session: "remote-session", Pane: "%7", Host: "other-host"})
	model.Exit()
	if queries != 0 || resizes != 0 {
		t.Fatalf("remote Open touched local geometry: queries=%d resizes=%d", queries, resizes)
	}
}

// TestRemoteCaptureFailsRatherThanReadingLocalTmux is the subtlest of the
// three read-only seams. Pane IDs are per-server, so a local capture of a
// remote pane %4 does not fail — it succeeds against an unrelated local pane.
func TestRemoteCaptureFailsRatherThanReadingLocalTmux(t *testing.T) {
	model := New(nil)
	model.UseRemoteControl(func(string) *exec.Cmd { return exec.Command("false") })
	text, _, err := model.capture.Capture("%4", 80)
	if err == nil {
		t.Fatal("a remote model captured something from local tmux")
	}
	if !errors.Is(err, ErrRemoteCaptureUnavailable) {
		t.Errorf("err = %v, want ErrRemoteCaptureUnavailable", err)
	}
	if text != "" {
		t.Errorf("content returned for a remote pane: %q", text)
	}
}
