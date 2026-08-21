package workspacecreate

import (
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/modal"
	"github.com/marcus/sidecar/internal/styles"
)

// kindRow is one row of the create modal's kind list. The list is a table so a
// later pane kind (File, Git diff, td issue, Note) is an entry here rather than
// new modal code.
type kindRow struct {
	Kind  Kind
	Label string
	// HostScoped rows are offered only by a host that can place them — a
	// terminal split needs a pane tree, which the global browser's preview
	// tiles do not have.
	HostScoped bool
}

// kindCatalog is every row the modal knows, in list order.
var kindCatalog = []kindRow{
	{Kind: KindShell, Label: "Shell"},
	{Kind: KindWorktree, Label: "Worktree"},
	{Kind: KindTerminalSplit, Label: "Terminal split", HostScoped: true},
}

const kindSeparator = " | "

// kindRowsFor is the catalog a host offers.
func kindRowsFor(hostScoped bool) []kindRow {
	rows := make([]kindRow, 0, len(kindCatalog))
	for _, row := range kindCatalog {
		if row.HostScoped && !hostScoped {
			continue
		}
		rows = append(rows, row)
	}
	return rows
}

func kindLabel(rows []kindRow, kind Kind) string {
	for _, row := range rows {
		if row.Kind == kind {
			return row.Label
		}
	}
	return ""
}

func kindIndex(rows []kindRow, kind Kind) int {
	for i, row := range rows {
		if row.Kind == kind {
			return i
		}
	}
	return 0
}

// kindSpans are each row's [start, end) columns inside the rendered toggle, so
// a click lands on the row it is over rather than on a proportional guess. A
// separator belongs to the row on its left, so no click between two rows misses
// both.
func kindSpans(rows []kindRow) [][2]int {
	spans := make([][2]int, 0, len(rows))
	x := 0
	sep := ansi.StringWidth(kindSeparator)
	for i, row := range rows {
		w := ansi.StringWidth(" " + row.Label + " ")
		end := x + w
		if i < len(rows)-1 {
			end += sep
		}
		spans = append(spans, [2]int{x, end})
		x = end
	}
	return spans
}

// kindFromClickX maps a click on the kind toggle to the row under it. Clicks in
// a separator, or past the last row in a region wider than the toggle, keep the
// nearest row rather than falling through to the first.
func kindFromClickX(rows []kindRow, current Kind, x, regionX, regionW int) Kind {
	if len(rows) == 0 || regionW <= 0 {
		return current
	}
	offset := x - regionX
	if offset < 0 {
		return rows[0].Kind
	}
	spans := kindSpans(rows)
	for i, span := range spans {
		if offset < span[1] {
			return rows[i].Kind
		}
	}
	return rows[len(rows)-1].Kind
}

// KindFromClickX maps a click on the two-row kind toggle to Shell (left) or
// Worktree (right). It is the host-independent form kept for callers without a
// form in hand; hosts should use Form.SetKindFromClickX, which knows which rows
// the form actually offers.
func KindFromClickX(x, regionX, regionW int) Kind {
	return kindFromClickX(kindRowsFor(false), KindShell, x, regionX, regionW)
}

func kindToggle(id string, rows []kindRow, selected *Kind, onChange func()) modal.Section {
	return modal.Custom(func(contentWidth int, focusID, hoverID string) modal.RenderedSection {
		sel := KindShell
		if selected != nil {
			sel = *selected
		}
		focused := focusID == id
		parts := make([]string, 0, len(rows)*2)
		for i, row := range rows {
			style := styles.Button
			if row.Kind == sel {
				style = styles.ButtonHover
				if focused {
					style = styles.ButtonFocused
				}
			}
			if i > 0 {
				parts = append(parts, styles.Muted.Render(kindSeparator))
			}
			parts = append(parts, style.Render(" "+row.Label+" "))
		}
		content := lipgloss.JoinHorizontal(lipgloss.Top, parts...)
		if ansi.StringWidth(content) > contentWidth && contentWidth > 0 {
			content = ansi.Truncate(content, contentWidth, "…")
		}
		return modal.RenderedSection{
			Content: content,
			Focusables: []modal.FocusableInfo{{
				ID: id, OffsetX: 0, OffsetY: 0,
				Width:  ansi.StringWidth(content),
				Height: 1,
			}},
		}
	}, func(msg tea.Msg, focusID string) (string, tea.Cmd) {
		if focusID != id || selected == nil || len(rows) == 0 {
			return "", nil
		}
		key, ok := msg.(tea.KeyPressMsg)
		if !ok {
			return "", nil
		}
		prev := *selected
		idx := kindIndex(rows, prev)
		switch key.String() {
		case "left", "h", "up", "k":
			if idx > 0 {
				idx--
			}
		case "right", "l", "down", "j":
			if idx < len(rows)-1 {
				idx++
			}
		}
		*selected = rows[idx].Kind
		if *selected != prev && onChange != nil {
			onChange()
		}
		return "", nil
	})
}
