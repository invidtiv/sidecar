package workspacelist

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

// Slice 2 of docs/plans/active/global-overview-workspaces.md: the list, filter,
// and sort component both Workspaces surfaces share. These tests pin the
// promises the plan makes about matching, ordering, selection, and the filter's
// keyboard contract — the behaviours a consumer is allowed to rely on.

func items() []Item {
	base := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	return []Item{
		{ID: "a", Name: "modal look and feel", Project: "sidecar", ProjectOrder: 0, Branch: "modal-look-and-feel", Task: "td-71de3d", Provider: "codex", Status: "working", Group: GroupWorking, ChangedAt: base},
		{ID: "b", Name: "Kanban scrolling", Project: "sidecar", ProjectOrder: 0, Provider: "shell", Status: "live", Group: GroupLive, ChangedAt: base.Add(-time.Hour)},
		{ID: "c", Name: "réponse", Project: "braid", ProjectOrder: 1, Branch: "feature/RÉPONSE", Provider: "claude", Status: "needs attention", Group: GroupNeedsAttention, ChangedAt: base.Add(-2 * time.Hour)},
		{ID: "d", Name: "old worktree", Project: "td", ProjectOrder: 2, Status: "no session", Group: GroupNoSession},
	}
}

func TestFilterMatchesEveryPromisedFieldCaseAndUnicodeSafely(t *testing.T) {
	all := items()
	cases := []struct {
		query string
		want  []string
	}{
		{"MODAL", []string{"a"}},                    // name, case-insensitive
		{"td-71", []string{"a"}},                    // task id
		{"modal-look", []string{"a"}},               // branch
		{"braid", []string{"c"}},                    // project
		{"claude", []string{"c"}},                   // provider
		{"needs", []string{"c"}},                    // semantic status label
		{"réponse", []string{"c"}},                  // unicode, and the branch's uppercase spelling
		{"sidecar working", []string{"a"}},          // every token must match
		{"   ", []string{"a", "b", "c", "d"}},       // whitespace-only is not a filter
		{"nothing at all", nil},                     // honest empty result
		{"no session", []string{"d"}},               // plain rows are matchable too
		{"scrolling sidecar", []string{"b"}},        // token order does not matter
		{"live", []string{"b"}},                     // presentation bucket
		{"sidecar", []string{"a", "b"}},             // project narrows, does not widen
		{"work", []string{"a", "d"}},                // substring across name and status
		{"réponse braid", []string{"c"}},            // combined
		{"RÉPONSE", []string{"c"}},                  // uppercase unicode query
		{"kanban", []string{"b"}},                   // case-insensitive name
		{"old", []string{"d"}},                      // last row
		{"sidecar braid td", nil},                   // no row belongs to three projects
		{"feature/", []string{"c"}},                 // punctuation inside a token
		{"codex", []string{"a"}},                    // provider only
		{"modal look", []string{"a"}},               // spaces split tokens, not phrases
		{"worktree", []string{"d"}},                 // name token
		{"td", []string{"a", "d"}},                  // matches the task id and the project
		{"", []string{"a", "b", "c", "d"}},          // empty query matches everything
		{"sidecar modal working", []string{"a"}},    // three-token AND
		{"kanban scrolling", []string{"b"}},         // two-token AND on one field
		{"braid claude attention", []string{"c"}},   // across three fields
		{"sidecar live", []string{"b"}},             // project + bucket
		{"nosuch", nil},                             // no match
		{"td-71d3", nil},                            // near miss on a task id
		{"session", []string{"d"}},                  // status word
		{"look and feel", []string{"a"}},            // multi-token phrase-ish
		{"BRAID", []string{"c"}},                    // uppercase project
		{"shell", []string{"b"}},                    // provider label
		{"working", []string{"a"}},                  // status
		{"modal feel sidecar", []string{"a"}},       // reordered tokens
		{"c", []string{"a", "b", "c"}},              // a single letter is a legitimate substring search
		{"scroll", []string{"b"}},                   // partial word
		{"réponse feature", []string{"c"}},          // unicode plus ascii
		{"no", []string{"d"}},                       // substring search, not fuzzy matching
		{"needs attention braid", []string{"c"}},    // full label
		{"old td", []string{"d"}},                   // name + project
		{"modal-look-and-feel", []string{"a"}},      // whole branch
		{"KANBAN SCROLLING", []string{"b"}},         // uppercase multi-token
		{"sidecar codex td-71d63", nil},             // one bad token fails the whole query
		{"live shell", []string{"b"}},               // bucket + provider
		{"attention", []string{"c"}},                // single status word
		{"worktree td", []string{"d"}},              // name + project
		{"réponse claude braid", []string{"c"}},     // everything about one row
		{"modal", []string{"a"}},                    // simple
		{"feel", []string{"a"}},                     // simple
		{"and", []string{"a"}},                      // stop-words are not special
		{"sidecar kanban scrolling", []string{"b"}}, // project + name
	}
	for _, tc := range cases {
		var got []string
		for _, item := range Filtered(all, tc.query) {
			got = append(got, item.ID)
		}
		if strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Fatalf("Filtered(%q) = %v, want %v", tc.query, got, tc.want)
		}
	}
}

