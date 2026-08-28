package workspacelist

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func testSidebarRow(id, text string, data any) SidebarRow {
	return SidebarRow{ID: id, Data: data, Render: func(width int, selected, focused bool) []string {
		return []string{ApplySelection(" "+text+"\n   detail", width, selected, focused)}
	}}
}

func TestRenderSidebarOwnsSectionsViewportAndTypedGeometry(t *testing.T) {
	sections := []SidebarSection{
		{Title: "Shells", Action: &SidebarAction{ID: "new-shell", Label: "+"}, Rows: []SidebarRow{
			testSidebarRow("shell:a", "alpha", -1), testSidebarRow("shell:b", "beta", -2),
		}},
		{Title: "Workspaces", Rows: []SidebarRow{
			testSidebarRow("wt:a", "topic", 0), testSidebarRow("wt:b", "spike", 1),
		}},
	}
	rendered := RenderSidebar(SidebarOptions{
		Width: 36, Height: 9, Title: "Workspaces", Focused: true,
		SelectedID: "wt:b", HeaderAction: &SidebarAction{ID: "new", Label: "New"},
		FilterActive: true, FilterLine: "/ side (2 of 4)", Sections: sections,
	})
	if rendered.ScrollOffset == 0 {
		t.Fatal("selected final row did not move the shared viewport")
	}
	plain := ansi.Strip(rendered.View)
	if !strings.Contains(plain, "Workspaces") || !strings.Contains(plain, "spike") {
		t.Fatalf("shared render lost chrome or selection:\n%s", plain)
	}
	var header, filter, selected *Region
	for i := range rendered.Regions {
		region := &rendered.Regions[i]
		switch {
		case region.Kind == RegionHeaderAction:
			header = region
		case region.Kind == RegionFilter:
			filter = region
		case region.Kind == RegionRow && region.ID == "wt:b":
			selected = region
		}
	}
	if header == nil || header.ID != "new" || filter == nil || filter.Y != 1 || selected == nil || selected.Data != 1 {
		t.Fatalf("typed geometry header=%#v filter=%#v selected=%#v", header, filter, selected)
	}
	if selected.Y+selected.H > 9 {
		t.Fatalf("selected region extends beyond viewport: %#v", selected)
	}
}

func TestRenderSidebarSeparatesSectionsAndKeepsRegionsOnTheirRows(t *testing.T) {
	sections := []SidebarSection{
		{Title: "Shells", Count: 2, Rows: []SidebarRow{
			testSidebarRow("shell:a", "alpha", -1), testSidebarRow("shell:b", "beta", -2),
		}},
		{Title: "Workspaces", Count: 1, Rows: []SidebarRow{testSidebarRow("wt:a", "topic", 0)}},
	}
	// The project sidebar and the global browser configure the same renderer
	// differently; the section shape they get must not differ.
	configurations := map[string]SidebarOptions{
		"project": {HeaderAction: &SidebarAction{ID: "new", Label: "New"}},
		"global":  {HeaderMeta: &SidebarAction{ID: "sort", Label: "Activity"}, FooterLines: []string{"1 project unavailable"}},
	}
	for name, opts := range configurations {
		t.Run(name, func(t *testing.T) {
			opts.Width, opts.Height, opts.Title, opts.Focused = 36, 20, "Workspaces", true
			opts.SelectedID, opts.FilterActive, opts.FilterLine, opts.Sections = "shell:a", true, "/ filter…", sections
			rendered := RenderSidebar(opts)
			lines := strings.Split(ansi.Strip(rendered.View), "\n")
			trimmed := make([]string, len(lines))
			for i, line := range lines {
				trimmed[i] = strings.TrimRight(line, " ")
			}
			// The filter is chrome; the first visible section sits immediately
			// beneath it, while later sections own a pre-header blank line.
			if trimmed[1] != "/ filter…" || !strings.HasPrefix(trimmed[2], "○ SHELLS (2) ─") {
				t.Fatalf("first section is not flush against the filter chrome:\n%s", strings.Join(trimmed[:5], "\n"))
			}
			workspaces := indexOfLineContaining(trimmed, "○ WORKSPACES (1) ─")
			if workspaces < 2 || trimmed[workspaces-1] != "" || trimmed[workspaces-2] == "" {
				t.Fatalf("later heading is not preceded by exactly one blank:\n%s", strings.Join(trimmed, "\n"))
			}
			alpha := indexOfLineContaining(trimmed, "alpha")
			beta := indexOfLineContaining(trimmed, "beta")
			if alpha < 0 || beta != alpha+3 || trimmed[alpha+2] != "" {
				t.Fatalf("adjacent two-line cards do not have exactly one blank line between them:\n%s", strings.Join(trimmed, "\n"))
			}
			for _, region := range rendered.Regions {
				if region.Kind != RegionRow {
					continue
				}
				rowName := map[string]string{"shell:a": "alpha", "shell:b": "beta", "wt:a": "topic"}[region.ID]
				if !strings.Contains(trimmed[region.Y], rowName) {
					t.Fatalf("region %q points at row %d = %q, want the %q row", region.ID, region.Y, trimmed[region.Y], rowName)
				}
			}
		})
	}
}

