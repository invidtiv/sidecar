# Plan: Worktree lifecycle hardening

**Research snapshot:** 2026-08-08  
**Status:** Proposed  
**Scope:** Workspace creation on disk, Sidecar tracking and switching, task links,
diff/status presentation, push, direct merge, pull request creation/import, and
post-merge cleanup.

## Decision first

Treat a worktree lifecycle as one explicit, sequential operation over an immutable
repository snapshot. The TUI should render and drive that operation; it should not
own Git rules, infer paths again halfway through the workflow, or mutate branch refs
behind another worktree's back.

The target shape is:

1. Identify the repository, worktree, branch, base, remote, and checked-out paths
   once before an operation starts.
2. Run a visible preflight that refuses unsafe states before changing Git or disk.
3. Execute steps sequentially, recording which mutations completed and what recovery
   is available.
4. Return typed results to Bubble Tea; only `Update` mutates plugin state.
5. Refresh from Git after completion rather than treating optimistic UI state as
   authoritative.

Sidecar remains a presentation-layer tool, so this plan does **not** add a Sidecar
CLI for operations Git and `gh` already own. It does move validation, resolution,
refusal, and sequencing into state-free or narrowly stateful library functions that
can be tested headlessly and reused if ownership changes later.

Use pull requests as the recommended integration path. Keep direct merge, but only
after it can target the worktree that actually has the base branch checked out and
can leave a clear recovery state on conflict or push failure. Do not expand merge
strategies until the existing `--no-ff` path is safe; strategy choice is a later,
separable enhancement.

## User journey and acceptance evidence

A user can create a workspace from any Sidecar worktree, understand what setup did,
work and inspect the complete change, then either open a PR or merge directly without
damaging another checkout:

1. Creation names the exact base ref, branch, directory, task, and setup actions.
2. Sidecar tracks the new worktree with an identity that cannot collide with a
   similarly named or nested worktree.
3. Switching restores that worktree's own plugin/file state and drops stale async
   results from the previous context.
4. The Diff tab distinguishes working-tree changes, commits, and the aggregate branch
   change against the selected base. Errors and truncation are visible.
5. PR creation shows the resolved remote/base/head, lets the user review/edit the
   title and body, and reaches a terminal state for merged, closed, or unavailable.
6. Direct merge works when the base is checked out in the main worktree, refuses a
   dirty or ambiguous target, and never rewrites a checked-out branch ref without
   updating its index and working tree.
7. Cleanup is sequential, never runs from a directory it just deleted, and deletes
   only the items the user selected after revalidating their identities.
8. All modal content fits at supported terminal sizes. In particular, the task-link
   search field remains on one line inside its modal.

Real proof requires disposable repositories with two or more linked worktrees, a
bare remote, an isolated Sidecar state tree, and Sidecar's private tmux server. Unit
fixtures alone are not sufficient.

## Audit findings

### Critical

#### 1. Direct merge targets the wrong checkout and normally cannot check out the base

`performDirectMerge` runs fetch, checkout, pull, merge, and push in
`p.ctx.WorkDir`. When Sidecar is currently opened on the feature worktree, Git refuses
`git checkout <base>` because the base is already checked out in the main worktree.
When Sidecar is opened on main, the workflow mutates the user's live main checkout
without first checking its dirtiness or preserving its original branch.

A disposable two-worktree reproduction produced:

```text
fatal: 'main' is already used by worktree at '.../repo'
```

If checkout, pull, merge, or push fails after an earlier mutation, the workflow
reports an error but does not describe or repair the partially changed repository.
A merge conflict can be left active; a successful local merge followed by a failed
push is presented only as “Direct Merge Failed.”

Affected code: `internal/plugins/workspace/merge.go` (`performDirectMerge`,
`transitionToMergeError`).

#### 2. Post-merge pull can desynchronize a checked-out worktree

`proceedToMergeWorkflow` records the branch of `p.ctx.WorkDir`, not the branch checked
out in the main worktree. If Sidecar is active in a feature worktree, post-merge pull
therefore takes the “base is not current” path and runs:

```text
git update-ref refs/heads/<base> origin/<base>
```

Git permits this even when `<base>` is checked out in another worktree. The ref moves,
but that worktree's index and files do not. A disposable reproduction moved `main`
to the feature commit and immediately made the main checkout report a staged deletion.
This is data-integrity risk, not only stale presentation.

Cleanup also launches local deletion, remote deletion, and base update concurrently.
Those commands share one repository and can race with deletion of their command
directory.

