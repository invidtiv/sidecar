# Document pane tabs, per-surface restore, and a shared file viewer

Click a file in a workspace or shell terminal → it opens beside the terminal.
Click another → it becomes a second tab, not a replacement. Switch to a
different shell and back → the same files are still there. The header is only
the tab strip: each label is the path, left-truncated so the filename end
always wins.

The pane is the same file viewer Files uses in its preview: wrap, info,
reveal, tab keys, content search, line jump, blame. Files keeps the tree,
file ops, and inline edit. Improvements to the viewer land in both places.

This is Phase 8 of
[preview-pane-tree-and-doc-panes.md](preview-pane-tree-and-doc-panes.md).
§3.8's boundary holds: one document model has no tab state; the pane tree
does not know about tabs; persistence is a list. Hosts own the tab group
and the chrome.

---

## 1. What exists today (verified against current `main`)

### 1.1 Any regular file already opens in the pane

- `openResolvedFilePreview` (`terminal_links.go:325`) opens every contained
  regular file in the doc pane when `paneRoot != nil`. The file-browser tab
  switch is only the flag-off fallback.
- Bare `main.go` and `main.go:37` are both links
  (`TestBareGoPathAndLineOpenRawDocPane`). `docPaneTarget` is "non-empty
  path", not "markdown".
- `applyDocRenderMode` (`doc_panes.go:154`): markdown with no line number
  opens rendered; anything else, including `path:line`, opens raw with the
  existing syntax highlighting from `filepreview`.

A second `openDocPaneFileForSurface` retargets the existing
`docview.Model` (`doc_panes.go:99-114`). The previous file is gone.

### 1.2 The header is four things in one row

`docHeaderChips` + `renderDocPane` (`doc_panes.go:538-567`) paint:

```
[internal/plugins/workspace/plugin.go] [Raw] [×]     q close · m render
```

The path chip is right-truncated to `max(width/2, 8)` so the mode chip, the
close chip, and the hint string can stay. A deep path loses its filename
first. Click targets sit on the mode and close chips
(`registerDocPaneRegions`). `m` and `q`/`esc` already work; they are also
drawn on the header.

The app footer already lists Close / Raw / Grow from `Commands()` when
`workspace-doc` is focused. The in-header hints are a second, narrower copy.

### 1.3 Persistence is one slot, and switching shells clears it

`WorkspaceState.PaneLayout` (`state.go:73`) is a single layout for the
project. `PaneLayoutJSON` already has `tabs []PaneDocTabJSON` and `active`,
but `encodePaneNode` writes exactly one tab and `decodePaneNode` returns
after the first valid tab.

- `selectedTerminalSurface` (`doc_panes.go:32`) already distinguishes
  `shell:<tmuxName>` from `workspace:<pathKey>`. Project shells share
  `ctx.WorkDir`; the tmux name is the identity that matters.
- `persistedPaneLayout` (`doc_panes.go:339`) writes a terminal-only layout
  when the open doc's surface is not the *current* selection.
- Selection handlers save after changing the selected index and before
  `loadSelectedContent` / `resetDocPanesForSelection`. The early save
  therefore describes the new surface as empty.
- `TestShellSelectionIdentityClosesSameRootDocument` asserts that: open
  README on shell A, select shell B, persisted layout is terminal-only for
  B, A's document is gone.

`restorePaneLayout` rebuilds a matching surface on relaunch, so a
single-shell session that never changes selection does come back. Any
other shell or workspace in the same project overwrites the slot.

### 1.4 Two previews, one of them is a plugin field pile

`internal/filepreview` already loads for both hosts. `internal/docview`
is one document: load, highlight, markdown toggle, scroll. It has no wrap,
no selection, no `/` search, no `:line`, and image/binary are stubs.

Files preview is not a type. It is fields on `filebrowser.Plugin`
(`previewLines`, `previewScroll`, `markdownRenderMode`,
`previewWrapEnabled`, `tabs`, `contentSearch*`, `selection`, `infoMode`,
`blameState`, inline-edit session…). `tabs.go` talks to the tree
(preview-vs-pinned, `syncTreeSelection`) and to the inline editor.
`getPreviewLines` / wrap / selection live in `view.go` on the plugin.

