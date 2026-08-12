package overview

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/termpreview"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

// The global Workspaces preview is one read-only terminal box. Not a pane tree,
// not a docview, not a workspace plugin instance per project: the plan is
// explicit that rendering pane-tree layouts globally is deferred, and that a
// project whose own preview has a document open still previews here as its
// selected pane's captured output alone.
//
// It is also deliberately cheap. There is exactly one capture in flight at a
// time — for the selected pane and nothing else — started by a selection change
// and repeated only while the tab is visible. No control-mode client, no output
// buffer, no watcher, and none of them per project. A capture that arrives for
// a superseded generation or a pane the cursor has already left is dropped.
//
// Nothing captured here is persisted. The snapshot lives in memory for as long
// as the tab is on screen and is released when it is not, so a pane's contents
// never reach config, state, or diagnostics.

const (
	// previewCaptureLines is the scrollback each capture asks tmux for. It is
	// bounded on purpose: the preview is for recognising a workspace, not for
	// reading its history, and the owning project's Workspaces plugin is one
	// Enter away when the full buffer is what the user wants.
	previewCaptureLines = 200

	// previewFocusedPoll / previewVisiblePoll are the adaptive cadences. A
	// focused preview is what the user is reading, so it refreshes fastest; a
	// visible-but-unfocused one tracks the board's own live cadence. Anything
	// hidden, or with no live pane behind it, does not poll at all.
	previewFocusedPoll = 2 * time.Second
	previewVisiblePoll = livePollEvery

	// globalSidebarPercent is the list's share of the tab. The floors below it
	// are the same idea as the project plugin's: neither pane may be squeezed
	// into an unreadable column.
	globalSidebarPercent  = 42
	globalListMinWidth    = 28
	globalPreviewMinWidth = 44
	globalDividerWidth    = 1
)

// previewFocus is which of the two panes owns the keyboard.
type previewFocus uint8

const (
	focusList previewFocus = iota
	focusPreview
)

// previewMsg is one completed capture, tagged with everything needed to reject
// it: the generation it was started under and the exact workspace and pane it
// was started for.
type previewMsg struct {
	Generation  int
	WorkspaceID string
	PaneID      string
	Output      string
	Err         error
	At          time.Time
}

// previewPollMsg is the adaptive refresh tick for an unchanged live pane.
type previewPollMsg struct {
	Generation  int
	WorkspaceID string
}

// previewState is the whole of the global preview: which item it is showing,
// what was captured, how far back the reader has scrolled, and whether anyone
// is looking at it.
type previewState struct {
	visible bool
	focus   previewFocus
	// full is the narrow layout's state: at widths that cannot sustain two
	// useful panes the list is full width, and the preview replaces it rather
	// than sharing it.
	full bool

	// generation supersedes in-flight captures. Every selection change, refresh,
	// and visibility change bumps it, so a slow capture cannot paint over a pane
	// the user has already moved off.
	generation  int
	workspaceID string
	snapshot    termpreview.Snapshot
	reason      string
	offset      int
	scheduled   bool
	requestedAt time.Time

	metrics PreviewMetrics
}

// PreviewMetrics is what the cadence was tuned against: how many captures the
// preview actually ran, how many late ones it threw away, and how long the
// newest selection waited for its first frame. It is read by tests and by the
// trace log; it is not persisted.
type PreviewMetrics struct {
	Captures    int
	Rejected    int
	Polls       int
	LastLatency time.Duration
}

// PreviewMetrics returns a copy of the preview's work counters.
func (m *Model) PreviewMetrics() PreviewMetrics { return m.preview.metrics }

// WorkspacesPreviewVisible reports whether the preview believes anyone is
// looking at it. It exists so the app can prove that scope and tab changes
// actually reach the thing that decides whether to capture a pane.
func (m *Model) WorkspacesPreviewVisible() bool { return m.preview.visible }

func (m *Model) now() time.Time {
	if m.collector.Now != nil {
		return m.collector.Now()
	}
	return time.Now()
}

