package workspacelist

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/ui"
)

// SidebarAction is an optional action painted in a header. Omitting it paints
// and registers nothing, which lets read-only consumers use the same sidebar
// without inheriting project workspace mutations.
type SidebarAction struct {
	ID      string
	Label   string
	Hovered bool
}

// SidebarRow is one caller-owned record projected into the shared list. Data
// is returned unchanged in the row's Region so consumers never decode display
// text or a volatile visible index to recover their record.
type SidebarRow struct {
	ID     string
	Data   any
	Render func(width int, selected, focused bool) []string
}

// SidebarSection is a headed run of rows. The optional action is commonly the
// project's create-shell/create-worktree affordance; global sections omit it.
//
// Title is the bare name ("Shells", "Needs Attention") and Count is rendered
// beside it. They are kept apart rather than pre-joined so a narrow heading can
// drop the count and still name its rows. An empty Title means an unheaded run.
type SidebarSection struct {
	Title  string
	Count  int
	Action *SidebarAction
	Rows   []SidebarRow
}

// heading is the section's widest form: name and count together.
func (s SidebarSection) heading() string { return SectionTitle(s.Title, s.Count) }

// SidebarOptions contains only resolved presentation state. Collection,
// selection side effects, preview loading and mutations stay with the caller.
type SidebarOptions struct {
	Width, Height int
	Title         string
	Focused       bool
	SelectedID    string
	ScrollOffset  int
	HeaderAction  *SidebarAction
	HeaderMeta    *SidebarAction
	PrefixLines   []string
	FilterLine    string
	FilterActive  bool
	Sections      []SidebarSection
	EmptyLines    []string
	FooterLines   []string
}

// SidebarRendered is the exact view, geometry and viewport produced by one
// layout pass. Regions use content-local coordinates; callers add their panel
// border/padding origin once when registering them.
type SidebarRendered struct {
	View         string
	Regions      []Region
	ScrollOffset int
	VisibleRows  int
}

const (
	RegionHeaderAction  RegionKind = "workspacelist-header-action"
	RegionSectionAction RegionKind = "workspacelist-section-action"
)

type sidebarFlatRow struct {
	section int
	row     SidebarRow
}

// RenderSidebar is the single row/section/viewport/scrollbar/hit-region
// renderer used by project and global Workspaces.
func RenderSidebar(opts SidebarOptions) SidebarRendered {
	width, height := opts.Width, opts.Height
	if width < 1 || height < 1 {
		return SidebarRendered{}
	}
	title := opts.Title
	if title == "" {
		title = "Workspaces"
	}
	lines := make([]string, 0, height)
	regions := make([]Region, 0)
	header, placed := sidebarHeader(title, opts.HeaderMeta, opts.HeaderAction, width)
	lines = append(lines, fit(header, width))
	for _, control := range placed {
		regions = append(regions, Region{Kind: control.kind, ID: control.id, X: control.x, Y: 0, W: control.w, H: 1})
	}
	for _, line := range opts.PrefixLines {
		lines = append(lines, fit(line, width))
	}
	if opts.FilterActive {
		y := len(lines)
		lines = append(lines, fit(opts.FilterLine, width))
		regions = append(regions, Region{Kind: RegionFilter, X: 0, Y: y, W: width, H: 1})
	}

	footerRows := min(len(opts.FooterLines), max(0, height-len(lines)))
	bodyHeight := max(0, height-len(lines)-footerRows)
	flat := make([]sidebarFlatRow, 0)
	for sectionIndex, section := range opts.Sections {
		for _, row := range section.Rows {
			flat = append(flat, sidebarFlatRow{section: sectionIndex, row: row})
		}
	}

	scroll := min(max(opts.ScrollOffset, 0), max(0, len(flat)-1))
	selected := -1
	for i := range flat {
		if flat[i].row.ID == opts.SelectedID {
			selected = i
			break
		}
	}
	if selected >= 0 && selected < scroll {
		scroll = selected
	}
	for selected >= 0 {
		end := sidebarVisibleEnd(flat, opts.Sections, scroll, bodyHeight, width, opts.SelectedID, opts.Focused)
		if selected < end || scroll >= selected {
			break
		}
		scroll++
	}

	visibleEnd := sidebarVisibleEnd(flat, opts.Sections, scroll, bodyHeight, width, opts.SelectedID, opts.Focused)
	rowWidth := max(1, width-1)
	y := len(lines)
	bodyStart := y
	lastSection := -1
	visibleRows := 0
	for i := scroll; i < visibleEnd; i++ {
		entry := flat[i]
		section := opts.Sections[entry.section]
		if entry.section != lastSection && section.Title != "" {
			// Sections are separated by one blank line; the first heading in view
			// sits flush against the chrome above it, so a scrolled list does not
			// spend a row on a separator with nothing before it.
			if y > bodyStart {
				lines = append(lines, "")
				y++
			}
			heading, x, w := sidebarSectionHeader(section, rowWidth)
			lines = append(lines, fit(heading, rowWidth))
			if section.Action != nil && w > 0 {
				regions = append(regions, Region{Kind: RegionSectionAction, ID: section.Action.ID, X: x, Y: y, W: w, H: 1, SectionHeader: true})
			}
			y++
			lastSection = entry.section
		}
		rowLines := sidebarRowLines(entry.row.Render(rowWidth, entry.row.ID == opts.SelectedID, opts.Focused))
		if len(rowLines) == 0 {
			rowLines = []string{""}
		}
		for j := range rowLines {
			rowLines[j] = fit(rowLines[j], rowWidth)
		}
		lines = append(lines, rowLines...)
		regions = append(regions, Region{Kind: RegionRow, ID: entry.row.ID, X: 0, Y: y, W: rowWidth, H: len(rowLines), VisibleIndex: i, Data: entry.row.Data})
		y += len(rowLines)
		visibleRows++
	}
	if len(flat) == 0 {
		for _, line := range opts.EmptyLines {
			if len(lines) >= height-footerRows {
				break
			}
			lines = append(lines, fit(line, rowWidth))
		}
	}

	// The scrollbar occupies the final content column and spans the actual body
	// rows, including section headings. This keeps row targets off its column.
	chromeRows := height - footerRows - bodyHeight
	renderedBodyRows := max(0, len(lines)-chromeRows)
	if renderedBodyRows > 0 {
		body := strings.Join(lines[chromeRows:], "\n")
		scrollbar := ui.RenderScrollbar(ui.ScrollbarParams{TotalItems: len(flat), ScrollOffset: scroll, VisibleItems: max(1, visibleRows), TrackHeight: renderedBodyRows})
		joined := lipgloss.JoinHorizontal(lipgloss.Top, body, scrollbar)
		lines = append(lines[:chromeRows], strings.Split(joined, "\n")...)
	}
	for len(lines) < height-footerRows {
		lines = append(lines, strings.Repeat(" ", width))
	}
	for _, line := range opts.FooterLines[:footerRows] {
		lines = append(lines, fit(line, width))
	}
	if len(lines) > height {
		lines = lines[:height]
	}
	return SidebarRendered{View: strings.Join(lines, "\n"), Regions: regions, ScrollOffset: scroll, VisibleRows: visibleRows}
}

