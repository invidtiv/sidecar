package workspacediff

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/marcus/sidecar/internal/livewatch"
)

// This file is the diff pane's binding to internal/livewatch.
//
// A diff moves less often than a document and costs far more to re-read: one
// snapshot is roughly half a dozen git subprocesses. So this binding is the
// conservative one. It spends nothing while the repository is idle — no timer,
// no poll, no subprocess — and re-runs the diff only when the filesystem says
// the repository actually moved.
//
// What it watches is two sets, and the split is deliberate:
//
//   - git's administrative files. HEAD, the index, packed-refs, FETCH_HEAD and
//     the refs tree cover every change that went through git: a commit, a
//     stage, a checkout, a merge, a rebase, a stash, a fetch. These are
//     resolved through `git rev-parse --git-path`, so a linked worktree — whose
//     HEAD and index live outside the common directory — works too.
//
//   - the files currently in the diff. An unstaged edit to a tracked file
//     touches nothing under .git, so the admin watch alone would miss the most
//     ordinary thing a user does while reviewing: keep editing. Watching the
//     paths already under review, and their directories, catches that and the
//     neighbouring file in the same package, for no additional cost.
//
// The honest gap: the first edit to a file in a directory nothing has touched
// yet is not observed until some git command runs. Closing it would mean either
// walking the worktree, which the tickets rule out, or polling `git status` on
// a timer, which would spend subprocesses on an idle repository — the one thing
// this ticket's acceptance criteria explicitly forbid. A diff is a review
// surface, not a monitor, and in practice a git command follows closely.

// gitAdminPaths are the administrative entries whose movement means the
// repository state changed. They are resolved per worktree, not assumed to sit
// under <worktree>/.git.
var gitAdminPaths = []string{"index", "HEAD", "packed-refs", "FETCH_HEAD"}

// resolveGitAdminTargets asks git where its administrative files live for
// workdir. It runs git and must not be called on the startup path; the diff
// pane calls it from inside a tea.Cmd when the pane opens.
func resolveGitAdminTargets(ctx context.Context, workdir string) []livewatch.Target {
	targets := make([]livewatch.Target, 0, len(gitAdminPaths)+1)
	for _, name := range gitAdminPaths {
		if path := resolveGitPath(ctx, workdir, name); path != "" {
			targets = append(targets, livewatch.File(path))
		}
	}
	// refs is a tree, and a branch update writes a leaf inside it. Registering
	// it as a directory target means any ref that moves reports, without this
	// package having to enumerate branches.
	if path := resolveGitPath(ctx, workdir, "refs"); path != "" {
		targets = append(targets, livewatch.Dir(path))
	}
	return targets
}

func resolveGitPath(ctx context.Context, workdir, name string) string {
	out, err := gitOutputBytes(ctx, workdir, "rev-parse", "--path-format=absolute", "--git-path", name)
	if err != nil {
		return ""
	}
	path := strings.TrimSpace(string(out))
	if path == "" {
		return ""
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(workdir, path)
	}
	return filepath.Clean(path)
}

// WatchTargets returns everything worth watching for this view: git's
// administrative files plus the files currently under review and the
// directories holding them.
//
// admin is the result of [ResolveAdminTargets] for the view's worktree, passed
// in rather than resolved here because resolving it runs git and this is called
// from the update loop every time the file list changes.
func (v *View) WatchTargets(workdir string, admin []livewatch.Target) []livewatch.Target {
	targets := make([]livewatch.Target, 0, len(admin)+2*len(v.Files))
	targets = append(targets, admin...)
	if workdir == "" {
		return targets
	}
	seenDir := make(map[string]bool, len(v.Files))
	for _, f := range v.Files {
		if f.Path == "" {
			continue
		}
		abs := filepath.Join(workdir, f.Path)
		targets = append(targets, livewatch.File(abs))
		dir := filepath.Dir(abs)
		if !seenDir[dir] {
			seenDir[dir] = true
			targets = append(targets, livewatch.Dir(dir))
		}
	}
	return targets
}

// ResolveAdminTargets returns a command that resolves git's administrative
// paths for workdir off the update goroutine.
func ResolveAdminTargets(workdir string, workspaceID string, epoch uint64) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return AdminTargetsMsg{
			Epoch:       epoch,
			WorkspaceID: workspaceID,
			WorkDir:     workdir,
			Targets:     resolveGitAdminTargets(ctx, workdir),
		}
	}
}

// AdminTargetsMsg carries the resolved git administrative paths for a worktree.
type AdminTargetsMsg struct {
	Epoch       uint64
	WorkspaceID string
	WorkDir     string
	Targets     []livewatch.Target
}

// GetEpoch implements the plugin epoch check.
func (m AdminTargetsMsg) GetEpoch() uint64 { return m.Epoch }

