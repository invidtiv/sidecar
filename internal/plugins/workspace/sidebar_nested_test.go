package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/workspacelist"
)

func nestedSidebarPlugin(t *testing.T) *Plugin {
	t.Helper()
	p := New()
	workDirA := filepath.Join(t.TempDir(), "sidecar")
	workDirB := filepath.Join(t.TempDir(), "sidecar-feature")
	if err := os.MkdirAll(workDirA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(workDirB, 0o755); err != nil {
		t.Fatal(err)
	}
	p.ctx = &plugin.Context{WorkDir: workDirA, ProjectRoot: workDirA, Epoch: 3}
	p.width, p.height = 140, 40
	p.focused = true
	p.viewMode = ViewModeList
	p.sidebarVisible = true
	p.sidebarWidth = 30
	p.activePane = PaneSidebar
	p.shells = []*ShellSession{
		{Name: "here", TmuxName: "sidecar-sh-sidecar-1", WorkDir: workDirA},
	}
	p.worktrees = []*Worktree{
		{Name: "main", Path: workDirA, IsMain: true, Key: "main"},
		{Name: "feature", Path: workDirB, Key: "feature"},
	}
	p.nestedByWorkDir = map[string][]*ShellSession{
		filepath.Clean(workDirB): {
			{Name: "sibling", TmuxName: "sidecar-sh-sidecar-feature-1", WorkDir: workDirB},
		},
	}
	p.selectTopShellAt(0)
	return p
}

func TestNestedShellsAppearUnderSiblingWorktreeOnly(t *testing.T) {
	p := nestedSidebarPlugin(t)
	view := ansi.Strip(p.renderSidebarContent(48, 24))
	if !strings.Contains(view, "here") {
		t.Fatalf("current worktree shell missing from Shells section:\n%s", view)
	}
	if !strings.Contains(view, "sibling") {
		t.Fatalf("sibling shell missing from Workspaces nest:\n%s", view)
	}
	if !strings.Contains(view, workspacelist.KindGlyph(workspacelist.KindShell)) {
		t.Fatalf("nested shell row missing shell kind glyph:\n%s", view)
	}

	// Current worktree row has no nested copy of "here".
	if strings.Count(view, "here") != 1 {
		t.Fatalf("current worktree shell appeared more than once:\n%s", view)
	}

	items := p.visibleSidebarItems()
	if len(items) != 4 { // here, main, feature, sibling
		t.Fatalf("visible items = %d, want 4: %+v", len(items), items)
	}
	if items[0].kind != navKindShell || items[1].kind != navKindWorktree || items[2].kind != navKindWorktree || items[3].kind != navKindNestedShell {
		t.Fatalf("item kinds = %+v", items)
	}
	if items[3].shell == nil || items[3].shell.TmuxName != "sidecar-sh-sidecar-feature-1" {
		t.Fatalf("nested item = %+v", items[3])
	}
	if items[1].worktreeIdx != 0 || len(p.visibleNestedShells(p.worktrees[0])) != 0 {
		t.Fatal("current worktree grew a nest")
	}
}

func TestNestedShellsStayOutOfTopShellsSection(t *testing.T) {
	p := nestedSidebarPlugin(t)
	if len(p.shells) != 1 || p.shells[0].TmuxName != "sidecar-sh-sidecar-1" {
		t.Fatalf("top Shells = %v, want only the current workDir shell", shellNames(p.shells))
	}
	for _, idx := range p.visibleShellIndices() {
		if p.shells[idx].TmuxName == "sidecar-sh-sidecar-feature-1" {
			t.Fatal("sibling shell leaked into the top Shells section")
		}
	}
}

func TestNestedShellKeyboardAndMouseSelect(t *testing.T) {
	p := nestedSidebarPlugin(t)
	var walk []string
	walk = append(walk, selectionLabel(p))
	for i := 0; i < 3; i++ {
		p.moveCursor(1)
		walk = append(walk, selectionLabel(p))
	}
	want := []string{"shell:here", "worktree:main", "worktree:feature", "nested:sibling"}
	if strings.Join(walk, ",") != strings.Join(want, ",") {
		t.Fatalf("j walk = %v, want %v", walk, want)
	}
	if !p.selectingShell() || p.getSelectedShell() == nil || p.getSelectedShell().TmuxName != "sidecar-sh-sidecar-feature-1" {
		t.Fatalf("nested selection did not resolve: shellSelected=%v nested=%q selected=%v",
			p.shellSelected, p.selectedNestedTmux, p.getSelectedShell())
	}

	p.moveCursor(-1)
	if selectionLabel(p) != "worktree:feature" {
		t.Fatalf("k from nested = %q, want parent worktree", selectionLabel(p))
	}

	p.selectTopShellAt(0)
	p.handleMouseClick(mouse.MouseAction{
		Type:   mouse.ActionClick,
		Region: &mouse.Region{ID: regionWorktreeItem, Data: nestedShellHit{TmuxName: "sidecar-sh-sidecar-feature-1"}},
	})
	if selectionLabel(p) != "nested:sibling" {
		t.Fatalf("mouse click = %q, want nested:sibling", selectionLabel(p))
	}
}

func TestNestedShellInteractionAndAttachTargetTmuxSession(t *testing.T) {
	logPath := installSuccessfulFakeTmux(t)
	p := nestedSidebarPlugin(t)
	const session = "sidecar-sh-sidecar-feature-1"
	parent, shell := p.findNestedShell(session)
	if shell == nil {
		t.Fatal("nested shell fixture missing")
	}
	shell.Agent = &Agent{
		Type: AgentShell, TmuxSession: session, TmuxPane: "%9",
		OutputBuf: tty.NewOutputBuffer(outputBufferCap),
	}
	var attached string
	p.attachSession = func(sessionName, displayName string) tea.Cmd {
		attached = sessionName
		return nil
	}
	p.selectNestedShell(parent, session)
	p.handleListKeys(key("enter"))
	if attached != "" {
		t.Fatalf("Enter full-attached nested shell %q instead of entering embedded interaction", attached)
	}
	if p.viewMode != ViewModeInteractive || p.interactiveState == nil || !p.interactiveState.Active {
		t.Fatalf("Enter did not activate embedded interaction: mode=%v state=%#v", p.viewMode, p.interactiveState)
	}
	if p.interactiveState.TargetSession != session || p.interactiveState.TargetPane != "%9" {
		t.Fatalf("embedded target = %#v", p.interactiveState)
	}
	attachLiveTerminal(p, false)
	if err := os.WriteFile(logPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	runCommandTree(p.handleInteractiveKeys(keyPressForText("NESTED_INPUT")))
	tty.WaitForPendingSends()
	logged, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logged), "paste-buffer") || !strings.Contains(string(logged), "-t %9") {
		t.Fatalf("embedded input missed nested pane: %s", logged)
	}

	p.exitInteractiveMode()
	attached = ""
	cmd := p.handleListKeys(key("t"))
	if cmd != nil {
		cmd()
	}
	if attached != session {
		t.Fatalf("t attached %q, want %s", attached, session)
	}

	attached = ""
	cmd = p.handleMouseDoubleClick(mouse.MouseAction{
		Type:   mouse.ActionDoubleClick,
		Region: &mouse.Region{ID: regionWorktreeItem, Data: nestedShellHit{TmuxName: session}},
	})
	if cmd != nil {
		cmd()
	}
	if attached != session {
		t.Fatalf("double-click attached %q", attached)
	}
}

