> **Deprecated — superseded by
> [Workspaces: finishing the cross-project surface](../active/workspaces-cross-project-completion.md).**
>
> Parts of this shipped: one header grammar, one row grammar, the shared View
> surface, project sorting, and per-scope persistence. Parts were deliberately
> decided against, and one part was wrong about the code. Its slice 5 assumed a
> large state-free operation core had to be extracted from the workspace plugin;
> the survey in
> [the extraction brief](../implemented/workspace-create-extraction.md) found the
> boundary is not where the receivers are but wherever the git layer stops, and
> the sequencing changed accordingly.
>
> Kept for the product reasoning in "Decision first" and "Product principles",
> which the successor still follows. Do not use its slice list.

# Plan: Workspace sidebar as one cross-project control surface

**Research snapshot:** 2026-08-14
**Status:** proposed
**Scope:** the project Workspaces sidebar and global Workspaces sidebar, including
their shared row model, creation entry points, view controls, mouse affordances,
and scope transitions. Preview-pane/windowing changes are out of scope except
where sidebar actions or focus must preserve them.

**Builds on:**

- [First-class global Overview and cross-project Workspaces](../implemented/global-overview-workspaces.md)
- [Workspace sidebar convergence](../implemented/workspace-sidebar-convergence.md)
- [Drag-to-reorder shells and worktrees](workspace-sidebar-drag-reorder.md)
- [Modal redesign](modal-redesign.md)

## Decision first

Treat project and global Workspaces as two scopes of one workspace navigator,
not as an operational plugin and a read-only imitation of it.

Both sidebars will use the same four-part grammar:

1. a **scope selector** (`All projects` or the current project);
2. one **Create** action in the same position;
3. one **View** action for grouping, sorting, visibility, and saved views; and
4. one shared **workspace row** whose fields retain the same meaning and order.

Global Workspaces becomes capable of creating shells, worktrees, and fetched-PR
worktrees. The create flow asks which project owns the new item, runs the same
validation and lifecycle core as project Workspaces, stays in global scope, and
selects the new row when inventory observes it. Project Workspaces supplies the
current project implicitly. There must not be a hidden project plugin, a
temporary project switch, or a second implementation of shell/worktree
creation.

The default information architecture is:

```text
PROJECT                                      GLOBAL

Workspaces  [ sidecar ▾ ] [ + ] [ ≡ ]        Workspaces  [ All projects ▾ ] [ + ] [ ≡ ]
[/ filter…                     ]             [/ filter…                     ]

SHELLS (2)                                  NEEDS ATTENTION (2)
 ● ❯ fix terminal resize          2m          ◆ ⑂ sidecar / release fix      2m
     codex  working                             claude  blocked · td-a12bc3 · +41 -3

WORKSPACES (4)                             WORKING (5)
 ○ ⑂ global workspace shortcut     3m          ● ❯ braid / model benchmark   4m
     grok  idle · feature/global                    grok  working
```

The symbols are illustrative; the contract is the hierarchy and field order.
The selected theme remains authoritative for colours and exact glyph fallback.

This supersedes the earlier deliberate decision that global Workspaces is
read-only. That boundary was useful while the global browser was young, but it
now produces an obvious product gap. Sidecar still does not owe a new CLI or API
for these actions: Git and tmux own the underlying capabilities. It does owe one
shared, state-free set of validation/refusal rules so its two presentation
surfaces cannot disagree.

## Product principles for this redesign

### One object, one visual grammar

A shell is rendered as a shell everywhere; a worktree is rendered as a
worktree everywhere. Scope may add context (the project name globally) but may
not reorder the meaning of fields, substitute a different status vocabulary,
or change the selection/hover language.

### Scope is always visible and always one action away

The top app header continues to distinguish Overview from a project. The
sidebar also names its data scope because that is where the user is deciding
what to create, filter, or open. A user should never have to infer scope from
which sections happen to be present.

### Global is an operating surface, not a dashboard dead end

The user can inspect and type into live sessions globally today. Creation is
the remaining daily-use discontinuity. Global mutations are allowed when the
owning project is explicit and the exact project lifecycle rules are reused.
Destructive and Git lifecycle actions remain context-sensitive and keep their
existing refusal/confirmation behavior.

### Progressive mouse enhancement

