package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/contentlink"
	"github.com/marcus/sidecar/internal/contentpanes"
	"github.com/marcus/sidecar/internal/docview"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/issueview"
	"github.com/marcus/sidecar/internal/keymap"
	"github.com/marcus/sidecar/internal/livepanes"
	"github.com/marcus/sidecar/internal/livewatch"
	"github.com/marcus/sidecar/internal/markdown"
	"github.com/marcus/sidecar/internal/mouse"
	appmsg "github.com/marcus/sidecar/internal/msg"
	"github.com/marcus/sidecar/internal/noteview"
	"github.com/marcus/sidecar/internal/paneframe"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/projectdir"
	"github.com/marcus/sidecar/internal/resourceview"
	"github.com/marcus/sidecar/internal/state"
	"github.com/marcus/sidecar/internal/tabs"
	"github.com/marcus/sidecar/internal/terminallink"
	"github.com/marcus/sidecar/internal/ui"
	"github.com/marcus/sidecar/internal/uirequest"
	"github.com/marcus/sidecar/internal/workspacediff"
)

const (
	appDeckLeafRegion    = "app-content-leaf"
	appDeckDividerRegion = "app-content-divider"
	appDeckTabRegion     = "app-content-tab"
	appDeckCloseRegion   = "app-content-close"
)

type appContentResolvedMsg struct {
	Key       string
	SurfaceID string
	Candidate contentlink.Pending
	Ref       contentlink.Ref
	Found     bool
}

type appContentLinkHit struct {
	Generation uint64
	Ref        contentlink.Ref
	Rect       mouse.Rect
}

type appContentDeck struct {
	key, workdir, stateRoot, pluginID string
	global                            bool
	deck                              *contentpanes.Deck
	plugin                            plugin.Plugin
	layout                            panelayout.Layout
	laidOut                           bool
	canvas                            paneframe.Box
	mouse                             *mouse.Handler
	queued                            []tea.Cmd
	primaryInner                      paneframe.Box
	links                             []appContentLinkHit
	tabHits                           []appDeckTabHit
	generation                        uint64
	press                             *appContentLinkHit
	pressX, pressY                    int
	dragged                           bool
	resolution                        *contentlink.ResolutionIndex
	pending                           map[contentlink.Pending]bool
	resourceMatchers                  []contentlink.ResourceMatcher
	dragSplit                         int
	live                              *livepanes.Set
	suppressRefresh                   bool
	edit                              *appDeckDocumentEdit
}

func appDeckKey(workdir, pluginID string) string { return workdir + "\x00" + pluginID }

const globalTasksDeckRoot = "@global-tasks"

func (m *Model) contentDeckEligible(p plugin.Plugin) bool {
	if p == nil || p.ID() == workspacePluginID || !features.IsEnabled(features.PluginContentPanes.Name) {
		return false
	}
	_, links := p.(plugin.ContentLinkProvider)
	_, focus := p.(plugin.PaneFocusProvider)
	return links && focus
}

func (m *Model) activeContentDeck() *appContentDeck {
	if m.configOpen() {
		return nil
	}
	p, stateRoot, global := m.contentDeckSurface()
	if !m.contentDeckEligible(p) {
		return nil
	}
	key := appDeckKey(stateRoot, p.ID())
	h := m.contentDecks[key]
	ctx := contentpanes.SurfaceContext{Root: m.ui.WorkDir, DiffRoot: m.ui.WorkDir, Surface: p.ID(), Epoch: m.registry.Context().Epoch}
	if h == nil {
		cfg := contentpanes.Config{ConfigureViewer: configureAppDeckViewer}
		if manager := ResourceProviderManager(); manager != nil {
			cfg.ResourceResolver = resourceResolver(manager)
		}
		var saved contentpanes.State
		if raw := state.GetContentDeck(stateRoot, p.ID()); len(raw) > 0 {
			_ = json.Unmarshal(raw, &saved)
		}
		h = &appContentDeck{key: key, workdir: m.ui.WorkDir, stateRoot: stateRoot, pluginID: p.ID(), plugin: p, global: global,
			mouse: mouse.NewHandler(), resolution: contentlink.NewResolutionIndex(contentlink.MaxPendingResolutions),
			pending: make(map[contentlink.Pending]bool)}
		h.live = h.newLiveSet()
		if manager := ResourceProviderManager(); manager != nil {
			h.resourceMatchers = manager.Snapshot().TerminalMatchers()
		}
		if saved.Root != nil {
			h.deck = contentpanes.Decode(ctx, cfg, saved)
			h.queued = append(h.queued, h.deck.LoadVisible()...)
		} else {
			h.deck = contentpanes.New(ctx, cfg)
		}
		m.contentDecks[key] = h
	} else {
		h.plugin = p
		h.workdir = m.ui.WorkDir
		h.queued = append(h.queued, h.deck.SetContext(ctx)...)
	}
	h.syncInnerFocus()
	return h
}

func (m Model) currentContentDeck() *appContentDeck {
	if m.configOpen() || m.registry == nil {
		return nil
	}
	p, stateRoot, _ := m.contentDeckSurface()
	if !m.contentDeckEligible(p) {
		return nil
	}
	return m.contentDecks[appDeckKey(stateRoot, p.ID())]
}

func (m Model) contentDeckSurface() (plugin.Plugin, string, bool) {
	if m.globalTasksFocused() {
		return m.globalTasksPlugin(), globalTasksDeckRoot, true
	}
	if m.inGlobalScope() {
		return nil, "", false
	}
	return m.ActivePlugin(), m.ui.WorkDir, false
}

func appDeckFloors() panelayout.Floors {
	return paneframe.ChromeFloorsFor(panelayout.Floors{
		// Files itself contains a tree and preview split. Below this width its
		// inner minimums wrap despite receiving the correct leaf size, so preserve
		// the useful primary surface and refuse another outer split.
		Primary:  panelayout.Floor{Width: 80, Height: 10},
		Doc:      panelayout.Floor{Width: markdown.MinWidthForMarkdown, Height: 8},
		Issue:    panelayout.Floor{Width: markdown.MinWidthForMarkdown, Height: 8},
		Note:     panelayout.Floor{Width: markdown.MinWidthForMarkdown, Height: 8},
		Diff:     panelayout.Floor{Width: markdown.MinWidthForMarkdown, Height: 8},
		Resource: panelayout.Floor{Width: markdown.MinWidthForMarkdown, Height: 8},
	}, appDeckChromeForKind)
}

