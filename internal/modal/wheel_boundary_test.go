package modal

import (
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/mouse"
)

func wheel(x, y int, down bool) tea.MouseWheelMsg {
	btn := tea.MouseWheelUp
	if down {
		btn = tea.MouseWheelDown
	}
	return tea.MouseWheelMsg{X: x, Y: y, Button: btn}
}

// longText returns n numbered lines so content reliably overflows the viewport.
func longText(n int) string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = "line " + strconv.Itoa(i)
	}
	return strings.Join(lines, "\n")
}

// centerOf returns a point inside the named hit region.
func centerOf(t *testing.T, h *mouse.Handler, id string) (int, int) {
	t.Helper()
	for _, r := range h.HitMap.Regions() {
		if r.ID == id {
			return r.Rect.X + r.Rect.W/2, r.Rect.Y + r.Rect.H/2
		}
	}
	t.Fatalf("region %q not registered", id)
	return 0, 0
}

// bodyPoint returns a point over modal-body that no control covers.
func bodyPoint(t *testing.T, h *mouse.Handler) (int, int) {
	t.Helper()
	for _, r := range h.HitMap.Regions() {
		if r.ID != "modal-body" {
			continue
		}
		x := r.Rect.X + r.Rect.W/2
		for y := r.Rect.Y; y < r.Rect.Y+r.Rect.H; y++ {
			if hit := h.HitMap.Test(x, y); hit != nil && hit.ID == "modal-body" {
				return x, y
			}
		}
	}
	t.Fatalf("no free modal-body point found")
	return 0, 0
}

func scrollableModal() *Modal {
	return New("Long").AddSection(Text(longText(200)))
}

func TestWheelAtBoundaryTopAndBottom(t *testing.T) {
	m := scrollableModal()
	h := mouse.NewHandler()
	m.Render(80, 24, h)
	x, y := bodyPoint(t, h)

	if !m.WheelAtBoundary(wheel(x, y, false), h) {
		t.Error("expected bounded scrolling up at top")
	}
	if m.WheelAtBoundary(wheel(x, y, true), h) {
		t.Error("expected movable scrolling down at top")
	}

	m.ScrollToBottom()
	m.Render(80, 24, h)
	if !m.WheelAtBoundary(wheel(x, y, true), h) {
		t.Error("expected bounded scrolling down at bottom")
	}
	if m.WheelAtBoundary(wheel(x, y, false), h) {
		t.Error("expected movable scrolling up at bottom (reverse must pass)")
	}
	if m.lastMaxScroll <= 0 {
		t.Fatalf("expected a positive cached max scroll, got %d", m.lastMaxScroll)
	}
}

func TestWheelAtBoundaryMiddleIsMovableBothWays(t *testing.T) {
	m := scrollableModal()
	h := mouse.NewHandler()
	m.Render(80, 24, h)
	x, y := bodyPoint(t, h)

	m.ScrollBy(wheelLines)
	m.Render(80, 24, h)
	if m.scrollOffset == 0 || m.scrollOffset >= m.lastMaxScroll {
		t.Fatalf("expected a mid-content offset, got %d/%d", m.scrollOffset, m.lastMaxScroll)
	}
	if m.WheelAtBoundary(wheel(x, y, true), h) || m.WheelAtBoundary(wheel(x, y, false), h) {
		t.Error("expected movable in both directions mid-content")
	}
}

func TestWheelAtBoundaryShortAndEmptyContent(t *testing.T) {
	cases := map[string]*Modal{
		"short": New("Confirm").AddSection(Text("Delete this?")).AddSection(Buttons(Btn(" OK ", "ok"))),
		"empty": New("Empty"),
	}
	for name, m := range cases {
		t.Run(name, func(t *testing.T) {
			h := mouse.NewHandler()
			m.Render(80, 24, h)
			x, y := bodyPoint(t, h)
			if !m.WheelAtBoundary(wheel(x, y, true), h) {
				t.Error("expected bounded scrolling down on non-scrollable modal")
			}
			if !m.WheelAtBoundary(wheel(x, y, false), h) {
				t.Error("expected bounded scrolling up on non-scrollable modal")
			}
			if m.lastMaxScroll != 0 {
				t.Errorf("expected zero max scroll, got %d", m.lastMaxScroll)
			}
		})
	}
}