func sidebarVisibleEnd(flat []sidebarFlatRow, sections []SidebarSection, scroll, height, width int, selectedID string, focused bool) int {
	if height <= 0 || scroll >= len(flat) {
		return scroll
	}
	remaining, end, lastSection := height, scroll, -1
	for end < len(flat) {
		entry := flat[end]
		section := sections[entry.section]
		need := 0
		if entry.section != lastSection && section.Title != "" {
			need++
			if remaining < height {
				need++ // the blank separator RenderSidebar draws above the heading
			}
		}
		rowLines := sidebarRowLines(entry.row.Render(max(1, width-1), entry.row.ID == selectedID, focused))
		need += max(1, len(rowLines))
		if need > remaining {
			break
		}
		remaining -= need
		lastSection = entry.section
		end++
	}
	return end
}

func sidebarRowLines(rendered []string) []string {
	var lines []string
	for _, block := range rendered {
		lines = append(lines, strings.Split(block, "\n")...)
	}
	return lines
}

// renderControl gives every sidebar control one style: a flat pill at rest and
// an accent pill on hover. Project's "New" and global's view control sit in the
// same place and do the same kind of job, so they may not read as two different
// species of thing — one a button, the other a muted caption that gives no clue
// it can be pressed.
func renderControl(action *SidebarAction) string {
	return renderControlLabel(action, action.Label)
}

// renderControlLabel paints a control with a caller-chosen label, so a
// degradation step can shorten the text without inventing a second style.
func renderControlLabel(action *SidebarAction, label string) string {
	style := styles.Button
	if action.Hovered {
		style = styles.ButtonHover
	}
	return styles.RenderPillWithStyle(label, style, nil)
}

// placedControl is one header control that survived layout, with the geometry
// its hit region needs.
type placedControl struct {
	kind RegionKind
	id   string
	x, w int
}

