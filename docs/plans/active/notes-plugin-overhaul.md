# Notes Plugin Overhaul

**Status:** Draft
**Created:** 2026-08-17

## Goal

Bring the notes plugin up to the quality bar the rest of sidecar sets. Today it is
functional but rough: the right pane visibly jumps between viewing and editing, selection
is mouse-only and read-only, paste works only in one of three editor modes (and only by
accident), notes render as raw text despite glamour being vendored, and the td
integration is a one-way shell-out. The target is the best terminal notes experience
available: markdown-rendered by default, notational-velocity capture speed, and a single
editor surface that doesn't reflow when you start typing.

## Where we are (findings from code exploration)

Storage is already in good shape and should not change: notes live in td's per-project
SQLite via `td/pkg/notes` (`store.go`), shared with the `td` CLI, title derived from the
first content line. The problems are all in the presentation and interaction layer.

### The core structural problem: three render paths, three scroll models

The right pane has three distinct renderers (`view.go:182-220`):

1. **Preview** — custom renderer with line numbers, `~` filler, its own wrapping and
   `previewScrollOff`/`previewCursorLine` scroll state (`view.go:225-330`).
2. **Bubbles textarea** — no line numbers, its own wrap column and internal viewport.
3. **tmux/vim inline editor** — a PTY session on a temp-file export of the note
   (`inline_edit.go`).

Every transition between 1 and 2 reflows the text: line numbers appear/disappear
(horizontal shift of `lineNumWidth+1`), the wrap column changes, and cursor/scroll
position is not carried across (`plugin.go:917-923`, `plugin.go:1025`). Worse, making a
mouse selection *mid-edit* silently swaps the renderer back to preview
(`view.go:207-210`), and the next keypress swaps it back — the text visibly jumps twice
just from selecting a word. Three independent height calculations
(`trackTextareaScroll`, `previewViewport()`, `updateTextareaDimensions()`) drift against
each other. This is the "jumping between active/inactive states" — it is architectural,
not a set of small bugs, and papering over it per-transition will not converge.

### Other findings

- **Selection is display-and-copy only.** Mouse drag selects and auto-copies
  (`mouse.go:362-423`); there is no keyboard selection (`v`/`V`, shift-arrows), no cut,
  no delete-selection, no select-all. `getSelectedText` is read-only.
- **Paste is accidental.** The plugin has no `tea.PasteMsg` handler; paste reaches the
  textarea only via the unhandled-message catch-all (`plugin.go:626-633`), so it works
  only in textarea edit mode and is silently dropped in preview, search, and the list.
- **No markdown rendering**, even though `charm.land/glamour/v2` is in `go.mod` and
  `internal/markdown/renderer.go` already provides a width-aware, xxhash-cached
  `RenderContent` used by the conversations plugin.
- **td integration is shallow.** Note→task goes through `exec.Command("td", "create",
  ...)` with stdout scraping (`task_modal.go:227-268`) despite the plugin already
  importing `td/pkg`. No task ID is stored on the note, no back-link, no
  notes-for-task view. External edits (`td note` from an agent) appear only on manual
  `r` refresh — no watch.
- **Search is NV-shaped but crude.** Incremental full-text fuzzy with proper
  enter-to-open-or-create semantics (`plugin.go:1261-1327`) — the right model — but
  byte-wise scoring (breaks on non-ASCII), O(all content) per keystroke, no match
  highlighting, no recency weighting.
- **Small correctness debts:** byte-based truncation can split runes/ANSI
  (`view.go:311-314`); undo covers only delete/archive, in-memory, 20 deep; wheel
  behavior is inconsistent across the three editors; `loadNoteIntoEditor` resets scroll
  and jumps the cursor to the last line on every list movement.

## Design direction

### 1. Collapse to two modes: rendered view + one editor

Replace the preview/textarea split with **two** states over **one layout contract**:

- **View mode (default): glamour-rendered markdown** via `internal/markdown.Renderer`.
  This is the resting state of every note — what you see when browsing the list.
- **Edit mode: the textarea**, restyled to match the view's gutter and wrap column so
  entering edit changes the cursor, not the layout.

