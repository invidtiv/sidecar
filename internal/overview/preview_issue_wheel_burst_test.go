package overview

import (
	"strings"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/issueview"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

// openLongPreviewIssue opens one issue pane holding far more rendered rows than
// its viewport, which is the surface mid-range trackpad inertia floods.
func openLongPreviewIssue(t *testing.T) (*Model, *previewIssue) {
	t.Helper()
	stubPreviewTd(t)
	m := linkPreviewModel(t, workspaceinventory.KindWorktree)
	m.WorkspacesView(previewWide, previewTall)
	run(t, m, m.openPreviewIssue("td-196c42"))
	m.WorkspacesView(previewWide, previewTall)
	issue := m.preview.issue
	if issue == nil || issue.view() == nil {
		t.Fatal("the preview opened no issue pane")
	}
	issue.view().SetData(&issueview.Data{
		ID:          "td-beef01",
		Title:       "A long issue",
		Description: strings.Repeat("Paragraph line.\n\n", 200),
	})
	return m, issue
}

// A dense flick moves the card only on the shared burst's apply points, and no
// input distance is lost: what the flushes applied plus what the burst still
// holds is everything the gesture delivered.
func TestPreviewIssueWheelBurstKeepsTheGestureWhole(t *testing.T) {
	m, issue := openLongPreviewIssue(t)
	at := time.Unix(100, 0)
	m.collector.Now = func() time.Time { return at }

	rawDistance := 0
	flushes := 0
	for i := range 40 {
		before := issue.view().ScrollOffset()
		m.scrollPreviewIssueByWheel(mouse.WheelScrollLines)
		rawDistance += mouse.WheelScrollLines
		if issue.view().ScrollOffset() != before {
			flushes++
		}
		at = at.Add(time.Duration(4+i/8) * time.Millisecond)
	}

	pending := issue.wheel.Pending()
	if got := issue.view().ScrollOffset() + pending; got != rawDistance {
		t.Fatalf("scrolled %d + pending %d = %d, want all %d input lines", issue.view().ScrollOffset(), pending, got, rawDistance)
	}
	if flushes == 0 || flushes >= 40 {
		t.Fatalf("%d repaints for 40 events, want immediate first movement and coalesced remainder", flushes)
	}
}

// The boundary filter drops inertia at the card's edges, and the drop must take
// any held delta with it — otherwise the next gesture's first flush spends it.
func TestPreviewIssueWheelAtBoundaryDropsHeldBurst(t *testing.T) {
	m, issue := openLongPreviewIssue(t)
	issue.view().Scroll(100000)

	at := time.Unix(300, 0)
	m.collector.Now = func() time.Time { return at }
	issue.wheel.Add(mouse.WheelScrollLines, at)
	issue.wheel.Add(mouse.WheelScrollLines, at.Add(time.Millisecond))
	if issue.wheel.Pending() == 0 {
		t.Fatal("test premise: burst has no held delta")
	}

	if !m.previewIssueWheelAtBoundary(mouse.WheelScrollLines) {
		t.Fatal("inertia at the last row was not identified as a boundary")
	}
	if pending := issue.wheel.Pending(); pending != 0 {
		t.Fatalf("held delta survived the boundary drop: %d", pending)
	}
	if m.previewIssueWheelAtBoundary(-mouse.WheelScrollLines) {
		t.Fatal("wheel back into the card was dropped")
	}
}
