package tabs

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/ui"
)

// MinBudget is the floor for one tab's column share, chrome and the per-tab
// close control included.
const MinBudget = 10

// Label is the text a host wants drawn on one tab.
type Label struct {
	Text    string
	Preview bool
}

// FitLabel shortens a tab's text so the rendered pill fits in maxWidth.
// maxWidth is the label column budget (chrome and close already reserved).
// The function returns label text, not the painted tab.
type FitLabel func(text string, index, total, maxWidth int, active bool) string

// Hit is a drawn tab's click target. Col is relative to the strip's first
// column; Width is the rendered pill. CloseCol/CloseW are the × control on
// the pill's right edge; CloseW is 0 when the tab was too narrow to hold one.
type Hit struct {
	Index    int
	Col      int
	Width    int
	Rendered string
	CloseCol int
	CloseW   int
}

// Strip is the header row: only tabs, packed left to right.
type Strip struct {
	Row  string
	Tabs []Hit
}

// RegisterHits calls add once for each painted tab, then once for each tab's
// close control. Close is registered second so it wins the cells it occupies,
// the same order paneframe uses for the leaf-header X.
func (s Strip) RegisterHits(add func(col, width, index int, close bool)) {
	if add == nil {
		return
	}
	for _, tab := range s.Tabs {
		add(tab.Col, tab.Width, tab.Index, false)
	}
	for _, tab := range s.Tabs {
		if tab.CloseW > 0 {
			add(tab.CloseCol, tab.CloseW, tab.Index, true)
		}
	}
}

func tabActive(focused bool, index, active int) bool {
	return focused && index == active
}

func tabChromeWidth(index, total int, active bool) int {
	return lipgloss.Width(styles.RenderTab("X", index, total, active, false)) - 1
}

func closeSuffix() string {
	return " " + ui.CloseButtonLabel
}

func closeOffset(rendered string) (col, width int) {
	plain := ansi.Strip(rendered)
	runes := []rune(plain)
	at := -1
	for i, r := range runes {
		if string(r) == ui.CloseButtonLabel {
			at = i
		}
	}
	if at < 0 {
		return 0, 0
	}
	start := at
	if at > 0 && runes[at-1] == ' ' {
		start = at - 1
	}
	return lipgloss.Width(string(runes[:start])), lipgloss.Width(string(runes[start : at+1]))
}

func tabHit(index, col int, rendered string) Hit {
	hit := Hit{Index: index, Col: col, Width: lipgloss.Width(rendered), Rendered: rendered}
	if off, w := closeOffset(rendered); w > 0 {
		hit.CloseCol = col + off
		hit.CloseW = w
	}
	return hit
}

func identityFit(text string, _, _, maxWidth int, _ bool) string {
	if maxWidth < 1 {
		return ""
	}
	return text
}

// fitRendered left-fits label text so RenderTab stays within maxWidth.
func fitRendered(text string, index, total, maxWidth int, active, preview bool, fit FitLabel) string {
	if maxWidth < 1 {
		return ""
	}
	if fit == nil {
		fit = identityFit
	}
	close := closeSuffix()
	closeW := lipgloss.Width(close)
	chrome := tabChromeWidth(index, total, active)
	labelW := maxWidth - chrome - closeW
	if labelW < 1 {
		close = ""
		labelW = maxWidth - chrome
	}
	if labelW < 1 {
		labelW = 1
	}
	for labelW >= 1 {
		rendered := styles.RenderTab(fit(text, index, total, labelW, active)+close, index, total, active, preview)
		if w := lipgloss.Width(rendered); w <= maxWidth {
			return rendered
		}
		if labelW == 1 {
			if close != "" {
				close = ""
				labelW = maxWidth - chrome
				if labelW < 1 {
					labelW = 1
				}
				continue
			}
			return ansi.Truncate(rendered, maxWidth, "")
		}
		labelW--
	}
	return ""
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

func paintStrip(rendered []string, widths []int, start, end int, showLeft, showRight bool, width int) Strip {
	var tokens []string
	var hits []Hit
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
		hits = append(hits, tabHit(i, x, rendered[i]))
		x += widths[i]
	}
	if showRight {
		if len(tokens) > 0 {
			tokens = append(tokens, " ")
		}
		tokens = append(tokens, styles.Muted.Render(">"))
	}
	return Strip{Row: padTabRow(strings.Join(tokens, ""), width), Tabs: hits}
}

// LayoutStrip paints labels left to right with overflow markers. Leftover
// width goes to the active tab. Hits describe the tabs actually painted.
func LayoutStrip(labels []Label, active, width int, focused bool, fit FitLabel) Strip {
	if width < 1 {
		return Strip{}
	}
	n := len(labels)
	if n == 0 {
		return Strip{Row: strings.Repeat(" ", width)}
	}
	if active < 0 || active >= n {
		active = 0
	}
	if fit == nil {
		fit = identityFit
	}

	if n == 1 {
		rendered := fitRendered(labels[0].Text, 0, 1, width, tabActive(focused, 0, active), labels[0].Preview, fit)
		return Strip{
			Row:  padTabRow(rendered, width),
			Tabs: []Hit{tabHit(0, 0, rendered)},
		}
	}

	share := (width - (n - 1)) / n
	if share < MinBudget {
		share = MinBudget
	}

	rendered := make([]string, n)
	widths := make([]int, n)
	for i, label := range labels {
		rendered[i] = fitRendered(label.Text, i, n, share, tabActive(focused, i, active), label.Preview, fit)
		widths[i] = lipgloss.Width(rendered[i])
	}

	start, end, showLeft, showRight := VisibleRange(widths, active, width)
	if start > end {
		return Strip{Row: strings.Repeat(" ", width)}
	}

	leftover := width - packedTabsWidth(widths, start, end, showLeft, showRight)
	if leftover > 0 && active >= start && active <= end {
		rendered[active] = fitRendered(labels[active].Text, active, n, widths[active]+leftover, tabActive(focused, active, active), labels[active].Preview, fit)
		widths[active] = lipgloss.Width(rendered[active])
	}

	return paintStrip(rendered, widths, start, end, showLeft, showRight, width)
}
