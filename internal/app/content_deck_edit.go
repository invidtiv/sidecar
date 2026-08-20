package app

import (
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/docview"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/inlineedit"
	"github.com/marcus/sidecar/internal/mouse"
	appmsg "github.com/marcus/sidecar/internal/msg"
	"github.com/marcus/sidecar/internal/paneframe"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/ui"
)

const appContentEditSurfacePrefix = "app-content-edit\x00"

// appDeckDocumentEdit is the app deck's host state around inlineedit's
// shared tmux lifecycle. The Deck still owns the document model and tab; this
// state owns only the transient editor session drawn in front of that model.
type appDeckDocumentEdit struct {
	h       *appContentDeck
	leafID  int
	session *inlineedit.Session
	width   int
	height  int
	pending func() tea.Cmd
}

type appContentEditHost struct{ edit *appDeckDocumentEdit }

var _ inlineedit.Host = appContentEditHost{}

func (x appContentEditHost) EditorViewport() (width, height int) {
	inner, ok := x.edit.innerBox()
	if !ok {
		return 0, 0
	}
	return inner.W, max(inner.H-paneframe.HeaderRows, 0)
}

func (x appContentEditHost) EditorOrigin() (xPos, yPos int, ok bool) {
	inner, ok := x.edit.innerBox()
	if !ok || inner.W <= 0 || inner.H <= paneframe.HeaderRows {
		return 0, 0, false
	}
	return inner.X, inner.Y + paneframe.HeaderRows, true
}

func (e *appDeckDocumentEdit) innerBox() (paneframe.Box, bool) {
	if e == nil || e.h == nil || !e.h.laidOut {
		return paneframe.Box{}, false
	}
	host := appDeckHost{e.h}
	for _, placement := range e.h.layout.Leaves {
		if placement.Node != nil && placement.Node.ID == e.leafID {
			return paneframe.GeometryForChrome(placement.Box, host.Chrome(placement.Node)).Inner, true
		}
	}
	return paneframe.Box{}, false
}

func (h *appContentDeck) appContentDocumentEdit(create bool) *appDeckDocumentEdit {
	if h == nil {
		return nil
	}
	if h.edit == nil && create {
		h.edit = &appDeckDocumentEdit{h: h}
	}
	return h.edit
}

func (e *appDeckDocumentEdit) editor() *inlineedit.Session {
	if e == nil {
		return nil
	}
	if e.session == nil {
		cfg := tty.DefaultConfig()
		e.session = inlineedit.New(appContentEditHost{edit: e}, &cfg)
		// A pane editor cannot suspend Sidecar into a full-screen attach: the
		// passive deck behind it would retain a session with no visible owner.
		e.session.Model.Config.AttachKey = ""
	}
	if e.session.Host == nil {
		e.session.Host = appContentEditHost{edit: e}
	}
	return e.session
}

func (e *appDeckDocumentEdit) editing() bool {
	return e != nil && e.session != nil && e.session.Active
}

func (h *appContentDeck) appContentDocumentEditing() bool {
	e := h.appContentDocumentEdit(false)
	return e != nil && e.editing() && h.deck != nil && h.deck.FocusedLeaf() == e.leafID
}

func (m Model) appContentDocumentEditContext() bool {
	h := m.currentContentDeck()
	return h != nil && h.appContentDocumentEditing()
}

func (h *appContentDeck) appContentEditSurface() string {
	if h == nil {
		return ""
	}
	return appContentEditSurfacePrefix + h.key
}

func (h *appContentDeck) activeDocumentForEdit() (int, *docview.Model) {
	if h == nil || h.deck == nil {
		return 0, nil
	}
	leafID := h.deck.FocusedLeaf()
	leaf := panelayout.Find(h.deck.Tree(), leafID)
	if leaf == nil || leaf.Kind != panelayout.Document {
		return 0, nil
	}
	view, _ := h.deck.Viewer(leafID).(*docview.Model)
	return leafID, view
}

