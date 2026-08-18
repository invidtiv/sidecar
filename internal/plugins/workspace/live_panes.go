package workspace

import (
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/marcus/sidecar/internal/issueview"
	"github.com/marcus/sidecar/internal/livepanes"
	"github.com/marcus/sidecar/internal/livewatch"
	"github.com/marcus/sidecar/internal/workspacediff"
)

// Live refresh for the workspace plugin's content panes.
//
// The lifecycle — start a watcher lazily, adopt it across a project switch,
// re-arm the listener, release every descriptor — belongs to [livepanes] and is
// shared with the global browser, so what is left here is only what this
// surface answers differently: which of its panes are on screen, and how each
// kind re-reads itself.
//
// One watcher per kind, not one per pane. The panes of a kind almost always
// want the same signal — every issue card reads the same td store, every diff
// reads the same repository — and on macOS each registration costs a descriptor
// per file in the watched directory, so a watcher per tab would be expensive for
// no benefit.
//
// Only visible panes are watched. A background tab in a directory nobody is
// looking at is a registration paid for a frame nobody sees; [livepanes] re-reads
// a pane when it comes back into view, and the no-change gate means an unchanged
// file costs one read and no repaint.

// The kinds this surface registers. They name the messages the set produces, so
// they are constants rather than literals scattered through the file.
const (
	liveIssues = "issues"
	liveDocs   = "docs"
	liveDiffs  = "diffs"
)

// liveOwner distinguishes this surface's live-refresh messages from the global
// browser's. Both are hosted in one process and read the same bus.
const liveOwner = "workspace"

// newLiveSet registers the pane kinds this surface refreshes.
//
// Adding a kind is one entry here. That is the whole reason the lifecycle moved
// out: a new content pane that forgets to refresh is a defect nobody sees until
// they are watching an agent work and the pane quietly stops being true.
func (p *Plugin) newLiveSet() *livepanes.Set {
	return livepanes.NewSet(liveOwner, p.liveEpoch,
		livepanes.Binding{
			Kind: liveIssues,
			// The td store moves whenever any issue anywhere changes, and a single
			// `td` command can write several times. A longer settle than the
			// default turns a burst into one re-read.
			Config:  livewatch.Config{Quiet: 400 * time.Millisecond, MaxLatency: 2 * time.Second},
			Prepare: p.resolveTDStores,
			Targets: p.issueWatchTargets,
			Refresh: p.refreshIssuePanes,
			Owed:    p.issueRefreshOwed,
		},
		livepanes.Binding{
			Kind:    liveDocs,
			Config:  livewatch.Config{Ignore: isEditorScratchPath},
			Targets: p.docWatchTargets,
			Refresh: p.refreshDocPanes,
			Owed:    p.docRefreshOwed,
		},
		livepanes.Binding{
			Kind: liveDiffs,
			// A repository under rebase or checkout writes hundreds of ref files
			// in a burst, and each re-read is roughly half a dozen git
			// subprocesses. Settle generously: a diff that lands a second late is
			// invisible, a diff re-run fifty times is not.
			Config:  livewatch.Config{Quiet: 500 * time.Millisecond, MaxLatency: 3 * time.Second},
			Prepare: p.resolveDiffAdminTargets,
			Targets: p.diffWatchTargets,
			Refresh: p.refreshDiffPanes,
			Owed:    p.diffRefreshOwed,
		},
	)
}

func (p *Plugin) liveEpoch() uint64 {
	if p.ctx == nil {
		return 0
	}
	return p.ctx.Epoch
}

// reconcileLiveWatches brings every watch set in line with the panes that are
// actually on screen.
//
// It is called from the plugin's Update loop rather than from each of the two
// dozen places that create, close, retarget or restore a pane. Those call sites
// are spread across pane creation, tab selection, layout decode, leaf close and
// project switch, and missing one would show up as either a pane that never
// updates or a watcher that outlives its pane — both silent.
//
// It is cheap enough to run per message: the target lists are built from cached
// per-worktree resolutions and the visible tabs' own paths, and the watcher
// diffs the result, touching the kernel only for registrations that changed.
func (p *Plugin) reconcileLiveWatches() tea.Cmd {
	if p.ctx == nil {
		return nil
	}
	if p.live == nil {
		p.live = p.newLiveSet()
	}
	return p.live.Reconcile()
}

// stopLiveWatchers releases every descriptor the live-refresh loop holds.
func (p *Plugin) stopLiveWatchers() { p.live.Stop() }

