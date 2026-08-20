package workspace

import (
	"os"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/app"
	"github.com/marcus/sidecar/internal/contentlink"
	"github.com/marcus/sidecar/internal/contentpanes"
	"github.com/marcus/sidecar/internal/docview"
	"github.com/marcus/sidecar/internal/issueview"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/resourceview"
	"github.com/marcus/sidecar/internal/state"
	"github.com/marcus/sidecar/internal/workspacediff"
)

func (p *Plugin) workspaceDeckContext(root, surface string) contentpanes.SurfaceContext {
	epoch := uint64(0)
	if p.ctx != nil {
		epoch = p.ctx.Epoch
	}
	return contentpanes.SurfaceContext{
		Root: root, DiffRoot: root, Surface: surface, DiffSurface: p.diffWorkspaceID(root, surface),
		BaseRef: p.selectedDiffBaseRef(), Epoch: epoch,
	}
}

func (p *Plugin) configureDeckViewer(kind panelayout.Kind, model any) {
	switch view := model.(type) {
	case *issueview.Model:
		view.OpenHandler = func(id string) tea.Cmd {
			ctx := p.contentDeck.Context()
			return p.openIssuePaneForSurface(ctx.Root, ctx.Surface, id)
		}
		view.OpenInTDHandler = app.OpenIssueInTD
	case *workspacediff.View:
		view.ViewMode = p.diff.ViewMode
		if w := state.GetDiffTabFileListWidth(); w > 0 {
			view.SetListWidth(w)
		}
		p.attachDiffPaintTo(view)
	}
}

func (p *Plugin) ensureWorkspaceDeck(root, surface string) *contentpanes.Deck {
	ctx := p.workspaceDeckContext(root, surface)
	cfg := contentpanes.Config{Renderer: p.markdownRenderer, ResourceResolver: p.resolveResource, ConfigureViewer: p.configureDeckViewer}
	hidden := p.hiddenPaneLayout
	if hidden != nil && hidden.Root == root && hidden.Surface == surface && paneLayoutHasRetainedTabs(hidden) {
		// The host's hidden snapshot owns exact wire-compatible geometry. Re-adopt
		// it before reopening so drag ratios and tabs survive both an in-process
		// hide and a relaunch.
		p.contentDeck = contentpanes.Decode(ctx, cfg, contentpanes.State{Version: 1, Root: workspaceDeckNode(hidden)})
	} else if p.contentDeck == nil {
		saved := p.encodePaneNode(p.paneRoot)
		if saved != nil {
			p.contentDeck = contentpanes.Decode(ctx, cfg, contentpanes.State{Version: 1, Root: workspaceDeckNode(saved)})
		} else {
			p.contentDeck = contentpanes.New(ctx, cfg)
		}
	} else {
		p.contentDeck.SetContext(ctx)
	}
	return p.contentDeck
}

func workspaceDeckNode(saved *state.PaneLayoutJSON) *contentpanes.NodeState {
	if saved == nil {
		return nil
	}
	if saved.Split != nil {
		axis := "columns"
		if saved.Split.Axis == "rows" {
			axis = "rows"
		}
		return &contentpanes.NodeState{Axis: axis, Ratio: saved.Split.Ratio, A: workspaceDeckNode(saved.Split.A), B: workspaceDeckNode(saved.Split.B)}
	}
	n := &contentpanes.NodeState{}
	pane := contentpanes.PaneState{Active: saved.Active}
	switch saved.Kind {
	case contentKindTerminal:
		n.Kind = "primary"
		return n
	case contentKindDoc:
		n.Kind, pane.Kind = "document", "document"
		for _, tab := range saved.Tabs {
			pane.Tabs = append(pane.Tabs, contentpanes.TabState{Ref: contentlink.Ref{Kind: contentlink.KindFile, Value: tab.Path}, Scroll: tab.Scroll, Wrap: tab.Wrap, Rendered: tab.Mode != "raw"})
		}
	case contentKindIssue:
		n.Kind, pane.Kind = "issue", "issue"
		for _, tab := range saved.IssueTabs {
			pane.Tabs = append(pane.Tabs, contentpanes.TabState{Ref: contentlink.Ref{Kind: contentlink.KindIssue, Value: tab.Issue}, Scroll: tab.Scroll})
		}
		if len(pane.Tabs) == 0 && saved.Issue != "" {
			pane.Tabs = append(pane.Tabs, contentpanes.TabState{Ref: contentlink.Ref{Kind: contentlink.KindIssue, Value: saved.Issue}, Scroll: saved.Scroll})
		}
	case contentKindDiff:
		n.Kind, pane.Kind = "diff", "diff"
		for _, tab := range saved.DiffTabs {
			pane.Tabs = append(pane.Tabs, contentpanes.TabState{Ref: contentlink.Ref{Kind: contentlink.KindDiff, Value: tab.Spec}, Scroll: tab.Scroll, Scope: tab.Scope, Mode: tab.Mode, Path: tab.Path})
		}
	case contentKindResource:
		n.Kind, pane.Kind = "resource", "resource"
		for _, tab := range saved.ResourceTabs {
			pane.Tabs = append(pane.Tabs, contentpanes.TabState{Ref: contentlink.Ref{Kind: contentlink.KindResource, Provider: tab.Provider, Matcher: tab.Matcher, Value: tab.Locator}, Scroll: tab.Scroll})
		}
	default:
		return nil
	}
	if len(pane.Tabs) == 0 {
		return nil
	}
	n.Pane = &pane
	return n
}

