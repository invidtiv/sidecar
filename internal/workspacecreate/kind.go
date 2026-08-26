package workspacecreate

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/modal"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/styles"
)

// Kind is the workspace type the form will create.
type Kind int

const (
	KindShell Kind = iota
	KindWorktree
	// KindTerminalSplit creates a live terminal beside the workspace's own
	// terminal. It owns no branch and no agent, so it needs at most a name.
	KindTerminalSplit
	// The pane kinds below open one leaf of the pane tree. Each needs a
	// target, so the modal grows a second step for them; each has somewhere
	// to be placed, so the placement row shows for every one of them.
	KindFile
	KindDiff
	KindIssue
	// KindResource opens a configured terminal-resource provider's locator as
	// a resource pane. One row exists per configured instance; ProviderID on
	// the row says which, and the label is that instance ID.
	KindResource
	KindNote
)

// kindRow is one row of the create modal's kind list. The list is a table so a
// later pane kind is an entry here rather than new modal code.
type kindRow struct {
	Kind  Kind
	Label string
	// Description is the aligned second column of the vertical list.
	Description string
	// NeedsTarget marks the pane kinds whose step 2 is a target picker.
	NeedsTarget bool
	// ProviderID names the configured instance behind a KindResource row.
	ProviderID string
	// NeedsLiveTerminal marks the row that opens a SECOND live terminal beside
	// the host's own. It is the one capability the two hosts genuinely differ
	// on: the project workspace owns a live terminal peer, the global browser
	// has one producer bound to the selected row. Every other row places a
	// passive pane in a tree, which both hosts have — so nothing else belongs
	// here, and a passive row tagged with it goes missing on a surface that
	// could have drawn it.
	NeedsLiveTerminal bool
}

// kindCatalog is every row the modal knows, in list order. Provider rows are
// appended per host from config, after Issue where the mockup puts them.
var kindCatalog = []kindRow{
	{Kind: KindShell, Label: "Shell", Description: "new agent/shell session"},
	{Kind: KindWorktree, Label: "Worktree", Description: "shell in a new worktree"},
	{Kind: KindTerminalSplit, Label: "Terminal split", Description: "terminal beside current pane", NeedsLiveTerminal: true},
	{Kind: KindFile, Label: "File", Description: "open a file in a split", NeedsTarget: true},
	{Kind: KindDiff, Label: "Git diff", Description: "open a diff in a split", NeedsTarget: true},
	{Kind: KindIssue, Label: "td issue", Description: "open an issue in a split", NeedsTarget: true},
	{Kind: KindNote, Label: "Note", Description: "open a note in a split", NeedsTarget: true},
}

// providerDescription is the fixed description for resource rows.
const providerDescription = "open a resource in a split"

const (
	kindSeparator  = " | "
	kindFrameOpen  = "["
	kindFrameClose = "]"

	// verticalListMinRows is the row count past which the horizontal toggle
	// becomes the mockup's vertical list with aligned descriptions.
	verticalListMinRows = 5
)

// ProviderItem is one configured terminal-resource provider a host offers.
type ProviderItem struct {
	ID string
}

// kindRowsFor is the catalog a host offers.
func kindRowsFor(allowTerminalSplit bool) []kindRow {
	return kindRowsForOpts(rowOpts{allowTerminalSplit: allowTerminalSplit})
}

// rowOpts is what shapes a host's catalog beyond the base table.
type rowOpts struct {
	allowTerminalSplit bool
	showNotes          bool
	providers          []ProviderItem
}

