# Plan: Drag-to-reorder shells and worktrees in Workspaces sidebar

## Goal

Let users drag rows in the Workspaces plugin sidebar to reorder **within** a section:

- Reorder **shells** among other shells
- Reorder **worktrees** among other worktrees

**Hard rule:** a shell cannot be dropped into the worktrees section (or vice versa). Sections stay separate; only relative order inside each list changes.

Out of scope for this plan: keyboard reorder bindings, kanban card drag, plugin-tab reorder, cross-project overview cards.

---

## Current behavior (baseline)

| Concern | Shells | Worktrees |
|--------|--------|-----------|
| In-memory list | `Plugin.shells` | `Plugin.worktrees` |
| Display order source | `shells.json` array order | `git worktree list` (main first) |
| User reorder today | None | None |
| Hit region | `regionWorktreeItem` with **negative** index (`-(i+1)`) | same region with **absolute** index `i` |
| Click | selects on press | selects on press |
| Double-click | attach/recreate shell | attach agent if present |
| Drag today | not used on rows | not used on rows |

Key files:

- `internal/plugins/workspace/view_list.go` — sidebar render + hit rects
- `internal/plugins/workspace/mouse.go` — click/drag handlers
- `internal/plugins/workspace/shell_manifest.go` / `shell_merge.go` — shell order truth
- `internal/plugins/workspace/update.go` — `RefreshDoneMsg` replaces `p.worktrees`
- `internal/state/state.go` — `WorkspaceState` (selection only today)
- Closest gesture template: **filebrowser drag-to-move** (`internal/plugins/filebrowser/mouse.go`) — arm → 2-cell threshold → active → drop, with edge auto-scroll

There is no generic list-reorder utility in `internal/mouse`. That package already exposes `StartDrag`, `ActionDrag`/`ActionDragEnd`, `DragStartID`, and drop `Region` under the cursor. Reorder logic lives in the workspace plugin, modeled on filebrowser.

---

## Product rules

1. **Sections are closed.** Drop targets only resolve to rows of the **same kind** as the drag source. Hovering the other section, headers, separator, `+` buttons, empty space, or the preview pane is an invalid drop (no-op on release; optional subtle “can’t drop” cue).
2. **Click stays instant.** Press still selects the row (current behavior). Drag arms on the same press; motion under a small threshold does **not** reorder.
3. **Double-click still attaches.** Threshold + double-click timing already coexist in filebrowser; do not start a committed reorder until threshold is crossed.
4. **Order is durable.**
   - Shells: persist in `shells.json` (array order is already the UI order).
   - Worktrees: new persisted ordered ID list; reapplied after every git refresh.
5. **Main worktree is free to move.** Default order when no saved preference exists remains git/main-first. After the user reorders, that order wins (including placing main anywhere).
6. **New items.** New shells append (today’s `AddShell`). New/unknown worktrees append after known ordered keys on the next refresh. Deleted IDs drop from the saved order list.
7. **Kanban inherits list order.** Shells lane and within-lane worktree order already follow `p.shells` / `p.worktrees`; no separate kanban order store.

---

## UX

### Gesture

```
press row  → select + arm drag (StartDrag)
motion     → if |dx|≥2 or |dy|≥2, promote to active reorder
active     → insertion index follows cursor within same section
release    → if active and insert index valid & different, commit + persist
             else: no order change (plain click)
cancel     → Esc / button-less motion / modal open / plugin blur: clear drag
```

Mirror filebrowser constants (`dragThresholdDX/DY = 2`) unless testing shows 2-line items need a slightly larger vertical threshold.

### Visual feedback (list mode only)

While reorder is active:

- **Source row** dimmed (reuse or add a small style akin to `FileBrowserDragSource`)
- **Insertion bar** (1-row line / accent underline) at the gap before the drop index — better for reorder than filebrowser’s “onto folder” highlight
- Optional status fragment in the existing toast/status path: `reorder · Shell 2` (keep short; no second footer)

Do **not** live-permute the underlying slice every motion event in a way that fights hit-map rebuilds; prefer preview-via-insert-index and commit on release. (If live preview is easy and stable, fine — insert-index preview is the safer default.)

### Scrolling

- Worktrees already window with `scrollOffset` / `visibleCount`. During active reorder over the worktree section, **edge auto-scroll** when the pointer is near the top/bottom of the worktree viewport (copy filebrowser’s throttled auto-scroll).
- Shells are fully painted (no window). No shell auto-scroll required unless shell lists become windowed later.

### Cross-section

If the drag starts on a shell and the cursor is over a worktree row (or the reverse): treat as **invalid drop** — keep insert index at “none”, do not swap kinds, do not animate a forbidden insertion bar in the foreign section.

---

## Design

### A. Pure reorder helpers (test-first core)

Add small pure functions (new file e.g. `internal/plugins/workspace/reorder.go`):