func TestMatchFieldsIsTheSameMatcherForCallersWithoutItems(t *testing.T) {
	if !MatchFields("codex working", "topic", "sidecar", "branch", "", "codex", "working") {
		t.Fatal("project-side field matching disagrees with the item matcher")
	}
	if MatchFields("codex missing", "topic", "sidecar", "branch", "", "codex", "working") {
		t.Fatal("an unmatched token must fail the whole query")
	}
}

func TestFourSortsAreStableAndPresentationOnly(t *testing.T) {
	all := items()
	order := func(mode Sort) []string {
		var got []string
		for _, item := range Sorted(all, mode) {
			got = append(got, item.ID)
		}
		return got
	}
	want := map[Sort][]string{
		SortActivity: {"c", "a", "b", "d"}, // needs attention, working, live, no session
		SortProject:  {"a", "b", "c", "d"}, // configured project order, then input order
		SortRecent:   {"a", "b", "c", "d"}, // newest change first; zero times last
		SortName:     {"b", "a", "d", "c"}, // case-insensitive by name
	}
	for mode, expected := range want {
		if got := order(mode); strings.Join(got, ",") != strings.Join(expected, ",") {
			t.Fatalf("%s sort = %v, want %v", mode.Label(), got, expected)
		}
	}
	// Sorting is presentation only: the input slice and its identities survive.
	for i, item := range items() {
		if all[i].ID != item.ID {
			t.Fatal("Sorted mutated its input")
		}
	}
	// An unchanged poll must not churn: sorting twice is identical.
	if strings.Join(order(SortActivity), ",") != strings.Join(order(SortActivity), ",") {
		t.Fatal("repeated sorts disagree")
	}
	if SortActivity.Next() != SortProject || SortProject.Next() != SortRecent || SortRecent.Next() != SortName || SortName.Next() != SortActivity {
		t.Fatal("`s` does not cycle Activity → Project → Recent → Name")
	}
}

func TestActivityGroupingIsTheKanbanProjection(t *testing.T) {
	sections := Grouped(Sorted(items(), SortActivity), SortActivity)
	var got []Group
	for _, section := range sections {
		got = append(got, section.Group)
	}
	want := []Group{GroupNeedsAttention, GroupWorking, GroupLive, GroupNoSession}
	if len(got) != len(want) {
		t.Fatalf("sections = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sections = %v, want %v", got, want)
		}
	}
	// The other sorts are one unheaded run: the chosen sort is the only thing
	// organising the list.
	if sections := Grouped(Sorted(items(), SortName), SortName); len(sections) != 1 || sections[0].Group != "" {
		t.Fatalf("non-activity sort produced sections: %#v", sections)
	}
}

