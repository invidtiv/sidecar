package workspaceops

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/projectdir"
	"github.com/marcus/sidecar/internal/shellstate"
	"github.com/marcus/sidecar/internal/tmuxenv"
)

// Deleting a worktree used to leave the shells that lived in it behind in
// shells.json (td-f017b9). The rows survived their directory: every one of
// them named a path that no longer existed, and nothing ever cleaned them up.
// The liveness reaper will not, by design — it refuses to close an entry whose
// session it has never observed alive, so a shell whose session died with the
// worktree is exactly the case it leaves alone.
//
// So the delete has to forget them itself, and both surfaces have to do it the
// same way. That is what this file is: the state-free "which shells are rooted
// here" rule, plus one operation that applies it.

// PathRootedIn reports whether path lies at or beneath root.
//
// The comparison is deliberately stricter than a string prefix. Worktree
// directories are siblings with related names — a repo with `feature` and
// `feature-2` checked out side by side is ordinary — and `strings.HasPrefix`
// would let deleting `feature` sweep up every shell in `feature-2`. The test is
// therefore made on path components: equal, or separated by a path separator.
//
// Both sides are canonicalised (absolute, symlink-resolved where the path still
// exists, cleaned) so that /tmp and /private/tmp on macOS, or a trailing
// separator, do not decide the answer. Resolution is best-effort: by the time
// a caller asks, the worktree may already be gone.
//
// A parent never matches a child: a shell in the repo root is not rooted in one
// of its worktrees, and deleting that worktree must not touch it.
func PathRootedIn(path, root string) bool {
	if strings.TrimSpace(path) == "" || strings.TrimSpace(root) == "" {
		return false
	}
	p := canonicalWorkPath(path)
	r := canonicalWorkPath(root)
	if p == r {
		return true
	}
	// filepath.Rel answers "how do I get from root to path"; anything that has
	// to climb out with ".." is not inside.
	rel, err := filepath.Rel(r, p)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// canonicalWorkPath makes a path absolute and resolves symlinks as far as the
// filesystem allows.
//
// Resolving only the whole path is not enough. On macOS the temp and worktree
// roots sit under /var, a symlink to /private/var, so an existing directory
// canonicalises to /private/var/... while a path one level deeper that no
// longer exists — a shell's recorded subdirectory after the worktree is gone —
// stays at /var/.... Comparing those two says "not inside" when they plainly
// are. So the deepest existing ancestor is resolved and the remaining
// components are re-attached unresolved, which gives both sides of a
// comparison the same prefix whether or not they still exist.
func canonicalWorkPath(path string) string {
	return CanonicalWorkPath(path)
}

// CanonicalWorkPath is canonicalWorkPath for callers outside this package that
// compare a path which may no longer exist (a removed worktree) against one git
// reports canonically.
func CanonicalWorkPath(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	path = filepath.Clean(path)

	var trailing []string
	current := path
	for {
		if resolved, err := filepath.EvalSymlinks(current); err == nil {
			for i := len(trailing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, trailing[i])
			}
			return filepath.Clean(resolved)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return path
		}
		trailing = append(trailing, filepath.Base(current))
		current = parent
	}
}

// ShellsRootedIn selects the definitions whose recorded WorkDir lies at or
// beneath root.
//
// A definition with an empty WorkDir is never selected. Manifests written
// before td-4819be did not record one, and an entry that does not say where it
// lives is not evidence that it lives here — guessing would mean deleting one
// worktree could forget a shell in a different one. Those entries stay, and the
// liveness reaper handles them once their session has been observed.
//
// This is a pure function over the manifest rows so the rule can be tested, and
// reused, without a repository, a tmux server, or a state directory.
func ShellsRootedIn(defs []shellstate.Definition, root string) []shellstate.Definition {
	if strings.TrimSpace(root) == "" {
		return nil
	}
	var out []shellstate.Definition
	for _, def := range defs {
		if strings.TrimSpace(def.WorkDir) == "" {
			continue
		}
		if PathRootedIn(def.WorkDir, root) {
			out = append(out, def)
		}
	}
	return out
}

// WorktreeShell is an affected managed shell and the project manifest owning it.
type WorktreeShell struct {
	ProjectRoot string
	Definition  shellstate.Definition
	CanClose    bool
	projectDir  string
}

// ListShellsInWorktree finds shells rooted in the removed directory across all
// registered projects. The worktree's project does not own every shell started
// inside it: an agent may have opened it from another project's workspace.
func ListShellsInWorktree(worktreePath string) ([]WorktreeShell, error) {
	return ListShellsInWorktreeWithStateDir(config.StateDir(), worktreePath)
}

// ListShellsInWorktreeWithStateDir is the read-only inventory for an explicit
// state directory, shared by deletion planning and execution.
func ListShellsInWorktreeWithStateDir(stateDir, worktreePath string) ([]WorktreeShell, error) {
	if strings.TrimSpace(worktreePath) == "" {
		return nil, nil
	}
	projects, err := projectdir.ListRegisteredWithBase(stateDir)
	if err != nil {
		return nil, err
	}
	var selected []WorktreeShell
	for _, project := range projects {
		defs, err := shellstate.ListAtPath(filepath.Join(project.Dir, "shells.json"))
		if err != nil {
			return nil, fmt.Errorf("read shells for project %s: %w", project.Registered, err)
		}
		for _, def := range ShellsRootedIn(defs, worktreePath) {
			canClose := def.Namespace == "" || CanonicalWorkPath(def.Namespace) == CanonicalWorkPath(tmuxenv.Namespace())
			selected = append(selected, WorktreeShell{ProjectRoot: project.Registered, Definition: def, CanClose: canClose, projectDir: project.Dir})
		}
	}
	return selected, nil
}

