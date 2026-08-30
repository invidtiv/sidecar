package tty

import (
	"os/exec"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// A remote pane whose session ends must leave interactive mode and say why.
//
// The reported symptom was that typing `exit` in a remote shell made the row
// "eventually go away but not be responsive". The row leaving is the host's
// answer — liveness, and now the reap. The preview not leaving is this: a
// remote model had no path to endDeadSession at all.
//
// Locally, a control channel that fails falls back to polling capture-pane, and
// the first capture answers "can't find pane", which is what ends the mode. A
// remote model's capture fallback is deliberately unavailable, because a local
// capture-pane for a remote %4 does not fail — it succeeds against an unrelated
// local pane. So the local death signal simply does not exist remotely, and the
// model retried ssh every 250ms forever with interactive mode still on.
//
// The evidence that replaces it is the attach error. These two tests pin both
// halves: that the error carries tmux's own words, and that the model acts on
// them without acting on a link failure.

// TestControlAttachErrorCarriesTheReason is the information half. Before this,
// stderr was copied to io.Discard and a failed attach reported "exit status 1",
// which no classifier can do anything with.
func TestControlAttachErrorCarriesTheReason(t *testing.T) {
	cmd := exec.Command("sh", "-c", `printf "can't find session: sidecar-sh-gone\n" >&2; exit 1`)
	channel, err := newProcessControlChannelCommand("sidecar-sh-gone", cmd)
	if err == nil {
		if channel != nil {
			_ = channel.Close()
		}
		t.Fatal("a control command that exited 1 was accepted as an attach")
	}
	if !IsSessionDeadError(err) {
		t.Fatalf("attach error %q does not read as a dead session; the reason was lost", err)
	}
}

// TestControlAttachErrorKeepsALinkFailureAmbiguous is why this is not a retry
// budget. A machine that is briefly unreachable must keep retrying; only tmux
// saying the session is not there ends the mode.
func TestControlAttachErrorKeepsALinkFailureAmbiguous(t *testing.T) {
	cmd := exec.Command("sh", "-c", `printf "ssh: connect to host aerie port 22: Connection refused\n" >&2; exit 255`)
	channel, err := newProcessControlChannelCommand("sidecar-sh-1", cmd)
	if err == nil {
		if channel != nil {
			_ = channel.Close()
		}
		t.Fatal("a control command that exited 255 was accepted as an attach")
	}
	if IsSessionDeadError(err) {
		t.Fatalf("an unreachable host read as a dead session: %q", err)
	}
}

func remoteModelInInteractiveMode(t *testing.T) (*Model, *bool) {
	t.Helper()
	m := New(nil)
	m.visible = true
	m.State = &State{
		Active:        true,
		TargetSession: "remote-shell",
		TargetPane:    "%4",
		OutputBuf:     NewOutputBuffer(m.Config.ScrollbackLines),
	}
	m.scopeTarget = "%4"
	m.controlGen = 1 // zero is reserved for "no mailbox overflow" in direct deliveries
	m.UseRemoteControl(func(string) *exec.Cmd { return exec.Command("false") })

	ended := false
	m.OnSessionEnded = func() tea.Cmd {
		ended = true
		return nil
	}
	return m, &ended
}

// TestRemoteFallbackEndsTheModeWhenTheSessionIsGone is the defect itself.
func TestRemoteFallbackEndsTheModeWhenTheSessionIsGone(t *testing.T) {
	m, ended := remoteModelInInteractiveMode(t)

	m.handleControlDelivery(terminalControlMsg{
		Scope: m.Scope(),
		Event: terminalControlEvent{
			kind: terminalFallbackEvent,
			err:  errorf("tmux control attach: exit status 1: can't find session: remote-shell"),
			gen:  m.controlGen,
		},
	})

	if !*ended {
		t.Fatal("a remote pane whose session is gone stayed interactive; nothing told the host why")
	}
	if m.IsActive() {
		t.Error("the model is still active after its remote session ended")
	}
}

// TestRemoteFallbackKeepsRetryingWhenTheLinkFails. An unreachable machine is
// not a dead session, and ending the mode on one would make every network
// hiccup look like the user's shell exiting.
func TestRemoteFallbackKeepsRetryingWhenTheLinkFails(t *testing.T) {
	m, ended := remoteModelInInteractiveMode(t)

	m.handleControlDelivery(terminalControlMsg{
		Scope: m.Scope(),
		Event: terminalControlEvent{
			kind: terminalFallbackEvent,
			err:  errorf("tmux control attach: exit status 255: ssh: connect to host aerie port 22: Connection refused"),
			gen:  m.controlGen,
		},
	})

	if *ended {
		t.Fatal("a dropped link ended the mode as if the session had exited")
	}
	if !m.IsActive() {
		t.Error("a dropped link deactivated the model")
	}
	if !m.recoveryPending {
		t.Error("a dropped link did not leave the recovery path armed")
	}
}

// TestLocalFallbackIsUnchanged. The remote answer must not become a second
// death signal on the local path, whose capture fallback already answers this
// within one poll.
func TestLocalFallbackIsUnchanged(t *testing.T) {
	m, ended := remoteModelInInteractiveMode(t)
	m.UseLocalControl()

	m.handleControlDelivery(terminalControlMsg{
		Scope: m.Scope(),
		Event: terminalControlEvent{
			kind: terminalFallbackEvent,
			err:  errorf("tmux control attach: exit status 1: can't find session: remote-shell"),
			gen:  m.controlGen,
		},
	})

	if *ended {
		t.Fatal("the local fallback path started ending sessions on a control error")
	}
	if !m.recoveryPending {
		t.Error("the local fallback path no longer arms recovery")
	}
}

type stringError string

func (e stringError) Error() string { return string(e) }

func errorf(message string) error { return stringError(message) }
