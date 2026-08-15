// Package tabs is a content-neutral ordered tab group and strip layout.
// Hosts own keys, labels, and values; this package does not know about
// files, issues, panes, or workspaces.
package tabs

// Group is an ordered tab set with one active index.
type Group[T any] struct {
	Items  []Item[T]
	Active int
}

// Item is one open tab. Keys need not be unique; Find returns the first match.
type Item[T any] struct {
	Key     string
	Value   T
	Preview bool
}

// CloseResult tells a host what CloseMatching removed so it can clean values
// and decide whether to load the survivor. The group does not know about
// tmux, file deletion, or preview reset.
type CloseResult[T any] struct {
	Removed       []Item[T]
	ActiveRemoved bool
	Empty         bool
}

// Find returns the first item whose Key equals key, or -1.
func (g Group[T]) Find(key string) int {
	for i, item := range g.Items {
		if item.Key == key {
			return i
		}
	}
	return -1
}

// ActiveItem is the selected tab, or false when the group is empty or Active is out of range.
func (g Group[T]) ActiveItem() (Item[T], bool) {
	if g.Active < 0 || g.Active >= len(g.Items) {
		return Item[T]{}, false
	}
	return g.Items[g.Active], true
}

// OpenOrFocus selects the first item with key, or appends value and selects it.
func (g *Group[T]) OpenOrFocus(key string, value T) (index int, created bool) {
	if i := g.Find(key); i >= 0 {
		g.Select(i)
		return i, false
	}
	g.Append(key, value)
	return g.Active, true
}

// Append always adds a tab and selects it.
func (g *Group[T]) Append(key string, value T) {
	g.AppendItem(Item[T]{Key: key, Value: value})
}

// AppendItem always adds item and selects it.
func (g *Group[T]) AppendItem(item Item[T]) {
	g.Items = append(g.Items, item)
	g.Active = len(g.Items) - 1
}

// Select makes i the active tab. Out of range is a no-op.
func (g *Group[T]) Select(i int) {
	if i < 0 || i >= len(g.Items) {
		return
	}
	g.Active = i
}

// Cycle moves Active by delta, wrapping at the ends. Fewer than two tabs is a no-op.
func (g *Group[T]) Cycle(delta int) {
	n := len(g.Items)
	if n < 2 {
		return
	}
	g.Active = (g.Active + delta) % n
	if g.Active < 0 {
		g.Active += n
	}
}

// CloseActive removes the selected tab. An empty group leaves Active at 0.
func (g *Group[T]) CloseActive() CloseResult[T] {
	if len(g.Items) == 0 {
		g.Active = 0
		return CloseResult[T]{Empty: true}
	}
	return g.CloseAt(g.Active)
}

// CloseAt removes the tab at index. Out of range is a no-op.
func (g *Group[T]) CloseAt(index int) CloseResult[T] {
	if index < 0 || index >= len(g.Items) {
		return CloseResult[T]{Empty: len(g.Items) == 0}
	}
	return g.closeMatching(func(_ Item[T], i int) bool { return i == index })
}

// CloseMatching removes every item for which pred is true.
// Removals before the original Active are counted once, then Active is clamped.
func (g *Group[T]) CloseMatching(pred func(Item[T]) bool) CloseResult[T] {
	if pred == nil {
		return CloseResult[T]{Empty: len(g.Items) == 0}
	}
	return g.closeMatching(func(item Item[T], _ int) bool { return pred(item) })
}

func (g *Group[T]) closeMatching(pred func(Item[T], int) bool) CloseResult[T] {
	if len(g.Items) == 0 {
		g.Active = 0
		return CloseResult[T]{Empty: true}
	}

	originalActive := g.Active
	removedBeforeActive := 0
	kept := make([]Item[T], 0, len(g.Items))
	removed := make([]Item[T], 0)
	activeRemoved := false

	for i, item := range g.Items {
		if !pred(item, i) {
			kept = append(kept, item)
			continue
		}
		removed = append(removed, item)
		if i == originalActive {
			activeRemoved = true
		}
		if i < originalActive {
			removedBeforeActive++
		}
	}
	if len(removed) == 0 {
		return CloseResult[T]{}
	}

	g.Items = kept
	if len(g.Items) == 0 {
		g.Active = 0
		return CloseResult[T]{Removed: removed, ActiveRemoved: activeRemoved, Empty: true}
	}

	g.Active = originalActive - removedBeforeActive
	if g.Active >= len(g.Items) {
		g.Active = len(g.Items) - 1
	}
	if g.Active < 0 {
		g.Active = 0
	}
	return CloseResult[T]{Removed: removed, ActiveRemoved: activeRemoved}
}

// VisibleRange is the overflow window for this group, keeping Active on screen.
func (g Group[T]) VisibleRange(widths []int, maxWidth int) (start, end int, showLeft, showRight bool) {
	return VisibleRange(widths, g.Active, maxWidth)
}

// VisibleRange is the inclusive [start, end] window of tabs that fit in
// maxWidth given their rendered widths, keeping the active tab visible.
// showLeft and showRight are the overflow markers for tabs outside the window.
func VisibleRange(widths []int, active, maxWidth int) (start, end int, showLeft, showRight bool) {
	if len(widths) == 0 {
		return 0, -1, false, false
	}
	if active < 0 || active >= len(widths) {
		return 0, -1, false, false
	}

	start = active
	end = active
	used := widths[active]

	for {
		expanded := false
		if end+1 < len(widths) && used+1+widths[end+1] <= maxWidth {
			end++
			used += 1 + widths[end]
			expanded = true
		}
		if start-1 >= 0 && used+1+widths[start-1] <= maxWidth {
			start--
			used += 1 + widths[start]
			expanded = true
		}
		if !expanded {
			break
		}
	}

	showLeft = start > 0
	showRight = end < len(widths)-1

	for {
		indicatorTokens := 0
		if showLeft {
			indicatorTokens++
		}
		if showRight {
			indicatorTokens++
		}

		tabCount := end - start + 1
		if tabCount < 1 {
			return 0, -1, false, false
		}

		totalTokens := tabCount + indicatorTokens
		sepCount := totalTokens - 1
		totalWidth := sumTabWidths(widths, start, end) + indicatorTokens + sepCount

		if totalWidth <= maxWidth || tabCount == 1 {
			break
		}

		if end-active >= active-start {
			end--
		} else {
			start++
		}

		showLeft = start > 0
		showRight = end < len(widths)-1
	}

	return start, end, showLeft, showRight
}

func sumTabWidths(widths []int, start, end int) int {
	total := 0
	for i := start; i <= end; i++ {
		total += widths[i]
	}
	return total
}
