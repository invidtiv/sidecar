package overview

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/marcus/sidecar/internal/livewatch"
	"github.com/marcus/sidecar/internal/projectdir"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

// Cross-instance freshness for the global browser's inventory.
//
// The poll this surface already runs is deliberately live-only: it refreshes
// tmux evidence for workspaces it knows about and performs no Git or metadata
// reads (see Collector.RefreshProjectStatus). That is the right trade for a
// 5-second cadence, but it means durable state — the shells a manifest claims,
// the worktrees Git reports — was only ever re-read on an explicit refresh, a
// project-set change, or a mutation this instance itself performed.
//
// A second Sidecar on the same host breaks that assumption. Shells created over
// SSH or mosh land in the same $XDG_STATE_HOME/sidecar tree this instance
// reads, and until something forced a full cycle they simply did not appear.
//
// Two mechanisms close the gap, deliberately split by cost:
//
//   - A watcher on each configured project's shells.json. Same host, same
//     filesystem, so this is fsnotify rather than polling: a shell another
//     instance creates shows up as fast as the write lands, and an idle install
//     pays nothing.
//   - A staggered sweep that re-inventories a few projects per poll tick. This
//     covers what no watcher on Sidecar's own state can see — a worktree an
//     agent created with bare `git worktree add`, which touches the repository's
//     .git directory and nothing of ours — and backstops any dropped event.
//
// What is deliberately *not* watched is each project's worktrees/ directory.
// Those hold one entry per worktree (50+ on this repository), and fsnotify's
// kqueue backend costs a descriptor per file in every watched directory, so the
// watch set would grow with worktree count on every project at once. The sweep
// covers worktrees at a latency the creating gesture can absorb.

const (
	// inventorySweepEvery is how long a full pass over every configured
	// project's durable state should take. It is a staleness target, not a
	// timer: the sweep rides the existing poll, refreshing a slice of projects
	// per tick sized so the rotation completes within roughly this long.
	inventorySweepEvery = 60 * time.Second

	// maxWatchedManifests bounds the manifest watch set.
	//
	// livewatch caps one watcher at 64 registrations and silently drops targets
	// past it, which would degrade into exactly the staleness this file exists
	// to fix — on an arbitrary subset of projects, with no signal. Clamping here
	// instead keeps the overflow explicit and traced; projects past the bound
	// still refresh on the sweep, just not instantly.
	maxWatchedManifests = 60
)

// lookupProjectDirs is overridable so tests resolve manifests against a
// temporary state tree instead of reading the developer's real one.
var lookupProjectDirs = projectdir.LookupAll

type (
	// shellManifestsResolvedMsg carries the manifest path for each configured
	// project. Resolution reads every registered project's meta.json, so it
	// happens once per watch setup inside a command.
	shellManifestsResolvedMsg struct {
		Generation uint64
		Paths      map[string]string
	}

	shellWatchStartedMsg struct {
		Generation uint64
		Watcher    *livewatch.PathWatcher
	}

	// shellManifestChangedMsg is the raw watcher signal: something under the
	// watched set moved, with no indication of what.
	shellManifestChangedMsg struct{}

	// shellManifestDigestsMsg carries a content fingerprint per manifest, from
	// which the update goroutine derives the changed set without touching disk.
	shellManifestDigestsMsg struct {
		Generation uint64
		Digests    map[string]string
	}
)

func isShellWatchMessage(msg tea.Msg) bool {
	switch msg.(type) {
	case shellManifestsResolvedMsg, shellWatchStartedMsg, shellManifestChangedMsg, shellManifestDigestsMsg:
		return true
	default:
		return false
	}
}

