# Plan: First-class global Overview and cross-project Workspaces

**Research snapshot:** 2026-08-11

**Builds on:** [Cross-project agent overview](cross-project-overview.md)

## Decision first

Evolve the existing app-level **Overview** into Sidecar's first-class global
space. It gets its own top navigation and remembers its own active tab, while
the current project remains intact underneath it.

The two initial global tabs are:

- **Agents** — the existing cross-project semantic Kanban board;
- **Workspaces** — a two-pane browser over every configured project's durable
  shells and Git worktrees, with a status-grouped/filterable list on the left
  and a read-only live terminal preview on the right.

Project space keeps its current plugin tabs (`Files`, `Workspaces`, `Git`,
`Tasks`, and any enabled plugins). The header renders the tabs owned by the
active space only. Project tabs do not sit behind an active global view, and
global tabs do not appear as project plugins.

```text
Global space                         Project space

Sidecar / Overview                  Sidecar / sidecar
        [Agents] [Workspaces]               [Files] [Workspaces] [Git] …
                 |                                   |
                 v                                   v
      all configured projects              one project/worktree context
```

This is not a synthetic project, `ProjectConfig`, plugin registry, or
`projects.mode`. Entering Overview must not call `registry.Reinit`, alter the
current worktree, or replace project-scoped state. Sidecar remains a
presentation layer over Git, tmux, and provider state, so this work does not
add a ceremonial CLI/API/MCP surface.

## Why this hierarchy

The existing Overview solved cross-project attention, but its header still
shows project plugin tabs even though none of them is active. That makes the
screen look project-scoped while its data is global. Conversely, turning
Overview into a normal plugin would imply that it belongs to the selected
project and would force app-owned navigation and collection into the plugin
registry.

Two explicit spaces make scope visible:

- the title says whether the user is in `Overview` or a named project;
- the visible tabs belong to that space;
- switching global tabs is cheap and never reloads a project;
- returning to project space restores the exact prior project, worktree, plugin,
  selection, and preview state; and
- selecting a global item can still perform the existing validated transition
  into its owning project's Workspaces plugin.

The design deliberately shares data, status, filtering, list interaction,
two-pane geometry, and terminal rendering. It does not force global Workspaces
to instantiate one mutating project Workspaces plugin per repository.

## Target journeys

### 1. Move between global and project space

1. From any project plugin, `K` or a click on the **Sidecar** brand opens the
   last-used Overview tab. On first use that is **Agents**.
2. The underlying current project/plugin is preserved and collection starts
   asynchronously only for the visible global tab.
3. `K`, `q`, or another brand click returns to the exact project/plugin that was
   visible before Overview opened.
4. `@` or a click on the destination name opens the existing destination
   switcher. Its pinned Overview item opens the last-used global tab; a project
   item performs the normal validated project switch.
5. Within either space, backtick/bracket cycling and numeric shortcuts operate
   only on that space's visible tabs. They never silently cross the scope
   boundary.
6. Selecting a different project through `@` updates the remembered project
   destination. Returning to Overview does not lose it.

The Sidecar brand is the fast two-state toggle; the destination switcher is the
explicit many-project route. No new global shortcut is required.

### 2. Read cross-project agent state

The **Agents** tab retains the current board behavior and shared
`agentstatus` semantics: Working, Needs attention, Done, Idle, and Paused. It
keeps stable card identity, per-lane scrolling, compact fallback, freshness,
and validated navigation.

The tab label changes from an implicit whole-screen "Overview" to **Agents**;
the content title can remain `Agent Overview`. Existing in-review Overview
polish and provider-detection fixes are part of the baseline, not work to
reimplement here.

### 3. Browse all shells and workspaces

1. The user switches to global **Workspaces**.
2. The left pane incrementally lists every configured project's:
   - durable Sidecar shell definitions, including plain non-agent shells;
   - every Git worktree, including the main worktree and worktrees with no
     agent/session; and
   - a project-scoped unavailable row when inventory cannot be read safely.
