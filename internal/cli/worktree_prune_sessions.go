package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/marcus/sidecar/internal/managedtarget"
	"github.com/marcus/sidecar/internal/shellliveness"
	"github.com/marcus/sidecar/internal/shellstate"
	"github.com/marcus/sidecar/internal/workspaceops"
)

// `sidecar worktree prune-sessions` (td-0b90da): the non-interactive way out
// for worktree sessions left running after their worktree was removed with
// plain git. Before it, the only exit was a raw `tmux kill-session` per orphan
// on the default server: `shell delete` refuses sidecar-ws-… sessions and
// `worktree delete` refuses a worktree that is already gone.
//
// The decision is shellliveness.PlanWorktreeOrphans and the close is
// workspaceops.PruneOrphanedWorktreeSession. This file is the binding: scope,
// rendering, and exit codes.

const (
	pruneStatusPlanned = "planned"
	pruneStatusPruned  = "pruned"

	pruneResultClosed      = "closed"
	pruneResultAlreadyGone = "already_gone"
	pruneResultChanged     = "changed"
	pruneResultFailed      = "failed"
)

type pruneSessionsDocument struct {
	Status  string              `json:"status"`
	Orphans []pruneSessionsItem `json:"orphans"`
	// Skipped names the guard that stopped observation, when one did. An empty
	// orphan list with a Skipped reason is "could not look", not "found none".
	Skipped string `json:"skipped,omitempty"`
}

type pruneSessionsItem struct {
	Project     string   `json:"project,omitempty"`
	ProjectRoot string   `json:"projectRoot,omitempty"`
	Root        string   `json:"root"`
	Session     string   `json:"session"`
	SessionPath string   `json:"sessionPath"`
	Reason      string   `json:"reason"`
	Shells      []string `json:"shells,omitempty"`
	Result      string   `json:"result,omitempty"`
	Error       string   `json:"error,omitempty"`
}

func runWorktreePruneSessions(env Env, args []string) int {
	cmd := RootCommand().FindSubcommand("worktree").FindSubcommand("prune-sessions")
	help := RenderHelp(cmd)
	usage := newUsageReporter(env, wantsJSON(args), help)

	jsonOutput, planOnly, yes := false, false, false
	projectFlag := ""
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case isHelp(arg):
			_, _ = fmt.Fprint(env.Stdout, help)
			return 0
		case arg == "--json":
			jsonOutput = true
		case arg == "--plan" || arg == "--dry-run":
			planOnly = true
		case arg == "--yes":
			yes = true
		case arg == "--project" || strings.HasPrefix(arg, "--project="):
			value, next, ok := takeFlagArg(arg, args, i, "--project")
			if !ok || strings.TrimSpace(value) == "" {
				return usage("--project requires a project name")
			}
			projectFlag = value
			i = next
		default:
			if strings.HasPrefix(arg, "-") {
				return usage("unknown option %q", arg)
			}
			return usage("worktree prune-sessions takes no positional arguments")
		}
	}
	if planOnly && yes {
		return usage("--yes cannot be combined with --plan or --dry-run")
	}
	if !planOnly && !yes {
		return usage("--yes is required to close sessions; use --plan or --dry-run to inspect them first")
	}

	ctx := env.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	projects, err := loadRegisteredProjects(env.StateDir)
	if err != nil {
		return emitWorktreeDeleteError(env, jsonOutput, "state", err.Error(), 1)
	}
	scope := ""
	if projectFlag != "" {
		project, err := matchProject(env.StateDir, projects, projectFlag, resolveProjectOnly)
		if err != nil {
			return emitWorktreeDeleteError(env, jsonOutput, "project", err.Error(), createDestExitCode(err))
		}
		scope = project.Key
	}

	plan := managedtarget.WorktreeOrphans(ctx, toManagedProjects(projects))
	doc := pruneSessionsDocument{Status: pruneStatusPlanned, Orphans: []pruneSessionsItem{}, Skipped: plan.Skipped}
	for _, orphan := range plan.Orphans {
		if scope != "" && orphan.ProjectKey != scope {
			continue
		}
		doc.Orphans = append(doc.Orphans, pruneSessionsItem{
			Project: orphan.ProjectKey, ProjectRoot: orphan.ProjectRoot, Root: orphan.Root,
			Session: orphan.Session, SessionPath: orphan.SessionPath, Reason: string(orphan.Reason),
			Shells: shellsRootedIn(projects, orphan.ProjectKey, orphan.Root),
		})
	}
	if plan.Skipped != "" && len(doc.Orphans) == 0 {
		return emitWorktreeDeleteError(env, jsonOutput, "inventory", "could not observe worktree sessions: "+plan.Skipped, 1)
	}

	if planOnly {
		if jsonOutput {
			return writeJSON(env, doc)
		}
		return writePruneSessionsPlan(env, doc)
	}

	doc.Status = pruneStatusPruned
	exit := 0
	for i := range doc.Orphans {
		item := &doc.Orphans[i]
		gone, err := workspaceops.PruneOrphanedWorktreeSession(ctx, workspaceops.OrphanedSessionPrune{
			ProjectRoot: item.ProjectRoot, Root: item.Root, Session: item.Session, SessionPath: item.SessionPath,
		})
		switch {
		case errors.Is(err, workspaceops.ErrOrphanChanged):
			item.Result, item.Error = pruneResultChanged, err.Error()
			if exit == 0 {
				exit = exitInputRejected
			}
		case err != nil:
			item.Result, item.Error = pruneResultFailed, err.Error()
			exit = 1
		case gone:
			item.Result = pruneResultAlreadyGone
		default:
			item.Result = pruneResultClosed
		}
	}
	if jsonOutput {
		if code := writeJSON(env, doc); code != 0 {
			return code
		}
		return exit
	}
	if len(doc.Orphans) == 0 {
		_, _ = fmt.Fprintln(env.Stdout, "No orphaned worktree sessions.")
		return exit
	}
	for _, item := range doc.Orphans {
		switch item.Result {
		case pruneResultClosed:
			_, _ = fmt.Fprintf(env.Stdout, "Closed %s (%s)\n", item.Session, item.Root)
		case pruneResultAlreadyGone:
			_, _ = fmt.Fprintf(env.Stdout, "Already gone: %s\n", item.Session)
		default:
			_, _ = fmt.Fprintf(env.Stdout, "Left %s: %s\n", item.Session, item.Error)
		}
	}
	return exit
}

