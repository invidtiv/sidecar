// Package contentpanes owns the host-independent lifecycle of Sidecar's
// passive Document, Issue, Diff, and Resource panes.
//
// A Deck deliberately does not render or persist itself. Hosts provide the
// available box when opening a leaf, render the returned viewer models, and
// choose where the reference-only State is stored. All potentially expensive
// work is returned as tea.Cmd; New, Decode, PlanOpen, and Encode are pure.
package contentpanes

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/contentlink"
	"github.com/marcus/sidecar/internal/panelayout"
)

// SurfaceContext is the stable identity and resolution context for one deck.
// BaseRef is used only by working-tree Diff viewers.
type SurfaceContext struct {
	Root    string
	Surface string
	BaseRef string
	Epoch   uint64
}

// Placement is the geometry against which a new split is trialled. Boxes are
// the last boxes by leaf ID and let panelayout choose the largest content leaf.
type Placement struct {
	Box    panelayout.Box
	Boxes  map[int]panelayout.Box
	Floors panelayout.Floors
}

// Status is the typed result of Open.
type Status uint8

const (
	StatusRefused Status = iota
	StatusOpened
	StatusFocused
	// StatusAction means the reference is valid but is not a passive leaf.
	// URL and Internal refs are returned to the host for activation.
	StatusAction
)

// Refusal is why Open made no change.
type Refusal string

const (
	RefusalNone        Refusal = ""
	RefusalInvalid     Refusal = "invalid-reference"
	RefusalUnsupported Refusal = "unsupported-kind"
	RefusalPlacement   Refusal = "no-placement"
	RefusalFit         Refusal = "does-not-fit"
)

// Outcome describes exactly what Open did. Command, when non-nil, performs
// the viewer load and returns a Result for Apply.
type Outcome struct {
	Status      Status
	Refusal     Refusal
	Ref         contentlink.Ref
	Kind        panelayout.Kind
	LeafID      int
	TabID       uint64
	CreatedLeaf bool
	CreatedTab  bool
	Command     tea.Cmd
}

// Accepted reports whether the deck accepted the target or delegated a valid
// action to the host.
func (o Outcome) Accepted() bool { return o.Status != StatusRefused }

// TabInfo is one tab as a host needs to render it. Viewer is intentionally the
// existing shared model rather than a second rendering interface.
type TabInfo struct {
	ID     uint64
	Ref    contentlink.Ref
	Viewer any
}

// AsyncID pins a result to the exact deck tab request that produced it.
// Tab IDs are never reused; Generation changes whenever that tab starts work.
type AsyncID struct {
	TabID      uint64
	Generation uint64
	Epoch      uint64
	Surface    string
}

// Result is the only asynchronous message a Deck consumes. Payload remains a
// viewer-owned type such as docview.LoadedMsg or resourceview.ResolvedMsg.
type Result struct {
	ID      AsyncID
	Payload any
}

type tab struct {
	id         uint64
	identity   string
	ref        contentlink.Ref
	generation uint64
	view       viewer
	ctx        SurfaceContext
}

type pane struct {
	kind   panelayout.Kind
	leafID int
	tabs   []*tab
	active int
}

// Deck is one primary surface plus at most one homogeneous leaf for each
// passive content kind.
type Deck struct {
	ctx SurfaceContext
	cfg Config

	root  *panelayout.Node
	focus int

	panes  map[int]*pane
	hidden map[panelayout.Kind]*pane

	nextTabID uint64
}

// New constructs a primary-only deck without doing I/O.
func New(ctx SurfaceContext, cfg Config) *Deck {
	return &Deck{
		ctx:    ctx,
		cfg:    cfg,
		root:   &panelayout.Node{ID: 1, Kind: panelayout.Primary},
		focus:  1,
		panes:  make(map[int]*pane),
		hidden: make(map[panelayout.Kind]*pane),
	}
}

// Context returns the current resolution identity.
func (d *Deck) Context() SurfaceContext {
	if d == nil {
		return SurfaceContext{}
	}
	return d.ctx
}

// SetContext changes the deck's async scope without starting work. Results
// from the old epoch or surface are subsequently stale.
func (d *Deck) SetContext(ctx SurfaceContext) {
	if d != nil {
		d.ctx = ctx
	}
}

