package overview

import (
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/marcus/sidecar/internal/issueview"
	"github.com/marcus/sidecar/internal/livewatch"
	"github.com/marcus/sidecar/internal/workspacediff"
)

// Live refresh for the global browser's preview panes.
//
// This is the parity half of the workspace plugin's live_panes.go. The same
// three panes — issue card, document, diff — are reachable from the global
// browser as well as from a project, and a refresh that landed on one and not
// the other would be a defect the user experiences as the feature working
// only sometimes. The decision logic is shared through internal/livewatch; only
// the plumbing differs, because this surface tags its results with a workspace
// ID and delivers them through its own async message registry.
//
// One difference from the project surface is deliberate. Preview panes are
// memory-only and are torn down whenever the global cursor leaves the row, so
// the watchers are stopped from the same paths that close a preview rather than
// living for the session.

type (
	previewIssueWatchStartedMsg struct{ Watcher *livewatch.PathWatcher }
	previewIssueStoreChangedMsg struct{}

	previewDocWatchStartedMsg struct{ Watcher *livewatch.PathWatcher }
	previewDocFileChangedMsg  struct{}

	previewDiffWatchStartedMsg struct{ Watcher *livewatch.PathWatcher }
	previewDiffRepoChangedMsg  struct{}

	previewTDStoreResolvedMsg struct {
		Root    string
		Targets []livewatch.Target
	}
)

// isLiveWatchMessage reports whether msg belongs to the live-refresh loop.
// These are background results: they must land whether or not this browser is
// the visible surface, exactly like the shell probes above them.
func isLiveWatchMessage(msg tea.Msg) bool {
	switch msg.(type) {
	case previewIssueWatchStartedMsg, previewIssueStoreChangedMsg,
		previewDocWatchStartedMsg, previewDocFileChangedMsg,
		previewDiffWatchStartedMsg, previewDiffRepoChangedMsg,
		previewTDStoreResolvedMsg, workspacediff.AdminTargetsMsg:
		return true
	default:
		return false
	}
}

// handleLiveWatchMsg handles a live-refresh message, reporting whether msg was
// one of them.
func (m *Model) handleLiveWatchMsg(msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case previewTDStoreResolvedMsg:
		if m.preview.tdStoreTargets == nil {
			m.preview.tdStoreTargets = make(map[string][]livewatch.Target)
		}
		delete(m.preview.tdStoreResolving, msg.Root)
		m.preview.tdStoreTargets[msg.Root] = msg.Targets
		return m.reconcileLiveWatches(), true

	case previewIssueWatchStartedMsg:
		m.preview.issueWatchStarting = false
		if !adoptPreviewWatcher(&m.preview.issueWatcher, msg.Watcher, m.preview.issue != nil) {
			return nil, true
		}
		m.preview.issueWatcher.Watch(m.previewIssueTargets()...)
		return livewatch.Listen(m.preview.issueWatcher, previewIssueStoreChangedMsg{}), true

	case previewIssueStoreChangedMsg:
		return m.refreshPreviewIssues(), true

	case previewDocWatchStartedMsg:
		m.preview.docWatchStarting = false
		if !adoptPreviewWatcher(&m.preview.docWatcher, msg.Watcher, m.preview.doc != nil) {
			return nil, true
		}
		m.preview.docWatcher.Watch(m.previewDocTargets()...)
		return livewatch.Listen(m.preview.docWatcher, previewDocFileChangedMsg{}), true

	case previewDocFileChangedMsg:
		return m.refreshPreviewDocs(), true

	case previewDiffWatchStartedMsg:
		m.preview.diffWatchStarting = false
		if !adoptPreviewWatcher(&m.preview.diffWatcher, msg.Watcher, m.preview.diff != nil) {
			return nil, true
		}
		m.preview.diffWatcher.Watch(m.previewDiffTargets()...)
		return livewatch.Listen(m.preview.diffWatcher, previewDiffRepoChangedMsg{}), true

	case previewDiffRepoChangedMsg:
		return m.refreshPreviewDiffs(), true

	case workspacediff.AdminTargetsMsg:
		if m.preview.diffAdminTargets == nil {
			m.preview.diffAdminTargets = make(map[string][]livewatch.Target)
		}
		delete(m.preview.diffAdminResolving, msg.WorkDir)
		m.preview.diffAdminTargets[msg.WorkDir] = msg.Targets
		return m.reconcileLiveWatches(), true
	}
	return nil, false
}

