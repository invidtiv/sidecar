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