// Tree returns a detached layout tree so hosts cannot mutate deck ownership.
func (d *Deck) Tree() *panelayout.Node {
	if d == nil {
		return nil
	}
	return panelayout.Clone(d.root)
}

// FocusedLeaf returns the leaf that owns outer keyboard focus.
func (d *Deck) FocusedLeaf() int {
	if d == nil {
		return 0
	}
	return d.focus
}

// FocusLeaf moves focus to a visible leaf.
func (d *Deck) FocusLeaf(id int) bool {
	if d == nil {
		return false
	}
	n := panelayout.Find(d.root, id)
	if n == nil || n.Split != nil {
		return false
	}
	d.focus = id
	return true
}

// CycleFocus walks visible leaves in layout order and wraps.
func (d *Deck) CycleFocus(delta int) int {
	if d == nil || d.root == nil {
		return 0
	}
	ids := leafIDs(d.root)
	if len(ids) == 0 {
		return 0
	}
	i := 0
	for j, id := range ids {
		if id == d.focus {
			i = j
			break
		}
	}
	i = (i + delta) % len(ids)
	if i < 0 {
		i += len(ids)
	}
	d.focus = ids[i]
	return d.focus
}

// PlanOpen exposes the shared placement answer without mutating the deck.
func (d *Deck) PlanOpen(ref contentlink.Ref, boxes map[int]panelayout.Box) (panelayout.OpenPlan, bool) {
	if d == nil {
		return panelayout.OpenPlan{}, false
	}
	_, kind, _, ok := normalizeRef(d.ctx, ref)
	if !ok || kind == panelayout.Primary {
		return panelayout.OpenPlan{}, false
	}
	return panelayout.PlanOpen(d.root, kind, boxes)
}

// Open opens or focuses one homogeneous passive tab. A split is first applied
// to a clone and laid out against placement; refusal leaves tree, focus,
// hidden state, and tabs unchanged.
func (d *Deck) Open(ctx SurfaceContext, ref contentlink.Ref, placement Placement) Outcome {
	if d == nil {
		return Outcome{Status: StatusRefused, Refusal: RefusalPlacement, Ref: ref}
	}
	if !contentlink.Activatable(ref.Kind) {
		return Outcome{Status: StatusRefused, Refusal: RefusalUnsupported, Ref: ref}
	}
	normalized, kind, identity, ok := normalizeRef(ctx, ref)
	if !ok {
		return Outcome{Status: StatusRefused, Refusal: RefusalInvalid, Ref: ref}
	}
	if kind == panelayout.Primary {
		return Outcome{Status: StatusAction, Ref: normalized, Kind: kind}
	}

	plan, ok := panelayout.PlanOpen(d.root, kind, placement.Boxes)
	if !ok {
		return Outcome{Status: StatusRefused, Refusal: RefusalPlacement, Ref: normalized, Kind: kind}
	}

	if plan.Retarget != 0 {
		p := d.panes[plan.Retarget]
		if p == nil || p.kind != kind {
			return Outcome{Status: StatusRefused, Refusal: RefusalPlacement, Ref: normalized, Kind: kind}
		}
		d.ctx = ctx
		return d.openTab(p, normalized, identity, false)
	}

	newLeaf := &panelayout.Node{Kind: kind}
	trial := panelayout.Clone(d.root)
	trial, leafID := panelayout.SplitLeaf(trial, plan.Split, plan.Axis, newLeaf)
	if leafID <= 0 {
		return Outcome{Status: StatusRefused, Refusal: RefusalPlacement, Ref: normalized, Kind: kind}
	}
	if _, _, fits := panelayout.LayoutPanes(trial, placement.Box, placement.Floors); !fits {
		return Outcome{Status: StatusRefused, Refusal: RefusalFit, Ref: normalized, Kind: kind}
	}

	var p *pane
	if remembered := d.hidden[kind]; remembered != nil {
		p = remembered
	} else {
		p = &pane{kind: kind}
	}
	d.root, leafID = panelayout.SplitLeaf(d.root, plan.Split, plan.Axis, &panelayout.Node{Kind: kind})
	if leafID <= 0 {
		return Outcome{Status: StatusRefused, Refusal: RefusalPlacement, Ref: normalized, Kind: kind}
	}
	delete(d.hidden, kind)
	p.leafID = leafID
	d.panes[leafID] = p
	d.focus = leafID
	d.ctx = ctx
	out := d.openTab(p, normalized, identity, true)
	out.CreatedLeaf = true
	return out
}

