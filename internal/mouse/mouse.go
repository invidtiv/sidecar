package mouse

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

// Rect represents a rectangular region.
type Rect struct {
	X, Y, W, H int
}

// Contains returns true if the point (x, y) is within the rectangle.
func (r Rect) Contains(x, y int) bool {
	return x >= r.X && x < r.X+r.W && y >= r.Y && y < r.Y+r.H
}

// Region is a named rectangular hit region with associated data.
type Region struct {
	ID   string
	Rect Rect
	Data any
}

// HitMap tracks hit regions for mouse click detection.
type HitMap struct {
	regions []Region
}

// NewHitMap creates a new empty HitMap.
func NewHitMap() *HitMap {
	return &HitMap{
		regions: make([]Region, 0, 32),
	}
}

// Clear removes all regions from the hit map.
func (h *HitMap) Clear() {
	h.regions = h.regions[:0]
}

// Add adds a new region to the hit map.
func (h *HitMap) Add(id string, rect Rect, data any) {
	h.regions = append(h.regions, Region{
		ID:   id,
		Rect: rect,
		Data: data,
	})
}

// AddRect adds a region using individual coordinates.
func (h *HitMap) AddRect(id string, x, y, w, height int, data any) {
	h.Add(id, Rect{X: x, Y: y, W: w, H: height}, data)
}

// Test returns the first region containing the point, or nil if none.
func (h *HitMap) Test(x, y int) *Region {
	// Test in reverse order so later (topmost) regions take priority
	for i := len(h.regions) - 1; i >= 0; i-- {
		if h.regions[i].Rect.Contains(x, y) {
			return &h.regions[i]
		}
	}
	return nil
}

// Regions returns a copy of all registered regions (for testing).
func (h *HitMap) Regions() []Region {
	return append([]Region(nil), h.regions...)
}

// Handler combines a HitMap with mouse state tracking for drag and double-click detection.
type Handler struct {
	HitMap *HitMap

	// Click tracking for double-click detection
	lastClickX      int
	lastClickY      int
	lastClickTime   time.Time
	lastClickRegion string
	lastClickCount  int
	lastClickShift  bool
	lastClickAlt    bool

	// Drag tracking
	dragging       bool
	dragStartX     int
	dragStartY     int
	dragStartValue int // Initial value when drag started (e.g., sidebar width)
	dragRegion     string
}

// NewHandler creates a new mouse handler.
func NewHandler() *Handler {
	return &Handler{
		HitMap: NewHitMap(),
	}
}

// ClickResult represents the result of processing a click event.
type ClickResult struct {
	Region        *Region
	IsDoubleClick bool
	IsTripleClick bool
	ClickCount    int
}

// HandleClick processes a mouse click and returns the hit region.
// Tracks click timing for double-click detection.
func (h *Handler) HandleClick(x, y int) ClickResult {
	return h.handleClickWithModifiers(x, y, false, false)
}

func (h *Handler) handleClickWithModifiers(x, y int, shift, alt bool) ClickResult {
	region := h.HitMap.Test(x, y)

	result := ClickResult{Region: region}

	if region != nil {
		// Count successive clicks at the same cell. Reset after triple-click so
		// a fourth click starts a new gesture.
		now := time.Now()
		if region.ID == h.lastClickRegion &&
			x == h.lastClickX && y == h.lastClickY &&
			shift == h.lastClickShift && alt == h.lastClickAlt &&
			now.Sub(h.lastClickTime) < 400*time.Millisecond {
			h.lastClickCount++
		} else {
			h.lastClickCount = 1
		}
		result.ClickCount = h.lastClickCount
		result.IsDoubleClick = h.lastClickCount == 2
		result.IsTripleClick = h.lastClickCount == 3
		if result.IsTripleClick {
			h.lastClickRegion = ""
			h.lastClickTime = time.Time{}
			h.lastClickCount = 0
		} else {
			h.lastClickRegion = region.ID
			h.lastClickTime = now
			h.lastClickX = x
			h.lastClickY = y
			h.lastClickShift = shift
			h.lastClickAlt = alt
		}
	}

	return result
}

