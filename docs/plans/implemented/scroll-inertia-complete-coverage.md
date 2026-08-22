# Plan: Complete inertial wheel boundary coverage

**Research snapshot:** 2026-08-14  
**Status:** proposed  
**Tracking:** `td-bcfe53` (planning); implementation should get its own epic and
behavior-sized child tasks.  
**Builds on:** commit `5b3f6173` / `td-8d89d0` and [Files plugin: Apple Mouse scroll stall](files-plugin-scroll-stall.md).

## Decision first

Every Sidecar-owned bounded wheel surface must be able to answer one read-only question before Bubble Tea calls `Update` and `View`:

> Would this exact wheel event change the surface currently under the pointer?

If the answer is certainly no, `app.FilterInput` drops the event. If Sidecar does not own the scroll state, cannot identify the target, may load more data, or is forwarding to a mouse-reporting terminal application, it returns **unknown/movable** and the event continues normally.

Extend the boundary contract already introduced by `5b3f6173`; do not add a second debounce queue, timer, goroutine, or global rate limiter. Boundary filtering is the only available Bubble Tea v2 hook that prevents both `Update` and the mandatory repaint. Coalescing inside a component can reduce mutation work but cannot prevent an event backlog from repainting the whole app.

The implementation should make boundary ownership compositional:

```text
tea.WithFilter(app.FilterInput)
                │
                ▼
       active input owner?
       ├─ app overlay ──────────────► overlay boundary
       ├─ global Workspaces ────────► overview boundary
       ├─ global Tasks ─────────────► embedded Tasks adapter boundary
       └─ active project plugin ────► plugin boundary
                                      ├─ plugin modal
                                      ├─ bounded Sidecar pane
                                      └─ terminal/application-owned: unknown
```

The current blanket modal exclusions must go away only after the active modal can give an exact answer. The safe default remains forwarding, never guessing.

## User journey and failure mode

A Magic Mouse or trackpad flick emits dozens to hundreds of wheel messages, including a long inertial tail after a list or document has reached its edge. Bubble Tea renders after every accepted message. A surface that clamps its offset during `Update` is visually correct but still causes a full Sidecar layout and terminal repaint for every no-op tail event. That can make Sidecar appear frozen until the queue drains.

The completed journey is:

1. Flick over any Sidecar list, document, diff, preview, modal, or embedded TUI.
2. The surface moves immediately and preserves its existing wheel semantics.
3. Once it reaches a known boundary, remaining same-direction inertia is discarded before `Update` and `View`.
4. The first reverse-direction event passes immediately.
5. A surface with more data to load continues receiving the event that can trigger that load.
6. A terminal application with mouse reporting receives its wheel stream unchanged; local terminal scrollback is filtered only at a proven live or exhausted-history edge.

No default tmux server lifecycle change is part of this work. Real-app proof must isolate both the tmux server and Sidecar state tree.

## Current contract and the hole in it

Commit `5b3f6173` added:

- `tea.WithFilter(app.FilterInput)` at the program entry point;
- `plugin.WheelBoundaryConsumer` for a read-only pre-update answer;
- shared `scroll.Bounds` movement/boundary rules;
- coverage for Files, Git, project Workspaces, global Workspaces, documents, issue panes, diffs/tasks inside workspace previews, and Sidecar-owned terminal scrollback; and
- conservative forwarding for terminal applications that own mouse input or scrollback whose older history is not yet known to be exhausted.

Two deliberate escape hatches now define the remaining risk:

1. `internal/app/input_filter.go` returns false for **every** app modal.
2. Implemented plugins return false in modal/edit/search modes, while plugins without `WheelBoundaryConsumer` are always forwarded.

Clamping in `internal/modal`, `issueview`, list renderers, textareas, or embedded models does not solve the freeze. It happens after the filter and therefore still incurs `Update` plus `View`.

## Complete inventory

