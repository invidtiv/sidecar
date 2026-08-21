# TD issue tabs in project and global Workspaces

Landed on `workspace-tabs` as `td-489182` (commits `6d7b539`–`9685ea3`). Follow-up: `td-aa3855` (hidden snapshot can stale live document tabs on relaunch).

Click a `td-…` link beside a terminal and the issue opens in an issue pane. Click another issue and it becomes a second tab instead of replacing the first. The same journey works in project Workspaces and global Workspaces, including mouse selection of a drawn tab.

This is tracked by `td-489182`. It deliberately reuses the tab consolidation already implied by `td-210cb8` (Phase 8 of [workspace-doc-pane-tabs.md](../active/workspace-doc-pane-tabs.md)); issue tabs must not become a third tab model or renderer.

---

## 1. What exists today (verified against current `main`)

### 1.1 Files and file panes have two tab implementations

Project and global file panes already share:

- `docview.Tabs` for ordered `*docview.Model` values, active index, dedup, close, cycle, and overflow range;
- `docview.LayoutTabStrip` for drawing a left-truncated path strip and returning the exact hit geometry; and
- host-specific click payloads (`docTabHit` and `previewDocTabHit`) that add pane/workspace identity without copying layout math.

The Files plugin still has its own `FileTab`, `activeTab`, open/close/cycle logic, overflow window, `styles.RenderTab` loop, and `tabHit` geometry in `internal/plugins/filebrowser/tabs.go`. Its extra state is real: preview tabs, inline-editor sessions, cached preview results, and tree selection are Files host concerns. The ordered-group and strip behavior are duplicates.

`td-f98f10` records a concrete bug in Files' duplicate close logic when deleting a directory removes several tabs. That bug should be fixed before extraction, then its cases should become invariants of the shared group.

### 1.2 TD issues share one viewer but have no tab group

`internal/issueview.Model` is already the shared one-issue component. It owns fetch identity, rendering, scroll, parent/subtask selection, mouse-local hit geometry, and yank data.

Each host wraps one model:

- project: `workspace.issuePane.view`;
- global: `overview.previewIssue.view`.

A second terminal-link activation calls `Load` on that same model, so it erases the previous issue. Parent/subtask activation also retargets the model.

Both hosts independently draw a title chip, a close chip, and `q close`; both independently register the body and close regions. Neither header uses the shared file-tab strip.

### 1.3 The two contexts have different lifetime rules

Project Workspaces persists each pane-tree leaf per terminal surface. Documents already persist a list plus active index. An issue leaf persists only legacy scalar fields (`Issue`, `Scroll`) and restores one model.

Global Workspaces keeps preview panes in memory per selected workspace. File tabs are memory-only there, and issue state should follow that rule rather than adding a new durable store.

---

## 2. Intended journey

1. In project Workspaces, click `td-111111` in shell A. An issue pane opens and focuses its first tab.
2. Click `td-222222`. It appends and becomes active. The first tab remains open.
3. Click the first tab. Its issue body and scroll position return immediately; the click does not reach the terminal or a neighboring pane.
4. Use `{` / `}` to cycle. Use `x` to close only the active issue. Closing the last tab removes the issue pane.
5. Use `q` / `esc` in project Workspaces to hide the issue pane while retaining its tabs for that surface, matching project file-pane semantics. Switch to shell B and back or relaunch on shell A: A's tab set, active tab, and each tab's scroll are restored.
6. Repeat the open, append/focus, click, cycle, and close journey in global Workspaces. Its tab set survives row switches through the existing in-memory per-workspace cache but is not written to disk.
7. Activate a parent or subtask from an issue card. It goes through the same open-or-focus operation: an already-open issue is focused; a new issue is appended. Navigation never creates duplicate IDs or silently destroys the issue the user was reading.

The header is only the tab strip. The footer remains the source of keyboard hints.

---

## 3. Design

### 3.1 Extract the shared tab foundation before adding issue tabs

Split the current Phase 8 concept into two dependency steps:

1. **Shared tab foundation (no Phase 7 dependency).** Extract the ordered group and tab-strip layout used by project/global file panes and Files. Move only behavior with multiple current consumers.
2. **Files hosts the full shared viewer (`td-210cb8`).** Keep its existing Phase 7 dependency for search, image, selection, and the rest of the viewer-field cleanup.

TD issue tabs depend on step 1, not on the full viewer migration. This lands the DRY boundary early without coupling issue rendering to file rendering.

The target shape is a content-neutral package (use `internal/tabs` unless the implementation reveals a clearer existing home):

```go
type Group[T any] struct {
    Items  []Item[T]
    Active int
}

type Item[T any] struct {
    Key     string
    Value   T
    Preview bool
}

type Label struct {
    Text    string
    Preview bool
}

func LayoutStrip(labels []Label, active, width int, focused bool,
    fit FitLabel) Strip
```

