package overview

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/contentlink"
	"github.com/marcus/sidecar/internal/contentpanes"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/layoutapply"
	"github.com/marcus/sidecar/internal/layoutreport"
	"github.com/marcus/sidecar/internal/panecodec"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/state"
	"github.com/marcus/sidecar/internal/termpanes"
	"github.com/marcus/sidecar/internal/uirequest"
	"github.com/marcus/sidecar/internal/workspacediff"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

// remoteNewShellReason declines creating a tmux session on this machine for a
// remote row. Carry a live terminal by session, or wait for host tmux splits.
const remoteNewShellReason = "a new shell pane on a remote row cannot be created here yet; carry an existing live terminal with its session from layout get"

func (m *Model) applyLayoutRequest(req uirequest.Request) tea.Cmd {
	relayed := req.Origin.HostID != ""
	if !req.Origin.Sessions && !relayed {
		return nil
	}
	payload, err := uirequest.DecodeLayoutPayload(req.Payload)
	if err != nil {
		return m.ackLayout(req, uirequest.StatusDeclined, "invalid layout payload: "+err.Error(), nil, nil)
	}
	if !m.preview.visible {
		return m.ackLayout(req, uirequest.StatusDeclined, layoutapply.SessionsNotOnScreenReason, nil, nil)
	}

	rowID, ws, ok, reason := m.resolveLayoutRequestRow(req)
	if !ok {
		return m.ackLayout(req, uirequest.StatusDeclined, reason, nil, nil)
	}

	if payload.Mode == uirequest.LayoutModeGet {
		report := m.buildSessionsLayoutReport(rowID, ws)
		return m.ackLayout(req, uirequest.StatusOpened, "", nil, report)
	}

	if cmd := m.focusSessionsLayoutRow(rowID); cmd != nil {
		// bindPreview is synchronous; the cmd is terminal attach work.
		_ = cmd
	}
	selected, has := m.SelectedWorkspace()
	if !has || selected.ID != rowID {
		return m.ackLayout(req, uirequest.StatusDeclined, "no Sessions row named "+rowID+" is on screen", nil, nil)
	}
	root := selected.Path
	surface := selected.ID
	return layoutapply.Apply(overviewLayoutHost{m: m, req: req, root: root, surface: surface}, req, payload, root, surface)
}

func (m *Model) resolveLayoutRequestRow(req uirequest.Request) (string, workspaceinventory.Workspace, bool, string) {
	if req.Origin.HostID != "" && !req.Origin.Sessions {
		bound, ok := m.bindOpenWorkspace(req)
		if !ok {
			return "", workspaceinventory.Workspace{}, false, "no Sessions row matches that origin"
		}
		return bound.ID, *bound, true, ""
	}
	return m.resolveSessionsLayoutRow(req)
}

func (m *Model) resolveSessionsLayoutRow(req uirequest.Request) (string, workspaceinventory.Workspace, bool, string) {
	rowID := strings.TrimSpace(req.Origin.SessionsRow)
	if rowID == "" {
		if selected, ok := m.SelectedWorkspace(); ok {
			return selected.ID, selected, true, ""
		}
		if m.preview.workspaceID != "" {
			if ws, ok := m.catalog[m.preview.workspaceID]; ok {
				return ws.ID, ws, true, ""
			}
		}
		return "", workspaceinventory.Workspace{}, false, "no Sessions row is selected"
	}
	if ws, ok := m.catalog[rowID]; ok {
		return rowID, ws, true, ""
	}
	for _, ws := range m.catalog {
		// Remote rows are excluded from the by-name fallback for the same
		// reason as the `sidecar open` path: a display name or session name is
		// unique per machine, and unordered iteration would bind a local
		// request to another machine's row at random. An explicit row ID still
		// resolves above, because that IS host-scoped.
		if ws.Remote() {
			continue
		}
		if ws.Name == rowID || ws.TmuxName == rowID || ws.ID == rowID {
			return ws.ID, ws, true, ""
		}
	}
	return "", workspaceinventory.Workspace{}, false, "unknown Sessions row " + strconvQuote(rowID)
}

func strconvQuote(s string) string { return `"` + s + `"` }

func (m *Model) focusSessionsLayoutRow(rowID string) tea.Cmd {
	if m.preview.workspaceID == rowID {
		return nil
	}
	if !m.workspaces.SelectID(rowID) {
		return nil
	}
	return m.bindPreview(false)
}

