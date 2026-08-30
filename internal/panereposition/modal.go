package panereposition

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/modal"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/ui"
)

const (
	ModalContext = "pane-reposition"
	cellAction   = "pane-layout-cell:"
)

// ModalAction is a host-facing result from the shared reposition controller.
type ModalAction int

const (
	ModalIdle ModalAction = iota
	ModalChanged
	ModalCommit
	ModalCancel
)

// ModalResult separates modal lifecycle from a refused/no-op move. Reason is
// display-ready and never implies that the draft or live tree was mutated.
type ModalResult struct {
	Action ModalAction
	Reason string
}

// CommitResult is an atomic replay result. Root is the original live root with
// source leaves grafted in place; the controller never returns its draft clone.
type CommitResult struct {
	Root     *panelayout.Node
	Focus    int
	ZoomLeaf int
	Moved    bool
	Reason   string
}

// Controller owns one modal draft. It is presentation-neutral about the host:
// scope identifies the active surface, root identity catches replacement, and
// fingerprint catches in-place structural edits while the modal is open.
type Controller struct {
	scope               string
	originalRoot        *panelayout.Node
	originalFingerprint string
	leafID              int
	draft               *panelayout.Node
	moves               []panelayout.MoveDestination
	box                 panelayout.Box
	floors              panelayout.Floors
	zoomed              bool
	title               string
	dialog              *modal.Modal
	applyLive           func(*panelayout.Node, panelayout.MovePlan) (*panelayout.Node, int)
}

// NewController starts a detached draft for one live leaf.
func NewController(scope string, root *panelayout.Node, leafID int, box panelayout.Box, floors panelayout.Floors, zoomed bool, title string) *Controller {
	leaf := panelayout.Find(root, leafID)
	if scope == "" || root == nil || leaf == nil || leaf.Split != nil {
		return nil
	}
	return &Controller{
		scope:               scope,
		originalRoot:        root,
		originalFingerprint: Fingerprint(root),
		leafID:              leafID,
		draft:               panelayout.Clone(root),
		box:                 box,
		floors:              floors,
		zoomed:              zoomed,
		title:               title,
	}
}

func (c *Controller) LeafID() int {
	if c == nil {
		return 0
	}
	return c.leafID
}

func (c *Controller) Draft() *panelayout.Node {
	if c == nil {
		return nil
	}
	return c.draft
}

func (c *Controller) Zoomed() bool { return c != nil && c.zoomed }

// Modal exposes the declarative modal for wheel-boundary routing.
func (c *Controller) Modal() *modal.Modal {
	if c == nil {
		return nil
	}
	c.ensureModal()
	return c.dialog
}

// HandleKey owns every key while the modal is open. Its custom movement keys
// are intercepted before modal.HandleKey so Enter always commits and Esc always
// discards, independent of which miniature cell the pointer last hovered.
func (c *Controller) HandleKey(msg tea.KeyPressMsg) (ModalResult, tea.Cmd) {
	if c == nil {
		return ModalResult{}, nil
	}
	switch msg.String() {
	case "esc":
		return ModalResult{Action: ModalCancel}, nil
	case "enter":
		return ModalResult{Action: ModalCommit}, nil
	case "z":
		c.zoomed = !c.zoomed
		c.invalidate()
		return ModalResult{Action: ModalChanged}, nil
	case "h", "left":
		return c.moveDirection(panelayout.DirectionLeft), nil
	case "j", "down":
		return c.moveDirection(panelayout.DirectionDown), nil
	case "k", "up":
		return c.moveDirection(panelayout.DirectionUp), nil
	case "l", "right":
		return c.moveDirection(panelayout.DirectionRight), nil
	default:
		c.ensureModal()
		_, cmd := c.dialog.HandleKey(msg)
		return ModalResult{}, cmd
	}
}

func (c *Controller) HandleMouse(msg tea.MouseMsg, handler *mouse.Handler) ModalResult {
	if c == nil || handler == nil {
		return ModalResult{}
	}
	c.ensureModal()
	action := c.dialog.HandleMouse(msg, handler)
	if action == "cancel" {
		return ModalResult{Action: ModalCancel}
	}
	if !strings.HasPrefix(action, cellAction) {
		return ModalResult{}
	}
	cell, ok := panelayout.ParseCell(strings.TrimPrefix(action, cellAction))
	if !ok {
		return ModalResult{Reason: "the move destination is invalid"}
	}
	return c.move(panelayout.MoveDestination{Cell: cell})
}

