package app

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/contentlink"
	"github.com/marcus/sidecar/internal/issueview"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/tty"
)

// longDeckIssueData gives a deck issue card enough rendered rows for a dense
// flick to travel without reaching the bottom.
func longDeckIssueData() *issueview.Data {
	return &issueview.Data{
		ID:          "td-beef01",
		Title:       "A long issue",
		Description: strings.Repeat("Paragraph line.\n\n", 200),
	}
}

// A dense flick over a deck issue leaf repaints only on the shared burst's
// apply points, and no input distance is lost: what the flushes applied plus
// what the leaf's burst still holds is everything the gesture delivered.
func TestAppDeckIssueWheelBurstKeepsTheGestureWhole(t *testing.T) {
	root := t.TempDir()
	p := &deckHostTestPlugin{id: "file-browser", focus: "preview", frame: "primary"}
	m := appDeckTestModel(t, root, p)
	m.renderContent(200, 40)
	h := m.currentContentDeck()
	m.openAppContent(root, p.id, contentlink.Ref{Kind: contentlink.KindIssue, Value: "td-beef01"})
	m.renderContent(200, 40)

	var leaf *panelayout.Node
	for _, placement := range h.layout.Leaves {
		if placement.Node.Kind == panelayout.Issue {
			leaf = placement.Node
		}
	}
	if leaf == nil {
		t.Fatal("the deck laid out no issue leaf")
	}
	view := h.deck.Viewer(leaf.ID).(*issueview.Model)
	view.SetData(longDeckIssueData())
	m.renderContent(200, 40)

	at := time.Unix(100, 0)
	h.wheelNow = func() time.Time { return at }
	rawDistance := 0
	flushes := 0
	for i := range 40 {
		before := view.ScrollOffset()
		h.handlePassiveMouse(tea.MouseWheelMsg{Button: tea.MouseWheelDown}, leaf)
		rawDistance++
		if view.ScrollOffset() != before {
			flushes++
		}
		at = at.Add(time.Duration(4+i/8) * time.Millisecond)
	}

	pending := h.wheel.For(issueWheelSurfaceKey(leaf.ID)).Pending()
	if got := view.ScrollOffset() + pending; got != rawDistance {
		t.Fatalf("scrolled %d + pending %d = %d, want all %d input lines", view.ScrollOffset(), pending, got, rawDistance)
	}
	if flushes == 0 || flushes >= 40 {
		t.Fatalf("%d repaints for 40 events, want immediate first movement and coalesced remainder", flushes)
	}
}

// The deck boundary filter drops inertia at the card's edges and must take any
// held delta with it.
func TestAppDeckIssueBoundaryDropClearsHeldBurst(t *testing.T) {
	root := t.TempDir()
	p := &deckHostTestPlugin{id: "file-browser", focus: "preview", frame: "primary"}
	m := appDeckTestModel(t, root, p)
	m.renderContent(200, 40)
	h := m.currentContentDeck()
	m.openAppContent(root, p.id, contentlink.Ref{Kind: contentlink.KindIssue, Value: "td-beef01"})
	m.renderContent(200, 40)

	var box mouse.Rect
	var leafID int
	for _, placement := range h.layout.Leaves {
		if placement.Node.Kind == panelayout.Issue {
			box = placement.Box
			leafID = placement.Node.ID
		}
	}
	if leafID == 0 {
		t.Fatal("the deck laid out no issue leaf")
	}
	view := h.deck.Viewer(leafID).(*issueview.Model)
	view.SetData(longDeckIssueData())
	view.Scroll(100000)

	burst := h.wheel.For(issueWheelSurfaceKey(leafID))
	at := time.Unix(300, 0)
	burst.Add(1, at)
	burst.Add(1, at.Add(time.Millisecond))
	if burst.Pending() == 0 {
		t.Fatal("test premise: burst has no held delta")
	}

	x, y := box.X+box.W/2, box.Y+box.H/2
	down := tea.MouseWheelMsg{X: x, Y: y, Button: tea.MouseWheelDown}
	bounded, owned := m.appContentWheelAtBoundary(down)
	if !owned || !bounded {
		t.Fatalf("inertia at the last row was not dropped: bounded=%v owned=%v", bounded, owned)
	}
	if pending := burst.Pending(); pending != 0 {
		t.Fatalf("held delta survived the boundary drop: %d", pending)
	}
	up := tea.MouseWheelMsg{X: x, Y: y, Button: tea.MouseWheelUp}
	if bounded, _ := m.appContentWheelAtBoundary(up); bounded {
		t.Fatal("wheel back into the card was dropped")
	}
}

// The preview modal drives the card's own viewport, so its flick coalesces too:
// one flush per earned window, nothing lost.
func TestIssuePreviewModalWheelBurstCoalesces(t *testing.T) {
	m := &Model{width: 100, height: 40, issuePreviewData: longDeckIssueData()}
	m.ensureIssuePreviewModal()
	view := m.ensureIssuePreviewView()
	if view == nil || m.issuePreviewModal == nil {
		t.Fatal("test premise: the preview modal did not build its card")
	}
	m.issuePreviewWheelNow = func() time.Time { return time.Unix(400, 0) }

	rawDistance := 0
	flushes := 0
	for range 40 {
		before := view.ScrollOffset()
		m.handleIssuePreviewMouse(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
		rawDistance += modalWheelLines
		if view.ScrollOffset() != before {
			flushes++
		}
	}
	pending := m.issuePreviewWheel.Pending()
	if got := view.ScrollOffset() + pending; got != rawDistance {
		t.Fatalf("scrolled %d + pending %d = %d, want all %d input lines", view.ScrollOffset(), pending, got, rawDistance)
	}
	if flushes == 0 || flushes >= 40 {
		t.Fatalf("%d repaints for 40 events, want immediate first movement and coalesced remainder", flushes)
	}

	// Closing the modal drops any held delta: it belonged to the closed card,
	// never to whatever the modal shows next.
	m.resetIssuePreview()
	if pending := m.issuePreviewWheel.Pending(); pending != 0 {
		t.Fatalf("held delta survived closing the modal: %d", pending)
	}
}

// The modal boundary answer resets the same burst Update applies through, so a
// tail dropped at the top of the card cannot leak into the next gesture.
func TestIssuePreviewModalBoundaryDropClearsHeldBurst(t *testing.T) {
	m := &Model{width: 100, height: 40, issuePreviewData: longDeckIssueData()}
	m.ensureIssuePreviewModal()
	view := m.ensureIssuePreviewView()
	view.Scroll(100000)

	at := time.Unix(500, 0)
	m.issuePreviewWheel = &tty.WheelBurst{}
	m.issuePreviewWheel.Add(modalWheelLines, at)
	m.issuePreviewWheel.Add(modalWheelLines, at.Add(time.Millisecond))
	if m.issuePreviewWheel.Pending() == 0 {
		t.Fatal("test premise: burst has no held delta")
	}

	if !m.issuePreviewWheelAtBoundary(tea.MouseWheelMsg{Button: tea.MouseWheelDown}) {
		t.Fatal("inertia at the last row was not identified as a boundary")
	}
	if pending := m.issuePreviewWheel.Pending(); pending != 0 {
		t.Fatalf("held delta survived the boundary drop: %d", pending)
	}
	if m.issuePreviewWheelAtBoundary(tea.MouseWheelMsg{Button: tea.MouseWheelUp}) {
		t.Fatal("wheel back into the card was dropped")
	}
}
