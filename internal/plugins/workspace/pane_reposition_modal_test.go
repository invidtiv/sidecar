package workspace

import (
	"reflect"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/tty"
)

func TestProjectPaneLayoutModalReleasesInteractiveInputBeforeDraft(t *testing.T) {
	enableWorkspaceFeature(t, features.PaneMove.Name)
	root := t.TempDir()
	writeDocPaneFixture(t, root, "README.md", "# move\n")
	p := docPaneTestPlugin(t, root, false)
	p.openTerminalPath("README.md", 1)
	primary := panelayout.FirstOfKind(p.paneRoot, panelayout.Terminal)
	if primary == nil {
		t.Fatal("fixture has no primary terminal")
	}
	terminal := tty.New(nil)
	terminal.ExitAction = tty.ExitReleasesInput
	terminal.State = &tty.State{Active: true, EscapePressed: true, EscapeTimerPending: true}
	p.primaryTermPane().Terminal = terminal
	p.viewMode = ViewModeInteractive
	p.interactiveState = &InteractiveState{Active: true, LeafID: primary.ID}

	p.openPaneLayoutModal(primary.ID)

	if p.paneLayoutModal == nil {
		t.Fatal("interactive header entry did not create the layout modal")
	}
	if p.viewMode != ViewModeList || p.interactiveState != nil {
		t.Fatalf("modal drafted before host left interactive mode: mode=%v state=%+v", p.viewMode, p.interactiveState)
	}
	if terminal.State == nil || terminal.State.EscapePressed || terminal.State.EscapeTimerPending {
		t.Fatalf("terminal input state was not released first: %+v", terminal.State)
	}
	if !terminal.IsActive() {
		t.Fatal("releasing input closed the watched terminal/control subscription")
	}
}

func TestProjectPaneLayoutModalAbsorbsPasteBeforeHiddenFilter(t *testing.T) {
	enableWorkspaceFeature(t, features.PaneMove.Name)
	root := t.TempDir()
	writeDocPaneFixture(t, root, "README.md", "# move\n")
	p := docPaneTestPlugin(t, root, false)
	p.openTerminalPath("README.md", 1)
	doc := panelayout.FirstOfKind(p.paneRoot, panelayout.Document)
	if doc == nil {
		t.Fatal("fixture has no document leaf")
	}
	p.listFilter.Focus()
	p.listFilter.Insert("kept")
	p.openPaneLayoutModal(doc.ID)
	if p.paneLayoutModal == nil {
		t.Fatal("fixture did not open pane layout modal")
	}

	p.Update(tea.PasteMsg{Content: "-pasted"})

	if got := p.listFilter.Query(); got != "kept" {
		t.Fatalf("paste escaped modal into hidden project filter: %q", got)
	}
}

func TestProjectPaneLayoutModalCommitsIdentityAndZoom(t *testing.T) {
	enableWorkspaceFeature(t, features.PaneMove.Name)
	root := t.TempDir()
	writeDocPaneFixture(t, root, "README.md", "# move\n")
	p := docPaneTestPlugin(t, root, false)
	p.openTerminalPath("README.md", 1)
	doc := panelayout.FirstOfKind(p.paneRoot, panelayout.Document)
	if doc == nil || p.contentDeck == nil {
		t.Fatal("fixture has no document/deck")
	}
	before := moveGridIDs(p.paneRoot)
	docBefore := doc
	p.openPaneLayoutModal(doc.ID)
	p.handlePaneLayoutModalKey(tea.KeyPressMsg{Code: 'h', Text: "h"})
	p.handlePaneLayoutModalKey(tea.KeyPressMsg{Code: 'z', Text: "z"})
	p.handlePaneLayoutModalKey(tea.KeyPressMsg{Code: tea.KeyEnter})

	if p.paneLayoutModal != nil || panelayout.Find(p.paneRoot, doc.ID) != docBefore {
		t.Fatal("project modal stayed open or replaced the moved leaf")
	}
	if after := moveGridIDs(p.paneRoot); reflect.DeepEqual(after, before) {
		t.Fatalf("modal move left grid unchanged: %v", after)
	}
	if got := moveGridIDs(p.contentDeck.Tree()); !reflect.DeepEqual(got, moveGridIDs(p.paneRoot)) {
		t.Fatalf("deck did not adopt project modal move: %v", got)
	}
	peer, ok := p.previewPeerBox()
	if !ok {
		t.Fatal("preview peer disappeared")
	}
	zoom := p.paneZoom.Leaf(p.paneLayoutModalScope(), p.paneRoot)
	layout, ok := LayoutPaneTreeWithZoom(p.paneRoot, peer, paneTreeFloors(), p.paneFocus, zoom)
	if !ok || !layout.Zoomed || len(layout.Leaves) != 1 || layout.Leaves[0].Node.ID != doc.ID {
		t.Fatalf("zoom did not follow project leaf: %+v", layout)
	}
}
