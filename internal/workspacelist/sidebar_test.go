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
		{Title: SectionTitle("Shells", 2), Rows: []SidebarRow{
			testSidebarRow("shell:a", "alpha", -1), testSidebarRow("shell:b", "beta", -2),
		}},
		{Title: SectionTitle("Workspaces", 1), Rows: []SidebarRow{testSidebarRow("wt:a", "topic", 0)}},
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
