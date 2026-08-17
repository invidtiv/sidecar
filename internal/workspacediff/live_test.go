package workspacediff

import (
	"errors"
	"path/filepath"
	"slices"
	"strconv"
	"testing"

	"github.com/marcus/sidecar/internal/livewatch"
)

// td-e9a275: a viewed diff must follow the repository, keeping the selected
// file and scroll offset, and must not re-run git when nothing moved.

// patch builds a working-tree diff containing one hunk per named path. The
// hunks are deliberately taller than the test viewport, so a preserved scroll
// offset is not clamped away for reasons that have nothing to do with refresh.
func patch(paths ...string) string {
	out := ""
	for _, p := range paths {
		out += "diff --git a/" + p + " b/" + p + "\n" +
			"--- a/" + p + "\n" +
			"+++ b/" + p + "\n" +
			"@@ -1,80 +1,160 @@\n"
		for i := range 80 {
			out += " context " + strconv.Itoa(i) + "\n"
			out += "+added " + strconv.Itoa(i) + " in " + p + "\n"
		}
	}
	return out
}

func snapshot(paths ...string) *Snapshot {
	return &Snapshot{State: LoadStateReady, WorkingTree: patch(paths...)}
}

// loadedDiff returns a working-tree view that has completed one load.
func loadedDiff(t *testing.T, workdir string, snap *Snapshot) *View {
	t.Helper()
	v := &View{Target: Target{Kind: TargetWorkingTree}}
	v.SetSize(120, 40)
	v.Bind(workdir, "ws-1", 5)
	v.ApplyLoadedSnapshot(snap, workdir, "ws-1")
	if len(v.Files) == 0 {
		t.Fatal("test setup produced no files; the patch fixture did not parse")
	}
	return v
}

func refreshSnapshotMsg(v *View, snap *Snapshot, err error) SnapshotMsg {
	return SnapshotMsg{
		Epoch: v.Epoch, WorkspaceID: v.WorkspaceID, Identity: v.Target.Identity(),
		Snapshot: snap, Err: err, Refresh: true,
	}
}

func TestDiffRefreshIsNotOfferedUntilTheRepositoryMoves(t *testing.T) {
	v := loadedDiff(t, t.TempDir(), snapshot("a.go"))
	if cmd := v.Refresh(v.WorkDir, "", v.WorkspaceID, false); cmd != nil {
		t.Fatal("Refresh() returned a command on an idle repository; six git processes for nothing")
	}
}

func TestDiffRefreshIsOfferedOnceTheRepositoryMoves(t *testing.T) {
	v := loadedDiff(t, t.TempDir(), snapshot("a.go"))
	v.Observe()
	if cmd := v.Refresh(v.WorkDir, "", v.WorkspaceID, false); cmd == nil {
		t.Fatal("Refresh() = nil after the repository moved")
	}
	v.Observe()
	if cmd := v.Refresh(v.WorkDir, "", v.WorkspaceID, false); cmd != nil {
		t.Fatal("Refresh() stacked a second snapshot load; a rebase would queue dozens")
	}
}

// A commit or a range diff renders the same bytes forever, so re-running it
// would spend subprocesses to produce what is already on screen.
func TestImmutableTargetsNeverRefresh(t *testing.T) {
	for _, target := range []Target{
		{Kind: TargetCommit, A: "abc1234"},
		{Kind: TargetRange, A: "main", B: "HEAD"},
	} {
		v := &View{Target: target, State: LoadStateReady}
		v.SetSize(120, 40)
		v.Observe()
		if cmd := v.Refresh(t.TempDir(), "", "ws-1", false); cmd != nil {
			t.Errorf("Refresh() returned a command for %s, which cannot change", target.Identity())
		}
	}
}

func TestDiffRefreshSuppressedStaysOwed(t *testing.T) {
	v := loadedDiff(t, t.TempDir(), snapshot("a.go"))
	v.Observe()
	if cmd := v.Refresh(v.WorkDir, "", v.WorkspaceID, true); cmd != nil {
		t.Fatal("Refresh(suppressed=true) issued a command")
	}
	if !v.RefreshPending() {
		t.Fatal("a suppressed refresh was dropped instead of deferred")
	}
	if cmd := v.Refresh(v.WorkDir, "", v.WorkspaceID, false); cmd == nil {
		t.Fatal("the deferred refresh did not land once the veto lifted")
	}
}

func TestUnchangedDiffRefreshDoesNotRepaint(t *testing.T) {
	v := loadedDiff(t, t.TempDir(), snapshot("a.go", "b.go"))
	v.Observe()
	v.Refresh(v.WorkDir, "", v.WorkspaceID, false)

	before := v.Content
	v.ApplySnapshotMsg(refreshSnapshotMsg(v, snapshot("a.go", "b.go"), nil), v.WorkDir, v.WorkspaceID)
	if v.Content != before {
		t.Fatal("an unchanged refresh rebuilt the content")
	}
}