3. Each row has a primary workspace/shell name and a project subtitle. When
   space allows it also shows kind, branch/task, provider, semantic state, and
   relative age. Project colour may reinforce identity but is never the only
   differentiator.
4. Up/down or `j`/`k` changes selection and immediately updates the right pane.
   Selection is preserved by stable ID when refresh, filtering, or sorting
   changes row position.
5. If the item owns one unambiguous live pane, the right side shows a bounded,
   read-only terminal preview. If it has no pane, is stale, or is ambiguous, the
   pane shows useful metadata and the reason no preview is available.
6. Right/left moves focus between list and preview. Preview navigation scrolls
   captured output but never forwards terminal input.
7. Enter or double-click validates the stable identity, switches to the owning
   project/worktree when necessary, focuses project **Workspaces**, and applies
   the existing pending selection after asynchronous inventory load. It never
   auto-attaches, sends a key, creates a session, or acknowledges a prompt.

The global browser is read-only. Creation, rename, delete, attach, interactive
input, Git lifecycle, Diff, and Task actions remain in the owning project's
Workspaces plugin, where all of their validation and refusal rules already
live.

### 4. Filter and sort without losing keyboard flow

The default order is **Activity**, a vertical projection of the Kanban model:

1. Needs attention;
2. Working;
3. Done;
4. live plain shells;
5. Idle;
6. Paused / unavailable;
7. no live session.

Within a group, use most recent meaningful state change, configured-project
order, then stable local display order. Do not reorder on animation frames or
unchanged polls.

`s` cycles explicit sort modes: **Activity**, **Project**, **Recent**, and
**Name**. The active sort is visible in the left-pane header and clickable.
Sorting changes presentation only; it never changes identities or collection.

`/` focuses an inline filter. The same filter interaction is added to the
project Workspaces list so the useful behavior is not global-only:

- match case-insensitively across workspace/shell name, project, branch, task
  ID/title already available, provider, and semantic status label;
- update results as text changes;
- keep arrow/`ctrl+n`/`ctrl+p` navigation live while filtering;
- Enter leaves the filter focused item selected and returns to list navigation;
- first Escape clears the query, second Escape exits filter focus; and
- show `N of M` plus a clear no-match state.

The explicit `/` entry preserves existing project Workspaces shortcuts such as
`n`, `D`, and `p`. While the filter owns focus, both consumers report a text
input context so app/global shortcuts cannot steal printable characters or
pastes. Filter and sort state are in-memory per consumer in the first version;
they are not written to config or project state.

## Product model and shared seams

### App scope and top navigation

Replace the implicit `overviewActive` routing concept with a small explicit
scope model, without turning it into a generalized navigation framework:

```go
type AppScope uint8

const (
    ScopeProject AppScope = iota
    ScopeGlobal
)

type GlobalTab uint8

const (
    GlobalAgents GlobalTab = iota
    GlobalWorkspaces
)
```

The app continues to own the current project/plugin. The Overview model owns
the current global tab and the state of both global views. `headerLayout`, tab
hit regions, cycling, numeric shortcuts, footer context, and help use one list
of visible tab specifications chosen from the active scope.

Do not encode global tabs as fake plugin indices. Use typed tab IDs in mouse
regions and key routing so a future enabled/disabled tab cannot shift an index
into the wrong action.

### Cross-project inventory

Extend `internal/workspaceinventory` from an agent-card collector into a
read-only catalog that can project either agent-only or all-workspace views.
Do not weaken the current Overview guarantee by making the collector mutate
manifests or initialize project plugins.

The catalog item needs to distinguish session health from agent activity:

```go
type Item struct {
    ID                       string
    ProjectKey, ProjectName  string
    ProjectRoot              string
    Kind                     Kind // worktree or shell
    Key, Name, Path, Branch  string
    TaskID                   string
    Provider                 string
    PaneID, TmuxName         string
    Live, Ambiguous          bool
    Agent                    *agentstatus.Presentation
    ObservedAt               time.Time
}
```

Plain shells and worktrees without agents do not receive fabricated
`agentstatus` values. The list projection assigns them presentation buckets
such as `live shell` or `no session`; the Agents board filters to items with
supported durable/detected agent evidence and continues to use the shared
semantic reducer.

Keep the existing operational constraints:

- one global `tmux list-panes -a` per refresh cycle;
- one lightweight `git --no-optional-locks worktree list --porcelain` per
  de-duplicated configured project inventory refresh;
- bounded project and pane-capture concurrency;
- exact project/worktree/shell identities and collision refusal;
- incremental per-project results, last-good freshness, cancellation, and
  generation rejection; and
- no diff/stat/PR/CI/td/conversation work merely to populate the browser.

Inventory is shared between Agents and Workspaces. Switching tabs should reuse
fresh results and trackers, not launch two independent collectors. The visible
tab may request a projection-specific status/preview refresh, but project
inventory has one cache and one generation owner.

### Shared workspace-list component

Extract a presentation component such as `internal/workspacelist` rather than
copying `workspace.renderSidebarContent`:

- stable-ID selection and selection preservation;
- section/group definitions and row view models;
- filter input state plus the pure multi-field matcher;
- sort mode and stable ordering;
- viewport/scrollbar, constrained height, empty/loading/error rows;
- keyboard navigation and semantic selected/activated actions;
- mouse hover, click, double-click, and sort/filter hit regions; and
- rendering hooks for project subtitle and source-specific metadata.

The component receives resolved status and display fields. It does not know how
to inspect tmux, switch projects, create worktrees, or attach to terminals.

Adopt it in project Workspaces before or in the same slice that uses it in the
global browser. Characterization tests must preserve the existing shell-first
navigation, selection styles, scrollbar, mouse geometry, sidebar-width
persistence, and all project-owned commands when no filter is active.

### Shared two-pane and terminal preview presentation

Reuse the existing workspace split geometry and low-level terminal rendering,
but do not extract the whole Workspaces plugin. A narrow shared presentation
layer should own:

- sidebar/preview width calculation and drag geometry;
- focused/unfocused panel rendering and constrained height;
- terminal header, ANSI-safe truncation, background fill, scrollbar, native
  cursor suppression for read-only captures, and empty/unavailable messages;
- a `PreviewSource`-shaped seam that returns immutable selected-pane snapshots.

Project Workspaces adapts its existing live `tty.OutputBuffer` and terminal
surface. Global Workspaces uses a read-only selected-pane source. The global
source captures only the selected unambiguous pane, starts asynchronously after
selection settles, rejects stale selection generations, and polls only while
the global Workspaces tab is visible and the app is eligible. It must not start
one control-mode client, output buffer, watcher, or workspace plugin per
project.

Start with a conservative selected-preview cadence and measure it. A selection
change may trigger an immediate capture; an unchanged visible live pane may
refresh near the existing visible workspace cadence; hidden, unfocused, stale,
and unavailable previews stop or slow down. Captured terminal contents remain
in memory and are never persisted or included in diagnostics.

### Actions and navigation

Both global tabs activate the same app-owned validated navigation command.
Resolve an item by stable `ProjectKey + Kind + Key`; never by display name,
branch, tmux title, or current list index.

Global Workspaces exposes only `Open`, `Filter`, `Sort`, `Refresh`, and pane
navigation/scroll commands. Project Workspaces keeps its full command set.
This boundary prevents a convenient global browser from becoming a second,
divergent implementation of destructive workspace behavior.

## Layout behavior

The global Workspaces wide layout mirrors the familiar project screen while
making scope explicit:

```text
┌ Workspaces ─────────────── Activity ┐ ┌ sidecar / modal look and feel ─────┐
│ / filter…                           │ │ codex · working · changed 2m        │
│ NEEDS ATTENTION  1                  │ │ branch modal-look-and-feel          │
│   modal look and feel               │ │                                    │
│   sidecar · codex · working         │ │ [read-only selected pane output]    │
│ WORKING  2                          │ │                                    │
│   kanban scrolling polish           │ │                                    │
│   sidecar · shell · live            │ │                                    │
│ NO SESSION  3                       │ │                                    │
└─────────────────────────────────────┘ └────────────────────────────────────┘
```

The project name is always textual, not merely a hue. Rows should remain two
lines where that materially improves scanning, degrading to one ANSI-safe
truncated line at narrow sidebar widths.

At widths that cannot sustain two useful panes, render a full-width list first.
Right/Enter opens a full-width read-only preview; Escape/left returns to the
list. Do not shrink both panes into unreadable columns. Header and unified
footer remain visible at every supported size.

## Implementation slices

### Slice 0 — Align the current baseline

1. Finish or explicitly resolve the current in-review Overview work before
   changing the same Kanban/model seams (`td-71de3d`, `td-3ca6f1`, and
   `td-847b0c` at this research snapshot).
2. Characterize current app entry/exit, header tabs, project-switcher behavior,
   Overview selection/navigation, project workspace sidebar navigation, pane
   geometry, and terminal preview behavior.
3. Record the exact current wide/narrow screenshots and keyboard/mouse
   transcript on isolated tmux and state paths.
4. Independently review the baseline tests. This slice changes no product
   behavior.

### Slice 1 — Make global/project scope visible

1. Add typed app scope and typed global tab state while preserving the current
   project/plugin underneath.
2. Render `[Agents] [Workspaces]` in global scope and project plugin tabs only
   in project scope. Add scope-aware hit regions, numeric/cycle routing, footer,
   help, and narrow-header behavior.
3. Make `K`, `q`, and the Sidecar brand restore the remembered project
   destination; keep `@` and destination-name click as the explicit switcher.
4. Keep the Workspaces global tab as an honest loading/placeholder view. Prove
   scope transitions do not call `registry.Reinit` or start cross-project I/O
   before Overview is entered.

This is the navigation steel thread. It ships only if the existing Agents tab
is unchanged and returning to a project is exact.

### Slice 2 — Shared catalog, list, filter, and sort

1. Extend the read-only inventory to return all durable shells and Git
   worktrees while retaining an agent-only projection for Agents.
2. Extract the shared list/filter/sort component and adapt project Workspaces
   to it without changing its non-filtered journey.
3. Add `/` filtering to project Workspaces with a dedicated text-input context,
   paste handling, mouse focus, counts, no-match state, and stable selection.
4. Render global Workspaces incrementally using the same component, with
   project subtitles, Activity grouping, four sort modes, failures, freshness,
   scrollbar, and narrow list behavior.
5. Prove switching Agents/Workspaces reuses one inventory/cache and does not
   duplicate tmux/Git fan-out.

### Slice 3 — Read-only selected terminal preview

1. Extract/reuse the two-pane geometry and low-level terminal presentation
   needed by both consumers; keep Diff, Task, attach, and lifecycle code
   project-owned.
2. Add the global selected-pane preview source with generation cancellation,
   immediate selected capture, adaptive visible polling, and no persistence.
3. Add focus navigation, preview scrolling, mouse/wheel behavior, unavailable
   metadata states, and the narrow full-screen preview transition.
4. Measure selection latency, capture count, event-loop responsiveness, and
   idle/unfocused work before tuning cadence.

### Slice 4 — Exact navigation and integrated polish

1. Route Enter/double-click through the existing validation and pending
   selection path for worktrees, agent shells, and plain shells.