Keyboard behavior is complete without mouse motion events. Terminals that
report motion gain hover bands, hoverable controls, tooltips/status copy, and a
row overflow target. Terminals that report clicks but not motion retain the
same buttons and click targets with no invisible-only action.

### Calm rows, rich detail on demand

The sidebar optimizes for finding the right workspace, not displaying every
known fact. The row carries identity, attention, and the next most useful
operational facts. The preview header, row action menu, and detail panes carry
the rest.

## Current-state findings

The implementation is closer to convergence than the screenshots suggest:

- `internal/workspacelist.RenderSidebar` already owns shared section layout,
  viewport, scrollbar, selection geometry, and typed mouse regions.
- `internal/workspacelist.RenderRow` already provides a shared status marker,
  kind glyph, provider treatment, two-line layout, and narrow fallback.
- `internal/termpreview.SplitFor` already keeps sidebar/preview geometry aligned.
- Both surfaces share filtering primitives, wheel-follow-selection semantics,
  sidebar width, and a live `tty.Model`-based preview.

The remaining drift is primarily product structure and caller policy:

| Concern | Project Workspaces | Global Workspaces | Cost today |
| --- | --- | --- | --- |
| Create | `New`, section `+`, `n`, `ctrl+n`, `F` | none | User must leave the place where they noticed the need |
| Header | `New`; section-specific `+` buttons | sort label only | Controls move and mean different things |
| Organization | fixed Shells / Workspaces structure | Activity / Project / Recent / Name sort-owned sections | Grouping and sorting are conflated and cannot be compared |
| Filter | shared `/` interaction | shared `/` interaction | Matching is shared, but view/filter discovery differs |
| Pinning | absent | persistent pins | A useful list primitive exists in only one scope |
| Reordering | separately planned for project | absent | Manual order needs an explicit relationship to sort/group |
| Row context | project implicit | project prefix in row | Correct difference, but secondary-field priorities still vary |
| Row actions | footer shortcuts and header buttons | footer shortcuts, double-click, a few direct keys | Mouse discovery is weak and action availability is hard to scan |
| Scope switch | `K`, brand, `@` switcher | `K`, brand, `@`, double-click/open | Powerful but distributed; the sidebar itself gives no direct cue |
| View preferences | mostly config/manual order | in-memory sort plus persisted idle toggle/pins | Persistence rules are inconsistent |

There is also an architectural seam to repair. Creation, validation, refusal,
and completion behavior currently lives behind the stateful workspace plugin.
Global cannot safely reuse it without either switching scope or extracting an
operation core. Instantiating one plugin per project would duplicate watchers,
polling, terminal ownership, and state; it is explicitly rejected.

## Target journeys

### 1. Scan the same way in either scope

1. Open project or global Workspaces.
2. Read the status marker, kind, name, age, and detail in the same positions.
3. Move with keyboard, wheel, or click; selection and preview follow identically.
4. Hover (when available) uses a quiet band distinct from focused selection.
5. Open the row action menu with the same visible overflow target or keyboard
   command; the menu contains only valid actions for that row and scope.

### 2. Create from project Workspaces

1. Press `n` or activate the header `+`.
2. Choose **Shell**, **Workspace**, or **From PR**. The menu remembers the last
   type, but never auto-executes a destructive or networked operation.
3. The current project is displayed as fixed context, not as a redundant form
   field.
4. Complete the existing type-specific flow.
5. The new item is selected in the current list and its preview becomes ready
   through the normal inventory/session path.

`ctrl+n` may remain a direct New Shell accelerator and `F` a direct From PR
accelerator. The header and `n` use one menu rather than three differently
placed buttons. Remove the section `+` controls after the shared Create menu is
proven; they duplicate the same decision and make headings visually noisy.

### 3. Create from global Workspaces

1. Press `n` or activate the same header `+`.
2. Choose the type and owning project. The project defaults to the selected
   row's project, then the last global-create project, then the current project.
   The chosen project remains plainly visible through confirmation.
3. Run the same validation, refusal, setup, and recovery flow project
   Workspaces would use. A create against a missing/unavailable project refuses
   before mutation and offers to reveal or edit the project configuration.
4. Stay in global scope while the catalog refreshes only the affected project.
5. Bind the eventual result by stable identity, select it, and show its preview.
   If collection times out, preserve a success notice with **Open project** and
   **Refresh** actions rather than pretending creation failed.

No default tmux server restart is ever part of this flow. Shell creation uses
the normal Sidecar-managed session path; proof uses only an isolated server.