Affected code: `internal/plugins/workspace/merge.go` (`proceedToMergeWorkflow`,
`pullAfterMerge`, `advanceMergeStep`, `performSelectedCleanup`,
`deleteRemoteBranch`).

#### 3. Per-worktree metadata collides for nested or same-basename paths

`projectdir.WorktreeDir` keys metadata only by `filepath.Base(worktreePath)`. These
valid, simultaneously present worktrees resolve to the same state directory:

```text
/repos/feature/auth
/repos/fix/auth
```

Task IDs, base branches, PR URLs, and chosen agents can therefore overwrite each
other silently. Hierarchical branch names make this a normal case rather than an
exotic filesystem edge case. The same display name is also used as an async message
and agent-map key in several paths.

Affected code: `internal/projectdir/projectdir.go`,
`internal/plugins/workspace/worktree.go`, `stats.go`, `messages.go`, and agent/session
maps in the workspace plugin.

### High

#### 4. Async Git operations are not bound to an immutable context

Some commands capture paths and an epoch correctly; many creation, task, conflict,
merge, PR, remote, and cleanup commands read the shared mutable `p.ctx` inside their
async closure. `Registry.Reinit` mutates that context in place. The workspace
plugin's `Init` resets worktree/agent lists but does not cancel/reset every modal and
merge workflow, and most lifecycle result messages carry no epoch or operation ID.

An operation started in repository A can finish after a project/worktree switch and
be applied to repository B's UI or use B's root for metadata. `loadConflicts` reads
plugin slices from a command goroutine, and `performSelectedCleanup` mutates
`managedSessions` from its command goroutine, outside Bubble Tea's update loop.

Affected code: `internal/plugin/registry.go`, `internal/plugins/workspace/plugin.go`,
`worktree.go`, `conflicts.go`, `merge.go`, `fetch_pr.go`, and `update.go`.

#### 5. Creation reports success across important partial failures

Once `git worktree add` succeeds, failures writing `.td-root`, task/base/agent
metadata, starting `td`, copying environment files, or running the setup script are
log-only. `setupWorktree` always returns `nil`, so the caller cannot even aggregate
its warnings. `CreateDoneMsg` then appends the worktree and starts or attaches the
agent as if setup were complete.

The setup implementation also claims to source the main worktree but reads env files
and `.worktree-setup.sh` from `p.ctx.WorkDir`; when Sidecar was started in a subfolder
or another linked worktree, that is not the main root. `MAIN_WORKTREE` is populated
with the same possibly incorrect path.

There is no in-progress guard, so repeated submit keys can start concurrent creation
commands. There is also a trust issue: by default Sidecar silently copies common
`.env` files and executes any checked-in `.worktree-setup.sh` through Bash. That
duplicates secrets and runs repository code without showing those actions in the
confirmation UI.

Affected code: `internal/plugins/workspace/worktree.go`, `setup.go`,
`create_modal.go`, and the `CreateDoneMsg` handler in `update.go`.

#### 6. Merge and cleanup lack a safe preflight and recovery contract

Only the feature worktree's uncommitted state is checked. The target worktree, merge
state, upstream relationship, remote identity, target protection, and whether HEAD
changed since review are not revalidated. Direct merge hardcodes `origin`, `git pull`
with Git's ambient strategy, `--no-ff`, and an automatic push.

After PR/direct merge, local worktree removal, local branch deletion, remote branch
deletion, and pull run as loosely coordinated operations. Local branch deletion
falls through to `-D`; unlike the standalone delete helper, this cleanup path does
not use the default-branch guard. Cancellation only hides the workflow and cancels
PR text generation; it does not abort an active Git merge/rebase or explain how to
continue.

The main worktree is offered Delete, Push, Merge, and task-link commands just like a
feature worktree. Most of these eventually fail, but the UI invites invalid and in
some cases risky workflows.

Affected code: `internal/plugins/workspace/commands.go`, `keys.go`, `merge.go`, and
`view_modals.go`.

#### 7. Pull request identity and terminal states are incomplete

PR import requests only `headRefName` and assumes the PR targets the repository's
default branch. It cannot correctly import a PR against a release branch and cannot
fetch a fork PR through `origin <headRefName>`. PR creation/push also assumes
`origin`, regardless of branch upstream or GitHub repository topology.