func (m *Model) buildSessionsLayoutReport(rowID string, ws workspaceinventory.Workspace) json.RawMessage {
	root, layout := m.sessionsLayoutSource(rowID, ws)
	var viewport *panelayout.Box
	if peer, placed := m.previewPeerBox(); placed {
		box := panelayout.Box(peer)
		viewport = &box
	}
	boxes := map[int]panelayout.Box(nil)
	if peer, placed := m.previewPeerBox(); placed && root != nil {
		boxes = layoutreport.LiveBoxes(root, peer, previewPaneFloors())
	}
	if layout != nil {
		layout.Root = ws.Path
		layout.Surface = rowID
		layout.Open = true
	}
	return layoutreport.Build(layoutreport.Source{
		Surface:  rowID,
		Root:     ws.Path,
		Tree:     root,
		Viewport: viewport,
		Floors:   previewPaneFloors(),
		Layout:   layout,
		Boxes:    boxes,
	})
}

func (m *Model) sessionsLayoutSource(rowID string, ws workspaceinventory.Workspace) (*panelayout.Node, *state.PaneLayoutJSON) {
	if m.preview.workspaceID == rowID && m.preview.paneRoot != nil {
		return m.preview.paneRoot, m.sessionsPaneLayoutJSON()
	}
	if cached, ok := m.preview.paneCache[rowID]; ok && cached.root != nil {
		return cached.root, encodeCachedPreviewLayout(cached)
	}
	if persisted := loadSessionsPaneLayout(rowID); persisted != nil && state.PaneLayoutOpen(persisted) {
		return treeFromPersist(persisted), persisted
	}
	return &panelayout.Node{ID: 1, Kind: panelayout.Primary}, nil
}

func encodeCachedPreviewLayout(cached previewPaneCache) *state.PaneLayoutJSON {
	st := contentpanes.State{Version: 1, Root: cachedNodeState(cached.root, cached.deck)}
	live := []panecodec.Live{{Kind: panecodec.KindTerminal}}
	if shell := panelayout.FirstOfKind(cached.root, panelayout.Shell); shell != nil && cached.terminals != nil {
		rec := panecodec.Live{Kind: panecodec.KindShell}
		if leaf := cached.terminals.Leaf(shell.ID); leaf != nil {
			rec.Session = leaf.Session
			rec.Name = leaf.Name
		}
		live = append(live, rec)
	}
	return panecodec.Encode(st, panecodec.Options{Live: live})
}

func cachedNodeState(node *panelayout.Node, deck *contentpanes.Deck) *contentpanes.NodeState {
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
			A: cachedNodeState(node.Split.A, deck), B: cachedNodeState(node.Split.B, deck),
		}
	}
	switch node.Kind {
	case panelayout.Primary:
		return &contentpanes.NodeState{Kind: "primary"}
	case panelayout.Shell:
		return &contentpanes.NodeState{Kind: "shell"}
	default:
		if deck == nil {
			return &contentpanes.NodeState{Kind: previewContentKindName(node.Kind)}
		}
		pane := findPreviewPaneState(deck.Encode().Root, previewContentKindName(node.Kind))
		return previewContentLeafState(previewContentKindName(node.Kind), pane)
	}
}

func treeFromPersist(layout *state.PaneLayoutJSON) *panelayout.Node {
	next := 0
	return persistNode(layout, &next)
}

func persistNode(n *state.PaneLayoutJSON, next *int) *panelayout.Node {
	if n == nil {
		return nil
	}
	if n.Split != nil {
		a := persistNode(n.Split.A, next)
		b := persistNode(n.Split.B, next)
		if a == nil {
			return b
		}
		if b == nil {
			return a
		}
		*next++
		axis := panelayout.Columns
		if n.Split.Axis == "rows" {
			axis = panelayout.Rows
		}
		ratio := n.Split.Ratio
		if ratio == 0 {
			ratio = 50
		}
		return &panelayout.Node{ID: *next, Split: &panelayout.Split{
			Axis: axis, Ratio: panelayout.ClampRatio(ratio), A: a, B: b,
		}}
	}
	kind, ok := restorePreviewKind(n.Kind)
	if !ok {
		return nil
	}
	*next++
	return &panelayout.Node{ID: *next, Kind: kind}
}

func (m *Model) ackLayout(req uirequest.Request, status uirequest.Status, reason string, items []uirequest.AckItem, layout json.RawMessage) tea.Cmd {
	if req.Origin.HostID == "" {
		layoutapply.WriteAck(config.StateDir(), hostInstanceID(), req, status, reason, items, layout)
		return nil
	}
	m.ackRemote(req, status, reason, "", 0, layout, items)
	return nil
}

