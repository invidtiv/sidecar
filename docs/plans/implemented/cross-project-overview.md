# Plan: Cross-project agent overview

**Research snapshot:** 2026-08-09

**Supersedes:** [Cross-Project Overview — Vision & Exploration](../deprecated/cross-project-vision.md)

**Follow-on:** [First-class global Overview and cross-project Workspaces](global-overview-workspaces.md)

## Decision first

Add **Overview** as a first-class Sidecar destination reached from a pinned,
visually distinct item at the top of the project switcher. The switcher still
opens from `@` or by clicking the project name in the header. Overview is not a
synthetic `ProjectConfig`, a configured project, or a normal plugin tab: it is an
app-level view over all configured projects.

The first useful version is one cross-project Kanban board showing agent-backed
worktrees and agent-backed project shells from every configured project. It uses
the same semantic states and precedence as the Workspaces List and Kanban views:

- **Working** — provider-backed evidence says the agent is active;
- **Needs attention** — provider-backed evidence says the agent is blocked on a
  permission, confirmation, question, or other visible user action;
- **Done** — an observed working/blocked agent has transitioned to idle and that
  completion has not yet been seen;
- **Idle** — explicit or conservative idle state that is not a new completion;
- **Paused / unavailable** — no live session, an ended session, a missing path,
  an error, or status that cannot be established safely.

“Waiting” is deliberately not inferred from generic idleness. Sidecar already
detects many actionable waits as `agentactivity.StateBlocked` from bounded,
current terminal evidence. The cross-project view must preserve that evidence
and call the lane **Needs attention** so it is not confused with “finished and
waiting for another prompt.” Unknown, stale, ambiguous, or unavailable evidence
must remain visibly unknown/unavailable rather than becoming a notification.

Extract the current Kanban presentation and interaction into a small reusable
component, but keep collection and actions source-local. The project-specific
Workspaces view continues to own worktree operations and terminal interaction;
Overview is initially read-only except for navigation. No Sidecar CLI, API, or
MCP surface is needed: Sidecar is presenting Git, tmux, and agent state owned by
underlying tools. The shared status reducer and collector should nevertheless be
state-free or narrowly stateful library code so a headless caller could reuse
the rules later without extracting them from a view.

## Outcome and user journey

### Open Overview

1. The user presses `@` or clicks the project name in the top-left header.
2. The existing project switcher opens with a pinned **Overview** item above all
   configured projects, separated from them and rendered with an overview icon,
   accent treatment, and an “All configured projects” description.
3. Overview remains at the top while projects are filtered. It is not written to
   `config.json` and is not included in the configured-project count.
4. Pressing Enter or clicking the item closes the switcher and opens Overview.
   The current project, worktree, active plugin, and project theme remain intact
   behind it; entering Overview does not call `registry.Reinit`.
5. The header reads `Sidecar / Overview`. Clicking it opens the same switcher.
   Selecting a normal plugin tab exits Overview and returns to the still-current
   project without another project reload.

The switcher should retain its current low-friction selection behavior: when
opened from a project, the current project is initially selected even though
Overview is pinned above it; when opened from Overview, Overview is selected.
Moving the cursor over Overview uses the global theme preview, and cancelling
the switcher restores the theme for the actual current project.

### Read the board

1. Overview paints immediately from an in-memory snapshot or a bounded loading
   state. No project walk, file read, database open, or subprocess runs on the
   Bubble Tea update/render path or before the first frame.
2. Project results arrive incrementally. A slow, missing, non-Git, or broken
   project gets its own visible error/freshness state and does not hold up the
   other projects.
3. Each card identifies the project first, then the worktree or shell and agent.
   The card includes semantic status, useful task/branch context when already
   available from Sidecar metadata, and freshness. It does not run diff, GitHub,
   CI, td, or conversation queries just to populate the first version.
4. The board includes:
   - worktrees with a live or previously configured agent;
   - project shells that are explicitly agent-backed; and
   - offline/unavailable entries only when Sidecar has durable evidence that an
     agent belongs there.
5. It omits plain shells and worktrees that have never had an agent. This keeps
   the overview about agent attention rather than becoming a second all-repo
   worktree browser.
6. Keyboard navigation uses the same arrows/vim keys as the Workspaces Kanban.
   A single click selects a card. Enter or a double-click opens it.
7. Narrow terminals get a compact list projection of the same cards and states;
   the header/footer remain visible at every supported size.