func adoptPreviewWatcher(slot **livewatch.PathWatcher, incoming *livewatch.PathWatcher, wanted bool) bool {
	if incoming == nil {
		return false
	}
	if !wanted {
		go incoming.Stop()
		return false
	}
	if *slot != nil && *slot != incoming {
		old := *slot
		go old.Stop()
	}
	*slot = incoming
	return true
}

// stopLiveWatchers releases every descriptor the preview's live refresh holds.
// Stop blocks on the watcher goroutine, so it runs detached: this is called
// from Model.Stop, which must not stall the surface being torn down.
func (m *Model) stopLiveWatchers() {
	for _, slot := range []**livewatch.PathWatcher{
		&m.preview.issueWatcher, &m.preview.docWatcher, &m.preview.diffWatcher,
	} {
		if *slot == nil {
			continue
		}
		w := *slot
		*slot = nil
		go w.Stop()
	}
}

// reconcileLiveWatches brings the three preview watch sets in line with the
// panes currently open, starting and releasing watchers as previews come and
// go.
//
// Called once per update for the same reason the project surface does it: a
// preview pane is opened, retargeted, cached and closed from a dozen places,
// and reconciling in one of them makes "the watch set matches the pane set"
// hold by construction rather than by vigilance.
func (m *Model) reconcileLiveWatches() tea.Cmd {
	return tea.Batch(
		m.syncPreviewIssueWatch(),
		m.syncPreviewDocWatch(),
		m.syncPreviewDiffWatch(),
	)
}

// ---------------------------------------------------------------------------
// Issue cards (td-312e4e)
// ---------------------------------------------------------------------------

func (m *Model) previewIssueTargets() []livewatch.Target {
	issue := m.preview.issue
	if issue == nil || issue.root == "" || len(issue.tabs.Items) == 0 {
		return nil
	}
	return m.preview.tdStoreTargets[issue.root]
}

func (m *Model) syncPreviewIssueWatch() tea.Cmd {
	issue := m.preview.issue
	if issue == nil || issue.root == "" || len(issue.tabs.Items) == 0 {
		if m.preview.issueWatcher != nil {
			m.preview.issueWatcher.Watch()
		}
		return nil
	}

	var cmds []tea.Cmd
	if _, done := m.preview.tdStoreTargets[issue.root]; !done && !m.preview.tdStoreResolving[issue.root] {
		if m.preview.tdStoreResolving == nil {
			m.preview.tdStoreResolving = make(map[string]bool)
		}
		m.preview.tdStoreResolving[issue.root] = true
		root := issue.root
		// Resolving the td root walks parents and can shell out to git, so it
		// happens in a command rather than on this goroutine.
		cmds = append(cmds, func() tea.Msg {
			return previewTDStoreResolvedMsg{Root: root, Targets: issueview.StoreTargets(root)}
		})
	}

	targets := m.previewIssueTargets()
	switch {
	case m.preview.issueWatcher != nil:
		m.preview.issueWatcher.Watch(targets...)
	case len(targets) > 0 && !m.preview.issueWatchStarting:
		m.preview.issueWatchStarting = true
		cmds = append(cmds, livewatch.Start(livewatch.Config{
			Quiet:      400 * time.Millisecond,
			MaxLatency: 2 * time.Second,
		}, targets, func(w *livewatch.PathWatcher, err error) tea.Msg {
			if err != nil {
				return previewIssueWatchStartedMsg{}
			}
			return previewIssueWatchStartedMsg{Watcher: w}
		}))
	}
	return tea.Batch(cmds...)
}

func (m *Model) refreshPreviewIssues() tea.Cmd {
	cmds := []tea.Cmd{livewatch.Listen(m.preview.issueWatcher, previewIssueStoreChangedMsg{})}
	issue := m.preview.issue
	if issue == nil {
		return tea.Batch(cmds...)
	}
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
	return tea.Batch(cmds...)
}

// ---------------------------------------------------------------------------
// Document panes (td-03c21c)
// ---------------------------------------------------------------------------