### 4. Change what the list shows

The header `≡` opens one declarative **View** surface in both scopes. It
separates concepts that are currently bundled into sort modes:

```text
View

SHOW       Active · All · Attention
TYPE       All · Shells · Worktrees
GROUP BY   Activity · Project · Kind · None
SORT BY    Recent · Name · Manual
PROJECT    All projects · selected…      (global only)

            Reset view
```

Recommended defaults:

- **Global:** Show Active, all types, group by Activity, sort Recent.
- **Project:** Show All, all types, group by Kind, sort Manual.

Rules:

- Group and sort are independent. `Project + Recent` and `Activity + Name` are
  valid combinations rather than hidden special cases.
- **Manual** is offered only where a durable order exists. Initially this is
  project Workspaces after the drag-reorder plan lands. Global pinned items may
  later gain manual pin order, but global unpinned inventory does not pretend
  to own Git/tmux order.
- Pins are a top section independent of the chosen grouping. The selected pin
  retains its normal status/kind grammar and is not duplicated below.
- Project pinning is added using the same identity/state primitive as global
  pinning. Pins are a view preference, not a change to shell manifests or Git.
- View preferences persist separately for global scope and each project. A
  project can opt back into defaults with Reset view.
- The compact header shows the active view name (`Active`, `Attention`, or a
  saved name) only when it fits. The `≡` target is always present.

### 5. Filter quickly or precisely

`/` remains the fast path. Plain text keeps the current case-insensitive match
across name, project, branch, task, provider, and semantic status.

Add optional discoverable field tokens rather than a separate advanced UI:

```text
project:sidecar type:shell status:blocked agent:claude has:pr pinned:true
```

- Unknown tokens remain plain text; typing a colon must never make a result
  mysteriously unreachable.
- Completion suggestions appear only after a recognized prefix plus `:` and
  never take focus from the result list.
- `tab` accepts a visible completion; arrows continue to navigate results.
- The filter row shows `N of M` and, when space permits, removable applied
  token chips. At narrow widths it remains one text line.
- The View surface writes the same structured filter state used by tokens. A
  mouse user and a keyboard user therefore operate one model.
- Escape behavior remains: clear query/facets first, release filter focus
  second, leave scope only when the visible filter is already clear.

Saved views are a small second slice after the base facets work. A saved view
is a name plus show/type/group/sort/filter settings in Sidecar state, not a new
workspace data store. Ship built-ins (`Active`, `Attention`, `All`) before
user-named views; add user naming only if the built-ins prove insufficient.

### 6. Move between All projects and one project

The scope selector sits in the same header position in both sidebars:

- In global it reads **All projects**. Activating it opens a compact project
  picker; choosing a project leaves global scope, focuses project Workspaces,
  and preserves the selected global identity when it belongs to that project.
- In project it reads the project name. Activating it offers **All projects**
  first, followed by configured projects. Choosing All enters global
  Workspaces directly (not merely the last global tab).
- Clicking the project segment of a global row is a direct version of the same
  transition. It opens project Workspaces with the row selected. Clicking the
  rest of the row only selects it; double-click may retain the direct-open
  behavior for muscle memory.
- `K` and the Sidecar brand remain the quick two-state toggle and continue to
  remember the prior global tab. The scope selector is the explicit
  workspace-to-workspace route; `@` remains the app-wide destination switcher.
- A return to global restores its filter, view, selection, scroll position, and
  preview tab. A return to project restores that project's corresponding state.

This deliberately provides three routes with distinct jobs: brand/`K` toggles
spaces, `@` changes app destination, and the sidebar scope selector changes the
workspace list's scope. Help text should explain that distinction once rather
than hiding one route.

## Workspace row specification

### Semantic order

Every row uses this order, regardless of scope:

```text
line 1:  [status] [kind] [project /] name [identity flags]          [age]
line 2:                  [provider] [activity] · [branch/task] · [git facts]
```

1. **Status marker** answers “does this need attention?” It is the existing
   semantic marker vocabulary; health overrides activity.
2. **Kind glyph** answers “shell or worktree?” (`❯` and `⑂`, with existing
   non-Nerd-Font fallback). It never substitutes for status.
3. **Project** appears globally in muted text followed by a subdued separator.
   It is omitted project-side because the scope selector already states it.
