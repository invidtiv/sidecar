package overview

import (
	tea "charm.land/bubbletea/v2"

	"github.com/marcus/sidecar/internal/contentlink"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/resource"
	"github.com/marcus/sidecar/internal/resourceview"
	"github.com/marcus/sidecar/internal/terminallink"
	"github.com/marcus/sidecar/internal/termpreview"
	"github.com/marcus/sidecar/internal/ui"
)

const (
	previewResourceRegionKind = "global-preview-resource"
	previewResourceTabKind    = "global-preview-resource-tab"
)

func isPreviewResourceRegion(kind string) bool {
	return kind == previewResourceRegionKind || kind == previewResourceTabKind
}

// previewResourceTabHit is the tab stored on the resource header region.
type previewResourceTabHit struct {
	Index int
	Close bool
}

// previewResource is the memory-only Resource pane beside the selected
// terminal. Everything a Resource leaf DOES — the click journey, the
// documented keys, tab open/focus/close — belongs to resourceview.Pane, which
// the project workspace binds as well. What is left here is only what this
// surface owns: which workspace row the pane belongs to, whether it holds the
// keyboard, and the epoch that scopes its answers.
type previewResource struct {
	pane    *resourceview.Pane
	tabs    *resourceview.Tabs
	surface string
	focused bool
	epoch   uint64
}

func (r *previewResource) view() *resourceview.Model {
	if r == nil || r.tabs == nil {
		return nil
	}
	return r.tabs.Active()
}

// previewResourceHost is this surface's answer to resourceview.Host. It is
// deliberately four short methods: anything longer here is behavior that
// should have lived on the shared Pane instead.
//
// It carries the previewResource it was built for so a late callback from a
// pane the user has already left cannot act on whichever pane is current — the
// same guard newPreviewIssueModel's OpenHandler makes.
type previewResourceHost struct {
	m   *Model
	res *previewResource
}

var _ resourceview.Host = previewResourceHost{}

func (h previewResourceHost) current() bool {
	return h.m != nil && h.res != nil && h.m.preview.resource == h.res
}

func (h previewResourceHost) FocusLeaf() {
	if !h.current() {
		return
	}
	h.m.focusPreviewPane(panelayout.Resource)
}

// EnterFromTerminal is smaller here than on the project surface, and the
// difference is real rather than an omission. The project workspace owns the
// live tmux pane, so its ritual has to capture the exact viewport before the
// new leaf resizes tmux. This browser only watches a pane it does not own and
// re-captures it on the next sync, so there is no viewport of its own to
// freeze. What it does owe is what every other preview link activation here
// owes: drop the selection anchored to the buffer that is about to reflow, and
// stop typing into a pane that is losing the keyboard.
//
// The interactive exit goes through the pane-command queue because Host
// returns no command and a render/setter path has no runtime to dispatch one
// with; Update drains the queue on the next pass.
func (h previewResourceHost) EnterFromTerminal() {
	if !h.current() {
		return
	}
	h.m.clearPreviewSelection()
	if h.m.PreviewInteractive() {
		h.m.queuePreviewCmd(h.m.exitPreviewInteractive())
	}
}

// Persist is a deliberate no-op. The global browser keeps preview-pane state
// in memory only — exactly as its document, issue, and diff panes do — so a
// resource tab survives a row switch through paneCache and nothing else. The
// project workspace is the surface that writes references to disk; a global
// surface that also persisted them would be the parity bug, not this.
func (h previewResourceHost) Persist() {}

// OpenURL reuses the one confirmed browser path this surface already has, so a
// provider-supplied URL is opened by exactly the rule that opens a URL printed
// in terminal output.
func (h previewResourceHost) OpenURL(url string) tea.Cmd {
	return terminallink.OpenHTTP(url)
}

// previewResourceResolvedMsg adds workspace identity to resourceview's own
// request identity. Routing resolves the surface first, then the model ID and
// generation the view itself checks.
type previewResourceResolvedMsg struct {
	resourceview.ResolvedMsg
	WorkspaceID string
}

// The app injects provider state through this interface. Asserting it here
// makes a signature drift a build error rather than a pane that silently never
// appears.
var _ resourceview.Surface = (*Model)(nil)

// SetResourceResolver supplies the provider-backed resolver. Until the app
// wires one, opening a tab degrades to a typed error card rather than a
// spinner that never ends.
func (m *Model) SetResourceResolver(resolve resourceview.Resolver) {
	m.resolveResource = resolve
	if m.preview.deck != nil {
		ctx := m.preview.deck.Context()
		m.preview.deck.SetResourceResolver(m.previewResourceResolver(ctx.Surface, ctx.Epoch))
	}
	if res := m.preview.resource; res != nil {
		res.tabs.SetResolver(m.previewResourceResolver(res.surface, res.epoch))
	}
	for workspaceID, cached := range m.preview.paneCache {
		if cached.deck != nil {
			ctx := cached.deck.Context()
			cached.deck.SetResourceResolver(m.previewResourceResolver(ctx.Surface, ctx.Epoch))
		}
		if cached.resource == nil || cached.resource == m.preview.resource {
			continue
		}
		cached.resource.tabs.SetResolver(m.previewResourceResolver(workspaceID, cached.resource.epoch))
	}
}

