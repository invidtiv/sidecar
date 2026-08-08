# Plan: Git plugin performance and reliability cleanup (td-8acb2c)

**Task:** `td-8acb2c` — Review Git plugin performance and technical debt

**Research snapshot:** 2026-08-08

## Decision first

Make every Git operation leave the Bubble Tea event loop immediately, batch path
operations into one Git invocation, and make refresh a single-flight snapshot
pipeline. The first implementation slice should fix stage/unstage end to end;
the same operation/result boundary can then absorb the other synchronous writes
without a large plugin rewrite. Keep the first slice narrow: inject only the Git
write executor it needs, then consolidate read execution when the snapshot work
has demonstrated the shared policy.

Do not add a Sidecar Git CLI or API. Sidecar is a presentation-layer client of
Git, which already owns complete headless behavior. Keep path selection,
operation eligibility, cursor restoration, and result handling as state-free
functions where practical, but invoke Git directly through a narrow internal
runner. This preserves testability without pretending Sidecar owns a new Git
domain.

## User journey and acceptance evidence

From the Git plugin, Marcus can stage a file, an untracked folder, or all changes
and Sidecar remains responsive throughout:

1. The keypress returns immediately and the plugin displays a small in-progress
   state without clearing the existing file list or diff.
2. Navigation, repaint, resize, and switching plugins still work while Git runs.
3. One logical stage request uses one `git add` invocation, including a folder
   containing many displayed children.
4. Success produces one coherent status/history/preview update, restores a
   sensible selection by stable identity, and does not flash stale data.
5. Failure preserves the pre-operation model and reports Git's actionable output;
   a partial process-per-file result is impossible.
6. Repeated destructive/write keys are refused or coalesced while an incompatible
   operation is active.
7. The same behavior works in a normal checkout and a linked Git worktree.

Proof must include focused tests, a subprocess-counting shim, and the real TUI
driven by `scripts/tmux-drive.sh`. A passing unit suite alone is insufficient.

## Measured initiating defect

`updateStatus` currently calls `FileTree.StageFile`, `StageAll`, `UnstageFile`,
and `UnstageAll` synchronously. Folder staging loops through every child and runs
one `git add -- <path>` per file before `Update` returns. Bubble Tea therefore
cannot process input or render during the entire operation.

A synthetic repository with 1,000 untracked files measured on this Mac:

| Operation | Wall time |
| --- | ---: |
| 1,000 sequential `git add -- <file>` calls | 8.95 s |
| one `git add -- bulk` call | 0.05 s |
| one `git add -A` call | 0.05 s |
| `git status --porcelain=v2 -z --untracked-files=all` | 0.01 s |
| unstaged `git diff --numstat` | 0.01 s |
| staged `git diff --numstat --cached` | 0.03 s |

The roughly 180x folder-staging gap explains why direct Git feels fast while
Sidecar freezes. Endpoint-security process interception will amplify the fixed
cost of every spawned process.

## Current findings

### P0: event-loop blocking and unsafe refresh ownership

- Folder, file, all-stage, and all-unstage execute synchronously in
  `updateStatus`. `getLastCommitMessage` also runs synchronously when opening the
  amend editor. These violate the plugin's otherwise asynchronous operation
  pattern.
- `refresh()` runs in a `tea.Cmd`, but calls `p.tree.Refresh()` from that worker.
  `Refresh` assigns slices on the model-owned tree while `Update`, `View`, and
  another refresh may read or mutate it. Bubble Tea messages should carry a
  completed snapshot and let `Update` install it.
- `RefreshDoneMsg` has no epoch/request identity. Unlike diff/history messages,
  it cannot reject a stale result after project reinitialization or an older
  refresh arriving after a newer one.
- There is no Git-operation state for stage/unstage/discard/stash/branch writes.
  Moving writes async without adding compatibility/refusal rules would allow
  index-lock conflicts and out-of-order results.

### P1: duplicated work and scaling costs

- A successful write explicitly launches status refresh plus recent-history/push
  status. The index watcher can launch the same pair about 100 ms later; focus
  and manual refresh add more independent requests. There is debounce but no
  single-flight/coalescing or dirty-follow-up rule.
