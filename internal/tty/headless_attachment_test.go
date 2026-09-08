package tty

import (
	"errors"
	"strings"
	"testing"
	"time"
)

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
