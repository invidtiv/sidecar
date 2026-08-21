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

const (
	kindSeparator  = " | "
	kindFrameOpen  = "["
	kindFrameClose = "]"
)

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
	x := ansi.StringWidth(kindFrameOpen)
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

// kindDisabledSelected is the selected-but-unavailable row: the selected row's
// chrome so the toggle still says which kind is active, in muted text so the
// row still says it cannot be created. It is a function rather than a value so
// it reads the colour at render time and follows a theme change.
func kindDisabledSelected() lipgloss.Style {
	return styles.ButtonHover.Foreground(styles.TextMuted)
}

func kindRowStyle(rowKind, sel Kind, disabled bool, hovered bool) lipgloss.Style {
	if disabled && rowKind == sel {
		// A disabled row that is still the active kind — its Name field and
		// placement row are drawn below it — must read as selected, or the
		// toggle shows nothing selected at all. Selected chrome, muted text.
		return kindDisabledSelected()
	}
	if disabled {
		return styles.Muted
	}
	if rowKind == sel {
		return styles.ButtonFocused
	}
	if hovered {
		return styles.ButtonHover
	}
	return styles.Button
}

func kindButtonStyles(sel Kind, hovered bool) (shellStyle, treeStyle lipgloss.Style) {
	return kindRowStyle(KindShell, sel, false, hovered && sel != KindShell),
		kindRowStyle(KindWorktree, sel, false, hovered && sel != KindWorktree)
}

// kindFrameStyle is the [ ] around the kind row. It uses the same colours as a
// modal input border so "this field is active" is not the same signal as
// "this kind is selected".
func kindFrameStyle(focused, hovered bool) lipgloss.Style {
	s := lipgloss.NewStyle()
	switch {
	case focused:
		return s.Foreground(styles.Primary)
	case hovered:
		return s.Foreground(styles.TextMuted)
	default:
		return s.Foreground(styles.BorderNormal)
	}
}

func renderKindToggle(rows []kindRow, sel Kind, focused, hovered bool, disabledReason func(Kind) string, contentWidth int) string {
	frame := kindFrameStyle(focused, hovered)
	parts := make([]string, 0, len(rows)*2+2)
	parts = append(parts, frame.Render(kindFrameOpen))
	for i, row := range rows {
		disabled := disabledReason != nil && disabledReason(row.Kind) != ""
		style := kindRowStyle(row.Kind, sel, disabled, hovered && row.Kind != sel)
		if i > 0 {
			parts = append(parts, styles.Muted.Render(kindSeparator))
		}
		parts = append(parts, style.Render(" "+row.Label+" "))
	}
	parts = append(parts, frame.Render(kindFrameClose))
	content := lipgloss.JoinHorizontal(lipgloss.Top, parts...)
	if ansi.StringWidth(content) > contentWidth && contentWidth > 0 {
		content = ansi.Truncate(content, contentWidth, "…")
	}
	return content
}

// kindToggle renders the row list. disabledReason answers, per row, why that
// row cannot be created right now; a disabled row is drawn muted whether or not
// it is selected, so the rule is visible before the row is entered.
func kindToggle(id string, rows []kindRow, selected *Kind, onChange func(), disabledReason func(Kind) string) modal.Section {
	return modal.Custom(func(contentWidth int, focusID, hoverID string) modal.RenderedSection {
		sel := KindShell
		if selected != nil {
			sel = *selected
		}
		content := renderKindToggle(rows, sel, focusID == id, hoverID == id, disabledReason, contentWidth)
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
