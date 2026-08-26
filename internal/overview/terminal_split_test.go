package overview

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/termpanes"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/workspacecreate"
)

func createOverviewTerminalSplit(t *testing.T, placement workspacecreate.Placement) (*Model, *termpanes.Leaf) {
	t.Helper()
	m, _ := previewModel(t)
	m.preview.visible = true
	m.bindPreview(false)
	original := ensurePreviewTerminalSession
	ensurePreviewTerminalSession = func(session, workDir string) (string, error) {
		if !strings.HasPrefix(session, termpanes.SessionPrefix) || workDir != "/tmp/sidecar-alpha" {
			t.Fatalf("session seed = %q in %q", session, workDir)
		}
		return "%peer", nil
	}
	t.Cleanup(func() { ensurePreviewTerminalSession = original })
	m.OpenPaneSwitcher()
	m.createForm.SetKind(workspacecreate.KindTerminalSplit)
	m.createForm.SetPlacement(placement)
	cmd := m.createPreviewTerminalSplit()
	if cmd == nil {
		t.Fatalf("create command is nil: %s", m.createError)
	}
	msg := cmd().(previewTerminalSplitCreatedMsg)
	leaf := m.preview.terminalPanes.Leaf(msg.LeafID)
	if leaf == nil {
		t.Fatal("terminal split did not attach collection state")
	}
	m.applyPreviewTerminalSplitCreated(msg)
	return m, leaf
}

func terminalSplitParent(root *panelayout.Node, leafID int) *panelayout.Node {
	if root == nil || root.Split == nil {
		return nil
	}
	if root.Split.A.ID == leafID || root.Split.B.ID == leafID {
		return root
	}
	if parent := terminalSplitParent(root.Split.A, leafID); parent != nil {
		return parent
	}
	return terminalSplitParent(root.Split.B, leafID)
}

func TestOverviewTerminalSplitCreateAndPlacement(t *testing.T) {
	tests := []struct {
		name      string
		placement workspacecreate.Placement
		wantAxis  panelayout.Axis
	}{
		{name: "auto", placement: workspacecreate.PlacementAuto, wantAxis: panelayout.Columns},
		{name: "right", placement: workspacecreate.PlacementRight, wantAxis: panelayout.Columns},
		{name: "below", placement: workspacecreate.PlacementBelow, wantAxis: panelayout.Rows},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			m, leaf := createOverviewTerminalSplit(t, test.placement)
			if leaf.PaneID != "%peer" || leaf.Target.Source != "shell" || leaf.Name == "" {
				t.Fatalf("created leaf = %+v", leaf)
			}
			parent := terminalSplitParent(m.preview.paneRoot, leaf.ID)
			if parent == nil || parent.Split.Axis != test.wantAxis {
				t.Fatalf("placement parent = %+v, want axis %v", parent, test.wantAxis)
			}
		})
	}
}

func TestOverviewTerminalSplitCapRefusesCreate(t *testing.T) {
	m, _ := createOverviewTerminalSplit(t, workspacecreate.PlacementAuto)
	m.OpenPaneSwitcher()
	m.createForm.SetKind(workspacecreate.KindTerminalSplit)
	if got := m.createForm.KindDisabledReason(); got != termpanes.CapDisabledReason {
		t.Fatalf("disabled reason = %q", got)
	}
	for _, action := range []string{
		workspacecreate.ActionCreate,
		workspacecreate.ActionPlaceAuto,
		workspacecreate.ActionPlaceRight,
		workspacecreate.ActionPlaceBelow,
	} {
		if cmd := m.applyCreateAction(action); cmd != nil {
			t.Fatalf("cap-refused action %s returned a command", action)
		}
		if panelayout.LiveLeafCount(m.preview.paneRoot) != panelayout.LiveLeafCap {
			t.Fatalf("cap-refused action %s changed the live leaf count", action)
		}
	}
}

func TestOverviewTerminalSplitSurvivesRowNavigation(t *testing.T) {
	m, leaf := createOverviewTerminalSplit(t, workspacecreate.PlacementAuto)
	leaf.Scroll = 17
	leaf.Name = "build logs"
	m.workspaces.SelectID("b")
	m.bindPreview(false)
	if got := m.preview.terminalPanes.Leaf(leaf.ID); got != nil {
		t.Fatalf("row B adopted row A terminal split: leafID=%d got source=%q id=%d", leaf.ID, got.Target.Source, got.ID)
	}
	m.workspaces.SelectID("a")
	m.bindPreview(false)
	if got := m.preview.terminalPanes.Leaf(leaf.ID); got != leaf || got.Scroll != 17 || got.Name != "build logs" {
		t.Fatalf("row A terminal state was not reattached: got=%p %+v want=%p", got, got, leaf)
	}
}

func TestOverviewTerminalSplitReleasesDeletedRow(t *testing.T) {
	m, leaf := createOverviewTerminalSplit(t, workspacecreate.PlacementAuto)
	m.workspaces.SelectID("b")
	m.bindPreview(false)
	result := m.results["sidecar"]
	result.Workspaces = result.Workspaces[1:]
	m.results["sidecar"] = result
	m.syncBoard()
	if cached := m.preview.paneCache["a"].terminals; cached != nil && cached.Leaf(leaf.ID) != nil {
		t.Fatal("deleted row retained terminal peer state")
	}
}

