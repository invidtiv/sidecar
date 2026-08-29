package overview

import (
	"strings"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/agentstatus"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/hostproto"
	"github.com/marcus/sidecar/internal/hosts"
	"github.com/marcus/sidecar/internal/workspaceinventory"
	"github.com/marcus/sidecar/internal/workspacelist"
)

// hostModel is a model with one host's data already folded in, without any
// registry, connection or ssh. The integration under test is what the Sessions
// browser does with remote rows, not how they arrive.
func hostModel(t *testing.T, id string, health hosts.Health, snapshot *hostproto.Snapshot) *Model {
	t.Helper()
	m := New(workspaceinventory.Collector{})
	m.hostHealth = map[string]hosts.Health{id: health}
	m.hostResults = map[string][]workspaceinventory.ProjectResult{}
	m.hostProjects = map[string][]Project{}
	if snapshot != nil && health.State.Shows() {
		results := hosts.ProjectResults(id, *snapshot, !health.State.Healthy())
		m.hostResults[id] = results
		projects := make([]Project, 0, len(results))
		for index, result := range results {
			projects = append(projects, Project{Name: result.ProjectName, Path: result.ProjectRoot, Key: result.ProjectKey, Index: index})
		}
		m.hostProjects[id] = projects
	}
	return m
}

func remoteSnapshot(lane string) *hostproto.Snapshot {
	return &hostproto.Snapshot{
		Generation: 1,
		ObservedAt: time.Now(),
		Projects: []hostproto.Project{{
			Key: "/home/me/api", Name: "api", Root: "/home/me/api",
			Items: []hostproto.Item{{
				ID: "/home/me/api:shell:s1", ProjectKey: "/home/me/api", ProjectName: "api",
				Kind: "shell", Key: "s1", Name: "Claude pane", Session: "api-claude", PaneID: "%7",
				Provider: "claude", Live: true, Preview: "line one\nDo you want to proceed?",
				Agent: &hostproto.Presentation{Lane: lane, Label: lane, Icon: "◆", Attention: lane == "blocked"},
			}},
		}},
	}
}

func rowByName(t *testing.T, m *Model, name string) workspacelist.Item {
	t.Helper()
	m.syncWorkspaces()
	for _, item := range m.workspaces.Items() {
		if item.Name == name {
			return item
		}
	}
	t.Fatalf("no row named %q in %v", name, rowNames(m))
	return workspacelist.Item{}
}

func rowNames(m *Model) []string {
	names := make([]string, 0)
	for _, item := range m.workspaces.Items() {
		names = append(names, item.Name)
	}
	return names
}

// TestRemoteRowsJoinTheOrdinaryList is the whole design claim: a remote
// workspace is an ordinary row, in the ordinary lane grouping.
func TestRemoteRowsJoinTheOrdinaryList(t *testing.T) {
	m := hostModel(t, "mac-mini", hosts.Health{State: hosts.StateOnline}, remoteSnapshot("blocked"))
	row := rowByName(t, m, "Claude pane")

	if row.Group != workspacelist.GroupNeedsAttention {
		t.Errorf("group = %q, want the ordinary blocked grouping", row.Group)
	}
	if row.Provider != "claude" {
		t.Errorf("provider = %q", row.Provider)
	}
	// The host name goes into the project label, which is what the Project
	// sort groups by and what each row is prefixed with.
	if !strings.HasPrefix(row.Project, "mac-mini") {
		t.Errorf("project label %q does not name the host", row.Project)
	}
	if !strings.Contains(row.Project, "api") {
		t.Errorf("project label %q lost the project name", row.Project)
	}
}

// TestRemoteRowsAreSearchableByHost. The filter searches the project label, so
// naming the host there makes "mac-mini" a usable query for free.
func TestRemoteRowsAreSearchableByHost(t *testing.T) {
	m := hostModel(t, "mac-mini", hosts.Health{State: hosts.StateOnline}, remoteSnapshot("working"))
	row := rowByName(t, m, "Claude pane")
	if !workspacelist.Match(row, "mac-mini") {
		t.Error("a remote row is not findable by its host name")
	}
	if workspacelist.Match(row, "some-other-host") {
		t.Error("a remote row matched an unrelated host name")
	}
}

