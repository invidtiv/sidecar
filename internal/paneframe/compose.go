package paneframe

import (
	tea "charm.land/bubbletea/v2"

	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/ui"
)

// Host is everything a surface answers about its own pane tree that the frame
// cannot work out for itself. It is deliberately small: the frame asks what is
// in a leaf, which border it wears, who has focus, and what state a handle is
// in — never what kind of thing a leaf is or how the surface stores it.
//
// Both the project workspace plugin and the global Workspaces browser implement
// it, which is what makes their windowing the same behaviour rather than two
// behaviours that happen to look alike.
type Host interface {
	// Content adapts a leaf to the content contract. A leaf whose content is
	// gone answers nil, and the canvas leaves its box blank rather than letting a
	// neighbour spread into it.
	Content(node *panelayout.Node) Content
	// Chrome is the border this leaf wears, given the surface's focus state.
	Chrome(node *panelayout.Node) Chrome
	// Focus is the leaf ID that owns the surface's keyboard focus.
	Focus() int
	// SetFocus gives one leaf the surface's keyboard focus. It is the write half
	// of Focus, and it exists so the two halves cannot be different values: the
	// ring is drawn from Focus, a pointer moves it through SetFocus, and a
	// surface has no third place to record "who is being typed into". A surface
	// whose live terminal holds the keyboard separately must give it up here —
	// focus that has moved off a leaf and keys that have not is precisely the
	// divergence this pair is for.
	SetFocus(node *panelayout.Node)
	// Layout is the pane tree as the surface last DREW it — the same placement
	// Compose and RegisterRegions were driven with — so the frame can answer
	// which leaf a screen point belongs to. It must be false whenever the tree
	// is not what is on screen. Answering geometry the surface could place but
	// did not draw hands out phantom leaf boxes to whatever replaced the tree,
	// and a pointer then focuses panes the user cannot see.
	Layout() (panelayout.Layout, bool)
	// HandleState is the pointer state of one split's drag handle.
	HandleState(splitID int) ui.HandleState
	// QueueSizeCmd takes a command a content returned from SetSize. A render has
	// no runtime to dispatch one with, so the surface holds it for the next
	// update rather than dropping it.
	QueueSizeCmd(tea.Cmd)
}

// ChromeFloors adds each leaf's own chrome budget to a set of content floors, so
// a tree only claims to fit when every leaf has room for its border as well as
// its content. Callers state the minimum a content needs; the frame knows what
// it costs to draw one.
func ChromeFloors(content panelayout.Floors) panelayout.Floors {
	grow := func(f panelayout.Floor) panelayout.Floor {
		return panelayout.Floor{Width: f.Width + Overhead, Height: f.Height + BorderWidth}
	}
	return panelayout.Floors{
		Terminal: grow(content.Terminal),
		Doc:      grow(content.Doc),
		Issue:    grow(content.Issue),
		Diff:     grow(content.Diff),
		Resource: grow(content.Resource),
	}
}

// Compose draws every leaf and divider of an already-laid-out tree onto the box
// layout gave it. The caller lays out first so it can inspect Layout.Zoomed
// before anything is rendered — composing a leaf sizes its content, which is a
// side effect a surface that is about to decline the frame must not pay.
//
// Joining rendered blocks back together instead would re-derive that geometry in
// string space at every level of nesting, and the levels only have to disagree
// by a cell for a divider to walk sideways. canvas is the surface-local OUTER
// rectangle the tree was laid out in, and width/height are the returned block's
// size: the canvas rectangle in production, a smaller viewport in tests.
func Compose(host Host, layout panelayout.Layout, canvas Box, width, height int) string {
	if host == nil {
		return ""
	}
	surface := ui.NewCanvas(width, height)
	blit := func(box Box, content string) {
		surface.Blit(Box{X: box.X - canvas.X, Y: box.Y - canvas.Y, W: box.W, H: box.H}, content)
	}
	for _, placement := range layout.Leaves {
		blit(placement.Box, ComposeLeaf(host, placement, layout.Zoomed))
	}
	for _, divider := range layout.Dividers {
		blit(divider.Box, RenderDividerHandle(divider, host.HandleState(divider.SplitID)))
	}
	return surface.String()
}

// ComposeLeaf draws one placed leaf's content in its INNER box and wraps the
// OUTER box in this surface's chrome. placement.Box is surface-local OUTER.
//
// Sizing inside a frame is what the document viewer already required, and both
// contents answer nil: a live terminal is resized from the state change that
// moved its box, not from a render. A content that does answer one is asserting
// geometry beyond this process, and a render has nothing to dispatch it with, so
// the frame hands it to the host for the next update rather than dropping it.
func ComposeLeaf(host Host, placement panelayout.Placement, zoomed bool) string {
	geom := Geometry(placement.Box)
	return WrapLeaf(RenderContent(host, placement.Node, geom.Inner, zoomed), geom.Outer, host.Chrome(placement.Node))
}

