package overview

import (
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/marcus/sidecar/internal/issueview"
	"github.com/marcus/sidecar/internal/livepanes"
	"github.com/marcus/sidecar/internal/livewatch"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/workspacediff"
)

// Live refresh for the global browser's preview panes.
//
// This is the parity half of the workspace plugin's live_panes.go. The same
// pane kinds — issue card, document, diff — are reachable from the global
// browser as well as from a project, and a refresh that landed on one and not
// the other would be a defect the user experiences as the feature working only
// sometimes. The lifecycle is shared through [livepanes] and the decision logic
// through [livewatch]; what differs here is only the plumbing, because this
// surface tags its results with a workspace ID and delivers them through its own
// async message registry.
//
// One difference from the project surface is inherent rather than chosen.
// Preview panes are memory-only and are torn down whenever the global cursor
// leaves the row, so a pane that exists is almost always a pane on screen; the
// visibility check below only has to answer the case the layout could not place.

// The kinds this surface registers, and the owner that distinguishes its
// messages from the project surface's on the shared bus.
const (
	livePreviewOwner  = "overview"
	livePreviewIssues = "issues"
	livePreviewNotes  = "notes"
	livePreviewDocs   = "docs"
	livePreviewDiffs  = "diffs"

	// remoteDocumentRefreshInterval is the visible-tab conditional read cadence
	// for remote Documents. Hidden tabs are not checked.
	remoteDocumentRefreshInterval = 2 * time.Second
	remoteDocumentStaleNotice     = "stale"
)

type remoteDocumentRefreshTickMsg struct {
	Generation  int
	WorkspaceID string
	HostID      string
	Incarnation uint64
	Epoch       uint64
}

// previewTDStoreResolvedMsg carries the resolved td store location for a
// worktree. Resolving it walks parents and can shell out to git, so it happens
// in a command rather than on the update goroutine.
type previewTDStoreResolvedMsg struct {
	Root    string
	Targets []livewatch.Target
}

// isLiveWatchMessage reports whether msg belongs to the live-refresh loop.
// These are background results: they must land whether or not this browser is
// the visible surface, exactly like the shell probes above them.
func isLiveWatchMessage(msg tea.Msg) bool {
	switch msg.(type) {
	case previewTDStoreResolvedMsg, workspacediff.AdminTargetsMsg, remoteDocumentRefreshTickMsg:
		return true
	}
	return livepanes.Owns(livePreviewOwner, msg)
}

// newLiveSet registers the preview kinds this surface refreshes. Adding a kind
// is one entry here.
func (m *Model) newLiveSet() *livepanes.Set {
	return livepanes.NewSet(livePreviewOwner, nil,
		livepanes.Binding{
			Kind:    livePreviewIssues,
			Config:  livewatch.Config{Quiet: 400 * time.Millisecond, MaxLatency: 2 * time.Second},
			Prepare: m.resolvePreviewTDStore,
			Targets: m.previewIssueTargets,
			Refresh: m.refreshPreviewIssues,
			Owed:    m.previewIssueRefreshOwed,
		},
		livepanes.Binding{
			Kind:    livePreviewNotes,
			Config:  livewatch.Config{Quiet: 400 * time.Millisecond, MaxLatency: 2 * time.Second},
			Prepare: m.resolvePreviewTDStore,
			Targets: m.previewNoteTargets,
			Refresh: m.refreshPreviewNotes,
			Owed:    m.previewNoteRefreshOwed,
		},
		livepanes.Binding{
			Kind:    livePreviewDocs,
			Config:  livewatch.Config{Ignore: isPreviewEditorScratchPath},
			Targets: m.previewDocTargets,
			Refresh: m.refreshPreviewDocs,
			Owed:    m.previewDocRefreshOwed,
		},
		livepanes.Binding{
			Kind:    livePreviewDiffs,
			Config:  livewatch.Config{Quiet: 500 * time.Millisecond, MaxLatency: 3 * time.Second},
			Prepare: m.resolvePreviewDiffAdmin,
			Targets: m.previewDiffTargets,
			Refresh: m.refreshPreviewDiffs,
			Owed:    m.previewDiffRefreshOwed,
		},
	)
}

