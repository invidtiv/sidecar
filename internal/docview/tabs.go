package docview

import (
	"path/filepath"

	"github.com/marcus/sidecar/internal/tabs"
)

// Tabs is a document pane's open files. The pane tree points at this group,
// not at one model.
type Tabs struct {
	Items  []Item
	Active int
}

// Item is one open document.
type Item struct {
	View *Model
}

func (t Tabs) asGroup() tabs.Group[*Model] {
	g := tabs.Group[*Model]{Active: t.Active}
	g.Items = make([]tabs.Item[*Model], len(t.Items))
	for i, item := range t.Items {
		key := ""
		if item.View != nil {
			key = NormalizeTabPath(item.View.Title())
		}
		g.Items[i] = tabs.Item[*Model]{Key: key, Value: item.View}
	}
	return g
}

func (t *Tabs) fromGroup(g tabs.Group[*Model]) {
	t.Items = make([]Item, len(g.Items))
	for i, item := range g.Items {
		t.Items[i] = Item{View: item.Value}
	}
	t.Active = g.Active
}

// ActiveView is the selected document, or nil.
func (t Tabs) ActiveView() *Model {
	if item, ok := t.asGroup().ActiveItem(); ok {
		return item.Value
	}
	return nil
}

// IndexOf returns the tab whose display path matches path, or -1.
// The key is the root-relative path, slash-normalized.
func (t Tabs) IndexOf(path string) int {
	key := NormalizeTabPath(path)
	if key == "" || key == "." {
		return -1
	}
	return t.asGroup().Find(key)
}

// Append adds view and selects it.
func (t *Tabs) Append(view *Model) {
	g := t.asGroup()
	key := ""
	if view != nil {
		key = NormalizeTabPath(view.Title())
	}
	g.Append(key, view)
	t.fromGroup(g)
}

// Select makes i the active tab.
func (t *Tabs) Select(i int) {
	g := t.asGroup()
	g.Select(i)
	t.fromGroup(g)
}

// Cycle moves Active by delta, wrapping at the ends. A single tab is a no-op.
func (t *Tabs) Cycle(delta int) {
	g := t.asGroup()
	g.Cycle(delta)
	t.fromGroup(g)
}

// CloseActive removes the selected tab. The next tab (or the new last) is
// selected. An empty group leaves Active at 0.
func (t *Tabs) CloseActive() {
	g := t.asGroup()
	g.CloseActive()
	t.fromGroup(g)
}

// CloseAt removes the tab at index. Out of range is a no-op.
func (t *Tabs) CloseAt(index int) {
	g := t.asGroup()
	g.CloseAt(index)
	t.fromGroup(g)
}

// VisibleRange is the overflow window for this group: the inclusive [start, end]
// of tabs that fit in maxWidth, keeping Active on screen.
func (t Tabs) VisibleRange(widths []int, maxWidth int) (start, end int, showLeft, showRight bool) {
	return tabs.VisibleRange(widths, t.Active, maxWidth)
}

// VisibleTabRange is the inclusive [start, end] window of tabs that fit in
// maxWidth given their rendered widths, keeping the active tab visible.
// showLeft and showRight are the overflow markers for tabs outside the window.
func VisibleTabRange(widths []int, active, maxWidth int) (start, end int, showLeft, showRight bool) {
	return tabs.VisibleRange(widths, active, maxWidth)
}

// NormalizeTabPath is the dedup key: slash-normalized, cleaned display path.
func NormalizeTabPath(path string) string {
	return filepath.ToSlash(filepath.Clean(path))
}
