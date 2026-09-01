package overview

import (
	"context"
	"errors"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/contentlink"
	"github.com/marcus/sidecar/internal/contentpanes"
	"github.com/marcus/sidecar/internal/contentservice"
	"github.com/marcus/sidecar/internal/docview"
	"github.com/marcus/sidecar/internal/hosts"
	"github.com/marcus/sidecar/internal/issueview"
	appmsg "github.com/marcus/sidecar/internal/msg"
	"github.com/marcus/sidecar/internal/noteview"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/resourceview"
	"github.com/marcus/sidecar/internal/state"
	"github.com/marcus/sidecar/internal/workspacediff"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

func (m *Model) previewDeckContext() (contentpanes.SurfaceContext, bool) {
	workspace, ok := m.SelectedWorkspace()
	if !ok || workspace.ID == "" || workspace.Path == "" {
		return contentpanes.SurfaceContext{}, false
	}
	// A remote Path names a directory on another machine. Documents, issues,
	// notes, diffs, and resources are admitted only while the host still
	// Shows(); disconnected/disabled/unavailable stay closed.
	if workspace.Remote() && !m.hostShows(workspace.HostID) {
		return contentpanes.SurfaceContext{}, false
	}
	return m.previewSurfaceContext(workspace), true
}

func (m *Model) previewSurfaceContext(workspace workspaceinventory.Workspace) contentpanes.SurfaceContext {
	return contentpanes.SurfaceContext{
		Root: workspace.Path, DiffRoot: previewDiffPath(workspace), Surface: workspace.ID, Epoch: m.preview.contentEpoch,
		Source: sourceContextFromWorkspace(workspace, m.hostIncarnationFor(workspace.HostID)),
	}
}

func (m *Model) hostShows(hostID string) bool {
	if hostID == "" {
		return true
	}
	health, ok := m.hostHealth[hostID]
	return ok && health.State.Shows()
}

func (m *Model) documentSource(ctx contentpanes.SurfaceContext) contentpanes.Source {
	if !ctx.Source.Remote() {
		return contentpanes.LocalSource{}
	}
	return m.HostContentSource(ctx.Source.HostID)
}

// HostContentSource is the Document adapter for hostID. Tests inject a fake
// through contentSource; production builds RemoteSource from the Sessions
// runner and the host's advertised verbs.
func (m *Model) HostContentSource(hostID string) contentpanes.Source {
	if m.contentSource != nil {
		return m.contentSource
	}
	if hostID == "" {
		return contentpanes.LocalSource{}
	}
	return contentpanes.NewRemoteSource(hostID, m.hostVerbs(hostID), m.RunHostSidecar)
}

// RunHostSidecar is the hosts.RunSidecar seam the bound workspace plugin
// reuses for content reads and relayed acks.
func (m *Model) RunHostSidecar(ctx context.Context, hostID string, args []string, out any) error {
	return runRemoteSidecar(ctx, m.hostRegistry, hostID, args, out)
}

func remoteContentErrorCmd(err error) tea.Cmd {
	if err == nil {
		return nil
	}
	var missing *contentservice.MissingCapabilityError
	if errors.As(err, &missing) {
		return appmsg.ShowToast(missing.Error(), 4*time.Second)
	}
	return appmsg.ShowToast(err.Error(), 4*time.Second)
}

func remoteDocumentUnsupported(hostID, action string) tea.Cmd {
	if hostID == "" {
		hostID = "that host"
	}
	return appmsg.ShowToast(action+" isn't available for files on "+hostID, 3*time.Second)
}

func remoteIssueUnsupported(hostID, action string) tea.Cmd {
	if hostID == "" {
		hostID = "that host"
	}
	return appmsg.ShowToast(action+" isn't available for issues on "+hostID, 3*time.Second)
}

func remoteContentKindAdmitted(ref contentlink.Ref) bool {
	switch ref.Kind {
	case contentlink.KindFile, contentlink.KindIssue, contentlink.KindDiff, contentlink.KindResource:
		return true
	case contentlink.KindInternal:
		return ref.Namespace == "note"
	default:
		return false
	}
}

func sourceContextFromWorkspace(ws workspaceinventory.Workspace, incarnation uint64) contentpanes.SourceContext {
	src := contentpanes.SourceContext{
		HostID:          ws.HostID,
		HostIncarnation: incarnation,
		ProjectKey:      ws.ProjectKey,
		ProjectRoot:     ws.ProjectRoot,
		WorkspaceID:     ws.ID,
		WorkspaceKind:   ws.Kind,
		WorkspaceKey:    ws.Key,
		Root:            ws.Path,
	}
	if ws.HostID != "" {
		if _, rest, ok := hosts.SplitScopedKey(ws.ID); ok {
			src.WorkspaceID = rest
		}
		if _, rest, ok := hosts.SplitScopedKey(ws.ProjectKey); ok {
			src.ProjectKey = rest
		}
	}
	return src
}

func (m *Model) previewDeckConfig(ctx contentpanes.SurfaceContext) contentpanes.Config {
	return contentpanes.Config{
		Source:           m.documentSource(ctx),
		ResourceResolver: m.previewResourceResolver(ctx.Surface, ctx.Epoch),
		ConfigureViewer: func(kind panelayout.Kind, model any) {
			switch view := model.(type) {
			case *issueview.Model:
				view.OpenHandler = func(id string) tea.Cmd { return m.openPreviewIssue(id) }
				if ctx.Source.Remote() {
					hostID := ctx.Source.HostID
					view.OpenInTDHandler = func(string) tea.Cmd {
						return remoteIssueUnsupported(hostID, "Open in td")
					}
					view.FallbackRefs = nil
				} else {
					view.OpenInTDHandler = func(id string) tea.Cmd { return func() tea.Msg { return OpenIssueInTDMsg{IssueID: id} } }
					// Same cross-project fallback as the other two hosts: the app-level
					// config, read inside the fetch command.
					view.FallbackRefs = m.issueFallbackRefs
				}
			case *workspacediff.View:
				view.ViewMode = m.diff.ViewMode
				if w := state.GetDiffTabFileListWidth(); w > 0 {
					view.SetListWidth(w)
				}
			}
		},
	}
}

func (m *Model) newPreviewDeck(ctx contentpanes.SurfaceContext) *contentpanes.Deck {
	return contentpanes.New(ctx, m.previewDeckConfig(ctx))
}

func (m *Model) ensurePreviewDeck() (*contentpanes.Deck, contentpanes.SurfaceContext, []tea.Cmd, bool) {
	ctx, ok := m.previewDeckContext()
	if !ok {
		return nil, ctx, nil, false
	}
	if m.preview.deck == nil {
		m.preview.contentEpoch++
		ctx.Epoch = m.preview.contentEpoch
		m.preview.deck = m.newPreviewDeck(ctx)
		return m.preview.deck, ctx, nil, true
	}
	return m.preview.deck, ctx, m.preview.deck.SetContext(ctx), true
}

func (m *Model) previewDeckPlacement() (contentpanes.Placement, bool) {
	peer, ok := m.previewPeerBox()
	if !ok {
		return contentpanes.Placement{}, false
	}
	plan := m.pendingOpenPlan
	m.pendingOpenPlan = nil
	return contentpanes.Placement{
		Box: peer, Boxes: m.lastPreviewBoxes(), Floors: previewPaneFloors(), Split: m.openSplit, Plan: plan,
	}, true
}

func (m *Model) openPreviewContent(ref contentlink.Ref, name string) tea.Cmd {
	ctx, ok := m.previewDeckContext()
	if !ok {
		return nil
	}
	if ctx.Source.Remote() && !remoteContentKindAdmitted(ref) {
		return nil
	}
	deck, ctx, adopt, ok := m.ensurePreviewDeck()
	if !ok {
		return nil
	}
	placement, ok := m.previewDeckPlacement()
	if !ok {
		return nil
	}
	wasInteractive := m.PreviewInteractive()
	out := deck.Open(ctx, ref, placement)
	if out.Status == contentpanes.StatusRefused {
		if out.Refusal != contentpanes.RefusalFit {
			return nil
		}
		dimension := "wider"
		plan, planned := deck.PlanOpen(ref, placement.Boxes)
		if planned && panelayout.ApplyAxisOverride(plan, placement.Split).Axis == panelayout.Rows {
			dimension = "taller"
		}
		return appmsg.ShowToast(name+" pane needs a "+dimension+" window; layout left unchanged", 3*time.Second)
	}
	m.syncPreviewDeckProjection(ctx)
	var cmds []tea.Cmd
	if wasInteractive {
		cmds = append(cmds, m.exitPreviewInteractive())
	}
	for _, cmd := range adopt {
		cmds = append(cmds, wrapPreviewDeckCmd(cmd, ctx.Surface))
	}
	cmds = append(cmds, wrapPreviewDeckLoad(out, ctx.Surface), m.syncTerminalGeometry())
	return tea.Batch(cmds...)
}

func wrapPreviewDeckLoad(out contentpanes.Outcome, workspaceID string) tea.Cmd {
	return wrapPreviewDeckCmd(out.Command, workspaceID)
}

func wrapPreviewDeckCmd(cmd tea.Cmd, workspaceID string) tea.Cmd {
	if cmd == nil {
		return nil
	}
	return func() tea.Msg {
		msg := cmd()
		result, ok := msg.(contentpanes.Result)
		if !ok {
			return msg
		}
		switch payload := result.Payload.(type) {
		case docview.LoadedMsg:
			return previewDocLoadedMsg{LoadedMsg: payload, WorkspaceID: workspaceID}
		case issueview.LoadedMsg:
			return previewIssueLoadedMsg{LoadedMsg: payload, WorkspaceID: workspaceID}
		case noteview.LoadedMsg:
			return previewNoteLoadedMsg{LoadedMsg: payload, WorkspaceID: workspaceID}
		default:
			return payload
		}
	}
}

func (m *Model) syncPreviewDeckProjection(ctx contentpanes.SurfaceContext) {
	deck := m.preview.deck
	if deck == nil {
		return
	}
	root, focusShell := m.graftPreviewShellLeaves(m.preview.paneRoot, deck.Tree())
	m.preview.paneRoot = root
	m.preview.paneFocus = deck.FocusedLeaf()
	// A projection must not steal the keyboard from a live terminal the user is
	// in — the deck's focus answer only knows its own passive leaves.
	if focusShell != 0 {
		m.preview.paneFocus = focusShell
	}
	m.preview.paneNextID = panelayout.MaxID(m.preview.paneRoot) + 1

	oldDoc := m.preview.doc
	oldIssue := m.preview.issue
	oldNote := m.preview.note
	oldDiff := m.preview.diff
	oldResource := m.preview.resource
	m.preview.doc = nil
	m.preview.issue = nil
	m.preview.note = nil
	m.preview.diff = nil
	m.preview.resource = nil
	for _, kind := range []panelayout.Kind{panelayout.Document, panelayout.Issue, panelayout.Note, panelayout.Diff, panelayout.Resource} {
		leafID := deck.Leaf(kind)
		if leafID == 0 {
			continue
		}
		items, active := deck.Tabs(leafID)
		switch kind {
		case panelayout.Document:
			doc := oldDoc
			if doc == nil || doc.surface != ctx.Surface {
				doc = &previewDoc{}
			}
			doc.root, doc.surface = ctx.Root, ctx.Surface
			doc.tabs = docview.Tabs{Active: active}
			for _, item := range items {
				if view, ok := item.Viewer.(*docview.Model); ok {
					doc.tabs.Items = append(doc.tabs.Items, docview.Item{View: view})
				}
			}
			if len(items) > 0 {
				doc.epoch = items[0].ID
			}
			m.preview.doc = doc
		case panelayout.Issue:
			issue := oldIssue
			if issue == nil || issue.surface != ctx.Surface {
				issue = &previewIssue{}
			}
			issue.root, issue.surface = ctx.Root, ctx.Surface
			issue.tabs = issueview.Tabs{}
			for _, item := range items {
				if view, ok := item.Viewer.(*issueview.Model); ok {
					issue.tabs.Append(item.Ref.Value, view)
				}
			}
			issue.tabs.Active = active
			if len(items) > 0 {
				issue.epoch = items[0].ID
			}
			m.preview.issue = issue
		case panelayout.Note:
			note := oldNote
			if note == nil || note.surface != ctx.Surface {
				note = &previewNote{}
			}
			note.root, note.surface = ctx.Root, ctx.Surface
			note.tabs = noteview.Tabs{}
			for _, item := range items {
				if view, ok := item.Viewer.(*noteview.Model); ok {
					note.tabs.Append(item.Ref.Value, view)
				}
			}
			note.tabs.Active = active
			if len(items) > 0 {
				note.epoch = items[0].ID
			}
			m.preview.note = note
		case panelayout.Diff:
			diff := oldDiff
			if diff == nil || diff.surface != ctx.Surface {
				diff = &previewDiff{}
			}
			diff.root, diff.surface = ctx.Root, ctx.Surface
			diff.tabs = workspacediff.Group{}
			for _, item := range items {
				if view, ok := item.Viewer.(*workspacediff.View); ok {
					diff.tabs.Append(item.Ref.Value, view)
				}
			}
			diff.tabs.Active = active
			m.preview.diff = diff
		case panelayout.Resource:
			res := oldResource
			if res == nil || res.surface != ctx.Surface {
				res = &previewResource{tabs: resourceview.NewTabs(nil, m.previewResourceResolver(ctx.Surface, ctx.Epoch))}
			}
			res.surface, res.epoch = ctx.Surface, ctx.Epoch
			res.tabs.Items = nil
			res.tabs.SetResolver(m.previewResourceResolver(ctx.Surface, ctx.Epoch))
			res.tabs.SetEpoch(ctx.Epoch)
			for _, item := range items {
				if view, ok := item.Viewer.(*resourceview.Model); ok {
					res.tabs.Append(resourceview.TabKey(view.Reference()), view)
				}
			}
			res.tabs.Group.Active = active
			res.pane = resourceview.NewPane(res.tabs, previewResourceHost{m: m, res: res})
			m.preview.resource = res
		}
	}
	focusID := deck.FocusedLeaf()
	if focusShell != 0 {
		focusID = focusShell
	}
	m.focusPreviewPaneByID(focusID)
	m.persistSessionsLayout()
}

func (m *Model) focusPreviewPaneByID(id int) {
	if m.preview.deck != nil {
		m.preview.deck.FocusLeaf(id)
	}
	_, cmd := m.focusPreviewLeaf(id)
	m.queuePreviewCmd(cmd)
}

func (m *Model) applyPreviewDeckResult(result contentpanes.Result) tea.Cmd {
	if m.preview.deck == nil {
		return nil
	}
	cmd, applied := m.preview.deck.Apply(result)
	if applied {
		if ctx, ok := m.previewDeckContext(); ok {
			m.syncPreviewDeckProjection(ctx)
		}
	}
	return cmd
}

func previewDeckTabCount(deck *contentpanes.Deck, kind panelayout.Kind) int {
	if deck == nil {
		return 0
	}
	items, _ := deck.Tabs(deck.Leaf(kind))
	return len(items)
}

func (m *Model) finishPreviewDeckClose() tea.Cmd {
	if ctx, ok := m.previewDeckContext(); ok {
		m.syncPreviewDeckProjection(ctx)
	}
	if m.preview.deck == nil || m.preview.deck.Leaf(panelayout.Document) != 0 ||
		m.preview.deck.Leaf(panelayout.Issue) != 0 || m.preview.deck.Leaf(panelayout.Note) != 0 ||
		m.preview.deck.Leaf(panelayout.Diff) != 0 ||
		m.preview.deck.Leaf(panelayout.Resource) != 0 {
		return m.syncTerminalGeometry()
	}
	return tea.Batch(m.focusList(), m.syncTerminalGeometry())
}
