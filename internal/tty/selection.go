package tty

import (
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/textselect"
)

// The selection engine lives in internal/textselect, where surfaces that are
// not terminals can reach it. The terminal is one of its hosts rather than its
// owner, and re-exports it here so a host that embeds a terminal keeps asking
// one package about the pane, its window and the selection over it.
//
// The aliases below add no behaviour: the terminal runs the same engine every
// other surface will. Only what is genuinely the terminal's — the viewport a
// Geometry is built from — is written out as a function here.

// Coordinate mapping: where a pointer is over a drawn buffer.
type (
	// Buffer is the lines a gesture reads. *OutputBuffer implements it.
	Buffer = textselect.Buffer
	Cell   = textselect.Cell
	// Geometry places a drawn window on screen. See GeometryFor.
	Geometry = textselect.Geometry
)

const DefaultTabWidth = textselect.DefaultTabWidth

var (
	OutputRowAt          = textselect.OutputRowAt
	AbsoluteLine         = textselect.AbsoluteLine
	BufferBase           = textselect.BufferBase
	BufferAbsolute       = textselect.BufferAbsolute
	ScrollKeepsSelection = textselect.ScrollKeepsSelection
	ColAt                = textselect.ColAt
	LineIndexAt          = textselect.LineIndexAt
	CellAt               = textselect.CellAt
	ClampedCellAt        = textselect.ClampedCellAt
	LineTextAt           = textselect.LineTextAt
	WordSpanAt           = textselect.WordSpanAt
	UnitSpanAt           = textselect.UnitSpanAt
	SelectAllSpan        = textselect.SelectAllSpan
	SelectedLines        = textselect.SelectedLines
	EdgeScrollRows       = textselect.EdgeScrollRows
	EdgeScrollDelta      = textselect.EdgeScrollDelta
)

// GeometryFor places a drawn window on screen. The origin is the host's — it
// alone knows where its chrome ends — and everything else is the layout's, so a
// surface cannot hit-test against a window different from the one it drew.
func GeometryFor(x, y int, layout Viewport, tabWidth int) Geometry {
	if tabWidth <= 0 {
		tabWidth = DefaultTabWidth
	}
	return Geometry{
		Content:   mouse.Rect{X: x, Y: y, W: layout.DisplayWidth, H: layout.DisplayHeight},
		Start:     layout.Start,
		End:       layout.End,
		ColOffset: layout.Fit.ColOffset,
		TabWidth:  tabWidth,
	}
}

// The click/drag gesture: what a release without motion means, the granularity
// a drag extends by, and the held-pointer edge scroll.
type (
	ClickResolution  = textselect.ClickResolution
	SelectionUnit    = textselect.SelectionUnit
	PressEvent       = textselect.PressEvent
	Pointer          = textselect.Pointer
	AutoScrollTarget = textselect.AutoScrollTarget
)

const (
	ClickNone     = textselect.ClickNone
	ClickActivate = textselect.ClickActivate
	ClickForward  = textselect.ClickForward

	SelectUnitChar = textselect.SelectUnitChar
	SelectUnitWord = textselect.SelectUnitWord
	SelectUnitLine = textselect.SelectUnitLine

	DragScrollStep     = textselect.DragScrollStep
	AutoScrollInterval = textselect.AutoScrollInterval
	AutoScrollStep     = textselect.AutoScrollStep
	AutoScrollMaxRun   = textselect.AutoScrollMaxRun
)

var AutoScrollHoldExpired = textselect.AutoScrollHoldExpired

// Click intent: which pointer actions are gestures, and what a click resolves
// to when the gesture ends without motion.
type (
	ClickIntent        = textselect.ClickIntent
	PointerIntent      = textselect.PointerIntent
	PointerIntentInput = textselect.PointerIntentInput
)

const (
	PointerIgnore     = textselect.PointerIgnore
	PointerPress      = textselect.PointerPress
	PointerSelectWord = textselect.PointerSelectWord
	PointerSelectLine = textselect.PointerSelectLine
	PointerWheel      = textselect.PointerWheel
	PointerDrag       = textselect.PointerDrag
	PointerFinish     = textselect.PointerFinish
	PointerAbandon    = textselect.PointerAbandon
)

var (
	ResolveClick        = textselect.ResolveClick
	PointerIntentFor    = textselect.PointerIntentFor
	PressesTerminal     = textselect.PressesTerminal
	PressLeavesTerminal = textselect.PressLeavesTerminal
)