// StartDrag begins tracking a drag operation.
func (h *Handler) StartDrag(x, y int, regionID string, startValue int) {
	h.dragging = true
	h.dragStartX = x
	h.dragStartY = y
	h.dragStartValue = startValue
	h.dragRegion = regionID
}

// IsDragging returns true if a drag operation is in progress.
func (h *Handler) IsDragging() bool {
	return h.dragging
}

// DragRegion returns the ID of the region the in-progress drag STARTED in (the
// drag source), not the region currently under the cursor. It is empty once
// EndDrag has run; consumers that need the source at drag-end time should read
// MouseAction.DragStartID, which survives the reset.
func (h *Handler) DragRegion() string {
	return h.dragRegion
}

// DragDelta returns the X and Y movement since drag started.
func (h *Handler) DragDelta(x, y int) (dx, dy int) {
	return x - h.dragStartX, y - h.dragStartY
}

// DragStartValue returns the initial value when the drag started.
func (h *Handler) DragStartValue() int {
	return h.dragStartValue
}

// EndDrag stops tracking the drag operation.
func (h *Handler) EndDrag() {
	h.dragging = false
	h.dragRegion = ""
}

// Clear resets the handler state and clears the hit map.
func (h *Handler) Clear() {
	h.HitMap.Clear()
}

