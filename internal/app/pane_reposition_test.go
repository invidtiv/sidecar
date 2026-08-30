package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/contentlink"
	"github.com/marcus/sidecar/internal/docview"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/inlineedit"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/overview"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/panereposition"
	"github.com/marcus/sidecar/internal/state"
	"github.com/marcus/sidecar/internal/ui"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

func TestAppPaneLayoutHeaderModalCommitsMoveAndZoom(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# pane\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := &deckHostTestPlugin{id: "files", focus: "preview", frame: "primary"}
	m := appDeckTestModel(t, root, p)
	features.SetOverride(features.PaneMove.Name, true)
	m.renderContent(200, 40)
	if cmd := m.openAppContent(root, p.id, contentlink.Ref{Kind: contentlink.KindFile, Value: "README.md"}); cmd == nil {
		t.Fatal("document open returned no load command")
	}
	m.renderContent(200, 40)
	h := m.currentContentDeck()
	docID := h.deck.Leaf(panelayout.Document)
	docBefore := panelayout.Find(h.root, docID)
	beforeGrid := gridIDs(h.root)
	if docBefore == nil {
		t.Fatal("host projection has no document leaf")
	}

	var layout *mouse.Region
	for _, region := range h.mouse.HitMap.Regions() {
		if region.ID == appDeckLayoutRegion {
			copy := region
			layout = &copy
			break
		}
	}
	if layout == nil {
		t.Fatal("document header registered no layout region")
	}
	h.deck.FocusLeaf(docID)
	h.openAppContentFinder()
	if !h.appContentSearchActive() {
		t.Fatal("fixture did not open a document input surface")
	}
	if resolved := h.mouse.HitMap.Test(layout.Rect.X+layout.Rect.W/2, layout.Rect.Y); resolved == nil || resolved.ID != appDeckLayoutRegion {
		t.Fatalf("layout glyph resolves to %#v", resolved)
	}
	m.appContentMouse(tea.MouseClickMsg(tea.Mouse{X: layout.Rect.X, Y: layout.Rect.Y, Button: tea.MouseLeft}))
	if h.appContentSearchActive() {
		t.Fatal("layout controller opened before releasing the document input surface")
	}
	if m.activeModal() != ModalPaneReposition || h.layoutModal == nil || m.activeContext != panereposition.ModalContext {
		t.Fatalf("layout click did not own app modal routing: modal=%v context=%q", m.activeModal(), m.activeContext)
	}

	m.handleAppPaneLayoutKey(tea.KeyPressMsg{Code: 'h', Text: "h"})
	m.handleAppPaneLayoutKey(tea.KeyPressMsg{Code: 'z', Text: "z"})
	m.handleAppPaneLayoutKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if h.layoutModal != nil || m.activeModal() == ModalPaneReposition {
		t.Fatal("enter did not close the pane reposition modal")
	}
	if panelayout.Find(h.root, docID) != docBefore {
		t.Fatal("app host replaced the moved projection leaf")
	}
	if after := gridIDs(h.root); reflect.DeepEqual(after, beforeGrid) {
		t.Fatalf("move left the app deck grid unchanged: %+v", after)
	}
	if got := gridIDs(h.deck.Tree()); !reflect.DeepEqual(got, gridIDs(h.root)) {
		t.Fatalf("deck did not adopt modal move: deck=%v host=%v", got, gridIDs(h.root))
	}
	m.renderContent(200, 40)
	if !h.layout.Zoomed || len(h.layout.Leaves) != 1 || h.layout.Leaves[0].Node.ID != docID {
		t.Fatalf("committed zoom did not follow moved leaf: %+v", h.layout)
	}
}

