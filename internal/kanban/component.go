package kanban

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/ui"
)

type RegionKind string

const (
	RegionColumn     RegionKind = "column"
	RegionColumnBody RegionKind = "column-body"
	RegionCard       RegionKind = "card"
)

type HitRegion struct {
	Kind       RegionKind
	Column     int
	Row        int
	CardID     string
	X, Y, W, H int
}

type PointerKind string

const (
	PointerHover       PointerKind = "hover"
	PointerClick       PointerKind = "click"
	PointerDoubleClick PointerKind = "double-click"
)

type ActionKind string

const (
	ActionNone      ActionKind = ""
	ActionSelected  ActionKind = "selected"
	ActionActivated ActionKind = "activated"
)

type Action struct {
	Kind   ActionKind
	CardID string
	LaneID LaneID
}

type CardRenderer func(card Card, line, width int, selected, hovered bool) string

type RenderOptions struct {
	Width, Height  int
	Header         string
	HeaderRight    string
	MinColumnWidth int
	CardHeight     int
	RenderCard     CardRenderer
}

type RenderResult struct {
	View    string
	Regions []HitRegion
	Compact bool
}

// Component owns reusable board interaction state. It deliberately emits
// semantic card IDs; consumers keep domain actions and payloads source-local.
type Component struct {
	board     Board
	selection Selection
	scroll    map[LaneID]int
	hoverID   string
}

func (c *Component) SetBoard(board Board) {
	if c.scroll == nil {
		c.scroll = make(map[LaneID]int)
	}
	c.selection = board.PreserveSelection(c.board, c.selection)
	c.board = board
	c.selection = board.Clamp(c.selection)
	c.clampScroll()
}

func (c *Component) Board() Board { return c.board }

func (c *Component) Selection() Selection { return c.selection }

func (c *Component) Select(selection Selection) { c.selection = c.board.Clamp(selection) }

func (c *Component) MoveColumn(delta int) { c.selection = c.board.MoveColumn(c.selection, delta) }

func (c *Component) MoveRow(delta int) { c.selection = c.board.MoveRow(c.selection, delta) }

// MoveInColumn targets the lane under a wheel gesture and moves its selection.
// Keeping selection and viewport together means the next render can follow the
// newly selected card without snapping a separately scrolled viewport back.
func (c *Component) MoveInColumn(column, delta int) {
	c.selection = c.board.Clamp(Selection{Column: column, Row: c.selection.Row})
	c.selection = c.board.MoveRow(c.selection, delta)
}

func (c *Component) ScrollLane(column, delta int) {
	if column < 0 || column >= len(c.board.Lanes) {
		return
	}
	id := c.board.Lanes[column].ID
	c.scroll[id] += delta
	c.clampScroll()
}

func (c *Component) ClearHover() { c.hoverID = "" }

// VisibleCards returns the actual per-lane card window used by Render. It also
// scrolls the selected card into view, so background consumers such as the
// animation scheduler cannot drift from the next painted frame.
func (c *Component) VisibleCards(maxCards int) []Card {
	if maxCards <= 0 {
		return nil
	}
	c.ensureSelectedVisible(maxCards)
	var cards []Card
	for _, lane := range c.board.Lanes {
		start := c.scroll[lane.ID]
		end := min(len(lane.Cards), start+maxCards)
		if start < end {
			cards = append(cards, lane.Cards[start:end]...)
		}
	}
	return cards
}

func (c *Component) Compact(width, minColumnWidth int) bool {
	return UsesCompact(width, len(c.board.Lanes), minColumnWidth, 4)
}

