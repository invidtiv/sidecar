package notes

import (
	"fmt"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/msg"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/tty"
)

// InlineEditStartedMsg is sent when inline edit mode starts successfully.
type InlineEditStartedMsg struct {
	SessionName string
	NoteID      string
	NotePath    string
	Editor      string
}

// InlineEditExitedMsg is sent when inline edit mode exits.
type InlineEditExitedMsg struct {
	NoteID   string
	NotePath string
}

// enterInlineEditMode starts inline editing for the selected note.
// Creates a tmux session running the user's editor and delegates to tty.Model.
func (p *Plugin) enterInlineEditMode(noteID string) tea.Cmd {
	// Check feature flag
	if !features.IsEnabled(features.TmuxInlineEdit.Name) {
		return p.openInExternalEditor()
	}

	if p.store == nil {
		return nil
	}

	// Get note path (creates temp file with content)
	notePath := p.store.NotePath(noteID)
	if notePath == "" {
		return nil
	}

	editor := tty.ResolveEditor()

	return func() tea.Msg {
		if !tty.EditorAvailable() {
			// Fall back to external editor
			return nil
		}

		session, err := tty.StartEditorSession(tty.EditorSessionOptions{
			NamePrefix:  "sidecar-note-edit-",
			Editor:      editor,
			Path:        notePath,
			Width:       p.width,
			Height:      p.height,
			CursorAtEnd: true,
		})
		if err != nil {
			return msg.ToastMsg{
				Message:  fmt.Sprintf("Failed to start editor: %v", err),
				Duration: 3 * time.Second,
				IsError:  true,
			}
		}

		return InlineEditStartedMsg{
			SessionName: session.Name,
			NoteID:      noteID,
			NotePath:    notePath,
			Editor:      session.Editor,
		}
	}
}

// handleInlineEditStarted processes the InlineEditStartedMsg and activates the tty model.
func (p *Plugin) handleInlineEditStarted(msg InlineEditStartedMsg) tea.Cmd {
	p.inlineEditMode = true
	p.inlineEditSession = msg.SessionName
	p.inlineEditNoteID = msg.NoteID
	p.inlineEditPath = msg.NotePath
	p.inlineEditEditor = msg.Editor

	// Initialize auto-save state - read initial content for change detection
	if content, err := os.ReadFile(msg.NotePath); err == nil {
		p.inlineLastSavedContent = string(content)
	} else {
		p.inlineLastSavedContent = ""
	}

	// Configure the tty model callbacks
	p.inlineEditor.OnExit = func() tea.Cmd {
		return func() tea.Msg {
			return InlineEditExitedMsg{
				NoteID:   p.inlineEditNoteID,
				NotePath: p.inlineEditPath,
			}
		}
	}

	// Enter interactive mode on the tty model
	width := p.calculateInlineEditorWidth()
	height := p.calculateInlineEditorHeight()
	p.inlineEditor.SetDimensions(width, height)

	// Return batch: enter tty mode + start auto-save timer
	return tea.Batch(
		p.inlineEditor.Enter(msg.SessionName, ""),
		p.scheduleInlineAutoSave(),
	)
}

// exitInlineEditMode cleans up inline edit state and kills the tmux session.
func (p *Plugin) exitInlineEditMode() {
	tty.EditorSession{Name: p.inlineEditSession, Editor: p.inlineEditEditor}.Kill()
	p.resetInlineEditState()
}

// resetInlineEditState invalidates all session-scoped state without touching
// the note store. Stop uses it before closing a project store so an old timer
// can never operate on the next project's store.
func (p *Plugin) resetInlineEditState() {
	p.inlineEditMode = false
	p.inlineEditSession = ""
	p.inlineEditNoteID = ""
	p.inlineEditPath = ""
	p.inlineEditEditor = ""
	p.inlineEditorDragging = false
	p.showExitConfirmation = false
	p.pendingClickRegion = ""
	p.pendingClickData = nil
	if p.inlineEditor != nil {
		p.inlineEditor.Exit()
	}

	// Reset auto-save state
	p.inlineAutoSaveGen++
	p.inlineLastSavedContent = ""
}