// handleLiveWatchMsg handles a live-refresh message, reporting whether msg was
// one of them.
func (m *Model) handleLiveWatchMsg(msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case remoteDocumentRefreshTickMsg:
		return m.applyRemoteDocumentRefreshTick(msg), true
	case previewTDStoreResolvedMsg:
		if m.preview.tdStoreTargets == nil {
			m.preview.tdStoreTargets = make(map[string][]livewatch.Target)
		}
		delete(m.preview.tdStoreResolving, msg.Root)
		m.preview.tdStoreTargets[msg.Root] = msg.Targets
		return m.reconcileLiveWatches(), true

	case workspacediff.AdminTargetsMsg:
		if m.preview.diffAdminTargets == nil {
			m.preview.diffAdminTargets = make(map[string][]livewatch.Target)
		}
		delete(m.preview.diffAdminResolving, msg.WorkDir)
		m.preview.diffAdminTargets[msg.WorkDir] = msg.Targets
		return m.reconcileLiveWatches(), true
	}
	return m.preview.live.Handle(msg)
}

// stopLiveWatchers releases every descriptor the preview's live refresh holds.
func (m *Model) stopLiveWatchers() { m.preview.live.Stop() }

// reconcileLiveWatches brings the preview watch sets in line with the panes
// currently on screen.
//
// Called once per update for the same reason the project surface does it: a
// preview pane is opened, retargeted, cached and closed from a dozen places, and
// reconciling in one of them makes "the watch set matches the visible pane set"
// hold by construction rather than by vigilance.
func (m *Model) reconcileLiveWatches() tea.Cmd {
	if m.preview.live == nil {
		m.preview.live = m.newLiveSet()
	}
	return m.preview.live.Reconcile()
}

