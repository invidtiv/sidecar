package resourceview

import (
	tea "charm.land/bubbletea/v2"

	"github.com/marcus/sidecar/internal/markdown"
	"github.com/marcus/sidecar/internal/resource"
	"github.com/marcus/sidecar/internal/tabs"
)

// MaxTabs bounds one Resource leaf. Clicking keys forever must cost bounded
// memory and a bounded tab strip; past the bound the oldest unfocused tab is
// dropped rather than refusing the user's click.
const MaxTabs = 16

// TabKey is the stable identity of one tab before a resolve: the provider
// instance, the matcher, and the exact matched locator. The same locator from
// two providers is two resources, so the instance is part of the key.
func TabKey(ref resource.Reference) string {
	return ref.Instance + "\x00" + ref.Matcher + "\x00" + ref.Locator
}

// Tabs is the tabbed set of resource references in one Resource leaf.
//
// Ordering, cycling, close semantics and the overflow window come from
// tabs.Group, the same generic the file and issue strips use. What this type
// adds is only what is specific to resources: keys built from references,
// arming without resolving, routing an answer to the tab that asked, and the
// canonical re-key that merges two tabs naming one resource.
type Tabs struct {
	tabs.Group[*Model]

	renderer *markdown.Renderer
	resolve  Resolver

	// nextModelID hands each model a distinct identity so a late answer can be
	// matched to the tab that asked even after tabs close and indices shift.
	nextModelID int

	width, height int
	epoch         uint64
}

// NewTabs creates an empty tab set.
func NewTabs(renderer *markdown.Renderer, resolve Resolver) *Tabs {
	if renderer == nil {
		renderer, _ = markdown.NewRenderer()
	}
	return &Tabs{renderer: renderer, resolve: resolve}
}

// Len reports how many tabs are open.
func (t *Tabs) Len() int { return len(t.Items) }

// Empty reports whether the leaf has nothing left in it.
func (t *Tabs) Empty() bool { return len(t.Items) == 0 }

// Active returns the focused tab, or nil.
func (t *Tabs) Active() *Model {
	item, ok := t.ActiveItem()
	if !ok {
		return nil
	}
	return item.Value
}

// ActiveIndex is the focused tab's position, which the host persists.
func (t *Tabs) ActiveIndex() int { return t.Group.Active }

// At returns the tab at index, or nil.
func (t *Tabs) At(i int) *Model {
	if i < 0 || i >= len(t.Items) {
		return nil
	}
	return t.Items[i].Value
}

// All returns the models in strip order.
func (t *Tabs) All() []*Model {
	out := make([]*Model, 0, len(t.Items))
	for _, item := range t.Items {
		out = append(out, item.Value)
	}
	return out
}

// Labels is the tab strip's text, in order.
func (t *Tabs) Labels() []string {
	out := make([]string, 0, len(t.Items))
	for _, item := range t.Items {
		out = append(out, item.Value.TabLabel())
	}
	return out
}

// SetEpoch records the host's scoping value for subsequent loads.
func (t *Tabs) SetEpoch(epoch uint64) { t.epoch = epoch }

// SetSize sizes every tab, because a tab selected later must already know the
// box it will be drawn into.
func (t *Tabs) SetSize(width, height int) {
	t.width, t.height = width, height
	for _, item := range t.Items {
		item.Value.SetSize(width, height)
	}
}

// Open focuses an existing tab for ref, or appends a new one and resolves it.
// It is the single entry point for a terminal click, a restored tab being
// selected, and `sidecar open --provider`, so all three behave identically.
func (t *Tabs) Open(ref resource.Reference) tea.Cmd {
	if i := t.Find(TabKey(ref)); i >= 0 {
		t.Select(i)
		// A tab that was armed rather than resolved starts now.
		return t.Items[i].Value.Resolve()
	}
	t.evictIfFull()
	m := t.newModel()
	t.Append(TabKey(ref), m)
	return m.Load(t.nextModelID, ref, t.epoch)
}

// Arm appends a tab without resolving it. Restore uses this so relaunch does
// not fan out one process per remembered tab.
func (t *Tabs) Arm(ref resource.Reference, scroll int) *Model {
	if i := t.Find(TabKey(ref)); i >= 0 {
		return t.Items[i].Value
	}
	t.evictIfFull()
	m := t.newModel()
	m.Arm(t.nextModelID, ref, t.epoch)
	m.SetPendingScroll(scroll)
	t.Append(TabKey(ref), m)
	return m
}

func (t *Tabs) newModel() *Model {
	m := New(t.renderer, t.resolve)
	m.SetSize(t.width, t.height)
	t.nextModelID++
	return m
}

