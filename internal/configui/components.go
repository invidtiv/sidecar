package configui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/styles"
)

// The Configuration surface aligns every page against one grid: labels occupy
// a fixed left column and their controls start at one shared column, so help
// text, completions, and nested pickers can align to the control rather than to
// the pane edge.
const (
	// LabelColumn is the width of the fixed left label column.
	LabelColumn = 24
	// RowIndent is the left inset shared by rows inside a section.
	RowIndent = 2
	// ControlColumn is the column every control starts at, measured from the
	// pane's content origin.
	ControlColumn = RowIndent + LabelColumn
)

// State is the interaction state of a control. Every control renders rest,
// focus, and hover distinctly so keyboard and mouse describe the same surface.
type State struct {
	Focused bool
	Hovered bool
	// Disabled marks a control the current environment cannot offer.
	Disabled bool
}

// Styles are built per call rather than cached in package vars: the theme is
// swapped at runtime (and previewed live), and styles.* is reassigned when it
// is.

func titleStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(styles.Primary).Bold(true)
}

func mutedStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(styles.TextMuted)
}

func bodyStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(styles.TextPrimary)
}

func chipStyle() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(styles.TextPrimary).
		Background(styles.SurfaceRaised).
		Padding(0, 1)
}

func accentChipStyle() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(styles.Primary).
		Background(styles.BgTertiary).
		Padding(0, 1).
		Bold(true)
}

// PaneTitle is the page or pane title at the top of a pane, with the breathing
// room the design brief asks for supplied by the caller's blank line.
func PaneTitle(text string) string { return titleStyle().Render(text) }

// SectionHeader titles a group of controls. It carries its own leading blank
// line so sections separate by whitespace rather than by divider lines.
func SectionHeader(text string) string {
	return "\n" + titleStyle().Render(text)
}

// Body renders ordinary primary text.
func Body(text string) string { return bodyStyle().Render(text) }

// Muted renders quieter secondary text.
func Muted(text string) string { return mutedStyle().Render(text) }

// HelpLine renders muted help aligned to the control it belongs to, not to the
// pane edge.
func HelpLine(text string) string {
	return strings.Repeat(" ", ControlColumn) + mutedStyle().Render(text)
}

// FormRow renders a labelled control: the label in the fixed left column, the
// control starting at the shared column.
func FormRow(label, control string, state State) string {
	labelText := label
	if width := ansi.StringWidth(labelText); width > LabelColumn-1 {
		labelText = ansi.Truncate(labelText, LabelColumn-1, "…")
	}
	labelStyle := bodyStyle()
	switch {
	case state.Disabled:
		labelStyle = mutedStyle()
	case state.Focused:
		labelStyle = lipgloss.NewStyle().Foreground(styles.Primary).Bold(true)
	case state.Hovered:
		labelStyle = lipgloss.NewStyle().Foreground(styles.TextPrimary).Bold(true)
	}
	pad := LabelColumn - ansi.StringWidth(labelText)
	if pad < 1 {
		pad = 1
	}
	return strings.Repeat(" ", RowIndent) + labelStyle.Render(labelText) +
		strings.Repeat(" ", pad) + control
}

// Toggle renders an ON/OFF pill. The pill is the control; FormRow supplies the
// label column around it.
func Toggle(on bool, state State) string {
	label := "OFF"
	style := lipgloss.NewStyle().
		Foreground(styles.TextSecondary).
		Background(styles.SurfaceRaised).
		Padding(0, 1)
	if on {
		label = "ON"
		style = lipgloss.NewStyle().
			Foreground(styles.Success).
			Background(styles.BgTertiary).
			Padding(0, 1).
			Bold(true)
	}
	switch {
	case state.Disabled:
		style = style.Foreground(styles.TextMuted)
	case state.Focused:
		style = style.Background(styles.Primary).Foreground(styles.OnPrimaryColor)
	case state.Hovered:
		style = style.Background(styles.BgTertiary)
	}
	return style.Render(label)
}