func oneLineSidebarRow(id, text string) SidebarRow {
	return SidebarRow{ID: id, Data: id, Render: func(width int, selected, focused bool) []string {
		return []string{ApplySelection(" "+text, width, selected, focused)}
	}}
}

func numberedSidebarRows(n int) []SidebarRow {
	rows := make([]SidebarRow, n)
	for i := range rows {
		rows[i] = oneLineSidebarRow(fmt.Sprintf("r%d", i), fmt.Sprintf("item-%d", i))
	}
	return rows
}

func TestRenderSidebarKeepsOffsetZeroWhenSelectionFits(t *testing.T) {
	rows := numberedSidebarRows(12)
	rendered := RenderSidebar(SidebarOptions{
		Width: 40, Height: 20, Title: "Workspaces", Focused: true,
		SelectedID: "r3", ScrollOffset: 0,
		Sections: []SidebarSection{{Title: "Items", Count: 12, Rows: rows}},
	})
	if rendered.ScrollOffset != 0 {
		t.Fatalf("scroll = %d, want 0 when the selected row fits from the top", rendered.ScrollOffset)
	}
	plain := ansi.Strip(rendered.View)
	if !strings.Contains(plain, "item-0") || !strings.Contains(plain, "item-3") {
		t.Fatalf("first or selected row missing:\n%s", plain)
	}
}

func TestRenderSidebarScrollsTheMinimumToRevealSelection(t *testing.T) {
	// Title + blank + heading + three one-line cards and their two gaps fill
	// height 8. Twenty rows, last selected.
	const n, height = 20, 8
	rows := numberedSidebarRows(n)
	rendered := RenderSidebar(SidebarOptions{
		Width: 40, Height: height, Title: "Workspaces", Focused: true,
		SelectedID: fmt.Sprintf("r%d", n-1), ScrollOffset: 0,
		Sections: []SidebarSection{{Title: "Items", Count: n, Rows: rows}},
	})
	// Body is 6 lines: heading + 3 rows + 2 inter-card gaps.
	want := n - 3
	if rendered.ScrollOffset != want {
		t.Fatalf("scroll = %d, want the minimum %d (not the selected index %d)", rendered.ScrollOffset, want, n-1)
	}
	plain := ansi.Strip(rendered.View)
	if strings.Contains(plain, "item-0") {
		t.Fatalf("first item should sit above the fold:\n%s", plain)
	}
	if !strings.Contains(plain, fmt.Sprintf("item-%d", n-1)) {
		t.Fatalf("selected last item is not on screen:\n%s", plain)
	}
}

func TestRenderSidebarClampsScrollToFillTheBody(t *testing.T) {
	rows := numberedSidebarRows(12)
	rendered := RenderSidebar(SidebarOptions{
		Width: 40, Height: 20, Title: "Workspaces", Focused: true,
		SelectedID: "r11", ScrollOffset: 11,
		Sections: []SidebarSection{{Title: "Items", Count: 12, Rows: rows}},
	})
	if rendered.ScrollOffset != 3 {
		t.Fatalf("scroll = %d, want 3: the smallest last page that fills the body", rendered.ScrollOffset)
	}
	plain := ansi.Strip(rendered.View)
	if strings.Contains(plain, "item-0") {
		t.Fatalf("clamp kept an item above the filled last page:\n%s", plain)
	}
	if !strings.Contains(plain, "item-11") {
		t.Fatalf("clamp lost the selected item:\n%s", plain)
	}
}

