package overview

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

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

// A rewrite producing identical bytes is not a change. Manifests are rewritten
// wholesale, so fingerprinting by content rather than mtime is what keeps a
// no-op write from costing a Git listing.
func TestIdenticalManifestRewriteIsNotAChange(t *testing.T) {
	state, root := t.TempDir(), t.TempDir()
	stubManifests(t, map[string]string{root: state})
	writeManifest(t, state, "alpha")

	m := New(workspaceinventory.Collector{})
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

// Opening the tab is the gesture a user makes when they suspect the board is
// stale, so it must re-read durable state rather than resume a live-only poll.
func TestEnsureRefreshesOnTabEntryButNotTwice(t *testing.T) {
	m := New(workspaceinventory.Collector{})
	projects := []Project{{Name: "one", Path: t.TempDir()}}

	if cmd := m.Ensure(projects); cmd == nil {
		t.Fatal("first entry did not start a cycle")
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

	// A finished cycle leaves a poll scheduled. Re-entering the tab then has to
	// refresh anyway — that poll only re-reads tmux evidence.
	m.loading = false
	m.pollScheduled = true
	if cmd := m.Ensure(projects); cmd == nil {
		t.Fatal("re-entry with a poll scheduled did not refresh")
	}
	if m.generation == generation {
		t.Fatal("re-entry did not start a new generation")
	}
}
