# Pane Repositioning — move mode, the layout button, and `sidecar layout move`

**Status:** agreed, not started. **Tracking:** `td-2ec104`.

One sentence: **a pane you have opened should be movable — by keyboard from where you are standing, by mouse from a button in its own header — and by an agent through the same planner.**

Today a pane's position is decided once, at open time, by `PlanOpen`'s auto rule or by an explicit `--at` cell, and it is never decided again. The only structural edits a user can make afterwards are close (`x`, the header `×`) and resize (divider drag, `+`/`-` on the sidebar seam). If the auto rule stacked the diff under the issue and you wanted it beside the terminal, the recourse is to close it and re-open it somewhere else — which loses the pane's own state, and on a Shell leaf is not even the same thing twice. That is the gap this plan closes.

## The model: extract and reinsert

A move **pulls the leaf out of the tree and grafts it back at the destination**, placed by the same grid rules an open at that cell would use. The sibling takes the removed leaf's place and its split collapses; the destination is planned against the tree that remains. It is not quite a close-and-re-open: the leaf is spliced as the same `*Node` with the same leaf ID, so the host-owned content and `termpanes.Deck` state keyed by that ID — tabs, scroll position, selection, and any live local or remote terminal — travel with it instead of being reconstructed. Its share of the box travels too, per decision 8.

```
before                after: move the focused pane right
+-------+-------+     +-----+-----+-----+
|       |   B   |     |  A  |  B  |  C  |
|   A   +-------+     |     |     |     |
|       |  [C]  |     |     |     |     |
+-------+-------+     +-----+-----+-----+
```

The layout can therefore gain and lose columns, which is the point: a move is how a 2×2 becomes three columns and back. The alternative — swap-with-neighbour — preserves shape and is cheaper to reason about, but it cannot express the rearrangement people actually want, and it would be a second placement grammar sitting beside `PlanOpenAt`'s. Extract-and-reinsert factors out and reuses the structural part of that grammar: the same `Cell` addresses, occupied-cell inserts, 4×4 caps, per-kind floors, and refuse-don't-squeeze law. Moving to a new leftmost column is the one deliberate extension because `PlanOpenAt` can append a right column but a `Cell` cannot name the space before `1.1`.

**Addresses are read against the tree as it stands before the move.** This matters and is easy to get wrong. If the focused pane is at `2.1` and you ask for `1.2`, `1.2` means the cell you can see at `1.2` right now — not the cell that will be at `1.2` after the source column collapses. The planner removes first and translates the requested address into the post-removal tree itself; callers never do that arithmetic. A destination that stops existing once the source is removed (asking a one-pane column to accept its own occupant) is a no-op, reported as one.

## Current state this builds on

Verified in the tree at the time of writing. Most of what a move needs already exists; the structural missing piece is one move planner, followed by the three surfaces' bindings.

