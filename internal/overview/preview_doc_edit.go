package overview

import (
	"path/filepath"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/inlineedit"
	"github.com/marcus/sidecar/internal/mouse"
	appmsg "github.com/marcus/sidecar/internal/msg"
	"github.com/marcus/sidecar/internal/termpreview"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/ui"
)

// Inline editing inside the browser's preview document pane. The lifecycle is
// [inlineedit]'s; this file answers only what this surface answers differently,
// and is the peer of internal/plugins/workspace/doc_edit.go.

// previewDocEditSurface tags this surface's editor messages; the project
// workspace reads the same bus.
const previewDocEditSurface = "overview"

// previewDocEditHost is the pane's half of the editor's dimension contract. It
// answers from the box the pane was last drawn in, which is the same box the
// document viewer is sized with.
type previewDocEditHost struct{ doc *previewDoc }

var _ inlineedit.Host = previewDocEditHost{}

func (h previewDocEditHost) EditorViewport() (width, height int) {
	if h.doc == nil {
		return 0, 0
	}
	return h.doc.box.W, max(h.doc.box.H-termpreview.HeaderRows, 0)
}

func (h previewDocEditHost) EditorOrigin() (x, y int, ok bool) {
	if h.doc == nil || h.doc.box.W <= 0 || h.doc.box.H <= termpreview.HeaderRows {
		return 0, 0, false
	}
	return h.doc.box.X, h.doc.box.Y + termpreview.HeaderRows, true
}

// editor is the pane's editor session, bound to its host on first use.
func (d *previewDoc) editor() *inlineedit.Session {
	if d == nil {
		return nil
	}
	if d.edit == nil {
		config := tty.DefaultConfig()
		d.edit = inlineedit.New(previewDocEditHost{d}, &config)
		// No full-screen attach from a pane: suspending sidecar would leave the
		// browser behind it holding a session nobody owns.
		d.edit.Model.Config.AttachKey = ""
	}
	if d.edit.Host == nil {
		d.edit.Host = previewDocEditHost{d}
	}
	return d.edit
}

func (d *previewDoc) editing() bool { return d != nil && d.edit != nil && d.edit.Active }

// releaseEdit kills this pane's session, if it has one. The guards ask first on
// the routes that can; this covers the ones that drop the pane outright (a
// selection change, a pane rebuilt for another workspace), where an unreleased
// session would be an orphan editor holding the file.
func (d *previewDoc) releaseEdit() {
	if d == nil || d.edit == nil {
		return
	}
	d.edit.Exit()
}

func (d *previewDoc) editorPath() (abs, rel string, ok bool) {
	view := d.view()
	if d == nil || view == nil {
		return "", "", false
	}
	rel = view.Title()
	if rel == "" {
		return "", "", false
	}
	root := view.Root()
	if root == "" {
		root = d.root
	}
	if filepath.IsAbs(rel) {
		return rel, rel, true
	}
	if root == "" {
		return "", "", false
	}
	return filepath.Join(root, rel), rel, true
}

// previewDocEditing reports that a live editor in the preview owns the
// keyboard. It is the document pane's answer to PreviewInteractive: this
// browser bypasses the app's context ladder, so "the preview owns the keyboard"
// has to be stated here for the app to honour it.
func (m *Model) previewDocEditing() bool {
	return m.preview.doc.editing()
}

// PreviewOwnsKeyboard reports that the preview — a live terminal or a live
// editor — is taking every key. The app asks so the mouse mode, the cursor and
// the key ladder all follow one fact.
func (m *Model) PreviewOwnsKeyboard() bool {
	return m.PreviewInteractive() || m.previewDocEditing()
}

// enterPreviewDocEdit opens the focused document's file in an inline editor.
func (m *Model) enterPreviewDocEdit() tea.Cmd {
	doc := m.preview.doc
	if doc == nil || doc.editing() {
		return nil
	}
	if !features.IsEnabled(features.TmuxInlineEdit.Name) {
		return appmsg.ShowToast("Inline edit is disabled (features.tmux-inline-edit)", 3*time.Second)
	}
	abs, rel, ok := doc.editorPath()
	if !ok {
		return nil
	}
	session := doc.editor()
	width, height := session.Viewport()
	if width <= 0 || height <= 0 {
		return nil
	}
	line := 0
	if view := doc.view(); view != nil {
		line = view.TopSourceLine()
	}
	return inlineedit.Start(inlineedit.StartOptions{
		Surface:    previewDocEditSurface,
		AbsPath:    abs,
		Path:       rel,
		Line:       line,
		Width:      width,
		Height:     height,
		Activation: session.NextActivation(),
		Epoch:      doc.epoch,
	})
}

