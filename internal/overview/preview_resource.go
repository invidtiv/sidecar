package overview

import (
	tea "charm.land/bubbletea/v2"

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
type previewResourceTabHit int

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

// SetResourceResolver supplies the provider-backed resolver. Until the app
// wires one, opening a tab degrades to a typed error card rather than a
// spinner that never ends.
func (m *Model) SetResourceResolver(resolve resourceview.Resolver) { m.resolveResource = resolve }

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
	workspace, ok := m.SelectedWorkspace()
	if !ok || !ref.Valid() {
		return nil
	}
	leafID, refusal := m.ensurePreviewPane(panelayout.Resource, "Resource")
	if refusal != nil {
		return refusal
	}
	if leafID == 0 {
		return nil
	}
	res := m.ensurePreviewResource(workspace.ID)
	var open tea.Cmd
	if fromTerminal {
		open = res.pane.ActivateFromTerminal(ref)
	} else {
		open = res.pane.Activate(ref)
	}
	return tea.Batch(open, m.syncTerminalGeometry())
}

// ensurePreviewResource returns the pane bound to this workspace row, building
// one if the row has none. A row change builds a fresh pane rather than
// retargeting the old one, so the epoch that scopes answers changes with it.
func (m *Model) ensurePreviewResource(workspaceID string) *previewResource {
	if res := m.preview.resource; res != nil && res.surface == workspaceID {
		return res
	}
	res := &previewResource{surface: workspaceID, epoch: m.nextPreviewContentEpoch()}
	res.tabs = resourceview.NewTabs(nil, m.previewResourceResolver(workspaceID, res.epoch))
	res.tabs.SetEpoch(res.epoch)
	res.pane = resourceview.NewPane(res.tabs, previewResourceHost{m: m, res: res})
	m.preview.resource = res
	return res
}

// previewResourceResolver wraps the host-supplied resolver so every answer
// carries the surface and epoch it was asked from. resourceview's Resolver
// signature does not carry either — the view checks only model ID and
// generation — so the host stamps them on the way back.
func (m *Model) previewResourceResolver(workspaceID string, epoch uint64) resourceview.Resolver {
	return func(modelID int, generation uint64, ref resourceview.Ref, refresh bool) tea.Cmd {
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
		cmd := resolve(modelID, generation, ref, refresh)
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
	if m.preview.resource == nil {
		return nil
	}
	m.forgetPreviewResource(m.preview.workspaceID)
	if leaf := panelayout.FirstOfKind(m.preview.paneRoot, panelayout.Resource); leaf != nil {
		m.preview.paneRoot, m.preview.paneFocus = panelayout.Close(m.preview.paneRoot, leaf.ID)
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

// forgetPreviewResource drops the in-memory tab set for workspaceID. Global
// resource tabs are not written to disk; q/esc and last-x must not leave a
// cache entry that a later row switch would restore.
func (m *Model) forgetPreviewResource(workspaceID string) {
	m.preview.resource = nil
	if cached, ok := m.preview.paneCache[workspaceID]; ok {
		cached.resource = nil
		m.preview.paneCache[workspaceID] = cached
	}
}

func (m *Model) closePreviewResourceTab() tea.Cmd {
	res := m.preview.resource
	if res == nil {
		return nil
	}
	if res.tabs.Len() <= 1 {
		return m.closePreviewResource()
	}
	empty, cmd := res.pane.CloseActiveTab()
	if empty {
		return tea.Batch(cmd, m.closePreviewResource())
	}
	return cmd
}

func (m *Model) clickPreviewResourceTab(index int) tea.Cmd {
	res := m.preview.resource
	if res == nil {
		return nil
	}
	if index == res.tabs.ActiveIndex() {
		m.focusPreviewPane(panelayout.Resource)
		return nil
	}
	// SelectTab focuses the leaf itself and resolves a tab that was only
	// armed, which is why the click does not do either of those here.
	return res.pane.SelectTab(index)
}

func (m *Model) renderPreviewResource(res *previewResource, box termpreview.Box) string {
	contentHeight := max(box.H-termpreview.HeaderRows, 0)
	focused := m.PreviewFocused() && res.focused
	res.tabs.SetSize(box.W, contentHeight)
	header := m.composePreviewHeader(
		resourceview.LayoutTabStrip(res.tabs, ui.ReserveHeaderClose(box.W).TabsWidth, focused).Row,
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
	for _, tab := range resourceview.LayoutTabStrip(res.tabs, ui.ReserveHeaderClose(box.W).TabsWidth, focused).Tabs {
		m.workspacesMouse.HitMap.AddRect(
			previewResourceTabKind,
			box.X+tab.Col, box.Y, tab.Width, 1,
			previewResourceTabHit(tab.Index),
		)
	}
}

func (m *Model) handlePreviewResourceMouse(action mouse.MouseAction) tea.Cmd {
	res := m.preview.resource
	if res == nil {
		return nil
	}
	if tab, ok := action.Region.Data.(previewResourceTabHit); ok {
		if action.Type == mouse.ActionClick || action.Type == mouse.ActionDoubleClick {
			return m.clickPreviewResourceTab(int(tab))
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

// resourceScrollAtBoundary reports whether a wheel notch would move the card,
// without leaving it moved. resourceview has no boundary predicate of its own,
// and probing is exact where guessing is not: the bottom edge depends on the
// rendered height, which the host does not know.
func resourceScrollAtBoundary(view *resourceview.Model, delta int) bool {
	if view == nil {
		return true
	}
	before := view.Scroll()
	moved := view.ScrollBy(delta)
	view.ScrollTo(before)
	return !moved
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
	// A focused Resource leaf is its own input context. Do not let an unowned
	// key navigate or type into the terminal behind the visible card.
	return true, nil
}
