package workspace

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/ui"
)

// docTabMinBudget is the floor for one tab's column share, chrome included.
const docTabMinBudget = 8

// docTabHit is a drawn tab's click target. Index is the tab in the group;
// LeafID is the pane-tree leaf so two doc panes cannot steal each other's click.
type docTabHit struct {
	LeafID int
	Index  int
}

// docTabPlacement is one drawn tab on the strip.
type docTabPlacement struct {
	Index    int
	Col      int
	Width    int
	Rendered string
}

// docTabStrip is the header row: only tabs, packed left to right.
type docTabStrip struct {
	Row  string
	Tabs []docTabPlacement
}

func docTabActive(focused bool, index, active int) bool {
	return focused && index == active
}

func renderDocTab(label string, index, total int, active bool) string {
	return styles.RenderTab(label, index, total, active, false)
}

func tabChromeWidth(index, total int, active bool) int {
	return lipgloss.Width(renderDocTab("X", index, total, active)) - 1
}

// fitDocTab left-truncates path so RenderTab fits in maxWidth. The filename
// end is what survives; the pill is never clipped in half.
func fitDocTab(path string, index, total, maxWidth int, active bool) string {
	if maxWidth < 1 {
		return ""
	}
	labelW := maxWidth - tabChromeWidth(index, total, active)
	if labelW < 1 {
		labelW = 1
	}
	for labelW >= 1 {
		rendered := renderDocTab(ui.TruncateStart(path, labelW), index, total, active)
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

func docTabPaths(doc *docPane) []string {
	if doc == nil {
		return nil
	}
	paths := make([]string, 0, len(doc.tabs.Items))
	for _, item := range doc.tabs.Items {
		path := ""
		if item.View != nil {
			path = item.View.Title()
		}
		paths = append(paths, path)
	}
	return paths
}

func packedDocTabsWidth(widths []int, start, end int, showLeft, showRight bool) int {
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

func padDocTabRow(s string, width int) string {
	w := lipgloss.Width(s)
	if w == width {
		return s
	}
	if w > width {
		return ansi.Truncate(s, width, "")
	}
	return s + strings.Repeat(" ", width-w)
}

func paintDocTabStrip(rendered []string, widths []int, start, end int, showLeft, showRight bool, width int) docTabStrip {
	var tokens []string
	var tabs []docTabPlacement
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
		tabs = append(tabs, docTabPlacement{Index: i, Col: x, Width: widths[i], Rendered: rendered[i]})
		x += widths[i]
	}
	if showRight {
		if len(tokens) > 0 {
			tokens = append(tokens, " ")
		}
		tokens = append(tokens, styles.Muted.Render(">"))
	}
	return docTabStrip{Row: padDocTabRow(strings.Join(tokens, ""), width), Tabs: tabs}
}

// layoutDocTabStrip is the doc leaf's header: a left-truncated path tab strip
// and nothing else. Overflow lives on the tab group, not layoutHeaderChips.
func layoutDocTabStrip(doc *docPane, width int, focused bool) docTabStrip {
	if width < 1 {
		return docTabStrip{}
	}
	paths := docTabPaths(doc)
	n := len(paths)
	if n == 0 {
		return docTabStrip{Row: strings.Repeat(" ", width)}
	}
	active := 0
	if doc != nil {
		active = doc.tabs.Active
	}
	if active < 0 || active >= n {
		active = 0
	}

	if n == 1 {
		rendered := fitDocTab(paths[0], 0, 1, width, docTabActive(focused, 0, active))
		return docTabStrip{
			Row:  padDocTabRow(rendered, width),
			Tabs: []docTabPlacement{{Index: 0, Col: 0, Width: lipgloss.Width(rendered), Rendered: rendered}},
		}
	}

	share := (width - (n - 1)) / n
	if share < docTabMinBudget {
		share = docTabMinBudget
	}

	rendered := make([]string, n)
	widths := make([]int, n)
	for i, path := range paths {
		rendered[i] = fitDocTab(path, i, n, share, docTabActive(focused, i, active))
		widths[i] = lipgloss.Width(rendered[i])
	}

	start, end, showLeft, showRight := doc.tabs.VisibleRange(widths, width)
	if start > end {
		return docTabStrip{Row: strings.Repeat(" ", width)}
	}

	leftover := width - packedDocTabsWidth(widths, start, end, showLeft, showRight)
	if leftover > 0 && active >= start && active <= end {
		rendered[active] = fitDocTab(paths[active], active, n, widths[active]+leftover, docTabActive(focused, active, active))
		widths[active] = lipgloss.Width(rendered[active])
	}

	return paintDocTabStrip(rendered, widths, start, end, showLeft, showRight, width)
}
