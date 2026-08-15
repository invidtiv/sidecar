package workspacelist

import (
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
			if trimmed[1] != "/ filter…" || trimmed[2] != "Shells (2)" {
				t.Fatalf("first heading does not sit flush under the filter row:\n%s", strings.Join(trimmed[:5], "\n"))
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