func TestNestedShellExplicitEEntersFromInheritedWorktreeTabs(t *testing.T) {
	for _, tab := range []PreviewTab{PreviewTabDiff, PreviewTabTask} {
		t.Run(map[PreviewTab]string{PreviewTabDiff: "diff", PreviewTabTask: "task"}[tab], func(t *testing.T) {
			installSuccessfulFakeTmux(t)
			p := nestedSidebarPlugin(t)
			const session = "sidecar-sh-sidecar-feature-1"
			parent, shell := p.findNestedShell(session)
			shell.Agent = &Agent{Type: AgentShell, TmuxSession: session, TmuxPane: "%9"}
			p.selectNestedShell(parent, session)
			p.activePane = PanePreview
			p.previewTab = tab

			p.handleListKeys(keyPressFor("E"))

			if p.viewMode != ViewModeInteractive || p.interactiveState == nil ||
				p.interactiveState.TargetSession != session || p.interactiveState.TargetPane != "%9" {
				t.Fatalf("E from inherited %v tab did not enter nested terminal: mode=%v state=%#v",
					tab, p.viewMode, p.interactiveState)
			}
		})
	}
}

func TestNestedShellPreviewCommandsMatchProjectShell(t *testing.T) {
	p := nestedSidebarPlugin(t)
	const session = "sidecar-sh-sidecar-feature-1"
	parent, shell := p.findNestedShell(session)
	shell.Agent = &Agent{Type: AgentShell, TmuxSession: session, TmuxPane: "%9"}
	p.selectNestedShell(parent, session)
	p.activePane = PanePreview
	p.previewTab = PreviewTabDiff

	ids := commandIDs(p.Commands())
	for _, want := range []string{"interactive", "toggle-terminal"} {
		if !ids[want] {
			t.Errorf("nested shell commands missing %q: %v", want, ids)
		}
	}
	for _, unwanted := range []string{"prev-tab", "next-tab", "toggle-diff-scope", "toggle-diff-view"} {
		if ids[unwanted] {
			t.Errorf("nested shell advertised worktree-only command %q: %v", unwanted, ids)
		}
	}

	p.termPanelVisible = true
	ids = commandIDs(p.Commands())
	if !ids["switch-terminal-layout"] {
		t.Fatalf("visible nested terminal panel omitted layout command: %v", ids)
	}
}