func appDeckChromeForKind(kind panelayout.Kind) paneframe.Chrome {
	if kind == panelayout.Primary {
		return paneframe.ChromeNone
	}
	return paneframe.ChromeIdle
}

func (m *Model) renderContentDeck(h *appContentDeck, width, height int) string {
	if h == nil || width <= 0 || height <= 0 {
		return ""
	}
	h.plugin = m.focusedSurface()
	h.suppressRefresh = m.hasModal() || m.configOpen() || (m.inGlobalScope() && !h.global)
	for _, other := range m.contentDecks {
		if other != h && other.laidOut {
			other.releaseAppContentDocumentEdit()
			other.laidOut = false
			if other.live != nil {
				other.queued = append(other.queued, other.live.Reconcile())
			}
		}
	}
	h.generation++
	h.links = nil
	h.tabHits = nil
	h.mouse.Clear()
	h.canvas = paneframe.Box{W: width, H: height}
	layout, ok := panelayout.LayoutTree(h.deck.Tree(), h.canvas, appDeckFloors(), h.deck.FocusedLeaf())
	h.layout, h.laidOut = layout, ok
	if !ok {
		return ui.FitBlock(h.plugin.View(width, height), width, height)
	}
	view := paneframe.Compose(appDeckHost{h}, layout, h.canvas, width, height)
	m.adoptAppContentPlugin(h)
	paneframe.RegisterRegions(appDeckRegions{h}, appDeckHost{h}, layout)
	if h.live != nil {
		h.queued = append(h.queued, h.live.Reconcile())
	}
	return view
}

func (h *appContentDeck) visibleDocument() *docview.Model {
	if h == nil || !h.laidOut || h.deck == nil {
		return nil
	}
	leafID := h.deck.Leaf(panelayout.Document)
	for _, placement := range h.layout.Leaves {
		if placement.Node != nil && placement.Node.ID == leafID {
			view, _ := h.deck.Viewer(leafID).(*docview.Model)
			return view
		}
	}
	return nil
}

func (h *appContentDeck) newLiveSet() *livepanes.Set {
	return livepanes.NewSet("app-content:"+h.key, func() uint64 {
		if h.deck == nil {
			return 0
		}
		return h.deck.Context().Epoch
	}, livepanes.Binding{
		Kind:   "docs",
		Config: livewatch.Config{},
		Targets: func() []livewatch.Target {
			view := h.visibleDocument()
			if view == nil {
				return nil
			}
			view.SetRoot(h.workdir)
			if target := view.WatchTarget(); target.Path != "" {
				return []livewatch.Target{target}
			}
			return nil
		},
		Refresh: func() []tea.Cmd {
			view := h.visibleDocument()
			if view == nil {
				return nil
			}
			view.Observe()
			if cmd := view.Refresh(h.suppressRefresh); cmd != nil {
				return []tea.Cmd{cmd}
			}
			return nil
		},
		Owed: func() bool {
			view := h.visibleDocument()
			return view != nil && view.RefreshPending()
		},
	})
}

type appDeckHost struct{ h *appContentDeck }

func (x appDeckHost) Content(n *panelayout.Node) paneframe.Content {
	return &appDeckContent{h: x.h, node: n}
}
func (x appDeckHost) Focus() int { return x.h.deck.FocusedLeaf() }
func (x appDeckHost) SetFocus(n *panelayout.Node) {
	if n != nil && n.Split == nil {
		x.h.deck.FocusLeaf(n.ID)
		x.h.syncInnerFocus()
	}
}
func (x appDeckHost) Layout() (panelayout.Layout, bool) { return x.h.layout, x.h.laidOut }
func (x appDeckHost) HandleState(splitID int) ui.HandleState {
	return paneframe.HandleStateFor(splitID, x.h.mouse.IsDragging(), x.h.dragSplit, false, 0)
}
func (x appDeckHost) QueueSizeCmd(cmd tea.Cmd) { x.h.queued = append(x.h.queued, cmd) }
func (x appDeckHost) Chrome(n *panelayout.Node) paneframe.Chrome {
	if n != nil && n.Kind == panelayout.Primary {
		return paneframe.ChromeNone
	}
	if n != nil && n.ID == x.h.deck.FocusedLeaf() {
		return paneframe.ChromeActive
	}
	return paneframe.ChromeIdle
}

type appDeckContent struct {
	h    *appContentDeck
	node *panelayout.Node
	size paneframe.Size
}

func (c *appDeckContent) Kind() string { return fmt.Sprint(c.node.Kind) }
func (c *appDeckContent) Title() string {
	if c.node.Kind == panelayout.Primary {
		return c.h.plugin.Name()
	}
	if v := c.h.deck.Viewer(c.node.ID); v != nil {
		switch v := v.(type) {
		case *docview.Model:
			return v.Title()
		case *issueview.Model:
			return v.Title()
		case *noteview.Model:
			return v.Title()
		case *workspacediff.View:
			return v.Target.TabLabel()
		case *resourceview.Model:
			return v.Title()
		}
	}
	return ""
}
func (c *appDeckContent) SetSize(size paneframe.Size) tea.Cmd {
	c.size = size
	if c.node.Kind != panelayout.Primary || c.h.plugin == nil {
		return nil
	}
	updated, cmd := c.h.plugin.Update(tea.WindowSizeMsg{Width: size.Width, Height: size.Height})
	c.h.plugin = updated
	return cmd
}
func (c *appDeckContent) View(render paneframe.Render) string {
	if c.node.Kind == panelayout.Primary {
		c.h.primaryInner = paneframe.Box(render.Origin)
		frame := c.h.plugin.View(c.size.Width, c.size.Height)
		frame = c.h.scanPrimary(frame, render.Origin)
		return ui.FitBlock(frame, c.size.Width, c.size.Height)
	}
	if c.node.Kind == panelayout.Document {
		if frame, ok := c.h.renderAppContentDocumentEdit(c.node.ID, c.size); ok {
			return ui.FitBlock(frame, c.size.Width, c.size.Height)
		}
	}
	bodyH := max(c.size.Height-paneframe.HeaderRows, 0)
	body := ""
	switch v := c.h.deck.Viewer(c.node.ID).(type) {
	case *docview.Model:
		v.SetSize(c.size.Width, bodyH)
		body = v.View()
	case *issueview.Model:
		v.SetSize(c.size.Width, bodyH)
		body = v.View()
	case *noteview.Model:
		v.SetSize(c.size.Width, bodyH)
		body = v.View()
	case *workspacediff.View:
		v.SetSize(c.size.Width, bodyH)
		body = v.Render(c.size.Width, bodyH, workspacediff.RenderOpts{})
	case *resourceview.Model:
		v.SetSize(c.size.Width, bodyH)
		body = v.View()
	}
	return ui.FitBlock(c.h.tabHeader(c.node.ID, c.size.Width, render.Origin, render.Focused)+"\n"+body, c.size.Width, c.size.Height)
}