// handleInlineEditExited processes the InlineEditExitedMsg and saves note content.
func (p *Plugin) handleInlineEditExited(exitMsg InlineEditExitedMsg) tea.Cmd {
	noteID := exitMsg.NoteID
	notePath := exitMsg.NotePath

	// Clean up inline edit state
	p.exitInlineEditMode()

	if noteID == "" || notePath == "" || p.store == nil {
		return p.loadNotes()
	}

	// Inline editor writes bypass textarea state; sync buffers on the next reload.
	p.pendingEditorSyncID = noteID

	epoch := p.ctx.Epoch

	return func() tea.Msg {
		// Read back the edited content from temp file
		content, err := os.ReadFile(notePath)
		if err != nil {
			return NotesLoadedMsg{Err: err, Epoch: epoch}
		}

		// Clean up temp file
		_ = os.Remove(notePath)

		// Update note content in database
		if err := p.store.UpdateContent(noteID, string(content)); err != nil {
			return NoteSavedMsg{Note: nil, Err: err, Epoch: epoch}
		}

		return NoteContentSavedMsg{ID: noteID, Err: nil, Epoch: epoch}
	}
}

// calculateInlineEditorWidth returns the content width for the inline editor.
func (p *Plugin) calculateInlineEditorWidth() int {
	p.calculatePaneWidths()
	// Editor pane width minus borders and padding
	editorWidth := p.width - p.listWidth - dividerWidth
	return editorWidth - 4 // borders + padding
}

// calculateInlineEditorHeight returns the content height for the inline editor.
func (p *Plugin) calculateInlineEditorHeight() int {
	paneHeight := p.height
	if paneHeight < 4 {
		paneHeight = 4
	}
	innerHeight := paneHeight - 2 // pane borders

	// Subtract header line
	contentHeight := innerHeight - 1 // header line only

	if contentHeight < 5 {
		contentHeight = 5
	}
	return contentHeight
}

// calculateInlineEditorMouseCoords converts screen coordinates to editor-relative coordinates.
// Returns (col, row, ok) where col and row are 1-indexed for SGR mouse protocol.
// Returns ok=false if the coordinates are outside the editor content area.
func (p *Plugin) calculateInlineEditorMouseCoords(x, y int) (col, row int, ok bool) {
	if p.width <= 0 || p.height <= 0 {
		return 0, 0, false
	}

	// Calculate editor pane X offset
	p.calculatePaneWidths()
	editorX := p.listWidth + dividerWidth

	// Content X offset: editor pane start + border(1) + padding(1)
	contentX := editorX + 2

	// Calculate Y offset based on pane structure
	contentY := 0

	// Add pane border (top)
	contentY++

	// Add header line ("Editing: filename...")
	contentY++

	// Calculate relative coordinates
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

// renderInlineEditorContent renders the inline editor within the editor pane area.
func (p *Plugin) renderInlineEditorContent(visibleHeight int) string {
	// If showing exit confirmation, render that instead
	if p.showExitConfirmation {
		return p.renderExitConfirmation(visibleHeight)
	}

	var sb strings.Builder

	// Header with note title being edited and exit hint
	note := p.getSelectedNote()
	noteTitle := "Note"
	if note != nil {
		noteTitle = truncateTitle(note.Title, 30)
		if noteTitle == "" {
			noteTitle = "Untitled"
		}
	}
	header := fmt.Sprintf("Editing: %s", noteTitle)
	sb.WriteString(styles.Title.Render(header))
	sb.WriteString("  ")
	sb.WriteString(styles.Muted.Render("(Ctrl+\\ or ESC ESC to exit)"))
	sb.WriteString("\n")

	// Calculate content height (account for header)
	contentHeight := visibleHeight - 1 // header line only

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

	// Enforce total height constraint per CLAUDE.md
	return lipgloss.NewStyle().Height(visibleHeight).Render(sb.String())
}

// renderExitConfirmation renders the exit confirmation dialog overlay.
func (p *Plugin) renderExitConfirmation(visibleHeight int) string {
	options := []string{"Save & Exit", "Exit without saving", "Cancel"}

	var sb strings.Builder

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
		tty.EditorSession{Name: target, Editor: editor}.SaveAndQuit()

		// Exit inline edit mode and save note content
		noteID := p.inlineEditNoteID
		notePath := p.inlineEditPath
		p.exitInlineEditMode()

		// Process pending click and save note
		return p.processPendingClickActionWithSave(noteID, notePath)

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
	case regionNoteItem:
		// User clicked a note item - select it
		if idx, ok := data.(int); ok {
			p.cursor = idx
			p.activePane = PaneList
			p.loadNoteIntoEditor()
		}
		return p, nil
	case regionListPane:
		// User clicked list pane background - focus list
		p.activePane = PaneList
		p.selection.Clear()
		return p, nil
	}

	return p, nil
}

