package workspacediff

import "github.com/marcus/sidecar/internal/tabs"

// Group is one Diff leaf's open targets. The pane tree points at this,
// not at one View. Key = Target.Identity().
type Group struct {
	tabs.Group[*View]
}

// ActiveView is the selected Diff view, or nil.
func (g Group) ActiveView() *View {
	item, ok := g.ActiveItem()
	if !ok {
		return nil
	}
	return item.Value
}

// Find returns the first tab whose key is the target identity, or -1.
func (g Group) Find(id string) int {
	if id == "" {
		return -1
	}
	return g.Group.Find(id)
}

// OpenOrFocus selects the tab for t, or appends view and selects it.
// An empty Identity is a no-op.
func (g *Group) OpenOrFocus(t Target, view *View) (index int, created bool) {
	key := t.Identity()
	if key == "" {
		return -1, false
	}
	if view != nil {
		view.Target = t
	}
	return g.Group.OpenOrFocus(key, view)
}