// previewPaneVisible reports whether the preview's leaf of this kind is one the
// layout actually places.
//
// A preview that exists but could not be placed is not on screen and is not
// worth a registration. There are two such cases and they are easy to conflate:
// the window is too narrow to show a preview at all, so the list takes the full
// width; or the tree does not fit its peer box, so LayoutTree zooms to the
// focused leaf alone and every other leaf is gone.
//
// Only one case is genuinely unknown: before anything has been sized there is no
// peer box to lay out in. An existing preview counts as visible then, because it
// is about to be drawn and the alternative is a pane that never arms its watcher.
func (m *Model) previewPaneVisible(kind panelayout.Kind) bool {
	// Answered without laying anything out, because layoutPreviewPanes builds a
	// default tree when there is none — a reasonable thing for a renderer to do
	// and the wrong thing for a question asked on every update.
	if m.preview.paneRoot == nil {
		return false
	}
	if m.width == 0 || m.height == 0 {
		return true
	}
	peer, drawn := m.previewPeerBox()
	if !drawn {
		// Sized, and the preview is not on screen at this size.
		return false
	}
	layout, ok := m.layoutPreviewPanes(peer)
	if !ok {
		return false
	}
	for _, leaf := range layout.Leaves {
		if leaf.Node != nil && leaf.Node.Kind == kind {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Issue cards (td-312e4e)
// ---------------------------------------------------------------------------

func (m *Model) previewIssueTargets() []livewatch.Target {
	if m.preview.deck != nil && m.preview.deck.Context().Source.Remote() {
		return nil
	}
	issue := m.preview.issue
	if issue == nil || len(issue.tabs.Items) == 0 {
		return nil
	}
	if !m.previewPaneVisible(panelayout.Issue) {
		return nil
	}
	// A cross-project card is watched at its owning store; deduping per
	// directory keeps shared stores to one registration.
	seen := make(map[string]bool)
	var targets []livewatch.Target
	for _, root := range m.previewIssueRoots() {
		for _, t := range m.preview.tdStoreTargets[root] {
			if seen[t.Path] {
				continue
			}
			seen[t.Path] = true
			targets = append(targets, t)
		}
	}
	return targets
}

// previewIssueRoots lists every td store this surface's open issue cards can
// come from: the selected workspace's root plus, for any card resolved in
// another configured project, that card's adopted store. Deduplicated.
func (m *Model) previewIssueRoots() []string {
	issue := m.preview.issue
	if issue == nil {
		return nil
	}
	seen := make(map[string]bool)
	var roots []string
	add := func(root string) {
		if root == "" || seen[root] {
			return
		}
		seen[root] = true
		roots = append(roots, root)
	}
	add(issue.root)
	for _, item := range issue.tabs.Items {
		if item.Value != nil {
			add(item.Value.WorkDir())
		}
	}
	return roots
}

func (m *Model) resolvePreviewTDStore() tea.Cmd {
	if m.preview.deck != nil && m.preview.deck.Context().Source.Remote() {
		return nil
	}
	// Resolving walks parents and can shell out to git, so only roots behind
	// panes actually on screen are queued.
	var roots []string
	if m.preview.issue != nil && len(m.preview.issue.tabs.Items) > 0 && m.previewPaneVisible(panelayout.Issue) {
		roots = append(roots, m.previewIssueRoots()...)
	}
	if note := m.preview.note; note != nil && note.root != "" && len(note.tabs.Items) > 0 && m.previewPaneVisible(panelayout.Note) {
		roots = append(roots, note.root)
	}
	if len(roots) == 0 {
		return nil
	}
	if m.preview.tdStoreResolving == nil {
		m.preview.tdStoreResolving = make(map[string]bool)
	}
	var cmds []tea.Cmd
	for _, root := range roots {
		if _, done := m.preview.tdStoreTargets[root]; done {
			continue
		}
		if m.preview.tdStoreResolving[root] {
			continue
		}
		m.preview.tdStoreResolving[root] = true
		cmds = append(cmds, func() tea.Msg {
			return previewTDStoreResolvedMsg{Root: root, Targets: issueview.StoreTargets(root)}
		})
	}
	return tea.Batch(cmds...)
}

func (m *Model) refreshPreviewIssues() []tea.Cmd {
	issue := m.preview.issue
	if issue == nil {
		return nil
	}
	if m.preview.deck != nil && m.preview.deck.Context().Source.Remote() {
		if !m.previewPaneVisible(panelayout.Issue) {
			return nil
		}
		view := issue.view()
		if view == nil {
			return nil
		}
		view.Observe()
		if cmd := wrapPreviewIssueLoad(view.Refresh(false), issue.surface); cmd != nil {
			return []tea.Cmd{cmd}
		}
		return nil
	}
	var cmds []tea.Cmd
	for _, item := range issue.tabs.Items {
		view := item.Value
		if view == nil {
			continue
		}
		view.Observe()
		// Results must be wrapped with the workspace ID, or applyPreviewIssueLoaded
		// cannot tell which workspace's preview they belong to and drops them.
		if cmd := wrapPreviewIssueLoad(view.Refresh(false), issue.surface); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return cmds
}

// ---------------------------------------------------------------------------
// Document panes (td-03c21c)
// ---------------------------------------------------------------------------

// previewDocTargets is the file behind the visible document pane's active tab.
//
// The active tab only, matching the project surface: a background tab is not on
// screen, and selecting it brings it into the watch set, which is what makes
// [livepanes] re-read it.
func (m *Model) previewDocTargets() []livewatch.Target {
	if m.preview.deck != nil && m.preview.deck.Context().Source.Remote() {
		return nil
	}
	doc := m.preview.doc
	if doc == nil || !m.previewPaneVisible(panelayout.Document) {
		return nil
	}
	view := doc.tabs.ActiveView()
	if view == nil {
		return nil
	}
	// The overview opens documents by file descriptor, so the model never
	// learned where the file lives. Without this it has no path to re-read.
	view.SetRoot(doc.root)
	t := view.WatchTarget()
	if t.Path == "" {
		return nil
	}
	return []livewatch.Target{t}
}

func (m *Model) refreshPreviewDocs() []tea.Cmd {
	doc := m.preview.doc
	if doc == nil || !m.previewPaneVisible(panelayout.Document) {
		return nil
	}
	view := doc.tabs.ActiveView()
	if view == nil {
		return nil
	}
	view.Observe()
	// An editor holding the file is writing it: a re-read would rebuild the
	// pane under a live session.
	if cmd := wrapPreviewDocLoad(view.Refresh(doc.editing()), doc.surface); cmd != nil {
		return []tea.Cmd{cmd}
	}
	return nil
}

func (m *Model) visibleRemoteDocument() bool {
	if m.preview.deck == nil || !m.preview.deck.Context().Source.Remote() {
		return false
	}
	if m.preview.doc == nil || m.preview.doc.view() == nil {
		return false
	}
	if !m.hostShows(m.preview.deck.Context().Source.HostID) {
		return false
	}
	return m.previewPaneVisible(panelayout.Document)
}

func (m *Model) visibleRemoteIssue() bool {
	if m.preview.deck == nil || !m.preview.deck.Context().Source.Remote() {
		return false
	}
	if m.preview.issue == nil || m.preview.issue.view() == nil {
		return false
	}
	if !m.hostShows(m.preview.deck.Context().Source.HostID) {
		return false
	}
	return m.previewPaneVisible(panelayout.Issue)
}

func (m *Model) visibleRemoteNote() bool {
	if m.preview.deck == nil || !m.preview.deck.Context().Source.Remote() {
		return false
	}
	if m.preview.note == nil || m.preview.note.view() == nil {
		return false
	}
	if !m.hostShows(m.preview.deck.Context().Source.HostID) {
		return false
	}
	return m.previewPaneVisible(panelayout.Note)
}

func (m *Model) visibleRemoteContent() bool {
	return m.visibleRemoteDocument() || m.visibleRemoteIssue() || m.visibleRemoteNote()
}

func (m *Model) remoteDocumentRefreshCmd() tea.Cmd {
	if m.preview.remoteDocTick || !m.visibleRemoteContent() {
		return nil
	}
	src := m.preview.deck.Context().Source
	m.preview.remoteDocTick = true
	msg := remoteDocumentRefreshTickMsg{
		Generation:  m.preview.generation,
		WorkspaceID: m.preview.workspaceID,
		HostID:      src.HostID,
		Incarnation: src.HostIncarnation,
		Epoch:       m.preview.contentEpoch,
	}
	return tea.Tick(remoteDocumentRefreshInterval, func(time.Time) tea.Msg { return msg })
}

func (m *Model) applyRemoteDocumentRefreshTick(msg remoteDocumentRefreshTickMsg) tea.Cmd {
	m.preview.remoteDocTick = false
	if msg.Generation != m.preview.generation || msg.WorkspaceID != m.preview.workspaceID {
		return m.remoteDocumentRefreshCmd()
	}
	if m.preview.deck == nil {
		return nil
	}
	src := m.preview.deck.Context().Source
	if src.HostID != msg.HostID || src.HostIncarnation != msg.Incarnation || m.preview.contentEpoch != msg.Epoch {
		return m.remoteDocumentRefreshCmd()
	}
	if !m.visibleRemoteContent() {
		return nil
	}
	var cmds []tea.Cmd
	if m.visibleRemoteDocument() {
		cmds = append(cmds, m.refreshPreviewDocs()...)
	}
	if m.visibleRemoteIssue() {
		cmds = append(cmds, m.refreshPreviewIssues()...)
	}
	if m.visibleRemoteNote() {
		cmds = append(cmds, m.refreshPreviewNotes()...)
	}
	if next := m.remoteDocumentRefreshCmd(); next != nil {
		cmds = append(cmds, next)
	}
	return tea.Batch(cmds...)
}

// isPreviewEditorScratchPath drops the files an editor leaves beside the one
// being read: vim's 4913 probe, swap files, backups, emacs lock files.
func isPreviewEditorScratchPath(path string) bool {
	base := filepath.Base(path)
	if strings.HasPrefix(base, ".#") || strings.HasSuffix(base, "~") || base == "4913" {
		return true
	}
	switch filepath.Ext(base) {
	case ".swp", ".swx", ".swo", ".tmp":
		return true
	}
	return false
}

// ---------------------------------------------------------------------------
// Diff panes (td-e9a275)
// ---------------------------------------------------------------------------

func (m *Model) previewDiffTargets() []livewatch.Target {
	diff := m.preview.diff
	if diff == nil || diff.root == "" || len(diff.tabs.Items) == 0 {
		return nil
	}
	if !m.previewPaneVisible(panelayout.Diff) {
		return nil
	}
	targets := append([]livewatch.Target(nil), m.preview.diffAdminTargets[diff.root]...)
	if view := diff.tabs.ActiveView(); view != nil {
		targets = append(targets, view.WatchTargets(diff.root, nil)...)
	}
	return targets
}

func (m *Model) resolvePreviewDiffAdmin() tea.Cmd {
	diff := m.preview.diff
	if diff == nil || diff.root == "" || len(diff.tabs.Items) == 0 {
		return nil
	}
	// Five `git rev-parse` calls. Not spent on a pane that is not on screen.
	if !m.previewPaneVisible(panelayout.Diff) {
		return nil
	}
	if _, done := m.preview.diffAdminTargets[diff.root]; done {
		return nil
	}
	if m.preview.diffAdminResolving[diff.root] {
		return nil
	}
	if m.preview.diffAdminResolving == nil {
		m.preview.diffAdminResolving = make(map[string]bool)
	}
	m.preview.diffAdminResolving[diff.root] = true
	return workspacediff.ResolveAdminTargets(diff.root, "", 0)
}

func (m *Model) refreshPreviewDiffs() []tea.Cmd {
	diff := m.preview.diff
	if diff == nil {
		return nil
	}
	var cmds []tea.Cmd
	for _, item := range diff.tabs.Items {
		view := item.Value
		if view == nil {
			continue
		}
		view.Observe()
		// Snapshot results are a shared family — a project plugin's pane hosts
		// the same view and waits on the same message — so these go out
		// unwrapped, exactly as this surface's explicit loads do.
		if cmd := view.Refresh(diff.root, "", view.WorkspaceID, false); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return cmds
}

// ---------------------------------------------------------------------------
// Owed refreshes
// ---------------------------------------------------------------------------

// A change that arrived while a refresh was vetoed is remembered by the pane
// and has to be re-driven, or it lands only when some later write happens to
// arrive. This surface passes false for every veto today, so these report only
// the in-flight-collision case — but the contract is the same one the project
// surface relies on, and a veto added here later must not silently lose a
// change.

func (m *Model) previewIssueRefreshOwed() bool {
	issue := m.preview.issue
	if issue == nil {
		return false
	}
	for _, item := range issue.tabs.Items {
		if item.Value != nil && item.Value.RefreshPending() {
			return true
		}
	}
	return false
}

func (m *Model) previewNoteTargets() []livewatch.Target {
	if m.preview.deck != nil && m.preview.deck.Context().Source.Remote() {
		return nil
	}
	note := m.preview.note
	if note == nil || note.root == "" || len(note.tabs.Items) == 0 {
		return nil
	}
	if !m.previewPaneVisible(panelayout.Note) {
		return nil
	}
	return m.preview.tdStoreTargets[note.root]
}

func (m *Model) refreshPreviewNotes() []tea.Cmd {
	note := m.preview.note
	if note == nil {
		return nil
	}
	if m.preview.deck != nil && m.preview.deck.Context().Source.Remote() {
		if !m.previewPaneVisible(panelayout.Note) {
			return nil
		}
		view := note.view()
		if view == nil {
			return nil
		}
		view.Observe()
		if cmd := wrapPreviewNoteLoad(view.Refresh(false), note.surface); cmd != nil {
			return []tea.Cmd{cmd}
		}
		return nil
	}
	var cmds []tea.Cmd
	for _, item := range note.tabs.Items {
		view := item.Value
		if view == nil {
			continue
		}
		view.Observe()
		if cmd := wrapPreviewNoteLoad(view.Refresh(false), note.surface); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return cmds
}

func (m *Model) previewNoteRefreshOwed() bool {
	note := m.preview.note
	if note == nil {
		return false
	}
	for _, item := range note.tabs.Items {
		if item.Value != nil && item.Value.RefreshPending() {
			return true
		}
	}
	return false
}

func (m *Model) previewDocRefreshOwed() bool {
	doc := m.preview.doc
	if doc == nil {
		return false
	}
	view := doc.tabs.ActiveView()
	return view != nil && view.RefreshPending()
}

func (m *Model) previewDiffRefreshOwed() bool {
	diff := m.preview.diff
	if diff == nil {
		return false
	}
	for _, item := range diff.tabs.Items {
		if item.Value != nil && item.Value.RefreshPending() {
			return true
		}
	}
	return false
}
