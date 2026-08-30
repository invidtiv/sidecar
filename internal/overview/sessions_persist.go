package overview

import (
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/contentpanes"
	"github.com/marcus/sidecar/internal/panecodec"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/state"
	"github.com/marcus/sidecar/internal/terminallink"
	"github.com/marcus/sidecar/internal/termpanes"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

// Arrowing the Sessions sidebar would otherwise write once per row. A short
// debounce collapses a flick into one save of the landing ID.
var sessionsSelectedDebounce = 300 * time.Millisecond

type sessionsSelectedTickMsg struct {
	generation int
	id         string
}

func (m *Model) armSessionsSelected(id string) tea.Cmd {
	if id == "" || m.pendingRestoreSelected != "" {
		return nil
	}
	m.sessionsSelectedPending = id
	m.sessionsSelectedGen++
	return m.sessionsSelectedCmd()
}

func (m *Model) sessionsSelectedCmd() tea.Cmd {
	if m.sessionsSelectedPending == "" {
		return nil
	}
	gen := m.sessionsSelectedGen
	id := m.sessionsSelectedPending
	if sessionsSelectedDebounce <= 0 {
		return func() tea.Msg {
			return sessionsSelectedTickMsg{generation: gen, id: id}
		}
	}
	return tea.Tick(sessionsSelectedDebounce, func(time.Time) tea.Msg {
		return sessionsSelectedTickMsg{generation: gen, id: id}
	})
}

func (m *Model) applySessionsSelectedTick(msg sessionsSelectedTickMsg) {
	if msg.generation != m.sessionsSelectedGen {
		return
	}
	_ = saveSessionsSelected(msg.id)
}

// applyPendingSessionsSelection selects the persisted row when the catalog
// delivers it. A row that never returns is not an error: drop the pending
// restore and leave the list on whatever it already chose.
func (m *Model) applyPendingSessionsSelection() {
	if m.pendingRestoreSelected == "" {
		return
	}
	if m.workspaces.SelectID(m.pendingRestoreSelected) {
		return
	}
	if m.loading {
		return
	}
	m.pendingRestoreSelected = ""
	if m.preview.visible && m.preview.workspaceID != "" {
		_ = saveSessionsSelected(m.preview.workspaceID)
	}
}

func previewTreeComposed(root *panelayout.Node) bool {
	if root == nil {
		return false
	}
	if root.Split != nil {
		return true
	}
	return root.Kind != panelayout.Primary
}

func (m *Model) persistSessionsLayout() {
	id := m.preview.workspaceID
	if id == "" {
		return
	}
	if !previewTreeComposed(m.preview.paneRoot) {
		_ = saveSessionsPaneLayout(id, nil)
		return
	}
	layout := m.sessionsPaneLayoutJSON()
	if layout == nil {
		_ = saveSessionsPaneLayout(id, nil)
		return
	}
	if ws, ok := m.catalog[id]; ok {
		if ws.Remote() {
			// Never persist another machine's path into this machine's state
			// tree: a later restore would resolve it locally.
			return
		}
		layout.Root = ws.Path
	}
	layout.Surface = id
	layout.Open = true
	_ = saveSessionsPaneLayout(id, layout)
}

func (m *Model) sessionsPaneLayoutJSON() *state.PaneLayoutJSON {
	if m.preview.paneRoot == nil {
		return nil
	}
	st := contentpanes.State{
		Version:   1,
		Root:      m.previewNodeState(m.preview.paneRoot),
		FocusKind: previewFocusKind(m.preview.paneRoot, m.preview.paneFocus),
	}
	return panecodec.Encode(st, panecodec.Options{Live: m.previewLiveRecords(m.preview.paneRoot)})
}

func (m *Model) previewLiveRecords(node *panelayout.Node) []panecodec.Live {
	live := []panecodec.Live{{Kind: panecodec.KindTerminal}}
	shell := panelayout.FirstOfKind(node, panelayout.Shell)
	if shell == nil || m.preview.terminalPanes == nil {
		return live
	}
	leaf := m.preview.terminalPanes.Leaf(shell.ID)
	rec := panecodec.Live{Kind: panecodec.KindShell}
	if leaf != nil {
		rec.Session = leaf.Session
		rec.Name = leaf.Name
	}
	return append(live, rec)
}

func (m *Model) previewNodeState(node *panelayout.Node) *contentpanes.NodeState {
	if node == nil {
		return nil
	}
	if node.Split != nil {
		axis := "columns"
		if node.Split.Axis == panelayout.Rows {
			axis = "rows"
		}
		return &contentpanes.NodeState{
			Axis: axis, Ratio: node.Split.Ratio,
			A: m.previewNodeState(node.Split.A), B: m.previewNodeState(node.Split.B),
		}
	}
	switch node.Kind {
	case panelayout.Primary:
		return &contentpanes.NodeState{Kind: "primary"}
	case panelayout.Shell:
		return &contentpanes.NodeState{Kind: "shell"}
	case panelayout.Document:
		return previewContentLeafState("document", m.previewPaneState(panelayout.Document))
	case panelayout.Issue:
		return previewContentLeafState("issue", m.previewPaneState(panelayout.Issue))
	case panelayout.Note:
		return previewContentLeafState("note", m.previewPaneState(panelayout.Note))
	case panelayout.Diff:
		return previewContentLeafState("diff", m.previewPaneState(panelayout.Diff))
	case panelayout.Resource:
		return previewContentLeafState("resource", m.previewPaneState(panelayout.Resource))
	default:
		return nil
	}
}

func previewContentLeafState(kind string, pane *contentpanes.PaneState) *contentpanes.NodeState {
	if pane == nil || len(pane.Tabs) == 0 {
		return nil
	}
	return &contentpanes.NodeState{Kind: kind, Pane: pane}
}

func (m *Model) previewPaneState(kind panelayout.Kind) *contentpanes.PaneState {
	if m.preview.deck == nil {
		return nil
	}
	return findPreviewPaneState(m.preview.deck.Encode().Root, previewContentKindName(kind))
}

func findPreviewPaneState(n *contentpanes.NodeState, kind string) *contentpanes.PaneState {
	if n == nil {
		return nil
	}
	if n.Kind == kind && n.Pane != nil {
		return n.Pane
	}
	if pane := findPreviewPaneState(n.A, kind); pane != nil {
		return pane
	}
	return findPreviewPaneState(n.B, kind)
}

func previewContentKindName(kind panelayout.Kind) string {
	switch kind {
	case panelayout.Primary:
		return "primary"
	case panelayout.Document:
		return "document"
	case panelayout.Issue:
		return "issue"
	case panelayout.Note:
		return "note"
	case panelayout.Diff:
		return "diff"
	case panelayout.Resource:
		return "resource"
	case panelayout.Shell:
		return "shell"
	default:
		return ""
	}
}

func previewFocusKind(root *panelayout.Node, focus int) string {
	leaf := panelayout.Find(root, focus)
	if leaf == nil || leaf.Split != nil {
		return "primary"
	}
	name := previewContentKindName(leaf.Kind)
	if name == "" {
		return "primary"
	}
	return name
}

func parsePreviewFocusKind(raw string) (panelayout.Kind, bool) {
	switch raw {
	case "primary", panecodec.KindTerminal:
		return panelayout.Primary, true
	case "document", panecodec.KindDoc:
		return panelayout.Document, true
	case panecodec.KindIssue:
		return panelayout.Issue, true
	case panecodec.KindNote:
		return panelayout.Note, true
	case panecodec.KindDiff:
		return panelayout.Diff, true
	case panecodec.KindResource:
		return panelayout.Resource, true
	case panecodec.KindShell:
		return panelayout.Shell, true
	default:
		return 0, false
	}
}

func (m *Model) restorePreviewPanes(workspaceID string) tea.Cmd {
	if cached, ok := m.preview.paneCache[workspaceID]; ok && cached.root != nil {
		m.preview.paneRoot, m.preview.paneFocus, m.preview.paneNextID = cached.root, cached.focus, cached.nextID
		m.preview.doc, m.preview.issue, m.preview.note, m.preview.diff = cached.doc, cached.issue, cached.note, cached.diff
		m.preview.resource = cached.resource
		m.preview.deck = cached.deck
		if cached.terminals != nil {
			cached.terminals.Range(func(_ int, leaf *termpanes.Leaf) bool {
				if leaf.Target.Source == "shell" {
					m.preview.terminalPanes.Attach(leaf)
				}
				return true
			})
		}
		m.preview.paneDragSplitID = 0
		return nil
	}
	if layout := loadSessionsPaneLayout(workspaceID); layout != nil && state.PaneLayoutOpen(layout) {
		return m.warmPreviewFromLayout(workspaceID, layout)
	}
	m.resetActivePreviewPanes()
	return nil
}

func (m *Model) warmPreviewFromLayout(workspaceID string, layout *state.PaneLayoutJSON) tea.Cmd {
	ws, ok := m.catalog[workspaceID]
	if !ok || ws.Remote() {
		m.resetActivePreviewPanes()
		return nil
	}
	m.preview.contentEpoch++
	ctx := contentpanes.SurfaceContext{
		Root: ws.Path, DiffRoot: previewDiffPath(ws), Surface: ws.ID, Epoch: m.preview.contentEpoch,
	}
	st, live := panecodec.Decode(layout, panecodec.Options{AcceptTab: m.acceptRestoredPreviewTab(ws.Path)})
	if previewLiveKindCount(live, panecodec.KindTerminal) != 1 {
		m.resetActivePreviewPanes()
		return nil
	}
	deck := contentpanes.Decode(ctx, m.previewDeckConfig(ctx), st)
	nextID := 0
	restored := restorePreviewTree(st.Root, deck, &nextID, make(map[panelayout.Kind]bool))
	if restored == nil || panelayout.FirstOfKind(restored, panelayout.Primary) == nil {
		m.resetActivePreviewPanes()
		return nil
	}
	m.preview.deck = deck
	m.preview.paneRoot = restored
	m.preview.paneNextID = panelayout.MaxID(restored) + 1
	m.preview.paneDragSplitID = 0
	m.attachRestoredPreviewShell(ws, live)
	if kind, ok := parsePreviewFocusKind(st.FocusKind); ok && kind == panelayout.Shell {
		if shell := panelayout.FirstOfKind(m.preview.paneRoot, panelayout.Shell); shell != nil {
			m.preview.paneFocus = shell.ID
		}
	}
	m.syncPreviewDeckProjection(ctx)
	m.applyRestoredPreviewFocus(st.FocusKind)

	var cmds []tea.Cmd
	for _, cmd := range deck.LoadVisible() {
		if wrapped := wrapPreviewDeckCmd(cmd, ws.ID); wrapped != nil {
			cmds = append(cmds, wrapped)
		}
	}
	if ensure := m.ensureRestoredPreviewShell(ws); ensure != nil {
		cmds = append(cmds, ensure)
	}
	return tea.Batch(cmds...)
}

func (m *Model) attachRestoredPreviewShell(ws workspaceinventory.Workspace, live []panecodec.Live) {
	shell := panelayout.FirstOfKind(m.preview.paneRoot, panelayout.Shell)
	sh := previewLiveOfKind(live, panecodec.KindShell)
	if shell == nil {
		return
	}
	if sh == nil || !strings.HasPrefix(strings.TrimSpace(sh.Session), termpanes.SessionPrefix) {
		m.preview.paneRoot, m.preview.paneFocus = panelayout.Close(m.preview.paneRoot, shell.ID)
		m.preview.paneNextID = panelayout.MaxID(m.preview.paneRoot) + 1
		return
	}
	leaf := m.terminalLeaf(shell.ID)
	leaf.Requested = true
	leaf.Name = sh.Name
	if leaf.Name == "" {
		leaf.Name = "Terminal"
	}
	leaf.Session = sh.Session
	leaf.Target.Source = "shell"
	leaf.Target.SourceID = ws.ID
}

func (m *Model) ensureRestoredPreviewShell(ws workspaceinventory.Workspace) tea.Cmd {
	shell := panelayout.FirstOfKind(m.preview.paneRoot, panelayout.Shell)
	if shell == nil {
		return nil
	}
	leaf := m.preview.terminalPanes.Leaf(shell.ID)
	if leaf == nil || leaf.Session == "" || leaf.Target.Source != "shell" {
		return nil
	}
	workspaceID, leafID, session, workDir := ws.ID, leaf.ID, leaf.Session, ws.Path
	return func() tea.Msg {
		paneID, err := ensurePreviewTerminalSession(session, workDir)
		return previewTerminalSplitCreatedMsg{WorkspaceID: workspaceID, LeafID: leafID, Session: session, PaneID: paneID, Err: err}
	}
}

func (m *Model) applyRestoredPreviewFocus(kind string) {
	want, ok := parsePreviewFocusKind(kind)
	leaf := panelayout.FirstOfKind(m.preview.paneRoot, panelayout.Primary)
	if ok {
		if focused := panelayout.FirstOfKind(m.preview.paneRoot, want); focused != nil {
			leaf = focused
		}
	}
	if leaf == nil {
		return
	}
	m.preview.paneFocus = leaf.ID
	if m.preview.deck != nil {
		m.preview.deck.FocusLeaf(leaf.ID)
	}
}

func (m *Model) acceptRestoredPreviewTab(root string) func(string, contentpanes.TabState) bool {
	return func(kind string, tab contentpanes.TabState) bool {
		if kind != panecodec.KindDoc {
			return true
		}
		display, _, ok := terminallink.ResolveFile(root, tab.Ref.Value)
		return ok && display != "" && !filepath.IsAbs(display)
	}
}

func restorePreviewTree(n *contentpanes.NodeState, deck *contentpanes.Deck, nextID *int, seen map[panelayout.Kind]bool) *panelayout.Node {
	if n == nil {
		return nil
	}
	if n.A != nil || n.B != nil {
		a := restorePreviewTree(n.A, deck, nextID, seen)
		b := restorePreviewTree(n.B, deck, nextID, seen)
		if a == nil {
			return b
		}
		if b == nil {
			return a
		}
		*nextID++
		axis := panelayout.Columns
		if n.Axis == "rows" {
			axis = panelayout.Rows
		}
		ratio := n.Ratio
		if ratio == 0 {
			ratio = 50
		}
		return &panelayout.Node{ID: *nextID, Split: &panelayout.Split{
			Axis: axis, Ratio: panelayout.ClampRatio(ratio), A: a, B: b,
		}}
	}
	kind, ok := restorePreviewKind(n.Kind)
	if !ok || seen[kind] {
		return nil
	}
	if kind != panelayout.Primary && kind != panelayout.Shell {
		if deck == nil || deck.Leaf(kind) == 0 {
			return nil
		}
	}
	seen[kind] = true
	*nextID++
	return &panelayout.Node{ID: *nextID, Kind: kind, ContentID: *nextID}
}

func restorePreviewKind(kind string) (panelayout.Kind, bool) {
	switch kind {
	case "primary", panecodec.KindTerminal:
		return panelayout.Primary, true
	case "document", panecodec.KindDoc:
		return panelayout.Document, true
	case panecodec.KindIssue:
		return panelayout.Issue, true
	case panecodec.KindNote:
		return panelayout.Note, true
	case panecodec.KindDiff:
		return panelayout.Diff, true
	case panecodec.KindResource:
		return panelayout.Resource, true
	case panecodec.KindShell:
		return panelayout.Shell, true
	default:
		return 0, false
	}
}

func previewLiveKindCount(live []panecodec.Live, kind string) int {
	n := 0
	for _, l := range live {
		if l.Kind == kind {
			n++
		}
	}
	return n
}

func previewLiveOfKind(live []panecodec.Live, kind string) *panecodec.Live {
	for i := range live {
		if live[i].Kind == kind {
			return &live[i]
		}
	}
	return nil
}

// forgetSessionsRow drops everything this surface remembers about a workspace
// that has just gone away: its cached pane, its persisted pane layout, and any
// selection still pointing at it.
//
// A local deletion's row disappears because the mutation is followed by a
// re-inventory of the project it belonged to. A remote one has no such refresh
// — a local inventory would answer a question about another machine — so the
// row is dropped from that host's last-known results here instead. See
// dropRemoteWorkspaceRow: it is a latency mask, and the host's next snapshot
// restates the project either way.
func (m *Model) forgetSessionsRow(id string) {
	if id == "" {
		return
	}
	m.dropRemoteWorkspaceRow(id)
	if m.preview.paneCache != nil {
		delete(m.preview.paneCache, id)
	}
	_ = saveSessionsPaneLayout(id, nil)
	if loadSessionsSelected() == id {
		_ = saveSessionsSelected("")
	}
	if m.pendingRestoreSelected == id {
		m.pendingRestoreSelected = ""
	}
	if m.sessionsSelectedPending == id {
		m.sessionsSelectedPending = ""
	}
}
