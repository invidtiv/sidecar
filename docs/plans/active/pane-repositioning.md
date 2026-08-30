# Pane Repositioning — move mode, the layout button, and `sidecar layout move`

**Status:** agreed, not started. **Tracking:** `td-2ec104`.

One sentence: **a pane you have opened should be movable — by keyboard from where you are standing, by mouse from a button in its own header — and by an agent through the same planner.**

Today a pane's position is decided once, at open time, by `PlanOpen`'s auto rule or by an explicit `--at` cell, and it is never decided again. The only structural edits a user can make afterwards are close (`x`, the header `×`) and resize (divider drag, `+`/`-` on the sidebar seam). If the auto rule stacked the diff under the issue and you wanted it beside the terminal, the recourse is to close it and re-open it somewhere else — which loses the pane's own state, and on a Shell leaf is not even the same thing twice. That is the gap this plan closes.

## The model: extract and reinsert

A move **pulls the leaf out of the tree and grafts it back at the destination**, exactly as if it had been opened there. The sibling takes the removed leaf's place and its split collapses; the destination is planned against the tree that remains.

```
before                after: move the focused pane right
+-------+-------+     +-----+-----+-----+
|       |   B   |     |  A  |  B  |  C  |
|   A   +-------+     |     |     |     |
|       |  [C]  |     |     |     |     |
+-------+-------+     +-----+-----+-----+
```

The layout can therefore gain and lose columns, which is the point: a move is how a 2×2 becomes three columns and back. The alternative — swap-with-neighbour — preserves shape and is cheaper to reason about, but it cannot express the rearrangement people actually want, and it would be a second placement grammar sitting beside `PlanOpenAt`'s. Extract-and-reinsert reuses that grammar wholesale: the same `Cell` addresses, the same 4×4 caps, the same per-kind floors, the same refuse-don't-squeeze law.

**Addresses are read against the tree as it stands before the move.** This matters and is easy to get wrong. If the focused pane is at `2.1` and you ask for `1.2`, `1.2` means the cell you can see at `1.2` right now — not the cell that will be at `1.2` after the source column collapses. The planner removes first and translates the requested address into the post-removal tree itself; callers never do that arithmetic. A destination that stops existing once the source is removed (asking a one-pane column to accept its own occupant) is a no-op, reported as one.

## Current state this builds on

Verified in the tree at the time of writing. Most of what a move needs already exists; the missing piece is a single planner function and the three surfaces' bindings.

- **`internal/panelayout` owns structure and the grid vocabulary.** `Grid`/`GridOf` project a tree onto columns-of-stacked-panes, `Cell`/`ParseCell` address `col.row` (1-based), `MaxGridColumns`/`MaxGridRows` are both 4, and `GridColumnCapMessage`/`GridRowCapMessage`/`LiveCapMessage` are the refusal strings a caller shows. `PlanOpenAt` is the explicit-cell planner and its resolution rules (occupied cell inserts and pushes down, one row past the end appends, one column past the end opens a column, anything further refuses) are the rules a move must match.
- **The mutation primitives are `SplitLeaf`/`ApplyPlan` and `Close`.** `Close` splices the sibling over the parent and returns the new focus. **There is no `Move`, `Swap`, or `Reparent` today** — this is the one genuinely new piece of structure code.
- **`internal/paneframe` owns presentation and hit ordering.** `RegionSink` already has `Leaf`, `Divider`, `Tabs`, `Title`, `Close`, `Body`, and `RegisterRegions` documents the one order that makes the visible thing the clickable thing. `paneframe.go`'s own comment already reserves the title as a future drag target ("renaming it today, dragging the leaf by it later").
- **The header `×` is shared chrome.** `ui.ReserveHeaderClose(width)` returns `{TabsWidth, CloseCol, CloseW, Width}` and `ui.ComposeHeaderClose` joins the padded tab row to the button. It reserves **one** control, and a row too narrow to hold it whole draws none. Both Workspaces surfaces and the app deck compose their headers through it.
- **Three hosts bind to the frame.** `internal/plugins/workspace` (`doc_panes.go`, `content.go`), `internal/overview` (`workspaces.go`, `pane_host.go`), and `internal/app/content_deck.go` for the plugin decks (gated on `features.PluginContentPanes`). Each is the sole `RegisterRegions` caller for its surface.
- **Zoom exists only as a fallback.** `LayoutTree` returns `Layout{Zoomed: true}` when the tree does not fit the box and gives the focused leaf everything. There is **no user-facing zoom state** — nothing requests it, nothing persists it, no key reaches it.
- **The agent surface is further along than expected.** `sidecar layout get [--json]` reports the grid, and `sidecar layout apply --spec` already accepts a **full layout** that replaces the screen, carrying live leaves by tmux session and refusing to destroy one. `internal/layoutapply` runs it all-or-nothing against a trial tree with per-pane ack verdicts. So an agent can already rearrange panes today by read-modify-write; see "Parity" below for what that means for `layout move`.

