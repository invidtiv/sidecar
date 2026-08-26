package workspace

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/mouse"
	appmsg "github.com/marcus/sidecar/internal/msg"
	"github.com/marcus/sidecar/internal/state"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/uirequest"
	"github.com/marcus/sidecar/internal/workspacediff"
)

func TestDOpensDiffLeafBesideTheTerminal(t *testing.T) {
	root := t.TempDir()
	p := docPaneTestPlugin(t, root, false)
	if !p.paneTreeShowing() {
		t.Fatal("premise: tree is showing")
	}

	cmd := p.handleListKeys(tea.KeyPressMsg{Code: 'd', Text: "d"})
	if cmd == nil {
		t.Fatal("d did not open a Diff leaf")
	}
	if !p.paneTreeShowing() {
		t.Fatal("d hid the pane tree")
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

// Clicking a Diff body region (file list, hunks, inner divider) must take
// pane-tree focus the same way Tab / a tab-chip click does. Those inner hits
// win over regionPaneLeaf, so without an explicit focusLeaf call the previous
// leaf keeps the keyboard and q opens quit instead of closing the Diff.
func TestDiffBodyClickFocusesTheDiffLeaf(t *testing.T) {
	root := t.TempDir()
	p := docPaneTestPlugin(t, root, false)
	if cmd := p.showDiffCmd(); cmd == nil {
		t.Fatal("show-diff opened nothing")
	}
	_, leaf := p.activeDiffPane()
	if leaf == nil || leaf.Kind != PaneDiff {
		t.Fatal("no Diff leaf")
	}
	view := p.activeDiffView()
	if view == nil {
		t.Fatal("no Diff view")
	}

	termID := terminalLeafID(p.paneRoot)
	p.focusLeaf(termID)
	if p.paneFocus != termID {
		t.Fatalf("premise: paneFocus = %d, want terminal %d", p.paneFocus, termID)
	}

	// A body click from a live terminal abandons the armed click and leaves
	// interactive mode, matching selectDiffTab.
	p.pointer.Arm(tty.ClickForward, 4, 4)
	p.viewMode = ViewModeInteractive
	p.interactiveState = &InteractiveState{Active: true, TargetPane: "%902", TargetSession: "diff-click"}
	t.Cleanup(p.stopTerminalModels)
	view.Focus = DiffTabFocusDiff

	p.handleMouseClick(mouse.MouseAction{
		Type:   mouse.ActionClick,
		Region: &mouse.Region{ID: regionDiffTabFile, Data: 0},
	})
	if p.paneFocus != leaf.ID {
		t.Fatalf("file-row click paneFocus = %d, want Diff leaf %d", p.paneFocus, leaf.ID)
	}
	if p.activePane != PanePreview {
		t.Fatalf("file-row click activePane = %v, want preview", p.activePane)
	}
	if p.shellLeafFocused() {
		t.Fatal("file-row click left the terminal panel focused")
	}
	if view.Focus != DiffTabFocusFileList {
		t.Fatalf("file-row click inner focus = %v, want file list", view.Focus)
	}
	if p.viewMode != ViewModeList {
		t.Fatalf("file-row click left viewMode = %v, want list", p.viewMode)
	}
	if p.pointer.Resolution != tty.ClickNone {
		t.Fatalf("file-row click left pointer resolution %v", p.pointer.Resolution)
	}

	p.focusLeaf(termID)
	view.Focus = DiffTabFocusFileList
	p.handleMouseClick(mouse.MouseAction{
		Type:   mouse.ActionClick,
		Region: &mouse.Region{ID: regionDiffTabDiffPane},
	})
	if p.paneFocus != leaf.ID {
		t.Fatalf("hunk-pane click paneFocus = %d, want Diff leaf %d", p.paneFocus, leaf.ID)
	}
	if p.activePane != PanePreview {
		t.Fatalf("hunk-pane click activePane = %v, want preview", p.activePane)
	}
	if p.shellLeafFocused() {
		t.Fatal("hunk-pane click left the terminal panel focused")
	}
	if view.Focus != DiffTabFocusDiff {
		t.Fatalf("hunk-pane click inner focus = %v, want hunks", view.Focus)
	}

	p.focusLeaf(termID)
	p.handleMouseClick(mouse.MouseAction{
		Type:   mouse.ActionClick,
		Region: &mouse.Region{ID: regionDiffTabDivider},
	})
	if p.paneFocus != leaf.ID {
		t.Fatalf("divider click paneFocus = %d, want Diff leaf %d", p.paneFocus, leaf.ID)
	}

	p.focusLeaf(termID)
	beforeFocus := p.paneFocus
	p.handleMouseScroll(mouse.MouseAction{
		Type:   mouse.ActionScrollDown,
		Delta:  1,
		Region: &mouse.Region{ID: regionDiffTabFile, Data: 0},
	})
	if p.paneFocus != beforeFocus {
		t.Fatalf("wheel moved paneFocus from %d to %d", beforeFocus, p.paneFocus)
	}

	p.handleMouseClick(mouse.MouseAction{
		Type:   mouse.ActionClick,
		Region: &mouse.Region{ID: regionDiffTabDiffPane},
	})
	handled, cmd := p.handleDiffKey(tea.KeyPressMsg{Code: 'q', Text: "q"})
	if !handled || cmd == nil {
		t.Fatalf("q after body click: handled=%v cmd=%v", handled, cmd != nil)
	}
	if still, _ := p.activeDiffPane(); still != nil || p.paneRoot.Split != nil {
		t.Fatalf("q after body click left the Diff open: root=%#v", p.paneRoot)
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

	saved := p.paneLayoutJSON(p.paneRoot)
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
	plan, ok := planPaneOpen(p.paneRoot, PaneDoc, nil)
	if !ok || plan.Retarget != 0 || plan.Axis != SplitRows {
		t.Fatalf("File after Diff should stack on the Diff leaf: %#v ok=%v", plan, ok)
	}
	plan, ok = planPaneOpen(p.paneRoot, PaneDiff, nil)
	if !ok || plan.Retarget == 0 {
		t.Fatalf("second Diff should retarget: %#v ok=%v", plan, ok)
	}
	_ = strings.TrimSpace(root)
}

func TestClickThenCLISharesResolvedIdentity(t *testing.T) {
	root := initTwoCommitRepo(t)
	headShort := strings.TrimSpace(runGitOutput(t, root, "rev-parse", "--short=7", "HEAD"))
	headFull := strings.TrimSpace(runGitOutput(t, root, "rev-parse", "HEAD"))
	parentShort := strings.TrimSpace(runGitOutput(t, root, "rev-parse", "--short=7", "HEAD~1"))
	parentFull := strings.TrimSpace(runGitOutput(t, root, "rev-parse", "HEAD~1"))

	cases := []struct {
		name  string
		token string
		want  string
	}{
		{"commit", headShort, "c:" + headFull},
		{"two-dot", parentShort + ".." + headShort, "r:" + parentFull + ".." + headFull},
		{"three-dot", parentShort + "..." + headShort, "r:" + parentFull + "..." + headFull},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := docPaneTestPlugin(t, root, true)
			if _, ok := p.activateDiffLink(tc.token); !ok {
				t.Fatalf("activateDiffLink(%q) failed", tc.token)
			}
			diff, _ := p.activeDiffPane()
			if diff == nil || diff.view() == nil {
				t.Fatal("click opened no Diff view")
			}
			if got := diff.view().Target.Identity(); got != tc.want {
				t.Fatalf("click identity = %q, want %q", got, tc.want)
			}

			surfaceRoot, surface, ok := p.selectedTerminalSurface()
			if !ok {
				t.Fatal("no surface")
			}
			cli := uirequest.DiffTarget(root, tc.token)
			if cli.Identity() != tc.want {
				t.Fatalf("DiffTarget identity = %q, want %q", cli.Identity(), tc.want)
			}
			if cmd := p.openDiffPaneForSurface(surfaceRoot, surface, cli); cmd == nil {
				t.Fatal("openDiffPaneForSurface failed")
			}
			diff, _ = p.activeDiffPane()
			if keys := diffTabKeys(diff); !reflect.DeepEqual(keys, []string{tc.want}) {
				t.Fatalf("tabs after click+CLI = %v, want [%s]", keys, tc.want)
			}
		})
	}
}

func TestHashClickCommitTabNeverLeavesLoading(t *testing.T) {
	root := initTwoCommitRepo(t)
	short := strings.TrimSpace(runGitOutput(t, root, "rev-parse", "--short=7", "HEAD"))
	p := docPaneTestPlugin(t, root, true)
	p.shells[0].Agent.OutputBuf.Update("landed " + short + "\n")

	cmd, ok := p.activateDiffLink(short)
	if !ok {
		t.Fatal("activateDiffLink failed")
	}
	deliverDiffLoads(t, p, cmd)

	diff, _ := p.activeDiffPane()
	if diff == nil || diff.view() == nil {
		t.Fatal("no Diff view")
	}
	view := diff.view()
	if view.CommitDetail == nil {
		t.Fatalf("CommitDetail still nil; state=%v", view.State)
	}
	if view.State == workspacediff.LoadStateLoading {
		t.Fatal("state stayed Loading")
	}
	if view.Focus != workspacediff.FocusCommitFiles {
		t.Fatalf("focus = %v, want commit file list", view.Focus)
	}
	got := view.Render(80, 12, workspacediff.RenderOpts{})
	if strings.Contains(got, "Loading diff…") {
		t.Fatalf("render still loading: %q", got)
	}
	if strings.Contains(got, "Working Tree vs HEAD") {
		t.Fatalf("commit tab rendered working-tree chrome: %q", got)
	}
}

func TestRangeClickLoadsFilesNotWorkingTree(t *testing.T) {
	root := initTwoCommitRepo(t)
	a := strings.TrimSpace(runGitOutput(t, root, "rev-parse", "--short=7", "HEAD~1"))
	b := strings.TrimSpace(runGitOutput(t, root, "rev-parse", "--short=7", "HEAD"))
	token := a + ".." + b
	p := docPaneTestPlugin(t, root, true)
	p.shells[0].Agent.OutputBuf.Update("compare " + token + "\n")

	cmd, ok := p.activateDiffLink(token)
	if !ok {
		t.Fatal("activateDiffLink range failed")
	}
	deliverDiffLoads(t, p, cmd)

	diff, _ := p.activeDiffPane()
	if diff == nil || diff.view() == nil {
		t.Fatal("no range view")
	}
	view := diff.view()
	if view.State == workspacediff.LoadStateLoading {
		t.Fatal("range stayed Loading")
	}
	if view.Target.Kind != workspacediff.TargetRange || view.Target.Dots != ".." {
		t.Fatalf("target = %+v", view.Target)
	}
	if view.CommitDetail != nil || view.Snapshot != nil {
		t.Fatal("range tab took a commit or snapshot")
	}
	if len(view.Files) == 0 {
		t.Fatal("range file list empty")
	}
	got := view.Render(140, 12, workspacediff.RenderOpts{})
	if strings.Contains(got, "Loading diff…") || strings.Contains(got, "Working Tree vs HEAD") {
		t.Fatalf("range chrome wrong: %q", got)
	}
}

func TestShowDiffToastsWhenPanesDisabled(t *testing.T) {
	root := t.TempDir()
	p := docPaneTestPlugin(t, root, false)
	p.paneRoot = nil
	p.paneFocus = 0

	cmd := p.showDiffCmd()
	if cmd == nil {
		t.Fatal("flag-off d returned no toast")
	}
	msg := cmd()
	// Static feature state is a flash, not a stored notification.
	flash, ok := msg.(appmsg.FlashMsg)
	if !ok {
		t.Fatalf("flag-off d produced %T, want FlashMsg", msg)
	}
	if flash.Text != features.WorkspaceDocPanesDisabledDiff {
		t.Fatalf("flash = %q", flash.Text)
	}
	if p.paneRoot != nil {
		t.Fatal("flag-off d created a pane tree")
	}

	click := p.clickPreviewAction(previewActionDiff)
	if click == nil {
		t.Fatal("flag-off Diff chip returned no toast")
	}
	if got := click(); got.(appmsg.FlashMsg).Text != features.WorkspaceDocPanesDisabledDiff {
		t.Fatalf("chip flash = %#v", got)
	}
}

func initTwoCommitRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	runGitOutput(t, root, "init", "-b", "main")
	runGitOutput(t, root, "config", "user.email", "sidecar@example.test")
	runGitOutput(t, root, "config", "user.name", "Sidecar Test")
	runGitOutput(t, root, "commit", "--allow-empty", "-m", "one")
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitOutput(t, root, "add", "a.go")
	runGitOutput(t, root, "commit", "-m", "two")
	return root
}

func deliverDiffLoads(t *testing.T, p *Plugin, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		for _, child := range msg {
			deliverDiffLoads(t, p, child)
		}
	case workspacediff.CommitDetailMsg, workspacediff.RangeMsg, workspacediff.SnapshotMsg,
		workspacediff.CommitFileDiffMsg:
		p.update(msg)
	}
}