// ForgetShellsInWorktree closes every managed shell rooted in worktreePath on
// this tmux server, across all projects: the manifest row and tmux session go.
// Shells recorded on another socket stay recorded and produce a warning.
//
// Killing the sessions is the point, not a side effect. A shell whose working
// directory has been deleted is orphaned in exactly the way td-a66836 fixed for
// the worktree's own session — a shell (usually an agent) left running in a
// directory that no longer exists, invisible in the UI because its workspace is
// gone, and unreachable except by knowing the tmux name. Removing only the
// manifest row would make that *worse*: the row is the last thing that records
// the session exists at all. So this goes through DeleteManagedShell, which
// removes the durable identity first and then closes the session, and which is
// the same operation the Workspaces surfaces already use to close a shell by
// hand.
//
// Errors are collected rather than fatal: one shell that will not close must
// not leave the other shells' rows behind.
//
// It is reached through DeleteWorktree rather than called beside it: the
// forgetting is part of removing a worktree, not a step a caller has to
// remember, which is what stops one surface growing it and another not.
func ForgetShellsInWorktree(_ string, worktreePath string) error {
	selected, err := ListShellsInWorktree(worktreePath)
	if err != nil {
		return err
	}
	var errs []error
	for _, shell := range selected {
		def := shell.Definition
		if !shell.CanClose {
			errs = append(errs, fmt.Errorf("shell %s in project %s is on another tmux socket; retained its record and did not close it", def.TmuxName, shell.ProjectRoot))
			continue
		}
		// Use the inventoried directory directly. Resolving the root again can
		// select another manifest when older installs have duplicate registrations.
		if err := deleteManagedShellForForget(shell.projectDir, def.TmuxName, def.Namespace, def.CreatedAt); err != nil {
			errs = append(errs, fmt.Errorf("shell %s in project %s: %w", def.TmuxName, shell.ProjectRoot, err))
		}
	}
	return errors.Join(errs...)
}

// deleteManagedShellForForget is indirected so tests can exercise the selection
// rule and the ordering without a tmux server.
var deleteManagedShellForForget = func(projectDir, sessionName, namespace string, observedAt time.Time) error {
	return deleteManagedShellAtPath(projectDir, sessionName, namespace, func(string) error {
		return shellstate.RemoveIfUnchangedAtPath(filepath.Join(projectDir, "shells.json"), shellstate.Identity{TmuxName: sessionName, Namespace: namespace}, observedAt)
	})
}

// forgetShellsInWorktree is indirected so DeleteWorktree's tests can exercise
// the removal ordering without a manifest or a tmux server.
var forgetShellsInWorktree = ForgetShellsInWorktree

// pruneOrphanedManagedShells is the prune-only cleanup. Recorded ownership
// selects shells for an explicit worktree delete, but cannot prove their live
// directories disappeared: a dedicated shell may have followed a moved checkout.
// Refuse the entire prune before any teardown if a selected shell is live or
// unknown. Explicit DeleteWorktree retains its intentional closure semantics.
func pruneOrphanedManagedShells(ctx context.Context, projectRoot, root string) error {
	type selectedShell struct {
		project string
		def     shellstate.Definition
		id      string
	}
	var selected []selectedShell
	for _, project := range projectdir.LookupEquivalent(projectRoot) {
		defs, err := shellstate.ListAtPath(filepath.Join(project.Dir, "shells.json"))
		if err != nil {
			return err
		}
		for _, def := range ShellsRootedIn(defs, root) {
			if def.CreatedAt.IsZero() {
				return fmt.Errorf("%w: shell %s has no record incarnation", ErrOrphanChanged, def.TmuxName)
			}
			if def.Namespace != "" && CanonicalWorkPath(def.Namespace) != CanonicalWorkPath(tmuxenv.Namespace()) {
				return fmt.Errorf("%w: cannot verify shell %s on another tmux socket", ErrOrphanChanged, def.TmuxName)
			}
			state, err := readOrphanSession(ctx, def.TmuxName)
			if errors.Is(err, errOrphanSessionGone) {
				selected = append(selected, selectedShell{project: project.Registered, def: def})
				continue
			}
			if err != nil {
				return fmt.Errorf("%w: cannot verify shell %s: %v", ErrOrphanChanged, def.TmuxName, err)
			}
			if state.ID == "" || !PathRootedIn(state.Path, root) || len(state.PanePaths) == 0 {
				return fmt.Errorf("%w: shell %s no longer has verified worktree directory evidence", ErrOrphanChanged, def.TmuxName)
			}
			for _, pane := range state.PanePaths {
				if !PaneDirectoryMissing(pane) {
					return fmt.Errorf("%w: shell %s pane directory %q is not known to be missing", ErrOrphanChanged, def.TmuxName, pane)
				}
			}
			selected = append(selected, selectedShell{project: project.Registered, def: def, id: state.ID})
		}
	}
	if !rootStillMissing(root) {
		return fmt.Errorf("%w: %s exists again", ErrOrphanChanged, root)
	}
	// Carry only the verified snapshot into teardown. Re-listing would include
	// unverified new rows; killing by name would reach replacement sessions.
	for _, shell := range selected {
		if err := ForgetManagedShell(shell.project, shell.def.TmuxName, shell.def.Namespace, shell.def.CreatedAt); err != nil {
			if errors.Is(err, shellstate.ErrShellChanged) {
				return fmt.Errorf("%w: %s: %v", ErrOrphanChanged, shell.def.TmuxName, err)
			}
			return err
		}
		if shell.id != "" {
			if err := killSessionByID(ctx, shell.id); err != nil {
				return err
			}
		}
	}
	return nil
}