func TestNestedShellUsesOrdinaryTerminalSurfaceContracts(t *testing.T) {
	p := nestedSidebarPlugin(t)
	const session = "sidecar-sh-sidecar-feature-1"
	parent, shell := p.findNestedShell(session)
	buffer := tty.NewOutputBuffer(outputBufferCap)
	buffer.Update("nested output")
	shell.Agent = &Agent{Type: AgentShell, TmuxSession: session, TmuxPane: "%9", OutputBuf: buffer}
	p.selectNestedShell(parent, session)
	p.previewTab = PreviewTabDiff // A shell surface is terminal-shaped regardless of the old worktree tab.

	if !p.previewShowsTerminal() || p.liveTerminalOutputBuffer(false) != buffer {
		t.Fatal("nested selection did not use the ordinary terminal buffer/window")
	}
	if p.captureShellSessionByName(session, 1) == nil {
		t.Fatal("nested shell was invisible to semantic/fallback capture lookup")
	}
	history, ok := p.terminalHistoryFor(false)
	if !ok || history.Key != terminalHistoryKey("shell", session) || history.Target != "%9" || history.Buffer != buffer {
		t.Fatalf("nested history source = %#v, ok=%v", history, ok)
	}
	wantRoot, err := filepath.EvalSymlinks(shell.WorkDir)
	if err != nil {
		t.Fatal(err)
	}
	root, identity, ok := p.selectedTerminalSurface()
	if !ok || root != filepath.Clean(wantRoot) || identity != "shell:"+session {
		t.Fatalf("nested terminal surface = root %q identity %q ok=%v", root, identity, ok)
	}
	links := p.terminalLinkSurfaceContext(false)
	if !links.ok || links.rawRoot != filepath.Clean(shell.WorkDir) || links.surface != "shell:"+session {
		t.Fatalf("nested link context = %#v", links)
	}
	if got := p.termPanelWorkDir(); got != shell.WorkDir {
		t.Fatalf("nested terminal panel cwd = %q, want %q", got, shell.WorkDir)
	}
	if got := p.terminalProjectionIdentity(false); !strings.HasPrefix(got, "shell:"+session+"\x00") {
		t.Fatalf("nested projection identity = %q", got)
	}

	p.activePane = PanePreview
	p.viewMode = ViewModeInteractive
	p.interactiveState = &InteractiveState{Active: true, TargetSession: session, TargetPane: "%9"}
	if !p.nativeTerminalActive() {
		t.Fatal("nested embedded terminal did not enable native cursor/mouse mode")
	}
}

func TestNewShellPersistsWorkDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "shells.json")
	manifest := &ShellManifest{Version: manifestVersion, path: path}
	p := New()
	p.ctx = &plugin.Context{WorkDir: "/work/sidecar", ProjectRoot: "/work/sidecar", Epoch: 1}
	p.shellManifest = manifest

	_, cmd := p.Update(ShellCreatedMsg{
		SessionName: "sidecar-sh-sidecar-1",
		DisplayName: "Shell 1",
		PaneID:      "%3",
	})
	_ = cmd

	if len(p.shells) != 1 || p.shells[0].WorkDir != "/work/sidecar" {
		t.Fatalf("in-memory WorkDir = %+v", p.shells[0])
	}
	reloaded, err := LoadShellManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	def := reloaded.FindShell("sidecar-sh-sidecar-1")
	if def == nil || def.WorkDir != "/work/sidecar" {
		t.Fatalf("persisted definition = %+v", def)
	}
}

