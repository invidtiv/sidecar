package overview

import (
	"path/filepath"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/contentlink"
	"github.com/marcus/sidecar/internal/contentpanes"
	"github.com/marcus/sidecar/internal/targetactivation"
	"github.com/marcus/sidecar/internal/termpreview"
)

type terminalLinkRootContext struct {
	raw  string
	root string
}

func (m *Model) terminalAllowedLinkKinds() contentlink.KindSet {
	kinds := contentlink.NewKindSet(contentlink.KindURL, contentlink.KindFile, contentlink.KindInternal, contentlink.KindSession)
	if m.preview.paneRoot != nil {
		kinds[contentlink.KindIssue] = struct{}{}
		kinds[contentlink.KindDiff] = struct{}{}
		kinds[contentlink.KindResource] = struct{}{}
	}
	return kinds
}

func (m *Model) SetTerminalLinkCoordinator(coordinator termpreview.LinkCoordinator) {
	m.terminalLinks = coordinator
}

// PrepareTerminalLinks builds the immutable visible-row answer on the update
// path. View and pointer hit testing only read the terminal leaf's link state.
func (m *Model) PrepareTerminalLinks() {
	if m == nil || m.terminalLinks == nil || !m.preview.visible {
		return
	}
	window := m.previewWindow()
	buffer := m.previewBuffer()
	scope, allowed, ok := m.previewTerminalLinkScope("")
	if !window.ok || buffer == nil || !ok {
		m.previewTerminalLeaf().LinkState = termpreview.LinkState{}
		return
	}
	lines := buffer.LinesRange(window.layout.Start, window.layout.End)
	rows := make([]termpreview.LinkRow, 0, len(lines))
	for i, line := range lines {
		rows = append(rows, termpreview.LinkRow{
			AbsoluteLine: window.input.AbsoluteBase + window.layout.Start + i,
			Text:         line,
		})
	}
	target := m.previewTerminalLeaf().Target.Session + "\x00" + m.previewTerminalLeaf().Target.Pane
	scope.Target = target
	scope.Buffer = buffer
	m.previewTerminalLeaf().LinkState = m.terminalLinks.Prepare(termpreview.LinkPrepare{
		Scope: scope, Rows: rows, Allowed: allowed,
		Matchers: m.resourceMatchers, Previous: m.previewTerminalLeaf().LinkState,
	})
}

// previewTerminalLinkScope is the source-aware identity a terminal row is
// prepared under. Remote rows keep the host Path as a hint and never
// EvalSymlinks it on this machine.
func (m *Model) previewTerminalLinkScope(target string) (termpreview.LinkScope, contentlink.KindSet, bool) {
	workspace, ok := m.SelectedWorkspace()
	if !ok || workspace.Path == "" {
		return termpreview.LinkScope{}, nil, false
	}
	root := workspace.Path
	sourceHost := ""
	if workspace.Remote() {
		if !m.hostShows(workspace.HostID) {
			return termpreview.LinkScope{}, nil, false
		}
		sourceHost = workspace.HostID
	} else {
		root = m.canonicalTerminalLinkRoot(workspace.Path)
		if root == "" {
			return termpreview.LinkScope{}, nil, false
		}
	}
	allowed := m.terminalAllowedLinkKinds()
	return termpreview.LinkScope{
		Host: "overview", Surface: m.preview.workspaceID, Target: target,
		Root: root, SourceHost: sourceHost, Buffer: m.previewBuffer(),
		AllowedKinds:      termpreview.AllowedKindsKey(allowed),
		MatcherGeneration: m.linkMatcherGeneration,
	}, allowed, true
}