// appDeckMoveFixture opens a document beside the primary plugin leaf, focuses
// it, and returns the model with pane_move on. The deck is the app's own, so
// every assertion below travels through the real app key ladder.
func appDeckMoveFixture(t *testing.T) (*Model, *appContentDeck, int) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# pane\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := &deckHostTestPlugin{id: "file-browser", focus: "preview", frame: "primary"}
	m := appDeckTestModel(t, root, p)
	features.SetOverride(features.PaneMove.Name, true)
	t.Cleanup(func() { features.SetOverride(features.PaneMove.Name, features.PaneMove.Default) })
	m.renderContent(200, 40)
	if cmd := m.openAppContent(root, p.id, contentlink.Ref{Kind: contentlink.KindFile, Value: "README.md"}); cmd == nil {
		t.Fatal("document open returned no load command")
	}
	m.renderContent(200, 40)
	h := m.currentContentDeck()
	if h == nil {
		t.Fatal("app content deck was not created")
	}
	docID := h.deck.Leaf(panelayout.Document)
	if docID == 0 {
		t.Fatal("document leaf did not open")
	}
	h.deck.FocusLeaf(docID)
	h.syncInnerFocus()
	m.updateContext()
	return m, h, docID
}

// M3: the deck's second entry onto the shared modal. The press goes through
// Update, so it passes every rung the real app runs — inputs, editors, the
// global switch — and lands on the same controller the header ⊞ opens.
func TestAppDeckMoveKeyOpensTheSharedRepositionModal(t *testing.T) {
	m, h, docID := appDeckMoveFixture(t)
	if m.activeContext != "workspace-doc" {
		t.Fatalf("focused document leaf reports context %q, want workspace-doc", m.activeContext)
	}
	beforeGrid := gridIDs(h.root)
	docBefore := panelayout.Find(h.root, docID)

	updated, _ := m.Update(tea.KeyPressMsg{Code: 'M', Text: "M"})
	got := asAppModel(t, updated)
	if h.layoutModal == nil || h.layoutModal.LeafID() != docID {
		t.Fatalf("M did not open the deck's reposition modal for the focused leaf: %v", h.layoutModal)
	}
	if got.activeModal() != ModalPaneReposition || got.activeContext != panereposition.ModalContext {
		t.Fatalf("M left app modal routing at modal=%v context=%q", got.activeModal(), got.activeContext)
	}

	// The modal owns the keyboard from here, still through the real ladder.
	updated, _ = got.Update(tea.KeyPressMsg{Code: 'h', Text: "h"})
	got = asAppModel(t, updated)
	updated, _ = got.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	got = asAppModel(t, updated)
	if h.layoutModal != nil || got.activeModal() == ModalPaneReposition {
		t.Fatal("enter did not close the modal M opened")
	}
	if panelayout.Find(h.root, docID) != docBefore {
		t.Fatal("the committed move replaced the moved leaf instead of grafting it")
	}
	if after := gridIDs(h.root); reflect.DeepEqual(after, beforeGrid) {
		t.Fatalf("M-driven commit left the deck grid unchanged: %+v", after)
	}
	if deck := gridIDs(h.deck.Tree()); !reflect.DeepEqual(deck, gridIDs(h.root)) {
		t.Fatalf("deck did not adopt the M-driven move: deck=%v host=%v", deck, gridIDs(h.root))
	}
}

// The primary plugin leaf is the plugin's own browse surface. M there belongs to
// the plugin, and the deck must not turn it into a pane move.
func TestAppDeckMoveKeyIgnoresTheFocusedPrimaryLeaf(t *testing.T) {
	m, h, docID := appDeckMoveFixture(t)
	primary := h.deck.Leaf(panelayout.Primary)
	if primary == 0 || primary == docID {
		t.Fatalf("fixture has no distinct primary leaf: primary=%d doc=%d", primary, docID)
	}
	h.deck.FocusLeaf(primary)
	h.syncInnerFocus()
	m.updateContext()

	updated, _ := m.Update(tea.KeyPressMsg{Code: 'M', Text: "M"})
	got := asAppModel(t, updated)
	if h.layoutModal != nil || got.activeModal() == ModalPaneReposition {
		t.Fatal("M opened the reposition modal from the primary plugin leaf")
	}
}

