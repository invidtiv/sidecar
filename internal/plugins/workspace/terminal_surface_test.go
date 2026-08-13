package workspace

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/tty"
)

// markerBuffer fills an output buffer with uniquely tagged rows so a rendered
// frame can be searched for the exact row a terminal surface starts on.
func markerBuffer(tag string, rows int) *tty.OutputBuffer {
	buffer := tty.NewOutputBuffer(outputBufferCap)
	lines := make([]string, 0, rows)
	for i := 0; i < rows; i++ {
		lines = append(lines, fmt.Sprintf("%s%02d", tag, i))
	}
	buffer.Write(strings.Join(lines, "\n"))
	return buffer
}

// surfacePlugin builds a plugin with a worktree agent and (optionally) a shell,
// both backed by marker buffers, sized for a realistic terminal.
func surfacePlugin(shellSelected bool) *Plugin {
	p := New()
	p.ctx = &plugin.Context{}
	p.width = 120
	p.height = 40
	p.focused = true
	p.activePane = PanePreview
	p.viewMode = ViewModeList
	p.previewTab = PreviewTabOutput
	p.sidebarVisible = true
	p.sidebarWidth = 40
	p.shellSelected = shellSelected
	p.selectedShellIdx = 0
	p.worktrees = []*Worktree{{
		Name: "worktree",
		Agent: &Agent{
			TmuxSession: "agent-session", TmuxPane: "%11",
			OutputBuf: markerBuffer("AGENT", 4),
		},
	}}
	p.shells = []*ShellSession{{
		Name: "Shell", TmuxName: "shell-session",
		Agent: &Agent{
			TmuxSession: "shell-session", TmuxPane: "%12",
			OutputBuf: markerBuffer("SHELL", 4),
		},
	}}
	return p
}

// findMarker returns the row and visual column of a marker token in a rendered
// frame, in the same plugin-local coordinates terminalSurfaceGeometry reports.
func findMarker(t *testing.T, rendered, marker string) (row, col int) {
	t.Helper()
	for i, line := range strings.Split(rendered, "\n") {
		plain := ansi.Strip(line)
		if idx := strings.Index(plain, marker); idx >= 0 {
			return i, ansi.StringWidth(plain[:idx])
		}
	}
	t.Fatalf("marker %q not present in rendered frame:\n%s", marker, rendered)
	return 0, 0
}

func TestPreviewSplitSidebarVisibleAndHidden(t *testing.T) {
	p := surfacePlugin(false)

	split := p.previewSplitFor(120)
	// available = 119, sidebar = 47, preview = 72.
	if split.SidebarWidth != 47 || split.PreviewWidth != 72 {
		t.Fatalf("split = %+v, want sidebar 47 / preview 72", split)
	}
	if split.PreviewX != split.SidebarWidth+dividerWidth {
		t.Fatalf("PreviewX = %d, want %d", split.PreviewX, split.SidebarWidth+dividerWidth)
	}
	if split.ContentX != split.PreviewX+previewContentInset {
		t.Fatalf("ContentX = %d, want %d", split.ContentX, split.PreviewX+previewContentInset)
	}
	if split.ContentWidth != split.PreviewWidth-panelOverhead ||
		split.SidebarContentWidth != split.SidebarWidth-panelOverhead {
		t.Fatalf("content widths = %+v, want outer minus panelOverhead", split)
	}

	p.sidebarVisible = false
	hidden := p.previewSplitFor(120)
	if hidden.SidebarWidth != 0 || hidden.PreviewX != 0 ||
		hidden.PreviewWidth != 120 || hidden.ContentX != previewContentInset ||
		hidden.ContentWidth != 120-panelOverhead {
		t.Fatalf("hidden-sidebar split = %+v", hidden)
	}
}