// handleLiveWatchMsg handles the live-refresh messages, reporting whether msg
// was one of them.
func (p *Plugin) handleLiveWatchMsg(msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case tdStoreResolvedMsg:
		return p.handleTDStoreResolved(msg), true
	case workspacediff.AdminTargetsMsg:
		return p.handleDiffAdminTargets(msg), true
	}
	return p.live.Handle(msg)
}

// ---------------------------------------------------------------------------
// Visibility
// ---------------------------------------------------------------------------

// visibleContentLeaves is the set of content leaves the last painted frame
// actually placed.
//
// It reads the recorded frame rather than re-deriving a layout, for the same
// reason pointer hits do: what is on screen is what was composed, not what the
// tree would compose if asked again. A frame that drew no tree — the kanban
// board, a modal, a preview too small to place — placed no leaves, and a zoomed
// frame placed exactly one, so both fall out of this without a special case.
func (p *Plugin) visibleContentLeaves() map[int]bool {
	visible := make(map[int]bool)
	// Focus is this surface's visibility contract — see SetFocused. A plugin the
	// user has switched away from is not repainted, so its recorded frame stays
	// frozen at whatever it last drew; without this, a diff pane on a tab nobody
	// is looking at would keep spending six git subprocesses per burst, which is
	// exactly the cost watching only visible panes exists to avoid.
	if !p.focused || !p.paneFrameDrawn {
		return visible
	}
	for _, placement := range p.paneFrame.Leaves {
		if placement.Node == nil || placement.Node.Kind == PaneTerminal {
			continue
		}
		visible[placement.Node.ContentID] = true
	}
	return visible
}

// ---------------------------------------------------------------------------
// Issue cards (td-312e4e)
// ---------------------------------------------------------------------------

// issueWatchTargets is the td store behind any visible issue pane, or nil when
// none is on screen.
//
// Resolution is cached per worktree and never done here. Finding the td root can
// walk parent directories and shell out to git, and this runs on the update
// goroutine every time the pane set is reconciled; doing the real work inline
// would be exactly the kind of hidden filesystem cost the startup rules exist to
// prevent.
func (p *Plugin) issueWatchTargets() []livewatch.Target {
	visible := p.visibleContentLeaves()
	seen := make(map[string]bool)
	var targets []livewatch.Target
	for id, pane := range p.issues {
		if pane == nil || !visible[id] || len(pane.tabs.Items) == 0 || pane.root == "" {
			continue
		}
		for _, t := range p.tdStoreTargets[pane.root] {
			if seen[t.Path] {
				continue
			}
			seen[t.Path] = true
			targets = append(targets, t)
		}
	}
	return targets
}

// resolveTDStores returns commands resolving the td store for any visible issue
// pane whose worktree has not been resolved yet.
func (p *Plugin) resolveTDStores() tea.Cmd {
	visible := p.visibleContentLeaves()
	var cmds []tea.Cmd
	for id, pane := range p.issues {
		if pane == nil || !visible[id] || pane.root == "" || len(pane.tabs.Items) == 0 {
			continue
		}
		if _, done := p.tdStoreTargets[pane.root]; done {
			continue
		}
		if p.tdStoreResolving[pane.root] {
			continue
		}
		if p.tdStoreResolving == nil {
			p.tdStoreResolving = make(map[string]bool)
		}
		p.tdStoreResolving[pane.root] = true
		root, epoch := pane.root, p.liveEpoch()
		cmds = append(cmds, func() tea.Msg {
			return tdStoreResolvedMsg{Epoch: epoch, Root: root, Targets: issueview.StoreTargets(root)}
		})
	}
	return tea.Batch(cmds...)
}

// tdStoreResolvedMsg carries the resolved td store location for a worktree.
type tdStoreResolvedMsg struct {
	Epoch   uint64
	Root    string
	Targets []livewatch.Target
}

// GetEpoch implements the plugin epoch check.
func (m tdStoreResolvedMsg) GetEpoch() uint64 { return m.Epoch }

func (p *Plugin) handleTDStoreResolved(msg tdStoreResolvedMsg) tea.Cmd {
	delete(p.tdStoreResolving, msg.Root)
	if p.tdStoreTargets == nil {
		p.tdStoreTargets = make(map[string][]livewatch.Target)
	}
	p.tdStoreTargets[msg.Root] = msg.Targets
	return p.reconcileLiveWatches()
}