Concretely:

- Kill the custom preview renderer's line numbers and `~` filler (or add the same fixed
  gutter to both modes — pick one, apply to both). The wrap column, left margin, and
  height math must come from a single shared function both renderers call.
- Maintain a **line map** between source lines and rendered markdown lines so that
  entering edit mode places the textarea cursor at the source line corresponding to the
  rendered line under the view cursor, and leaving edit mode scrolls the rendered view
  to the line you were editing. Glamour output is not 1:1 with source; the map can be
  approximate (per-block anchors from goldmark's AST positions are enough — exact
  per-line fidelity is not required to eliminate the *jump*, only the *teleport*).
- **Clicking into the note body switches to edit mode** (raw source), and all selection
  happens there. View mode is for reading and scrolling only — mouse interaction with
  rendered glamour output is limited to scroll. This deliberately trades one predictable,
  user-initiated style shift on click-in for never having to map selections through
  styled, re-wrapped rendered text. The block-level line map softens the shift: the
  source view opens scrolled to the block that was clicked, not the top of the note.
  `Enter`/`i` from the list enter the same edit mode for keyboard-first use.
- Within edit mode, selection must **not** switch renderers — it overlays the source
  renderer (see §3).
- **Edit mode shows syntax-highlighted markdown source** — not WYSIWYG, not glamour:
  the buffer is exactly the note's bytes, one source line per logical line, so cursor,
  selection, and copy math stay plain-text. Line-level cues (heading color/bold, dimmed
  code-fence interiors, colored list/quote markers) come from a cheap per-line
  classifier; inline spans (`**bold**`, `` `code` ``, links) from a small per-visible-line
  scanner. Selection is applied as an overlay *after* syntax color using the existing
  column-range injection machinery, so the two compose. Implementation note: bubbles'
  textarea cannot style spans of its own content, so edit mode uses the textarea as the
  editing engine but renders through our own line renderer — the same wrapper the
  selection work (§3) needs anyway.
- The tmux/vim inline editor stays as the power-user escape hatch (`e`), unchanged in
  role. It already exports to a temp `.md` and diffs back.
- Ship markdown-by-default behind a feature flag for one release
  (`notes.markdownView`), defaulting on; the flag is an escape hatch, not a fork —
  off means "render view mode as plain wrapped text," same layout contract.

Lesson borrowed: this is the Obsidian/Typora model adapted to a TUI — rendered at rest,
source when editing, and the transition preserves place. The failure mode to avoid is
Bear/HedgeDoc-style live-WYSIWYG in a terminal; glamour cannot re-render per keystroke at
acceptable cost, and we don't need it to.

### 2. Fix paste properly

Add an explicit `tea.PasteMsg` handler with per-context behavior:

- **Editor (textarea):** insert at cursor (what happens today, but deliberate).
- **Search input:** insert into the query.
- **View mode / list:** paste creates a behavior decision — the NV-style move is
  "paste into the current note at end" or "create a new note from clipboard." Start
  with append-to-current-note in view mode and no-op in the list; revisit after use.
- Multi-line paste must not trigger per-line auto-save churn; suppress the debounce
  until the paste message is fully applied.

### 3. Real selection: keyboard-first, actionable

Build on the existing `ui.SelectionState` machinery rather than inventing a new one:

- **Keyboard selection in edit mode:** `v` (vim users) and shift-arrows/shift-home/end
  (everyone else) extend a selection anchored at the cursor. This lives in the textarea
  layer; if bubbles' textarea can't express it, wrap it — do not fork the render path.
- **Actions on selection:** `y`/copy (exists), `d`/`x`/backspace/delete = cut or delete,
  `p` replaces selection, typing replaces selection (standard editor contract).
- **Mouse selection stays**, still auto-copies. In view mode, a click enters edit mode
  first (§1) and a click-drag begins a selection there; within edit mode it sets the
  same selection state the keyboard uses, so drag-then-delete works.
- **Select-all** (`ctrl+a` in edit mode or `ggVG` chords are both fine; pick `ctrl+a`).
- Text-edit **undo**: once selections can delete text, accidental data loss gets easy.
  Add a simple content-snapshot undo ring (per note, in-memory, coalesced with the
  auto-save debounce) covering edit-mode changes — `u` / `ctrl+z`. Vim mode keeps vim's
  own undo.

