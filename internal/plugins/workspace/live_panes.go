package workspace

import (
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/marcus/sidecar/internal/issueview"
	"github.com/marcus/sidecar/internal/livewatch"
	"github.com/marcus/sidecar/internal/workspacediff"
)

// Live refresh for the workspace plugin's three content panes.
//
// One watcher per kind, not one per pane. The panes of a kind almost always
// want the same signal — every issue card reads the same td store, every diff
// reads the same repository — and on macOS each registration costs a descriptor
// per file in the watched directory, so a watcher per tab would be expensive
// for no benefit. A single watcher with a reconciled target set gives the same
// answer at a fraction of the cost, and there is exactly one thing to stop.
//
// All three are created lazily, from inside a tea.Cmd, the first time a pane of
// that kind opens. A session that never opens an issue card never opens a
// descriptor for one, and nothing here touches the startup path.

// Messages for the live-refresh loop. Each kind has a started message carrying
// the watcher, and a signal message that means "your targets moved, go look".
type (
	issueWatchStartedMsg struct {
		Epoch   uint64
		Watcher *livewatch.PathWatcher
	}
	issueStoreChangedMsg struct{}

	docWatchStartedMsg struct {
		Epoch   uint64
		Watcher *livewatch.PathWatcher
	}
	docFileChangedMsg struct{}

	diffWatchStartedMsg struct {
		Epoch   uint64
		Watcher *livewatch.PathWatcher
	}
	diffRepoChangedMsg struct{}
)

// GetEpoch lets the plugin's normal stale-message check drop watchers started
// for a project the user has since switched away from.
func (m issueWatchStartedMsg) GetEpoch() uint64 { return m.Epoch }
func (m docWatchStartedMsg) GetEpoch() uint64   { return m.Epoch }
func (m diffWatchStartedMsg) GetEpoch() uint64  { return m.Epoch }

// stopLiveWatchers releases every descriptor the live-refresh loop holds.
//
// Stop blocks until the watcher goroutine has drained, so this runs detached.
// Plugin.Stop is both the quit and the project-switch boundary, and stalling it
// would stall the switch.
func (p *Plugin) stopLiveWatchers() {
	for _, w := range []**livewatch.PathWatcher{&p.issueWatcher, &p.docWatcher, &p.diffWatcher} {
		if *w == nil {
			continue
		}
		watcher := *w
		*w = nil
		go watcher.Stop()
	}
}

