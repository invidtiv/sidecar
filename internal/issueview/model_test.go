package issueview

import (
	"errors"
	"fmt"
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

func TestModelInsetsContentAndHitGeometryByOneColumn(t *testing.T) {
	for _, width := range []int{7, 60} {
		t.Run(fmt.Sprintf("width_%d", width), func(t *testing.T) {
			m := New(nil)
			m.SetSize(width, 24)
			apply(t, m, sample(), nil)

			lines := rows(t, m.View(), width, 24)
			for i, line := range lines {
				plain := ansi.Strip(line)
				if plain[0] != ' ' || plain[len(plain)-1] != ' ' {
					t.Fatalf("row %d lacks one-column outer inset: %q", i, plain)
				}
			}

			wantHitWidth := width - 2
			if m.needsScrollbar() {
				wantHitWidth--
			}
			for _, hit := range m.Hits() {
				if hit.X != 1 || hit.W != wantHitWidth {
					t.Fatalf("hit %+v, want x=1 width=%d", hit, wantHitWidth)
				}
			}
		})
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

func TestPendingScrollIsAppliedOnlyByTheCurrentLoadGeneration(t *testing.T) {
	m := New(nil)
	m.SetSize(40, 3)
	first := result(t, m, sample(), nil)
	m.SetPendingScroll(4)

	secondData := sample()
	secondData.ID = "td-second"
	secondData.Description = strings.Repeat("line\n\n", 20)
	second := result(t, m, secondData, nil)
	m.SetPendingScroll(3)
	if m.SetResult(first) {
		t.Fatal("stale generation consumed pending scroll")
	}
	if got := m.ScrollOffset(); got != 3 {
		t.Fatalf("pending scroll after stale result = %d, want 3", got)
	}
	if !m.SetResult(second) {
		t.Fatal("current generation was rejected")
	}
	if got := m.ScrollOffset(); got != 3 {
		t.Fatalf("restored scroll = %d, want 3", got)
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

func TestClickOpensParentAndSubtaskAndBodyClickOnlyActivates(t *testing.T) {
	m := New(nil)
	m.SetSize(60, 22)
	for _, tc := range []struct {
		id   string
		kind HitKind
	}{
		{id: "td-parent1", kind: HitParent},
		{id: "td-ae3a2a", kind: HitChild},
	} {
		apply(t, m, sample(), nil)
		_ = m.View() // populate hits from the rendered rows
		var target Hit
		found := false
		for _, h := range m.Hits() {
			if h.Kind == tc.kind && h.ID == tc.id {
				target = h
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("no hit for %s: %+v", tc.id, m.Hits())
		}
		kind, cmd := m.HandleClick(target.X, target.Y)
		if kind != tc.kind || cmd != nil {
			t.Fatalf("click %s = %v cmd=%v", tc.id, kind, cmd != nil)
		}
		if !m.Active() {
			t.Fatal("click did not activate the card")
		}
		if m.SelectedID() != tc.id || m.IssueID() != tc.id {
			t.Fatalf("click selected/opened %q/%q, want %s", m.SelectedID(), m.IssueID(), tc.id)
		}
	}

	apply(t, m, sample(), nil)
	m.SetActive(false)
	_ = m.View()
	kind, cmd := m.HandleClick(0, 0)
	if kind != HitBody || cmd != nil || m.IssueID() != sample().ID || !m.Active() {
		t.Fatalf("padding/body click = kind %v cmd=%v issue=%q active=%v", kind, cmd != nil, m.IssueID(), m.Active())
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

func TestOpenHandlerReceivesNavigationInsteadOfRetargeting(t *testing.T) {
	m := New(nil)
	m.SetSize(60, 16)
	apply(t, m, sample(), nil)
	m.SetActive(true)

	var opened []string
	m.OpenHandler = func(id string) tea.Cmd {
		opened = append(opened, id)
		return nil
	}

	_, cmd := m.handleKeyString("up")
	if cmd != nil {
		t.Fatal("OpenHandler's nil command should pass through")
	}
	if m.IssueID() != sample().ID {
		t.Fatalf("OpenHandler path retargeted the model to %q", m.IssueID())
	}
	if len(opened) != 1 || opened[0] != "td-parent1" {
		t.Fatalf("opened = %v, want the parent", opened)
	}

	_, _ = m.handleKeyString("down")
	_, _ = m.handleKeyString("down")
	_, cmd = m.handleKeyString("enter")
	if cmd != nil || m.IssueID() != sample().ID {
		t.Fatalf("enter retargeted: id=%q cmd=%v", m.IssueID(), cmd != nil)
	}
	if len(opened) != 2 || opened[1] != "td-83cfc9" {
		t.Fatalf("enter opened = %v, want the first child", opened)
	}

	m.handleKeyString("right")
	if m.IssueID() != sample().ID || opened[len(opened)-1] != "td-sib3" {
		t.Fatalf("sibling opened = %v id=%q", opened, m.IssueID())
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