func TestRenderSidebarStaleOffsetDoesNotHideAFittingSelection(t *testing.T) {
	rows := numberedSidebarRows(8)
	rendered := RenderSidebar(SidebarOptions{
		Width: 40, Height: 20, Title: "Workspaces", Focused: true,
		SelectedID: "r2", ScrollOffset: 6,
		Sections: []SidebarSection{{Title: "Items", Count: 8, Rows: rows}},
	})
	if rendered.ScrollOffset != 0 {
		t.Fatalf("scroll = %d, want 0: the selection fits from the top", rendered.ScrollOffset)
	}
	if !strings.Contains(ansi.Strip(rendered.View), "item-0") {
		t.Fatal("first item is still above the fold after a stale offset")
	}
}

func TestVisibleEndAndMaxScrollCountCardAndSectionSpacing(t *testing.T) {
	sections := []SidebarSection{
		{Title: "Shells", Count: 2, Rows: numberedSidebarRows(2)},
		{Title: "Worktrees", Count: 2, Rows: []SidebarRow{
			oneLineSidebarRow("r2", "item-2"), oneLineSidebarRow("r3", "item-3"),
		}},
	}
	flat := []sidebarFlatRow{
		{section: 0, row: sections[0].Rows[0]},
		{section: 0, row: sections[0].Rows[1]},
		{section: 1, row: sections[1].Rows[0]},
		{section: 1, row: sections[1].Rows[1]},
	}

	// Seven rows fit: heading + two cards + their gap, then the next
	// section's pre-line + heading + first card. The fourth card is below the
	// fold because it also needs an inter-card line.
	if got := sidebarVisibleEnd(flat, sections, 0, 7, 40, "r0", true); got != 3 {
		t.Fatalf("visible end at offset 0 = %d, want 3", got)
	}
	// Starting at the second card repeats its section heading at the top. The
	// same seven rows then fit the complete tail, so one is the maximum useful
	// scroll offset and larger offsets would leave avoidable empty space.
	if got := sidebarVisibleEnd(flat, sections, 1, 7, 40, "r0", true); got != 4 {
		t.Fatalf("visible end at offset 1 = %d, want 4", got)
	}
	if got := sidebarMaxScroll(flat, sections, 7, 40, "r0", true); got != 1 {
		t.Fatalf("max scroll = %d, want 1", got)
	}
}

func indexOfLineContaining(lines []string, want string) int {
	for i, line := range lines {
		if strings.Contains(line, want) {
			return i
		}
	}
	return -1
}

func TestRenderSidebarOmitsAbsentActions(t *testing.T) {
	rendered := RenderSidebar(SidebarOptions{Width: 40, Height: 8, Sections: []SidebarSection{{Title: "LIVE", Rows: []SidebarRow{testSidebarRow("a", "alpha", nil)}}}})
	for _, region := range rendered.Regions {
		if region.Kind == RegionHeaderAction || region.Kind == RegionSectionAction {
			t.Fatalf("read-only sidebar registered absent action: %#v", region)
		}
	}
}

func TestSectionHeadersUseSharedCategoryGrammarRuleAndActionGeometry(t *testing.T) {
	cases := []struct {
		title string
		want  string
	}{
		{"Pinned", "📌 PINNED (2) ─"},
		{"Needs Attention", "◆ NEEDS ATTENTION (2) ─"},
		{"Working", "● WORKING (2) ─"},
		{"Live", "● LIVE (2) ─"},
		{"Idle", "○ IDLE (2) ─"},
	}
	for _, tc := range cases {
		t.Run(tc.title, func(t *testing.T) {
			section := SidebarSection{Title: tc.title, Count: 2, Action: &SidebarAction{ID: "add", Label: "+"}, Rows: numberedSidebarRows(2)}
			rendered := RenderSidebar(SidebarOptions{Width: 40, Height: 12, Sections: []SidebarSection{section}})
			line := strings.TrimRight(strings.Split(ansi.Strip(rendered.View), "\n")[1], " ")
			if !strings.HasPrefix(line, tc.want) || !strings.HasSuffix(line, "+") {
				t.Fatalf("header = %q, want glyph/title/count/rule/action", line)
			}
			var action *Region
			for i := range rendered.Regions {
				if rendered.Regions[i].Kind == RegionSectionAction {
					action = &rendered.Regions[i]
				}
			}
			if action == nil || action.X+action.W != 39 || action.Y != 1 {
				t.Fatalf("section action geometry = %#v, want it on the header's trailing cells", action)
			}
		})
	}

	narrow := RenderSidebar(SidebarOptions{Width: 12, Height: 8, Sections: []SidebarSection{{
		Title: "Needs Attention", Count: 23, Action: &SidebarAction{ID: "add", Label: "+"}, Rows: numberedSidebarRows(1),
	}}})
	line := strings.Split(ansi.Strip(narrow.View), "\n")[1]
	if ansi.StringWidth(line) != 12 || strings.Contains(line, "+") || strings.Contains(line, "(23)") {
		t.Fatalf("narrow section header did not drop action then count cleanly: %q", line)
	}
	for _, region := range narrow.Regions {
		if region.Kind == RegionSectionAction {
			t.Fatalf("narrow header kept an invisible action region: %#v", region)
		}
	}
}