// processPendingClickActionWithSave handles the click and saves note content.
func (p *Plugin) processPendingClickActionWithSave(noteID, notePath string) (*Plugin, tea.Cmd) {
	epoch := p.ctx.Epoch

	// Create save command
	saveCmd := func() tea.Msg {
		if noteID == "" || notePath == "" || p.store == nil {
			return nil
		}

		// Read back the edited content from temp file
		content, err := os.ReadFile(notePath)
		if err != nil {
			return NotesLoadedMsg{Err: err, Epoch: epoch}
		}

		// Clean up temp file
		_ = os.Remove(notePath)

		// Update note content in database
		if err := p.store.UpdateContent(noteID, string(content)); err != nil {
			return NoteSavedMsg{Note: nil, Err: err, Epoch: epoch}
		}

		return NoteContentSavedMsg{ID: noteID, Err: nil, Epoch: epoch}
	}

	// Process the pending click
	p2, _ := p.processPendingClickAction()

	return p2, saveCmd
}

// isInlineEditSupported checks if inline editing can be used for notes.
func (p *Plugin) isInlineEditSupported() bool {
	// Check feature flag
	if !features.IsEnabled(features.TmuxInlineEdit.Name) {
		return false
	}

	// Check if tmux is available
	if !tty.EditorAvailable() {
		return false
	}

	return true
}

// isInlineEditSessionAlive checks if the tmux session for inline editing still exists.
func (p *Plugin) isInlineEditSessionAlive() bool {
	if p.inlineEditSession == "" {
		return false
	}
	return (tty.EditorSession{Name: p.inlineEditSession, Editor: p.inlineEditEditor}).IsAlive()
}

// handleInlineEditorKey processes keyboard input when inline editor is active.
func (p *Plugin) handleInlineEditorKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	if !p.inlineEditMode || p.inlineEditor == nil {
		return false, nil
	}

	// Delegate to tty model
	cmd := p.inlineEditor.Update(msg)
	return true, cmd
}

// handleInlineEditorMouse processes mouse input when inline editor is active.
func (p *Plugin) handleInlineEditorMouse(msg tea.MouseMsg) (bool, tea.Cmd) {
	if !p.inlineEditMode || p.inlineEditor == nil {
		return false, nil
	}

	// Delegate to tty model
	cmd := p.inlineEditor.Update(msg)
	return true, cmd
}

// handleTtyMessages processes tty-related messages.
func (p *Plugin) handleTtyMessages(msg tea.Msg) (bool, tea.Cmd) {
	if !p.inlineEditMode || p.inlineEditor == nil {
		return false, nil
	}

	switch msg := msg.(type) {
	case tty.EscapeTimerMsg, tty.CaptureResultMsg, tty.PollTickMsg, tty.PaneResizedMsg, tty.SessionDeadMsg, tty.PasteResultMsg:
		cmd := p.inlineEditor.Update(msg)
		return true, cmd
	case InlineEditStartedMsg:
		return true, p.handleInlineEditStarted(msg)
	case InlineEditExitedMsg:
		return true, p.handleInlineEditExited(msg)
	}

	return false, nil
}

