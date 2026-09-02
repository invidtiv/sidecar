package overview

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/hosts"
	"github.com/marcus/sidecar/internal/uirequest"
	"github.com/marcus/sidecar/internal/workspacelist"
)

// Hiding a remote is a view filter over the shared catalog, so the tests below
// assert against both projections and against the control that has to explain
// the absence. A machine whose rows vanish with nothing on screen saying why is
// exactly the confusion this feature exists to remove.

func healthyHost(t *testing.T, id string) *Model {
	t.Helper()
	return hostModel(t, id, hosts.Health{State: hosts.StateOnline}, remoteSnapshot("working"))
}

// TestHiddenHostLeavesBothProjections: the list and the board read one gate.
func TestHiddenHostLeavesBothProjections(t *testing.T) {
	m := healthyHost(t, "mac-mini")
	m.syncBoard()
	if len(m.workspaces.Items()) == 0 {
		t.Fatal("fixture contributed no remote rows")
	}
	if len(m.cards) == 0 {
		t.Fatal("fixture contributed no remote card")
	}

	m.setHostHidden("mac-mini", true)
	m.syncBoard()

	if names := rowNames(m); len(names) != 0 {
		t.Errorf("a hidden host still lists rows: %v", names)
	}
	if len(m.cards) != 0 {
		t.Errorf("a hidden host still shows cards: %v", m.cards)
	}

	m.setHostHidden("mac-mini", false)
	m.syncBoard()
	if len(m.workspaces.Items()) == 0 {
		t.Error("unhiding a host did not bring its rows back")
	}
}

// TestHiddenUnhealthyHostDropsItsHealthRow. A hidden machine is hidden whole:
// leaving the "unreachable" row behind would keep the machine on screen while
// claiming its workspaces do not exist.
func TestHiddenUnhealthyHostDropsItsHealthRow(t *testing.T) {
	m := hostModel(t, "builder", hosts.Health{State: hosts.StateUnreachable}, nil)
	m.syncWorkspaces()
	if len(m.hostHealthRows()) != 1 {
		t.Fatalf("fixture contributed no health row: %v", rowNames(m))
	}
	m.setHostHidden("builder", true)
	m.syncWorkspaces()
	if rows := m.hostHealthRows(); len(rows) != 0 {
		t.Errorf("a hidden host still reports its health as a row: %v", rows)
	}
}

// TestSortControlMarksHiddenRemotes: the mark, and only when it means
// something. A count appears at two, not at one — the user who hid a single
// machine does not need to be told the number is one.
func TestSortControlMarksHiddenRemotes(t *testing.T) {
	if note := hiddenHostsNote(0); note != "" {
		t.Errorf("nothing hidden but the control is marked: %q", note)
	}
	if note := hiddenHostsNote(1); note != workspacelist.HostHiddenGlyph {
		t.Errorf("one hidden host = %q, want the bare struck glyph %q", note, workspacelist.HostHiddenGlyph)
	}
	if note := hiddenHostsNote(3); note != workspacelist.HostHiddenGlyph+"3" {
		t.Errorf("three hidden hosts = %q, want the struck glyph and a count", note)
	}
}

// TestSortPillCarriesTheHiddenMark drives it through the rendered header, so
// the mark is proven on the control a user actually clicks.
func TestSortPillCarriesTheHiddenMark(t *testing.T) {
	m := healthyHost(t, "mac-mini")
	m.syncWorkspaces()
	header := ansi.Strip(m.renderWorkspaceList(0, 0, 70, 30))
	if strings.Contains(header, workspacelist.HostHiddenGlyph) {
		t.Fatalf("the header is marked with nothing hidden:\n%s", header)
	}

	m.setHostHidden("mac-mini", true)
	m.syncWorkspaces()
	header = ansi.Strip(m.renderWorkspaceList(0, 0, 70, 30))
	if !strings.Contains(header, workspacelist.HostHiddenGlyph) {
		t.Errorf("a hidden host left no mark on the sort control:\n%s", header)
	}
}