This inventory is based on production wheel dispatch (`MouseWheelMsg` and `ActionScrollUp/Down`), active-modal routing, every implementation of `WheelBoundaryConsumer`, and embedded Bubble Tea adapters. Keyboard-only scrolling is not an inertia source and is listed only where it shares bounds that the wheel contract must reuse.

### A. Covered today; preserve and regression-test

| Surface | Boundary owner today | Important exception |
| --- | --- | --- |
| Files tree and file preview | `filebrowser.Plugin.WheelAtBoundary` | Inline editor and overlays forward |
| Git status tree, diff pane, commit preview, full diff | `gitstatus.Plugin.WheelAtBoundary` | Search/no-repo/modal modes forward |
| Project Workspaces sidebar, docs/issues, diffs, commit files, watched/live terminal scrollback | `workspace.Plugin.WheelAtBoundary` | Plugin modal modes forward; pane mouse reporting forwards |
| Global Workspaces sidebar, doc/issue tabs, diff/task tabs, terminal preview | `overview.WorkspacesWheelAtBoundary` | Rename/view flyouts forward; pane mouse reporting forwards |
| Shared document and issue components when hosted in workspace panes | `docview/issueview.ScrollAtBoundary` through the host | App issue-preview modal does not ask it |

These are not “done forever.” Their tests must join a shared coverage table so new view modes and hit regions cannot silently fall back to unfiltered input.

### B. App-level overlays: all currently bypass the filter

`Model.activeModal()` selects exactly one of these owners, but `wheelAtBoundary` sees only `hasModal()` and forwards unconditionally.

| Active overlay | Actual wheel behavior | Gap / boundary source |
| --- | --- | --- |
| Command palette | Wheel moves filtered command cursor by 3 | Cursor vs filtered entry count; outside-modal events are absorbed and are known no-ops |
| Help | Declarative modal body scroll | Shared modal content bounds |
| Update preview / complete / error | Declarative modal body scroll | Shared modal content bounds |
| Update changelog | Host-owned `changelogScrollOffset`, synchronized into modal state | Rendered changelog line count and viewport; current downward offset is render-clamped only |
| Diagnostics | Declarative modal body scroll | Shared modal content bounds |
| Quit confirm | Declarative modal, normally not scrollable | Empty/short modal is bounded in both directions |
| Project switcher | Wheel moves cursor, ensures visibility, clears/rebuilds modal, and previews theme | Cursor vs filtered projects; this is high priority because boundary inertia still rebuilds and previews |
| Worktree switcher | Wheel/list behavior through its modal host | Filtered worktree cursor/list bounds plus modal body if applicable |
| Theme switcher | Wheel moves cursor and previews theme | Cursor vs filtered themes; high priority because no-op tails can repeat expensive visual work |
| Open In picker | Wheel moves cursor/list | Cursor vs available filtered apps |
| Issue lookup | Search-result cursor and viewport | Cursor vs current search results; loading/empty results are bounded |
| td issue preview | Host intercepts every wheel and calls `issueview.Scroll(±3)` | `issuePreviewView.ScrollAtBoundary`; this is the reported journey |

The project-add flow is nested under the project switcher state rather than a separate `ModalKind`; its declarative modal body must be queried as the active sub-owner. The changelog is likewise a nested overlay inside the update modal and must take precedence over the update dialog underneath it.

### C. Shared declarative modal library and plugin modal hosts

`internal/modal.Modal` handles wheel input for `modal-body`, but it stores only `scrollOffset` and `lastViewportH`; `maxScroll` is local to `buildLayout`. Consequently a caller cannot ask whether a wheel is a no-op. It also increments downward offsets before render and relies on the next layout pass to clamp them.

Add an exact, render-derived boundary query to the modal itself. It must:

- use the most recently built layout (`scrollOffset`, viewport height, and cached maximum scroll);
- identify whether the pointer is over the modal body using the same hit map as `HandleMouse`;
- return bounded for wheel over backdrop/non-scrollable chrome because the modal absorbs those events;
- return unknown before the first trustworthy render or after content/size invalidation; and
- expose the query without rebuilding or mutating visible content.