func (c *Controller) moveDirection(direction panelayout.Direction) ModalResult {
	destination, ok := panelayout.MoveDirection(c.draft, c.leafID, direction)
	if !ok {
		return ModalResult{Reason: BoundaryReason(direction)}
	}
	return c.move(destination)
}

func (c *Controller) move(destination panelayout.MoveDestination) ModalResult {
	outcome := panelayout.PlanMove(c.draft, c.leafID, destination, c.box, c.floors)
	if outcome.Status != panelayout.MoveMoved {
		return ModalResult{Reason: Reason(outcome.Reason)}
	}
	next, focus := panelayout.ApplyMove(c.draft, outcome.Plan)
	if focus != c.leafID || panelayout.Find(next, c.leafID) == nil {
		return ModalResult{Reason: LayoutChangedReason}
	}
	c.draft = next
	c.moves = append(c.moves, destination)
	c.invalidate()
	return ModalResult{Action: ModalChanged}
}

// Commit revalidates every accepted draft move against a fresh clone of the
// still-active live tree before touching the live nodes. A stale scope, tree
// replacement, in-place structural change, or newly refused step returns the
// original root untouched.
func (c *Controller) Commit(scope string, live *panelayout.Node, box panelayout.Box, floors panelayout.Floors) CommitResult {
	result := CommitResult{Root: live, Focus: c.LeafID()}
	if c == nil || scope != c.scope || live != c.originalRoot || Fingerprint(live) != c.originalFingerprint {
		result.Reason = LayoutChangedReason
		return result
	}
	trial := panelayout.Clone(live)
	plans := make([]panelayout.MovePlan, 0, len(c.moves))
	expected := make([]string, 0, len(c.moves))
	for _, destination := range c.moves {
		outcome := panelayout.PlanMove(trial, c.leafID, destination, box, floors)
		if outcome.Status != panelayout.MoveMoved {
			result.Reason = Reason(outcome.Reason)
			return result
		}
		var focus int
		trial, focus = panelayout.ApplyMove(trial, outcome.Plan)
		if focus != c.leafID {
			result.Reason = LayoutChangedReason
			return result
		}
		plans = append(plans, outcome.Plan)
		expected = append(expected, Fingerprint(trial))
	}

	source := panelayout.Find(live, c.leafID)
	snapshot := snapshotTree(live)
	root := live
	for i, plan := range plans {
		apply := c.applyLive
		if apply == nil {
			apply = panelayout.ApplyMove
		}
		root, result.Focus = apply(root, plan)
		// ApplyMove is deterministic for a plan compiled against the same
		// structure. Keep this guard and rollback anyway: it turns any future
		// drift in that invariant into an atomic refusal instead of a partial
		// multi-step commit.
		if result.Focus != c.leafID || panelayout.Find(root, c.leafID) != source || Fingerprint(root) != expected[i] {
			snapshot.restore()
			result.Root = live
			result.Focus = c.leafID
			result.Reason = LayoutChangedReason
			return result
		}
	}
	result.Root = root
	result.Moved = len(plans) > 0
	if c.zoomed {
		result.ZoomLeaf = c.leafID
	}
	return result
}

type treeSnapshot struct {
	nodes  map[*panelayout.Node]panelayout.Node
	splits map[*panelayout.Split]panelayout.Split
}

func snapshotTree(root *panelayout.Node) treeSnapshot {
	snapshot := treeSnapshot{
		nodes:  make(map[*panelayout.Node]panelayout.Node),
		splits: make(map[*panelayout.Split]panelayout.Split),
	}
	var visit func(*panelayout.Node)
	visit = func(node *panelayout.Node) {
		if node == nil {
			return
		}
		snapshot.nodes[node] = *node
		if node.Split != nil {
			snapshot.splits[node.Split] = *node.Split
			visit(node.Split.A)
			visit(node.Split.B)
		}
	}
	visit(root)
	return snapshot
}

func (s treeSnapshot) restore() {
	for split, value := range s.splits {
		*split = value
	}
	for node, value := range s.nodes {
		*node = value
	}
}

// Render draws the declarative modal. Hosts overlay the result onto their own
// background so the controller never needs to know which surface owns it.
func (c *Controller) Render(width, height int, handler *mouse.Handler) string {
	if c == nil {
		return ""
	}
	c.ensureModal()
	return c.dialog.Render(width, height, handler)
}