Existing PR detection parses an English `gh` error string. Waiting polls the current
branch with `gh pr view`, records only “merged or not,” and retries forever if the PR
is closed without merge. There is no stable PR number/node identity in workflow
state.

PR title/body generation automatically invokes the selected agent with the branch
diff and immediately creates the PR. The user cannot review/edit the generated text,
and code may be sent to an external model as a surprising side effect of choosing
Merge.

Affected code: `internal/plugins/workspace/fetch_pr.go`, `merge.go`,
`fetch_pr_view.go`, and merge modal sections in `view_modals.go`.

#### 8. Diff/status failures are rendered as an empty clean state

`getDiff` suppresses both its fallback error and all untracked-file errors. The
plugin defines `DiffErrorMsg` and `StatsErrorMsg`, but `Update` does not handle them.
The Diff tab then renders “No changes,” conflating clean, loading, truncated, and
failed states.

The default file list combines uncommitted files with individual commits; it does
not offer one aggregate branch diff against the selected base. Untracked content is
silently capped at 50 files, while stats read every untracked file through `wc -l`.
The cap is not disclosed in the UI. Conflict detection compares only staged,
unstaged, and untracked paths, so committed edits on two worktree branches are not
reported even though the label suggests branch-level conflict detection.

Affected code: `internal/plugins/workspace/diff.go`, `stats.go`, `conflicts.go`,
`view_diff.go`, and `update.go`.

### Medium

#### 9. The known task-link modal width bug is reproducible

At 80x42, the real isolated app renders the right border of the task search field on
new lines below the field. The modal uses the legacy manual `modalStyle` path, treats
`modalW` as both content and outer width, imposes a minimum input width that can
exceed the available content, and computes mouse regions from nominal rather than
rendered geometry.

The modal also has a fixed eight-row dropdown rather than a height-aware viewport.
Title truncation uses byte length/slicing, which can split UTF-8 and does not model
terminal cell width. The create modal's custom task section repeats some of this
width logic, although the observed wrapping defect is in the existing-worktree
“Link Task” modal.

Affected code: `internal/plugins/workspace/view_modals.go` (`renderTaskLinkModal`),
`create_modal.go`, `keys.go`, and `mouse.go`.

#### 10. Worktree switching restores project-wide, not worktree-local, UI state

The implemented design says active plugin and sidebar state are per worktree, but
`switchProject` stores `ActivePlugin` by `ProjectRoot`, and the file browser also
persists content state by `ProjectRoot`. Switching branches can therefore carry tabs,
selected files, scroll positions, and active-plugin choice into a worktree where
those paths do not exist. Workspace selection/shell-name state is also root-keyed but
is genuinely repo-wide; the fix should classify fields rather than blindly changing
every state key.

The switch path synchronously shells out repeatedly: the `W` handler lists worktrees,
`initWorktreeSwitcher` lists them again, and `switchProject` re-queries repository
name/main/list information several more times before plugins restart.

Affected code: `internal/app/model.go`, `worktree_switcher_modal.go`, `update.go`,
`internal/state/state.go`, and plugin state callers.

#### 11. Display names and branch assumptions leak into correctness

Nested worktrees have a relative display name such as `feature/auth`, but
`loadStats` sends `filepath.Base(path)` (`auth`) as the result key, so stats do not
attach to the listed worktree. Creation validation approximates Git ref rules rather
than asking Git; component-level `.lock` cases can pass the UI and fail later.

Detached, bare, locked, missing, and externally created worktrees are parsed only
partially. Push, merge, task, and delete commands remain visible even when their
branch/path state cannot support the operation. `CreatedAt` is reset to `time.Now()`
on every list parse and `UpdatedAt` is not reconstructed, so age presentation is not
authoritative.

Affected code: `internal/plugins/workspace/worktree.go`, `stats.go`, `commands.go`,
and `view_list.go`.

#### 12. Refresh cost scales as unbounded subprocess fan-out

Each refresh lists worktrees, launches stats work per worktree (multiple Git commands
plus `wc` batches), launches separate conflict scans per worktree, and reloads the
selected diff/commit data. `tea.Batch` permits these to overlap without a repository
operation queue or concurrency bound. On repositories with many worktrees or many
untracked files this creates avoidable process and filesystem pressure; `wc` also
reads untracked paths without the diff view's size limit.

No measurement currently records refresh subprocess count, latency, truncation, or
which result generation won. Optimize from an inventory snapshot and measured trace,
not by adding a cache that can hide Git changes.

