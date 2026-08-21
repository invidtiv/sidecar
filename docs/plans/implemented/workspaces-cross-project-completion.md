# Workspaces: finishing the cross-project surface

**Status:** implemented **Branch:** `workspace-panel-redesign` **Written:** 2026-08-15, at `c313c741` **Supersedes:** [the original sidebar redesign](../deprecated/workspace-sidebar-redesign.md) **Companions:** [baseline](workspace-sidebar-redesign-baseline.md) · [extraction brief](workspace-create-extraction.md)

This was the working brief for completing the cross-project Workspaces surface. It is retained as the decision and implementation record.

## Completion

Implemented on 2026-08-15. Global Workspaces can now create shells and worktrees in a chosen project, follow the created row, delete shells, and route worktree merges through the existing project workflow. Project and global creation share the same worktree planner, setup, journal, environment, agent launch, and refusal seams; global actions do not instantiate or temporarily switch project plugins.

The implementation was independently reviewed through two fix cycles, merged with the latest `main`, and independently reviewed again at the integration boundary. Focused tests, the full Go build/vet/test gates, diff checks, and an isolated real-app render of both global create flows passed. Mouse-only section and header actions are covered by direct hit-region tests because the headless tmux driver cannot click.

The remainder of this document preserves the pre-implementation brief and its decisions for historical context.

## The goal, in the user's words

> My goal is to get to the point where in the global workspace you can create
> new shells and worktrees, you can delete them or close them as well, and it
> feels natural. Probably if they were sorted by project there would be a plus
> button next to the project headers that would start the dialogue with the
> correct project selected; otherwise it would ask them to choose a project to
> create the shell or workspace in, and sort it appropriately and select it once
> it was created.

Everything below serves that. The guiding principle for any judgement call, also the user's: **the design should be more intuitive and easier to use for the end user.**

## Where things stand

Verify with `git log --oneline 97aaead7..` before trusting this.

**Both sidebars now share:**

- one header grammar, `[⇅ Sort] [+]`, with a defined degradation ladder — both controls, then the sort keeps its glyph and loses its word, then create alone, then nothing. Dropped controls take their hit regions with them.
- one row grammar: kind glyph, age, and a single gutter width for both kinds. A row with nothing on line two is one row, not two with a blank.
- one View surface (`v`; global keeps `s` as an alias), one sort vocabulary in `workspacelist`, one lane-to-section mapping, one age formatter.
- persistence: project sort in `state.WorkspaceState.ListSort`, global in `state.State.WorkspaceListSort`, both stored as display labels that degrade safely.

**The project sidebar** sorts by Manual, Activity, Recent, or Name. Manual is the default and keeps the Shells/Worktrees tree; every computed sort flattens it, so a shell living in a worktree becomes a peer row carrying its worktree the way a global row carries its project. The main checkout is no longer offered as a row unless it is hosting shells.

**`internal/workspaceops` exists** and holds the path-containment and durable-write layer, the tmux session helpers, and `CreateShell(ShellSpec)`.

**Not built:** the scope selector, the row action menu, hover bands, project pins, filter tokens, saved views, and anything global-create.

## Decisions already made — do not relitigate

Each of these was argued through with the user. If you want to change one, say so explicitly rather than drifting.

| Decision | Why |
| --- | --- |
| A computed sort flattens the tree | "Most recent first" has no honest reading over a parent and its children, and Activity exists to surface a blocked shell buried under an idle worktree |
| Manual stays the default | It is the shape of the project rather than a judgement about it |
| `⇅` not `Sort:` | The pill shares its row with create; six columns of label is the difference between both controls fitting and neither |
| `v` opens View, `V` toggles kanban | The two surfaces should not spend their obvious "view" key differently; kanban is the rarer action |
| The main checkout is not a row | Nothing in the list creates, deletes, merges, or pushes it, and selecting it showed a static explainer. It keeps its row only when hosting shells, because hiding it would take live sessions off the surface |
| Global still lists main checkouts | The trade-off differs there: a project with no worktrees would vanish, and global is how you discover a project exists. `showIdleWorktrees` hides them by default anyway |
| Age counts seconds under a minute | It draws the eye to something that just moved, which is what the column is for |
| No temporary project switch for global create | The plan's original constraint, still right |

## The work

### Step 4 — global shell create *(next; no destructive risk)*

`workspaceops.CreateShell` is callable without a plugin, so this needs no further extraction. What is missing is a host for the view concerns:

- choosing the owning project, defaulting to the selected row's project, then the last global-create project;
- resolving *that project's* shell names — `generateShellSessionName` and `nextShellDisplayName` are project-scoped and currently plugin methods. A global caller needs the target project's existing session and display names; inventory has them.
- progress, and binding the created identity so the new row is selected when it appears;
- refreshing only the affected project.

