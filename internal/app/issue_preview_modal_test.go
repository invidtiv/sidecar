package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
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
		Labels:      []string{"windowing", "refactor"},
		Description: "## Why\n\nThe modal owns fetch and render today.\n\n- move fetch\n- move render\n",
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
	if got != string(want) {
		t.Errorf("modal rendering changed\n--- got ---\n%s\n--- want ---\n%s", got, want)
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