- **`internal/panelayout` owns structure and the grid vocabulary.** `Grid`/`GridOf` project a tree onto columns-of-stacked-panes, `Cell`/`ParseCell` address `col.row` (1-based), `MaxGridColumns`/`MaxGridRows` are both 4, and `GridColumnCapMessage`/`GridRowCapMessage`/`LiveCapMessage` are the refusal strings a caller shows. `PlanOpenAt` is the explicit-cell planner and its resolution rules (occupied cell inserts and pushes down, one row past the end appends, one column past the end opens a column, anything further refuses) are the rules a move must match.
- **The mutation primitives are `SplitLeaf`/`ApplyPlan` and `Close`.** `Close` splices the sibling over the parent and returns the new focus. **There is no `Move`, `Swap`, or `Reparent` today** — this is the one genuinely new piece of structure code.
- **`internal/paneframe` owns presentation and hit ordering.** `RegionSink` already has `Leaf`, `Divider`, `Tabs`, `Title`, `Close`, `Body`, and `RegisterRegions` documents the one order that makes the visible thing the clickable thing. `paneframe.go`'s own comment already reserves the title as a future drag target ("renaming it today, dragging the leaf by it later").
- **The header `×` is shared chrome.** `ui.ReserveHeaderClose(width)` returns `{TabsWidth, CloseCol, CloseW, Width}` and `ui.ComposeHeaderClose` joins the padded tab row to the button. It reserves **one** control, and a row too narrow to hold it whole draws none. Both Workspaces surfaces and the app deck compose their headers through it.
- **Three hosts bind to the frame.** `internal/plugins/workspace` (`doc_panes.go`, `content.go`), `internal/overview` (`workspaces.go`, `pane_host.go`), and `internal/app/content_deck.go` for the plugin decks (gated on `features.PluginContentPanes`). Each is the sole `RegisterRegions` caller for its surface.
- **Zoom exists only as a fallback.** `LayoutTree` returns `Layout{Zoomed: true}` when the tree does not fit the box and gives the focused leaf everything. There is **no user-facing zoom state** — nothing requests it, nothing persists it, no key reaches it.
- **The agent surface already supports full-layout replacement.** `sidecar layout get [--json]` reports the grid, and `sidecar layout apply --spec` accepts a **full layout** that replaces the screen, carrying live leaves by tmux session and refusing to destroy one. `internal/layoutapply` runs it all-or-nothing against a trial tree with per-pane ack verdicts. So an agent can already rearrange panes by read-modify-write; see "Parity" below for what that means for `layout move`.
- **`remote-sidecar` makes host identity part of live-pane state.** The branch adds `Host` to `tty.Target` and `termpanes.Target`, makes a remote pane read-only until the user explicitly takes input, and protects interactive remote geometry with a host-aware lease plus generation fencing for queued work. A remote pane is still an ordinary `Primary` or `Shell` leaf in `panelayout`; moving it must preserve the target and control subscription without reopening either, must not resize the remote tmux server while the viewer is in browse state, and must release interactive input ownership before a modal can rearrange it.

## Settled decisions

### 1. `M` enters move mode; `h/j/k/l` and the arrows move; `esc`/`enter` leave it

A transient mode rather than a chord tier or four direct chords. One key to discover, one mnemonic to remember, and repeated moves cost one keystroke each — which is what rearranging actually involves. It also sidesteps the problem that killed `ctrl+w` in the deprecated windowing plan: mode is enterable only from browse state, so nothing new competes with a live PTY for a keystroke.

**Why `M` and not `m`.** `m` is free in `workspace-doc`, `workspace-issue`, `workspace-note`, `workspace-resource`, `workspace-diff`, `workspace-preview`, `global-workspaces-issue`, `global-workspaces-note`, `global-workspaces-resource` and `global-workspaces-diff` — but it is **taken in exactly the two contexts that would break parity**: `global-workspaces-doc` spends it on `render`, and `global-workspaces` (the context a focused primary terminal reports on the Sessions surface) spends it on `merge-workflow`. One key must mean one thing in every pane, so a key that works in ten pane contexts and not the other two is not a candidate. `M` is bound nowhere except `git-status` (`stash-pop`), which is a plugin browse context and never a pane leaf.

Bindings land in the twelve pane-leaf contexts only — `workspace-preview`, `workspace-doc|issue|note|diff|resource`, `global-workspaces`, `global-workspaces-doc|issue|note|diff|resource` — and **not** in the plugins' own browse contexts (`file-browser-tree`, `git-status`, `notes-list`, …). Standing in a plugin's list is not standing in a pane; move mode is entered from the pane you mean to move. On a focused passive leaf inside a plugin deck the leaf already reports the `workspace-*` pane context, so the plugin decks inherit the binding for free, exactly as they did for the pane switcher.

While move mode is live it owns the keyboard for the surface, at the same precedence a focused content's overlay owns it. Its keys:

| Key | Action |
|---|---|
| `h` / `left` | Move to the column left; open a new column if there is none |
| `l` / `right` | Move to the column right; open a new column if there is none |
| `j` / `down` | Move down one row within the column |
| `k` / `up` | Move up one row within the column |
| `esc` / `enter` / `M` | Leave move mode, keeping every move made |

There is no undo key; a move is its own inverse, and `esc` committing rather than reverting is what makes the mode feel like dragging and not like a transaction.

**The direction rule** — how a keypress becomes a `MoveDestination`, given a focused leaf at `(c, r)` in the pre-move grid:

- `j` → `(c, r+1)`, `k` → `(c, r-1)`. Out of range at either end is a no-op.
- `l` → `(c+1, last+1)` where `last` is the destination column's current row count: the pane appends at the bottom of the column to its right. When `c` is the last column, `l` opens column `c+1` — but only if the source column holds more than one pane (otherwise the move is the identity, since the source column collapses as the new one opens) and `ColumnsAtCap` is false.
- `h` → the same, mirrored, toward `c-1`; at `c == 1` it opens a new leftmost column under the same two conditions.

Appending at the bottom of the destination column, rather than inserting at the source's row index, is the rule that makes repeated `l` presses walk a pane across the layout predictably instead of shuffling the destination's occupants.

The planner destination is therefore **not just a `Cell`**. A pre-move cell can express every occupied-cell insert and the one-past-the-end right column, but it cannot distinguish "insert at `1.1`" from "create a new column before column 1." `MoveDestination` represents either a pre-move cell or an outer column edge (`before-first-column` / `after-last-column`). Direction keys compile to that type; the CLI's cell form remains a cell, while `--to left|right` can reach the symmetric outer edges. This keeps the left-edge rule explicit instead of smuggling a zero or another invalid coordinate through `Cell`.

**Every refusal and no-op is spoken.** A move that the caps or the floors decline shows the existing message (`GridColumnCapMessage`, `GridRowCapMessage`, or the fit refusal) as a toast on the surface. A boundary or identity no-op says why (`already at the top`, `that move leaves the layout unchanged`) through the surface's replaceable toast rather than stacking repeated messages. A key that silently does nothing in a mode built for direct manipulation is the worst outcome available.

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

**`esc` in the modal reverts** — the opposite of move mode's `esc`, and deliberately so. The modal shows a preview of a proposed arrangement, so it gets a cancel; the mode moves the real thing under you, so it does not. This is the one place the two entry points differ, and it is the difference between a dialog and direct manipulation. The modal edits a structural clone and records its accepted move destinations; `enter` first replays the entire sequence against a fresh clone of the still-active tree, then replays it against the live tree only if every move still accepts. A changed tree identity/generation or any newly refused step leaves the live tree untouched and reports the stale/refused result. `esc` simply discards the draft. The clone is never installed as the live tree because doing that would replace the node identities whose host-owned state the move promises to preserve.

Opening the modal from an interactive live pane first releases input ownership but leaves the control subscription open; closing the modal returns to browse state. On a remote pane, that same transition releases the geometry lease and fences queued input or resize work before the first draft move. The modal cannot own the keyboard while the PTY or a remote lease still does.

The modal is built on `internal/modal` with the miniature as a `Custom` section. It does not close, split, resize, or rename panes: every one of those already has a key and a hit region, and a second home for them is how two answers to one question get built. (Note that the modal-redesign plan is in flight; this modal follows its three rules — one surface, columns not wrapped lines, minimal form elements — rather than the current visual language.)

**Zoom is new state, and this plan owns it.** Today `Layout.Zoomed` is only the doesn't-fit fallback. This adds zoom state scoped to the active pane tree: when its leaf exists, `LayoutTree` gives that leaf the whole box and reports `Zoomed: true` — the same code path the fallback already takes, which is what makes it cheap. One rule, not two: a requested zoom and a forced zoom produce the same `Layout`. Zoom is **view state, not persisted** — it is not written to the pane layout in `shells.json` and does not survive a restart. It follows the same leaf through a move and clears when that leaf closes or when the host replaces/switches the active pane tree; a bare `zoomLeaf int` that can accidentally match the same numeric ID in another workspace is insufficient. The modal keeps draft zoom beside its draft tree and commits both together. A user-facing zoom key is the obvious follow-on and is deliberately **out of scope here**: `z` is taken in both diff-pane contexts (`toggle-diff-scope`), so it needs its own parity decision, and the modal is a complete way to reach zoom in the meantime.

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

It routes through `internal/layoutapply` as a third mode beside batch and spec, gets the same never-queue rule, the same exit codes (`0` moved or already in the requested place, `2` usage, `3` no instance, `4` declined with the reason verbatim), and the same ack envelope carrying the landed cell. Structured output adds an explicit `unchanged` status/verdict for a no-op instead of calling it moved or overloading `retargeted`. The `--to` direction words are exactly the direction rule from decision 1, so the CLI and the keyboard cannot drift into two answers.

