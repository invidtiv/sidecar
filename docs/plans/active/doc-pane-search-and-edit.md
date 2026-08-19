# Doc-pane search and inline edit

Bring file (doc) panes in workspaces — both the project workspace plugin
(`internal/plugins/workspace`) and the global Workspaces browser
(`internal/overview`) — toward parity with the files plugin's file viewer:

1. **`/` in-file search** in a focused doc pane, matching the files plugin's
   content search (incremental typing, Enter to commit, `n`/`N`, match
   highlighting).
2. **Inline editing** of the file shown in a doc pane, using the same
   tmux-PTY editor the files plugin uses. First time a pane hosts an editor.

Direction of travel: doc panes converge on the files plugin's viewer feature
set (minus the file-tree explorer). Shared code is the point — an improvement
to search or editing on one surface must land on all three (filebrowser,
workspace panes, overview panes) for free, or the design is wrong.

## Settled decisions

- **Search lives in `internal/docview`.** The filebrowser's content search is
  plugin-coupled (state on `filebrowser.Plugin`, rendering in its `view.go`);
  nothing there is importable. Build search as a docview facility modeled on
  `internal/docview/select.go`: state on (or beside) `docview.Model`, matches
  painted via the `DecorateRow` path (never into the cached layout,
  `model.go:230-236`), scroll via the existing `starts[]` visual-row map.
- **Match coordinates are docview's.** Filebrowser stores byte offsets in raw
  source lines; docview works in visual rows with tab expansion. The shared
  match type uses source line + byte columns and converts through `starts[]`
  at paint/scroll time.
- **Inline edit is extracted into a new `internal/inlineedit` package** before
  a third copy exists. Today `internal/plugins/filebrowser/inline_edit.go`
  (702 lines) and `internal/plugins/notes/inline_edit.go` (719 lines) are
  near-duplicates over the shared `internal/tty` backend. The package owns:
  session lifecycle (start/attach/reattach/exit, activation-epoch guards,
  stale-start cleanup), the save/discard/cancel exit confirmation, mouse
  forwarding + native-cursor plumbing, and a **host contract** for dimensions
  and origin (the risky part — both existing copies hand-mirror their host's
  render layout; the contract makes the host supply `Viewport()` /
  `Origin()` instead of the editor guessing).
- **Filebrowser and notes migrate onto the shared packages** as part of this
  work, not later. That is what keeps the three surfaces improving together,
  and it is the only real test that the extraction is right.
- **Feature flag:** pane editing ships behind the existing
  `features.TmuxInlineEdit` gate, same as filebrowser. The gate is a *host*
  concern, not a package one: `internal/inlineedit` must not check the flag
  itself, because notes' inline edit is deliberately ungated today
  (`features.TmuxInlineEdit` appears only in
  `filebrowser/inline_edit.go:43,337`) and the extraction must not silently
  switch notes off.
- **`/` and `e` are free** in both `workspace-doc` and the overview doc-pane
  key space today; use `/` for search, `e` for edit (matching filebrowser).
- **`ctrl+p` / `f` in overview doc panes: yes.** The project workspace already
  answers both on a focused doc pane (`doc_panes.go:1072-1075` — `openDocFinder`
  and `openDocProjectSearch`); overview does not. That is exactly the
  one-surface-only bug the parity rule forbids, so closing it is in scope here
  rather than deferred to a backlog. Phase 2 carries it, since it is the same
  routing work as `/`.

## Unresolved questions

*(All of the questions below were settled in Phases 1 and 2. Kept as the
record of what was decided.)*
- **Settled (Phase 1):** the search UI in panes is a one-row bar inside the
  pane body, rendered by `docview.Model.View()` itself, so both surfaces
  inherit it with no rendering code. Key ownership is modelled on the existing
  `doc.mode` precedence: a live search owns every key in the pane and esc
  closes it.
- **Settled (Phase 2):** in-file search is a *sibling* of the finder /
  project-search overlay, not a third mode on it. The overlay keeps
  `workspace-doc-search`; the in-file bar is `workspace-doc-find` on the
  project surface and `global-workspaces-doc-find` in the browser. Command IDs
  are the Files plugin's own (`search-content`, `confirm`, `next-match`,
  `prev-match`, `cancel`) so one feature has one vocabulary everywhere.
