package notify

import "testing"

// Two machines legitimately run a tmux session with the same name over a
// checkout at the same path. Identity has to separate them, or one host's
// replacement group and dedupe window would answer for another's.
func TestStableKeySeparatesMachines(t *testing.T) {
	local := Origin{TmuxSession: "api-claude", ProjectKey: "api", WorkDir: "/home/me/api"}
	remote := local
	remote.HostID = "mac-mini"
	other := local
	other.HostID = "linux-box"

	if local.StableKey() == "" {
		t.Fatal("a resolvable origin has no stable key")
	}
	if local.StableKey() == remote.StableKey() {
		t.Error("a remote origin shares its key with the local look-alike")
	}
	if remote.StableKey() == other.StableKey() {
		t.Error("two hosts share one key")
	}
	if (Origin{}).StableKey() != "" {
		t.Error("an origin identifying nobody produced a key")
	}
}

// A local `sidecar notify dismiss` must not reach a record that came off
// another machine's stream, however much the session names agree.
func TestMatchesRefusesAcrossMachines(t *testing.T) {
	caller := Origin{TmuxSession: "api-claude", WorkDir: "/home/me/api"}
	remote := caller
	remote.HostID = "mac-mini"
	if remote.Matches(caller) || caller.Matches(remote) {
		t.Error("a local caller matched a remote record")
	}
	if !caller.Matches(caller) {
		t.Error("a caller no longer matches itself")
	}
}

// The derived ID is the whole of same-machine duplicate prevention for remote
// work, and the destination host is the boundary it is scoped to.
func TestRemoteIDIsDerivedAndHostScoped(t *testing.T) {
	first := RemoteID("mac-mini", "event-key")
	if first != RemoteID("mac-mini", "event-key") {
		t.Error("the derived id is not stable")
	}
	if first == RemoteID("linux-box", "event-key") {
		t.Error("two hosts derived one id for their own events")
	}
	if first == RemoteID("mac-mini", "another-key") {
		t.Error("two events on one host derived the same id")
	}
	if len(first) < 8 || first[:4] != "ntf-" {
		t.Errorf("id %q is not recognisable as a notification id", first)
	}
}

// Adopting a remote record would seed a local lane tracker with a key no local
// observation can produce, and the next complete sweep would withdraw the
// remote agent's wait while it was still waiting.
func TestRemoteTransitionsAreOwnedByNoLocalProject(t *testing.T) {
	n := Notification{
		Origin:     Origin{HostID: "mac-mini", TmuxSession: "api-claude", WorkDir: "/home/me/api"},
		Transition: &TransitionMetadata{Class: TransitionWaiting, LaneKey: "lane", ProjectRoot: "/home/me/api"},
	}
	if TransitionOwnedByProject(n, "/home/me/api") {
		t.Error("a local project claimed a remote transition")
	}
	local := n
	local.Origin.HostID = ""
	if !TransitionOwnedByProject(local, "/home/me/api") {
		t.Error("the local case regressed; the assertion above proves nothing")
	}
}