type appDeckTabHit struct {
	leafID, index int
	close         bool
	rect          mouse.Rect
}

func (h *appContentDeck) tabHeader(leafID, width int, origin mouse.Rect, focused bool) string {
	items, active := h.deck.Tabs(leafID)
	labels := make([]tabs.Label, 0, len(items))
	for _, item := range items {
		label := item.Ref.Value
		if view, ok := item.Viewer.(*noteview.Model); ok && view.Title() != "" {
			label = view.Title()
		}
		if label == "" {
			label = string(item.Ref.Kind)
		}
		labels = append(labels, tabs.Label{Text: label})
	}
	reserve := ui.ReserveHeaderClose(width)
	strip := tabs.LayoutStrip(labels, active, reserve.TabsWidth, focused, nil)
	strip.RegisterHits(func(col, width, index int, close bool) {
		h.tabHits = append(h.tabHits, appDeckTabHit{
			leafID: leafID, index: index, close: close,
			rect: mouse.Rect{X: origin.X + col, Y: origin.Y, W: width, H: 1},
		})
	})
	return ui.ComposeHeaderClose(strip.Row, width, false)
}

func (h *appContentDeck) syncInnerFocus() {
	provider, ok := h.plugin.(plugin.PaneFocusProvider)
	if ok {
		provider.SetPaneFocusActive(h.deck.Leaf(panelayout.Primary) == h.deck.FocusedLeaf())
	}
	for _, placement := range h.layout.Leaves {
		if view, ok := h.deck.Viewer(placement.Node.ID).(*issueview.Model); ok {
			focused := placement.Node.ID == h.deck.FocusedLeaf()
			view.SetActive(focused)
			view.SetFocused(focused)
		}
	}
}

func configureAppDeckViewer(kind panelayout.Kind, model any) {
	if kind != panelayout.Issue {
		return
	}
	view, ok := model.(*issueview.Model)
	if !ok {
		return
	}
	view.OpenHandler = func(issueID string) tea.Cmd {
		return func() tea.Msg {
			return ActivateTargetMsg{Target: uirequest.Target{Kind: uirequest.TargetKindIssue, Value: issueID}}
		}
	}
	view.OpenInTDHandler = OpenIssueInTD
}

type appDeckRegions struct{ h *appContentDeck }

func (r appDeckRegions) Leaf(n *panelayout.Node, b paneframe.Box) {
	r.h.mouse.HitMap.AddRect(appDeckLeafRegion, b.X, b.Y, b.W, b.H, n.ID)
}
func (r appDeckRegions) Divider(id int, b paneframe.Box) {
	r.h.mouse.HitMap.AddRect(appDeckDividerRegion, b.X, b.Y, b.W, b.H, id)
}
func (r appDeckRegions) Tabs(n *panelayout.Node, b paneframe.Box) {
	for _, hit := range r.h.tabHits {
		if n != nil && hit.leafID == n.ID {
			r.h.mouse.HitMap.Add(appDeckTabRegion, hit.rect, hit)
		}
	}
}

// Title is the header name's own target. The deck hosts no leaf that is
// renamed from its pane, so it registers nothing and the tab strip under it
// keeps the cells.
func (r appDeckRegions) Title(*panelayout.Node, paneframe.Box) {}

func (r appDeckRegions) Close(n *panelayout.Node, b paneframe.Box) {
	if n.Kind == panelayout.Primary || b.W <= 0 {
		return
	}
	// The drawn × is the padded three-cell button from ComposeHeaderClose, so
	// the hit rect must be the same reserved geometry. Registering only the
	// last column left the glyph itself dead: clicks had to land one cell to
	// its right to close.
	reserve := ui.ReserveHeaderClose(b.W)
	if reserve.CloseW < 1 {
		return
	}
	r.h.mouse.HitMap.AddRect(appDeckCloseRegion, b.X+reserve.CloseCol, b.Y, reserve.CloseW, 1, n.ID)
}
func (r appDeckRegions) Body(*panelayout.Node, paneframe.Box) {}

func (h *appContentDeck) scanPrimary(frame string, origin mouse.Rect) string {
	provider, ok := h.plugin.(plugin.ContentLinkProvider)
	if !ok {
		return frame
	}
	lines := strings.Split(frame, "\n")
	for _, surface := range provider.ContentLinkSurfaces() {
		if !surface.ReadOnly || surface.Rect.W <= 0 || surface.Rect.H <= 0 {
			continue
		}
		for row := 0; row < surface.Rect.H && surface.Rect.Y+row < len(lines); row++ {
			y := surface.Rect.Y + row
			segment := ansi.Cut(lines[y], surface.Rect.X, surface.Rect.X+surface.Rect.W)
			allowedKinds := surface.Kinds
			if allowedKinds == nil {
				// Surface.KindSet's zero value is allow-none. ScanFrame keeps nil
				// as allow-all for direct compatibility callers, so adapt the two
				// contracts explicitly at this boundary.
				allowedKinds = contentlink.KindSet{}
			}
			result := contentlink.ScanFrame(segment, contentlink.FrameOptions{Ready: h.resolution.Snapshot(), Matchers: h.resourceMatchers,
				InternalNamespaces: sidecarIntentNamespaces, AllowedKinds: allowedKinds, Decorate: true})
			for _, span := range result.Spans {
				h.links = append(h.links, appContentLinkHit{Generation: h.generation, Ref: span.Ref(), Rect: mouse.Rect{
					X: origin.X + surface.Rect.X + span.StartCol, Y: origin.Y + y, W: span.EndCol - span.StartCol + 1, H: 1,
				}})
			}
			for _, candidate := range result.Pending {
				if !h.pending[candidate] {
					h.pending[candidate] = true
					h.queued = append(h.queued, resolveAppContentLink(h.key, surface, candidate))
				}
			}
			prefix := ansi.Cut(lines[y], 0, surface.Rect.X)
			suffix := ansi.Cut(lines[y], surface.Rect.X+surface.Rect.W, ansi.StringWidth(lines[y]))
			lines[y] = prefix + result.Output + suffix
		}
	}
	return strings.Join(lines, "\n")
}

