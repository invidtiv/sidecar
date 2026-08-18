package notes

import (
	"strings"
	"testing"
	"time"
)

func TestEditHistoryTypingBurstIsOneUnit(t *testing.T) {
	h := &editHistory{}
	now := time.Now()
	h.prepare(editOpTyping, editSnapshot{content: "ab"}, now)
	h.prepare(editOpTyping, editSnapshot{content: "abc"}, now.Add(100*time.Millisecond))
	h.prepare(editOpTyping, editSnapshot{content: "abcd"}, now.Add(200*time.Millisecond))
	if len(h.undo) != 1 {
		t.Fatalf("burst undo entries = %d, want 1", len(h.undo))
	}
	if h.undo[0].content != "ab" {
		t.Fatalf("burst snapshot = %q, want content before first keystroke", h.undo[0].content)
	}
}

func TestEditHistoryTypingBurstCopiesOneSnapshot(t *testing.T) {
	h := &editHistory{}
	now := time.Now()
	calls := 0
	snapshot := func() editSnapshot {
		calls++
		return editSnapshot{content: strings.Repeat("large note", 1000)}
	}
	h.prepareLazy(editOpTyping, snapshot, now)
	h.prepareLazy(editOpTyping, snapshot, now.Add(100*time.Millisecond))
	h.prepareLazy(editOpTyping, snapshot, now.Add(200*time.Millisecond))
	if calls != 1 {
		t.Fatalf("snapshot copies = %d, want one per typing burst", calls)
	}
}

func TestEditHistoryPasteIsOwnUnit(t *testing.T) {
	h := &editHistory{}
	now := time.Now()
	h.prepare(editOpTyping, editSnapshot{content: "ab"}, now)
	h.prepare(editOpPaste, editSnapshot{content: "abc"}, now.Add(50*time.Millisecond))
	if len(h.undo) != 2 {
		t.Fatalf("paste after typing = %d units, want 2", len(h.undo))
	}
}

func TestEditHistoryExpiredBurstStartsNewUnit(t *testing.T) {
	h := &editHistory{}
	now := time.Now()
	h.prepare(editOpTyping, editSnapshot{content: "a"}, now)
	h.prepare(editOpTyping, editSnapshot{content: "ab"}, now.Add(typingBurstWindow+time.Millisecond))
	if len(h.undo) != 2 {
		t.Fatalf("expired burst = %d units, want 2", len(h.undo))
	}
}

func TestEditHistoryUndoRedoRoundTrip(t *testing.T) {
	h := &editHistory{}
	h.prepare(editOpPaste, editSnapshot{content: "one", row: 0, col: 0}, time.Now())
	prev, ok := h.undoTo(editSnapshot{content: "two", row: 0, col: 3})
	if !ok || prev.content != "one" {
		t.Fatalf("undo = (%q, %v), want one", prev.content, ok)
	}
	if !h.canRedo() {
		t.Fatal("expected redo after undo")
	}
	next, ok := h.redoTo(editSnapshot{content: "one", row: 0, col: 0})
	if !ok || next.content != "two" {
		t.Fatalf("redo = (%q, %v), want two", next.content, ok)
	}
}

func TestEditHistoryCapsEntriesAndBytes(t *testing.T) {
	h := &editHistory{}
	now := time.Now()
	for i := 0; i < maxEditUndoEntries+10; i++ {
		h.prepare(editOpPaste, editSnapshot{content: strings.Repeat("x", 8) + string(rune('A'+i%26))}, now.Add(time.Duration(i)*time.Second))
	}
	if len(h.undo) > maxEditUndoEntries {
		t.Fatalf("undo entries = %d, want <= %d", len(h.undo), maxEditUndoEntries)
	}

	h = &editHistory{}
	big := strings.Repeat("n", maxEditUndoBytes/3)
	for i := 0; i < 6; i++ {
		h.prepare(editOpPaste, editSnapshot{content: big + string(rune('0'+i))}, now.Add(time.Duration(i)*time.Second))
	}
	if h.bytes > maxEditUndoBytes {
		t.Fatalf("retained bytes = %d, want <= %d", h.bytes, maxEditUndoBytes)
	}
	if len(h.undo) < 1 {
		t.Fatal("byte cap dropped every snapshot")
	}
}