// Selector renders a value with a disclosure arrow.
func Selector(value string, state State) string {
	style := chipStyle()
	switch {
	case state.Disabled:
		style = style.Foreground(styles.TextMuted)
	case state.Focused:
		style = accentChipStyle()
	case state.Hovered:
		style = style.Background(styles.BgTertiary)
	}
	return style.Render(value + "  ▾")
}

// Button renders an action pill. primary marks the action the page recommends.
func Button(label string, primary bool, state State) string {
	style := chipStyle()
	if primary {
		style = accentChipStyle()
	}
	switch {
	case state.Disabled:
		style = style.Foreground(styles.TextMuted)
	case state.Focused:
		style = lipgloss.NewStyle().
			Foreground(styles.OnPrimaryColor).
			Background(styles.Primary).
			Padding(0, 1).
			Bold(true)
	case state.Hovered:
		style = style.Background(styles.BgTertiary)
	}
	return style.Render(label)
}

// ButtonRow joins rendered buttons into one indented row.
func ButtonRow(buttons ...string) string {
	present := make([]string, 0, len(buttons))
	for _, button := range buttons {
		if button != "" {
			present = append(present, button)
		}
	}
	if len(present) == 0 {
		return ""
	}
	return strings.Repeat(" ", RowIndent) + strings.Join(present, "  ")
}

// StatusRow renders a diagnostic row. A healthy row is quiet confirmation; an
// actionable one carries a badge and reads as a control.
func StatusRow(ok bool, label, detail, badge string, width int, state State) string {
	marker := lipgloss.NewStyle().Foreground(styles.Success).Render("✓")
	if !ok {
		marker = lipgloss.NewStyle().Foreground(styles.Warning).Render("●")
	}
	labelStyle := bodyStyle()
	switch {
	case state.Focused:
		labelStyle = lipgloss.NewStyle().Foreground(styles.Primary).Bold(true)
	case state.Hovered:
		labelStyle = lipgloss.NewStyle().Foreground(styles.TextPrimary).Bold(true)
	}
	left := strings.Repeat(" ", RowIndent) + marker + " " + labelStyle.Render(label)
	if detail != "" {
		pad := ControlColumn - ansi.StringWidth(left)
		if pad < 2 {
			pad = 2
		}
		left += strings.Repeat(" ", pad) + mutedStyle().Render(detail)
	}
	if badge == "" {
		return left
	}
	rendered := accentChipStyle().Render(badge)
	pad := width - ansi.StringWidth(left) - ansi.StringWidth(rendered)
	if pad < 1 {
		pad = 1
	}
	return left + strings.Repeat(" ", pad) + rendered
}

// ListRow renders a focusable row that fills the pane width, so the whole row
// is legibly the control.
func ListRow(text string, width int, state State) string {
	style := lipgloss.NewStyle().Foreground(styles.TextSecondary)
	prefix := "  "
	switch {
	case state.Focused:
		style = lipgloss.NewStyle().Foreground(styles.Primary).Bold(true)
		prefix = lipgloss.NewStyle().Foreground(styles.Primary).Render("▸ ")
	case state.Hovered:
		style = lipgloss.NewStyle().Foreground(styles.TextPrimary)
	}
	row := prefix + style.Render(text)
	if pad := width - ansi.StringWidth(row); pad > 0 {
		row += strings.Repeat(" ", pad)
	}
	return row
}

// BackBar is the top row of a focused child route: the route's own title on the
// left and the visible parent-return control on the right. Escape does the same
// thing the control does.
func BackBar(title, parent string, width int, state State) string {
	left := titleStyle().Render(title)
	control := BackControl(parent, state)
	pad := width - ansi.StringWidth(left) - ansi.StringWidth(control)
	if pad < 1 {
		pad = 1
	}
	return left + strings.Repeat(" ", pad) + control
}

// BackControl renders the parent-return control on its own, for callers that
// place it outside a bar.
func BackControl(parent string, state State) string {
	label := "←  Back"
	if parent != "" {
		label = "←  Back to " + parent
	}
	return Button(label, false, state)
}
