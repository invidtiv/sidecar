# Unified focus system: Tab cycles windows, click focuses everywhere

## Context

In both the global workspace (Workspaces tab, `internal/overview`) and the project workspace (`internal/plugins/workspace`), Tab does not move focus between all visible windows: the global overview has no Tab handling at all, and in the project workspace the hand-written cycler (`cycleDocumentFocus`, `doc_panes.go:1235-1284`) hard-resets `termPanelFocused`, making the terminal panel unreachable. Click-to-focus exists but is five hand-wired mouse arms each mutating fragmented focus state (`activePane` + `paneFocus` + `termPanelFocused` on the project side; `previewFocus` + `paneFocus` + `doc.focused`/`issue.focused` on the global side).

Desired behavior: **Tab / Shift+Tab cycle focus across all visible windows** (sidebar, terminal(s), doc/files pane, TD issue pane); **click focuses whatever was clicked**, defocusing the previous window; **exception:** a terminal in interactive (typing) mode captures Tab. The solution must generalize as the windowing system grows more splits.

This aligns with `docs/plans/active/workspace-windowing-system.md` (§3.2 Tab row, §3.3 precedence ladder, §3.5 mouse item 4) — this slice builds the ring/setter seam that doc wants, without fighting its M0/M1 milestones.

## Design

- **One pure focus ring** in `internal/panelayout` (shared by both surfaces already). A focus target is sidebar | tree leaf (by ID) | terminal panel (transitional). Ring order = sidebar, then leaves in placement order (A-then-B walk of `layoutNode`, matching `LayoutPanes`), then terminal panel.
- **One `setFocusTarget` per surface** — the *only* writer of focus state. Keyboard cycling and every mouse click route through it.
- **Terminal panel joins the ring now** via a synthetic `TargetTermPanel` entry. When windowing-plan M1 absorbs the panel as a tree leaf, that enum case and the ring argument are single-site deletions — a bridge, not a second system.
- **Ring scope = windows currently on screen.** Output tab: sidebar + all tree leaves + panel. Diff/Task tabs: sidebar + one preview target; `workspacediff.Focus` (file-list↔diff) stays intra-window (Tab never did that; `h`/`l`/`enter` do — and doc M3 turns diff into a leaf, joining the ring for free). Kanban: board ↔ preview, as list view.
- **Interactive exception is structural, not coded**: project `ViewModeInteractive` dispatches to `handleInteractiveKeys` (`keys.go:50-51`) before list keys; global `PreviewInteractive()` short-circuits at `workspaces.go:389-391`. Tab handling lands after both. Verified.

## Work items (ordered)

### 1. Pure ring — new `internal/panelayout/focusring.go`

```go
type TargetKind int
const (
    TargetSidebar TargetKind = iota
    TargetLeaf
    TargetTermPanel // deleted when windowing M1 absorbs the panel as a leaf
)
type Target struct { Kind TargetKind; Leaf int }

// Ring: sidebar (when visible), leaves in placement order, panel (when visible).
func Ring(root *Node, sidebarVisible, termPanelVisible bool) []Target
// CycleTarget wraps; unknown current → first (last if reverse); empty ring → current.
func CycleTarget(ring []Target, current Target, reverse bool) Target
```

State-free, table-testable, no bubbletea.

### 2. Project setter — new `internal/plugins/workspace/focus.go`

```go
func (p *Plugin) focusRing() []panelayout.Target
func (p *Plugin) currentFocusTarget() panelayout.Target
func (p *Plugin) setFocusTarget(t panelayout.Target) // sole writer of activePane/paneFocus/termPanelFocused
func (p *Plugin) cyclePaneFocus(reverse bool)
```