// ResolveRemoteTerminalLink is KindFile/KindDiff pending resolution for a
// showing remote Sessions row. It must not touch this machine's twin path.
func (m *Model) ResolveRemoteTerminalLink(hostID, root string, candidate contentlink.Pending) (contentlink.Ref, bool) {
	if m == nil || hostID == "" || (candidate.Kind != contentlink.KindFile && candidate.Kind != contentlink.KindDiff) {
		return contentlink.Ref{}, false
	}
	if !m.hostShows(hostID) {
		return contentlink.Ref{}, false
	}
	ctx, ok := m.previewDeckContext()
	if !ok || ctx.Source.HostID != hostID {
		return contentlink.Ref{}, false
	}
	if root != "" && ctx.Source.Root != "" && ctx.Source.Root != root {
		return contentlink.Ref{}, false
	}
	ref, err := contentpanes.ResolveDocument(m.documentSource(ctx), ctx.Source, candidate)
	return ref, err == nil && ref.Value != ""
}

func (m *Model) canonicalTerminalLinkRoot(raw string) string {
	if raw == "" {
		return ""
	}
	raw = filepath.Clean(raw)
	if m.terminalLinkRoot.raw == raw && m.terminalLinkRoot.root != "" {
		return m.terminalLinkRoot.root
	}
	root, err := filepath.EvalSymlinks(raw)
	if err != nil {
		root = raw
	}
	root = filepath.Clean(root)
	m.terminalLinkRoot = terminalLinkRootContext{raw: raw, root: root}
	return root
}

type previewLinkRevalidatedMsg struct {
	Generation  int
	WorkspaceID string
	Scope       termpreview.LinkScope
	Span        contentlink.Span
	Result      termpreview.FreshLinkResult
}

func (m *Model) revalidatePreviewLink(span contentlink.Span) (tea.Cmd, bool) {
	if m.terminalLinks == nil {
		return nil, false
	}
	scope := m.previewTerminalLeaf().LinkState.Scope()
	if scope.Root == "" {
		return nil, false
	}
	raw := span.Extra.Raw
	if raw == "" {
		raw = span.Value
	}
	request := termpreview.FreshLinkRequest{Root: scope.Root, RawRoot: m.previewResolveRoot(), HostID: scope.SourceHost, Candidate: contentlink.Pending{Kind: span.Kind, Raw: raw}}
	generation, workspaceID := m.preview.generation, m.preview.workspaceID
	cmd := m.terminalLinks.ResolveFresh(request, func(result termpreview.FreshLinkResult) tea.Msg {
		return previewLinkRevalidatedMsg{Generation: generation, WorkspaceID: workspaceID, Scope: scope, Span: span, Result: result}
	})
	return cmd, cmd != nil
}

func (m *Model) applyPreviewLinkRevalidated(msg previewLinkRevalidatedMsg) tea.Cmd {
	current := m.previewTerminalLeaf().LinkState.Scope()
	if msg.Generation != m.preview.generation || msg.WorkspaceID != m.preview.workspaceID || msg.Scope != current || !msg.Result.Found ||
		msg.Result.Request.Root != current.Root || msg.Result.Ref.Kind != msg.Span.Kind {
		return nil
	}
	span := msg.Span
	span.Value = msg.Result.Ref.Value
	span.Extra.Raw = ""
	plan, err := targetactivation.PlanForSpan(span)
	if err != nil {
		return nil
	}
	if plan.Kind == targetactivation.PlanOpenFile {
		workspace, ok := m.SelectedWorkspace()
		if !ok || workspace.Remote() {
			return nil
		}
		ctx, ok := m.previewDeckContext()
		if !ok || ctx.Source.Remote() {
			return nil
		}
		ref, err := contentpanes.ResolveDocument(m.previewDeckConfig(ctx).Source, ctx.Source, contentlink.Pending{
			Kind: contentlink.KindFile, Raw: plan.Path,
		})
		if err != nil || ref.Value == "" {
			return nil
		}
		ref.Line = plan.Line
		cmd := m.openPreviewContent(ref, "Document")
		if cmd != nil {
			m.clearPreviewSelection()
		}
		return cmd
	}
	cmd, _ := m.activatePreviewPlan(plan)
	return cmd
}