Once that primitive exists, inventory and wire every plugin modal host:

| Host | Modal families currently forwarded |
| --- | --- |
| Workspaces | doc info; create/setup/result; task link; rename shell/worktree; delete shell/worktree; type selector; agent choice/config; fetch PR; merge; commit-for-merge |
| Files | exit confirmation; project search; quick open; info; blame; file operations; inline-editor overlays |
| Git | commit; branch picker; pull/push; conflict; stash/discard confirmations; error dialogs; history/path filtering overlays |
| Notes | info, delete, and task-link modals; exit confirmation |
| td monitor | Sidecar setup modal plus embedded td-owned modals |

Short confirmation dialogs still matter: their entire wheel stream is a known no-op and currently repaints Sidecar. Custom sections with their own scroll state must answer from that child first; the containing modal body answer is valid only when the body owns the wheel.

### D. Sidecar plugin/global surfaces with no boundary consumer

| Surface | Wheel behavior and current cost | Required exact bound |
| --- | --- | --- |
| Notes list | Moves cursor and calls `loadNoteIntoEditor`, even after clamp paths | Cursor vs displayed notes; avoid invoking note load when unchanged |
| Notes markdown preview | Mutates `previewScrollOff`; bottom clamps during rendering | Extract exact rendered maximum from wrapped preview lines and pane height |
| Notes textarea edit | Synthesizes three up/down key events into the textarea | Use textarea cursor/viewport semantics only if its API exposes an exact answer; otherwise keep unknown, but cache/no-op suppression should be investigated locally |
| Notes inline tmux editor | Forwards wheel to the embedded terminal/editor | Application-owned; always unknown/forward unless the shared tty model proves local ownership |
| Overview activity board | Wheel moves the row within the pointed kanban column | Selected row vs cards in that column; empty columns are bounded |
| Command palette | Listed with app overlays but implemented in `internal/palette` | Add a palette boundary method rather than reproducing its filtering rules in app |

### E. Explicit first-pass exclusion: deprecated Conversations plugin

The Conversations plugin is deprecated, default-off, and likely to be removed. Its sidebar, turn/message panes, detail view, and resume-session modal accept wheel input without implementing `WheelBoundaryConsumer`, but they are explicitly **out of scope for the first pass**. Do not change or test those paths as part of this plan, and do not let their current behavior block the completion gate.

If Conversations is undeprecated rather than removed, update this inventory and plan before implementation resumes for that plugin. The re-entry work must cover its cursor-driven sidebar and turn/message panes, exact rendered bounds for `detailScroll`, the lazy `hasMoreSessions` exception, and its resume modal. Until then, “complete coverage” in this plan means complete coverage across all supported, non-deprecated scrolling surfaces.

### F. Embedded TUI seams

| Surface | Current routing | Plan |
| --- | --- | --- |
| Project `td` monitor | Sidecar forwards wheel into `td/pkg/monitor.Model.Update`; monitor owns panel/modal/list state | Add a small read-only wheel-boundary contract to the embedded monitor API, implemented beside its existing mouse routing and `ScrollOffset`/panel bounds. Sidecar's adapter translates coordinates and delegates. Keep setup modal on Sidecar's modal contract. |
| Project Tasks plugin | Sidecar translates panel coordinates and forwards to embedded Tasks model | Add/consume an equivalent read-only boundary method in Tasks' embedding API. It must report unknown for Tasks-owned overlays until those overlays implement exact bounds. |
| Global Tasks | Same Tasks plugin instance selected through `globalTasksPlugin()` | The same adapter contract covers it; explicitly test global routing because `app.FilterInput` takes a different branch. |

