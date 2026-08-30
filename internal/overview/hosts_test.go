package overview

import (
	"context"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/tty"

	"github.com/marcus/sidecar/internal/agentstatus"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/hostproto"
	"github.com/marcus/sidecar/internal/hosts"
	"github.com/marcus/sidecar/internal/notify"
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
				ID: "/home/me/api:shell:s1", ProjectKey: "/home/me/api", ProjectName: "api", ProjectRoot: "/home/me/api",
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
		hosts.StateNoTmux, hosts.StateNotProtocol, hosts.StateDisabled,
	} {
		// Seed the rows FIRST, from a healthy snapshot, then drive the host
		// into the unhealthy state through handleHostUpdate. Building the
		// fixture already-unhealthy tested the fixture: its helper only
		// populates results when the state shows them, so the "no rows left on
		// screen" assertion below was true by construction rather than because
		// handleHostUpdate drops them.
		m := hostModel(t, "mac-mini", hosts.Health{State: hosts.StateOnline}, remoteSnapshot("working"))
		m.hostRegistry = hosts.NewRegistry(hosts.ClientOptions{})
		t.Cleanup(m.hostRegistry.Stop)
		m.hostRegistered = map[string]bool{"mac-mini": true}
		m.syncWorkspaces()
		if _, seeded := m.hostResults["mac-mini"]; !seeded {
			t.Fatalf("%s: fixture did not seed rows to begin with", state)
		}

		m.handleHostUpdate(hostUpdateMsg{Update: hosts.Update{
			HostID: "mac-mini",
			Health: hosts.Health{State: state, Detail: "because reasons"},
		}})
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

// remoteVerbCallSites is every verb literal remoteActionRefusal is actually
// called with, read out of this package's own source rather than typed here by
// hand — a hand-maintained list is exactly how "create" and "send" survived in
// remoteVerbs, asserted on by a test, while no call site ever passed them.
func remoteVerbCallSites(t *testing.T) map[string]bool {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package source: %v", err)
	}
	pattern := regexp.MustCompile(`remoteActionRefusal\([^,]+,\s*"([^"]+)"`)
	verbs := map[string]bool{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		for _, match := range pattern.FindAllStringSubmatch(string(data), -1) {
			verbs[match[1]] = true
		}
	}
	if len(verbs) == 0 {
		t.Fatal("no remoteActionRefusal call sites found; the scan is broken, not the code")
	}
	return verbs
}