Within a lane, sort actionable and recent information predictably:

1. newly changed state before unchanged state;
2. newest `ChangedAt` first when known;
3. configured-project order; then
4. stable worktree/shell display order.

Do not reorder cards on animation frames or inconsequential polls. Preserve the
selected card by stable ID when a refreshed snapshot changes lane or position.

### Open an agent in its project

Enter or double-click performs one app-owned navigation transition:

1. resolve the card's stable project and workspace identity;
2. switch to the card's exact worktree path, or the owning project root for a
   project shell, through the existing project/worktree switch path;
3. focus the Workspaces plugin;
4. retain a pending selection until the destination inventory and tmux
   reconnection finish, then select the matching worktree or shell; and
5. show the normal project Workspaces view without automatically attaching,
   sending a key, acknowledging a prompt, or otherwise mutating the session.

The pending selection is necessary because `registry.Reinit` is synchronous but
the destination Workspaces inventory is asynchronous. Do not compose unordered
`tea.Batch` messages and hope that selection arrives after `RefreshDoneMsg`.
If the project or session disappeared between snapshot and navigation, switch to
the project when safe and show a clear stale-card toast rather than selecting a
different similarly named session.

## What exists now

| Current seam | Current behavior | Required change |
| --- | --- | --- |
| `internal/app/model.go`, `view.go`, `update.go` | `@`/header click opens a modal over `[]config.ProjectConfig`; selection always calls `switchProject` | Model a destination separately from project config, pin Overview, and add an app-level Overview state |
| `internal/plugin/registry.go` | Every project switch stops, initializes, and starts all project-scoped plugins | Do not reinitialize on Overview entry; reuse the existing path only when leaving for a card/project |
| `internal/plugins/workspace/view_kanban.go` | Rendering, hit regions, card text, and workspace state are coupled to `*workspace.Plugin` | Extract reusable board layout/selection/rendering while keeping workspace projection local |
| `internal/plugins/workspace/kanban.go` | One precedence function groups project worktrees into Working, Blocked, Done, Idle, Paused; Shells is a special first column | Extract the semantic presentation input/output used by List, project Kanban, and Overview; express Shells as data rather than renderer control flow |
| `internal/agentactivity` | Product-neutral provider probes classify `working`, `blocked`, `idle`, or `unknown`; `Tracker` debounces idle and distinguishes unseen Done from seen Idle | Reuse this authority and evidence; do not create a second “waiting” detector in Overview |
| `workspace.refreshWorktrees` | One project builds a full repo snapshot and loads changes/stats with bounded concurrency | Add a lighter read-only cross-project inventory path; do not run full diff/stat refresh for every project |
| Workspaces agent polling | Current-project agents poll at visible/background/unfocused cadences; entering project Kanban immediately polls all current-project agents | Add a cross-project status collector with one tmux inventory per cycle and bounded captures only for matched agent panes |
| Shell/worktree metadata | Project-scoped data and `shells.json` help reconnect sessions; reconciliation may update manifests | Overview reads metadata without mutation; it must never reconcile/prune/write another project's manifest |
| app toasts/footer | Transient status exists, but there is no durable cross-project attention stream or notification center | Define attention transitions independently of the first view, then add an in-app badge/filter before any OS notifier |

The old plan focused on Git worktree lifecycle, PR counts, CI status, recent
activity, a `projects.mode`, and an AI bridge. Those are not prerequisites for
this journey and would make the first refresh much slower and less trustworthy.
They are deferred until observed use of the agent overview calls for them.

## Product model and boundaries

### 1. Stable identities

Every card needs identity that cannot collide when two projects use the same
worktree, shell, branch, or tmux session name:

```go
type AgentRef struct {
    ProjectKey  string // canonical configured project/main-worktree identity
    WorkspaceKey string // canonical worktree path key or durable shell key
    Kind        WorkspaceKind // worktree or project-shell
    TmuxTarget  string
    PaneID      string
    Provider    string
}
```

The card ID is derived from `ProjectKey + Kind + WorkspaceKey`, never from a
display name. The first collector should correlate tmux panes using one global
`list-panes` snapshot, canonical pane/current paths, exact project/worktree
inventory, Sidecar metadata, and tmux namespace where available. Use longest
valid path ownership when a pane is inside a worktree. A session-name-only match
is insufficient across projects.