`file-browser-preview` already binds the keys the workspace pane wants:
`x` close-tab, `{` / `}` cycle, `m` toggle, `w` wrap, `I` info,
`ctrl+r` reveal, `/` search, `:` line jump, `B` blame, `Y` yank path.
Tab labels are `filepath.Base`, right-truncated, with `<` `>` overflow
(`tabs.go:375`). That is the key vocabulary to share and the visual
vocabulary workspace will not copy — workspace tabs show a left-truncated
path, not a basename.

`workspace-doc` currently binds `q`/`esc` close-pane, `m` toggle, `+`/`-`
resize, `tab` focus. No `x`, no `{` / `}`, no wrap/info/reveal.

### 1.5 The pane tree stays unaware of tabs

`docview.Model` holds path, scroll, render mode, and a generation-stamped
load. It has no tab state. A pane that wants tabs owns a list of models
plus an active index.

`PaneDoc.DocID` stays an indirection into the plugin registry; the
registry entry is a tab group, not one model.

### 1.6 Global Workspaces is a different surface

`internal/overview/preview_links.go` is not a pane tree and has no
per-shell persist. File tabs and click-to-select on that surface landed
later in [workspace-file-tab-clicks.md](workspace-file-tab-clicks.md);
they stay memory-only for the selected row.

---

## 2. Goals and non-goals

**Goals**

1. A workspace doc pane holds several files as tabs. Clicking a path that
   is already open focuses that tab (and jumps to `path:line` when the
   link has one). Clicking a new path appends a tab.
2. The header is only the tab strip. No Raw chip, no ×, no `q close · m
   render`. Each tab label is the relative path, left-truncated
   (`…/workspace/plugin.go`) to the width that tab is given. A single tab
   gets the whole row.
3. Focused-doc keys match Files for the viewer: `x` closes the active
   file, `{` / `}` cycle, `m` toggles render on markdown, `w` wrap, `I`
   info, `ctrl+r` reveal, plus content search, line jump, and blame as
   the viewer grows. Default mode stays what shipped: rendered markdown,
   raw highlighted code.
4. Open files, active tab, render mode, wrap, scroll, and split ratio
   are remembered per shell and per workspace. Switching away and back,
   or relaunching onto the same surface, restores them.
5. The viewer is one implementation. Files hosts it in the preview pane;
   Workspaces hosts it in a `PaneDoc` leaf. A fix or a new preview
   command lands once.

**Non-goals**

- Putting the Files plugin, or its tree, in a pane-tree leaf.
- A second terminal leaf, neighbor navigation, `ctrl+w` (Phase 7).
- Preview-vs-pinned tabs on the workspace surface. A terminal click is
  a decision; every workspace tab is pinned. Files may keep a preview
  tab for tree cursor follow — that is a Files host concern.
- Inline edit (tmux in the preview box) in the workspace pane. The
  sibling terminal is already there; `e` can open `$EDITOR` or send the
  path to that shell. The Files inline editor stays in Files until a
  later extract has a reason.
- Tree-native file ops (rename, delete, mkdir, yank/paste, drag-drop)
  in the workspace pane.
- Quick-open and project search in the workspace pane. Those stay Files
  (or become app-wide later).
- Global Workspaces tabs or persist.
- Named layouts, drag-to-reorder tabs, a hard tab cap.

---

## 3. Design

### 3.1 The cut

Files is a tree plugin that hosts a preview. Workspaces is a terminal
plugin that hosts a preview. The shared object is the preview, not the
plugin.

```
                    ┌─────────────────────┐
                    │  file viewer        │
                    │  (docview → fileview)│
                    │  one file + tabs    │
                    │  wrap, search, info │
                    │  reveal, blame, …   │
                    └─────────┬───────────┘
                              │
              ┌───────────────┴───────────────┐
              │                               │
     Files preview pane              Workspace PaneDoc leaf
     (tree, file ops,                (pane tree, per-surface
      inline edit,                    persist, left-truncated
      basename tab labels)            path tab labels)
```

| In the shared viewer | Files host only | Workspace host only |
|---|---|---|
| One-file model: load, highlight, markdown, wrap, scroll, selection, images | Tree, watcher, drag-drop | Pane tree, `SplitLeaf` / `ClosePane` |
| Tab group: open / focus / close / cycle, overflow window | Preview-vs-pinned, `syncTreeSelection` | Per-surface `PaneLayouts`, hide vs forget |
| Path actions: info, reveal, yank path/contents, blame | Rename, delete, mkdir, yank/paste | `q` hide, `+`/`-` resize the split |
| Content search, line jump | Quick-open, project search | Terminal link activation |
| Tab strip geometry (`RenderTab` + overflow) | Basename (or parent/base) labels | Left-truncated path labels |
| | Inline edit | |