- Every status snapshot launches status, staged numstat, unstaged numstat, and
  potentially batched `wc` subprocesses. Diff-stat association linearly scans
  the relevant entry slice for every numstat row, making it quadratic for large
  change sets. The basename fallback can attach stats to the wrong file when
  different directories contain the same basename.
- Untracked line counts call external `wc` over every untracked file, including
  content that may be large or non-text. This is presentation data on the
  critical refresh path and needs a measured budget, graceful deferral, or
  removal.
- `AllEntries()` allocates and rebuilds the display slice repeatedly across
  cursor helpers, message handlers, and renders. This is secondary to subprocess
  costs but easy to measure once realistic benchmarks exist.
- Full-file preview performs three sequential Git/file reads, and rapid cursor
  movement has stale-project protection but no request identity for two requests
  for different versions of the same selected path.

### P1: correctness and lifecycle debt

- The watcher assumes `<repoRoot>/.git` is a directory. In a linked worktree it
  is a file, while the real index, HEAD, and refs live under paths reported by
  `git rev-parse --git-path`; direct watches can fail or miss updates.
- Folder staging is non-atomic at the Sidecar request level: if child N fails,
  earlier children remain staged even though the UI reports one failed action.
  A single pathspec removes that Sidecar-created partial-failure mode.
- After staging, cursor movement is computed from old counts
  (`stagedCount + 1`) rather than preserving a stable file/commit identity. A
  changed grouping or folder collapse can select an unrelated item.
- Status and history parsers are not consistently `-z`/NUL-delimited. Tabs,
  newlines, quoting, and rename forms can be misparsed. Numstat rename parsing
  and basename matching are especially fragile.
- Most command helpers have no cancellation or timeout policy. A credential
  prompt, hook, pager/config surprise, or stuck remote command can occupy a
  worker indefinitely. Interactive remote operations need deliberate progress
  and cancellation behavior, not a global short timeout.

### P2: maintainability and startup debt

- `Start()` synchronously reads and may write `.gitignore` before returning its
  `tea.Cmd`, contrary to Sidecar's documented first-frame rule. Move the
  best-effort check off the startup path and surface a non-blocking warning.
- Git process creation, output/error normalization, environment policy, and
  read/write classification are scattered. Introduce one small concrete runner
  and command description; do not build a generalized framework or public API.
- `GetStashList` spawns `echo` once per parsed stash solely as a dummy import-use
  workaround. Remove it and parse the numeric index normally.
- Errors are often discarded for stats, full-file reads, watcher setup, and
  history detail. Optional data may degrade gracefully, but diagnostics should
  record which component failed so performance and correctness problems are
  observable.
- Existing tests cover parsers, diffs, history, rendering, and individual
  features, but not operation responsiveness, one-process batching, refresh
  ordering, stale results, watcher coalescing, linked worktrees, unusual paths,
  or large-change-set performance.

## Proposed design

### 1. Async, batched operation lifecycle

Represent writes with a small plugin-local operation model:

```go
type operationKind string

type operationRequest struct {
    ID       uint64
    Epoch    uint64 // implements the existing plugin.EpochMessage contract
    Kind     operationKind
    Paths    []string
}

type operationResultMsg struct {
    ID    uint64
    Epoch uint64 // implements the existing plugin.EpochMessage contract
    Kind  operationKind
    Err   error
}
```

The key handler resolves the selected entry into pathspecs, records the active
operation, and returns one command. For a collapsed untracked folder, prefer its
folder path (`git add -- folder/`) when it exactly represents that subtree;
otherwise pass all selected child paths in one invocation with `--`. `stage all`
remains `git add -A`. Apply the snapshot only after the result message.

Allow navigation and plugin switching during writes. Reject conflicting writes
with a short toast while one is active; do not queue arbitrary mutations. Show
`Staging…`, `Unstaging…`, or the relevant verb in the Git sidebar/status area and
disable only incompatible commands. Capture stable selection identity before the
write (`path + staged/unstaged section`, or commit hash) and resolve the nearest
valid selection against the new snapshot.

### 2. Snapshot refresh coordinator

Change `FileTree.Refresh` into a loader that builds and returns a new immutable
snapshot without touching the live model. `statusLoadedMsg` carries epoch and a
monotonic request ID. Only `Update` swaps `p.tree`.

