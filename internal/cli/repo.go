package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/marcus/sidecar/internal/reposervice"
)

// repoCommand is the read-only repository contract a viewing Sidecar invokes on
// a host. It is an internal transport endpoint, not a git CLI.
func repoCommand() *Command {
	jsonFlag := Flag{Name: "--json", Summary: "Write the structured result object to stdout (required for the machine contract)", Bool: true}
	helpFlag := Flag{Name: "--help", Short: "-h", Summary: "Show this help", Bool: true}
	workspaceFlag := Flag{Name: "--workspace", Arg: "ID", Summary: "Unscoped durable workspace id (projectKey:shell:name or projectKey:worktree:path)"}

	notARepo := "A workspace that is not a git repository answers with noRepository rather than failing, so a viewer can say so instead of offering to initialize one on the wrong machine.\n"

	statusCmd := &Command{
		Name:    "status",
		Summary: "Read one repository's branch, upstream, state, and changed files",
		Usage:   "sidecar repo status --workspace ID [--json]",
		Long: "Read the current branch, upstream ref, ahead/behind counts, detached-HEAD flag, in-progress state, origin's URL, stash count, and the changed-file rows for a durable workspace identity on this machine.\n\n" +
			"One call, one instant: branch and file state come from a single `git status --porcelain=v2 --branch`, so a viewer never renders a branch from one moment beside files from another.\n" +
			"Each changed-file row carries the staged, unstaged, and untracked senses for that path, with the +/- counts of each sense, because a path staged and then edited again is one file with two patches.\n" +
			"In-progress state is one of merge, rebase, cherry-pick, revert, or bisect; empty is an ordinary working tree.\n" +
			notARepo +
			"\n--json writes the machine contract.",
		Flags:     []Flag{workspaceFlag, jsonFlag, helpFlag},
		Args:      ArgSpec{Min: 0, Max: 0},
		ExitCodes: repoExitCodes("read"),
		Examples: []Example{
			{Command: "sidecar repo status --workspace /home/me/api:worktree:/home/me/api --json"},
		},
		Run: runRepoStatus,
	}

	diffCmd := &Command{
		Name:    "diff",
		Summary: "Read one raw unified patch for one path",
		Usage:   "sidecar repo diff --workspace ID --path REL --mode staged|unstaged|untracked|commit [--commit HASH] [--json]",
		Long: "Read one file's patch from a durable workspace identity on this machine, as raw unified diff text.\n\n" +
			"--mode is required and never inferred. A staged and an unstaged change to the same path are two different patches, and answering with the wrong one is a quiet, plausible lie about the host's working tree.\n" +
			"--mode commit needs --commit and returns that commit's patch for the path; a merge is diffed against its first parent, because git's combined diff for a clean merge is empty.\n" +
			"--mode untracked renders a file git does not track as the addition it is.\n" +
			"The patch is text and is not parsed here: the viewer runs the same parser it runs on a local patch, so a host upgrade is never a rendering change.\n" +
			"A patch over 512KiB is cut and says so rather than being returned short and silent.\n" +
			notARepo +
			"\n--json writes the machine contract.",
		Flags: []Flag{
			workspaceFlag,
			{Name: "--path", Arg: "REL", Summary: "File path relative to the workspace root"},
			{Name: "--mode", Arg: "MODE", Summary: "Which patch: staged, unstaged, untracked, or commit"},
			{Name: "--commit", Arg: "HASH", Summary: "Commit object name, required for --mode commit"},
			jsonFlag,
			helpFlag,
		},
		Args:      ArgSpec{Min: 0, Max: 0},
		ExitCodes: repoExitCodes("read"),
		Examples: []Example{
			{Command: "sidecar repo diff --workspace /home/me/api:worktree:/home/me/api --path internal/cli/repo.go --mode unstaged --json"},
			{Command: "sidecar repo diff --workspace /home/me/api:worktree:/home/me/api --path README.md --mode commit --commit 4f2b91c --json"},
		},
		Run: runRepoDiff,
	}

	historyCmd := &Command{
		Name:    "history",
		Summary: "Read one page of the commit log",
		Usage:   "sidecar repo history --workspace ID [--limit N] [--cursor HASH] [--author TEXT] [--path REL] [--json]",
		Long: "Read commit rows — hash, short hash, subject, author, author date, parent hashes, and pushed state — newest first.\n\n" +
			"History is paged, not walked. --cursor is the hash of the previous page's last row, not an offset: an offset silently repeats or skips a commit when the host commits between two pages.\n" +
			"nextCursor is empty when the page reached the end of the log.\n" +
			"The host caps one page at 500 rows regardless of --limit, because the host is the machine that would pay for serializing an entire log.\n" +
			"--author and --path filter on the host. Subject search stays with the viewer, which runs it over the rows it already has.\n" +
			"Pushed state is asked of git for this page's commits alone, so a branch far ahead of its upstream cannot make the answer depend on a cap.\n" +
			notARepo +
			"\n--json writes the machine contract.",
		Flags: []Flag{
			workspaceFlag,
			{Name: "--limit", Arg: "N", Summary: "Rows in this page (default 100, host maximum 500)"},
			{Name: "--cursor", Arg: "HASH", Summary: "Continue after this commit: the previous page's last hash"},
			{Name: "--author", Arg: "TEXT", Summary: "Only commits whose author matches this text"},
			{Name: "--path", Arg: "REL", Summary: "Only commits touching this path, relative to the workspace root"},
			jsonFlag,
			helpFlag,
		},
		Args:      ArgSpec{Min: 0, Max: 0},
		ExitCodes: repoExitCodes("read"),
		Examples: []Example{
			{Command: "sidecar repo history --workspace /home/me/api:worktree:/home/me/api --limit 50 --json"},
			{Command: "sidecar repo history --workspace /home/me/api:worktree:/home/me/api --cursor 4f2b91c8ab --json"},
		},
		Run: runRepoHistory,
	}

	commitCmd := &Command{
		Name:    "commit",
		Summary: "Read one commit's metadata and file list",
		Usage:   "sidecar repo commit --workspace ID --commit HASH [--json]",
		Long: "Read one commit's subject, body, author, date, parent hashes, merge flag, and file list with per-file status and +/- counts.\n\n" +
			"This reads a commit. It does not create one: `sidecar repo` has no write path of any kind.\n" +
			"A merge's file list is its diff against the first parent, because git's combined diff for a clean merge lists nothing.\n" +
			notARepo +
			"\n--json writes the machine contract.",
		Flags: []Flag{
			workspaceFlag,
			{Name: "--commit", Arg: "HASH", Summary: "Commit object name"},
			jsonFlag,
			helpFlag,
		},
		Args:      ArgSpec{Min: 0, Max: 0},
		ExitCodes: repoExitCodes("read"),
		Examples: []Example{
			{Command: "sidecar repo commit --workspace /home/me/api:worktree:/home/me/api --commit 4f2b91c8ab --json"},
		},
		Run: runRepoCommit,
	}

	refsCmd := &Command{
		Name:    "refs",
		Summary: "List local and remote branches and the stash",
		Usage:   "sidecar repo refs --workspace ID [--json]",
		Long: "List local branches with their upstream and ahead/behind counts, remote-tracking branches, and the stash entries.\n\n" +
			"Listing only. A viewer bound to this host shows the branches and refuses to switch to one.\n" +
			notARepo +
			"\n--json writes the machine contract.",
		Flags:     []Flag{workspaceFlag, jsonFlag, helpFlag},
		Args:      ArgSpec{Min: 0, Max: 0},
		ExitCodes: repoExitCodes("listed"),
		Examples: []Example{
			{Command: "sidecar repo refs --workspace /home/me/api:worktree:/home/me/api --json"},
		},
		Run: runRepoRefs,
	}

	return &Command{
		Name:    "repo",
		Summary: "Read-only repository contract a viewing Sidecar invokes on a host",
		Usage:   "sidecar repo <command>",
		Long: "Read one machine's git repository state — status, patches, history, commits, and refs — for a viewing Sidecar, over the existing host request seam.\n\n" +
			"This is not a git CLI and must not be adopted as one. Sidecar does not own git: an agent that wants to stage a file, commit, or push runs `git`.\n" +
			"These verbs exist because a viewing Sidecar needs one machine's repository state in one round trip, normalized to the model its panes already render.\n" +
			"Every verb is non-interactive, read-only, workspace-scoped, and strictly enumerated. There is no write path here, and adding one is a separate decision with its own confirmation and credential questions.",
		Sub: []*Command{commitCmd, diffCmd, historyCmd, refsCmd, statusCmd},
		Run: runRepoRoot,
	}
}

