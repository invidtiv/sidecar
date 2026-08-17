package overview

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

// O in the global issue preview asks for the same jump the project issue pane
// and the preview modal make. This surface cannot reach the app's plugins, so
// it raises the request and the app performs the jump.
func TestPreviewIssueOpensInTD(t *testing.T) {
	stubPreviewTd(t)
	m := linkPreviewModel(t, workspaceinventory.KindShell)
	run(t, m, m.openPreviewIssue("td-1111aa"))
	issue := m.preview.issue
	if issue == nil || issue.view() == nil {
		t.Fatal("no issue pane to focus")
	}
	issue.focused = true

	want := issue.view().SelectedID()
	handled, cmd := m.previewIssueKey(tea.KeyPressMsg{Code: 'O', Text: "O"})
	if !handled || cmd == nil {
		t.Fatalf("O on a focused issue preview: handled=%v cmd=%v", handled, cmd != nil)
	}
	msg, ok := cmd().(OpenIssueInTDMsg)
	if !ok {
		t.Fatalf("O produced %T, want OpenIssueInTDMsg", cmd())
	}
	if msg.IssueID != want {
		t.Fatalf("O asked for %q, want the selected issue %q", msg.IssueID, want)
	}

	issue.view().SetActive(true)
	issue.view().SetFocused(true)
	issue.view().SetSize(80, 20)
	if got := issue.view().View(); !strings.Contains(got, "O") || !strings.Contains(got, "td") {
		t.Fatalf("the ACTIONS row does not offer O:\n%s", got)
	}

	if got := m.WorkspaceFocusContext(); got != ctxGlobalWorkspacesIssue {
		t.Fatalf("focus context = %q, want the issue leaf's own", got)
	}
	var advertised bool
	for _, c := range m.Commands() {
		if c.ID == "open-in-td" {
			advertised = true
		}
	}
	if !advertised {
		t.Fatal("open-in-td is missing from the global issue pane's commands")
	}
}