func kindRowsForOpts(opts rowOpts) []kindRow {
	rows := make([]kindRow, 0, len(kindCatalog)+1+len(opts.providers))
	for _, row := range kindCatalog {
		if row.Kind == KindNote && !opts.showNotes {
			continue
		}
		if row.NeedsLiveTerminal && !opts.allowTerminalSplit {
			continue
		}
		rows = append(rows, row)
	}
	// A provider row opens a passive Resource pane, which every host with a
	// pane tree can place — and both hosts have one. It is offered wherever the
	// provider is configured, exactly like the File and Git diff rows beside it.
	for _, p := range opts.providers {
		id := strings.TrimSpace(p.ID)
		if id == "" {
			continue
		}
		rows = append(rows, kindRow{
			Kind:        KindResource,
			Label:       id,
			Description: providerDescription,
			NeedsTarget: true,
			ProviderID:  id,
		})
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

// kindIsPane reports whether kind opens a leaf of the pane tree, which is the
// set the placement row belongs to.
func kindIsPane(kind Kind) bool {
	switch kind {
	case KindTerminalSplit, KindFile, KindDiff, KindIssue, KindResource, KindNote:
		return true
	}
	return false
}

// kindNeedsTarget reports whether kind's create flow continues onto a target
// picker step rather than submitting from the kind list.
func kindNeedsTarget(kind Kind) bool {
	switch kind {
	case KindFile, KindDiff, KindIssue, KindResource, KindNote:
		return true
	}
	return false
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

// kindFromClickX maps a click on the horizontal kind toggle to the row under
// it. Clicks in a separator, or past the last row in a region wider than the
// toggle, keep the nearest row rather than falling through to the first.
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
// form in hand; hosts should use Form.SetKindFromClick, which knows how the
// list is drawn this session.
func KindFromClickX(x, regionX, regionW int) Kind {
	return kindFromClickX(kindRowsFor(false), KindShell, x, regionX, regionW)
}

// kindDisabledSelected is the selected-but-unavailable row: the selected row's
// chrome so the list still says which kind is active, in muted text so the row
// still says it cannot be created. It is a function rather than a value so
// it reads the colour at render time and follows a theme change.
func kindDisabledSelected() lipgloss.Style {
	return styles.ButtonHover.Foreground(styles.TextMuted)
}

func kindRowStyle(disabled, selected, hovered bool) lipgloss.Style {
	if disabled && selected {
		// A disabled row that is still the active kind — its Name field and
		// placement row are drawn below it — must read as selected, or the
		// list shows nothing selected at all. Selected chrome, muted text.
		return kindDisabledSelected()
	}
	if disabled {
		// The unselected row's chrome with muted text: the list is one filled
		// block, and a row that dropped the fill would read as a hole in it
		// rather than as an unavailable choice.
		return styles.Button.Foreground(styles.TextMuted)
	}
	if selected {
		return styles.ButtonFocused
	}
	if hovered {
		return styles.ButtonHover
	}
	return styles.Button
}

// kindFrameStyle is the [ ] around the horizontal kind toggle. It uses the same
// colours as a modal input border so "this field is active" is not the same
// signal as "this kind is selected".
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

func renderKindToggle(rows []kindRow, selIdx int, focused, hovered bool, disabledReason func(Kind) string, contentWidth int) string {
	frame := kindFrameStyle(focused, hovered)
	parts := make([]string, 0, len(rows)*2+2)
	parts = append(parts, frame.Render(kindFrameOpen))
	for i, row := range rows {
		disabled := disabledReason != nil && disabledReason(row.Kind) != ""
		selected := i == selIdx
		style := kindRowStyle(disabled, selected, hovered && !selected)
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

// renderKindList is the vertical kind list: one row per kind, label column
// aligned, description column aligned after it. Selection is by ROW, not
// Kind — resource providers share one Kind, so only the index can say which
// instance is highlighted. A disabled row stays visible with its reason
// inline in place of the description, so the rule is read before the row is
// entered.
func renderKindList(rows []kindRow, selIdx int, focused, hovered bool, disabledReason func(Kind) string, contentWidth int) string {
	labelW := 0
	for _, row := range rows {
		if w := ansi.StringWidth(row.Label); w > labelW {
			labelW = w
		}
	}
	lines := make([]string, 0, len(rows))
	for i, row := range rows {
		reason := ""
		if disabledReason != nil {
			reason = disabledReason(row.Kind)
		}
		disabled := reason != ""
		selected := i == selIdx
		var line string
		cursor := "  "
		if selected {
			cursor = "❯ "
		}
		line += cursor
		line += row.Label + strings.Repeat(" ", labelW-ansi.StringWidth(row.Label)) + "   "
		desc := row.Description
		if disabled {
			desc = reason
		}
		style := kindRowStyle(disabled, selected, hovered && !selected)
		lines = append(lines, renderKindRow(style, line+desc, contentWidth))
	}
	content := strings.Join(lines, "\n")
	if contentWidth > 0 {
		content = truncateLines(content, contentWidth)
	}
	return content
}

// renderKindRow draws one list row across the modal's whole content column.
// A row's fill is what separates it from its neighbours, so every row has to
// reach the same right edge: chrome sized to the text leaves the list with a
// ragged edge whose shape is an accident of the longest description.
func renderKindRow(style lipgloss.Style, text string, contentWidth int) string {
	if contentWidth < 1 {
		return style.Render(text)
	}
	inner := contentWidth - style.GetHorizontalFrameSize()
	if inner < 1 {
		return style.Render(text)
	}
	if ansi.StringWidth(text) > inner {
		text = ansi.Truncate(text, inner, "…")
	}
	return style.Width(contentWidth).Render(text)
}

// kindControl renders the row list however this catalog is drawn: the
// horizontal toggle while it is short, the vertical description list once the
// row count passes the mockup's threshold. Selection is row-precise: the
// selected index resolves the highlighted row (resource providers share one
// Kind, so only the row knows which instance), and onSelect receives that
// whole row. disabledReason answers, per row, why that row cannot be created
// right now; a disabled row is drawn muted whether or not it is selected, so
// the rule is visible before the row is entered.
func kindControl(id string, rows []kindRow, selectedIndex func() int, onSelect func(kindRow), disabledReason func(Kind) string) modal.Section {
	vertical := len(rows) >= verticalListMinRows
	render := renderKindToggle
	if vertical {
		render = renderKindList
	}
	height := 1
	if vertical {
		height = len(rows)
	}
	return modal.Custom(func(contentWidth int, focusID, hoverID string) modal.RenderedSection {
		idx := 0
		if selectedIndex != nil {
			idx = clampIndex(selectedIndex(), len(rows))
		}
		content := render(rows, idx, focusID == id, hoverID == id, disabledReason, contentWidth)
		return modal.RenderedSection{
			Content: content,
			Focusables: []modal.FocusableInfo{{
				ID: id, OffsetX: 0, OffsetY: 0,
				Width:  contentWidth,
				Height: height,
			}},
		}
	}, func(msg tea.Msg, focusID string) (string, tea.Cmd) {
		if focusID != id || selectedIndex == nil || len(rows) == 0 {
			return "", nil
		}
		key, ok := msg.(tea.KeyPressMsg)
		if !ok {
			return "", nil
		}
		idx := clampIndex(selectedIndex(), len(rows))
		switch key.String() {
		case "left", "h", "up", "k":
			if idx > 0 {
				idx--
			}
		case "right", "l", "down", "j":
			if idx < len(rows)-1 {
				idx++
			}
		default:
			return "", nil
		}
		if onSelect != nil {
			onSelect(rows[idx])
		}
		return "", nil
	})
}

// verticalArrowsSteerKindList reports that up/down, pressed while focusID
// holds focus, belong to the kind list rather than to the focused field. The
// kind step is meant to be steered with arrows alone — open it, arrow to the
// row, Enter — so the list keeps up/down everywhere except the fields that
// give them a meaning of their own: a combo's dropdown moves with them, while
// a plain input, a checkbox, and a button have nothing to do with a vertical
// arrow. A new field that grows a vertical gesture belongs in this list.
func verticalArrowsSteerKindList(focusID string) bool {
	switch focusID {
	case FieldProject, FieldBase, FieldAgent:
		return false
	}
	return true
}

// moveKindSelection moves the list by delta rows and stops at either end,
// rather than wrapping: the ends of a short list are easier to feel than to
// count, and a wrap past Note back onto Shell reads as a lost keypress.
func (f *Form) moveKindSelection(delta int) {
	if f == nil || len(f.rows) == 0 {
		return
	}
	idx := clampIndex(f.selectedRowIndex(), len(f.rows)) + delta
	if idx < 0 || idx >= len(f.rows) {
		return
	}
	f.selectRow(idx)
}

func clampIndex(idx, length int) int {
	if idx < 0 {
		return 0
	}
	if idx >= length {
		return length - 1
	}
	return idx
}

// SetKindFromClick picks the row under a click on the kind control, whichever
// shape it is drawn in this session: the vertical list answers by row, the
// horizontal toggle by column spans.
func (f *Form) SetKindFromClick(region mouse.Rect, x, y int) {
	if f == nil {
		return
	}
	if len(f.rows) < verticalListMinRows {
		f.SetKind(kindFromClickX(f.rows, f.kind, x, region.X, region.W))
		return
	}
	// The vertical list answers by row so two provider rows never collapse
	// into one selection.
	f.selectRow(kindIndexAt(f.rows, y-region.Y, region.H))
}

// kindIndexAt clamps a click row to a catalog index.
func kindIndexAt(rows []kindRow, offset, regionH int) int {
	if len(rows) == 0 || regionH <= 0 {
		return 0
	}
	if offset < 0 {
		return 0
	}
	if offset >= len(rows) {
		return len(rows) - 1
	}
	return offset
}

// SetKindFromClickX picks the row under a click on the horizontal toggle. It
// remains for callers and tests pinned to the short-catalog geometry; hosts
// should call SetKindFromClick, which also serves the vertical list.
func (f *Form) SetKindFromClickX(x, regionX, regionW int) {
	if f == nil {
		return
	}
	f.SetKind(kindFromClickX(f.rows, f.kind, x, regionX, regionW))
}