func (c *Component) HandlePointer(kind PointerKind, region HitRegion) Action {
	if region.Column < 0 || region.Column >= len(c.board.Lanes) {
		return Action{}
	}
	if region.Kind == RegionColumn {
		if kind == PointerClick {
			c.Select(Selection{Column: region.Column})
		}
		return Action{}
	}
	card, ok := c.board.CardAt(Selection{Column: region.Column, Row: region.Row})
	if !ok || card.ID != region.CardID {
		return Action{}
	}
	switch kind {
	case PointerHover:
		c.hoverID = card.ID
		return Action{}
	case PointerClick:
		c.Select(Selection{Column: region.Column, Row: region.Row})
		return Action{Kind: ActionSelected, CardID: card.ID, LaneID: c.board.Lanes[region.Column].ID}
	case PointerDoubleClick:
		c.Select(Selection{Column: region.Column, Row: region.Row})
		return Action{Kind: ActionActivated, CardID: card.ID, LaneID: c.board.Lanes[region.Column].ID}
	default:
		return Action{}
	}
}

func (c *Component) Render(options RenderOptions) RenderResult {
	if options.MinColumnWidth <= 0 {
		options.MinColumnWidth = 16
	}
	if options.CardHeight <= 0 {
		options.CardHeight = 4
	}
	if c.Compact(options.Width, options.MinColumnWidth) {
		return RenderResult{Compact: true}
	}
	layout := CalculateLayout(options.Width, options.Height, len(c.board.Lanes), options.MinColumnWidth, options.CardHeight)
	maxCards := layout.MaxCards
	showBelowRow := false
	for _, lane := range c.board.Lanes {
		if len(lane.Cards) > maxCards {
			showBelowRow = layout.ContentRows > layout.CardHeight
			break
		}
	}
	if showBelowRow {
		maxCards = max(1, (layout.ContentRows-1)/layout.CardHeight)
	}
	c.ensureSelectedVisible(maxCards)

	borderStyle := lipgloss.NewStyle().Foreground(styles.BorderNormal)
	vertSep := borderStyle.Render("│")
	innerWidth := options.Width - 4
	widths := layout.ColumnWidths
	header := styles.Title.Render(options.Header)
	right := styles.Muted.Render(options.HeaderRight)
	headerGap := max(1, innerWidth-ansi.StringWidth(options.Header)-ansi.StringWidth(options.HeaderRight))
	lines := []string{header + strings.Repeat(" ", headerGap) + right, borderStyle.Render(bracketRule(widths, "┬"))}

	regions := make([]HitRegion, 0)
	columnHeaders := make([]string, 0, len(c.board.Lanes))
	columnX := 2
	for column, lane := range c.board.Lanes {
		width := widths[column]
		columnHeaders = append(columnHeaders, renderLaneHeader(lane, width, column == c.selection.Column))
		regions = append(regions, HitRegion{Kind: RegionColumn, Column: column, Row: -1, X: columnX, Y: 3, W: width, H: 1})
		columnX += width + 1
	}
	lines = append(lines, strings.Join(columnHeaders, vertSep), borderStyle.Render(bracketRule(widths, "┼")))
	contentLimit := layout.ContentRows
	if showBelowRow {
		contentLimit--
	}
	columnX = 2
	for column, width := range widths {
		regions = append(regions, HitRegion{Kind: RegionColumnBody, Column: column, Row: -1, X: columnX, Y: 5, W: width, H: layout.ContentRows})
		columnX += width + 1
	}

	visibleByLane := make([][]Card, len(c.board.Lanes))
	hiddenBelowByLane := make([]int, len(c.board.Lanes))
	scrollbarsByLane := make([][]string, len(c.board.Lanes))
	maxRows := 0
	for column, lane := range c.board.Lanes {
		start := c.scroll[lane.ID]
		end := min(len(lane.Cards), start+maxCards)
		visible := lane.Cards[start:end]
		visibleByLane[column] = visible
		hiddenBelowByLane[column] = len(lane.Cards) - end
		scrollbarsByLane[column] = strings.Split(ui.RenderScrollbar(ui.ScrollbarParams{
			TotalItems: len(lane.Cards), ScrollOffset: start, VisibleItems: maxCards, TrackHeight: contentLimit,
		}), "\n")
		rows := len(visible)
		if rows == 0 && lane.State != CellReady {
			rows = 1
		}
		maxRows = max(maxRows, rows)
	}
	maxRows = min(maxRows, maxCards)
	for visibleRow := 0; visibleRow < maxRows; visibleRow++ {
		columnX = 2
		for column, lane := range c.board.Lanes {
			width := widths[column]
			if visibleRow < len(visibleByLane[column]) {
				row := c.scroll[lane.ID] + visibleRow
				card := visibleByLane[column][visibleRow]
				regions = append(regions, HitRegion{Kind: RegionCard, Column: column, Row: row, CardID: card.ID, X: columnX, Y: 5 + visibleRow*layout.CardHeight, W: width, H: layout.CardHeight})
			}
			columnX += width + 1
		}
		for line := 0; line < layout.CardHeight; line++ {
			cells := make([]string, 0, len(c.board.Lanes))
			for column, lane := range c.board.Lanes {
				width := widths[column]
				cardWidth := max(0, width-1)
				cell := ""
				switch {
				case visibleRow < len(visibleByLane[column]):
					card := visibleByLane[column][visibleRow]
					selected := column == c.selection.Column && c.scroll[lane.ID]+visibleRow == c.selection.Row
					if options.RenderCard != nil {
						cell = options.RenderCard(card, line, cardWidth, selected, card.ID == c.hoverID)
					} else {
						cell = defaultCardLine(card, line, cardWidth, selected)
					}
				case visibleRow == 0 && line == 0 && lane.State != CellReady:
					cell = styles.Muted.Render(" " + emptyCellMessage(lane))
				}
				scrollbarLine := visibleRow*layout.CardHeight + line
				cells = append(cells, fit(cell, cardWidth)+scrollbarAt(scrollbarsByLane[column], scrollbarLine))
			}
			lines = append(lines, strings.Join(cells, vertSep))
		}
	}
	for rendered := maxRows * layout.CardHeight; rendered < contentLimit; rendered++ {
		cells := make([]string, len(c.board.Lanes))
		for column := range cells {
			cells[column] = strings.Repeat(" ", max(0, widths[column]-1)) + scrollbarAt(scrollbarsByLane[column], rendered)
		}
		lines = append(lines, strings.Join(cells, vertSep))
	}
	if showBelowRow {
		cells := make([]string, len(c.board.Lanes))
		for column, hidden := range hiddenBelowByLane {
			if hidden > 0 {
				cells[column] = fit(styles.Muted.Render(fmt.Sprintf(" ↓ %d more below", hidden)), widths[column])
			} else {
				cells[column] = strings.Repeat(" ", widths[column])
			}
		}
		lines = append(lines, strings.Join(cells, vertSep))
	}
	return RenderResult{View: styles.RenderPanel(strings.Join(lines, "\n"), options.Width, options.Height, true), Regions: regions}
}

