package filebrowser

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/app"
	"github.com/marcus/sidecar/internal/clip"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/inlineedit"
	"github.com/marcus/sidecar/internal/msg"
	"github.com/marcus/sidecar/internal/tty"
)

// InlineEditStartedMsg is sent when inline edit mode starts successfully.
type InlineEditStartedMsg struct {
	SessionName   string
	FilePath      string
	OriginalMtime time.Time // File mtime before editing (to detect changes)
	Editor        string    // Editor command used (vim, nano, emacs, etc.)
	Activation    uint64
	Epoch         uint64
}

// InlineEditExitedMsg is sent when inline edit mode exits.
type InlineEditExitedMsg struct {
	FilePath   string
	Activation uint64
	Epoch      uint64
}

// enterInlineEditMode starts inline editing for the specified file.
// Creates a tmux session running the user's editor and delegates to tty.Model.
// lineNo is 0-indexed; converted to 1-indexed for editor.
func (p *Plugin) enterInlineEditMode(path string, lineNo int) tea.Cmd {
	// Check feature flag
	if !features.IsEnabled(features.TmuxInlineEdit.Name) {
		return p.openFile(path)
	}

	fullPath := filepath.Join(p.ctx.WorkDir, path)

	editor := tty.ResolveEditor()

	// Size the session to the viewport it will be rendered into, not to the whole
	// plugin rect. Passing p.width/p.height created the pane several rows taller
	// than the visible area, so the editor laid out its status and command lines
	// off the bottom of the pane (td-a87445).
	editorWidth := p.calculateInlineEditorWidth()
	editorHeight := p.calculateInlineEditorHeight()
	p.edit.Activation++
	activation := p.edit.Activation
	var epoch uint64
	if p.ctx != nil {
		epoch = p.ctx.Epoch
	}

	return func() tea.Msg {
		if !tty.EditorAvailable() {
			// Fall back to external editor
			return nil
		}

		// Capture original mtime to detect changes later
		var origMtime time.Time
		if info, err := os.Stat(fullPath); err == nil {
			origMtime = info.ModTime()
		}

		session, err := tty.StartEditorSession(tty.EditorSessionOptions{
			NamePrefix: "sidecar-edit-",
			Editor:     editor,
			Path:       fullPath,
			Line:       lineNo,
			Width:      editorWidth,
			Height:     editorHeight,
		})
		if err != nil {
			return msg.ToastMsg{
				Message:  fmt.Sprintf("Failed to start editor: %v", err),
				Duration: 3 * time.Second,
				IsError:  true,
			}
		}

		return InlineEditStartedMsg{
			SessionName:   session.Name,
			FilePath:      path,
			OriginalMtime: origMtime,
			Editor:        session.Editor,
			Activation:    activation,
			Epoch:         epoch,
		}
	}
}

// handleInlineEditStarted processes the InlineEditStartedMsg and activates the tty model.
func (p *Plugin) handleInlineEditStarted(msg InlineEditStartedMsg) tea.Cmd {
	if msg.Activation != p.edit.Activation || p.ctx == nil || msg.Epoch != p.ctx.Epoch {
		return p.cleanupStaleInlineEditStart(msg)
	}
	p.activePane = PanePreview
	p.inlineEditOrigMtime = msg.OriginalMtime

	// Configure the tty model callbacks
	activation, epoch, filePath := msg.Activation, msg.Epoch, msg.FilePath
	p.edit.Model.OnExit = func() tea.Cmd {
		return func() tea.Msg {
			return InlineEditExitedMsg{FilePath: filePath, Activation: activation, Epoch: epoch}
		}
	}
	p.edit.Model.OnAttach = func() tea.Cmd {
		// Attach to full tmux session
		return p.attachToInlineEditSession()
	}

	// Enter interactive mode on the tty model, sized to the host viewport.
	enterCmd := p.editor().Begin(msg.SessionName, msg.Editor, msg.FilePath)

	// Show copy/paste hint toast on first entry
	if !p.inlineEditCopyPasteHintShown {
		p.inlineEditCopyPasteHintShown = true
		// A one-time key hint, not an event to keep.
		hintCmd := app.ShowFlash(fmt.Sprintf("Copy/paste: %s / %s", p.getInlineEditCopyKey(), p.getInlineEditPasteKey()))
		return tea.Batch(enterCmd, hintCmd)
	}
	return enterCmd
}

