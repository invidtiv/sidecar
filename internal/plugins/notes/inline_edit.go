package notes

import (
	"fmt"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
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
	Activation  uint64
	Epoch       uint64
}

// InlineEditExitedMsg is sent when inline edit mode exits.
type InlineEditExitedMsg struct {
	NoteID     string
	NotePath   string
	Activation uint64
	Epoch      uint64
}

// enterInlineEditMode starts inline editing for the selected note.
// Creates a tmux session running the user's editor and delegates to tty.Model.
func (p *Plugin) enterInlineEditMode(noteID string) tea.Cmd {
	if p.store == nil {
		return nil
	}
	if p.inlineEditMode && p.inlineEditNoteID == noteID && p.inlineEditor != nil && p.inlineEditor.IsActive() {
		return nil
	}

	// Get note path (creates temp file with content)
	notePath := p.store.NotePath(noteID)
	if notePath == "" {
		return inlineEditUnavailableToast("note file unavailable")
	}

	editor := tty.ResolveEditor()

	// Size the session to the viewport it will be rendered into rather than the
	// whole plugin rect, so the editor's bottom rows stay visible (td-a87445).
	editorWidth := p.calculateInlineEditorWidth()
	editorHeight := p.calculateInlineEditorHeight()
	p.inlineEditActivation++
	activation := p.inlineEditActivation
	var epoch uint64
	if p.ctx != nil {
		epoch = p.ctx.Epoch
	}

	return func() tea.Msg {
		if !tty.EditorAvailable() {
			removeNoteExport(notePath)
			return inlineEditUnavailableToast("tmux not found")()
		}

		session, err := tty.StartEditorSession(tty.EditorSessionOptions{
			NamePrefix:  "sidecar-note-edit-",
			Editor:      editor,
			Path:        notePath,
			Width:       editorWidth,
			Height:      editorHeight,
			CursorAtEnd: true,
		})
		if err != nil {
			removeNoteExport(notePath)
			return inlineEditUnavailableToast(err.Error())()
		}

		return InlineEditStartedMsg{
			SessionName: session.Name,
			NoteID:      noteID,
			NotePath:    notePath,
			Editor:      session.Editor,
			Activation:  activation,
			Epoch:       epoch,
		}
	}
}

func inlineEditUnavailableToast(reason string) tea.Cmd {
	return func() tea.Msg {
		return msg.ToastMsg{
			Message:  fmt.Sprintf("Failed to start editor: %s", reason),
			Duration: 3 * time.Second,
			IsError:  true,
		}
	}
}

// editSelectedNote starts the right-pane tty editor for the selected Active note.
func (p *Plugin) editSelectedNote() tea.Cmd {
	if p.viewFilter != FilterActive {
		return nil
	}
	note := p.getSelectedNote()
	if note == nil {
		return nil
	}
	// The session below reads the note from the store; an unsaved buffer has to
	// land first or it is overwritten by what vim reads and writes back.
	if cmd := p.persistDirtyEditor(); cmd != nil {
		return cmd
	}
	if cmd := p.loadNoteIntoEditor(); cmd != nil {
		return cmd
	}
	return p.enterInlineEditMode(note.ID)
}

// handleInlineEditStarted processes the InlineEditStartedMsg and activates the tty model.
func (p *Plugin) handleInlineEditStarted(msg InlineEditStartedMsg) tea.Cmd {
	if !p.ownsInlineEditMessage(msg.Activation, msg.Epoch) {
		return p.cleanupStaleInlineEditStart(msg)
	}
	p.inlineEditMode = true
	p.activePane = PaneEditor
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
	activation, epoch := msg.Activation, msg.Epoch
	noteID, notePath := msg.NoteID, msg.NotePath
	exit := func() tea.Cmd {
		return func() tea.Msg {
			return InlineEditExitedMsg{
				NoteID:     noteID,
				NotePath:   notePath,
				Activation: activation,
				Epoch:      epoch,
			}
		}
	}
	p.inlineEditor.OnExit = exit
	p.inlineEditor.OnSessionEnded = exit
	p.clearInlineEditorAttachKey()
	p.inlineEditor.OnAttach = nil

	// Enter interactive mode on the tty model
	width := p.calculateInlineEditorWidth()
	height := p.calculateInlineEditorHeight()
	p.inlineEditor.Resize(width, height)

	// Return batch: enter tty mode + start auto-save timer
	return tea.Batch(
		p.inlineEditor.Open(tty.Target{Session: msg.SessionName}),
		p.scheduleInlineAutoSave(),
	)
}

func (p *Plugin) cleanupStaleInlineEditStart(msg InlineEditStartedMsg) tea.Cmd {
	if p.inlineEditor != nil && p.inlineEditor.IsActive() {
		if p.inlineEditSession == msg.SessionName || p.inlineEditor.GetTarget() == msg.SessionName {
			return nil
		}
	}
	if msg.NotePath != p.inlineEditPath {
		removeNoteExport(msg.NotePath)
	}
	return (tty.EditorSession{Name: msg.SessionName, Editor: msg.Editor}).KillCmd()
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
	p.inlineEditActivation++
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
		p.inlineEditor.Close()
	}

	// Reset auto-save state
	p.inlineAutoSaveGen++
	p.inlineLastSavedContent = ""
}