- **Settled (Phase 2):** the finder / project-search overlay moved out of the
  workspace plugin into `internal/panesearch` (`Mode`, `Outcome`, `Caches`).
  Binding `ctrl+p` / `f` in the browser by copying 250 lines would have been
  the seam failure the plan warns about; extracting it made the browser's host
  file the only new per-surface code.

## Phases

### Phase 1 — `docview` search core

New file `internal/docview/search.go` (mirror `select.go`'s shape):

- `SearchState`: mode (off / typing / committed), query, `[]Match{Line,
  StartCol, EndCol}`, cursor.
- `HandleSearchKey(msg) (handled bool, cmd tea.Cmd)` implementing the two
  phases exactly as filebrowser's `handleContentSearchKey`
  (`internal/plugins/filebrowser/handlers.go:889-983`): typing phase (esc
  clears, enter commits, backspace, printable via `ui.PrintableKeyText`,
  incremental re-match); committed phase (`n`/`N` wrap + scroll,
  `j/k/ctrl+d/ctrl+u` scroll-within-search, enter keeps position, esc
  clears).
- Matching: case-insensitive scan over the loaded lines (port
  `updateContentMatches`, `operations.go:579-610`).
- Scroll: port the vim-like `scrollToContentMatch` margin behavior through
  docview's `starts[]`/`ApplyLine`.
- Highlight: decorate matched rows on the way to the screen with
  `styles.SearchMatch` / `SearchMatchCurrent`; reuse the ANSI-injection
  approach from `filebrowser/ansi_highlight.go` for rendered-markdown rows
  (move that helper into docview or a shared spot — filebrowser will import
  it back).
- Search bar rendering: one row owned by docview's `View()` when search is
  active (`/ query█ (3/17) [n/N]`), so every host gets it for free.
- Live refresh: recompute matches when the model reloads (docview `live.go`
  gate), matching `filebrowser/live_preview.go:114-115`.
- **Wrap mode** (`docview/wrap.go`): a source line becomes several visual rows,
  so a match's byte span can straddle a wrap boundary and one match can paint
  on two rows. Highlighting and scroll-to-match must both go through
  `starts[]` per visual row rather than assuming one row per match; toggling
  `w` while a search is committed must keep the cursor on the same match.
- **Selection coexistence**: search decoration and `select.go`'s selection
  decoration land on the same rows. Decide the precedence once in docview
  (selection over match, current match over other matches) rather than per
  host.

Tests: match computation, phase transitions, wrap-around, highlight of
tab-expanded, wrapped, and ANSI rows, and a match under an active selection.

### Phase 2 — bind search in both pane surfaces

- **Project workspace**: in `doc_panes.go` `handleDocKey`
  (`doc_panes.go:1040-1113`), `/` enters search on the active tab's view;
  while active, route keys to `HandleSearchKey` before other doc keys.
  `FocusContext` (`commands.go:409-486`) reports a `workspace-doc-search`-style
  context so `consumesTextInput` holds (extend `docSearchActive()` or add a
  sibling check in `commands.go:490-521`).
- **Overview**: same binding in `previewDocKey`
  (`internal/overview/preview_links.go:671-712`). Overview keys don't flow
  through `activeContext` — `WorkspacesKey` must consume all keys while
  search is typing (mind step 2 of the app ladder,
  `internal/app/update.go:885-891`). Ordering is already in our favour: a
  focused preview routes through `previewKey` at `workspaces.go:481`, which
  reaches `previewDocKey` (`preview.go:449`) before the list's own `/` filter
  case (`workspaces.go:583`), so `/` in a focused doc pane does not collide
  with workspace filtering. Keep that ordering — a `/` handled after the
  filter case is the bug to watch for.
- Keybindings registered in `internal/keymap/bindings.go` for `workspace-doc`
  and the overview doc context; footer command entries on both surfaces.
- **Also in this phase**: give overview doc panes `ctrl+p` and `f`, routed to
  the same finder / project-search core the workspace uses. Same routing seam
  as `/`, so it costs little here and closes a standing parity gap.
- Search dismisses on pane focus loss (same rule as `closeUnfocusedDocSearches`).

### Phase 3 — extract `internal/inlineedit`

- Move the shared lifecycle out of `filebrowser/inline_edit.go` and
  `notes/inline_edit.go` into `internal/inlineedit`:
  `Editor{Enter(path, line), Started(msg), Reattach(), Exit(),
  AttachFullScreen()}`, activation epochs, exit-confirmation model +
  rendering (use `internal/modal` idioms), click-away / pending-click
  handling, `Cursor()`, `PreferredMouseMode()`, mouse-coordinate forwarding.
- Host contract (interface the plugin/pane implements): editor viewport
  width/height, screen origin, and a redraw hook. No dimension math inside
  the package beyond the contract.
- Migrate filebrowser and notes to it; their `inline_edit.go` files shrink to
  the host-contract implementation plus entry keybindings. App contexts
  `file-browser-inline-edit` / `notes-inline-edit` stay unchanged.
- Verify with the existing filebrowser/notes editing flows via
  `scripts/tmux-drive.sh` (isolated per AGENTS.md) before touching panes.

### Phase 4 — inline edit in doc panes

- **Project workspace**: `e` on a focused doc pane (with a real file tab)
  enters edit via `inlineedit.Editor`, sized to the pane body (the
  `docContent.SetSize` box, `content.go:146-155`, minus header rows). New
  focus context `workspace-doc-edit`; add it to the interactive forward list
  in `internal/app/update.go:894-905`. Editor state lives on `docPane`,
  keyed per leaf; only the focused pane's editor receives keys. Pane close /
  tab close / focus loss with a live session triggers the exit confirmation.
  Suspend the leaf's `livepanes` refresh while editing (parallel to
  filebrowser's `autoRefreshBlocked`); `isEditorScratchPath` already filters
  editor churn (`live_panes.go:375-388`).
- **Overview**: same via `previewDoc`. Overview bypasses `activeContext`, so
  gate inside `WorkspacesKey` the way `PreviewInteractive()` does for
  terminal panes (`app/view.go:87`, `app/update.go:1405`) — likely by
  widening that signal to "preview owns the keyboard".
- Native cursor: surface the editor's `Cursor()` through each surface's
  cursor plumbing; `PreferredMouseMode()` likewise.
- Resize: pane drag/resize and window resize call the host contract's
  resize; `+`/`-` pane resize must resize the session, not clip it.
- One editor session per pane leaf; opening edit in a second pane while one
  is active is allowed (sessions are independent), but only the focused
  pane's editor is attached/receiving keys.

### Phase 5 — migrate filebrowser content search onto docview search

Optional but strongly preferred once Phases 1–2 are proven: replace
filebrowser's `contentSearch*` state, `updateContentMatches`,
`highlightLineMatches`, and `renderContentSearchBar` with the docview
facility. If filebrowser's renderer can't adopt it yet (its preview
rendering is not docview), keep its implementation but delete the duplicated
ANSI-highlight helper in favor of the shared one, and record the remaining
duplication here.

## Acceptance evidence

- `go build ./... && go test ./...` green, including new docview search tests.
- `tmux-drive.sh` proof runs (both axes isolated; run `paths` first):
  - `/` search in a project-workspace doc pane and in an overview preview doc
    pane: type, highlight, `n`/`N`, esc.
  - `e` edit in both surfaces: open, type, save-and-exit persists to disk,
    exit-without-saving discards, confirmation on close with live session.
  - Filebrowser and notes inline edit still work post-extraction.
- Keyboard-shortcuts skill table updated for the new `workspace-doc` /
  overview bindings.

## Risks

- **Dimension/origin drift** is the historical failure mode of inline edit —
  the host contract exists to make it structural. Any off-by-one shows up as
  a clipped or misaligned PTY; verify with PNG snaps at several pane sizes.
- **Overview key routing** is a different mechanism than the project surface
  (step 2 vs step 3 of the app ladder); test each independently.
- **Two-surface parity** is enforced by putting behavior in docview /
  inlineedit, not by mirroring code in `pane_host.go` files — if a change
  requires touching both surfaces' bindings beyond ~20 lines, reconsider the
  seam.
