package paneframe

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/ui"
)

// This package is the single place project and global workspaces decide what a
// pane looks like and where it can be clicked. These tests pin the decisions
// both surfaces inherit, so a change to one of them is a change to both by
// construction rather than by somebody remembering.

type fakeContent struct {
	kind  string
	body  string
	size  Size
	sized int
	cmd   tea.Cmd
}

func (c *fakeContent) Kind() string  { return c.kind }
func (c *fakeContent) Title() string { return c.kind }
func (c *fakeContent) SetSize(size Size) tea.Cmd {
	c.size = size
	c.sized++
	return c.cmd
}
func (c *fakeContent) View(Render) string { return c.body }

type fakeHost struct {
	contents map[int]*fakeContent
	focus    int
	chrome   map[int]Chrome
	handles  map[int]ui.HandleState
	queued   []tea.Cmd
	layout   panelayout.Layout
	laid     bool
	setFocus []int
}

func (h *fakeHost) Content(node *panelayout.Node) Content {
	if node == nil {
		return nil
	}
	content, ok := h.contents[node.ID]
	if !ok {
		return nil
	}
	return content
}

func (h *fakeHost) Focus() int { return h.focus }

func (h *fakeHost) Chrome(node *panelayout.Node) Chrome {
	if node == nil {
		return ChromeIdle
	}
	return h.chrome[node.ID]
}

func (h *fakeHost) HandleState(splitID int) ui.HandleState { return h.handles[splitID] }

func (h *fakeHost) QueueSizeCmd(cmd tea.Cmd) { h.queued = append(h.queued, cmd) }

func (h *fakeHost) SetFocus(node *panelayout.Node) {
	if node == nil {
		return
	}
	h.focus = node.ID
	h.setFocus = append(h.setFocus, node.ID)
}

func (h *fakeHost) Layout() (panelayout.Layout, bool) { return h.layout, h.laid }

func twoLeafTree(axis panelayout.Axis) *panelayout.Node {
	return &panelayout.Node{
		ID: 3,
		Split: &panelayout.Split{
			Axis: axis, Ratio: 50,
			A: &panelayout.Node{ID: 1, Kind: panelayout.Terminal},
			B: &panelayout.Node{ID: 2, Kind: panelayout.Document},
		},
	}
}

func TestInsetIsOnePanelOfChrome(t *testing.T) {
	outer := Box{X: 10, Y: 4, W: 40, H: 12}
	inner := Inset(outer)
	if inner.X != outer.X+ContentInset || inner.Y != outer.Y+BorderRows {
		t.Fatalf("inset origin = (%d,%d), want (%d,%d)", inner.X, inner.Y, outer.X+ContentInset, outer.Y+BorderRows)
	}
	if inner.W != outer.W-Overhead || inner.H != outer.H-BorderWidth {
		t.Fatalf("inset size = %dx%d, want %dx%d", inner.W, inner.H, outer.W-Overhead, outer.H-BorderWidth)
	}
	// The left inset plus the right inset is the whole width overhead. A surface
	// that got this wrong would draw its content one column off centre.
	if ContentInset*2 != Overhead {
		t.Fatalf("ContentInset %d does not halve Overhead %d", ContentInset, Overhead)
	}
	if geom := Geometry(outer); geom.Outer != outer || geom.Inner != inner {
		t.Fatalf("Geometry = %+v, want outer %+v inner %+v", geom, outer, inner)
	}
}

// Floors are stated as content minimums and spent as outer boxes. If this
// stopped adding chrome, a split would be accepted that leaves a pane no room
// for the border it is about to draw.
func TestChromeFloorsBudgetOneBorderPerLeaf(t *testing.T) {
	content := panelayout.Floors{
		Terminal: panelayout.Floor{Width: 10, Height: 3},
		Doc:      panelayout.Floor{Width: 30, Height: 3},
		Issue:    panelayout.Floor{Width: 31, Height: 4},
		Diff:     panelayout.Floor{Width: 32, Height: 5},
	}
	got := ChromeFloors(content)
	for name, pair := range map[string][2]panelayout.Floor{
		"terminal": {content.Terminal, got.Terminal},
		"doc":      {content.Doc, got.Doc},
		"issue":    {content.Issue, got.Issue},
		"diff":     {content.Diff, got.Diff},
	} {
		want := panelayout.Floor{Width: pair[0].Width + Overhead, Height: pair[0].Height + BorderWidth}
		if pair[1] != want {
			t.Fatalf("%s floor = %+v, want %+v", name, pair[1], want)
		}
	}
}