// TestRemoteAgentsShareTheBoardLanes. "Is anything blocked?" is a question
// about every machine at once, so a remote blocked agent belongs in the same
// column as a local one.
func TestRemoteAgentsShareTheBoardLanes(t *testing.T) {
	m := hostModel(t, "mac-mini", hosts.Health{State: hosts.StateOnline}, remoteSnapshot("blocked"))
	m.syncBoard()
	found := false
	for _, workspace := range m.cards {
		if workspace.Remote() && workspace.Presentation.Lane == agentstatus.LaneBlocked {
			found = true
		}
	}
	if !found {
		t.Errorf("no remote blocked card on the board; cards=%d", len(m.cards))
	}
}

// TestUnhealthyHostShowsARowNotASilence. A machine that simply stops appearing
// is indistinguishable from one with nothing running on it.
func TestUnhealthyHostShowsARowNotASilence(t *testing.T) {
	for _, state := range []hosts.State{
		hosts.StateUnreachable, hosts.StateNoSidecar, hosts.StateProtocol,
		hosts.StateNoTmux, hosts.StateNotProtocol,
	} {
		m := hostModel(t, "mac-mini", hosts.Health{State: state, Detail: "because reasons"}, remoteSnapshot("working"))
		m.syncWorkspaces()

		var health workspacelist.Item
		for _, item := range m.workspaces.Items() {
			if IsHostHealthRow(item.ID) {
				health = item
			}
			if item.Name == "Claude pane" {
				t.Errorf("%s: a host that cannot show rows left one on screen", state)
			}
		}
		if health.ID == "" {
			t.Fatalf("%s: no health row; rows=%v", state, rowNames(m))
		}
		if !strings.Contains(health.Status, string(state)) {
			t.Errorf("%s: status %q does not name the state", state, health.Status)
		}
		if fix := state.Fix(); fix != "" && !strings.Contains(health.Status, fix) {
			t.Errorf("%s: status %q does not name the fix", state, health.Status)
		}
	}
}

// TestOnlineHostShowsNoHealthRow: a working host speaks through its workspaces.
func TestOnlineHostShowsNoHealthRow(t *testing.T) {
	m := hostModel(t, "mac-mini", hosts.Health{State: hosts.StateOnline}, remoteSnapshot("working"))
	m.syncWorkspaces()
	for _, item := range m.workspaces.Items() {
		if IsHostHealthRow(item.ID) {
			t.Errorf("a healthy host produced a health row: %+v", item)
		}
	}
}

// TestStaleHostKeepsItsRowsAndSaysSo. Last-known beats a blank host, as long
// as it is labelled.
func TestStaleHostKeepsItsRowsAndSaysSo(t *testing.T) {
	m := hostModel(t, "mac-mini", hosts.Health{State: hosts.StateStale}, remoteSnapshot("blocked"))
	row := rowByName(t, m, "Claude pane")
	if !strings.Contains(row.Status, "stale") {
		t.Errorf("a stale host's row does not say so: %q", row.Status)
	}
}

// TestRemoteWorkspacesAreNeverActedOn is the safety property. A remote path
// resolved against THIS machine either fails confusingly or — far worse —
// succeeds against an unrelated local directory.
func TestRemoteWorkspacesAreNeverActedOn(t *testing.T) {
	remote := workspaceinventory.Workspace{
		ID: "h\x1fx", HostID: "mac-mini", Name: "Claude pane",
		Kind: workspaceinventory.KindWorktree, Path: "/home/me/api",
	}
	for _, verb := range []string{"delete", "rename", "open"} {
		reason := remoteActionRefusal(remote, verb)
		if reason == "" {
			t.Fatalf("%s was permitted on a remote workspace", verb)
		}
		if !strings.Contains(reason, "mac-mini") {
			t.Errorf("%s refusal %q does not say which machine", verb, reason)
		}
	}
	local := workspaceinventory.Workspace{ID: "x", Name: "local", Kind: workspaceinventory.KindWorktree}
	if reason := remoteActionRefusal(local, "delete"); reason != "" {
		t.Errorf("a local workspace was refused: %q", reason)
	}
	// deleteRefusal is the shared gate the worktree path uses; the remote
	// check must be inside it, not beside it.
	if reason := deleteRefusal(remote); !strings.Contains(reason, "mac-mini") {
		t.Errorf("deleteRefusal did not refuse a remote worktree: %q", reason)
	}
}

