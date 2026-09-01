package overview

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/targetactivation"
)

// Slice 7: after resource admission, the per-kind refusals from slices 3–6
// still hold. A remote pane must not launch a viewer-local command against
// remote identity; in-document search and validated URLs stay available.

func TestRemoteActionAuditsHoldAfterResourceAdmission(t *testing.T) {
	t.Run("document e/ctrl+p/f refuse and / still searches", func(t *testing.T) {
		m, _ := showingRemoteResourceModel(t)
		resolver := &fakeResolver{}
		m.SetResourceResolver(resolver.resolve)
		runRemoteDescribe(t, m)
		cmd, handled := m.activatePreviewPlan(targetactivation.Plan{
			Kind: targetactivation.PlanOpenResource, Provider: "jira-work", Matcher: "project-key", Locator: "CASH-1245",
		})
		if !handled || cmd == nil {
			t.Fatalf("resource open handled=%v cmd=%v", handled, cmd != nil)
		}
		run(t, m, cmd)
		if refs := resolver.refs(); len(refs) != 0 {
			t.Fatalf("resource pane asked the local manager: %v", refs)
		}

		openRemoteTwin(t, m, 0)
		if !m.docPaneFocused() {
			m.focusPreviewPane(panelayout.Document)
		}
		if !m.docPaneFocused() {
			t.Fatal("document pane is not focused")
		}
		for _, tc := range []struct {
			key    tea.KeyPressMsg
			want   string
			action string
		}{
			{tea.KeyPressMsg{Code: 'e', Text: "e"}, "Inline editing", "e"},
			{tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl}, "File finding", "ctrl+p"},
			{tea.KeyPressMsg{Code: 'f', Text: "f"}, "Project search", "f"},
		} {
			handled, cmd := m.WorkspacesKey(tc.key)
			if !handled {
				t.Fatalf("%s was not handled after a resource pane opened", tc.action)
			}
			toast, ok := toastFrom(t, cmd)
			if !ok || !strings.Contains(toast.Message, tc.want) || !strings.Contains(toast.Message, "mac-mini") {
				t.Fatalf("%s toast = %#v", tc.action, toast)
			}
			if m.preview.doc.editing() {
				t.Fatalf("%s started an inline editor", tc.action)
			}
			if m.preview.doc.mode != nil {
				t.Fatalf("%s opened a local finder/search", tc.action)
			}
		}
		if !pressWorkspaces(t, m, tea.KeyPressMsg{Code: '/', Text: "/"}) {
			t.Fatal("/ was not handled")
		}
		if !m.preview.doc.view().SearchActive() {
			t.Fatal("in-document search did not start")
		}
	})

	t.Run("issue Open in td refuses", func(t *testing.T) {
		m, _ := showingRemoteIssueNoteModel(t)
		run(t, m, m.openPreviewIssue("td-a4dd72"))
		if m.preview.issue == nil || m.preview.issue.view() == nil {
			t.Fatal("no remote issue pane")
		}
		m.preview.issue.focused = true
		m.preview.issue.view().SetFocused(true)
		m.preview.issue.view().SetSize(80, 16)
		handled, cmd := m.previewIssueKey(tea.KeyPressMsg{Code: 'O', Text: "O"})
		if !handled || cmd == nil {
			t.Fatalf("O on remote issue: handled=%v cmd=%v", handled, cmd != nil)
		}
		toast, ok := toastFrom(t, cmd)
		if !ok || !strings.Contains(toast.Message, "Open in td") || !strings.Contains(toast.Message, "mac-mini") {
			t.Fatalf("remote O toast = %#v", toast)
		}
		if _, isJump := cmd().(OpenIssueInTDMsg); isJump {
			t.Fatal("remote O sent OpenIssueInTDMsg")
		}
	})

	t.Run("resource never uses local manager and URLs stay local", func(t *testing.T) {
		m, _ := showingRemoteResourceModel(t)
		resolver := &fakeResolver{}
		m.SetResourceResolver(resolver.resolve)
		runRemoteDescribe(t, m)
		resourceSpansOn(t, m, resourceLine)
		m.WorkspacesView(previewWide, previewTall)
		cmd, claimed := m.activatePreviewLinkAt(previewNeedleAction(t, m, "CASH-1245"), false)
		if !claimed || cmd == nil {
			t.Fatal("resource click was not claimed")
		}
		run(t, m, cmd)
		if refs := resolver.refs(); len(refs) != 0 {
			t.Fatalf("remote resource asked the local manager: %v", refs)
		}
		if m.preview.resource == nil || m.preview.resource.pane == nil {
			t.Fatal("no resource pane")
		}
		if open := m.preview.resource.pane.OpenSource(); open == nil {
			t.Fatal("validated source URL produced no open command")
		}
	})
}
