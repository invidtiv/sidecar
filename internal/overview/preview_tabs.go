package overview

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/features"
	appmsg "github.com/marcus/sidecar/internal/msg"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/panereposition"
	"github.com/marcus/sidecar/internal/state"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/termpanes"
	"github.com/marcus/sidecar/internal/termpreview"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/ui"
	"github.com/marcus/sidecar/internal/workspacediff"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

const (
	previewGitRegionKind    = "global-preview-git"
	previewActionRegionKind = "global-preview-action"
	previewDiffDividerKind  = "global-preview-diff-divider"
)

type previewDiffDividerHit struct{}

// previewGitHit marks the Git action chip (a jump, not a tab).
type previewGitHit struct{}

// previewActionHit marks a Diff or Task action chip (a jump, not a tab).
type previewActionHit int

const (
	previewActionDiff previewActionHit = iota
	previewActionTask
)

func gitActionChip() string {
	return styles.RenderPillWithStyle("Git", styles.BarChip, nil)
}

func diffActionChip() string {
	return styles.RenderPillWithStyle("Diff", styles.BarChip, nil)
}

func taskActionChip() string {
	return styles.RenderPillWithStyle("Task", styles.BarChip, nil)
}

func (m *Model) previewActionChips() []string {
	chips := []string{diffActionChip()}
	if workspace, ok := m.SelectedWorkspace(); ok && workspace.TaskID != "" {
		chips = append(chips, taskActionChip())
	}
	return chips
}

func (m *Model) clickPreviewAction(hit previewActionHit) tea.Cmd {
	if m.PreviewInteractive() {
		_ = m.exitPreviewInteractive()
	}
	if !features.IsEnabled(features.WorkspaceDocPanes.Name) {
		return appmsg.ShowFlash(features.WorkspaceDocPanesDisabledDiff)
	}
	workspace, ok := m.SelectedWorkspace()
	if !ok {
		return nil
	}
	if hit == previewActionTask {
		if workspace.TaskID == "" {
			return nil
		}
		return m.openPreviewIssue(workspace.TaskID)
	}
	return m.openPreviewDiff(workspacediff.WorkingTreeTarget())
}

// previewDiffPath is the checkout workspacediff should read. Shells have no
// worktree of their own; the mini-diff is the project's main checkout.
func previewDiffPath(workspace workspaceinventory.Workspace) string {
	if workspace.Kind == workspaceinventory.KindShell {
		return workspace.ProjectRoot
	}
	return workspace.Path
}

func (m *Model) applyDiffSnapshot(msg workspacediff.SnapshotMsg) tea.Cmd {
	return m.applyPreviewDiffSnapshot(msg)
}

func (m *Model) applyCommitDetail(msg workspacediff.CommitDetailMsg) {
	_ = m.applyPreviewDiffCommit(msg)
}

func (m *Model) persistDiffViewMode() {
	switch m.diff.ViewMode {
	case workspacediff.ViewSideBySide:
		_ = state.SetWorkspaceDiffMode("side-by-side")
	case workspacediff.ViewFullFile:
		_ = state.SetWorkspaceDiffMode("full-file")
	default:
		_ = state.SetWorkspaceDiffMode("unified")
	}
}

func (m *Model) registerPreviewActionRegions(box termpreview.Box) {
	if box.W < 1 {
		return
	}
	workspace, ok := m.SelectedWorkspace()
	if !ok {
		return
	}
	chips, actionStart, gitIndex := m.previewHitChips(workspace)
	if len(chips) == 0 {
		return
	}
	hintFloor := 0
	if m.PreviewInteractive() {
		hintFloor = len([]rune(m.interactiveHints()))
	}
	for i, placement := range termpreview.LayoutChips(chips, box.W, hintFloor) {
		if !placement.Drawn {
			continue
		}
		if i == gitIndex {
			m.workspacesMouse.HitMap.AddRect(previewGitRegionKind, box.X+placement.Col, box.Y, placement.Width, 1, previewGitHit{})
			continue
		}
		if i < actionStart {
			continue
		}
		hit := previewActionDiff
		if i > actionStart {
			hit = previewActionTask
		}
		m.workspacesMouse.HitMap.AddRect(previewActionRegionKind, box.X+placement.Col, box.Y, placement.Width, 1, hit)
	}
}