func repoExitCodes(answered string) []ExitCode {
	return []ExitCode{
		{Code: 0, Summary: answered + ", including a workspace that is not a git repository"},
		{Code: 1, Summary: "internal or load failure"},
		{Code: 2, Summary: "usage error"},
		{Code: 5, Summary: "value rejected: unknown workspace, containment, or an unknown commit"},
	}
}

func runRepoRoot(env Env, args []string) int {
	cmd := RootCommand().FindSubcommand("repo")
	if len(args) == 0 || isHelp(args[0]) {
		_, _ = fmt.Fprint(env.Stdout, RenderHelp(cmd))
		return 0
	}
	sub := cmd.FindSubcommand(args[0])
	if sub != nil && sub.Run != nil {
		return sub.Run(env, args[1:])
	}
	cliErrf(env.Stderr, "unknown repo command %q\n\n%s", args[0], RenderHelp(cmd))
	return 2
}

type repoFlags struct {
	workspace string
	path      string
	mode      string
	commit    string
	cursor    string
	author    string
	limit     int
	json      bool
}

// repoFlagSet says which optional flags a sub-verb accepts, so an argument that
// belongs to a different verb is a usage error rather than a silently ignored
// one.
type repoFlagSet struct {
	name   string
	path   bool
	mode   bool
	commit bool
	paging bool
}

