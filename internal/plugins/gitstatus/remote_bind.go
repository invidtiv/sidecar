package gitstatus

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/marcus/sidecar/internal/app"
	"github.com/marcus/sidecar/internal/hostproto"
	appmsg "github.com/marcus/sidecar/internal/msg"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

// The bound-host half of the Git plugin.
//
// One rule governs everything here: while bound, this plugin must not read this
// machine's repository for the project it is showing. A same-named checkout is a
// different project, and rendering its branch, its staged files, or its patches
// under a remote label is the failure the remote-project work exists to prevent
// — a green test that does it has still failed.
//
// The rule is kept structurally rather than by vigilance: while bound, repoRoot
// stays empty and hasRepo stays false, so no code path here has a directory to
// run git in even if one were reached.

// remoteBound reports that this plugin is showing another machine's project.
func (p *Plugin) remoteBound() bool {
	return p.ctx != nil && p.ctx.HostID != ""
}

// remoteWorkspaceID is the durable identity the host resolves repository state
// against: the bound worktree, or the project's main checkout.
//
// It is composed here rather than remembered, so a bind that moves to another
// worktree cannot leave this surface reading the previous one. It is deliberately
// the same id Files composes, because it is the same workspace.
func (p *Plugin) remoteWorkspaceID() string {
	if p.ctx == nil || p.ctx.ProjectKey == "" {
		return ""
	}
	key := p.ctx.HostWorktreeKey
	if key == "" {
		key = p.ctx.ProjectKey
	}
	return p.ctx.ProjectKey + ":" + string(workspaceinventory.KindWorktree) + ":" + key
}

func (p *Plugin) hostVerbs() hostproto.VerbCapabilities {
	if p.ctx == nil || p.ctx.HostVerbs == nil {
		return hostproto.VerbCapabilities{}
	}
	return p.ctx.HostVerbs()
}

func (p *Plugin) hostShows() bool {
	return p.ctx != nil && p.ctx.HostShows != nil && p.ctx.HostShows()
}

// remoteUnavailable is why this bound surface cannot read a repository, or ""
// when it can. A local project always answers "".
func (p *Plugin) remoteUnavailable() string {
	if !p.remoteBound() {
		return ""
	}
	if p.ctx.RemoteRunner == nil {
		return "[" + p.ctx.HostID + "] is not reachable from this Sidecar"
	}
	return remoteRepoUnavailable(p.ctx.HostID, p.remoteWorkspaceID(), p.hostVerbs(), p.hostShows())
}

// remoteAvailable reports that a bound surface has everything it needs to read
// the host.
func (p *Plugin) remoteAvailable() bool {
	return p.remoteBound() && p.remoteUnavailable() == ""
}

// unavailableReason is the sentence the Git tab paints instead of a repository.
//
// The connection reasons are knowable before any call; the host's own refusal —
// this workspace is not a git repository — is only knowable from an answer, so
// it is remembered from the last read and reported here alongside them.
func (p *Plugin) unavailableReason() string {
	if reason := p.remoteUnavailable(); reason != "" {
		return strings.TrimSpace(reason)
	}
	return strings.TrimSpace(p.remoteRefusal)
}

// repoSource is the repository-read seam for whichever machine owns this
// project. A nil source means there is nothing to read and the view is a
// sentence.
func (p *Plugin) repoSource() RepoSource {
	if p.repoSourceOverride != nil {
		return p.repoSourceOverride
	}
	if p.remoteBound() {
		if !p.remoteAvailable() {
			return nil
		}
		return &remoteRepoSource{
			hostID:      p.ctx.HostID,
			workspaceID: p.remoteWorkspaceID(),
			run:         p.ctx.RemoteRunner,
		}
	}
	if !p.hasRepo || p.tree == nil || p.repoRoot == "" {
		return nil
	}
	return localRepoSource{root: p.repoRoot, load: p.statusLoader}
}