// SetWorkspacesVisible tells the preview whether anyone is looking at it. It is
// the switch behind "polls only while the global Workspaces tab is visible":
// becoming visible captures the selected pane immediately, and becoming hidden
// cancels the in-flight capture, stops the poll, and drops the captured output.
func (m *Model) SetWorkspacesVisible(visible bool) tea.Cmd {
	if m.preview.visible == visible {
		return nil
	}
	m.preview.visible = visible
	// Moving between the catalog's two projections supersedes any activation
	// still being validated. Agents and Workspaces share one cache, one
	// generation, and one poll, so nothing else here marks the moment the user
	// left the view they pressed enter on — and a destination that opens itself
	// after the user moved on is exactly the surprise the generation/request
	// machinery exists to prevent.
	m.requestID++
	if !visible {
		m.releasePreview()
		return nil
	}
	return m.previewSelect()
}

// releasePreview cancels whatever the preview was doing and forgets what it
// captured. Captured terminal contents are memory-only, and a tab nobody is
// looking at has no reason to keep holding them.
func (m *Model) releasePreview() {
	m.preview.generation++
	m.preview.workspaceID = ""
	m.preview.snapshot = termpreview.Snapshot{}
	m.preview.reason = ""
	m.preview.offset = 0
	m.preview.scheduled = false
}

// previewSync starts a capture when the selection has moved to a different
// item. It is called after every interaction and after every inventory
// increment, so the preview follows the cursor without the list needing to know
// that a preview exists.
func (m *Model) previewSync() tea.Cmd {
	if !m.preview.visible {
		return nil
	}
	selected, ok := m.workspaces.Selected()
	if !ok {
		if m.preview.workspaceID != "" {
			m.releasePreview()
		}
		return nil
	}
	if selected.ID == m.preview.workspaceID {
		return nil
	}
	return m.previewSelect()
}

// previewSelect binds the preview to the current selection and captures it
// straight away. Every caller is a path the user can feel — a selection change,
// the tab becoming visible, an explicit refresh — so none of them waits out a
// poll interval before showing anything.
func (m *Model) previewSelect() tea.Cmd {
	m.preview.generation++
	m.preview.scheduled = false
	m.preview.snapshot = termpreview.Snapshot{}
	m.preview.offset = 0
	m.preview.reason = ""

	workspace, ok := m.SelectedWorkspace()
	if !ok {
		m.preview.workspaceID = ""
		return nil
	}
	m.preview.workspaceID = workspace.ID

	// An item with no single live pane behind it is not captured at all. There
	// is nothing to read, and guessing among several panes is exactly what the
	// catalog refuses to do.
	if reason, unavailable := previewUnavailable(workspace); unavailable {
		m.preview.reason = reason
		return nil
	}
	return m.capturePreviewCmd(workspace)
}

// previewUnavailable explains, in the user's terms, why an item has no live
// preview. The empty reason means it does.
func previewUnavailable(workspace workspaceinventory.Workspace) (string, bool) {
	switch {
	case workspace.Ambiguous:
		return "Several panes match this workspace — refusing to guess which one is yours", true
	case workspace.PaneID == "":
		return "No live session for this workspace", true
	case !workspace.Live:
		return "The session for this workspace has ended", true
	}
	return "", false
}

// capturePreviewCmd captures exactly one pane: the selected one. The capture
// function is the collector's, so a test injects it in one place and the
// browser never opens a second way to talk to tmux.
func (m *Model) capturePreviewCmd(workspace workspaceinventory.Workspace) tea.Cmd {
	capture := m.collector.Capture
	if capture == nil {
		return nil
	}
	generation, id, pane := m.preview.generation, workspace.ID, workspace.PaneID
	m.preview.requestedAt = m.now()
	m.preview.metrics.Captures++
	m.tracef("preview generation=%d capture workspace=%s pane=%s", generation, id, pane)
	return func() tea.Msg {
		output, err := capture(pane, previewCaptureLines)
		return previewMsg{Generation: generation, WorkspaceID: id, PaneID: pane, Output: output, Err: err, At: time.Now()}
	}
}

// applyPreview accepts a capture if it is still the one the surface asked for.
func (m *Model) applyPreview(msg previewMsg) tea.Cmd {
	if msg.Generation != m.preview.generation || msg.WorkspaceID != m.preview.workspaceID {
		m.preview.metrics.Rejected++
		m.tracef("preview generation=%d drained stale_generation=%d workspace=%s", m.preview.generation, msg.Generation, msg.WorkspaceID)
		return nil
	}
	m.preview.metrics.LastLatency = m.now().Sub(m.preview.requestedAt)
	if msg.Err != nil {
		m.preview.snapshot = termpreview.Snapshot{PaneID: msg.PaneID, Err: msg.Err, CapturedAt: msg.At}
		m.preview.reason = "Could not read this pane: " + msg.Err.Error()
		return m.schedulePreviewPoll()
	}
	m.preview.reason = ""
	m.preview.snapshot = termpreview.Snapshot{
		PaneID:     msg.PaneID,
		Lines:      termpreview.SnapshotLines(msg.Output),
		CapturedAt: msg.At,
	}
	return m.schedulePreviewPoll()
}