func (p *Plugin) workspaceDeckPlacement() (contentpanes.Placement, bool) {
	peer, ok := p.previewPeerBox()
	if !ok {
		return contentpanes.Placement{}, false
	}
	return contentpanes.Placement{Box: peer, Boxes: p.lastPaneBoxes(), Floors: paneTreeFloors(), Split: p.openSplit}, true
}

func (p *Plugin) openWorkspaceContent(root, surface string, ref contentlink.Ref, name string) tea.Cmd {
	return p.openWorkspaceContentFile(root, surface, ref, name, nil)
}

func (p *Plugin) openWorkspaceContentFile(root, surface string, ref contentlink.Ref, name string, file *os.File) tea.Cmd {
	wasInteractive := p.viewMode == ViewModeInteractive
	deck := p.ensureWorkspaceDeck(root, surface)
	placement, ok := p.workspaceDeckPlacement()
	if !ok {
		return nil
	}
	ctx := p.workspaceDeckContext(root, surface)
	var out contentpanes.Outcome
	if file != nil {
		out = deck.OpenDocumentFile(ctx, ref, placement, file)
	} else {
		out = deck.Open(ctx, ref, placement)
	}
	if out.Status == contentpanes.StatusRefused {
		if out.Refusal == contentpanes.RefusalFit {
			dimension := "wider"
			plan, planned := deck.PlanOpen(ref, placement.Boxes)
			if planned && panelayout.ApplyAxisOverride(plan, placement.Split).Axis == panelayout.Rows {
				dimension = "taller"
			}
			p.toastMessage, p.toastTime = name+" pane needs a "+dimension+" window; layout left unchanged", time.Now()
		}
		return nil
	}
	p.syncWorkspaceDeckProjection(root, surface)
	p.hiddenPaneLayout = nil
	if wasInteractive {
		// sidecar-open may split beside a live terminal without taking its input
		// mode away. Record the new leaf for close/hide routing without invoking
		// setFocusTarget, whose deliberate click/key behavior exits interactive.
		p.paneFocus, p.activePane = out.LeafID, PanePreview
	} else {
		p.focusLeaf(out.LeafID)
	}
	p.saveSelectionState()
	if out.CreatedLeaf {
		return tea.Batch(unwrapWorkspaceDeckLoad(out.Command), p.resizeDocTerminalCmd())
	}
	return unwrapWorkspaceDeckLoad(out.Command)
}

func unwrapWorkspaceDeckLoad(cmd tea.Cmd) tea.Cmd {
	if cmd == nil {
		return nil
	}
	return func() tea.Msg {
		msg := cmd()
		if result, ok := msg.(contentpanes.Result); ok {
			return result.Payload
		}
		return msg
	}
}

func (p *Plugin) syncWorkspaceDeckProjection(root, surface string) {
	deck := p.contentDeck
	if deck == nil {
		return
	}
	oldDocs, oldIssues, oldDiffs, oldResources := p.docs, p.issues, p.diffs, p.resources
	p.paneRoot, p.paneFocus = reconcileWorkspaceDeckTree(p.paneRoot, deck.Tree()), deck.FocusedLeaf()
	p.paneNextID = panelayout.MaxID(p.paneRoot) + 1
	p.docs = make(map[int]*docPane)
	p.issues = make(map[int]*issuePane)
	p.diffs = make(map[int]*diffPane)
	p.resources = make(map[int]*resourcePane)
	for _, kind := range []panelayout.Kind{panelayout.Document, panelayout.Issue, panelayout.Diff, panelayout.Resource} {
		leafID := deck.Leaf(kind)
		if leafID == 0 {
			continue
		}
		items, active := deck.Tabs(leafID)
		switch kind {
		case panelayout.Document:
			doc := oldDocs[leafID]
			if doc == nil {
				doc = newDocPane(leafID, root, surface, nil)
			}
			doc.leafID, doc.root, doc.surface = leafID, root, surface
			doc.tabs = docview.Tabs{Active: active}
			for _, item := range items {
				if view, ok := item.Viewer.(*docview.Model); ok {
					doc.tabs.Items = append(doc.tabs.Items, docview.Item{View: view})
				}
			}
			p.docs[leafID] = doc
		case panelayout.Issue:
			issue := oldIssues[leafID]
			if issue == nil {
				issue = &issuePane{}
			}
			issue.leafID, issue.root, issue.surface, issue.tabs = leafID, root, surface, issueview.Tabs{}
			for _, item := range items {
				if view, ok := item.Viewer.(*issueview.Model); ok {
					issue.tabs.Append(item.Ref.Value, view)
				}
			}
			issue.tabs.Active = active
			p.issues[leafID] = issue
		case panelayout.Diff:
			diff := oldDiffs[leafID]
			if diff == nil {
				diff = &diffPane{}
			}
			diff.leafID, diff.root, diff.surface, diff.tabs = leafID, root, surface, workspacediff.Group{}
			for _, item := range items {
				if view, ok := item.Viewer.(*workspacediff.View); ok {
					diff.tabs.Append(item.Ref.Value, view)
				}
			}
			diff.tabs.Active = active
			p.diffs[leafID] = diff
		case panelayout.Resource:
			res := oldResources[leafID]
			if res == nil {
				res = p.newResourcePane(leafID, root, surface)
			}
			res.leafID, res.root, res.surface = leafID, root, surface
			res.tabs.Items = nil
			res.tabs.SetResolver(p.resolveResource)
			for _, item := range items {
				if view, ok := item.Viewer.(*resourceview.Model); ok {
					res.tabs.Append(resourceview.TabKey(view.Reference()), view)
				}
			}
			res.tabs.Group.Active = active
			p.resources[leafID] = res
		}
	}
}