func resolveAppContentLink(key string, surface contentlink.Surface, candidate contentlink.Pending) tea.Cmd {
	return func() tea.Msg {
		msg := appContentResolvedMsg{Key: key, SurfaceID: surface.ID, Candidate: candidate}
		switch candidate.Kind {
		case contentlink.KindFile:
			rel, _, ok := terminallink.ResolveFile(surface.WorkDir, candidate.Raw)
			msg.Ref, msg.Found = contentlink.Ref{Kind: contentlink.KindFile, Value: rel}, ok
		case contentlink.KindDiff:
			target, ok := workspacediff.ParseSpec(candidate.Raw)
			if !ok {
				return msg
			}
			resolved, err := workspacediff.ResolveSpec(context.Background(), surface.WorkDir, target)
			msg.Ref, msg.Found = contentlink.Ref{Kind: contentlink.KindDiff, Value: resolved.Identity()}, err == nil
		}
		return msg
	}
}

func (h *appContentDeck) takeQueued() tea.Cmd {
	cmds := h.queued
	h.queued = nil
	return tea.Batch(cmds...)
}

func (m *Model) openAppContent(workdir, pluginID string, ref contentlink.Ref) tea.Cmd {
	h := m.activeContentDeck()
	if h == nil || h.workdir != workdir || h.pluginID != pluginID {
		return nil
	}
	if ref.Kind == contentlink.KindURL {
		return terminallink.OpenHTTP(ref.Value)
	}
	if ref.Kind == contentlink.KindInternal && h.pluginID == "notes" {
		cmd, err := sidecarIntents.activate(IntentAppContext{ProjectRoot: m.ui.ProjectRoot}, ref)
		if err != nil {
			return nil
		}
		return cmd
	}
	out := m.openAppContentOutcome(h, ref, "")
	if out.Status == contentpanes.StatusRefused {
		if out.Refusal == contentpanes.RefusalFit {
			return appmsg.ShowToast("Content pane needs a wider window; layout left unchanged", 3*time.Second)
		}
		return nil
	}
	return out.Command
}

func (m *Model) openAppContentOutcome(h *appContentDeck, ref contentlink.Ref, split string) contentpanes.Outcome {
	boxes := make(map[int]panelayout.Box)
	for _, leaf := range h.layout.Leaves {
		boxes[leaf.Node.ID] = leaf.Box
	}
	out := h.deck.Open(contentpanes.SurfaceContext{Root: h.workdir, DiffRoot: h.workdir, Surface: h.pluginID, Epoch: m.registry.Context().Epoch}, ref,
		contentpanes.Placement{Box: h.canvas, Boxes: boxes, Floors: appDeckFloors(), Split: split})
	if out.Accepted() {
		h.syncInnerFocus()
		m.persistAppContentDeck(h)
	}
	return out
}

func (m *Model) handleAppContentUIRequest(req uirequest.Request) (tea.Cmd, bool) {
	if req.Action != uirequest.ActionOpen || req.Origin.TmuxSession != "" || !m.appContentRequestMatchesProject(req) {
		return nil, false
	}
	h := m.activeContentDeck()
	if h == nil {
		return nil, false
	}
	var ref contentlink.Ref
	switch req.Target.Kind {
	case uirequest.TargetKindIssue:
		ref = contentlink.Ref{Kind: contentlink.KindIssue, Value: req.Target.Value}
	case uirequest.TargetKindNote:
		ref = contentlink.Ref{Kind: contentlink.KindInternal, Namespace: "note", Value: req.Target.Value}
	case uirequest.TargetKindDiff:
		ref = contentlink.Ref{Kind: contentlink.KindDiff, Value: req.Target.Value}
	case uirequest.TargetKindResource:
		resolved, refusal := resourceview.ReferenceForLocator(h.resourceMatchers, req.Target.Provider, req.Target.Value)
		if refusal != "" {
			m.ackAppContentRequest(req, uirequest.StatusDeclined, refusal, 0)
			return nil, true
		}
		ref = contentlink.Ref{Kind: contentlink.KindResource, Provider: resolved.Instance, Matcher: resolved.Matcher, Value: resolved.Locator}
	default:
		return nil, false
	}
	out := m.openAppContentOutcome(h, ref, req.Options.Split)
	if !out.Accepted() {
		reason := string(out.Refusal)
		if out.Refusal == contentpanes.RefusalFit || out.Refusal == contentpanes.RefusalPlacement {
			reason = "the window is too small to split"
		}
		m.ackAppContentRequest(req, uirequest.StatusDeclined, reason, 0)
		return nil, true
	}
	status := uirequest.StatusOpened
	if out.Status == contentpanes.StatusFocused || !out.CreatedLeaf {
		status = uirequest.StatusRetargeted
	}
	m.ackAppContentRequest(req, status, "", out.LeafID)
	return out.Command, true
}

func (m *Model) appContentRequestMatchesProject(req uirequest.Request) bool {
	if m.ui == nil || req.Origin.ProjectKey == "" {
		return false
	}
	if dir, ok := projectdir.Lookup(m.ui.ProjectRoot); ok {
		return filepath.Base(dir) == req.Origin.ProjectKey
	}
	return sameCanonicalAppPath(m.ui.ProjectRoot, req.Origin.WorkDir)
}

func sameCanonicalAppPath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	canonical := func(path string) string {
		path = filepath.Clean(path)
		if abs, err := filepath.Abs(path); err == nil {
			path = abs
		}
		if resolved, err := filepath.EvalSymlinks(path); err == nil {
			path = filepath.Clean(resolved)
		}
		return path
	}
	return canonical(a) == canonical(b)
}

