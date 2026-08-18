package resourceview

import (
	tea "charm.land/bubbletea/v2"

	"github.com/marcus/sidecar/internal/markdown"
	"github.com/marcus/sidecar/internal/resource"
)

// MaxTabs bounds one Resource leaf. Clicking keys forever must cost bounded
// memory and a bounded tab strip; past the bound the oldest unfocused tab is
// dropped rather than refusing the user's click.
const MaxTabs = 16

// Tabs is the tabbed set of resource references in one Resource leaf.
//
// Tab identity before a resolve is {instance, matcher, locator}. After a
// resolve supplies a provider-stable identity the tab is re-keyed, and if that
// collides with an already-open tab the two merge rather than leaving the user
// with two tabs for one ticket.
type Tabs struct {
	renderer *markdown.Renderer
	resolve  Resolver

	models []*Model
	active int

	// nextModelID hands each model a distinct identity so a late answer can
	// be matched to the tab that asked, even after tabs are closed and the
	// slice indices shift.
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
func (t *Tabs) Len() int { return len(t.models) }

// Empty reports whether the leaf has nothing left in it.
func (t *Tabs) Empty() bool { return len(t.models) == 0 }

// Active returns the focused tab, or nil when there are none.
func (t *Tabs) Active() *Model {
	if t.active < 0 || t.active >= len(t.models) {
		return nil
	}
	return t.models[t.active]
}

// ActiveIndex is the focused tab's position, which the host persists.
func (t *Tabs) ActiveIndex() int { return t.active }

// At returns the tab at index, or nil.
func (t *Tabs) At(i int) *Model {
	if i < 0 || i >= len(t.models) {
		return nil
	}
	return t.models[i]
}

// All returns the tabs in strip order.
func (t *Tabs) All() []*Model { return t.models }

// Labels is the tab strip's text, in order.
func (t *Tabs) Labels() []string {
	out := make([]string, 0, len(t.models))
	for _, m := range t.models {
		out = append(out, m.TabLabel())
	}
	return out
}

// SetEpoch records the host's scoping value for subsequent loads.
func (t *Tabs) SetEpoch(epoch uint64) { t.epoch = epoch }

// SetSize sizes every tab, because a tab selected later must already know the
// box it will be drawn into.
func (t *Tabs) SetSize(width, height int) {
	t.width, t.height = width, height
	for _, m := range t.models {
		m.SetSize(width, height)
	}
}

// Open focuses an existing tab for ref, or appends a new one and resolves it.
// It is the single entry point for a terminal click, a restored tab being
// selected, and a `sidecar open --provider` request, so all three produce the
// same tab behavior.
func (t *Tabs) Open(ref resource.Reference) tea.Cmd {
	if i, ok := t.indexOf(ref); ok {
		t.active = i
		// A tab that was armed rather than resolved starts now.
		return t.models[i].Resolve()
	}
	t.evictIfFull()
	m := New(t.renderer, t.resolve)
	m.SetSize(t.width, t.height)
	t.nextModelID++
	t.models = append(t.models, m)
	t.active = len(t.models) - 1
	return m.Load(t.nextModelID, ref, t.epoch)
}

// Arm appends a tab without resolving it. Restore uses this so relaunch does
// not fan out one process per remembered tab; the active tab is resolved by
// the host calling ResolveActive.
func (t *Tabs) Arm(ref resource.Reference, scroll int) *Model {
	if i, ok := t.indexOf(ref); ok {
		return t.models[i]
	}
	t.evictIfFull()
	m := New(t.renderer, t.resolve)
	m.SetSize(t.width, t.height)
	t.nextModelID++
	m.Arm(t.nextModelID, ref, t.epoch)
	m.SetPendingScroll(scroll)
	t.models = append(t.models, m)
	return m
}

// SetActive focuses a tab by index and resolves it if it is still armed.
func (t *Tabs) SetActive(i int) tea.Cmd {
	if i < 0 || i >= len(t.models) {
		return nil
	}
	t.active = i
	return t.models[i].Resolve()
}

// Next and Prev cycle the strip, wrapping. Selecting an armed tab resolves it.
func (t *Tabs) Next() tea.Cmd {
	if len(t.models) == 0 {
		return nil
	}
	return t.SetActive((t.active + 1) % len(t.models))
}

func (t *Tabs) Prev() tea.Cmd {
	if len(t.models) == 0 {
		return nil
	}
	return t.SetActive((t.active - 1 + len(t.models)) % len(t.models))
}

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

// Close removes the tab at index and reports whether the leaf is now empty.
// Closing is the user's explicit act and is the only thing besides a confirmed
// cleanup that may drop a reference.
func (t *Tabs) Close(i int) (empty bool) {
	if i < 0 || i >= len(t.models) {
		return len(t.models) == 0
	}
	t.models = append(t.models[:i], t.models[i+1:]...)
	if t.active > i || t.active >= len(t.models) {
		t.active--
	}
	if t.active < 0 {
		t.active = 0
	}
	return len(t.models) == 0
}

// CloseActive removes the focused tab.
func (t *Tabs) CloseActive() (empty bool) { return t.Close(t.active) }

// Apply routes a resolve result to the tab that asked for it, applies any
// canonical re-key, and merges a tab that has just become a duplicate.
//
// It returns false when no open tab owns the message, which is the correct
// outcome for a result arriving after its tab was closed.
func (t *Tabs) Apply(msg ResolvedMsg) bool {
	for i, m := range t.models {
		if !m.Accepts(msg) {
			continue
		}
		if !m.Apply(msg) {
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
// another tab already holds that identity, the two are merged: the resolved
// one wins and the duplicate is dropped, so clicking a key and then its
// canonical form does not leave two tabs for one resource.
func (t *Tabs) rekeyAndMerge(i int, identity string) {
	if identity == "" || i < 0 || i >= len(t.models) {
		return
	}
	m := t.models[i]
	before := m.Reference()
	m.Rekey(identity)
	after := m.Reference()
	if after == before {
		return
	}
	for j := len(t.models) - 1; j >= 0; j-- {
		if j == i || t.models[j].Reference() != after {
			continue
		}
		// Drop the duplicate. The resolved tab is the one with content, so
		// it is the one that survives.
		t.models = append(t.models[:j], t.models[j+1:]...)
		if j < i {
			i--
		}
		if t.active == j {
			t.active = i
		} else if t.active > j {
			t.active--
		}
	}
}

// indexOf finds an open tab for ref.
func (t *Tabs) indexOf(ref resource.Reference) (int, bool) {
	for i, m := range t.models {
		if m.Reference() == ref {
			return i, true
		}
	}
	return -1, false
}

// evictIfFull drops the oldest tab that is not focused once the bound is hit.
func (t *Tabs) evictIfFull() {
	for len(t.models) >= MaxTabs {
		victim := 0
		if victim == t.active && len(t.models) > 1 {
			victim = 1
		}
		t.Close(victim)
	}
}

// References is the reference-only projection the project surface persists:
// no title, field, body, error, URL, or auth state, by construction rather
// than by remembering to strip them.
func (t *Tabs) References() []PersistedTab {
	out := make([]PersistedTab, 0, len(t.models))
	for _, m := range t.models {
		ref := m.Reference()
		out = append(out, PersistedTab{
			Provider: ref.Instance,
			Matcher:  ref.Matcher,
			Locator:  ref.Locator,
			Scroll:   m.Scroll(),
		})
	}
	return out
}

// PersistedTab is one reference plus its scroll. It mirrors the state package's
// JSON shape without this package depending on state, so the view layer stays
// free of persistence concerns.
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