// SetResourceMatchers replaces the compiled external matchers the scanner may
// run, in precedence order. Empty — the default — means no provider is ready,
// which must read as ordinary unlinked text.
func (m *Model) SetResourceMatchers(matchers []terminallink.ResourceMatcher) {
	m.resourceMatchers = matchers
}

// activatePreviewResource is the terminal-click entry point: a span the
// scanner produced becomes a reference, and the shared Pane owns everything
// after that.
func (m *Model) activatePreviewResource(ref resourceview.Ref) tea.Cmd {
	return m.openPreviewResourceRef(ref, true)
}

// OpenPreviewResource opens or focuses a resource tab without the terminal
// ritual. It is the `sidecar open --provider` path: nothing was clicked in
// terminal output, so there is no selection to clear.
func (m *Model) OpenPreviewResource(ref resourceview.Ref) tea.Cmd {
	return m.openPreviewResourceRef(ref, false)
}

func (m *Model) openPreviewResourceRef(ref resourceview.Ref, fromTerminal bool) tea.Cmd {
	_, ok := m.SelectedWorkspace()
	if !ok || !ref.Valid() {
		return nil
	}
	if fromTerminal {
		m.clearPreviewSelection()
	}
	return m.openPreviewContent(contentlink.Ref{Kind: contentlink.KindResource, Provider: ref.Instance, Matcher: ref.Matcher, Value: ref.Locator}, "Resource")
}

// previewResourceResolver wraps the host-supplied resolver so every answer
// carries the surface and epoch it was asked from. resourceview's Resolver
// signature does not carry either — the view checks only model ID and
// generation — so the host stamps them on the way back.
func (m *Model) previewResourceResolver(workspaceID string, epoch uint64) resourceview.Resolver {
	// The model's own epoch is ignored: this surface scopes by the epoch and
	// workspace row captured when the resolver was built, which is what a row
	// switch invalidates.
	return func(modelID int, generation, _ uint64, ref resourceview.Ref, refresh bool) tea.Cmd {
		resolve := m.resolveResource
		if resolve == nil {
			return func() tea.Msg {
				return previewResourceResolvedMsg{
					ResolvedMsg: resourceview.ResolvedMsg{
						ModelID: modelID, Generation: generation, Epoch: epoch,
						Ref: ref, Refresh: refresh,
						Err: resource.Errorf(resource.CodeUnavailable,
							"no resource provider is configured for %s", ref.Instance),
					},
					WorkspaceID: workspaceID,
				}
			}
		}
		cmd := resolve(modelID, generation, epoch, ref, refresh)
		if cmd == nil {
			return nil
		}
		return func() tea.Msg {
			msg := cmd()
			resolved, ok := msg.(resourceview.ResolvedMsg)
			if !ok {
				return msg
			}
			resolved.Epoch = epoch
			return previewResourceResolvedMsg{ResolvedMsg: resolved, WorkspaceID: workspaceID}
		}
	}
}

// applyPreviewResourceResolved lands a provider answer, or discards it.
//
// The surface check is the half of stale-result routing resourceview cannot
// make: a result for a row the user has left is dropped rather than applied to
// the stashed pane behind their back. The epoch check is the same rule one row
// deeper, for a pane closed and reopened on the same row.
func (m *Model) applyPreviewResourceResolved(msg previewResourceResolvedMsg) {
	res := m.preview.resource
	if res == nil || msg.WorkspaceID == "" || msg.WorkspaceID != m.preview.workspaceID {
		return
	}
	if res.surface != msg.WorkspaceID || res.epoch != msg.Epoch {
		return
	}
	res.pane.Apply(msg.ResolvedMsg)
}

func (m *Model) closePreviewResource() tea.Cmd {
	if m.preview.deck == nil {
		return nil
	}
	m.preview.deck.FocusLeaf(m.preview.deck.Leaf(panelayout.Resource))
	for m.preview.deck.Leaf(panelayout.Resource) != 0 {
		m.preview.deck.CloseActive()
	}
	if ctx, ok := m.previewDeckContext(); ok {
		m.syncPreviewDeckProjection(ctx)
	}
	if m.preview.doc != nil {
		m.focusPreviewPane(panelayout.Document)
		return m.syncTerminalGeometry()
	}
	if m.preview.issue != nil {
		m.focusPreviewPane(panelayout.Issue)
		return m.syncTerminalGeometry()
	}
	if m.preview.diff != nil {
		m.focusPreviewPane(panelayout.Diff)
		return m.syncTerminalGeometry()
	}
	return tea.Batch(m.focusList(), m.syncTerminalGeometry())
}

func (m *Model) closePreviewResourceTab() tea.Cmd {
	if m.preview.resource == nil || m.preview.resource.tabs == nil {
		return nil
	}
	return m.closePreviewResourceTabAt(m.preview.resource.tabs.ActiveIndex())
}