Exact names may change, but the ownership must not:

| Shared tabs package | Content/host owns |
|---|---|
| ordered items and active-index invariants | `docview.Model` / `issueview.Model` |
| find by stable key, append/focus, select, cycle, close | file path or issue-ID normalization |
| visible overflow window | loading and async result routing |
| `styles.RenderTab` composition | Files preview/pinned policy and inline-editor sessions |
| label fitting and returned hit placements | pane-tree leaf/workspace identity in mouse payloads |

The group must support closing an arbitrary matching set in one operation so the corrected `td-f98f10` behavior does not regress. It returns enough information for a host to clean up removed values and load the newly active one; it does not know about tmux or file deletion.

Do not generalize Output/Diff/Task chips into this package. They are a fixed view switch, not an open-resource tab group, and have different layout and lifecycle rules.

### 3.2 Migrate existing file consumers onto the foundation

Move `docview.Tabs` and `docview.LayoutTabStrip` onto the shared group/strip, leaving compatibility aliases or thin file-label helpers only while callers migrate. Then replace Files' duplicate active-index, cycle, visible-range, render, and hit-placement logic.

Files retains a host-owned value around the shared item for:

- ephemeral preview vs pinned state;
- cached file result and scroll until Phase 8 moves those into `fileview`;
- inline-editor session metadata and cleanup; and
- tree-selection synchronization.

Files supplies basename or parent/basename candidates. Workspace file panes supply relative paths with left truncation. Same group and strip, deliberately different labels.

This extraction is complete only when the old range/layout algorithm is gone from `filebrowser/tabs.go`, not merely wrapped by another type.

### 3.3 Add an issue tab group above `issueview.Model`

One issue tab contains one `*issueview.Model`, so scroll, selection, loading, and rendered data naturally stay per tab. Its stable key is the trimmed, validated TD issue ID. The group provides:

- `OpenOrFocus(issueID)`: focus an existing key or append a fresh model;
- `Select(index)` and `Cycle(delta)`;
- `CloseActive()`; and
- access to the active model for render, keys, mouse, and yank.

Put issue-specific orchestration next to `issueview`, not in either host. In particular, parent/subtask activation should emit an issue-open intent (or call a supplied open callback) instead of privately retargeting its own model. Both hosts route that intent through `OpenOrFocus`, wrap the resulting load with their existing surface/epoch identity, and deliver it to the model that asked.

Every async load keeps a unique model ID or equivalent tab identity. A result for a closed tab, an old generation, a different project epoch, or another global workspace is ignored. Reusing the current constant global model ID for several live tabs is not safe.

### 3.4 One issue-tab strip, two host payloads

Both contexts call the same `LayoutStrip` result for drawing and hit-region registration. Candidate text is `issueview.Model.Title()`:

- before load it is the issue ID;
- after load it is the ID plus headline;
- issue labels truncate at the end so the stable ID remains visible; and
- leftover width goes to the active tab, as it does for file tabs.

Project click data includes `{LeafID, Index}` so a tab cannot target another pane leaf. Global click data contains the index and is already scoped by the active cached workspace. General pane/body regions are registered first; specific tab regions are registered last.

There is no close chip and no in-header `q close`. Mouse close is not added as a special per-tab target in this slice; click selects, `x` closes, and the footer advertises the action. This matches current file-pane tabs.

### 3.5 Keyboard and close semantics

Add the same resource-tab commands to `workspace-issue` and `global-workspaces-issue`:

| Key | Project Workspaces | Global Workspaces |
|---|---|---|
| `{` / `}` | previous / next issue tab | previous / next issue tab |
| `x` | close active; last tab forgets pane | close active; last tab closes pane |
| `q` / `esc` | hide pane and retain tabs for surface | close pane and forget in-memory set |
| `enter` | open/focus selected parent or subtask as a tab | same |
| `y` / `Y` | yank active issue / ID | same |
| `tab` / `shift+tab` | existing pane focus ring | existing global focus behavior |

Project hide behavior must be the same distinction files already use: `q` retains, last `x` forgets. Global follows its file-pane rule and remains memory-only. Issue focus continues to consume unowned keys so nothing types through to the terminal.

Update `Commands()`, keymap registration, and default bindings together. Footer names stay short (`Tab×`, `Tab←`, `Tab→`).

### 3.6 Persist project tabs, migrate the scalar shape

Give an issue leaf a list from now on:

```go
type PaneIssueTabJSON struct {
    Issue  string `json:"issue"`
    Scroll int    `json:"scroll,omitempty"`
}

type PaneLayoutJSON struct {
    // existing fields
    IssueTabs []PaneIssueTabJSON `json:"issueTabs,omitempty"`
    Active    int                `json:"active,omitempty"`

    // legacy read-only fields
    Issue  string `json:"issue,omitempty"`
    Scroll int    `json:"scroll,omitempty"`
}
```

