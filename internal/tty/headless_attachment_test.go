package tty

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/testenv"
	"github.com/marcus/sidecar/internal/tmuxenv"
)

// Capture teardown must clear a session-scoped lease even when the selected
// pane vanished or the capture pipe is dead. Another viewer's token survives.
func TestHeadlessReleaseAfterCaptureTargetOrTransportLoss(t *testing.T) {
	testenv.RequireTmux(t)
	root, err := os.MkdirTemp("/tmp", "sidecar-release-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_PANE", "")
	t.Setenv("TMUX_TMPDIR", root)
	socket := tmuxenv.SocketPath()
	if err := os.MkdirAll(filepath.Dir(socket), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = exec.Command("tmux", "-S", socket, "kill-server").Run()
		_ = os.RemoveAll(root)
	})
	run := func(t *testing.T, args ...string) string {
		t.Helper()
		out, err := exec.Command("tmux", append([]string{"-S", socket}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("tmux %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run(t, "new-session", "-d", "-s", "release-test", "-x", "80", "-y", "24", "sleep 120")
	for _, loss := range []string{"pane", "transport", "foreign token", "incarnation changed"} {
		t.Run(loss, func(t *testing.T) {
			pane := run(t, "new-window", "-d", "-P", "-F", "#{pane_id}", "-t", "release-test", "sleep 120")
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			identity, err := InspectHeadlessPane(ctx, pane)
			if err != nil {
				t.Fatal(err)
			}
			manager := newControlManager(func(session string) (controlChannel, error) {
				return newProcessControlChannelForSocket(socket, session)
			}, 5*time.Millisecond)
			defer manager.Stop()
			sub, err := manager.Subscribe(ControlRequest{Session: identity.Session, Pane: pane, Visible: true})
			if err != nil {
				t.Fatal(err)
			}
			defer sub.Close()
			waitFor(t, sub.UsingControl)
			g, err := NewHeadlessGeometry(manager, identity, "aerie-mobile-abcdef-123")
			if err != nil {
				t.Fatal(err)
			}
			g.token = "aerie-mobile-abcdef-123:1:0"
			run(t, "set-option", "-t", "release-test", leaseOptionName, g.token)
			if loss == "pane" {
				run(t, "kill-pane", "-t", pane)
			} else {
				manager.Stop()
			}
			if loss == "foreign token" {
				run(t, "set-option", "-t", "release-test", leaseOptionName, "another-viewer:2:0")
			}
			if loss == "incarnation changed" {
				g.expected.ServerPID++
			}
			if err := g.Release(); err != nil {
				t.Fatalf("release after %s loss: %v", loss, err)
			}
			owner := run(t, "show-options", "-qv", "-t", "release-test", leaseOptionName)
			want := ""
			if loss == "foreign token" {
				want = "another-viewer:2:0"
			}
			if loss == "incarnation changed" {
				want = "aerie-mobile-abcdef-123:1:0"
			}
			if owner != want {
				t.Fatalf("owner after %s loss = %q, want %q", loss, owner, want)
			}
		})
	}
}

func headlessGeometryHarness(t *testing.T) (*HeadlessGeometry, *fakeControlChannel) {
	t.Helper()
	factory := newFakeControlFactory()
	manager := newControlManager(factory.create, time.Hour)
	t.Cleanup(manager.Stop)
	sub, err := manager.Subscribe(ControlRequest{Session: "mobile", Pane: "%7", Visible: true, Focused: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sub.Close)
	var channel *fakeControlChannel
	waitFor(t, func() bool {
		channel = factory.channel("mobile")
		return channel != nil
	})
	geometry, err := NewHeadlessGeometry(manager, HeadlessTargetIdentity{
		ServerPID: 42, SessionID: "$3", SessionCreated: "1700000000", Session: "mobile", Pane: "%7", Width: 80, Height: 24,
	}, "aerie-mobile-abcdef-123")
	if err != nil {
		t.Fatal(err)
	}
	return geometry, channel
}

func respondHeadless(command fakeControlCommand, lines []string, err error) {
	command.callback(controlResponse{Lines: lines, Err: err})
}

func respondHeadlessCommand(channel *fakeControlChannel, text string, response controlResponse) {
	channel.mu.Lock()
	var callbacks []func(controlResponse)
	for _, command := range channel.commands {
		if command.text == text {
			callbacks = append(callbacks, command.callback)
		}
	}
	channel.mu.Unlock()
	for i, callback := range callbacks {
		got := controlResponse{}
		if i == len(callbacks)-1 {
			got = response
		}
		callback(got)
	}
}

func TestHeadlessClaimRefusesFailedOwnerRead(t *testing.T) {
	geometry, channel := headlessGeometryHarness(t)
	done := make(chan error, 1)
	go func() { done <- geometry.ClaimResize(80, 24) }()
	read := waitForControlCommand(t, channel, "#{session_created}", 0)
	respondHeadless(read, nil, errors.New("read failed"))
	if err := <-done; err == nil || !strings.Contains(err.Error(), "strict owner read failed") {
		t.Fatalf("ClaimResize error = %v", err)
	}
	if got := countControlCommands(channel, "if-shell"); got != 0 {
		t.Fatalf("failed read issued %d conditional writes", got)
	}
}

func TestHeadlessClaimRefusesFailedOrForeignOwnerWrite(t *testing.T) {
	for _, tc := range []struct {
		name     string
		response controlResponse
	}{
		{name: "write failure", response: controlResponse{Err: errors.New("write failed")}},
		{name: "owner changed", response: controlResponse{Lines: []string{headlessOwnerMismatch}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			geometry, channel := headlessGeometryHarness(t)
			done := make(chan error, 1)
			go func() { done <- geometry.ClaimResize(80, 24) }()
			read := waitForControlCommand(t, channel, "#{session_created}", 0)
			respondHeadless(read, []string{"42\t$3\t1700000000\tmobile\t%7\tother:1:0"}, nil)
			write := waitForControlCommand(t, channel, "if-shell", 0)
			respondHeadlessCommand(channel, write.text, tc.response)
			cleanup := waitForControlCommand(t, channel, "set-option -u", 0)
			respondHeadlessCommand(channel, cleanup.text, controlResponse{Lines: []string{headlessOwnerMismatch}})
			if err := <-done; err == nil {
				t.Fatal("ClaimResize accepted an unacknowledged owner write")
			}
			if geometry.token != "" {
				t.Fatalf("failed claim retained token %q", geometry.token)
			}
		})
	}
}

func TestHeadlessMultiPaneClaimResizesAndVerifiesTheSelectedPane(t *testing.T) {
	geometry, channel := headlessGeometryHarness(t)
	geometry.expected.PaneCount = 2
	done := make(chan error, 1)
	go func() { done <- geometry.ClaimResize(46, 23) }()
	owner := waitForControlCommand(t, channel, "#{session_created}", 0)
	respondHeadless(owner, []string{"42\t$3\t1700000000\tmobile\t%7\t"}, nil)
	layout := waitForControlCommand(t, channel, "#{window_id}\\011#{window_width}", 0)
	respondHeadless(layout, []string{"42\t$3\t1700000000\tmobile\t%7\t@1\t80\t24\t39\t24\t2"}, nil)
	claim := waitForControlCommand(t, channel, "if-shell", 0)
	if !strings.Contains(claim.text, "resize-window -t %7 -x 87 -y 23") || !strings.Contains(claim.text, "resize-pane -t %7 -x 46 -y 23") {
		t.Fatalf("multi-pane claim did not preserve sibling extent around selected geometry: %q", claim.text)
	}
	if !strings.Contains(claim.text, "#{window_id}|#{window_width}|#{window_height}|#{pane_width}|#{pane_height}|#{window_panes},@1|80|24|39|24|2") {
		t.Fatalf("multi-pane claim did not bind the observed layout: %q", claim.text)
	}
	respondHeadlessCommand(channel, claim.text, controlResponse{Lines: []string{headlessOwnerSuccess}})
	verified := waitForControlCommand(t, channel, "#{window_id}\\011#{window_width}", 1)
	respondHeadless(verified, []string{"42\t$3\t1700000000\tmobile\t%7\t@1\t87\t23\t46\t23\t2"}, nil)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if geometry.token == "" {
		t.Fatal("verified multi-pane claim did not retain ownership")
	}
}

func TestHeadlessMultiPaneClaimRefusesUnacceptedPaneGeometry(t *testing.T) {
	geometry, channel := headlessGeometryHarness(t)
	geometry.expected.PaneCount = 2
	done := make(chan error, 1)
	go func() { done <- geometry.ClaimResize(46, 23) }()
	owner := waitForControlCommand(t, channel, "#{session_created}", 0)
	respondHeadless(owner, []string{"42\t$3\t1700000000\tmobile\t%7\t"}, nil)
	layout := waitForControlCommand(t, channel, "#{window_id}\\011#{window_width}", 0)
	respondHeadless(layout, []string{"42\t$3\t1700000000\tmobile\t%7\t@1\t80\t24\t39\t24\t2"}, nil)
	claim := waitForControlCommand(t, channel, "if-shell", 0)
	respondHeadlessCommand(channel, claim.text, controlResponse{Lines: []string{headlessOwnerSuccess}})
	verified := waitForControlCommand(t, channel, "#{window_id}\\011#{window_width}", 1)
	respondHeadless(verified, []string{"42\t$3\t1700000000\tmobile\t%7\t@1\t87\t23\t45\t23\t2"}, nil)
	cleanup := waitForControlCommand(t, channel, "set-option -u", 0)
	respondHeadlessCommand(channel, cleanup.text, controlResponse{Lines: []string{headlessOwnerSuccess}})
	if err := <-done; err == nil || !strings.Contains(err.Error(), "selected pane accepted 45x23") {
		t.Fatalf("unaccepted geometry error = %v", err)
	}
	if geometry.token != "" {
		t.Fatalf("unaccepted geometry retained token %q", geometry.token)
	}
}

func TestHeadlessMultiPaneClaimRefusesLayoutChangeAtMutation(t *testing.T) {
	geometry, channel := headlessGeometryHarness(t)
	geometry.expected.PaneCount = 2
	done := make(chan error, 1)
	go func() { done <- geometry.ClaimResize(46, 23) }()
	owner := waitForControlCommand(t, channel, "#{session_created}", 0)
	respondHeadless(owner, []string{"42\t$3\t1700000000\tmobile\t%7\t"}, nil)
	layout := waitForControlCommand(t, channel, "#{window_id}\\011#{window_width}", 0)
	respondHeadless(layout, []string{"42\t$3\t1700000000\tmobile\t%7\t@1\t80\t24\t39\t24\t2"}, nil)
	claim := waitForControlCommand(t, channel, "if-shell", 0)
	if !strings.Contains(claim.text, "@1|80|24|39|24|2") {
		t.Fatalf("claim omitted observed layout guard: %q", claim.text)
	}
	// An external client changes the layout after the read but before this
	// conditional mutation. tmux evaluates the guard against the new values and
	// takes the refusal branch without running either resize command.
	respondHeadlessCommand(channel, claim.text, controlResponse{Lines: []string{headlessOwnerMismatch}})
	cleanup := waitForControlCommand(t, channel, "set-option -u", 0)
	respondHeadlessCommand(channel, cleanup.text, controlResponse{Lines: []string{headlessOwnerMismatch}})
	if err := <-done; err == nil || !strings.Contains(err.Error(), "target layout changed") {
		t.Fatalf("layout race error = %v", err)
	}
	if geometry.token != "" {
		t.Fatalf("layout race retained token %q", geometry.token)
	}
}

func TestHeadlessReleaseConditionallyClearsOnlyItsExactToken(t *testing.T) {
	geometry, channel := headlessGeometryHarness(t)
	claimed := make(chan error, 1)
	go func() { claimed <- geometry.ClaimResize(80, 24) }()
	read := waitForControlCommand(t, channel, "#{session_created}", 0)
	respondHeadless(read, []string{"42\t$3\t1700000000\tmobile\t%7\t"}, nil)
	claim := waitForControlCommand(t, channel, "if-shell", 0)
	respondHeadlessCommand(channel, claim.text, controlResponse{Lines: []string{headlessOwnerSuccess}})
	if err := <-claimed; err != nil {
		t.Fatal(err)
	}
	token := geometry.token
	released := make(chan error, 1)
	go func() { released <- geometry.Release() }()
	release := waitForControlCommand(t, channel, "set-option -u", 0)
	if !strings.Contains(release.text, "#{==:#{@sidecar-owner},"+token+"}") ||
		!strings.Contains(release.text, "set-option -u") {
		t.Fatalf("release is not exact-owner conditional: %q", release.text)
	}
	// The false branch models another viewer taking ownership after the mobile
	// attachment's last successful operation. Release must complete without an
	// unconditional second clear.
	respondHeadlessCommand(channel, release.text, controlResponse{Lines: []string{headlessOwnerMismatch}})
	if err := <-released; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(release.text, "set-option -u") {
		t.Fatalf("release omitted conditional clear: %q", release.text)
	}
}

func TestHeadlessInputRequiresAcknowledgedCurrentOwner(t *testing.T) {
	geometry, channel := headlessGeometryHarness(t)
	geometry.token = "aerie-mobile-abcdef-123:1:0"
	done := make(chan error, 1)
	go func() { done <- geometry.SendLiteral([]byte("echo proof\r")) }()
	input := waitForControlCommand(t, channel, "send-keys", 0)
	if !strings.Contains(input.text, "#{==:#{@sidecar-owner},aerie-mobile-abcdef-123:1:0}") {
		t.Fatalf("input lacks current-owner condition: %q", input.text)
	}
	respondHeadlessCommand(channel, input.text, controlResponse{Err: errors.New("tmux rejected input")})
	cleanup := waitForControlCommand(t, channel, "set-option -u", 0)
	respondHeadlessCommand(channel, cleanup.text, controlResponse{Lines: []string{headlessOwnerMismatch}})
	if err := <-done; err == nil {
		t.Fatal("SendLiteral accepted a failed tmux command")
	}
	if geometry.token != "" {
		t.Fatalf("failed input retained token %q", geometry.token)
	}
}

func TestHeadlessLateHeartbeatExpiresAndCannotRestoreControl(t *testing.T) {
	geometry, channel := headlessGeometryHarness(t)
	start := time.Unix(1700000000, 0)
	geometry.token = "aerie-mobile-abcdef-123:1:0"
	geometry.lastPresence = start
	geometry.now = func() time.Time { return start.Add(HeadlessPresenceTimeout + time.Millisecond) }
	done := make(chan error, 1)
	go func() { done <- geometry.Heartbeat() }()
	release := waitForControlCommand(t, channel, "set-option -u", 0)
	respondHeadlessCommand(channel, release.text, controlResponse{Lines: []string{headlessOwnerSuccess}})
	if err := <-done; err == nil || !strings.Contains(err.Error(), "presence expired") {
		t.Fatalf("late heartbeat error = %v", err)
	}
	if geometry.token != "" {
		t.Fatalf("late heartbeat restored token %q", geometry.token)
	}
	if got := countControlCommands(channel, "set-option -t mobile @sidecar-owner"); got != 0 {
		t.Fatalf("late heartbeat emitted %d owner refreshes", got)
	}
}
