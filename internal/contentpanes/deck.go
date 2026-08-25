// Package contentpanes owns the host-independent lifecycle of Sidecar's
// passive Document, Issue, Note, Diff, and Resource panes.
//
// A Deck deliberately does not render or persist itself. Hosts provide the
// available box when opening a leaf, render the returned viewer models, and
// choose where the reference-only State is stored. All potentially expensive
// work is returned as tea.Cmd; New, Decode, PlanOpen, and Encode are pure.
package contentpanes

import (
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/contentlink"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/resourceview"
)

// SurfaceContext is the stable identity and resolution context for one deck.
// BaseRef is used only by working-tree Diff viewers.
type SurfaceContext struct {
	Root        string
	DiffRoot    string
	Surface     string
	DiffSurface string
	BaseRef     string
	Epoch       uint64
}

// Placement is the geometry against which a new split is trialled. Boxes are
// the last boxes by leaf ID and let panelayout choose the largest content leaf.
type Placement struct {
	Box    panelayout.Box
	Boxes  map[int]panelayout.Box
	Floors panelayout.Floors
	Split  string
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

// SetContext changes the deck's async scope. Tabs are rebound to the new
// identity so in-flight results from the old context cannot land, and the
// active tab of every visible pane is asked to load. Hidden panes stay armed
// until they are shown. Loading visible tabs here is what keeps a sibling
// document from sitting on "Loading document…" after a split.
func (d *Deck) SetContext(ctx SurfaceContext) []tea.Cmd {
	if d == nil || sameContext(d.ctx, ctx) {
		return nil
	}
	d.ctx = ctx
	for _, p := range d.panesAndHidden() {
		for _, t := range p.tabs {
			d.rebindTab(t, p.kind, ctx)
		}
	}
	return d.LoadVisible()
}

// LoadVisible starts a load for the active tab of every visible pane that
// still needs one. Decode and SetContext arm viewers without I/O; hosts must
// return these commands or a visible pane sits on its placeholder until
// something else happens to select it.
func (d *Deck) LoadVisible() []tea.Cmd { return d.loadVisible(nil) }

// LoadVisibleKind is LoadVisible narrowed to one kind, for the moments when
// only one viewer's dependency has changed — a resource resolver arriving after
// startup — and re-focusing every other visible pane would be work the change
// did not ask for.
func (d *Deck) LoadVisibleKind(kind panelayout.Kind) []tea.Cmd {
	return d.loadVisible(&kind)
}

func (d *Deck) loadVisible(only *panelayout.Kind) []tea.Cmd {
	if d == nil {
		return nil
	}
	var cmds []tea.Cmd
	for _, p := range d.panes {
		if p == nil || p.active < 0 || p.active >= len(p.tabs) {
			continue
		}
		if only != nil && p.kind != *only {
			continue
		}
		t := p.tabs[p.active]
		if cmd := d.start(t, t.view.focus(d.ctx, t.ref, int(t.id))); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return cmds
}

// ReloadFocused force-reloads the focused pane's active tab, including a tab
// already showing a document. It is the user-facing recovery for a pane stuck
// on its loading placeholder.
func (d *Deck) ReloadFocused() tea.Cmd {
	if d == nil {
		return nil
	}
	p := d.panes[d.focus]
	if p == nil || p.active < 0 || p.active >= len(p.tabs) {
		return nil
	}
	t := p.tabs[p.active]
	return d.start(t, t.view.reload(d.ctx, t.ref, int(t.id)))
}

func (d *Deck) rebindTab(t *tab, kind panelayout.Kind, ctx SurfaceContext) {
	if d == nil || t == nil {
		return
	}
	state := t.view.snapshot(t.ref)
	d.nextTabID++
	t.id = d.nextTabID
	t.view = newViewer(d.cfg, kind)
	t.view.arm(ctx, t.ref, int(t.id), state)
	t.ctx = ctx
	// A fresh tab/model identity rejects old raw viewer broadcasts as well as
	// Deck Results, even when surface and epoch are reused.
	t.generation = 0
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

func (d *Deck) Leaf(kind panelayout.Kind) int {
	if d == nil {
		return 0
	}
	if leaf := panelayout.FirstOfKind(d.root, kind); leaf != nil {
		return leaf.ID
	}
	return 0
}

func (d *Deck) SetRatio(splitID, ratio int) bool {
	if d == nil {
		return false
	}
	n := panelayout.Find(d.root, splitID)
	return n != nil && n.Split != nil && panelayout.SetRatio(d.root, splitID, ratio)
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
	plan = panelayout.ApplyAxisOverride(plan, placement.Split)

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
	trial, leafID := panelayout.ApplyPlan(trial, plan, newLeaf)
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
	d.root, leafID = panelayout.ApplyPlan(d.root, plan, &panelayout.Node{Kind: kind})
	if leafID <= 0 {
		return Outcome{Status: StatusRefused, Refusal: RefusalPlacement, Ref: normalized, Kind: kind}
	}
	if leaf := panelayout.Find(d.root, leafID); leaf != nil {
		leaf.ContentID = leafID
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

// OpenDocumentFile opens a validated, already-open file through the ordinary
// Deck lifecycle. The returned command owns file when accepted.
func (d *Deck) OpenDocumentFile(ctx SurfaceContext, ref contentlink.Ref, placement Placement, file *os.File) Outcome {
	out := d.Open(ctx, ref, placement)
	if !out.Accepted() || out.Kind != panelayout.Document || file == nil {
		return out
	}
	if out.Command == nil {
		_ = file.Close()
		return out
	}
	t := d.tabByID(out.TabID)
	if t == nil {
		_ = file.Close()
		return out
	}
	viewer, ok := t.view.(*documentViewer)
	if !ok {
		_ = file.Close()
		return out
	}
	out.Command = d.start(t, viewer.loadFile(ctx, ref, int(t.id), file))
	return out
}

// ReplaceActive retargets the active tab without changing the leaf geometry.
// Hosts use this for picker-style navigation where Enter replaces the current
// document while Shift+Enter remains the ordinary append operation.
func (d *Deck) ReplaceActive(ctx SurfaceContext, ref contentlink.Ref) Outcome {
	if d == nil {
		return Outcome{Status: StatusRefused, Refusal: RefusalPlacement, Ref: ref}
	}
	normalized, kind, identity, ok := normalizeRef(ctx, ref)
	if !ok || kind == panelayout.Primary {
		return Outcome{Status: StatusRefused, Refusal: RefusalInvalid, Ref: ref}
	}
	adopt := d.SetContext(ctx)
	leafID := d.Leaf(kind)
	p := d.panes[leafID]
	if p == nil || p.active < 0 || p.active >= len(p.tabs) {
		return Outcome{Status: StatusRefused, Refusal: RefusalPlacement, Ref: normalized, Kind: kind}
	}
	for i, t := range p.tabs {
		if t.identity == identity {
			p.active, d.focus = i, leafID
			out := d.openTab(p, normalized, identity, false)
			out.Command = batchCmds(append(adopt, out.Command)...)
			return out
		}
	}
	d.nextTabID++
	t := &tab{id: d.nextTabID, identity: identity, ref: normalized, view: newViewer(d.cfg, kind), ctx: ctx}
	p.tabs[p.active] = t
	d.focus = leafID
	return Outcome{Status: StatusOpened, Ref: normalized, Kind: kind, LeafID: leafID,
		TabID: t.id, CreatedTab: true, Command: batchCmds(append(adopt, d.start(t, t.view.load(ctx, normalized, int(t.id))))...)}
}

func batchCmds(cmds ...tea.Cmd) tea.Cmd {
	var out []tea.Cmd
	for _, cmd := range cmds {
		if cmd != nil {
			out = append(out, cmd)
		}
	}
	switch len(out) {
	case 0:
		return nil
	case 1:
		return out[0]
	default:
		return tea.Batch(out...)
	}
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
			d.rebindTab(t, p.kind, d.ctx)
			t.ref = ref
			cmd = t.view.load(d.ctx, ref, int(t.id))
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
	return a.Root == b.Root && a.DiffRoot == b.DiffRoot && a.Surface == b.Surface && a.DiffSurface == b.DiffSurface && a.BaseRef == b.BaseRef && a.Epoch == b.Epoch
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

// ApplyBroadcast routes an existing viewer-owned broadcast message through
// every tab. Viewer identities decide whether it applies.
func (d *Deck) ApplyBroadcast(payload any) tea.Cmd {
	if d == nil || payload == nil {
		return nil
	}
	var cmds []tea.Cmd
	for _, p := range d.panesAndHidden() {
		for _, t := range append([]*tab(nil), p.tabs...) {
			cmd, ok := t.view.apply(d.ctx, payload)
			if !ok {
				continue
			}
			ref, identity := t.view.reference(t.ref)
			t.ref, t.identity = ref, identity
			d.deduplicate(t)
			if cmd != nil {
				// Viewer follow-ups already carry their own model/epoch/surface
				// identity and must remain broadcast messages for sibling hosts.
				cmds = append(cmds, cmd)
			}
		}
	}
	return tea.Batch(cmds...)
}

func (d *Deck) ConfigureViewers(configure func(panelayout.Kind, any)) {
	if d == nil || configure == nil {
		return
	}
	for _, p := range d.panesAndHidden() {
		for _, t := range p.tabs {
			configure(p.kind, t.view.model())
		}
	}
}

// SetResourceResolver updates existing viewers and the factory used by future
// Resource tabs created after provider discovery completes.
//
// Rebinding starts nothing. A deck that is on screen follows this with
// LoadVisibleKind(panelayout.Resource) and returns the commands, which is what
// finally resolves tabs armed before any provider was ready; a deck the host is
// only holding does not, because a load whose command is dropped would leave
// the tab loading forever.
func (d *Deck) SetResourceResolver(resolve resourceview.Resolver) {
	if d == nil {
		return
	}
	d.cfg.ResourceResolver = resolve
	d.ConfigureViewers(func(_ panelayout.Kind, model any) {
		if view, ok := model.(*resourceview.Model); ok {
			view.SetResolver(resolve)
		}
	})
}

// Viewer returns the active host-independent viewer model for a passive leaf.
// Its dynamic type is *docview.Model, *issueview.Model, *noteview.Model,
// *workspacediff.View, or *resourceview.Model.
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

// ForgetLeaf forgets a pane outright, dropping all its tabs and collapsing its leaf.
// Neither the live leaf nor any hidden snapshot survives.
func (d *Deck) ForgetLeaf(leafID int) bool {
	if d == nil {
		return false
	}
	p := d.panes[leafID]
	if p != nil {
		delete(d.panes, leafID)
		delete(d.hidden, p.kind)
		p.leafID = 0
		p.tabs = nil
	}
	if panelayout.Find(d.root, leafID) != nil {
		d.root, d.focus = panelayout.Close(d.root, leafID)
	}
	return p != nil
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
		var activeTab *tab
		if p.active >= 0 && p.active < len(p.tabs) {
			activeTab = p.tabs[p.active]
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
			if candidate == activeTab {
				p.active = i
				return
			}
		}
		for i, candidate := range p.tabs {
			if candidate == survivor {
				p.active = i
				break
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
	case panelayout.Note:
		return "note"
	case panelayout.Diff:
		return "diff"
	case panelayout.Resource:
		return "resource"
	default:
		return fmt.Sprintf("unknown-%d", kind)
	}
}
