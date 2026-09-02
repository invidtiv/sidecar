# Git on a remote-bound project

Status: **active; slices 4f–4j implemented** on `remote-viewer-screen` (td-7a1393: td-3a4da2, td-ca9621, td-d7a47f, td-edd823, td-6fa047). Remaining: 4k, the rest of the viewer half. This is slice 4's Git half of [Remote destinations in `@` and `W`](remote-project-switcher.md), split out for the same reason Files was: it needs a new host verb family and a plugin-wide source seam, not a landing rule. **Created:** 2026-09-01 **Verified against the tree on 2026-09-01.**

Related: [Files on a remote-bound project](remote-files-plugin.md) is the pattern this plan follows — a host verb, a source seam at the one place the plugin reads the world, and refusals that name the host. [Sidecar as its own remote host runtime](sidecar-remote-hosts.md) is the ssh transport and the `hostproto` hello. [Remote host content-pane parity](../implemented/remote-host-content-pane-parity.md) is the `contentpanes.Source` read path, which answers branch-vs-base diffs and is **not** what this plugin needs.

## Decision first

Bound to `[aerie] Sidecar`, the Git tab shows aerie's working tree, aerie's branch, aerie's commits, and aerie's patches. It never shows a same-named checkout on this disk, and in this slice it changes nothing on either machine.

Git reads through one new host verb family and refuses everything else by name:

```text
Git, bound to [aerie] Sidecar
   |
   +-- status  (files, branch, ahead/behind, repo state) -> sidecar repo status   (NEW, RepoReadV1)
   +-- patches (staged, unstaged, untracked, commit file) -> sidecar repo diff    (NEW)
   +-- history (commit rows, graph parents, push state)   -> sidecar repo history (NEW)
   +-- commit detail (subject, body, file list)           -> sidecar repo commit  (NEW)
   +-- stash list, branch list                            -> sidecar repo refs    (NEW)
   |
   +-- stage/unstage/commit/amend/discard   -> refused, naming aerie
   +-- push/pull/fetch/branch switch        -> refused, naming aerie
   +-- stash push/pop/apply, init           -> refused, naming aerie
   +-- open file in editor, blame           -> refused, naming aerie
   +-- filesystem watcher                   -> absent; refresh is explicit
```

Read-only is where this slice stops, not where the feature stops. The north star is full parity across hosts: an operation you can perform on this machine's project you can perform on a bound one. This plan is deliberately shaped so that becomes an added verb and a flipped row in one refusal table, never a rewrite — see [Beyond read-only](#beyond-read-only).

A refusal that names the host is a finished state. A Git tab that stages a file in a same-named local checkout because the label said `[aerie]` is the failure this whole plan exists to prevent, and it is the one outcome no test may pass with.

## User contract

