package docview

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/ui"
)

// TabMinBudget is the floor for one tab's column share, chrome included.
const TabMinBudget = 8

// TabHit is a drawn tab's click target. Col is relative to the strip's first
// column; Width is the rendered pill.
type TabHit struct {
	Index    int
	Col      int
	Width    int
	Rendered string
}

// TabStrip is the header row: only tabs, packed left to right.
type TabStrip struct {
	Row  string
	Tabs []TabHit
}

func tabActive(focused bool, index, active int) bool {
	return focused && index == active
}

func renderTab(label string, index, total int, active bool) string {
	return styles.RenderTab(label, index, total, active, false)
}

func tabChromeWidth(index, total int, active bool) int {
	return lipgloss.Width(renderTab("X", index, total, active)) - 1
}

// fitTab left-truncates path so RenderTab fits in maxWidth. The filename end
// is what survives; the pill is never clipped in half.
func fitTab(path string, index, total, maxWidth int, active bool) string {
	if maxWidth < 1 {
		return ""
	}
	labelW := maxWidth - tabChromeWidth(index, total, active)
	if labelW < 1 {
		labelW = 1
	}
	for labelW >= 1 {
		rendered := renderTab(ui.TruncateStart(path, labelW), index, total, active)
		if w := lipgloss.Width(rendered); w <= maxWidth {
			return rendered
		}
		if labelW == 1 {
			return ansi.Truncate(rendered, maxWidth, "")
		}
		labelW--
	}
	return ""
}

func tabPaths(tabs Tabs) []string {
	paths := make([]string, 0, len(tabs.Items))
	for _, item := range tabs.Items {
		path := ""
		if item.View != nil {
			path = item.View.Title()
		}
		paths = append(paths, path)
	}
	return paths
}

func packedTabsWidth(widths []int, start, end int, showLeft, showRight bool) int {
	tabCount := end - start + 1
	if tabCount < 1 {
		return 0
	}
	total := 0
	for i := start; i <= end; i++ {
		total += widths[i]
	}
	indicators := 0
	if showLeft {
		indicators++
	}
	if showRight {
		indicators++
	}
	tokens := tabCount + indicators
	seps := tokens - 1
	if seps < 0 {
		seps = 0
	}
	return total + indicators + seps
}

func padTabRow(s string, width int) string {
	w := lipgloss.Width(s)
	if w == width {
		return s
	}
	if w > width {
		return ansi.Truncate(s, width, "")
	}
	return s + strings.Repeat(" ", width-w)
}

func paintTabStrip(rendered []string, widths []int, start, end int, showLeft, showRight bool, width int) TabStrip {
	var tokens []string
	var tabs []TabHit
	x := 0
	if showLeft {
		tokens = append(tokens, styles.Muted.Render("<"))
		x++
	}
	for i := start; i <= end; i++ {
		if len(tokens) > 0 {
			tokens = append(tokens, " ")
			x++
		}
		tokens = append(tokens, rendered[i])
		tabs = append(tabs, TabHit{Index: i, Col: x, Width: widths[i], Rendered: rendered[i]})
		x += widths[i]
	}
	if showRight {
		if len(tokens) > 0 {
			tokens = append(tokens, " ")
		}
		tokens = append(tokens, styles.Muted.Render(">"))
	}
	return TabStrip{Row: padTabRow(strings.Join(tokens, ""), width), Tabs: tabs}
}

// LayoutTabStrip is the document header: a left-truncated path tab strip and
// nothing else. Overflow lives on the tab group. Hosts draw this row and
// register hit regions from the same Tabs so a click cannot land on a tab
// that was never rendered.
func LayoutTabStrip(tabs Tabs, width int, focused bool) TabStrip {
	if width < 1 {
		return TabStrip{}
	}
	paths := tabPaths(tabs)
	n := len(paths)
	if n == 0 {
		return TabStrip{Row: strings.Repeat(" ", width)}
	}
	active := tabs.Active
	if active < 0 || active >= n {
		active = 0
	}

	if n == 1 {
		rendered := fitTab(paths[0], 0, 1, width, tabActive(focused, 0, active))
		return TabStrip{
			Row:  padTabRow(rendered, width),
			Tabs: []TabHit{{Index: 0, Col: 0, Width: lipgloss.Width(rendered), Rendered: rendered}},
		}
	}

	share := (width - (n - 1)) / n
	if share < TabMinBudget {
		share = TabMinBudget
	}

	rendered := make([]string, n)
	widths := make([]int, n)
	for i, path := range paths {
		rendered[i] = fitTab(path, i, n, share, tabActive(focused, i, active))
		widths[i] = lipgloss.Width(rendered[i])
	}

	start, end, showLeft, showRight := tabs.VisibleRange(widths, width)
	if start > end {
		return TabStrip{Row: strings.Repeat(" ", width)}
	}

	leftover := width - packedTabsWidth(widths, start, end, showLeft, showRight)
	if leftover > 0 && active >= start && active <= end {
		rendered[active] = fitTab(paths[active], active, n, widths[active]+leftover, tabActive(focused, active, active))
		widths[active] = lipgloss.Width(rendered[active])
	}

	return paintTabStrip(rendered, widths, start, end, showLeft, showRight, width)
}