func (p *Plugin) cleanupStaleInlineEditStart(msg InlineEditStartedMsg) tea.Cmd {
	// A delayed start may refer to a name that tmux has since reused for the
	// currently active editor. Never let stale-message cleanup kill the target
	// that owns the live model activation.
	return p.edit.CleanupStale(msg.SessionName, msg.Editor)
}

// getInlineEditCopyKey returns the configured copy key for inline edit mode.
func (p *Plugin) getInlineEditCopyKey() string {
	if p.ctx != nil && p.ctx.Config != nil {
		if key := p.ctx.Config.Plugins.Workspace.InteractiveCopyKey; key != "" {
			return key
		}
	}
	return "alt+c"
}

// getInlineEditPasteKey returns the configured paste key for inline edit mode.
func (p *Plugin) getInlineEditPasteKey() string {
	if p.ctx != nil && p.ctx.Config != nil {
		if key := p.ctx.Config.Plugins.Workspace.InteractivePasteKey; key != "" {
			return key
		}
	}
	return "alt+v"
}

// copyInlineEditorOutputCmd copies the inline editor output to the clipboard.
func (p *Plugin) copyInlineEditorOutputCmd() tea.Cmd {
	empty := app.FlashMsg{Text: "No output to copy"}
	return clip.CopyFrom(
		func() (string, tea.Msg) {
			if p.edit.Model == nil || p.edit.Model.State == nil || p.edit.Model.State.OutputBuf == nil {
				return "", empty
			}
			lines := p.edit.Model.State.OutputBuf.Lines()
			stripped := make([]string, 0, len(lines))
			for _, line := range lines {
				stripped = append(stripped, ansi.Strip(line))
			}
			return strings.Join(stripped, "\n"), empty
		},
		func(r clip.Result, text string) tea.Msg {
			count := strings.Count(text, "\n") + 1
			return app.FlashMsg{Text: r.Message(fmt.Sprintf("Copied %d line(s)", count))}
		},
	)
}

// reattachInlineEditSession re-attaches to an existing tmux session after tab switch.
// Called when returning to a tab that was previously in edit mode.
func (p *Plugin) reattachInlineEditSession() tea.Cmd {
	if p.edit.Name == "" {
		return nil
	}
	p.activePane = PanePreview
	p.edit.Activation++
	activation := p.edit.Activation
	var epoch uint64
	if p.ctx != nil {
		epoch = p.ctx.Epoch
	}
	filePath := p.edit.Path

	// Configure the tty model callbacks (same as handleInlineEditStarted)
	p.edit.Model.OnExit = func() tea.Cmd {
		return func() tea.Msg {
			return InlineEditExitedMsg{FilePath: filePath, Activation: activation, Epoch: epoch}
		}
	}
	p.edit.Model.OnAttach = func() tea.Cmd {
		return p.attachToInlineEditSession()
	}

	// Enter interactive mode with the existing session
	return p.editor().Reopen()
}

// exitInlineEditMode cleans up inline edit state and kills the tmux session.
func (p *Plugin) exitInlineEditMode() {
	p.edit.Exit()
	p.inlineEditOrigMtime = time.Time{}
}

func (p *Plugin) ownsInlineEditMessage(activation, epoch uint64) bool {
	return p.ctx != nil && p.edit.OwnsMessage(activation, epoch, p.ctx.Epoch)
}

// isInlineEditSessionAlive checks if the tmux session for inline editing still exists.
// Returns false if the session has ended (vim quit).
func (p *Plugin) isInlineEditSessionAlive() bool {
	return p.edit.IsAlive()
}