func TestPreviewSplitPreservesClamps(t *testing.T) {
	p := surfacePlugin(false)

	// Minimum sidebar width: a tiny percentage still gets sidebarMinWidth.
	p.sidebarWidth = 1
	if got := p.previewSplitFor(120).SidebarWidth; got != sidebarMinWidth {
		t.Fatalf("SidebarWidth = %d, want floor %d", got, sidebarMinWidth)
	}

	// available-previewMinWidth cap: a huge percentage still leaves the preview
	// previewMinWidth columns.
	p.sidebarWidth = 95
	split := p.previewSplitFor(120)
	if split.SidebarWidth != 119-previewMinWidth || split.PreviewWidth != previewMinWidth {
		t.Fatalf("capped split = %+v, want sidebar %d / preview %d",
			split, 119-previewMinWidth, previewMinWidth)
	}

	// previewW floor: when the cap itself goes negative the preview is still
	// pinned at previewMinWidth (degenerate narrow terminal).
	p.sidebarWidth = 40
	narrow := p.previewSplitFor(30)
	if narrow.PreviewWidth != previewMinWidth {
		t.Fatalf("narrow PreviewWidth = %d, want %d", narrow.PreviewWidth, previewMinWidth)
	}
}

func TestPreviewDimensionsClampsAndFallback(t *testing.T) {
	p := surfacePlugin(false)

	// Both kinds lose exactly the panel borders and the one header row.
	worktreeW, worktreeH := p.calculatePreviewDimensions()
	p.shellSelected = true
	shellW, shellH := p.calculatePreviewDimensions()
	p.shellSelected = false
	if worktreeW != shellW || worktreeH != shellH {
		t.Fatalf("dimensions differ by selection kind: %dx%d vs %dx%d",
			worktreeW, worktreeH, shellW, shellH)
	}
	if want := p.height - panelBorderWidth - terminalHeaderRows; worktreeH != want {
		t.Fatalf("preview height = %d, want %d", worktreeH, want)
	}

	// Degenerate small terminal: the 20x5 floors still apply.
	p.sidebarVisible = false
	p.width, p.height = 10, 6
	w, h := p.calculatePreviewDimensions()
	if w != 20 || h != 5 {
		t.Fatalf("tiny preview dims = (%d,%d), want (20,5) floors", w, h)
	}

	// With the sidebar visible the width floor is never reached, because the
	// available-previewMinWidth cap goes negative on a narrow terminal and pins
	// the preview at previewMinWidth. Pre-existing behaviour, asserted so the
	// clamp ordering cannot silently change.
	p.sidebarVisible = true
	if w, _ = p.calculatePreviewDimensions(); w != previewMinWidth-panelOverhead {
		t.Fatalf("tiny split width = %d, want %d", w, previewMinWidth-panelOverhead)
	}
}

func TestTerminalSurfaceGeometryUnsizedPlugin(t *testing.T) {
	p := surfacePlugin(false)
	p.width, p.height = 0, 0
	if surface := p.terminalSurfaceGeometry(false); surface.OK {
		t.Fatalf("unsized plugin surface = %+v, want !OK", surface)
	}
	// The term panel cannot be placed while hidden.
	p = surfacePlugin(false)
	if surface := p.terminalSurfaceGeometry(true); surface.OK {
		t.Fatalf("hidden term panel surface = %+v, want !OK", surface)
	}
}