func TestWheelAtBoundaryBackdropAndControlAreAbsorbed(t *testing.T) {
	m := New("Long").AddSection(Buttons(Btn(" OK ", "ok"))).AddSection(Text(longText(200)))
	h := mouse.NewHandler()
	m.Render(80, 24, h)

	// Backdrop: outside the modal box entirely.
	if !m.WheelAtBoundary(wheel(0, 0, true), h) {
		t.Error("expected wheel over the backdrop to be bounded (absorbed)")
	}

	// Control: over a focusable button, which the modal does not scroll.
	bx, by := centerOf(t, h, "ok")
	if !m.WheelAtBoundary(wheel(bx, by, true), h) {
		t.Error("expected wheel over a control to be bounded (absorbed)")
	}
	if !m.WheelAtBoundary(wheel(bx, by, false), h) {
		t.Error("expected wheel over a control to be bounded in both directions")
	}

	// Sanity: the body at the same moment is still movable downward.
	x, y := bodyPoint(t, h)
	if m.WheelAtBoundary(wheel(x, y, true), h) {
		t.Error("expected body to remain movable")
	}
}

func TestWheelAtBoundaryUnknownBeforeFirstRender(t *testing.T) {
	m := scrollableModal()
	h := mouse.NewHandler()
	if m.WheelAtBoundary(wheel(1, 1, true), h) {
		t.Error("expected unknown (false) before the first render")
	}
	if m.WheelAtBoundary(wheel(1, 1, false), h) {
		t.Error("expected unknown (false) before the first render")
	}
	// A render without a mouse handler builds no hit map: still unknown.
	m.Render(80, 24, nil)
	if m.WheelAtBoundary(wheel(1, 1, true), h) {
		t.Error("expected unknown (false) after a handler-less render")
	}
	if m.WheelAtBoundary(wheel(1, 1, true), nil) {
		t.Error("expected unknown (false) with a nil handler")
	}
}

func TestWheelAtBoundaryUnknownAfterInvalidate(t *testing.T) {
	m := scrollableModal()
	h := mouse.NewHandler()
	m.Render(80, 24, h)
	x, y := bodyPoint(t, h)
	if !m.WheelAtBoundary(wheel(x, y, false), h) {
		t.Fatal("precondition: expected bounded at top")
	}

	m.Invalidate()
	if m.WheelAtBoundary(wheel(x, y, false), h) {
		t.Error("expected unknown (false) after Invalidate")
	}

	// Next render restores an exact answer.
	m.Render(80, 24, h)
	if !m.WheelAtBoundary(wheel(x, y, false), h) {
		t.Error("expected bounded again after re-render")
	}
}

func TestWheelAtBoundaryAsyncContentRebuild(t *testing.T) {
	text := "short"
	m := New("Async").AddSection(Custom(func(w int, focusID, hoverID string) RenderedSection {
		return RenderedSection{Content: text}
	}, nil))
	h := mouse.NewHandler()
	m.Render(80, 24, h)
	x, y := bodyPoint(t, h)
	if !m.WheelAtBoundary(wheel(x, y, true), h) {
		t.Fatal("precondition: short content is bounded downward")
	}

	// Async load replaces the content; the host must invalidate.
	text = longText(200)
	m.Invalidate()
	if m.WheelAtBoundary(wheel(x, y, true), h) {
		t.Error("expected unknown (false) between the content change and the next render")
	}
	m.Render(80, 24, h)
	if m.WheelAtBoundary(wheel(x, y, true), h) {
		t.Error("expected movable downward once the long content is rendered")
	}
}

