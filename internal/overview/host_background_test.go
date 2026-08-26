package overview

import (
	"testing"

	"github.com/marcus/sidecar/internal/termpreview"
)

func TestHostBackgroundReportReachesGlobalTerminalProjection(t *testing.T) {
	m := &Model{}
	m.Update(termpreview.HostBackgroundMsg{ANSI: "host-bg"})
	if m.terminalDefaultBackground != "host-bg" {
		t.Fatalf("global terminal background = %q, want host-bg", m.terminalDefaultBackground)
	}
}