// The handle is one cell but a pointer is not, so both axes widen by one cell
// on each side — and the cell they take is a neighbouring leaf's border, never
// its header row, because layout places OUTER boxes with the divider between
// them.
func TestDividerHitBoxWidensSymmetricallyOnBothAxes(t *testing.T) {
	column := DividerHitBox(panelayout.Divider{Axis: panelayout.Columns, Box: Box{X: 20, Y: 3, W: 1, H: 12}})
	if column != (Box{X: 19, Y: 3, W: 3, H: 12}) {
		t.Fatalf("column hit = %+v, want one cell either side", column)
	}
	row := DividerHitBox(panelayout.Divider{Axis: panelayout.Rows, Box: Box{X: 5, Y: 9, W: 30, H: 1}})
	if row != (Box{X: 5, Y: 8, W: 30, H: 3}) {
		t.Fatalf("row hit = %+v, want one cell either side", row)
	}
}

// The widened target must not reach a leaf's header row, which is where its
// tabs and close button live. With OUTER boxes the divider is one cell from
// each leaf's border and two from its header, so one cell of slack is exactly
// what fits.
func TestDividerHitBoxStopsAtTheNeighbouringBorders(t *testing.T) {
	root := twoLeafTree(panelayout.Rows)
	peer := Box{W: 60, H: 24}
	layout, ok := panelayout.LayoutTree(root, peer, ChromeFloors(panelayout.Floors{
		Terminal: panelayout.Floor{Width: 10, Height: 3},
		Doc:      panelayout.Floor{Width: 10, Height: 3},
	}), 1)
	if !ok || len(layout.Dividers) != 1 {
		t.Fatalf("layout = %+v ok=%v", layout, ok)
	}
	hit := DividerHitBox(layout.Dividers[0])
	for _, placement := range layout.Leaves {
		header := Inset(placement.Box).Y
		if header >= hit.Y && header < hit.Y+hit.H {
			t.Fatalf("divider target %+v covers leaf %+v's header row %d", hit, placement.Box, header)
		}
	}
}

func TestHandleStateResolvesPerSplit(t *testing.T) {
	cases := []struct {
		name       string
		splitID    int
		dragging   bool
		dragSplit  int
		hovering   bool
		hoverSplit int
		want       ui.HandleState
	}{
		{name: "idle", splitID: 7, want: ui.HandleIdle},
		{name: "hovered", splitID: 7, hovering: true, hoverSplit: 7, want: ui.HandleHover},
		{name: "hover on another split", splitID: 7, hovering: true, hoverSplit: 8, want: ui.HandleIdle},
		{name: "dragged", splitID: 7, dragging: true, dragSplit: 7, want: ui.HandleDrag},
		{name: "drag on another split", splitID: 7, dragging: true, dragSplit: 8, want: ui.HandleIdle},
		// A drag with no split recorded is the surface's other handles — the
		// sidebar split, the diff list — and stays with whichever asked.
		{name: "drag with no split recorded", splitID: 7, dragging: true, want: ui.HandleDrag},
		{name: "drag beats hover", splitID: 7, dragging: true, dragSplit: 7, hovering: true, hoverSplit: 7, want: ui.HandleDrag},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := HandleStateFor(tc.splitID, tc.dragging, tc.dragSplit, tc.hovering, tc.hoverSplit)
			if got != tc.want {
				t.Fatalf("state = %v, want %v", got, tc.want)
			}
		})
	}
}

// Every leaf gets its own perimeter. This is the property the global workspace
// was missing, and the one a future surface inherits for free by composing here.
func TestComposeWrapsEveryLeafInItsOwnPanel(t *testing.T) {
	for _, axis := range []panelayout.Axis{panelayout.Columns, panelayout.Rows} {
		t.Run(fmt.Sprint(axis), func(t *testing.T) {
			root := twoLeafTree(axis)
			peer := Box{W: 80, H: 20}
			host := &fakeHost{
				contents: map[int]*fakeContent{
					1: {kind: "terminal", body: "term"},
					2: {kind: "doc", body: "doc"},
				},
				focus:   1,
				chrome:  map[int]Chrome{1: ChromeActive, 2: ChromeIdle},
				handles: map[int]ui.HandleState{3: ui.HandleHover},
			}
			layout, ok := panelayout.LayoutTree(root, peer, ChromeFloors(panelayout.Floors{
				Terminal: panelayout.Floor{Width: 10, Height: 3},
				Doc:      panelayout.Floor{Width: 10, Height: 3},
			}), host.Focus())
			if !ok || len(layout.Leaves) != 2 || len(layout.Dividers) != 1 {
				t.Fatalf("layout = %+v ok=%v", layout, ok)
			}
			rows := strings.Split(ansi.Strip(Compose(host, layout, peer, peer.W, peer.H)), "\n")
			for _, placement := range layout.Leaves {
				assertCompletePanel(t, rows, placement.Box)
			}
			// Both leaves were sized to their INNER box, not the box layout placed.
			for _, placement := range layout.Leaves {
				content := host.contents[placement.Node.ID]
				inner := Inset(placement.Box)
				if content.size.Width != inner.W || content.size.Height != inner.H {
					t.Fatalf("leaf %d sized %+v, want inner %dx%d", placement.Node.ID, content.size, inner.W, inner.H)
				}
			}
		})
	}
}