// SetActive focuses a tab by index and resolves it if it is still armed.
// Selecting is what turns a restored reference into a request, which is the
// behavior both hosts need and neither should re-derive.
func (t *Tabs) SetActive(i int) tea.Cmd {
	if i < 0 || i >= len(t.Items) {
		return nil
	}
	t.Select(i)
	return t.Items[i].Value.Resolve()
}

// Cycle moves by delta and resolves the tab that lands, wrapping.
func (t *Tabs) Cycle(delta int) tea.Cmd {
	if len(t.Items) < 2 {
		return nil
	}
	t.Group.Cycle(delta)
	return t.ResolveActive()
}

// Next and Prev are the documented { and } bindings.
func (t *Tabs) Next() tea.Cmd { return t.Cycle(1) }
func (t *Tabs) Prev() tea.Cmd { return t.Cycle(-1) }

// ResolveActive starts the active tab's resolve if it is still armed.
func (t *Tabs) ResolveActive() tea.Cmd {
	if m := t.Active(); m != nil {
		return m.Resolve()
	}
	return nil
}

// RefreshActive re-resolves the active tab, bypassing freshness.
func (t *Tabs) RefreshActive() tea.Cmd {
	if m := t.Active(); m != nil {
		return m.Refresh()
	}
	return nil
}

// ReArmPending returns every tab waiting on an answer the host has decided to
// discard back to a resolvable state, and reports how many changed. A host
// calls this when it drops results wholesale — a workspace row switch, a
// project change — so no tab is left on a loading card forever.
func (t *Tabs) ReArmPending() int {
	n := 0
	for _, item := range t.Items {
		if item.Value.ReArm() {
			n++
		}
	}
	return n
}

// Close removes the tab at index and reports whether the leaf is now empty.
// Closing is the user's explicit act, and with a confirmed cleanup it is the
// only thing that may drop a reference.
func (t *Tabs) Close(i int) (empty bool) {
	return t.CloseAt(i).Empty
}

// CloseActive removes the focused tab.
func (t *Tabs) CloseActive() (empty bool) {
	return t.Group.CloseActive().Empty
}

// Apply routes a resolve result to the tab that asked for it, applies any
// canonical re-key, and merges a tab that has just become a duplicate.
//
// It returns false when no open tab owns the message, which is the correct
// outcome for a result arriving after its tab was closed.
func (t *Tabs) Apply(msg ResolvedMsg) bool {
	for i, item := range t.Items {
		if !item.Value.Accepts(msg) {
			continue
		}
		if !item.Value.Apply(msg) {
			return false
		}
		if msg.Err == nil {
			t.rekeyAndMerge(i, msg.Document.Identity)
		}
		return true
	}
	return false
}

// rekeyAndMerge adopts the provider's canonical identity for the tab at i. If
// another tab already holds that identity the two are merged: the resolved one
// wins, so clicking a key and then its canonical form leaves one tab.
func (t *Tabs) rekeyAndMerge(i int, identity string) {
	if identity == "" || i < 0 || i >= len(t.Items) {
		return
	}
	m := t.Items[i].Value
	before := m.Reference()
	m.Rekey(identity)
	after := m.Reference()
	if after == before {
		return
	}
	key := TabKey(after)
	t.Items[i].Key = key
	// Drop any other tab that now names the same resource. The resolved tab
	// is the one with content, so it is the one that survives.
	survivor := t.Items[i].Value
	t.CloseMatching(func(item tabs.Item[*Model]) bool {
		return item.Key == key && item.Value != survivor
	})
	if j := t.Find(key); j >= 0 {
		t.Select(j)
	}
}

// evictIfFull drops the oldest tab that is not focused once the bound is hit.
func (t *Tabs) evictIfFull() {
	for len(t.Items) >= MaxTabs {
		victim := 0
		if victim == t.Group.Active && len(t.Items) > 1 {
			victim = 1
		}
		t.CloseAt(victim)
	}
}

// References is the reference-only projection the project surface persists:
// no title, field, body, error, URL, or auth state, by construction rather
// than by remembering to strip them.
func (t *Tabs) References() []PersistedTab {
	out := make([]PersistedTab, 0, len(t.Items))
	for _, item := range t.Items {
		ref := item.Value.Reference()
		out = append(out, PersistedTab{
			Provider: ref.Instance,
			Matcher:  ref.Matcher,
			Locator:  ref.Locator,
			Scroll:   item.Value.Scroll(),
		})
	}
	return out
}

// PersistedTab is one reference plus its scroll. It mirrors the state
// package's JSON shape without this package depending on state, so the view
// layer stays free of persistence concerns.
type PersistedTab struct {
	Provider string
	Matcher  string
	Locator  string
	Scroll   int
}

// View renders the active tab.
func (t *Tabs) View() string {
	if m := t.Active(); m != nil {
		return m.View()
	}
	return ""
}
