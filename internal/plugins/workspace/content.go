package workspace

import (
	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/ui"
	"github.com/marcus/sidecar/internal/workspacediff"
)

// Content is what one pane-tree leaf shows. The tree places boxes and the frame
// composes them; neither learns what is inside one. A bottom-relative offset, a
// freeze, a tmux geometry lease are the content's business alone — the
// structure layer never sees a *tty.Model.
//
// The contract is four methods on purpose. Capability beyond it — update,
// focus, keys, pointers, scrolling, persistence — arrives as an optional
// interface when its first real implementation does, not before: a document
// leaf has no native cursor and no transport to gate, and a mandatory method
// invites a wrong stub.
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

// Content kinds are the keys a leaf is persisted under as well as the keys the
// adapter is chosen by, so a leaf cannot be written under one name and restored
// as another.
const (
	contentKindTerminal = "terminal"
	contentKindDoc      = "doc"
	contentKindIssue    = "issue"
	contentKindDiff     = "diff"
)

// Size is the box a content draws into. It is the leaf's whole box, header row
// included: the terminal leaf's header is still drawn by the legacy renderer
// from inside its box, so each content spends its own header row. M1 absorbs
// the terminal panel into the tree, and this becomes the box below the row the
// frame draws.
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
	// Origin is the leaf's box in plugin-local coordinates, which is what hit
	// math needs; the size handed to SetSize is content-local.
	Origin mouse.Rect
}

// paneContent adapts a leaf to the content contract. It is the one place that
// maps a leaf kind to an implementation, so nothing in the render path asks
// what kind of leaf it is drawing. A leaf whose content is gone has none, and
// the canvas leaves its box blank rather than letting a neighbour spread into
// it.
func (p *Plugin) paneContent(node *PaneNode) Content {
	if node == nil {
		return nil
	}
	switch node.Kind {
	case PaneDoc:
		doc := p.docs[node.ContentID]
		if doc == nil || doc.view() == nil {
			return nil
		}
		return &docContent{p: p, doc: doc}
	case PaneIssue:
		issue := p.issues[node.ContentID]
		if issue == nil || issue.view() == nil {
			return nil
		}
		return &issueContent{p: p, issue: issue}
	case PaneDiff:
		diff := p.diffs[node.ContentID]
		if diff == nil || diff.view() == nil {
			return nil
		}
		return &diffContent{p: p, diff: diff}
	default:
		return &terminalContent{p: p}
	}
}

// terminalContent is the terminal leaf. Its header row is drawn from inside its
// body, not by the frame, because the terminal panel still puts a second
// surface with a second header inside this one leaf; M1 makes each surface a
// leaf the frame can head itself.
type terminalContent struct {
	p    *Plugin
	size Size
}

func (c *terminalContent) Kind() string { return contentKindTerminal }

// Title is the selection this terminal is showing, which is the name the
// sidebar chose it by.
func (c *terminalContent) Title() string {
	if c.p.selectingShell() {
		shell := c.p.getSelectedShell()
		if shell == nil || shell.Name == "" {
			return "Shell"
		}
		return shell.Name
	}
	if wt := c.p.selectedWorktree(); wt != nil {
		return wt.Name
	}
	return ""
}

// SetSize records the box and nothing else. A live tmux pane is resized from
// docTerminalResizeCmds, on the state change that moved the box; a resize
// returned here would put a SIGWINCH into the agent on every frame.
func (c *terminalContent) SetSize(size Size) tea.Cmd {
	c.size = size
	return nil
}

// View draws the legacy preview and holds it to the box it was given. The
// legacy renderer answers its states in their own shape — a header row and two
// lines of "no agent running" is three ragged rows for any box — and a leaf that
// hands back less than its rectangle is a leaf whose neighbours are placed by
// the width of its longest line. The frame's compositor would clip and pad it
// anyway; doing it here is what makes the contract true rather than survivable.
func (c *terminalContent) View(Render) string {
	return ui.FitBlock(c.p.renderPreviewContentLegacy(c.size.Width, c.size.Height), c.size.Width, c.size.Height)
}