## Settled decisions

### 1. `M` enters move mode; `h/j/k/l` and the arrows move; `esc`/`enter` leave it

A transient mode rather than a chord tier or four direct chords. One key to discover, one mnemonic to remember, and repeated moves cost one keystroke each — which is what rearranging actually involves. It also sidesteps the problem that killed `ctrl+w` in the deprecated windowing plan: mode is enterable only from browse state, so nothing new competes with a live PTY for a keystroke.

**Why `M` and not `m`.** `m` is free in `workspace-doc`, `workspace-issue`, `workspace-note`, `workspace-resource`, `workspace-diff`, `workspace-preview`, `global-workspaces-issue`, `global-workspaces-note`, `global-workspaces-resource` and `global-workspaces-diff` — but it is **taken in exactly the two contexts that would break parity**: `global-workspaces-doc` spends it on `render`, and `global-workspaces` (the context a focused primary terminal reports on the Sessions surface) spends it on `merge-workflow`. One key must mean one thing in every pane, so a key that works in eight pane contexts and not the other two is not a candidate. `M` is bound nowhere except `git-status` (`stash-pop`), which is a plugin browse context and never a pane leaf.

Bindings land in the pane-leaf contexts only — `workspace-preview`, `workspace-doc|issue|note|diff|resource`, `global-workspaces`, `global-workspaces-doc|issue|note|diff|resource` — and **not** in the plugins' own browse contexts (`file-browser-tree`, `git-status`, `notes-list`, …). Standing in a plugin's list is not standing in a pane; move mode is entered from the pane you mean to move. On a focused passive leaf inside a plugin deck the leaf already reports the `workspace-*` pane context, so the plugin decks inherit the binding for free, exactly as they did for the pane switcher.

While move mode is live it owns the keyboard for the surface, at the same precedence a focused content's overlay owns it. Its keys:

| Key | Action |
|---|---|
| `h` / `left` | Move to the column left; open a new column if there is none |
| `l` / `right` | Move to the column right; open a new column if there is none |
| `j` / `down` | Move down one row within the column |
| `k` / `up` | Move up one row within the column |
| `esc` / `enter` / `M` | Leave move mode, keeping every move made |

There is no undo key; a move is its own inverse, and `esc` committing rather than reverting is what makes the mode feel like dragging and not like a transaction.

**The direction rule** — how a keypress becomes a `Cell`, given a focused leaf at `(c, r)` in the pre-move grid:

- `j` → `(c, r+1)`, `k` → `(c, r-1)`. Out of range at either end is a no-op.
- `l` → `(c+1, last+1)` where `last` is the destination column's current row count: the pane appends at the bottom of the column to its right. When `c` is the last column, `l` opens column `c+1` — but only if the source column holds more than one pane (otherwise the move is the identity, since the source column collapses as the new one opens) and `ColumnsAtCap` is false.
- `h` → the same, mirrored, toward `c-1`; at `c == 1` it opens a new leftmost column under the same two conditions.

