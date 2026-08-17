# Notes Plugin Overhaul

**Status:** Phase 1 shipped (2026-08-17) — later phases still open
**Created:** 2026-08-17
**Phase 1:** td-71789d (`e312ddd4`, `055d9f7e`, `cc907480`)

## Goal

Bring the notes plugin up to the quality bar the rest of sidecar sets. Today it is
functional but rough: the right pane visibly jumps between viewing and editing, selection
is mouse-only and read-only, paste works only in one of three editor modes (and only by
accident), notes render as raw text despite glamour being vendored, and the td
integration is a one-way shell-out. The target is the best terminal notes experience
available: markdown-rendered by default, notational-velocity capture speed, and a single
built-in editor surface whose frame and viewport anchor do not jump when you start typing.
Rendered markdown and raw source cannot be visually identical; the contract is stable
geometry and place, not a pixel-identical mode switch. The tmux/$EDITOR path remains an
explicit power-user escape hatch, not a second implementation of built-in editing behavior.

## Where we are (findings from code exploration)

The storage boundary is already in good shape and should remain td-owned: notes live in
td's per-project SQLite through Sidecar's `store.go` adapter over `td/pkg/notes`, shared
with the `td note` CLI. One contract mismatch does need resolving: td stores `Title` and
`Content` separately and its CLI can edit them independently, while Sidecar creates the
title as the first content line and `UpdateContent` silently rewrites the title from that
line on every built-in save. Most problems are in presentation and interaction, but title
authority and note/task links are td-domain decisions rather than view details.

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
  (`mouse.go:362-423`); there is no keyboard selection (shift-arrows or an anchor mode), no cut,
  no delete-selection, no select-all. `getSelectedText` is read-only.
- **Paste is accidental.** The plugin has no `tea.PasteMsg` handler; paste reaches the
  textarea only via the unhandled-message catch-all (`plugin.go:626-633`), so it works
  only in textarea edit mode and is silently dropped in preview, search, and the list.
  Worse, that catch-all bypasses the normal before/after comparison, so a paste can change
  the visible buffer without setting `editorDirty` or starting auto-save.
- **No markdown rendering**, even though `charm.land/glamour/v2` is in `go.mod` and
  `internal/markdown/renderer.go` already provides a width-aware, xxhash-cached
  `RenderContent` used by document, issue, file-preview, update, and conversation views.
- **td integration is shallow.** Note→task goes through `exec.Command("td", "create",
  ...)` with stdout scraping (`task_modal.go:227-268`) despite the plugin already
  importing `td/pkg/notes`. td does not currently expose public task creation to in-process
  clients, and its public `notes.Note` has no metadata or issue-link field. No task ID is
  stored on the note, no back-link exists, and agents cannot query notes for a task.
  External edits (`td note` from an agent) appear only on manual `r` refresh — no watch.
  `pendingEditorSyncID` is only a one-shot resync after Sidecar's own tmux/$EDITOR writes;
  it is not an external-change or conflict protocol.
- **Search is NV-shaped but crude.** Incremental full-text fuzzy with proper
  enter-to-open-or-create semantics (`plugin.go:1261-1327`) — the right model — but
  byte-wise scoring and backspace break on non-ASCII, the empty-result view falls back to
  showing all notes, equal-score ordering is not deterministic, and there is no match
  highlighting or recency weighting.
- **Small correctness debts:** byte-based truncation can split runes/ANSI
  (`view.go:311-314`); undo covers only delete/archive, in-memory, 20 deep; wheel
  behavior is inconsistent across the three editors; `loadNoteIntoEditor` resets scroll
  and jumps the cursor to the last line on every list selection change; several async
  note results carry an `Epoch` but are applied without a stale-project check; and the
  note→task command reads mutable `p.ctx`/`p.store` from inside its async closure. Editor
  exports also use a predictable temp path with `0644` permissions for note content.

## Design direction

### 1. Collapse to two built-in modes: rendered view + one editor

Replace the preview/textarea split with **two** states over **one layout contract**:

- **View mode (default): glamour-rendered markdown** via `internal/markdown.Renderer`.
  This is the resting state of every note — what you see when browsing the list.
- **Edit mode: the textarea**, restyled to match the view's gutter and wrap column so
  entering edit changes the cursor, not the layout.

Concretely:

- Remove the custom preview's line numbers and `~` filler. Notes are prose first, and a
  permanent gutter spends scarce width without helping the normal capture/read journey.
  If line numbers return as an option later, reserve exactly the same gutter in both
  modes. The wrap column, left margin, content height, scrollbar column, and status row
  must come from one layout value both modes consume.
- Maintain a **line map** between source lines and rendered markdown lines so that
  entering edit mode places the textarea cursor at the source line corresponding to the
  rendered line under the view cursor, and leaving edit mode scrolls the rendered view
  to the line you were editing. Glamour output is not 1:1 with source; the map can be
  approximate (per-block anchors from goldmark's AST positions are enough — exact
  per-line fidelity is not required to eliminate the *jump*, only the *teleport*).
- The current `internal/markdown.Renderer` returns only `[]string`; it cannot provide that
  map. Add an adjacent structured result (`Lines` plus source anchors) or a Notes-specific
  mapping adapter without breaking existing consumers. Do not parse ANSI output after the
  fact and guess which source block produced it. Prove the mapping steel thread on a plain
  paragraph before committing the whole editor transition to it.
- **Clicking into the note body switches to edit mode** (raw source), and all selection
  happens there. View mode is for reading and scrolling only — mouse interaction with
  rendered glamour output is limited to scroll. This deliberately trades one predictable,
  user-initiated style shift on click-in for never having to map selections through
  styled, re-wrapped rendered text. `Enter`/`i` from the list enter the same edit mode
  for keyboard-first use.
- **Click-in lands where you clicked, precisely.** This matters most on a scrolled
  note: the mapping is screen row + view scroll offset → rendered line → source line
  (via the line map) → cursor placed there, with the edit view scrolled so that source
  line sits on (or near) the same screen row the click happened on. Block-level anchors
  are the floor, not the target: within a plain paragraph the rendered-to-source line
  offset is computable from wrap math, so most clicks can land on the exact line and an
  approximate column; only inside constructs glamour reshapes heavily (tables, deep
  nesting) do we degrade to top-of-block. Today this is "okay, not great" — make it a
  tested contract: given a note, a scroll offset, and a click point, the resulting
  cursor position is asserted in unit tests, not eyeballed.
- Within edit mode, selection must **not** switch renderers — it overlays the source
  renderer (see §3).
- **Edit mode shows syntax-highlighted markdown source** — not WYSIWYG, not glamour:
  the buffer is exactly the note's bytes, one source line per logical line, so cursor,
  selection, and copy math stay plain-text. Line-level cues (heading color/bold, dimmed
  code-fence interiors, colored list/quote markers) come from a cheap per-line
  classifier; inline spans (`**bold**`, `` `code` ``, links) from a small per-visible-line
  scanner. Selection is applied as an overlay *after* syntax color using the existing
  column-range injection machinery, so the two compose. Do not keep a hidden textarea as
  the editing engine while independently reimplementing its wrapping, viewport, cursor,
  and drawing: that recreates the dual-layout bug inside edit mode, and those textarea
  details are not all a stable public contract. Start by keeping the textarea's own view
  authoritative and applying a selection overlay. If span styling cannot be added without
  duplicating layout, promote the editor to a reusable component that owns buffer,
  layout, cursor, selection, and undo together; make syntax highlighting a follow-on to
  that component rather than a prerequisite for selection.
- The tmux/vim inline editor stays as the power-user escape hatch (`e`), unchanged in
  role. It already exports to a temp `.md` and diffs back.
- Notes itself is already experimental behind `notes_plugin` (default off), so avoid a
  second flag unless independent rollback is genuinely needed. If release risk justifies
  one, use the repository convention `notes_markdown_view`, register it in
  `internal/features/features.go`, document it, default it off during opt-in proof, then
  flip/remove it deliberately. In either case, `m` should toggle rendered/raw view at
  runtime, matching Sidecar's existing document panes; raw view uses the same layout
  contract rather than preserving the old renderer.

