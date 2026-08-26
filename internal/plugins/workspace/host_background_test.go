package workspace

import (
	"testing"

	"github.com/marcus/sidecar/internal/termpreview"
)

func TestHostBackgroundReportReachesProjectTerminalProjection(t *testing.T) {
	p := &Plugin{}
	updated, _ := p.update(termpreview.HostBackgroundMsg{ANSI: "host-bg"})
	p = updated.(*Plugin)
	if p.terminalDefaultBackground != "host-bg" {
		t.Fatalf("project terminal background = %q, want host-bg", p.terminalDefaultBackground)
	}
}