func fullTmuxAttachEnabled() bool {
	return features.IsEnabled(features.TmuxFullAttach.Name)
}

func (p *Plugin) applyInlineEditorAttachKey() {
	if p.edit.Model == nil {
		return
	}
	if !fullTmuxAttachEnabled() {
		p.edit.Model.Config.AttachKey = ""
		return
	}
	key := tty.DefaultConfig().AttachKey
	if p.ctx != nil {
		if resolved := app.TerminalConfig(p.ctx.Config).AttachKey; resolved != "" {
			key = resolved
		}
	}
	p.edit.Model.Config.AttachKey = key
}

// attachToInlineEditSession attaches to the inline edit tmux session in full-screen mode.
func (p *Plugin) attachToInlineEditSession() tea.Cmd {
	if !fullTmuxAttachEnabled() || p.edit.Name == "" {
		return nil
	}

	sessionName := p.edit.Name
	p.exitInlineEditMode()

	return func() tea.Msg {
		// Suspend the TUI and attach to tmux
		return AttachToTmuxMsg{SessionName: sessionName}
	}
}

// AttachToTmuxMsg requests the app to suspend and attach to a tmux session.
type AttachToTmuxMsg struct {
	SessionName string
}

// editor returns the shared inline-edit session, binding the host contract on
// first use so a plugin built without New() still maps coordinates correctly.
func (p *Plugin) editor() *inlineedit.Session {
	if p.edit.Host == nil {
		p.edit.Host = p
	}
	return &p.edit
}

// EditorViewport implements inlineedit.Host: the PTY's content box.
func (p *Plugin) EditorViewport() (width, height int) {
	return p.calculateInlineEditorWidth(), p.calculateInlineEditorHeight()
}

// EditorOrigin implements inlineedit.Host: the top-left content cell in
// plugin-local coordinates.
func (p *Plugin) EditorOrigin() (x, y int, ok bool) {
	return p.inlineEditorOrigin()
}

// calculateInlineEditorWidth returns the content width for the inline editor.
// Must stay in sync with renderNormalPanes() preview width calculation.
func (p *Plugin) calculateInlineEditorWidth() int {
	if !p.treeVisible {
		return p.width - 4 // borders + padding (panelOverhead)
	}
	p.calculatePaneWidths()
	return p.previewWidth - 4 // borders + padding
}

// calculateInlineEditorHeight returns the content height for the inline editor.
// Account for pane borders, header lines, and tab line.
func (p *Plugin) calculateInlineEditorHeight() int {
	paneHeight := p.height
	if paneHeight < 4 {
		paneHeight = 4
	}
	innerHeight := paneHeight - 2 // pane borders

	// Subtract header lines (matches renderInlineEditorContent)
	contentHeight := innerHeight - 2 // header + empty line
	if len(p.tabs) > 1 {
		contentHeight-- // tab line
	}

	if contentHeight < 5 {
		contentHeight = 5
	}
	return contentHeight
}

// isInlineEditSupported checks if inline editing can be used for the given file.
func (p *Plugin) isInlineEditSupported(path string) bool {
	// Check feature flag
	if !features.IsEnabled(features.TmuxInlineEdit.Name) {
		return false
	}

	// Check if tmux is available
	if !tty.EditorAvailable() {
		return false
	}

	// Don't support inline editing for binary files
	if p.isBinary {
		return false
	}

	return true
}

