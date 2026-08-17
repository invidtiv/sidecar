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
			// Chrome, then one blank line, then content: the filter row stays
			// with the header and the first heading starts the list.
			if trimmed[1] != "/ filter…" || trimmed[2] != "" || trimmed[3] != "Shells (2)" {
				t.Fatalf("chrome is not separated from the first heading by one blank line:\n%s", strings.Join(trimmed[:5], "\n"))
			}
			workspaces := indexOfLine(trimmed, "Workspaces (1)")
			if workspaces < 2 || trimmed[workspaces-1] != "" || trimmed[workspaces-2] == "" {
				t.Fatalf("later heading is not preceded by exactly one blank:\n%s", strings.Join(trimmed, "\n"))
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
	// Title + blank + heading + 5 one-line rows fill height 8. Twenty rows, last
	// selected.
	const n, height = 20, 8
	rows := numberedSidebarRows(n)
	rendered := RenderSidebar(SidebarOptions{
		Width: 40, Height: height, Title: "Workspaces", Focused: true,
		SelectedID: fmt.Sprintf("r%d", n-1), ScrollOffset: 0,
		Sections: []SidebarSection{{Title: "Items", Count: n, Rows: rows}},
	})
	// Body is 6 lines: heading + 5 rows. Last page starts at n-5.
	want := n - 5
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
	if rendered.ScrollOffset != 0 {
		t.Fatalf("scroll = %d, want 0 after the list fits in the taller pane", rendered.ScrollOffset)
	}
	plain := ansi.Strip(rendered.View)
	if !strings.Contains(plain, "item-0") {
		t.Fatalf("clamp left the first item above the fold:\n%s", plain)
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

func indexOfLine(lines []string, want string) int {
	for i, line := range lines {
		if line == want {
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

// TestHeaderSpacerSeparatesChromeFromContentOnBothSurfaces pins td-a453b5. The
// panel header's "+" and the first section heading's "+" used to sit on
// adjacent rows and read as one two-button cluster; one blank line tells the
// chrome and the list apart. Both surfaces render through this function, so
// both inherit it — the project shape below is the plugin's configuration and
// the global shape is the Model's.
func TestHeaderSpacerSeparatesChromeFromContentOnBothSurfaces(t *testing.T) {
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
			if strings.TrimSpace(lines[1]) != "" {
				t.Fatalf("no blank line under the header: %q", lines[1])
			}
			if !strings.Contains(lines[2], "Shells (2)") {
				t.Fatalf("first heading = %q, want it one blank line under the header", lines[2])
			}
		})
	}
}

// TestHeaderSpacerIsDroppedRatherThanClippingAShortPane guards the height
// budget: air is worth a row in an ordinary pane and is not worth the only rows
// a very short one has. Whatever it decides, the view still fits exactly.
func TestHeaderSpacerIsDroppedRatherThanClippingAShortPane(t *testing.T) {
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
		// Where the list is drawn at all, the heading is on row 1 when the pane
		// could not afford the blank line and on row 2 when it could. An empty
		// body pads with blanks, so the heading's row — not a blank row — is what
		// says whether the spacer was spent.
		heading := -1
		for i, line := range lines {
			if strings.Contains(line, "Items (6)") {
				heading = i
				break
			}
		}
		switch {
		case heading < 0:
			if height > headerSpacerMinBody+1 {
				t.Fatalf("height %d drew no list at all:\n%s", height, strings.Join(lines, "\n"))
			}
		case height > headerSpacerMinBody+1:
			if heading != 2 {
				t.Fatalf("height %d has room for the spacer; heading is on row %d:\n%s", height, heading, strings.Join(lines, "\n"))
			}
			if !strings.Contains(ansi.Strip(view), "item-0") {
				t.Fatalf("height %d clipped the list away:\n%s", height, strings.Join(lines, "\n"))
			}
		default:
			if heading != 1 {
				t.Fatalf("height %d spent a row on the spacer; heading is on row %d:\n%s", height, heading, strings.Join(lines, "\n"))
			}
		}
	}
}