func TestSelectionFollowsIdentityThroughRefreshFilterAndSort(t *testing.T) {
	var m Model
	m.SetItems(items())
	m.Render(RenderOptions{Width: 40, Height: 20})
	if !m.SelectID("c") {
		t.Fatal("could not select a visible identity")
	}

	// A refresh that reorders and renames rows keeps the same item selected.
	refreshed := items()
	refreshed[2].Name = "renamed"
	refreshed = append(refreshed[2:], refreshed[:2]...)
	m.SetItems(refreshed)
	if m.SelectedID() != "c" {
		t.Fatalf("refresh moved the cursor to %q", m.SelectedID())
	}

	// Sorting is presentation: the selected identity does not move.
	m.CycleSort()
	if m.SelectedID() != "c" {
		t.Fatalf("sorting moved the cursor to %q", m.SelectedID())
	}

	// A query that still matches the selection keeps it; one that removes it
	// falls back to the first visible row rather than to a neighbour by index.
	m.FocusFilter()
	for _, r := range "réponse" {
		m.FilterKey(string(r), string(r))
	}
	if m.SelectedID() != "c" {
		t.Fatalf("filtering to a matching row moved the cursor to %q", m.SelectedID())
	}
	m.FilterKey("ctrl+u", "")
	for _, r := range "modal" {
		m.FilterKey(string(r), string(r))
	}
	if m.SelectedID() != "a" {
		t.Fatalf("filtering away the selection selected %q, want the first match", m.SelectedID())
	}
}

func TestFilterKeyboardContract(t *testing.T) {
	var m Model
	m.SetItems(items())
	m.Render(RenderOptions{Width: 40, Height: 20})

	// Navigation stays live while filtering.
	m.FocusFilter()
	if result := m.FilterKey("down", ""); result != KeyIgnored {
		t.Fatalf("down was consumed by the filter: %v", result)
	}
	for _, r := range "sidecar" {
		if result := m.FilterKey(string(r), string(r)); result != KeyHandled {
			t.Fatalf("typing %q was not handled: %v", r, result)
		}
	}
	if matched, total := m.Counts(); matched != 2 || total != 4 {
		t.Fatalf("counts = %d of %d, want 2 of 4", matched, total)
	}

	// Enter accepts: the query stays, focus returns to the list.
	if result := m.FilterKey("enter", ""); result != KeyAccept || m.Filter().Focused() {
		t.Fatalf("enter did not accept: %v focused=%v", result, m.Filter().Focused())
	}
	if !m.Filter().Active() || m.Filter().Query() != "sidecar" {
		t.Fatal("enter discarded the query")
	}

	// First escape clears the query, second releases focus.
	m.FocusFilter()
	if result := m.FilterKey("esc", ""); result != KeyHandled || m.Filter().Query() != "" || !m.Filter().Focused() {
		t.Fatalf("first escape = %v query=%q focused=%v", result, m.Filter().Query(), m.Filter().Focused())
	}
	if result := m.FilterKey("esc", ""); result != KeyExit || m.Filter().Focused() {
		t.Fatalf("second escape = %v focused=%v", result, m.Filter().Focused())
	}
	if matched, _ := m.Counts(); matched != 4 {
		t.Fatalf("clearing the query did not restore the list: %d rows", matched)
	}

	// Pastes go through the same insertion path as keystrokes.
	m.FocusFilter()
	m.Filter().Insert("braid")
	m.Reproject()
	if matched, _ := m.Counts(); matched != 1 {
		t.Fatalf("paste filtered to %d rows, want 1", matched)
	}
}