func (m *Model) ackAppContentRequest(req uirequest.Request, status uirequest.Status, reason string, pane int) {
	surface := ""
	if h := m.currentContentDeck(); h != nil {
		surface = "plugin:" + h.pluginID
	} else if p := m.focusedSurface(); p != nil {
		surface = "plugin:" + p.ID()
	}
	_ = uirequest.WriteAck(config.StateDir(), req.ID, req.Action, uirequest.Ack{
		Instance: uirequest.InstanceID("app-content"), Host: uirequest.HostName(), PID: os.Getpid(),
		Status: status, Reason: reason, Surface: surface, Pane: pane, At: time.Now().UTC(),
	})
}

func (m *Model) adoptAppContentPlugin(h *appContentDeck) {
	if h == nil || h.plugin == nil || m.registry == nil {
		return
	}
	if h.global {
		if m.globalTasks != nil {
			m.globalTasks.plugin = h.plugin
		}
		return
	}
	plugins := m.registry.Plugins()
	for i, candidate := range plugins {
		if candidate.ID() == h.pluginID {
			m.registry.Replace(i, h.plugin)
			return
		}
	}
}

func (m *Model) persistAppContentDeck(h *appContentDeck) {
	if h == nil {
		return
	}
	raw, err := json.Marshal(h.deck.Encode())
	if err == nil {
		_ = state.SetContentDeck(h.stateRoot, h.pluginID, raw)
	}
}

func (m *Model) applyAppContentResult(result contentpanes.Result) tea.Cmd {
	for _, h := range m.contentDecks {
		if cmd, ok := h.deck.Apply(result); ok {
			m.persistAppContentDeck(h)
			return cmd
		}
	}
	return nil
}

func (m *Model) applyAppContentBroadcast(payload any) tea.Cmd {
	var cmds []tea.Cmd
	for _, h := range m.contentDecks {
		cmds = append(cmds, h.deck.ApplyBroadcast(payload))
	}
	return tea.Batch(cmds...)
}

func (m *Model) appContentMouse(msg tea.MouseMsg) (tea.Cmd, bool) {
	h := m.activeContentDeck()
	if h == nil || !h.laidOut {
		return nil, false
	}
	mi := msg.Mouse()
	action := h.mouse.HandleMouse(msg)
	if action.Type == mouse.ActionClick && action.Region != nil && action.Region.ID == appDeckDividerRegion {
		if splitID, ok := action.Region.Data.(int); ok {
			if split := panelayout.Find(h.deck.Tree(), splitID); split != nil && split.Split != nil {
				h.dragSplit = splitID
				h.mouse.StartDrag(mi.X, mi.Y, appDeckDividerRegion, split.Split.Ratio)
				return nil, true
			}
		}
	}
	if action.Type == mouse.ActionDrag && action.DragStartID == appDeckDividerRegion {
		split := panelayout.Find(h.deck.Tree(), h.dragSplit)
		if split != nil && split.Split != nil {
			ratio := h.mouse.DragStartValue()
			if split.Split.Axis == panelayout.Rows && h.canvas.H > 0 {
				ratio += action.DragDY * 100 / h.canvas.H
			} else if split.Split.Axis == panelayout.Columns && h.canvas.W > 0 {
				ratio += action.DragDX * 100 / h.canvas.W
			}
			h.deck.SetRatio(h.dragSplit, ratio)
		}
		return nil, true
	}
	if action.Type == mouse.ActionDragEnd && action.DragStartID == appDeckDividerRegion {
		h.dragSplit = 0
		m.persistAppContentDeck(h)
		return nil, true
	}
	switch msg.(type) {
	case tea.MouseClickMsg:
		if mi.Button == tea.MouseLeft {
			h.press, h.dragged, h.pressX, h.pressY = nil, false, mi.X, mi.Y
			for i := range h.links {
				hit := &h.links[i]
				if hit.Generation == h.generation && hit.Rect.Contains(mi.X, mi.Y) {
					h.press = hit
					break
				}
			}
		}
	case tea.MouseMotionMsg:
		if h.press != nil && (mi.X != h.pressX || mi.Y != h.pressY) {
			h.dragged = true
		}
	case tea.MouseReleaseMsg:
		if h.press != nil {
			hit, activate := *h.press, !h.dragged && h.press.Rect.Contains(mi.X, mi.Y)
			h.press = nil
			if activate {
				return m.openAppContent(h.workdir, h.pluginID, hit.Ref), true
			}
		}
	}
	if click, ok := msg.(tea.MouseClickMsg); ok && click.Button == tea.MouseLeft {
		if paneframe.FocusLeafAt(appDeckHost{h}, mi.X, mi.Y) {
			h.syncInnerFocus()
		}
	}
	leaf := panelayout.Find(h.deck.Tree(), h.deck.FocusedLeaf())
	if leaf == nil || leaf.Kind == panelayout.Primary {
		adjusted := offsetMouse(msg, -h.primaryInner.X, -h.primaryInner.Y)
		newPlugin, cmd := h.plugin.Update(adjusted)
		h.plugin = newPlugin
		m.adoptAppContentPlugin(h)
		return cmd, true
	}
	cmd := h.handlePassiveMouse(msg, leaf)
	m.persistAppContentDeck(h)
	return cmd, true
}

func offsetMouse(msg tea.MouseMsg, dx, dy int) tea.MouseMsg {
	switch msg := msg.(type) {
	case tea.MouseClickMsg:
		msg.X, msg.Y = msg.X+dx, msg.Y+dy
		return msg
	case tea.MouseReleaseMsg:
		msg.X, msg.Y = msg.X+dx, msg.Y+dy
		return msg
	case tea.MouseMotionMsg:
		msg.X, msg.Y = msg.X+dx, msg.Y+dy
		return msg
	case tea.MouseWheelMsg:
		msg.X, msg.Y = msg.X+dx, msg.Y+dy
		return msg
	default:
		return msg
	}
}

