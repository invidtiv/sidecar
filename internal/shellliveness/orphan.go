package shellliveness

import (
	"path/filepath"
	"sort"
	"strings"
)

// Rootless worktree sessions (td-0b90da).
//
// The reap in reap.go handles a session that died. This file handles the
// opposite failure: a worktree session (sidecar-ws-…) that is still running
// after the worktree it was started in has gone. Git owns worktrees, so
// removing one with plain `git worktree remove` is a reasonable thing for a
// person or an agent to do, and Sidecar is never told. The session is not dead,
// so the reap never looks at it. It is live but rootless, and until this
// existed nothing reconciled it against git's own inventory.
//
// Like the reap, the decision is state-free: it holds nothing, takes every
// piece of evidence as an argument, and returns a decision rather than a
// command. The collector (internal/managedtarget) gathers the evidence, and
// every surface — `sidecar agent list`, `sidecar shell list`,
// `sidecar worktree prune-sessions`, and a headless `sidecar host serve` if it
// ever needs it — asks the same questions of the same functions.
//
// The asymmetry is the reap's too. A missed orphan costs one idle row. A false
// orphan costs a live agent session killed underneath someone. So every
// ambiguous signal falls toward "not an orphan":
//
//   - A project whose `git worktree list` failed has no verdicts. A failed
//     listing says nothing about any one worktree, and an unreadable repository
//     is also what an unmounted volume looks like.
//   - A root is judged against every inventory that answered, not only its
//     owner's. A root another repository still lists is not gone.
//   - A session name that a worktree git still lists would also produce is
//     never an orphan. Session names are derived from a directory's base name,
//     so `repo-foo` recreated at another path reuses the name of the removed
//     one, and that session belongs to the new checkout.
//   - A session must prove it lives in the root. Its tmux start directory has
//     to be at or beneath the removed root, so a name that happens to match is
//     not enough.
//   - A root that still exists is never an orphan, whatever git says. A
//     directory re-cloned over a removed worktree, or a project that lost its
//     .git inside a parent repository, answers git with a listing that omits a
//     perfectly live checkout. Only a root whose directory is gone while its
//     parent is not has been removed; a missing parent is an unmounted volume.
//   - An owner's listing only counts when it includes the owner's own root. A
//     listing that does not is some other repository answering.
//   - A failed tmux listing produces nothing, for the same reason as the reap.

// OrphanReason says why a worktree root no longer backs its sessions. Empty
// means it does, or that the evidence cannot say.
type OrphanReason string

const (
	// OrphanNotListed: the owning repository answered and no answering
	// repository lists the root. This is `git worktree remove`.
	OrphanNotListed OrphanReason = "worktree_not_in_git"
	// OrphanPrunable: git still lists the root but marks it prunable, because
	// its directory is gone. This is `rm -rf` without `git worktree prune`.
	OrphanPrunable OrphanReason = "worktree_prunable"
	// OrphanDirectoryMissing: a worktree session no registered project
	// accounts for, whose own start directory is gone. Its name is derived
	// from that directory, which is the whole of its identity.
	OrphanDirectoryMissing OrphanReason = "worktree_directory_missing"
)

// ListedWorktree is one entry of `git worktree list --porcelain`.
type ListedWorktree struct {
	// Path is canonical: absolute and symlink-resolved.
	Path     string
	Prunable bool
	// Sessions are the tmux names Sidecar would give this worktree's session.
	// A live session under one of them belongs to this worktree.
	Sessions []string
}

// WorktreeInventory is one repository's worktree listing.
type WorktreeInventory struct {
	ProjectRoot string
	// Answered reports that git produced a listing. False is not "empty": it is
	// no evidence at all.
	Answered  bool
	Worktrees []ListedWorktree
}

// RegisteredRoot is a worktree Sidecar created and recorded for a project.
type RegisteredRoot struct {
	ProjectKey, ProjectRoot string
	// ProjectPath is the project path exactly as registered. ProjectRoot is
	// canonical for comparison; ProjectPath is what state lookups match on.
	ProjectPath string
	// Root is canonical, resolved as far as the filesystem still allows.
	Root string
	// Sessions are the tmux names Sidecar may have started this worktree's
	// session under, most canonical first.
	Sessions []string
	// Missing reports that Root is gone while its parent is not. A root that
	// exists, or whose parent is also gone, gets no verdict.
	Missing bool
}

// TmuxSession is one entry of the session listing.
type TmuxSession struct {
	Name string
	// Path is the session's start directory (#{session_path}), canonical.
	// Empty means tmux did not say, and an unknown directory proves nothing.
	Path string
	// PathMissing reports that Path no longer exists while its parent still
	// does. The parent check is what keeps an unmounted volume, where every
	// path is missing at once, from reading as a removed worktree.
	PathMissing bool
	// NameFromPath reports that Name is one of the names Sidecar derives from
	// Path, which is how a session that no registered project accounts for
	// proves it is a worktree session for that directory.
	NameFromPath bool
}

// OrphanObservation is everything one pass knows.
type OrphanObservation struct {
	Inventories []WorktreeInventory
	Registered  []RegisteredRoot
	Sessions    []TmuxSession
	// ListingFailed reports that the tmux session listing errored.
	ListingFailed bool
	// SessionPrefix is the worktree session prefix (sidecar-ws-). Sessions
	// outside it are never judged.
	SessionPrefix string
}

