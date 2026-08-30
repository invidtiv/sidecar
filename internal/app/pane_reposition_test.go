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
	"github.com/marcus/sidecar/internal/features"
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

	updated, _ := m.Update(tea.KeyPressMsg{Code: '/', Text: "/"})
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
