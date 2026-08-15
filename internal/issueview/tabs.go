package issueview

import (
	"strings"

	"github.com/marcus/sidecar/internal/tabs"
	"github.com/marcus/sidecar/internal/terminallink"
)

// Tabs is one issue pane's open issues. The pane tree points at this group,
// not at one model. The stable key is a trimmed, validated TD issue ID.
type Tabs struct {
	tabs.Group[*Model]
}

// NormalizeID is the tab key: trimmed and held to the same shape a terminal
// link click can produce. Invalid values are empty.
func NormalizeID(id string) string {
	id = strings.TrimSpace(id)
	if !terminallink.IssueID(id) {
		return ""
	}
	return id
}

// ActiveView is the selected issue, or nil.
func (t Tabs) ActiveView() *Model {
	item, ok := t.ActiveItem()
	if !ok {
		return nil
	}
	return item.Value
}

// Find returns the first tab whose key is the normalized issue ID, or -1.
func (t Tabs) Find(issueID string) int {
	key := NormalizeID(issueID)
	if key == "" {
		return -1
	}
	return t.Group.Find(key)
}

// OpenOrFocus selects the tab for issueID, or appends view and selects it.
// An invalid ID is a no-op.
func (t *Tabs) OpenOrFocus(issueID string, view *Model) (index int, created bool) {
	key := NormalizeID(issueID)
	if key == "" {
		return -1, false
	}
	return t.Group.OpenOrFocus(key, view)
}
