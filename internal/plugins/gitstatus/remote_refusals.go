package gitstatus

import (
	tea "charm.land/bubbletea/v2"
	appmsg "github.com/marcus/sidecar/internal/msg"
	"github.com/marcus/sidecar/internal/plugin"
)

// What a bound Git tab will not do, and why it says so instead.
//
// Git owns no host write verb. `sidecar repo` reads a repository and stops
// there: nothing in it stages, commits, fetches, checks a branch out, or
// answers blame. Refusing is the finished behaviour for those, not a
// placeholder — and refusing by name matters more than refusing quietly,
// because every one of these gestures has a local meaning that would apply to a
// same-named checkout on THIS machine. Doing nothing and doing it here look
// identical to the user right up until the wrong branch is checked out.
//
// The gate is one table in one place rather than a guard at each call site, and
// that is what the write half of this plan is shaped around: a `sidecar repo
// apply` slice deletes rows from this table and adds one command per row, and
// never has to hunt the call sites that used to refuse.

// remoteRefusal names one gesture the Git tab performs on this machine and does
// not perform on a bound one.
type remoteRefusal string

const (
	refuseStage        remoteRefusal = "stage"
	refuseUnstage      remoteRefusal = "unstage"
	refuseStageAll     remoteRefusal = "stage-all"
	refuseUnstageAll   remoteRefusal = "unstage-all"
	refuseCommit       remoteRefusal = "commit"
	refuseAmend        remoteRefusal = "amend"
	refuseDiscard      remoteRefusal = "discard"
	refusePush         remoteRefusal = "push"
	refusePull         remoteRefusal = "pull"
	refuseFetch        remoteRefusal = "fetch"
	refuseBranchSwitch remoteRefusal = "branch-switch"
	refuseStash        remoteRefusal = "stash"
	refuseStashPop     remoteRefusal = "stash-pop"
	refuseStashApply   remoteRefusal = "stash-apply"
	refuseInit         remoteRefusal = "init"
	refuseOpenEditor   remoteRefusal = "open-in-editor"
	refuseBlame        remoteRefusal = "blame"
)

// remoteRefusals is the whole set, each row carrying what that gesture would
// have done on this machine.
//
// Two rows have no key on this surface today and are here because the contract
// names them, not because a keypress reaches them. `init` is structurally
// unreachable — a bound pane never enters no-repo mode, which is the only view
// that offers it — and the Git tab binds no blame gesture at all; a user reaches
// for blame in Files, whose own table refuses it. Both stay listed so the write
// slice has one inventory to work from rather than two.
var remoteRefusals = map[remoteRefusal]string{
	refuseStage:        "Staging a file",
	refuseUnstage:      "Unstaging a file",
	refuseStageAll:     "Staging every file",
	refuseUnstageAll:   "Unstaging every file",
	refuseCommit:       "Committing",
	refuseAmend:        "Amending the last commit",
	refuseDiscard:      "Discarding changes",
	refusePush:         "Pushing",
	refusePull:         "Pulling",
	refuseFetch:        "Fetching",
	refuseBranchSwitch: "Switching branch",
	refuseStash:        "Stashing changes",
	refuseStashPop:     "Popping a stash",
	refuseStashApply:   "Applying a stash",
	refuseInit:         "Initializing a repository",
	refuseOpenEditor:   "Opening a file in an editor",
	refuseBlame:        "Git blame",
}

// remoteRefusedKeys are the sidebar keys whose only meaning is one of those
// gestures.
//
// The three that are missing are missing on purpose: enter, f, and the branch
// picker's own enter each mean something else on a different row — expanding a
// folder, filtering the log by author, listing branches — so the bound key loop
// resolves the row first and then refuses out of the same table.
var remoteRefusedKeys = map[string]remoteRefusal{
	"s":      refuseStage,
	"u":      refuseUnstage,
	"S":      refuseStageAll,
	"U":      refuseUnstageAll,
	"c":      refuseCommit,
	"A":      refuseAmend,
	"D":      refuseDiscard,
	"P":      refusePush,
	"L":      refusePull,
	"z":      refuseStash,
	"Z":      refuseStashPop,
	"ctrl+z": refuseStashApply,
}

// refuseRemote answers a gesture that has no host verb behind it. It names the
// host, so the sentence cannot be read as "this file cannot be staged".
func (p *Plugin) refuseRemote(what remoteRefusal) tea.Cmd {
	return appmsg.ShowFlash(plugin.FormatRemoteUnavailable(remoteRefusalText(what), p.hostID()))
}