For `--sessions`, the move changes the local viewer's pane tree even when the selected workspace is remote; it does not send a layout mutation to `sidecar host serve`. Destination resolution inherits `remote-sidecar`'s host-scoped rule: an explicit remote row ID may resolve, while the local-only name/session fallback must not bind an ambiguous row from another machine. The acknowledgement names the host-scoped surface it actually changed.

The planner itself — `panelayout.PlanMove` — is state-free and takes a tree, a leaf ID and a destination. The keys, the modal and the CLI are three callers of one function. This is the seam that makes the CLI verb a thin addition rather than a parallel implementation.

### 6. Gated on a feature flag until it is proven

`pane_move`, default off, checked in exactly two places: the key binding's availability and the header control's reserve. A flag here is cheap because it is the same shape as `plugin_content_panes` and because the header chrome change is visible to every user of every surface on the first frame after install.

### 7. Live `Shell` and `Primary` leaves move like any other pane

A live leaf's position is geometry, not session identity: the leaf and the host-owned terminal state keyed by its ID move without reopening the session. So the primary terminal is movable off `1.1` and a split shell is movable anywhere the caps allow.

The alternative — pinning the primary — would make it the one pane in the grid vocabulary that has an address but cannot be sent to one, and `layout apply --spec` already accepts a primary in any column. A rule the agent surface does not enforce is not a rule.

Two consequences to hold: a local live leaf's geometry change goes through the host's existing `syncTerminalGeometry`/resize path after the structural commit, never through an ad hoc resize inside the planner; and `LiveLeafCap` is unaffected because a move creates no live leaf. Remote geometry follows `remote-sidecar`'s ownership contract instead: move mode is unreachable while the PTY owns the keyboard, the header modal releases interactive input and its lease before moving, and a remote leaf in browse state is fitted into its new local box without resizing the remote tmux server. Re-entering interactive mode may claim the lease and assert the new geometry through the existing remote path. In every case the `Host` target and live control subscription survive the move.

### 8. The moved pane carries its ratio

`SplitLeaf` creates every split at `Ratio: 50`, so the naive move drops a pane you had dragged to 70% back to half. It should not: a pane's share of its box is something you set deliberately, and losing it on every move would make rearranging a layout cost a re-drag each time.

The rule, stated precisely because "the pane's ratio" is not a thing the tree stores — a *split* owns a ratio, a leaf does not:

- **At extraction**, the leaf carries the percentage of its parent split it occupied: its parent's `Ratio` when it is the `A` child, `100 - Ratio` when it is `B`. A leaf with no parent split (it was the whole tree) carries nothing.
- **At reinsertion**, the new split's `Ratio` is set so the moved leaf gets that same percentage — the carried value directly when it lands as `A` (a `NewFirst` insert), inverted when it lands as `B`. `MovePlan` therefore carries both the percentage and whether one exists (`CarriedRatio int`, `HasCarriedRatio bool` or an equivalent optional value), and `ApplyMove` sets it after splicing only when present. A missing value must not pass through `ClampRatio(0)` and turn into 15; `SplitLeaf`'s hardcoded 50 stays the right default when there is nothing to carry and for an ordinary *open*.
- **The carry is axis-agnostic.** A leaf that was 70% of a column's width becomes 70% of its new row stack's height. Carrying per-axis instead would mean a pane moved right then down loses the value anyway, which is the behaviour this decision exists to remove.
- **`ClampRatio` and the floors still apply**, unchanged and at the usual time: the carried value is a request, and a 15/85 clamp or a floor that cannot be met resolves it exactly as it resolves a dragged one. Nothing new refuses.
- **Repeated moves carry it forward**, so walking a pane across the layout with four `l` presses preserves the share through all four.

What is *not* carried is the ratio of the split the pane leaves behind: `Close` splices the sibling over the parent, and that split's ratio ceases to exist. That is already true of every close today.

### 9. The modal miniature and the CLI sketch share a projection, not a renderer