// TestTerminalSurfaceGeometryMatchesRenderedOrigin is the regression guard the
// six hand-rolled copies of this arithmetic never had: it asserts the origin
// the helper reports is the row and column the render path actually draws the
// terminal's first content row on.
func TestTerminalSurfaceGeometryMatchesRenderedOrigin(t *testing.T) {
	tests := []struct {
		name   string
		marker string
		setup  func(p *Plugin)
	}{
		{
			name:   "worktree with sidebar",
			marker: "AGENT00",
		},
		{
			name:   "worktree sidebar hidden",
			marker: "AGENT00",
			setup:  func(p *Plugin) { p.sidebarVisible = false },
		},
		{
			name:   "worktree flash active",
			marker: "AGENT00",
			setup:  func(p *Plugin) { p.flashPreviewTime = time.Now() },
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := surfacePlugin(false)
			if tc.setup != nil {
				tc.setup(p)
			}
			surface := p.terminalSurfaceGeometry(false)
			if !surface.OK {
				t.Fatalf("surface not placeable: %+v", surface)
			}
			rendered := p.View(p.width, p.height)
			row, col := findMarker(t, rendered, tc.marker)
			if row != surface.Y || col != surface.X {
				t.Fatalf("terminal drawn at (%d,%d), surface reports (%d,%d)",
					col, row, surface.X, surface.Y)
			}
		})
	}

	t.Run("shell with sidebar", func(t *testing.T) {
		p := surfacePlugin(true)
		surface := p.terminalSurfaceGeometry(false)
		rendered := p.View(p.width, p.height)
		row, col := findMarker(t, rendered, "SHELL00")
		if row != surface.Y || col != surface.X {
			t.Fatalf("shell terminal drawn at (%d,%d), surface reports (%d,%d)",
				col, row, surface.X, surface.Y)
		}
		// Both kinds now put their chips on the terminal's own header row, so a
		// shell and a worktree start on the same row.
		worktree := surfacePlugin(false).terminalSurfaceGeometry(false)
		if worktree.Y != surface.Y {
			t.Fatalf("shell Y %d vs worktree Y %d, want the same row", surface.Y, worktree.Y)
		}
	})
}

func TestTerminalSurfaceGeometryTermPanelMatchesRenderedOrigin(t *testing.T) {
	for _, tc := range []struct {
		name   string
		layout TermPanelLayout
		shell  bool
	}{
		{"worktree bottom", TermPanelBottom, false},
		{"worktree right", TermPanelRight, false},
		{"shell bottom", TermPanelBottom, true},
		{"shell right", TermPanelRight, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := surfacePlugin(tc.shell)
			p.termPanelVisible = true
			p.termPanelSize = 50
			p.termPanelLayout = tc.layout
			p.termPanelSession = "panel-session"
			p.termPanelPaneID = "%13"
			p.termPanelOutput = markerBuffer("PANEL", 3)

			surface := p.terminalSurfaceGeometry(true)
			if !surface.OK {
				t.Fatalf("term panel surface not placeable: %+v", surface)
			}
			rendered := p.View(p.width, p.height)
			row, col := findMarker(t, rendered, "PANEL00")
			if row != surface.Y || col != surface.X {
				t.Fatalf("term panel drawn at (%d,%d), surface reports (%d,%d)",
					col, row, surface.X, surface.Y)
			}
			if surface.HeaderY != surface.Y-terminalHeaderRows {
				t.Fatalf("HeaderY = %d, want %d", surface.HeaderY, surface.Y-terminalHeaderRows)
			}

			// The panel's own size is the split's, not the whole preview's.
			width, height, _ := p.calculateTermPanelDimensions()
			if surface.Width != width || surface.Height != height {
				t.Fatalf("surface size = (%d,%d), want (%d,%d)",
					surface.Width, surface.Height, width, height)
			}
		})
	}
}