Hosts pass a box, a keymap context, and a `Label(path, width)` func.
They do not fork render or scroll.

`internal/docview` is the seed. Grow it until it is the viewer. Rename
to `internal/fileview` when Files migrates, so the name matches both
hosts and the import churn happens once.

Workspace does not import `filebrowser`. Path actions that are still
methods on `filebrowser.Plugin` move next to the viewer as functions
of `(root, path)` (and current line, when that matters) *before*
workspace needs them. Reveal is already that shape
(`revealInFileManager`). Info is already stat + git fetch on a path.

### 3.2 User journey

1. On shell A, click `README.md`. Pane opens, rendered, header is the
   left-truncated path and nothing else. Footer shows Close / Raw / …
2. Click `internal/plugins/workspace/plugin.go`. A second tab appears.
   Active tab is the Go file, raw. `m` is a no-op. `w` wraps. `I` shows
   file info. `ctrl+r` reveals it in Finder.
3. `{` / `}` or a click moves between them. Each tab keeps its own
   scroll, mode, and wrap because each is its own document model.
4. `x` closes the Go file. README is selected. `x` again closes the last
   tab and the pane; the terminal takes the width back.
5. Open two files on A, press `q`. The pane hides, terminal is
   full-width. The files are still this surface's set.
6. Switch to shell B. B has no files. Click `main.go`. B has its own set.
7. Switch back to A. The pane returns with the same two files, same
   active tab, same modes, same scroll, same split ratio.
8. Relaunch Sidecar onto A. Same restore.

In Files, the right pane is the same viewer: same wrap, info, reveal,
search, tab keys. The tree still owns navigation and file ops. A wrap
or blame fix does not get made twice.

### 3.3 Tab group

The workspace registry entry:

```go
type docPane struct {
    leafID  int
    root    string
    surface string
    tabs    fileview.Tabs // or docview.Tabs while the package still has that name
}

type Tabs struct {
    Items  []Item
    Active int
}

type Item struct {
    View *Model
}
```

The same `Tabs` type is what Files holds instead of `[]FileTab` plus a
parallel pile of preview fields. Files may keep a `Preview bool` on the
host side for the ephemeral tree-follow tab; the viewer does not.

`openDocPaneFileForSurface`:

- same `root`+path already in `tabs` → select it, apply line target, do
  not append;
- otherwise append a new model, `applyDocRenderMode`, select it;
- first file on a surface with no live pane: `SplitLeaf` as today.

`DocID` on the leaf still points at this `docPane`. Hit regions for tabs
use `Data = tab index`.

Dedup key is the root-relative display path, slash-normalized. An
absolute path that escaped the root is not a tab we persist (same rule
`decodePaneNode` already uses).

### 3.4 Header: path tabs, left-truncated, nothing else

`renderDocPane` paints one row: the tab strip. Hints string is empty.
`docHeaderChips` goes away.

Layout, in order:

1. Measure each tab with `styles.RenderTab` using a *candidate* label
   from the host's `Label` func.
2. One tab: the candidate is the full relative path, then
   `ui.TruncateStart(path, rowWidth)` so the filename end is what
   survives. The tab takes the row.
3. Several tabs: pack left-to-right (`visibleTabRange`, `<` / `>` when
   the active tab plus neighbors do not fit). Each tab's budget starts
   from a share of the row (roughly `row / n`, floors at 8). Leftover
   columns after packing go to the **active** tab so it can show more
   path. Every workspace label is `ui.TruncateStart`, never
   `Truncate`/`TruncateString` from the left-of-filename side.
4. Two tabs with the same basename (`cmd/sidecar/main.go` and
   `internal/foo/main.go`) stay distinguishable because the path is the
   label. No parent/base special case on this host.

Clicking a drawn tab selects it. There is no close hit-region. Width
collapse still drops whole tabs via the overflow window; it does not
clip a pill in half. `layoutHeaderChips` is the wrong helper — it drops
trailing chips to protect a hint string this header does not have. The
overflow window lives on the tab group so both hosts share it.

Do not invent a new tab visual. `styles.RenderTab` is what the rest of
the app uses.

Files may keep basename labels via its own `Label`. Same strip, same
overflow, different text.

### 3.5 Keys

`workspace-doc` (focused doc pane only):

