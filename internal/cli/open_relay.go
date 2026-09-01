package cli

import (
	"fmt"
	"os"
	"time"

	"github.com/marcus/sidecar/internal/hostserve"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/uirequest"
)

// sessionLeaseOwner reads @sidecar-owner for a tmux session. Tests replace it
// so a unit test never talks to the developer's live tmux server.
var sessionLeaseOwner = readCLISessionOwner

func readCLISessionOwner(session string) string {
	if session == "" || os.Getenv("TMUX") == "" {
		return ""
	}
	return tty.ReadTmuxSessionOwner(session)
}

func refuseRelayIfUnavailable(stateDir string, origin uirequest.Origin) error {
	if origin.TmuxSession == "" {
		return nil
	}
	owner := sessionLeaseOwner(origin.TmuxSession)
	if owner == "" {
		return nil
	}
	if localInstanceOwns(stateDir, owner) {
		return nil
	}
	presence, ok := hostserve.LookupLiveViewer(stateDir, owner, time.Now())
	if ok && presence.HasCapability(hostserve.ViewerCapabilityUIRequestRelayV1) {
		return nil
	}
	return &destError{code: 4, msg: fmt.Sprintf(
		"the screen is held by %s, which cannot receive pane requests (viewer disconnected, too old, or presence expired)",
		owner,
	)}
}

func localInstanceOwns(stateDir, owner string) bool {
	wantPID := tty.InstancePID(owner)
	if wantPID <= 0 || tty.InstanceHost(owner) != uirequest.HostName() {
		return false
	}
	instances, err := uirequest.ListInstances(stateDir)
	if err != nil {
		return false
	}
	for _, inst := range instances {
		if inst.PID == wantPID {
			return true
		}
	}
	return false
}