// A deck-owned input surface types. M is text there, and the modal — which
// would take the keyboard away from the input — must not open.
func TestAppDeckMoveKeyLeavesDeckInputSurfacesAlone(t *testing.T) {
	m, h, _ := appDeckMoveFixture(t)
	h.openAppContentFinder()
	if !h.appContentSearchActive() {
		t.Fatal("fixture did not open a document input surface")
	}
	updated, _ := m.Update(tea.KeyPressMsg{Code: 'M', Text: "M"})
	got := asAppModel(t, updated)
	if h.layoutModal != nil || got.activeModal() == ModalPaneReposition {
		t.Fatal("M opened the reposition modal out of a focused deck input surface")
	}
	if !h.appContentSearchActive() {
		t.Fatal("M closed the deck input surface it should have been typed into")
	}
}

// The in-file search types into the document, so M is a character there. The
// finder above proves the deck-owned search rung; this one proves the docview
// rung under it, which the ladder answers separately.
func TestAppDeckMoveKeyLeavesTheInFileSearchAlone(t *testing.T) {
	m, h, docID := appDeckMoveFixture(t)
	view, ok := h.deck.Viewer(docID).(*docview.Model)
	if !ok {
		t.Fatalf("focused leaf %d is not a document view", docID)
	}
	view.StartSearch()
	if !view.SearchActive() {
		t.Fatal("fixture did not open the in-file search")
	}
	m.updateContext()
	if m.activeContext != "workspace-doc-find" {
		t.Fatalf("in-file search reports context %q, want workspace-doc-find", m.activeContext)
	}

	updated, _ := m.Update(tea.KeyPressMsg{Code: 'M', Text: "M"})
	got := asAppModel(t, updated)
	if h.layoutModal != nil || got.activeModal() == ModalPaneReposition {
		t.Fatal("M opened the reposition modal out of the in-file search")
	}
	if !view.SearchActive() {
		t.Fatal("M closed the in-file search instead of being typed into it")
	}
	if q := view.SearchQuery(); q != "M" {
		t.Fatalf("in-file search query = %q, want the typed M", q)
	}
}

// A live inline editor owns every key, so M is a character inside tmux. The
// editor rung sits above the deck's own keys in the ladder; this pins that
// order rather than trusting it.
func TestAppDeckMoveKeyLeavesTheInlineEditorAlone(t *testing.T) {
	m, h, docID := appDeckMoveFixture(t)
	e := h.appContentDocumentEdit(true)
	e.leafID = docID
	session := e.editor()
	session.Active = true
	session.Path = "README.md"
	t.Cleanup(h.releaseAppContentDocumentEdit)
	if !h.appContentDocumentEditing() {
		t.Fatal("fixture did not put a live editor on the focused leaf")
	}
	m.updateContext()
	// The deck would otherwise answer M here: the leaf itself is a legal target.
	if leaf := panelayout.Find(h.deck.Tree(), docID); m.appPaneMoveShortcutLeaf(h, leaf) != docID {
		t.Fatal("fixture leaf is not a move target, so the ladder order proves nothing")
	}

	updated, _ := m.Update(tea.KeyPressMsg{Code: 'M', Text: "M"})
	got := asAppModel(t, updated)
	if h.layoutModal != nil || got.activeModal() == ModalPaneReposition {
		t.Fatal("M opened the reposition modal out of a live inline editor")
	}
	// The editor rung consumed the key. Its session has no tmux behind it here,
	// so it answers by exiting — the point is that it answered, above the deck.
	if h.appContentDocumentEditing() {
		t.Fatal("the editor rung did not answer M")
	}
}