// adoptWatcher installs a watcher created off the update goroutine, stopping it
// instead if the plugin no longer wants one.
//
// A project switch runs Stop then Init then Start, so a watcher started before
// the switch can still land afterwards. Whichever watcher is not adopted has to
// be stopped, or its goroutine and its descriptors live for the rest of the
// process.
func adoptWatcher(slot **livewatch.PathWatcher, incoming *livewatch.PathWatcher, wanted bool) bool {
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

// ---------------------------------------------------------------------------
// Issue cards (td-312e4e)
// ---------------------------------------------------------------------------

// syncIssueWatch points the td-store watcher at the store behind the open issue
// panes, starting it on first use and releasing it when the last card closes.
func (p *Plugin) syncIssueWatch() tea.Cmd {
	targets := p.issueWatchTargets()
	if len(targets) == 0 {
		if p.issueWatcher != nil {
			p.issueWatcher.Watch()
		}
		return nil
	}
	if p.issueWatcher != nil {
		p.issueWatcher.Watch(targets...)
		return nil
	}
	if p.issueWatchStarting {
		return nil
	}
	p.issueWatchStarting = true
	epoch := p.ctx.Epoch
	return livewatch.Start(livewatch.Config{
		// The td store moves whenever any issue anywhere changes, and a single
		// `td` command can write several times. A longer settle than the default
		// turns a burst into one re-read; the ticket asks for a second or two,
		// not for sub-second.
		Quiet:      400 * time.Millisecond,
		MaxLatency: 2 * time.Second,
	}, targets, func(w *livewatch.PathWatcher, err error) tea.Msg {
		if err != nil {
			return issueWatchStartedMsg{Epoch: epoch}
		}
		return issueWatchStartedMsg{Epoch: epoch, Watcher: w}
	})
}

// issueWatchTargets is the td store behind any open issue pane, or nil when
// none is open.
//
// Resolution is cached per worktree and never done here. Finding the td root
// can walk parent directories and shell out to git, and this runs on the update
// goroutine every time the pane set is reconciled; doing the real work inline
// would be exactly the kind of hidden filesystem cost the startup rules exist
// to prevent.
func (p *Plugin) issueWatchTargets() []livewatch.Target {
	seen := make(map[string]bool)
	var targets []livewatch.Target
	for _, pane := range p.issues {
		if pane == nil || len(pane.tabs.Items) == 0 || pane.root == "" {
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

// resolveTDStores returns commands resolving the td store for any open issue
// pane whose worktree has not been resolved yet.
func (p *Plugin) resolveTDStores() tea.Cmd {
	var cmds []tea.Cmd
	for _, pane := range p.issues {
		if pane == nil || pane.root == "" || len(pane.tabs.Items) == 0 {
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
		root, epoch := pane.root, p.ctx.Epoch
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
	return p.syncIssueWatch()
}

// handleIssueWatchStarted adopts the td-store watcher and begins listening.
func (p *Plugin) handleIssueWatchStarted(msg issueWatchStartedMsg) tea.Cmd {
	p.issueWatchStarting = false
	if !adoptWatcher(&p.issueWatcher, msg.Watcher, len(p.issues) > 0) {
		return nil
	}
	p.issueWatcher.Watch(p.issueWatchTargets()...)
	return livewatch.Listen(p.issueWatcher, issueStoreChangedMsg{})
}

// handleIssueStoreChanged re-reads every open issue card and re-arms the
// listener.
func (p *Plugin) handleIssueStoreChanged() tea.Cmd {
	cmds := []tea.Cmd{livewatch.Listen(p.issueWatcher, issueStoreChangedMsg{})}
	for _, pane := range p.issues {
		if pane == nil {
			continue
		}
		for _, item := range pane.tabs.Items {
			view := item.Value
			if view == nil {
				continue
			}
			view.Observe()
			if cmd := view.Refresh(p.issueRefreshSuppressed()); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
	}
	return tea.Batch(cmds...)
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

// syncDocWatch points the document watcher at exactly the files the open doc
// tabs are showing.
//
// Every open tab is watched, not just the active one. A background tab whose
// file changed and is then selected should already be current; re-reading on
// selection would show the user a stale frame first.
func (p *Plugin) syncDocWatch() tea.Cmd {
	targets := p.docWatchTargets()
	if len(targets) == 0 {
		if p.docWatcher != nil {
			p.docWatcher.Watch()
		}
		return nil
	}
	if p.docWatcher != nil {
		p.docWatcher.Watch(targets...)
		return nil
	}
	if p.docWatchStarting {
		return nil
	}
	p.docWatchStarting = true
	epoch := p.ctx.Epoch
	return livewatch.Start(livewatch.Config{
		Ignore: isEditorScratchPath,
	}, targets, func(w *livewatch.PathWatcher, err error) tea.Msg {
		if err != nil {
			return docWatchStartedMsg{Epoch: epoch}
		}
		return docWatchStartedMsg{Epoch: epoch, Watcher: w}
	})
}

func (p *Plugin) docWatchTargets() []livewatch.Target {
	var targets []livewatch.Target
	seen := make(map[string]bool)
	for _, pane := range p.docs {
		if pane == nil {
			continue
		}
		for _, item := range pane.tabs.Items {
			view := item.View
			if view == nil {
				continue
			}
			view.SetRoot(pane.root)
			t := view.WatchTarget()
			if t.Path == "" || seen[t.Path] {
				continue
			}
			seen[t.Path] = true
			targets = append(targets, t)
		}
	}
	return targets
}

func (p *Plugin) handleDocWatchStarted(msg docWatchStartedMsg) tea.Cmd {
	p.docWatchStarting = false
	if !adoptWatcher(&p.docWatcher, msg.Watcher, len(p.docs) > 0) {
		return nil
	}
	p.docWatcher.Watch(p.docWatchTargets()...)
	return livewatch.Listen(p.docWatcher, docFileChangedMsg{})
}

func (p *Plugin) handleDocFileChanged() tea.Cmd {
	cmds := []tea.Cmd{livewatch.Listen(p.docWatcher, docFileChangedMsg{})}
	for _, pane := range p.docs {
		if pane == nil {
			continue
		}
		suppressed := p.docRefreshSuppressed(pane)
		for _, item := range pane.tabs.Items {
			view := item.View
			if view == nil {
				continue
			}
			view.Observe()
			if cmd := view.Refresh(suppressed); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
	}
	return tea.Batch(cmds...)
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

// syncDiffWatch points the repository watcher at git's administrative files
// plus the files currently under review.
//
// The admin paths have to be resolved by running git, which is why they are
// resolved once per worktree and cached: this is called every time the diff's
// file list changes, and re-running `git rev-parse` five times on each of those
// would be exactly the per-change subprocess cost the ticket rules out.
func (p *Plugin) syncDiffWatch() tea.Cmd {
	root := p.diffWatchRoot()
	if root == "" {
		if p.diffWatcher != nil {
			p.diffWatcher.Watch()
		}
		return nil
	}

	var cmds []tea.Cmd
	admin, resolved := p.diffAdminTargets[root]
	if !resolved {
		if !p.diffAdminResolving[root] {
			if p.diffAdminResolving == nil {
				p.diffAdminResolving = make(map[string]bool)
			}
			p.diffAdminResolving[root] = true
			cmds = append(cmds, workspacediff.ResolveAdminTargets(root, "", p.ctx.Epoch))
		}
		// Watch the files under review now; the admin paths join when they land.
	}

	targets := p.diffWatchTargets(root, admin)
	switch {
	case p.diffWatcher != nil:
		p.diffWatcher.Watch(targets...)
	case !p.diffWatchStarting:
		p.diffWatchStarting = true
		epoch := p.ctx.Epoch
		cmds = append(cmds, livewatch.Start(livewatch.Config{
			// A repository under rebase or checkout writes hundreds of ref files
			// in a burst, and each re-read is roughly half a dozen git
			// subprocesses. Settle generously: a diff that lands a second late is
			// invisible, a diff re-run fifty times is not.
			Quiet:      500 * time.Millisecond,
			MaxLatency: 3 * time.Second,
		}, targets, func(w *livewatch.PathWatcher, err error) tea.Msg {
			if err != nil {
				return diffWatchStartedMsg{Epoch: epoch}
			}
			return diffWatchStartedMsg{Epoch: epoch, Watcher: w}
		}))
	}
	return tea.Batch(cmds...)
}

// diffWatchRoot is the worktree the diff surfaces belong to, or "" when none is
// showing a diff. Diff views in one plugin instance always share a worktree.
//
// The legacy view counts only once it has actually loaded. It is constructed
// eagerly for every session, so keying off its existence would start a
// repository watcher — and hold its descriptors — for users who never open the
// Diff surface at all.
func (p *Plugin) diffWatchRoot() string {
	for _, pane := range p.diffs {
		if pane != nil && pane.root != "" && len(pane.tabs.Items) > 0 {
			return pane.root
		}
	}
	if p.diff.WorkDir != "" && p.diff.State != workspacediff.LoadStateUnknown {
		return p.diff.WorkDir
	}
	return ""
}

func (p *Plugin) diffWatchTargets(root string, admin []livewatch.Target) []livewatch.Target {
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
	add(admin)
	// Only the visible tab's files. A background diff tab is not on screen, and
	// walking every tab's file list on every reconcile would put an O(files)
	// cost on the update loop for panes nobody is looking at. Selecting the tab
	// reconciles the watch set, and the tab's own load path covers the gap.
	for _, pane := range p.diffs {
		if pane == nil || pane.root != root {
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
	return p.syncDiffWatch()
}

func (p *Plugin) handleDiffWatchStarted(msg diffWatchStartedMsg) tea.Cmd {
	p.diffWatchStarting = false
	if !adoptWatcher(&p.diffWatcher, msg.Watcher, p.diffWatchRoot() != "") {
		return nil
	}
	p.diffWatcher.Watch(p.diffWatchTargets(p.diffWatchRoot(), p.diffAdminTargets[p.diffWatchRoot()])...)
	return livewatch.Listen(p.diffWatcher, diffRepoChangedMsg{})
}

func (p *Plugin) handleDiffRepoChanged() tea.Cmd {
	cmds := []tea.Cmd{livewatch.Listen(p.diffWatcher, diffRepoChangedMsg{})}
	suppressed := p.diffRefreshSuppressed()
	// The plugin's own diff view, which predates panes and is still what the
	// non-pane Diff surface renders. It is a diff pane by any other name and
	// goes stale the same way.
	p.diff.Observe()
	if cmd := p.diff.Refresh(p.diff.WorkDir, p.selectedDiffBaseRef(), p.diff.WorkspaceID, suppressed); cmd != nil {
		cmds = append(cmds, cmd)
	}
	for _, pane := range p.diffs {
		if pane == nil {
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
	return tea.Batch(cmds...)
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

// ---------------------------------------------------------------------------
// Reconciliation
// ---------------------------------------------------------------------------

// reconcileLiveWatches brings all three watch sets in line with the panes that
// are actually open.
//
// It is called from the plugin's Update loop rather than from each of the two
// dozen places that create, close, retarget or restore a pane. Those call sites
// are spread across pane creation, tab selection, layout decode, leaf close and
// project switch, and missing one would show up as either a pane that never
// updates or a watcher that outlives its pane — both silent. Reconciling from
// one place makes the invariant "the watch set matches the pane set" hold by
// construction.
//
// It is cheap enough to run per message: the target lists are built from cached
// per-worktree resolutions and the open tabs' own paths, and PathWatcher.Watch
// diffs the result, touching the kernel only for registrations that actually
// changed.
func (p *Plugin) reconcileLiveWatches() tea.Cmd {
	if p.ctx == nil {
		return nil
	}
	return tea.Batch(
		p.resolveTDStores(),
		p.syncIssueWatch(),
		p.syncDocWatch(),
		p.syncDiffWatch(),
	)
}

// handleLiveWatchMsg handles the live-refresh messages, reporting whether msg
// was one of them.
func (p *Plugin) handleLiveWatchMsg(msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case tdStoreResolvedMsg:
		return p.handleTDStoreResolved(msg), true
	case issueWatchStartedMsg:
		return p.handleIssueWatchStarted(msg), true
	case issueStoreChangedMsg:
		return p.handleIssueStoreChanged(), true
	case docWatchStartedMsg:
		return p.handleDocWatchStarted(msg), true
	case docFileChangedMsg:
		return p.handleDocFileChanged(), true
	case diffWatchStartedMsg:
		return p.handleDiffWatchStarted(msg), true
	case diffRepoChangedMsg:
		return p.handleDiffRepoChanged(), true
	case workspacediff.AdminTargetsMsg:
		return p.handleDiffAdminTargets(msg), true
	}
	return nil, false
}