type overviewLayoutHost struct {
	m             *Model
	req           uirequest.Request
	root, surface string
}

func (h overviewLayoutHost) PaneRoot() *panelayout.Node { return h.m.preview.paneRoot }
func (h overviewLayoutHost) LastBoxes() map[int]panelayout.Box {
	return h.m.lastPreviewBoxes()
}
func (h overviewLayoutHost) PeerBox() (panelayout.Box, bool) {
	return h.m.previewPeerBox()
}
func (h overviewLayoutHost) Floors() panelayout.Floors { return previewPaneFloors() }
func (h overviewLayoutHost) EnsureDeck() {
	h.m.ensurePreviewDeck()
}
func (h overviewLayoutHost) DeckTree() *panelayout.Node {
	if h.m.preview.deck == nil {
		return nil
	}
	return h.m.preview.deck.Tree()
}
func (h overviewLayoutHost) TerminalEnabled() bool {
	if h.remoteSelected() {
		return false
	}
	return features.IsEnabled(features.WorkspaceTerminalPanel.Name)
}
func (h overviewLayoutHost) TerminalOffReason() string {
	if h.remoteSelected() {
		return remoteNewShellReason
	}
	return features.WorkspaceTerminalPanel.Name + " is off"
}
func (h overviewLayoutHost) ShellCapMessage() string { return termpanes.CapMessage }
func (h overviewLayoutHost) ShellVisible() bool {
	return panelayout.FirstOfKind(h.m.preview.paneRoot, panelayout.Shell) != nil
}
func (h overviewLayoutHost) SplitOrigin() string {
	if selected, ok := h.m.SelectedWorkspace(); ok {
		return selected.TmuxName
	}
	return ""
}
func (h overviewLayoutHost) TermPanelSessionName() string {
	if selected, ok := h.m.SelectedWorkspace(); ok && selected.TmuxName != "" {
		return termpanes.SessionName(selected.TmuxName)
	}
	return ""
}
func (h overviewLayoutHost) LiveShellSessions() map[string]bool {
	out := make(map[string]bool)
	shell := panelayout.FirstOfKind(h.m.preview.paneRoot, panelayout.Shell)
	if shell == nil || h.m.preview.terminalPanes == nil {
		return out
	}
	if leaf := h.m.preview.terminalPanes.Leaf(shell.ID); leaf != nil && leaf.Session != "" {
		out[leaf.Session] = true
	}
	return out
}
func (h overviewLayoutHost) FocusedLeaf() int { return h.m.layoutMoveFocusedLeaf() }
func (h overviewLayoutHost) CommitMove(plan panelayout.MovePlan) (string, tea.Cmd) {
	return h.m.commitLayoutMove(plan)
}
func (h overviewLayoutHost) ResolveTargets(kind panelayout.Kind, spec uirequest.LayoutPane) ([]uirequest.Target, string) {
	if h.remoteSelected() {
		return h.m.resolveRemoteLayoutTargets(kind, spec)
	}
	return layoutapply.ResolveTargets(kind, spec, h.root, h.m.previewResourceMatchers())
}
func (h overviewLayoutHost) CommitPassive(targets []uirequest.Target, plan panelayout.OpenPlan) (string, string, tea.Cmd) {
	if len(targets) == 0 {
		return uirequest.ItemVerdictDeclined, "a pane needs at least one target", nil
	}
	kind, _ := previewKindForTarget(targets[0].Kind)
	retargeted := h.m.willRetargetPreviewPane(kind)
	if plan.Split != 0 || plan.Retarget != 0 {
		h.m.pendingOpenPlan = &plan
	}
	defer func() { h.m.pendingOpenPlan = nil }()
	cmd, opened := h.m.openPreviewTarget(targets[0])
	if !opened {
		reason := "the window is too small to split"
		if cmd == nil {
			return uirequest.ItemVerdictDeclined, reason, nil
		}
		return uirequest.ItemVerdictDeclined, reason, cmd
	}
	for _, extra := range targets[1:] {
		extraCmd, _ := h.m.openPreviewTarget(extra)
		cmd = tea.Batch(cmd, extraCmd)
	}
	if retargeted {
		return uirequest.ItemVerdictRetargeted, "", cmd
	}
	return uirequest.ItemVerdictOpened, "", cmd
}
func (h overviewLayoutHost) CommitShell(spec uirequest.LayoutPane, plan panelayout.OpenPlan) (string, string, tea.Cmd) {
	if h.remoteSelected() {
		return uirequest.ItemVerdictDeclined, remoteNewShellReason, nil
	}
	if !h.TerminalEnabled() {
		return uirequest.ItemVerdictDeclined, h.TerminalOffReason(), nil
	}
	before := panelayout.FirstOfKind(h.m.preview.paneRoot, panelayout.Shell)
	cmd := h.m.openPreviewTerminalSplit(spec.Name, plan)
	after := panelayout.FirstOfKind(h.m.preview.paneRoot, panelayout.Shell)
	if after == nil || after == before {
		reason := "the window is too small to split"
		return uirequest.ItemVerdictDeclined, reason, cmd
	}
	return uirequest.ItemVerdictOpened, "", cmd
}
func (h overviewLayoutHost) RestoreSpec(layout *state.PaneLayoutJSON) tea.Cmd {
	return h.m.restoreSpecPreviewLayout(layout)
}
func (h overviewLayoutHost) AdoptSpecShell(spec uirequest.LayoutPane) (string, string, tea.Cmd) {
	if h.remoteSelected() {
		return uirequest.ItemVerdictDeclined, remoteNewShellReason, nil
	}
	ws, ok := h.m.SelectedWorkspace()
	if !ok {
		return uirequest.ItemVerdictDeclined, layoutapply.SpecOriginRequired, nil
	}
	shell := panelayout.FirstOfKind(h.m.preview.paneRoot, panelayout.Shell)
	if shell == nil {
		return uirequest.ItemVerdictDeclined, layoutapply.SpecOriginRequired, nil
	}
	leaf := h.m.terminalLeaf(shell.ID)
	leaf.Requested = true
	if name := strings.TrimSpace(spec.Name); name != "" {
		leaf.Name = name
	}
	if leaf.Name == "" {
		leaf.Name = "Terminal"
	}
	if leaf.Session == "" {
		leaf.Session = termpanes.SessionName(ws.TmuxName)
	}
	leaf.Target.Source = "shell"
	leaf.Target.SourceID = ws.ID
	h.m.persistSessionsLayout()
	workspaceID, leafID, session, workDir := ws.ID, leaf.ID, leaf.Session, ws.Path
	return uirequest.ItemVerdictOpened, "", func() tea.Msg {
		paneID, err := ensurePreviewTerminalSession(session, workDir)
		return previewTerminalSplitCreatedMsg{WorkspaceID: workspaceID, LeafID: leafID, Session: session, PaneID: paneID, Err: err}
	}
}
func (h overviewLayoutHost) AfterSpecCommit() { h.m.persistSessionsLayout() }
func (h overviewLayoutHost) LandedLeaf(kind panelayout.Kind) int {
	return layoutapply.LandedLeafID(h.m.preview.paneRoot, kind)
}
func (h overviewLayoutHost) Ack(req uirequest.Request, status uirequest.Status, reason string, items []uirequest.AckItem, layout json.RawMessage) {
	h.m.ackLayout(req, status, reason, items, layout)
}

