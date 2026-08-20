// Package paneframe is the presentation half of the pane tree: the chrome one
// placed leaf wears, the handle one split draws, and the compositor that turns a
// panelayout.Layout into cells. internal/panelayout owns structure and geometry
// and knows nothing about rendering; this package owns rendering and knows
// nothing about terminals, documents, issues, or persistence.
//
// It exists so the project workspace preview and the global Workspaces browser
// ("Sessions") compose panes through one code path. Every windowing behaviour a
// user can see — per-leaf borders, focus and interactive chrome, drag-handle
// hover and drag colours, the widened divider hit target, the order regions are
// registered in — is decided here once and is therefore the same on both
// surfaces by construction. A change made to one workspace's windowing that does
// not land in the other is a bug in this package's callers, not a feature.
package paneframe

import (
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/termpreview"
	"github.com/marcus/sidecar/internal/ui"

	tea "charm.land/bubbletea/v2"
)

// Box is the pane tree's rectangle. Layout places it; this package draws in it.
type Box = termpreview.Box

// Chrome constants are what one RenderPanel costs a leaf. Layout hands out OUTER
// boxes; a content draws into the INNER box these describe.
const (
	// BorderWidth is the left plus right border, one column each.
	BorderWidth = 2
	// PaddingWidth is the left plus right padding, one column each.
	PaddingWidth = 2
	// Overhead is the total columns a panel spends on chrome.
	Overhead = BorderWidth + PaddingWidth
	// ContentInset is the columns chrome spends on the left alone.
	ContentInset = Overhead / 2
	// BorderRows is the panel's top border row; the bottom costs the same, which
	// is why the height subtraction below is BorderWidth rather than BorderRows.
	BorderRows = 1
	// HeaderRows is the single row every leaf reserves for its tab strip and
	// close button, read from the shared presentation layer so a terminal leaf
	// and a document leaf put their bodies on the same relative row.
	HeaderRows = termpreview.HeaderRows
)

// Geom is one leaf's chrome versus its content. Layout places Outer; tmux, the
// native cursor, tab hits, and content hits all use Inner.
type Geom struct {
	Outer Box
	Inner Box
}

// Inset is the content box inside one RenderPanel: ContentInset columns of
// border plus padding on each side, one border row top and bottom.
func Inset(outer Box) Box {
	return Box{
		X: outer.X + ContentInset,
		Y: outer.Y + BorderRows,
		W: outer.W - Overhead,
		H: outer.H - BorderWidth,
	}
}

// Geometry pairs a placed OUTER box with the INNER box its content draws into.
func Geometry(outer Box) Geom { return Geom{Outer: outer, Inner: Inset(outer)} }

// Size is the box a content draws into: its INNER rectangle, header row
// included. The content spends its own header row.
type Size struct {
	Width, Height int
}

// Render is what the frame knows about a placed leaf and the content does not.
// It carries no theme: styles are process-global in internal/styles, so a
// content reads them there rather than through a copy that can go stale.
type Render struct {
	// Focused is true when this leaf owns the preview's keyboard focus.
	Focused bool
	// Zoomed is true when the box could not hold the whole tree and this leaf
	// was given all of it.
	Zoomed bool
	// Origin is the leaf's INNER box in surface-local coordinates, which is what
	// hit math needs; the size handed to SetSize is content-local.
	Origin mouse.Rect
}

// Content is what one pane-tree leaf shows. The tree places boxes and this
// package composes them; neither learns what is inside one. A bottom-relative
// offset, a freeze, a tmux geometry lease are the content's business alone — the
// structure layer never sees a *tty.Model.
//
// The contract is four methods on purpose. Capability beyond it — update, focus,
// keys, pointers, scrolling, persistence — arrives as an optional interface when
// its first real implementation does, not before: a document leaf has no native
// cursor and no transport to gate, and a mandatory method invites a wrong stub.
type Content interface {
	// Kind is the stable persistence key and registry key for this content.
	Kind() string
	// Title is the content's identity for a header row.
	Title() string
	// SetSize gives the content its box. The command is how a content asserts
	// geometry it owns beyond this process, such as a live tmux pane.
	SetSize(Size) tea.Cmd
	// View draws exactly Size.Height rows of exactly Size.Width columns.
	View(Render) string
}

// Chrome is the border a leaf wears. It is a reader of focus, not a decision a
// content makes: a leaf that chose its own border could disagree with the leaf
// beside it about which one of them has the keyboard.
type Chrome int

const (
	// ChromeIdle is a leaf that does not own the surface's focus.
	ChromeIdle Chrome = iota
	// ChromeActive is the focused leaf's border.
	ChromeActive
	// ChromeInteractive is the focused terminal leaf while the user is typing
	// into the live pane.
	ChromeInteractive
	// ChromeFlash is the focused terminal leaf's attention flash.
	ChromeFlash
	// ChromeNone leaves the placed outer box unframed. Its content receives the
	// whole box, so an already-composed primary surface can sit beside framed
	// passive leaves without acquiring a second, visually nested perimeter.
	ChromeNone
)