| Key | Action |
|---|---|
| `x` | Close the active tab. Last tab closes the pane and forgets the set. |
| `{` / `}` | Previous / next tab |
| `m` | Toggle render when the active file is markdown; no-op otherwise |
| `w` | Toggle line wrap |
| `I` | File info modal |
| `ctrl+r` | Reveal in the OS file manager |
| `Y` | Copy the relative path |
| `y` | Copy selection, or the whole file if there is none |
| `/` | Content search (once the viewer has it) |
| `:` | Line jump (once the viewer has it) |
| `B` | Blame (once the viewer has it) |
| `q` / `esc` | Hide the pane. Terminal goes full width. The tab set stays remembered for this surface. |
| `+` / `-` | Resize the workspace split |
| `tab` / `shift+tab` | Focus cycle (sidebar / terminal / doc) |

`q` is hide. `x` on the last file is forget. That is the distinction
the current single `q` cannot make, and it is why restore was losing
work: hide and forget were the same write.

`+` / `-` stay workspace split resize, not Files tree-width. The
viewer does not own those keys.

Footer `Commands()`: Close stays bound to `q` (hide). Add Close-tab
(`x`), cycle, Wrap, Info, Reveal. Drop the in-header hint string so
the footer is the only place those words appear. `m` remains "Raw" /
"Render" and is omitted from the footer when the active file is not
markdown.

Register bindings next to the existing `workspace-doc` block in
`plugin.go` (`RegisterPluginBinding`). Update
`.claude/skills/keyboard-shortcuts/SKILL.md` and the website
workspaces page in the same slice as the keys.

`,` / `.` stay Output/Diff/Task on worktrees. They must not cycle doc
tabs. `m` cannot start a merge while the doc is focused —
`handleDocKey` already swallows keys; keep it that way. Viewer keys
live in the viewer or in `handleDocKey`, not in the list keymap.

### 3.6 Per-surface persist

Replace the single slot with a map. Keep the old field long enough to
read once.

```go
type WorkspaceState struct {
    // ...
    PaneLayout  *PaneLayoutJSON            `json:"paneLayout,omitempty"`  // read-only migrate
    PaneLayouts map[string]*PaneLayoutJSON `json:"paneLayouts,omitempty"` // surface → layout
}
```

`PaneLayoutJSON` gains one field:

```go
Open bool `json:"open,omitempty"`
```

`Open == false` means this surface has tabs but the pane is hidden
(`q`). `Open == true` (or omitted on old records that still have a
split) means restore the split.

Each doc leaf still serializes `tabs` + `active`. Encode **every** tab,
not just the first. Each tab:

```go
type PaneDocTabJSON struct {
    Path   string `json:"path"`
    Mode   string `json:"mode,omitempty"`   // "raw" | "rendered"
    Wrap   bool   `json:"wrap,omitempty"`
    Scroll int    `json:"scroll,omitempty"`
}
```

Write path is:

1. Before the selected surface changes, encode the live tree (or the
   hidden remembered set) into `PaneLayouts[oldSurface]`.
2. Change selection.
3. Restore `PaneLayouts[newSurface]`: if missing or `Open == false` or
   every tab is stale, live tree is a single terminal leaf; if `Open`
   and at least one tab still resolves, rebuild the split.
4. Persist the whole map.

`TestShellSelectionIdentityClosesSameRootDocument` asserts the map
shape: shell A's README is still in `PaneLayouts["shell:A"]` after
selecting B; B's live tree is terminal-only; selecting A again reopens
README.

Migrate `PaneLayout` on read: if the map is empty and the legacy field
is set, insert it at `legacy.Surface`. Stop writing the legacy field.

Do not prune map entries just because a shell is temporarily missing
from the in-memory list (startup). Prune when a shell or worktree is
actually deleted.

Feature-off must keep leaving `PaneLayouts` untouched, the same way
`paneRoot == nil` already preserves `PaneLayout`.

Files persist stays `FileBrowserState.Tabs`. The viewer serializes
enough for both hosts (path, mode, wrap, scroll); each host stores that
in its own state document.

### 3.7 Restore cost

A surface with eight tabs must not syntax-highlight eight files on
every shell switch.

- Rebuild the tab list and the tree immediately.
- `Load` the **active** tab now.
- Other tabs load on first select (or when they become active after
  the previously active path was pruned).
- A tab that has not loaded yet shows the existing loading state, not
  an empty box that looks like a bug.

Stale paths (deleted files, escaped paths) drop out of the restored
list, same as `TestRestorePaneLayoutPrunesStaleTabsAndRejectsOtherRoot`.
If every tab is stale, the live tree is terminal-only and the map
entry becomes terminal-only / forgotten.