func (h overviewLayoutHost) remoteSelected() bool {
	ws, ok := h.m.SelectedWorkspace()
	return ok && ws.Remote()
}

// resolveRemoteLayoutTargets classifies a descriptor against the host Source
// rather than this machine's filesystem. A failed resolve is a refusal, not a
// silent skip: apply is all-or-nothing.
func (m *Model) resolveRemoteLayoutTargets(kind panelayout.Kind, spec uirequest.LayoutPane) ([]uirequest.Target, string) {
	if kind == panelayout.Resource {
		return layoutapply.ResolveTargets(kind, spec, "", m.previewResourceMatchers())
	}
	if len(spec.Targets) == 0 {
		if kind == panelayout.Diff {
			spec.Targets = []string{workspacediff.IdentityWorkingTree}
		} else {
			return nil, "a " + kind.Name() + " pane needs at least one target"
		}
	}
	want, ok := layoutapply.WireKind(kind)
	if !ok {
		return nil, "unsupported pane kind " + kind.Name()
	}
	pendingKind, ok := remoteLayoutPendingKind(kind)
	if !ok {
		return nil, "unsupported pane kind " + kind.Name()
	}
	ctx, ok := m.previewDeckContext()
	if !ok {
		return nil, "that host is not available for content"
	}
	src := m.previewDeckConfig(ctx).Source
	targets := make([]uirequest.Target, 0, len(spec.Targets))
	for _, raw := range spec.Targets {
		raw = strings.TrimSpace(raw)
		line := 0
		if kind == panelayout.Document {
			raw, line = splitLayoutFileLine(raw)
		}
		if kind == panelayout.Note {
			raw = noteLayoutTarget(raw)
		}
		if kind == panelayout.Diff && raw == "" {
			raw = workspacediff.IdentityWorkingTree
		}
		ref, err := contentpanes.ResolveDocument(src, ctx.Source, contentlink.Pending{Kind: pendingKind, Raw: raw})
		if err != nil {
			return nil, fmt.Sprintf("target %q: %v", raw, err)
		}
		if ref.Value == "" {
			host := ctx.Source.HostID
			if host == "" {
				host = "that host"
			}
			return nil, fmt.Sprintf("target %q: not found on %s", raw, host)
		}
		tgt := targetFromResolvedRef(ref, line)
		if tgt.Kind != want {
			got := string(tgt.Kind)
			if mapped, ok := panelayout.KindByName(got); ok {
				got = mapped.Name()
			}
			return nil, fmt.Sprintf("target %q resolves to a %s pane, want %s", raw, got, kind.Name())
		}
		targets = append(targets, tgt)
	}
	return targets, ""
}

