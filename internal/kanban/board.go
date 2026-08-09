// Package kanban provides reusable board data, stable selection, navigation,
// and layout calculations. Consumers resolve domain objects before building
// cards and resolve semantic card IDs back into their own actions.
package kanban

import "image/color"

type LaneID string

type Card struct {
	ID       string
	Title    string
	Subtitle string
	Detail   string
	Meta     string
}

type Lane struct {
	ID          LaneID
	Label       string
	HeaderColor color.Color
	State       CellState
	Message     string
	Cards       []Card
}

type CellState string

const (
	CellReady   CellState = "ready"
	CellLoading CellState = "loading"
	CellError   CellState = "error"
	CellEmpty   CellState = "empty"
)

type Board struct {
	Lanes []Lane
}

type Selection struct {
	Column int
	Row    int
}

type Position struct {
	Column int
	Row    int
}

type Layout struct {
	ColumnWidth int
	CardHeight  int
	MaxCards    int
	ContentRows int
}

func (b Board) ColumnCount() int { return len(b.Lanes) }

func (b Board) ItemCount(column int) int {
	if column < 0 || column >= len(b.Lanes) {
		return 0
	}
	return len(b.Lanes[column].Cards)
}

func (b Board) CardAt(s Selection) (Card, bool) {
	if s.Column < 0 || s.Column >= len(b.Lanes) || s.Row < 0 || s.Row >= len(b.Lanes[s.Column].Cards) {
		return Card{}, false
	}
	return b.Lanes[s.Column].Cards[s.Row], true
}

func (b Board) PositionOf(id string) (Position, bool) {
	for column := range b.Lanes {
		for row := range b.Lanes[column].Cards {
			if b.Lanes[column].Cards[row].ID == id {
				return Position{Column: column, Row: row}, true
			}
		}
	}
	return Position{}, false
}

func (b Board) Clamp(s Selection) Selection {
	if len(b.Lanes) == 0 {
		return Selection{}
	}
	if s.Column < 0 {
		s.Column = 0
	} else if s.Column >= len(b.Lanes) {
		s.Column = len(b.Lanes) - 1
	}
	count := b.ItemCount(s.Column)
	if count == 0 || s.Row < 0 {
		s.Row = 0
	} else if s.Row >= count {
		s.Row = count - 1
	}
	return s
}

func (b Board) MoveColumn(s Selection, delta int) Selection {
	s.Column += delta
	return b.Clamp(s)
}

func (b Board) MoveRow(s Selection, delta int) Selection {
	if b.ItemCount(s.Column) == 0 {
		return b.Clamp(s)
	}
	s.Row += delta
	return b.Clamp(s)
}

func (b Board) PreserveSelection(previous Board, s Selection) Selection {
	if card, ok := previous.CardAt(s); ok {
		if pos, found := b.PositionOf(card.ID); found {
			return Selection(pos)
		}
	}
	return b.Clamp(s)
}

func MinimumWidth(columns, columnWidth, outerWidth int) int {
	if columns <= 0 {
		return outerWidth
	}
	return columnWidth*columns + (columns - 1) + outerWidth
}

func UsesCompact(width, columns, columnWidth, outerWidth int) bool {
	return width < MinimumWidth(columns, columnWidth, outerWidth)
}

// CalculateLayout mirrors the existing Sidecar board geometry while keeping
// height constraints testable independently of a view renderer.
func CalculateLayout(width, height, columns, minColumnWidth, cardHeight int) Layout {
	innerWidth := width - 4
	columnWidth := 0
	if columns > 0 {
		columnWidth = (innerWidth - columns - 1) / columns
		if columnWidth < minColumnWidth {
			columnWidth = minColumnWidth
		}
	}
	contentRows := height - 6
	if contentRows < cardHeight {
		contentRows = cardHeight
	}
	return Layout{
		ColumnWidth: columnWidth,
		CardHeight:  cardHeight,
		MaxCards:    contentRows / cardHeight,
		ContentRows: contentRows,
	}
}