func (m *Model) closePreviewResourceTabAt(index int) tea.Cmd {
	if m.preview.deck == nil {
		return nil
	}
	m.preview.deck.CloseTab(m.preview.deck.Leaf(panelayout.Resource), index)
	return m.finishPreviewDeckClose()
}

func (m *Model) clickPreviewResourceTab(index int) tea.Cmd {
	if m.preview.deck == nil {
		return nil
	}
	// SelectTab owns focus and resolves an armed tab even when it is already
	// selected. Skipping it for the active index stranded re-armed tabs after
	// a workspace-row switch.
	cmd := m.preview.deck.SelectTab(m.preview.deck.Leaf(panelayout.Resource), index)
	if ctx, ok := m.previewDeckContext(); ok {
		m.syncPreviewDeckProjection(ctx)
	}
	return cmd
}

func (m *Model) renderPreviewResource(res *previewResource, box termpreview.Box) string {
	contentHeight := max(box.H-termpreview.HeaderRows, 0)
	focused := m.PreviewFocused() && res.focused
	res.tabs.SetSize(box.W, contentHeight)
	header := m.composePreviewHeader(
		resourceview.LayoutTabStrip(res.tabs, ui.ReserveHeaderClose(box.W).TabsWidth, focused).HoverClose(m.tabCloseHoverIn(panelayout.Resource)).Row,
		box.W, panelayout.Resource)
	if contentHeight <= 0 {
		return header
	}
	return header + "\n" + res.tabs.View()
}

// registerPreviewResourceRegion covers the Resource leaf's INNER box.
func (m *Model) registerPreviewResourceRegion(box termpreview.Box) {
	if m.preview.resource == nil {
		return
	}
	m.workspacesMouse.HitMap.AddRect(
		previewResourceRegionKind,
		box.X, box.Y, box.W, box.H,
		previewResourceRegionKind,
	)
}

func (m *Model) registerPreviewResourceTabRegions(box termpreview.Box) {
	res := m.preview.resource
	if res == nil {
		return
	}
	focused := m.PreviewFocused() && res.focused
	// The strip is laid out by the same call that drew it, so a click cannot
	// land on a tab that overflow pushed out of the header.
	strip := resourceview.LayoutTabStrip(res.tabs, ui.ReserveHeaderClose(box.W).TabsWidth, focused)
	strip.RegisterHits(func(col, width, index int, close bool) {
		m.workspacesMouse.HitMap.AddRect(
			previewResourceTabKind,
			box.X+col, box.Y, width, 1,
			previewResourceTabHit{Index: index, Close: close},
		)
	})
}

func (m *Model) handlePreviewResourceMouse(action mouse.MouseAction) tea.Cmd {
	res := m.preview.resource
	if res == nil {
		return nil
	}
	if tab, ok := action.Region.Data.(previewResourceTabHit); ok {
		if action.Type == mouse.ActionClick || action.Type == mouse.ActionDoubleClick {
			if tab.Close {
				return m.closePreviewResourceTabAt(tab.Index)
			}
			return m.clickPreviewResourceTab(tab.Index)
		}
		switch action.Type {
		case mouse.ActionScrollUp, mouse.ActionScrollDown:
			res.pane.Scroll(action.Delta)
		}
		return nil
	}
	kind, _ := regionKind(action.Region)
	if kind != previewResourceRegionKind {
		return nil
	}
	switch action.Type {
	case mouse.ActionClick, mouse.ActionDoubleClick:
		// The card is passive: a provider document has no clickable targets,
		// so a press inside it only moves focus.
		m.focusPreviewPane(panelayout.Resource)
	case mouse.ActionScrollUp, mouse.ActionScrollDown:
		res.pane.Scroll(action.Delta)
	}
	return nil
}

// resourceScrollAtBoundary reports whether a wheel notch would move the card.
// The predicate belongs to resourceview so both surfaces answer a wheel over a
// Resource pane identically; this wrapper only handles the missing view.
func resourceScrollAtBoundary(view *resourceview.Model, delta int) bool {
	if view == nil {
		return true
	}
	return view.ScrollAtBoundary(delta)
}

func (m *Model) previewResourceKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	res := m.preview.resource
	if res == nil || !res.focused || m.PreviewInteractive() {
		return false, nil
	}
	// The shared Pane answers the documented Resource keys first, so this
	// surface cannot quietly rebind one of them. It deliberately does not
	// claim q, esc, or x — those follow this surface's own content-pane rule.
	if handled, cmd := res.pane.HandleKey(msg.String()); handled {
		return true, cmd
	}
	switch msg.String() {
	case "q", "esc":
		return true, m.closePreviewResource()
	case "x":
		return true, m.closePreviewResourceTab()
	}
	// Unclaimed keys fall through to WorkspacesKey, which lets host globals
	// through and swallows the rest so they cannot drive the list.
	return false, nil
}
