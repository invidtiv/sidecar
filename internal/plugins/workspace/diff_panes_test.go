package workspace

import (
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/state"
	"github.com/marcus/sidecar/internal/workspacediff"
)

func TestDFromDiffTabShowsTreeAndDiffLeaf(t *testing.T) {
	root := t.TempDir()
	p := docPaneTestPlugin(t, root, false)
	p.previewTab = PreviewTabDiff
	if p.paneTreeShowing() {
		t.Fatal("premise: Diff tab hides the pane tree")
	}

	cmd := p.handleListKeys(tea.KeyPressMsg{Code: 'd', Text: "d"})
	if cmd == nil {
		t.Fatal("d did not open a Diff leaf")
	}
	if p.previewTab != PreviewTabOutput {
		t.Fatalf("previewTab = %v, want Output so the tree is drawn", p.previewTab)
	}
	if !p.paneTreeShowing() {
		t.Fatal("d from the Diff tab did not show the pane tree")
	}
	diff, leaf := p.activeDiffPane()
	if diff == nil || leaf == nil || leaf.Kind != PaneDiff {
		t.Fatalf("no Diff leaf: pane=%#v root=%#v", diff, p.paneRoot)
	}
	if p.paneRoot.Split == nil || p.paneRoot.Split.Axis != SplitCols || p.paneRoot.Split.A.Kind != PaneTerminal {
		t.Fatalf("first Diff did not split the terminal to the right: %#v", p.paneRoot)
	}
	if p.paneFocus != leaf.ID {
		t.Fatalf("focus = %d, want the new Diff leaf %d", p.paneFocus, leaf.ID)
	}
	if view := diff.view(); view == nil || view.Target.Identity() != workspacediff.IdentityWorkingTree {
		t.Fatalf("active target = %#v, want wt", view)
	}
}

func TestQOnFocusedDiffLeafHidesAndDoesNotQuit(t *testing.T) {
	root := t.TempDir()
	p := docPaneTestPlugin(t, root, false)
	if cmd := p.showDiffCmd(); cmd == nil {
		t.Fatal("show-diff opened nothing")
	}
	if !p.diffFocused() {
		t.Fatal("Diff leaf is not focused after open")
	}
	if got := p.FocusContext(); got != "workspace-diff" {
		t.Fatalf("FocusContext = %q, want workspace-diff", got)
	}

	handled, cmd := p.handleDiffKey(tea.KeyPressMsg{Code: 'q', Text: "q"})
	if !handled || cmd == nil {
		t.Fatalf("q: handled=%v cmd=%v", handled, cmd != nil)
	}
	if still, _ := p.activeDiffPane(); still != nil || p.paneRoot.Split != nil {
		t.Fatalf("q did not hide the Diff leaf: root=%#v", p.paneRoot)
	}
	if p.FocusContext() == "workspace-diff" {
		t.Fatal("hidden Diff leaf kept workspace-diff focus context")
	}
}

func TestDiffLeafEncodeDecodeRoundTripWT(t *testing.T) {
	root := t.TempDir()
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	p := docPaneTestPlugin(t, root, true)
	if cmd := p.openDiffPaneForSurface(resolved, "shell:test-shell", workspacediff.WorkingTreeTarget()); cmd == nil {
		t.Fatal("open Diff leaf failed")
	}
	diff, _ := p.activeDiffPane()
	if diff == nil || diff.view() == nil {
		t.Fatal("no active Diff view to persist")
	}
	diff.view().Scroll = 3

	saved := p.encodePaneNode(p.paneRoot)
	if saved == nil || saved.Split == nil {
		t.Fatalf("encode lost the tree: %#v", saved)
	}
	leaf := saved.Split.B
	if leaf.Kind != contentKindDiff {
		t.Fatalf("encoded kind = %q, want %q (must not fall through to doc)", leaf.Kind, contentKindDiff)
	}
	if len(leaf.DiffTabs) != 1 || leaf.DiffTabs[0].Spec != workspacediff.IdentityWorkingTree {
		t.Fatalf("encoded DiffTabs = %#v, want wt", leaf.DiffTabs)
	}

	restored := docPaneTestPlugin(t, root, true)
	if cmd := restored.restorePaneLayout(&state.PaneLayoutJSON{
		Root: resolved, Surface: "shell:test-shell", Open: true,
		Split: saved.Split,
	}); cmd == nil {
		t.Fatal("restore did not schedule a load")
	}
	got, leafNode := restored.activeDiffPane()
	if got == nil || leafNode == nil || leafNode.Kind != PaneDiff {
		t.Fatal("restored tree has no Diff leaf")
	}
	if view := got.view(); view == nil || view.Target.Identity() != workspacediff.IdentityWorkingTree {
		t.Fatalf("restored target = %#v, want wt", view)
	}
	if !supportedPaneTree(restored.paneRoot) {
		t.Fatal("restored Diff tree is not supported")
	}
}

