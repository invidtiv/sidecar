package workspace

import (
	"path/filepath"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/inlineedit"
	"github.com/marcus/sidecar/internal/mouse"
	appmsg "github.com/marcus/sidecar/internal/msg"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/ui"
)

// Inline editing inside a document leaf.
//
// The lifecycle is [inlineedit]'s; what is here is only what this surface
// answers differently: which leaf is being edited, where that leaf's body is,
// and which of the pane's own actions must stop and ask before they take a live
// editor away. The global browser answers the same questions in
// internal/overview/preview_doc_edit.go, and neither file owns any editor
// behaviour of its own.

// docEditSurface tags this surface's editor messages. Both projections are
// hosted in one process and read the same bus.
const docEditSurface = "workspace"

// docEditHost is the leaf's half of the editor's dimension contract. It answers
// from the box the leaf was last drawn in — the same numbers the document
// viewer is sized with — so the PTY cannot be sized against a rectangle the
// frame never gave this leaf.
type docEditHost struct{ doc *docPane }

var _ inlineedit.Host = docEditHost{}

func (h docEditHost) EditorViewport() (width, height int) {
	if h.doc == nil {
		return 0, 0
	}
	return h.doc.boxW, maxInt(h.doc.boxH-terminalHeaderRows, 0)
}

func (h docEditHost) EditorOrigin() (x, y int, ok bool) {
	if h.doc == nil || h.doc.boxW <= 0 || h.doc.boxH <= terminalHeaderRows {
		return 0, 0, false
	}
	return h.doc.boxX, h.doc.boxY + terminalHeaderRows, true
}

// editor is the leaf's editor session, bound to its host on first use. Leaves
// are built in a dozen places, so binding here rather than at construction is
// what keeps a session from silently opening at 0x0.
func (d *docPane) editor() *inlineedit.Session {
	if d == nil {
		return nil
	}
	if d.edit == nil {
		config := tty.DefaultConfig()
		d.edit = inlineedit.New(docEditHost{d}, &config)
		// A pane has no full-screen attach: suspending sidecar from inside a
		// leaf would leave the split behind it holding a session nobody owns.
		d.edit.Model.Config.AttachKey = ""
	}
	if d.edit.Host == nil {
		d.edit.Host = docEditHost{d}
	}
	return d.edit
}

// editing reports that this leaf is hosting a live editor.
func (d *docPane) editing() bool { return d != nil && d.edit != nil && d.edit.Active }