2. Handle disappeared projects/items, ambiguous panes, duplicate names,
   linked worktrees, and same-project navigation without selecting a neighbor
   or sending terminal input.
3. Preserve each global tab's filter/sort/selection/scroll state while toggling
   spaces during the process lifetime.
4. Complete wide/narrow, mouse/keyboard, theme, Nerd Font fallback, footer, and
   first-frame/startup proof.
5. Independently review the integrated candidate and fix findings before the
   feature is considered complete.

## Likely file map

| Area | Responsibility |
| --- | --- |
| `internal/app/model.go`, `view.go`, `update.go` | Typed scope/global tabs, scope-aware header, shortcuts, mouse regions, destination restoration |
| `internal/overview/` | Global tab model, shared catalog ownership, Agents and Workspaces projections, selected preview lifecycle |
| `internal/workspaceinventory/` | Read-only all-workspace inventory, pane correlation, cache/freshness, validation |
| `internal/workspacelist/` (new) | Stable list model, filter matcher/input, sort/group, rendering, keyboard/mouse, tests |
| `internal/kanban/`, `internal/agentstatus/` | Existing shared Agents semantics and board; no new status path |
| `internal/plugins/workspace/view_list.go`, `keys.go`, `mouse.go`, `commands.go` | Adopt shared list/filter behavior while retaining project actions |
| `internal/plugins/workspace/view_preview.go`, `pane_geometry.go`, `internal/tty/` | Extract only shared split/terminal presentation and immutable preview source boundary |
| `internal/keymap/bindings.go` | Global-tab and filter contexts; audit collisions before assigning defaults |
| `internal/styles/` | Scope-tab, project subtitle, group heading, and preview states using existing theme tokens where possible |
| `scripts/tmux-drive.sh`, headless guide | Multi-project all-workspace fixture and isolated real-app proof |

## Verification

### Automated behavior

- global/project scope transitions preserve exact workdir, project root,
  project plugin, workspace selection, and plugin lifecycle counts;
- scope-specific tabs, clicks, cycling, numeric shortcuts, help, footer hints,
  and narrow header truncation never route to hidden tabs;
- no Overview collector work occurs before entry; switching global tabs shares
  inventory rather than duplicating project/tmux operations;
- inventory includes every Git worktree and durable shell, distinguishes plain
  sessions from agent activity, de-duplicates linked/configured roots, refuses
  collisions, and never writes manifests/config/state;
- Agents projection retains the full shared semantic matrix and excludes only
  genuinely non-agent items;
- Activity/Project/Recent/Name sorts are stable, preserve selection by ID, and
  do not churn on unchanged polls;
- the shared filter matches every promised field, handles Unicode/case safely,
  clears/exits predictably, consumes keys/pastes only while focused, and behaves
  identically in project and global lists;
- project Workspaces retains creation, deletion, attach, interactive, Diff,
  Task, sidebar resizing, shell-first navigation, and Kanban toggle behavior;
- global Workspaces exposes no mutating/interactive command path;
- selected preview rejects stale captures, captures only the selected pane,
  pauses/slows when hidden or unfocused, constrains output width/height, and
  handles empty/missing/ambiguous panes;
- mouse regions follow rendered geometry for tabs, groups, rows, filter, sort,
  divider, preview, scrollbars, click, and double-click; and
- Enter/double-click validates and navigates to the exact project/workspace,
  including duplicate display names, without attach or terminal input.

Run focused package tests first, then `go test ./...`, `go vet ./...`,
`go build ./...`, and `git diff --check`. Use `GOWORK=off` gates as required by
the current canonical release/testing baseline.

### Real app proof

Before every proof, run `./scripts/tmux-drive.sh paths` and confirm both the
tmux server and Sidecar config/state/manifest roots are isolated. Never stop,
restart, replace, or clean the default tmux server.

The fixture should contain:

- at least three configured projects and one linked worktree outside its main
  project root;