func TestOverviewTerminalSplitCloseAndSessionEnd(t *testing.T) {
	t.Run("explicit close keeps primary and kills peer", func(t *testing.T) {
		m, leaf := createOverviewTerminalSplit(t, workspacecreate.PlacementAuto)
		original := killPreviewTerminalSession
		var killed string
		killPreviewTerminalSession = func(session string) tea.Cmd { killed = session; return nil }
		t.Cleanup(func() { killPreviewTerminalSession = original })
		m.closePreviewShellLeaf(leaf.ID, termpanes.CloseExplicit)
		if killed != leaf.Session || panelayout.FirstOfKind(m.preview.paneRoot, panelayout.Terminal) == nil || panelayout.FirstOfKind(m.preview.paneRoot, panelayout.Shell) != nil {
			t.Fatalf("explicit close killed=%q tree=%+v", killed, m.preview.paneRoot)
		}
	})
	t.Run("ended session closes without kill", func(t *testing.T) {
		m, leaf := createOverviewTerminalSplit(t, workspacecreate.PlacementAuto)
		original := killPreviewTerminalSession
		killPreviewTerminalSession = func(string) tea.Cmd { t.Fatal("ended session was killed again"); return nil }
		t.Cleanup(func() { killPreviewTerminalSession = original })
		m.applyPreviewSplitCloseProbe(previewSplitCloseProbeMsg{WorkspaceID: m.preview.workspaceID, LeafID: leaf.ID, Session: leaf.Session, Err: errors.New("gone")})
		if panelayout.FirstOfKind(m.preview.paneRoot, panelayout.Shell) != nil {
			t.Fatal("ended terminal split stayed in the tree")
		}
	})
}

func TestOverviewTerminalSplitCloseConfirmsRunningProcess(t *testing.T) {
	m, leaf := createOverviewTerminalSplit(t, workspacecreate.PlacementAuto)
	m.applyPreviewSplitCloseProbe(previewSplitCloseProbeMsg{
		WorkspaceID: m.preview.workspaceID, LeafID: leaf.ID, Session: leaf.Session,
		Evidence: termpanes.CloseEvidence{CurrentCommand: "node", ShellCommand: "zsh"},
	})
	if m.previewSplitCloseLeaf != leaf.ID {
		t.Fatal("running peer did not open a leaf-scoped confirmation")
	}
	if handled, _ := m.handlePreviewSplitCloseKey(tea.KeyPressMsg{Code: tea.KeyEscape}); !handled || m.previewSplitCloseLeaf != 0 {
		t.Fatal("close confirmation did not cancel cleanly")
	}
}

func TestOverviewTerminalGeometrySettlesEveryLiveLeaf(t *testing.T) {
	m, leaf := createOverviewTerminalSplit(t, workspacecreate.PlacementAuto)
	primary := m.primaryTerminalState().terminal.(*fakeTerminal)
	peer := m.terminalState(leaf.ID).terminal.(*fakeTerminal)
	primaryBefore, peerBefore := len(primary.dims), len(peer.dims)
	m.syncTerminalGeometry()
	if got := len(primary.dims) - primaryBefore; got != 1 {
		t.Fatalf("primary resize count = %d, want 1", got)
	}
	if got := len(peer.dims) - peerBefore; got != 1 {
		t.Fatalf("peer resize count = %d, want 1", got)
	}
}

func TestReviewTerminalMessagesReachEveryLiveLeaf(t *testing.T) {
	m, leaf := createOverviewTerminalSplit(t, workspacecreate.PlacementAuto)
	primaryLeaf := m.primaryTerminalLeaf()
	m.preview.paneFocus = primaryLeaf.ID
	primary := m.primaryTerminalState().terminal.(*fakeTerminal)
	peer := m.terminalState(leaf.ID).terminal.(*fakeTerminal)

	m.WorkspacesTerminalMsg(tty.CaptureResultMsg{Output: "fanout frame"})
	if got := primary.buffer.String(); got != "fanout frame" {
		t.Fatalf("focused primary output = %q", got)
	}
	if got := peer.buffer.String(); got != "fanout frame" {
		t.Fatalf("unfocused peer output = %q; terminal message was not fanned out", got)
	}
}

func TestOverviewTerminalSplitRenameTargetsLeaf(t *testing.T) {
	m, leaf := createOverviewTerminalSplit(t, workspacecreate.PlacementAuto)
	m.OpenRenameTerminalLeaf(leaf.ID)
	m.renameInput.SetValue("peer build")
	if cmd := m.executeRename(); cmd != nil {
		t.Fatal("peer rename unexpectedly persisted a catalog shell")
	}
	if leaf.Name != "peer build" || m.renameOpen {
		t.Fatalf("peer rename = %q open=%v", leaf.Name, m.renameOpen)
	}
}