// The subtle one. Cursor is a positional index, so staging or committing the
// file above the one being read shifts every index and would silently select a
// different file.
func TestDiffRefreshKeepsTheSelectedFileWhenAFileAboveItLeaves(t *testing.T) {
	v := loadedDiff(t, t.TempDir(), snapshot("a.go", "b.go", "c.go"))
	v.Cursor = 2
	selected := v.Files[v.Cursor].Path
	if selected != "c.go" {
		t.Fatalf("test setup selected %q, want c.go", selected)
	}

	v.Observe()
	v.Refresh(v.WorkDir, "", v.WorkspaceID, false)
	// a.go was committed away; c.go is now at index 1.
	v.ApplySnapshotMsg(refreshSnapshotMsg(v, snapshot("b.go", "c.go"), nil), v.WorkDir, v.WorkspaceID)

	if v.Cursor < 0 || v.Cursor >= len(v.Files) {
		t.Fatalf("Cursor = %d with %d files after refresh", v.Cursor, len(v.Files))
	}
	if got := v.Files[v.Cursor].Path; got != selected {
		t.Fatalf("selection moved to %q after refresh, want %q held", got, selected)
	}
}

func TestDiffRefreshKeepsScrollForTheSameFile(t *testing.T) {
	v := loadedDiff(t, t.TempDir(), snapshot("a.go", "b.go"))
	v.Cursor = 1
	v.DiffScroll = 3

	v.Observe()
	v.Refresh(v.WorkDir, "", v.WorkspaceID, false)
	v.ApplySnapshotMsg(refreshSnapshotMsg(v, snapshot("a.go", "b.go", "c.go"), nil), v.WorkDir, v.WorkspaceID)

	if got := v.Files[v.Cursor].Path; got != "b.go" {
		t.Fatalf("selection = %q after refresh, want b.go", got)
	}
	if v.DiffScroll != 3 {
		t.Fatalf("DiffScroll = %d after refresh, want 3 preserved", v.DiffScroll)
	}
}

// When the selected file leaves the diff entirely there is no position to keep,
// and holding a stale offset into a different file's patch would be worse than
// starting at the top.
func TestDiffRefreshResetsScrollWhenTheSelectedFileLeaves(t *testing.T) {
	v := loadedDiff(t, t.TempDir(), snapshot("a.go", "b.go"))
	v.Cursor = 1
	v.DiffScroll = 5

	v.Observe()
	v.Refresh(v.WorkDir, "", v.WorkspaceID, false)
	v.ApplySnapshotMsg(refreshSnapshotMsg(v, snapshot("a.go"), nil), v.WorkDir, v.WorkspaceID)

	if v.DiffScroll != 0 {
		t.Fatalf("DiffScroll = %d after the selected file left the diff, want 0", v.DiffScroll)
	}
	if v.Cursor >= len(v.Files)+len(v.Commits) && v.TotalItems() > 0 {
		t.Fatalf("Cursor = %d with %d items after refresh", v.Cursor, v.TotalItems())
	}
}

// An index.lock held by another git command is routine during a rebase. Losing
// a review to it would be worse than being one refresh stale.
func TestFailedDiffRefreshKeepsTheDiffOnScreen(t *testing.T) {
	v := loadedDiff(t, t.TempDir(), snapshot("a.go"))
	before := v.Content

	v.Observe()
	v.Refresh(v.WorkDir, "", v.WorkspaceID, false)
	v.ApplySnapshotMsg(refreshSnapshotMsg(v, nil, errors.New("index.lock exists")), v.WorkDir, v.WorkspaceID)

	if v.State == LoadStateError {
		t.Fatal("a failed refresh put the pane into its error state")
	}
	if v.Content != before {
		t.Fatal("a failed refresh discarded the diff on screen")
	}
}

func TestExplicitLoadResetsTheRefreshGate(t *testing.T) {
	v := loadedDiff(t, t.TempDir(), snapshot("a.go"))
	v.Observe()
	// An explicit reload defines what is on screen again.
	v.ApplyLoadedSnapshot(snapshot("a.go"), v.WorkDir, v.WorkspaceID)
	if v.RefreshPending() {
		t.Fatal("a refresh owed before an explicit load survived it")
	}
}

func TestWatchTargetsCoverAdminPathsAndFilesUnderReview(t *testing.T) {
	workdir := t.TempDir()
	v := loadedDiff(t, workdir, snapshot(filepath.Join("pkg", "a.go"), "b.go"))

	got := v.WatchTargets(workdir, nil)
	paths := make([]string, 0, len(got))
	for _, tgt := range got {
		paths = append(paths, tgt.Path)
	}

	for _, want := range []string{
		filepath.Join(workdir, "pkg", "a.go"),
		filepath.Join(workdir, "b.go"),
		filepath.Join(workdir, "pkg"),
		workdir,
	} {
		if !slices.Contains(paths, want) {
			t.Errorf("WatchTargets() missing %q; got %v", want, paths)
		}
	}
}

func TestWatchTargetsIncludesTheSuppliedAdminPaths(t *testing.T) {
	workdir := t.TempDir()
	v := loadedDiff(t, workdir, snapshot("a.go"))

	head := filepath.Join(workdir, ".git", "HEAD")
	got := v.WatchTargets(workdir, []livewatch.Target{livewatch.File(head)})

	found := false
	for _, tgt := range got {
		if tgt.Path == head {
			found = true
		}
	}
	if !found {
		t.Fatal("WatchTargets() dropped the git administrative paths it was given")
	}
}