- duplicate shell/worktree display names in different projects;
- agent items in Needs attention, Working, Done, Idle, and Paused states;
- a live plain shell, an untyped shell that becomes an identified agent, a
  worktree with no agent/session, a main worktree, an ambiguous pane case, and
  one missing/non-Git project;
- enough items in multiple groups to scroll; and
- a feature/task name shared across name, branch, and task fields for filter
  proof.

Capture text, PNG, and retained key/mouse transcripts for:

1. startup into project space with no pre-entry Overview work;
2. `K`/brand entry into Agents and exact return to the prior project plugin;
3. global tab keyboard/click switching with only global tabs visible;
4. incremental all-workspace loading, status grouping, all four sorts, and
   wide/narrow layouts;
5. `/` filtering in global and project Workspaces, including paste, arrows,
   no-match, clear, and selection preservation;
6. arrowing across projects while the right preview follows the exact selected
   pane without writing input;
7. hidden/unfocused preview polling and subprocess/capture counts;
8. Enter/double-click into a duplicate-named non-current worktree and plain
   shell, proving exact project Workspaces selection and no auto-attach; and
9. return to Overview with each tab's in-memory view state intact.

Compare isolated config, state, manifests, tmux sessions, and repositories
before/after. The global journey may update only deliberately scoped ephemeral
test state; browsing itself must be read-only.

## Acceptance criteria

1. Overview is visibly a global space with its own Agents and Workspaces top
   navigation; project plugin tabs appear only in project space.
2. The user can toggle global/project space with one action and recover the
   exact prior destination without project reinitialization.
3. Global Workspaces shows every configured project's shells and Git worktrees,
   including plain/no-session entries, with textual project identity and honest
   live/agent/freshness state.
4. Activity grouping is a faithful vertical projection of shared Kanban
   semantics, with stable Project/Recent/Name alternatives.
5. Arrow navigation updates a bounded read-only selected terminal preview; no
   global browse path sends terminal input or mutates workspace state.
6. `/` filtering is fast, keyboard-safe, and shared with project Workspaces,
   matching names, projects, branches, tasks, providers, and statuses.
7. Enter/double-click opens the exact owning project Workspaces item through
   validated stable identity and never auto-attaches.
8. Agents and Workspaces share one read-only, bounded, incremental,
   cancellable inventory/cache with one tmux inventory per refresh cycle.
9. Existing project Workspaces behavior and current Agents Kanban behavior do
   not regress, including narrow layouts and header/footer constraints.
10. Automated gates, behavior-faithful isolated real-app proof, and independent
    review pass before completion.

## Explicitly deferred

- cross-project creation, rename, delete, attach, terminal input, or other
  mutating bulk actions;
- cloning the project Workspaces Diff and Task preview tabs into global space;
- persisted filters, sort modes, selection, or launching directly into global
  Workspaces until real use demonstrates value;
- task/PR/CI/conversation aggregation or search that requires new slow adapters;
- background OS notifications or durable attention history;
- filesystem discovery beyond configured projects;
- silently choosing among multiple plausible panes; and
- a Sidecar CLI/API/MCP for data and capabilities still owned by Git, tmux, and
  agent harnesses.

## Non-blocking product choices to validate in the steel thread

The plan recommends these defaults and does not need to pause implementation
unless real proof contradicts them:

- keep the global destination name **Overview** rather than introducing
  `Global` as another noun;
- name the tabs **Agents** and **Workspaces** rather than `Kanban` and `List`,
  because the labels describe the content rather than its current rendering;
- include all durable workspaces, with no-session rows demoted to the bottom,
  rather than hiding them behind a default Live filter; and
- use `/` to enter filtering rather than stealing printable project Workspace
  commands for type-to-filter.

If the all-workspace fixture proves the no-session tail overwhelms active work,
the first adjustment should be an explicit `All / Live` projection control
that preserves the same catalog and selection model, not a different collector
or hidden heuristic.