// TestRemoteWorkspacesAreNeverActedOn is the safety property, now asked as a
// capability question rather than a blanket no.
//
// The verbs that reach the host as its own `sidecar` invocation are allowed,
// because the machine that owns the state is the machine that changes it. The
// verbs whose implementation runs HERE stay refused: a remote path resolved
// against this filesystem either fails confusingly or — far worse — succeeds
// against an unrelated local directory.
func TestRemoteWorkspacesAreNeverActedOn(t *testing.T) {
	remote := workspaceinventory.Workspace{
		ID: "h\x1fx", HostID: "mac-mini", Name: "Claude pane",
		Kind: workspaceinventory.KindWorktree, Path: "/home/me/api",
	}
	for _, verb := range []string{"delete", "merge", "open"} {
		reason := remoteActionRefusal(remote, verb)
		if reason == "" {
			t.Fatalf("%s was permitted on a remote workspace", verb)
		}
		if !strings.Contains(reason, "mac-mini") {
			t.Errorf("%s refusal %q does not say which machine", verb, reason)
		}
	}
	// rename is the one verb this gate is consulted for and permits. "create"
	// and "send" used to be listed here and asserted on here, and neither the
	// gate nor the assertion was reachable from any call site: creation
	// resolves a createTarget from the form rather than judging a selected row,
	// and there is no standalone send action. A map entry nothing consults is a
	// gate that looks open without being a gate.
	if reason := remoteActionRefusal(remote, "rename"); reason != "" {
		t.Errorf("rename is a host-side verb but was refused: %q", reason)
	}
	asked := remoteVerbCallSites(t)
	for verb := range remoteVerbs {
		if !asked[verb] {
			t.Errorf("remoteVerbs lists %q, which remoteActionRefusal is never called with", verb)
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
	// mergeRefusal likewise: it is what the footer consults, so with the
	// remote clause inside it Merge is hidden on a remote row up front rather
	// than offered and then taken back by the navigation guard.
	if reason := mergeRefusal(remote); !strings.Contains(reason, "mac-mini") {
		t.Errorf("mergeRefusal did not refuse a remote worktree: %q", reason)
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

// TestRemotePaneEntersInteractiveMode proves the Phase B surface handoff: the
// same local chrome becomes interactive and the terminal component is asked to
// claim input. The fake keeps ssh/tmux out of this surface-level test.
func TestRemotePaneEntersInteractiveMode(t *testing.T) {
	var terminal *activatingRemoteTerminal
	original := newPreviewTerminal
	newPreviewTerminal = func(config tty.Config, hooks tty.Hooks) previewTerminal {
		terminal = &activatingRemoteTerminal{modeRecordingTerminal: modeRecordingTerminal{calls: &[]string{}}}
		return terminal
	}
	t.Cleanup(func() { newPreviewTerminal = original })

	m := hostModel(t, "mac-mini", hosts.Health{State: hosts.StateOnline}, remoteSnapshot("blocked"))
	m.hostRegistry = hosts.NewRegistry(hosts.ClientOptions{})
	t.Cleanup(m.hostRegistry.Stop)
	m.hostRegistry.Sync(context.Background(), []hosts.Host{{ID: "mac-mini", Target: "mac-mini"}})
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

	m.syncPreviewTerminal()
	_ = m.enterPreviewInteractive()
	if terminal == nil || terminal.activated != 1 {
		t.Fatalf("remote input activation count = %v, want 1", terminal)
	}
	if !m.previewTerminalLeaf().Interactive || !m.PreviewInteractive() {
		t.Error("remote pane did not enter the ordinary interactive chrome")
	}
}

// TestLocalPaneStillEntersInteractive is the other half: the guard must refuse
// remote panes only. Without this, returning a refusal unconditionally would
// pass every assertion above.
func TestLocalPaneStillEntersInteractive(t *testing.T) {
	m := New(workspaceinventory.Collector{})
	local := workspaceinventory.Workspace{
		ID: "local-1", Kind: workspaceinventory.KindShell, Name: "Local shell",
		TmuxName: "proj-1", PaneID: "%1", Live: true, Path: t.TempDir(),
	}
	m.catalog = map[string]workspaceinventory.Workspace{local.ID: local}
	m.workspaces.SetItems([]workspacelist.Item{{ID: local.ID, Name: local.Name}})
	m.workspaces.SelectID(local.ID)
	m.preview.visible = true
	m.preview.workspaceID = local.ID

	if cmd := m.enterPreviewInteractive(); cmd != nil {
		if post, refused := cmd().(notify.PostMsg); refused {
			t.Errorf("a local pane was refused interactive mode: %q", post.Notification.Title)
		}
	}
}

// TestPreviewSetsControlModeOnEveryActivation is the integration half of the
// contaminated-Model defect: the surface must SET the mode each time, not
// change it when it notices a difference.
func TestPreviewSetsControlModeOnEveryActivation(t *testing.T) {
	var calls []string
	original := newPreviewTerminal
	newPreviewTerminal = func(config tty.Config, hooks tty.Hooks) previewTerminal {
		return &modeRecordingTerminal{calls: &calls}
	}
	t.Cleanup(func() { newPreviewTerminal = original })

	m := hostModel(t, "mac-mini", hosts.Health{State: hosts.StateOnline}, remoteSnapshot("working"))
	m.hostRegistry = hosts.NewRegistry(hosts.ClientOptions{})
	t.Cleanup(m.hostRegistry.Stop)
	m.hostRegistry.Sync(context.Background(), []hosts.Host{{ID: "mac-mini", Target: "mac-mini"}})
	m.preview.visible = true
	m.syncWorkspaces()

	remote, ok := m.catalog[hosts.ScopedKey("mac-mini", "/home/me/api:shell:s1")]
	if !ok {
		t.Fatalf("no remote row in the catalog: %v", rowNames(m))
	}
	local := workspaceinventory.Workspace{
		ID: "local-1", Kind: workspaceinventory.KindShell, Name: "Local shell",
		TmuxName: "proj-1", PaneID: "%1", Live: true,
	}
	m.catalog[local.ID] = local
	m.workspaces.SetItems([]workspacelist.Item{{ID: remote.ID, Name: remote.Name}, {ID: local.ID, Name: local.Name}})

	// Remote first, then local — the order that used to leave the local pane
	// wired to the remote host's ssh.
	m.workspaces.SelectID(remote.ID)
	m.preview.workspaceID = remote.ID
	m.syncPreviewTerminal()

	m.workspaces.SelectID(local.ID)
	m.preview.workspaceID = local.ID
	m.syncPreviewTerminal()

	if len(calls) < 2 {
		t.Fatalf("mode was set %d times for two activations: %v", len(calls), calls)
	}
	if calls[0] != "remote" {
		t.Errorf("first activation set %q, want remote", calls[0])
	}
	if last := calls[len(calls)-1]; last != "local" {
		t.Errorf("after selecting a local row the mode is %q; the terminal is still pointed at the remote host", last)
	}
}

// modeRecordingTerminal is a previewTerminal that records only which control
// mode it was put into. Everything else is inert: this test is about the
// decision, not about a tmux.
type modeRecordingTerminal struct {
	calls    *[]string
	active   bool
	released int
}

type activatingRemoteTerminal struct {
	modeRecordingTerminal
	activated int
}

func (t *activatingRemoteTerminal) ActivateInput() tea.Cmd {
	t.activated++
	return nil
}

func (t *modeRecordingTerminal) UseRemoteControl(tty.ControlSpawner) {
	*t.calls = append(*t.calls, "remote")
}
func (t *modeRecordingTerminal) UseLocalControl() { *t.calls = append(*t.calls, "local") }

func (t *modeRecordingTerminal) Open(tty.Target) tea.Cmd { t.active = true; return nil }
func (t *modeRecordingTerminal) Close()                  { t.active = false }
func (t *modeRecordingTerminal) IsActive() bool          { return t.active }
func (t *modeRecordingTerminal) Buffer() *tty.OutputBuffer {
	return nil
}
func (t *modeRecordingTerminal) SetDimensions(int, int) tea.Cmd      { return nil }
func (t *modeRecordingTerminal) PaneSize() (int, int)                { return 80, 24 }
func (t *modeRecordingTerminal) CursorState() (int, int, bool)       { return 0, 0, false }
func (t *modeRecordingTerminal) SetHooks(tty.Hooks)                  {}
func (t *modeRecordingTerminal) ReleaseInput()                       { t.released++ }
func (t *modeRecordingTerminal) Exit()                               {}
func (t *modeRecordingTerminal) Update(tea.Msg) tea.Cmd              { return nil }
func (t *modeRecordingTerminal) SendUnknownSequence(tea.Msg) tea.Cmd { return nil }
func (t *modeRecordingTerminal) History() tty.HistoryInfo            { return tty.HistoryInfo{} }
func (t *modeRecordingTerminal) PrependHistory(string, int) bool     { return false }
func (t *modeRecordingTerminal) PaneMouseReporting() bool            { return false }
func (t *modeRecordingTerminal) SendClick(int, int) tea.Cmd          { return nil }
func (t *modeRecordingTerminal) NoteMouseActivity()                  {}
func (t *modeRecordingTerminal) NoteInput()                          {}
func (t *modeRecordingTerminal) SendWheelNotches(bool, int, int, int) tea.Cmd {
	return nil
}

// TestHostOrdinalsDoNotCollideAcrossHosts. Restarting the project index at
// zero per host made every host's first project tie with every other's, so the
// Project sort interleaved two machines instead of grouping them.
func TestHostOrdinalsDoNotCollideAcrossHosts(t *testing.T) {
	m := New(workspaceinventory.Collector{})
	m.hostHealth = map[string]hosts.Health{}
	m.hostResults = map[string][]workspaceinventory.ProjectResult{}
	m.hostProjects = map[string][]Project{}
	for _, id := range []string{"alpha", "beta"} {
		m.hostHealth[id] = hosts.Health{State: hosts.StateOnline}
		results := hosts.ProjectResults(id, *remoteSnapshot("working"), false)
		m.hostResults[id] = results
		for index, result := range results {
			m.hostProjects[id] = append(m.hostProjects[id], Project{
				Name: result.ProjectName, Path: result.ProjectRoot, Key: result.ProjectKey, Index: index,
			})
		}
	}

	seen := map[int]string{}
	m.eachHostWorkspace(func(order int, label string, _ workspaceinventory.Workspace, _ bool) {
		host := strings.SplitN(label, hostRowPrefix, 2)[0]
		if other, clash := seen[order]; clash && other != host {
			t.Errorf("ordinal %d is shared by %s and %s", order, other, host)
		}
		seen[order] = host
	})
	if len(seen) < 2 {
		t.Fatalf("expected distinct ordinals per host, got %v", seen)
	}
}

// TestDisabledHostIsVisible. `disabled` means "off this week"; a machine that
// silently vanished would be indistinguishable from a deleted entry.
func TestDisabledHostIsVisible(t *testing.T) {
	m := hostModel(t, "sleepy", hosts.Health{State: hosts.StateDisabled}, nil)
	m.syncWorkspaces()
	var row workspacelist.Item
	for _, item := range m.workspaces.Items() {
		if IsHostHealthRow(item.ID) {
			row = item
		}
	}
	if row.ID == "" {
		t.Fatalf("a disabled host produced no row: %v", rowNames(m))
	}
	if !strings.Contains(row.Status, string(hosts.StateDisabled)) {
		t.Errorf("status %q does not say it is disabled", row.Status)
	}
	if !strings.Contains(row.Status, hosts.StateDisabled.Fix()) {
		t.Errorf("status %q does not name the fix", row.Status)
	}
}

// TestDeregisteredHostIsNotResurrected. A stopped client can still deliver one
// last update after its state was pruned; applying it would leave a permanent
// error row for a machine the user removed.
func TestDeregisteredHostIsNotResurrected(t *testing.T) {
	m := hostModel(t, "gone", hosts.Health{State: hosts.StateOnline}, remoteSnapshot("working"))
	m.hostRegistry = hosts.NewRegistry(hosts.ClientOptions{})
	t.Cleanup(m.hostRegistry.Stop)
	// The user removed this host: it is no longer registered.
	// Empty-but-non-nil is the feature-off/no-host reload state. The guard must
	// not use len(map)>0 or this exact final update gets through.
	m.hostRegistered = map[string]bool{}
	delete(m.hostHealth, "gone")

	m.handleHostUpdate(hostUpdateMsg{Update: hosts.Update{
		HostID: "gone",
		Health: hosts.Health{State: hosts.StateUnreachable, Detail: "the connection to the host ended"},
	}})
	if _, back := m.hostHealth["gone"]; back {
		t.Error("a de-registered host came back as an error row")
	}
}

func TestRemoteControlInvalidatedByRemovalOrRetarget(t *testing.T) {
	target := tty.Target{Session: "s", Pane: "%1", Host: "mini"}
	old := map[string]hosts.Host{"mini": {ID: "mini", Target: "old"}}
	for name, next := range map[string]map[string]hosts.Host{
		"removed":    {},
		"retargeted": {"mini": {ID: "mini", Target: "new"}},
	} {
		if !remoteControlInvalidated(target, old, next) {
			t.Errorf("%s host left the selected control alive", name)
		}
	}
	if remoteControlInvalidated(target, old, map[string]hosts.Host{"mini": {ID: "mini", Target: "old"}}) {
		t.Error("unchanged host unnecessarily invalidated its selected control")
	}
}

func TestHostRemovalClosesSelectedRemoteTerminalAndHistory(t *testing.T) {
	features.Init(config.Default())
	features.SetOverride(features.SidecarRemoteHosts.Name, true)
	t.Cleanup(func() { features.SetOverride(features.SidecarRemoteHosts.Name, features.SidecarRemoteHosts.Default) })

	m := New(workspaceinventory.Collector{})
	terminal := newFakeTerminal("remote")
	terminal.active = true
	m.primaryTerminalState().terminal = terminal
	m.setPrimaryTarget(tty.Target{Session: "s", Pane: "%1", Host: "mini"})
	m.primaryTerminalLeaf().Buffer = terminal.buffer
	m.primaryTerminalLeaf().History.Record(100)
	m.primaryTerminalLeaf().Interactive = true
	m.hostConfigured = map[string]hosts.Host{"mini": {ID: "mini", Target: "old"}}
	m.SetConfig(&config.Config{})

	_ = m.SyncHosts()
	if terminal.released != 1 || terminal.active {
		t.Fatalf("removed host terminal lifecycle: closes=%d releases=%d active=%v", terminal.closes, terminal.released, terminal.active)
	}
	if m.previewTarget() != (tty.Target{}) || m.primaryTerminalLeaf().History.HistorySize != 0 || m.primaryTerminalLeaf().History.Loading {
		t.Fatalf("removed host retained target/history: target=%+v history=%+v", m.previewTarget(), m.primaryTerminalLeaf().History)
	}
}

func TestHostRetargetClosesSelectedRemoteControlBeforeReconnect(t *testing.T) {
	features.Init(config.Default())
	features.SetOverride(features.SidecarRemoteHosts.Name, true)
	t.Cleanup(func() { features.SetOverride(features.SidecarRemoteHosts.Name, features.SidecarRemoteHosts.Default) })

	m := New(workspaceinventory.Collector{})
	m.hostRegistry = hosts.NewRegistry(hosts.ClientOptions{Dial: func(context.Context) (*hosts.Conn, error) {
		return nil, context.Canceled
	}})
	t.Cleanup(m.hostRegistry.Stop)
	m.hostRegistry.Sync(context.Background(), []hosts.Host{{ID: "mini", Target: "old"}})
	oldIncarnation, ok := m.hostRegistry.Incarnation("mini")
	if !ok {
		t.Fatal("old host client has no incarnation")
	}
	m.hostResults = make(map[string][]workspaceinventory.ProjectResult)
	m.hostHealth = make(map[string]hosts.Health)
	m.hostProjects = make(map[string][]Project)
	m.hostRegistered = map[string]bool{"mini": true}

	terminal := newFakeTerminal("remote")
	terminal.active = true
	m.primaryTerminalState().terminal = terminal
	m.setPrimaryTarget(tty.Target{Session: "s", Pane: "%1", Host: "mini"})
	m.primaryTerminalLeaf().Buffer = terminal.buffer
	m.primaryTerminalLeaf().History.Record(100)
	m.primaryTerminalLeaf().Interactive = true
	m.hostConfigured = map[string]hosts.Host{"mini": {ID: "mini", Target: "old"}}
	cfg := config.Default()
	cfg.Hosts.List = []config.HostConfig{{ID: "mini", Target: "new"}}
	m.SetConfig(cfg)

	_ = m.SyncHosts()
	if terminal.released != 1 || terminal.active {
		t.Fatalf("retargeted host terminal lifecycle: closes=%d releases=%d active=%v", terminal.closes, terminal.released, terminal.active)
	}
	if m.previewTarget() != (tty.Target{}) || m.primaryTerminalLeaf().History.HistorySize != 0 {
		t.Fatalf("retargeted host retained old target/history: target=%+v history=%+v", m.previewTarget(), m.primaryTerminalLeaf().History)
	}
	newIncarnation, ok := m.hostRegistry.Incarnation("mini")
	if !ok || newIncarnation == oldIncarnation {
		t.Fatalf("retarget incarnation old=%d new=%d found=%v", oldIncarnation, newIncarnation, ok)
	}

	// This message was already queued when the old client was replaced. The ID
	// alone still matches, so only the incarnation can keep the old machine from
	// overwriting the new registration.
	_ = m.handleHostUpdate(hostUpdateMsg{Update: hosts.Update{
		HostID: "mini", Incarnation: oldIncarnation,
		Health: hosts.Health{State: hosts.StateUnreachable, Detail: "old target"},
	}})
	if health, exists := m.hostHealth["mini"]; exists && health.Detail == "old target" {
		t.Fatalf("queued old-client update applied after retarget: %+v", health)
	}
	_ = m.handleHostUpdate(hostUpdateMsg{Update: hosts.Update{
		HostID: "mini", Incarnation: newIncarnation,
		Health: hosts.Health{State: hosts.StateUnreachable, Detail: "new target"},
	}})
	if got := m.hostHealth["mini"].Detail; got != "new target" {
		t.Fatalf("current-client update detail = %q, want new target", got)
	}
}

// TestHostHealthRowExplainsItselfInThePreview. The health row is the row most
// in need of explaining itself, and it is not a workspace — so the preview's
// ordinary path misses it and would go blank exactly when the user clicked the
// thing telling them something is wrong.
func TestHostHealthRowExplainsItselfInThePreview(t *testing.T) {
	m := hostModel(t, "mac-mini", hosts.Health{
		State:  hosts.StateNoSidecar,
		Detail: "zsh:1: command not found: sidecar",
	}, nil)

	detail := m.HostHealthDetail(hostHealthRowID("mac-mini"))
	if detail == "" {
		t.Fatal("a health row has nothing to show in the preview")
	}
	for _, want := range []string{"mac-mini", string(hosts.StateNoSidecar), "command not found", hosts.StateNoSidecar.Fix()} {
		if !strings.Contains(detail, want) {
			t.Errorf("detail %q is missing %q", detail, want)
		}
	}
	// A workspace row must fall through to the ordinary preview.
	if got := m.HostHealthDetail("some-project:shell:x"); got != "" {
		t.Errorf("a workspace row was treated as a health row: %q", got)
	}
	if got := m.HostHealthDetail(hostHealthRowID("never-registered")); got != "" {
		t.Errorf("an unknown host produced detail: %q", got)
	}
}
