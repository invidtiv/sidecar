package overview

import (
	"context"
	"reflect"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/hosts"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/panereposition"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

func TestSessionsPaneLayoutHeaderModalPreservesIdentityAndZoom(t *testing.T) {
	m := linkPreviewModel(t, workspaceinventory.KindWorktree)
	run(t, m, openPreviewDocSpan(m, mustPreviewSpan(t, m, previewNeedleAction(t, m, "README.md"))))
	m.preview.focus = focusPreview
	doc := panelayout.FirstOfKind(m.preview.paneRoot, panelayout.Document)
	if doc == nil {
		t.Fatal("fixture has no document leaf")
	}
	m.preview.paneFocus = doc.ID
	before := globalMoveGridIDs(m.preview.paneRoot)
	docBefore := doc
	enableGlobalPaneMove(t)
	m.WorkspacesView(previewWide, previewTall)

	var layout *mouse.Region
	for _, region := range m.workspacesMouse.HitMap.Regions() {
		if region.ID == previewPaneLayoutKind {
			if id, ok := region.Data.(previewPaneLayoutHit); ok && int(id) == doc.ID {
				copy := region
				layout = &copy
				break
			}
		}
	}
	if layout == nil {
		t.Fatal("document header registered no layout region")
	}
	m.WorkspacesMouse(tea.MouseClickMsg(tea.Mouse{X: layout.Rect.X, Y: layout.Rect.Y, Button: tea.MouseLeft}))
	if m.paneLayoutModal == nil || m.WorkspaceFocusContext() != panereposition.ModalContext {
		t.Fatalf("layout click did not open modal/context: modal=%v context=%q", m.paneLayoutModal, m.WorkspaceFocusContext())
	}
	m.WorkspacesKey(globalMoveKey('h'))
	m.WorkspacesKey(globalMoveKey('z'))
	m.WorkspacesKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.paneLayoutModal != nil {
		t.Fatal("enter did not close modal")
	}
	if panelayout.Find(m.preview.paneRoot, doc.ID) != docBefore {
		t.Fatal("Sessions modal commit replaced the moved leaf")
	}
	if after := globalMoveGridIDs(m.preview.paneRoot); reflect.DeepEqual(after, before) {
		t.Fatalf("modal move left grid unchanged: %v", after)
	}
	peer, ok := m.previewPeerBox()
	if !ok {
		t.Fatal("preview peer disappeared")
	}
	layoutTree, ok := m.layoutPreviewPanes(peer)
	if !ok || !layoutTree.Zoomed || len(layoutTree.Leaves) != 1 || layoutTree.Leaves[0].Node.ID != doc.ID {
		t.Fatalf("zoom did not follow moved Sessions leaf: %+v", layoutTree)
	}
}

func TestSessionsPaneLayoutModalAbsorbsPasteBeforeHiddenFilter(t *testing.T) {
	m := linkPreviewModel(t, workspaceinventory.KindWorktree)
	run(t, m, openPreviewDocSpan(m, mustPreviewSpan(t, m, previewNeedleAction(t, m, "README.md"))))
	doc := panelayout.FirstOfKind(m.preview.paneRoot, panelayout.Document)
	if doc == nil {
		t.Fatal("fixture has no document leaf")
	}
	m.workspaces.Filter().Focus()
	m.workspaces.Filter().Insert("kept")
	m.openPaneLayoutModal(doc.ID)
	if m.paneLayoutModal == nil {
		t.Fatal("fixture did not open pane layout modal")
	}

	if !m.WorkspacesPaste("-pasted") {
		t.Fatal("open Sessions pane modal did not absorb paste")
	}
	if got := m.workspaces.Filter().Query(); got != "kept" {
		t.Fatalf("paste escaped modal into hidden Sessions filter: %q", got)
	}
}

func TestSessionsRemoteLayoutModalReleasesInputLeaseBeforeDraft(t *testing.T) {
	enableGlobalPaneMove(t)
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
	m.width, m.height, m.sidebarVisible = previewWide, previewTall, true
	m.syncPreviewTerminal()
	_ = m.enterPreviewInteractive()
	leaf := panelayout.FirstOfKind(m.preview.paneRoot, panelayout.Terminal)
	if terminal == nil || terminal.activated != 1 || leaf == nil || !m.PreviewInteractive() {
		t.Fatalf("remote fixture did not own input: terminal=%+v leaf=%+v", terminal, leaf)
	}

	m.openPaneLayoutModal(leaf.ID)

	if terminal.released != 1 || !terminal.active {
		t.Fatalf("modal entry did not release remote input/lease while preserving control: released=%d active=%v", terminal.released, terminal.active)
	}
	if m.PreviewInteractive() || m.paneLayoutModal == nil {
		t.Fatalf("draft began before remote input release: interactive=%v modal=%v", m.PreviewInteractive(), m.paneLayoutModal)
	}
}