// TestHiddenMarkSurvivesTheNarrowHeader. The mark matters most where the panel
// is narrowest, so it must not be what the degradation ladder sheds first.
func TestHiddenMarkSurvivesTheNarrowHeader(t *testing.T) {
	m := healthyHost(t, "mac-mini")
	m.setHostHidden("mac-mini", true)
	m.syncWorkspaces()
	// 22 is the narrowest header that keeps a sort control at all — with
	// nothing hidden it shows the bare sort glyph there. The mark must last
	// exactly as long, which is what a mark-only rung in the ladder buys.
	for _, width := range []int{70, 40, 30, 24, 22} {
		header := ansi.Strip(m.renderWorkspaceList(0, 0, width, 30))
		if !strings.Contains(header, workspacelist.HostHiddenGlyph) {
			t.Errorf("at width %d the hidden mark was dropped:\n%s", width, header)
		}
	}
}

// TestHiddenCountIgnoresUnconfiguredHosts: a leftover entry for a machine the
// user has since deleted must not advertise rows nothing can restore.
func TestHiddenCountIgnoresUnconfiguredHosts(t *testing.T) {
	m := healthyHost(t, "mac-mini")
	m.setHostHidden("mac-mini", true)
	m.setHostHidden("a-machine-that-is-gone", true)
	if got := m.hiddenHostCount(); got != 1 {
		t.Errorf("hiddenHostCount = %d, want 1 — only configured machines count", got)
	}
}

// TestPruneKeepsHiddenHostsWhenNothingIsConfigured. "Every host was removed"
// and "the feature is switched off" look the same from here, so an empty
// configured set is never taken as permission to forget the user's choices.
func TestPruneKeepsHiddenHostsWhenNothingIsConfigured(t *testing.T) {
	m := healthyHost(t, "mac-mini")
	m.setHostHidden("mac-mini", true)
	m.hostConfiguredIDs = nil
	if m.pruneHiddenHosts() {
		t.Error("pruning cleared the hidden set with no host configured")
	}
	if !m.hiddenHosts["mac-mini"] {
		t.Error("the hidden entry was dropped anyway")
	}

	m.hostConfiguredIDs = []string{"builder"}
	if !m.pruneHiddenHosts() {
		t.Error("pruning kept an entry for a machine that is no longer configured")
	}
	if m.hiddenHosts["mac-mini"] {
		t.Error("a de-registered machine kept its hiding decision")
	}
}

// TestRemotesSectionAppearsOnlyWithHosts. A checkbox group for a feature the
// user has not set up is chrome that explains nothing.
func TestRemotesSectionAppearsOnlyWithHosts(t *testing.T) {
	m := catalogModel(t)
	m.width, m.height = 100, 40
	m.openViewFlyout()
	if body := ansi.Strip(m.viewFlyout.Render(100, 40, m.viewFlyoutMouse)); strings.Contains(body, viewFlyoutRemotesText) {
		t.Errorf("the remotes section is drawn with no host configured:\n%s", body)
	}

	remote := healthyHost(t, "mac-mini")
	remote.width, remote.height = 100, 40
	remote.openViewFlyout()
	body := ansi.Strip(remote.viewFlyout.Render(100, 40, remote.viewFlyoutMouse))
	if !strings.Contains(body, viewFlyoutRemotesText) {
		t.Fatalf("no remotes toggle with a host configured:\n%s", body)
	}
	if !strings.Contains(body, "mac-mini") {
		t.Errorf("the remotes section does not list the machine:\n%s", body)
	}
	if !strings.Contains(body, workspacelist.HostGlyph+" "+viewFlyoutRemotesText) {
		t.Errorf("the remotes toggle is not marked with the host glyph:\n%s", body)
	}
}

// TestFilterLineOnlyWhenFiltering. "Filter: none" is a row of chrome spent
// saying nothing, and it dilutes the line that matters when a query is live.
func TestFilterLineOnlyWhenFiltering(t *testing.T) {
	m := catalogModel(t)
	m.width, m.height = 100, 40
	m.openViewFlyout()
	if body := ansi.Strip(m.viewFlyout.Render(100, 40, m.viewFlyoutMouse)); strings.Contains(body, "Filter") {
		t.Errorf("the flyout shows a filter line with no filter active:\n%s", body)
	}

	m.workspaces.Filter().SetQuery("modal")
	if body := ansi.Strip(m.viewFlyout.Render(100, 40, m.viewFlyoutMouse)); !strings.Contains(body, "Filter: modal") {
		t.Errorf("a live query is not reported in the flyout:\n%s", body)
	}
}

