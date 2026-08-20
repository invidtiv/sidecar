package tasks

import (
	"reflect"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/marcus/sidecar/internal/plugin"
)

func TestTasksProjectsVisibleStopsAndFocusesDirectly(t *testing.T) {
	p, _ := liveModel(t)
	p.View(100, 24)
	if got, want := p.PaneFocusStops(), []plugin.PaneFocusStop{{ID: "list"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("list stops = %#v, want %#v", got, want)
	}
	if cmd := p.SetPaneFocus("list"); cmd != nil || p.PaneFocus() != "list" {
		t.Fatalf("direct list focus cmd=%v focus=%q", cmd, p.PaneFocus())
	}
	p.SetPaneFocus("bogus")
	if p.PaneFocus() != "list" {
		t.Fatal("unknown focus mutated Tasks")
	}
	if got := p.ContentLinkSurfaces(); got != nil {
		t.Fatalf("unsafe Tasks link surface = %#v", got)
	}
}

func TestTasksPromptRetainsTabAndRefusesOuterFocus(t *testing.T) {
	p, _ := liveModel(t)
	p.View(100, 24)
	press(t, p, tea.KeyPressMsg{Code: tea.KeyTab})
	if got := p.PaneFocusStops(); got != nil {
		t.Fatalf("prompt-owned Tab exposed stops: %#v", got)
	}
	before := p.PaneFocus()
	p.SetPaneFocus("detail")
	if got := p.PaneFocus(); got != before {
		t.Fatalf("outer focus reached behind prompt: %q -> %q", before, got)
	}
}

func TestTasksOuterActiveChromeDoesNotChangeInnerFocus(t *testing.T) {
	p, _ := liveModel(t)
	p.View(100, 24)
	before := p.PaneFocus()
	p.SetPaneFocusActive(false)
	_ = p.View(100, 24)
	p.SetPaneFocusActive(true)
	_ = p.View(100, 24)
	if got := p.PaneFocus(); got != before {
		t.Fatalf("outer chrome changed Tasks focus: %q -> %q", before, got)
	}
}
