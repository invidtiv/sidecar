package workspace

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/app"
)

// collectMsgs flattens whatever a command produced into the messages a running
// program would see, so a batched jump can be asserted on as a whole.
func collectMsgs(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range batch {
			out = append(out, collectMsgs(c)...)
		}
		return out
	}
	if msg == nil {
		return nil
	}
	return []tea.Msg{msg}
}

// O on a focused issue leaf is the same jump the issue preview modal makes:
// focus td and open the issue there.
func TestOpenFocusedIssueInTDFromWorkspacePane(t *testing.T) {
	stubTd(t)
	root := t.TempDir()
	p := docPaneTestPlugin(t, root, true)
	steelThreadPaneTree(t, p, root)
	issue, leaf := p.activeIssuePane()
	p.paneFocus = leaf.ID
	p.activePane = PanePreview

	want := issue.view().SelectedID()
	if want == "" {
		t.Fatal("the pane has no issue to open")
	}

	handled, cmd := p.handleIssueKey(tea.KeyPressMsg{Code: 'O', Text: "O"})
	if !handled || cmd == nil {
		t.Fatalf("O on a focused issue leaf: handled=%v cmd=%v", handled, cmd != nil)
	}

	var focused, opened bool
	for _, msg := range collectMsgs(cmd) {
		switch m := msg.(type) {
		case app.FocusPluginByIDMsg:
			focused = m.PluginID == "td-monitor"
		case app.OpenFullIssueMsg:
			opened = m.IssueID == want
		}
	}
	if !focused || !opened {
		t.Fatalf("O did not make the td jump: focused=%v opened=%v", focused, opened)
	}

	// The pane advertises the key it now answers.
	issue.view().SetActive(true)
	issue.view().SetFocused(true)
	issue.view().SetSize(80, 20)
	if got := issue.view().View(); !strings.Contains(got, "O") || !strings.Contains(got, "td") {
		t.Fatalf("the ACTIONS row does not offer O:\n%s", got)
	}

	var advertised bool
	for _, c := range p.Commands() {
		if c.ID == "open-in-td" && c.Context == "workspace-issue" {
			advertised = true
		}
	}
	if !advertised {
		t.Fatal("open-in-td is missing from the focused issue pane's commands")
	}
}