Do not inspect or duplicate private embedded-model state from Sidecar. The boundary rule belongs beside the component that owns its cursor, filters, panels, pagination, and modal precedence. Dependency releases/updates must be ordered: upstream `td`/Tasks contract and tests, then Sidecar dependency bump and adapter proof. If an upstream change cannot land with the first Sidecar slice, keep forwarding those embedded surfaces; do not approximate their bounds.

### G. Deliberate non-filtering and non-wheel cases

- A tmux pane whose application enabled mouse reporting owns the wheel stream.
- Local terminal scrollback with potentially older unloaded history is not at its top boundary until tmux confirms exhaustion.
- A pagination/list surface that can load more data is not at its bottom boundary until exhaustion is known.
- Horizontal wheel actions are outside this vertical-inertia plan unless live measurement shows the same queue failure; do not accidentally drop them.
- Keyboard navigation, drag motion, and hover have separate performance paths. They may share bounds but are not routed through this wheel filter.
- Pointer coordinates with stale/unbuilt hit regions are unknown and forward.

## Target design

### 1. One tri-state meaning, even if the Go API stays boolean

The existing boolean contract is safe because `true` means “certain no-op” and `false` combines movable and unknown. Preserve that semantic. Rename only if a review finds the current name repeatedly encourages callers to guess.

Every implementation follows this order:

1. Resolve the active input owner using the same precedence as `Update`.
2. Resolve the region using the hit map produced by the last `View`.
3. Ask the component that owns that region's scroll state.
4. Return true only when applying the signed delta cannot change state or trigger a legitimate load/forward.
5. Reset gesture-only `WheelBurst` state when dropping a tail, as covered surfaces already do; do not mutate visible selection/content.

### 2. Shared bounds, not duplicated arithmetic

For each surface, extract one maximum/position calculation used by:

- wheel movement;
- keyboard movement where semantics match;
- renderer clamping; and
- the pre-update boundary query.

Use `internal/scroll.Bounds` for ordinary row offsets/cursors. Add narrowly named helpers for wrapped markdown, filtered indices, lazy pagination, and terminal history. Do not calculate a plausible maximum independently in the filter; geometry drift can turn a performance optimization into broken scroll.

### 3. Modal composition

Add a read-only modal query along the lines of:

```go
func (m *Modal) WheelAtBoundary(msg tea.MouseWheelMsg, h *mouse.Handler) bool
```

The exact signature may change during implementation, but the library must own body/backdrop geometry and cached layout bounds. App/plugin hosts own precedence between a modal and a custom child:

```text
issue preview modal
  pointer over issue custom section -> issueview.ScrollAtBoundary
  pointer over absorbed modal chrome -> modal.WheelAtBoundary

update modal
  changelog visible -> changelog bounds
  otherwise -> active update dialog modal bounds
```

Cursor-driven picker modals should expose host/component queries because their wheel changes selection rather than the modal body's `scrollOffset`.

### 4. Cheap proof that filtering happened before render

State assertions alone cannot distinguish “clamped during Update” from “dropped before Update.” Add instrumentation-friendly tests using a model/plugin whose `Update` and `View` counters are observable, or test `FilterInput` returning nil directly for each owner class. At least one integration test must feed a large inertial tail through a real `tea.Program` and show that accepted updates and renders stop at the boundary.

Avoid permanent production telemetry unless measurement proves it useful. A test-only counter or opt-in diagnostic is sufficient.

## Dependency-ordered implementation plan

### Phase 0 — Lock the contract and coverage ledger

1. Turn the inventory above into a table-driven test ledger of active owner, pointer region, direction, expected bounded/movable/unknown result.
2. Add negative tests first: modal open must never query the covered plugin; terminal mouse reporting and lazy/unexhausted history must never be dropped.
3. Add a regression test proving the current td issue-preview bottom inertia reaches `Update`; it should flip when Phase 2 lands.
4. Record a repeatable stress fixture: hundreds of same-direction wheel events, then one reverse event. It must not use sleeps.