Keep at most one status load in flight. If watcher/focus/manual/operation events
arrive during it, set one `refreshPending` bit and run exactly one follow-up after
completion. A Git write result should request a high-priority refresh; the
watcher event it causes should be absorbed. History/push status should refresh
only when HEAD/upstream may have changed, not after ordinary index-only staging.
File staging does not change recent commits.

Preserve the current snapshot and preview while loading. Once the new snapshot
is installed, request at most one preview for the resolved selection. Give
preview requests both epoch and request ID so a late result for the same path
cannot overwrite newer content.

### 3. One narrow Git runner

Centralize command construction in a concrete internal runner that supports:

- repository working directory;
- read-only `--no-optional-locks` policy;
- NUL-safe output where Git supports it;
- captured stderr and consistent typed errors;
- injectable execution for tests and subprocess counting;
- context cancellation for reads and operations known to be safely interruptible;
- explicit interactive/remote policy so Git never unexpectedly reads the TUI's
  terminal.

Do not automatically kill an in-flight index or worktree mutation on plugin
switch, project switch, or shutdown. Its result message may become stale and be
ignored by the new model, but cancellation is allowed only after the specific
Git operation is shown to have safe interruption semantics. This avoids trading
a stale UI result for a partially applied repository mutation.

Keep parsing and domain-shaped functions separate from execution. The runner is
an adapter at the external-process seam, not a new service locator or interface
on every helper.

### 4. Cheap, correct status snapshots

Parse porcelain v2 and numstat with NUL delimiters and exact normalized paths.
Build maps from path to entry once, reducing stat association from quadratic to
linear and deleting the ambiguous basename fallback. Add fixtures for spaces,
tabs, newlines, Unicode, renames/copies, binary files, staged+unstaged files,
conflicts, and duplicate basenames.

Measure untracked line counts independently. Recommended first move: omit counts
for untracked files from the initial snapshot and load visible/selected counts
later only if the UI value proves useful. Status and staging responsiveness are
more valuable than eagerly counting every line in files Git does not track.

Cache the flattened display entries once per installed snapshot/expand-collapse
change. Benchmark before changing diff renderer internals; current evidence does
not justify a rendering rewrite.

### 5. Watch the real Git paths

Resolve the common Git directory and relevant files with `git rev-parse
--path-format=absolute --git-path ...` (with a compatibility fallback if the
installed Git requires it). Watch the actual index, HEAD, packed refs, and refs
locations for both main checkouts and linked worktrees. Treat fsnotify as an
invalidation hint: coalesced refresh establishes truth.

Watcher setup failure should degrade to focus/manual refresh and a diagnostic,
not make the plugin unusable. Tests should create a real linked worktree and
prove an external stage/commit becomes visible.

## Implementation sequence

### Slice 1 — staging steel thread (P0)

- Add operation request/result state and the smallest test-injectable Git write
  executor seam; do not migrate unrelated read helpers yet.
- Move file, folder, stage-all, and unstage operations into `tea.Cmd`.
- Batch every logical selection into one Git invocation.
- Add busy feedback, conflicting-write refusal, error output, and stable
  selection restoration.
- Prove 1,000-file folder staging remains interactive and spawns one `git add`.
- Prove the same stage/unstage behavior in a linked worktree. Correct watcher
  invalidation for *external* linked-worktree changes remains Slice 4.

This slice directly fixes the reported defect and establishes the pattern for
later writes.

### Slice 2 — safe, coalesced snapshots (P0/P1)

- Return immutable status snapshots in epoch/request-tagged messages.
- Add single-flight plus one dirty follow-up; absorb write-triggered watcher
  duplicates.
- Separate index refresh from HEAD/history refresh and deduplicate preview loads.
- Add deterministic out-of-order and project-switch tests; run the race detector
  on the package.

### Slice 3 — snapshot scale and parser correctness (P1)

- Replace quadratic stat lookup with exact maps and NUL-safe parsing.
- Remove eager untracked `wc` from the critical path or defer it behind a
  measured visible-item loader.
- Cache flattened entries and add large-repository benchmarks with committed,
  modified, staged, untracked, binary, and unusual-path fixtures.
- Record benchmark baselines and regression thresholds in tests or a documented
  benchmark command; avoid brittle absolute timing assertions in unit tests.

### Slice 4 — watcher and remaining operation lifecycle (P1)