func scrollbarAt(lines []string, row int) string {
	if row < 0 || row >= len(lines) {
		return " "
	}
	return lines[row]
}

func (c *Component) ensureSelectedVisible(maxCards int) {
	if maxCards <= 0 || c.selection.Column < 0 || c.selection.Column >= len(c.board.Lanes) {
		return
	}
	lane := c.board.Lanes[c.selection.Column]
	offset := c.scroll[lane.ID]
	if c.selection.Row < offset {
		offset = c.selection.Row
	} else if c.selection.Row >= offset+maxCards {
		offset = c.selection.Row - maxCards + 1
	}
	c.scroll[lane.ID] = max(0, offset)
	c.clampScroll()
}

func (c *Component) clampScroll() {
	for _, lane := range c.board.Lanes {
		last := max(0, len(lane.Cards)-1)
		c.scroll[lane.ID] = min(max(0, c.scroll[lane.ID]), last)
	}
}

func defaultCardLine(card Card, line, width int, selected bool) string {
	if len(card.Lines) > 0 {
		if line < 0 || line >= len(card.Lines) {
			return strings.Repeat(" ", width)
		}
		return renderSpans(card.Lines[line].Spans, width, selected)
	}
	values := []string{card.Title, card.Subtitle, card.Detail, card.Meta}
	if line < 0 || line >= len(values) {
		return strings.Repeat(" ", width)
	}
	value := " " + values[line]
	if selected {
		return styles.CardSelected.Width(width).Render(fit(value, width))
	}
	if line > 0 {
		return styles.Muted.Width(width).Render(fit(value, width))
	}
	return fit(value, width)
}