func TestPaneContentDiffArmIsNotTerminal(t *testing.T) {
	root := t.TempDir()
	p := docPaneTestPlugin(t, root, false)
	if cmd := p.showDiffCmd(); cmd == nil {
		t.Fatal("show-diff opened nothing")
	}
	_, leaf := p.activeDiffPane()
	content := p.paneContent(leaf)
	if content == nil {
		t.Fatal("Diff leaf adapted to nil")
	}
	if content.Kind() != contentKindDiff {
		t.Fatalf("Diff leaf adapted as %q, want %q (default is terminal)", content.Kind(), contentKindDiff)
	}
	if _, ok := content.(*terminalContent); ok {
		t.Fatal("Diff leaf fell through to terminalContent")
	}
	if content.Kind() == contentKindTerminal {
		t.Fatal("Diff leaf Kind is terminal")
	}
}

func TestShowDiffAdvertisedOnListAndPreview(t *testing.T) {
	root := t.TempDir()
	p := docPaneTestPlugin(t, root, false)
	p.activePane = PaneSidebar
	if !commandNamed(p, "show-diff") {
		t.Fatalf("workspace-list Commands() omitted show-diff: %#v", p.Commands())
	}
	p.activePane = PanePreview
	if !commandNamed(p, "show-diff") {
		t.Fatalf("workspace-preview Commands() omitted show-diff: %#v", p.Commands())
	}
	var name string
	for _, cmd := range p.Commands() {
		if cmd.ID == "show-diff" {
			name = cmd.Name
		}
	}
	if name != "Diff" {
		t.Fatalf("show-diff name = %q, want Diff", name)
	}
}

func TestHandleListKeysRoutesDBeforeSwitch(t *testing.T) {
	root := t.TempDir()
	p := docPaneTestPlugin(t, root, false)
	p.previewTab = PreviewTabDiff
	if cmd := p.handleListKeys(tea.KeyPressMsg{Code: 'd', Text: "d"}); cmd == nil {
		t.Fatal("handleListKeys d opened nothing")
	}
	if !p.diffFocused() {
		t.Fatal("d did not focus the Diff leaf")
	}
}

func commandNamed(p *Plugin, id string) bool {
	for _, cmd := range p.Commands() {
		if cmd.ID == id {
			return true
		}
	}
	return false
}

func TestDiffLeafDoesNotChangeFileIssueSteelThread(t *testing.T) {
	// Placement of File then Issue is unchanged; Diff is a new kind that
	// retargets itself rather than inserting a third column.
	root := t.TempDir()
	p := docPaneTestPlugin(t, root, false)
	if cmd := p.showDiffCmd(); cmd == nil {
		t.Fatal("open Diff failed")
	}
	plan, ok := planPaneOpen(p.paneRoot, PaneDoc)
	if !ok || plan.Retarget != 0 || plan.Axis != SplitRows {
		t.Fatalf("File after Diff should stack on the Diff leaf: %#v ok=%v", plan, ok)
	}
	plan, ok = planPaneOpen(p.paneRoot, PaneDiff)
	if !ok || plan.Retarget == 0 {
		t.Fatalf("second Diff should retarget: %#v ok=%v", plan, ok)
	}
	_ = strings.TrimSpace(root)
}