func TestSharedMovementAndResizeClamp(t *testing.T) {
	if got := MoveIndex(2, 3, 4); got != 3 {
		t.Fatalf("move = %d, want 3", got)
	}
	if got := ResizePercent(40, 100, 100); got != 60 {
		t.Fatalf("upper resize clamp = %d", got)
	}
	if got := ResizePercent(40, -100, 100); got != 10 {
		t.Fatalf("lower resize clamp = %d", got)
	}
}

// The header sheds controls in a stated order rather than clipping them. A
// control clipped to "Activi…" or a bare "…" is a target whose meaning a reader
// cannot recover but whose click still fires, so a control that cannot be drawn
// whole is dropped along with its hit region.
func TestHeaderControlsDegradeInAStatedOrder(t *testing.T) {
	opts := func(width int) SidebarOptions {
		return SidebarOptions{
			Width: width, Height: 6, Title: "Workspaces",
			HeaderMeta:   &SidebarAction{ID: "sort", Label: SortPillLabel(SortActivity)},
			HeaderAction: &SidebarAction{ID: "new", Label: "+"},
		}
	}
	kinds := func(r SidebarRendered) map[RegionKind]bool {
		out := map[RegionKind]bool{}
		for _, region := range r.Regions {
			out[region.Kind] = true
		}
		return out
	}

	// Wide: both controls, sort with its word.
	wide := RenderSidebar(opts(56))
	header := ansi.Strip(strings.Split(wide.View, "\n")[0])
	if !strings.Contains(header, SortGlyph+" Activity") || !strings.Contains(header, "+") {
		t.Fatalf("wide header lost a control: %q", header)
	}
	if k := kinds(wide); !k[RegionSort] || !k[RegionHeaderAction] {
		t.Fatalf("wide header regions = %v, want both", k)
	}

	// Squeezed: the sort keeps its glyph and its region, create keeps its word.
	// The sort's label has a substitute — the section headings name the
	// grouping — so it sheds text before create sheds anything.
	mid := RenderSidebar(opts(24))
	header = ansi.Strip(strings.Split(mid.View, "\n")[0])
	if strings.Contains(header, "Activity") {
		t.Fatalf("squeezed header kept the sort word: %q", header)
	}
	if !strings.Contains(header, SortGlyph) || !strings.Contains(header, "+") {
		t.Fatalf("squeezed header should keep the sort glyph and create: %q", header)
	}
	if k := kinds(mid); !k[RegionSort] || !k[RegionHeaderAction] {
		t.Fatalf("squeezed header regions = %v, want both", k)
	}

	// Narrow: create is the last to go, because it is the only action here with
	// no substitute in the header.
	narrow := RenderSidebar(opts(16))
	header = ansi.Strip(strings.Split(narrow.View, "\n")[0])
	if !strings.Contains(header, "+") {
		t.Fatalf("narrow header dropped create before the sort: %q", header)
	}
	if k := kinds(narrow); k[RegionSort] {
		t.Fatalf("narrow header kept a sort region it did not draw: %q", header)
	}

	// Nothing fits: no header regions at all, so no invisible click targets.
	tiny := RenderSidebar(opts(11))
	if len(kinds(tiny)) > 0 {
		header = ansi.Strip(strings.Split(tiny.View, "\n")[0])
		t.Fatalf("tiny header registered regions for controls it did not draw: %q", header)
	}
}

