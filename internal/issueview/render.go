package issueview

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/markdown"
	"github.com/marcus/sidecar/internal/styles"
)

const (
	rowText rowKind = iota
	rowParent
	rowChild
)

type rowKind int

type row struct {
	kind     rowKind
	text     string
	childIdx int
	cursor   int // nav index this row belongs to, or -1
}

// Heading is the issue's headline: "<id>: <title>". Hosts use this for
// chrome (pane chips, modal titles) so the card can draw the id separately.
func Heading(d *Data) string {
	if d == nil {
		return ""
	}
	title := d.ID
	if d.Title != "" {
		title += ": " + d.Title
	}
	return title
}

// StatusLine joins the issue's status, type, priority, and points.
// Kept for hosts that still want a single meta row.
func StatusLine(d *Data) string {
	if d == nil {
		return ""
	}
	var parts []string
	if d.Status != "" {
		parts = append(parts, "["+d.Status+"]")
	}
	if d.Type != "" {
		parts = append(parts, d.Type)
	}
	if d.Priority != "" {
		parts = append(parts, d.Priority)
	}
	if d.Points > 0 {
		parts = append(parts, fmt.Sprintf("%dp", d.Points))
	}
	return strings.Join(parts, "  ")
}

// ParentLine names the issue's parent.
func ParentLine(d *Data) string {
	if d == nil || d.ParentID == "" {
		return ""
	}
	if d.Parent != nil && d.Parent.Title != "" {
		return "Parent: " + d.Parent.ID + "  " + d.Parent.Title
	}
	return "Parent: " + d.ParentID
}

// LabelsLine lists the issue's labels.
func LabelsLine(d *Data) string {
	if d == nil || len(d.Labels) == 0 {
		return ""
	}
	return "Labels: " + strings.Join(d.Labels, ", ")
}

// Description renders the issue body as markdown wrapped to width, falling back
// to the raw text when no renderer is available. A nil renderer builds the
// default one.
func Description(renderer *markdown.Renderer, d *Data, width int) string {
	if d == nil || d.Description == "" {
		return ""
	}
	if renderer == nil {
		var err error
		if renderer, err = markdown.NewRenderer(); err != nil {
			return d.Description
		}
	}
	return strings.Join(renderer.RenderContent(d.Description, width), "\n")
}

func (m *Model) buildRows() []row {
	if m.data == nil {
		return nil
	}
	d := m.data
	width := m.contentWidth()
	var rows []row
	add := func(text string) {
		if text != "" {
			rows = append(rows, row{kind: rowText, text: text, cursor: -1})
		}
	}
	blank := func() { rows = append(rows, row{kind: rowText, text: "", cursor: -1}) }

	add(m.statusHeader(d, width))
	for _, line := range wrapPlain(d.Title, width) {
		add(styles.Title.Render(line))
	}
	add(m.metaLine(d))
	if line := labelsStyled(d); line != "" {
		add(line)
	}

	nav := m.navItems()
	parentIdx := -1
	childBase := 0
	if len(nav) > 0 && nav[0].kind == navParent {
		parentIdx = 0
		childBase = 1
	}

	if d.Parent != nil || d.ParentID != "" {
		blank()
		add(sectionRule("PARENT", "", width))
		rows = append(rows, row{
			kind:   rowParent,
			text:   parentRowText(d, width),
			cursor: parentIdx,
		})
	}

	if desc := Description(m.renderer, d, width); desc != "" {
		blank()
		descLines := strings.Split(desc, "\n")
		add(sectionRule("DESCRIPTION", fmt.Sprintf("%d", len(descLines)), width))
		for _, line := range descLines {
			add(line)
		}
	}

	if acc := renderAcceptance(m.renderer, d, width); acc != "" {
		blank()
		accLines := strings.Split(acc, "\n")
		add(sectionRule("ACCEPTANCE", fmt.Sprintf("%d", len(accLines)), width))
		for _, line := range accLines {
			add(line)
		}
	}

	if len(d.Children) > 0 {
		blank()
		add(sectionRule("SUBTASKS", childCount(d.Children), width))
		for i, child := range d.Children {
			rows = append(rows, row{
				kind:     rowChild,
				text:     childRowText(child, width),
				childIdx: i,
				cursor:   childBase + i,
			})
		}
	}

	if logs := recentLogs(d.Logs, 8); len(logs) > 0 {
		blank()
		add(sectionRule("RECENT LOGS", fmt.Sprintf("%d", len(d.Logs)), width))
		for _, log := range logs {
			add(logRowText(log, width))
		}
	}

	if hints := m.actionHints(width); hints != "" {
		blank()
		add(sectionRule("ACTIONS", "", width))
		add(hints)
	}
	return rows
}