Lesson borrowed: this is the Obsidian/Typora model adapted to a TUI — rendered at rest,
source when editing, and the transition preserves place. The failure mode to avoid is
Bear/HedgeDoc-style live-WYSIWYG in a terminal. We do not need it, and any claim that
per-keystroke glamour rendering is too slow should be based on a benchmark with realistic
note sizes rather than assumed.

### 2. Fix paste properly

Add an explicit `tea.PasteMsg` handler with per-context behavior:

- **Editor (textarea):** insert at the cursor, replace an active selection, mark dirty,
  update the preview source, and schedule one auto-save debounce.
- **Search input:** normalize line breaks to spaces, insert the whole paste into the
  query, and rescore once.
- **View mode:** in the active view, enter edit at the mapped reading position and insert
  there. Never append silently at end; that disconnects the mutation from what the user
  is looking at. Archived/deleted notes stay read-only and show a short toast.
- **List:** in the active view, create a new note from a non-blank paste (first non-blank
  line as the initial title) and open it for editing — the NV capture path. Archived and
  deleted lists stay read-only and show a short toast instead of silently dropping text.
- `tea.PasteMsg` is already one atomic message, including multi-line content; no per-line
  suppression mechanism is needed. Test that it creates one undo unit and one debounce.

### 3. Real selection: keyboard-first, actionable

Build on the existing `ui.SelectionState` rendering machinery, but store editable
selection endpoints in source coordinates (logical line plus rune offset) and map them to
terminal cells only at the render edge. A screen-row selection cannot survive wrapping or
resize correctly.

- **Keyboard selection in edit mode:** shift-arrows/shift-home/end extend from the cursor
  when the terminal reports those modifiers. Provide a discoverable fallback such as
  `alt+s` to set/clear the anchor and let ordinary movement extend it. A bare `v` cannot
  enter selection in this modeless editor — it must keep typing the letter `v`.
- **Actions on selection:** keep the modeless editor contract: `alt+c` copies, `alt+x`
  cuts, backspace/delete removes, and typing or paste replaces. Never claim bare
  `y`/`d`/`x`/`p`; printable keys belong to the note buffer.
- **Mouse selection stays**, still auto-copies. In view mode, a click enters edit mode
  first (§1) and a click-drag begins a selection there; within edit mode it sets the
  same selection state the keyboard uses, so drag-then-delete works.
- **Select-all:** use a non-conflicting registered chord such as `alt+a`. `ctrl+a` already
  means line-start in the textarea and should not silently change meaning.
- Text-edit **undo**: once selections can delete text, accidental data loss gets easy.
  Add a bounded content-snapshot undo/redo ring per note. Group by editing operation and
  short typing bursts — paste and delete-selection are each one unit — not by the
  persistence debounce. Use `ctrl+z` for undo and `ctrl+y`/`ctrl+shift+z` for redo; bare
  `u` must type. Cap both entry count and retained bytes. Vim mode keeps vim's own undo.

Every new chord must be registered in `internal/keymap/bindings.go`, exposed through
`Commands()` for footer/palette discoverability where appropriate, and tested in the
`notes-editor` context. Modifier tests must include combined modifiers, not just key
string fixtures.

### 4. td integration: use the library, keep the link

- **Task creation API:** replace `exec.Command("td", "create", ...)` and stdout scraping
  with a narrow public td application package that owns the same validation, defaults,
  session attribution, and logged write path as `td create`. `td/pkg/notes` does not
  expose task creation today; Sidecar must not import td internals or recreate CLI rules.
- **Persist a first-class link:** td notes have no metadata field today. Add a td-owned
  note↔issue relation and expose it through td's public package and CLI; do not hide a
  `task: td-…` convention inside body text. The relation must be queryable in both
  directions so an agent can find notes for a task without Sidecar. Sidecar renders
  linked-task status in the header/info modal and uses the established
  `app.OpenIssueInTD(taskID)` path to open it.