// RenderContent draws one leaf's body into its INNER box, with no chrome around
// it, so the compose path never asks what kind of leaf it is drawing. A leaf
// with no content draws nothing and the canvas leaves its box blank, rather than
// shifting its neighbours into it. inner is surface-local, which is what turns
// the leaf's box into the rectangle a pointer is tested against.
func RenderContent(host Host, node *panelayout.Node, inner Box, zoomed bool) string {
	content := host.Content(node)
	if content == nil {
		return ""
	}
	if cmd := content.SetSize(Size{Width: inner.W, Height: inner.H}); cmd != nil {
		host.QueueSizeCmd(cmd)
	}
	return content.View(Render{
		Focused: node != nil && host.Focus() == node.ID,
		Zoomed:  zoomed,
		Origin:  mouse.Rect(inner),
	})
}

// LeafAt is the leaf whose OUTER box contains a surface-local point, or nil for
// a point on a divider's drag target, outside the canvas, or on nothing the tree
// placed.
//
// Placement boxes never overlap, so at most one leaf can answer a point and the
// first match is the only match. Dividers are subtracted first because
// DividerHitBox deliberately reaches one cell into the leaf's border on each
// side, and that cell belongs to the handle: this is the same precedence
// RegisterRegions gives it by registering dividers after leaf bodies, said once
// so a pointer cannot resize past a pane and re-focus it in the same press.
func LeafAt(layout panelayout.Layout, x, y int) *panelayout.Node {
	for _, divider := range layout.Dividers {
		if boxContains(DividerHitBox(divider), x, y) {
			return nil
		}
	}
	for _, placement := range layout.Leaves {
		if placement.Node == nil {
			continue
		}
		if boxContains(placement.Box, x, y) {
			return placement.Node
		}
	}
	return nil
}

func boxContains(box Box, x, y int) bool {
	if box.W <= 0 || box.H <= 0 {
		return false
	}
	return x >= box.X && x < box.X+box.W && y >= box.Y && y < box.Y+box.H
}

// FocusLeafAt moves the surface's keyboard focus to the leaf under a pointer,
// and reports whether a leaf was there. It moves focus and nothing else: the
// event carries on to whichever region claimed it, so a press inside a terminal
// leaf still reaches the live pane and a press on a file row still selects it.
//
// Focus is answered from GEOMETRY rather than from the region a press landed
// on, and that is the whole point. Which region consumes a press belongs to the
// content — a terminal leaf's presses are claimed by the live pane and
// forwarded to tmux, a diff leaf's by its file rows — so a surface that hung
// focus off those handlers needed one focus call per leaf kind and got the ring
// wrong for the kind nobody remembered. A leaf's box, by contrast, is something
// every leaf has, including kinds that do not exist yet.
func FocusLeafAt(host Host, x, y int) bool {
	if host == nil {
		return false
	}
	layout, ok := host.Layout()
	if !ok {
		return false
	}
	node := LeafAt(layout, x, y)
	if node == nil {
		return false
	}
	host.SetFocus(node)
	return true
}

// RegionSink is where a surface puts the hit regions one composed frame earns.
// The frame decides the ORDER; the surface decides the region IDs and payloads,
// because those are its own mouse vocabulary.
type RegionSink interface {
	// Leaf covers a whole leaf, OUTER, and is the lowest-priority target.
	Leaf(node *panelayout.Node, outer Box)
	// Divider is one split's widened drag target.
	Divider(splitID int, hit Box)
	// Tabs is a leaf's header tab strip, INNER.
	Tabs(node *panelayout.Node, inner Box)
	// Close is a leaf's header X, INNER.
	Close(node *panelayout.Node, inner Box)
	// Body is anything a leaf's content owns inside its own box — a diff list
	// divider, a file row — INNER. It registers last so it beats everything the
	// frame put down.
	Body(node *panelayout.Node, inner Box)
}

// RegisterRegions puts one composed frame's targets into the sink in the one
// order that makes the visible thing the clickable thing. The order is the whole
// point of this function existing, so it is stated here once rather than
// re-derived per surface:
//
//   - Leaf bodies first: they are the fallback for scroll and focus.
//   - Dividers next, in layout order — each split before the splits inside it.
//     Widened targets overlap once splits nest, and two can only overlap when
//     one split encloses the other, because sibling subtrees are held apart by
//     the divider between them. Registering the enclosing split first leaves the
//     enclosed one last, and HitMap's reverse scan returns it for a point both
//     claim.
//   - Tab strips next: they win the one cell a column divider reaches into a
//     header, which is the cell a click on the leftmost tab lands on.
//   - Close buttons next: the X occupies the right edge the strip no longer
//     claims, and a one-cell miss on a tab must not steal the close.
//   - Content-owned regions last, so they beat the tree divider and the leaf
//     body drawn under them.
func RegisterRegions(sink RegionSink, layout panelayout.Layout) {
	if sink == nil {
		return
	}
	for _, placement := range layout.Leaves {
		sink.Leaf(placement.Node, placement.Box)
	}
	for _, divider := range layout.Dividers {
		sink.Divider(divider.SplitID, DividerHitBox(divider))
	}
	for _, placement := range layout.Leaves {
		sink.Tabs(placement.Node, Inset(placement.Box))
	}
	for _, placement := range layout.Leaves {
		sink.Close(placement.Node, Inset(placement.Box))
	}
	for _, placement := range layout.Leaves {
		sink.Body(placement.Node, Inset(placement.Box))
	}
}