func (m *Model) statusHeader(d *Data, width int) string {
	left := ""
	if d.Status != "" {
		left = statusStyle(d.Status).Render(StatusLabel(d.Status))
	}
	right := styles.Muted.Render(d.ID)
	return ruleBetween(left, right, width)
}

func (m *Model) metaLine(d *Data) string {
	var parts []string
	if d.Type != "" {
		parts = append(parts, typeStyle(d.Type).Render(typeIcon(d.Type))+" "+styles.Muted.Render(d.Type))
	}
	if d.Priority != "" {
		parts = append(parts, priorityStyle(d.Priority).Render(d.Priority))
	}
	if d.Points > 0 {
		parts = append(parts, styles.Muted.Render(fmt.Sprintf("%dpts", d.Points)))
	}
	if when := formatCreated(d.CreatedAt); when != "" {
		parts = append(parts, styles.Muted.Render("created "+when))
	}
	return strings.Join(parts, "  ")
}

func labelsStyled(d *Data) string {
	if d == nil || len(d.Labels) == 0 {
		return ""
	}
	return styles.Muted.Render("Labels: ") + styles.Subtle.Render(strings.Join(d.Labels, ", "))
}

func parentRowText(d *Data, width int) string {
	id := d.ParentID
	title := ""
	typ := "epic"
	if d.Parent != nil {
		id = d.Parent.ID
		title = d.Parent.Title
		if d.Parent.Type != "" {
			typ = d.Parent.Type
		}
	}
	icon := typeStyle(typ).Render(typeIcon(typ))
	idStr := styles.Muted.Render(id)
	prefix := "↑ " + icon + " " + idStr + "  "
	room := width - ansi.StringWidth(prefix)
	if room < 8 {
		room = 8
	}
	if title == "" || title == id {
		return strings.TrimRight(prefix, " ")
	}
	return prefix + styles.Body.Render(truncateCell(title, room))
}

func childRowText(child Ref, width int) string {
	icon := typeStyle(child.Type).Render(typeIcon(child.Type))
	idStr := styles.Muted.Render(child.ID)
	st := statusPlainStyle(child.Status).Render(child.Status)
	prefix := icon + " " + idStr + "  " + st + "  "
	room := width - ansi.StringWidth(prefix)
	if room < 8 {
		room = 8
	}
	return prefix + styles.Body.Render(truncateCell(child.Title, room))
}

func logRowText(log Log, width int) string {
	when := formatLogTime(log.Timestamp)
	sess := truncateSession(log.Session)
	left := styles.Muted.Render(when) + "  " + styles.Subtle.Render(sess) + "  "
	room := width - ansi.StringWidth(left)
	if room < 8 {
		room = 8
	}
	return left + styles.Body.Render(truncateCell(log.Message, room))
}

func renderAcceptance(renderer *markdown.Renderer, d *Data, width int) string {
	if d == nil || strings.TrimSpace(d.Acceptance) == "" {
		return ""
	}
	if renderer == nil {
		var err error
		if renderer, err = markdown.NewRenderer(); err != nil {
			return d.Acceptance
		}
	}
	return strings.Join(renderer.RenderContent(d.Acceptance, width), "\n")
}

