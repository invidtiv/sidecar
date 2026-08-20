// Package inlineedit owns the shared lifecycle of an inline tmux-PTY editor
// hosted inside a pane: session start bookkeeping, activation-epoch guards,
// stale-start cleanup, mouse/cursor coordinate translation, and the
// save/discard/cancel exit confirmation.
//
// The package is deliberately host-agnostic. It never measures the host's
// layout: the host supplies its editor viewport and screen origin through the
// Host contract, so dimension drift is a host bug rather than a mirrored
// calculation inside here. It also never consults feature flags — gating
// inline edit is a host concern (filebrowser gates on
// features.TmuxInlineEdit; notes deliberately does not).
package inlineedit

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/tty"
)

// Host is the contract a pane implements to host an inline editor.
type Host interface {
	// EditorViewport reports the content cells available to the PTY.
	EditorViewport() (width, height int)
	// EditorOrigin reports the top-left cell of that content area in
	// host-local coordinates. ok is false when the host has no usable rect.
	EditorOrigin() (x, y int, ok bool)
}

// Session holds the state shared by every inline-editor host.
type Session struct {
	Model *tty.Model
	Host  Host

	// Redraw, when set, is batched after operations that change the editor's
	// geometry so a host that caches its render can invalidate it.
	Redraw func() tea.Cmd

	Active     bool   // an inline edit session is current
	Name       string // tmux session name
	Path       string // file being edited
	EditorCmd  string // editor command (vim, nano, ...)
	Activation uint64 // scopes async start/exit messages
	Dragging   bool   // mouse drag in progress inside the editor

	ShowExitConfirm    bool
	ConfirmSelection   int // 0=Save&Exit, 1=Exit without saving, 2=Cancel
	PendingClickRegion string
	PendingClickData   interface{}
}

// New builds a session bound to host with a fresh tty model.
func New(host Host, config *tty.Config) *Session {
	return &Session{Model: tty.New(config), Host: host}
}

// NextActivation bumps and returns the activation counter.
func (s *Session) NextActivation() uint64 {
	if s == nil {
		return 0
	}
	s.Activation++
	return s.Activation
}

// OwnsMessage reports whether an async message belongs to the live session.
func (s *Session) OwnsMessage(activation, epoch, currentEpoch uint64) bool {
	return s != nil && activation == s.Activation && epoch == currentEpoch
}

// Target describes the tmux session backing this editor.
func (s *Session) Target() tty.EditorSession {
	if s == nil {
		return tty.EditorSession{}
	}
	return tty.EditorSession{Name: s.Name, Editor: s.EditorCmd}
}

// IsModelActive reports whether the underlying tty model is attached.
func (s *Session) IsModelActive() bool {
	return s != nil && s.Model != nil && s.Model.IsActive()
}

// Viewport returns the host's editor viewport, or zeros without a host.
func (s *Session) Viewport() (width, height int) {
	if s == nil || s.Host == nil {
		return 0, 0
	}
	return s.Host.EditorViewport()
}

// Begin records a freshly started tmux editor session and opens the tty model
// at the host's viewport.
func (s *Session) Begin(name, editorCmd, path string) tea.Cmd {
	if s == nil || s.Model == nil {
		return nil
	}
	s.Active = true
	s.Name = name
	s.EditorCmd = editorCmd
	s.Path = path
	width, height := s.Viewport()
	s.Model.Resize(width, height)
	return s.Model.Open(tty.Target{Session: name})
}

// Reopen re-attaches the tty model to the session already recorded here, used
// when a host returns to a pane/tab that was left in edit mode.
func (s *Session) Reopen() tea.Cmd {
	if s == nil || s.Model == nil || s.Name == "" {
		return nil
	}
	width, height := s.Viewport()
	s.Model.Resize(width, height)
	return s.Model.Open(tty.Target{Session: s.Name})
}

