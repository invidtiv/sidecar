package workspacelist

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/styles"
)

// MarkerTone describes non-semantic row identities and health states. Agent
// activity should use Lane instead; the caller has already resolved that lane
// and this renderer only gives the resolved marker its shared visual treatment.
type MarkerTone string

const (
	MarkerMuted   MarkerTone = "muted"
	MarkerLive    MarkerTone = "live"
	MarkerMain    MarkerTone = "main"
	MarkerWarning MarkerTone = "warning"
	MarkerError   MarkerTone = "error"
)

// RowMarker is a caller-resolved status symbol. Style is reserved for the
// project sidebar's animated working/blocked frames; ordinary callers provide
// Lane or Tone and get the same colour vocabulary everywhere.
type RowMarker struct {
	Icon     string
	Lane     string
	Tone     MarkerTone
	Style    lipgloss.Style
	HasStyle bool
}

// RowField carries both the semantic text used under selection and an optional
// styled form used on an unselected row. This lets project-only badges remain
// project-owned without moving their layout into the plugin.
type RowField struct {
	Text     string
	Rendered string
}

func PlainField(text string) RowField { return RowField{Text: text} }

// RowPresentation is the neutral two-line workspace row contract shared by
// the project and global Workspaces surfaces. Callers resolve all state and
// supply display fields; this package owns layout, marker placement, provider
// treatment, ANSI-safe fitting, and selection.
type RowPresentation struct {
	Marker   RowMarker
	Name     string
	Age      string
	NameMeta []RowField

	BeforeProvider []RowField
	Provider       string
	AfterProvider  []RowField
}

// RenderRow renders one or two physical lines. Narrow rows retain the status
// marker and name first, then add secondary identity only while space remains.
func RenderRow(row RowPresentation, width int, selected, focused bool) []string {
	if width <= 0 {
		return []string{""}
	}
	if width < twoLineWidth {
		line := narrowRow(row, width, selected)
		return []string{finishRow(line, width, selected, focused)}
	}

	line1 := rowLineOne(row, width, selected)
	line2 := "   " + strings.Join(renderRowFields(row, selected), "  ")
	if strings.TrimSpace(ansi.Strip(line2)) == "" {
		line2 = ""
	}
	if selected {
		return strings.Split(ApplySelection(fit(line1, width)+"\n"+fit(line2, width), width, true, focused), "\n")
	}
	return []string{
		styles.ListItemNormal.Width(width).Render(fit(line1, width)),
		styles.ListItemNormal.Width(width).Render(fit(line2, width)),
	}
}

func rowLineOne(row RowPresentation, width int, selected bool) string {
	icon := row.Marker.Icon
	if icon == "" {
		icon = "○"
	}
	if !selected {
		icon = markerStyle(row.Marker).Render(icon)
	}
	prefix := " " + icon + " "
	meta := renderFields(row.NameMeta, selected)
	metaText := ""
	if len(meta) > 0 {
		metaText = strings.Join(meta, "")
	}
	age := row.Age
	reserved := ansi.StringWidth(prefix) + ansi.StringWidth(metaText)
	if age != "" {
		reserved += ansi.StringWidth(age) + 2
	}
	nameWidth := max(1, width-reserved)
	name := row.Name
	if ansi.StringWidth(name) > nameWidth {
		name = ansi.Truncate(name, nameWidth, "…")
	}
	line := prefix + name + metaText
	if age != "" {
		if pad := width - ansi.StringWidth(line) - ansi.StringWidth(age) - 1; pad > 0 {
			line += strings.Repeat(" ", pad) + age
		}
	}
	return fit(line, width)
}

func renderRowFields(row RowPresentation, selected bool) []string {
	fields := renderFields(row.BeforeProvider, selected)
	if row.Provider != "" {
		provider := styles.AgentLabel(row.Provider)
		if provider == "" {
			provider = row.Provider
		}
		if !selected {
			if chip := styles.RenderAgentChip(row.Provider); chip != "" {
				provider = chip
			}
		}
		fields = append(fields, provider)
	}
	return append(fields, renderFields(row.AfterProvider, selected)...)
}

func renderFields(fields []RowField, selected bool) []string {
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		if field.Text == "" {
			continue
		}
		value := field.Text
		if !selected && field.Rendered != "" {
			value = field.Rendered
		}
		out = append(out, value)
	}
	return out
}

func narrowRow(row RowPresentation, width int, selected bool) string {
	icon := row.Marker.Icon
	if icon == "" {
		icon = "○"
	}
	if !selected {
		icon = markerStyle(row.Marker).Render(icon)
	}
	prefix := " " + icon + " "
	secondary := narrowSecondary(row, selected)
	if secondary == "" || width < ansi.StringWidth(prefix)+8 {
		return fit(prefix+row.Name, width)
	}
	// Keep enough of the primary name to identify the row. Secondary identity
	// is deliberately bounded; it may disappear before the marker or name do.
	secondaryWidth := min(ansi.StringWidth(secondary), max(1, width/3))
	secondary = ansi.Truncate(secondary, secondaryWidth, "…")
	separator := " · "
	nameWidth := max(1, width-ansi.StringWidth(prefix)-ansi.StringWidth(separator)-ansi.StringWidth(secondary))
	name := ansi.Truncate(row.Name, nameWidth, "…")
	return fit(prefix+name+separator+secondary, width)
}

func narrowSecondary(row RowPresentation, selected bool) string {
	fields := renderRowFields(row, selected)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func finishRow(line string, width int, selected, focused bool) string {
	line = fit(line, width)
	if selected {
		return selectionStyle(focused).Width(width).Render(line)
	}
	return styles.ListItemNormal.Width(width).Render(line)
}

func markerStyle(marker RowMarker) lipgloss.Style {
	if marker.HasStyle {
		return marker.Style
	}
	if marker.Lane != "" {
		return lipgloss.NewStyle().Foreground(styles.LaneColor(marker.Lane))
	}
	switch marker.Tone {
	case MarkerLive:
		return styles.StatusCompleted
	case MarkerMain:
		return lipgloss.NewStyle().Foreground(styles.Primary)
	case MarkerWarning:
		return styles.StatusModified
	case MarkerError:
		return styles.StatusDeleted
	default:
		return styles.Muted
	}
}