func remoteLayoutPendingKind(kind panelayout.Kind) (contentlink.Kind, bool) {
	switch kind {
	case panelayout.Document:
		return contentlink.KindFile, true
	case panelayout.Issue:
		return contentlink.KindIssue, true
	case panelayout.Note:
		return contentlink.KindInternal, true
	case panelayout.Diff:
		return contentlink.KindDiff, true
	default:
		return "", false
	}
}

func targetFromResolvedRef(ref contentlink.Ref, line int) uirequest.Target {
	switch ref.Kind {
	case contentlink.KindFile:
		return uirequest.Target{Kind: uirequest.TargetKindFile, Value: ref.Value, Line: line}
	case contentlink.KindIssue:
		return uirequest.Target{Kind: uirequest.TargetKindIssue, Value: ref.Value}
	case contentlink.KindInternal:
		if ref.Namespace == "note" {
			return uirequest.Target{Kind: uirequest.TargetKindNote, Value: ref.Value}
		}
	case contentlink.KindDiff:
		return uirequest.Target{Kind: uirequest.TargetKindDiff, Value: ref.Value}
	case contentlink.KindResource:
		return uirequest.Target{Kind: uirequest.TargetKindResource, Value: ref.Value, Provider: ref.Provider, Matcher: ref.Matcher}
	}
	return uirequest.Target{}
}

func splitLayoutFileLine(raw string) (string, int) {
	if colonIdx := strings.LastIndex(raw, ":"); colonIdx > 0 && colonIdx < len(raw)-1 {
		if n, err := strconv.Atoi(raw[colonIdx+1:]); err == nil && n > 0 {
			return raw[:colonIdx], n
		}
	}
	return raw, 0
}

func noteLayoutTarget(raw string) string {
	if parsed, err := contentlink.ParseInternalURI(raw); err == nil && parsed.Ref.Namespace == "note" && parsed.Ref.Value != "" {
		return parsed.Ref.Value
	}
	return raw
}

func (m *Model) restoreSpecPreviewLayout(layout *state.PaneLayoutJSON) tea.Cmd {
	ws, ok := m.SelectedWorkspace()
	if !ok {
		return nil
	}
	if ws.Remote() && !m.hostShows(ws.HostID) {
		return nil
	}
	m.preview.contentEpoch++
	ctx := m.previewSurfaceContext(ws)
	st, live := panecodec.Decode(layout, panecodec.Options{AcceptTab: m.acceptRestoredPreviewTab(ws)})
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
	m.attachSpecPreviewShell(ws, live)
	m.syncPreviewDeckProjection(ctx)

	var cmds []tea.Cmd
	for _, cmd := range deck.LoadVisible() {
		if wrapped := wrapPreviewDeckCmd(cmd, ws.ID); wrapped != nil {
			cmds = append(cmds, wrapped)
		}
	}
	return tea.Batch(cmds...)
}

func (m *Model) attachSpecPreviewShell(ws workspaceinventory.Workspace, live []panecodec.Live) {
	shell := panelayout.FirstOfKind(m.preview.paneRoot, panelayout.Shell)
	if shell == nil {
		return
	}
	sh := previewLiveOfKind(live, panecodec.KindShell)
	leaf := m.terminalLeaf(shell.ID)
	leaf.Requested = true
	leaf.Target.Source = "shell"
	leaf.Target.SourceID = ws.ID
	if sh != nil {
		leaf.Name = sh.Name
		if strings.HasPrefix(strings.TrimSpace(sh.Session), termpanes.SessionPrefix) {
			leaf.Session = sh.Session
		}
	}
	if leaf.Name == "" {
		leaf.Name = "Terminal"
	}
}
