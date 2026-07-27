package filebrowser

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/atotto/clipboard"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/app"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/msg"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/tty"
)

// InlineEditStartedMsg is sent when inline edit mode starts successfully.
type InlineEditStartedMsg struct {
	SessionName   string
	FilePath      string
	OriginalMtime time.Time // File mtime before editing (to detect changes)
	Editor        string    // Editor command used (vim, nano, emacs, etc.)
}

// InlineEditExitedMsg is sent when inline edit mode exits.
type InlineEditExitedMsg struct {
	FilePath string
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
		}
	}
}

// handleInlineEditStarted processes the InlineEditStartedMsg and activates the tty model.
func (p *Plugin) handleInlineEditStarted(msg InlineEditStartedMsg) tea.Cmd {
	p.inlineEditMode = true
	p.activePane = PanePreview
	p.inlineEditSession = msg.SessionName
	p.inlineEditFile = msg.FilePath
	p.inlineEditOrigMtime = msg.OriginalMtime
	p.inlineEditEditor = msg.Editor

	// Configure the tty model callbacks
	p.inlineEditor.OnExit = func() tea.Cmd {
		return func() tea.Msg {
			return InlineEditExitedMsg{FilePath: p.inlineEditFile}
		}
	}
	p.inlineEditor.OnAttach = func() tea.Cmd {
		// Attach to full tmux session
		return p.attachToInlineEditSession()
	}

	// Enter interactive mode on the tty model
	width := p.calculateInlineEditorWidth()
	height := p.calculateInlineEditorHeight()
	p.inlineEditor.SetDimensions(width, height)

	enterCmd := p.inlineEditor.Enter(msg.SessionName, "")

	// Show copy/paste hint toast on first entry
	if !p.inlineEditCopyPasteHintShown {
		p.inlineEditCopyPasteHintShown = true
		hintCmd := func() tea.Msg {
			return app.ToastMsg{
				Message:  fmt.Sprintf("Copy/paste: %s / %s", p.getInlineEditCopyKey(), p.getInlineEditPasteKey()),
				Duration: 3 * time.Second,
			}
		}
		return tea.Batch(enterCmd, hintCmd)
	}
	return enterCmd
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
	return func() tea.Msg {
		if p.inlineEditor == nil || p.inlineEditor.State == nil || p.inlineEditor.State.OutputBuf == nil {
			return app.ToastMsg{Message: "No output to copy", Duration: 2 * time.Second}
		}
		lines := p.inlineEditor.State.OutputBuf.Lines()
		if len(lines) == 0 {
			return app.ToastMsg{Message: "No output to copy", Duration: 2 * time.Second}
		}
		stripped := make([]string, 0, len(lines))
		for _, line := range lines {
			stripped = append(stripped, ansi.Strip(line))
		}
		text := strings.Join(stripped, "\n")
		if err := clipboard.WriteAll(text); err != nil {
			return app.ToastMsg{Message: "Copy failed: " + err.Error(), Duration: 2 * time.Second, IsError: true}
		}
		return app.ToastMsg{Message: fmt.Sprintf("Copied %d line(s)", len(stripped)), Duration: 2 * time.Second}
	}
}

// reattachInlineEditSession re-attaches to an existing tmux session after tab switch.
// Called when returning to a tab that was previously in edit mode.
func (p *Plugin) reattachInlineEditSession() tea.Cmd {
	if p.inlineEditSession == "" {
		return nil
	}
	p.activePane = PanePreview

	// Configure the tty model callbacks (same as handleInlineEditStarted)
	p.inlineEditor.OnExit = func() tea.Cmd {
		return func() tea.Msg {
			return InlineEditExitedMsg{FilePath: p.inlineEditFile}
		}
	}
	p.inlineEditor.OnAttach = func() tea.Cmd {
		return p.attachToInlineEditSession()
	}

	// Enter interactive mode with the existing session
	width := p.calculateInlineEditorWidth()
	height := p.calculateInlineEditorHeight()
	p.inlineEditor.SetDimensions(width, height)

	return p.inlineEditor.Enter(p.inlineEditSession, "")
}