Appending at the bottom of the destination column, rather than inserting at the source's row index, is the rule that makes repeated `l` presses walk a pane across the layout predictably instead of shuffling the destination's occupants.

**Every refusal is spoken.** A move that the caps or the floors decline shows the existing message (`GridColumnCapMessage`, `GridRowCapMessage`, or the fit refusal) as a toast on the surface. A key that silently does nothing in a mode built for direct manipulation is the worst outcome available.

**Chrome while moving.** The moving leaf takes a distinct border state — a new `paneframe` leaf border state beside focused and interactive, decided in `paneframe` once so both Workspaces surfaces and the deck get it by construction — and the footer swaps to the mode's own hints via `Commands()`.

### 2. A layout button in the pane header, left of the `×`

The second control on the header's right edge, opening the reposition modal for **that** leaf. It is what makes the feature discoverable: a keyboard mode nobody knows about is a feature nobody has.

`ui.ReserveHeaderClose` generalises to `ui.ReserveHeaderControls(width, controls ...HeaderControl)`, returning the tab strip's width and each control's column, with an explicit **drop order** as the row narrows: the layout button is dropped first, the close `×` last. A clipped control is a target whose meaning cannot be recovered, so the existing all-or-nothing rule per control stands. `ReserveHeaderClose` becomes a one-line wrapper so no caller has to change in the same commit that adds the reserve.

The glyph is `⊞` (U+229E), drawn through `ui.ResolveButtonStyle` with the same one-cell padding as the `×`, so the pane header does not invent a third button look. **This needs a width assertion**: the reserve arithmetic assumes a one-column glyph, so a test must pin `ansi.StringWidth` of the rendered label, and the plan accepts an ASCII fallback if the terminal-width answer is ambiguous rather than shipping a glyph that reflows the tab strip.

`RegionSink` gains `Layout(node *panelayout.Node, hit Box)`, registered **after `Title` and before `Close`** — the same reasoning `RegisterRegions` already documents for the close button, one rung earlier. Each host implements it beside its existing `registerPaneCloseRegion`, with the same hover tracking.

### 3. The modal repositions and zooms, and does nothing else

A miniature of the current layout with the pane it was opened from highlighted. Move it with `h/j/k/l`/arrows, or click a destination cell. `z` zooms it. `enter` commits, `esc` cancels.

```
  Move pane · src/main.go

  +-----------+-----------+
  |           |  README   |
  |   shell   +-----------+
  |           | [ doc  ]  |
  +-----------+-----------+

  hjkl move   z zoom   enter done   esc cancel
```

**`esc` in the modal reverts** — the opposite of move mode's `esc`, and deliberately so. The modal shows a preview of a proposed arrangement, so it gets a cancel; the mode moves the real thing under you, so it does not. This is the one place the two entry points differ, and it is the difference between a dialog and direct manipulation.

The modal is built on `internal/modal` with the miniature as a `Custom` section. It does not close, split, resize, or rename panes: every one of those already has a key and a hit region, and a second home for them is how two answers to one question get built. (Note that the modal-redesign plan is in flight; this modal follows its three rules — one surface, columns not wrapped lines, minimal form elements — rather than the current visual language.)

**Zoom is new state, and this plan owns it.** Today `Layout.Zoomed` is only the doesn't-fit fallback. This adds a per-surface `zoomLeaf int`: when set and the leaf exists, `LayoutTree` gives that leaf the whole box and reports `Zoomed: true` — the same code path the fallback already takes, which is what makes it cheap. One rule, not two: a requested zoom and a forced zoom produce the same `Layout`. Zoom is **view state, not persisted** — it is not written to the pane layout in `shells.json` and does not survive a restart — and it clears when the zoomed leaf closes or the tree changes shape. A user-facing zoom key is the obvious follow-on and is deliberately **out of scope here**: `z` is taken in both diff-pane contexts (`toggle-diff-scope`), so it needs its own parity decision, and the modal is a complete way to reach zoom in the meantime.