4. **Name** is the strongest text and the last identity field to disappear.
5. **Identity flags** are rare facts such as PR, conflict, locked, missing, or
   pinned. Use glyph + accessible label in the action/detail surface; never
   encode a destructive/refusal state by colour alone.
6. **Age** is the age of the latest meaningful state change, right-aligned. It
   does not update on animation frames or unchanged polling.
7. **Provider/activity** comes first on line two because it explains a live
   agent. Plain shells omit the empty second line unless another fact exists.
8. **Branch or task** follows. Prefer task ID/title for task-bound work; branch
   otherwise. Do not repeat both when they say the same thing.
9. **Git facts** are compact and ordered: PR, conflicts, dirty stats. Detailed
   refusal/setup/error copy belongs in preview/detail or the action menu.

Nested shells under a worktree use a tree connector/indent in the identity
column while keeping marker and kind aligned with top-level rows. Indentation
must not consume the marker gutter or create a separate renderer.

### Responsive tiers

One renderer owns deterministic degradation:

| Available row width | Rendering |
| --- | --- |
| 48+ | Two lines; all primary and high-value secondary fields; age right-aligned |
| 32–47 | Two lines; project/name retained, secondary fields elide from the right |
| 20–31 | One line; status, kind, name, then age if it fits; global project drops before name |
| <20 | Status + truncated name; kind retained only if at least four name cells remain |

Elision priority is fixed and tested: detailed Git facts, branch/task,
activity text, provider, age, project, kind, then name. Status and at least one
name cell always survive. ANSI width, Unicode width, full-row selection fill,
and scrollbar reservation remain shared invariants.

### Selection, hover, and focus

- **Selected + list focused:** full-width selection band, strongest state.
- **Selected + preview focused:** quieter full-width band; identity remains
  visible without implying keyboard ownership.
- **Hovered, not selected:** subtle one-row/item band derived from the theme;
  never brighter than focused selection.
- **Pressed/dragged:** accent edge or insertion bar, not a third background.
- **Unavailable:** dim text plus a specific marker; it remains selectable so
  the user can read why it is unavailable.

Hover IDs are rebuilt from the exact render geometry every frame. Focus wins
over hover. Disabling mouse motion removes only hover paint/tooltips, not
buttons or actions.

## Controls and row action menu

### Header controls

Use semantic controls with responsive labels:

| Control | Wide | Narrow | Keyboard |
| --- | --- | --- | --- |
| Scope | `All projects ▾` / `sidecar ▾` | `All ▾` / project abbreviation | activate via palette; retain `K` and `@` routes |
| Create | `+ New` | `+` | `n`; `ctrl+n` direct shell; `F` direct PR |
| View | `≡ Active` | `≡` | `s` opens View (no longer cycles invisibly) |
| Filter | inline row while active, optional icon while inactive | `/` | `/` |

Buttons use one shared style: flat at rest, accent foreground on hover, subtle
band on keyboard focus/press. Keep labels text-based when space allows; glyphs
alone require help/palette descriptions and stable hit boxes. Header and
section hit regions remain typed outputs from `RenderSidebar`.

### Row actions

Add one overflow target (`…`) at the right edge of the selected or hovered row
when width permits. It opens a declarative action menu. Provide a keyboard
command (`a`, unless the complete context audit finds a collision) and optional
right-click as equivalent routes. The menu is capability-driven:

- common: Pin/Unpin, Rename shell, Open project Workspaces, Open in Git;
- live session: Type, Attach (when enabled), Stop/Restart where currently valid;
- worktree: Link task, Push, Merge, Delete when existing refusal rules permit;
- shell: Delete/terminate with the existing confirmation;
- unavailable: Explain, Refresh, Reveal/Edit project configuration.

Direct shortcuts remain for expert flow. The menu is discoverability, not a
second command implementation. Disabled actions should generally be omitted;
when the refusal itself is useful, show the item disabled with a short reason.
The footer continues to be app-owned and must not be duplicated in the sidebar.

## Architecture

### 1. Evolve `internal/workspacelist` into the shared view model

Keep the package presentation-only. Extend its neutral inputs rather than
importing app, overview, workspace plugin, Git, tmux, or inventory packages.

Recommended concepts:

```go
type Scope struct {
    ID, Label, ShortLabel string
    Kind ScopeKind // all-projects or project
}

type ViewSpec struct {
    Visibility Visibility
    Type       ItemTypeFilter
    Group      GroupMode
    Sort       SortMode
    Query      Query
}

type Capabilities struct {
    CanCreate, CanPin, CanReorder, CanOpenScope, CanOpenActions bool
}
```