If two projects still plausibly own a pane, return an ambiguous/unavailable
record with diagnostic detail and do not capture it, notify from it, or navigate
to it. A later session-identity migration may add project-qualified durable
metadata, but renaming or replacing live tmux sessions is explicitly outside
this plan and must never be required for the first slice.

### 2. Shared semantic presentation

Move the current health/activity precedence behind a small product-neutral
input/output seam, for example `internal/agentstatus`:

```go
type Input struct {
    Provider       string
    Live           bool
    Missing        bool
    Orphaned       bool
    Paused         bool
    Err            error
    LegacyStatus   string
    Activity       agentactivity.Tracker
    CapturedAt     time.Time
}

type Presentation struct {
    Lane           LaneID
    Icon           string
    Label          string
    Attention      bool
    Evidence       string
    ChangedAt      time.Time
    Freshness      Freshness
}
```

Health/liveness still overrides stale activity. Supported providers use
`agentactivity`; unsupported providers retain the existing conservative legacy
projection and are visibly marked as lower-confidence. `Attention` is true only
for fresh, visible blocker evidence. Workspaces List, project Kanban, Overview,
and later notifications all consume this same result.

Do not let the generic Kanban component decide agent state. It receives lanes
and display fields that have already been resolved by the semantic layer.

### 3. Reusable Kanban component

Extract a small component such as `internal/kanban` with:

- ordered lane definitions and per-lane label/style;
- cards with stable ID plus title/subtitle/detail/meta presentation fields;
- selection by card ID, column/row keyboard movement, scroll offsets, and
  selection preservation across snapshots;
- width/height-constrained rendering, lane counts, empty/loading/error cells,
  and a compact-layout signal;
- mouse hit regions, hover, click selection, and double-click activation; and
- semantic actions such as `Selected(id)` and `Activated(id)`, not workspace
  callbacks or `any` payloads hidden inside the renderer.

Adapters remain thin:

- Workspaces maps its worktrees and shells into board cards, keeps its existing
  list fallback on narrow screens, and resolves selected IDs back to workspace
  actions.
- Overview maps cross-project snapshots into the same card model, adds the
  project label/freshness, uses a compact cross-project list when narrow, and
  resolves activation into app navigation.

The extraction must preserve existing Workspaces behavior before Overview uses
it: lanes, Shells column, selection, animation, keybindings, hit regions,
double-click behavior, card text, narrow fallback, and header/footer height.

### 4. App-level Overview model

Add an Overview model with a narrow top-level-view contract. Construction,
`Init`, and the synchronous part of `Start` remain I/O-free; `Start` returns
commands for collection. `Update`, `View`, `Commands`, and `FocusContext` mirror
what the app needs from a visible plugin. The app renders either the active
project plugin or Overview, but Overview is not registered as a project plugin
and is not included in numbered plugin tabs.

App state should retain:

- whether Overview is active;
- the last snapshot and per-project load/error/freshness states;
- board selection/scroll state;
- a collection generation and cancellation function; and
- the underlying current project/plugin, unchanged while Overview is active.

Overview gets its own root context, short footer commands (`Open`, `Refresh`,
and any existing navigation hints), mouse routing, and `q` behavior consistent
with other root views. Plugin tab clicks and numeric plugin shortcuts first exit
Overview, then focus the chosen current-project plugin.

### 5. Read-only cross-project collector

Create a collector outside the view layer, for example `internal/overview` plus
read-only inventory helpers extracted from Workspaces. Its output is immutable
snapshot data; it does not retain Bubble Tea models or mutate repositories,
manifests, tmux sessions, task stores, or config.

Collection proceeds in stages:

1. Normalize and de-duplicate configured projects by canonical main-worktree
   identity while preserving configured order and display names.
2. Load lightweight Git worktree identity and existing Sidecar agent/shell
   metadata per project with bounded concurrency.
3. Take one read-only tmux server/pane inventory for the whole refresh, including
   pane ID, session name, current path, current command, title, and liveness.
4. Correlate panes to durable project/workspace identities. Refuse ambiguous
   matches.
5. Capture only matched live agent panes, with a global capture concurrency
   bound, and feed current-bottom evidence through the existing provider probes
   and persistent tracker keyed by `AgentRef`.
6. Emit project results incrementally and a final refresh summary. Generation
   IDs/cancellation reject results from an exited/restarted refresh.

Do not call the current full `BuildRepoSnapshot` plus
`loadRefreshChanges` pipeline unchanged for every configured project. Overview
does not initially need dirty-file inventories, line stats, conflict detection,
PR hydration, task database queries, GitHub calls, conversation scans, terminal
screen models, or plugin initialization.

