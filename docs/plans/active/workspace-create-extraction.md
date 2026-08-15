# Extraction brief: the workspace creation core

**Surveyed:** 2026-08-15, on `workspace-panel-redesign` at `a1c2fc51`
**Serves:** [Workspace sidebar redesign](workspace-sidebar-redesign.md) slices 5 and 6,
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

## Recommended sequencing

The plan's slice 5 and slice 6 are still the right order, but the first is
smaller and the second can start sooner than written.

1. **Move the 26 package functions into `internal/workspaceops`.** No behaviour
   change, no signature change, no view involvement. Mechanical and reviewable
   on its own.
2. **Give the six methods explicit inputs.** `reconcilePendingCreation` and
   friends take what they read rather than reaching for it. Still project-only.
3. **Extract shell creation** as described above. This unblocks the steel
   thread without touching the worktree lifecycle at all.
4. **Global shell create.** The remaining work is a host for the view concerns
   in group three: project choice, progress, and binding the result. No
   worktree, Git, or PR logic involved.
5. **Global worktree create**, which needs the plan/confirm/setup/recovery
   journey and is where the risk actually is.

Steps 1–3 cannot destroy user work: they move code and add parameters. Step 5
is the one that warrants its own branch and an independent review.

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