// GeometryForChrome pairs a placed box with the content box implied by its
// chrome. Borderless content owns the whole placement; every framed state uses
// the shared panel inset.
func GeometryForChrome(outer Box, chrome Chrome) Geom {
	if chrome == ChromeNone {
		return Geom{Outer: outer, Inner: outer}
	}
	return Geometry(outer)
}

// WrapLeaf draws content inside one leaf's OUTER box. Content bytes are never
// dimmed; only the border states change.
//
// The chrome the caller asks for is passed through EffectiveChrome first, so a
// surface cannot draw a focused leaf while an app-level surface outside every
// pane tree holds the keyboard. Applying it here rather than in each Host is
// what makes exactly-one-focused-pane an app-wide property: every focused
// border on both surfaces, including the lone-preview frame that never goes
// through Compose, is painted by this function.
func WrapLeaf(content string, outer Box, chrome Chrome) string {
	switch EffectiveChrome(chrome) {
	case ChromeNone:
		return ui.FitBlock(content, outer.W, outer.H)
	case ChromeInteractive:
		return styles.RenderPanelWithGradient(content, outer.W, outer.H, styles.GetInteractiveGradient())
	case ChromeFlash:
		return styles.RenderPanelWithGradient(content, outer.W, outer.H, styles.GetFlashGradient())
	case ChromeActive:
		return styles.RenderPanel(content, outer.W, outer.H, true)
	default:
		return styles.RenderPanel(content, outer.W, outer.H, false)
	}
}

// RenderDividerHandle draws one split's 1-cell drag handle in its own state.
func RenderDividerHandle(divider panelayout.Divider, state ui.HandleState) string {
	if divider.Axis == panelayout.Rows {
		return ui.RenderHandle(max(divider.Box.W, 0), false, state)
	}
	return ui.RenderHandle(max(divider.Box.H, 0), true, state)
}

// DividerHitBox widens a 1-cell divider into the forgiving target a pointer is
// actually tested against: the divider plus one cell of the leaf on each side,
// on both axes.
//
// What that cell is, is why this is safe. Layout places OUTER boxes with the
// divider in the gap between them, so the cell either side is a leaf's own
// border — never its header row, which sits one cell further in. A header's
// tabs and close button are therefore never masked, and the two axes can behave
// the same way. (They did not always: before every leaf wore its own chrome, a
// row divider sat directly against the lower leaf's header and could only widen
// upward.)
//
// Tab and close targets are registered after dividers regardless, so even an
// overlap would resolve to the header. This keeps the pointer from having to be
// exact in the first place.
func DividerHitBox(divider panelayout.Divider) Box {
	hit := divider.Box
	if divider.Axis == panelayout.Columns {
		hit.X--
		hit.W += 2
		return hit
	}
	hit.Y--
	hit.H += 2
	return hit
}

// DividerHitBoxFor widens a divider only into neighbouring framed chrome. A
// borderless leaf owns every cell in its placement, so taking its edge column
// or row would steal a real content target. When a nested divider touches more
// than one leaf on a side, every touching leaf must be framed before that side
// contributes the one-cell resize margin.
func DividerHitBoxFor(host Host, layout panelayout.Layout, divider panelayout.Divider) Box {
	if host == nil {
		return DividerHitBox(divider)
	}
	hit := divider.Box
	if divider.Axis == panelayout.Columns {
		if dividerSideFramed(host, layout, divider, -1) {
			hit.X--
			hit.W++
		}
		if dividerSideFramed(host, layout, divider, 1) {
			hit.W++
		}
		return hit
	}
	if dividerSideFramed(host, layout, divider, -1) {
		hit.Y--
		hit.H++
	}
	if dividerSideFramed(host, layout, divider, 1) {
		hit.H++
	}
	return hit
}

func dividerSideFramed(host Host, layout panelayout.Layout, divider panelayout.Divider, side int) bool {
	found := false
	for _, placement := range layout.Leaves {
		box := placement.Box
		touches := false
		if divider.Axis == panelayout.Columns {
			edge := box.X
			if side < 0 {
				edge = box.X + box.W
			}
			dividerEdge := divider.Box.X
			if side > 0 {
				dividerEdge += divider.Box.W
			}
			touches = edge == dividerEdge &&
				box.Y < divider.Box.Y+divider.Box.H && box.Y+box.H > divider.Box.Y
		} else {
			edge := box.Y
			if side < 0 {
				edge = box.Y + box.H
			}
			dividerEdge := divider.Box.Y
			if side > 0 {
				dividerEdge += divider.Box.H
			}
			touches = edge == dividerEdge &&
				box.X < divider.Box.X+divider.Box.W && box.X+box.W > divider.Box.X
		}
		if !touches {
			continue
		}
		found = true
		if host.Chrome(placement.Node) == ChromeNone {
			return false
		}
	}
	return found
}

// HandleStateFor resolves a split's handle state from the surface's pointer
// state. dragSplitID is the split a drag in progress owns (0 when the surface
// does not track one); hoverSplitID is the split the pointer is over.
func HandleStateFor(splitID int, dragging bool, dragSplitID int, hovering bool, hoverSplitID int) ui.HandleState {
	if dragging && dragSplitID != 0 && dragSplitID != splitID {
		dragging = false
	}
	if hovering && hoverSplitID != splitID {
		hovering = false
	}
	return ui.HandleStateFrom(hovering, dragging)
}