`cli/layout_render.go`'s `renderLayoutSketch` already draws an ASCII picture of a layout for terminal output; the modal needs a styled one for a `Custom` section. Two renderers over `GridOf` is fine — they have different targets, different constraints and no shared styling. A third *projection* of the tree is not: both read `Grid` and neither derives columns for itself.

## Work sequence

### Integration baseline — take `remote-sidecar` before surface work

M0 is presentation-neutral and can be built independently. Before M1 changes `internal/features`, `internal/keymap`, `internal/overview`, or terminal geometry handling, land or rebase onto the active `remote-sidecar` work so its host-aware target identity, read-only remote geometry rule, input lease, and queued-work fencing remain the authority. Do not recreate those rules in pane movement. The overlapping files and seams include `internal/features/features.go`, `internal/keymap/bindings.go`, `internal/overview/model.go`, `internal/overview/workspaces.go`, `internal/overview/interactive.go`, `internal/overview/content_deck.go`, `internal/termpanes/deck.go`, and `internal/tty/tty.go`.

### M0 — `PlanMove` and the structural primitive

The whole feature in one state-free function plus one tree edit, testable with no surface at all.

- Factor the structural grid-placement part of `PlanOpenAt` into an internal helper that takes a post-removal tree and a placement destination without applying open-only policy. `PlanOpenAt` keeps its current kind-deduplication, live-cap, and `Primary` refusal checks before calling that helper; `PlanMove` calls the helper after extraction. Calling `PlanOpenAt` directly is incorrect because moving the already-existing `Primary` is allowed while opening another one is not.
- `panelayout.PlanMove(root *Node, leafID int, dest MoveDestination, box Box, floors Floors) MoveOutcome` — remove the leaf from a trial copy, translate a pre-move cell or outer-column edge into the post-removal tree, plan the insert through the shared structural helper, and run `LayoutPanes` against the real surface box before accepting it. `MoveOutcome` distinguishes moved, no-op, and refused and carries the visible refusal reason, avoiding a zero `MovePlan` that ambiguously means either no-op or failure. Its accepted `MovePlan` carries the destination plan **and** decision 8's optional carried ratio, read off the leaf's parent split before the removal.
- `panelayout.MoveDirection(root *Node, leafID int, dir Direction) (MoveDestination, bool)` — decision 1's direction rule, including the otherwise-unrepresentable new leftmost column, so the keys, the modal and `--to right` compile the same answer.
- `panelayout.ApplyMove(root *Node, plan MovePlan) (*Node, int)` — retain the source leaf pointer and ID, `Close` it from the tree, then graft it with the structural plan, set the new split's ratio from the move plan, and return the new focus. Preserving both pointer and ID is the contract that keeps every host's keyed state attached.
- **Proof:** table tests over the grid vocabulary — every direction from every cell of a 1×1, 2×1, 2×2, 1×4 and 4×1 layout; explicit before-first and after-last column moves; both caps; the tree that escapes the grid (refused with the existing reason); translation when removing a row before the destination or collapsing a column before it; moved/no-op/refused outcome separation; a fit test proving a move that would break the floors is refused with the tree byte-for-byte untouched; and the ratio carry: `A`-child and `B`-child extraction, `A`-child and `B`-child landing, an axis change preserving the percentage, four moves in a row preserving it, and the clamp still applying to a carried value outside 15–85.
- Live-leaf coverage belongs here too: a `Primary` move off `1.1` and a `Shell` move, both asserting the leaf pointer and ID are unchanged afterwards and that `LiveLeafCount` is unchanged.

### M1 — Move mode on the two Workspaces surfaces

- `M` bindings in the twelve pane-leaf contexts, plus `Commands()` entries so the footer and `?` find them; the mode's own keys registered as a context that owns the keyboard while it is live.
- The moving-leaf border state in `paneframe`, and the toast path for every refusal string.
- Live-leaf geometry: after each committed local move, ask the host's existing geometry synchronizer to reconcile the new boxes. A browsed remote pane does not assert geometry; no pane-move code calls the remote resize or lease APIs directly.
- The `pane_move` flag.
- **Proof:** `internal/keymap` parity test asserting both surfaces bind `M` to `move-pane` in every pane context and that the navigation, search, terminal-interactive, and editor contexts are untouched; a host test driving the real key ladder; and an isolated `tmux-drive.sh` run (`paths` confirmed first, both axes private) carrying a pane across a 2×2 and back, including one run that moves the primary terminal off `1.1` and one that moves a pane whose ratio was dragged away from 50.