| Gesture | Required result |
| --- | --- |
| Git tab while bound to a connected host that advertises `RepoReadV1` | Aerie's sidebar: aerie's changed files with their staged/unstaged/untracked state, aerie's branch and upstream, aerie's ahead/behind counts, aerie's repository state (merging, rebasing, detached). Nothing on this disk is stat'ed and no local `git` runs for this project. |
| Cursor onto a changed file | Aerie's patch for that file, in the staged or unstaged sense the sidebar row means, rendered by this machine's diff parser and renderer. |
| `Enter` / full-screen diff, side-by-side, full-file, wrap, minimap | Unchanged. They render a patch; which machine produced it is below them. |
| Commit list, graph, commit detail, commit file list | Aerie's, through the host verbs. Author and path filter on aerie, so the answer is about aerie's whole log; subject search runs here, over the rows already returned. |
| Stash list, branch list | Aerie's, read-only. Selecting a branch in the picker refuses rather than switching. |
| Git tab while bound to a host that does **not** advertise `RepoReadV1` | Today's unavailable view, with the reason: that host's Sidecar is too old for the repository contract. Never a guess from a version string, never a local repo. |
| Git tab while bound to a host that is connecting, stale, or unreachable | Unavailable view naming the host and the health reason Sessions already shows. No stale status presented as live. |
| Bound project whose host workspace is not a git repository | "`[aerie] Sidecar` is not a git repository." Never this machine's no-repo view, which offers to run `git init` here. |
| Any write — stage, unstage, stage all, unstage all, commit, amend, discard, push, pull, fetch, branch switch, stash, stash pop, stash apply, init | Refused, naming the host. Nothing is staged, committed, or fetched on either machine. |
| Open file in editor | Refused, naming the host: no host verb opens an editor there, and an editor here would open a same-named path on this disk. |
| Blame | The Git tab binds no blame gesture on either machine; blame is Files', and a bound Files tab refuses it by name from its own table. The row is kept in Git's refusal table so the write slice has one inventory. |
| Open in file browser | Follows to the bound Files tab, which is already remote. |
| Open in GitHub, yank commit, yank id | Work: the remote URL comes from `repo status` and rides on the status answer; the clipboard and the browser are this machine's. |
| `r` / the refresh command while bound | One `repo status` round trip, plus the history and patch reads the current view is showing. |
| A host snapshot generation bump while Git is on screen | Status refreshes on that signal. There is no timer poll and no filesystem watcher. |
| The mouse | The subset the bound keyboard performs: selecting a file or a commit, scrolling the sidebar and the diff pane, focusing a pane, dragging the divider and the scrollbars. The double-click that opens an editor refuses from the same table as its key. |
| Returning to a local project | Exactly today's Git. No remote state survives the unbind. |

## Current behavior

The Git plugin is fully inert while bound. `Init` returns as soon as `ctx.HostID != ""`, before `NewFileTree` and before any state read; `Start` returns nil rather than `detectRepo`; `Update` returns at the top; `View` paints `plugin.FormatRemoteUnavailable`; `Commands()` returns nil; `Diagnostics()` reports one warn row naming the host; `FocusContext()` answers `git-status` so key routing has a context; `inNoRepoMode` is false while bound, so the local `git init` path is never offered for another machine's project (`internal/plugins/gitstatus/plugin.go`, proven in `remote_bind_test.go`).

Every read the plugin performs is a `git` subprocess against `p.repoRoot` through `gitReadOnly` (`gitcmd.go`), spread across `tree.go` (status), `history.go` (log, rev-list, push state), `diff.go` (patches, numstat, name-status), `branch.go`, `stash.go`, `remote.go`, and `github.go`. `p.repoRoot` is a local absolute path; `p.tree` is a `FileTree` of changed paths built from `git status`; `historyLoader` is already an injected function seam used by tests.

`plugin.Context` carries `HostID`, `HostIncarnation`, `ProjectKey`, `HostWorktreeKey`, `HostWorkspaces`, `RemoteControlSpawner`, `RemoteRunner`, `HostVerbs`, and `HostShows`. Files composes its browse identity as `ProjectKey + ":worktree:" + HostWorktreeKey` (`filebrowser/remote_bind.go`); Git needs the same identity and nothing new on the context.

`contentservice` serves file, issue, note, diff, and resource kinds over a durable workspace id, and `contentpanes.Source.LoadDiff` reads them remotely. Its diff kind is `workspacediff`: a **branch-versus-base** snapshot with aggregate committed and uncommitted patches, `merge-base`, and a unique-commit list. It has no staging axis, no per-file staged/unstaged patch, no branch or upstream row, no ahead/behind counts, no stash list, and no author or date on a commit row. It answers the Diff tab's question, not the Git tab's.

`hostserve` advertises `CreateShellAgent`, `ContentReadV1`, `ContentTreeV1`, and `UIRequestRelayV1` in `serveVerbCapabilities`.

## Settled decisions

1. **Git needs a new host verb family, not a reuse of `content read --kind diff`.** The two answer different questions, above. Bending `workspacediff` to carry a staging axis would make the Diff tab's model worse to make the Git tab's model possible; a viewer that showed a base-relative patch where the user asked for the unstaged one would be wrong in the most quiet way available.