Affected code: `internal/plugins/workspace/worktree.go`, `stats.go`, `conflicts.go`,
`diff.go`, and the refresh handler in `update.go`.

## What is already sound

- Worktree creation itself uses argument vectors rather than a shell and places new
  worktrees relative to `ProjectRoot`, avoiding the historical subfolder bug.
- Branch pushes use `--force-with-lease` when force is requested rather than raw
  `--force`.
- Selected diff and many detailed diff messages carry epochs and validate the
  selected workspace/file before applying.
- Plugin startup defers worktree and shell discovery to commands, preserving the
  first-frame contract.
- The global switcher validates path existence and reinitializes plugins through one
  registry seam.
- Focused tests and `go test -race` for `internal/plugins/workspace`, `internal/app`,
  and `internal/projectdir` pass. The gap is lifecycle integration coverage, not a
  generally broken test suite.

## Implementation plan

### Slice 1 — Stop unsafe merge and cleanup behavior

Ship the smallest safety improvement before refactoring the whole lifecycle.

1. Disable Merge/Delete/Push actions that are invalid for main, detached, bare,
   locked, or missing worktrees; explain the refusal in the UI.
2. Replace the `update-ref` post-merge path. Resolve which worktree has the base
   branch checked out and update it with a normal `pull --ff-only` only when clean.
   If no safe checked-out target exists, fetch and report that the local base was
   intentionally left unchanged.
3. Run cleanup steps sequentially from an immutable surviving repository path.
   Never start remote/base operations in parallel with worktree deletion.
4. Add a direct-merge preflight: stable source/target OIDs, clean source and target,
   no merge/rebase/cherry-pick in progress, resolved remote, and target checkout.
5. On failure, return completed steps, current Git state, and explicit Continue,
   Abort, Retry Push, or Dismiss choices as applicable. Do not silently run
   `merge --abort` if it would discard user conflict resolution.

Primary files: `internal/plugins/workspace/merge.go`, `commands.go`, `keys.go`,
`view_modals.go`, plus a new focused Git lifecycle file/package.

Acceptance proof:

- Direct merge from Sidecar running in both main and feature worktree contexts.
- Base checked out in main; feature checked out elsewhere; no checkout refusal.
- Dirty target refusal makes no mutation.
- Conflict leaves a visible recoverable state; Abort restores the pre-merge target.
- Push failure preserves the local merge and offers Retry Push.
- Post-PR pull never changes a checked-out ref behind its worktree.

### Slice 2 — Introduce stable identity and immutable operation context

1. Add a `RepoSnapshot`/`WorktreeSnapshot` model populated from one Git inventory.
   It should carry canonical common-dir/root, normalized path, branch/detached state,
   HEAD OID, base ref/OID, checked-out branch map, remote/upstream, lock/prunable
   state, and a stable operation key.
2. Separate `Worktree.Key` from `Worktree.Name`; use the key for maps, messages,
   polling generations, metadata, and stale-result checks. Name remains presentation.
3. Make per-worktree storage collision-safe and inspectable: allocate a slug with a
   path `meta.json` (or a short path hash plus metadata), migrate existing basename
   directories on first unambiguous access, and refuse ambiguous legacy collisions
   rather than choosing one silently.
4. Give every lifecycle command `{epoch, operationID, repoKey, worktreeKey}` and
   capture all paths/arguments before returning the `tea.Cmd`.
5. Cancel owned subprocess contexts from `Stop`/`Init`; reset operation/modal state on
   reinit. Apply results only when all identity fields still match.
6. Keep all plugin map/slice mutation in `Update`; commands return typed results only.

Primary files: `internal/projectdir`, `internal/plugin`,
`internal/plugins/workspace/{types,messages,plugin,update,worktree,merge,fetch_pr}.go`.

Acceptance proof:

- `/repos/feature/auth` and `/repos/fix/auth` retain independent task/base/PR/agent
  metadata across restart.
- Switching projects during a deliberately delayed refresh/PR operation cannot
  change the new project's state.
- Race tests exercise switch/cancel/result delivery and remain green.
- Legacy state migrates without modifying unrelated project directories.

### Slice 3 — Make creation an explicit, recoverable setup operation

1. Resolve/validate the branch with Git plumbing and show the exact source ref/OID,
   destination path, remote policy, and task before submit.
2. Add an operation-busy state that disables duplicate submit/cancel races and shows
   the current creation/setup step.
