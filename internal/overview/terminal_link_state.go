package overview

import (
	"path/filepath"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/contentlink"
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
	rawRoot := m.previewResolveRoot()
	root := m.canonicalTerminalLinkRoot(rawRoot)
	if !window.ok || buffer == nil || root == "" {
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
	allowed := m.terminalAllowedLinkKinds()
	scope := termpreview.LinkScope{
		Host: "overview", Surface: m.preview.workspaceID, Target: target,
		Root: root, Buffer: buffer,
		AllowedKinds:      termpreview.AllowedKindsKey(allowed),
		MatcherGeneration: m.linkMatcherGeneration,
	}
	m.previewTerminalLeaf().LinkState = m.terminalLinks.Prepare(termpreview.LinkPrepare{
		Scope: scope, Rows: rows, Allowed: allowed,
		Matchers: m.resourceMatchers, Previous: m.previewTerminalLeaf().LinkState,
	})
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
	request := termpreview.FreshLinkRequest{Root: scope.Root, RawRoot: m.previewResolveRoot(), Candidate: contentlink.Pending{Kind: span.Kind, Raw: raw}}
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
		file, err := openPreviewFile(current.Root, plan.Path, plan.Path)
		if err != nil {
			return nil
		}
		_ = file.Close()
		cmd := m.openPreviewContent(contentlink.Ref{Kind: contentlink.KindFile, Value: plan.Path, Line: plan.Line}, "Document")
		if cmd != nil {
			m.clearPreviewSelection()
		}
		return cmd
	}
	cmd, _ := m.activatePreviewPlan(plan)
	return cmd
}
