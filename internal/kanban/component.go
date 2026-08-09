package kanban

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/styles"
)

type RegionKind string

const (
	RegionColumn RegionKind = "column"
	RegionCard   RegionKind = "card"
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

func (c *Component) ScrollLane(column, delta int) {
	if column < 0 || column >= len(c.board.Lanes) {
		return
	}
	id := c.board.Lanes[column].ID
	c.scroll[id] += delta
	c.clampScroll()
}

func (c *Component) ClearHover() { c.hoverID = "" }

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
	c.ensureSelectedVisible(layout.MaxCards)

	borderStyle := lipgloss.NewStyle().Foreground(styles.BorderNormal)
	horizSep, vertSep := borderStyle.Render("─"), borderStyle.Render("│")
	innerWidth := options.Width - 4
	header := styles.Title.Render(options.Header)
	right := styles.Muted.Render(options.HeaderRight)
	headerGap := max(1, innerWidth-ansi.StringWidth(options.Header)-ansi.StringWidth(options.HeaderRight))
	lines := []string{header + strings.Repeat(" ", headerGap) + right, strings.Repeat(horizSep, innerWidth)}

	regions := make([]HitRegion, 0)
	columnHeaders := make([]string, 0, len(c.board.Lanes))
	columnX := 2
	for column, lane := range c.board.Lanes {
		style := lipgloss.NewStyle().Bold(true).Width(layout.ColumnWidth)
		if lane.HeaderColor != nil {
			style = style.Foreground(lane.HeaderColor)
		}
		if column == c.selection.Column {
			style = style.Underline(true)
		}
		columnHeaders = append(columnHeaders, style.Render(fmt.Sprintf("%s (%d)", lane.Label, len(lane.Cards))))
		regions = append(regions, HitRegion{Kind: RegionColumn, Column: column, Row: -1, X: columnX, Y: 3, W: layout.ColumnWidth, H: 1})
		columnX += layout.ColumnWidth + 1
	}
	lines = append(lines, strings.Join(columnHeaders, vertSep), strings.Repeat(horizSep, innerWidth))

	visibleByLane := make([][]Card, len(c.board.Lanes))
	maxRows := 0
	for column, lane := range c.board.Lanes {
		start := c.scroll[lane.ID]
		end := min(len(lane.Cards), start+layout.MaxCards)
		if start < end {
			visibleByLane[column] = lane.Cards[start:end]
		}
		rows := len(visibleByLane[column])
		if rows == 0 && lane.State != CellReady {
			rows = 1
		}
		maxRows = max(maxRows, rows)
	}
	maxRows = min(maxRows, layout.MaxCards)
	for visibleRow := 0; visibleRow < maxRows; visibleRow++ {
		columnX = 2
		for column, lane := range c.board.Lanes {
			if visibleRow < len(visibleByLane[column]) {
				row := c.scroll[lane.ID] + visibleRow
				card := visibleByLane[column][visibleRow]
				regions = append(regions, HitRegion{Kind: RegionCard, Column: column, Row: row, CardID: card.ID, X: columnX, Y: 5 + visibleRow*layout.CardHeight, W: layout.ColumnWidth - 1, H: layout.CardHeight})
			}
			columnX += layout.ColumnWidth + 1
		}
		for line := 0; line < layout.CardHeight; line++ {
			cells := make([]string, 0, len(c.board.Lanes))
			for column, lane := range c.board.Lanes {
				width := layout.ColumnWidth - 1
				cell := ""
				if visibleRow < len(visibleByLane[column]) {
					card := visibleByLane[column][visibleRow]
					selected := column == c.selection.Column && c.scroll[lane.ID]+visibleRow == c.selection.Row
					if options.RenderCard != nil {
						cell = options.RenderCard(card, line, width, selected, card.ID == c.hoverID)
					} else {
						cell = defaultCardLine(card, line, width, selected)
					}
				} else if visibleRow == 0 && line == 0 && lane.State != CellReady {
					message := lane.Message
					if message == "" {
						message = string(lane.State)
					}
					cell = styles.Muted.Render(" " + message)
				}
				cells = append(cells, fit(cell, width))
			}
			lines = append(lines, strings.Join(cells, vertSep))
		}
	}
	for rendered := maxRows * layout.CardHeight; rendered < layout.ContentRows; rendered++ {
		cells := make([]string, len(c.board.Lanes))
		for i := range cells {
			cells[i] = strings.Repeat(" ", layout.ColumnWidth-1)
		}
		lines = append(lines, strings.Join(cells, vertSep))
	}
	return RenderResult{View: styles.RenderPanel(strings.Join(lines, "\n"), options.Width, options.Height, true), Regions: regions}
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
	values := []string{card.Title, card.Subtitle, card.Detail, card.Meta}
	if line < 0 || line >= len(values) {
		return strings.Repeat(" ", width)
	}
	value := " " + values[line]
	if selected {
		return styles.ListItemSelected.Width(width).Render(fit(value, width))
	}
	if line > 0 {
		return styles.Muted.Width(width).Render(fit(value, width))
	}
	return fit(value, width)
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