func parseRepoFlags(env Env, help string, args []string, allow repoFlagSet) (repoFlags, int, bool) {
	var flags repoFlags
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-h" || arg == "--help":
			_, _ = fmt.Fprint(env.Stdout, help)
			return flags, 0, false
		case arg == "--json":
			flags.json = true
		case arg == "--workspace" || strings.HasPrefix(arg, "--workspace="):
			val, next, ok := takeFlagArg(arg, args, i, "--workspace")
			if !ok || val == "" {
				cliErrf(env.Stderr, "--workspace requires an id\n\n%s", help)
				return flags, 2, false
			}
			flags.workspace = val
			i = next
		case allow.path && (arg == "--path" || strings.HasPrefix(arg, "--path=")):
			val, next, ok := takeFlagArg(arg, args, i, "--path")
			if !ok || val == "" {
				cliErrf(env.Stderr, "--path requires a path relative to the workspace root\n\n%s", help)
				return flags, 2, false
			}
			flags.path = val
			i = next
		case allow.mode && (arg == "--mode" || strings.HasPrefix(arg, "--mode=")):
			val, next, ok := takeFlagArg(arg, args, i, "--mode")
			if !ok || val == "" {
				cliErrf(env.Stderr, "--mode requires staged, unstaged, untracked, or commit\n\n%s", help)
				return flags, 2, false
			}
			flags.mode = val
			i = next
		case allow.commit && (arg == "--commit" || strings.HasPrefix(arg, "--commit=")):
			val, next, ok := takeFlagArg(arg, args, i, "--commit")
			if !ok || val == "" {
				cliErrf(env.Stderr, "--commit requires a commit object name\n\n%s", help)
				return flags, 2, false
			}
			flags.commit = val
			i = next
		case allow.paging && (arg == "--cursor" || strings.HasPrefix(arg, "--cursor=")):
			val, next, ok := takeFlagArg(arg, args, i, "--cursor")
			if !ok || val == "" {
				cliErrf(env.Stderr, "--cursor requires the previous page's last commit hash\n\n%s", help)
				return flags, 2, false
			}
			flags.cursor = val
			i = next
		case allow.paging && (arg == "--author" || strings.HasPrefix(arg, "--author=")):
			val, next, ok := takeFlagArg(arg, args, i, "--author")
			if !ok || val == "" {
				cliErrf(env.Stderr, "--author requires text to match\n\n%s", help)
				return flags, 2, false
			}
			flags.author = val
			i = next
		case allow.paging && (arg == "--limit" || strings.HasPrefix(arg, "--limit=")):
			val, next, ok := takeFlagArg(arg, args, i, "--limit")
			if !ok || val == "" {
				cliErrf(env.Stderr, "--limit requires a number\n\n%s", help)
				return flags, 2, false
			}
			n, err := strconv.Atoi(val)
			if err != nil {
				cliErrf(env.Stderr, "--limit must be an integer\n\n%s", help)
				return flags, 2, false
			}
			flags.limit = n
			i = next
		case strings.HasPrefix(arg, "-"):
			cliErrf(env.Stderr, "unknown option %q\n\n%s", arg, help)
			return flags, 2, false
		default:
			cliErrf(env.Stderr, "repo %s takes no positional arguments\n\n%s", allow.name, help)
			return flags, 2, false
		}
	}
	if flags.workspace == "" {
		cliErrf(env.Stderr, "--workspace is required\n\n%s", help)
		return flags, 2, false
	}
	return flags, 0, true
}

