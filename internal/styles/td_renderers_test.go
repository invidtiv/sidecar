package styles

import (
	"testing"

	"github.com/marcus/td/pkg/monitor"
)

func TestTDPanelRendererDerivesFromActiveTheme(t *testing.T) {
	t.Cleanup(func() {
		ApplyTheme("sidecar-modern")
	})

	ApplyTheme("sidecar-modern")
	panelRenderer := CreateTDPanelRenderer()

	// Active panel gradient
	activeOut := panelRenderer("content", 20, 5, monitor.PanelStateActive)
	if activeOut == "" {
		t.Fatal("expected non-empty active panel render")
	}

	// Normal panel gradient
	normalOut := panelRenderer("content", 20, 5, monitor.PanelStateNormal)
	if normalOut == "" {
		t.Fatal("expected non-empty normal panel render")
	}

	// Divider hover gradient
	dividerHoverOut := panelRenderer("content", 20, 5, monitor.PanelStateDividerHover)
	if dividerHoverOut == "" {
		t.Fatal("expected non-empty divider hover render")
	}

	// Divider active gradient
	dividerActiveOut := panelRenderer("content", 20, 5, monitor.PanelStateDividerActive)
	if dividerActiveOut == "" {
		t.Fatal("expected non-empty divider active render")
	}

	// Switch to dracula and ensure renderers reflect the new theme dynamically
	ApplyTheme("dracula")
	draculaActiveOut := panelRenderer("content", 20, 5, monitor.PanelStateActive)
	if draculaActiveOut == activeOut {
		t.Error("expected active panel gradient to change when theme changes")
	}
}

func TestTDModalRendererDerivesFromActiveTheme(t *testing.T) {
	t.Cleanup(func() {
		ApplyTheme("sidecar-modern")
	})

	ApplyTheme("sidecar-modern")
	modalRenderer := CreateTDModalRenderer()

	// Depth 1 modal
	d1 := modalRenderer("modal content", 30, 8, monitor.ModalTypeIssue, 1)
	if d1 == "" {
		t.Fatal("expected non-empty depth 1 modal render")
	}

	// Depth 2 modal
	d2 := modalRenderer("modal content", 30, 8, monitor.ModalTypeIssue, 2)
	if d2 == "" {
		t.Fatal("expected non-empty depth 2 modal render")
	}

	// Depth 3 modal
	d3 := modalRenderer("modal content", 30, 8, monitor.ModalTypeIssue, 3)
	if d3 == "" {
		t.Fatal("expected non-empty depth 3 modal render")
	}

	// Handoffs modal
	handoffs := modalRenderer("modal content", 30, 8, monitor.ModalTypeHandoffs, 1)
	if handoffs == "" {
		t.Fatal("expected non-empty handoffs modal render")
	}

	// Confirmation modal
	conf := modalRenderer("modal content", 30, 8, monitor.ModalTypeConfirmation, 1)
	if conf == "" {
		t.Fatal("expected non-empty confirmation modal render")
	}

	// Change theme and verify output changes accordingly
	ApplyTheme("dracula")
	draculaD1 := modalRenderer("modal content", 30, 8, monitor.ModalTypeIssue, 1)
	if draculaD1 == d1 {
		t.Error("expected depth 1 modal gradient to update when theme changes")
	}
}

func TestTDModalEmptySemanticFallbacksUseNeutralGray(t *testing.T) {
	t.Cleanup(func() {
		ApplyTheme("sidecar-modern")
	})

	blank := GetCurrentTheme()
	blank.Name = "td-empty-semantic"
	blank.Colors.Success = ""
	blank.Colors.Error = ""
	RegisterTheme(blank)
	ApplyTheme("td-empty-semantic")

	handoffs := getTDModalGradient(monitor.ModalTypeHandoffs, 1)
	if len(handoffs.Stops) == 0 {
		t.Fatal("handoffs gradient has no stops")
	}
	got := RGBToHex(handoffs.Stops[0].Color)
	if got != "#888888" {
		t.Errorf("empty Success fallback = %s, want #888888", got)
	}
	if got == "#5b8f63" {
		t.Error("empty Success still uses the hardcoded dark-theme green")
	}

	confirm := getTDModalGradient(monitor.ModalTypeConfirmation, 1)
	if len(confirm.Stops) == 0 {
		t.Fatal("confirmation gradient has no stops")
	}
	got = RGBToHex(confirm.Stops[0].Color)
	if got != "#888888" {
		t.Errorf("empty Error fallback = %s, want #888888", got)
	}
	if got == "#c06c64" {
		t.Error("empty Error still uses the hardcoded dark-theme red")
	}
}
