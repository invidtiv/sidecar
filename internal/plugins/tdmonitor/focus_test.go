package tdmonitor

import (
	"log/slog"
	"os"
	"reflect"
	"testing"

	"github.com/marcus/td/pkg/monitor"

	"github.com/marcus/sidecar/internal/plugin"
)

// liveMonitor is a plugin hosting a real td model over an isolated database,
// which is the only way to ask td where its panel cycle stands.
func liveMonitor(t *testing.T) *Plugin {
	t.Helper()
	p := New()
	ctx := &plugin.Context{
		WorkDir: tempTdProject(t),
		Logger:  slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
	}
	if err := p.Init(ctx); err != nil {
		t.Fatalf("init: %v", err)
	}
	startAndSettle(t, p)
	if p.model == nil {
		t.Fatal("no td model was built")
	}
	t.Cleanup(p.Stop)
	return p
}

func TestTDMonitorProjectsStableStopsAndFocusesDirectly(t *testing.T) {
	p := liveMonitor(t)
	p.View(120, 36)
	want := []plugin.PaneFocusStop{{ID: "current-work"}, {ID: "task-list"}, {ID: "activity"}}
	if got := p.PaneFocusStops(); !reflect.DeepEqual(got, want) {
		t.Fatalf("stops = %#v, want %#v", got, want)
	}
	if cmd := p.SetPaneFocus("activity"); cmd != nil {
		t.Fatal("direct focus unexpectedly returned a command")
	}
	if got := p.PaneFocus(); got != "activity" || p.model.ActivePanel != monitor.PanelActivity {
		t.Fatalf("focus=%q panel=%v", got, p.model.ActivePanel)
	}
	p.SetPaneFocus("bogus")
	if p.model.ActivePanel != monitor.PanelActivity {
		t.Fatal("unknown direct focus mutated td")
	}
	if got := p.ContentLinkSurfaces(); got != nil {
		t.Fatalf("unsafe td link surface = %#v", got)
	}
}

func TestTDMonitorResponsiveAndOverlayTabOwnership(t *testing.T) {
	p := liveMonitor(t)
	p.View(monitor.MinWidth-1, monitor.MinHeight)
	if got := p.PaneFocusStops(); len(got) != 0 {
		t.Fatalf("compact replacement exposed stops: %#v", got)
	}
	p.View(120, 36)
	p.model.HelpOpen = true
	before := p.model.ActivePanel
	if got := p.PaneFocusStops(); len(got) != 0 {
		t.Fatalf("overlay-owned Tab exposed stops: %#v", got)
	}
	p.SetPaneFocus("task-list")
	if p.model.ActivePanel != before {
		t.Fatal("direct focus reached behind overlay")
	}
}

func TestTDMonitorWithoutModelHasNoCapabilities(t *testing.T) {
	p := New()
	if got := p.PaneFocusStops(); got != nil || p.PaneFocus() != "" {
		t.Fatalf("nil model capability: stops=%#v focus=%q", got, p.PaneFocus())
	}
	if cmd := p.SetPaneFocus("activity"); cmd != nil {
		t.Fatal("nil model returned a focus command")
	}
}