```go
// MoveIndex reorders a slice conceptually: from -> to (insert index semantics).
func MoveIndex(n, from, to int) (newFrom, newTo int, ok bool)

// ApplyOrder sorts items so known IDs appear in orderIDs sequence;
// unknown IDs keep relative order and append after known ones.
func ApplyOrder[T any](items []T, orderIDs []string, id func(T) string) []T

// ReorderShellDefinitions reorders only the named subsequence inside the full
// manifest, preserving positions of unrelated (sibling workDir) entries.
func ReorderShellDefinitions(defs []ShellDefinition, visibleOrder []string) []ShellDefinition
```

Shell manifest nuance: `shells.json` is **shared across worktrees** of a project; each instance only **displays** shells matching `shellDiscoveryPattern(workDir)`. Reorder must change the relative order of the **visible** `tmuxName`s without inventing a second file or clobbering siblings. Implementation approach:

1. Walk full `defs`; collect indices of entries whose `tmuxName` is in the visible set being reordered.
2. Rewrite those slots with the new visible order.
3. Leave every non-visible entry exactly where it was.

This keeps merge rules (`mergeShellState`, `TestMergePreservesManifestOrder`) honest: array order remains the single source of truth.

### B. Shell persistence

Extend `ShellManifest` with a locked mutation, same pattern as `AddShell` / `RemoveShell`:

```go
func (m *ShellManifest) ReorderVisibleShells(orderedTmuxNames []string) error
```

- Uses `mutateLocked` (re-read under lock, merge, write) so cross-instance races do not clobber renames.
- No-op if order already matches.
- After success, local `p.shells` is already in the desired order; watcher may fire — `mergeShellState` should re-read the same order and not reshuffle.

### C. Worktree persistence

Git has no display-order concept. Store order in project workspace state:

```go
// internal/state/state.go — WorkspaceState
type WorkspaceState struct {
    WorkspaceName     string            `json:"workspaceName,omitempty"`
    ShellTmuxName     string            `json:"shellTmuxName,omitempty"`
    ShellDisplayNames map[string]string `json:"shellDisplayNames,omitempty"`
    WorktreeOrder     []string          `json:"worktreeOrder,omitempty"` // IdentityKey sequence
}
```

Notes:

- Keyed the same way as today’s selection (`SetWorkspaceState(workdir, …)`). Prefer **main repo / project root** as the map key if selection is already workdir-scoped inconsistently — **match existing `saveSelectionState` keying** so one worktree switch does not fork multiple orders unexpectedly. Audit `saveSelectionState` / `GetWorkspaceState` call sites during implement; if selection is per-workdir, order should use the same key (likely main repo path is better long-term — document choice in the PR).
- Identity: use `Worktree.IdentityKey()` (stable across refresh; already used for selection rebind in `RefreshDoneMsg`).
- On `RefreshDoneMsg` after assigning `p.worktrees = msg.Worktrees`, call `p.worktrees = ApplyOrder(p.worktrees, savedOrder, IdentityKey)` then rebind `selectedIdx` by key (already does key rebind — run ApplyOrder **before** rebind).
- On successful reorder: rewrite `WorktreeOrder` from the new `p.worktrees` keys and `SetWorkspaceState`.
- Prune: when saving, only include keys still present; when applying, drop missing keys from the saved slice.

**Default (no saved order):** keep current git/main-first list unchanged.

### D. Plugin drag state

On `Plugin`:

```go
type listDragKind int // none, shell, worktree

dragArmed, dragActive bool
dragKind              listDragKind
dragFromIdx           int   // index in p.shells or p.worktrees
dragInsertIdx         int   // destination insert index within same list; -1 invalid
dragSourceID          string // TmuxName or IdentityKey (stable if list mutates mid-drag)
// optional: last auto-scroll time for worktree edge scroll
```

Region: keep using `regionWorktreeItem` and the existing negative/non-negative `Data` encoding so click/double-click paths stay intact. Optionally introduce `regionShellItem` later for clarity; not required for v1 if decoding stays centralized.

### E. Mouse integration (`mouse.go`)

**Click (`regionWorktreeItem`):** after existing select logic:

```go
p.armListReorder(action) // sets kind/from/sourceID, StartDrag(x,y, regionWorktreeItem, fromIdx)
```

**Drag:**

```go
case regionWorktreeItem (via DragStartID):
    if sub-threshold && !dragActive { return }
    dragActive = true
    resolve insert index from action.Region + Y within row (upper half = before, lower half = after)
    reject if target kind != dragKind
    edge-scroll worktrees if needed
```

**Drag end:**

```go
if !dragActive { clear; return } // plain click already selected
if invalid insert or from == to after normalize { clear; return }
apply MoveIndex on p.shells or p.worktrees
update selectedShellIdx / selectedIdx to follow the moved item
persist (ReorderVisibleShells or WorktreeOrder)
clear drag state
```

**Conflict with existing drag end:** `handleMouseDragEnd` currently always persists sidebar width on the default branch. Gate list-reorder end **before** that default so a shell reorder does not write sidebar width or resize panes.