// Inline auto-save interval (2 seconds)
const inlineAutoSaveInterval = 2 * time.Second

// scheduleInlineAutoSave schedules the next auto-save tick.
func (p *Plugin) scheduleInlineAutoSave() tea.Cmd {
	if !p.inlineEditMode {
		return nil
	}
	p.inlineAutoSaveGen++
	gen := p.inlineAutoSaveGen
	return tea.Tick(inlineAutoSaveInterval, func(t time.Time) tea.Msg {
		return InlineAutoSaveTickMsg{Generation: gen}
	})
}

// performInlineAutoSave reads the temp file and saves if content changed.
func (p *Plugin) performInlineAutoSave() tea.Cmd {
	if !p.inlineEditMode || p.inlineEditPath == "" || p.store == nil {
		return p.scheduleInlineAutoSave()
	}

	noteID := p.inlineEditNoteID
	notePath := p.inlineEditPath
	epoch := p.ctx.Epoch

	return func() tea.Msg {
		// Read current content from temp file
		content, err := os.ReadFile(notePath)
		if err != nil {
			// File not readable - schedule next tick without saving
			return InlineAutoSaveResultMsg{Err: err, Epoch: epoch}
		}

		contentStr := string(content)

		// Check if content changed since last save
		if contentStr == p.inlineLastSavedContent {
			// No changes - schedule next tick
			return InlineAutoSaveResultMsg{Err: nil, Epoch: epoch}
		}

		// Content changed - save to database
		if err := p.store.UpdateContent(noteID, contentStr); err != nil {
			return InlineAutoSaveResultMsg{Err: err, Epoch: epoch}
		}

		// Update last saved content tracker
		p.inlineLastSavedContent = contentStr

		return InlineAutoSaveResultMsg{Err: nil, Epoch: epoch}
	}
}

// saveNoteAfterInlineExit saves note content after inline edit session exits.
// Used when detecting session death proactively (e.g., vim :wq exit).
func (p *Plugin) saveNoteAfterInlineExit(noteID, notePath string) tea.Cmd {
	if noteID == "" || notePath == "" || p.store == nil {
		return p.loadNotes()
	}

	// Inline editor writes bypass textarea state; sync buffers on the next reload.
	p.pendingEditorSyncID = noteID

	epoch := p.ctx.Epoch

	return func() tea.Msg {
		// Read back the edited content from temp file
		content, err := os.ReadFile(notePath)
		if err != nil {
			return NotesLoadedMsg{Err: err, Epoch: epoch}
		}

		// Clean up temp file
		_ = os.Remove(notePath)

		// Update note content in database
		if err := p.store.UpdateContent(noteID, string(content)); err != nil {
			return NoteSavedMsg{Note: nil, Err: err, Epoch: epoch}
		}

		return NoteContentSavedMsg{ID: noteID, Err: nil, Epoch: epoch}
	}
}

// saveAndExitInlineEditMode saves current content and exits inline edit mode.
// Used for click-away auto-save behavior.
func (p *Plugin) saveAndExitInlineEditMode() tea.Cmd {
	noteID := p.inlineEditNoteID
	notePath := p.inlineEditPath
	epoch := p.ctx.Epoch

	// Exit inline edit mode (kills tmux session)
	p.exitInlineEditMode()

	if noteID == "" || notePath == "" || p.store == nil {
		return nil
	}

	// Inline editor writes bypass textarea state; sync buffers on the next reload.
	p.pendingEditorSyncID = noteID

	return func() tea.Msg {
		// Read content from temp file
		content, err := os.ReadFile(notePath)
		if err != nil {
			return InlineAutoSaveResultMsg{Err: err, Epoch: epoch}
		}

		// Clean up temp file
		_ = os.Remove(notePath)

		// Save to database
		if err := p.store.UpdateContent(noteID, string(content)); err != nil {
			return InlineAutoSaveResultMsg{Err: err, Epoch: epoch}
		}

		return NoteContentSavedMsg{ID: noteID, Err: nil, Epoch: epoch}
	}
}
