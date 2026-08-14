package issueview

import (
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func sample() *Data {
	return &Data{
		ID:          "td-abc123",
		Title:       "Extract the issue preview into a component",
		Status:      "in_progress",
		Type:        "task",
		Priority:    "P1",
		Points:      3,
		ParentID:    "td-parent1",
		Labels:      []string{"windowing", "refactor"},
		Description: "## Why\n\nThe modal owns fetch and render today.\n",
	}
}

func rows(t *testing.T, view string, width, height int) []string {
	t.Helper()
	lines := strings.Split(view, "\n")
	if len(lines) != height {
		t.Fatalf("got %d rows, want %d", len(lines), height)
	}
	for i, line := range lines {
		if w := ansi.StringWidth(line); w != width {
			t.Errorf("row %d is %d cells wide, want %d: %q", i, w, width, line)
		}
	}
	return lines
}

func TestModelRendersStandaloneAtItsSize(t *testing.T) {
	m := New(nil)
	m.SetSize(60, 12)
	apply(t, m, sample(), nil)

	lines := rows(t, m.View(), 60, 12)
	if !strings.Contains(lines[0], "td-abc123: Extract the issue preview") {
		t.Errorf("first row is not the heading: %q", lines[0])
	}
	joined := strings.Join(lines, "\n")
	for _, want := range []string{"[in_progress]  task  P1  3p", "Parent: td-parent1", "Labels: windowing, refactor", "Why"} {
		if !strings.Contains(joined, want) {
			t.Errorf("view missing %q:\n%s", want, joined)
		}
	}
}

func TestModelRendersLoadingAndError(t *testing.T) {
	m := New(nil)
	m.SetSize(40, 4)
	m.loading = true
	m.issueID = "td-abc123"
	if got := strings.Join(rows(t, m.View(), 40, 4), "\n"); !strings.Contains(got, "Loading issue") {
		t.Errorf("loading view missing its message:\n%s", got)
	}

	apply(t, m, nil, errors.New("issue \"td-abc123\" not found"))
	if got := strings.Join(rows(t, m.View(), 40, 4), "\n"); !strings.Contains(got, "not found") {
		t.Errorf("error view missing the failure:\n%s", got)
	}
}

func TestModelIgnoresStaleResults(t *testing.T) {
	m := New(nil)
	m.SetSize(40, 4)
	current := result(t, m, sample(), nil)

	stale := current
	stale.RequestGeneration--
	if m.SetResult(stale) {
		t.Error("a stale request generation was applied")
	}
	wrongIssue := current
	wrongIssue.IssueID = "td-other"
	if m.SetResult(wrongIssue) {
		t.Error("a result for another issue was applied")
	}
	if !m.SetResult(current) {
		t.Fatal("the current result was rejected")
	}
	if m.Title() != Heading(sample()) {
		t.Errorf("title is %q", m.Title())
	}
}

func TestModelScrollsWithinItsContent(t *testing.T) {
	m := New(nil)
	m.SetSize(40, 3)
	long := sample()
	long.Description = strings.Repeat("a line of prose\n\n", 30)
	apply(t, m, long, nil)

	first := m.View()
	m.Scroll(2)
	if m.View() == first {
		t.Error("scrolling did not move the viewport")
	}
	m.Scroll(-1000)
	if m.View() != first {
		t.Error("scrolling back to the top did not restore the first view")
	}
	m.Scroll(10000)
	rows(t, m.View(), 40, 3)
}

// result builds a LoadedMsg addressed to m's current load without running td.
func result(t *testing.T, m *Model, data *Data, err error) LoadedMsg {
	t.Helper()
	m.requestGeneration++
	m.issueID = sample().ID
	m.loading = true
	m.invalidateRender()
	return LoadedMsg{
		ModelID:           m.modelID,
		RequestGeneration: m.requestGeneration,
		Epoch:             m.epoch,
		IssueID:           m.issueID,
		Data:              data,
		Error:             err,
	}
}

// apply builds and applies a result, failing if the model rejects it.
func apply(t *testing.T, m *Model, data *Data, err error) {
	t.Helper()
	if !m.SetResult(result(t, m, data, err)) {
		t.Fatal("the model rejected its own result")
	}
}

func TestRenderPiecesOmitWhatIsMissing(t *testing.T) {
	empty := &Data{ID: "td-1"}
	if got := Heading(empty); got != "td-1" {
		t.Errorf("heading = %q", got)
	}
	if got := StatusLine(empty); got != "" {
		t.Errorf("status line = %q", got)
	}
	if got := ParentLine(empty); got != "" {
		t.Errorf("parent line = %q", got)
	}
	if got := LabelsLine(empty); got != "" {
		t.Errorf("labels line = %q", got)
	}
	if got := Description(nil, empty, 40); got != "" {
		t.Errorf("description = %q", got)
	}
	if got := Heading(nil); got != "" {
		t.Errorf("heading of nil data = %q", got)
	}
}

func TestExtractTdError(t *testing.T) {
	cases := map[string]string{
		"":                                    "",
		"ERROR: issue not found":              "issue not found",
		"usage: td show\nError: bad id":       "bad id",
		"noise\nERROR: first\nERROR: last":    "last",
		"nothing resembling a failure at all": "",
	}
	for in, want := range cases {
		if got := extractTdError(in); got != want {
			t.Errorf("extractTdError(%q) = %q, want %q", in, got, want)
		}
	}
}