func (p *Plugin) ownsInlineEditMessage(activation, epoch uint64) bool {
	return p.ctx != nil && activation == p.inlineEditActivation && epoch == p.ctx.Epoch
}

// handleInlineEditExited processes the InlineEditExitedMsg and saves note content.
func (p *Plugin) handleInlineEditExited(exitMsg InlineEditExitedMsg) tea.Cmd {
	if !p.ownsInlineEditMessage(exitMsg.Activation, exitMsg.Epoch) {
		if exitMsg.NotePath != p.inlineEditPath {
			removeNoteExport(exitMsg.NotePath)
		}
		return nil
	}
	noteID := exitMsg.NoteID
	notePath := exitMsg.NotePath

	// Clean up inline edit state
	p.exitInlineEditMode()

	if noteID == "" || notePath == "" || p.store == nil {
		removeNoteExport(notePath)
		return p.loadNotes()
	}

	// Inline editor writes bypass textarea state; sync buffers on the next reload.
	p.pendingEditorSyncID = noteID

	epoch := p.ctx.Epoch
	activation := p.inlineEditActivation
	store := p.store

	return func() tea.Msg {
		// Read back the edited content from temp file
		content, err := os.ReadFile(notePath)
		if err != nil {
			removeNoteExport(notePath)
			return NotesLoadedMsg{Err: err, Epoch: epoch}
		}

		// Clean up temp file
		removeNoteExport(notePath)

		// Update note content in database
		if err := store.UpdateContent(noteID, string(content)); err != nil {
			return NoteSavedMsg{Note: nil, Err: err, Epoch: epoch, EditorActivation: activation}
		}

		return NoteContentSavedMsg{ID: noteID, Err: nil, Epoch: epoch, EditorActivation: activation}
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
// Subtract one row for the "Editing:" header only — an extra empty-line
// deduction left a blank row under vim (td-4a5f77).
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

	// SGR mouse protocol uses 1-indexed coordinates, and the editor renders the
	// pane at its observed size — clipped and scrolled when it is larger than
	// this viewport — so map through the fit rather than assuming they line up
	// (td-73fa86).
	// A miss from an active editor means the click landed outside the pane —
	// letterbox padding around a pane smaller than the viewport — so it must not
	// fall back to the raw mapping and forward a cell the pane does not have.
	if p.inlineEditor != nil && p.inlineEditor.IsActive() {
		return p.inlineEditor.PaneCoords(relX+1, relY+1)
	}
	return relX + 1, relY + 1, true
}

func (p *Plugin) inlineEditorOrigin() (x, y int, ok bool) {
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
	return p.focused && p.activePane == PaneEditor && p.inlineEditMode &&
		p.inlineEditor != nil && p.inlineEditor.IsActive() && !p.showExitConfirmation &&
		!p.showInfoModal && !p.showDeleteModal && !p.showTaskModal
}

// PreferredMouseMode reduces idle hover traffic only while the inline terminal
// owns input. Modal and ordinary notes views retain all-motion hover.
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
	// The tmux pane can be larger than this viewport when another sidecar
	// instance drives the same session; say what is hidden (td-73fa86).
	if p.inlineEditor != nil {
		if indicator := p.inlineEditor.SizeIndicator(); indicator != "" {
			sb.WriteString("  ")
			sb.WriteString(styles.Muted.Render(indicator))
		}
	}
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

	// Enforce total height constraint per AGENTS.md
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
		exportPath := p.inlineEditPath
		p.exitInlineEditMode()
		removeNoteExport(exportPath)
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
			return p, p.loadNoteIntoEditor()
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
	activation := p.inlineEditActivation
	store := p.store

	// Create save command
	saveCmd := func() tea.Msg {
		if noteID == "" || notePath == "" || store == nil {
			removeNoteExport(notePath)
			return nil
		}

		// Read back the edited content from temp file
		content, err := os.ReadFile(notePath)
		if err != nil {
			removeNoteExport(notePath)
			return NotesLoadedMsg{Err: err, Epoch: epoch}
		}

		// Clean up temp file
		removeNoteExport(notePath)

		// Update note content in database
		if err := store.UpdateContent(noteID, string(content)); err != nil {
			return NoteSavedMsg{Note: nil, Err: err, Epoch: epoch, EditorActivation: activation}
		}

		return NoteContentSavedMsg{ID: noteID, Err: nil, Epoch: epoch, EditorActivation: activation}
	}

	// Process the pending click
	p2, _ := p.processPendingClickAction()

	return p2, saveCmd
}