// exitInlineEditMode cleans up inline edit state and kills the tmux session.
func (p *Plugin) exitInlineEditMode() {
	tty.EditorSession{Name: p.inlineEditSession, Editor: p.inlineEditEditor}.Kill()
	p.inlineEditMode = false
	p.inlineEditSession = ""
	p.inlineEditFile = ""
	p.inlineEditOrigMtime = time.Time{}
	p.inlineEditEditor = ""
	p.inlineEditorDragging = false
	p.inlineEditor.Exit()
}

// isInlineEditSessionAlive checks if the tmux session for inline editing still exists.
// Returns false if the session has ended (vim quit).
func (p *Plugin) isInlineEditSessionAlive() bool {
	if p.inlineEditSession == "" {
		return false
	}
	// Check if the tmux session exists using has-session
	return (tty.EditorSession{Name: p.inlineEditSession, Editor: p.inlineEditEditor}).IsAlive()
}

// attachToInlineEditSession attaches to the inline edit tmux session in full-screen mode.
func (p *Plugin) attachToInlineEditSession() tea.Cmd {
	if p.inlineEditSession == "" {
		return nil
	}

	sessionName := p.inlineEditSession
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
	if p.showExitConfirmation {
		return p.renderExitConfirmation(visibleHeight)
	}

	var sb strings.Builder

	// Tab line (to match normal preview rendering)
	if len(p.tabs) > 1 {
		tabLine := p.renderPreviewTabs(p.previewWidth - 4)
		sb.WriteString(tabLine)
		sb.WriteString("\n")
	}

	// Header with file being edited and exit hint
	fileName := filepath.Base(p.inlineEditFile)
	header := fmt.Sprintf("Editing: %s", fileName)
	sb.WriteString(styles.Title.Render(header))
	sb.WriteString("  ")
	sb.WriteString(styles.Muted.Render("(Ctrl+\\ or ESC ESC to exit)"))
	sb.WriteString("\n")

	// Calculate content height (account for tab line and header)
	contentHeight := visibleHeight
	if len(p.tabs) > 1 {
		contentHeight-- // tab line
	}
	contentHeight -= 2 // header + empty line

	// Render terminal content from tty model
	if p.inlineEditor != nil {
		content := p.inlineEditor.View()
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
	options := []string{"Save & Exit", "Exit without saving", "Cancel"}

	var sb strings.Builder

	// Tab line (keep consistent with editor view)
	if len(p.tabs) > 1 {
		tabLine := p.renderPreviewTabs(p.previewWidth - 4)
		sb.WriteString(tabLine)
		sb.WriteString("\n")
	}

	sb.WriteString(styles.Title.Render("Exit editor?"))
	sb.WriteString("\n\n")

	for i, opt := range options {
		if i == p.exitConfirmSelection {
			sb.WriteString(styles.ListItemSelected.Render("> " + opt))
		} else {
			sb.WriteString("  " + opt)
		}
		sb.WriteString("\n")
	}

	sb.WriteString("\n")
	sb.WriteString(styles.Muted.Render("[j/k to select, Enter to confirm, Esc to cancel]"))

	return sb.String()
}

// handleExitConfirmationChoice processes the user's selection in the exit confirmation dialog.
func (p *Plugin) handleExitConfirmationChoice() (*Plugin, tea.Cmd) {
	p.showExitConfirmation = false

	switch p.exitConfirmSelection {
	case 0: // Save & Exit
		target := p.inlineEditSession
		editor := p.inlineEditEditor

		// Try to send editor-specific save-and-quit commands
		// If unknown editor, we still proceed but skip the save attempt
		tty.EditorSession{Name: target, Editor: editor}.SaveAndQuit()

		// Give editor a moment to process, then kill session
		// (Session may already be dead from quit command, kill-session will fail silently)
		p.exitInlineEditMode()
		return p.processPendingClickAction()

	case 1: // Exit without saving
		// Kill session immediately, then process pending action
		p.exitInlineEditMode()
		return p.processPendingClickAction()

	case 2: // Cancel
		p.pendingClickRegion = ""
		p.pendingClickData = nil
		return p, nil
	}

	return p, nil
}

// processPendingClickAction handles the click that triggered exit confirmation.
func (p *Plugin) processPendingClickAction() (*Plugin, tea.Cmd) {
	region := p.pendingClickRegion
	data := p.pendingClickData

	// Clear pending state
	p.pendingClickRegion = ""
	p.pendingClickData = nil

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

// calculateInlineEditorMouseCoords converts screen coordinates to editor-relative coordinates.
// Returns (col, row, ok) where col and row are 1-indexed for SGR mouse protocol.
// Returns ok=false if the coordinates are outside the editor content area.
func (p *Plugin) calculateInlineEditorMouseCoords(x, y int) (col, row int, ok bool) {
	contentX, contentY, ok := p.inlineEditorOrigin()
	if !ok {
		return 0, 0, false
	}
	relX := x - contentX
	relY := y - contentY

	if relX < 0 || relY < 0 {
		return 0, 0, false
	}

	// Validate bounds against editor dimensions
	editorWidth := p.calculateInlineEditorWidth()
	editorHeight := p.calculateInlineEditorHeight()

	if relX >= editorWidth || relY >= editorHeight {
		return 0, 0, false
	}

	// SGR mouse protocol uses 1-indexed coordinates
	return relX + 1, relY + 1, true
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
	cursor := p.inlineEditor.Cursor()
	if cursor == nil {
		return nil
	}
	x, y, ok := p.inlineEditorOrigin()
	if !ok {
		return nil
	}
	copy := *cursor
	copy.X += x
	copy.Y += y
	if copy.X < 0 || copy.X >= p.width || copy.Y < 0 || copy.Y >= p.height {
		return nil
	}
	return &copy
}

func (p *Plugin) inlineEditorNativeActive() bool {
	return p.focused && p.activePane == PanePreview && p.inlineEditMode &&
		p.inlineEditor != nil && p.inlineEditor.IsActive() && !p.showExitConfirmation &&
		!p.projectSearchMode && !p.quickOpenMode && !p.infoMode && !p.blameMode
}

// PreferredMouseMode reduces idle hover traffic only while the inline terminal
// owns input. Modal and ordinary file-browser views retain all-motion hover.
func (p *Plugin) PreferredMouseMode() tea.MouseMode {
	if p.inlineEditorNativeActive() {
		return p.inlineEditor.PreferredMouseMode()
	}
	return tea.MouseModeAllMotion
}

// forwardMousePressToInlineEditor sends a mouse press event to the inline editor.
// col and row are 1-indexed coordinates relative to the editor content area.
func (p *Plugin) forwardMousePressToInlineEditor(col, row int) tea.Cmd {
	if p.inlineEditor == nil || !p.inlineEditor.IsActive() {
		return nil
	}
	if p.inlineEditSession == "" {
		return nil
	}

	scope := p.inlineEditor.Scope()
	return (tty.EditorSession{Name: p.inlineEditSession, Editor: p.inlineEditEditor}).MouseCmd(scope, 0, col, row, false)
}

// forwardMouseDragToInlineEditor sends a mouse drag/motion event to the inline editor.
// col and row are 1-indexed coordinates relative to the editor content area.
func (p *Plugin) forwardMouseDragToInlineEditor(col, row int) tea.Cmd {
	if p.inlineEditor == nil || !p.inlineEditor.IsActive() {
		return nil
	}
	if p.inlineEditSession == "" {
		return nil
	}

	scope := p.inlineEditor.Scope()
	return (tty.EditorSession{Name: p.inlineEditSession, Editor: p.inlineEditEditor}).MouseCmd(scope, 32, col, row, false)
}

// forwardMouseReleaseToInlineEditor sends a mouse release event to the inline editor.
// col and row are 1-indexed coordinates relative to the editor content area.
func (p *Plugin) forwardMouseReleaseToInlineEditor(col, row int) tea.Cmd {
	if p.inlineEditor == nil || !p.inlineEditor.IsActive() {
		return nil
	}
	if p.inlineEditSession == "" {
		return nil
	}

	scope := p.inlineEditor.Scope()
	return (tty.EditorSession{Name: p.inlineEditSession, Editor: p.inlineEditEditor}).MouseCmd(scope, 0, col, row, true)
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