// ResizeToViewport re-sizes the PTY to the host's current viewport.
func (s *Session) ResizeToViewport() tea.Cmd {
	if s == nil || s.Model == nil {
		return nil
	}
	width, height := s.Viewport()
	cmd := s.Model.Resize(width, height)
	return s.batchRedraw(cmd)
}

func (s *Session) batchRedraw(cmd tea.Cmd) tea.Cmd {
	if s == nil || s.Redraw == nil {
		return cmd
	}
	if redraw := s.Redraw(); redraw != nil {
		return tea.Batch(cmd, redraw)
	}
	return cmd
}

// CleanupStale kills the tmux session named by a stale start message, unless
// that name is the one the live model is currently driving (tmux reuses names).
func (s *Session) CleanupStale(name, editorCmd string) tea.Cmd {
	if s != nil && s.Model != nil && s.Model.IsActive() {
		if s.Name == name || s.Model.GetTarget() == name {
			return nil
		}
	}
	return (tty.EditorSession{Name: name, Editor: editorCmd}).KillCmd()
}

// Reset clears session-scoped state and closes the tty model without killing
// the tmux session. Callers that own the session should use Exit.
func (s *Session) Reset() {
	if s == nil {
		return
	}
	s.Active = false
	s.Name = ""
	s.Path = ""
	s.EditorCmd = ""
	s.Activation++
	s.Dragging = false
	if s.Model != nil {
		s.Model.Close()
	}
}

// Exit kills the tmux session and resets state.
func (s *Session) Exit() {
	if s == nil {
		return
	}
	s.Target().Kill()
	s.Reset()
}

// IsAlive reports whether the tmux session still exists.
func (s *Session) IsAlive() bool {
	if s == nil || s.Name == "" {
		return false
	}
	return s.Target().IsAlive()
}

// SaveAndQuit asks the editor to write and quit.
func (s *Session) SaveAndQuit() {
	if s == nil || s.Name == "" {
		return
	}
	s.Target().SaveAndQuit()
}

// NativeActive reports whether the embedded terminal owns input right now.
// Hosts AND this with their own conditions (focus, pane, modals).
func (s *Session) NativeActive() bool {
	return s != nil && s.Active && s.IsModelActive() && !s.ShowExitConfirm
}

// PreferredMouseMode returns the terminal's preferred mouse mode when native
// is true, and all-motion hover otherwise.
func (s *Session) PreferredMouseMode(native bool) tea.MouseMode {
	if native && s != nil && s.Model != nil {
		return s.Model.PreferredMouseMode()
	}
	return tea.MouseModeAllMotion
}

// MouseCoords converts host-local screen coordinates to 1-indexed editor
// coordinates for the SGR mouse protocol. ok is false outside the content area.
func (s *Session) MouseCoords(x, y int) (col, row int, ok bool) {
	if s == nil || s.Host == nil {
		return 0, 0, false
	}
	contentX, contentY, ok := s.Host.EditorOrigin()
	if !ok {
		return 0, 0, false
	}
	relX := x - contentX
	relY := y - contentY
	if relX < 0 || relY < 0 {
		return 0, 0, false
	}

	width, height := s.Viewport()
	if relX >= width || relY >= height {
		return 0, 0, false
	}

	// The editor renders the pane at its observed size — clipped and scrolled
	// when it is larger than this viewport — so map through the fit rather
	// than assuming they line up (td-73fa86). A miss from an active editor
	// means the click landed in letterbox padding, so it must not fall back to
	// the raw mapping and forward a cell the pane does not have.
	if s.IsModelActive() {
		return s.Model.PaneCoords(relX+1, relY+1)
	}
	return relX + 1, relY + 1, true
}

