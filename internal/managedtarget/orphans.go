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

// paneListingFormat carries, per pane, the session's start directory (the
// proof a worktree session lives in the root it is named after) and the pane's
// current directory (the proof no one is working somewhere that still exists).
// One `list-panes -a` answers both for every session at once.
const paneListingFormat = "#{session_name}\t#{session_path}\t#{pane_current_path}"

// listTmuxSessions is indirected so tests can supply a listing without a tmux
// server.
var listTmuxSessions = func(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, "tmux", "list-panes", "-a", "-F", paneListingFormat).Output()
	return string(out), err
}

// listWorktreeStates is indirected for the same reason.
var listWorktreeStates = workspaceops.ListWorktreeStates

// ObserveOptions says how much evidence a caller needs.
type ObserveOptions struct {
	// WantRoots asks for root verdicts even when no session is suspect, for a
	// caller (`shell list`) that marks shells rather than sessions.
	WantRoots bool
}

// ObserveWorktreeOrphans gathers the evidence for projects. A tmux server with
// no sessions is an empty listing, not a failure: `no server running` means
// there is nothing live to be orphaned.
//
// Git is asked only when there is something to judge. Every verdict needs a
// directory that is gone, so a pass with no live worktree session in a missing
// directory (and no caller asking for root verdicts) never spawns git at all.
// That keeps the common `agent list` free of a git process per project.
func ObserveWorktreeOrphans(ctx context.Context, projects []Project, opts ObserveOptions) shellliveness.OrphanObservation {
	if ctx == nil {
		ctx = context.Background()
	}
	obs := shellliveness.OrphanObservation{SessionPrefix: workspaceops.WorktreeSessionPrefix}
	anyRootMissing := false
	for _, proj := range projects {
		if strings.TrimSpace(proj.Path) == "" {
			continue
		}
		projectRoot := workspaceops.CanonicalWorkPath(proj.Path)
		for _, root := range proj.Worktrees {
			if strings.TrimSpace(root) == "" {
				continue
			}
			missing := pathMissingWithParent(root)
			anyRootMissing = anyRootMissing || missing
			obs.Registered = append(obs.Registered, shellliveness.RegisteredRoot{
				ProjectKey: proj.Key, ProjectRoot: projectRoot, ProjectPath: proj.Path,
				Root: workspaceops.CanonicalWorkPath(root), Sessions: workspaceops.WorktreeSessionNames(root, ""),
				Missing: missing,
			})
		}
	}

	suspect := false
	out, err := listTmuxSessions(ctx)
	if err != nil {
		if !noTmuxServer(err) {
			obs.ListingFailed = true
		}
	} else {
		index := map[string]int{}
		for _, line := range strings.Split(out, "\n") {
			fields := strings.SplitN(strings.TrimRight(line, "\r"), "\t", 3)
			name := strings.TrimSpace(fields[0])
			if name == "" {
				continue
			}
			i, seen := index[name]
			if !seen {
				session := shellliveness.TmuxSession{Name: name}
				if len(fields) > 1 {
					if path := strings.TrimSpace(fields[1]); path != "" && filepath.IsAbs(path) {
						session.Path = workspaceops.CanonicalWorkPath(path)
						session.PathMissing = pathMissingWithParent(path)
						session.NameFromPath = slices.Contains(workspaceops.WorktreeSessionNames(path, ""), name)
					}
				}
				i = len(obs.Sessions)
				index[name] = i
				obs.Sessions = append(obs.Sessions, session)
			}
			if len(fields) > 2 {
				if pane := strings.TrimSpace(fields[2]); pane != "" && directoryExists(pane) {
					obs.Sessions[i].PaneInExistingDir = true
				}
			}
		}
		for _, session := range obs.Sessions {
			if session.PathMissing && !session.PaneInExistingDir && strings.HasPrefix(session.Name, workspaceops.WorktreeSessionPrefix) {
				suspect = true
			}
		}
	}

	needRoots := opts.WantRoots && anyRootMissing
	if !suspect && !needRoots {
		return obs
	}
	for _, proj := range projects {
		if strings.TrimSpace(proj.Path) == "" {
			continue
		}
		// A project the deadline skipped is still recorded, unanswered. The
		// decision's "every listing answered" rule can only see inventories
		// that are present, so leaving it out would read as an answer.
		inv := shellliveness.WorktreeInventory{ProjectRoot: workspaceops.CanonicalWorkPath(proj.Path)}
		if ctx.Err() != nil {
			obs.Inventories = append(obs.Inventories, inv)
			continue
		}
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
	}
	return obs
}

// WorktreeOrphans is ObserveWorktreeOrphans followed by the decision.
func WorktreeOrphans(ctx context.Context, projects []Project, opts ObserveOptions) shellliveness.OrphanPlan {
	return shellliveness.PlanWorktreeOrphans(ObserveWorktreeOrphans(ctx, projects, opts))
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

func directoryExists(path string) bool {
	info, err := os.Stat(path)
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
