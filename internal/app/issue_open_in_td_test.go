package app

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/overview"
)

func assertTDJump(t *testing.T, cmd tea.Cmd, want string) {
	t.Helper()
	var focused, opened bool
	for _, msg := range collectMsgs(cmd) {
		switch m := msg.(type) {
		case FocusPluginByIDMsg:
			focused = m.PluginID == "td-monitor"
		case OpenFullIssueMsg:
			opened = m.IssueID == want
		}
	}
	if !focused || !opened {
		t.Fatalf("not the td jump for %q: focused=%v opened=%v", want, focused, opened)
	}
}

func TestOpenIssueInTDIsTheOneJump(t *testing.T) {
	assertTDJump(t, OpenIssueInTD("td-651ca2"), "td-651ca2")
	if cmd := OpenIssueInTD(""); cmd != nil {
		t.Fatal("an unnamed issue produced a jump")
	}
}

// The global Workspaces issue preview cannot reach the plugins itself; the app
// answers its request with the same jump every other surface makes.
func TestOverviewIssueRequestReachesTD(t *testing.T) {
	m := nativeTestModel(t, &nativeTestPlugin{})
	_, cmd := m.Update(overview.OpenIssueInTDMsg{IssueID: "td-651ca2"})
	assertTDJump(t, cmd, "td-651ca2")
}

// O is the key the issue panes answer, and the modal answers it too, so the
// same keystroke means the same thing wherever an issue is in front of the user.
func TestIssuePreviewModalAnswersOpenKeyInBothCases(t *testing.T) {
	for _, key := range []tea.KeyPressMsg{
		{Code: 'o', Text: "o"},
		{Code: 'O', Text: "O"},
	} {
		m := nativeTestModel(t, &nativeTestPlugin{focused: true})
		m.width, m.height = 100, 40
		m.showIssuePreview = true
		m.activeContext = "issue-preview"
		m.issuePreviewData = sampleIssuePreviewData()
		want := m.issuePreviewData.ID
		m.ensureIssuePreviewView()

		updated, cmd := m.Update(key)
		if got := asAppModel(t, updated); got.showIssuePreview {
			t.Fatalf("%q left the preview modal open", key.String())
		}
		assertTDJump(t, cmd, want)
	}
}