// schedulePreviewPoll arms the next refresh, or nothing at all. This is the
// whole of the adaptive cadence: hidden previews and previews with no live pane
// behind them schedule no work, and a visible one refreshes faster while it has
// focus than while it is merely on screen.
func (m *Model) schedulePreviewPoll() tea.Cmd {
	interval := m.previewInterval()
	if interval == 0 || m.preview.scheduled {
		return nil
	}
	generation, id := m.preview.generation, m.preview.workspaceID
	m.preview.scheduled = true
	return tea.Tick(interval, func(time.Time) tea.Msg {
		return previewPollMsg{Generation: generation, WorkspaceID: id}
	})
}

// previewInterval is the cadence the preview is currently owed, and zero for
// "do no work at all": hidden, nothing selected, or an item with no live pane
// behind it.
func (m *Model) previewInterval() time.Duration {
	if !m.preview.visible || m.preview.workspaceID == "" || m.preview.reason != "" {
		return 0
	}
	if m.preview.focus == focusPreview {
		return previewFocusedPoll
	}
	return previewVisiblePoll
}

// pollPreview re-captures an unchanged selection.
func (m *Model) pollPreview(msg previewPollMsg) tea.Cmd {
	if msg.Generation != m.preview.generation || msg.WorkspaceID != m.preview.workspaceID {
		m.preview.metrics.Rejected++
		return nil
	}
	m.preview.scheduled = false
	if !m.preview.visible {
		return nil
	}
	workspace, ok := m.SelectedWorkspace()
	if !ok {
		return nil
	}
	if reason, unavailable := previewUnavailable(workspace); unavailable {
		m.preview.reason = reason
		m.preview.snapshot = termpreview.Snapshot{}
		return nil
	}
	m.preview.metrics.Polls++
	return m.capturePreviewCmd(workspace)
}

// PreviewFocused reports that the read-only preview owns the keyboard.
func (m *Model) PreviewFocused() bool { return m.preview.focus == focusPreview }

// previewKey handles a key while the preview has focus. Every one of them
// scrolls or moves focus: there is no path here that reaches a terminal, which
// is what "read-only" has to mean in the key routing and not only in the docs.
func (m *Model) previewKey(key string) (bool, tea.Cmd) {
	page := max(1, m.previewRows()/2)
	switch key {
	case "left", "h", "esc":
		m.focusList()
		return true, nil
	case "j", "down":
		m.scrollPreview(-1)
	case "k", "up":
		m.scrollPreview(1)
	case "ctrl+d", "pgdown":
		m.scrollPreview(-page)
	case "ctrl+u", "pgup":
		m.scrollPreview(page)
	case "G", "end":
		m.preview.offset = 0
	case "g", "home":
		m.preview.offset = m.previewMaxOffset()
	default:
		return false, nil
	}
	return true, nil
}

// focusPreviewPane moves focus right. On a narrow tab there is no room for two
// panes, so the preview takes the whole width instead of sharing it.
//
// Focusing captures only when there is nothing on screen yet. The faster
// focused cadence takes effect at the next tick rather than by cancelling the
// one already in flight: moving focus back and forth must not be a way to run
// captures faster than the cadence allows.
func (m *Model) focusPreviewPane() tea.Cmd {
	m.preview.focus = focusPreview
	if m.previewNarrow() {
		m.preview.full = true
	}
	if m.preview.reason == "" && m.preview.snapshot.Empty() {
		return m.previewSelect()
	}
	return nil
}

func (m *Model) focusList() {
	m.preview.focus = focusList
	m.preview.full = false
}

func (m *Model) scrollPreview(delta int) {
	m.preview.offset = min(max(m.preview.offset+delta, 0), m.previewMaxOffset())
}

func (m *Model) previewMaxOffset() int {
	return max(0, len(m.preview.snapshot.Lines)-m.previewRows())
}

// previewRows is the body height of the preview box at the last rendered size.
func (m *Model) previewRows() int {
	return max(1, m.height-termpreview.HeaderRows)
}

