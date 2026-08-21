package issueview

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestNormalizeIDAcceptsOnlyTerminalLinkShape(t *testing.T) {
	if got := NormalizeID("  td-1a2b3c  "); got != "td-1a2b3c" {
		t.Fatalf("NormalizeID trimmed = %q", got)
	}
	for _, id := range []string{"", "--force", "td-xyz", "td-1a2b3c extra", "issue-1"} {
		if got := NormalizeID(id); got != "" {
			t.Fatalf("NormalizeID(%q) = %q, want empty", id, got)
		}
	}
}

func TestTabsOpenOrFocusSelectCloseAndCycle(t *testing.T) {
	var group Tabs
	one, two, three := New(nil), New(nil), New(nil)
	one.issueID, two.issueID, three.issueID = "td-1111aa", "td-2222bb", "td-3333cc"

	if idx, created := group.OpenOrFocus("td-1111aa", one); idx != 0 || !created || group.ActiveView() != one {
		t.Fatalf("first open = idx=%d created=%v", idx, created)
	}
	if idx, created := group.OpenOrFocus("td-2222bb", two); idx != 1 || !created {
		t.Fatalf("second open = idx=%d created=%v", idx, created)
	}
	if idx, created := group.OpenOrFocus(" td-1111aa ", three); idx != 0 || created || group.ActiveView() != one {
		t.Fatalf("refocus created a duplicate: idx=%d created=%v n=%d", idx, created, len(group.Items))
	}
	if len(group.Items) != 2 {
		t.Fatalf("tabs = %d, want 2", len(group.Items))
	}

	if _, created := group.OpenOrFocus("not-an-id", New(nil)); created || group.Find("not-an-id") >= 0 {
		t.Fatal("invalid id opened a tab")
	}

	group.OpenOrFocus("td-3333cc", three)
	group.Select(0)
	group.Cycle(1)
	if group.ActiveView() != two {
		t.Fatalf("cycle forward = %q", group.ActiveView().IssueID())
	}
	group.Cycle(-1)
	if group.ActiveView() != one {
		t.Fatalf("cycle back = %q", group.ActiveView().IssueID())
	}

	group.Select(1)
	closed := group.CloseActive()
	if closed.Empty || len(group.Items) != 2 || group.ActiveView() != three {
		t.Fatalf("close middle left n=%d empty=%v", len(group.Items), closed.Empty)
	}
	group.CloseActive()
	last := group.CloseActive()
	if !last.Empty || len(group.Items) != 0 || group.Active != 0 {
		t.Fatalf("close last left n=%d empty=%v active=%d", len(group.Items), last.Empty, group.Active)
	}
}

func TestLayoutTabStripEndTruncatesSoIDStaysVisible(t *testing.T) {
	m := New(nil)
	m.SetData(&Data{ID: "td-abc123", Title: "A headline that will not fit in a narrow tab"})
	group := Tabs{}
	group.OpenOrFocus("td-abc123", m)

	strip := LayoutTabStrip(group, 18, true)
	got := ansi.Strip(strip.Row)
	if !strings.Contains(got, "td-abc123") {
		t.Fatalf("narrow strip dropped the id: %q", got)
	}
	if strings.Contains(got, "will not fit") {
		t.Fatalf("narrow strip did not end-truncate the title: %q", got)
	}
	if !strings.Contains(got, "…") {
		t.Fatalf("narrow strip has no end ellipsis: %q", got)
	}
}

func TestLayoutTabStripShowsTwoTitles(t *testing.T) {
	a, b := New(nil), New(nil)
	a.SetData(&Data{ID: "td-1111aa", Title: "First"})
	b.SetData(&Data{ID: "td-2222bb", Title: "Second"})
	group := Tabs{}
	group.OpenOrFocus("td-1111aa", a)
	group.OpenOrFocus("td-2222bb", b)

	strip := LayoutTabStrip(group, 48, true)
	got := ansi.Strip(strip.Row)
	if !strings.Contains(got, "td-1111aa") || !strings.Contains(got, "td-2222bb") {
		t.Fatalf("two-tab strip dropped an id: %q", got)
	}
	if strings.Count(got, "×") != 2 {
		t.Fatalf("two-tab strip = %q, want one × per tab", got)
	}
	if len(strip.Tabs) != 2 {
		t.Fatalf("visible tabs = %d, want 2", len(strip.Tabs))
	}
	for i, hit := range strip.Tabs {
		if hit.CloseW < 1 {
			t.Fatalf("tab %d has no close hit: %#v", i, hit)
		}
	}
}