// TestRemoteCheckboxTogglesOneMachine, through the same action path a click
// and a keypress both take.
func TestRemoteCheckboxTogglesOneMachine(t *testing.T) {
	m := healthyHost(t, "mac-mini")
	m.hostConfiguredIDs = []string{"builder", "mac-mini"}
	m.width, m.height = 100, 40
	m.openViewFlyout()

	m.applyViewFlyoutAction(viewFlyoutHostID("mac-mini"), m.viewFlyoutSnapshot())
	if !m.hiddenHosts["mac-mini"] {
		t.Fatal("toggling a remote's checkbox did not hide it")
	}
	if m.hiddenHosts["builder"] {
		t.Error("hiding one machine hid another")
	}
	if names := rowNames(m); len(names) != 0 {
		t.Errorf("rows survived the machine being hidden: %v", names)
	}

	m.applyViewFlyoutAction(viewFlyoutHostID("mac-mini"), m.viewFlyoutSnapshot())
	if m.hiddenHosts["mac-mini"] {
		t.Error("toggling the checkbox again did not bring the machine back")
	}
	if len(m.workspaces.Items()) == 0 {
		t.Error("the rows did not come back with the machine")
	}
}

// TestRemotesMasterToggleMovesEveryMachine, and reads back the state of the
// group rather than a value of its own.
func TestRemotesMasterToggleMovesEveryMachine(t *testing.T) {
	m := healthyHost(t, "mac-mini")
	m.hostConfiguredIDs = []string{"builder", "mac-mini"}
	m.width, m.height = 100, 40
	m.openViewFlyout()
	if !m.viewFlyoutShowHosts {
		t.Fatal("the master toggle starts off with every machine shown")
	}

	// A click reports the checkbox without flipping the bound value.
	m.applyViewFlyoutAction(viewFlyoutRemotesID, m.viewFlyoutSnapshot())
	if !m.hiddenHosts["builder"] || !m.hiddenHosts["mac-mini"] {
		t.Fatalf("the master toggle left machines visible: %v", m.hiddenHosts)
	}
	if m.viewFlyoutShowHosts {
		t.Error("the master toggle still reads as on with everything hidden")
	}

	m.applyViewFlyoutAction(viewFlyoutRemotesID, m.viewFlyoutSnapshot())
	if len(m.hiddenHosts) != 0 {
		t.Errorf("the master toggle did not restore every machine: %v", m.hiddenHosts)
	}
	if !m.viewFlyoutShowHosts {
		t.Error("the master toggle reads as off with everything shown")
	}
}

// TestMasterTogglesFollowThePerHostBoxes: the master is a reading of the
// group, so checking one machine back on turns it on.
func TestMasterFollowsThePerHostBoxes(t *testing.T) {
	m := healthyHost(t, "mac-mini")
	m.hostConfiguredIDs = []string{"builder", "mac-mini"}
	m.width, m.height = 100, 40
	m.openViewFlyout()

	m.applyViewFlyoutAction(viewFlyoutHostID("mac-mini"), m.viewFlyoutSnapshot())
	m.applyViewFlyoutAction(viewFlyoutHostID("builder"), m.viewFlyoutSnapshot())
	if m.viewFlyoutShowHosts {
		t.Fatal("every machine is hidden but the master still reads as on")
	}
	m.applyViewFlyoutAction(viewFlyoutHostID("builder"), m.viewFlyoutSnapshot())
	if !m.viewFlyoutShowHosts {
		t.Error("one machine is back but the master still reads as off")
	}
}

// TestHiddenHostsArePersisted. The choice outlives the session; the list a
// user left is the list they come back to.
func TestHiddenHostsArePersisted(t *testing.T) {
	original := saveSessionsHiddenHosts
	var saved []string
	saveSessionsHiddenHosts = func(ids []string) error {
		saved = append([]string(nil), ids...)
		return nil
	}
	t.Cleanup(func() { saveSessionsHiddenHosts = original })

	m := healthyHost(t, "mac-mini")
	m.hostConfiguredIDs = []string{"builder", "mac-mini"}
	m.width, m.height = 100, 40
	m.openViewFlyout()
	m.applyViewFlyoutAction(viewFlyoutHostID("mac-mini"), m.viewFlyoutSnapshot())

	if len(saved) != 1 || saved[0] != "mac-mini" {
		t.Errorf("saved hidden hosts = %v, want [mac-mini]", saved)
	}
}

