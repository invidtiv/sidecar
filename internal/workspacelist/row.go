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

// Kind is a presentation identity for a row. These strings match the catalog
// kinds the caller already resolved; this package never imports inventory.
const (
	KindWorktree = "worktree"
	KindShell    = "shell"
)

// KindGlyph is the Agents-board pair: worktree ⑂, shell ❯. Unknown or empty
// kinds render nothing so a project-plugin row can omit them.
func KindGlyph(kind string) string {
	switch kind {
	case KindShell:
		return "❯"
	case KindWorktree:
		return "⑂"
	default:
		return ""
	}
}

// RowPresentation is the neutral two-line workspace row contract shared by
// the project and global Workspaces surfaces. Callers resolve all state and
// supply display fields; this package owns layout, marker placement, provider
// treatment, ANSI-safe fitting, and selection.
//
// Kind is a presentation field, not a status marker. Marker stays the
// outdented gutter icon (working/live/idle/ambiguous/main).
type RowPresentation struct {
	Marker     RowMarker
	Kind       string
	Name       string
	NamePrefix RowField
	Age        string
	NameMeta   []RowField

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
	namePrefix := renderField(row.NamePrefix, selected)
	meta := renderFields(row.NameMeta, selected)
	metaText := ""
	if len(meta) > 0 {
		metaText = strings.Join(meta, "")
	}
	age := row.Age
	reserved := ansi.StringWidth(prefix) + ansi.StringWidth(namePrefix) + ansi.StringWidth(metaText)
	if age != "" {
		reserved += ansi.StringWidth(age) + 2
	}
	nameWidth := max(1, width-reserved)
	name := row.Name
	if ansi.StringWidth(name) > nameWidth {
		name = ansi.Truncate(name, nameWidth, "…")
	}
	line := prefix + namePrefix + name + metaText
	if age != "" {
		if pad := width - ansi.StringWidth(line) - ansi.StringWidth(age) - 1; pad > 0 {
			line += strings.Repeat(" ", pad) + age
		}
	}
	return fit(line, width)
}

func renderRowFields(row RowPresentation, selected bool) []string {
	fields := make([]string, 0, 1+len(row.BeforeProvider)+1+len(row.AfterProvider))
	if glyph := KindGlyph(row.Kind); glyph != "" {
		if !selected {
			glyph = styles.Muted.Render(glyph)
		}
		fields = append(fields, glyph)
	}
	fields = append(fields, renderFields(row.BeforeProvider, selected)...)
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
		if value := renderField(field, selected); value != "" {
			out = append(out, value)
		}
	}
	return out
}

func renderField(field RowField, selected bool) string {
	if field.Text == "" {
		return ""
	}
	if !selected && field.Rendered != "" {
		return field.Rendered
	}
	return field.Text
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
