package issueview

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
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
		Parent:      &Ref{ID: "td-parent1", Title: "Windowing epic", Type: "epic", Status: "in_progress"},
		Labels:      []string{"windowing", "refactor"},
		CreatedAt:   "2026-08-10T18:42:00-07:00",
		Description: "## Why\n\nThe modal owns fetch and render today.\n",
		Children: []Ref{
			{ID: "td-83cfc9", Title: "Snapshot diff", Status: "closed", Type: "task"},
			{ID: "td-3d427b", Title: "Icon registry", Status: "closed", Type: "feature"},
			{ID: "td-ae3a2a", Title: "Card gutter", Status: "open", Type: "task"},
		},
		Siblings: []Ref{
			{ID: "td-sib1", Title: "First story", Status: "closed", Type: "task"},
			{ID: "td-abc123", Title: "Extract the issue preview into a component", Status: "in_progress", Type: "task"},
			{ID: "td-sib3", Title: "Third story", Status: "open", Type: "task"},
		},
		Logs: []Log{
			{Timestamp: "2026-08-10T21:31:00-07:00", Session: "ses_2a31ff", Message: "Started work"},
			{Timestamp: "2026-08-10T21:47:00-07:00", Session: "ses_2a31ff", Message: "Checks passing"},
		},
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
			t.Errorf("row %d is %d cells wide, want %d: %q", i, w, width, ansi.Strip(line))
		}
	}
	return lines
}

func stripped(view string) string {
	return ansi.Strip(view)
}