Encode every validated tab plus active index. Decode the active model eagerly and create the remaining models lazily; selecting an unloaded tab fetches it. An invalid legacy or list entry is dropped without discarding the rest of the pane tree. If all entries are invalid, collapse that issue leaf and return its box to its sibling.

On read, treat legacy `Issue` + `Scroll` as a one-tab list. Stop writing the legacy fields after the first save. No database migration or cached issue body is needed; restore re-fetches TD and restores only the per-tab scroll target.

Extend the existing hidden-layout predicate beyond document tabs so a project issue-only layout can be hidden and reopened. Opening an issue while hidden restores that surface's remembered pane tree, then focuses/appends the ID.

### 3.7 Keep global tabs in the existing per-workspace cache

Replace `previewIssue.view` with the shared issue group. The surrounding `previewIssue` still carries root, workspace surface, focus, epoch, and model-ID allocation. `paneCache[workspaceID]` already provides the lifetime boundary: switching rows restores that workspace's in-memory issue tabs; application restart does not.

Loaded-message routing must first resolve the workspace cache entry and then the specific tab/model identity. Closing or switching a tab must not make a late result land on whichever tab is now active.

---

## 4. Phases

Each phase is independently reviewable. Focused tests stay with the phase that introduces the behavior; later handoffs reuse that evidence unless integration changes invalidate it.

### Phase 0 — repair and characterize current Files tabs

Complete `td-f98f10`. Add invariant tests for removing tabs before/at/after the active tab, removing several tabs, removing all tabs, cleaning values, and activating/loading the correct survivor. This provides a known-correct source before extraction.

### Phase 1 — shared tab group and strip

Extract the content-neutral group and strip. Migrate project/global file panes and Files to it. Preserve:

- file open/focus, preview/pin, close, cycle, and persistence behavior;
- Files duplicate-basename labels and workspace left-truncated paths;
- overflow markers and active-tab visibility;
- mouse regions derived from the strip actually rendered; and
- both Nerd Font and fallback tab rendering.

This phase is the reusable-tab portion of `td-210cb8`, but does not wait on Phase 7 of the file-viewer plan. Update `td-210cb8` to depend on this phase and retain its later viewer-host cleanup scope.

### Phase 2 — project issue tabs steel thread

Make `issuePane` host the shared issue group. Terminal-link activation appends or focuses. Render the shared strip; bind `x`, `{`, and `}`; route title clicks through `{LeafID, Index}`. Parent/subtask activation uses the same open/focus path. Keep existing issue body mouse behavior, yank, scroll, focus ring, pane placement, and terminal geometry intact.

Acceptance: open two issue links in one project shell, click and key-cycle between them, see independent scroll, focus an existing ID without duplication, and close one without closing the pane.

### Phase 3 — project persistence and hide/forget

Write `IssueTabs` + active index, migrate the scalar legacy shape, lazy-load inactive tabs, and make `q` hide while last `x` forgets. Prove shell A / shell B isolation, switch-away-and-back, relaunch restore, invalid-entry pruning, stale loads, and an issue-only hidden layout.

### Phase 4 — global issue tabs

Move global `previewIssue` to the same issue group and strip. Add click regions, keys, open/focus parent/subtask behavior, per-workspace cache routing, and close semantics. Do not add disk persistence.

Acceptance: open two issues in a global shell/worktree row, click and cycle, switch rows and back, and confirm the original row's in-memory set returns.

### Phase 5 — documentation and real-app proof

Update keyboard-shortcut guidance and user-facing Workspaces docs. Run focused tests, `go test ./...`, and `go build ./...`, then use `scripts/tmux-drive.sh` after checking `paths` to prove project and global journeys with isolated tmux and Sidecar state. Never touch the default tmux server.

Proof must click visible filename/issue-ID cells by coordinates; resolving a region and then clicking that same region is circular and cannot detect overlap/order bugs. Cover narrow overflow, neighboring Output/Diff/Task chips, the document leaf stacked with the issue leaf, scroll-wheel routing, and closing the active tab while a load is in flight.

---

## 5. Completion gates

- One shared ordered tab group and one shared strip/overflow implementation are used by Files, project/global file panes, and project/global issue panes.
- No host keeps a parallel active index or recomputes strip hit geometry.
- Files-only preview/edit/tree behavior remains outside the shared package.
- Project issue tabs persist per surface with legacy scalar migration; global issue tabs remain memory-only per workspace.
- Keyboard, mouse, focus, close/hide, async-load, narrow-width, and restore behavior have focused coverage.
- The integrated candidate passes focused tests, `go test ./...`, `go build ./...`, isolated real-app proof, and independent review.