// refreshIssuePanes re-reads every visible issue card.
func (p *Plugin) refreshIssuePanes() []tea.Cmd {
	visible := p.visibleContentLeaves()
	suppressed := p.issueRefreshSuppressed()
	var cmds []tea.Cmd
	for id, pane := range p.issues {
		if pane == nil || !visible[id] {
			continue
		}
		for _, item := range pane.tabs.Items {
			view := item.Value
			if view == nil {
				continue
			}
			view.Observe()
			if cmd := view.Refresh(suppressed); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
	}
	return cmds
}

// issueRefreshOwed reports whether a visible card is holding a change it was
// vetoed from applying, so the reconcile can retry it once the veto lifts.
func (p *Plugin) issueRefreshOwed() bool {
	visible := p.visibleContentLeaves()
	for id, pane := range p.issues {
		if pane == nil || !visible[id] {
			continue
		}
		for _, item := range pane.tabs.Items {
			if item.Value != nil && item.Value.RefreshPending() {
				return true
			}
		}
	}
	return false
}

// issueRefreshSuppressed vetoes an issue re-read while a modal owns the screen.
//
// The card underneath is not visible, so refreshing it buys nothing and costs
// three `td` subprocesses per card. The change stays owed and lands when the
// modal closes.
func (p *Plugin) issueRefreshSuppressed() bool {
	return p.viewMode != ViewModeList
}

// ---------------------------------------------------------------------------
// Document panes (td-03c21c)
// ---------------------------------------------------------------------------

// docWatchTargets is the file behind every visible document pane's active tab.
//
// The active tab only. A background tab is not on screen, and its registration
// would cost a descriptor per file in its directory for a frame nobody sees;
// selecting it brings it into the watch set, which is what makes [livepanes]
// re-read it.
func (p *Plugin) docWatchTargets() []livewatch.Target {
	visible := p.visibleContentLeaves()
	var targets []livewatch.Target
	seen := make(map[string]bool)
	for id, pane := range p.docs {
		if pane == nil || !visible[id] {
			continue
		}
		view := pane.view()
		if view == nil {
			continue
		}
		// The pane may have been opened by file descriptor, in which case the
		// model never learned where the file lives and has no path to re-read.
		view.SetRoot(pane.root)
		t := view.WatchTarget()
		if t.Path == "" || seen[t.Path] {
			continue
		}
		seen[t.Path] = true
		targets = append(targets, t)
	}
	return targets
}

// refreshDocPanes re-reads the active tab of every visible document pane.
func (p *Plugin) refreshDocPanes() []tea.Cmd {
	visible := p.visibleContentLeaves()
	var cmds []tea.Cmd
	for id, pane := range p.docs {
		if pane == nil || !visible[id] {
			continue
		}
		view := pane.view()
		if view == nil {
			continue
		}
		view.Observe()
		if cmd := view.Refresh(p.docRefreshSuppressed(pane)); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return cmds
}

// docRefreshOwed reports whether a visible document is holding a change it was
// vetoed from applying.
//
// This is the one that matters most in practice: the file picker, the info
// overlay and a pane search all veto while leaving the pane drawn, so the change
// is not re-driven by the pane going away and coming back.
func (p *Plugin) docRefreshOwed() bool {
	visible := p.visibleContentLeaves()
	for id, pane := range p.docs {
		if pane == nil || !visible[id] {
			continue
		}
		if view := pane.view(); view != nil && view.RefreshPending() {
			return true
		}
	}
	return false
}

// docRefreshSuppressed vetoes a document re-read while something else owns the
// pane: a search the user is typing into, or the info overlay. Both would have
// their state rebuilt underneath them by a reload.
func (p *Plugin) docRefreshSuppressed(pane *docPane) bool {
	if p.viewMode != ViewModeList {
		return true
	}
	if p.docInfo != nil {
		return true
	}
	return pane != nil && pane.mode != nil
}

// isEditorScratchPath reports whether a path is a file an editor leaves beside
// the one the user is reading.
//
// Vim in particular writes a probe file named 4913, a swap file, and a backup
// before it touches the real file. Those are not the document, and reacting to
// them makes the pane churn during someone else's edit.
func isEditorScratchPath(path string) bool {
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

// diffWatchRoot is the worktree the visible diff surfaces belong to, or "" when
// none is showing a diff. Diff views in one plugin instance always share a
// worktree.
//
// The legacy view counts only once it has actually loaded. It is constructed
// eagerly for every session, so keying off its existence would start a
// repository watcher — and hold its descriptors — for users who never open the
// Diff surface at all.
func (p *Plugin) diffWatchRoot() string {
	visible := p.visibleContentLeaves()
	for id, pane := range p.diffs {
		if pane != nil && visible[id] && pane.root != "" && len(pane.tabs.Items) > 0 {
			return pane.root
		}
	}
	if p.diff.WorkDir != "" && p.diff.State != workspacediff.LoadStateUnknown {
		return p.diff.WorkDir
	}
	return ""
}

// resolveDiffAdminTargets resolves git's administrative paths for the worktree
// under review, once per worktree.
//
// They have to be resolved by running git, which is why they are cached: this
// runs every time the pane set is reconciled, and re-running `git rev-parse`
// five times on each of those is exactly the per-change subprocess cost the
// startup rules exist to prevent.
func (p *Plugin) resolveDiffAdminTargets() tea.Cmd {
	root := p.diffWatchRoot()
	if root == "" {
		return nil
	}
	if _, resolved := p.diffAdminTargets[root]; resolved {
		return nil
	}
	if p.diffAdminResolving[root] {
		return nil
	}
	if p.diffAdminResolving == nil {
		p.diffAdminResolving = make(map[string]bool)
	}
	p.diffAdminResolving[root] = true
	return workspacediff.ResolveAdminTargets(root, "", p.liveEpoch())
}

// diffWatchTargets is git's administrative files plus the files currently under
// review in a visible diff pane.
func (p *Plugin) diffWatchTargets() []livewatch.Target {
	root := p.diffWatchRoot()
	if root == "" {
		return nil
	}
	visible := p.visibleContentLeaves()
	seen := make(map[string]bool)
	var targets []livewatch.Target
	add := func(list []livewatch.Target) {
		for _, t := range list {
			key := t.Path
			if t.Dir {
				key = "d:" + key
			}
			if seen[key] {
				continue
			}
			seen[key] = true
			targets = append(targets, t)
		}
	}
	add(p.diffAdminTargets[root])
	for id, pane := range p.diffs {
		if pane == nil || !visible[id] || pane.root != root {
			continue
		}
		if view := pane.tabs.ActiveView(); view != nil {
			add(view.WatchTargets(root, nil))
		}
	}
	if p.diff.WorkDir == root && p.diff.State != workspacediff.LoadStateUnknown {
		add(p.diff.WatchTargets(root, nil))
	}
	return targets
}

// handleDiffAdminTargets caches git's administrative paths for a worktree and
// folds them into the watch set.
func (p *Plugin) handleDiffAdminTargets(msg workspacediff.AdminTargetsMsg) tea.Cmd {
	if p.diffAdminResolving != nil {
		delete(p.diffAdminResolving, msg.WorkDir)
	}
	if p.diffAdminTargets == nil {
		p.diffAdminTargets = make(map[string][]livewatch.Target)
	}
	p.diffAdminTargets[msg.WorkDir] = msg.Targets
	return p.reconcileLiveWatches()
}

// refreshDiffPanes re-runs every visible diff.
func (p *Plugin) refreshDiffPanes() []tea.Cmd {
	visible := p.visibleContentLeaves()
	suppressed := p.diffRefreshSuppressed()
	var cmds []tea.Cmd
	// The plugin's own diff view, which predates panes and is still what the
	// non-pane Diff surface renders. It is a diff pane by any other name and
	// goes stale the same way.
	p.diff.Observe()
	if cmd := p.diff.Refresh(p.diff.WorkDir, p.selectedDiffBaseRef(), p.diff.WorkspaceID, suppressed); cmd != nil {
		cmds = append(cmds, cmd)
	}
	for id, pane := range p.diffs {
		if pane == nil || !visible[id] {
			continue
		}
		for _, item := range pane.tabs.Items {
			view := item.Value
			if view == nil {
				continue
			}
			view.Observe()
			if cmd := view.Refresh(pane.root, p.selectedDiffBaseRef(), "", suppressed); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
	}
	return cmds
}

// diffRefreshOwed reports whether a visible diff is holding a change it was
// vetoed from applying — the usual case being a signal that arrived while a
// stage, discard or commit was still writing.
func (p *Plugin) diffRefreshOwed() bool {
	if p.diff.RefreshPending() {
		return true
	}
	visible := p.visibleContentLeaves()
	for id, pane := range p.diffs {
		if pane == nil || !visible[id] {
			continue
		}
		for _, item := range pane.tabs.Items {
			if item.Value != nil && item.Value.RefreshPending() {
				return true
			}
		}
	}
	return false
}

// diffRefreshSuppressed vetoes a diff re-run while a modal owns the screen or a
// write operation is in flight.
//
// The second one matters: staging, discarding and committing all move the index
// and would otherwise have the pane re-reading underneath the operation that is
// still writing. The operation issues its own reload when it finishes, and the
// owed signal lands harmlessly after that with nothing new to show.
func (p *Plugin) diffRefreshSuppressed() bool {
	return p.viewMode != ViewModeList || p.activeLifecycleOperationID != ""
}
