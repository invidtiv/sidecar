package noteview

import (
	"strings"

	"github.com/marcus/sidecar/internal/contentlink"
	"github.com/marcus/sidecar/internal/tabs"
)

// Tabs is one note pane's open notes. The stable key is a trimmed, validated
// td note ID.
type Tabs struct {
	tabs.Group[*Model]
}

// NormalizeID is the tab key. Invalid values are empty.
func NormalizeID(id string) string {
	id = strings.TrimSpace(id)
	if !contentlink.NoteID(id) {
		return ""
	}
	return id
}

// ActiveView is the selected note, or nil.
func (t Tabs) ActiveView() *Model {
	item, ok := t.ActiveItem()
	if !ok {
		return nil
	}
	return item.Value
}

// Find returns the first tab whose key is the normalized note ID, or -1.
func (t Tabs) Find(noteID string) int {
	key := NormalizeID(noteID)
	if key == "" {
		return -1
	}
	return t.Group.Find(key)
}

// OpenOrFocus selects the tab for noteID, or appends view and selects it.
func (t *Tabs) OpenOrFocus(noteID string, view *Model) (index int, created bool) {
	key := NormalizeID(noteID)
	if key == "" {
		return -1, false
	}
	return t.Group.OpenOrFocus(key, view)
}