// previewNarrow reports a tab too narrow to sustain two useful panes.
func (m *Model) previewNarrow() bool {
	return m.width < globalListMinWidth+globalDividerWidth+globalPreviewMinWidth
}

// previewSplit is the outer two-pane split, taken from the same shared layer
// the project plugin's sidebar/preview split is taken from.
func (m *Model) previewSplit(width int) termpreview.Split {
	return termpreview.SplitFor(width, termpreview.SplitConfig{
		SidebarVisible: true,
		SidebarPercent: globalSidebarPercent,
		DividerWidth:   globalDividerWidth,
		SidebarMin:     globalListMinWidth,
		PreviewMin:     globalPreviewMinWidth,
	})
}

// renderPreview draws the read-only terminal box: the selected item's identity
// on the header row, then its captured output or the reason there is none.
// selectedPaneSource is the global browser's implementation of the shared
// snapshot seam. It is the only way the renderer reaches captured output, which
// is what keeps the surface read-only by construction: the seam has one method,
// it returns a value, and there is nothing on it to write through.
type selectedPaneSource struct{ state *previewState }

var _ termpreview.Source = selectedPaneSource{}

func (s selectedPaneSource) Snapshot() (termpreview.Snapshot, bool) {
	if s.state == nil || s.state.workspaceID == "" {
		return termpreview.Snapshot{}, false
	}
	return s.state.snapshot, !s.state.snapshot.Empty()
}

func (m *Model) renderPreview(width, height int) string {
	if width < 1 || height < 1 {
		return ""
	}
	workspace, ok := m.SelectedWorkspace()
	if !ok {
		return termpreview.RenderReadOnly(termpreview.Snapshot{}, termpreview.ReadOnlyOptions{
			Width: width, Height: height, Message: "No workspace selected",
		}).View
	}

	chips := []string{previewChip(workspace.Name, m.PreviewFocused())}
	if workspace.ProjectName != "" {
		chips = append(chips, styles.Muted.Render(workspace.ProjectName))
	}

	hints := previewHints(workspace, m.preview.snapshot, m.now())
	message := m.preview.reason
	if message != "" {
		message += "\n\n" + previewMetadata(workspace)
	}
	snapshot, _ := selectedPaneSource{state: &m.preview}.Snapshot()
	result := termpreview.RenderReadOnly(snapshot, termpreview.ReadOnlyOptions{
		Width:     width,
		Height:    height,
		Chips:     chips,
		Hints:     styles.Muted.Render(hints),
		Offset:    m.preview.offset,
		Message:   message,
		Scrollbar: true,
	})
	return result.View
}

func previewChip(name string, focused bool) string {
	if name == "" {
		name = "Workspace"
	}
	if focused {
		return styles.Title.Render("▸ " + name)
	}
	return styles.Title.Render(name)
}

// previewHints is the header's right region: what the item is doing and how
// fresh the reading is, plus the scroll state when the reader has moved back.
func previewHints(workspace workspaceinventory.Workspace, snap termpreview.Snapshot, now time.Time) string {
	parts := make([]string, 0, 3)
	if workspace.HasAgent() && workspace.Presentation.Label != "" {
		parts = append(parts, workspace.Presentation.Label)
	} else if workspace.Live {
		parts = append(parts, "live")
	}
	if !snap.CapturedAt.IsZero() {
		if age := now.Sub(snap.CapturedAt); age >= time.Second {
			parts = append(parts, fmt.Sprintf("captured %ds ago", int(age.Seconds())))
		} else {
			parts = append(parts, "captured now")
		}
	}
	parts = append(parts, "read-only")
	return strings.Join(parts, " · ")
}

// previewMetadata is what the pane shows instead of output: the identity a user
// needs to decide whether to open the item in its owning project.
func previewMetadata(workspace workspaceinventory.Workspace) string {
	lines := []string{"project  " + workspace.ProjectName}
	lines = append(lines, "kind     "+string(workspace.Kind))
	if workspace.Branch != "" {
		lines = append(lines, "branch   "+workspace.Branch)
	}
	if workspace.TaskID != "" {
		lines = append(lines, "task     "+workspace.TaskID)
	}
	if workspace.Provider != "" {
		lines = append(lines, "agent    "+workspace.Provider)
	}
	if workspace.TmuxName != "" {
		lines = append(lines, "session  "+workspace.TmuxName)
	}
	if workspace.Path != "" {
		lines = append(lines, "path     "+workspace.Path)
	}
	return strings.Join(lines, "\n")
}