### 3.8 Hide vs forget vs switch

| Action | Live tree | `PaneLayouts[surface]` |
|---|---|---|
| `q` / `esc` | Close the split, resize terminal | Tabs kept, `Open=false` |
| `x` last tab | Close the split, resize terminal | Entry becomes terminal-only (forgotten) |
| `x` not last | Stay split, next tab selected | Tabs rewritten, `Open=true` |
| Switch to another surface | Restore *that* surface | Previous surface written first |
| Click a file while hidden | Reopen split at last ratio | New/focused tab, `Open=true` |
| Relaunch onto a surface | Restore if `Open` | Unchanged |

Clicking a file while hidden reopens the remembered set and focuses
(or appends) the clicked path. That is how `q` stays a hide, not a
dead end.

### 3.9 Defaults

`applyDocRenderMode` stays the authority for a first-time open:

- markdown, no line → rendered
- markdown, `path:line` → raw (so the line number means something)
- anything else → raw

Restored tabs honor the saved `Mode` and `Wrap`. A new tab goes through
`applyDocRenderMode`, not through the previous tab's mode.

---

## 4. Phases

Each phase is a reviewable slice with its own user-visible change.
Workspace is the first host. Files migrates onto the same viewer after
that viewer exists and workspace is using it. Do not extract a third
package that neither host consumes.

**Phase 1 — remember per surface, still one file.**
Add `PaneLayouts`, migrate `PaneLayout`, save *before* the surface
changes, restore the incoming surface. Single-tab encode/decode still,
but write the one tab into the map under the right key. `Open` is
always true in this phase; `q` still forgets. Acceptance: open README
on shell A, switch to B and back, README is back. Relaunch onto A,
README is back. Switching to a workspace at the same root does not
steal A's set.

**Phase 2 — several files.**
Tab group on the doc registry (the type that will become `fileview.Tabs`).
Open appends or focuses. `x` / `{` / `}` work. Last `x` forgets.
Persist the full tab list, active index, and scroll. Lazy-load
non-active tabs. The current chip header can keep showing the active
path only. Acceptance: click `README.md` then `main.go`; both exist as
tabs; `x` on `main.go` leaves README; switch shells and back restores
both.

**Phase 3 — the header.**
Replace chips + hints with the left-truncated tab strip in §3.4.
Click a tab to select. No mode/close hit regions. Update the render
tests that currently require `Rendered`, `×`, and `q close` in the
header (`doc_panes_test.go` around the `guide.md` header assertion).
Narrow widths: one deep path still shows `…/plugin.go`; two tabs
still show both filenames.

**Phase 4 — `q` hides.**
`Open=false` on `q`/`esc`. Live tree collapses. Clicking a file or
restoring an `Open` surface reopens at the last ratio. Footer Close
stays `q`. Acceptance: two tabs, `q`, full-width terminal, switch
away and back, both tabs return. `x` `x` on the same set, switch
away and back, no pane.

**Phase 5 — path actions on the viewer.**
Move reveal, info, yank path/contents off `filebrowser.Plugin` and
onto the viewer as `(root, path)` commands. Add wrap to the document
model. Bind `w`, `I`, `ctrl+r`, `Y` (and `y` once selection exists)
in `workspace-doc`. Files calls the same functions. Acceptance: `I`
and `ctrl+r` work on a workspace tab and in Files; wrap is one flag
on the model, persisted per tab.

**Phase 6 — docs and workspace proof.**
Keyboard skill, website workspaces page, `Commands()` labels.
Isolated tmux/state proof: named project, named shell A / shell B,
click two paths on A, `q`, switch B, switch A, confirm both tabs and
the left-truncated header. Do not touch the default tmux server.

**Phase 7 — grow the viewer.**
Content search, line jump, real image preview (docview's stubs go
away), selection copy. Each is a viewer feature with tests against
the model, then bindings in both hosts.

**Phase 8 — Files hosts the viewer.**
First extract the content-neutral tab group and strip described in
[workspace-issue-pane-tabs.md](workspace-issue-pane-tabs.md), without waiting
on Phase 7; Files, project/global document panes, and later issue panes all use
that one foundation. The rest of this phase still follows Phase 7: delete the
preview field pile and remaining `filebrowser/tabs.go` state that the viewer now
owns. Files `View` composes tree + viewer. Preview
tests that talked to plugin fields talk to the viewer. Rename
`docview` → `fileview` in this slice so the import churn happens
once, with both hosts already compiling against the grown API.
Inline edit, tree ops, quick-open, and project search stay in Files.

