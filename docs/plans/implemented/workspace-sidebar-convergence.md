# Workspace sidebar convergence

**Task:** td-256221  
**Date:** 2026-08-12  
**Builds on:** [First-class global Overview and cross-project Workspaces](global-overview-workspaces.md)

## Decision

Make the established project Workspaces sidebar the reference implementation
for both project and global Workspaces. Extract its layout and interaction as a
configurable presentation component, then make both consumers supply data and
capabilities to it.

The shared component owns visual and interaction behavior: panel chrome,
header and section placement, two-line rows, selection styling, viewport,
scrollbar, mouse geometry, wheel behavior, and sidebar resize. It does not own
workspace collection, preview loading, navigation, or mutations.

Global Workspaces remains read-only. In particular, it passes no create-shell,
create-worktree, rename, delete, attach, or interactive actions. Project
Workspaces retains those actions and their current validation/refusal paths.

Filtering and sorting should exist in the shared list model, but exposure is a
consumer choice. Keep the already-landed `/` filter in project Workspaces. Do
not add a project sort binding or header control in the first convergence pass;
global Workspaces keeps its current Activity/Project/Recent/Name control. That
avoids making a second product decision while removing the duplicated view.

## Current-state findings

The first implementation shared less than its plan required:

- `internal/workspacelist.Model` became a new global renderer. Project
  Workspaces uses only its filter matcher and no-match row; its established
  sidebar renderer remains in `internal/plugins/workspace/view_list.go`.
- Global wheel input moved the viewport without moving selection. Project
  wheel input moves selection and updates the preview.
- Global used a fixed 42% split with no drag or persistence. Project uses the
  saved `WorkspaceSidebarWidth` and a draggable divider.
- Global rendered raw list/preview content without project panel borders or
  padding, so its hit regions and right edge occupied different coordinates.
- The two renderers therefore have independent row height, headings, narrow
  fallback, scrollbar track, hover/click areas, and empty states. Fixing them
  separately will keep producing drift.

The td-256221 corrective patch aligns the outer panel geometry, protects the
preview's final content column, restores a persistent draggable divider, fixes
border-offset hit regions, and changes global wheel input to project-style
selection movement. The remaining work below removes the duplicate sidebar
renderer rather than continuing to polish it independently.

## Target journey

1. A user opens project or global Workspaces and sees the same sidebar frame,
   spacing, row selection treatment, scrollbar, and divider behavior.
2. Keyboard and wheel navigation move the selected workspace by the same
   amount and keep it visible; the corresponding preview follows selection.
3. Resizing either sidebar uses the same drag target, percentage calculation,
   bounds, and persisted width. Opening the other surface reflects that width.
4. Project Workspaces still shows its `New` and section `+` actions and retains
   shell/worktree lifecycle behavior.
5. Global Workspaces shows project and activity metadata in the same row shell,
   but offers no mutation or terminal-input path.
6. Global filtering/sorting continues to work. Project filtering keeps working;
   project sorting remains available in the model but undiscoverable and
   unbound until deliberately enabled.

## Implementation slices

### 1. Characterize the reference sidebar

- Extend the existing project sidebar baseline tests to cover wide and narrow
  rows, shells plus worktrees, empty state, warnings/toasts, filter-active
  state, selection with each pane focused, scrollbar, wheel step, click and
  double-click regions, divider drag bounds, and persisted width.
- Record semantic expectations separately from exact themed bytes so shared
  extraction can change implementation without weakening behavior.
- Include doc panes in the fixture: sidebar extraction must not alter the
  sidebar -> terminal -> document focus cycle or pane-tree divider priority.

### 2. Extract the project sidebar presentation

- Replace the global-specific renderer in `internal/workspacelist.Model` with a
  sidebar component derived from `renderSidebarContent`.
- Give it presentation-neutral row and section inputs plus explicit optional
  header/section actions. A missing action draws no button and registers no hit
  region; this is how global creation stays absent.
- Keep stable-ID selection, filter matching, stable sorting, viewport, and
  scrollbar in the component. Return typed hit regions from the exact rendered
  geometry.
- Move sidebar split/drag percentage arithmetic and bounds into one helper used
  by project and global consumers. Keep persistence at the caller/state seam.
- Do not import the workspace plugin, Overview, tmux, Git, or app packages from
  the component.

### 3. Adapt project Workspaces first

- Project shells and worktrees project into shared rows while callbacks retain
  current create, select, delete, attach, lifecycle, warning, and toast paths.
- Preserve shell-first navigation, main-worktree treatment, sidebar display
  config, activity animation, loaded-content commands, and absolute identity.
- Keep `/` filtering enabled with its current text-input context.
- Keep sort mode fixed to the existing project order and expose no sort command
  or clickable sort label in this slice.
- Remove the old rendering and hit-region loops only after the project
  characterization suite passes against the component.

### 4. Adapt global Workspaces

- Project global catalog items into the same row shell, supplying project name,
  provider/status, detail, relative age, and Activity sections.
- Pass read-only capabilities: select, preview focus, validated open, filter,
  sort, and refresh only.
- Keep stable identity through refresh/filter/sort and the current bounded,
  immutable selected-pane preview.
- Delete the duplicate global row renderer and its independent geometry once
  the parity tests and narrow fallback pass.

### 5. Integrated proof and cleanup

- Add contract tests that render both consumers from equivalent fixtures and
  compare shared frame, row, scrollbar, wheel, resize, and hit-region behavior.
  Assert the deliberate differences: project action regions exist; global ones
  do not; global subtitles include project identity and may use activity groups.
- Run focused packages, then `go test ./...`, `go vet ./...`, `go build ./...`,
  and `git diff --check`.
- Use `scripts/tmux-drive.sh` only after `paths` proves both the tmux socket and
  Sidecar state/config tree are isolated. Capture wide/narrow text and PNG proof
  for both project and global Workspaces, wheel selection, divider drag, filter,
  global sort, right-edge terminal text, and exact global-to-project opening.
- Independently review the integrated candidate before closing the task.

## Acceptance criteria

1. Project and global Workspaces use one sidebar renderer and one interaction
   model; neither keeps a parallel row/viewport/hit-region implementation.
2. Panel chrome, spacing, selection, scrollbar, scrolling, divider drag,
   persistence, narrow behavior, and hit regions behave consistently.
3. The right preview's last available content column and enclosing border remain
   visible at every supported width.
4. Project creation and lifecycle actions are unchanged; global Workspaces has
   no create-shell or other mutating/interactive action or hit region.
5. Filtering is shared and enabled in both current consumers. Sorting is shared,
   enabled globally, and initially unbound/hidden in project Workspaces.
6. Project doc-pane focus/geometry and global read-only preview/navigation do
   not regress.
7. Focused tests, full gates, isolated real-app proof, and independent review
   are recorded with the task.