func (h *appContentDeck) handlePassiveMouse(msg tea.MouseMsg, leaf *panelayout.Node) tea.Cmd {
	mi := msg.Mouse()
	if _, ok := msg.(tea.MouseClickMsg); ok && mi.Button == tea.MouseLeft {
		region := h.mouse.HitMap.Test(mi.X, mi.Y)
		if region != nil {
			switch region.ID {
			case appDeckCloseRegion:
				h.deck.FocusLeaf(leaf.ID)
				h.deck.CloseActive()
				h.syncInnerFocus()
			case appDeckTabRegion:
				if hit, ok := region.Data.(appDeckTabHit); ok {
					h.deck.FocusLeaf(hit.leafID)
					if hit.close {
						h.deck.CloseTab(hit.leafID, hit.index)
						h.syncInnerFocus()
						return nil
					}
					return h.deck.SelectTab(hit.leafID, hit.index)
				}
			}
		}
	}
	if wheel, ok := msg.(tea.MouseWheelMsg); ok {
		delta := 1
		if wheel.Button == tea.MouseWheelUp {
			delta = -1
		}
		switch v := h.deck.Viewer(leaf.ID).(type) {
		case *docview.Model:
			v.Scroll(delta)
		case *issueview.Model:
			v.Scroll(delta)
		case *noteview.Model:
			v.Scroll(delta)
		case *workspacediff.View:
			v.ScrollContent(delta, v.Height())
		case *resourceview.Model:
			v.ScrollBy(delta)
		}
	}
	return nil
}

// appContentWheelAtBoundary mirrors appContentMouse's pointer ownership before
// Update runs. The pre-update filter must ask the leaf under the pointer, not
// the primary plugin hidden beside it, or a short Files preview can swallow a
// valid wheel intended for a long passive document.
func (m Model) appContentWheelAtBoundary(wheel tea.MouseWheelMsg) (boundary, owned bool) {
	h := m.currentContentDeck()
	if h == nil || !h.laidOut {
		return false, false
	}
	delta := 0
	switch wheel.Button {
	case tea.MouseWheelUp:
		delta = -1
	case tea.MouseWheelDown:
		delta = 1
	default:
		return false, false
	}
	leaf := paneframe.LeafAtForHost(appDeckHost{h}, h.layout, wheel.X, wheel.Y)
	if leaf == nil {
		return false, false
	}
	if leaf.Kind == panelayout.Primary {
		consumer, ok := h.plugin.(plugin.WheelBoundaryConsumer)
		if !ok {
			return false, true
		}
		adjusted, ok := offsetMouse(wheel, -h.primaryInner.X, -h.primaryInner.Y).(tea.MouseWheelMsg)
		if !ok {
			return false, true
		}
		return consumer.WheelAtBoundary(adjusted), true
	}
	switch v := h.deck.Viewer(leaf.ID).(type) {
	case *docview.Model:
		return v.ScrollAtBoundary(delta), true
	case *issueview.Model:
		return v.ScrollAtBoundary(delta), true
	case *noteview.Model:
		return v.ScrollAtBoundary(delta), true
	case *workspacediff.View:
		return v.ScrollAtBoundary(delta, v.Height()), true
	case *resourceview.Model:
		return v.ScrollAtBoundary(delta), true
	default:
		return false, true
	}
}

func (m *Model) handleAppContentKey(key tea.KeyPressMsg) (tea.Cmd, bool) {
	h := m.activeContentDeck()
	if h == nil || h.deck.Leaf(panelayout.Document)+h.deck.Leaf(panelayout.Issue)+h.deck.Leaf(panelayout.Note)+h.deck.Leaf(panelayout.Diff)+h.deck.Leaf(panelayout.Resource) == 0 {
		return nil, false
	}
	leaf := panelayout.Find(h.deck.Tree(), h.deck.FocusedLeaf())
	if leaf == nil {
		return nil, false
	}
	if leaf.Kind == panelayout.Primary {
		provider, ok := h.plugin.(plugin.PaneFocusProvider)
		if !ok || len(provider.PaneFocusStops()) == 0 {
			// A primary sub-mode with no projected stops owns its keys. Git's
			// full-screen diff is the important case: Tab returns to its sidebar
			// and must not enter a passive outer leaf left open beside it.
			return nil, false
		}
	}
	if key.Code == tea.KeyTab {
		cmd := h.cycleCombinedFocus(key.Mod.Contains(tea.ModShift))
		m.persistAppContentDeck(h)
		return cmd, true
	}
	if leaf.Kind == panelayout.Primary {
		return nil, false
	}
	if view, ok := h.deck.Viewer(leaf.ID).(*docview.Model); ok && view.SearchActive() {
		_, cmd := view.HandleSearchKey(key)
		return cmd, true
	}
	switch key.String() {
	case "q", "esc":
		h.deck.HideFocused()
		h.syncInnerFocus()
		m.persistAppContentDeck(h)
		return nil, true
	case "x":
		h.deck.CloseActive()
		h.syncInnerFocus()
		m.persistAppContentDeck(h)
		return nil, true
	case "{":
		cmd := h.deck.CycleTab(-1)
		m.persistAppContentDeck(h)
		return cmd, true
	case "}":
		cmd := h.deck.CycleTab(1)
		m.persistAppContentDeck(h)
		return cmd, true
	}
	// Sidecar's own globals outrank a passive leaf. A focused document, note,
	// or diff must not swallow the keys that switch plugins, open the palette,
	// or reach the switchers — those belong to the host's switch, which runs
	// later in the key ladder. Returning false hands them back; everything the
	// deck structurally owns (tab, q/esc, x, tab cycling) was answered above,
	// and an active in-document search consumed its keys even earlier.
	if keymap.GlobalKeys[key.String()] {
		return nil, false
	}
	switch v := h.deck.Viewer(leaf.ID).(type) {
	case *docview.Model:
		switch key.String() {
		case "/":
			v.StartSearch()
			return nil, true
		case "e":
			return m.enterAppContentDocumentEdit(), true
		case "r":
			return h.deck.ReloadFocused(), true
		case "m":
			v.ToggleRenderMode()
			return nil, true
		case "w":
			v.ToggleWrap()
			return nil, true
		case "ctrl+r":
			return docview.Reveal(v.Root(), v.Title()), true
		}
		v.HandleKey(key)
		return nil, true
	case *issueview.Model:
		_, cmd := v.HandleKey(key)
		return cmd, true
	case *noteview.Model:
		switch key.String() {
		case "y":
			if data := v.Data(); data != nil {
				return noteview.CopyMarkdown(data), true
			}
		case "Y", "shift+y":
			if data := v.Data(); data != nil {
				return noteview.CopyID(data), true
			}
		}
		_, cmd := v.HandleKey(key)
		return cmd, true
	case *workspacediff.View:
		cmd, _ := v.HandleKey(key)
		return cmd, true
	case *resourceview.Model:
		switch key.String() {
		case "r":
			return v.Refresh(), true
		case "o":
			if safe, ok := contentlink.SafeHTTPURL(v.SourceURL()); ok {
				return openPathCmd(safe), true
			}
			return nil, true
		case "j", "down":
			v.ScrollBy(1)
			return nil, true
		case "k", "up":
			v.ScrollBy(-1)
			return nil, true
		case "pgdown":
			v.ScrollBy(max(v.Height()-1, 1))
			return nil, true
		case "pgup":
			v.ScrollBy(-max(v.Height()-1, 1))
			return nil, true
		}
	}
	return nil, true
}