func writePruneSessionsPlan(env Env, doc pruneSessionsDocument) int {
	if len(doc.Orphans) == 0 {
		_, _ = fmt.Fprintln(env.Stdout, "No orphaned worktree sessions.")
		return 0
	}
	_, _ = fmt.Fprintf(env.Stdout, "%d orphaned worktree session(s):\n", len(doc.Orphans))
	for _, item := range doc.Orphans {
		project := item.Project
		if project == "" {
			project = "(no project)"
		}
		_, _ = fmt.Fprintf(env.Stdout, "  %s  %s  %s  %s\n", item.Session, project, item.Reason, item.Root)
		for _, shell := range item.Shells {
			_, _ = fmt.Fprintf(env.Stdout, "      + shell %s\n", shell)
		}
	}
	_, _ = fmt.Fprintln(env.Stdout, "No changes made. Re-run with --yes to close these sessions.")
	return 0
}

// worktreeOrphanPlan observes every registered project. Callers narrow the
// result; they never narrow the observation.
func worktreeOrphanPlan(env Env) (shellliveness.OrphanPlan, error) {
	projects, err := loadRegisteredProjects(env.StateDir)
	if err != nil {
		return shellliveness.OrphanPlan{}, err
	}
	ctx := env.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return managedtarget.WorktreeOrphans(ctx, toManagedProjects(projects)), nil
}

func toManagedProjects(projects []registeredProject) []managedtarget.Project {
	converted := make([]managedtarget.Project, len(projects))
	for i, p := range projects {
		converted[i] = managedtarget.Project{Key: p.Key, Path: p.Path, Dir: p.Dir, Worktrees: p.Worktrees}
	}
	return converted
}

// shellsRootedIn names the managed shells a prune of root would also close.
func shellsRootedIn(projects []registeredProject, projectKey, root string) []string {
	if projectKey == "" {
		return nil
	}
	var names []string
	for _, p := range projects {
		if p.Key != projectKey {
			continue
		}
		for _, def := range workspaceops.ShellsRootedIn(p.Shells, root) {
			names = append(names, def.TmuxName)
		}
	}
	return names
}

// orphanedShellRoots maps each shell in defs to the removed worktree it is
// rooted in, for `shell list`.
func orphanedShellRoots(defs []shellstate.Definition, roots map[string]shellliveness.OrphanReason) map[string]string {
	if len(roots) == 0 {
		return nil
	}
	out := map[string]string{}
	for root := range roots {
		for _, def := range workspaceops.ShellsRootedIn(defs, root) {
			out[def.TmuxName] = root
		}
	}
	return out
}