`RenderSidebar` receives resolved header controls, `ViewSpec`, rows, and
capabilities; it returns rendered content plus typed regions for scope, create,
view, filter, row, row-project, row-overflow, and reorder. It does not invoke
callbacks.

Move global's stable-ID `Model` behavior toward a consumer-neutral list model
that both callers can own. In particular, grouping, sorting, pin partitioning,
filter parsing, selected identity, and viewport math should not be independently
reimplemented in the project plugin. Caller input order remains available as
the Manual sort baseline.

### 2. Extract a state-free workspace operation core

Create a narrow package (name during implementation after dependency review;
`internal/workspaceops` is illustrative) for the rules and execution plans
currently reachable only through the workspace plugin:

```go
type ProjectTarget struct {
    Name, Root, WorkDir string
}

type CreateRequest struct {
    Project ProjectTarget
    Kind    CreateKind
    // branch/task/provider/setup inputs already represented by current flow
}

type Plan struct {
    Summary, StableResultHint string
    Steps                     []Step
    Refusal                   string
}
```

- Planning and refusal are pure/state-free where possible.
- External Git, shell-manifest, tmux, setup, and provider calls sit behind the
  existing narrow adapters/functions rather than leaking into either view.
- Execution reports typed progress/result messages a project plugin or global
  host can consume.
- Recovery (retry setup, open anyway, remove newly created worktree) remains
  one workflow over the same operation record.
- Project Workspaces migrates first and must preserve current behavior before
  global adopts it. Do not begin with global-only copies.

This is not a general job framework. It is the smallest seam that lets two
Sidecar views invoke the same operation and refusal logic.

### 3. Add an app-owned global create coordinator

Global scope needs short-lived create UI state, not a workspace plugin:

- project/type selection and form state;
- one active operation with progress/recovery;
- affected-project refresh request;
- stable result hint used to bind the next inventory record; and
- cancellation that closes UI without abandoning an already-running external
  operation.

Keep collection in `workspaceinventory`. A successful operation invalidates
only the affected project result and schedules an async refresh. It must not
restart the global collector, reinitialize the project registry, or scan every
project synchronously.

### 4. Persist presentation state deliberately

Sidecar owns view preferences, so they belong in its existing state adapter:

- one global `ViewSpec`, pinned order, selected ID, and scroll state;
- one project `ViewSpec` and pinned order keyed by canonical project identity;
- last global-create project and create kind;
- optional named saved views in a later schema addition.

Missing fields use the recommended defaults. Unknown enum values fall back
safely. Migrations stay inside the state adapter. Do not write view state to
shell manifests or Git config.

### 5. Keep startup and terminal ownership unchanged

- No filesystem walks, database opens, Git commands, tmux calls, or project
  fan-out in `Init()` or synchronous `Start()` paths.
- Hover and view changes are pure in-memory projections.
- Global create/project choices load asynchronously after the modal appears.
- Only the visible selected terminal owns a `tty.Model`; scope changes reconcile
  and close it using the existing visibility contract.
- Sidebar redesign must not introduce a second preview geometry calculation or
  a second terminal capture pipeline.

## Implementation slices

### Slice 0: Lock the journey and visual contract

- Capture current wide, medium, and narrow project/global fixtures using the
  shared renderer tests and isolated real app.
- Add a product-level matrix for fields, controls, commands, scopes, and action
  availability. Treat deliberate differences as data, not caller branches.
- Audit every `workspace-list`, `global-workspaces`, filter, terminal, document,
  issue, modal, inline-editor, and overlay context before assigning `a`, `s`, or
  any new scope shortcut. Global navigation must not steal terminal input.
- Prototype the proposed header and rows as deterministic renderer fixtures in
  both Nerd Font and fallback modes. Confirm the four responsive tiers before
  changing lifecycle behavior.

**Ship gate:** a reviewed visual/interaction spec and fixtures; no production
behavior change required.

### Slice 1: Shared chrome, hover, and typed regions

- Extend `RenderSidebar` with scope/create/view/overflow controls and typed
  regions. General regions register first, exact controls last.
- Add shared rest/hover/pressed/focused styles derived from theme tokens.
- Add mouse-motion tracking as progressive enhancement and clear stale hover on
  resize, scroll, modal open, scope change, and mouse leave/no-motion timeout.