func assertCompletePanel(t *testing.T, rows []string, outer Box) {
	t.Helper()
	for _, corner := range [][2]int{
		{outer.X, outer.Y},
		{outer.X + outer.W - 1, outer.Y},
		{outer.X, outer.Y + outer.H - 1},
		{outer.X + outer.W - 1, outer.Y + outer.H - 1},
	} {
		if corner[1] >= len(rows) {
			t.Fatalf("leaf %+v corner %v is off screen", outer, corner)
		}
		runes := []rune(rows[corner[1]])
		if corner[0] >= len(runes) {
			t.Fatalf("leaf %+v corner %v is off the row", outer, corner)
		}
		if !strings.ContainsRune("╭╮╰╯┌┐└┘╔╗╚╝", runes[corner[0]]) {
			t.Fatalf("leaf %+v corner %v is %q, not a panel corner", outer, corner, runes[corner[0]])
		}
	}
}

// A leaf whose content is gone draws nothing and keeps its box. The alternative
// — letting a neighbour spread into it — is how a missing pane silently moves
// every other pane on screen.
func TestComposeLeavesAnEmptyLeafBlankRatherThanReflowing(t *testing.T) {
	host := &fakeHost{contents: map[int]*fakeContent{}, chrome: map[int]Chrome{}}
	node := &panelayout.Node{ID: 1, Kind: panelayout.Document}
	got := ComposeLeaf(host, panelayout.Placement{Node: node, Box: Box{W: 20, H: 6}}, false)
	if lines := strings.Split(ansi.Strip(got), "\n"); len(lines) != 6 {
		t.Fatalf("empty leaf drew %d rows, want 6", len(lines))
	}
}

// A content that asserts geometry from inside a render has nowhere to dispatch
// it, so the frame hands it back rather than dropping it.
func TestRenderContentHandsSizeCommandsToTheHost(t *testing.T) {
	cmd := func() tea.Msg { return nil }
	host := &fakeHost{contents: map[int]*fakeContent{1: {kind: "terminal", cmd: cmd}}, chrome: map[int]Chrome{}}
	node := &panelayout.Node{ID: 1, Kind: panelayout.Terminal}
	RenderContent(host, node, Box{W: 10, H: 4}, false)
	if len(host.queued) != 1 {
		t.Fatalf("host received %d size commands, want 1", len(host.queued))
	}
}

type recordingSink struct{ calls []string }

func (s *recordingSink) Leaf(node *panelayout.Node, _ Box) {
	s.calls = append(s.calls, fmt.Sprintf("leaf:%d", node.ID))
}
func (s *recordingSink) Divider(splitID int, _ Box) {
	s.calls = append(s.calls, fmt.Sprintf("divider:%d", splitID))
}
func (s *recordingSink) Tabs(node *panelayout.Node, _ Box) {
	s.calls = append(s.calls, fmt.Sprintf("tabs:%d", node.ID))
}
func (s *recordingSink) Close(node *panelayout.Node, _ Box) {
	s.calls = append(s.calls, fmt.Sprintf("close:%d", node.ID))
}
func (s *recordingSink) Body(node *panelayout.Node, _ Box) {
	s.calls = append(s.calls, fmt.Sprintf("body:%d", node.ID))
}