// Cursor exposes the editor's native cursor in host-local coordinates,
// clamped to the host rect.
func (s *Session) Cursor(hostWidth, hostHeight int) *tea.Cursor {
	if s == nil || s.Model == nil || s.Host == nil {
		return nil
	}
	cursor := s.Model.Cursor()
	if cursor == nil {
		return nil
	}
	x, y, ok := s.Host.EditorOrigin()
	if !ok {
		return nil
	}
	moved := *cursor
	moved.X += x
	moved.Y += y
	if moved.X < 0 || moved.X >= hostWidth || moved.Y < 0 || moved.Y >= hostHeight {
		return nil
	}
	return &moved
}

// SizeIndicator reports what part of a larger tmux pane is hidden, if any.
func (s *Session) SizeIndicator() string {
	if s == nil || s.Model == nil {
		return ""
	}
	return s.Model.SizeIndicator()
}

func (s *Session) forwardMouse(button, col, row int, release bool) tea.Cmd {
	if s == nil || !s.IsModelActive() || s.Name == "" || !s.Model.PaneMouseReporting() {
		return nil
	}
	s.Model.NoteMouseActivity()
	return s.Target().MouseCmd(s.Model.Scope(), button, col, row, release)
}

// ForwardMousePress sends a press at 1-indexed editor coordinates.
func (s *Session) ForwardMousePress(col, row int) tea.Cmd {
	return s.forwardMouse(0, col, row, false)
}

// ForwardMouseDrag sends a drag/motion at 1-indexed editor coordinates.
func (s *Session) ForwardMouseDrag(col, row int) tea.Cmd {
	return s.forwardMouse(32, col, row, false)
}

// ForwardMouseRelease sends a release at 1-indexed editor coordinates.
func (s *Session) ForwardMouseRelease(col, row int) tea.Cmd {
	return s.forwardMouse(0, col, row, true)
}

// ClearPendingClick drops the click that triggered an exit confirmation.
func (s *Session) ClearPendingClick() {
	if s == nil {
		return
	}
	s.PendingClickRegion = ""
	s.PendingClickData = nil
}

// TakePendingClick returns and clears the pending click.
func (s *Session) TakePendingClick() (string, interface{}) {
	if s == nil {
		return "", nil
	}
	region, data := s.PendingClickRegion, s.PendingClickData
	s.ClearPendingClick()
	return region, data
}

// SetPendingClick records the click to replay once the editor exits.
func (s *Session) SetPendingClick(region string, data interface{}) {
	if s == nil {
		return
	}
	s.PendingClickRegion = region
	s.PendingClickData = data
}

// ExitConfirmOptions are the choices offered before leaving a live session.
var ExitConfirmOptions = []string{"Save & Exit", "Exit without saving", "Cancel"}

// MoveConfirmSelection moves the exit-confirmation cursor by delta, wrapping.
func (s *Session) MoveConfirmSelection(delta int) {
	if s == nil {
		return
	}
	n := len(ExitConfirmOptions)
	s.ConfirmSelection = ((s.ConfirmSelection+delta)%n + n) % n
}

// RenderExitConfirm renders the save/discard/cancel dialog body.
func (s *Session) RenderExitConfirm() string {
	selection := 0
	if s != nil {
		selection = s.ConfirmSelection
	}
	var sb strings.Builder
	sb.WriteString(styles.Title.Render("Exit editor?"))
	sb.WriteString("\n\n")
	for i, opt := range ExitConfirmOptions {
		if i == selection {
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

// EditingHeader renders the "Editing: <name>" header line with the exit hint
// and, when the tmux pane is larger than the viewport, the size indicator.
func (s *Session) EditingHeader(title string) string {
	var sb strings.Builder
	sb.WriteString(styles.Title.Render("Editing: " + title))
	sb.WriteString("  ")
	sb.WriteString(styles.Muted.Render("(Ctrl+\\ or ESC ESC to exit)"))
	if indicator := s.SizeIndicator(); indicator != "" {
		sb.WriteString("  ")
		sb.WriteString(styles.Muted.Render(indicator))
	}
	return sb.String()
}