2. **That family is `sidecar repo`.** `content` is the read-only *document* contract; repository state is a different subject with different arguments and different caps, and folding it into `content read --kind status` would make one verb's flag set the union of two unrelated ones. `repo` sits beside `content` under the same rules: non-interactive, read-only, workspace-scoped, strictly enumerated, JSON-capped.

3. **`sidecar repo` is not a git wrapper, and its help says so.** Sidecar does not own git. An agent that wants to stage a file runs `git add`; this verb exists because a *viewing Sidecar* needs one machine's repository state in one round trip, normalized to the model its panes already render. The help text names that purpose so nobody adopts it as a general git CLI.

4. **One capability bit, `RepoReadV1`.** A viewer needs to know whether this host speaks the contract, not which subset — the sub-verbs ship together and a host that has one has all of them. A host that predates it is read as false and Git refuses, naming the host. No inference from version strings, the same rule `ContentReadV1` and `ContentTreeV1` already state.

5. **The seam is `RepoSource`, at the point the plugin reads the world.** One interface with a local implementation (today's `gitReadOnly` code moved, not rewritten) and a remote one over `ctx.RemoteRunner`. There is no second status model, no second diff parser, no second renderer, and no second sidebar. Everything above the seam — parsing, hunks, side-by-side, full-file, minimap, graph, search, filters, the cursor, the view — is unchanged and cannot tell which machine answered.

6. **The patch is text; the renderer stays where it is.** `repo diff` returns raw unified diff bytes and the viewer runs `ParseUnifiedDiff` on them, exactly as it does for a local patch. Parsing on the host would put a second parser on the wire and make a host upgrade a rendering change.

7. **Every write refuses, by name, from one table.** Git owns no host write verb and this plan does not invent one. The refusals live in a single map keyed by gesture, the shape `filebrowser/remote_refusals.go` already uses, so a later write slice flips rows rather than hunting call sites. `Commands()` returns the reachable subset while bound so the footer tells the truth.

8. **There is no remote watcher.** `internal/livewatch` is a filesystem signal and does not cross the boundary. Status refreshes on explicit request and on the host snapshot generation the viewer already receives. A timer poll of `repo status` over ssh is cost with no signal behind it.

9. **History is paged, not walked.** `repo history` takes a limit and a cursor. A viewer scrolling a long history asks for more; it never asks a host to serialize an entire log, and the host caps what one call may return regardless of what was asked.

10. **The bound repo's identity is the same durable workspace id Files uses.** `ProjectKey + ":worktree:" + HostWorktreeKey`, composed per call rather than remembered, so a bind that moves to another worktree cannot leave this surface reading the previous one. Nothing new is added to `plugin.Context`.

11. **A bound workspace that is not a repository is its own answer.** It is not this machine's no-repo view: that view offers to run `git init`, and running it would initialize a repository *here*, under a label that says aerie.

12. **GitHub links come from the host's remote URL.** `repo status` returns it, the viewer builds the URL, and this machine's browser opens it. That is the correct division: the URL is the host's fact, opening it is the viewer's action.

## Beyond read-only

The north star is full parity: what you can do to a project on this machine you can do to a bound one. This plan ships the read half, and it is worth being explicit about what the write half will need so that nothing here forecloses it.

- **A `sidecar repo apply`-shaped write verb**, enumerated the way the read verbs are — stage, unstage, discard, commit, and the network operations as distinct named operations with explicit arguments, never a passthrough that runs an arbitrary git command line on the host.
- **Refusal rows become verb calls.** Decision 7 exists for this: the write slice edits one table and adds one command per row, and the gestures, the keys, and the views above them do not move.
- **Confirmation and blast radius.** A destructive gesture aimed at another machine needs the same confirmation as a local one plus the host in the sentence, and `git push` from a viewer needs the host's credentials, which is a question this plan does not answer.
- **Concurrency.** Two viewers and a human at the host can all hold the same index. The read verbs already use `--no-optional-locks`; a write verb needs a real answer about `index.lock` contention rather than a retry loop.

None of that is in scope here, and no half-built write path ships as part of this slice.

## Settled while implementing

- **`NoRepository` is reachable only for a shell-kind workspace.** `contentservice` resolves a `:worktree:` id through `git worktree list`, so a project root without a `.git` is rejected before `reposervice` sees it, with "workspace ... no longer owns this worktree". The Git plugin composes a worktree id, so the "not a git repository" row of the user contract is answered by that **rejection**, not by the `NoRepository` flag. Slice 4g owes the honest sentence: a bound project the host cannot resolve as a worktree must not read as a host failure, and must never fall through to this machine's no-repo view.
- **`sidecar repo` borrows contentservice's resolvers rather than copying them.** `LookupWorkspace` and `ContainedRelative` are exported from `internal/contentservice` for this. Which root a viewer is reading, and whether a path escapes it, are each one rule with one implementation: a second copy is how two verb families start disagreeing about which directory a bound surface is showing.
- **A commit patch keeps git's header.** `git show HASH -- PATH` prints the commit header whether or not the path matched, so a header-only result collapses to empty (the local plugin's `normalizeCommitDiff` rule, ported rather than reinvented) and a real patch keeps its header, which `ParseUnifiedDiff` already skips.
- **An enumerated flag value is a usage error, not a rejected value.** `--mode sideways` exits 2; a workspace, path, or commit the host will not serve exits 5. A viewer branches on the difference.
- **`RepoSource` answers status only, and the branch row rides with it.** The seam is one method because status is the only read slice 4g performs; 4h and 4i add their own. `RepoStatus` carries the branch row and the in-progress state alongside the tree because the host answers all three in one `repo status`, while a local project still gets its branch row from the history load — moving that read into the local source would be two more subprocesses on every refresh, which is not "moved, not rewritten".
- **A bound pane keeps `repoRoot` empty and `hasRepo` false.** That is what makes "no local git for a bound project" structural rather than a matter of remembering: no code path has a directory to run git in even if one were reached. `beginWrite` refuses an empty root for the same reason — otherwise a stray write would run in whatever directory Sidecar was started from.
- **`RepoSource.Diff` takes one request struct, and the mode is the row's.** `DiffRequest{Path, Mode, Commit, Parent}` carries reposervice's own mode strings, so what a viewer names is exactly what `--mode` receives. `diffModeForRow` is the single rule that turns a sidebar row into one: untracked wins, then staged, then unstaged. `Parent` is the first parent of a merge and is local-only — a host resolves parents on its own side, which is why a commit patch is one call either way.
- **The answer's mode is checked against the request.** A host that replied with the unstaged patch for a staged row would be plausible in every visible way, and no rendering assertion would catch it, so `remoteRepoSource.Diff` refuses a mismatch by name.
- **An exit-5 patch is about the path, not about the repository.** The status read is what answers "is this a git repository"; a refused diff says the host will not serve *this* patch and names the mode and path. Routing it through `classify` would have painted "not a git repository" because a row was odd.
- **Full-file view is refused, not invented.** It needs the file's contents on both sides of the change and no `sidecar repo` verb answers those. `loadFullFileDiff` returns nothing for a bound pane and both diff surfaces say why and render the patch the host *did* answer, rather than sitting on "Loading full file…". The viewer's saved `full-file` preference applies to whichever machine owns the project, so this state is reachable on the first frame of a bind, not only after a keypress.
- **A folder row is an aggregate, and a bound one refuses.** `GetFolderDiff` is a loop over the files in the folder; serving it from a host would make one cursor move one round trip per file. The bound pane says so and points at `enter`, and the files inside read their own patches. The previous row's patch is cleared when the cursor lands on a folder, so nothing stale is left on screen under the folder's name.
- **The two diff surfaces are the local handlers themselves.** Once patches load through the seam, `updateDiff` and `updateStatusDiffPane` reach nothing that takes a directory on this disk, so the bound key loop routes to them rather than growing a second copy. The two loaders that do — a folder's aggregate patch and a full-file view — refuse at their own door, which keeps "no local git while bound" structural rather than a property of the key list.
- **Truncation is the source's fact and rides with the patch.** `RepoDiff.Truncated` reaches both loaded messages and both headers label it. A local read never sets it: `GetDiff` does not cap.
- **A history request carries the viewer's position twice, and each source reads the one it understands.** `HistoryRequest` has both a `Cursor` — the previous page's last hash, which is the host's contract — and a `Skip`, which is what a local `git log` walk takes. The caller fills both from the list it already holds, so the plugin never branches on which machine it is talking to, and neither implementation has to adopt the other's notion of where a log is. Making the local half take a hash cursor would have been a rewrite of a working paging path for no gain; making the host take an offset would have been decision 9 in reverse.
- **Author and path filter on the host; subject search does not.** `repo history` takes `--author` and `--path` because git narrows the log before it is serialized, which is what a local project already does. A subject search runs in the viewer over the rows in hand — also what a local project already does — and the two are not symmetrical by accident: a filter that ran over one page would present itself as an answer about the whole history, and the wire is the only place that can be true. The `history` CLI help already said this; the seam now matches it.
- **The branch row is answered once per machine, and the history load stopped clobbering it.** A host answers it with `repo status` in the same refresh and stamps each row's own `Pushed`; a local project still gets it from the history load's `GetPushStatus`. `applyRecentCommits` therefore assigns `p.pushStatus` only when the page actually carried one, rather than blanking a branch row that arrived from a status read seconds earlier.
- **A rejected page is about the query; a rejected `refs` is about the workspace.** `repo history` and `repo commit` take arguments a host may refuse — a filter, a cursor, a hash — so an exit 5 there names the query, the same rule 4h settled for a patch. `repo refs` takes none beyond the workspace, so its exit 5 is the workspace answer and routes through `classify`.
- **No host verb answers a total commit count.** `GetCommitCount` is a local `rev-list --count`, and a bound pane has no directory to run it in, so the sidebar header shows the page it has rather than a total it would have to guess. That is structural rather than a guard: `loadCommitCount` already required a repository root.
- **The stash list rides on `Refs` but has no read-only surface yet.** The Git tab's only stash reader is the stash-pop confirm, which is a write and refuses. The list is routed through the seam anyway, so the write slice does not have to reach around it, and the remote half is proven at the wire instead of at a pane that does not exist.
- **"Open in GitHub" works, because the remote URL rides on the status answer.** 4i left it refusing at its door for want of a carrier; 4j gave it one. `RepoStatus.RemoteURL` is filled by whichever source answered — a host from `repo status`, a local project not at all — and `openCommitInGitHub` reads it while bound and still asks git while local, so nothing changed for a local pane and nothing runs git for a bound one. That is the division decision 12 states: the URL is the repository's fact, opening it is this machine's browser.
- **"Commits are not read from [aerie] yet" is gone, and a failed history read is a diagnostic.** The empty state is the shared "No commits" for both machines. Because that sentence is true for an empty log and for a log the host would not serve, a bound `Diagnostics()` carries the history error as its own warn row.
- **Sidebar movement is four shared functions.** `cursorDown`, `cursorUp`, `cursorToTop`, and `cursorToBottom` are lifted out of the local key switch and used by both loops. Two copies of "which row is the cursor on, and does reaching it need another page" is how one surface quietly stops paging while the other keeps working.
- **The mouse is the keyboard's subset, routed to the same handlers.** `updateRemote` now routes `tea.MouseMsg` to `updateRemoteMouse`, which answers the three view modes a bound pane can be in and nothing else. Inside them it calls the local handlers, for the reason the two diff surfaces do: everything they reach reads through `RepoSource`. Exactly one pointer gesture did not — the double-click that opens a file in an editor — and it now goes through `openFileEntry`, which both the key and the click share, so the two cannot drift. A folder's double-click in the diff pane stays put rather than opening a full-screen view that would never load.
- **The refusal table is keyed by gesture, not by key.** Files could key its table by keystroke because its refused gestures each own one. Git's do not: `f` is fetch on a file row and an author filter on a commit row, `enter` is an editor on a file and an expansion on a folder, and the branch-switch refusal is reached through a modal rather than a keypress. So the table maps a named gesture to what it would have done, a small second map names the keys whose only meaning is one of them, and the three context-sensitive gestures resolve the row first and then refuse out of the same table.
- **Two rows have no key, and they are the honest ones to keep.** `init` is structurally unreachable — a bound pane never enters no-repo mode, which is the only view that offers it — and the Git tab binds no blame gesture at all on either machine. Both stay listed because the contract names them and because the write slice should work from one inventory rather than two, and a test asserts the table is exactly the contract's list.
- **Structural guards stay where they are, and register a row as well.** `beginWrite` still refuses an empty repository root, and `loadFolderDiff`, `loadFullFolderDiff`, and `loadFullFileDiff` still refuse a bound pane at their own door. Those are not refusals of a gesture; they are the reason no code path has a directory to run git in. Deleting them because the table now catches the keys would turn a structural property into a property of the key list.
- **`Commands()` is the reachable set, checked against what the loops accept.** The bound footer gained `open-in-file-browser` in `git-status` and `open-in-github` in the two commit contexts, which are gestures 4g–4i left working and unadvertised, and gained the `O` and `o` keys that reach them. It does not list `open-in-github` in `git-status`, where a local pane does, because on a file row that key does nothing. A test asserts the exact set both ways — nothing advertised that refuses, nothing performed that is hidden — and that every advertised id is a command this plugin has.
- **"Both repositories untouched" is a fingerprint, not an absence of complaint.** Each refused gesture is driven with `git status --porcelain` and `git rev-parse HEAD` taken on the twin before and after (through git's absolute path, so the PATH shim can stand in for git during the drive), and with the host's call log checked to be empty across every refusal: a refused key asks the host for nothing at all. A refusal that returned early after already running git passes a sentence assertion and fails this one.
- **The bound message loop is its own entry point** (`updateRemote`), not a set of guards inside the local one. The local handlers reach stage, discard, push, and the patch loaders, all of which take a path on this disk; a whitelist at the door is checkable, and a dozen guards spread through them is not.

## Slices

Each sub-slice is independently testable and leaves the tree in a shippable state. Every commit references its td task. Slice letters continue the ones Files used, so the ordering across slice 4 stays unambiguous.

### 4f — the `sidecar repo` verb family — implemented (td-3a4da2)

`internal/reposervice` (new, sibling to `internal/contentservice`, and using its containment, encoding, and error conventions rather than new ones):

- `Status(workspace)` — branch, upstream, ahead/behind, detached and in-progress state (merge, rebase, cherry-pick, bisect), remote URL, stash count, and the changed-file rows with staged, unstaged, and untracked state per path.
- `Diff(workspace, path, staged|unstaged|untracked)` and `CommitDiff(workspace, hash, path)` — one raw unified patch, capped, with a truthful truncation flag.
- `History(workspace, limit, cursor, filters)` — commit rows with hash, subject, author, date, parent hashes, and pushed state.
- `Commit(workspace, hash)` — subject, body, author, date, parents, merge flag, and the file list with status and add/delete counts.
- `Refs(workspace)` — local and remote branches, and the stash list.

`internal/cli/repo.go`: `sidecar repo status|diff|history|commit|refs --workspace ID [...] [--json]`, with the enumerated exit codes the `content` verbs use (0 answered, 1 internal, 2 usage, 5 rejected). Help text carries decision 3.

`internal/hostserve/serve.go`: `RepoReadV1: true` in `serveVerbCapabilities`, and the field on `hostproto.VerbCapabilities` with the degradation rule in its doc comment.

Proof: unit tests over a temporary repository fixture for each verb — a staged and an unstaged change to the same path returning different patches, an untracked file, a detached HEAD, an in-progress rebase, a repository with no upstream, a workspace that is not a repository, an unknown workspace, and a patch large enough to truncate. CLI tests for the JSON contract and each exit code.

### 4g — the `RepoSource` seam and a bound status pane — implemented (td-ca9621)

`internal/plugins/gitstatus`: a `RepoSource` interface consumed wherever the plugin reads repository state, a local implementation that is today's `gitReadOnly` code moved rather than rewritten, and a remote implementation over `ctx.RemoteRunner` gated on `ctx.HostVerbs().RepoReadV1`.

`Init`/`Start`/`Update`/`View`/`Commands`/`Diagnostics` stop returning early on `HostID` alone and instead branch on whether a usable remote source exists. `View` keeps the unavailable state for every case where one does not, with the reason distinguishing "host offline", "host too old", "no bound workspace", and "not a git repository".

Proof: a bound plugin with a fake `RepoSource` paints aerie's sidebar; a **local twin repository** containing a staged file named `LOCAL-TWIN` is present on disk and never appears; no `git` subprocess runs against any local path while bound (asserted by a PATH shim, the way the twin tests already assert); a host without `RepoReadV1` refuses naming the host.

### 4h — patches through the seam — implemented (td-d7a47f)

Working-tree diffs — staged, unstaged, and untracked — load through `RepoSource`. The inline diff pane and the full-screen diff view are unchanged: they receive raw patch text and parse it as they do today.

Proof: the diff pane shows `REMOTE-MARKER` for a twin path and never `LOCAL-TWIN`; the staged and unstaged patches for one path differ and each row loads its own; a truncated patch is labelled rather than silently short. The no-local-git tripwire drives the patch surfaces under the PATH shim, and a local fixture asserts the routed loaders return exactly the bytes the three git readers do.

### 4i — history, commits, and refs — implemented (td-edd823)

Commit list, graph, commit detail, commit file list, stash list, and branch list load through `RepoSource`, paged per decision 9. Author and path filter on the host; subject search runs in the viewer over the rows it holds. The branch picker lists and refuses to switch.

Proof: the commit list is the host's; the graph draws from host parent hashes; scrolling past the first page issues exactly one more call; the branch picker's Enter refuses naming the host.

### 4j — honest refusals for everything else — implemented (td-6fa047)

One refusal table covering stage, unstage, stage all, unstage all, commit, amend, discard, push, pull, fetch, branch switch, stash, stash pop, stash apply, init, open in editor, and blame, each answering with `plugin.FormatRemoteUnavailable`-shaped text naming the host. `Commands()` returns the reachable subset while bound rather than nil.

Status refresh binds to the host snapshot generation already delivered in project scope; no watcher is started while bound.

Proof: each refused gesture is asserted to name the host and to leave both repositories untouched, checked by comparing `git status --porcelain` and `git rev-parse HEAD` on both sides before and after; no `startWatcher` while bound.

### 4k — proof and docs

Run on the slice 2.5 loopback fixture, which already plants a host project with `REMOTE-MARKER` and a viewer twin with `LOCAL-TWIN`. The fixture gains a staged change, an unstaged change, an untracked file, and a second commit on the host so the Git tab has something to show, and the same shapes on the twin so a viewer reading the wrong machine is visible rather than merely wrong.

`docs/reference/cli.md` documents `sidecar repo`.

```bash
./scripts/loopback-remote.sh up
# 8 (Sessions) until loopback is LIVE, then @ -> [loopback] Loopback -> 2 (Git)
./scripts/loopback-remote.sh down
```

## Proof and isolation

Same bar as every slice before it: private tmux sockets and private Sidecar state on both sides, `SIDECAR_ISOLATED_STATE=1`, no live workstation as a default. The tripwire this plan owes is a **local twin repository**: a same-named checkout on the viewer with its own staged file, its own branch name, and its own commits. A test that passes while showing those has failed regardless of what it asserted, and a test that passes while a `git` subprocess ran against a local path has failed for the same reason.

Packages: `internal/reposervice` (new), `internal/cli`, `internal/hostserve`, `internal/hostproto`, `internal/plugins/gitstatus`.

## Related plan updates

- [remote-project-switcher.md](remote-project-switcher.md): slice 4's Git half is this document. td, Tasks, and Notes remain.

## Changelog

- **2026-09-01** — Slice 4j implemented: one refusal table in `gitstatus/remote_refusals.go` covers stage, unstage, stage all, unstage all, commit, amend, discard, push, pull, fetch, branch switch, stash, stash pop, stash apply, init, open in editor, and blame, each answering with `plugin.FormatRemoteUnavailable`-shaped text that names the host. The branch-switch refusal moved out of the picker into it; the structural guards that make "no local git while bound" true — an empty `repoRoot` in `beginWrite`, and the three loaders that take a directory on this disk — stayed and registered rows as well. `Commands()` is now the reachable subset in both directions, with `open in file browser` and `open in GitHub` added because they work: the host's remote URL rides on the status answer, so the GitHub link is built from the host's fact and opened by this machine's browser. The mouse stopped being inert: selection, scrolling, pane focus, the divider, and the scrollbars route to the local handlers, and the one pointer gesture that reached a write refuses from the table like its key. Every refusal is proven to name the host, to ask the host for nothing, and to leave the twin's index and HEAD exactly where they were.
- **2026-09-01** — Slice 4i implemented: the commit list, the graph, commit detail with its file list, the branch picker, and the stash list load through `RepoSource`. History is paged by cursor — scrolling past the first page issues exactly one more `repo history` call, proven against a two-page host log — author and path filter on the host while subject search runs in the viewer, and the branch picker lists the host's branches and refuses to switch by name. The branch row is read once per machine: a host answers it with `repo status` and stamps each row's pushed state, a local project still gets it from the history load. The local half is today's `history.go`, `branch.go`, and `stash.go` routed rather than rewritten, and the no-local-git tripwire now drives the history, commit-detail, search, filter, and branch-picker surfaces under the PATH shim.
- **2026-09-01** — Slice 4h implemented: staged, unstaged, untracked, and commit-file patches load through `RepoSource.Diff`, so a bound pane shows the host's patch for the row the cursor is on, in that row's staging sense. The inline pane and the full-screen diff view both reach it, the local half is today's `GetDiff`/`GetNewFileDiff`/`GetCommitDiff` routed rather than rewritten (proven byte-for-byte against a real repository fixture), and a truncated patch is labelled in both headers. Full-file view and a folder's combined patch refuse by name on a bound pane; nothing else about the parser, the renderer, wrap, or the minimap moved.
- **2026-09-01** — Slice 4g implemented: `RepoSource` with a local and a remote implementation, and a bound status pane showing the host's changed files, branch, upstream, ahead/behind, and in-progress state. The four unavailable reasons are distinct sentences, and "not a git repository" is answered on both of its paths — the `NoRepository` flag and the exit-5 rejection of a worktree id the host will not resolve. A local twin repository is planted in every bound test and a `git` PATH shim records any local invocation.
- **2026-09-01** — Slice 4f implemented: `internal/reposervice`, `sidecar repo status|diff|history|commit|refs`, and `RepoReadV1`. `contentservice` exports `LookupWorkspace` and `ContainedRelative` so there is one workspace resolver and one containment rule across both verb families.
- **2026-09-01** — Created. Split out of remote-project-switcher.md slice 4 once it was clear Git needs a verb family of its own: `contentservice`'s diff kind is branch-versus-base and carries no staging axis, no branch or upstream row, and no author or date, so it cannot answer the Git tab.
