package app

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/plugin"
)

type wheelBoundaryPlugin struct {
	nativeTestPlugin
	atBoundary bool
	lastY      int
}

func (p *wheelBoundaryPlugin) WheelAtBoundary(msg tea.MouseWheelMsg) bool {
	p.lastY = msg.Mouse().Y
	return p.atBoundary
}

func TestFilterInputDropsOnlyConfirmedBoundaryWheelBeforeUpdate(t *testing.T) {
	p := &wheelBoundaryPlugin{atBoundary: true}
	m := routerTestModel(t, p)
	wheel := tea.MouseWheelMsg{X: 10, Y: headerHeight + 7, Button: tea.MouseWheelDown}

	if got := FilterInput(m, wheel); got != nil {
		t.Fatalf("boundary wheel survived filter as %T", got)
	}
	if p.lastY != 7 {
		t.Fatalf("plugin wheel Y = %d, want header-adjusted 7", p.lastY)
	}

	p.atBoundary = false
	if got := FilterInput(&m, wheel); got == nil {
		t.Fatal("movable wheel was dropped")
	}
	key := tea.KeyPressMsg{Code: 'j', Text: "j"}
	if got := FilterInput(m, key); got != key {
		t.Fatalf("non-wheel message changed: %#v", got)
	}
}

func TestFilterInputDoesNotAskCoveredPluginUnderModal(t *testing.T) {
	p := &wheelBoundaryPlugin{atBoundary: true, lastY: -1}
	m := routerTestModel(t, p)
	m.showQuitConfirm = true
	wheel := tea.MouseWheelMsg{X: 10, Y: 10, Button: tea.MouseWheelDown}

	if got := FilterInput(m, wheel); got == nil {
		t.Fatal("modal wheel was dropped using the covered plugin boundary")
	}
	if p.lastY != -1 {
		t.Fatalf("covered plugin was consulted under modal, Y=%d", p.lastY)
	}
}

var _ plugin.WheelBoundaryConsumer = (*wheelBoundaryPlugin)(nil)