// renderInlineEditorContent renders the inline editor within the preview pane area.
// This is called from renderPreviewPane() when inline edit mode is active.
func (p *Plugin) renderInlineEditorContent(visibleHeight int) string {
	// If showing exit confirmation, render that instead
	if p.edit.ShowExitConfirm {
		return p.renderExitConfirmation(visibleHeight)
	}

	var sb strings.Builder

	// Tab line (to match normal preview rendering)
	if len(p.tabs) > 1 {
		tabLine := p.renderPreviewTabs(p.previewWidth - 4)
		sb.WriteString(tabLine)
		sb.WriteString("\n")
	}

	// Header with file being edited and exit hint. The tmux pane can be larger
	// than this viewport when another sidecar instance drives the same session;
	// the shared header says what is hidden (td-73fa86).
	sb.WriteString(p.edit.EditingHeader(filepath.Base(p.edit.Path)))
	sb.WriteString("\n")

	// Calculate content height (account for tab line and header)
	contentHeight := visibleHeight
	if len(p.tabs) > 1 {
		contentHeight-- // tab line
	}
	contentHeight -= 2 // header + empty line

	// Render terminal content from tty model
	if p.edit.Model != nil {
		content := p.edit.Model.View()
		lines := strings.Split(content, "\n")

		// Limit to content height
		if len(lines) > contentHeight {
			lines = lines[:contentHeight]
		}

		sb.WriteString(strings.Join(lines, "\n"))
	}

	// Enforce total height constraint per AGENTS.md
	return lipgloss.NewStyle().Height(visibleHeight).Render(sb.String())
}

// renderExitConfirmation renders the exit confirmation dialog overlay.
func (p *Plugin) renderExitConfirmation(visibleHeight int) string {
	var sb strings.Builder

	// Tab line (keep consistent with editor view)
	if len(p.tabs) > 1 {
		tabLine := p.renderPreviewTabs(p.previewWidth - 4)
		sb.WriteString(tabLine)
		sb.WriteString("\n")
	}

	sb.WriteString(p.edit.RenderExitConfirm())

	return sb.String()
}

// handleExitConfirmationChoice processes the user's selection in the exit confirmation dialog.
func (p *Plugin) handleExitConfirmationChoice() (*Plugin, tea.Cmd) {
	p.edit.ShowExitConfirm = false

	switch p.edit.ConfirmSelection {
	case 0: // Save & Exit
		// Try to send editor-specific save-and-quit commands.
		// If unknown editor, we still proceed but skip the save attempt.
		p.edit.SaveAndQuit()

		// Give editor a moment to process, then kill session
		// (Session may already be dead from quit command, kill-session will fail silently)
		p.exitInlineEditMode()
		return p.processPendingClickAction()

	case 1: // Exit without saving
		// Kill session immediately, then process pending action
		p.exitInlineEditMode()
		return p.processPendingClickAction()

	case 2: // Cancel
		p.edit.ClearPendingClick()
		return p, nil
	}

	return p, nil
}

// processPendingClickAction handles the click that triggered exit confirmation.
func (p *Plugin) processPendingClickAction() (*Plugin, tea.Cmd) {
	region, data := p.edit.TakePendingClick()

	switch region {
	case "tree-item":
		// User clicked a tree item - select it
		if idx, ok := data.(int); ok {
			return p.selectTreeItem(idx)
		}
		// Fallback: if data is missing, load preview for current selection
		return p, p.loadCurrentTreeItemPreview()
	case "tree-pane":
		// User clicked tree pane background - focus tree and refresh preview
		p.activePane = PaneTree
		return p, p.loadCurrentTreeItemPreview()
	case "preview-tab":
		// User clicked a tab - switch to it using switchTab to trigger edit state restoration
		if idx, ok := data.(int); ok {
			return p, p.switchTab(idx)
		} else if len(p.tabs) > 1 {
			// Fallback: switch to a different tab than current
			newTab := 0
			if p.activeTab == 0 {
				newTab = 1
			}
			return p, p.switchTab(newTab)
		}
	}

	return p, nil
}

// loadCurrentTreeItemPreview returns a Cmd to load the preview for the currently selected tree item.
func (p *Plugin) loadCurrentTreeItemPreview() tea.Cmd {
	if p.tree == nil || p.treeCursor < 0 || p.treeCursor >= p.tree.Len() {
		return nil
	}
	node := p.tree.GetNode(p.treeCursor)
	if node == nil || node.IsDir {
		return nil
	}
	// Update previewFile so PreviewLoadedMsg is accepted
	p.previewFile = node.Path
	return LoadPreview(p.ctx.WorkDir, node.Path, p.ctx.Epoch)
}

