package workspaceops

import (
	"errors"
	"path/filepath"
	"strings"

	"github.com/marcus/sidecar/internal/projectdir"
	"github.com/marcus/sidecar/internal/shellstate"
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

// ForgetShellsInWorktree closes every managed shell of projectRoot that is
// rooted in worktreePath: the manifest row goes, and so does the tmux session.
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
func ForgetShellsInWorktree(projectRoot, worktreePath string) error {
	if strings.TrimSpace(projectRoot) == "" || strings.TrimSpace(worktreePath) == "" {
		return nil
	}
	projectDir, err := projectdir.Resolve(projectRoot)
	if err != nil {
		return err
	}
	defs, err := shellstate.ListAtPath(filepath.Join(projectDir, "shells.json"))
	if err != nil {
		return err
	}
	var errs []error
	for _, def := range ShellsRootedIn(defs, worktreePath) {
		if err := deleteManagedShellForForget(projectRoot, def.TmuxName, def.Namespace); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// deleteManagedShellForForget is indirected so tests can exercise the selection
// rule and the ordering without a tmux server.
var deleteManagedShellForForget = DeleteManagedShell

// forgetShellsInWorktree is indirected so DeleteWorktree's tests can exercise
// the removal ordering without a manifest or a tmux server.
var forgetShellsInWorktree = ForgetShellsInWorktree
