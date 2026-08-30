package panereposition

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/ui"
)

func TestPaneHeaderFeatureGateAndDropOrder(t *testing.T) {
	features.Init(config.Default())
	features.SetOverride(features.PaneMove.Name, false)
	t.Cleanup(func() { features.Init(config.Default()) })
	off := ReserveHeader(30, true)
	if off.LayoutW != 0 || off.CloseW != ui.CloseButtonWidth() {
		t.Fatalf("disabled reserve = %+v", off)
	}

	features.SetOverride(features.PaneMove.Name, true)
	wide := ReserveHeader(30, true)
	if wide.LayoutW != ui.LayoutButtonWidth() || wide.CloseW != ui.CloseButtonWidth() {
		t.Fatalf("enabled reserve = %+v", wide)
	}
	narrow := ReserveHeader(ui.LayoutButtonWidth()+ui.CloseButtonWidth()-1, true)
	if narrow.LayoutW != 0 || narrow.CloseW != ui.CloseButtonWidth() {
		t.Fatalf("narrow reserve = %+v, want layout dropped before close", narrow)
	}
	row := ComposeHeader(strings.Repeat("t", narrow.TabsWidth), narrow.Width, true, false, false)
	plain := ansi.Strip(row)
	if strings.Contains(plain, ui.LayoutButtonLabel) || !strings.Contains(plain, ui.CloseButtonLabel) {
		t.Fatalf("narrow row = %q", plain)
	}
}