// Registration order is what makes the visible thing the clickable thing, and it
// is the thing two independent implementations got subtly different before. The
// order is asserted here so neither surface can drift from it.
func TestRegisterRegionsOrdersTargetsSoTheTopOneWins(t *testing.T) {
	layout := panelayout.Layout{
		Leaves: []panelayout.Placement{
			{Node: &panelayout.Node{ID: 1}, Box: Box{W: 10, H: 10}},
			{Node: &panelayout.Node{ID: 2}, Box: Box{X: 11, W: 10, H: 10}},
		},
		Dividers: []panelayout.Divider{{SplitID: 3, Box: Box{X: 10, W: 1, H: 10}}},
	}
	sink := &recordingSink{}
	RegisterRegions(sink, layout)
	want := []string{
		"leaf:1", "leaf:2",
		"divider:3",
		"tabs:1", "tabs:2",
		"close:1", "close:2",
		"body:1", "body:2",
	}
	if strings.Join(sink.calls, ",") != strings.Join(want, ",") {
		t.Fatalf("registration order = %v, want %v", sink.calls, want)
	}
}

// Tabs and close buttons are tested against the leaf's INNER box; the leaf
// itself against its OUTER. Handing a tab strip the outer box would put every
// tab target two columns left of the tab a user can see.
func TestRegisterRegionsUsesOuterForLeavesAndInnerForHeaders(t *testing.T) {
	outer := Box{X: 4, Y: 2, W: 30, H: 10}
	var leafBox, tabBox, closeBox, bodyBox Box
	sink := boxSink{
		leaf:  func(b Box) { leafBox = b },
		tabs:  func(b Box) { tabBox = b },
		close: func(b Box) { closeBox = b },
		body:  func(b Box) { bodyBox = b },
	}
	RegisterRegions(sink, panelayout.Layout{
		Leaves: []panelayout.Placement{{Node: &panelayout.Node{ID: 1}, Box: outer}},
	})
	if leafBox != outer {
		t.Fatalf("leaf region = %+v, want OUTER %+v", leafBox, outer)
	}
	inner := Inset(outer)
	for name, got := range map[string]Box{"tabs": tabBox, "close": closeBox, "body": bodyBox} {
		if got != inner {
			t.Fatalf("%s region = %+v, want INNER %+v", name, got, inner)
		}
	}
}

type boxSink struct {
	leaf, tabs, close, body func(Box)
}

func (s boxSink) Leaf(_ *panelayout.Node, b Box)  { s.leaf(b) }
func (s boxSink) Divider(int, Box)                {}
func (s boxSink) Tabs(_ *panelayout.Node, b Box)  { s.tabs(b) }
func (s boxSink) Close(_ *panelayout.Node, b Box) { s.close(b) }
func (s boxSink) Body(_ *panelayout.Node, b Box)  { s.body(b) }

// everyKindTree places one leaf of every kind the pane tree knows, so a kind
// added later fails these tests until it is added here too.
func everyKindTree() *panelayout.Node {
	return &panelayout.Node{
		ID: 10,
		Split: &panelayout.Split{
			Axis: panelayout.Columns, Ratio: 50,
			A: &panelayout.Node{ID: 11, Split: &panelayout.Split{
				Axis: panelayout.Rows, Ratio: 50,
				A: &panelayout.Node{ID: 1, Kind: panelayout.Terminal},
				B: &panelayout.Node{ID: 2, Kind: panelayout.Document},
			}},
			B: &panelayout.Node{ID: 12, Split: &panelayout.Split{
				Axis: panelayout.Rows, Ratio: 50,
				A: &panelayout.Node{ID: 3, Kind: panelayout.Issue},
				B: &panelayout.Node{ID: 4, Kind: panelayout.Diff},
			}},
		},
	}
}

func everyKindLayout(t *testing.T) (panelayout.Layout, Box) {
	t.Helper()
	peer := Box{X: 20, Y: 2, W: 100, H: 40}
	floors := ChromeFloors(panelayout.Floors{
		Terminal: panelayout.Floor{Width: 10, Height: 3},
		Doc:      panelayout.Floor{Width: 10, Height: 3},
		Issue:    panelayout.Floor{Width: 10, Height: 3},
		Diff:     panelayout.Floor{Width: 10, Height: 3},
	})
	layout, ok := panelayout.LayoutTree(everyKindTree(), peer, floors, 1)
	if !ok || len(layout.Leaves) != 4 {
		t.Fatalf("layout = %+v ok=%v, want four placed leaves", layout, ok)
	}
	return layout, peer
}

