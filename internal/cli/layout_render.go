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
	Cell    string   `json:"cell"`
	Kind    string   `json:"kind"`
	Pane    int      `json:"pane"`
	Session string   `json:"session,omitempty"`
	Tabs    []string `json:"tabs,omitempty"`
	Active  int      `json:"active,omitempty"`
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
//	+-- primary --------+-- file ------------+
//	| terminal          | README.md          |
//	+-------------------+-- issue -----------+
//	                    | td-756c34          |
//	                    +--------------------+
//
// A tree outside the vocabulary says so instead of pretending.
func renderLayoutSketch(report layoutReportView) string {
	if report.Grid == nil || len(report.Grid.Columns) == 0 {
		return "(this layout does not resolve to grid columns; see the raw tree in --json)\n"
	}
	const colWidth = 24
	maxRows := 0
	for _, column := range report.Grid.Columns {
		if len(column.Panes) > maxRows {
			maxRows = len(column.Panes)
		}
	}
	var out strings.Builder
	for row := 1; row <= maxRows; row++ {
		top, mid, bottom := strings.Builder{}, strings.Builder{}, strings.Builder{}
		for _, column := range report.Grid.Columns {
			idx := row - 1
			if idx >= len(column.Panes) {
				continue
			}
			cell := column.Panes[idx]
			label := cell.Kind
			detail := paneDetail(cell)
			mid.WriteString("| " + padCell(detail, colWidth-3))
			if idx == 0 {
				top.WriteString("+- " + padCell(label, colWidth-4) + "-+")
				bottom.WriteString("+-" + strings.Repeat("-", colWidth-2) + "+")
			} else {
				top.WriteString("- " + padCell(label, colWidth-4) + "-+")
				bottom.WriteString("+" + strings.Repeat("-", colWidth-2) + "+")
			}
			mid.WriteString("|\n")
		}
		out.WriteString(top.String() + "\n")
		out.WriteString(mid.String())
		out.WriteString(bottom.String() + "\n")
	}
	return out.String()
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
	fmt.Fprintf(&out, "%-5s %-9s %-38s %s\n", "CELL", "KIND", "TARGETS/TABS", "SESSION")
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
					truncateRunes(cell.Session, 28))
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
