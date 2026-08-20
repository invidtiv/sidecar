package overview

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/terminallink"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

func commandNamed(m *Model, id string) bool {
	for _, cmd := range m.Commands() {
		if cmd.ID == id {
			return true
		}
	}
	return false
}

func TestWorkspaceFocusContextFollowsTheFocusedLeaf(t *testing.T) {
	stubPreviewTd(t)
	m := linkPreviewModel(t, workspaceinventory.KindWorktree)

	if got := m.WorkspaceFocusContext(); got != ctxGlobalWorkspaces {
		t.Fatalf("list context = %q", got)
	}

	run(t, m, m.openPreviewIssue("td-196c42"))
	if got := m.WorkspaceFocusContext(); got != ctxGlobalWorkspacesIssue {
		t.Fatalf("focused issue context = %q", got)
	}
	for _, want := range []string{"yank-issue", "yank-issue-key", "close", "open-item", "close-tab", "prev-tab", "next-tab"} {
		if !commandNamed(m, want) {
			t.Fatalf("issue Commands() omitted %s: %#v", want, m.Commands())
		}
	}
	for _, cmd := range m.Commands() {
		switch cmd.ID {
		case "close-tab":
			if cmd.Name != "Tab×" {
				t.Fatalf("close-tab name = %q, want Tab×", cmd.Name)
			}
		case "prev-tab":
			if cmd.Name != "Tab←" {
				t.Fatalf("prev-tab name = %q, want Tab←", cmd.Name)
			}
		case "next-tab":
			if cmd.Name != "Tab→" {
				t.Fatalf("next-tab name = %q, want Tab→", cmd.Name)
			}
		}
	}
	if commandNamed(m, "pin") {
		t.Fatalf("issue Commands() leaked the list: %#v", m.Commands())
	}

	run(t, m, openPreviewDocSpan(m, terminallink.Span{Kind: terminallink.KindFile, Value: "README.md"}))
	if got := m.WorkspaceFocusContext(); got != ctxGlobalWorkspacesDoc {
		t.Fatalf("focused document context = %q", got)
	}
	if !commandNamed(m, "yank-path") || !commandNamed(m, "close") {
		t.Fatalf("document Commands() = %#v", m.Commands())
	}
	if commandNamed(m, "yank-issue") {
		t.Fatalf("document Commands() leaked the issue: %#v", m.Commands())
	}

	m.workspaces.FocusFilter()
	if got := m.WorkspaceFocusContext(); got != ctxGlobalWorkspacesFilter {
		t.Fatalf("filter context = %q", got)
	}
}

func TestFocusedIssueYankKeysReturnACopyCommand(t *testing.T) {
	stubPreviewTd(t)
	m := linkPreviewModel(t, workspaceinventory.KindWorktree)
	run(t, m, m.openPreviewIssue("td-196c42"))
	if m.preview.issue == nil || m.preview.issue.view() == nil || m.preview.issue.view().Data() == nil {
		t.Fatal("issue did not load")
	}

	handled, cmd := m.previewIssueKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
	if !handled || cmd == nil {
		t.Fatalf("y on a loaded issue: handled=%v cmd=%v", handled, cmd != nil)
	}
	handled, cmd = m.previewIssueKey(tea.KeyPressMsg{Code: 'Y', Text: "Y"})
	if !handled || cmd == nil {
		t.Fatalf("Y on a loaded issue: handled=%v cmd=%v", handled, cmd != nil)
	}

	if m.preview.issue == nil || m.preview.issue.view() == nil || m.preview.issue.view().IssueID() != "td-196c42" {
		t.Fatalf("yank moved the issue: %#v", m.preview.issue)
	}
}
