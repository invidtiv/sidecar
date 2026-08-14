package docview

import (
	"path/filepath"
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

// ActiveView is the selected document, or nil.
func (t Tabs) ActiveView() *Model {
	if t.Active < 0 || t.Active >= len(t.Items) {
		return nil
	}
	return t.Items[t.Active].View
}

// IndexOf returns the tab whose display path matches path, or -1.
// The key is the root-relative path, slash-normalized.
func (t Tabs) IndexOf(path string) int {
	key := NormalizeTabPath(path)
	if key == "" || key == "." {
		return -1
	}
	for i, item := range t.Items {
		if item.View == nil {
			continue
		}
		if NormalizeTabPath(item.View.Title()) == key {
			return i
		}
	}
	return -1
}

// Append adds view and selects it.
func (t *Tabs) Append(view *Model) {
	t.Items = append(t.Items, Item{View: view})
	t.Active = len(t.Items) - 1
}

// Select makes i the active tab.
func (t *Tabs) Select(i int) {
	if i < 0 || i >= len(t.Items) {
		return
	}
	t.Active = i
}

// Cycle moves Active by delta, wrapping at the ends. A single tab is a no-op.
func (t *Tabs) Cycle(delta int) {
	n := len(t.Items)
	if n < 2 {
		return
	}
	t.Active = (t.Active + delta) % n
	if t.Active < 0 {
		t.Active += n
	}
}

// CloseActive removes the selected tab. The next tab (or the new last) is
// selected. An empty group leaves Active at 0.
func (t *Tabs) CloseActive() {
	if len(t.Items) == 0 {
		return
	}
	t.Items = append(t.Items[:t.Active], t.Items[t.Active+1:]...)
	if t.Active >= len(t.Items) {
		t.Active = len(t.Items) - 1
	}
	if t.Active < 0 {
		t.Active = 0
	}
}

// VisibleRange is the overflow window for this group: the inclusive [start, end]
// of tabs that fit in maxWidth, keeping Active on screen.
func (t Tabs) VisibleRange(widths []int, maxWidth int) (start, end int, showLeft, showRight bool) {
	return VisibleTabRange(widths, t.Active, maxWidth)
}

// VisibleTabRange is the inclusive [start, end] window of tabs that fit in
// maxWidth given their rendered widths, keeping the active tab visible.
// showLeft and showRight are the overflow markers for tabs outside the window.
func VisibleTabRange(widths []int, active, maxWidth int) (start, end int, showLeft, showRight bool) {
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

// NormalizeTabPath is the dedup key: slash-normalized, cleaned display path.
func NormalizeTabPath(path string) string {
	return filepath.ToSlash(filepath.Clean(path))
}