// The deck's info overlay owns its keys, and appPaneMoveShortcutLeaf refuses
// while it is up. Both rungs are pinned: the resolver and the real ladder.
func TestAppDeckMoveKeyLeavesTheInfoOverlayAlone(t *testing.T) {
	m, h, docID := appDeckMoveFixture(t)
	view, ok := h.deck.Viewer(docID).(*docview.Model)
	if !ok {
		t.Fatalf("focused leaf %d is not a document view", docID)
	}
	_ = h.openAppContentInfo(view, docID)
	if h.info == nil || h.infoLeaf != docID {
		t.Fatalf("fixture did not open the info overlay: info=%v leaf=%d", h.info, h.infoLeaf)
	}
	leaf := panelayout.Find(h.deck.Tree(), docID)
	if id := m.appPaneMoveShortcutLeaf(h, leaf); id != 0 {
		t.Fatalf("M resolved leaf %d with the info overlay open", id)
	}

	updated, _ := m.Update(tea.KeyPressMsg{Code: 'M', Text: "M"})
	got := asAppModel(t, updated)
	if h.layoutModal != nil || got.activeModal() == ModalPaneReposition {
		t.Fatal("M opened the reposition modal out of the info overlay")
	}
	if h.info == nil {
		t.Fatal("M closed the info overlay it should have been handed to")
	}
}

// Opening the modal releases the deck's input surfaces, and for a live inline
// edit that release kills a tmux session holding an unsaved buffer. Unlike the
// plugin/scope/shutdown releases, the modal leaves this deck laid out and comes
// back to it, so the buffer is asked about rather than discarded — through the
// same confirmation the editor's click-away path raises, and then the modal
// opens on the leaf that was actually requested.
func TestAppPaneLayoutModalConfirmsBeforeDiscardingALiveInlineEdit(t *testing.T) {
	m, h, docID := appDeckMoveFixture(t)
	primary := h.deck.Leaf(panelayout.Primary)
	if primary == 0 || primary == docID {
		t.Fatalf("fixture has no distinct primary leaf: primary=%d doc=%d", primary, docID)
	}
	e := h.appContentDocumentEdit(true)
	e.leafID = docID
	session := e.editor()
	session.Active = true
	session.Path = "README.md"
	t.Cleanup(h.releaseAppContentDocumentEdit)
	// Pin the session live without a tmux server; the guard exists only for a
	// session that still has something to lose.
	original := editSessionAlive
	editSessionAlive = func(*inlineedit.Session) bool { return true }
	t.Cleanup(func() { editSessionAlive = original })

	// Focus sits away from the editor, which is the state where both doors — M
	// and the header ⊞ — used to reach the release and kill the buffer silently.
	h.deck.FocusLeaf(primary)
	h.syncInnerFocus()
	m.updateContext()

	if cmd := m.openAppPaneLayoutModal(h, docID); cmd != nil {
		t.Fatal("the guarded open scheduled work instead of standing down")
	}
	if h.layoutModal != nil {
		t.Fatal("the modal opened over a live inline edit without asking")
	}
	if !session.ShowExitConfirm {
		t.Fatal("no confirmation was raised before discarding the editor buffer")
	}
	if !session.Active || h.appContentDocumentEdit(false) == nil {
		t.Fatalf("the editor was torn down anyway: active=%v state=%v", session.Active, h.appContentDocumentEdit(false))
	}
	if h.deck.FocusedLeaf() != docID {
		t.Fatalf("focus is on leaf %d, so the confirmation's keys cannot reach it", h.deck.FocusedLeaf())
	}

	// Discard: the dialog answers, the editor goes, and the move the user asked
	// for is what they get — one gesture, not a lost buffer and a lost modal.
	session.ConfirmSelection = 1
	if _, handled := m.handleAppContentEditKey(tea.KeyPressMsg{Code: tea.KeyEnter}); !handled {
		t.Fatal("the confirmation did not answer enter")
	}
	if session.ShowExitConfirm || session.Active {
		t.Fatalf("discard left the editor up: confirm=%v active=%v", session.ShowExitConfirm, session.Active)
	}
	if h.layoutModal == nil || h.layoutModal.LeafID() != docID {
		t.Fatalf("the deferred modal did not open on the requested leaf: %v", h.layoutModal)
	}
}

