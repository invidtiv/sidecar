package inlineedit

import (
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/msg"
	"github.com/marcus/sidecar/internal/tty"
)

// Pane-hosted editors (workspace doc panes and the global browser's preview
// doc pane) start the same way and pump the same messages, so the start command
// and the message pump live here rather than being written twice. The
// filebrowser and notes plugins keep their own start paths: their pre-checks,
// toasts and full-screen attach genuinely differ.

// StartedMsg reports a tmux editor session started for a pane leaf. Surface
// distinguishes the two workspace projections, which both see every message.
type StartedMsg struct {
	Surface     string
	LeafID      int
	SessionName string
	Editor      string
	Path        string // the path the host knows the document by
	Activation  uint64
	Epoch       uint64
}

// ExitedMsg reports that a pane's editor session ended.
type ExitedMsg struct {
	Surface    string
	LeafID     int
	Path       string
	Activation uint64
	Epoch      uint64
}

// StartOptions is what a pane host must say to open an editor: which file, at
// what size, on behalf of which leaf.
type StartOptions struct {
	Surface       string
	LeafID        int
	AbsPath       string // file handed to the editor
	Path          string // path echoed back in StartedMsg
	Line          int    // 1-indexed; 0 leaves the editor at the top
	Width, Height int
	Activation    uint64
	Epoch         uint64
}

// Start launches a tmux editor session for a pane leaf. It reports failures as
// toasts rather than errors: there is nowhere else in a pane to say them.
func Start(opts StartOptions) tea.Cmd {
	editor := tty.ResolveEditor()
	return func() tea.Msg {
		if !tty.EditorAvailable() {
			return msg.ToastMsg{
				Message:  "Inline edit needs tmux",
				Duration: 3 * time.Second,
				IsError:  true,
			}
		}
		session, err := tty.StartEditorSession(tty.EditorSessionOptions{
			NamePrefix: "sidecar-pane-edit-",
			Editor:     editor,
			Path:       opts.AbsPath,
			Line:       opts.Line,
			Width:      opts.Width,
			Height:     opts.Height,
		})
		if err != nil {
			return msg.ToastMsg{
				Message:  fmt.Sprintf("Failed to start editor: %v", err),
				Duration: 3 * time.Second,
				IsError:  true,
			}
		}
		return StartedMsg{
			Surface:     opts.Surface,
			LeafID:      opts.LeafID,
			SessionName: session.Name,
			Editor:      session.Editor,
			Path:        opts.Path,
			Activation:  opts.Activation,
			Epoch:       opts.Epoch,
		}
	}
}

// Route hands one message to the live tty model and reports whether the session
// is still attached afterwards. A host that gets alive=false owes its own exit
// bookkeeping — the editor quit on its own.
func (s *Session) Route(m tea.Msg) (cmd tea.Cmd, alive bool) {
	if s == nil || s.Model == nil || !s.Active {
		return nil, false
	}
	cmd = s.Model.Update(m)
	return cmd, s.Model.IsActive()
}

// Outcome is what the exit confirmation decided.
type Outcome int

const (
	// OutcomePending means the key moved the selection or was swallowed.
	OutcomePending Outcome = iota
	// OutcomeSave writes the buffer and leaves.
	OutcomeSave
	// OutcomeDiscard leaves without writing.
	OutcomeDiscard
	// OutcomeCancel returns to the editor.
	OutcomeCancel
)

// HandleConfirmKey drives the exit confirmation and reports what it decided.
// handled is false for keys the dialog does not own, so a host can swallow them
// itself rather than let them reach the surface behind a modal.
func (s *Session) HandleConfirmKey(key string) (outcome Outcome, handled bool) {
	if s == nil || !s.ShowExitConfirm {
		return OutcomePending, false
	}
	switch key {
	case "j", "down":
		s.MoveConfirmSelection(1)
		return OutcomePending, true
	case "k", "up":
		s.MoveConfirmSelection(-1)
		return OutcomePending, true
	case "enter":
		s.ShowExitConfirm = false
		switch s.ConfirmSelection {
		case 0:
			s.SaveAndQuit()
			return OutcomeSave, true
		case 1:
			return OutcomeDiscard, true
		default:
			return OutcomeCancel, true
		}
	case "esc", "q":
		s.ShowExitConfirm = false
		s.ClearPendingClick()
		return OutcomeCancel, true
	}
	return OutcomePending, true
}