func TestModelRendersStandaloneAtItsSize(t *testing.T) {
	m := New(nil)
	m.SetSize(60, 24)
	apply(t, m, sample(), nil)

	lines := rows(t, m.View(), 60, 24)
	joined := stripped(strings.Join(lines, "\n"))
	for _, want := range []string{
		"IN PROGRESS",
		"td-abc123",
		"Extract the issue preview into a component",
		"P1",
		"3pts",
		"task",
		"Labels: windowing, refactor",
		"DESCRIPTION",
		"Why",
		"SUBTASKS",
		"td-83cfc9",
		"Card gutter",
		"RECENT LOGS",
		"Started work",
		"PARENT",
		"td-parent1",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("view missing %q:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "[in_progress]") {
		t.Errorf("view still uses the old bracket status line:\n%s", joined)
	}
}

func TestModelRendersLoadingAndError(t *testing.T) {
	m := New(nil)
	m.SetSize(40, 4)
	m.loading = true
	m.issueID = "td-abc123"
	if got := stripped(strings.Join(rows(t, m.View(), 40, 4), "\n")); !strings.Contains(got, "Loading issue") {
		t.Errorf("loading view missing its message:\n%s", got)
	}

	apply(t, m, nil, errors.New("issue \"td-abc123\" not found"))
	if got := stripped(strings.Join(rows(t, m.View(), 40, 4), "\n")); !strings.Contains(got, "not found") {
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

func TestInactiveArrowsScrollInsteadOfNavigating(t *testing.T) {
	m := New(nil)
	m.SetSize(50, 6)
	apply(t, m, sample(), nil)
	m.SetActive(false)

	before := m.IssueID()
	handled, cmd := m.handleKeyString("up")
	if !handled || cmd != nil {
		t.Fatalf("inactive up: handled=%v cmd=%v", handled, cmd != nil)
	}
	if m.IssueID() != before {
		t.Fatalf("inactive up navigated to %q", m.IssueID())
	}
	handled, cmd = m.handleKeyString("left")
	if handled || cmd != nil {
		t.Fatalf("inactive left should be unclaimed: handled=%v cmd=%v", handled, cmd != nil)
	}
}

func TestActiveArrowsWalkParentAndSiblings(t *testing.T) {
	m := New(nil)
	m.SetSize(60, 16)
	apply(t, m, sample(), nil)
	m.SetActive(true)

	// Up from the issue itself loads the parent.
	_, cmd := m.handleKeyString("up")
	if cmd != nil {
		t.Fatal("up without workDir should not fetch")
	}
	if m.IssueID() != "td-parent1" {
		t.Fatalf("up went to %q, want the parent", m.IssueID())
	}

	// Restore and walk siblings.
	apply(t, m, sample(), nil)
	m.SetActive(true)
	m.handleKeyString("right")
	if m.IssueID() != "td-sib3" {
		t.Fatalf("right went to %q, want td-sib3", m.IssueID())
	}
	apply(t, m, sample(), nil)
	m.SetActive(true)
	m.handleKeyString("left")
	if m.IssueID() != "td-sib1" {
		t.Fatalf("left went to %q, want td-sib1", m.IssueID())
	}
}

func TestActiveDownSelectsSubtasksAndEnterOpensThem(t *testing.T) {
	m := New(nil)
	m.SetSize(60, 20)
	apply(t, m, sample(), nil)
	m.SetActive(true)

	_, _ = m.handleKeyString("down") // parent row
	_, _ = m.handleKeyString("down") // first child
	if got := m.SelectedID(); got != "td-83cfc9" {
		t.Fatalf("first down-down selected %q, want td-83cfc9", got)
	}
	_, _ = m.handleKeyString("down")
	if got := m.SelectedID(); got != "td-3d427b" {
		t.Fatalf("next child selected %q, want td-3d427b", got)
	}
	_, _ = m.handleKeyString("enter")
	if m.IssueID() != "td-3d427b" {
		t.Fatalf("enter opened %q, want td-3d427b", m.IssueID())
	}
}

func TestClickSelectsSubtask(t *testing.T) {
	m := New(nil)
	m.SetSize(60, 22)
	apply(t, m, sample(), nil)
	_ = m.View() // populate hits

	var child Hit
	found := false
	for _, h := range m.Hits() {
		if h.Kind == HitChild && h.ID == "td-ae3a2a" {
			child = h
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("no hit for td-ae3a2a: %+v", m.Hits())
	}
	kind, cmd := m.HandleClick(1, child.Y)
	if kind != HitChild || cmd != nil {
		t.Fatalf("click = %v cmd=%v", kind, cmd != nil)
	}
	if !m.Active() {
		t.Fatal("click did not activate the card")
	}
	if m.SelectedID() != "td-ae3a2a" {
		t.Fatalf("click selected %q, want td-ae3a2a", m.SelectedID())
	}
}

func TestJAndKStillScrollWhenActive(t *testing.T) {
	m := New(nil)
	m.SetSize(40, 5)
	long := sample()
	long.Description = strings.Repeat("a line of prose\n\n", 30)
	apply(t, m, long, nil)
	m.SetActive(true)
	first := m.View()
	_, _ = m.handleKeyString("j")
	if m.View() == first {
		t.Fatal("j did not scroll an active card")
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
	if got := StatusLabel("in_review"); got != "IN REVIEW" {
		t.Errorf("StatusLabel(in_review) = %q", got)
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

func TestRefsFromTreeNodes(t *testing.T) {
	got := refsFromNodes([]treeNode{
		{ID: "td-a", Title: "One", Status: "open", Type: "task"},
		{ID: "td-b", Title: "Two", Status: "closed", Type: "bug"},
	})
	if len(got) != 2 || got[1].ID != "td-b" || got[0].Title != "One" {
		t.Fatalf("refsFromNodes = %#v", got)
	}
}

// result builds a LoadedMsg addressed to m's current load without running td.
func result(t *testing.T, m *Model, data *Data, err error) LoadedMsg {
	t.Helper()
	m.requestGeneration++
	if data != nil {
		m.issueID = data.ID
	} else {
		m.issueID = sample().ID
	}
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

func TestHandleKeyAcceptsKeyPressMsg(t *testing.T) {
	m := New(nil)
	m.SetSize(40, 4)
	apply(t, m, sample(), nil)
	handled, cmd := m.HandleKey(tea.KeyPressMsg{Code: 'j'})
	if !handled || cmd != nil {
		t.Fatalf("HandleKey(j) = %v %v", handled, cmd != nil)
	}
}