// The entry exists because the deck does. With plugin_content_panes off there
// is no deck to move a pane inside, and M must find nothing to open.
func TestAppDeckMoveEntryIsAbsentWithoutPluginContentPanes(t *testing.T) {
	m, h, docID := appDeckMoveFixture(t)
	features.SetOverride(features.PluginContentPanes.Name, false)
	t.Cleanup(func() { features.SetOverride(features.PluginContentPanes.Name, features.PluginContentPanes.Default) })

	if deck := m.currentContentDeck(); deck != nil {
		t.Fatal("a content deck is still the focused surface with plugin_content_panes off")
	}
	leaf := panelayout.Find(h.deck.Tree(), docID)
	if id := m.appPaneMoveShortcutLeaf(m.currentContentDeck(), leaf); id != 0 {
		t.Fatalf("M resolved leaf %d with plugin_content_panes off", id)
	}
	if _, ok := m.appContentContext(); ok {
		t.Fatal("the deck still reports a pane context with plugin_content_panes off")
	}
	for _, cmd := range m.appContentCommands() {
		if cmd.ID == panereposition.CommandMove {
			t.Fatal("the Move command is still advertised with plugin_content_panes off")
		}
	}

	updated, _ := m.Update(tea.KeyPressMsg{Code: 'M', Text: "M"})
	got := asAppModel(t, updated)
	if h.layoutModal != nil || got.activeModal() == ModalPaneReposition {
		t.Fatal("M opened the reposition modal with plugin_content_panes off")
	}
}

// Without pane_move the whole entry is gone: no key, no footer command.
func TestAppDeckMoveEntryIsAbsentWithoutPaneMove(t *testing.T) {
	m, h, _ := appDeckMoveFixture(t)
	features.SetOverride(features.PaneMove.Name, false)

	for _, cmd := range m.appContentCommands() {
		if cmd.ID == panereposition.CommandMove {
			t.Fatal("the Move command is still advertised with pane_move off")
		}
	}
	updated, _ := m.Update(tea.KeyPressMsg{Code: 'M', Text: "M"})
	got := asAppModel(t, updated)
	if h.layoutModal != nil || got.activeModal() == ModalPaneReposition {
		t.Fatal("M opened the reposition modal with pane_move off")
	}
}