**Modals / interactive mode:** existing guards already swallow background drags when modals are open. Do not arm reorder when not in list/kanban sidebar context; in interactive mode a sidebar click already exits interactive — arming after that is fine.

### F. Render integration (`view_list.go`)

When `dragActive`:

- Dim source row in `renderShellEntryForSession` / `renderWorktreeItem`
- Draw insertion indicator at the Y of the gap corresponding to `dragInsertIdx` (account for headers, separator, scrollOffset, 2-line item height)

Hit-map registration stays absolute indices (already correct for worktrees).

### G. Refresh / create / delete interactions

| Event | Behavior |
|-------|----------|
| `RefreshDoneMsg` | Apply `WorktreeOrder`; rebind selection by key |
| Shell watcher / merge | Manifest order already applied; no extra sort |
| Create shell | Append (unchanged); order list grows at end |
| Create worktree | Append or appear via refresh; if not in `WorktreeOrder`, append |
| Delete shell/worktree | Existing removal; next save prunes order IDs |
| Project / worktree switch | Load that project’s state order as today for selection |

---

## Implementation steps

1. **Pure helpers + unit tests** (`reorder.go`, `reorder_test.go`)
   - MoveIndex edge cases (0, last, no-op, out of range)
   - ApplyOrder with missing/extra IDs
   - ReorderShellDefinitions with interleaved sibling entries
2. **ShellManifest.ReorderVisibleShells** + manifest tests (lock/mutate path)
3. **WorkspaceState.WorktreeOrder** field + getter/setter usage via existing `SetWorkspaceState` (or thin helpers if cleaner)
4. **Apply order on RefreshDoneMsg** + restore selection by IdentityKey (existing path)
5. **Plugin drag state + mouse arm/drag/end** with default-branch conflict fix in `handleMouseDragEnd`
6. **Visuals** in `view_list.go` (dim + insert bar)
7. **Worktree edge auto-scroll** during active reorder
8. **Mouse/gesture tests** (workspace `mouse_test.go` or `reorder_mouse_test.go`)
   - click without motion → no reorder
   - sub-threshold → no reorder
   - shell reorder commit + manifest order
   - worktree reorder commit + state order
   - cross-section drop → no change
   - selection index follows moved item
9. **Manual proof** via isolated `tmux-drive` if feasible (mouse is hard headless — unit tests carry the contract; manual smoke in a real terminal for insert-bar feel)
10. **Docs touch** (short): keyboard-shortcuts skill or UI notes only if we document the gesture; changelog entry when shipping

---

## Testing plan

| Layer | Coverage |
|-------|----------|
| Pure | MoveIndex, ApplyOrder, ReorderShellDefinitions |
| Manifest | Reorder under lock; sibling entries preserved; no-op write skip |
| State | WorktreeOrder round-trip in WorkspaceState |
| Update | RefreshDoneMsg reorders to saved keys; new wt appends |
| Mouse | arm/threshold/commit/cancel/cross-section invalid |
| Regression | existing shell merge order tests; selection restore; double-click attach still works |

Run: `go test ./internal/plugins/workspace/ ./internal/state/ ./internal/mouse/`

---

## Risks and mitigations

| Risk | Mitigation |
|------|------------|
| Cross-instance shell reorder races | `mutateLocked` re-read; only rewrite relative visible order |
| Refresh wipes worktree order | ApplyOrder on every successful refresh |
| Click becomes sluggish / steals double-click | 2-cell threshold; commit only when `dragActive` |
| `handleMouseDragEnd` side effects | Explicit branch for list reorder before sidebar width persist |
| 2-line rows make “half row” insert awkward | Use mid-Y of the 2-line hit rect |
| Shared shells.json confuses users who expect per-worktree shell order | Document that project shells share one ordered list; visibility filter remains per workDir |
| Main pinned expectation | Default still main-first; free move after first user reorder |

---

## Non-goals / follow-ups

- Keyboard (`K`/`J` with modifier, or dedicated move keys) — easy once MoveIndex exists
- Drag reorder in kanban columns
- Reordering plugin tabs in the app header
- Generic `internal/mouse` reorder widget (extract only if a second list needs it)
- Changing shell visibility rules or multi-project overview ordering

---

## Success criteria

- User can drag a shell between shell neighbors; order survives restart and matches `shells.json`.
- User can drag a worktree between worktree neighbors; order survives restart and survives `RefreshDoneMsg`.
- Dragging a shell over the worktrees block never inserts it there (and the reverse).
- Single click still selects; double-click still attaches.
- No regression to pane divider drag, terminal text selection, or modal mouse guards.

---

## Suggested PR shape

1. **PR 1 — model:** pure reorder + shell manifest reorder + WorktreeOrder apply on refresh (no mouse UX)
2. **PR 2 — interaction:** arm/threshold/drag-end + visuals + tests

Ship together if preferred; split keeps review focused on persistence correctness first.