- Resolve actual Git paths and cover linked worktrees.
- Move amend-message loading and any remaining local writes onto the operation
  boundary. Audit discard, stash, branch, commit, fetch, pull, and push for busy
  state, cancellation, stale result, credential/prompt, and error consistency.
- Refresh only the data each operation can invalidate.

### Slice 5 — startup and maintainability cleanup (P2)

- Move `.gitignore` maintenance out of synchronous `Start()` and reconsider
  whether automatic mutation belongs at every launch.
- Remove the stash `echo` subprocess and other dead/duplicated helpers.
- Consolidate diagnostics and document the Git plugin performance harness.
- Profile the real renderer and preview modes; optimize only findings that exceed
  the agreed frame budget.

Each slice should be its own reviewable td task/PR and independently reviewed
before the next slice relies on it. Slice 1 and Slice 2 should not be combined:
the user-visible fix can land quickly, while snapshot ownership receives focused
race/order review.

## Verification gates

### Automated

- `go test ./internal/plugins/gitstatus`
- `go test -race ./internal/plugins/gitstatus`
- `go test ./...`
- Runner tests assert exact argv, one invocation per logical operation, `--`
  path separation, error propagation, and no command execution in `Update`.
- State-machine tests drive key message -> immediate command/busy model -> result
  -> coalesced snapshot, including failure, repeated keys, stale epoch, reversed
  result order, and focus/project changes.
- Real-Git fixtures cover linked worktrees, unusual filenames, duplicate
  basenames, renames, binary/untracked files, conflicts, hooks that fail, and a
  1,000-file directory.
- Benchmarks separately report operation process count/time, snapshot load,
  parser allocation, `AllEntries`, and representative `View` rendering.

### Real app

1. Build and install a versioned Sidecar binary.
2. Use a copied/synthetic repository, never the developer's live index, with
   1,000+ untracked files plus staged and modified files.
3. Put logging shims for `git` (and `wc` until removed) ahead of the real tools on
   `PATH`.
4. Start with `scripts/tmux-drive.sh`, stage the collapsed folder, send navigation
   and plugin-switch keys while staging, and capture before/during/after text and
   PNG snapshots.
5. Assert one write invocation, bounded/coalesced reads, visible progress, correct
   final status/diff/selection, and no lost input or header/footer movement.
6. Repeat stage, unstage, failure, and external-change flows in a linked worktree.
7. Run `SIDECAR_STARTUP_TRACE=stderr` and confirm the Git plugin performs no file
   reads/writes or process spawns synchronously in `Init`/`Start` before the first
   ready frame.

## Success budgets

Use behavior and relative measurements rather than machine-specific promises:

- `Update` for a Git keypress performs no filesystem or subprocess work and
  returns within one frame budget under the 1,000-file fixture.
- One logical stage/unstage action causes exactly one Git write process.
- Input and repaint remain observable during an intentionally delayed runner.
- A write plus its fsnotify event produces no more than one in-flight status load
  and one coalesced follow-up; index-only staging does not reload commit history.
- Snapshot build is approximately linear in changed-file count and carries no
  data race under the race detector.
- No stale operation, status, history, or preview message can alter a reinitialized
  project or overwrite a newer request.

## Deliberate deferrals

- No libgit2/go-git migration. The Git CLI is the correct boring adapter and is
  demonstrably fast when called once.
- No persistent status database or cache. Git remains the source of truth.
- No Sidecar Git CLI/API/MCP surface; Sidecar does not own Git's capability.
- No speculative renderer rewrite, background worker pool, or generalized job
  framework. Add concurrency only at measured seams and keep writes serialized.
- No automatic cancellation of a write merely because the user switches plugins,
  projects, or exits; project reinitialization/exit cancellation semantics must
  be operation-specific, explicit, and proven safe.

## Review checklist

- Independent reviewer traces the real keypress-to-index-to-render journey, not
  only unit tests.
- Verify every write path is absent from synchronous `Update`, `Init`, and
  pre-command `Start` code.
- Verify batching is path-safe and cannot interpret a filename as an option.
- Verify model state changes only on the Bubble Tea event loop.
- Verify operation compatibility rules prevent concurrent index writes.
- Verify refresh invalidation is minimal but never leaves stale visible state.
- Verify linked-worktree and unusual-filename fixtures exercise real Git.
- Compare subprocess logs and tmux captures against the stated budgets before
  approval.