Phase 1–6 are the workspace steel thread and can ship without Phase
8. Phase 5 is deliberately *before* Phase 8 so workspace is not
calling into `filebrowser` and Files is not left with a stale copy
of reveal/info. Phase 8 is its own reviewable epic; it is not a
prerequisite for using tabs beside a terminal.

---

## 5. Risks

- **Saving after the index change.** Today's write order is the whole
  persist bug. Phase 1 has to capture `oldSurface` *before*
  `selectTopShellAt` / `selectWorktreeAt`. A test that only inspects
  the map after a finished switch will miss a regression that writes
  B's empty layout onto A's key.
- **Two geometry authorities.** The tree still feeds
  `terminalLeafBox()`. Tabs do not add a row. `terminalHeaderRows`
  stays 1.
- **Restore storms.** Lazy-load is mandatory once tabs > 1. A test
  should count `Load` commands on restore: exactly one, for the
  active tab.
- **Boiling Files.** Phase 8 rewrites a working preview. Do it after
  two hosts exist and the contract is real. Extracting a viewer Files
  does not use is how a third implementation appears.
- **Import cycles.** Workspace must not import `filebrowser`. Path
  actions move to the viewer package (or a tiny sibling) before
  workspace binds them.
- **`m` vs merge, `,` / `.` vs tabs.** Viewer keys stay in the doc
  context. List and Output/Diff/Task keys stay out.
- **`+` / `-` mean different things.** Workspace: split ratio. Files:
  tree width. The viewer does not bind them.
- **Global header drift.** Do not route the workspace strip through a
  helper that still paints mode/close chips, or the global preview
  changes without a test that asked it to. Host chrome stays host-local
  until global opts in.
- **Inline edit temptation.** Leave it in Files. Pulling a PTY into
  the shared viewer to "reach parity" couples the workspace pane to
  a Files-only lifecycle.

---

## 6. Files

| File | Role |
|---|---|
| `internal/docview` (later `internal/fileview`) | Document model, then tabs, wrap, path actions, search |
| `internal/filepreview` | Shared loader (already) |
| `internal/state/state.go` | `PaneLayouts` map, `Open`, per-tab mode/wrap/scroll, migrate `PaneLayout` |
| `internal/plugins/workspace/doc_panes.go` | Host: tab group, header strip, hide vs forget, encode/decode |
| `internal/plugins/workspace/plugin.go` | Save-before-switch, restore from map, `workspace-doc` bindings |
| `internal/plugins/workspace/keys.go` | Dispatch only if `handleDocKey` does not own a key |
| `internal/plugins/workspace/commands.go` | Close-tab, cycle, hide Close, Wrap, Info, Reveal, conditional Raw |
| `internal/plugins/workspace/doc_panes_test.go` | Persist, tabs, header, hide |
| `internal/plugins/workspace/navigated_selection_test.go` | Map-aware persist |
| `internal/plugins/filebrowser/operations.go` | Reveal/info become calls into the viewer |
| `internal/plugins/filebrowser/{tabs,view,handlers}.go` | Phase 8: host the viewer; drop duplicated preview state |
| `.claude/skills/keyboard-shortcuts/SKILL.md` | Document pane table |
| `website/docs/workspaces-plugin.md` | Tabs, keys, restore |

---

## 7. Acceptance

A named shell A in an isolated Sidecar state tree:

1. Click `README.md`, then `internal/plugins/workspace/plugin.go`.
2. Header is two tabs, no Raw, no ×, no `q close`. The Go tab reads
   `…/workspace/plugin.go` (or the full path if it fits).
3. `m` on the Go file does nothing; on README it toggles render.
   `w` wraps. `I` opens info. `ctrl+r` reveals.
4. `x` closes the Go file. `q` hides README. Switch to shell B
   (empty), back to A: README is back, rendered as left.
5. Open both again. Quit Sidecar, relaunch onto A: both tabs, same
   active, same modes.
6. `x` `x`. Switch away and back: no pane.

After Phase 8, the same wrap / info / reveal / tab keys in Files are
the same functions. A viewer test covers both hosts' behavior; a
Files-only test covers the tree.

`gofmt` / `git diff --check` clean. Focused workspace + viewer tests,
including the persist cases. No default tmux server.
)
