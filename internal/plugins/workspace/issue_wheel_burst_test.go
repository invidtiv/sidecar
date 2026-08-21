package workspace

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/issueview"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/tty"
)

// newIssueBurstPlugin is a project window whose issue leaf holds one long card,
// which is the surface mid-range trackpad inertia floods.
func newIssueBurstPlugin(t *testing.T) *Plugin {
	t.Helper()
	p := docPaneTestPlugin(t, t.TempDir(), true)
	p.issues = make(map[int]*issuePane)
	p.paneRoot = &PaneNode{ID: 10, Split: &PaneSplit{Axis: SplitCols, Ratio: 50,
		A: &PaneNode{ID: 1, Kind: PaneTerminal},
		B: &PaneNode{ID: 3, Kind: PaneIssue, ContentID: 3},
	}}
	p.paneFocus = 3
	pane := &issuePane{leafID: 3, root: p.ctx.WorkDir, surface: "shell:test-shell"}
	p.issues[3] = pane
	view := issueview.New(nil)
	view.SetSize(60, 10)
	view.SetData(&issueview.Data{
		ID:          "td-f100d1",
		Title:       "A long issue",
		Description: strings.Repeat("Paragraph line.\n\n", 200),
	})
	if _, created := pane.tabs.OpenOrFocus("td-f100d1", view); !created {
		t.Fatal("test premise: the issue tab was not created")
	}
	return p
}

func (p *Plugin) issueWheelAction(down bool) mouse.MouseAction {
	delta := -mouse.WheelScrollLines
	if down {
		delta = mouse.WheelScrollLines
	}
	return mouse.MouseAction{
		Type:   mouse.ActionScrollDown,
		Region: &mouse.Region{ID: regionPaneLeaf, Data: 3},
		X:      70, Y: 20,
		Delta: delta,
	}
}

// A dense flick moves the card only on the shared burst's apply points, and no
// input distance is lost: what the flushes applied plus what the burst still
// holds is everything the gesture delivered.
func TestIssueLeafWheelBurstKeepsTheGestureWhole(t *testing.T) {
	p := newIssueBurstPlugin(t)
	at := time.Unix(100, 0)
	p.clock = func() time.Time { return at }
	action := p.issueWheelAction(true)

	rawDistance := 0
	flushes := 0
	for i := range 40 {
		before := p.issues[3].view().ScrollOffset()
		p.handleMouseScroll(action)
		rawDistance += action.Delta
		if p.issues[3].view().ScrollOffset() != before {
			flushes++
		}
		at = at.Add(time.Duration(4+i/8) * time.Millisecond)
	}

	pending := p.issues[3].wheel.Pending()
	if got := p.issues[3].view().ScrollOffset() + pending; got != rawDistance {
		t.Fatalf("scrolled %d + pending %d = %d, want all %d input lines", p.issues[3].view().ScrollOffset(), pending, got, rawDistance)
	}
	if flushes == 0 || flushes >= 40 {
		t.Fatalf("%d repaints for 40 events, want immediate first movement and coalesced remainder", flushes)
	}
}

func TestIssueLeafWheelFirstEventIsImmediateAndDenseTailIsHeld(t *testing.T) {
	p := newIssueBurstPlugin(t)
	at := time.Unix(200, 0)
	p.clock = func() time.Time { return at }
	action := p.issueWheelAction(true)

	p.handleMouseScroll(action)
	if got := p.issues[3].view().ScrollOffset(); got != mouse.WheelScrollLines {
		t.Fatalf("first notch scrolled %d lines, want immediate movement", got)
	}
	at = at.Add(tty.WheelDebounceInterval / 2)
	p.handleMouseScroll(action)
	if got := p.issues[3].view().ScrollOffset(); got != mouse.WheelScrollLines {
		t.Fatalf("held notch moved scroll to %d, want it unchanged until the burst flushes", got)
	}
	if pending := p.issues[3].wheel.Pending(); pending != mouse.WheelScrollLines {
		t.Fatalf("held delta = %d, want the whole notch kept", pending)
	}
}

// The boundary filter drops inertia at the card's edges, and the drop must take
// any held delta with it — otherwise the next gesture's first flush spends it.
func TestIssueLeafBoundaryDropClearsHeldBurst(t *testing.T) {
	p := newIssueBurstPlugin(t)
	at := time.Unix(300, 0)
	p.clock = func() time.Time { return at }
	p.issues[3].wheel.Add(mouse.WheelScrollLines, at)
	p.issues[3].wheel.Add(mouse.WheelScrollLines, at.Add(time.Millisecond))
	if p.issues[3].wheel.Pending() == 0 {
		t.Fatal("test premise: burst has no held delta")
	}
	p.mouseHandler.HitMap.AddRect(regionPaneLeaf, 60, 0, 80, 36, 3)

	down := tea.MouseWheelMsg{X: 70, Y: 20, Button: tea.MouseWheelDown}
	p.issues[3].view().Scroll(100000)
	if !p.WheelAtBoundary(down) {
		t.Fatal("inertia at the last row was not identified as a boundary")
	}
	if pending := p.issues[3].wheel.Pending(); pending != 0 {
		t.Fatalf("held delta survived the boundary drop: %d", pending)
	}
	up := tea.MouseWheelMsg{X: 70, Y: 20, Button: tea.MouseWheelUp}
	if p.WheelAtBoundary(up) {
		t.Fatal("wheel back into the card was dropped")
	}
}
