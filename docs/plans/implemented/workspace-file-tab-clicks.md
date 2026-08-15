# Click file tabs in project and global Workspaces

A file preview beside a terminal already has tabs. `{` / `}` move between
them. A click on a drawn tab should do the same thing, in the project
Workspaces plugin and in the global Workspaces view.

## Journey

1. Click `README.md` in a terminal. The file opens beside it.
2. Click `main.go`. A second tab appears.
3. Click the README tab. README is selected. The Go tab stays open.
4. The same three steps work on the global Workspaces tab.

## What is wrong today

Project Workspaces already draws the strip, registers `regionDocTab`, and
has `clickDocTab`. The existing test clicks the registered region, so it
cannot see a miss. Two things make a real click miss:

- Output / Diff / Task chips are registered *after* the file tabs, using
  `split.ContentX` rather than the terminal surface's X. When those chips
  overlap the document header, they win and the click changes preview tab
  (or no-ops on Output) instead of selecting the file.
- Global Workspaces still has a single `previewDoc` and replaces the file.
  There is nothing to click.

## Design

Tab strip geometry lives next to `docview.Tabs`. Both hosts call
`docview.LayoutTabStrip` for the header they draw *and* the hit regions they
register. A click cannot land on a tab that was not drawn.

Project keeps `docTabHit{LeafID, Index}` so two document leaves cannot steal
each other's click. Global stores the tab index on the region.

Global grows a `docview.Tabs` group:

- same surface + path already open → select, apply line
- new path → append
- `{` / `}` cycle while the document is focused
- `x` closes the active tab; last `x` or `q` closes the pane
- no persist (global previews are already memory-only)
- header is the tab strip only, matching project (`m` still toggles render)

`,` / `.` stay Output / Diff / Task on both surfaces.

## Proof

Tests click the *visual* tab (filename cells on the header row), not the
region the code just registered. Cover:

- project shell (no Output/Diff/Task chips)
- project worktree with those chips registered
- global, two files, click the inactive tab

Output/Diff/Task chip clicks still switch those tabs and must not type.