3. Split the result into “worktree created” and setup outcomes. Persist core identity
   first; return structured warnings for `.td-root`, task link/start, agent metadata,
   env copy, and setup hook.
4. Source main-root artifacts from `ProjectRoot`. Pass accurate `MAIN_WORKTREE`,
   `SOURCE_WORKTREE`, `WORKTREE_PATH`, and `WORKTREE_BRANCH` values.
5. Make env copying and setup-hook execution visible, configurable per project, and
   consented. Preserve a convenient trusted-repository default only if the UI names
   the files/script before execution. Never log env contents.
6. If setup fails, keep the successfully created worktree, do not auto-start an agent,
   and offer Retry Setup, Open Anyway, or Delete Newly Created Worktree. The delete
   option must revalidate that HEAD/path still match the creation result.
7. Start/link the td task only after its metadata is durably associated with the new
   worktree; report td failure without pretending the link vanished.

Primary files: `worktree.go`, `setup.go`, `create_modal.go`, `messages.go`,
`update.go`, configuration loading, and project documentation.

Acceptance proof:

- Create from main, a linked worktree, and a repo subdirectory.
- Hierarchical branch and destination paths.
- Missing `td`, unwritable state, failed setup hook, existing branch/path, and rapid
  double-submit.
- Env/hook choices are visible and no agent starts after failed required setup.

### Slice 4 — Make diff and status authoritative, bounded, and efficient

1. Define three explicit views from the same snapshot:
   - Working tree: staged, unstaged, and untracked changes versus HEAD.
   - Commits: commits unique to the branch versus the resolved base.
   - Aggregate: the complete branch change from merge-base to HEAD, plus clearly
     separated uncommitted changes.
2. Model loading, clean, error, and truncated states separately. Handle
   `DiffErrorMsg`/`StatsErrorMsg` and show actionable command/ref context.
3. Disclose untracked file/count/byte caps. Use `Lstat` and bounded reads/counting;
   do not invoke `wc` over arbitrary untracked paths.
4. Reuse one status/inventory result for stats, dirty-file overlap, list badges, and
   action gating. If “Conflicts” remains, compare committed branch changes against a
   common base as well as working-tree paths, or rename it to “Overlapping dirty
   files” so the UI does not overclaim.
5. Replace display-name result routing with stable keys and cell-width-safe path/title
   truncation.
6. Instrument refresh duration and subprocess counts under the existing startup/diag
   conventions. Bound per-worktree concurrency, then remove duplicated Git calls
   demonstrated by the trace. Do not add long-lived caches until measurements require
   them.

Primary files: `diff.go`, `stats.go`, `conflicts.go`, `view_diff.go`,
`view_list.go`, `update.go`, and shared git-status parsing where appropriate.

Acceptance proof:

- Aggregate diff with multiple commits plus staged, unstaged, untracked, binary, and
  oversized files.
- Invalid/missing worktree shows Error, never “No changes.”
- Nested worktree stats route to the correct row.
- Refresh with 1, 10, and 50 worktrees has a recorded process/latency budget and
  bounded concurrency.

### Slice 5 — Harden PR creation, import, and terminal states

1. Resolve remote/head/base from Git and GitHub metadata rather than hardcoding
   `origin`/default branch. Import `baseRefName`, head repository/owner, PR number,
   URL, and node ID; support fork PRs through `gh pr checkout` semantics or an
   equivalent explicit refspec.
2. Push only the reviewed source OID. If HEAD changes after review, return to review
   rather than creating a PR for different code.
3. Query existing PR identity structurally (`gh ... --json`), not from localized error
   text. Store PR number/URL in workflow state and centralized metadata.
4. Make agent-assisted description generation opt-in. Show whether it may contact an
   external provider, cap the supplied diff, and always present editable title/body
   before the PR is created. A deterministic commit/diff summary remains the default
   fallback.
5. Poll by stable PR identity and handle OPEN, MERGED, CLOSED, authentication failure,
   repository mismatch, and network unavailable as distinct states. Use bounded
   backoff and let the user stop watching without losing the PR URL.
6. Revalidate merged OID/base before offering destructive cleanup; squash/rebase PRs
   may not make `git branch -d` consider the head merged, so any force deletion must
   be an explicit, explained choice.

Primary files: `fetch_pr.go`, `fetch_pr_view.go`, `merge.go`, `view_modals.go`, and
the operation model from Slice 2.

Acceptance proof:

- Same-repo PR, fork PR, non-default base, existing PR, closed-unmerged PR, squash
  merge, and authentication/network failure.