### 6. Freshness and failure semantics

Each project and card carries `ObservedAt` and a freshness state. Preserve the
last good snapshot during a refresh or transient error, render it as stale, and
never silently present it as current. On first-load failure show a project error
row/card with retry guidance.

At minimum distinguish:

- loading with no prior data;
- fresh;
- refreshing with prior data;
- stale after a failed/expired refresh;
- unavailable/ambiguous; and
- configured path missing or not a Git project.

Stale status can remain useful for navigation, but it cannot create attention
events. Activation revalidates the destination before switching.

## Performance design and budgets

Performance is a product requirement because configured-project counts can be
large and endpoint security makes every file open and subprocess expensive.

- **Startup:** zero Overview filesystem, database, Git, GitHub, td, or tmux work
  before the first ready frame. The app should not pay for a view it has not
  opened.
- **Entry:** paint loading or cached data in the same update that activates the
  view; all collection runs in returned `tea.Cmd`s.
- **Fan-out:** use one global tmux inventory per refresh. Bound project inventory
  and pane-capture concurrency independently (start with the repository's
  existing maximum of four, then tune from measurements).
- **Scope:** inspect only configured projects. Do not recursively scan a projects
  root or agent-history home directories.
- **Work:** no diff/stat/PR/CI/task/conversation work in the initial board.
- **Incrementality:** deliver project results as they finish. A single project
  cannot delay the whole board.
- **Cadence:** while Overview is visible, refresh live status more often than
  project inventory; while hidden, the first release stops collection. A later
  attention supervisor may run a slower adaptive cadence outside Overview.
- **Caching:** cache immutable project inventory separately from live status and
  invalidate it on explicit refresh, config change, missing paths, or a bounded
  TTL. Do not persist terminal screen contents.
- **Stability:** do not start one fsnotify watcher, plugin registry, or control
  manager per project in the first version.

Before tuning intervals, record measurements for 1, 10, and 30 configured
projects with representative live agents: time to first project result, time to
complete refresh, subprocess count, tmux command/capture count, maximum
concurrency, allocations, and UI responsiveness during collection. The gate is
not a universal millisecond target; it is no blocked event loop, bounded work,
incremental useful output, and no polling growth when the result set is idle.

## Attention and notification path

The first board establishes the event seam even if persistent/background
notifications land in a later slice.

Define a state reducer over consecutive fresh snapshots:

```go
type AttentionEvent struct {
    ID          string // stable transition ID, not a toast string
    Agent       AgentRef
    Kind        AttentionKind
    Evidence    string
    FirstSeenAt time.Time
    ObservedAt  time.Time
}
```

Rules:

- entering fresh `Needs attention` creates one event;
- repeated observations of the same blocker update freshness but do not notify
  again;
- leaving the blocker resolves the event;
- re-entering after resolution creates a new event;
- Idle, unseen Done, stale, unknown, ambiguous, missing, and collector errors do
  not masquerade as “waiting for you”;
- opening Overview alone does not acknowledge or resolve anything; and
- navigating to a card does not send input or approve a prompt.

The first notification surface should be an in-app attention count/badge in the
global header. Activating it opens Overview focused or filtered to Needs
attention. Add transient toasts only if use shows they are not noisy. Put any
future macOS/system notifier behind a small adapter driven by the same events;
do not encode OS notification calls in provider probes or the Overview view.

To surface attention while the user stays in another project, promote the same
collector/reducer into a low-frequency background supervisor in a later slice.
That slice must reuse cached inventory, maintain the one-tmux-inventory rule,
pause or decay when Sidecar is unfocused, and expose freshness. It must not
instantiate every project plugin or touch the default tmux server beyond the
same read-only list/capture operations Sidecar already performs.

## Implementation slices

### Slice 0 — Characterize and extract without changing behavior

1. Add deterministic tests that lock the current Workspaces Kanban's lanes,
   Shells column, health precedence, supported/unsupported agent behavior,
   unseen Done to seen Idle transition, keyboard selection, mouse hit regions,
   double-click action, animation, narrow fallback, and constrained height.
2. Extract the shared `agentstatus` presentation seam and make Workspaces List
   and Kanban consume it. Preserve provider evidence and existing golden parity.
3. Extract the reusable Kanban component and adapt the existing Workspaces
   board to it. No visual or behavioral redesign in this slice.