func (d *Deck) openTab(p *pane, ref contentlink.Ref, identity string, freshLeaf bool) Outcome {
	for i, t := range p.tabs {
		if t.identity != identity {
			continue
		}
		p.active = i
		d.focus = p.leafID
		t.ref = ref
		var cmd tea.Cmd
		if !sameContext(t.ctx, d.ctx) {
			state := t.view.snapshot(t.ref)
			t.view.arm(d.ctx, ref, int(t.id), state)
			cmd = t.view.load(d.ctx, ref, int(t.id))
			t.ctx = d.ctx
		} else {
			cmd = t.view.focus(d.ctx, ref, int(t.id))
		}
		return Outcome{
			Status: StatusFocused, Ref: ref, Kind: p.kind, LeafID: p.leafID,
			TabID: t.id, CreatedLeaf: freshLeaf, Command: d.start(t, cmd),
		}
	}
	d.nextTabID++
	t := &tab{id: d.nextTabID, identity: identity, ref: ref, view: newViewer(d.cfg, p.kind), ctx: d.ctx}
	p.tabs = append(p.tabs, t)
	p.active = len(p.tabs) - 1
	d.focus = p.leafID
	cmd := t.view.load(d.ctx, ref, int(t.id))
	return Outcome{
		Status: StatusOpened, Ref: ref, Kind: p.kind, LeafID: p.leafID,
		TabID: t.id, CreatedLeaf: freshLeaf, CreatedTab: true, Command: d.start(t, cmd),
	}
}

func sameContext(a, b SurfaceContext) bool {
	return a.Root == b.Root && a.Surface == b.Surface && a.BaseRef == b.BaseRef && a.Epoch == b.Epoch
}

func (d *Deck) start(t *tab, cmd tea.Cmd) tea.Cmd {
	if t == nil || cmd == nil {
		return nil
	}
	t.generation++
	id := AsyncID{TabID: t.id, Generation: t.generation, Epoch: d.ctx.Epoch, Surface: d.ctx.Surface}
	return func() tea.Msg { return Result{ID: id, Payload: cmd()} }
}

// Apply routes one load result to the exact live or hidden tab that requested
// it. It returns an optional, identity-wrapped follow-up command.
func (d *Deck) Apply(result Result) (tea.Cmd, bool) {
	if d == nil || result.ID.Epoch != d.ctx.Epoch || result.ID.Surface != d.ctx.Surface {
		return nil, false
	}
	t := d.tabByID(result.ID.TabID)
	if t == nil || t.generation != result.ID.Generation {
		return nil, false
	}
	cmd, ok := t.view.apply(d.ctx, result.Payload)
	if !ok {
		return nil, false
	}
	ref, identity := t.view.reference(t.ref)
	t.ref, t.identity = ref, identity
	d.deduplicate(t)
	return d.start(t, cmd), true
}

// Viewer returns the active host-independent viewer model for a passive leaf.
// Its dynamic type is *docview.Model, *issueview.Model, *workspacediff.View,
// or *resourceview.Model.
func (d *Deck) Viewer(leafID int) any {
	if d == nil {
		return nil
	}
	p := d.panes[leafID]
	if p == nil || p.active < 0 || p.active >= len(p.tabs) {
		return nil
	}
	return p.tabs[p.active].view.model()
}

// Tabs returns the strip in display order and its active index. The returned
// slice is detached; viewer models remain the shared host-independent models.
func (d *Deck) Tabs(leafID int) ([]TabInfo, int) {
	if d == nil {
		return nil, 0
	}
	p := d.panes[leafID]
	if p == nil {
		return nil, 0
	}
	out := make([]TabInfo, 0, len(p.tabs))
	for _, t := range p.tabs {
		out = append(out, TabInfo{ID: t.id, Ref: t.ref, Viewer: t.view.model()})
	}
	return out, p.active
}

// SelectTab selects a tab and starts a deferred restore load when needed.
func (d *Deck) SelectTab(leafID, index int) tea.Cmd {
	p := d.panes[leafID]
	if p == nil || index < 0 || index >= len(p.tabs) {
		return nil
	}
	p.active = index
	d.focus = leafID
	t := p.tabs[index]
	return d.start(t, t.view.focus(d.ctx, t.ref, int(t.id)))
}

