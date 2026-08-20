package workspace

import (
	"testing"

	"github.com/marcus/sidecar/internal/app"
)

// TestPublicPaneMessagesOpenTheSamePanesAsAClick proves the seam is real: the
// message an outside surface sends lands on the same pane the terminal click
// path opens.
func TestPublicPaneMessagesOpenTheSamePanesAsAClick(t *testing.T) {
	stubTd(t)
	root := t.TempDir()
	p := docPaneTestPlugin(t, root, true)
	if cmd := p.openIssuePaneMsg(app.OpenIssuePaneMsg{Issue: "td-1111aa"}); cmd == nil {
		t.Fatal("OpenIssuePaneMsg opened nothing")
	}
	issue, _ := p.activeIssuePane()
	if issue == nil || issue.tabs.Find("td-1111aa") < 0 {
		t.Fatal("OpenIssuePaneMsg did not open the issue pane")
	}

	p.openDiffPaneMsg(app.OpenDiffPaneMsg{Spec: "wt"})
	diff, _ := p.activeDiffPane()
	if diff == nil {
		t.Fatal("OpenDiffPaneMsg did not open the diff pane")
	}
}

// TestAttachSessionMessageHonoursTheFeatureGate keeps the public entry as
// gated as the private path it wraps.
func TestAttachSessionMessageHonoursTheFeatureGate(t *testing.T) {
	p := New()
	if fullTmuxAttachEnabled() {
		t.Skip("full tmux attach is enabled; the gate is not the behaviour under test")
	}
	if cmd := p.attachSessionMsg(app.AttachSessionMsg{Session: "anything"}); cmd != nil {
		t.Fatal("attach by name ran with full tmux attach disabled")
	}
}