func reconcileWorkspaceDeckTree(current, fresh *panelayout.Node) *panelayout.Node {
	byID := make(map[int]*panelayout.Node)
	var index func(*panelayout.Node)
	index = func(n *panelayout.Node) {
		if n == nil {
			return
		}
		byID[n.ID] = n
		if n.Split != nil {
			index(n.Split.A)
			index(n.Split.B)
		}
	}
	index(current)
	var adopt func(*panelayout.Node) *panelayout.Node
	adopt = func(n *panelayout.Node) *panelayout.Node {
		if n == nil {
			return nil
		}
		out := byID[n.ID]
		if out == nil {
			out = &panelayout.Node{}
		}
		out.ID, out.Kind, out.ContentID = n.ID, n.Kind, n.ContentID
		if n.Split == nil {
			out.Split = nil
			return out
		}
		out.Split = &panelayout.Split{Axis: n.Split.Axis, Ratio: n.Split.Ratio, A: adopt(n.Split.A), B: adopt(n.Split.B)}
		return out
	}
	return adopt(fresh)
}

func (p *Plugin) replaceWorkspaceContent(root, surface string, ref contentlink.Ref) tea.Cmd {
	deck := p.ensureWorkspaceDeck(root, surface)
	out := deck.ReplaceActive(p.workspaceDeckContext(root, surface), ref)
	if !out.Accepted() {
		return nil
	}
	p.syncWorkspaceDeckProjection(root, surface)
	p.focusLeaf(out.LeafID)
	p.saveSelectionState()
	return unwrapWorkspaceDeckLoad(out.Command)
}

func (p *Plugin) applyWorkspaceDeckBroadcast(msg any) tea.Cmd {
	if p.contentDeck == nil {
		return nil
	}
	cmd := p.contentDeck.ApplyBroadcast(msg)
	ctx := p.contentDeck.Context()
	p.syncWorkspaceDeckProjection(ctx.Root, ctx.Surface)
	p.saveSelectionState()
	return cmd
}

func workspaceDeckTabCount(deck *contentpanes.Deck, kind panelayout.Kind) int {
	if deck == nil {
		return 0
	}
	items, _ := deck.Tabs(deck.Leaf(kind))
	return len(items)
}

func (p *Plugin) applyWorkspaceDeckResult(result contentpanes.Result) tea.Cmd {
	if p.contentDeck == nil {
		return nil
	}
	cmd, applied := p.contentDeck.Apply(result)
	if applied {
		ctx := p.contentDeck.Context()
		p.syncWorkspaceDeckProjection(ctx.Root, ctx.Surface)
		p.saveSelectionState()
	}
	return cmd
}

func (p *Plugin) selectWorkspaceDeckTab(kind panelayout.Kind, index int) tea.Cmd {
	if p.contentDeck == nil {
		return nil
	}
	cmd := p.contentDeck.SelectTab(p.contentDeck.Leaf(kind), index)
	ctx := p.contentDeck.Context()
	p.syncWorkspaceDeckProjection(ctx.Root, ctx.Surface)
	p.saveSelectionState()
	return cmd
}

func (p *Plugin) cycleWorkspaceDeckTab(kind panelayout.Kind, delta int) tea.Cmd {
	if p.contentDeck == nil || !p.contentDeck.FocusLeaf(p.contentDeck.Leaf(kind)) {
		return nil
	}
	cmd := p.contentDeck.CycleTab(delta)
	ctx := p.contentDeck.Context()
	p.syncWorkspaceDeckProjection(ctx.Root, ctx.Surface)
	p.saveSelectionState()
	return cmd
}

func (p *Plugin) closeWorkspaceDeckTab(kind panelayout.Kind) tea.Cmd {
	if p.contentDeck == nil || !p.contentDeck.FocusLeaf(p.contentDeck.Leaf(kind)) {
		return nil
	}
	p.contentDeck.CloseActive()
	leafClosed := p.contentDeck.Leaf(kind) == 0
	ctx := p.contentDeck.Context()
	p.syncWorkspaceDeckProjection(ctx.Root, ctx.Surface)
	p.saveSelectionState()
	if leafClosed {
		return p.resizeDocTerminalCmd()
	}
	return nil
}
