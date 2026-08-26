package app

import (
	"fmt"
	"image/color"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/styles"
)

func TestInitRequestsTheHostBackgroundColor(t *testing.T) {
	m, _ := scopeBaselineModel(t, "git")
	found := false
	for _, msg := range collectMsgs(m.Init()) {
		// Bubble Tea intentionally keeps the request message private; its
		// concrete package/type name is the observable command contract.
		if strings.HasSuffix(fmt.Sprintf("%T", msg), ".backgroundColorMsg") {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("Init did not request the host terminal background")
	}
}

func TestHostBackgroundReportReachesProjectPlugins(t *testing.T) {
	m, plugins := scopeBaselineModel(t, "git")
	host := color.RGBA{R: 40, G: 43, B: 51, A: 255}
	updated, _ := m.Update(tea.BackgroundColorMsg{Color: host})
	_ = asAppModel(t, updated)

	want := styles.BgANSISeqFor(host)
	for name, p := range plugins {
		if p.hostBackground != want {
			t.Errorf("plugin %s background = %q, want %q", name, p.hostBackground, want)
		}
	}
}