// docContent is the document leaf: the pane's own header row above a document
// viewport. The header is the pane's, not the viewer's — a leaf that decided
// for itself whether it spent its box's first row would put its body on a
// different relative row than its neighbours', which is the property
// termpreview.HeaderRows exists to state.
type docContent struct {
	p    *Plugin
	doc  *docPane
	size Size
}

func (c *docContent) Kind() string { return contentKindDoc }

func (c *docContent) Title() string {
	if view := c.doc.view(); view != nil {
		return view.Title()
	}
	return ""
}

// SetSize hands the viewer the box below the header row, which is the same
// subtraction termpreview.SurfaceIn makes for a terminal leaf.
func (c *docContent) SetSize(size Size) tea.Cmd {
	c.size = size
	if view := c.doc.view(); view != nil {
		view.SetSize(size.Width, maxInt(size.Height-terminalHeaderRows, 0))
	}
	return nil
}

// View draws the tab strip above the viewer. Focus is the frame's answer, so
// the active tab a click lands on matches the one the leaf drew.
func (c *docContent) View(render Render) string {
	body := ""
	if view := c.doc.view(); view != nil {
		body = view.View()
	}
	return composePaneLeaf(
		c.p.docPaneHeaderRow(c.doc, c.size.Width, render.Focused),
		body)
}

// issueContent is the td issue leaf: the pane's own header row above the issue
// component. It spends its box exactly as the document leaf does, because the
// row the frame draws is the pane's rather than the viewer's, and two leaves
// side by side must put their bodies on the same relative row.
type issueContent struct {
	p     *Plugin
	issue *issuePane
	size  Size
}

func (c *issueContent) Kind() string { return contentKindIssue }

// Title is the active tab's headline, which is its ID until the fetch lands.
func (c *issueContent) Title() string {
	if view := c.issue.view(); view != nil {
		return view.Title()
	}
	return ""
}

func (c *issueContent) SetSize(size Size) tea.Cmd {
	c.size = size
	if view := c.issue.view(); view != nil {
		view.SetSize(size.Width, maxInt(size.Height-terminalHeaderRows, 0))
	}
	return nil
}

func (c *issueContent) View(render Render) string {
	body := ""
	if view := c.issue.view(); view != nil {
		body = view.View()
	}
	return composePaneLeaf(
		c.p.issuePaneHeaderRow(c.issue, c.size.Width, render.Focused),
		body)
}

// diffContent is the Diff leaf: the pane's own header row above the
// workspacediff viewer. It spends its box like the document and issue leaves.
type diffContent struct {
	p    *Plugin
	diff *diffPane
	size Size
}

func (c *diffContent) Kind() string { return contentKindDiff }

func (c *diffContent) Title() string {
	if view := c.diff.view(); view != nil {
		return view.Target.TabLabel()
	}
	return "Diff"
}

func (c *diffContent) SetSize(size Size) tea.Cmd {
	c.size = size
	if view := c.diff.view(); view != nil {
		view.SetSize(size.Width, maxInt(size.Height-terminalHeaderRows, 0))
	}
	return nil
}

func (c *diffContent) View(render Render) string {
	body := ""
	if view := c.diff.view(); view != nil {
		c.p.attachDiffPaintTo(view)
		bodyH := maxInt(c.size.Height-terminalHeaderRows, 0)
		view.SetSize(c.size.Width, bodyH)
		body = view.Render(c.size.Width, bodyH, workspacediff.RenderOpts{
			Truncate: func(s string, w int, suffix string) string {
				if c.p.truncateCache != nil {
					return c.p.truncateCache.Truncate(s, w, suffix)
				}
				return s
			},
			PaintFile: c.p.paintDiffFile,
		})
	}
	return composePaneLeaf(
		c.p.diffPaneHeaderRow(c.diff, c.size.Width, render.Focused),
		body)
}

// composePaneLeaf joins a leaf's header row to the body under it. An empty
// header is a leaf that owes no header row; an empty body still costs the join
// its newline, because a leaf with no box left under its header has spent that
// row all the same.
func composePaneLeaf(header, body string) string {
	if header == "" {
		return body
	}
	return header + "\n" + body
}