// The whole point of the surface: one row of chrome above a terminal, never two
// and never three. Asserted against the rendered frame for every surface there
// is, because a stray blank line is exactly the kind of thing only the pixels
// can catch.
func TestExactlyOneHeaderRowAboveEveryTerminal(t *testing.T) {
	panelPlugin := func(shell bool, layout TermPanelLayout) *Plugin {
		p := surfacePlugin(shell)
		p.termPanelVisible = true
		p.termPanelSize = 50
		p.termPanelLayout = layout
		p.termPanelSession = "panel-session"
		p.termPanelPaneID = "%13"
		p.termPanelOutput = markerBuffer("PANEL", 3)
		return p
	}

	for _, tc := range []struct {
		name     string
		plugin   *Plugin
		marker   string
		termPane bool
		// aboveHeader is the row expected directly above the header: the panel
		// border for a primary surface, the split's divider for the bottom
		// layout's lower child.
		aboveHeader string
	}{
		{"shell", surfacePlugin(true), "SHELL00", false, "border"},
		{"worktree", surfacePlugin(false), "AGENT00", false, "border"},
		{"term panel right child", panelPlugin(false, TermPanelRight), "PANEL00", true, "border"},
		{"term panel bottom child", panelPlugin(false, TermPanelBottom), "PANEL00", true, "divider"},
		{"term panel bottom primary", panelPlugin(false, TermPanelBottom), "AGENT00", false, "border"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := tc.plugin
			surface := p.terminalSurfaceGeometry(tc.termPane)
			if !surface.OK {
				t.Fatalf("surface not placeable: %+v", surface)
			}
			rendered := p.View(p.width, p.height)
			row, _ := findMarker(t, rendered, tc.marker)
			if row != surface.Y {
				t.Fatalf("terminal drawn on row %d, surface reports %d", row, surface.Y)
			}
			if row-surface.HeaderY != 1 {
				t.Fatalf("%d rows between header (%d) and terminal (%d), want 1",
					row-surface.HeaderY, surface.HeaderY, row)
			}

			lines := strings.Split(rendered, "\n")
			header := ansi.Strip(lines[surface.HeaderY])
			if strings.TrimSpace(header) == "" {
				t.Fatalf("header row %d is blank: %q", surface.HeaderY, header)
			}
			above := ansi.Strip(lines[surface.HeaderY-1])
			switch tc.aboveHeader {
			case "border":
				if surface.HeaderY != previewBorderRows {
					t.Fatalf("header row = %d, want the row under the panel border (%d)",
						surface.HeaderY, previewBorderRows)
				}
			case "divider":
				// The bottom child's header sits directly on the split rule; a
				// second row of chrome would show up as a blank line here.
				if !strings.Contains(above, "─") {
					t.Fatalf("row above the header = %q, want the split divider", above)
				}
			}
		})
	}
}

// The chips name the surface and carry its hit regions, so they outrank the
// hints when the row runs out of columns — and a chip is dropped whole rather
// than sliced in half.
func TestTerminalHeaderRowTruncatesHintsFirst(t *testing.T) {
	keep := func(value string, max int) string { return ansi.Truncate(value, max, "") }
	chips := []string{"[Output]", "[Diff]", "[Task]"}
	const hints = "t to attach • E for interactive • ctrl-b d to detach"

	wide := terminalHeaderRow(chips, hints, 80, 0, keep)
	if !strings.Contains(wide, "[Task]") || !strings.HasSuffix(wide, "to detach") {
		t.Fatalf("wide header = %q, want all chips and the full hints", wide)
	}
	if got := ansi.StringWidth(wide); got != 80 {
		t.Fatalf("wide header width = %d, want 80", got)
	}

	// Narrow enough that the hints have to give: the chips are untouched.
	narrow := terminalHeaderRow(chips, hints, 30, 0, keep)
	if !strings.HasPrefix(narrow, "[Output] [Diff] [Task]") {
		t.Fatalf("narrow header = %q, want the chips intact", narrow)
	}
	if ansi.StringWidth(narrow) > 30 || strings.Contains(narrow, "\n") {
		t.Fatalf("narrow header = %q, want one row of at most 30 columns", narrow)
	}
	if strings.Contains(narrow, "to detach") {
		t.Fatalf("narrow header = %q, want the hints clipped from the right", narrow)
	}

	// Narrower still: whole chips drop off the end, none is clipped mid-chip.
	for width := 1; width <= 40; width++ {
		row := terminalHeaderRow(chips, hints, width, 0, keep)
		if ansi.StringWidth(row) > width {
			t.Fatalf("width %d produced a %d-column row: %q", width, ansi.StringWidth(row), row)
		}
		if strings.Count(row, "[") != strings.Count(row, "]") {
			t.Fatalf("width %d clipped a chip in half: %q", width, row)
		}
	}
	if row := terminalHeaderRow(chips, hints, 15, 0, keep); row != "[Output] [Diff]" {
		t.Fatalf("15-column header = %q, want the first two chips only", row)
	}
}