// handleShellWatchMsg handles a manifest-watch message, reporting whether msg
// was one of them.
func (m *Model) handleShellWatchMsg(msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case shellManifestsResolvedMsg:
		if msg.Generation != m.shellWatchGeneration {
			return nil, true
		}
		m.shellManifestResolving = false
		m.shellManifestPaths = msg.Paths
		// The first digest read establishes the baseline. Without it the first
		// signal would report every project as changed and re-inventory the
		// whole set at once — the burst this design exists to avoid.
		return tea.Batch(m.reconcileShellWatch(), m.readShellDigestsCmd()), true

	case shellWatchStartedMsg:
		if msg.Generation != m.shellWatchGeneration {
			if msg.Watcher != nil {
				go msg.Watcher.Stop()
			}
			return nil, true
		}
		m.shellWatchStarting = false
		if msg.Watcher == nil {
			return nil, true
		}
		if m.shellWatcher != nil && m.shellWatcher != msg.Watcher {
			old := m.shellWatcher
			go old.Stop()
		}
		m.shellWatcher = msg.Watcher
		m.shellWatcher.Watch(m.shellManifestTargets()...)
		return livewatch.Listen(m.shellWatcher, shellManifestChangedMsg{}), true

	case shellManifestChangedMsg:
		// Re-arm first: the read below is a command, and a manifest written
		// again while it runs must not be missed.
		return tea.Batch(
			livewatch.Listen(m.shellWatcher, shellManifestChangedMsg{}),
			m.readShellDigestsCmd(),
		), true

	case shellManifestDigestsMsg:
		if msg.Generation != m.shellWatchGeneration {
			return nil, true
		}
		return m.applyShellDigests(msg.Digests), true
	}
	return nil, false
}

// shellManifestTargets is the watch set: one shells.json per configured
// project. A file target registers its parent directory, and each project's
// state directory holds only a handful of entries, so the descriptor cost is
// proportional to project count rather than to worktree count.
func (m *Model) shellManifestTargets() []livewatch.Target {
	if len(m.shellManifestPaths) == 0 {
		return nil
	}
	targets := make([]livewatch.Target, 0, len(m.shellManifestPaths))
	for _, project := range m.projects {
		path, ok := m.shellManifestPaths[projectKey(project)]
		if !ok || path == "" {
			continue
		}
		if len(targets) >= maxWatchedManifests {
			m.tracef("shell watch clamped at %d manifests; %d projects configured — the rest refresh on the sweep", maxWatchedManifests, len(m.projects))
			break
		}
		targets = append(targets, livewatch.File(path))
	}
	return targets
}

// reconcileShellWatch brings the manifest watch set in line with the configured
// projects, resolving paths and starting the watcher as needed. It is swept
// once per update for the same reason the preview watchers are: the project set
// changes from more than one place, and reconciling in one of them makes the
// invariant hold by construction.
func (m *Model) reconcileShellWatch() tea.Cmd {
	if len(m.projects) == 0 {
		if m.shellWatcher != nil {
			m.shellWatcher.Watch()
		}
		return nil
	}

	var cmds []tea.Cmd
	if m.shellManifestPaths == nil && !m.shellManifestResolving {
		m.shellManifestResolving = true
		generation := m.shellWatchGeneration
		roots := make([]string, 0, len(m.projects))
		keyByRoot := make(map[string]string, len(m.projects))
		for _, project := range m.projects {
			roots = append(roots, project.Path)
			keyByRoot[project.Path] = projectKey(project)
		}
		cmds = append(cmds, func() tea.Msg {
			dirs := lookupProjectDirs(roots)
			paths := make(map[string]string, len(dirs))
			for root, dir := range dirs {
				if key, ok := keyByRoot[root]; ok {
					paths[key] = filepath.Join(dir, "shells.json")
				}
			}
			return shellManifestsResolvedMsg{Generation: generation, Paths: paths}
		})
		return tea.Batch(cmds...)
	}

	targets := m.shellManifestTargets()
	switch {
	case m.shellWatcher != nil:
		m.shellWatcher.Watch(targets...)
	case len(targets) > 0 && !m.shellWatchStarting:
		m.shellWatchStarting = true
		generation := m.shellWatchGeneration
		cmds = append(cmds, livewatch.Start(livewatch.Config{
			// Manifest writes are atomic (temp file plus rename) and arrive one
			// per user gesture, so this only has to absorb the create/rename
			// pair rather than an agent's write burst.
			Quiet:      200 * time.Millisecond,
			MaxLatency: time.Second,
		}, targets, func(w *livewatch.PathWatcher, err error) tea.Msg {
			if err != nil {
				return shellWatchStartedMsg{Generation: generation}
			}
			return shellWatchStartedMsg{Generation: generation, Watcher: w}
		}))
	}
	return tea.Batch(cmds...)
}

