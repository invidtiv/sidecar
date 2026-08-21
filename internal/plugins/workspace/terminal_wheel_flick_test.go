package workspace

import (
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/tty"
)

// flickWheelPlugin draws both terminal surfaces at once — the panel and the
// preview — over buffers deep enough that neither reaches its bound, so where a
// flick lands is the flick's answer and not the bound's. gap is the interval
// between the events of the flick, read from the clock the burst takes its time
// from.
func flickWheelPlugin(t *testing.T, gap time.Duration) *Plugin {
	t.Helper()
	p := passiveWheelPanelPlugin(t)
	givePaneScrollableOutput(p, 300)
	at := time.Now()
	p.clock = func() time.Time {
		at = at.Add(gap)
		return at
	}
	if p.terminalMaxScroll(false) < 60 || p.terminalMaxScroll(true) < 60 {
		t.Fatalf("test premise: bounds preview=%d panel=%d are too shallow for the flick",
			p.terminalMaxScroll(false), p.terminalMaxScroll(true))
	}
	return p
}

// flickOver replays one trackpad flick over a region and reports where that
// surface's window ended up.
func flickOver(p *Plugin, region string, events int) {
	for range events {
		p.handleMouseScroll(mouse.MouseAction{
			Type: mouse.ActionScrollUp, Delta: -mouse.WheelScrollLines, X: 60, Y: 8,
			Region: &mouse.Region{ID: region},
		})
	}
}

// The same flick travels the same distance whichever terminal surface it lands
// on. The panel had no coalescing at all until this slice — it applied every raw
// event the trackpad emitted — so a flick there ran further than the identical
// flick over the preview beside it.
func TestAFlickTravelsTheSameDistanceOnBothTerminalSurfaces(t *testing.T) {
	const events = 20
	gap := tty.WheelDebounceInterval / 3

	panel := flickWheelPlugin(t, gap)
	flickOver(panel, regionTermPanelContent, events)

	preview := flickWheelPlugin(t, gap)
	flickOver(preview, regionPreviewPane, events)

	if panel.termPanelScroll == 0 {
		t.Fatal("the flick over the panel moved nothing")
	}
	if panel.termPanelScroll != preview.previewScroll {
		t.Fatalf("panel travelled %d rows, preview %d — one flick, two distances",
			panel.termPanelScroll, preview.previewScroll)
	}
	// The coalescing is real: an uncoalesced flick lands on every line of every
	// event it emitted.
	if panel.termPanelScroll >= events*mouse.WheelScrollLines {
		t.Fatalf("panel travelled %d rows for %d events: the flick was not coalesced",
			panel.termPanelScroll, events)
	}
}

// Each terminal surface holds its own flick. A pointer crossing from one to the
// other ends the flick it leaves rather than spending the lines it was holding
// on the surface it arrives at — which is what one burst per plugin did: the
// preview's held-back delta landed on the panel's first notch.
func TestCrossingBetweenTerminalSurfacesEndsTheFlickItLeaves(t *testing.T) {
	p := flickWheelPlugin(t, tty.WheelDebounceInterval/3)

	// Two notches over the preview: the first lands, the second is held back
	// inside the debounce window.
	flickOver(p, regionPreviewPane, 2)
	if p.previewScroll != mouse.WheelScrollLines {
		t.Fatalf("previewScroll = %d, want only the first notch (%d) landed",
			p.previewScroll, mouse.WheelScrollLines)
	}

	flickOver(p, regionTermPanelContent, 1)

	if p.termPanelScroll != mouse.WheelScrollLines {
		t.Fatalf("termPanelScroll = %d, want only the panel's own notch (%d)",
			p.termPanelScroll, mouse.WheelScrollLines)
	}
	if p.previewScroll != mouse.WheelScrollLines {
		t.Fatalf("previewScroll = %d, want the preview left where the pointer left it",
			p.previewScroll)
	}
	if pending := p.terminalWheel(false).Pending(); pending != 0 {
		t.Fatalf("the preview still holds %d lines of a flick the pointer has left", pending)
	}
}

func TestHeldWheelReusesOneProjectWorkspacesFrameOnBothTerminalSurfaces(t *testing.T) {
	for _, tc := range []struct {
		name   string
		region string
	}{
		{name: "primary preview", region: regionPreviewPane},
		{name: "terminal panel", region: regionTermPanelContent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := flickWheelPlugin(t, tty.WheelDebounceInterval/3)

			// The first notch applies immediately and must take a normal render.
			flickOver(p, tc.region, 1)
			if p.reuseHeldWheelViewOnce {
				t.Fatal("first immediate notch requested cached-frame reuse")
			}

			p.wheelViewCache = "already rendered"
			p.wheelViewCacheW, p.wheelViewCacheH = p.width, p.height
			p.wheelViewCacheOK = true
			flickOver(p, tc.region, 1)
			if !p.reuseHeldWheelViewOnce {
				t.Fatal("held notch did not request cached-frame reuse")
			}
			if got := p.View(p.width, p.height); got != "already rendered" {
				t.Fatalf("held notch rebuilt the Workspaces view, got %q", got)
			}
			if p.reuseHeldWheelViewOnce {
				t.Fatal("cached-frame reuse was not consumed after one View")
			}
		})
	}
}

// A notch is the reader reading this pane, forwarded or not: the surface's own
// capture cadence decays from that clock, so a locally scrolled pane must be
// repainted at the active tier rather than the idle one.
func TestALocalNotchCountsAsActivityOnTheSurfaceItScrolled(t *testing.T) {
	p := watchedWheelPlugin(t, false)
	stale := time.Now().Add(-time.Hour)
	p.primaryTerminal.State.LastKeyTime = stale

	p.handleMouseScroll(mouse.MouseAction{
		Type: mouse.ActionScrollUp, Delta: -1, X: 60, Y: 8,
		Region: &mouse.Region{ID: regionPreviewPane},
	})

	if got := p.primaryTerminal.State.LastKeyTime; !got.After(stale) {
		t.Fatal("a local notch left the pane's capture cadence decaying towards the idle tier")
	}
	if interval := tty.CalculatePollingInterval(p.primaryTerminal.State.LastKeyTime); interval != tty.PollingDecayFast {
		t.Fatalf("the scrolled pane repaints every %v, want the active tier %v",
			interval, tty.PollingDecayFast)
	}
}