// TestHidingAHostKeepsItsPins. Hiding is a view filter; pruning pins against
// the catalog is how "this workspace was deleted" is detected. A hidden row is
// not deleted, and dropping its pin would be a one-way door: unhiding brings
// the row back unpinned, with nothing to undo.
func TestHidingAHostKeepsItsPins(t *testing.T) {
	original := savePinnedWorkspaceIDs
	var saved []string
	savePinnedWorkspaceIDs = func(ids []string) error {
		saved = append([]string(nil), ids...)
		return nil
	}
	t.Cleanup(func() { savePinnedWorkspaceIDs = original })

	m := healthyHost(t, "mac-mini")
	m.syncWorkspaces()
	var remoteID string
	for id := range m.catalog {
		remoteID = id
	}
	if remoteID == "" {
		t.Fatal("fixture contributed no remote row")
	}
	m.workspaces.SetPinned([]string{remoteID})

	m.setHostHidden("mac-mini", true)
	m.syncWorkspaces()

	if pins := m.workspaces.PinnedIDs(); len(pins) != 1 || pins[0] != remoteID {
		t.Errorf("hiding a machine dropped its pin: %v", pins)
	}
	if saved != nil {
		t.Errorf("hiding a machine rewrote the persisted pins to %v", saved)
	}
}

// TestHiddenHostStaysInTheCatalog. The catalog is what the browser knows
// exists — pins, live terminal splits and the remembered selection are all
// keyed off it, and a hidden row missing from it reads to every one of them as
// a row that was deleted.
func TestHiddenHostStaysInTheCatalog(t *testing.T) {
	m := healthyHost(t, "mac-mini")
	m.syncWorkspaces()
	before := len(m.catalog)
	if before == 0 {
		t.Fatal("fixture contributed no catalog rows")
	}
	m.setHostHidden("mac-mini", true)
	m.syncWorkspaces()
	if got := len(m.catalog); got != before {
		t.Errorf("catalog went from %d rows to %d when a machine was hidden", before, got)
	}
	if len(m.workspaces.Items()) != 0 {
		t.Error("the hidden machine's rows are still listed")
	}
}

// TestHiddenHostKeepsTheRememberedSelection. A row being withheld is not a row
// that went away, so the restore stays pending rather than the hide quietly
// rewriting which row the user comes back to.
func TestHiddenHostKeepsTheRememberedSelection(t *testing.T) {
	m := healthyHost(t, "mac-mini")
	m.syncWorkspaces()
	var remoteID string
	for id := range m.catalog {
		remoteID = id
	}
	m.pendingRestoreSelected = remoteID
	m.setHostHidden("mac-mini", true)
	m.syncWorkspaces()
	if m.pendingRestoreSelected != remoteID {
		t.Errorf("the remembered row was forgotten when its machine was hidden: %q", m.pendingRestoreSelected)
	}
}

// TestHiddenHostIsNotACreateTarget: creating there would produce a row the
// browser then withholds, which reads as a create that did nothing.
func TestHiddenHostIsNotACreateTarget(t *testing.T) {
	m := healthyHost(t, "mac-mini")
	m.syncWorkspaces()
	found := false
	for _, item := range m.createProjectItems() {
		if strings.Contains(item.Label, "mac-mini") {
			found = true
		}
	}
	if !found {
		t.Fatal("the machine is not a create target even while shown")
	}

	m.setHostHidden("mac-mini", true)
	m.syncWorkspaces()
	for _, item := range m.createProjectItems() {
		if strings.Contains(item.Label, "mac-mini") {
			t.Errorf("a hidden machine is still offered as a create target: %q", item.Label)
		}
	}
}

// TestHiddenHostDeclinesPaneRequests. A pane request is answered by the
// screen, and a hidden machine's row is not on it. Declining with a reason
// beats composing onto a surface the user is not looking at.
func TestHiddenHostDeclinesPaneRequests(t *testing.T) {
	m := healthyHost(t, "mac-mini")
	m.syncWorkspaces()
	var remoteID string
	for id := range m.catalog {
		remoteID = id
	}
	req := uirequest.Request{Origin: uirequest.Origin{Sessions: true, SessionsRow: remoteID}}
	if _, _, ok, reason := m.resolveSessionsLayoutRow(req); !ok {
		t.Fatalf("a shown machine's row was refused: %s", reason)
	}

	m.setHostHidden("mac-mini", true)
	m.syncWorkspaces()
	_, _, ok, reason := m.resolveSessionsLayoutRow(req)
	if ok {
		t.Fatal("a hidden machine's row still accepted a pane request")
	}
	if !strings.Contains(reason, "hidden") {
		t.Errorf("the refusal does not say why: %q", reason)
	}
}