- **External-change signal:** reuse `internal/livewatch` and a shared td-store target
  helper instead of polling. SQLite writes commonly land in `issues.db-wal`, so watch
  the resolved database directory as `issueview.StoreTargets` already does, coalesce the
  burst, and re-read asynchronously only while Notes is focused. Do not resolve paths or
  start watchers in `Init()`/the synchronous part of `Start()`.
- **External-change conflict:** record the editor's base `UpdatedAt` when loading a note.
  A clean buffer adopts changed content while preserving its reading/edit position. A
  dirty buffer shows a persistent "Changed outside Sidecar" state with Reload and Keep
  Mine/compare choices; it is never overwritten silently. Watch notification alone is
  not enough because auto-save can race an external write: add an atomic
  update-if-unchanged operation in td's notes API (or an equivalent revision contract)
  and surface a conflict outcome instead of last-writer-wins data loss.

This is dependency-ordered cross-repository work: land and release the td application API,
relation migration, agent-facing commands, and optimistic update contract before bumping
Sidecar to consume them. A local `go.work` proof is useful but is not release proof.

### 5. Search polish (keep the NV soul)

The enter-to-open-or-create loop is the plugin's best feature; keep its semantics
exactly. Improve mechanics:

- Rune-wise (and case-folded) fuzzy matching; fix the byte-wise scorer.
- Make backspace rune-safe and render a real empty-result/create affordance rather than
  falling back to the unfiltered list.
- Return match spans from the matcher and highlight them in the list; do not rescan the
  rendered label with a second matching algorithm.
- Score = fuzzy score + a bounded recency boost (recently-updated notes float up), then
  tie-break deterministically by `UpdatedAt` and note ID.
- Cache normalized title/content per note per load, then rescore on each query. Do not add
  an incremental index until realistic note counts show this O(notes × text) pass is a
  problem; record the measurement that would trigger it.

### 6. Correctness sweep (do first, small, independent)

- Rune- and cell-safe truncation in the view (`view.go:311-314`) using `ansi.Truncate` or
  the existing `ui` helpers rather than byte slicing.
- Make title authority explicit. Recommended: preserve td's separate `Note.Title` on
  content-only saves, use the NV query/first line as the title only when creating a note,
  and add an explicit rename action. At minimum, never silently replace a title written
  by `td note edit --title` when Sidecar auto-saves the body.
- Stop `loadNoteIntoEditor` resetting scroll/cursor to end-of-note on every list move;
  remember per-note view scroll, edit cursor, and edit viewport for the session.
- Unify the user-visible wheel contract across view/edit/inline: smooth movement, no
  focus activation, and no work/repaint at a boundary. The tmux editor still owns its
  specialized mouse-reporting semantics; do not translate its wheel into textarea rules.
- **Scrollbars.** Add scrollbars to the note list, the rendered view, and the edit
  view using the existing shared `ui.RenderScrollbar` (`internal/ui/scrollbar.go`) —
  reuse, not new machinery. All three must draw from the same viewport math as the
  unified layout function so the thumb position is honest across mode switches.
- Apply `plugin.IsStale` to every async note result that carries an epoch before changing
  UI state or loading again. Async commands capture immutable epoch/root/store/service
  values before returning; they never dereference a later project's `p.ctx` or `p.store`.
  `TaskCreatedMsg` currently drops `Epoch` on its error path and is one concrete symptom.
- Surface task-creation and optional-archive failures to the user rather than only logging
  them. Do not ignore archive errors or report one combined action as successful when it
  only partly completed.
- Replace predictable `sidecar-note-<id>.md` exports with uniquely created `0600` temp
  files and retain cleanup on every success, error, cancellation, and stale-result path.

## Phasing

Each phase is shippable alone and ordered so later phases build on earlier seams.