// editorPath is the absolute file the leaf's active tab is showing, or "".
func (d *docPane) editorPath() (abs, rel string, ok bool) {
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

// editingDocPane is the leaf whose editor is live, focused or not. Only one
// pane's editor takes keys, but every one of them owns tmux messages.
func (p *Plugin) editingDocPane() *docPane {
	for _, doc := range p.docs {
		if doc.editing() {
			return doc
		}
	}
	return nil
}

// focusedDocEdit is the editor that owns the keyboard: the focused document
// leaf's, and only while this surface is showing it.
func (p *Plugin) focusedDocEdit() *docPane {
	doc := p.focusedDocPane()
	if !doc.editing() {
		return nil
	}
	return doc
}

// docEditActive is the focus-context question: does a live editor in a focused
// document leaf own the keyboard?
func (p *Plugin) docEditActive() bool { return p.focusedDocEdit() != nil }

// enterDocEdit opens the focused document's file in an inline editor.
func (p *Plugin) enterDocEdit(doc *docPane) tea.Cmd {
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
	var epoch uint64
	if p.ctx != nil {
		epoch = p.ctx.Epoch
	}
	return inlineedit.Start(inlineedit.StartOptions{
		Surface:    docEditSurface,
		LeafID:     doc.leafID,
		AbsPath:    abs,
		Path:       rel,
		Line:       line,
		Width:      width,
		Height:     height,
		Activation: session.NextActivation(),
		Epoch:      epoch,
	})
}

// applyDocEditStarted attaches the leaf to the session tmux just started.
func (p *Plugin) applyDocEditStarted(msg inlineedit.StartedMsg) tea.Cmd {
	if msg.Surface != docEditSurface {
		return nil
	}
	doc := p.docForLeaf(msg.LeafID)
	if doc == nil {
		return (tty.EditorSession{Name: msg.SessionName, Editor: msg.Editor}).KillCmd()
	}
	session := doc.editor()
	var epoch uint64
	if p.ctx != nil {
		epoch = p.ctx.Epoch
	}
	if !session.OwnsMessage(msg.Activation, msg.Epoch, epoch) {
		return session.CleanupStale(msg.SessionName, msg.Editor)
	}
	leafID, activation := doc.leafID, msg.Activation
	session.Model.OnExit = func() tea.Cmd {
		return func() tea.Msg {
			return inlineedit.ExitedMsg{
				Surface:    docEditSurface,
				LeafID:     leafID,
				Path:       msg.Path,
				Activation: activation,
				Epoch:      msg.Epoch,
			}
		}
	}
	p.activePane = PanePreview
	p.focusLeaf(doc.leafID)
	return session.Begin(msg.SessionName, msg.Editor, msg.Path)
}

// applyDocEditExited tears the leaf's editor down and re-reads the file it was
// editing, so the pane shows what was just written.
func (p *Plugin) applyDocEditExited(msg inlineedit.ExitedMsg) tea.Cmd {
	if msg.Surface != docEditSurface {
		return nil
	}
	doc := p.docForLeaf(msg.LeafID)
	if doc == nil || !doc.editing() {
		return nil
	}
	return p.exitDocEdit(doc)
}

func (p *Plugin) docForLeaf(leafID int) *docPane {
	for _, doc := range p.docs {
		if doc != nil && doc.leafID == leafID {
			return doc
		}
	}
	return nil
}

// exitDocEdit kills the leaf's session and re-reads its document.
func (p *Plugin) exitDocEdit(doc *docPane) tea.Cmd {
	if doc == nil || doc.edit == nil {
		return nil
	}
	doc.edit.Exit()
	if view := doc.view(); view != nil {
		view.Observe()
		return view.Refresh(false)
	}
	return nil
}

// routeDocEditMsg offers an embedded-terminal message to every live pane
// editor. The messages are scope-tagged, so a session that does not own one
// ignores it.
func (p *Plugin) routeDocEditMsg(msg tea.Msg) tea.Cmd {
	var cmds []tea.Cmd
	for _, doc := range p.docs {
		if !doc.editing() {
			continue
		}
		cmd, alive := doc.edit.Route(msg)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
		if !alive {
			if exit := p.exitDocEdit(doc); exit != nil {
				cmds = append(cmds, exit)
			}
		}
	}
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}

// handleDocEditKey is the focused editor's input context. Every key is the
// editor's — the app forwards them all in the workspace-doc-edit context — bar
// the ones the exit confirmation owns while it is up.
func (p *Plugin) handleDocEditKey(doc *docPane, msg tea.KeyPressMsg) (bool, tea.Cmd) {
	session := doc.editor()
	if session.ShowExitConfirm {
		outcome, handled := session.HandleConfirmKey(msg.String())
		if !handled {
			return true, nil
		}
		return true, p.applyDocEditOutcome(doc, outcome)
	}
	// The editor may have quit without the model noticing yet (:wq races the
	// poll), in which case the key belongs to the document again.
	if !session.IsModelActive() || !session.IsAlive() {
		return true, p.exitDocEdit(doc)
	}
	cmd, alive := session.Route(msg)
	if !alive {
		return true, tea.Batch(cmd, p.exitDocEdit(doc))
	}
	return true, cmd
}

// applyDocEditOutcome performs what the confirmation decided and then the
// action that raised it.
func (p *Plugin) applyDocEditOutcome(doc *docPane, outcome inlineedit.Outcome) tea.Cmd {
	session := doc.editor()
	switch outcome {
	case inlineedit.OutcomeSave, inlineedit.OutcomeDiscard:
		exit := p.exitDocEdit(doc)
		pending := p.takeDocEditPending(doc)
		return tea.Batch(exit, pending)
	case inlineedit.OutcomeCancel:
		session.ClearPendingClick()
		doc.pendingEdit = nil
		return nil
	default:
		return nil
	}
}

// guardDocEdit stops an action that would take a live editor away, raising the
// exit confirmation and holding the action until it is answered. It reports
// whether it intercepted; a leaf with no editor is never intercepted, which is
// what keeps the ordinary pane keys unchanged.
func (p *Plugin) guardDocEdit(doc *docPane, action func() tea.Cmd) bool {
	if !doc.editing() {
		return false
	}
	session := doc.editor()
	if !session.IsAlive() {
		// Nothing to lose: the editor already quit.
		return false
	}
	session.ShowExitConfirm = true
	session.ConfirmSelection = 0
	doc.pendingEdit = action
	return true
}

func (p *Plugin) takeDocEditPending(doc *docPane) tea.Cmd {
	if doc == nil || doc.pendingEdit == nil {
		return nil
	}
	action := doc.pendingEdit
	doc.pendingEdit = nil
	return action()
}

// handleDocEditMouse routes the pointer while an editor is live: inside the
// leaf's body it is the editor's, and anywhere else it is a click away from a
// live session, which must ask before it is taken.
func (p *Plugin) handleDocEditMouse(doc *docPane, msg tea.MouseMsg) (bool, tea.Cmd) {
	session := doc.editor()
	if session.ShowExitConfirm {
		// The dialog owns the pointer the way it owns the keyboard; nothing
		// underneath it may act on a click.
		return true, nil
	}
	point := msg.Mouse()
	if !docEditPointInBody(session, point.X, point.Y) {
		// A divider is the one thing outside the body that still belongs to the
		// pane the editor is in: dragging it resizes the leaf, and the leaf's
		// SetSize resizes the PTY. Leave it to the ordinary pointer path, whose
		// gesture machine owns the drag.
		if region := p.mouseHandler.HitMap.Test(point.X, point.Y); region != nil &&
			region.ID == regionPaneTreeDivider {
			return false, nil
		}
		// Anything else outside is a request to leave a session that has not
		// been saved. Hold it until the confirmation is answered; the click
		// itself is spent saying so.
		if _, ok := msg.(tea.MouseClickMsg); !ok {
			// Hover, motion and release outside the body are not decisions, and
			// a drag in flight needs every one of them: hand them back.
			return false, nil
		}
		if p.guardDocEdit(doc, func() tea.Cmd { return nil }) {
			return true, nil
		}
		// The session already quit, so there is nothing to confirm: clean up and
		// let the next click act on the pane it landed in.
		return true, p.exitDocEdit(doc)
	}
	action := p.mouseHandler.HandleMouse(msg)
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
	if !alive {
		return true, tea.Batch(cmd, p.exitDocEdit(doc))
	}
	return true, cmd
}

// docEditPointInBody reports whether a plugin-local point is inside the
// editor's own rectangle.
func docEditPointInBody(session *inlineedit.Session, x, y int) bool {
	originX, originY, ok := session.Host.EditorOrigin()
	if !ok {
		return false
	}
	width, height := session.Viewport()
	return x >= originX && x < originX+width && y >= originY && y < originY+height
}

// docEditHeaderRow replaces the leaf's tab strip while it is editing: the same
// single row, saying which file has the keyboard and how to give it back.
func (p *Plugin) docEditHeaderRow(doc *docPane, width int) string {
	header := doc.editor().EditingHeader(filepath.Base(doc.edit.Path))
	return ui.FitBlock(header, width, 1)
}

// renderDocEditBody is the editor's pixels inside the leaf's body box.
func (p *Plugin) renderDocEditBody(doc *docPane, width, height int) string {
	session := doc.editor()
	if session.ShowExitConfirm {
		return ui.FitBlock(session.RenderExitConfirm(), width, height)
	}
	body := ""
	if session.Model != nil {
		body = session.Model.View()
	}
	return ui.FitBlock(body, width, height)
}

// docEditCursor is the focused editor's native cursor in plugin-local
// coordinates.
func (p *Plugin) docEditCursor() *tea.Cursor {
	doc := p.focusedDocEdit()
	if doc == nil || !p.focused || !doc.edit.NativeActive() {
		return nil
	}
	return doc.edit.Cursor(p.width, p.height)
}

// docEditNativeActive reports that a pane editor owns the keyboard, which is
// what the mouse mode follows.
func (p *Plugin) docEditNativeActive() bool {
	doc := p.focusedDocEdit()
	return doc != nil && p.focused && doc.edit.NativeActive()
}

// resizeDocEditCmd re-sizes a live editor to the box the leaf was just given.
// It is called from SetSize, so a drag handle, a window resize and +/- all
// reach the PTY through one path.
func (d *docPane) resizeDocEditCmd() tea.Cmd {
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