// calculateInlineEditorMouseCoords converts screen coordinates to editor-relative
// coordinates. Returns (col, row, ok) 1-indexed for the SGR mouse protocol.
func (p *Plugin) calculateInlineEditorMouseCoords(x, y int) (col, row int, ok bool) {
	return p.editor().MouseCoords(x, y)
}

func (p *Plugin) inlineEditorOrigin() (x, y int, ok bool) {
	if p.width <= 0 || p.height <= 0 {
		return 0, 0, false
	}

	// Calculate preview pane X offset
	var previewX int
	if p.treeVisible {
		p.calculatePaneWidths()
		previewX = p.treeWidth + dividerWidth
	}

	// Content X offset: preview pane start + border(1) + padding(1)
	contentX := previewX + 2

	// Calculate Y offset based on input bars and pane structure
	contentY := 0

	// Account for input bars (content search, file op, line jump)
	if p.contentSearchMode || p.fileOpMode != FileOpNone || p.lineJumpMode {
		contentY++
		if p.fileOpMode != FileOpNone && p.fileOpError != "" {
			contentY++ // error line
		}
	}

	// Add pane border (top)
	contentY++

	// Add tab line if multiple tabs
	if len(p.tabs) > 1 {
		contentY++
	}

	// Add header line ("Editing: filename...")
	contentY++
	return contentX, contentY, true
}

// Cursor exposes the inline editor's native cursor in plugin-local coordinates.
func (p *Plugin) Cursor() *tea.Cursor {
	if !p.inlineEditorNativeActive() {
		return nil
	}
	return p.editor().Cursor(p.width, p.height)
}

func (p *Plugin) inlineEditorNativeActive() bool {
	return p.focused && p.activePane == PanePreview && p.edit.NativeActive() &&
		!p.projectSearchMode && !p.quickOpenMode && !p.infoMode && !p.blameMode
}

// PreferredMouseMode reduces idle hover traffic only while the inline terminal
// owns input. Modal and ordinary file-browser views retain all-motion hover.
func (p *Plugin) PreferredMouseMode() tea.MouseMode {
	return p.edit.PreferredMouseMode(p.inlineEditorNativeActive())
}

// forwardMousePressToInlineEditor sends a mouse press event to the inline editor.
// col and row are 1-indexed coordinates relative to the editor content area.
func (p *Plugin) forwardMousePressToInlineEditor(col, row int) tea.Cmd {
	return p.edit.ForwardMousePress(col, row)
}

// forwardMouseDragToInlineEditor sends a mouse drag/motion event to the inline editor.
// col and row are 1-indexed coordinates relative to the editor content area.
func (p *Plugin) forwardMouseDragToInlineEditor(col, row int) tea.Cmd {
	return p.edit.ForwardMouseDrag(col, row)
}

// forwardMouseReleaseToInlineEditor sends a mouse release event to the inline editor.
// col and row are 1-indexed coordinates relative to the editor content area.
func (p *Plugin) forwardMouseReleaseToInlineEditor(col, row int) tea.Cmd {
	return p.edit.ForwardMouseRelease(col, row)
}

// selectTreeItem selects the given tree item and loads its preview.
func (p *Plugin) selectTreeItem(idx int) (*Plugin, tea.Cmd) {
	if idx < 0 || idx >= p.tree.Len() {
		return p, nil
	}

	p.treeCursor = idx
	p.ensureTreeCursorVisible()
	p.activePane = PaneTree

	node := p.tree.GetNode(idx)
	if node == nil || node.IsDir {
		return p, nil
	}

	return p, LoadPreview(p.ctx.WorkDir, node.Path, p.ctx.Epoch)
}

// enterInlineEditModeAtCurrentLine starts inline editing at the current preview line.
func (p *Plugin) enterInlineEditModeAtCurrentLine(path string) tea.Cmd {
	lineNo := p.getCurrentPreviewLine()
	return p.enterInlineEditMode(path, lineNo)
}