// applyPreviewDocEditStarted attaches the pane to the session tmux just started.
func (m *Model) applyPreviewDocEditStarted(msg inlineedit.StartedMsg) tea.Cmd {
	if msg.Surface != previewDocEditSurface {
		return nil
	}
	doc := m.preview.doc
	if doc == nil {
		return (tty.EditorSession{Name: msg.SessionName, Editor: msg.Editor}).KillCmd()
	}
	session := doc.editor()
	if !session.OwnsMessage(msg.Activation, msg.Epoch, doc.epoch) {
		return session.CleanupStale(msg.SessionName, msg.Editor)
	}
	activation := msg.Activation
	session.Model.OnExit = func() tea.Cmd {
		return func() tea.Msg {
			return inlineedit.ExitedMsg{
				Surface:    previewDocEditSurface,
				Path:       msg.Path,
				Activation: activation,
				Epoch:      msg.Epoch,
			}
		}
	}
	doc.focused = true
	return session.Begin(msg.SessionName, msg.Editor, msg.Path)
}

func (m *Model) applyPreviewDocEditExited(msg inlineedit.ExitedMsg) tea.Cmd {
	if msg.Surface != previewDocEditSurface || !m.previewDocEditing() {
		return nil
	}
	return m.exitPreviewDocEdit()
}

// exitPreviewDocEdit kills the session and re-reads the document it was editing.
func (m *Model) exitPreviewDocEdit() tea.Cmd {
	doc := m.preview.doc
	if doc == nil || doc.edit == nil {
		return nil
	}
	doc.edit.Exit()
	view := doc.view()
	if view == nil {
		return nil
	}
	view.Observe()
	return wrapPreviewDocLoad(view.Refresh(false), doc.surface)
}

// PreviewDocEditMsg offers the pane editor one of the terminal component's own
// messages, alongside the browser's terminal.
func (m *Model) PreviewDocEditMsg(msg tea.Msg) tea.Cmd {
	if !m.previewDocEditing() {
		return nil
	}
	cmd, alive := m.preview.doc.edit.Route(msg)
	if alive {
		return cmd
	}
	return tea.Batch(cmd, m.exitPreviewDocEdit())
}

// handlePreviewDocEditKey is the live editor's input context: every key is the
// editor's bar the ones the exit confirmation owns.
func (m *Model) handlePreviewDocEditKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	doc := m.preview.doc
	session := doc.editor()
	if session.ShowExitConfirm {
		outcome, handled := session.HandleConfirmKey(msg.String())
		if !handled {
			return true, nil
		}
		return true, m.applyPreviewDocEditOutcome(outcome)
	}
	if !session.IsModelActive() || !session.IsAlive() {
		return true, m.exitPreviewDocEdit()
	}
	cmd, alive := session.Route(msg)
	if !alive {
		return true, tea.Batch(cmd, m.exitPreviewDocEdit())
	}
	return true, cmd
}

func (m *Model) applyPreviewDocEditOutcome(outcome inlineedit.Outcome) tea.Cmd {
	doc := m.preview.doc
	switch outcome {
	case inlineedit.OutcomeSave, inlineedit.OutcomeDiscard:
		exit := m.exitPreviewDocEdit()
		pending := doc.pendingEdit
		doc.pendingEdit = nil
		if pending == nil {
			return exit
		}
		return tea.Batch(exit, pending())
	case inlineedit.OutcomeCancel:
		doc.editor().ClearPendingClick()
		doc.pendingEdit = nil
		return nil
	default:
		return nil
	}
}

// guardPreviewDocEdit stops an action that would take a live editor away,
// raising the exit confirmation and holding the action until it is answered.
func (m *Model) guardPreviewDocEdit(action func() tea.Cmd) bool {
	doc := m.preview.doc
	if !doc.editing() {
		return false
	}
	session := doc.editor()
	if !session.IsAlive() {
		return false
	}
	session.ShowExitConfirm = true
	session.ConfirmSelection = 0
	doc.pendingEdit = action
	return true
}

