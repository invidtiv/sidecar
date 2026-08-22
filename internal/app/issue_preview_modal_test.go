package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/issueview"
	"github.com/marcus/sidecar/internal/mouse"
)

// sampleIssuePreviewData is the fixture both the modal golden and the
// standalone component test render, so the two surfaces stay comparable.
func sampleIssuePreviewData() *IssuePreviewData {
	return &IssuePreviewData{
		ID:          "td-abc123",
		Title:       "Extract the issue preview into a component",
		Status:      "in_progress",
		Type:        "task",
		Priority:    "P1",
		Points:      3,
		ParentID:    "td-parent1",
		Parent:      &issueview.Ref{ID: "td-parent1", Title: "Windowing epic", Type: "epic", Status: "in_progress"},
		Labels:      []string{"windowing", "refactor"},
		CreatedAt:   "2026-08-10T18:42:00-07:00",
		Description: "## Why\n\nThe modal owns fetch and render today.\n\n- move fetch\n- move render\n",
		Children: []issueview.Ref{
			{ID: "td-83cfc9", Title: "Snapshot diff", Status: "closed", Type: "task"},
			{ID: "td-3d427b", Title: "Icon registry", Status: "closed", Type: "feature"},
			{ID: "td-ae3a2a", Title: "Card gutter", Status: "open", Type: "task"},
		},
		Logs: []issueview.Log{
			{Timestamp: "2026-08-10T21:31:00-07:00", Session: "ses_2a31ff", Message: "Started work"},
			{Timestamp: "2026-08-10T21:47:00-07:00", Session: "ses_2a31ff", Message: "Checks passing"},
		},
	}
}

// TestIssuePreviewModalMatchesGolden pins the modal's rendering so the
// extraction into internal/issueview cannot change what the user sees.
func TestIssuePreviewModalMatchesGolden(t *testing.T) {
	m := &Model{width: 100, height: 40, issuePreviewData: sampleIssuePreviewData()}
	m.ensureIssuePreviewModal()
	if m.issuePreviewModal == nil {
		t.Fatal("expected a preview modal for populated data")
	}
	// Styling is compared out: the hint colors follow whichever theme another
	// test in this package installed. The golden pins layout and text.
	got := ansi.Strip(m.issuePreviewModal.Render(m.width, m.height, mouse.NewHandler()))

	golden := filepath.Join("testdata", "issue-preview-modal.txt")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	wantView := strings.TrimSuffix(string(want), "\n")
	if got != wantView {
		t.Errorf("modal rendering changed\n--- got ---\n%s\n--- want ---\n%s", got, wantView)
	}
}

func TestWorkspaceIssueLoadIsNotSwallowedByThePreviewHost(t *testing.T) {
	// A td link click in a workspace pane broadcasts issueview.LoadedMsg.
	// The preview modal is one host of that type; it must not eat a load
	// that belongs to a pane, or the pane stays on "Loading issue…".
	probe := &nativeTestPlugin{focused: true}
	m := nativeTestModel(t, probe)
	msg := issueview.LoadedMsg{ModelID: 3, RequestGeneration: 1, IssueID: "td-651ca2"}
	if _, cmd := m.Update(msg); cmd != nil {
		t.Fatalf("forwarded load returned a cmd: %v", cmd != nil)
	}
	if len(probe.seen) != 1 {
		t.Fatalf("plugin saw %d messages, want the workspace load", len(probe.seen))
	}
	got, ok := probe.seen[0].(issueview.LoadedMsg)
	if !ok || got.IssueID != "td-651ca2" {
		t.Fatalf("plugin saw %#v, want the workspace LoadedMsg", probe.seen[0])
	}

	m.showIssuePreview = true
	m.issuePreviewView = issueview.New(nil)
	_ = m.issuePreviewView.Load(issuePreviewModelID, "", "td-other", 0)
	probe.seen = nil
	if _, claimed := m.claimIssuePreviewLoad(msg); claimed {
		t.Fatal("the modal claimed a load addressed to a workspace pane")
	}
}

func TestIssuePreviewEscDeactivatesBeforeClose(t *testing.T) {
	m := nativeTestModel(t, &nativeTestPlugin{focused: true})
	m.width, m.height = 100, 40
	m.showIssuePreview = true
	m.issuePreviewData = sampleIssuePreviewData()
	view := m.ensureIssuePreviewView()
	view.SetActive(true)
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	got := asAppModel(t, updated)
	if !got.showIssuePreview {
		t.Fatal("first Esc closed the preview instead of leaving the card")
	}
	if got.issuePreviewView == nil || got.issuePreviewView.Active() {
		t.Fatal("first Esc did not deactivate the card")
	}
	updated, _ = got.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	got = asAppModel(t, updated)
	if got.showIssuePreview {
		t.Fatal("second Esc did not close the preview")
	}
}

func TestIssuePreviewActivateGatesArrowNavigation(t *testing.T) {
	m := &Model{width: 100, height: 40, issuePreviewData: sampleIssuePreviewData()}
	view := m.ensureIssuePreviewView()
	if view == nil {
		t.Fatal("expected a view for populated data")
	}
	if view.Active() {
		t.Fatal("the card must start inactive so arrows still scroll the modal")
	}
	before := view.IssueID()
	if handled, cmd := view.HandleKey(tea.KeyPressMsg{Code: tea.KeyUp}); !handled || cmd != nil {
		t.Fatalf("inactive up: handled=%v cmd=%v", handled, cmd != nil)
	}
	if view.IssueID() != before {
		t.Fatalf("inactive up navigated to %q", view.IssueID())
	}

	view.SetActive(true)
	if _, cmd := view.HandleKey(tea.KeyPressMsg{Code: tea.KeyUp}); cmd != nil {
		t.Fatal("up without a workDir should not fetch")
	}
	if view.IssueID() != "td-parent1" {
		t.Fatalf("active up went to %q, want the parent epic", view.IssueID())
	}
}

// TestIssuePreviewModalStates keeps the loading and error modals intact.
func TestIssuePreviewModalStates(t *testing.T) {
	loading := &Model{width: 100, height: 40, issuePreviewLoading: true}
	loading.ensureIssuePreviewModal()
	if loading.issuePreviewModal == nil {
		t.Fatal("expected a loading modal")
	}
	if got := loading.issuePreviewModal.Render(100, 40, mouse.NewHandler()); !strings.Contains(got, "Fetching issue data") {
		t.Errorf("loading modal missing its message:\n%s", got)
	}

	failed := &Model{width: 100, height: 40, issuePreviewError: os.ErrNotExist}
	failed.ensureIssuePreviewModal()
	if failed.issuePreviewModal == nil {
		t.Fatal("expected an error modal")
	}
	if got := failed.issuePreviewModal.Render(100, 40, mouse.NewHandler()); !strings.Contains(got, "Issue Not Found") {
		t.Errorf("error modal missing its title:\n%s", got)
	}

	empty := &Model{width: 100, height: 40}
	empty.ensureIssuePreviewModal()
	if empty.issuePreviewModal != nil {
		t.Error("expected no modal without data")
	}
}