func TestWheelAtBoundaryAfterResize(t *testing.T) {
	m := New("Resize").AddSection(Text(longText(30)))
	h := mouse.NewHandler()

	// Tall screen: content fits, so the body cannot scroll.
	m.Render(80, 60, h)
	x, y := bodyPoint(t, h)
	if !m.WheelAtBoundary(wheel(x, y, true), h) {
		t.Error("expected bounded downward when content fits the tall screen")
	}

	// Short screen: the same content now overflows.
	m.Render(80, 16, h)
	x, y = bodyPoint(t, h)
	if m.WheelAtBoundary(wheel(x, y, true), h) {
		t.Error("expected movable downward after shrinking the screen")
	}

	// Grow again: offset re-clamps to zero and the body is bounded once more.
	m.ScrollToBottom()
	m.Render(80, 60, h)
	x, y = bodyPoint(t, h)
	if m.scrollOffset != 0 {
		t.Errorf("expected offset re-clamped to 0 after growing, got %d", m.scrollOffset)
	}
	if !m.WheelAtBoundary(wheel(x, y, true), h) {
		t.Error("expected bounded downward again after growing the screen")
	}
}

func TestWheelAtBoundaryAfterFocusAutoScroll(t *testing.T) {
	m := New("Focus").
		AddSection(Text(longText(200))).
		AddSection(Buttons(Btn(" OK ", "ok"), Btn(" Cancel ", "cancel")))
	h := mouse.NewHandler()
	m.Render(80, 24, h)
	x, y := bodyPoint(t, h)
	if m.scrollOffset != 0 {
		t.Fatalf("precondition: expected offset 0, got %d", m.scrollOffset)
	}

	// Tabbing to the trailing buttons scrolls them into view, which lands at
	// the bottom of the content.
	m.HandleKey(tea.KeyPressMsg{Code: tea.KeyTab})
	if m.scrollOffset != m.lastMaxScroll {
		t.Fatalf("expected focus auto-scroll to reach max %d, got %d", m.lastMaxScroll, m.scrollOffset)
	}
	if !m.WheelAtBoundary(wheel(x, y, true), h) {
		t.Error("expected bounded downward after focus auto-scroll to the bottom")
	}
	if m.WheelAtBoundary(wheel(x, y, false), h) {
		t.Error("expected movable upward after focus auto-scroll to the bottom")
	}
}

func TestWheelMovementClampsThroughBounds(t *testing.T) {
	m := scrollableModal()
	h := mouse.NewHandler()
	m.Render(80, 24, h)
	x, y := bodyPoint(t, h)

	// A long inertial tail must never push the offset past the cached max.
	for range 200 {
		m.HandleMouse(wheel(x, y, true), h)
		if m.scrollOffset > m.lastMaxScroll {
			t.Fatalf("offset %d overshot max %d before render", m.scrollOffset, m.lastMaxScroll)
		}
	}
	if m.scrollOffset != m.lastMaxScroll {
		t.Errorf("expected offset pinned at max %d, got %d", m.lastMaxScroll, m.scrollOffset)
	}

	for range 200 {
		m.HandleMouse(wheel(x, y, false), h)
		if m.scrollOffset < 0 {
			t.Fatalf("offset went negative: %d", m.scrollOffset)
		}
	}
	if m.scrollOffset != 0 {
		t.Errorf("expected offset back at 0, got %d", m.scrollOffset)
	}
}

func TestWheelAtBoundaryHorizontalIsUnknown(t *testing.T) {
	m := scrollableModal()
	h := mouse.NewHandler()
	m.Render(80, 24, h)
	x, y := bodyPoint(t, h)

	left := tea.MouseWheelMsg{X: x, Y: y, Button: tea.MouseWheelLeft}
	if m.WheelAtBoundary(left, h) {
		t.Error("expected horizontal wheel to be unknown (false)")
	}
	shifted := tea.MouseWheelMsg{X: x, Y: y, Button: tea.MouseWheelDown, Mod: tea.ModShift}
	if m.WheelAtBoundary(shifted, h) {
		t.Error("expected shift+wheel to be unknown (false)")
	}
}

// scrollOwnerStub is a section that owns its own scroll state.
type scrollOwnerStub struct {
	atTop    bool
	atBottom bool
	asked    int
}

func (s *scrollOwnerStub) Render(contentWidth int, focusID, hoverID string) RenderedSection {
	return RenderedSection{
		Content: strings.Join([]string{"child", "child", "child"}, "\n"),
		Focusables: []FocusableInfo{{
			ID: "child-view", OffsetX: 0, OffsetY: 0, Width: 10, Height: 3,
		}},
	}
}

