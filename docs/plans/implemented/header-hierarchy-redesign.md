# Plan: Header hierarchy redesign — global fleet tabs, brand anchor, and pinned project selector

**Status:** agreed — ready to implement.  
**Reference Mockup:** [`~/code/tui/mockups/header-redesign.tui.yaml`](file:///Users/marcus/code/tui/mockups/header-redesign.tui.yaml) (Live preview on `http://aerie.local:3333/?file=header-redesign.tui.yaml`)  
**Scope:** Sidecar header rendering (`internal/app/view.go`, `internal/app/scope.go`, `internal/styles/`, mouse hit regions).

---

## 1. Problem Statement

Sidecar recently introduced a global workspace layer (cross-project shells, agent fleet monitoring, and tasks), but for new users, it is virtually invisible:
1. **Hidden Entry Point**: Global scope is only reachable via the `K` shortcut or clicking the static "Sidecar" brand text, with zero visual affordance.
2. **Naming Collision**: Both global and project scopes contain a tab called `workspaces`, causing confusion.
3. **Ambiguous Hierarchy**: The mental model of "Global Fleet Layer" sitting above "Project Scope" is not reflected in the top bar.

---

## 2. Declarative Specification

The header maintains a **single physical row** with a 1-row spacer below (`headerHeight = 2`), divided into two stable anchor zones:

```
┌───────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┐
│ ◱ Sidecar │  Sessions   Activity   Tasks                              td   Git   Files   Workspaces   Notes    Vibes ▾            │
└───────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┘
  ▲             ▲                                                        ▲                                       ▲
  │             └─ Global Navigation (Always Left)                      └─ Project Tool Tabs                    └─ Far-Right Project
  └─ Brand Glyph & Anchor                                                  (Visible in Project Scope)              Selector (Fixed)
```

### 2.1 Top-Left Anchor & Brand
- **Brand Glyph + Name**: `◱ Sidecar` styled in cyan accent (`#38bdf8` / `styles.Primary` or `styles.Accent`).
- **Divider**: Subtle vertical rule `│` (`styles.Muted` / `styles.Subtitle`).
- **Click Behavior**: Clicking `◱ Sidecar` toggles global fleet scope or focuses the primary `Sessions` tab.

### 2.2 Global Navigation Tabs (Always Left-Aligned)
The global tabs are always positioned on the left side of the header bar, immediately following the brand divider:

1. **`Sessions`** (formerly *Global Workspaces*): Cross-project catalog of all terminal sessions, worktrees, and shells.
2. **`Activity`** (formerly *Agents*): Live agent execution streams, parallel tool runs, and status monitors.
3. **`Tasks`**: Global cross-project issue tracker / td backlog.

- **Tab Styling**: Rendered using standard tab styles (`styles.RenderTab`). In Global Scope, the active global tab receives the vibrant/gradient fill; in Project Scope, global tabs render in subtle inactive pill style (`[bg:#222228 fg:#a1a1aa]`).
- **Shortcuts**: In Global Scope, `1`, `2`, `3` switch between `Sessions`, `Activity`, and `Tasks`.

### 2.3 Far-Right Pinned Project Selector (Fixed Anchor)
The project switcher button is permanently pinned to the **far right edge** in both scopes to eliminate UI jitter:
- **In Project Scope**: `[ <ProjectName> ▾ ]` (e.g. `[ Vibes ▾ ]` or `[ sidecar ▾ ]` with git worktree indicator if applicable).
- **In Global Scope**: `[ Select Project ▾ ]`.
- **Click / Key Behavior**: Clicking the pill or pressing `@` opens the centered `Switch Project` fuzzy search modal (`ensureProjectSwitcherModal()`).

### 2.4 Project Tool Tabs (Right-Aligned, Preceding Project Selector)
When inside a project context, project plugins render immediately to the left of the project selector:
- **Capitalization**:
  - `td` (kept lowercase)
  - `Git` (capitalized)
  - `Files` (capitalized)
  - `Workspaces` (capitalized)
  - `Notes` (capitalized)
- **Active State**: The focused project plugin receives the vibrant active tab gradient/fill.
- **Global Scope Behavior**: When switching to Global Scope (`Sessions`, `Activity`, or `Tasks`), the project tool tabs collapse away, leaving only `[ Select Project ▾ ]` on the far right.

### 2.5 Clock Removal
- The header clock (`10:09`) is removed entirely to eliminate clutter and reclaim horizontal space on smaller viewports.

---

## 3. Scope State Matrix

### State A: In Project Scope (`Vibes`)
```
 ◱ Sidecar │  Sessions    Activity    Tasks                                                             td    Git    Files    Workspaces    Notes     Vibes ▾   
```
- Active Tab: `Workspaces` (or active plugin in project).
- Global tabs visible and 1-click reachable on Left.
- Project selector pinned to Far Right.

### State B: In Global Scope (`Sessions` Active)
```
 ◱ Sidecar │  Sessions    Activity    Tasks                                                                                                  Select Project ▾   
```
- Active Tab: `Sessions` (vibrant gradient pill).
- Far-right button says `Select Project ▾` at the exact same physical coordinates as `Vibes ▾`.

### State C: In Global Scope (`Activity` Active)
```
 ◱ Sidecar │  Sessions    Activity    Tasks                                                                                                  Select Project ▾   
```
- Active Tab: `Activity` (vibrant gradient pill).

### State D: In Global Scope (`Tasks` Active)
```
 ◱ Sidecar │  Sessions    Activity    Tasks                                                                                                  Select Project ▾   
```
- Active Tab: `Tasks` (vibrant gradient pill).

---

## 4. Affected Files & Implementation Steps

| Component | Target File | Description |
|---|---|---|
| **Scope Enums & Naming** | [`internal/app/scope.go`](file:///Users/marcus/code/sidecar/internal/app/scope.go) | Rename `GlobalWorkspaces` -> `GlobalSessions` (`"Sessions"`), `GlobalAgents` -> `GlobalActivity` (`"Activity"`). Retain `GlobalTasks` (`"Tasks"`). Update state persistence keys with backwards-compatibility for `workspaces`/`agents`. |
| **Header Layout & Rendering** | [`internal/app/view.go`](file:///Users/marcus/code/sidecar/internal/app/view.go) | Refactor `headerLayout()` and `renderHeader()`: Left cluster (`◱ Sidecar │ Sessions Activity Tasks`), Right cluster (`td Git Files Workspaces Notes Project ▾`). Remove clock rendering logic. |
| **Plugin Tab Names** | [`internal/plugins/*/plugin.go`](file:///Users/marcus/code/sidecar/internal/plugins/) | Capitalize display names returned by plugins (`Git`, `Files`, `Workspaces`, `Notes`), keeping `td` lowercase. |
| **Hit Regions & Mouse Routing** | [`internal/app/view.go`](file:///Users/marcus/code/sidecar/internal/app/view.go), [`internal/app/mouse.go`](file:///Users/marcus/code/sidecar/internal/app/mouse.go) | Update `getLogoBounds()`, `getTabBounds()`, and add `getProjectSelectorBounds()` matching the new geometry so clicks on global tabs, project tabs, and the far-right project selector route cleanly. |
| **Styles & Glyphs** | [`internal/styles/styles.go`](file:///Users/marcus/code/sidecar/internal/styles/styles.go) | Add brand logo style token with `◱` icon, brand cyan foreground, and subtle vertical rule divider styling. |
| **Tests** | [`internal/app/view_test.go`](file:///Users/marcus/code/sidecar/internal/app/view_test.go), [`internal/app/global_frame_test.go`](file:///Users/marcus/code/sidecar/internal/app/global_frame_test.go) | Update header golden string tests, width truncation tests, and tab bounds assertions. |