// Observe records that the repository may have moved.
func (v *View) Observe() {
	if v == nil {
		return
	}
	v.live.Observe()
}

// Refresh returns a command that re-runs this view's diff, or nil when no
// re-read is owed.
//
// Only working-tree targets refresh. A commit and a range diff are immutable
// once written: `c:abc123` renders the same bytes forever, so re-running it
// would spend six subprocesses to produce the content already on screen. They
// are still worth watching, because the same watcher serves every tab in a
// pane, but they never re-read.
func (v *View) Refresh(workdir, baseRef, workspaceID string, suppressed bool) tea.Cmd {
	if v == nil || v.Target.Kind != TargetWorkingTree {
		return nil
	}
	// Nothing loaded yet, or a load already running. The host's own load path
	// owns both, and stacking snapshot loads is precisely what the single
	// in-flight slot exists to prevent.
	if v.State == LoadStateUnknown || v.State == LoadStateLoading {
		return nil
	}
	if !v.live.Begin(suppressed) {
		return nil
	}
	// Deliberately not setting State to LoadStateLoading: the pane keeps showing
	// the diff it has until a different one arrives. Flipping to a loading state
	// here is what would make an unchanged refresh visible.
	cmd := LoadSnapshotCmdAt(workdir, baseRef, workspaceID, v.Epoch, v.Target.Identity())
	return func() tea.Msg {
		msg, _ := cmd().(SnapshotMsg)
		msg.Refresh = true
		return msg
	}
}

// RefreshPending reports whether a re-read is owed but has not started.
func (v *View) RefreshPending() bool {
	if v == nil {
		return false
	}
	return v.live.Pending()
}

// applyRefresh installs a snapshot produced by [View.Refresh], reporting
// whether anything changed.
func (v *View) applyRefresh(msg SnapshotMsg, workdir, workspaceID string) (tea.Cmd, bool) {
	stillOwed := v.live.Done()
	defer func() {
		if stillOwed {
			v.live.Observe()
		}
	}()

	// A failed refresh keeps the diff already on screen. Losing a review to a
	// transient git failure — an index.lock held by another command is the
	// common one, and it happens constantly during a rebase — would be worse
	// than being one refresh stale, and the next signal retries.
	if msg.Err != nil || msg.Snapshot == nil {
		return nil, false
	}
	if !v.live.Changed(fingerprintSnapshot(msg.Snapshot)) {
		return nil, false
	}

	selected := v.selectedFilePath()

	v.Snapshot = msg.Snapshot
	v.Error = ""
	v.State = msg.Snapshot.State
	v.ApplySnapshot()
	v.restoreSelection(selected)

	return v.LoadSelectedCommit(workdir, workspaceID), true
}

// selectedFilePath is the path of the file under the cursor, or "" when the
// cursor is on a commit or on nothing.
func (v *View) selectedFilePath() string {
	if v.Cursor < 0 || v.Cursor >= len(v.Files) {
		return ""
	}
	return v.Files[v.Cursor].Path
}

// restoreSelection re-points the cursor at the file it was on before the
// refresh, and keeps the diff scroll if it is still the same file.
//
// Cursor is a positional index into files-then-commits. That is fine for
// keyboard navigation and wrong for a refresh: stage the file above the one
// being read and every index shifts by one, so a purely clamped cursor silently
// selects a different file and the reader's scroll position becomes nonsense.
// Matching by path is what makes the selection survive.
func (v *View) restoreSelection(path string) {
	if path == "" {
		v.ClampCursor()
		return
	}
	for i, f := range v.Files {
		if f.Path == path {
			v.Cursor = i
			// Same file, same reading position. DiffScroll is clamped by
			// ClampScroll below in case the file's hunks shrank.
			v.ClampCursor()
			v.ClampScroll()
			return
		}
	}
	// The file left the diff entirely — it was committed, discarded or reverted.
	// There is no position to keep, so clamp and start the new selection at the
	// top rather than at a stale offset into a different file's patch.
	v.DiffScroll = 0
	v.ClampCursor()
	v.ClampScroll()
}

// fingerprintSnapshot reduces a snapshot to a change detector.
//
// Only the parts that reach the screen are included. BaseRef and the untracked
// accounting are load bookkeeping, not content, and folding them in would make
// the pane repaint when nothing the user can see moved.
func fingerprintSnapshot(s *Snapshot) string {
	if s == nil {
		return ""
	}
	return livewatch.Fingerprint(struct {
		State       LoadState
		WorkingTree string
		Commits     []CommitInfo
		Committed   string
		Uncommitted string
	}{s.State, s.WorkingTree, s.Commits, s.AggregateCommitted, s.AggregateUncommitted})
}