- User edits generated/fallback title and body before creation.
- Cleanup never infers “safe to force-delete” solely from `branch -d` failure.

### Slice 6 — Finish worktree UI and switching behavior

1. Replace the manual task-link modal with `internal/modal` sections and rendered
   geometry. Share one task picker between creation and existing-worktree linking.
2. Derive input/list widths from modal `contentWidth`; remove minimums that can exceed
   available space; use terminal cell width and rune-safe truncation.
3. Give task/branch lists height-aware viewports, keyboard and mouse parity, stable
   selection during async result arrival, and modal-specific footer commands.
4. Key active plugin and file-browser view state by `WorkDir` where the state projects
   worktree contents. Keep genuinely project-wide preferences keyed by `ProjectRoot`.
   Add an explicit migration for existing root-keyed state.
5. Build the worktree switcher from one cached inventory snapshot and pass that
   snapshot into switching/reinit where valid. Refresh once after the switch rather
   than spawning duplicate synchronous queries.
6. Show worktree state/action availability in the list: main, branch, detached,
   locked, missing/prunable, operation in progress, setup warning, PR state, and diff
   error/truncation.

Primary files: `view_modals.go`, `create_modal.go`, `keys.go`, `mouse.go`,
`commands.go`, `internal/app/worktree_switcher_modal.go`, `model.go`, and
`internal/state`.

Acceptance proof:

- Headless 60x24, 80x42, 120x40, and 200x50 captures for create, task link, diff,
  merge preflight/error/recovery, PR edit/wait/closed, and cleanup.
- The task search border stays on one line at every supported width and all hit regions
  select the rendered row.
- Switch A (Git/file A) → B (Files/file B) → A restores A rather than project-wide B.
- Both Nerd Font and fallback rendering modes remain within plugin width/height.

## Test and proof infrastructure

Add a behavior-faithful lifecycle harness rather than more parser-only fixtures:

1. Create a temporary main repository, a bare `origin`, two linked worktrees, and
   configurable local/remote divergence.
2. Drive the same exported Git operation functions used by the plugin.
3. Assert refs, worktree HEAD/index/status, worktree inventory, metadata, remote refs,
   and recovery state after every step.
4. Cover injected command failures after fetch, pull, merge, push, PR create, worktree
   delete, branch delete, and remote delete.
5. Run concurrent cancellation/switch tests under `go test -race`.
6. Use `scripts/tmux-drive.sh paths` before every visual proof and retain its isolated
   tmux socket and Sidecar state requirements. Never touch the default tmux server.

Minimum verification per slice:

```bash
go test ./internal/projectdir ./internal/app ./internal/plugins/workspace
go test -race ./internal/projectdir ./internal/app ./internal/plugins/workspace
go test ./...
```

Add a focused integration test command once the lifecycle harness has its own package;
it should remain local-only and require no GitHub credentials. Live `gh` proof is a
separate opt-in check against a disposable test repository.

## Delivery and review gates

- Land the slices independently; do not combine metadata migration, merge execution,
  diff redesign, and modal conversion in one change.
- Preserve current user data and unrelated worktrees. Migration must be additive
  until the new mapping is verified.
- Every slice needs independent code review. Green tests and CI are not completion by
  themselves.
- Any operation that deletes a worktree/branch, pushes a base branch, executes a repo
  hook, copies secrets, or contacts an external model must be visible and intentional.
- Final proof must use the actual Sidecar consumer path, not only direct helper calls.

## Deliberate deferrals

- No Sidecar CLI/API is added; Git/`gh` remain the agent-facing surfaces.
- Configurable squash/rebase/ff merge strategies wait until direct merge has the safe
  preflight/operation/recovery foundation.
- No database is needed. Collision-safe, inspectable files under the existing project
  state directory remain appropriate.
- No filesystem watcher is added for Git state. Refresh and operation completion are
  sufficient until measured staleness demonstrates a need.

## Audit evidence

- Reviewed current implementation, related tests, historical worktree plans, and Git
  history through `main` at `43a13c5`.
- Reproduced base checkout refusal and checked-out-ref desynchronization in disposable
  Git repositories.
- Reproduced the task-link input border wrapping in the real app at 80x42 using an
  isolated `scripts/tmux-drive.sh` run; both tmux and Sidecar state were private and
  the default tmux server was untouched.
- Passed focused normal and race-enabled tests for workspace, app, and projectdir
  before writing this plan.