// The rendered worktree header keeps its clickable chips at a realistic narrow
// width: the hit regions registered at the border row have to stay over chips.
func TestRenderedHeaderKeepsChipsAtNarrowWidth(t *testing.T) {
	p := surfacePlugin(false)
	p.width = 62 // preview pane pinned at its previewMinWidth floor
	surface := p.terminalSurfaceGeometry(false)
	header := ansi.Strip(strings.Split(p.View(p.width, p.height), "\n")[surface.HeaderY])
	for _, chip := range []string{"Output", "Diff", "Task"} {
		if !strings.Contains(header, chip) {
			t.Fatalf("narrow header %q dropped the %q chip", header, chip)
		}
	}
}

// The attach flash used to be prepended as an extra row, which pushed the
// terminal down one and left the term-panel hit regions a row high for its
// duration. It now lives in the header's right region and costs nothing.
func TestFlashHintRendersInHeaderAndAddsNoRow(t *testing.T) {
	p := surfacePlugin(false)
	before := p.terminalSurfaceGeometry(false)
	beforeRow, beforeCol := findMarker(t, p.View(p.width, p.height), "AGENT00")

	p.flashPreviewTime = time.Now()
	during := p.terminalSurfaceGeometry(false)
	if during != before {
		t.Fatalf("flash moved or resized the surface: %+v vs %+v", during, before)
	}

	rendered := p.View(p.width, p.height)
	row, col := findMarker(t, rendered, "AGENT00")
	if row != beforeRow || col != beforeCol {
		t.Fatalf("flash moved the terminal from (%d,%d) to (%d,%d)",
			beforeCol, beforeRow, col, row)
	}

	header := ansi.Strip(strings.Split(rendered, "\n")[during.HeaderY])
	if !strings.Contains(header, "Enter or double-click to attach") {
		t.Fatalf("flash hint missing from the header row: %q", header)
	}
	// It belongs to the right region, so the tab chips still lead the row.
	if !strings.Contains(header, "Output") ||
		strings.Index(header, "Output") > strings.Index(header, "Enter or double-click") {
		t.Fatalf("flash hint displaced the left chips: %q", header)
	}
}

func TestTerminalScrollState(t *testing.T) {
	p := surfacePlugin(false)
	p.previewScroll = 7

	follow, offset, fromBottom := p.terminalScrollState(false)
	if follow || offset != 7 || !fromBottom {
		t.Fatalf("primary scroll state = (%v,%d,%v), want (false,7,true)", follow, offset, fromBottom)
	}

	p.previewFreeze.Freeze(5)
	follow, offset, fromBottom = p.terminalScrollState(false)
	if follow || offset != 5 || fromBottom {
		t.Fatalf("pinned primary scroll state = (%v,%d,%v), want (false,5,false)",
			follow, offset, fromBottom)
	}
	p.previewFreeze.Release()
	p.previewScroll = 0
	if follow, _, _ = p.terminalScrollState(false); !follow {
		t.Fatal("a window against the live bottom is not following output")
	}

	p.termPanelScroll = 3
	follow, offset, fromBottom = p.terminalScrollState(true)
	if follow || offset != 3 || !fromBottom {
		t.Fatalf("panel scroll state = (%v,%d,%v), want (false,3,true)", follow, offset, fromBottom)
	}

	p.termPanelFreeze.Freeze(11)
	follow, offset, fromBottom = p.terminalScrollState(true)
	if follow || offset != 11 || fromBottom {
		t.Fatalf("anchored panel scroll state = (%v,%d,%v), want (false,11,false)",
			follow, offset, fromBottom)
	}
}

func TestResolvedPaneGeometryPrefersInteractiveState(t *testing.T) {
	p := surfacePlugin(false)
	if w, h := p.resolvedPaneGeometry(false, true); w != 0 || h != 0 {
		t.Fatalf("unknown geometry = (%d,%d), want (0,0)", w, h)
	}

	p.interactiveState = &InteractiveState{Active: true, PaneWidth: 90, PaneHeight: 30}
	if w, h := p.resolvedPaneGeometry(false, true); w != 90 || h != 30 {
		t.Fatalf("interactive geometry = (%d,%d), want (90,30)", w, h)
	}
	// A surface the interactive state does not describe must not adopt its size.
	if w, h := p.resolvedPaneGeometry(false, false); w != 0 || h != 0 {
		t.Fatalf("non-interactive geometry = (%d,%d), want (0,0)", w, h)
	}
}