func TestRenderShowsCountsGroupsNoMatchAndNarrowRows(t *testing.T) {
	var m Model
	m.SetItems(items())

	wide := ansi.Strip(m.Render(RenderOptions{Width: 46, Height: 20, Title: "Workspaces", Focused: true}).View)
	for _, want := range []string{"Workspaces", "Activity", "Needs Attention (1)", "Working (1)", "No Session (1)", "sidecar", "braid"} {
		if !strings.Contains(wide, want) {
			t.Fatalf("wide render is missing %q:\n%s", want, wide)
		}
	}
	for _, line := range strings.Split(wide, "\n") {
		if ansi.StringWidth(line) != 46 {
			t.Fatalf("render produced a %d-wide line in a 46-wide box: %q", ansi.StringWidth(line), line)
		}
	}

	// The filter row is chrome the list only spends a row on while a query is
	// live, so an unfiltered list starts at its first heading.
	if strings.Contains(wide, "/ filter") {
		t.Fatalf("an unfiltered list drew the filter row:\n%s", wide)
	}
	if got := strings.Split(wide, "\n")[1]; !strings.Contains(got, "Needs Attention (1)") {
		t.Fatalf("the first heading is on row %q, want it directly under the title", got)
	}
	m.FocusFilter()
	filtering := ansi.Strip(m.Render(RenderOptions{Width: 46, Height: 20, Title: "Workspaces", Focused: true}).View)
	if row := strings.Split(filtering, "\n")[1]; !strings.HasPrefix(row, "/ ") {
		t.Fatalf("a live filter drew %q under the title, want its query row:\n%s", row, filtering)
	}
	m.Filter().Reset()

	// No-match is an explicit state, with counts to explain it.
	m.FocusFilter()
	m.Filter().Insert("zzz")
	m.Reproject()
	empty := ansi.Strip(m.Render(RenderOptions{Width: 46, Height: 12}).View)
	if !strings.Contains(empty, "0 of 4") || !strings.Contains(empty, "No workspaces match") {
		t.Fatalf("no-match state is not honest:\n%s", empty)
	}

	// Narrow: one truncated line per row, still exactly the box width.
	m.Filter().Reset()
	m.Reproject()
	narrow := ansi.Strip(m.Render(RenderOptions{Width: 24, Height: 14}).View)
	if !strings.Contains(narrow, "modal") {
		t.Fatalf("narrow render dropped rows:\n%s", narrow)
	}
	for _, line := range strings.Split(narrow, "\n") {
		if ansi.StringWidth(line) != 24 {
			t.Fatalf("narrow render produced a %d-wide line: %q", ansi.StringWidth(line), line)
		}
	}
}

// A project whose inventory could not be read has to be visible in the list
// that is missing it — including in the normal case, where the catalog is
// longer than the pane and there are no leftover rows for it to occupy.
func TestFailureRowsSurviveACatalogLongerThanThePane(t *testing.T) {
	var m Model
	var many []Item
	for i := range 40 {
		item := items()[0]
		item.ID = string(rune('a'+i%26)) + strings.Repeat("x", i)
		many = append(many, item)
	}
	m.SetItems(many)
	m.SetFailures([]string{"braid unavailable: not a Git repository"})

	view := ansi.Strip(m.Render(RenderOptions{Width: 46, Height: 12}).View)
	if !strings.Contains(view, "braid unavailable") {
		t.Fatalf("failure row was squeezed out by a full viewport:\n%s", view)
	}
	if !strings.Contains(view, "modal look and feel") {
		t.Fatalf("failure row cost the list its items:\n%s", view)
	}
	for _, line := range strings.Split(view, "\n") {
		if ansi.StringWidth(line) != 46 {
			t.Fatalf("failure row broke the box width: %q", line)
		}
	}
	if lines := strings.Count(view, "\n") + 1; lines != 12 {
		t.Fatalf("render produced %d lines in a 12-row box", lines)
	}

	// A long outage list collapses into a count rather than taking the pane.
	failures := make([]string, 0, 20)
	for i := range 20 {
		failures = append(failures, "project"+string(rune('a'+i))+" unavailable: gone")
	}
	m.SetFailures(failures)
	collapsed := ansi.Strip(m.Render(RenderOptions{Width: 46, Height: 12}).View)
	if !strings.Contains(collapsed, "more projects unavailable") {
		t.Fatalf("long failure list did not collapse:\n%s", collapsed)
	}
	if !strings.Contains(collapsed, "modal look and feel") {
		t.Fatalf("failures pushed the catalog off the screen:\n%s", collapsed)
	}
}