Deliverable: no behavior change, but omissions become visible and each later slice has an explicit row to turn green.

### Phase 1 — Make shared modal bounds queryable

1. Cache `lastMaxScroll` and layout validity in `internal/modal.Modal` during `buildLayout`; invalidate it when sections or size/content assumptions change.
2. Clamp movement through `scroll.Bounds` instead of intentionally overshooting until render.
3. Implement the read-only pointer-aware modal boundary query.
4. Test top, bottom, short/empty content, body vs backdrop/control, resize, rebuilt async content, focus auto-scroll, and first render.
5. Wire simple declarative app and plugin confirmation/error/info dialogs first.

This is the steel thread: a long Help/Diagnostics modal drops an inertial tail, while a short confirmation drops all absorbed wheel events, through the real app filter.

### Phase 2 — Complete app-level overlay precedence

1. Replace `if m.hasModal() { return false }` with `activeModalWheelAtBoundary(msg)` using the same `activeModal()` precedence as `Update` and `View`.
2. Delegate simple overlays to the modal primitive.
3. Add exact cursor bounds to palette, project/worktree/theme switchers, Open In, and issue lookup.
4. Add nested precedence for project-add and update changelog.
5. Route td issue preview to `issueview.ScrollAtBoundary` when its custom viewport owns the pointer.
6. Ensure boundary events do not clear/rebuild picker modals, preview themes, or synchronize changelog state.

Acceptance: every `ModalKind` has an explicit ledger row and test; no blanket modal bypass remains.

### Phase 3 — Complete native Sidecar plugin surfaces

Implement `WheelBoundaryConsumer` for:

1. Notes list and markdown preview, with exact renderer-shared bounds and no `loadNoteIntoEditor` call when the cursor cannot move.
2. Overview activity-board columns.
3. Plugin-hosted declarative modals in Workspaces, Files, Git, and Notes, using their existing modal precedence before ordinary panes.

Keep textarea/inline-terminal paths unknown until their owner exposes a proven boundary. This phase must not add guesses based only on cursor position when wrapped content or an internal viewport can still move.

Do not modify the deprecated Conversations plugin in this phase. Its explicit exclusion remains in force unless the product decision changes and this plan is updated first.

### Phase 4 — Add embedded `td` and Tasks contracts

1. In `td`, define and test a read-only embedded-monitor boundary method beside its mouse router. Cover list panels, detail/preview scroll, filters, modal precedence, pagination, and empty/loading states.
2. Release/update Sidecar's `td` dependency and implement delegation in `tdmonitor.Plugin.WheelAtBoundary`, after translating the same coordinates used by `Update`.
3. In Tasks, add the equivalent public embedding contract and tests.
4. Release/update Sidecar's Tasks dependency and delegate from `tasks.Plugin.WheelAtBoundary` for both project and global hosts.
5. Run external-consumer/import-boundary checks so Sidecar depends only on the intended public APIs, not checkout layout or private fields.

Do not block native Sidecar coverage on these upstream releases. Until each contract is available, its adapter remains conservatively unfiltered.

### Phase 5 — Make completeness durable

1. Add compile-time assertions for every Sidecar plugin that handles wheel input and is expected to implement `WheelBoundaryConsumer`. The registry must record Conversations as a named deprecated exclusion rather than silently overlooking it.
2. Add a repository test or small explicit registry that fails when a new `ModalKind`, wheel-handling plugin, or modal host lacks a declared boundary policy. Prefer an explicit auditable registry over brittle source-code grep.
3. Document the rule in `ui-features`: every new scrollable surface/modal must provide exact pre-update bounds or explicitly declare why it is unknown.
4. Update the old Files scroll-stall plan to point to the implemented global contract or move it to `docs/plans/implemented` when its remaining promises are satisfied.

## Verification strategy

### Focused automated checks