// interactiveMouseCoords used to derive the terminal panel's origin from the
// primary terminal's viewport height while the split itself divides the
// container that height was carved out of. In the bottom layout the two floors
// disagree for about half of all window heights, and a click was then forwarded
// to the tmux row above or below the one under the cursor. The origin now comes
// from the same geometry the render path draws with, so the mapping holds at
// every height.
func TestTermPanelBottomMouseRowMatchesRenderedRow(t *testing.T) {
	for height := 24; height <= 41; height++ {
		t.Run(fmt.Sprintf("height %d", height), func(t *testing.T) {
			p := surfacePlugin(false)
			p.height = height
			p.termPanelVisible = true
			p.termPanelFocused = true
			p.termPanelSize = 50
			p.termPanelLayout = TermPanelBottom
			p.termPanelSession = "panel-session"
			p.termPanelPaneID = "%13"

			width, panelHeight, _ := p.calculateTermPanelDimensions()
			// A pane exactly the size of its viewport: every viewport row is the
			// tmux row with the same index, so an off-by-one is unambiguous.
			p.termPanelOutput = markerBuffer("PANEL", panelHeight)
			p.viewMode = ViewModeInteractive
			p.interactiveState = &InteractiveState{
				Active: true, TermPanel: true,
				TargetSession: "panel-session", TargetPane: "%13",
				PaneWidth: width, PaneHeight: panelHeight, CursorVisible: true,
			}

			surface := p.terminalSurfaceGeometry(true)
			if !surface.OK {
				t.Fatalf("panel surface not placeable: %+v", surface)
			}
			// The rendered frame is the ground truth for where the panel's rows are.
			renderedRow, renderedCol := findMarker(t, p.View(p.width, p.height), "PANEL00")
			if renderedRow != surface.Y || renderedCol != surface.X {
				t.Fatalf("panel drawn at (%d,%d), surface reports (%d,%d)",
					renderedCol, renderedRow, surface.X, surface.Y)
			}

			for _, offset := range []int{0, 1, panelHeight - 1} {
				col, row, ok := p.interactiveMouseCoords(surface.X, renderedRow+offset)
				if !ok {
					t.Fatalf("row offset %d reported no hit", offset)
				}
				if col != 1 || row != offset+1 {
					t.Fatalf("click on rendered row %d mapped to tmux (%d,%d), want (1,%d)",
						renderedRow+offset, col, row, offset+1)
				}
			}
		})
	}
}

// The Output tab is still the Output tab when there is no terminal to draw.
// A freshly created worktree has no agent, and the tab chips used to vanish
// with the terminal while their click regions stayed live under the message —
// so clicking "No agent running" silently switched tabs.
func TestOutputTabHeaderSurvivesEveryState(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(p *Plugin)
		body  string
	}{
		{
			name:  "no agent",
			setup: func(p *Plugin) { p.worktrees[0].Agent = nil },
			body:  "No agent running",
		},
		{
			name: "orphaned",
			setup: func(p *Plugin) {
				p.worktrees[0].Agent = nil
				p.worktrees[0].IsOrphaned = true
			},
			body: "Session Ended",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := surfacePlugin(false)
			tc.setup(p)
			rendered := p.View(p.width, p.height)

			header := ansi.Strip(strings.Split(rendered, "\n")[previewBorderRows])
			for _, chip := range []string{"Output", "Diff", "Task"} {
				if !strings.Contains(header, chip) {
					t.Fatalf("header %q dropped the %q chip", header, chip)
				}
			}
			if !strings.Contains(ansi.Strip(rendered), tc.body) {
				t.Fatalf("rendered frame lost its %q message", tc.body)
			}

			// The chips are drawn, so their hit regions belong on that row.
			hit := p.mouseHandler.HitMap.Test(p.previewSplit().ContentX+1, previewBorderRows)
			if hit == nil || hit.ID != regionPreviewTab {
				t.Fatalf("no tab hit region over the chips: %+v", hit)
			}
		})
	}
}