func TestSessionsPaneLayoutModalAbsorbsPasteAtAppBoundary(t *testing.T) {
	m := globalFrameModel(t)
	features.SetOverride(features.PaneMove.Name, true)
	repo := newOverviewGitRepo(t, "sessions-pane-paste")
	if err := state.SetShowIdleWorktrees(true); err != nil {
		t.Fatal(err)
	}
	m.overview = overview.New(workspaceinventory.Collector{Runner: sessionsPanePasteRunner{root: repo}})
	load := m.overview.Start([]overview.Project{{Name: "sessions-pane-paste", Path: repo, Key: workspaceinventory.CanonicalPath(repo)}})
	if !driveOverviewUntilSelected(t, m.overview, load, 0) {
		t.Fatal("fixture did not populate a selected Sessions workspace")
	}
	_ = m.overview.SetWorkspacesVisible(true)

	// A lone Primary has nowhere to be moved to, so its header offers no layout
	// control. Open a Diff leaf beside it first — that is also the shape this
	// test is about, since the modal's draft only means anything with two panes.
	contentHeightBefore := m.height - headerHeight - footerHeight
	diffX, diffY, hasDiff := renderedCell(m.renderContent(m.contentWidth(), contentHeightBefore), "Diff")
	if !hasDiff {
		t.Fatal("fixture rendered no Diff chip to open a second pane with")
	}
	updated, _ := m.Update(tea.MouseClickMsg{X: diffX, Y: headerHeight + diffY, Button: tea.MouseLeft})
	m = asAppModel(t, updated)

	updated, _ = m.Update(tea.KeyPressMsg{Code: '/', Text: "/"})
	m = asAppModel(t, updated)
	for _, r := range "sessions" {
		updated, _ = m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
		m = asAppModel(t, updated)
	}
	if !m.overview.WorkspacesFilterFocused() || !m.overview.WorkspacesFilterActive() {
		t.Fatal("fixture did not focus and populate the Sessions filter")
	}

	contentHeight := m.height - headerHeight - footerHeight
	before := m.renderContent(m.contentWidth(), contentHeight)
	layoutX, layoutY, ok := renderedCell(before, ui.LayoutButtonLabel)
	if !ok {
		t.Fatalf("Sessions Primary pane rendered no layout control:\n%s", ansi.Strip(before))
	}
	updated, _ = m.Update(tea.MouseClickMsg{X: layoutX, Y: headerHeight + layoutY, Button: tea.MouseLeft})
	m = asAppModel(t, updated)
	if !m.overview.WorkspacesPaneLayoutModalOpen() {
		t.Fatal("app-routed Sessions header click did not open the pane reposition modal")
	}
	modalBefore := m.renderContent(m.contentWidth(), contentHeight)

	updated, _ = m.Update(tea.PasteMsg{Content: "-pasted"})
	m = asAppModel(t, updated)
	if !m.overview.WorkspacesPaneLayoutModalOpen() {
		t.Fatal("paste closed the Sessions pane reposition modal")
	}
	if !m.overview.WorkspacesFilterFocused() || !m.overview.WorkspacesFilterActive() {
		t.Fatal("paste changed the hidden Sessions filter state")
	}
	if modalAfter := m.renderContent(m.contentWidth(), contentHeight); modalAfter != modalBefore {
		t.Fatal("paste changed the Sessions pane reposition modal or its draft tree")
	}

	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	m = asAppModel(t, updated)
	if m.overview.WorkspacesPaneLayoutModalOpen() {
		t.Fatal("escape did not close the Sessions pane reposition modal")
	}
	if after := m.renderContent(m.contentWidth(), contentHeight); after != before {
		t.Fatalf("paste changed the hidden Sessions filter or pane tree\nbefore:\n%s\nafter:\n%s", ansi.Strip(before), ansi.Strip(after))
	}
}

type sessionsPanePasteRunner struct{ root string }

func (r sessionsPanePasteRunner) Output(_ context.Context, name string, _ ...string) ([]byte, error) {
	switch name {
	case "tmux":
		return nil, nil
	case "git":
		return []byte("worktree " + r.root + "\nHEAD abcdef\nbranch refs/heads/main\n\n"), nil
	default:
		return nil, fmt.Errorf("unexpected command %q", name)
	}
}

func driveOverviewUntilSelected(t *testing.T, model *overview.Model, cmd tea.Cmd, depth int) bool {
	t.Helper()
	if _, ok := model.SelectedWorkspace(); ok {
		return true
	}
	// Selection is established by the inventory result. Do not execute the
	// completion batch's long-lived polling command if a broken fixture reaches
	// it without selecting a row.
	if cmd == nil || depth >= 5 {
		return false
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, sub := range batch {
			if driveOverviewUntilSelected(t, model, sub, depth+1) {
				return true
			}
		}
		return false
	}
	return driveOverviewUntilSelected(t, model, model.Update(msg), depth+1)
}

func renderedCell(view, needle string) (x, y int, ok bool) {
	for row, line := range strings.Split(view, "\n") {
		plain := ansi.Strip(line)
		if index := strings.Index(plain, needle); index >= 0 {
			return ansi.StringWidth(plain[:index]), row, true
		}
	}
	return 0, 0, false
}

func gridIDs(root *panelayout.Node) [][]int {
	grid := panelayout.GridOf(root)
	if grid == nil {
		return nil
	}
	out := make([][]int, grid.ColumnCount())
	for col := 1; col <= grid.ColumnCount(); col++ {
		for row := 1; row <= grid.RowCount(col); row++ {
			out[col-1] = append(out[col-1], grid.Cell(col, row).ID)
		}
	}
	return out
}