### 4. td integration: use the library, keep the link

- Replace the `exec.Command("td", "create", ...)` + stdout scraping with the `td/pkg`
  API the plugin already links against. (If task creation isn't exposed in `td/pkg`
  yet, that's a small td-side addition — prefer that over keeping the scrape.)
- **Persist the link:** store the created task ID on the note (td notes support
  metadata; if not, a `task: td-xxxxxx` line convention in the note body is acceptable
  v1). Render linked-task status in the note header/info modal, `Enter` on it opens the
  task.
- **External-change sync:** watch for changes instead of requiring `r`. The cheap,
  store-agnostic version: poll the store's max `updated_at` on a timer while the plugin
  is visible (matching how other plugins refresh), and merge using the existing
  `pendingEditorSyncID` rules — never clobber a dirty local buffer, surface a
  "note changed externally" state instead. This directly serves the agent workflow:
  an agent runs `td note`, sidecar shows it within seconds.

### 5. Search polish (keep the NV soul)

The enter-to-open-or-create loop is the plugin's best feature; keep its semantics
exactly. Improve mechanics:

- Rune-wise (and case-folded) fuzzy matching; fix the byte-wise scorer.
- Highlight match spans in the list.
- Score = fuzzy score + recency boost (recently-updated notes float up) — pure NV.
- Only rebuild the full-content scan when the query grows/shrinks makes it necessary;
  cache lowercased content per note per load. An index is overkill at per-project note
  volumes — say so if that changes.

### 6. Correctness sweep (do first, small, independent)

- Rune-safe truncation in the view (`view.go:311-314`) using the existing ANSI-aware
  width helpers.
- Stop `loadNoteIntoEditor` resetting scroll/cursor to end-of-note on every list move;
  remember per-note scroll position for the session.
- Unify the wheel contract across view/edit/inline so boundary passthrough is
  consistent.
- Fix `TaskCreatedMsg` dropping `Epoch` on the error path (`task_modal.go:236`).

## Phasing

Each phase is shippable alone and ordered so later phases build on earlier seams.

1. **Layout unification + correctness sweep.** One shared layout function (wrap column,
   gutter, height) used by view and edit renderers; carry cursor/scroll across the mode
   switch; selection no longer swaps renderers; §6 items. This kills the jumping and is
   the prerequisite for everything else. Verify with `tmux-drive.sh` snapshots of the
   transition (enter edit, select, leave edit — text must not shift).
2. **Markdown view mode.** Glamour via `internal/markdown.Renderer` as the default view
   renderer, line-map anchoring for the edit transition, feature flag escape hatch.
3. **Selection + paste + undo.** §2 and §3.
4. **td integration.** §4 — library API, persisted link, external-change polling.
5. **Search polish.** §5.

## Explicit non-goals

- No live-WYSIWYG markdown editing (rendered-while-typing).
- No storage change — td's SQLite via `td/pkg/notes` stays the source of truth.
- No notes CLI surface: sidecar is a presentation layer here; the owning CLI is `td
  note`, which agents already use. Any new *rules* (e.g. link-line conventions,
  merge/refusal logic for external sync) go in state-free functions so they could move
  to td if ownership ever shifts.
- The tmux/vim inline editor is retained, not expanded.

## Risks

- **Glamour vs. selection/mouse math — resolved by design.** View mode is read/scroll
  only; any click into the body drops to source edit mode, where selection operates on
  plain text. No selection ever maps through rendered glamour output.
- **Custom edit-mode renderer.** Syntax highlighting and selection both require
  rendering the textarea's content through our own line renderer (bubbles' textarea
  can't style spans). This is the largest single piece of new machinery; budget for it
  explicitly in phases 1/3 rather than discovering it mid-way. Keep the highlighter a
  pure `line -> spans` function, testable without the UI.
- **Line-map fidelity.** Accept block-level anchoring; chasing exact line mapping
  through glamour re-wrapping is not worth it.