// The mirror image: states that draw no chips register no click targets, so
// nothing under the welcome guide or the shell primer switches tabs.
func TestPreviewTabRegionsOnlyWhereChipsAreDrawn(t *testing.T) {
	countTabRegions := func(p *Plugin) int {
		p.View(p.width, p.height)
		n := 0
		for _, region := range p.mouseHandler.HitMap.Regions() {
			if region.ID == regionPreviewTab {
				n++
			}
		}
		return n
	}

	if got := countTabRegions(surfacePlugin(false)); got != 3 {
		t.Fatalf("worktree tab regions = %d, want 3", got)
	}
	if got := countTabRegions(surfacePlugin(true)); got != 0 {
		t.Fatalf("shell tab regions = %d, want 0", got)
	}

	main := surfacePlugin(false)
	main.worktrees[0].IsMain = true
	if got := countTabRegions(main); got != 0 {
		t.Fatalf("main-worktree tab regions = %d, want 0", got)
	}

	empty := surfacePlugin(false)
	empty.worktrees = nil
	if got := countTabRegions(empty); got != 0 {
		t.Fatalf("welcome-guide tab regions = %d, want 0", got)
	}
}

// While interactive the tab chips are decoration and the exit key is not, so
// the header's usual "chips first" rule is inverted for it: at 70 and 60
// columns the chips give way rather than let the way out get clipped.
func TestInteractiveExitHintSurvivesNarrowWidths(t *testing.T) {
	for _, width := range []int{120, 90, 70, 60} {
		p := surfacePlugin(false)
		p.width = width
		p.viewMode = ViewModeInteractive
		p.interactiveState = &InteractiveState{
			Active: true, TargetSession: "agent-session", TargetPane: "%11",
			CursorVisible: true,
		}
		surface := p.terminalSurfaceGeometry(false)
		header := ansi.Strip(strings.Split(p.View(p.width, p.height), "\n")[surface.HeaderY])

		want := "INTERACTIVE " + p.getInteractiveExitKey() + " exit"
		if !strings.Contains(header, want) {
			t.Fatalf("width %d: header %q lost the exit hint %q", width, header, want)
		}
		if strings.Contains(header, "\n") {
			t.Fatalf("width %d: header spilled onto a second row: %q", width, header)
		}
	}
}

// hintFloor inverts the header's usual priority for the columns it names.
func TestTerminalHeaderRowHintFloorDropsChips(t *testing.T) {
	keep := func(value string, max int) string { return ansi.Truncate(value, max, "") }
	chips := []string{"[Output]", "[Diff]", "[Task]"}
	const hints = "INTERACTIVE ctrl+\\ exit"

	row := terminalHeaderRow(chips, hints, 30, ansi.StringWidth(hints), keep)
	if !strings.HasSuffix(row, hints) {
		t.Fatalf("floored header = %q, want the hints whole", row)
	}
	if strings.Contains(row, "[Diff]") {
		t.Fatalf("floored header = %q, want chips dropped to make room", row)
	}
	if got := ansi.StringWidth(row); got > 30 {
		t.Fatalf("floored header width = %d, want at most 30", got)
	}

	// Too narrow for even the floor: still one row, never wider than width.
	tiny := terminalHeaderRow(chips, hints, 12, ansi.StringWidth(hints), keep)
	if ansi.StringWidth(tiny) > 12 || strings.Contains(tiny, "\n") {
		t.Fatalf("tiny floored header = %q", tiny)
	}
}