// refuseRemoteBranch is the branch-switch refusal with the branch in it: a
// picker listing a dozen of them cannot otherwise say which one was refused.
func (p *Plugin) refuseRemoteBranch(name string) tea.Cmd {
	text := remoteRefusalText(refuseBranchSwitch) + " to " + name
	return appmsg.ShowFlash(plugin.FormatRemoteUnavailable(text, p.hostID()))
}

func remoteRefusalText(what remoteRefusal) string {
	if text, ok := remoteRefusals[what]; ok {
		return text
	}
	return string(what)
}

func (p *Plugin) hostID() string {
	if p.ctx == nil {
		return ""
	}
	return p.ctx.HostID
}

// openFileEntry opens a file in this machine's editor, or refuses when the file
// belongs to another machine.
//
// No host verb opens an editor there, and an editor here would open whatever
// same-named path happens to exist on this disk — the exact failure the bound
// surface exists to prevent. Both the key and the double-click go through here
// so the two cannot drift apart.
func (p *Plugin) openFileEntry(path string) tea.Cmd {
	if p.remoteBound() {
		return p.refuseRemote(refuseOpenEditor)
	}
	return p.openFile(path)
}

// remoteCommands is the footer while bound.
//
// It is the reachable subset and nothing else: every entry here is a gesture
// updateRemote or updateRemoteKeys actually performs against the host, and
// every gesture they perform is here. A footer advertising a command that
// refuses, or hiding one that works, is the same bug in two directions.
func (p *Plugin) remoteCommands() []plugin.Command {
	if !p.remoteAvailable() {
		return nil
	}
	return []plugin.Command{
		// git-status context (the host's changed files in the sidebar)
		{ID: "refresh", Name: "Refresh", Description: "Re-read the host's repository status and history", Category: plugin.CategoryActions, Context: "git-status", Priority: 1},
		{ID: "show-diff", Name: "Diff", Description: "View the host's patch for this file", Category: plugin.CategoryView, Context: "git-status", Priority: 2},
		{ID: "show-history", Name: "History", Description: "Jump to the host's commit history", Category: plugin.CategoryNavigation, Context: "git-status", Priority: 3},
		{ID: "branch-picker", Name: "Branch", Description: "List the host's branches (switching is refused)", Category: plugin.CategoryGit, Context: "git-status", Priority: 3},
		{ID: "open-in-file-browser", Name: "Browse", Description: "Open file in file browser", Category: plugin.CategoryNavigation, Context: "git-status", Priority: 4},
		{ID: "toggle-sidebar", Name: "Sidebar", Description: "Toggle sidebar visibility", Category: plugin.CategoryView, Context: "git-status", Priority: 5},
		// git-status-commits context (the host's commits in the sidebar)
		{ID: "view-commit", Name: "View", Description: "View commit details", Category: plugin.CategoryView, Context: "git-status-commits", Priority: 1},
		{ID: "search-history", Name: "Search", Description: "Search the commits loaded from the host", Category: plugin.CategorySearch, Context: "git-status-commits", Priority: 2},
		{ID: "toggle-graph", Name: "Graph", Description: "Toggle commit graph display", Category: plugin.CategoryView, Context: "git-status-commits", Priority: 2},
		{ID: "filter-author", Name: "Author", Description: "Filter the host's history by author", Category: plugin.CategorySearch, Context: "git-status-commits", Priority: 3},
		{ID: "filter-path", Name: "Path", Description: "Filter the host's history by file path", Category: plugin.CategorySearch, Context: "git-status-commits", Priority: 3},
		{ID: "clear-filter", Name: "Clear", Description: "Clear history filters", Category: plugin.CategoryActions, Context: "git-status-commits", Priority: 3},
		{ID: "yank-commit", Name: "Yank", Description: "Copy commit as markdown", Category: plugin.CategoryActions, Context: "git-status-commits", Priority: 3},
		{ID: "yank-id", Name: "YankID", Description: "Copy commit ID", Category: plugin.CategoryActions, Context: "git-status-commits", Priority: 3},
		{ID: "open-in-github", Name: "GitHub", Description: "Open commit in GitHub", Category: plugin.CategoryActions, Context: "git-status-commits", Priority: 3},
		{ID: "next-match", Name: "Next", Description: "Next search match", Category: plugin.CategoryNavigation, Context: "git-status-commits", Priority: 4},
		{ID: "prev-match", Name: "Prev", Description: "Previous search match", Category: plugin.CategoryNavigation, Context: "git-status-commits", Priority: 4},
		{ID: "toggle-sidebar", Name: "Sidebar", Description: "Toggle sidebar visibility", Category: plugin.CategoryView, Context: "git-status-commits", Priority: 5},
		// git-history-search context (commit search modal)
		{ID: "select", Name: "Select", Description: "Jump to selected match", Category: plugin.CategoryActions, Context: "git-history-search", Priority: 1},
		{ID: "cancel", Name: "Cancel", Description: "Close search", Category: plugin.CategoryActions, Context: "git-history-search", Priority: 1},
		{ID: "navigate", Name: "Nav", Description: "Move through matches", Category: plugin.CategoryNavigation, Context: "git-history-search", Priority: 2},
		{ID: "toggle-regex", Name: "Regex", Description: "Toggle regex mode", Category: plugin.CategoryView, Context: "git-history-search", Priority: 3},
		{ID: "toggle-case", Name: "Case", Description: "Toggle case sensitivity", Category: plugin.CategoryView, Context: "git-history-search", Priority: 3},
		// git-path-filter context (path filter modal)
		{ID: "apply-filter", Name: "Apply", Description: "Apply path filter", Category: plugin.CategorySearch, Context: "git-path-filter", Priority: 1},
		{ID: "cancel", Name: "Cancel", Description: "Close path filter", Category: plugin.CategoryActions, Context: "git-path-filter", Priority: 1},
		// git-commit-preview context (the host's commit in the right pane)
		{ID: "view-diff", Name: "Diff", Description: "View the host's patch for this file", Category: plugin.CategoryView, Context: "git-commit-preview", Priority: 1},
		{ID: "back", Name: "Back", Description: "Return to sidebar", Category: plugin.CategoryNavigation, Context: "git-commit-preview", Priority: 1},
		{ID: "yank-commit", Name: "Yank", Description: "Copy commit as markdown", Category: plugin.CategoryActions, Context: "git-commit-preview", Priority: 3},
		{ID: "yank-id", Name: "YankID", Description: "Copy commit ID", Category: plugin.CategoryActions, Context: "git-commit-preview", Priority: 3},
		{ID: "open-in-github", Name: "GitHub", Description: "Open commit in GitHub", Category: plugin.CategoryActions, Context: "git-commit-preview", Priority: 3},
		{ID: "open-in-file-browser", Name: "Browse", Description: "Open file in file browser", Category: plugin.CategoryNavigation, Context: "git-commit-preview", Priority: 3},
		{ID: "toggle-sidebar", Name: "Sidebar", Description: "Toggle sidebar visibility", Category: plugin.CategoryView, Context: "git-commit-preview", Priority: 4},
		// git-status-diff context (inline diff pane)
		{ID: "toggle-diff-view", Name: "View", Description: "Toggle unified/split diff view", Category: plugin.CategoryView, Context: "git-status-diff", Priority: 2},
		{ID: "toggle-wrap", Name: "Wrap", Description: "Toggle line wrapping", Category: plugin.CategoryView, Context: "git-status-diff", Priority: 3},
		{ID: "reset-hscroll", Name: "Col 0", Description: "Snap horizontal scroll back to column 0", Category: plugin.CategoryNavigation, Context: "git-status-diff", Priority: 4},
		{ID: "toggle-sidebar", Name: "Sidebar", Description: "Toggle sidebar visibility", Category: plugin.CategoryView, Context: "git-status-diff", Priority: 3},
		// git-diff context (full-screen patch)
		{ID: "close-diff", Name: "Close", Description: "Close diff view", Category: plugin.CategoryView, Context: "git-diff", Priority: 1},
		{ID: "scroll", Name: "Scroll", Description: "Scroll diff content", Category: plugin.CategoryNavigation, Context: "git-diff", Priority: 2},
		{ID: "toggle-diff-view", Name: "View", Description: "Toggle unified/split diff view", Category: plugin.CategoryView, Context: "git-diff", Priority: 3},
		{ID: "toggle-wrap", Name: "Wrap", Description: "Toggle line wrapping", Category: plugin.CategoryView, Context: "git-diff", Priority: 3},
		{ID: "prev-file", Name: "Prev", Description: "Previous changed file", Category: plugin.CategoryNavigation, Context: "git-diff", Priority: 4},
		{ID: "next-file", Name: "Next", Description: "Next changed file", Category: plugin.CategoryNavigation, Context: "git-diff", Priority: 4},
		{ID: "open-in-file-browser", Name: "Browse", Description: "Open file in file browser", Category: plugin.CategoryNavigation, Context: "git-diff", Priority: 4},
	}
}