- `focusRing()`: Output tab → `panelayout.Ring(p.paneRoot, sidebarVisible, termPanelVisible)`; Diff/Task → `[sidebar, leaf(terminalLeafID)]` (matches today's two-state toggle).
- `setFocusTarget`: sidebar → `activePane=PaneSidebar`, clear panel flag; leaf → `activePane=PanePreview`, `paneFocus=t.Leaf`, clear panel flag (doc/issue focus is already *derived* from `paneFocus` — no bool sync needed); panel → `activePane=PanePreview`, `paneFocus=terminalLeafID`, `termPanelFocused=true`, plus `thawTermPanelWindow()` (mirrors the existing click arm — without this, panel focus arrives frozen).

### 3. Project keyboard rewiring

- `keys.go:571-576`: replace the `contentLeafIDs()>0` guard + `cycleDocumentFocus` with unconditional `p.cyclePaneFocus(reverse)` — this is what makes the panel Tab-reachable.
- Delete fallback two-state tab case at `keys.go:987-993` (dead).
- Delete `cycleDocumentFocus` (`doc_panes.go:1235-1284`).
- `issue_panes.go:186-190` (issue leaf declines Tab) stays; update its comment.

### 4. Global overview setter + Tab

- New `internal/overview/focus.go` mirroring Step 2 (no panel entry). `setFocusTarget` sidebar path returns `m.focusList()`; leaf path is a new `focusPreviewLeaf(leafID)` that generalizes `focusPreviewPane` (`preview_links.go:372-388`) — refactor `focusPreviewPane(kind)` into a thin wrapper (`FirstOfKind` + `focusPreviewLeaf`) so the doc/issue focused-bool sync has one body.
- Tab dispatch in `WorkspacesKey` (`workspaces.go:384`): insert after the `viewFlyoutOpen` branch, **before** the filter branch — after `PreviewInteractive()` (terminal exception) and so Tab moves focus even mid-query. When leaving a focused filter, `Filter().Blur()` (keeps the query). *If Tab-leaves-filter feels wrong in dogfooding, moving the branch below the filter check is one line.*

### 5. Keymap — `internal/keymap/bindings.go`

Add `tab`/`shift+tab` → `switch-pane` for contexts `global-workspaces`, `global-workspaces-doc`, `global-workspaces-issue` (mirroring project entries at `:492-493`, `:530-531`). Project contexts unchanged.

### 6. Mouse — one focus writer + first `regionPaneLeaf` step

- Project (`mouse.go:697-860`): each arm's focus-mutation lines become `setFocusTarget(...)` — `regionSidebar`/`regionListFilter`/`regionWorktreeItem` → sidebar; `regionPreviewPane` → leaf(terminal); `regionTermPanelContent` → panel (subsumes its thaw line); `regionDocPane`/`regionIssuePane` → leaf(leafID). All other per-arm logic (link activation, gesture prep) stays.
- Merge `regionDocPane` + `regionIssuePane` into one `regionPaneLeaf` (both already carry leaf ID as `Data`; registration at `doc_panes.go:1326`, `issue_panes.go:246`), arm switches on `FindPane(...).Kind`. Terminal regions keep their kind-string gesture arbitration until M1 (deliberately short of full §3.5 collapse).
- Overview: `focusPreviewPane` call sites in `preview_links.go`/`preview_issue.go` → `setFocusTarget`; sidebar region already calls `focusList()`. Press-leaves-terminal rule (`workspaces.go:668-687`) untouched.

## Deletions

| What | Where |
|---|---|
| `cycleDocumentFocus` | `doc_panes.go:1235-1284` |
| Fallback tab toggle | `keys.go:987-993` |
| `regionDocPane`/`regionIssuePane` constants (→ `regionPaneLeaf`) | workspace region consts + mouse arms |
| Direct focus-state writes in mouse arms | `mouse.go:697-860` |
| `focusPreviewPane` bool-sync body (→ wrapper) | `preview_links.go:372-388` |

Not deleted: ~40 `termPanelFocused` lifecycle *clears* (panel toggle/tab switch) — those go with M1.

## Verification

**Must pass unedited:** `issue_panes_test.go` `TestTabCyclesThroughTheIssueLeaf` (load-bearing ring-order compatibility check), `doc_panes_test.go`, `panetree_test.go`, `pane_tree_geometry_test.go`, both `interaction_parity_test.go`, `terminal_window_parity_test.go`, `internal/app/key_precedence_test.go`, golden transcripts in `internal/app/testdata/` (no rendered-byte change in this slice).

**New tests:**
- `panelayout/focusring_test.go` — table-driven: ring order == `LayoutPanes` placement order asserted against a real layout (guards drift); sidebar/panel visibility; wrap both directions; current-not-in-ring; empty ring.
- Project `focus_test.go` (reuse `docPaneTestPlugin` fixture, `doc_panes_test.go:24-45`): full cycle sidebar→terminal→doc→issue→panel→sidebar and reverse (**the panel-reachability regression test**); bare-terminal + sidebar toggle; Diff tab leaves `diffTabFocus` untouched; interactive-mode Tab doesn't change focus target; click each region → focus follows, previous loses it.
- Overview: same cycle table over mirrored tree; Tab blurs filter keeping query; Tab forwarded while `PreviewInteractive()`; previewOnly/listOnly arrangements.
- **Parity test** in each `interaction_parity_test.go`: same tree on both surfaces, N Tabs, identical target-kind sequences, sidebar + interactive exception included.

**Real-app proof** on a private tmux socket (per workspace TestMain discipline): open a workspace with doc + issue + panel visible, Tab around the full ring, click each window, enter interactive mode and confirm Tab reaches the shell.

## Risks

1. **Tab-leaves-filter on overview** — behavior change from "swallowed mid-query"; Blur keeps the query, one-line revert available.
2. **Panel thaw side-effect** — setter must reproduce `thawTermPanelWindow` or panel focus arrives frozen; covered by cycle test + `terminal_surface_test.go`.
3. **Ring/placement order drift** — asserted in `focusring_test.go` so a future layout change fails loudly.
4. **M1 collision** — only `TargetTermPanel` + the ring's panel argument touch M1 ground; both single-site deletions, commented as such.
