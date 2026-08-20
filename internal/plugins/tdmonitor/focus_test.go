package tdmonitor

import (
	"log/slog"
	"os"
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

// With the centre open td's three panels are a ring with one more stop: the
// centre belongs after the last panel going forward and before the first going
// back, and nowhere in between.
func TestTDMonitorRingWrapsAtTheLastPanel(t *testing.T) {
	p := liveMonitor(t)

	p.model.ActivePanel = monitor.PanelCurrentWork
	if p.AtFocusCycleEnd(false) {
		t.Fatal("the first panel is not the end of the forward cycle")
	}
	if !p.AtFocusCycleEnd(true) {
		t.Fatal("the first panel is where a reverse cycle wraps")
	}

	p.model.ActivePanel = monitor.PanelTaskList
	if p.AtFocusCycleEnd(false) || p.AtFocusCycleEnd(true) {
		t.Fatal("a middle panel is not a wrap point in either direction")
	}

	p.model.ActivePanel = monitor.PanelActivity
	if !p.AtFocusCycleEnd(false) {
		t.Fatal("the last panel is where a forward cycle wraps")
	}
	if p.AtFocusCycleEnd(true) {
		t.Fatal("the last panel is not the start of the ring")
	}
}

// The handback runs td's own panel command rather than assigning the panel, so
// the cursor clamping and scroll fix-up that go with a panel move still happen.
func TestTDMonitorFocusCycleStart(t *testing.T) {
	p := liveMonitor(t)

	p.model.ActivePanel = monitor.PanelActivity
	p.FocusCycleStart(false)
	if p.model.ActivePanel != monitor.PanelCurrentWork {
		t.Fatalf("forward handback focused panel %v, want the first", p.model.ActivePanel)
	}

	p.FocusCycleStart(true)
	if p.model.ActivePanel != monitor.PanelActivity {
		t.Fatalf("reverse handback focused panel %v, want the last", p.model.ActivePanel)
	}
}

// td binds tab to a modal's button cycle, to an epic's task section and to its
// forms and searches. Outside the main context the ring is not offered at all.
func TestTDMonitorOffersNoRingOutsideTheMainContext(t *testing.T) {
	p := liveMonitor(t)
	p.model.ActivePanel = monitor.PanelActivity
	p.model.HelpOpen = true
	if got := p.model.CurrentContextString(); got == "td-monitor" {
		t.Fatalf("expected a non-main context, got %q", got)
	}
	if p.AtFocusCycleEnd(false) || p.AtFocusCycleEnd(true) {
		t.Fatal("a td sub-context offered the centre a tab stop")
	}
}

// A plugin with no model yet — td not installed, or the build still in flight —
// answers no rather than reaching into a nil monitor.
func TestTDMonitorWithoutAModelHasNoRing(t *testing.T) {
	p := New()
	if p.AtFocusCycleEnd(false) || p.AtFocusCycleEnd(true) {
		t.Fatal("a plugin with no model claimed a wrap point")
	}
	if cmd := p.FocusCycleStart(false); cmd != nil {
		t.Fatal("a plugin with no model returned a handback command")
	}
}