### 4. All three paneframe hosts, or it is a bug

Project Workspaces, the global Sessions browser, and the plugin content decks. The first two are the standing parity rule; the deck inherits the pane-leaf key bindings and the header chrome by construction, so the marginal cost there is its `RegionSink.Layout` implementation and nothing else. The work lands in `panelayout`, `paneframe` and `ui`, with exactly one binding file per host, which is what makes "all three" the cheap answer rather than the thorough one.

### 5. Parity: `sidecar layout move`, as a verb over a capability agents already have

Sidecar owns the pane layout, so by the ownership test the capability needs a scriptable path — and the honest finding is that **it already has one**. `sidecar layout get --json` plus `sidecar layout apply --spec` is a complete read-modify-write rearrangement, all-or-nothing, refusing to destroy a live terminal. Nothing an agent wants to do with pane position is impossible today.

What is missing is ergonomics: expressing "put the diff below the issue" as a whole-layout spec means reconstructing every pane on screen, and every pane you reconstruct is a pane you can get wrong. So `layout move` is added as a **single-call verb over the same planner**, not as a new capability:

```
sidecar layout move 2.1 --to 1.2          # by cell
sidecar layout move --focused --to right  # by direction, the move-mode rule
sidecar layout move 2.1 --to 3            # append to a column; opens one past the end
```

It routes through `internal/layoutapply` as a third mode beside batch and spec, gets the same never-queue rule, the same exit codes (`0` moved, `2` usage, `3` no instance, `4` declined with the reason verbatim), and the same ack shape carrying the landed cell. The `--to` direction words are exactly the direction rule from decision 1, so the CLI and the keyboard cannot drift into two answers.

The planner itself — `panelayout.PlanMove` — is state-free and takes a tree, a leaf ID and a destination. The keys, the modal and the CLI are three callers of one function. This is the seam that makes the CLI verb a thin addition rather than a parallel implementation.

### 6. Gated on a feature flag until it is proven

`pane_move`, default off, checked in exactly two places: the key binding's availability and the header control's reserve. A flag here is cheap because it is the same shape as `plugin_content_panes` and because the header chrome change is visible to every user of every surface on the first frame after install.

## Unresolved questions

- **Does a move mode belong on a live `Shell` or `Primary` leaf at all?** The planner allows it — a live leaf's position is geometry, not session identity, so moving one costs a tmux resize and nothing else. But the primary terminal is the surface's own content and lives at `1.1` by every convention in the tree, and a layout with the primary at `3.2` may read as broken rather than arranged. **Proposed answer, to confirm during M1:** allow it, because refusing would be the only place the grid vocabulary has a pane it cannot address, and the `--spec` path already permits any primary position. Revisit if the proof run looks wrong.
- **Do ratios travel with a moved pane?** `SplitLeaf` creates every split at `Ratio: 50`, so a pane dragged to 70% and then moved lands back at 50/50. That is the existing behaviour of every structural edit and is probably right — a move is a re-open, and a re-open does not remember a ratio it never had. Recorded so it is a decision rather than a surprise.
- **Does the miniature share code with `cli/layout_render.go`'s ASCII sketch?** Both draw the same `Grid` projection; one is plain text for a terminal, one is styled for a modal. **Proposed: no shared renderer, one shared projection.** Two renderers over `GridOf` is fine; a third *projection* is not. Confirm when the miniature is written.

## Work sequence

### M0 — `PlanMove` and the structural primitive

The whole feature in one state-free function plus one tree edit, testable with no surface at all.