### M2 — The header button and the modal

- `ui.ReserveHeaderControls` with the drop order, `ReserveHeaderClose` as its wrapper, and the glyph width assertion.
- `RegionSink.Layout` in `paneframe.RegisterRegions` at its documented rung, implemented on all three hosts with hover.
- The modal: miniature `Custom` section, keyboard and click targeting, a cloned draft tree plus recorded moves, and an all-or-nothing `enter` revalidation/replay against the current tree / `esc` discard.
- Zoom state scoped to the active pane tree, `LayoutTree` honouring it, preserved through a move of the same leaf, cleared on leaf close or active-tree replacement, and absent from persistence.
- Live-pane entry releases interactive input before opening the modal; for a remote target this also releases the lease and fences queued remote work through the branch's existing path.
- **Proof:** a `paneframe` test pinning the region registration order with the new rung (the highest-probability regression in this plan); a narrow-header test proving the layout button drops before the `×`; modal tests proving commit reuses the live leaf, cancel never swaps in the clone, a stale tree or late refusal commits none of a multi-move draft, zoom cannot leak to another pane tree with the same numeric leaf ID, and interactive entry releases input before any move; and a `tmux-drive.sh` run clicking the button and moving a pane with the mouse.

### M3 — The plugin decks

- `RegionSink.Layout` on `internal/app/content_deck.go` and whatever its hover path needs; the bindings and chrome arrive with the pane contexts the leaves already report.
- **Proof:** a deck test driving move mode from a focused leaf inside a plugin deck, and a regression test that the entry is absent when `PluginContentPanes` is off.

### M4 — `sidecar layout move`

- The third mode in `internal/layoutapply` over `PlanMove`, the CLI command with `--focused`, cell and direction forms, `--sessions`/`--shell`/`--project` destinations, `StatusUnchanged`/`ItemVerdictUnchanged` for an accepted no-op, and the ack carrying the landed cell and host-scoped surface.
- `--help`, examples, and the `AgentDoc` entry, plus a note in `sidecar agents` output.
- Flip `pane_move` on by default.
- **Proof:** CLI tests proving the verb resolves to the same `MovePlan` the keys and the modal produce, that an explicit remote Sessions row changes only the viewer-owned layout, and that an ambiguous local name cannot select a remote row; a host test for the decline path; a gendoc refresh.

### M5 — Document

- `.claude/skills/keyboard-shortcuts/SKILL.md`: the `M` assignment table, why `m` was rejected, and the rule that move-mode keys are bound in pane-leaf contexts only.
- `.claude/skills/ui-features/SKILL.md` and `drag-pane/SKILL.md`: the header control reserve and its drop order, and the new region rung.
- `AGENTS.md` / the layout reference: `layout move` beside `layout get` and `layout apply`.

## Acceptance evidence

- `PlanMove` table tests covering every direction from every cell of five layout shapes, both outer column edges, pre/post-removal address translation, both caps, the escaped grid, distinct moved/no-op/refused outcomes, and the floors refusal leaving the tree untouched.
- Ratio-carry tests covering both child positions at extraction and at landing, an axis change, a repeated walk, and the clamp.
- A live-leaf move test on both `Primary` and `Shell` proving node identity and `LiveLeafCount` survive.
- A remote live-leaf host test proving `Host`, terminal/deck identity, and the control subscription survive a move; browse-state movement performs no remote resize; opening the modal from interactive state releases the remote input/geometry lease first.
- A keymap parity test holding both Workspaces surfaces to `M` in every pane context, and asserting the input contexts are unchanged.
- A `paneframe` region-order test including the new `Layout` rung over a nested tree.
- A narrow-header reserve test proving the drop order.
- Isolated `tmux-drive.sh` transcripts for M1 and M2, with `paths` confirmed before each run.
- A CLI test proving `layout move` and move mode compile the same plan from the same tree, including left/right outer-edge moves and an explicitly host-scoped Sessions row.