func TestShellCreatedMsgDoesNotAdoptNestedSibling(t *testing.T) {
	p := nestedSidebarPlugin(t)
	current := p.ctx.WorkDir
	sibling := p.worktrees[1].Path
	name := "sidecar-sh-sidecar-feature-1"

	path := filepath.Join(t.TempDir(), "shells.json")
	manifest := &ShellManifest{Version: manifestVersion, path: path, Shells: []ShellDefinition{
		{TmuxName: "sidecar-sh-sidecar-1", DisplayName: "here", WorkDir: current},
		{TmuxName: name, DisplayName: "sibling", WorkDir: sibling},
	}}
	if err := manifest.Save(); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	p.shellManifest = manifest

	p.Update(ShellCreatedMsg{SessionName: name, DisplayName: "sibling", PaneID: "%9"})

	for _, shell := range p.shells {
		if shell.TmuxName == name {
			t.Fatalf("sibling leaked into top Shells with WorkDir=%q", shell.WorkDir)
		}
	}
	if len(p.shells) != 1 || p.shells[0].TmuxName != "sidecar-sh-sidecar-1" {
		t.Fatalf("top Shells = %v, want only the current workDir shell", shellNames(p.shells))
	}

	reloaded, err := LoadShellManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	def := reloaded.FindShell(name)
	if def == nil || def.WorkDir != sibling {
		t.Fatalf("persisted sibling = %+v, want WorkDir %q", def, sibling)
	}

	_, nested := p.findNestedShell(name)
	if nested == nil {
		t.Fatal("sibling is no longer a nested row")
	}
	if nested.WorkDir != sibling {
		t.Fatalf("nested WorkDir = %q, want %q", nested.WorkDir, sibling)
	}

	leaked := &ShellSession{Name: "sibling", TmuxName: name, WorkDir: sibling, IsOrphaned: true}
	result := mergeShellState(shellMergeInput{
		Existing: append(append([]*ShellSession{}, p.shells...), leaked),
		Manifest: reloaded.Shells,
		WorkDir:  current,
	})
	for _, shell := range result.Shells {
		if shell.TmuxName == name {
			t.Fatalf("merge orphaned the sibling into top Shells: %+v", shell)
		}
	}
}

func TestShellToDefinitionKeepsWorkDir(t *testing.T) {
	got := shellToDefinition(&ShellSession{
		Name: "review", TmuxName: "sidecar-sh-feature-1", WorkDir: "/wt/feature",
	})
	if got.WorkDir != "/wt/feature" {
		t.Fatalf("WorkDir = %q", got.WorkDir)
	}
}

func TestEmptyWorktreeHasNoNest(t *testing.T) {
	p := nestedSidebarPlugin(t)
	p.worktrees = append(p.worktrees, &Worktree{Name: "empty", Path: filepath.Join(t.TempDir(), "empty"), Key: "empty"})
	items := p.visibleSidebarItems()
	var emptyChildren int
	for _, item := range items {
		if item.kind == navKindWorktree && p.worktrees[item.worktreeIdx].Name == "empty" {
			continue
		}
		if item.kind == navKindNestedShell && p.worktrees[item.worktreeIdx].Name == "empty" {
			emptyChildren++
		}
	}
	if emptyChildren != 0 {
		t.Fatalf("empty worktree grew %d nested rows", emptyChildren)
	}
}

// D on a nested row is the shell's own delete, not the parent worktree's. A
// shell reachable in the list is a shell that can be closed there; needing `W`
// to switch into its worktree first would make the nest a view-only projection.
func TestNestedShellAnswersDeleteWithoutSwitchingWorktrees(t *testing.T) {
	p := nestedSidebarPlugin(t)
	const session = "sidecar-sh-sidecar-feature-1"
	parent, shell := p.findNestedShell(session)
	shell.Agent = &Agent{Type: AgentShell, TmuxSession: session, TmuxPane: "%9"}
	p.selectNestedShell(parent, session)

	p.handleListKeys(keyPressFor("D"))

	if p.viewMode != ViewModeConfirmDeleteShell {
		t.Fatalf("view mode = %v, want the shell delete confirmation", p.viewMode)
	}
	if p.deleteConfirmShell == nil || p.deleteConfirmShell.TmuxName != session {
		t.Fatalf("confirming deletion of %#v, want the nested shell", p.deleteConfirmShell)
	}
	if p.deleteConfirmWorktree != nil {
		t.Fatalf("D armed the parent worktree's deletion: %#v", p.deleteConfirmWorktree)
	}

	// Confirming kills the session; the row leaves the nest when the kill lands.
	installSuccessfulFakeTmux(t)
	if cmd := p.executeShellDelete(); cmd == nil {
		t.Fatal("confirming produced no kill")
	} else {
		cmd()
	}
	updated, _ := p.Update(ShellKilledMsg{SessionName: session})
	p = updated.(*Plugin)
	if _, still := p.findNestedShell(session); still != nil {
		t.Fatal("the killed nested shell is still in the list")
	}
	if p.selectedNestedTmux != "" || p.shellSelected || p.selectedIdx != parent {
		t.Fatalf("selection after delete: nested=%q shell=%v idx=%d, want the parent worktree",
			p.selectedNestedTmux, p.shellSelected, p.selectedIdx)
	}
}

// The same holds when a nested shell exits on its own.
func TestNestedShellThatDiesLeavesTheNest(t *testing.T) {
	p := nestedSidebarPlugin(t)
	const session = "sidecar-sh-sidecar-feature-1"
	parent, _ := p.findNestedShell(session)
	p.selectNestedShell(parent, session)

	updated, _ := p.Update(ShellSessionDeadMsg{TmuxName: session})
	p = updated.(*Plugin)
	if _, still := p.findNestedShell(session); still != nil {
		t.Fatal("a dead nested shell stayed in the list")
	}
}