func sectionRule(title, right string, width int) string {
	left := styles.Muted.Bold(true).Render(title)
	r := ""
	if right != "" {
		r = styles.Muted.Render(right)
	}
	return ruleBetween(left, r, width)
}

func ruleBetween(left, right string, width int) string {
	used := ansi.StringWidth(left) + ansi.StringWidth(right)
	fill := width - used - 2
	if fill < 1 {
		fill = 1
	}
	line := styles.Subtle.Render(" " + strings.Repeat("─", fill) + " ")
	return left + line + right
}

func childCount(children []Ref) string {
	closed := 0
	for _, c := range children {
		if strings.EqualFold(c.Status, "closed") {
			closed++
		}
	}
	return fmt.Sprintf("%d/%d", closed, len(children))
}

func recentLogs(logs []Log, limit int) []Log {
	if len(logs) == 0 {
		return nil
	}
	sorted := append([]Log(nil), logs...)
	// Show chronological order; td may return either direction.
	// Stable enough: if timestamps parse, sort ascending; else keep as-is.
	parseable := true
	times := make([]time.Time, len(sorted))
	for i, l := range sorted {
		t, err := parseTime(l.Timestamp)
		if err != nil {
			parseable = false
			break
		}
		times[i] = t
	}
	if parseable {
		for i := 0; i < len(sorted); i++ {
			for j := i + 1; j < len(sorted); j++ {
				if times[j].Before(times[i]) {
					sorted[i], sorted[j] = sorted[j], sorted[i]
					times[i], times[j] = times[j], times[i]
				}
			}
		}
	}
	if len(sorted) > limit {
		sorted = sorted[len(sorted)-limit:]
	}
	return sorted
}

func formatCreated(raw string) string {
	t, err := parseTime(raw)
	if err != nil {
		return strings.TrimSpace(raw)
	}
	return t.Local().Format("2006-01-02 15:04")
}

func formatLogTime(raw string) string {
	t, err := parseTime(raw)
	if err != nil {
		if len(raw) >= 5 {
			return raw
		}
		return raw
	}
	return t.Local().Format("15:04")
}

func parseTime(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, fmt.Errorf("empty time")
	}
	if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return t, nil
	}
	return time.Parse(time.RFC3339, raw)
}

func truncateSession(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "—"
	}
	if len(s) > 10 {
		return s[:10]
	}
	return s
}

func truncateCell(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if ansi.StringWidth(s) <= width {
		return s
	}
	return ansi.Truncate(s, width, "…")
}

func wrapPlain(s string, width int) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if width < 8 {
		width = 8
	}
	if ansi.StringWidth(s) <= width {
		return []string{s}
	}
	var lines []string
	for ansi.StringWidth(s) > width {
		cut := width
		// Prefer a word break so a title does not split mid-word when it can
		// sit on the next line whole.
		r := []rune(s)
		if cut > len(r) {
			cut = len(r)
		}
		// Walk back to a space within the last third of the line.
		window := r[:cut]
		brk := -1
		for i := len(window) - 1; i > len(window)*2/3; i-- {
			if window[i] == ' ' {
				brk = i
				break
			}
		}
		if brk > 0 {
			cut = brk
		}
		lines = append(lines, strings.TrimSpace(string(r[:cut])))
		s = strings.TrimSpace(string(r[cut:]))
	}
	if s != "" {
		lines = append(lines, s)
	}
	return lines
}

func paintRow(text string, width int, selected, hovered, active bool) string {
	fitted := fitLine(text, width)
	switch {
	case selected && active:
		return styles.FillBackground(fitted, width, styles.Primary)
	case selected || hovered:
		return styles.FillBackground(fitted, width, styles.BgTertiary)
	default:
		return fitted
	}
}