- Use identical header layout in both consumers while retaining existing
  commands behind the controls.
- Keep project section `+` buttons temporarily; remove them only in Slice 5
  after Create parity is proven.

**Ship gate:** both sidebars render and hit-test the same header grammar; all
keyboard paths still work without mouse motion.

### Slice 2: One row schema and responsive renderer

- Expand the neutral row projection to the field order and elision priorities
  in this plan.
- Remove caller-specific line composition. Callers resolve status and facts;
  `RenderRow` alone decides placement, width degradation, selection, and hover.
- Add typed subregions for project segment and overflow without decoding ANSI
  text.
- Preserve nested-shell indentation, animated activity, warnings, PR/conflict
  facts, pins, age semantics, and provider fallback.
- Add cross-consumer fixture tests from the same semantic records.

**Ship gate:** equivalent records differ only by explicit scope context, and
every line fits its allocated ANSI width at every tested terminal size.

### Slice 3: Unified View model and precise filtering

- Separate Group from Sort in `workspacelist`; migrate current global modes
  without changing their result order until the new defaults are selected.
- Move project Shells/Workspaces projection onto the same stable-ID model while
  retaining Manual input order.
- Add Visibility, Type, and project facets plus pure token parsing.
- Build one declarative View surface and use it from both consumers.
- Persist global/per-project preferences through the state adapter.
- Add project pins. Integrate the drag-reorder plan by exposing Manual only
  when its persistence has landed; drag is disabled under non-Manual sorts to
  avoid pretending the reordered item will stay where dropped.

**Ship gate:** both scopes can express and restore the same meaningful view;
refresh/filter/group/sort never moves selection to a different identity.

### Slice 4: Scope selector and identity-preserving transitions

- Add the shared scope control and project picker.
- Route project → All directly to global Workspaces, while preserving `K`'s
  last-global-tab behavior.
- Route All → project through the existing validated project switch plus
  `PendingWorkspaceSelection`; do not duplicate reinitialization.
- Make the project segment of a global row an exact mouse target and command.
- Persist and restore per-scope selection, filter/view, scroll, and preview tab.
- Update help, command palette, and keyboard skill documentation.

**Ship gate:** repeated All ↔ project transitions preserve the same selected
workspace and do not leak terminal ownership or reset unrelated project state.

### Slice 5: Extract and migrate project creation

- Characterize shell, workspace, PR, setup, recovery, cancellation, and refusal
  journeys before extraction.
- Extract the state-free planner/operation seam and migrate project Workspaces
  to it with no visible regression.
- Consolidate project header `New` and section `+` affordances into the shared
  Create menu. Retain direct shortcuts.
- Independently review lifecycle safety and failure recovery before any global
  caller is added.

**Ship gate:** project creation behavior and recovery are unchanged, but no
business rule depends on view/plugin fields.

### Slice 6: Global creation steel thread, then breadth

- Steel thread: from global, create a plain shell in the selected row's project,
  stay global, refresh that project, select the shell, and type into it.
- Expand to agent shells, worktrees, task/branch setup, and From PR using the
  same project flow and modal sections.
- Add affected-project progress, stable-result binding, timeout recovery, and
  Open project/Refresh actions.
- Prove unavailable/missing project refusal and cancel/retry behavior.
- Ensure global shutdown/scope exit does not orphan UI state or kill a created
  tmux session.

**Ship gate:** every create type available project-side is available globally
with one additional explicit project field and identical validation/recovery.

### Slice 7: Row action menu and final interaction polish

- Add the shared overflow/right-click/keyboard menu over existing commands.
- Audit command availability and refusal reasons across row kind, status, scope,
  active pane, interactive terminal, documents, issues, modals, and overlays.
- Add hover help/status copy without introducing a plugin-rendered footer.
- Remove obsolete buttons, duplicate renderer branches, invisible sort cycling,
  and stale key/help entries.
- Revisit user-named saved views only after built-in views have real-use proof.

**Ship gate:** a mouse-only user can discover primary actions, a keyboard-only
user loses no speed, and both routes invoke the same command handlers.

### Slice 8: Integrated proof and cleanup

- Focused tests for `workspacelist`, state migration, overview/global host,
  workspace plugin, keymap contexts, creation operations, and scope navigation.
