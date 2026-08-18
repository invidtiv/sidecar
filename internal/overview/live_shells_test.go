package overview

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/agentstatus"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

// writeManifest writes a shells.json naming shells, returning its path.
func writeManifest(t *testing.T, dir string, shells ...string) string {
	t.Helper()
	type shell struct {
		TmuxName    string `json:"tmuxName"`
		DisplayName string `json:"displayName"`
	}
	file := struct {
		Version int     `json:"version"`
		Shells  []shell `json:"shells"`
	}{Version: 1}
	for _, name := range shells {
		file.Shells = append(file.Shells, shell{TmuxName: name, DisplayName: name})
	}
	data, err := json.Marshal(file)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "shells.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// collecting gives m the collection context the watch reconcile treats as its
// liveness signal, without running a cycle's fan-out.
func collecting(m *Model) {
	m.ctx, m.cancel = context.WithCancel(context.Background())
}

// stubManifests points manifest resolution at a temporary state tree for the
// duration of one test.
func stubManifests(t *testing.T, paths map[string]string) {
	t.Helper()
	previous := lookupProjectDirs
	lookupProjectDirs = func(roots []string) map[string]string {
		found := make(map[string]string, len(roots))
		for _, root := range roots {
			if dir, ok := paths[root]; ok {
				found[root] = dir
			}
		}
		return found
	}
	t.Cleanup(func() { lookupProjectDirs = previous })
}

// A shell another Sidecar on this host creates lands in the same state tree
// this instance reads. Before the manifest watch it stayed invisible until an
// explicit refresh, because the poll re-reads tmux evidence and no durable
// state at all.
func TestShellManifestChangeRefreshesOnlyTheChangedProject(t *testing.T) {
	stateOne, stateTwo := t.TempDir(), t.TempDir()
	rootOne, rootTwo := t.TempDir(), t.TempDir()
	writeManifest(t, stateOne, "alpha")
	writeManifest(t, stateTwo, "beta")
	stubManifests(t, map[string]string{rootOne: stateOne, rootTwo: stateTwo})

	m := New(workspaceinventory.Collector{})
	collecting(m)
	m.projects = []Project{
		{Name: "one", Path: rootOne, Key: workspaceinventory.CanonicalPath(rootOne)},
		{Name: "two", Path: rootTwo, Key: workspaceinventory.CanonicalPath(rootTwo)},
	}

	resolved := firstMsgOf[shellManifestsResolvedMsg](t, m.reconcileShellWatch())
	if len(resolved.Paths) != 2 {
		t.Fatalf("resolved manifests = %#v", resolved.Paths)
	}
	// The resolve reply schedules the baseline digest read.
	afterResolve, handled := m.handleShellWatchMsg(resolved)
	if !handled {
		t.Fatal("resolved manifests were not handled")
	}
	baseline := firstMsgOf[shellManifestDigestsMsg](t, afterResolve)
	if cmd := m.applyShellDigests(baseline.Digests); cmd != nil {
		t.Fatal("baseline digest read refreshed a project")
	}

	// Another instance adds a shell to the first project only.
	writeManifest(t, stateOne, "alpha", "gamma")
	changed := firstMsgOf[shellManifestDigestsMsg](t, m.readShellDigestsCmd())
	refresh := m.applyShellDigests(changed.Digests)
	if refresh == nil {
		t.Fatal("changed manifest did not refresh its project")
	}
	got := firstMsgOf[projectMutationRefreshMsg](t, refresh)
	if got.Project.Path != rootOne {
		t.Fatalf("refreshed project = %s, want %s", got.Project.Path, rootOne)
	}
	if !got.Background {
		t.Fatal("watcher-driven refresh must be marked background")
	}

	// The unchanged project costs nothing. A second read with no writes in
	// between must refresh neither.
	quiet := firstMsgOf[shellManifestDigestsMsg](t, m.readShellDigestsCmd())
	if cmd := m.applyShellDigests(quiet.Digests); cmd != nil {
		t.Fatal("unchanged manifests refreshed a project")
	}
}

// The watcher lifecycle, exercised against real descriptors rather than the
// package-wide stub. Nothing else in this package starts a PathWatcher, and a
// watcher that outlives its surface is the expensive failure: it holds kqueue
// descriptors and runs Git for a tab nobody is looking at.
func TestShellWatcherStopsWithTheSurfaceAndStaysStopped(t *testing.T) {
	state, root := t.TempDir(), t.TempDir()
	writeManifest(t, state, "alpha")
	stubManifests(t, map[string]string{root: state})

	m := New(workspaceinventory.Collector{})
	project := Project{Name: "one", Path: root, Key: workspaceinventory.CanonicalPath(root)}
	// A started surface whose first cycle has completed: Ensure establishes the
	// collection context the watch reconcile treats as its liveness signal, and
	// resolution waits for the project set to be complete.
	_ = m.Ensure([]Project{project})
	m.projects = []Project{project}
	m.loading = false

	resolved := firstMsgOf[shellManifestsResolvedMsg](t, m.reconcileShellWatch())
	m.shellManifestResolving = false
	m.shellManifestPaths = resolved.Paths
	started := firstMsgOf[shellWatchStartedMsg](t, m.reconcileShellWatch())
	if started.Watcher == nil {
		t.Fatal("watcher was not created")
	}
	if _, handled := m.handleShellWatchMsg(started); !handled {
		t.Fatal("watcher start was not handled")
	}
	if m.shellWatcher == nil || len(m.shellWatcher.WatchedDirs()) == 0 {
		t.Fatalf("watcher holds no registrations: %#v", m.shellWatcher)
	}
	watcher := m.shellWatcher
	// The generation the parked poll is carrying. Stop cancels its context,
	// which releases it immediately, so it arrives stamped with the cycle that
	// no longer exists.
	parked := m.generation

	m.Stop()
	if m.shellWatcher != nil {
		t.Fatal("Stop left the watcher attached")
	}
	// Stop detaches asynchronously; the descriptors come back either way.
	deadline := time.Now().Add(2 * time.Second)
	for len(watcher.WatchedDirs()) > 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if dirs := watcher.WatchedDirs(); len(dirs) > 0 {
		t.Fatalf("Stop leaked registrations: %v", dirs)
	}

	// A stopped surface still receives messages — cancelling the context
	// releases the parked poll, and that pollMsg is routed here. It must not
	// rebuild the watch behind a tab nobody is looking at.
	if cmd := m.Update(pollMsg{Generation: parked}); cmd != nil {
		if _, resurrected := findMsgOf[shellManifestsResolvedMsg](cmd); resurrected {
			t.Fatal("a stopped surface re-resolved manifests")
		}
	}
	if m.shellWatcher != nil || m.shellManifestPaths != nil {
		t.Fatalf("a stopped surface rebuilt its watch: watcher=%v paths=%v", m.shellWatcher, m.shellManifestPaths)
	}
}

// A rewrite producing identical bytes is not a change. Manifests are rewritten
// wholesale, so fingerprinting by content rather than mtime is what keeps a
// no-op write from costing a Git listing.
func TestIdenticalManifestRewriteIsNotAChange(t *testing.T) {
	state, root := t.TempDir(), t.TempDir()
	stubManifests(t, map[string]string{root: state})
	writeManifest(t, state, "alpha")

	m := New(workspaceinventory.Collector{})
	collecting(m)
	m.projects = []Project{{Name: "one", Path: root, Key: workspaceinventory.CanonicalPath(root)}}
	resolved := firstMsgOf[shellManifestsResolvedMsg](t, m.reconcileShellWatch())
	m.shellManifestPaths = resolved.Paths
	m.shellManifestResolving = false
	baseline := firstMsgOf[shellManifestDigestsMsg](t, m.readShellDigestsCmd())
	m.applyShellDigests(baseline.Digests)

	writeManifest(t, state, "alpha")
	again := firstMsgOf[shellManifestDigestsMsg](t, m.readShellDigestsCmd())
	if cmd := m.applyShellDigests(again.Digests); cmd != nil {
		t.Fatal("byte-identical rewrite refreshed the project")
	}
}

// The watch set must not grow with worktree count, and must not silently
// overflow livewatch's registration cap.
func TestShellManifestTargetsAreClampedPerProject(t *testing.T) {
	m := New(workspaceinventory.Collector{})
	paths := make(map[string]string, maxWatchedManifests+10)
	for i := 0; i < maxWatchedManifests+10; i++ {
		root := filepath.Join("/tmp/project", string(rune('a'+i%26)), string(rune('a'+i/26)))
		project := Project{Name: "p", Path: root, Key: workspaceinventory.CanonicalPath(root)}
		m.projects = append(m.projects, project)
		paths[projectKey(project)] = filepath.Join(root, "shells.json")
	}
	m.shellManifestPaths = paths
	if got := len(m.shellManifestTargets()); got != maxWatchedManifests {
		t.Fatalf("targets = %d, want %d", got, maxWatchedManifests)
	}
}

// The sweep is what covers durable state no watcher on Sidecar's own tree can
// see — a worktree created with bare `git worktree add`. Its per-tick cost must
// stay flat as project count grows, or it reintroduces the fan-out the
// live-only poll exists to avoid.
func TestSweepRotatesAndBoundsPerTickCost(t *testing.T) {
	m := New(workspaceinventory.Collector{})
	for i := 0; i < 20; i++ {
		root := filepath.Join("/tmp/sweep", string(rune('a'+i)))
		m.projects = append(m.projects, Project{Name: "p", Path: root, Key: workspaceinventory.CanonicalPath(root)})
	}
	// An all-idle board polls at the ready cadence, so a full rotation has to
	// fit in the ticks that cadence provides.
	m.results["one"] = workspaceinventory.ProjectResult{Workspaces: []workspaceinventory.Workspace{{Presentation: agentstatus.Presentation{Lane: agentstatus.LaneIdle}}}}
	batch := m.sweepBatchSize()
	if batch < 1 || batch > maxProjects {
		t.Fatalf("batch = %d, want within [1,%d]", batch, maxProjects)
	}
	ticks := int(inventorySweepEvery / m.pollInterval())
	if batch*ticks < len(m.projects) {
		t.Fatalf("batch %d over %d ticks cannot cover %d projects within %s", batch, ticks, len(m.projects), inventorySweepEvery)
	}

	// A full cycle has just re-read everything; only a live-only poll sweeps.
	m.liveOnly = false
	if cmd := m.sweepCmd(); cmd != nil {
		t.Fatal("full cycle swept on top of its own inventory")
	}

	m.liveOnly = true
	seen := map[string]bool{}
	for tick := 0; tick < len(m.projects); tick++ {
		before := m.sweepCursor
		if cmd := m.sweepCmd(); cmd == nil {
			t.Fatal("live poll did not sweep")
		}
		if got := m.sweepCursor - before; got != batch {
			t.Fatalf("tick advanced cursor by %d, want %d", got, batch)
		}
		for i := before; i < m.sweepCursor; i++ {
			seen[m.projects[i%len(m.projects)].Path] = true
		}
		if len(seen) == len(m.projects) {
			break
		}
	}
	if len(seen) != len(m.projects) {
		t.Fatalf("rotation covered %d of %d projects", len(seen), len(m.projects))
	}
}

// The feature racing itself: a sweep dispatched before another instance created
// a shell lands after the watcher refresh that found it, and — because a
// background result replaces the whole project — takes the shell back out.
//
// That does not heal. The live-only poll re-observes whatever membership it is
// given and never re-reads durable state, and the manifest digest already
// matches so the watcher will not fire again. Without the fence the shell stays
// gone until the project's next sweep rotation, which is minutes on a large set
// — the original symptom, with an extra step.
func TestSupersededBackgroundRefreshCannotUnseeANewShell(t *testing.T) {
	m := New(workspaceinventory.Collector{})
	project := Project{Name: "one", Path: "/tmp/one", Key: workspaceinventory.CanonicalPath("/tmp/one")}
	key := projectKey(project)
	m.projects = []Project{project}

	withShell := workspaceinventory.ProjectResult{
		ProjectKey: key,
		Workspaces: []workspaceinventory.Workspace{{ID: "remote-shell", ProjectKey: key, Kind: workspaceinventory.KindShell}},
	}
	withoutShell := workspaceinventory.ProjectResult{ProjectKey: key}

	// The sweep read durable state before the shell existed.
	sweptAt := time.Now()
	// The watcher then read it after, and lands first.
	m.applyProjectMutationRefresh(projectMutationRefreshMsg{
		Project: project, Result: withShell, Background: true, DispatchedAt: sweptAt.Add(time.Second),
	})
	if got := m.results[key].Workspaces; len(got) != 1 {
		t.Fatalf("watcher refresh did not add the shell: %#v", got)
	}

	// The sweep's older result arrives afterwards.
	m.applyProjectMutationRefresh(projectMutationRefreshMsg{
		Project: project, Result: withoutShell, Background: true, DispatchedAt: sweptAt,
	})
	if got := m.results[key].Workspaces; len(got) != 1 {
		t.Fatalf("a superseded sweep result removed the shell: %#v", got)
	}

	// A genuinely newer background read still applies, including removals — the
	// sweep noticing a deleted workspace is one of its jobs.
	m.applyProjectMutationRefresh(projectMutationRefreshMsg{
		Project: project, Result: withoutShell, Background: true, DispatchedAt: time.Now().Add(time.Second),
	})
	if got := m.results[key].Workspaces; len(got) != 0 {
		t.Fatalf("a newer background read failed to remove a gone workspace: %#v", got)
	}

	// A foreground mutation is never fenced: the user just performed it, and it
	// is by construction the newest thing known about the project.
	m.applyProjectMutationRefresh(projectMutationRefreshMsg{
		Project: project, Result: withShell, DispatchedAt: sweptAt.Add(-time.Hour),
	})
	if got := m.results[key].Workspaces; len(got) != 1 {
		t.Fatalf("a foreground mutation was fenced: %#v", got)
	}
}

// A background refresh that fails must not raise the create modal's error: the
// user never touched the project it names.
func TestBackgroundRefreshFailureStaysSilent(t *testing.T) {
	m := New(workspaceinventory.Collector{})
	project := Project{Name: "one", Path: "/tmp/one", Key: workspaceinventory.CanonicalPath("/tmp/one")}
	m.results[projectKey(project)] = workspaceinventory.ProjectResult{
		ProjectKey: projectKey(project),
		Workspaces: []workspaceinventory.Workspace{{ID: "keep"}},
	}
	cmd := m.applyProjectMutationRefresh(projectMutationRefreshMsg{
		Project:    project,
		Err:        os.ErrNotExist,
		Background: true,
	})
	if cmd != nil || m.createError != "" || m.createOpen {
		t.Fatalf("background failure surfaced: cmd=%v error=%q open=%v", cmd, m.createError, m.createOpen)
	}
	if got := m.results[projectKey(project)].Workspaces; len(got) != 1 {
		t.Fatalf("background failure discarded last good cards: %#v", got)
	}
}

// Entry has to re-read durable state, because the poll it would otherwise
// resume re-reads none — but moving between the two global tabs must not, since
// they share one catalog and one collector.
func TestEnsureRefreshesOnEntryButNotOnTabToggle(t *testing.T) {
	m := New(workspaceinventory.Collector{})
	projects := []Project{{Name: "one", Path: t.TempDir()}}

	if cmd := m.Ensure(projects); cmd == nil {
		t.Fatal("cold start did not begin a cycle")
	}
	// Both global tabs call Ensure during one entry; the second must not cancel
	// and restart the cycle the first began.
	generation := m.generation
	if cmd := m.Ensure(projects); cmd != nil {
		t.Fatal("second Ensure started a competing cycle")
	}
	if m.generation != generation {
		t.Fatalf("generation moved from %d to %d", generation, m.generation)
	}

	// A finished cycle. Toggling between Sessions and Activity now costs
	// nothing: the catalog behind both was just read.
	m.loading = false
	m.liveOnly = false
	m.lastFullInventory = time.Now()
	m.pollScheduled = true
	if cmd := m.Ensure(projects); cmd != nil {
		t.Fatal("tab toggle restarted the shared fan-out")
	}
	if m.generation != generation {
		t.Fatal("tab toggle started a new generation")
	}

	// A changed project set is a different catalog and always refreshes.
	if cmd := m.Ensure(append(projects, Project{Name: "two", Path: t.TempDir()})); cmd == nil {
		t.Fatal("changed project set did not refresh")
	}

	// Once the catalog has aged out, entry refreshes again — this is what makes
	// returning to the tab after a while show current data.
	m.loading = false
	m.lastFullInventory = time.Now().Add(-2 * inventorySweepEvery)
	generation = m.generation
	if cmd := m.Ensure(m.projects); cmd == nil {
		t.Fatal("stale catalog did not refresh on entry")
	}
	if m.generation == generation {
		t.Fatal("stale-catalog refresh did not start a new generation")
	}
}