// Focus is answered from a leaf's box, not from the region that consumed the
// press, so EVERY kind is click-to-focusable — including the terminal, whose
// presses belong to the live pane and never reach a focus handler. The table is
// exhaustive on purpose: a leaf kind added later cannot silently miss this.
func TestFocusLeafAtFocusesEveryLeafKind(t *testing.T) {
	layout, _ := everyKindLayout(t)
	for _, placement := range layout.Leaves {
		node := placement.Node
		t.Run(fmt.Sprint(node.Kind), func(t *testing.T) {
			box := placement.Box
			// The content box, not the outer one: the outer perimeter's cells are
			// the divider's widened drag target where two leaves meet, and that
			// cell is the handle's by the order RegisterRegions lays down.
			inner := Inset(box)
			points := [][2]int{
				{inner.X, inner.Y},
				{inner.X + inner.W - 1, inner.Y + inner.H - 1},
				{inner.X + inner.W/2, inner.Y + inner.H/2},
			}
			for _, point := range points {
				host := &fakeHost{focus: 999, layout: layout, laid: true}
				if !FocusLeafAt(host, point[0], point[1]) {
					t.Fatalf("point %v in leaf %d's box %+v found no leaf", point, node.ID, box)
				}
				if host.Focus() != node.ID {
					t.Fatalf("point %v focused leaf %d, want %d", point, host.Focus(), node.ID)
				}
			}
		})
	}
}

// A press that is not on a leaf leaves focus alone: a divider is the drag
// handle's, and a point off the canvas belongs to whatever is drawn there.
func TestFocusLeafAtIgnoresPointsOffEveryLeaf(t *testing.T) {
	layout, peer := everyKindLayout(t)
	points := map[string][2]int{}
	for i, divider := range layout.Dividers {
		points[fmt.Sprintf("divider %d", i)] = [2]int{divider.Box.X, divider.Box.Y}
	}
	points["left of the canvas"] = [2]int{peer.X - 1, peer.Y + 1}
	points["above the canvas"] = [2]int{peer.X + 1, peer.Y - 1}
	points["past the canvas"] = [2]int{peer.X + peer.W, peer.Y + peer.H}
	for name, point := range points {
		t.Run(name, func(t *testing.T) {
			host := &fakeHost{focus: 4, layout: layout, laid: true}
			if FocusLeafAt(host, point[0], point[1]) {
				t.Fatalf("point %v claimed a leaf", point)
			}
			if len(host.setFocus) != 0 || host.Focus() != 4 {
				t.Fatalf("point %v moved focus to %d", point, host.Focus())
			}
		})
	}
}

// A surface with no placeable tree answers no leaf, and the frame must not
// write focus on the strength of a layout that was never drawn.
func TestFocusLeafAtIgnoresASurfaceWithNoLayout(t *testing.T) {
	host := &fakeHost{focus: 2}
	if FocusLeafAt(host, 5, 5) || len(host.setFocus) != 0 {
		t.Fatalf("focus moved without a layout: %v", host.setFocus)
	}
	if FocusLeafAt(nil, 5, 5) {
		t.Fatal("a nil host claimed a leaf")
	}
}

// DividerHitBox reaches one cell into the border of the leaf on each side, so
// the pointer does not have to be exact to grab a handle. Those cells are the
// handle's, and focus has to agree: a press one cell off the divider must
// resize the split it is on, not also re-focus the pane it was dragged past.
// This is the same precedence RegisterRegions gives dividers over leaf bodies.
func TestFocusLeafAtLeavesTheWidenedDividerTargetToTheHandle(t *testing.T) {
	layout, _ := everyKindLayout(t)
	if len(layout.Dividers) == 0 {
		t.Fatal("the fixture placed no dividers")
	}
	for i, divider := range layout.Dividers {
		hit := DividerHitBox(divider)
		points := [][2]int{
			{hit.X, hit.Y},
			{hit.X + hit.W - 1, hit.Y + hit.H - 1},
			{hit.X + hit.W/2, hit.Y + hit.H/2},
		}
		for _, point := range points {
			host := &fakeHost{focus: 4, layout: layout, laid: true}
			if FocusLeafAt(host, point[0], point[1]) {
				t.Fatalf("divider %d: point %v inside its drag target claimed a leaf", i, point)
			}
			if len(host.setFocus) != 0 {
				t.Fatalf("divider %d: point %v moved focus to %v", i, point, host.setFocus)
			}
		}
	}
	// The widened margin really does overlap a leaf, so the exclusion above is
	// load-bearing rather than a tautology.
	hit := DividerHitBox(layout.Dividers[0])
	overlapped := false
	for _, placement := range layout.Leaves {
		box := placement.Box
		if hit.X < box.X+box.W && box.X < hit.X+hit.W && hit.Y < box.Y+box.H && box.Y < hit.Y+hit.H {
			overlapped = true
		}
	}
	if !overlapped {
		t.Fatal("premise: the widened divider target overlaps its neighbours' boxes")
	}
}