// HandleMouse is a convenience method for processing tea.MouseMsg events.
// Returns the action to take based on the mouse event.
func (h *Handler) HandleMouse(msg tea.MouseMsg) MouseAction {
	switch msg := msg.(type) {
	case tea.MouseClickMsg:
		m := msg.Mouse()
		if m.Button == tea.MouseLeft {
			shift := m.Mod.Contains(tea.ModShift)
			alt := m.Mod.Contains(tea.ModAlt)
			result := h.handleClickWithModifiers(m.X, m.Y, shift, alt)
			if result.Region == nil {
				return MouseAction{Type: ActionNone}
			}
			if result.IsDoubleClick {
				return MouseAction{
					Type:   ActionDoubleClick,
					Region: result.Region,
					X:      m.X,
					Y:      m.Y,
					Shift:  shift,
					Alt:    alt,
				}
			}
			if result.IsTripleClick {
				return MouseAction{
					Type:   ActionTripleClick,
					Region: result.Region,
					X:      m.X,
					Y:      m.Y,
					Shift:  shift,
					Alt:    alt,
				}
			}
			return MouseAction{
				Type:   ActionClick,
				Region: result.Region,
				X:      m.X,
				Y:      m.Y,
				Shift:  shift,
				Alt:    alt,
			}
		}

	case tea.MouseWheelMsg:
		// v2 delivers wheel events as a dedicated type (v1 folded them into
		// MouseActionPress with wheel buttons).
		m := msg.Mouse()
		shift := m.Mod.Contains(tea.ModShift)
		alt := m.Mod.Contains(tea.ModAlt)
		region := h.HitMap.Test(m.X, m.Y)
		// Modifiers are carried on the action, not just consumed here: consumers
		// use them to decide whether a notch is theirs or the terminal
		// application's (see workspace.forwardWheelToPane).
		switch m.Button {
		case tea.MouseWheelUp:
			// Shift+scroll = horizontal scroll
			if shift {
				return MouseAction{Type: ActionScrollLeft, Region: region, X: m.X, Y: m.Y, Delta: -10, Shift: shift, Alt: alt}
			}
			return MouseAction{Type: ActionScrollUp, Region: region, X: m.X, Y: m.Y, Delta: -WheelScrollLines, Alt: alt}
		case tea.MouseWheelDown:
			if shift {
				return MouseAction{Type: ActionScrollRight, Region: region, X: m.X, Y: m.Y, Delta: 10, Shift: shift, Alt: alt}
			}
			return MouseAction{Type: ActionScrollDown, Region: region, X: m.X, Y: m.Y, Delta: WheelScrollLines, Alt: alt}
		case tea.MouseWheelLeft:
			// Native horizontal scroll (trackpad) - reversed for Mac natural scrolling
			return MouseAction{Type: ActionScrollRight, Region: region, X: m.X, Y: m.Y, Delta: 10, Shift: shift, Alt: alt}
		case tea.MouseWheelRight:
			return MouseAction{Type: ActionScrollLeft, Region: region, X: m.X, Y: m.Y, Delta: -10, Shift: shift, Alt: alt}
		}

	case tea.MouseReleaseMsg:
		if h.dragging {
			m := msg.Mouse()
			// Capture everything the consumer needs *before* EndDrag clears the
			// drag state: the release point, the region under it, and the
			// region the drag started in.
			dx, dy := h.DragDelta(m.X, m.Y)
			region := h.HitMap.Test(m.X, m.Y)
			startRegion := h.dragRegion
			h.EndDrag()
			return MouseAction{
				Type:        ActionDragEnd,
				Region:      region,
				DragStartID: startRegion,
				X:           m.X,
				Y:           m.Y,
				DragDX:      dx,
				DragDY:      dy,
				Shift:       m.Mod.Contains(tea.ModShift),
				Alt:         m.Mod.Contains(tea.ModAlt),
			}
		}

	case tea.MouseMotionMsg:
		m := msg.Mouse()
		// A drag requires a held button. The app runs in all-motion mode, so
		// button-less motion arrives constantly; if a release was ever lost
		// (released outside the window, focus stolen mid-gesture) the handler
		// would otherwise stay "dragging" forever and turn plain mouse movement
		// into a drag. Treat the first button-less motion as the end of the
		// gesture and fall through to hover.
		if h.dragging && m.Button == tea.MouseNone {
			h.EndDrag()
		}
		if h.dragging {
			dx, dy := h.DragDelta(m.X, m.Y)
			return MouseAction{
				Type:        ActionDrag,
				Region:      h.HitMap.Test(m.X, m.Y),
				DragStartID: h.dragRegion,
				X:           m.X,
				Y:           m.Y,
				DragDX:      dx,
				DragDY:      dy,
				Shift:       m.Mod.Contains(tea.ModShift),
				Alt:         m.Mod.Contains(tea.ModAlt),
			}
		}
		// Track hover for visual feedback
		region := h.HitMap.Test(m.X, m.Y)
		return MouseAction{
			Type:   ActionHover,
			Region: region,
			X:      m.X,
			Y:      m.Y,
		}
	}

	return MouseAction{Type: ActionNone}
}

// ActionType represents the type of mouse action detected.
type ActionType int

const (
	ActionNone ActionType = iota
	ActionClick
	ActionDoubleClick
	ActionTripleClick
	ActionScrollUp
	ActionScrollDown
	ActionScrollLeft  // Shift+scroll up = scroll left
	ActionScrollRight // Shift+scroll down = scroll right
	ActionDrag
	ActionDragEnd
	ActionHover
)

// WheelScrollLines is how many lines one physical wheel notch scrolls. Vertical
// wheel actions report Delta in lines, not notches, so a consumer that needs the
// notch count — anything translating back into wheel events, such as forwarding
// to a terminal application — has to divide by this.
const WheelScrollLines = 3

// MouseAction represents a processed mouse event.
type MouseAction struct {
	Type   ActionType
	Region *Region
	X, Y   int
	// Delta is the scroll amount in lines for vertical wheel actions and in
	// columns for horizontal ones. See WheelScrollLines to recover notches.
	Delta  int
	DragDX int // Drag delta X
	DragDY int // Drag delta Y
	// DragStartID is the ID of the region the drag started in, for
	// ActionDrag/ActionDragEnd. Region, for those two actions, is the region
	// currently under the cursor (the drop target), which is usually a
	// different region entirely.
	DragStartID string
	Shift       bool
	Alt         bool
}