func (m *Model) enterAppContentDocumentEdit() tea.Cmd {
	h := m.activeContentDeck()
	if h == nil || h.appContentDocumentEditing() {
		return nil
	}
	if !features.IsEnabled(features.TmuxInlineEdit.Name) {
		return appmsg.ShowToast("Inline edit is disabled (features.tmux-inline-edit)", 3*time.Second)
	}
	leafID, view := h.activeDocumentForEdit()
	if leafID == 0 || view == nil || view.Title() == "" {
		return nil
	}
	root, rel := view.Root(), view.Title()
	if root == "" {
		root = h.workdir
	}
	abs := rel
	if !filepath.IsAbs(abs) {
		if root == "" {
			return nil
		}
		abs = filepath.Join(root, rel)
	}
	e := h.appContentDocumentEdit(true)
	e.leafID = leafID
	session := e.editor()
	width, height := session.Viewport()
	if width <= 0 || height <= 0 {
		return nil
	}
	return inlineedit.Start(inlineedit.StartOptions{
		Surface: h.appContentEditSurface(), LeafID: leafID,
		AbsPath: abs, Path: rel, Line: view.TopSourceLine(),
		Width: width, Height: height,
		Activation: session.NextActivation(), Epoch: h.deck.Context().Epoch,
	})
}

func (m *Model) applyAppContentEditStarted(msg inlineedit.StartedMsg) (tea.Cmd, bool) {
	if !strings.HasPrefix(msg.Surface, appContentEditSurfacePrefix) {
		return nil, false
	}
	for _, h := range m.contentDecks {
		if h == nil || h.appContentEditSurface() != msg.Surface {
			continue
		}
		e := h.appContentDocumentEdit(false)
		if e == nil || e.leafID != msg.LeafID {
			return (tty.EditorSession{Name: msg.SessionName, Editor: msg.Editor}).KillCmd(), true
		}
		session := e.editor()
		if h.deck == nil || h.deck.Leaf(panelayout.Document) != msg.LeafID ||
			!session.OwnsMessage(msg.Activation, msg.Epoch, h.deck.Context().Epoch) {
			return session.CleanupStale(msg.SessionName, msg.Editor), true
		}
		activation := msg.Activation
		session.Model.OnExit = func() tea.Cmd {
			return func() tea.Msg {
				return inlineedit.ExitedMsg{Surface: msg.Surface, LeafID: msg.LeafID, Path: msg.Path,
					Activation: activation, Epoch: msg.Epoch}
			}
		}
		h.deck.FocusLeaf(msg.LeafID)
		h.syncInnerFocus()
		return session.Begin(msg.SessionName, msg.Editor, msg.Path), true
	}
	return (tty.EditorSession{Name: msg.SessionName, Editor: msg.Editor}).KillCmd(), true
}

func (m *Model) applyAppContentEditExited(msg inlineedit.ExitedMsg) (tea.Cmd, bool) {
	if !strings.HasPrefix(msg.Surface, appContentEditSurfacePrefix) {
		return nil, false
	}
	for _, h := range m.contentDecks {
		if h != nil && h.appContentEditSurface() == msg.Surface {
			e := h.appContentDocumentEdit(false)
			if e == nil || e.leafID != msg.LeafID || e.session == nil || h.deck == nil ||
				!e.session.OwnsMessage(msg.Activation, msg.Epoch, h.deck.Context().Epoch) {
				return nil, true
			}
			return h.exitAppContentDocumentEdit(), true
		}
	}
	return nil, true
}

func (h *appContentDeck) exitAppContentDocumentEdit() tea.Cmd {
	e := h.appContentDocumentEdit(false)
	if e == nil || e.session == nil {
		return nil
	}
	e.session.Exit()
	e.pending = nil
	view, _ := h.deck.Viewer(e.leafID).(*docview.Model)
	if view == nil {
		return nil
	}
	view.Observe()
	return view.Refresh(false)
}

func (h *appContentDeck) releaseAppContentDocumentEdit() {
	e := h.appContentDocumentEdit(false)
	if e != nil && e.session != nil {
		e.session.Exit()
	}
	h.edit = nil
}

func (m *Model) routeAppContentEditMsg(msg tea.Msg) (tea.Cmd, bool) {
	var cmds []tea.Cmd
	handled := false
	for _, h := range m.contentDecks {
		e := h.appContentDocumentEdit(false)
		if e == nil || !e.editing() {
			continue
		}
		handled = true
		cmd, alive := e.session.Route(msg)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
		if !alive {
			if exit := h.exitAppContentDocumentEdit(); exit != nil {
				cmds = append(cmds, exit)
			}
		}
	}
	return tea.Batch(cmds...), handled
}