func (m *Model) previewDocTargets() []livewatch.Target {
	doc := m.preview.doc
	if doc == nil {
		return nil
	}
	var targets []livewatch.Target
	seen := make(map[string]bool)
	for _, item := range doc.tabs.Items {
		view := item.View
		if view == nil {
			continue
		}
		// The overview opens documents by file descriptor, so the model never
		// learned where the file lives. Without this it has no path to re-read.
		view.SetRoot(doc.root)
		t := view.WatchTarget()
		if t.Path == "" || seen[t.Path] {
			continue
		}
		seen[t.Path] = true
		targets = append(targets, t)
	}
	return targets
}

func (m *Model) syncPreviewDocWatch() tea.Cmd {
	targets := m.previewDocTargets()
	if len(targets) == 0 {
		if m.preview.docWatcher != nil {
			m.preview.docWatcher.Watch()
		}
		return nil
	}
	if m.preview.docWatcher != nil {
		m.preview.docWatcher.Watch(targets...)
		return nil
	}
	if m.preview.docWatchStarting {
		return nil
	}
	m.preview.docWatchStarting = true
	return livewatch.Start(livewatch.Config{
		Ignore: isPreviewEditorScratchPath,
	}, targets, func(w *livewatch.PathWatcher, err error) tea.Msg {
		if err != nil {
			return previewDocWatchStartedMsg{}
		}
		return previewDocWatchStartedMsg{Watcher: w}
	})
}

func (m *Model) refreshPreviewDocs() tea.Cmd {
	cmds := []tea.Cmd{livewatch.Listen(m.preview.docWatcher, previewDocFileChangedMsg{})}
	doc := m.preview.doc
	if doc == nil {
		return tea.Batch(cmds...)
	}
	for _, item := range doc.tabs.Items {
		view := item.View
		if view == nil {
			continue
		}
		view.Observe()
		if cmd := wrapPreviewDocLoad(view.Refresh(false), doc.surface); cmd != nil {
			cmds = append(cmds, cmd)
		}
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
	if diff == nil || diff.root == "" {
		return nil
	}
	targets := append([]livewatch.Target(nil), m.preview.diffAdminTargets[diff.root]...)
	if view := diff.tabs.ActiveView(); view != nil {
		targets = append(targets, view.WatchTargets(diff.root, nil)...)
	}
	return targets
}

func (m *Model) syncPreviewDiffWatch() tea.Cmd {
	diff := m.preview.diff
	if diff == nil || diff.root == "" || len(diff.tabs.Items) == 0 {
		if m.preview.diffWatcher != nil {
			m.preview.diffWatcher.Watch()
		}
		return nil
	}

	var cmds []tea.Cmd
	if _, done := m.preview.diffAdminTargets[diff.root]; !done && !m.preview.diffAdminResolving[diff.root] {
		if m.preview.diffAdminResolving == nil {
			m.preview.diffAdminResolving = make(map[string]bool)
		}
		m.preview.diffAdminResolving[diff.root] = true
		cmds = append(cmds, workspacediff.ResolveAdminTargets(diff.root, "", 0))
	}

	targets := m.previewDiffTargets()
	switch {
	case m.preview.diffWatcher != nil:
		m.preview.diffWatcher.Watch(targets...)
	case len(targets) > 0 && !m.preview.diffWatchStarting:
		m.preview.diffWatchStarting = true
		cmds = append(cmds, livewatch.Start(livewatch.Config{
			Quiet:      500 * time.Millisecond,
			MaxLatency: 3 * time.Second,
		}, targets, func(w *livewatch.PathWatcher, err error) tea.Msg {
			if err != nil {
				return previewDiffWatchStartedMsg{}
			}
			return previewDiffWatchStartedMsg{Watcher: w}
		}))
	}
	return tea.Batch(cmds...)
}

func (m *Model) refreshPreviewDiffs() tea.Cmd {
	cmds := []tea.Cmd{livewatch.Listen(m.preview.diffWatcher, previewDiffRepoChangedMsg{})}
	diff := m.preview.diff
	if diff == nil {
		return tea.Batch(cmds...)
	}
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
	return tea.Batch(cmds...)
}