// renderSpans lays spans left to right against a running width budget,
// truncating with ansi so multi-byte glyphs never split. Selection paints the
// background across the full cell; each span keeps its own foreground.
func renderSpans(spans []Span, width int, selected bool) string {
	var b strings.Builder
	remaining := width
	selectionBg := styles.CardSelected.GetBackground()
	for _, span := range spans {
		if remaining <= 0 {
			break
		}
		text := span.Text
		if ansi.StringWidth(text) > remaining {
			text = ansi.Truncate(text, remaining, "")
		}
		w := ansi.StringWidth(text)
		if w == 0 {
			continue
		}
		style := lipgloss.NewStyle()
		if span.Foreground != nil {
			style = style.Foreground(span.Foreground)
		}
		if selected {
			style = style.Background(selectionBg)
		} else if span.Background != nil {
			style = style.Background(span.Background)
		}
		if span.Bold {
			style = style.Bold(true)
		}
		b.WriteString(style.Render(text))
		remaining -= w
	}
	if remaining > 0 {
		pad := strings.Repeat(" ", remaining)
		if selected {
			pad = lipgloss.NewStyle().Background(selectionBg).Render(pad)
		}
		b.WriteString(pad)
	}
	return b.String()
}

// renderLaneHeader renders "LABEL count", the label in the lane's header
// colour and the count muted, truncated to width like any other cell.
func renderLaneHeader(lane Lane, width int, selected bool) string {
	labelStyle := lipgloss.NewStyle().Bold(true)
	countStyle := styles.Muted
	if lane.HeaderColor != nil {
		labelStyle = labelStyle.Foreground(lane.HeaderColor)
	}
	if selected {
		background := styles.CardSelected.GetBackground()
		labelStyle = labelStyle.Background(background)
		countStyle = countStyle.Background(background)
	}
	rendered := labelStyle.Render(lane.Label) + countStyle.Render(fmt.Sprintf(" %d", len(lane.Cards)))
	padding := strings.Repeat(" ", max(0, width-ansi.StringWidth(rendered)))
	if selected {
		padding = lipgloss.NewStyle().Background(styles.CardSelected.GetBackground()).Render(padding)
	}
	return ansi.Truncate(rendered, width, "") + padding
}

// bracketRule builds a per-column run of ─ joined by junction, landing the
// junction glyphs on the same column indices as the │ separators above and
// below it.
func bracketRule(widths []int, junction string) string {
	parts := make([]string, len(widths))
	for i, w := range widths {
		parts[i] = strings.Repeat("─", w)
	}
	return strings.Join(parts, junction)
}

// emptyCellMessage is the text shown in a lane's first content row when it
// has no cards to render. CellEmpty with no explicit message is a dim dot
// rather than a word.
func emptyCellMessage(lane Lane) string {
	if lane.Message != "" {
		return lane.Message
	}
	if lane.State == CellEmpty {
		return "·"
	}
	return string(lane.State)
}

func fit(value string, width int) string {
	if ansi.StringWidth(value) > width {
		value = ansi.Truncate(value, width, "")
	}
	if gap := width - ansi.StringWidth(value); gap > 0 {
		value += strings.Repeat(" ", gap)
	}
	return value
}