// OrphanSession is one live worktree session with no worktree behind it.
type OrphanSession struct {
	// ProjectKey, ProjectRoot and ProjectPath are empty for an
	// OrphanDirectoryMissing session no registered project accounts for.
	ProjectKey, ProjectRoot, ProjectPath string
	// Root is the removed worktree the session was started in.
	Root        string
	Session     string
	SessionPath string
	Reason      OrphanReason
}

// OrphanPlan is what one pass's evidence supports.
type OrphanPlan struct {
	Orphans []OrphanSession
	// Roots maps every registered root with a verdict to its reason, whether or
	// not a session is running in it. `shell list` uses it to mark shells
	// rooted in a removed worktree.
	Roots map[string]OrphanReason
	// Skipped names the guard that stopped the pass. Empty when it ran.
	Skipped string
}

// RootOrphanReason judges one registered root against every inventory. It is
// the git half of the decision, usable without a tmux listing.
func RootOrphanReason(root RegisteredRoot, inventories []WorktreeInventory) OrphanReason {
	if strings.TrimSpace(root.Root) == "" || !root.Missing {
		return ""
	}
	ownerAnswered := false
	prunable := false
	for _, inv := range inventories {
		if !inv.Answered {
			continue
		}
		if SamePath(inv.ProjectRoot, root.ProjectRoot) && listsPath(inv, inv.ProjectRoot) {
			ownerAnswered = true
		}
		for _, wt := range inv.Worktrees {
			if !SamePath(wt.Path, root.Root) {
				continue
			}
			if !wt.Prunable {
				return ""
			}
			prunable = true
		}
	}
	switch {
	case !ownerAnswered:
		return ""
	case prunable:
		return OrphanPrunable
	default:
		return OrphanNotListed
	}
}

// PlanWorktreeOrphans names every live worktree session whose worktree is gone.
func PlanWorktreeOrphans(obs OrphanObservation) OrphanPlan {
	plan := OrphanPlan{Roots: map[string]OrphanReason{}}
	for _, root := range obs.Registered {
		if reason := RootOrphanReason(root, obs.Inventories); reason != "" {
			plan.Roots[root.Root] = reason
		}
	}
	if obs.ListingFailed {
		plan.Skipped = "tmux listing failed"
		return plan
	}
	if strings.TrimSpace(obs.SessionPrefix) == "" {
		plan.Skipped = "no worktree session prefix"
		return plan
	}

	// Names a worktree git still lists would produce belong to that worktree,
	// whatever else they might also be derived from.
	claimed := map[string]bool{}
	for _, inv := range obs.Inventories {
		if !inv.Answered {
			continue
		}
		for _, wt := range inv.Worktrees {
			if wt.Prunable {
				continue
			}
			for _, name := range wt.Sessions {
				claimed[name] = true
			}
		}
	}
	live := make(map[string]TmuxSession, len(obs.Sessions))
	for _, session := range obs.Sessions {
		if strings.HasPrefix(session.Name, obs.SessionPrefix) {
			live[session.Name] = session
		}
	}

	decided := map[string]bool{}
	for _, root := range obs.Registered {
		reason := plan.Roots[root.Root]
		for _, name := range root.Sessions {
			session, ok := live[name]
			if !ok || decided[name] || claimed[name] {
				continue
			}
			// Every name a registered root could produce is decided here, even
			// when it is not an orphan, so the unattributed pass below cannot
			// reach a different answer about it.
			decided[name] = true
			if reason == "" || session.Path == "" || !PathWithin(session.Path, root.Root) {
				continue
			}
			plan.Orphans = append(plan.Orphans, OrphanSession{
				ProjectKey: root.ProjectKey, ProjectRoot: root.ProjectRoot, ProjectPath: root.ProjectPath, Root: root.Root,
				Session: name, SessionPath: session.Path, Reason: reason,
			})
		}
	}
	for _, session := range obs.Sessions {
		name := session.Name
		if !strings.HasPrefix(name, obs.SessionPrefix) || decided[name] || claimed[name] {
			continue
		}
		decided[name] = true
		if session.Path == "" || !session.PathMissing || !session.NameFromPath {
			continue
		}
		// A missing directory inside a registered root is that root's
		// question, and it was not an orphan there (its owner did not answer,
		// or git still lists it). The directory being gone does not overrule
		// that.
		if withinRegistered(session.Path, obs.Registered) {
			continue
		}
		plan.Orphans = append(plan.Orphans, OrphanSession{
			Root: session.Path, Session: name, SessionPath: session.Path, Reason: OrphanDirectoryMissing,
		})
	}
	sort.SliceStable(plan.Orphans, func(i, j int) bool {
		a, b := plan.Orphans[i], plan.Orphans[j]
		if a.ProjectKey != b.ProjectKey {
			return a.ProjectKey < b.ProjectKey
		}
		return a.Session < b.Session
	})
	return plan
}

func listsPath(inv WorktreeInventory, path string) bool {
	for _, wt := range inv.Worktrees {
		if SamePath(wt.Path, path) {
			return true
		}
	}
	return false
}

func withinRegistered(path string, roots []RegisteredRoot) bool {
	for _, root := range roots {
		if PathWithin(path, root.Root) {
			return true
		}
	}
	return false
}

// SamePath compares two already-canonical paths.
func SamePath(a, b string) bool {
	if strings.TrimSpace(a) == "" || strings.TrimSpace(b) == "" {
		return false
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

// PathWithin reports whether the canonical path lies at or beneath the
// canonical root, component-wise: `feature-2` is not within `feature`.
func PathWithin(path, root string) bool {
	if strings.TrimSpace(path) == "" || strings.TrimSpace(root) == "" {
		return false
	}
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