// Every registered header region must sit under the control it names, or a
// click lands on the wrong one.
func TestHeaderRegionsSitUnderTheirControls(t *testing.T) {
	rendered := RenderSidebar(SidebarOptions{
		Width: 56, Height: 6, Title: "Workspaces",
		HeaderMeta:   &SidebarAction{ID: "sort", Label: SortPillLabel(SortRecent)},
		HeaderAction: &SidebarAction{ID: "new", Label: "+"},
	})
	header := ansi.Strip(strings.Split(rendered.View, "\n")[0])
	for _, region := range rendered.Regions {
		if region.Y != 0 {
			continue
		}
		if region.X < 0 || region.X+region.W > 56 {
			t.Fatalf("%s region runs off the header: x=%d w=%d", region.Kind, region.X, region.W)
		}
		under := strings.TrimSpace(header[region.X : region.X+region.W])
		switch region.Kind {
		case RegionSort:
			if !strings.Contains(under, SortGlyph) {
				t.Fatalf("sort region covers %q, not the sort pill", under)
			}
		case RegionHeaderAction:
			if !strings.Contains(under, "+") {
				t.Fatalf("create region covers %q, not the create button", under)
			}
		}
	}
}

func TestFirstVisibleSectionIsFlushAgainstChromeOnBothSurfaces(t *testing.T) {
	sections := []SidebarSection{
		{Title: "Shells", Count: 2, Action: &SidebarAction{ID: "new-shell", Label: "+"}, Rows: []SidebarRow{
			oneLineSidebarRow("shell:a", "alpha"), oneLineSidebarRow("shell:b", "beta"),
		}},
	}
	for name, opts := range map[string]SidebarOptions{
		"project": {HeaderAction: &SidebarAction{ID: "new", Label: "+"}, HeaderMeta: &SidebarAction{ID: "sort", Label: SortPillLabel(SortManual)}},
		"global":  {HeaderMeta: &SidebarAction{ID: "sort", Label: SortPillLabel(SortActivity)}},
	} {
		t.Run(name, func(t *testing.T) {
			opts.Width, opts.Height, opts.Title, opts.Focused, opts.Sections = 40, 20, "Workspaces", true, sections
			lines := strings.Split(ansi.Strip(RenderSidebar(opts).View), "\n")
			if !strings.Contains(lines[0], "Workspaces") {
				t.Fatalf("row 0 is not the header: %q", lines[0])
			}
			if !strings.Contains(lines[1], "○ SHELLS (2) ─") {
				t.Fatalf("first heading = %q, want it flush under the header", lines[1])
			}
		})
	}
}

func TestFlushFirstSectionDoesNotClipAShortPane(t *testing.T) {
	rows := numberedSidebarRows(6)
	for height := 1; height <= 12; height++ {
		opts := SidebarOptions{
			Width: 40, Height: height, Title: "Workspaces", Focused: true, SelectedID: "r0",
			HeaderAction: &SidebarAction{ID: "new", Label: "+"},
			Sections:     []SidebarSection{{Title: "Items", Count: 6, Rows: rows}},
		}
		view := RenderSidebar(opts).View
		lines := strings.Split(ansi.Strip(view), "\n")
		if len(lines) != height {
			t.Fatalf("height %d rendered %d lines", height, len(lines))
		}
		if !strings.Contains(lines[0], "Workspaces") {
			t.Fatalf("height %d pushed the header off the top: %q", height, lines[0])
		}
		// Wherever the list can draw a whole heading plus its first card, the
		// heading is directly below the panel header at row 1.
		heading := -1
		for i, line := range lines {
			if strings.Contains(line, "○ ITEMS (6) ─") {
				heading = i
				break
			}
		}
		if height < 3 && heading >= 0 {
			t.Fatalf("height %d drew a partial list it cannot fit:\n%s", height, strings.Join(lines, "\n"))
		}
		if height >= 3 {
			if heading != 1 {
				t.Fatalf("height %d put the first heading on row %d, want row 1:\n%s", height, heading, strings.Join(lines, "\n"))
			}
			if !strings.Contains(ansi.Strip(view), "item-0") {
				t.Fatalf("height %d clipped the first card:\n%s", height, strings.Join(lines, "\n"))
			}
		}
	}
}