- `internal/scroll`: signed movement, clamping, empty/negative maxima.
- `internal/modal`: cached bounds and pointer ownership across content, focus, resize, async rebuild, and short dialogs.
- `internal/app`: every `ModalKind`, nested overlays, local-coordinate translation, covered-plugin exclusion, issue preview, global Tasks branch.
- Each native plugin: every pane/region and direction at top, middle, bottom; lazy-load and terminal-owned exceptions; reverse event after boundary.
- Embedded adapters: coordinate translation and faithful propagation of the upstream answer.
- Stress test: hundreds of boundary events produce zero updates/renders after the boundary, while the first reverse event produces one.

Use deterministic clocks for any retained `WheelBurst` tests. Do not make timing assertions with `time.Sleep`.

### Real-app proof

Use `scripts/tmux-drive.sh` only after:

```bash
./scripts/tmux-drive.sh paths
```

Confirm both the tmux socket and all config/state paths are isolated. Never launch a proof against the default tmux server or real Sidecar state.

Capture at least these journeys at realistic terminal sizes:

1. Long td issue preview: hard flick to bottom, immediate reverse, close modal.
2. Long Help or Diagnostics modal and a short confirmation dialog.
3. Project/theme switcher at both edges, confirming no repeated preview work.
4. Notes list and long markdown preview.
5. Project `td`, project Tasks, and global Tasks after upstream contracts land.
6. Workspace terminal with local scrollback, unloaded older history, exhausted history, and a mouse-reporting application.

`capture-pane` cannot prove native terminal cursor placement, but this work is about event-loop responsiveness and scroll position. Retain a timestamped event or render-count artifact plus screen captures showing the landing position and first reverse movement. A human Magic Mouse/trackpad pass is the final tactile check; scripted wheel bursts remain the repeatable regression proof.

### Integrated gates

After each independent slice and on the final integrated candidate:

```bash
go test ./...
go test -race -p=1 ./...
go build ./...
make lint
git diff --check
```

Use focused package tests during development. Re-run broad gates only when integration, dependencies, or shared contracts change; a handoff alone does not invalidate traceable results.

## Review and completion gates

- Each implementation slice gets independent review before approval; the final integrated candidate gets a separate cross-surface review focused on false positives that could swallow legitimate input.
- Review compares boundary queries to the actual `Update` routing and rendered geometry, not just to similarly named offsets.
- No surface is marked covered because it clamps correctly after `Update`.
- No known bounded Sidecar wheel surface remains absent from the ledger.
- Unknown cases are documented with their owner and the evidence needed to make them filterable later.
- The main tmux server and real Sidecar state are untouched by proof.
- Completed, verified, independently reviewed work is committed in focused dependency order; nothing is pushed unless explicitly requested.

## Risks and guardrails

| Risk | Guardrail |
| --- | --- |
| Dropping a legitimate first scroll | Return true only for a certain no-op; stale/unrendered geometry is unknown |
| Hiding pagination/load-more | Exhaustion is part of the boundary contract |
| Breaking terminal apps | Mouse-reporting/application-owned wheel always forwards |
| Modal/body geometry drift | Modal library owns its cached bounds and hit regions; hosts own only precedence/custom children |
| Duplicated max-scroll arithmetic | Movement, render clamp, and boundary share one helper |
| New surface silently regresses | Explicit registry/ledger and compile-time assertions |
| Deprecated Conversations accidentally expands the first pass | Keep it as an explicit registry exclusion; add it only after an undeprecation decision and plan update |
| Cross-repo dependency stalls | Ship native Sidecar phases independently; never approximate embedded private state |

## Definition of done

A hard Magic Mouse or trackpad flick can no longer leave Sidecar repainting a stationary owned surface. Every vertical wheel route is explicitly classified as covered, legitimately movable/loadable, or externally owned/unknown; all Sidecar-owned bounded paths drop their inertial tails before `Update` and `View`; reverse motion and terminal/application input remain immediate; the inventory is enforced by tests; isolated real-app proof is retained; and all substantive code has independent review.