type appDeckFocusStop struct {
	inner string
	leaf  int
}

func (h *appContentDeck) focusRing() ([]appDeckFocusStop, plugin.PaneFocusProvider) {
	provider, ok := h.plugin.(plugin.PaneFocusProvider)
	if !ok {
		return nil, nil
	}
	var ring []appDeckFocusStop
	primary := h.deck.Leaf(panelayout.Primary)
	for _, s := range provider.PaneFocusStops() {
		ring = append(ring, appDeckFocusStop{inner: s.ID, leaf: primary})
	}
	for _, placement := range h.layout.Leaves {
		if placement.Node.Kind != panelayout.Primary {
			ring = append(ring, appDeckFocusStop{leaf: placement.Node.ID})
		}
	}
	return ring, provider
}

func (h *appContentDeck) cycleCombinedFocus(reverse bool) tea.Cmd {
	ring, provider := h.focusRing()
	if len(ring) == 0 {
		return nil
	}
	current := 0
	for i, s := range ring {
		if s.leaf == h.deck.FocusedLeaf() && (s.leaf != h.deck.Leaf(panelayout.Primary) || s.inner == provider.PaneFocus()) {
			current = i
			break
		}
	}
	delta := 1
	if reverse {
		delta = -1
	}
	next := (current + delta + len(ring)) % len(ring)
	h.deck.FocusLeaf(ring[next].leaf)
	if ring[next].inner != "" {
		_ = provider.SetPaneFocus(ring[next].inner)
	}
	h.syncInnerFocus()
	return nil
}

type appDeckFocusCycler struct{ h *appContentDeck }

func (c appDeckFocusCycler) AtFocusCycleEnd(reverse bool) bool {
	ring, provider := c.h.focusRing()
	if len(ring) == 0 || provider == nil {
		return false
	}
	current := -1
	for i, s := range ring {
		if s.leaf == c.h.deck.FocusedLeaf() && (s.leaf != c.h.deck.Leaf(panelayout.Primary) || s.inner == provider.PaneFocus()) {
			current = i
			break
		}
	}
	if reverse {
		return current == 0
	}
	return current == len(ring)-1
}

func (c appDeckFocusCycler) FocusCycleStart(reverse bool) tea.Cmd {
	ring, provider := c.h.focusRing()
	if len(ring) == 0 || provider == nil {
		return nil
	}
	index := 0
	if reverse {
		index = len(ring) - 1
	}
	stop := ring[index]
	c.h.deck.FocusLeaf(stop.leaf)
	if stop.inner != "" {
		_ = provider.SetPaneFocus(stop.inner)
	}
	c.h.syncInnerFocus()
	return nil
}

func (m Model) appContentContext() (string, bool) {
	h := m.currentContentDeck()
	if h == nil {
		return "", false
	}
	leaf := panelayout.Find(h.deck.Tree(), h.deck.FocusedLeaf())
	if leaf == nil || leaf.Kind == panelayout.Primary {
		return "", false
	}
	switch leaf.Kind {
	case panelayout.Document:
		if h.appContentDocumentEditing() {
			return "workspace-doc-edit", true
		}
		if view, ok := h.deck.Viewer(leaf.ID).(*docview.Model); ok && view.SearchActive() {
			return "workspace-doc-find", true
		}
		return "workspace-doc", true
	case panelayout.Issue:
		return "workspace-issue", true
	case panelayout.Note:
		return "workspace-note", true
	case panelayout.Diff:
		return "workspace-diff", true
	case panelayout.Resource:
		return "workspace-resource", true
	default:
		return "", false
	}
}

func (m *Model) appContentCommands() []plugin.Command {
	ctx, ok := m.appContentContext()
	if !ok {
		return nil
	}
	command := func(id, name, description string, priority int) plugin.Command {
		return plugin.Command{ID: id, Name: name, Description: description, Context: ctx, Priority: priority,
			Handler: func() tea.Cmd { return m.runAppContentCommand(id) }}
	}
	if ctx == "workspace-doc-find" {
		cmds := docview.SearchCommands(ctx)
		for i := range cmds {
			id := cmds[i].ID
			cmds[i].Handler = func() tea.Cmd { return m.runAppContentCommand(id) }
		}
		return cmds
	}
	if ctx == "workspace-doc-edit" {
		return nil
	}
	cmds := []plugin.Command{
		command("close", "Close", "Hide content pane", 1),
		command("close-tab", "Tab×", "Close active content tab", 2),
		command("prev-tab", "Tab←", "Previous content tab", 3),
		command("next-tab", "Tab→", "Next content tab", 4),
		command("next-pane", "Focus", "Focus next pane", 5),
		command("prev-pane", "Back", "Focus previous pane", 6),
	}
	h := m.currentContentDeck()
	leaf := panelayout.Find(h.deck.Tree(), h.deck.FocusedLeaf())
	if leaf == nil {
		return cmds
	}
	switch v := h.deck.Viewer(leaf.ID).(type) {
	case *docview.Model:
		cmds = append(cmds,
			command("search-content", "InFile", "Search this file's contents", 7),
			command("edit", "Edit", "Edit this file inline", 8),
			command("toggle-wrap", "Wrap", "Toggle line wrapping", 10),
			command("reveal", "Reveal", "Reveal in file manager", 11),
		)
		if terminallink.Markdown(v.Title()) {
			name := "Raw"
			if !v.Rendered() {
				name = "Render"
			}
			cmds = append(cmds, command("render", name, "Toggle rendered and raw markdown", 9))
		}
	case *issueview.Model:
		cmds = append(cmds,
			command("open-item", "Open", "Open selected parent or subtask", 7),
			command("open-in-td", "TD", "Open selected issue in td", 8),
			command("yank-issue", "Yank", "Copy issue as markdown", 9),
			command("yank-issue-key", "YankID", "Copy issue ID", 10),
		)
	case *noteview.Model:
		cmds = append(cmds,
			command("yank-note", "Yank", "Copy note as markdown", 7),
			command("yank-note-key", "YankID", "Copy note ID", 8),
		)
	case *workspacediff.View:
		for _, viewerCommand := range v.Commands(ctx) {
			id := viewerCommand.ID
			viewerCommand.Handler = func() tea.Cmd { return m.runAppContentCommand(id) }
			cmds = append(cmds, viewerCommand)
		}
	case *resourceview.Model:
		for i, viewerCommand := range resourceview.Commands() {
			if viewerCommand.ID == resourceview.CommandCloseTab || viewerCommand.ID == resourceview.CommandPrevTab || viewerCommand.ID == resourceview.CommandNextTab {
				continue
			}
			cmds = append(cmds, command(viewerCommand.ID, viewerCommand.Name, viewerCommand.Name+" resource", 7+i))
		}
	}
	return cmds
}