- `panelayout.PlanMove(root *Node, leafID int, dest Cell) (MovePlan, string)` — remove the leaf from a trial copy, translate `dest` from the pre-move grid into the post-removal tree, then plan the insert with `PlanOpenAt`'s rules. Returns the visible reason on refusal, empty on success. A destination that resolves to the leaf's own position returns a no-op verdict, distinct from a refusal.
- `panelayout.MoveDirection(root *Node, leafID int, dir Direction) (Cell, bool)` — decision 1's direction rule, so the keys, the modal and `--to right` compile the same answer.
- `panelayout.ApplyMove(root *Node, plan MovePlan) (*Node, int)` — `Close` then `ApplyPlan`, splicing the same `*Node` (content identity is preserved; nothing is re-created) and returning the new focus.
- **Proof:** table tests over the grid vocabulary — every direction from every cell of a 1×1, 2×1, 2×2, 1×4 and 4×1 layout; both caps; the tree that escapes the grid (refused with the existing reason); the no-op cases; and a fit test proving a move that would break the floors is refused with the tree byte-for-byte untouched.

### M1 — Move mode on the two Workspaces surfaces

- `M` bindings in the ten pane-leaf contexts, plus `Commands()` entries so the footer and `?` find them; the mode's own keys registered as a context that owns the keyboard while it is live.
- The moving-leaf border state in `paneframe`, and the toast path for every refusal string.
- The `pane_move` flag.
- **Proof:** `internal/keymap` parity test asserting both surfaces bind `M` to `move-pane` in every pane context and that the navigation, search and editor contexts are untouched; a host test driving the real key ladder; and an isolated `tmux-drive.sh` run (`paths` confirmed first, both axes private) carrying a pane across a 2×2 and back.

### M2 — The header button and the modal

- `ui.ReserveHeaderControls` with the drop order, `ReserveHeaderClose` as its wrapper, and the glyph width assertion.
- `RegionSink.Layout` in `paneframe.RegisterRegions` at its documented rung, implemented on all three hosts with hover.
- The modal: miniature `Custom` section, keyboard and click targeting, `enter` commit / `esc` revert.
- Zoom state: `zoomLeaf` per surface, `LayoutTree` honouring it, cleared on close and on tree change, absent from persistence.
- **Proof:** a `paneframe` test pinning the region registration order with the new rung (the highest-probability regression in this plan); a narrow-header test proving the layout button drops before the `×`; modal tests for commit and revert; and a `tmux-drive.sh` run clicking the button and moving a pane with the mouse.

### M3 — The plugin decks

- `RegionSink.Layout` on `internal/app/content_deck.go` and whatever its hover path needs; the bindings and chrome arrive with the pane contexts the leaves already report.
- **Proof:** a deck test driving move mode from a focused leaf inside a plugin deck, and a regression test that the entry is absent when `PluginContentPanes` is off.

### M4 — `sidecar layout move`

- The third mode in `internal/layoutapply` over `PlanMove`, the CLI command with `--focused`, cell and direction forms, `--sessions`/`--shell`/`--project` destinations, and the ack carrying the landed cell.
- `--help`, examples, and the `AgentDoc` entry, plus a note in `sidecar agents` output.
- Flip `pane_move` on by default.
- **Proof:** CLI tests proving the verb resolves to the same `MovePlan` the keys and the modal produce; a host test for the decline path; a gendoc refresh.

### M5 — Document

- `.claude/skills/keyboard-shortcuts/SKILL.md`: the `M` assignment table, why `m` was rejected, and the rule that move-mode keys are bound in pane-leaf contexts only.
- `.claude/skills/ui-features/SKILL.md` and `drag-pane/SKILL.md`: the header control reserve and its drop order, and the new region rung.
- `AGENTS.md` / the layout reference: `layout move` beside `layout get` and `layout apply`.

## Acceptance evidence

- `PlanMove` table tests covering every direction from every cell of five layout shapes, both caps, the escaped grid, the no-ops, and the floors refusal leaving the tree untouched.
- A keymap parity test holding both Workspaces surfaces to `M` in every pane context, and asserting the input contexts are unchanged.
- A `paneframe` region-order test including the new `Layout` rung over a nested tree.
- A narrow-header reserve test proving the drop order.
- Isolated `tmux-drive.sh` transcripts for M1 and M2, with `paths` confirmed before each run.
- A CLI test proving `layout move` and move mode compile the same plan from the same tree.