// previewHitChips is the header chips that have hit targets. Git and Diff/Task
// action chips are registered even while typing, so they can jump without
// sending a letter to the pane. The identity chip is drawn but is not a hit.
func (m *Model) previewHitChips(workspace workspaceinventory.Workspace) (chips []string, actionStart, gitIndex int) {
	chips = m.previewHeaderChips(workspace)
	actionStart = 1
	if workspace.IsMain && workspace.Kind != workspaceinventory.KindShell {
		actionStart = 0
	}
	gitIndex = -1
	if m.canOpenInGit() {
		gitIndex = len(chips) - 1
	}
	return chips, actionStart, gitIndex
}

func (m *Model) renderOutputTerminal(width, height int) string {
	return m.renderOutputTerminalLeaf(m.primaryTerminalLeaf().ID, panelayout.Terminal, width, height)
}

func (m *Model) renderOutputTerminalLeaf(leafID int, kind panelayout.Kind, width, height int) string {
	workspace, ok := m.SelectedWorkspace()
	if !ok {
		// A host health row is not a workspace, so it lands here — and it is
		// the row most in need of explaining itself. "No workspace selected"
		// on the row that says a machine is unreachable is the least useful
		// thing this pane could say.
		message := m.HostHealthDetail(m.workspaces.SelectedID())
		if message == "" {
			message = "No workspace selected"
		}
		return termpreview.RenderBuffer(termpreview.RenderBufferInput{
			Width: width, Height: height, Message: message,
			DefaultBackground: m.terminalDefaultBackground,
		})
	}

	leaf := m.terminalLeaf(leafID)
	state := m.terminalState(leafID)
	interactive := leaf.Interactive && m.preview.paneFocus == leafID && state.terminal != nil && state.terminal.IsActive()
	chips := m.previewHeaderChips(workspace)
	if kind == panelayout.Shell {
		chips = []string{previewChip(leaf.Name, m.preview.paneFocus == leafID && m.previewOwnsChrome())}
	}
	hints := "typing · " + m.InteractiveExitKey() + " or esc esc to stop"
	if !interactive {
		hints = previewHints(workspace, m.PreviewFocused())
		if kind == panelayout.Shell {
			hints = ""
			if m.preview.paneFocus == leafID && m.previewOwnsChrome() {
				hints = "enter interactive"
			}
		}
	}
	message := m.preview.reason
	if kind == panelayout.Shell {
		message = ""
		if leaf.Buffer == nil {
			message = "Starting terminal..."
		}
	}
	buffer := leaf.Buffer
	if state.terminal != nil && state.terminal.IsActive() {
		buffer = state.terminal.Buffer()
	}
	// A remote row with no live channel still has something true to show: the
	// capture the host's own status pass already took and shipped with the
	// snapshot. It costs nothing extra — the host took it either way — and it
	// is the difference between "that machine has a blocked agent" and "that
	// machine has a blocked agent, and here is the question it is asking".
	if buffer == nil && workspace.Remote() {
		if snapshot := remotePreviewSnapshot(workspace); snapshot != "" {
			if message == "" {
				message = "Last seen on " + workspace.HostID
			}
			message += "\n\n" + snapshot
		}
	}
	if message != "" {
		message += "\n\n" + previewMetadata(workspace)
	}
	base, _ := tty.BufferBase(buffer)
	input := tty.ViewportInput{Buffer: buffer, AbsoluteBase: base, Width: width, Height: height - termpreview.HeaderRows, Scrollbar: true, TrimTrailing: tty.TrimsTrailingRows(interactive)}
	placement := tty.PlaceWindow(&leaf.Freeze, leaf.Scroll)
	input.Offset, input.OffsetFromBottom, input.Follow = placement.Offset, placement.FromBottom, placement.Follow
	if state.terminal != nil && state.terminal.IsActive() {
		input.PaneWidth, input.PaneHeight = state.terminal.PaneSize()
		if interactive {
			input.Interactive = true
			input.CursorRow, input.CursorCol, input.CursorVisible = state.terminal.CursorState()
		}
	}
	_, total := tty.BufferBase(input.Buffer)
	layout := tty.FitViewport(input)
	hints = m.appendTerminalWindowStatus(leaf, state, styles.Muted.Render(hints), input, layout, width, chips, interactive)
	hints = m.appendPreviewTerminalSearchStatus(hints)
	// Same resolution the project surface answers: one config rule for how far
	// carried backgrounds reach, so a pane cannot render differently depending
	// on which surface is showing it.
	terminalCfg := m.TerminalConfig()
	header := panereposition.ReserveHeader(width, kind == panelayout.Shell)
	return termpreview.RenderBuffer(termpreview.RenderBufferInput{
		Width: width, Height: height, Chips: chips, Hints: hints,
		DefaultBackground: m.terminalDefaultBackground,
		Layout:            layout, Buffer: input.Buffer, AbsoluteBase: input.AbsoluteBase,
		TotalItems: total, PaneHeight: input.PaneHeight, Interactive: input.Interactive,
		Follow: input.Follow, Selection: &leaf.Selection, TabWidth: tty.DefaultTabWidth,
		Message: message, Decorate: m.previewTerminalDecorator(leaf),
		Backgrounds: terminalCfg.Backgrounds, BackgroundSpanMax: terminalCfg.BackgroundSpanMax,
		BarStyle:      ui.ScrollbarStyle{Thumb: ui.HandleStateFrom(false, state.termBar.active)},
		Analyzer:      leaf.RowAnalyzer,
		LayoutButton:  header.LayoutW > 0,
		LayoutHovered: m.hoverPreviewLayout == leafID,
		// A split terminal is closable exactly like any other non-primary leaf,
		// the same header × the project surface draws on its split.
		CloseButton:  header.CloseW > 0,
		CloseHovered: kind == panelayout.Shell && m.previewCloseHover && m.hoverPreviewClose == panelayout.Shell,
	})
}

