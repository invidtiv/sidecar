package workspace

import (
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/contentlink"
	"github.com/marcus/sidecar/internal/targetactivation"
	"github.com/marcus/sidecar/internal/termpreview"
)

func (p *Plugin) SetTerminalLinkCoordinator(coordinator termpreview.LinkCoordinator) {
	p.terminalLinks = coordinator
}

func (p *Plugin) terminalAllowedLinkKinds() contentlink.KindSet {
	kinds := contentlink.NewKindSet(
		contentlink.KindURL, contentlink.KindFile, contentlink.KindInternal,
		contentlink.KindSession,
	)
	if p.paneRoot != nil {
		kinds[contentlink.KindIssue] = struct{}{}
		kinds[contentlink.KindDiff] = struct{}{}
		kinds[contentlink.KindResource] = struct{}{}
	}
	return kinds
}

// PrepareTerminalLinks prepares primary and split-panel visible rows from the
// same geometry the renderer and pointer mapper use.
func (p *Plugin) PrepareTerminalLinks() {
	if p == nil || p.terminalLinks == nil {
		return
	}
	p.primaryLinkState = p.prepareTerminalLinkSurface(false, p.primaryLinkState)
	if p.termPanelVisible {
		p.panelLinkState = p.prepareTerminalLinkSurface(true, p.panelLinkState)
	} else {
		p.panelLinkState = termpreview.LinkState{}
	}
}

func (p *Plugin) prepareTerminalLinkSurface(termPanel bool, previous termpreview.LinkState) termpreview.LinkState {
	buffer := p.terminalOutputBuffer(termPanel)
	context := p.terminalLinkSurfaceContext(termPanel)
	if buffer == nil || !context.ok {
		return termpreview.LinkState{}
	}
	in := p.terminalWindowInputFor(termPanel)
	layout := calculateTerminalViewportLayout(in)
	lines := buffer.LinesRange(layout.Start, layout.End)
	rows := make([]termpreview.LinkRow, 0, len(lines))
	for i, line := range lines {
		rows = append(rows, termpreview.LinkRow{
			AbsoluteLine: in.AbsoluteBase + layout.Start + i,
			Text:         line,
		})
	}
	allowed := p.terminalAllowedLinkKinds()
	scope := termpreview.LinkScope{
		Host: "workspace", Surface: context.surface, Target: context.target,
		Root: context.root, Buffer: buffer,
		AllowedKinds:      termpreview.AllowedKindsKey(allowed),
		MatcherGeneration: p.linkMatcherGeneration,
	}
	return p.terminalLinks.Prepare(termpreview.LinkPrepare{
		Scope: scope, Rows: rows, Allowed: allowed, Matchers: p.resourceMatchers,
		Previous: previous,
	})
}

type terminalLinkRevalidatedMsg struct {
	Epoch      uint64
	Context    terminalLinkSurfaceContext
	Scope      termpreview.LinkScope
	TermPanel  bool
	Link       terminalLink
	Freeze     terminalViewportFreeze
	PaneTarget bool
	Result     termpreview.FreshLinkResult
}

func (p *Plugin) revalidateTerminalLink(link terminalLink, context terminalLinkSurfaceContext, termPanel bool) (tea.Cmd, bool) {
	if p.terminalLinks == nil || p.ctx == nil || !context.ok {
		return nil, false
	}
	raw := link.Raw
	if raw == "" {
		raw = link.Value
	}
	request := termpreview.FreshLinkRequest{Root: context.root, RawRoot: context.rawRoot, Candidate: contentlink.Pending{Kind: link.Kind, Raw: raw}}
	scope := p.primaryLinkState.Scope()
	if termPanel {
		scope = p.panelLinkState.Scope()
	}
	if scope.Root == "" || scope.Root != context.root || scope.Surface != context.surface || scope.Target != context.target {
		return nil, false
	}
	epoch := p.ctx.Epoch
	freeze := p.captureTerminalViewportForDocOpen(termPanel)
	paneTarget := p.paneRoot != nil && (link.Kind == contentlink.KindDiff || (link.Kind == contentlink.KindFile && docPaneTarget(link.Value)))
	cmd := p.terminalLinks.ResolveFresh(request, func(result termpreview.FreshLinkResult) tea.Msg {
		return terminalLinkRevalidatedMsg{Epoch: epoch, Context: context, Scope: scope, TermPanel: termPanel, Link: link, Freeze: freeze, PaneTarget: paneTarget, Result: result}
	})
	return cmd, cmd != nil
}

func (p *Plugin) applyTerminalLinkRevalidated(msg terminalLinkRevalidatedMsg) tea.Cmd {
	if p.ctx == nil || msg.Epoch != p.ctx.Epoch || !msg.Result.Found || msg.Result.Ref.Kind != msg.Link.Kind {
		return nil
	}
	current := p.terminalLinkSurfaceContext(msg.TermPanel)
	currentScope := p.primaryLinkState.Scope()
	if msg.TermPanel {
		currentScope = p.panelLinkState.Scope()
	}
	if !current.ok || current != msg.Context || currentScope != msg.Scope || msg.Result.Request.Root != current.root {
		return nil
	}
	link := msg.Link
	link.Value = msg.Result.Ref.Value
	link.Root = current.root
	span := link.span()
	span.Extra.Raw = ""
	plan, err := targetactivation.PlanForSpan(span)
	if err != nil {
		return nil
	}
	switch plan.Kind {
	case targetactivation.PlanOpenFile:
		display := msg.Result.Ref.Value
		absolute := display
		if !filepath.IsAbs(filepath.FromSlash(display)) {
			absolute = filepath.Join(current.root, filepath.FromSlash(display))
		}
		cmd := p.openResolvedFilePreview(current.root, strings.TrimSuffix(current.surface, ":panel"), display, absolute, plan.Line)
		if cmd != nil {
			p.clearTerminalSelection()
			p.completeRevalidatedPaneActivation(contentlink.KindFile, msg.TermPanel, msg.Freeze, msg.PaneTarget)
		}
		return cmd
	case targetactivation.PlanOpenDiff:
		cmd, _ := p.activateDiffLink(plan.Spec)
		if cmd != nil {
			p.completeRevalidatedPaneActivation(contentlink.KindDiff, msg.TermPanel, msg.Freeze, msg.PaneTarget)
		}
		return cmd
	default:
		return nil
	}
}

func (p *Plugin) completeRevalidatedPaneActivation(kind contentlink.Kind, termPanel bool, freeze terminalViewportFreeze, paneTarget bool) {
	if !paneTarget {
		return
	}
	leaf := p.openedPaneLeaf(kind)
	if leaf == nil {
		return
	}
	p.applyTerminalViewportFreeze(freeze)
	p.exitInteractiveMode()
	p.activePane = PanePreview
	p.paneFocus = leaf.ID
	p.termPanelFocused = false
}