1. **Correctness + one layout contract.** Fix paste persistence, rune-safe search/delete
   and truncation, title preservation, async staleness/capture, and user-visible task
   errors first. Introduce one layout value (wrap column, margins, content height,
   scrollbar) used by view and edit; carry cursor/scroll across the mode switch; mouse
   selection no longer swaps renderers. This removes existing data-loss paths before
   expanding the editor.
2. **Markdown view steel thread.** Add a mapping-capable render result, prove one ordinary
   paragraph click/scroll → source cursor journey, then make glamour the default view.
   Add the `m` raw/render toggle and only add a feature flag if rollout needs it.
3. **Selection + undo/redo.** Add source-coordinate keyboard/mouse selection, replacement
   semantics, selection overlay, and bounded operation-based history. Add syntax spans
   only after the authoritative editor layout can support them without a shadow renderer.
4. **td integration, dependency ordered.** (a) td public task service, note↔issue relation,
   CLI queries, and optimistic note update; (b) td release and Sidecar dependency bump;
   (c) Sidecar task/link UI and livewatch refresh/conflict journey.
5. **Search polish.** Rune/case-folded matcher with spans, deterministic recency scoring,
   normalized-text cache, and measured performance.

## Explicit non-goals

- No live-WYSIWYG markdown editing (rendered-while-typing).
- No Sidecar-owned note database or metadata sidecar. td's SQLite and public packages stay
  the source of truth; the deliberate note↔issue relation and optimistic-update contract
  are td-owned schema/API changes.
- No notes CLI surface: sidecar is a presentation layer here; the owning CLI is `td
  note`, which agents already use. New owned capabilities such as linking and conflict
  refusal ship through td's deterministic CLI/public core as well as Sidecar's UI.
- The tmux/vim inline editor is retained, not expanded.

## Risks

- **Glamour vs. selection/mouse math — resolved by design.** View mode is read/scroll
  only; any click into the body drops to source edit mode, where selection operates on
  plain text. No selection ever maps through rendered glamour output.
- **Custom edit-mode renderer.** Syntax highlighting and selection both require
  more than bubbles' textarea natively exposes. A hidden textarea plus a parallel renderer
  would duplicate wrap/viewport/cursor logic and reintroduce drift. Keep textarea drawing
  authoritative for the first selection slice; if syntax spans force replacement, build
  one reusable editor component whose buffer and visual layout are the source of truth.
  Keep the highlighter itself a pure `line -> spans` function.
- **Line-map fidelity.** Block anchors plus wrap math give exact-line landing for
  ordinary prose and lists; accept top-of-block degradation only inside heavily
  reshaped constructs (tables, deep nesting). Chasing exact columns through those is
  not worth it — but the common case (clicking a paragraph in a scrolled note) must be
  exact-line, covered by tests.
- **External edits vs. auto-save.** A filesystem watch is notification, not concurrency
  control. Without an atomic expected-revision update, Sidecar can overwrite an agent's
  edit before the watch event is processed. The td optimistic-update contract is a
  completion requirement for live sync, not optional hardening.

## Verification contract

- Focused tests cover layout geometry, source↔render mapping, Unicode search/backspace,
  paste as one dirty/undo/debounce operation, selection across wraps and resize, stale
  epochs, title preservation, and optimistic-update conflicts.
- `go test ./...` and `go build ./...` pass with `GOWORK=off` against the released td
  version as well as in the local workspace when §4 changes dependencies.
- Real-app proof uses `./scripts/tmux-drive.sh paths` first and its isolated tmux socket
  and Sidecar state tree throughout. Capture at narrow and wide terminal sizes: browse →
  scroll → click into edit → select/replace → paste → undo/redo → leave/re-enter. The
  content's left edge and the clicked source line stay coherent across every transition.
- In a second process, run `td note edit` while Notes is (a) clean and (b) locally dirty.
  The clean view updates without losing position; the dirty case surfaces a durable
  conflict and neither side is silently overwritten. Also prove linked-task open/status
  and agent-side link discovery through the released td CLI.
- Resize and wheel proof includes the tmux editor but preserves its mouse-reporting
  semantics. Always stop `tmux-drive.sh`; never touch the default tmux server.