// sidebarHeader lays out the panel title and its right-hand controls: the sort
// pill, then the create button, in that reading order.
//
// Chrome degrades in a defined order rather than clipping. A control that
// cannot be drawn whole is dropped entirely, and its hit region with it,
// because a control clipped to "Activi…" — or to a bare "…" — is a target whose
// meaning a reader cannot recover but whose click still fires. Losing a control
// at 18 columns costs the user a mouse affordance they still have a key for;
// keeping a mystery button costs them a wrong action.
//
// The order is create last to go. When both cannot fit, the sort pill sheds its
// word and keeps its glyph — the list still says it is ordered by something and
// the control is still there to press — and only then disappears. Create is an
// action with no substitute in the header; the sort's label has one, because
// the section headings underneath already name the grouping.
func sidebarHeader(title string, sort, create *SidebarAction, width int) (string, []placedControl) {
	plain := styles.Title.Render(title)
	titleWidth := ansi.StringWidth(title)

	type candidate struct {
		kind  RegionKind
		id    string
		label string
	}
	// Widest form first, then each fallback, most complete to least.
	for _, attempt := range headerAttempts(sort, create) {
		controls := make([]candidate, 0, 2)
		for _, c := range attempt {
			if c.action != nil && c.label != "" {
				controls = append(controls, candidate{kind: c.kind, id: c.action.ID, label: renderControlLabel(c.action, c.label)})
			}
		}
		if len(controls) == 0 {
			continue
		}
		total := 0
		for i, c := range controls {
			total += ansi.StringWidth(c.label)
			if i > 0 {
				total++ // one column between adjacent controls
			}
		}
		if titleWidth+1+total > width {
			continue
		}
		x := width - total
		line := plain + strings.Repeat(" ", x-titleWidth)
		placed := make([]placedControl, 0, len(controls))
		for i, c := range controls {
			if i > 0 {
				line += " "
				x++
			}
			w := ansi.StringWidth(c.label)
			line += c.label
			placed = append(placed, placedControl{kind: c.kind, id: c.id, x: x, w: w})
			x += w
		}
		return line, placed
	}
	return plain, nil
}

type headerCandidate struct {
	kind   RegionKind
	action *SidebarAction
	label  string
}

// headerAttempts is the fixed degradation ladder, widest first.
func headerAttempts(sort, create *SidebarAction) [][]headerCandidate {
	sortFull, sortGlyph := "", ""
	if sort != nil {
		sortFull = sort.Label
		// The glyph-only form is the label's first cell when the caller built it
		// with SortPillLabel; a caller that passed a bare word keeps its word.
		if _, rest, ok := strings.Cut(sort.Label, " "); ok && rest != "" {
			sortGlyph = SortGlyph
		} else {
			sortGlyph = sort.Label
		}
	}
	createLabel := ""
	if create != nil {
		createLabel = create.Label
	}
	return [][]headerCandidate{
		{{RegionSort, sort, sortFull}, {RegionHeaderAction, create, createLabel}},
		{{RegionSort, sort, sortGlyph}, {RegionHeaderAction, create, createLabel}},
		{{RegionHeaderAction, create, createLabel}},
		{{RegionSort, sort, sortFull}},
		{{RegionSort, sort, sortGlyph}},
	}
}

// sidebarSectionHeader lays out one section heading and its optional action.
//
// The degradation order is deliberate: the action goes first, then the count,
// then the name truncates. A heading's job is naming what the rows beneath it
// are, and the panel header already offers the same create action the section
// "+" does — so when the two compete for a narrow row, the words win.
func sidebarSectionHeader(section SidebarSection, width int) (string, int, int) {
	full := section.heading()
	if section.Action != nil && section.Action.Label != "" {
		button := renderControl(section.Action)
		w := ansi.StringWidth(button)
		if ansi.StringWidth(full)+1+w <= width {
			x := width - w
			return styles.Muted.Render(full) + strings.Repeat(" ", x-ansi.StringWidth(full)) + button, x, w
		}
	}
	if ansi.StringWidth(full) > width {
		full = section.Title
	}
	return styles.Muted.Render(full), 0, 0
}

// MoveIndex applies the shared clamped selection semantics used by keyboard
// and wheel navigation in both workspace sidebars.
func MoveIndex(index, delta, count int) int {
	if count <= 0 {
		return -1
	}
	return min(max(index+delta, 0), count-1)
}

// ResizePercent applies the shared percentage delta and bounds used by both
// workspace dividers.
func ResizePercent(start, deltaColumns, viewportWidth int) int {
	if viewportWidth <= 0 {
		return min(max(start, 10), 60)
	}
	return min(max(start+deltaColumns*100/viewportWidth, 10), 60)
}

// ApplySelection gives both consumers the same focused and unfocused cursor
// treatment while leaving their row text and icons caller-owned. Do not wrap
// a pre-rendered marker: lipgloss.Render resets all attributes, and the
// name after the icon will lose the fill. Use RenderRow, which paints the
// marker and the name as sibling spans.
func ApplySelection(content string, width int, selected, focused bool) string {
	if !selected {
		return content
	}
	return selectionStyle(focused).Width(width).Render(content)
}