func (m *Model) appendTerminalWindowStatus(leaf *termpanes.Leaf, state *previewTerminalState, hints string, input tty.ViewportInput, layout tty.Viewport, width int, chips []string, interactive bool) string {
	budget := width - globalPanelOverhead
	for _, chip := range chips {
		budget -= ansi.StringWidth(chip) + 1
	}
	budget = max(budget, 1)
	mouseReporting := state.terminal != nil && state.terminal.IsActive() && state.terminal.PaneMouseReporting()
	notes := tty.WindowStatus(tty.WindowStatusInput{Layout: layout, AbsoluteBase: input.AbsoluteBase, LoadingOlder: leaf.History.Loading, MouseReporting: mouseReporting, PaneLive: interactive, PaneWidth: input.PaneWidth, PaneHeight: input.PaneHeight, LiveEdgeKey: m.previewLiveEdgeKey()})
	if leaf.History.Loading {
		for i, note := range notes {
			if strings.Contains(note.Compact, "loading") || strings.Contains(note.Text, "loading") {
				notes = append([]tty.StatusNote{note}, append(notes[:i], notes[i+1:]...)...)
				break
			}
		}
	}
	return tty.AppendStatus(hints, notes, budget, func(note string) string { return styles.Muted.Render(note) })
}

func (m *Model) previewHeaderChips(workspace workspaceinventory.Workspace) []string {
	chips := []string{previewChip(workspace.Name, m.PreviewFocused())}
	if workspace.IsMain && workspace.Kind != workspaceinventory.KindShell {
		chips = m.previewActionChips()
	} else {
		chips = append(chips, m.previewActionChips()...)
	}
	if m.canOpenInGit() {
		chips = append(chips, gitActionChip())
	} else if workspace.ProjectName != "" && (!workspace.IsMain || workspace.Kind == workspaceinventory.KindShell) {
		chips = append(chips, styles.Muted.Render(workspace.ProjectName))
	}
	return chips
}