// handlePreviewDocEditMouse routes the pointer while an editor is live: inside
// the pane body it is the editor's, and anywhere else it is a click away from a
// session that has not been saved.
func (m *Model) handlePreviewDocEditMouse(msg tea.MouseMsg) (bool, tea.Cmd) {
	doc := m.preview.doc
	session := doc.editor()
	if session.ShowExitConfirm {
		return true, nil
	}
	point := msg.Mouse()
	if !previewDocEditPointInBody(session, point.X, point.Y) {
		// A divider still belongs to the pane the editor is in: dragging it
		// resizes the pane, and the render that follows resizes the PTY. Leave
		// it to the ordinary pointer path, which owns the drag.
		if region := m.workspacesMouse.HitMap.Test(point.X, point.Y); region != nil &&
			region.ID == previewPaneDividerKind {
			return false, nil
		}
		if _, ok := msg.(tea.MouseClickMsg); !ok {
			// Hover, motion and release outside the body are not decisions, and
			// a drag in flight needs every one of them: hand them back.
			return false, nil
		}
		if m.guardPreviewDocEdit(func() tea.Cmd { return nil }) {
			return true, nil
		}
		return true, m.exitPreviewDocEdit()
	}
	action := m.workspacesMouse.HandleMouse(msg)
	switch action.Type {
	case mouse.ActionClick, mouse.ActionDoubleClick, mouse.ActionTripleClick:
		if col, row, ok := session.MouseCoords(action.X, action.Y); ok {
			session.Dragging = true
			return true, session.ForwardMousePress(col, row)
		}
		return true, nil
	case mouse.ActionHover:
		if session.Dragging {
			if col, row, ok := session.MouseCoords(action.X, action.Y); ok {
				return true, session.ForwardMouseDrag(col, row)
			}
		}
		return true, nil
	}
	if _, ok := msg.(tea.MouseReleaseMsg); ok && session.Dragging {
		session.Dragging = false
		if col, row, ok := session.MouseCoords(point.X, point.Y); ok {
			return true, session.ForwardMouseRelease(col, row)
		}
		return true, nil
	}
	cmd, alive := session.Route(msg)
	if alive {
		return true, cmd
	}
	return true, tea.Batch(cmd, m.exitPreviewDocEdit())
}

// previewDocEditPointInBody reports whether a tab-local point is inside the
// editor's own rectangle.
func previewDocEditPointInBody(session *inlineedit.Session, x, y int) bool {
	originX, originY, ok := session.Host.EditorOrigin()
	if !ok {
		return false
	}
	width, height := session.Viewport()
	return x >= originX && x < originX+width && y >= originY && y < originY+height
}

// renderPreviewDocEdit draws the editor in the pane's box: one header row
// saying which file has the keyboard, and the editor's pixels below it.
func (m *Model) renderPreviewDocEdit(doc *previewDoc, box termpreview.Box) string {
	session := doc.editor()
	header := ui.FitBlock(session.EditingHeader(filepath.Base(session.Path)), box.W, 1)
	bodyH := max(box.H-termpreview.HeaderRows, 0)
	if bodyH <= 0 {
		return header
	}
	body := ""
	if session.ShowExitConfirm {
		body = session.RenderExitConfirm()
	} else if session.Model != nil {
		body = session.Model.View()
	}
	return header + "\n" + ui.FitBlock(body, box.W, bodyH)
}

// resizePreviewDocEdit re-sizes a live editor to the box the pane was just
// given, so a window resize or a divider drag moves the PTY with the pane.
func (d *previewDoc) resizePreviewDocEdit() tea.Cmd {
	if !d.editing() {
		return nil
	}
	width, height := d.edit.Viewport()
	if width == d.editW && height == d.editH {
		return nil
	}
	d.editW, d.editH = width, height
	return d.edit.ResizeToViewport()
}

// previewDocEditCursor is the live editor's native cursor in tab-local
// coordinates.
func (m *Model) previewDocEditCursor() *tea.Cursor {
	if !m.previewDocEditing() {
		return nil
	}
	doc := m.preview.doc
	if !doc.edit.NativeActive() {
		return nil
	}
	return doc.edit.Cursor(m.width, m.height)
}