func (m *Model) runAppContentCommand(id string) tea.Cmd {
	h := m.currentContentDeck()
	if h == nil {
		return nil
	}
	switch id {
	case "close":
		h.deck.HideFocused()
		h.syncInnerFocus()
		m.persistAppContentDeck(h)
		m.updateContext()
	case "close-tab":
		h.deck.CloseActive()
		h.syncInnerFocus()
		m.persistAppContentDeck(h)
		m.updateContext()
	case "prev-tab":
		cmd := h.deck.CycleTab(-1)
		m.persistAppContentDeck(h)
		m.updateContext()
		return cmd
	case "next-tab":
		cmd := h.deck.CycleTab(1)
		m.persistAppContentDeck(h)
		m.updateContext()
		return cmd
	case "next-pane":
		cmd := h.cycleCombinedFocus(false)
		m.persistAppContentDeck(h)
		m.updateContext()
		return cmd
	case "prev-pane":
		cmd := h.cycleCombinedFocus(true)
		m.persistAppContentDeck(h)
		m.updateContext()
		return cmd
	}
	leaf := panelayout.Find(h.deck.Tree(), h.deck.FocusedLeaf())
	if leaf == nil {
		return nil
	}
	switch view := h.deck.Viewer(leaf.ID).(type) {
	case *docview.Model:
		switch id {
		case "search-content":
			view.StartSearch()
		case "edit":
			return m.enterAppContentDocumentEdit()
		case "render":
			view.ToggleRenderMode()
		case "toggle-wrap":
			view.ToggleWrap()
		case "reveal":
			return docview.Reveal(view.Root(), view.Title())
		case "confirm":
			_, cmd := view.HandleSearchKey(tea.KeyPressMsg{Code: tea.KeyEnter})
			return cmd
		case "next-match":
			_, cmd := view.HandleSearchKey(tea.KeyPressMsg{Code: 'n', Text: "n"})
			return cmd
		case "prev-match":
			_, cmd := view.HandleSearchKey(tea.KeyPressMsg{Code: 'N', Text: "N"})
			return cmd
		case "cancel":
			_, cmd := view.HandleSearchKey(tea.KeyPressMsg{Code: tea.KeyEscape})
			return cmd
		}
	case *issueview.Model:
		key := map[string]string{"open-item": "enter", "open-in-td": "O", "yank-issue": "y", "yank-issue-key": "Y"}[id]
		if key != "" {
			_, cmd := view.HandleKey(appContentKeyPress(key))
			return cmd
		}
	case *noteview.Model:
		switch id {
		case "yank-note":
			if data := view.Data(); data != nil {
				return noteview.CopyMarkdown(data)
			}
		case "yank-note-key":
			if data := view.Data(); data != nil {
				return noteview.CopyID(data)
			}
		}
	case *workspacediff.View:
		key := map[string]string{
			"diff-open": "enter", "diff-down": "j", "diff-up": "k", "diff-back": "h",
			"toggle-diff-view": "v", "toggle-diff-scope": "z", "next-file": ".", "prev-file": ",",
			"file-picker": "f", "diff-next-change": "n", "diff-top": "g", "diff-bottom": "G",
			"diff-page-down": "pgdown", "diff-page-up": "pgup", "diff-scroll-down": "j", "diff-scroll-up": "k",
		}[id]
		if key != "" {
			cmd, _ := view.HandleKey(appContentKeyPress(key))
			return cmd
		}
	case *resourceview.Model:
		switch id {
		case resourceview.CommandRefresh:
			return view.Refresh()
		case resourceview.CommandOpenSource:
			if safe, ok := contentlink.SafeHTTPURL(view.SourceURL()); ok {
				return openPathCmd(safe)
			}
		}
	}
	m.persistAppContentDeck(h)
	m.updateContext()
	return nil
}

func appContentKeyPress(key string) tea.KeyPressMsg {
	switch key {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "pgdown":
		return tea.KeyPressMsg{Code: tea.KeyPgDown}
	case "pgup":
		return tea.KeyPressMsg{Code: tea.KeyPgUp}
	default:
		runes := []rune(key)
		if len(runes) == 1 {
			return tea.KeyPressMsg{Code: runes[0], Text: key}
		}
		return tea.KeyPressMsg{}
	}
}

type appContentCommandPlugin struct {
	plugin.Plugin
	commands []plugin.Command
}

func (p appContentCommandPlugin) Commands() []plugin.Command { return p.commands }

func (h *appContentDeck) SetResourceMatchers(matchers []contentlink.ResourceMatcher) {
	h.resourceMatchers = append([]contentlink.ResourceMatcher(nil), matchers...)
}
func (h *appContentDeck) SetResourceResolver(resolve resourceview.Resolver) {
	h.deck.ConfigureViewers(func(kind panelayout.Kind, model any) {
		if v, ok := model.(*resourceview.Model); ok {
			v.SetResolver(resolve)
		}
	})
}
