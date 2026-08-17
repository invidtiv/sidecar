package tty

import (
	"errors"
	"testing"
)

// The embedded terminal ends its mode from this predicate alone, and it used to
// miss the two messages tmux prints most often when a Sidecar shell exits: a
// session name that no longer resolves, and a pane id with nothing behind it
// (td-6a4100).
func TestIsSessionDeadErrorCoversSessionAndPaneTargets(t *testing.T) {
	for _, message := range []string{
		"capture pane range: can't find session: sidecar-sh-demo-1",
		"capture-pane: no current target",
		"send-keys: can't find pane: %12",
	} {
		if !IsSessionDeadError(errors.New(message)) {
			t.Errorf("IsSessionDeadError(%q) = false, want the mode to end", message)
		}
	}
}

// A tmux server that is down is not one dead pane. Ending the mode there would
// throw a user out of a shell that comes back with the server.
func TestIsSessionDeadErrorIgnoresServerWideFailures(t *testing.T) {
	for _, message := range []string{
		"no server running on /private/tmp/tmux-501/default",
		"capture-pane: timeout after 2s",
	} {
		if IsSessionDeadError(errors.New(message)) {
			t.Errorf("IsSessionDeadError(%q) = true, want the mode kept", message)
		}
	}
	if IsSessionDeadError(nil) {
		t.Fatal("IsSessionDeadError(nil) = true")
	}
}
