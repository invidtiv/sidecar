package notes

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/ui"
)

func isShiftMotion(msg tea.KeyPressMsg) bool {
	if !msg.Mod.Contains(tea.ModShift) {
		return false
	}
	switch msg.Code {
	case tea.KeyUp, tea.KeyDown, tea.KeyLeft, tea.KeyRight,
		tea.KeyHome, tea.KeyEnd, tea.KeyPgUp, tea.KeyPgDown:
		return true
	}
	return false
}

func isMotionKey(msg tea.KeyPressMsg) bool {
	if isShiftMotion(msg) {
		return true
	}
	switch msg.String() {
	case "up", "down", "left", "right", "home", "end",
		"ctrl+a", "ctrl+e", "ctrl+n", "ctrl+p",
		"ctrl+f", "ctrl+b", "ctrl+home", "ctrl+end",
		"alt+left", "alt+right", "alt+f", "alt+b",
		"alt+<", "alt+>", "pgup", "pgdown":
		return true
	}
	return false
}

func isDeleteKey(msg tea.KeyPressMsg) bool {
	switch msg.String() {
	case "backspace", "delete", "ctrl+h", "ctrl+d",
		"ctrl+k", "ctrl+u", "ctrl+w", "alt+backspace",
		"alt+delete", "alt+d":
		return true
	}
	return false
}

func isInsertKey(msg tea.KeyPressMsg) bool {
	switch msg.String() {
	case "enter", "ctrl+m":
		return true
	}
	if msg.Text == "" {
		return false
	}
	if msg.Mod.Contains(tea.ModCtrl) || msg.Mod.Contains(tea.ModAlt) || msg.Mod.Contains(tea.ModSuper) {
		return false
	}
	return true
}

func stripShift(msg tea.KeyPressMsg) tea.KeyPressMsg {
	msg.Mod &^= tea.ModShift
	return msg
}

func (p *Plugin) hasEditSelection() bool {
	if !p.selection.HasSelection() {
		return false
	}
	return compareSrc(srcFromPoint(p.selection.Start), srcFromPoint(p.selection.End)) != 0
}

func (p *Plugin) clearEditSelection() {
	p.selection.Clear()
	p.selAnchor = ui.SelectionPoint{-1, -1}
	p.selExtend = false
}

func (p *Plugin) historyForCurrent() *editHistory {
	if p.editHistories == nil {
		p.editHistories = make(map[string]*editHistory)
	}
	id := "_"
	if p.editorNote != nil && p.editorNote.ID != "" {
		id = p.editorNote.ID
	}
	h := p.editHistories[id]
	if h == nil {
		h = &editHistory{}
		p.editHistories[id] = h
	}
	return h
}

func (p *Plugin) snapshot() editSnapshot {
	return editSnapshot{
		content: p.editorTextarea.Value(),
		row:     p.editorTextarea.Line(),
		col:     p.editorTextarea.Column(),
	}
}

func (p *Plugin) prepareEdit(kind editOpKind) {
	p.historyForCurrent().prepareLazy(kind, p.snapshot, time.Now())
}

func (p *Plugin) restoreSnapshot(snap editSnapshot) {
	p.editorTextarea.SetValue(snap.content)
	p.setTextareaCursorPosition(snap.row, snap.col)
	p.clearEditSelection()
}

func (p *Plugin) afterContentChange() tea.Cmd {
	p.syncPreviewFromTextarea()
	p.invalidateViewSurface()
	p.trackTextareaScroll()
	if p.editorNote != nil && p.editorTextarea.Value() == p.lastSavedContent {
		p.editorDirty = false
		return nil
	}
	p.editorDirty = true
	return p.startAutoSaveTimer()
}

func (p *Plugin) undoEditorEdit() (pluginResult, tea.Cmd) {
	prev, ok := p.historyForCurrent().undoTo(p.snapshot())
	if !ok {
		return p, nil
	}
	p.restoreSnapshot(prev)
	return p, p.afterContentChange()
}

func (p *Plugin) redoEditorEdit() (pluginResult, tea.Cmd) {
	next, ok := p.historyForCurrent().redoTo(p.snapshot())
	if !ok {
		return p, nil
	}
	p.restoreSnapshot(next)
	return p, p.afterContentChange()
}

// pluginResult avoids importing plugin in every helper signature below.
// handleEditorKey already returns plugin.Plugin; these stay on *Plugin.
type pluginResult = *Plugin

func (p *Plugin) toggleSelectAnchor() (pluginResult, tea.Cmd) {
	if p.selExtend || p.hasEditSelection() {
		p.clearEditSelection()
		return p, nil
	}
	p.selExtend = true
	p.selAnchor = ui.SelectionPoint{Line: p.editorTextarea.Line(), Col: p.editorTextarea.Column()}
	return p, nil
}

func (p *Plugin) selectAllEditor() (pluginResult, tea.Cmd) {
	start, end := allSourceRange(p.editorTextarea.Value())
	if compareSrc(start, end) == 0 {
		return p, nil
	}
	p.selExtend = false
	p.selAnchor = start.point()
	p.selection.SelectRange(start.point(), end.point(), false)
	p.selection.Anchor = p.selAnchor
	p.setTextareaCursorPosition(end.line, end.col)
	p.trackTextareaScroll()
	return p, nil
}

func (p *Plugin) extendSelectionByMotion(msg tea.KeyPressMsg) (pluginResult, tea.Cmd) {
	if !p.selAnchor.Valid() {
		if p.selection.Anchor.Valid() {
			p.selAnchor = p.selection.Anchor
		} else {
			p.selAnchor = ui.SelectionPoint{Line: p.editorTextarea.Line(), Col: p.editorTextarea.Column()}
		}
	}
	var cmd tea.Cmd
	p.editorTextarea, cmd = p.editorTextarea.Update(stripShift(msg))
	p.trackTextareaScroll()
	caret := srcPos{line: p.editorTextarea.Line(), col: p.editorTextarea.Column()}
	start, end, empty := caretPairToExclusive(srcFromPoint(p.selAnchor), caret)
	if empty {
		p.selection.Clear()
		p.selection.Anchor = p.selAnchor
	} else {
		p.selection.SelectRange(start.point(), end.point(), false)
		p.selection.Anchor = p.selAnchor
	}
	return p, cmd
}

func (p *Plugin) replaceEditorSelection(insert string, kind editOpKind) (pluginResult, tea.Cmd) {
	if !p.hasEditSelection() {
		return p, nil
	}
	p.prepareEdit(kind)
	start, end := orderSrc(srcFromPoint(p.selection.Start), srcFromPoint(p.selection.End))
	out, caret := spliceExclusive(p.editorTextarea.Value(), insert, start, end)
	p.editorTextarea.SetValue(out)
	p.setTextareaCursorPosition(caret.line, caret.col)
	p.clearEditSelection()
	return p, p.afterContentChange()
}

func (p *Plugin) deleteEditorSelection(kind editOpKind) (pluginResult, tea.Cmd) {
	return p.replaceEditorSelection("", kind)
}

func (p *Plugin) cutEditorSelection() (pluginResult, tea.Cmd) {
	if !p.hasEditSelection() {
		return p, nil
	}
	copyCmd := p.copySelectionCmd()
	_, delCmd := p.deleteEditorSelection(editOpCut)
	return p, tea.Batch(copyCmd, delCmd)
}

func (p *Plugin) copyEditorOrSelection() tea.Cmd {
	if p.hasEditSelection() {
		return p.copySelectionCmd()
	}
	return p.copyEditorContent()
}
