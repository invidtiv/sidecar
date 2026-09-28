package managedtarget

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/marcus/sidecar/internal/shellliveness"
	"github.com/marcus/sidecar/internal/workspaceops"
)

// The evidence half of rootless worktree session detection (td-0b90da). The
// decision is shellliveness.PlanWorktreeOrphans; this file only asks git and
// tmux what they know, once each: one `git worktree list` per project and one
// `tmux list-sessions` for the whole pass.

// sessionListingFormat carries each session's start directory, which is the
// proof a worktree session lives in the root it is named after.
const sessionListingFormat = "#{session_name}\t#{session_path}"

// listTmuxSessions is indirected so tests can supply a listing without a tmux
// server.
var listTmuxSessions = func(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, "tmux", "list-sessions", "-F", sessionListingFormat).Output()
	return string(out), err
}

// listWorktreeStates is indirected for the same reason.
var listWorktreeStates = workspaceops.ListWorktreeStates

// ObserveWorktreeOrphans gathers the evidence for projects. A tmux server with
// no sessions is an empty listing, not a failure: `no server running` means
// there is nothing live to be orphaned.
func ObserveWorktreeOrphans(ctx context.Context, projects []Project) shellliveness.OrphanObservation {
	if ctx == nil {
		ctx = context.Background()
	}
	obs := shellliveness.OrphanObservation{SessionPrefix: workspaceops.WorktreeSessionPrefix}
	for _, proj := range projects {
		if strings.TrimSpace(proj.Path) == "" {
			continue
		}
		projectRoot := workspaceops.CanonicalWorkPath(proj.Path)
		inv := shellliveness.WorktreeInventory{ProjectRoot: projectRoot}
		if states, err := listWorktreeStates(ctx, proj.Path); err == nil {
			inv.Answered = true
			for _, state := range states {
				if state.Bare || state.Path == "" {
					continue
				}
				inv.Worktrees = append(inv.Worktrees, shellliveness.ListedWorktree{
					Path: workspaceops.CanonicalWorkPath(state.Path), Prunable: state.Prunable,
					Sessions: workspaceops.WorktreeSessionNames(state.Path, ""),
				})
			}
		}
		obs.Inventories = append(obs.Inventories, inv)
		for _, root := range proj.Worktrees {
			if strings.TrimSpace(root) == "" {
				continue
			}
			obs.Registered = append(obs.Registered, shellliveness.RegisteredRoot{
				ProjectKey: proj.Key, ProjectRoot: projectRoot,
				Root: workspaceops.CanonicalWorkPath(root), Sessions: workspaceops.WorktreeSessionNames(root, ""),
			})
		}
	}

	out, err := listTmuxSessions(ctx)
	if err != nil {
		if !noTmuxServer(err) {
			obs.ListingFailed = true
		}
		return obs
	}
	for _, line := range strings.Split(out, "\n") {
		name, path, _ := strings.Cut(strings.TrimRight(line, "\r"), "\t")
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		session := shellliveness.TmuxSession{Name: name}
		if path = strings.TrimSpace(path); path != "" && filepath.IsAbs(path) {
			session.Path = workspaceops.CanonicalWorkPath(path)
			session.PathMissing = pathMissingWithParent(path)
			session.NameFromPath = slices.Contains(workspaceops.WorktreeSessionNames(path, ""), name)
		}
		obs.Sessions = append(obs.Sessions, session)
	}
	return obs
}

// WorktreeOrphans is ObserveWorktreeOrphans followed by the decision.
func WorktreeOrphans(ctx context.Context, projects []Project) shellliveness.OrphanPlan {
	return shellliveness.PlanWorktreeOrphans(ObserveWorktreeOrphans(ctx, projects))
}

// pathMissingWithParent reports that path is gone while the directory that
// held it is not. A missing parent is what an unmounted volume looks like, and
// that says nothing about whether anyone removed a worktree.
func pathMissingWithParent(path string) bool {
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		return false
	}
	info, err := os.Stat(filepath.Dir(path))
	return err == nil && info.IsDir()
}

func noTmuxServer(err error) bool {
	var exitErr *exec.ExitError
	message := err.Error()
	if errors.As(err, &exitErr) {
		message += " " + string(exitErr.Stderr)
	}
	return strings.Contains(message, "no server running") ||
		(strings.Contains(message, "error connecting to") && strings.Contains(message, "No such file or directory"))
}