// updateRemote is the bound plugin's whole message loop.
//
// It is a separate entry point rather than a guard inside the local one because
// the local handlers reach stage, discard, push, and the patch loaders, all of
// which take a working directory on this disk. Nothing routed here can arrive
// at one.
func (p *Plugin) updateRemote(msg tea.Msg) (plugin.Plugin, tea.Cmd) {
	if !p.remoteAvailable() {
		return p, nil
	}
	switch msg := msg.(type) {
	case app.RefreshMsg:
		return p, p.refresh()

	case app.PluginFocusedMsg:
		return p, p.refresh()

	case plugin.HostInventoryMsg:
		// The host's snapshot moved. That is the whole of a bound pane's live
		// refresh: internal/livewatch is a filesystem signal and does not cross
		// the boundary, so this and an explicit r are what it has.
		return p, p.refresh()

	case StatusSnapshotLoadedMsg:
		if plugin.IsStale(p.ctx, msg) || msg.RequestID != p.activeStatusRequestID {
			return p, nil
		}
		return p, p.applyStatusSnapshot(msg)

	case InlineDiffLoadedMsg:
		return p, p.applyInlineDiffLoaded(msg)

	case DiffLoadedMsg:
		return p, p.applyDiffLoaded(msg)

	case tea.KeyPressMsg:
		return p.updateRemoteKeys(msg)
	}
	return p, nil
}

// updateRemoteKeys is the bound pane's keyboard: movement, the sidebar, the
// patch for the row the cursor is on, and an explicit refresh.
//
// It is deliberately the reachable subset. History and the write refusals
// arrive with their own slices; wiring a key here that has nothing behind it
// would tell the user a gesture works when it does not.
//
// The two diff surfaces are the local handlers themselves, not copies of them.
// Everything they reach now reads through RepoSource, and the two loaders that
// still take a directory on this disk — a folder's aggregate patch and a
// full-file view — refuse for a bound pane at their own door, so a key that
// lands on one is a no-op rather than a local git invocation.
func (p *Plugin) updateRemoteKeys(msg tea.KeyPressMsg) (plugin.Plugin, tea.Cmd) {
	if p.tree == nil {
		if msg.String() == "r" {
			return p, p.refresh()
		}
		return p, nil
	}
	if p.viewMode == ViewModeDiff {
		return p.updateDiff(msg)
	}
	if p.activePane == PaneDiff {
		return p.updateStatusDiffPane(msg)
	}

	entries := p.treeEntries()
	total := p.totalSelectableItems()
	switch msg.String() {
	case "j", "down":
		if p.cursor < total-1 {
			p.cursor++
			p.ensureCursorVisible()
			return p, p.autoLoadDiff()
		}

	case "k", "up":
		if p.cursor > 0 {
			p.cursor--
			p.ensureCursorVisible()
			return p, p.autoLoadDiff()
		}

	case "g":
		p.cursor = 0
		p.scrollOff = 0
		return p, p.autoLoadDiff()

	case "G":
		if total > 0 {
			p.cursor = total - 1
			p.ensureCursorVisible()
			return p, p.autoLoadDiff()
		}

	case "l", "right":
		if p.sidebarVisible && p.selectedDiffFile != "" {
			p.activePane = PaneDiff
		}

	case "enter":
		// Only a folder's expansion, which is this viewer's own display state.
		// Enter on a file opens it in an editor locally; that is a host verb
		// nothing answers, and it refuses with the rest of them in 4j.
		if p.cursor < len(entries) && entries[p.cursor].IsFolder {
			entries[p.cursor].IsExpanded = !entries[p.cursor].IsExpanded
			return p, p.autoLoadDiff()
		}

	case "d":
		if p.cursor < len(entries) && !entries[p.cursor].IsFolder {
			entry := entries[p.cursor]
			p.diffReturnMode = p.viewMode
			p.viewMode = ViewModeDiff
			p.diffFile = entry.Path
			p.diffStaged = entry.Staged
			p.diffCommit = ""
			p.diffCommitSubject = ""
			p.diffCommitShortHash = ""
			p.diffScroll = 0
			p.diffLoaded = false
			return p, p.loadDiff(entry.Path, entry.Staged, entry.Status)
		}

	case "\\":
		p.toggleSidebar()
		if !p.sidebarVisible {
			return p, appmsg.ShowFlash("Sidebar hidden (\\ to restore)")
		}

	case "r":
		return p, p.refresh()
	}
	return p, nil
}