- Full `go test ./...`, `go vet ./...`, `go build ./...`, and `git diff --check`.
- Isolated `scripts/tmux-drive.sh paths` proof before starting Sidecar. Confirm
  both tmux and state/config resolve outside the user's live trees.
- Capture project/global at wide, medium, and narrow sizes with Nerd Fonts on
  and off. Prove row elision, scrollbar, hover/click, filter tokens, View,
  pins, scope transitions, creation progress/recovery, and right-edge clipping.
- Prove the global-create steel thread against the isolated tmux server: create,
  observe/select, enter interactive mode, type, exit, and return to project.
- Confirm the default tmux server was neither queried for destructive actions
  nor stopped/restarted/replaced.
- Obtain independent code and visual review of the integrated candidate. Green
  tests alone do not complete the redesign.

## Acceptance criteria

1. Project and global Workspaces present one header grammar, one row grammar,
   one responsive policy, one selection/hover language, and one view model.
2. Both scopes expose Create in the same location. Global supports every
   project-side creation type after an explicit owning-project choice.
3. Project and global creation invoke the same planning, validation, refusal,
   execution, progress, and recovery core; neither invokes the other view.
4. Global creation stays global, refreshes only the affected project, binds the
   created identity, and offers honest recovery when observation is delayed.
5. Status, kind, project, name, age, provider/activity, branch/task, and Git
   facts appear in the specified order and degrade deterministically by width.
6. Group, Sort, Visibility, Type, project facet, pins, and query are independent
   concepts. State persists at the correct global/per-project scope.
7. Plain filtering and token filtering use one pure matcher. Unknown tokens
   degrade to text rather than silently excluding data.
8. The scope selector moves All ↔ project without losing stable selection,
   view state, preview state, or terminal ownership. `K`, brand, and `@` retain
   their distinct existing jobs.
9. Hover is optional enhancement. Every control and action remains reachable by
   keyboard and click on terminals without motion reporting.
10. The overflow action menu exposes existing capability/refusal decisions; it
    contains no duplicate lifecycle implementation and never steals literal
    terminal/editor/modal input.
11. Plugin output stays within allocated height, header/footer remain visible,
    and the preview's rightmost column/border remains intact at supported widths.
12. Startup does no new synchronous I/O or subprocess work. View changes do not
    recollect inventory, and creation invalidates only its affected project.
13. Focused tests, full gates, isolated real-app lifecycle proof, and independent
    code plus visual review are recorded before completion.

## Explicit non-goals

- No Sidecar CLI/API/MCP is added for Git/tmux operations Sidecar does not own.
- No hidden workspace plugin per configured project and no synthetic global
  project.
- No global pane tree or preview/windowing redesign in this changeset.
- No restart, replacement, or cleanup of the default tmux server.
- No query service, database, or generalized job framework.
- No drag reorder while a computed sort is active.
- No hover-only actions and no second footer inside a plugin view.
- No automatic project switch after global creation.

## Risks and deliberate trade-offs

| Risk | Response |
| --- | --- |
| Global mutation makes scope mistakes costly | Project is explicit through confirmation; pure planning/refusal runs before mutation; affected-project refresh is keyed canonically |
| Extracting creation broadens a visual task | It is the smallest honest seam for the requested global create behavior; project migrates and proves parity first |
| Header controls crowd narrow terminals | Fixed degradation order and text labels on wide layouts; controls never wrap into extra plugin height |
| Rich filters become a mini-language | Tokens are optional, completions are discoverable, and unknown syntax remains ordinary text |
| Hover differs by terminal | It changes paint/help only; keyboard and click contracts are complete without it |
| Persisted views surprise users | Defaults are explicit, Reset view is always present, schema fallback is safe, and named views are deferred |
| Pins, groups, and manual order interact poorly | Pins partition first; group/sort apply to the remainder; Manual appears only where durable order exists |
| Scope selector duplicates `K` and `@` | Each route has a documented job and the selector uniquely preserves workspace identity across scopes |
| Concurrent active work overlaps app/overview/workspace files | Implement in dependency-ordered slices, preserve unrelated dirty files, and rebase each slice on the integrated candidate before review |

## Plan-level completion

This document is a product and implementation proposal. Creating it does not
claim that the redesigned sidebar, global creation, tests, runtime proof, or
review have been completed. Implementation should be tracked in dependency
order, with each substantive slice independently reviewed before the next
lifecycle-expanding slice begins.
