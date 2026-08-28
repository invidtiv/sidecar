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
	Pinned         bool
}

// RenderRow renders one or two physical lines. Line one carries the row's
// whole identity — kind glyph, project, name, and its age — so a reader can
// scan shells and worktrees apart without dropping to the second line. Line
// two is agent detail alone and collapses to nothing when there is none.
// Narrow rows retain the status marker and name first, then add secondary
// identity only while space remains.
func RenderRow(row RowPresentation, width int, selected, focused bool) []string {
	if width <= 0 {
		return []string{""}
	}
	if width < twoLineWidth {
		icon, rest := narrowRowParts(row, width, selected)
		return []string{finishRowLine(row.Marker, icon, rest, width, selected, focused)}
	}

	icon, rest := rowLineOneParts(row, width, selected)
	line2 := strings.Repeat(" ", rowIndent(row)) + strings.Join(renderRowFields(row, selected), "  ")
	if strings.TrimSpace(ansi.Strip(line2)) == "" {
		// A row with nothing to say on line two is one row, not two. Sidebar
		// body height is the scarcest resource on this surface: a plain shell
		// in the global list carries neither provider nor branch nor task, and
		// spending a blank row on each of them is what pushes a whole section
		// off the bottom of an ordinary pane.
		return []string{finishRowLine(row.Marker, icon, rest, width, selected, focused)}
	}
	if selected {
		return []string{
			finishRowLine(row.Marker, icon, rest, width, true, focused),
			selectionStyle(focused).Width(width).Render(fit(line2, width)),
		}
	}
	return []string{
		finishRowLine(row.Marker, icon, rest, width, false, focused),
		paintNormalLine(line2, width),
	}
}

// rowPrefixParts is the gutter: the status marker, then the kind glyph that
// tells a shell from a worktree. Both lines share its width so agent detail
// hangs under the name rather than under the marker. The icon is returned
// unstyled so selection can paint it as a sibling of the name.
func rowPrefixParts(row RowPresentation, selected bool) (icon, afterIcon string) {
	icon = row.Marker.Icon
	if icon == "" {
		icon = "○"
	}
	afterIcon = " "
	if glyph := KindGlyph(row.Kind); glyph != "" {
		if !selected {
			glyph = styles.Muted.Render(glyph)
		}
		afterIcon += glyph + " "
	}
	return icon, afterIcon
}

func rowIndent(row RowPresentation) int {
	if KindGlyph(row.Kind) != "" {
		return 5
	}
	return 3
}

func rowLineOneParts(row RowPresentation, width int, selected bool) (icon, rest string) {
	icon, afterIcon := rowPrefixParts(row, selected)
	namePrefix := renderField(row.NamePrefix, selected)
	meta := renderFields(row.NameMeta, selected)
	metaText := ""
	if len(meta) > 0 {
		metaText = strings.Join(meta, "")
	}
	age := row.Age
	prefixWidth := 1 + ansi.StringWidth(icon) + ansi.StringWidth(afterIcon)
	reserved := prefixWidth + ansi.StringWidth(namePrefix) + ansi.StringWidth(metaText)
	if age != "" {
		reserved += ansi.StringWidth(age) + 2
	}
	nameWidth := max(1, width-reserved)
	name := row.Name
	if ansi.StringWidth(name) > nameWidth {
		name = ansi.Truncate(name, nameWidth, "…")
	}
	rest = afterIcon + namePrefix + name + metaText
	if age != "" {
		if pad := width - prefixWidth - ansi.StringWidth(namePrefix) - ansi.StringWidth(name) - ansi.StringWidth(metaText) - ansi.StringWidth(age) - 1; pad > 0 {
			rest += strings.Repeat(" ", pad) + age
		}
	}
	return icon, fit(rest, max(0, width-1-ansi.StringWidth(icon)))
}

func renderRowFields(row RowPresentation, selected bool) []string {
	// The kind glyph belongs to line one; this line is agent identity only.
	fields := make([]string, 0, 1+len(row.BeforeProvider)+1+len(row.AfterProvider))
	if row.Pinned {
		mark := "*"
		if !selected {
			mark = styles.Muted.Render(mark)
		}
		fields = append(fields, mark)
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

func narrowRowParts(row RowPresentation, width int, selected bool) (icon, rest string) {
	icon, afterIcon := rowPrefixParts(row, selected)
	prefixWidth := 1 + ansi.StringWidth(icon) + ansi.StringWidth(afterIcon)
	remain := max(0, width-1-ansi.StringWidth(icon))
	secondary := narrowSecondary(row, selected)
	if secondary == "" || width < prefixWidth+8 {
		return icon, fit(afterIcon+row.Name, remain)
	}
	// Keep enough of the primary name to identify the row. Secondary identity
	// is deliberately bounded; it may disappear before the marker or name do.
	secondaryWidth := min(ansi.StringWidth(secondary), max(1, width/3))
	secondary = ansi.Truncate(secondary, secondaryWidth, "…")
	separator := " · "
	nameWidth := max(1, width-prefixWidth-ansi.StringWidth(separator)-ansi.StringWidth(secondary))
	name := ansi.Truncate(row.Name, nameWidth, "…")
	return icon, fit(afterIcon+name+separator+secondary, remain)
}

func narrowSecondary(row RowPresentation, selected bool) string {
	fields := renderRowFields(row, selected)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func finishRowLine(marker RowMarker, icon, rest string, width int, selected, focused bool) string {
	if selected {
		return paintSelectedLine(marker, icon, rest, width, focused)
	}
	return paintNormalLine(" "+markerStyle(marker).Render(icon)+rest, width)
}

// paintNormalLine keeps an unselected row on the pane's existing canvas while
// applying the ordinary text foreground around nested marker and provider
// styles. Those nested styles end with an ANSI reset, so repaint each segment
// to keep the foreground consistent without introducing a row-level fill.
func paintNormalLine(line string, width int) string {
	line = fit(line, width)
	style := styles.ListItemNormal
	var painted strings.Builder
	for line != "" {
		at, resetWidth := firstANSIReset(line)
		if at < 0 {
			painted.WriteString(style.Render(line))
			break
		}
		if at > 0 {
			painted.WriteString(style.Render(line[:at]))
		}
		line = line[at+resetWidth:]
	}
	return painted.String()
}

func firstANSIReset(line string) (at, width int) {
	short := strings.Index(line, "\x1b[m")
	long := strings.Index(line, "\x1b[0m")
	switch {
	case short < 0:
		return long, len("\x1b[0m")
	case long < 0 || short < long:
		return short, len("\x1b[m")
	default:
		return long, len("\x1b[0m")
	}
}

// paintSelectedLine applies the selection fill without wrapping a pre-rendered
// marker. lipgloss.Render ends with a full reset, so a parent wrap around the
// icon punches a hole in the background for the name that follows. The marker
// keeps its own colour (live status should not flatten to the cursor) and
// each span carries the selection background itself.
func paintSelectedLine(marker RowMarker, icon, rest string, width int, focused bool) string {
	sel := selectionStyle(focused)
	styledIcon := markerStyle(marker).Background(sel.GetBackground()).Render(icon)
	rest = fit(rest, max(0, width-1-ansi.StringWidth(icon)))
	return sel.Render(" ") + styledIcon + sel.Render(rest)
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