// renderBoundView is the Git tab while it is showing another machine.
//
// "Not connected", "too old for the repository contract", "no bound worktree",
// and "not a git repository" are four different things for the user to do next,
// so they are four different sentences. None of them is this machine's no-repo
// view, which offers to run `git init` here, under a label that names the host.
func (p *Plugin) renderBoundView() string {
	if reason := p.unavailableReason(); reason != "" {
		return styles.Title.Render(pluginName) + "\n\n" +
			styles.Muted.Render(pluginName+" is unavailable: "+reason)
	}
	if p.tree == nil {
		return styles.Title.Render(pluginName) + "\n\n" +
			styles.Muted.Render("Loading ["+p.ctx.HostID+"]…")
	}
	// The full-screen diff is the same view it is locally: it renders a patch,
	// and which machine produced it is below the renderer.
	if p.viewMode == ViewModeDiff {
		if p.sidebarVisible {
			return p.renderDiffTwoPane()
		}
		return p.renderDiffModal()
	}
	return p.renderThreePaneView()
}

// boundDiffPaneNotice is what a bound diff pane says when the selected row is
// one this build cannot answer honestly, or "" when it can.
//
// It is derived at render time from the row itself rather than remembered, so
// it cannot survive the selection that produced it.
func (p *Plugin) boundDiffPaneNotice() string {
	if !p.remoteBound() {
		return ""
	}
	entries := p.treeEntries()
	if p.cursor >= len(entries) || !entries[p.cursor].IsFolder {
		return ""
	}
	// A folder row is an aggregate, not one repository read. Reading it from a
	// host would be one round trip per file in the folder, on a cursor move.
	return "A folder's combined patch is not read from [" + p.ctx.HostID + "]. Press enter to open it and read one file's patch."
}

// boundFullFileNotice names the one diff view a bound pane cannot draw.
func boundFullFileNotice(hostID string) string {
	return "Full-file view is not available on [" + hostID + "]: the host answers patches, not file contents."
}

// noticeContentHeight is the room left for a patch under a sentence explaining
// what is missing from it. The pane's height is the app's, not this view's, so
// the notice comes out of the content rather than growing past it.
func noticeContentHeight(contentHeight int) int {
	if contentHeight <= 2 {
		return 1
	}
	return contentHeight - 2
}

// truncationLabel marks a patch the source had to cut, in the header where the
// view mode is. A short patch rendered as if it were whole is a lie about the
// change, and it is not one the reader can see for themselves.
func truncationLabel(truncated bool) string {
	if !truncated {
		return ""
	}
	return " " + lipgloss.NewStyle().Foreground(styles.Warning).Render("(truncated)")
}

// remoteCommands is the footer while bound.
//
// It lists what this build actually performs on the host and nothing else, so
// the footer tells the truth rather than advertising gestures that would have to
// refuse.
func (p *Plugin) remoteCommands() []plugin.Command {
	if !p.remoteAvailable() {
		return nil
	}
	return []plugin.Command{
		{ID: "refresh", Name: "Refresh", Description: "Re-read the host's repository status", Category: plugin.CategoryActions, Context: "git-status", Priority: 1},
		{ID: "show-diff", Name: "Diff", Description: "View the host's patch for this file", Category: plugin.CategoryView, Context: "git-status", Priority: 2},
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
