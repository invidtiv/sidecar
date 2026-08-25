package cli

import (
	"encoding/json"
	"fmt"
	"strings"
)

// The layout report as the CLI reads it back: the same JSON document the host
// built, decoded just enough to draw. --json callers never see this decoding —
// they get the payload bytes verbatim.
type layoutReportView struct {
	Version int    `json:"version"`
	Surface string `json:"surface,omitempty"`
	Root    string `json:"root,omitempty"`
	Grid    *struct {
		Columns []layoutColumnView `json:"columns"`
	} `json:"grid"`
	Caps struct {
		MaxColumns int `json:"maxColumns"`
		MaxRows    int `json:"maxRows"`
		LiveLeaves int `json:"liveLeaves"`
	} `json:"caps"`
}

type layoutColumnView struct {
	Column int              `json:"column"`
	Panes  []layoutPaneView `json:"panes"`
}

type layoutPaneView struct {
	Cell     string   `json:"cell"`
	Kind     string   `json:"kind"`
	Pane     int      `json:"pane"`
	Provider string   `json:"provider,omitempty"`
	Session  string   `json:"session,omitempty"`
	Tabs     []string `json:"tabs,omitempty"`
	Active   int      `json:"active,omitempty"`
}

// owner is the live thing a pane belongs to: a shell's tmux session, a
// resource pane's configured provider instance. Both answer the same question
// the last table column asks, and a pane never has both.
func (c layoutPaneView) owner() string {
	if c.Session != "" {
		return c.Session
	}
	return c.Provider
}

func decodeLayoutReport(raw json.RawMessage) layoutReportView {
	var report layoutReportView
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &report)
	}
	return report
}

// renderLayoutSketch draws the grid as stacked boxes, one box per cell:
//
//	+- primary -----------+ +- file --------------+
//	| terminal            | | README.md           |
//	+---------------------+ +- issue -------------+
//	                        | td-756c34           |
//	                        +---------------------+
//
// Columns are laid onto a character canvas rather than concatenated line by
// line, because a column that ends early still has to hold its horizontal
// place: the shorter column's rows are blank, not absent, or every cell to its
// right slides left and the sketch stops describing the layout. Within a
// column adjacent cells share the border between them, which is what makes a
// stack read as one column.
//
// A tree outside the vocabulary says so instead of pretending.
func renderLayoutSketch(report layoutReportView) string {
	if report.Grid == nil || len(report.Grid.Columns) == 0 {
		return "(this layout does not resolve to grid columns; see the raw tree in --json)\n"
	}
	const (
		colWidth = 23 // outer box width, both borders included
		colGap   = 1  // blank columns between two boxes
	)
	height := 0
	for _, column := range report.Grid.Columns {
		// A stack of n cells is n boxes sharing their touching borders.
		if lines := 2*len(column.Panes) + 1; lines > height {
			height = lines
		}
	}
	width := len(report.Grid.Columns)*(colWidth+colGap) - colGap
	canvas := make([][]rune, height)
	for i := range canvas {
		canvas[i] = []rune(strings.Repeat(" ", width))
	}
	put := func(row, col int, text string) {
		if row < 0 || row >= len(canvas) {
			return
		}
		for i, r := range []rune(text) {
			if col+i >= 0 && col+i < len(canvas[row]) {
				canvas[row][col+i] = r
			}
		}
	}
	for c, column := range report.Grid.Columns {
		x := c * (colWidth + colGap)
		for r, cell := range column.Panes {
			// Cell r's top border doubles as cell r-1's bottom border.
			put(2*r, x, headerBorder(cell.Kind, colWidth))
			put(2*r+1, x, "| "+padCell(paneDetail(cell), colWidth-4)+" |")
			put(2*r+2, x, "+"+strings.Repeat("-", colWidth-2)+"+")
		}
	}
	var out strings.Builder
	for _, line := range canvas {
		out.WriteString(strings.TrimRight(string(line), " "))
		out.WriteByte('\n')
	}
	return out.String()
}

// headerBorder is a cell's top edge with its kind named in it, held to width
// so the box below lines up under it exactly.
func headerBorder(kind string, width int) string {
	label := "- " + kind + " "
	if len(label)+3 > width {
		label = truncateRunes(label, width-3)
	}
	return "+" + label + strings.Repeat("-", width-2-len([]rune(label))) + "+"
}

func paneDetail(cell layoutPaneView) string {
	if cell.Session != "" {
		return cell.Session
	}
	if idx := cell.Active; idx >= 0 && idx < len(cell.Tabs) {
		return cell.Tabs[idx]
	}
	if len(cell.Tabs) > 0 {
		return cell.Tabs[0]
	}
	if cell.Kind == "primary" {
		return "terminal"
	}
	return ""
}

func padCell(s string, width int) string {
	s = truncateRunes(s, width)
	return s + strings.Repeat(" ", width-len([]rune(s)))
}

func truncateRunes(s string, width int) string {
	runes := []rune(s)
	if len(runes) <= width {
		return s
	}
	if width < 2 {
		return string(runes[:width])
	}
	return string(runes[:width-1]) + "…"
}

// renderLayoutTable is the sketch's companion: one line per cell with the
// full target list the sketch truncates.
func renderLayoutTable(report layoutReportView) string {
	var out strings.Builder
	fmt.Fprintf(&out, "caps %dx%d, live leaves ≤ %d\n\n", report.Caps.MaxColumns, report.Caps.MaxRows, report.Caps.LiveLeaves)
	fmt.Fprintf(&out, "%-5s %-9s %-38s %s\n", "CELL", "KIND", "TARGETS/TABS", "SESSION/PROVIDER")
	if report.Grid != nil {
		for _, column := range report.Grid.Columns {
			for _, cell := range column.Panes {
				tabs := cell.Tabs
				labels := make([]string, 0, len(tabs))
				for i, tab := range tabs {
					if i < len(tabs) && i == cell.Active {
						tab = tab + "*"
					}
					labels = append(labels, tab)
				}
				fmt.Fprintf(&out, "%-5s %-9s %-38s %s\n",
					cell.Cell, cell.Kind,
					truncateRunes(strings.Join(labels, ", "), 38),
					truncateRunes(cell.owner(), 28))
			}
		}
	} else {
		out.WriteString("(no grid projection)\n")
	}
	return out.String()
}

func cellOrDash(cell string) string {
	if strings.TrimSpace(cell) == "" {
		return "-"
	}
	return cell
}