// clearInlineEditorAttachKey makes ctrl+] inert in the notes vim pane. Notes
// has no full-screen tmux experience: the embedded pane is the whole editor,
// so there is nothing to hand a suspended Sidecar off to.
func (p *Plugin) clearInlineEditorAttachKey() {
	if p.inlineEditor == nil {
		return
	}
	p.inlineEditor.Config.AttachKey = ""
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

// handleTtyMessages processes tty-related messages.
func (p *Plugin) handleTtyMessages(msg tea.Msg) (bool, tea.Cmd) {
	if !p.inlineEditMode || p.inlineEditor == nil {
		return false, nil
	}

	switch msg := msg.(type) {
	case tea.MouseMsg:
		// Click-away (list / other note) is handled in handleMouse. Forwarding
		// here swallowed the press and left :wq / click-away hanging (td-bb475e).
		return false, nil
	case tty.EscapeTimerMsg, tty.CaptureResultMsg, tty.PollTickMsg, tty.PaneResizedMsg, tty.SessionDeadMsg, tty.PasteResultMsg:
		cmd := p.inlineEditor.Update(msg)
		return true, cmd
	case InlineEditStartedMsg:
		return true, p.handleInlineEditStarted(msg)
	case InlineEditExitedMsg:
		return true, p.handleInlineEditExited(msg)
	}
	// Model/control listener messages are intentionally private to tty.Model.
	// Let it identify and consume them instead of duplicating transport cases in
	// this plugin. A nil command means the message was either synchronous or not
	// terminal-owned and should continue through normal Notes routing.
	if cmd := p.inlineEditor.Update(msg); cmd != nil {
		return true, cmd
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
	activation := p.inlineEditActivation
	generation := p.inlineAutoSaveGen
	store := p.store
	lastSavedContent := p.inlineLastSavedContent

	return func() tea.Msg {
		// Read current content from temp file
		content, err := os.ReadFile(notePath)
		if err != nil {
			// File not readable - schedule next tick without saving
			return InlineAutoSaveResultMsg{Err: err, Epoch: epoch, Activation: activation, Generation: generation}
		}

		contentStr := string(content)

		// Check if content changed since last save
		if contentStr == lastSavedContent {
			// No changes - schedule next tick
			return InlineAutoSaveResultMsg{Epoch: epoch, Activation: activation, Generation: generation}
		}

		// Content changed - save to database
		if err := store.UpdateContent(noteID, contentStr); err != nil {
			return InlineAutoSaveResultMsg{Err: err, Epoch: epoch, Activation: activation, Generation: generation}
		}

		return InlineAutoSaveResultMsg{
			Epoch: epoch, Activation: activation, Generation: generation,
			Content: contentStr, Saved: true,
		}
	}
}

// saveNoteAfterInlineExit saves note content after inline edit session exits.
// Used when detecting session death proactively (e.g., vim :wq exit).
func (p *Plugin) saveNoteAfterInlineExit(noteID, notePath string) tea.Cmd {
	if noteID == "" || notePath == "" || p.store == nil {
		removeNoteExport(notePath)
		return p.loadNotes()
	}

	// Inline editor writes bypass textarea state; sync buffers on the next reload.
	p.pendingEditorSyncID = noteID

	epoch := p.ctx.Epoch
	activation := p.inlineEditActivation
	store := p.store

	return func() tea.Msg {
		// Read back the edited content from temp file
		content, err := os.ReadFile(notePath)
		if err != nil {
			removeNoteExport(notePath)
			return NotesLoadedMsg{Err: err, Epoch: epoch}
		}

		// Clean up temp file
		removeNoteExport(notePath)

		// Update note content in database
		if err := store.UpdateContent(noteID, string(content)); err != nil {
			return NoteSavedMsg{Note: nil, Err: err, Epoch: epoch, EditorActivation: activation}
		}

		return NoteContentSavedMsg{ID: noteID, Err: nil, Epoch: epoch, EditorActivation: activation}
	}
}

// saveAndExitInlineEditMode saves current content and exits inline edit mode.
// Used for click-away auto-save behavior.
func (p *Plugin) saveAndExitInlineEditMode() tea.Cmd {
	noteID := p.inlineEditNoteID
	notePath := p.inlineEditPath
	epoch := p.ctx.Epoch
	store := p.store

	// Exit inline edit mode (kills tmux session)
	p.exitInlineEditMode()
	activation := p.inlineEditActivation

	if noteID == "" || notePath == "" || store == nil {
		removeNoteExport(notePath)
		return nil
	}

	// Inline editor writes bypass textarea state; sync buffers on the next reload.
	p.pendingEditorSyncID = noteID

	return func() tea.Msg {
		// Read content from temp file
		content, err := os.ReadFile(notePath)
		if err != nil {
			removeNoteExport(notePath)
			return NoteSavedMsg{Err: err, Epoch: epoch, EditorActivation: activation}
		}

		// Clean up temp file
		removeNoteExport(notePath)

		// Save to database
		if err := store.UpdateContent(noteID, string(content)); err != nil {
			return NoteSavedMsg{Err: err, Epoch: epoch, EditorActivation: activation}
		}

		return NoteContentSavedMsg{ID: noteID, Err: nil, Epoch: epoch, EditorActivation: activation}
	}
}