func (c *Controller) ensureModal() {
	if c == nil || c.dialog != nil {
		return
	}
	title := "Move pane"
	if strings.TrimSpace(c.title) != "" {
		title += " · " + strings.TrimSpace(c.title)
	}
	c.dialog = modal.New(title,
		modal.WithWidth(62),
		modal.WithHints(false),
		modal.WithCloseOnBackdropClick(true),
		modal.WithCustomFooter("hjkl move   z zoom   enter done   esc cancel"),
	).AddSection(modal.Custom(c.renderMiniature, nil))
}

func (c *Controller) invalidate() {
	if c.dialog != nil {
		c.dialog.Invalidate()
	}
}

func (c *Controller) renderMiniature(contentWidth int, _, hoverID string) modal.RenderedSection {
	grid := panelayout.GridOf(c.draft)
	if grid == nil || grid.ColumnCount() == 0 || contentWidth < 1 {
		return modal.RenderedSection{Content: "Layout unavailable"}
	}
	status := "Zoom: off"
	if c.zoomed {
		status = "Zoom: on"
	}
	const gap = 1
	cols := grid.ColumnCount()
	available := max(contentWidth-gap*(cols-1), cols*3)
	baseW, extra := available/cols, available%cols
	maxRows := 1
	for col := 1; col <= cols; col++ {
		maxRows = max(maxRows, grid.RowCount(col))
	}
	totalH := maxRows * 3
	canvas := ui.NewCanvas(contentWidth, totalH)
	focusables := make([]modal.FocusableInfo, 0, panelayout.LeafCount(c.draft))
	x := 0
	for col := 1; col <= cols; col++ {
		colW := baseW
		if col <= extra {
			colW++
		}
		rows := grid.RowCount(col)
		y := 0
		for row := 1; row <= rows; row++ {
			cellH := totalH / rows
			if row <= totalH%rows {
				cellH++
			}
			node := grid.Cell(col, row)
			id := cellAction + panelayout.Cell{Col: col, Row: row}.String()
			label := node.Kind.Name()
			if node.ID == c.leafID {
				label = "[" + label + "]"
			}
			if hoverID == id {
				label = ">" + label
			}
			box := mouse.Rect{X: x, Y: y, W: colW, H: cellH}
			canvas.Blit(box, renderMiniCell(label, colW, cellH))
			focusables = append(focusables, modal.FocusableInfo{ID: id, OffsetX: x, OffsetY: y + 1, Width: colW, Height: cellH, MouseOnly: true})
			y += cellH
		}
		x += colW + gap
	}
	return modal.RenderedSection{Content: status + "\n" + canvas.String(), Focusables: focusables}
}

func renderMiniCell(label string, width, height int) string {
	if width < 2 || height < 2 {
		return ui.FitBlock(label, width, height)
	}
	inner := width - 2
	label = ansi.Truncate(label, inner, "")
	pad := max(inner-ansi.StringWidth(label), 0)
	left := pad / 2
	right := pad - left
	lines := make([]string, height)
	lines[0] = "┌" + strings.Repeat("─", inner) + "┐"
	for row := 1; row < height-1; row++ {
		body := strings.Repeat(" ", inner)
		if row == (height-1)/2 {
			body = strings.Repeat(" ", left) + label + strings.Repeat(" ", right)
		}
		lines[row] = "│" + body + "│"
	}
	lines[height-1] = "└" + strings.Repeat("─", inner) + "┘"
	return strings.Join(lines, "\n")
}

// Zoom is transient view state tied to one active tree identity. It follows a
// leaf through in-place moves and cannot leak to a replacement tree that happens
// to reuse the same numeric leaf ID.
type Zoom struct {
	scope  string
	root   *panelayout.Node
	leafID int
}

func (z *Zoom) Set(scope string, root *panelayout.Node, leafID int) {
	if z == nil {
		return
	}
	if scope == "" || root == nil || panelayout.Find(root, leafID) == nil {
		z.Reset()
		return
	}
	z.scope, z.root, z.leafID = scope, root, leafID
}

func (z *Zoom) Reset() {
	if z != nil {
		*z = Zoom{}
	}
}

func (z *Zoom) Leaf(scope string, root *panelayout.Node) int {
	if z == nil || z.scope != scope || z.root != root || panelayout.Find(root, z.leafID) == nil {
		if z != nil && z.leafID != 0 {
			z.Reset()
		}
		return 0
	}
	return z.leafID
}

func (z *Zoom) Active(scope string, root *panelayout.Node, leafID int) bool {
	return z.Leaf(scope, root) == leafID
}

func (c *Controller) String() string {
	if c == nil {
		return "pane reposition <nil>"
	}
	return fmt.Sprintf("pane reposition leaf=%d moves=%d zoom=%t", c.leafID, len(c.moves), c.zoomed)
}