func runRepoStatus(env Env, args []string) int {
	cmd := RootCommand().FindSubcommand("repo").FindSubcommand("status")
	help := RenderHelp(cmd)
	flags, code, ok := parseRepoFlags(env, help, args, repoFlagSet{name: "status"})
	if !ok {
		return code
	}
	result, err := reposervice.Default().Status(contentCtx(env), flags.workspace)
	if err != nil {
		return contentExit(env, err)
	}
	if flags.json {
		raw, err := reposervice.EncodeStatusResult(result)
		if err != nil {
			return contentExit(env, err)
		}
		return writeContentJSON(env, raw)
	}
	if result.NoRepository {
		_, _ = fmt.Fprintf(env.Stdout, "%s is not a git repository\n", result.Workspace)
		return 0
	}
	branch := result.Branch
	if result.Detached {
		branch = "(detached)"
	}
	_, _ = fmt.Fprintf(env.Stdout, "%s branch=%s upstream=%s ahead=%d behind=%d state=%s files=%d stashes=%d\n",
		result.Workspace, branch, result.Upstream, result.Ahead, result.Behind, result.State, len(result.Files), result.StashCount)
	return 0
}

func runRepoDiff(env Env, args []string) int {
	cmd := RootCommand().FindSubcommand("repo").FindSubcommand("diff")
	help := RenderHelp(cmd)
	flags, code, ok := parseRepoFlags(env, help, args, repoFlagSet{name: "diff", path: true, mode: true, commit: true})
	if !ok {
		return code
	}
	if flags.path == "" {
		cliErrf(env.Stderr, "--path is required\n\n%s", help)
		return 2
	}
	if flags.mode == "" {
		cliErrf(env.Stderr, "--mode is required\n\n%s", help)
		return 2
	}
	if flags.mode == reposervice.ModeCommit && flags.commit == "" {
		cliErrf(env.Stderr, "--commit is required for --mode commit\n\n%s", help)
		return 2
	}
	if flags.mode != reposervice.ModeCommit && flags.commit != "" {
		cliErrf(env.Stderr, "--commit is only valid with --mode commit\n\n%s", help)
		return 2
	}

	svc := reposervice.Default()
	var (
		result reposervice.DiffResult
		err    error
	)
	if flags.mode == reposervice.ModeCommit {
		result, err = svc.CommitDiff(contentCtx(env), flags.workspace, flags.commit, flags.path)
	} else {
		result, err = svc.Diff(contentCtx(env), flags.workspace, flags.path, flags.mode)
	}
	if err != nil {
		return contentExit(env, err)
	}
	if flags.json {
		raw, err := reposervice.EncodeDiffResult(result)
		if err != nil {
			return contentExit(env, err)
		}
		return writeContentJSON(env, raw)
	}
	if result.NoRepository {
		_, _ = fmt.Fprintf(env.Stdout, "%s is not a git repository\n", result.Workspace)
		return 0
	}
	_, _ = fmt.Fprintf(env.Stdout, "%s %s bytes=%d truncated=%t\n", result.Mode, result.Path, len(result.Patch), result.Truncated)
	return 0
}