func (m *Model) handleAppContentEditKey(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	h := m.activeContentDeck()
	if h == nil || !h.appContentDocumentEditing() {
		return nil, false
	}
	e := h.appContentDocumentEdit(false)
	session := e.session
	if session.ShowExitConfirm {
		outcome, _ := session.HandleConfirmKey(msg.String())
		switch outcome {
		case inlineedit.OutcomeSave, inlineedit.OutcomeDiscard:
			exit := h.exitAppContentDocumentEdit()
			pending := e.pending
			e.pending = nil
			if pending != nil {
				return tea.Batch(exit, pending()), true
			}
			return exit, true
		case inlineedit.OutcomeCancel:
			session.ClearPendingClick()
			e.pending = nil
		}
		return nil, true
	}
	if !session.IsModelActive() || !session.IsAlive() {
		return h.exitAppContentDocumentEdit(), true
	}
	cmd, alive := session.Route(msg)
	if !alive {
		return tea.Batch(cmd, h.exitAppContentDocumentEdit()), true
	}
	return cmd, true
}

func (h *appContentDeck) guardAppContentDocumentEdit(action func() tea.Cmd) bool {
	e := h.appContentDocumentEdit(false)
	if e == nil || !e.editing() || !e.session.IsAlive() {
		return false
	}
	e.session.ShowExitConfirm = true
	e.session.ConfirmSelection = 0
	e.pending = action
	return true
}

func (m *Model) handleAppContentEditMouse(msg tea.MouseMsg) (bool, tea.Cmd) {
	h := m.activeContentDeck()
	if h == nil || !h.appContentDocumentEditing() {
		return false, nil
	}
	e := h.appContentDocumentEdit(false)
	session := e.session
	if session.ShowExitConfirm {
		return true, nil
	}
	point := msg.Mouse()
	if !appContentEditPointInBody(session, point.X, point.Y) {
		if region := h.mouse.HitMap.Test(point.X, point.Y); region != nil && region.ID == appDeckDividerRegion {
			return false, nil
		}
		if _, ok := msg.(tea.MouseClickMsg); !ok {
			return false, nil
		}
		if h.guardAppContentDocumentEdit(func() tea.Cmd { return nil }) {
			return true, nil
		}
		return true, h.exitAppContentDocumentEdit()
	}
	action := h.mouse.HandleMouse(msg)
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
	return true, tea.Batch(cmd, h.exitAppContentDocumentEdit())
}

func appContentEditPointInBody(session *inlineedit.Session, x, y int) bool {
	if session == nil || session.Host == nil {
		return false
	}
	originX, originY, ok := session.Host.EditorOrigin()
	if !ok {
		return false
	}
	width, height := session.Viewport()
	return x >= originX && x < originX+width && y >= originY && y < originY+height
}

func (h *appContentDeck) renderAppContentDocumentEdit(leafID int, size paneframe.Size) (string, bool) {
	e := h.appContentDocumentEdit(false)
	if e == nil || !e.editing() || e.leafID != leafID {
		return "", false
	}
	session := e.editor()
	width, height := session.Viewport()
	if width != e.width || height != e.height {
		e.width, e.height = width, height
		if cmd := session.ResizeToViewport(); cmd != nil {
			h.queued = append(h.queued, cmd)
		}
	}
	header := ui.FitBlock(session.EditingHeader(filepath.Base(session.Path)), size.Width, paneframe.HeaderRows)
	bodyH := max(size.Height-paneframe.HeaderRows, 0)
	if bodyH == 0 {
		return header, true
	}
	body := ""
	if session.ShowExitConfirm {
		body = session.RenderExitConfirm()
	} else if session.Model != nil {
		body = session.Model.View()
	}
	return header + "\n" + ui.FitBlock(body, size.Width, bodyH), true
}

func (m Model) appContentDocumentEditCursor() *tea.Cursor {
	h := m.currentContentDeck()
	if h == nil || !h.appContentDocumentEditing() {
		return nil
	}
	session := h.appContentDocumentEdit(false).session
	if session == nil || !session.NativeActive() {
		return nil
	}
	return session.Cursor(h.canvas.W, h.canvas.H)
}

func (m Model) appContentDocumentEditMouseMode() (tea.MouseMode, bool) {
	h := m.currentContentDeck()
	if h == nil || !h.appContentDocumentEditing() {
		return tea.MouseModeAllMotion, false
	}
	session := h.appContentDocumentEdit(false).session
	return session.PreferredMouseMode(session.NativeActive()), true
}