4. Independently review the extraction before building Overview; a shared
   component that subtly changes the existing board is not a valid foundation.

### Slice 1 — Steel thread: switcher to two-project live board

1. Introduce a typed project-switcher destination model. Add the pinned Overview
   item, styling, filtering behavior, theme preview behavior, keyboard/mouse
   activation, and tests for `@` plus header-click entry.
2. Add the app-level Overview model/context with loading, empty, partial, error,
   and compact states. Entering/exiting must leave project plugin state intact.
3. Extract the minimum read-only inventory needed for agent-backed worktrees and
   shells. Prove one current project and one other configured project through
   real Git worktrees, Sidecar metadata, and an isolated tmux server.
4. Render those agents with the shared status and Kanban components.
5. Implement Enter/double-click navigation through a stable pending-selection
   message and prove the exact destination is selected after async reinit.

This slice is complete only when the real journey works end to end; a board fed
by hard-coded fixture data is not the steel thread.

### Slice 2 — All configured projects and performance hardening

1. Add normalization/de-duplication, bounded project fan-out, one global tmux
   inventory, bounded live captures, generation cancellation, incremental
   results, and last-good freshness.
2. Cover agent-backed project shells, provider detection parity, collisions,
   missing/non-Git projects, unavailable tmux, project removal during refresh,
   and card movement while selected.
3. Add explicit refresh and adaptive visible-view polling. Ensure leaving
   Overview cancels or drains work without leaking timers/goroutines.
4. Measure the 1/10/30-project cases and remove observed redundant process/file
   work before enabling Overview by default.

### Slice 3 — Cross-project attention surface

1. Add the pure attention transition reducer and tests for create/update/resolve/
   re-enter behavior, stale suppression, and blocker evidence retention.
2. Add a global header attention badge/count and activation into the Needs
   attention lane/filter.
3. Promote collection to a slower background supervisor only after measuring
   the visible-view collector. Share cache, identity, reducer, and freshness;
   do not create a second status path.
4. Evaluate in-app toast policy from real use. Defer OS notifications unless a
   concrete journey calls for them, then add one notifier adapter.

### Slice 4 — Follow-up product expansion from evidence

Only after the agent overview is useful and measured, consider:

- project summary cards or counts above the board;
- search/filter by project, provider, task, or lane;
- durable attention history and acknowledgement policy;
- PR/CI/task signals with their own slower adapters and caches;
- agent-facing exported snapshots if Sidecar begins to own a unique capability;
- persistence/restoration of Overview as the launch destination; and
- multi-select or cross-project actions, each with explicit validation and
  refusal rules.

These are not hidden requirements of the initial implementation.

## Likely file map

The exact split can adjust during extraction, but responsibilities should land
approximately here:

| Area | Change |
| --- | --- |
| `internal/agentactivity/` | Keep provider probes and transition evidence authoritative; add only contracts needed by shared presentation |
| `internal/agentstatus/` | New state/health/freshness presentation reducer shared by all agent surfaces |
| `internal/kanban/` | New generic board model, layout, rendering, keyboard/mouse interaction, and tests |
| `internal/workspaceinventory/` | New/extracted read-only project/worktree/agent metadata inventory; no mutation or Bubble Tea |
| `internal/overview/` | Cross-project snapshot collector, identity correlation, cache/freshness, attention reducer, and tests |
| `internal/plugins/workspace/kanban.go`, `view_kanban.go`, `view_list.go`, `keys.go`, `mouse.go` | Adapt existing List/Kanban to shared status/board; add stable pending destination selection |
| `internal/app/model.go`, `view.go`, `update.go`, `commands.go` | Top-level Overview state, switcher destination, header/footer/context routing, navigation sequencing, attention badge |
| `internal/config/` | No synthetic Overview entry; only add cadence/config if measurements prove a user-facing control is necessary |
| `scripts/tmux-drive.sh`, `docs/guides/active/headless-testing.md` | Reuse the isolated proof path; extend fixtures only where needed for multi-project navigation/status proof |

Avoid moving mutating workspace lifecycle code into a generic package merely to
support this view. The new library boundary is read-only inventory, semantic
projection, and presentation.

## Verification

### Automated behavior

- project-switcher ordering, selection, filtering, counts, theme preview,
  mouse activation, and no config mutation;
- Overview entry/exit without plugin `Stop`/`Init`/`Start` or current project
  state loss;