func runRepoHistory(env Env, args []string) int {
	cmd := RootCommand().FindSubcommand("repo").FindSubcommand("history")
	help := RenderHelp(cmd)
	flags, code, ok := parseRepoFlags(env, help, args, repoFlagSet{name: "history", path: true, paging: true})
	if !ok {
		return code
	}
	result, err := reposervice.Default().History(contentCtx(env), flags.workspace, reposervice.HistoryQuery{
		Limit: flags.limit, Cursor: flags.cursor, Author: flags.author, Path: flags.path,
	})
	if err != nil {
		return contentExit(env, err)
	}
	if flags.json {
		raw, err := reposervice.EncodeHistoryResult(result)
		if err != nil {
			return contentExit(env, err)
		}
		return writeContentJSON(env, raw)
	}
	if result.NoRepository {
		_, _ = fmt.Fprintf(env.Stdout, "%s is not a git repository\n", result.Workspace)
		return 0
	}
	_, _ = fmt.Fprintf(env.Stdout, "%s commits=%d next=%s\n", result.Workspace, len(result.Commits), result.NextCursor)
	return 0
}

func runRepoCommit(env Env, args []string) int {
	cmd := RootCommand().FindSubcommand("repo").FindSubcommand("commit")
	help := RenderHelp(cmd)
	flags, code, ok := parseRepoFlags(env, help, args, repoFlagSet{name: "commit", commit: true})
	if !ok {
		return code
	}
	if flags.commit == "" {
		cliErrf(env.Stderr, "--commit is required\n\n%s", help)
		return 2
	}
	result, err := reposervice.Default().Commit(contentCtx(env), flags.workspace, flags.commit)
	if err != nil {
		return contentExit(env, err)
	}
	if flags.json {
		raw, err := reposervice.EncodeCommitResult(result)
		if err != nil {
			return contentExit(env, err)
		}
		return writeContentJSON(env, raw)
	}
	if result.NoRepository || result.Commit == nil {
		_, _ = fmt.Fprintf(env.Stdout, "%s is not a git repository\n", result.Workspace)
		return 0
	}
	_, _ = fmt.Fprintf(env.Stdout, "%s %s files=%d\n", result.Commit.ShortHash, result.Commit.Subject, len(result.Commit.Files))
	return 0
}

func runRepoRefs(env Env, args []string) int {
	cmd := RootCommand().FindSubcommand("repo").FindSubcommand("refs")
	help := RenderHelp(cmd)
	flags, code, ok := parseRepoFlags(env, help, args, repoFlagSet{name: "refs"})
	if !ok {
		return code
	}
	result, err := reposervice.Default().Refs(contentCtx(env), flags.workspace)
	if err != nil {
		return contentExit(env, err)
	}
	if flags.json {
		raw, err := reposervice.EncodeRefsResult(result)
		if err != nil {
			return contentExit(env, err)
		}
		return writeContentJSON(env, raw)
	}
	if result.NoRepository {
		_, _ = fmt.Fprintf(env.Stdout, "%s is not a git repository\n", result.Workspace)
		return 0
	}
	_, _ = fmt.Fprintf(env.Stdout, "%s branches=%d remotes=%d stashes=%d\n",
		result.Workspace, len(result.Branches), len(result.RemoteBranches), len(result.Stashes))
	return 0
}