func TestRegionsFollowRenderedGeometry(t *testing.T) {
	var m Model
	m.SetItems(items())
	rendered := m.Render(RenderOptions{Width: 46, Height: 20})
	sort, ok := RegionAt(rendered.Regions, 44, 0)
	if !ok || sort.Kind != RegionSort {
		t.Fatalf("sort region = %#v ok=%v", sort, ok)
	}
	// No filter row is drawn until a query is live, so there is no region for
	// one to click either.
	if filter, ok := RegionAt(rendered.Regions, 3, 1); ok && filter.Kind == RegionFilter {
		t.Fatal("an unfiltered list registered a filter region")
	}
	m.FocusFilter()
	filtering := m.Render(RenderOptions{Width: 46, Height: 20})
	filter, ok := RegionAt(filtering.Regions, 3, 1)
	if !ok || filter.Kind != RegionFilter {
		t.Fatalf("filter region = %#v ok=%v", filter, ok)
	}
	m.Filter().Reset()
	var rows int
	for _, region := range rendered.Regions {
		if region.Kind == RegionRow {
			rows++
			if region.ID == "" {
				t.Fatal("a row region carries no stable identity")
			}
		}
	}
	if rows != 4 {
		t.Fatalf("row regions = %d, want one per drawn row", rows)
	}
}

func TestSelectionClampsAtBothEndsAndScrollFollows(t *testing.T) {
	var m Model
	m.SetItems(items())
	m.Render(RenderOptions{Width: 46, Height: 8})
	for i := 0; i < 10; i++ {
		m.Move(1)
	}
	if selected, _ := m.Selected(); selected.ID != "d" {
		t.Fatalf("moving past the end selected %q", selected.ID)
	}
	for i := 0; i < 10; i++ {
		m.Move(-1)
	}
	if selected, _ := m.Selected(); selected.ID != "c" {
		t.Fatalf("moving past the start selected %q", selected.ID)
	}
	if !m.Bottom() || m.SelectedID() != "d" {
		t.Fatalf("G did not reach the last row: %q", m.SelectedID())
	}
	if !m.Top() || m.SelectedID() != "c" {
		t.Fatalf("g did not reach the first row: %q", m.SelectedID())
	}
}

func TestRelativeAgeUsesTheBoardsUnits(t *testing.T) {
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	cases := map[time.Duration]string{
		0:                "now",
		20 * time.Second: "20s",
		5 * time.Minute:  "5m",
		3 * time.Hour:    "3h",
		50 * time.Hour:   "2d",
	}
	for ago, want := range cases {
		if got := RelativeAge(now.Add(-ago), now); got != want {
			t.Fatalf("RelativeAge(-%s) = %q, want %q", ago, got, want)
		}
	}
	if RelativeAge(time.Time{}, now) != "" {
		t.Fatal("a zero change time must render nothing")
	}
}

// Two shells in one project can share a display name; only the tmux session
// name tells them apart, and it is a filter field rather than a rendered one.
func TestFilterSeparatesIdenticallyNamedShellsByTmuxName(t *testing.T) {
	rows := []Item{
		{ID: "a", Name: "shell", Project: "sidecar", TmuxName: "sc-alpha", Status: "live", Group: GroupLive},
		{ID: "b", Name: "shell", Project: "sidecar", TmuxName: "sc-bravo", Status: "live", Group: GroupLive},
	}
	var matched []string
	for _, row := range rows {
		if Match(row, "sc-bravo") {
			matched = append(matched, row.ID)
		}
	}
	if len(matched) != 1 || matched[0] != "b" {
		t.Fatalf("query matched %v, want only the shell with that session", matched)
	}
	var m Model
	for _, row := range rows {
		rendered := ansi.Strip(strings.Join(m.renderRow(row, false, true, 60, time.Now()), "\n"))
		if strings.Contains(rendered, row.TmuxName) {
			t.Fatalf("the tmux session name became visible in the row: %q", rendered)
		}
	}
}