func (s *scrollOwnerStub) Update(msg tea.Msg, focusID string) (string, tea.Cmd) { return "", nil }

func (s *scrollOwnerStub) OwnsScrollRegion(id string) bool { return id == "child-view" }

func (s *scrollOwnerStub) ScrollAtBoundary(delta int) bool {
	s.asked++
	if delta < 0 {
		return s.atTop
	}
	return s.atBottom
}

func TestWheelAtBoundaryDelegatesToScrollOwnerSection(t *testing.T) {
	child := &scrollOwnerStub{atTop: true}
	m := New("Owner").AddSection(child).AddSection(Text(longText(200)))
	h := mouse.NewHandler()
	m.Render(80, 24, h)

	cx, cy := centerOf(t, h, "child-view")
	if !m.WheelAtBoundary(wheel(cx, cy, false), h) {
		t.Error("expected the child's bounded answer for upward wheel")
	}
	if m.WheelAtBoundary(wheel(cx, cy, true), h) {
		t.Error("expected the child's movable answer for downward wheel")
	}
	if child.asked != 2 {
		t.Errorf("expected the child to answer twice, got %d", child.asked)
	}

	// Over the modal body the child must not be consulted.
	before := child.asked
	x, y := bodyPoint(t, h)
	m.WheelAtBoundary(wheel(x, y, true), h)
	if child.asked != before {
		t.Error("expected the child not to be consulted for body wheel")
	}
}

func TestWheelAtBoundarySkipsHiddenScrollOwner(t *testing.T) {
	child := &scrollOwnerStub{atTop: true, atBottom: true}
	visible := true
	m := New("Owner").AddSection(When(func() bool { return visible }, child)).AddSection(Text(longText(200)))
	h := mouse.NewHandler()
	m.Render(80, 24, h)
	cx, cy := centerOf(t, h, "child-view")

	if !m.WheelAtBoundary(wheel(cx, cy, true), h) {
		t.Error("expected a When-wrapped owner to answer while visible")
	}

	visible = false
	m.Render(80, 24, h)
	if child.asked != 1 {
		t.Fatalf("expected exactly one delegation so far, got %d", child.asked)
	}
	// Its region is gone; the pointer now lands on body or backdrop, and the
	// hidden child must not be asked.
	m.WheelAtBoundary(wheel(cx, cy, true), h)
	if child.asked != 1 {
		t.Errorf("expected a hidden owner not to be consulted, got %d calls", child.asked)
	}
}

func TestScrollingCustomOwnsItsRegion(t *testing.T) {
	atBottom := false
	s := ScrollingCustom(
		func(w int, focusID, hoverID string) RenderedSection {
			return RenderedSection{
				Content:    "custom",
				Focusables: []FocusableInfo{{ID: "doc", Width: 6, Height: 1}},
			}
		},
		nil,
		func(id string) bool { return id == "doc" },
		func(delta int) bool { return delta > 0 && atBottom },
	)
	m := New("Doc").AddSection(s)
	h := mouse.NewHandler()
	m.Render(80, 24, h)
	dx, dy := centerOf(t, h, "doc")

	if m.WheelAtBoundary(wheel(dx, dy, true), h) {
		t.Error("expected movable downward while the child reports movable")
	}
	atBottom = true
	if !m.WheelAtBoundary(wheel(dx, dy, true), h) {
		t.Error("expected bounded downward once the child reports its bottom")
	}
}

func TestScrollingCustomWithoutCallbacksNeverClaims(t *testing.T) {
	s := ScrollingCustom(func(w int, focusID, hoverID string) RenderedSection {
		return RenderedSection{Content: "x"}
	}, nil, nil, nil)
	owner, ok := asScrollOwner(s)
	if !ok {
		t.Fatal("expected ScrollingCustom to implement ScrollOwnerSection")
	}
	if owner.OwnsScrollRegion("anything") {
		t.Error("expected no region ownership without callbacks")
	}
	if owner.ScrollAtBoundary(1) {
		t.Error("expected unknown (false) without an atBoundary callback")
	}
}