**Open question that gates this:** `createShell` reads config through `p.ctx`. A global caller needs a project's config without instantiating that project's plugin. Confirm the app's project registry can hand it over before building.

**The `+` affordance** the user described lands here: with global sorted by Project, a `+` on each project section header opens the dialogue with that project preselected; under any other sort the header `+` opens the same dialogue with a project chooser as its first field. One flow with one optional pre-answer. `SidebarSection.Action` already exists and `RenderSidebar` already draws and hit-tests it — `Model.Render` simply never sets it.

Decide deliberately: after creating under a non-project sort, a brand-new shell has no activity and sorts into Idle or No Session, possibly below the fold. Selecting it and letting the viewport follow is the honest answer.

### Step 5 — extract the git layer, then the worktree planner *(own branch; independent review)*

This is the real refactor. The worktree planner, `addCreatedWorktree`, setup, hooks, and the pending-creation journal did not move because they rest on `gitOutputContext`, `mainWorktreePathContext`, `repoNameContext`, and `SlugifyWorktreeName` — and there is no `internal/git` to move them to.

Two hazards specific to this:

- `Worktree` carries `*Agent`, which owns tmux panes, output buffers, and activity trackers. Moving that type drags the plugin.
- `pendingCreationJournal` serialises a whole `Worktree` to disk. Changing the type changes an on-disk format that interrupted creations depend on for recovery. Treat it as a format with a migration, not a struct.

Characterize before extracting. The journeys, none of which have tests yet: create with no setup config; create with env-file copying and a passing hook; required hook fails (refusal plus the three recovery options); optional hook fails (warning, worktree usable); interrupted creation reconciled from the journal on next launch; create from a PR fetch; name collides with an existing worktree or branch; cancel mid-plan and cancel after mutation has begun.

### Step 6 — global worktree create

Needs step 5. Same project-choice flow as step 4, plus plan/confirm, setup progress, and the recovery modal.

### Step 7 — global delete and merge

Tracked as **td-70e32d**. `D` deletes a shell from the global list, registered in the global context and footer-visible only where it applies; merge uses the existing strategy menu and protected-branch refusals. Needs the same core.

### Deferred, in rough priority

- **The scope selector** (`[sidecar ▾]` / `[All projects ▾]`) — the last piece of one header grammar, and the durable scope cue once narrow rows drop the project prefix.
- **Section `+` duplication.** The header `+` and the Worktrees section `+` create the same thing and now look identical. Only one should survive.
- **Row action menu**, hover bands, project pins, filter tokens, saved views. All from the deprecated plan; none blocking.

## Hazards this branch already hit

Read this section. Each cost real time.

**`tmux-drive.sh` cannot click.** It drives with `send-keys`. Two mouse defects shipped past a "verified in the real app" claim because the check used the keyboard. Anything mouse-only is verified by tests alone — say so rather than implying otherwise.

**Region kinds are not region IDs.** `renderSidebarContent` translates `workspacelist` region kinds into plugin region IDs. A kind missing from that switch registers under the kind string, and the handler keyed on the plugin ID never fires: a control that is drawn, hit-tested, and dead.

**`internal/overview` preference accessors are overridable package vars for a reason.** They are stubbed in `TestMain` so tests do not write the developer's real state file. A new preference added without registering there both leaks between tests through package globals and writes `~/.config/sidecar/state.json` during `go test`.

**`main` and this branch both edit `internal/workspacelist/row.go`.** One merge auto-resolved textually into a build break. Rebase or merge early, and check the build rather than trusting a clean merge.

**"State-free" is not "dependency-free."** A function with no receiver can still be welded to its package by the helpers it calls. Count dependencies, not receivers.

**Filter state and structural state are different questions.** "Is this row ever offered?" is a property of the project; "is it showing now?" is a property of the query. Conflating them made the `N of M` denominator shrink as the user typed.

## Verification expected

- `go build ./...`, `go vet ./...`, `go test ./...`, `git diff --check`.
- Regenerate the golden after intended row or chrome changes: `go test ./internal/workspacelist -run TestSidebarBaselineFixture -update`.
- Isolated real-app proof: `./scripts/tmux-drive.sh paths` first, confirm nothing resolves under `~/.local/state/sidecar` or `~/.config/sidecar`, and always `stop` when done.
- Independent review before reporting done, especially for steps 5 and 6. The review of steps 1 to 3 found four defects that green tests did not.

## Related issues

- **td-43833d** (P0) — `sidecar open` only works from inside a Sidecar project shell, so most agents cannot put a file in front of the user. The transport is already context-free; only target resolution is tmux-bound.
- **td-b69e73** — session ages reset on every Sidecar restart. The fix is to read tmux's own `session_created` and `session_activity` rather than reconstruct them.
- **td-70e32d** — global lifecycle parity (step 7 above).