// readShellDigestsCmd fingerprints every watched manifest off the update
// goroutine. The files are small and few; the point of doing it in a command is
// that it is still filesystem work, not that it is slow.
func (m *Model) readShellDigestsCmd() tea.Cmd {
	if len(m.shellManifestPaths) == 0 {
		return nil
	}
	generation := m.shellWatchGeneration
	paths := make(map[string]string, len(m.shellManifestPaths))
	for key, path := range m.shellManifestPaths {
		paths[key] = path
	}
	return func() tea.Msg {
		digests := make(map[string]string, len(paths))
		for key, path := range paths {
			digests[key] = manifestDigest(path)
		}
		return shellManifestDigestsMsg{Generation: generation, Digests: digests}
	}
}

// manifestDigest fingerprints one manifest by content rather than by mtime,
// so a rewrite that produces identical bytes costs no refresh. A missing or
// unreadable file digests as the empty string, which compares equal to another
// missing read and so does not churn.
func manifestDigest(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// applyShellDigests folds a digest read back in and refreshes exactly the
// projects whose manifest actually changed.
//
// This is what keeps the reactive path's cost independent of project count: the
// watcher cannot say which manifest moved, but comparing fingerprints can, so a
// shell created in one project costs one project's Git and tmux work rather
// than a full cycle's fan-out.
func (m *Model) applyShellDigests(digests map[string]string) tea.Cmd {
	previous := m.shellManifestDigests
	m.shellManifestDigests = digests
	if previous == nil {
		// Baseline read; nothing to compare against yet.
		return nil
	}

	var cmds []tea.Cmd
	for _, project := range m.projects {
		key := projectKey(project)
		current, watched := digests[key]
		if !watched {
			continue
		}
		if previous[key] == current {
			continue
		}
		m.tracef("shell manifest changed project=%s — refreshing", key)
		cmds = append(cmds, m.refreshOneProject(project, true))
	}
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}

// stopShellWatch releases the manifest watcher and forgets what it observed, so
// a surface that starts again re-establishes its baseline rather than comparing
// against fingerprints from before it was away.
func (m *Model) stopShellWatch() {
	m.shellWatchGeneration++
	m.shellManifestResolving = false
	m.shellWatchStarting = false
	m.shellManifestPaths = nil
	m.shellManifestDigests = nil
	if m.shellWatcher != nil {
		w := m.shellWatcher
		m.shellWatcher = nil
		go w.Stop()
	}
}

// ---------------------------------------------------------------------------
// Staggered inventory sweep
// ---------------------------------------------------------------------------

// sweepCmd re-inventories a slice of the configured projects, rotating through
// the set so every project's durable state is re-read within roughly
// inventorySweepEvery.
//
// It runs only after a live-only poll, because a full cycle has just re-read
// everything the sweep would. Rotating rather than refreshing the whole set at
// once is what keeps the per-tick cost flat as project count grows: 20 projects
// cost the same few subprocess spawns per tick as 5 do, they just take more
// ticks to come around.
func (m *Model) sweepCmd() tea.Cmd {
	if !m.liveOnly || len(m.projects) == 0 {
		return nil
	}
	batch := m.sweepBatchSize()
	// The panes this cycle just collected are moments old and cover every
	// session, so the sweep reuses them rather than spawning a tmux inventory
	// per project on top of the Git listing it already needs.
	panes := append([]workspaceinventory.Pane(nil), m.currentPanes...)
	cmds := make([]tea.Cmd, 0, batch)
	for i := 0; i < batch; i++ {
		project := m.projects[m.sweepCursor%len(m.projects)]
		m.sweepCursor++
		cmds = append(cmds, m.refreshOneProjectWithPanes(project, true, panes))
	}
	m.tracef("sweep generation=%d projects=%d batch=%d cursor=%d", m.generation, len(m.projects), batch, m.sweepCursor)
	return tea.Batch(cmds...)
}

// sweepBatchSize spreads a full rotation across the poll ticks that fit in
// inventorySweepEvery, bounded by the same concurrency ceiling the main cycle
// fans out under.
func (m *Model) sweepBatchSize() int {
	interval := m.pollInterval()
	if interval <= 0 {
		return 1
	}
	ticks := int(inventorySweepEvery / interval)
	if ticks < 1 {
		ticks = 1
	}
	// Ceiling division: a rotation that does not divide evenly must still
	// finish within the window rather than one tick after it.
	batch := (len(m.projects) + ticks - 1) / ticks
	if batch < 1 {
		batch = 1
	}
	if batch > maxProjects {
		batch = maxProjects
	}
	return batch
}