// The renderers bail to a full-preview, output-only layout when the split's two
// floors do not fit. The sizer and the geometry have to agree that there is no
// panel at all, or its tmux pane is resized to a split nothing draws and the
// cursor is placed against a surface that is nowhere on screen.
func TestTermPanelDimensionsFallBackWhenSplitDoesNotFit(t *testing.T) {
	for _, layout := range []TermPanelLayout{TermPanelBottom, TermPanelRight} {
		p := surfacePlugin(false)
		p.sidebarVisible = false
		p.width, p.height = 10, 6 // below both floors; preview clamps to 20x5
		p.termPanelVisible = true
		p.termPanelLayout = layout
		p.termPanelSize = 50

		previewWidth, previewHeight := p.calculatePreviewDimensions()
		if w, h, ok := p.calculateTermPanelDimensions(); ok {
			t.Fatalf("layout %v: term panel dims = (%d,%d), want no panel", layout, w, h)
		}
		if surface := p.terminalSurfaceGeometry(true); surface.OK {
			t.Fatalf("layout %v: term panel surface = %#v, want none", layout, surface)
		}
		if w, h := p.calculateAgentPaneDimensions(); w != previewWidth || h != previewHeight {
			t.Fatalf("layout %v: agent pane dims = (%d,%d), want the full preview (%d,%d)",
				layout, w, h, previewWidth, previewHeight)
		}
	}
}

// The regions are measured from the chips, so each one has to sit over the tab
// it names — the hand-rolled widths they replaced were a set of magic numbers
// nothing checked against the pixels.
func TestPreviewTabRegionsSitOverTheirChips(t *testing.T) {
	p := surfacePlugin(false)
	frame := p.View(p.width, p.height)
	header := strings.Split(frame, "\n")[previewBorderRows]

	names := []string{"Output", "Diff", "Task"}
	seen := 0
	for _, region := range p.mouseHandler.HitMap.Regions() {
		if region.ID != regionPreviewTab {
			continue
		}
		index, _ := region.Data.(int)
		under := ansi.Strip(ansi.Truncate(ansi.TruncateLeft(header, region.Rect.X, ""), region.Rect.W, ""))
		if !strings.Contains(under, names[index]) {
			t.Fatalf("region %d covers %q, want the %q chip", index, under, names[index])
		}
		seen++
	}
	if seen != len(names) {
		t.Fatalf("registered %d tab regions, want %d", seen, len(names))
	}
}

// A narrow interactive header drops chips to keep the exit hint. The regions
// have to drop with them: a region left behind sits on top of the hint, and
// clicking the word INTERACTIVE both exits interactive mode and switches tab.
func TestPreviewTabRegionsDropWithTheChipsTheHeaderDropped(t *testing.T) {
	p := surfacePlugin(false)
	p.sidebarVisible = false
	p.width = previewMinWidth // 36 content columns: too few for chips and hint
	p.viewMode = ViewModeInteractive
	p.interactiveState = &InteractiveState{
		Active: true, TargetSession: "agent-session", TargetPane: "%11",
	}

	frame := p.View(p.width, p.height)
	header := ansi.Strip(strings.Split(frame, "\n")[previewBorderRows])
	hintAt := strings.Index(header, "INTERACTIVE")
	if hintAt < 0 {
		t.Fatalf("header %q does not carry the exit hint", header)
	}

	names := []string{"Output", "Diff", "Task"}
	drawn := 0
	for _, name := range names {
		if idx := strings.Index(header, name); idx >= 0 && idx < hintAt {
			drawn++
		}
	}
	if drawn == len(names) {
		t.Fatalf("header %q kept every chip; widen the case until some are dropped", header)
	}

	seen := 0
	for _, region := range p.mouseHandler.HitMap.Regions() {
		if region.ID != regionPreviewTab {
			continue
		}
		seen++
		index, _ := region.Data.(int)
		if index >= drawn {
			t.Fatalf("region %d registered for a chip the header dropped (%q)", index, header)
		}
		// Columns are plugin-local; the header row starts at the preview's first
		// content column.
		end := region.Rect.X + region.Rect.W - p.previewSplit().ContentX
		if end > hintAt {
			t.Fatalf("region %d ends at column %d, past the hint at %d (%q)",
				index, end, hintAt, header)
		}
	}
	if seen != drawn {
		t.Fatalf("registered %d tab regions, want %d drawn chips", seen, drawn)
	}
}