- shared Workspaces List/Kanban/Overview semantic matrix for every supported
  provider plus conservative unsupported fallback;
- fresh visible blocker -> Needs attention; idle -> Done/Idle; stale/unknown/
  ambiguous -> never attention;
- health precedence over stale working/blocked activity;
- stable card selection across reordering and lane transitions;
- generic Kanban height/width constraints, narrow behavior, hit regions,
  keyboard, single click, and double-click;
- collector de-duplication, bounded concurrency, cancellation/generation, one
  tmux inventory per cycle, partial failures, last-good freshness, and no writes;
- project/worktree/session collision refusal;
- pending navigation applied after async destination load and not to a similarly
  named card; and
- attention event transition/deduplication tests when Slice 3 lands.

### Real app proof

Use `scripts/tmux-drive.sh` only after `./scripts/tmux-drive.sh paths` confirms
both tmux and Sidecar state/config isolation. Never stop, restart, replace, or
clean sessions from the default tmux server.

Build an isolated fixture with at least:

- two configured Git projects;
- duplicate worktree display names across projects to prove stable identity;
- worktree agents in Working, Needs attention, Done, Idle, and unavailable
  states, using real supported-provider evidence fixtures;
- one agent-backed project shell and one plain shell (the latter must be absent
  from Overview);
- one missing/broken configured project; and
- enough cards to exercise scrolling and narrow fallback.

Drive and capture this journey as text and PNG:

1. launch Sidecar and prove the first ready frame does not wait for Overview;
2. click the header project name, verify filter focus and pinned styling;
3. close, reopen with `@`, and enter Overview with Enter;
4. observe immediate loading/cached paint followed by incremental projects;
5. verify all semantic lanes, project labels, errors, freshness, footer hints,
   and stable layout;
6. navigate by keyboard and mouse;
7. single-click selects without switching;
8. double-click/Enter a non-current-project card, then prove the correct project,
   exact worktree/shell selection, Workspaces focus, and no automatic attach;
9. return to Overview and prove current-project state was retained; and
10. compare isolated tmux sessions, config, state, and manifests before/after to
    prove no unrelated mutation or loss.

Record process/tmux counts and refresh timing from this same behavior-faithful
fixture. Unit tests and static screenshots alone do not prove the collector or
navigation journey. Set `SIDECAR_OVERVIEW_TRACE=stderr` for privacy-safe
per-cycle counts and timings (configured/de-duplicated projects, first result,
completion, project operations, tmux inventories, captures, concurrency, and
cancellation/draining). The trace never includes paths, pane IDs, or captured
terminal content and is disabled by default.

## Acceptance criteria

1. Overview is always the visually distinct first switcher item and is reachable
   by keyboard and mouse from both `@` and the clickable header project name.
2. Entering Overview is immediate, performs no project/plugin reinitialization,
   and preserves the current project for a cheap return.
3. The first board incrementally shows agent-backed worktrees and shells across
   all configured projects with project identity, semantic state, and freshness;
   it excludes plain shells and never invents unavailable agents.
4. Workspaces List, project Kanban, Overview, and attention events share one
   tested status/health precedence, including provider evidence.
5. A real visible permission/question/confirmation wait appears as Needs
   attention. Generic idle, stale, unknown, and ambiguous state does not.
6. Enter and double-click navigate to the exact project and workspace identity,
   focus Workspaces, and never auto-attach or send terminal input.
7. Collection is read-only, asynchronous, cancellable, bounded, partial-result
   friendly, and uses no more than one tmux inventory per refresh cycle.
8. The shared Kanban component preserves the current project-specific board and
   constrains both consumers to their allocated dimensions.
9. Real isolated-tmux/state proof passes without touching the default tmux
   server or real Sidecar state tree.
10. The implementation and evidence receive independent review before the work
    is considered complete.

## Explicitly deferred

- GitHub PR counts, CI status, commit-ahead workflow lanes, and recent activity;
- td aggregation, conversation search, Kestrel/OpenClaw, chat, or autonomous
  cross-project actions;
- a `projects.mode: "overview"` setting or automatic launch into Overview;
- filesystem discovery beyond `projects.list`;
- persistent background monitoring in the first board slice;
- OS notifications before the in-app attention signal proves useful;
- renaming/replacing existing tmux sessions; and
- a Sidecar CLI/API/MCP merely to mirror this presentation-layer view.

These can be reconsidered from observed pressure without changing the core
identity, status, collector, Kanban, or attention seams established here.
