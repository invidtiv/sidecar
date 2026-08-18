package notes

import "time"

const (
	maxEditUndoEntries = 100
	maxEditUndoBytes   = 2 << 20
	typingBurstWindow  = 800 * time.Millisecond
)

// editOpKind groups consecutive mutations into one undo unit.
type editOpKind int

const (
	editOpNone editOpKind = iota
	editOpTyping
	editOpDelete
	editOpPaste
	editOpCut
	editOpReplace
)

// editSnapshot is the buffer and caret before a mutation.
type editSnapshot struct {
	content string
	row     int
	col     int
}

type editHistory struct {
	undo     []editSnapshot
	redo     []editSnapshot
	lastKind editOpKind
	lastAt   time.Time
	bytes    int
}

func (h *editHistory) prepare(kind editOpKind, snap editSnapshot, now time.Time) {
	h.prepareLazy(kind, func() editSnapshot { return snap }, now)
}

// prepareLazy avoids copying the entire note for every key in a coalesced
// typing/delete burst. Large notes pay once per undo unit, not per character.
func (h *editHistory) prepareLazy(kind editOpKind, snapshot func() editSnapshot, now time.Time) {
	if kind == editOpNone {
		return
	}
	if kind == editOpTyping && h.lastKind == editOpTyping && !h.lastAt.IsZero() && now.Sub(h.lastAt) < typingBurstWindow {
		h.lastAt = now
		return
	}
	if kind == editOpDelete && h.lastKind == editOpDelete && !h.lastAt.IsZero() && now.Sub(h.lastAt) < typingBurstWindow {
		h.lastAt = now
		return
	}
	snap := snapshot()
	if n := len(h.undo); n > 0 && h.undo[n-1].content == snap.content {
		h.lastKind = kind
		h.lastAt = now
		return
	}
	h.pushUndo(snap)
	h.clearRedo()
	h.lastKind = kind
	h.lastAt = now
}

func (h *editHistory) pushUndo(snap editSnapshot) {
	h.undo = append(h.undo, snap)
	h.bytes += len(snap.content)
	h.trim()
}

func (h *editHistory) clearRedo() {
	for _, s := range h.redo {
		h.bytes -= len(s.content)
	}
	h.redo = nil
}

func (h *editHistory) canUndo() bool { return len(h.undo) > 0 }
func (h *editHistory) canRedo() bool { return len(h.redo) > 0 }

func (h *editHistory) undoTo(current editSnapshot) (editSnapshot, bool) {
	if len(h.undo) == 0 {
		return editSnapshot{}, false
	}
	prev := h.undo[len(h.undo)-1]
	h.undo = h.undo[:len(h.undo)-1]
	h.bytes -= len(prev.content)
	h.redo = append(h.redo, current)
	h.bytes += len(current.content)
	h.trim()
	h.lastKind = editOpNone
	h.lastAt = time.Time{}
	return prev, true
}

func (h *editHistory) redoTo(current editSnapshot) (editSnapshot, bool) {
	if len(h.redo) == 0 {
		return editSnapshot{}, false
	}
	next := h.redo[len(h.redo)-1]
	h.redo = h.redo[:len(h.redo)-1]
	h.bytes -= len(next.content)
	h.undo = append(h.undo, current)
	h.bytes += len(current.content)
	h.trim()
	h.lastKind = editOpNone
	h.lastAt = time.Time{}
	return next, true
}

func (h *editHistory) trim() {
	for len(h.undo) > maxEditUndoEntries {
		h.bytes -= len(h.undo[0].content)
		h.undo = h.undo[1:]
	}
	for h.bytes > maxEditUndoBytes && len(h.undo) > 1 {
		h.bytes -= len(h.undo[0].content)
		h.undo = h.undo[1:]
	}
	for h.bytes > maxEditUndoBytes && len(h.redo) > 0 {
		h.bytes -= len(h.redo[0].content)
		h.redo = h.redo[1:]
	}
}