// TestNavigationRefusesARemoteWorkspace covers the route every activation
// takes: the board, the list, and reveal all end in RequestNavigationAction.
func TestNavigationRefusesARemoteWorkspace(t *testing.T) {
	m := New(workspaceinventory.Collector{})
	remote := workspaceinventory.Workspace{ID: "h\x1fx", HostID: "mac-mini", Name: "Claude pane"}
	cmd := m.RequestNavigationAction(remote, "")
	if cmd == nil {
		t.Fatal("navigation to a remote workspace produced no result at all")
	}
	msg := cmd()
	validation, ok := msg.(ValidationMsg)
	if !ok {
		t.Fatalf("navigating to a remote workspace produced %T, want a refusal", msg)
	}
	if validation.Err == nil || !strings.Contains(validation.Err.Error(), "mac-mini") {
		t.Errorf("refusal does not name the machine: %v", validation.Err)
	}
}

// TestRemotePreviewSnapshotKeepsTheTail: the bottom of a pane holds the
// question an agent is asking, which is the reason to show it at all.
func TestRemotePreviewSnapshotKeepsTheTail(t *testing.T) {
	var builder strings.Builder
	for i := 0; i < 100; i++ {
		builder.WriteString("line\n")
	}
	builder.WriteString("Do you want to proceed?")
	workspace := workspaceinventory.Workspace{HostID: "h", Preview: builder.String()}

	snapshot := remotePreviewSnapshot(workspace)
	if !strings.Contains(snapshot, "Do you want to proceed?") {
		t.Error("the tail was truncated away")
	}
	if got := len(strings.Split(snapshot, "\n")); got > remotePreviewSnapshotLines {
		t.Errorf("snapshot is %d lines, want at most %d", got, remotePreviewSnapshotLines)
	}
	if remotePreviewSnapshot(workspaceinventory.Workspace{HostID: "h"}) != "" {
		t.Error("an empty preview produced content")
	}
}

// TestNoHostsWithTheFeatureOff is the rollback guarantee at the model level:
// nothing is started, so no ssh child can exist.
func TestNoHostsWithTheFeatureOff(t *testing.T) {
	features.Init(&config.Config{})
	features.SetOverride(features.SidecarRemoteHosts.Name, false)
	t.Cleanup(func() { features.SetOverride(features.SidecarRemoteHosts.Name, features.SidecarRemoteHosts.Default) })

	m := New(workspaceinventory.Collector{})
	m.SetConfig(&config.Config{Hosts: config.HostsConfig{List: []config.HostConfig{{ID: "a", Target: "a"}}}})
	if cmd := m.SyncHosts(); cmd != nil {
		t.Error("SyncHosts started work with the feature off")
	}
	if m.hostRegistry != nil {
		t.Error("a registry was created with the feature off")
	}
	m.syncWorkspaces()
	if len(m.workspaces.Items()) != 0 {
		t.Errorf("rows appeared with the feature off: %v", rowNames(m))
	}
	if spawn := m.hostControlSpawner("a"); spawn != nil {
		t.Error("a control spawner was offered with the feature off")
	}
}

// TestControlSpawnerRefusesAnUnreachableHost. A live view must refuse rather
// than hang on a connect timeout inside the render path.
func TestControlSpawnerRefusesAnUnreachableHost(t *testing.T) {
	m := hostModel(t, "mac-mini", hosts.Health{State: hosts.StateUnreachable}, nil)
	if spawn := m.hostControlSpawner("mac-mini"); spawn != nil {
		t.Error("an unreachable host offered a control spawner")
	}
	if spawn := m.hostControlSpawner("never-registered"); spawn != nil {
		t.Error("an unknown host offered a control spawner")
	}
}

// TestRemotePaneRefusesInteractiveMode. Input is already dropped by the
// read-only sender, but entering the mode would put "typing" in the header of
// a pane that cannot receive a keystroke — which looks like it worked.
func TestRemotePaneRefusesInteractiveMode(t *testing.T) {
	m := hostModel(t, "mac-mini", hosts.Health{State: hosts.StateOnline}, remoteSnapshot("blocked"))
	m.syncWorkspaces()
	m.preview.visible = true
	for _, item := range m.workspaces.Items() {
		if item.Name == "Claude pane" {
			m.workspaces.SelectID(item.ID)
			m.preview.workspaceID = item.ID
		}
	}
	workspace, ok := m.SelectedWorkspace()
	if !ok || !workspace.Remote() {
		t.Fatalf("selection is not the remote row: %+v ok=%v", workspace, ok)
	}

	cmd := m.enterPreviewInteractive()
	if m.PreviewInteractive() {
		t.Error("a remote pane entered interactive mode")
	}
	if cmd == nil {
		t.Fatal("entering interactive on a remote pane said nothing at all")
	}
	if msg := cmd(); msg == nil {
		t.Error("no refusal message")
	}
}