// CycleTab moves the focused homogeneous tab strip and wraps.
func (d *Deck) CycleTab(delta int) tea.Cmd {
	p := d.panes[d.focus]
	if p == nil || len(p.tabs) < 2 {
		return nil
	}
	i := (p.active + delta) % len(p.tabs)
	if i < 0 {
		i += len(p.tabs)
	}
	return d.SelectTab(p.leafID, i)
}

// HideFocused collapses the focused passive leaf but remembers its tabs. A
// later Open of the same kind reuses that pane.
func (d *Deck) HideFocused() bool {
	if d == nil {
		return false
	}
	p := d.panes[d.focus]
	if p == nil {
		return false
	}
	leafID := p.leafID
	delete(d.panes, leafID)
	d.hidden[p.kind] = p
	d.root, d.focus = panelayout.Close(d.root, leafID)
	p.leafID = 0
	return true
}

// CloseActive closes the active tab. Closing the last tab forgets the pane and
// collapses its leaf; unlike HideFocused, no hidden snapshot survives.
func (d *Deck) CloseActive() bool {
	if d == nil {
		return false
	}
	p := d.panes[d.focus]
	if p == nil || p.active < 0 || p.active >= len(p.tabs) {
		return false
	}
	p.tabs = append(p.tabs[:p.active], p.tabs[p.active+1:]...)
	if len(p.tabs) > 0 {
		if p.active >= len(p.tabs) {
			p.active = len(p.tabs) - 1
		}
		return true
	}
	leafID := p.leafID
	delete(d.panes, leafID)
	delete(d.hidden, p.kind)
	d.root, d.focus = panelayout.Close(d.root, leafID)
	return true
}

// CloseTab closes a specific tab through the same last-tab collapse rule.
func (d *Deck) CloseTab(leafID, index int) bool {
	if d == nil {
		return false
	}
	p := d.panes[leafID]
	if p == nil || index < 0 || index >= len(p.tabs) {
		return false
	}
	d.focus = leafID
	p.active = index
	return d.CloseActive()
}

// Hidden reports whether kind has a remembered, currently collapsed pane.
func (d *Deck) Hidden(kind panelayout.Kind) bool {
	return d != nil && d.hidden[kind] != nil
}

func (d *Deck) tabByID(id uint64) *tab {
	for _, p := range d.panes {
		for _, t := range p.tabs {
			if t.id == id {
				return t
			}
		}
	}
	for _, p := range d.hidden {
		for _, t := range p.tabs {
			if t.id == id {
				return t
			}
		}
	}
	return nil
}

func (d *Deck) deduplicate(survivor *tab) {
	if survivor == nil || survivor.identity == "" {
		return
	}
	for _, p := range d.panesAndHidden() {
		contains := false
		for _, candidate := range p.tabs {
			contains = contains || candidate == survivor
		}
		if !contains {
			continue
		}
		for i := len(p.tabs) - 1; i >= 0; i-- {
			if p.tabs[i] == survivor || p.tabs[i].identity != survivor.identity {
				continue
			}
			p.tabs = append(p.tabs[:i], p.tabs[i+1:]...)
			if p.active > i {
				p.active--
			} else if p.active >= len(p.tabs) {
				p.active = max(len(p.tabs)-1, 0)
			}
		}
		for i, candidate := range p.tabs {
			if candidate == survivor {
				p.active = i
			}
		}
		return
	}
}

func (d *Deck) panesAndHidden() []*pane {
	out := make([]*pane, 0, len(d.panes)+len(d.hidden))
	for _, p := range d.panes {
		out = append(out, p)
	}
	for _, p := range d.hidden {
		out = append(out, p)
	}
	return out
}

func leafIDs(root *panelayout.Node) []int {
	if root == nil {
		return nil
	}
	if root.Split == nil {
		return []int{root.ID}
	}
	return append(leafIDs(root.Split.A), leafIDs(root.Split.B)...)
}

func kindName(kind panelayout.Kind) string {
	switch kind {
	case panelayout.Primary:
		return "primary"
	case panelayout.Document:
		return "document"
	case panelayout.Issue:
		return "issue"
	case panelayout.Diff:
		return "diff"
	case panelayout.Resource:
		return "resource"
	default:
		return fmt.Sprintf("unknown-%d", kind)
	}
}
