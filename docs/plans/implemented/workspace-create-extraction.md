> **Implemented for the part it drove.** Steps 1 to 3 are done: the containment
> and durable-write layer, the tmux session helpers, and shell creation now live
> in `internal/workspaceops`. Steps 4 to 6 — global shell create, extracting the
> git layer, and global worktree create — carried forward into
> [Workspaces: finishing the cross-project surface](../active/workspaces-cross-project-completion.md).
>
> Read the "Correction" section before trusting any survey like this one: the
> first version of this document counted receivers and concluded the move was
> mechanical, and it was not.

# Extraction brief: the workspace creation core

**Surveyed:** 2026-08-15, on `workspace-panel-redesign` at `a1c2fc51`
**Serves:** [Workspace sidebar redesign](../deprecated/workspace-sidebar-redesign.md) slices 5 and 6,
and [td-70e32d](https://example.invalid/td-70e32d) (global lifecycle parity)

This is the characterization the redesign plan asks for before extraction. It
exists because the plan's slice 5 assumes a large refactor — "extract the
state-free planner/operation seam" — and the code does not agree. Most of the
seam is already there.

## Headline: the core is mostly already state-free

`internal/plugins/workspace/create_operation.go` is 771 lines and 32 functions.
**26 of them are already package functions with no receiver**, taking explicit
arguments and returning explicit results:

- `resolveCreateOperation(ctx, workDir, projectRoot, name, base, dirPrefix, setup)`
  — the non-mutating preflight that produces a plan;
- `addCreatedWorktree(ctx, repoKey, plan)` and its injectable-runner twin;
- `runCreateSetup(ctx, plan, wt)` and `runSetupHookContext(ctx, plan)`;
- the whole pending-creation journal (`persistPendingCreation`,
  `loadPendingCreation`, `removePendingCreationWithOps`);
- every containment helper (`openContainedRegularFile`, `walkPinnedDirectory`,
  `ensureRealDirectoryPath`, …), which is where the security-sensitive path
  handling lives.

Only six are methods on `*Plugin`, and they are the view-side ones:

| Method | What it actually does |
| --- | --- |
| `reconcilePendingCreation` | reads `p.worktrees`, `p.repoSnapshot`, `p.ctx` |
| `clearPendingCreation` | thin wrapper over the package function |
| `surfaceInterruptedCreation` | sets `p.createSetupResult`, `p.viewMode` |
| `selectCreatedWorktree` | `p.selectWorktreeAt`, `p.resetPreviewScroll`, `p.ensureVisible` |
| `finishCreatedWorktree` | selection + `p.loadSelectedContent` + `p.StartAgentWithOptions` |
| `clearCreateModal` | modal teardown |

So the split is not "extract a core from a tangle". It is **move the 26
functions into their own package, and give the 6 methods a caller-supplied
interface for the parts that touch a view.**

## What the plugin actually supplies

Grepping `p.` across `create_operation.go` gives ~25 distinct symbols. They sort
cleanly into three groups, which is the encouraging part:

**Inputs the core needs** (must become explicit parameters):
`p.ctx` (WorkDir, ProjectRoot, Config), `p.repoSnapshot` (repo key),
`p.worktrees` (collision and reconcile checks), `p.findWorktree`.

**Effects the core performs** (already behind narrow functions):
`Sync`, `Write`, `Chmod`, `Close`, `CopyEnvFiles`, `RunHook`, `HookPath`,
`HookRequired`, `EnvFiles`, `AttachToWorktreeDir` — these are config values and
injectable operations, not plugin state.

**View concerns that must stay behind** (the actual seam):
`p.viewMode`, `p.createBusyStep`, `p.createPlan`, `p.createSetupResult`,
`p.createDeleteResult`, `p.createOperationModal`, `p.createOperationWidth`,
`p.ensureVisible`, `p.resetPreviewScroll`, `p.loadSelectedContent`,
`p.selectWorktreeAt`, `p.saveSelectionState`, `p.deferredCreations`,
`p.newLifecycleScope`, `p.operationCtx`.

The third group is what a global host would have to provide its own version of.
It is progress reporting, selection, and modal state — not business rules.

## The shell path is smaller still

`createShell` (shell.go:512) already returns a closure whose only captured state
is four resolved values: `workDir`, a generated session name, a display name,
and the preview dimensions. The tmux call inside it touches no plugin state at
all. Its plugin dependencies are:

- `p.generateShellSessionName()` — needs the project's existing session names;
- `p.nextShellDisplayName()` — needs the existing display names;
- `p.ctx.WorkDir`;
- `p.calculatePreviewDimensions()` — pure geometry from `p.width/p.height`.

**This is the steel thread and it is genuinely small.** A function taking
(workDir, sessionName, displayName, agentType, skipPerms, cols, rows) and
returning the same `ShellCreatedMsg` is a near-mechanical extraction. The
naming helpers need the target project's shell list, which a global caller can
supply from inventory.

## Correction: "state-free" is not the same as "dependency-free"

The survey above counted receivers and concluded the move was mechanical. It
was not, and attempting step 1 as written is what showed why.

The 26 functions take explicit arguments, but most of them rest on a bed of
package-local helpers that would have to move with them:

- `resolveCreateOperation` calls `gitOutputContext`, `mainWorktreePathContext`,
  `repoNameContext`, `SlugifyWorktreeName`, and `setupScriptName`;
- `addCreatedWorktree` and `runCreateSetup` reach the same git layer;
- `createShell` called `newShellSession`, `sessionExists`, and `getPaneID`.

There is no `internal/tmux` or `internal/git` package. Every one of those
helpers lives in `internal/plugins/workspace`. So the boundary is not where the
receivers are — it is wherever the git and tmux layers stop.

Two type dependencies compound it:

- `Worktree` carries `*Agent`, which owns tmux panes, output buffers, and
  activity trackers. Moving it would drag the plugin.
- `pendingCreationJournal` serialises a whole `Worktree` to disk. Changing the
  type changes an on-disk format that in-flight creations depend on for
  recovery, so it cannot move casually.

What actually moved, and why it was safe:

| Moved | Why it was clean |
| --- | --- |
| Path containment and durable writes (11 functions) | stdlib and `unix` only, no plugin types |
| tmux session create/exists/pane-id | already sat on `internal/tty` and `internal/shellstate` |
| Shell creation (`CreateShell`, `ShellSpec`) | its four inputs were already resolved values |

What did not move, and now belongs to the risky step rather than the safe one:
the worktree planner, `addCreatedWorktree`, setup, hooks, and the pending
journal. They need the git layer extracted first, which is its own decision.

The step-2 finding is also worth recording: the six `*Plugin` methods were
examined and left alone. Every one of them is genuinely a view method —
selection, modal state, progress reporting. There were no business rules hiding
in them to parameterise. That is good news about where the seam already sits.

## Recommended sequencing

The plan's slice 5 and slice 6 are still the right order, but the first is
smaller and the second can start sooner than written.

Steps 1–3 are **done**, in the corrected form above:

1. ~~Move the 26 package functions~~ → moved the containment and durable-write
   layer, which was the part with no plugin dependencies.
2. ~~Give the six methods explicit inputs~~ → examined and correctly left alone;
   they are view methods with no business rules in them.
3. **Extract shell creation** → done. `workspaceops.CreateShell(ShellSpec)`, with
   the plugin resolving the project-scoped names and calling it.

Remaining:

4. **Global shell create.** The operation is now callable from anywhere. What is
   left is a host for the view concerns: choosing the project, resolving that
   project's shell names, progress, and binding the created identity. No
   worktree, Git, or PR logic involved.
5. **Extract the git layer**, then the worktree planner and setup on top of it.
   This is the real refactor and the thing steps 1–3 were mistaken for.
6. **Global worktree create**, which needs step 5 plus the
   plan/confirm/setup/recovery journey.

Steps 5 and 6 are the ones that warrant their own branch and an independent
review. Steps 1–4 cannot destroy user work.

## Journeys to characterize before step 5

Not yet written, and required by the plan's ship gate. Each needs a test that
passes before extraction and after:

- create a worktree with no setup config;
- create with env-file copying and a setup hook that succeeds;
- setup hook fails and is required — refusal, and the recovery modal's three
  options (retry, open anyway, delete created worktree);
- setup hook fails and is optional — warning, worktree usable;
- interrupted creation reconciled from the pending journal on next launch;
- create from a PR fetch;
- name collides with an existing worktree or branch;
- cancel mid-plan, and cancel after mutation has begun.

## Open question the survey raised

`createShell` and `resolveCreateOperation` both read config through `p.ctx`.
A global caller has a project's config only if the app's project registry can
hand it over without instantiating that project's plugin. Worth confirming that
is reachable before step 4, since it is the one dependency in group one that is
not obviously available from inventory.
