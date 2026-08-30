# Pane Repositioning — move mode, the layout button, and `sidecar layout move`

**Status:** in progress — M0–M2 complete, verified, and independently reviewed; M3–M5 remain. **Tracking:** `td-2ec104`.

One sentence: **a pane you have opened should be movable — by keyboard from where you are standing, by mouse from a button in its own header, and by an agent through the same planner.**

M0–M2 close the human-facing gap: project Workspaces and global Sessions have keyboard move mode, and all three pane hosts have the header button, reposition modal, and scoped zoom. The feature remains default-off while M3 adds keyboard move mode to plugin content decks, M4 adds the direct agent verb and flips the flag on, and M5 updates durable user and agent documentation.

## Implementation status

| Milestone | Status | Current result | Evidence |
|---|---|---|---|
| M0 — structural planner | Complete | `PlanMove`, direction resolution, identity-preserving apply, ratio carry, fit/cap/refusal behavior | `td-c16c3c` closed; commit `48d15888` |
| M1 — Workspace move mode | Complete | `M` plus `h/j/k/l` and arrows on project Workspaces and global Sessions, with shared chrome, persistence, geometry reconciliation, and refusal feedback | `td-7a552b` closed; commit `9743b39f`; isolated keyboard and ratio proof |
| M2 — header modal and zoom | Complete | Shared `⊞`, modal draft/revalidation/commit, mouse targeting, scoped zoom, and input-ownership release on project Workspaces, global Sessions, and plugin content decks | `td-90aae8` closed; commit `9743b39f`; isolated mouse proof; independent review clean |
| M3 — plugin deck move mode | Remaining | Add the direct `M` keyboard journey to focused leaves inside app content decks; header modal and zoom are already present | Not started |
| M4 — `sidecar layout move` | Remaining | Add the single-call CLI verb over `PlanMove`, structured acknowledgements, and default-on rollout | Not started |
| M5 — documentation | Remaining | Update shortcut, UI, drag-pane, agent, and layout references after the final surface lands | Not started |

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

## Current implementation

- **`internal/panelayout` owns structure and the grid vocabulary.** `Grid`/`GridOf` project a tree onto columns-of-stacked-panes; `PlanMove`, `MoveDirection`, and `ApplyMove` implement extract-and-reinsert over the same placement grammar as `PlanOpenAt` while preserving the moved leaf pointer, ID, and carried share.
- **`internal/panereposition` owns shared interaction policy.** Its mode, modal controller, header adapter, structural fingerprint, and host-leaf graft helpers keep direction semantics, stale/refusal handling, and passive/live projection adoption aligned across hosts.
- **`internal/paneframe` and `internal/ui` own shared chrome.** `RegionSink.Layout` is registered after `Title` and before `Close`; `ReserveHeaderControls` reserves the `⊞` and `×` with layout-first drop order, and `ReserveHeaderClose` remains the compatibility wrapper.
- **Project Workspaces and global Sessions own thin host adapters.** Both expose keyboard move mode and the modal, preserve focused/live leaf identity, adopt passive deck projections, persist the committed layout, and invoke their existing terminal geometry synchronizers.
- **Plugin content decks have the M2 header/modal/zoom path.** `internal/app/content_deck.go` exposes `RegionSink.Layout`, app modal routing, mouse targeting, paste absorption, atomic deck adoption, and scoped zoom. Direct `M` keyboard move mode inside a deck remains M3.
- **Zoom is transient tree-scoped view state.** A requested zoom and forced-fit zoom share `LayoutTreeWithZoom`; zoom follows a moved leaf, clears on close or tree replacement, and is never encoded in persisted layouts.
- **`pane_move` is registered and defaults off.** Its availability gates the twelve pane-browse bindings and header reserve; Configuration → Feature Flags exposes it as **Move panes** through a scrolling registry page.
- **The agent surface still uses full-layout read-modify-write.** `sidecar layout get --json` plus `sidecar layout apply --spec` can rearrange a layout today. M4 adds the narrower `sidecar layout move` verb over the implemented planner.
- **Remote host identity and input ownership remain authoritative.** Move code does not call remote resize or lease APIs directly; modal entry releases interactive ownership through the existing path, and browse-state moves do not resize the remote tmux server.

## Settled decisions

### 1. `M` enters move mode; `h/j/k/l` and the arrows move; `esc`/`enter` leave it

A transient mode rather than a chord tier or four direct chords. One key to discover, one mnemonic to remember, and repeated moves cost one keystroke each — which is what rearranging actually involves. It also sidesteps the problem that killed `ctrl+w` in the deprecated windowing plan: mode is enterable only from browse state, so nothing new competes with a live PTY for a keystroke.

**Why `M` and not `m`.** `m` is free in `workspace-doc`, `workspace-issue`, `workspace-note`, `workspace-resource`, `workspace-diff`, `workspace-preview`, `global-workspaces-issue`, `global-workspaces-note`, `global-workspaces-resource` and `global-workspaces-diff` — but it is **taken in exactly the two contexts that would break parity**: `global-workspaces-doc` spends it on `render`, and `global-workspaces` (the context a focused primary terminal reports on the Sessions surface) spends it on `merge-workflow`. One key must mean one thing in every pane, so a key that works in ten pane contexts and not the other two is not a candidate. `M` is bound nowhere except `git-status` (`stash-pop`), which is a plugin browse context and never a pane leaf.

Bindings live in the twelve pane-leaf contexts only — `workspace-preview`, `workspace-doc|issue|note|diff|resource`, `global-workspaces`, `global-workspaces-doc|issue|note|diff|resource` — and **not** in the plugins' own browse contexts (`file-browser-tree`, `git-status`, `notes-list`, …). Standing in a plugin's list is not standing in a pane; move mode is entered from the pane you mean to move. A focused passive leaf inside a plugin deck already reports the appropriate pane context, but the app-level key-routing adapter that starts and owns move mode there remains M3.

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

**Chrome while moving.** The moving leaf takes the shared `paneframe` moving border state beside focused and interactive. The two Workspace surfaces use it today, their footers swap to the mode's hints through `Commands()`, and M3 reuses the same state in the app deck.

### 2. A layout button in the pane header, left of the `×`

The second control on the header's right edge, opening the reposition modal for **that** leaf. It is what makes the feature discoverable: a keyboard mode nobody knows about is a feature nobody has.

`ui.ReserveHeaderControls(width, controls ...HeaderControl)` returns the tab strip's width and each control's column, with an explicit **drop order** as the row narrows: the layout button is dropped first, the close `×` last. A clipped control is a target whose meaning cannot be recovered, so the all-or-nothing rule per control stands. `ReserveHeaderClose` remains a one-line compatibility wrapper.

The glyph is `⊞` (U+229E), drawn through `ui.ResolveButtonStyle` with the same one-cell padding as the `×`, so the pane header does not invent a third button look. The reserve arithmetic assumes a one-column glyph, and the test suite pins `ansi.StringWidth` of the rendered label.

`RegionSink` has `Layout(node *panelayout.Node, hit Box)`, registered **after `Title` and before `Close`** — the same reasoning `RegisterRegions` documents for the close button, one rung earlier. All three hosts implement it beside their close-region binding with the same hover tracking.

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

**Zoom is tree-scoped view state.** When its leaf exists, `LayoutTreeWithZoom` gives that leaf the whole box and reports `Zoomed: true` through the same shape as the doesn't-fit fallback. Zoom is **view state, not persisted** — it is not written to the pane layout in `shells.json` and does not survive a restart. It follows the same leaf through a move and clears when that leaf closes or when the host replaces or switches the active pane tree; scope includes tree identity so the same numeric leaf ID in another workspace cannot inherit it. The modal keeps draft zoom beside its draft tree and commits both together. A standalone zoom key remains deliberately out of scope: `z` is taken in both diff-pane contexts (`toggle-diff-scope`), while the modal is a complete route to zoom.

### 4. All three paneframe hosts, or it is a bug

Project Workspaces, the global Sessions browser, and the plugin content decks share the planner, header chrome, modal controller, scoped zoom, and one `paneframe` binding per host. Project and global surfaces also have keyboard move mode. M3 finishes keyboard parity for the app deck without adding a second planner, compositor, or modal.

### 5. Parity: `sidecar layout move`, as a verb over a capability agents already have

Sidecar owns the pane layout, so by the ownership test the capability needs a scriptable path — and the honest finding is that **it already has one**. `sidecar layout get --json` plus `sidecar layout apply --spec` is a complete read-modify-write rearrangement, all-or-nothing, refusing to destroy a live terminal. Nothing an agent wants to do with pane position is impossible today.

What is missing is ergonomics: expressing "put the diff below the issue" as a whole-layout spec means reconstructing every pane on screen, and every pane you reconstruct is a pane you can get wrong. M4 adds `layout move` as a **single-call verb over the same planner**, not as a new capability:

```
sidecar layout move 2.1 --to 1.2          # by cell
sidecar layout move --focused --to right  # by direction, the move-mode rule
sidecar layout move 2.1 --to 3            # append to a column; opens one past the end
```

It will route through `internal/layoutapply` as a third mode beside batch and spec, use the same never-queue rule and exit codes (`0` moved or already in the requested place, `2` usage, `3` no instance, `4` declined with the reason verbatim), and return the same ack envelope carrying the landed cell. Structured output adds an explicit `unchanged` status or verdict for a no-op instead of calling it moved or overloading `retargeted`. The `--to` direction words use the direction rule from decision 1, so the CLI and keyboard cannot drift into two answers.

For `--sessions`, the move changes the local viewer's pane tree even when the selected workspace is remote; it does not send a layout mutation to `sidecar host serve`. Destination resolution inherits `remote-sidecar`'s host-scoped rule: an explicit remote row ID may resolve, while the local-only name/session fallback must not bind an ambiguous row from another machine. The acknowledgement names the host-scoped surface it actually changed.

The planner itself — `panelayout.PlanMove` — is state-free and takes a tree, a leaf ID, and a destination. Keyboard mode and the modal already call it; M4 makes the CLI the third caller. This seam keeps the CLI verb a thin addition rather than a parallel implementation.

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

### Integration baseline — complete

M1 and M2 landed on the host-aware remote terminal model. `tty.Target` and `termpanes.Target` remain the authority for host identity, browse-state remote geometry stays read-only, and modal entry uses the existing input-release, lease-release, and queued-work fencing paths rather than recreating them in pane movement.

### M0 — `PlanMove` and the structural primitive — complete

Implemented in `48d15888`; `td-c16c3c` is closed.

- The structural grid-placement helper is shared with `PlanOpenAt` without importing open-only `Primary`, deduplication, or live-cap policy into moves.
- `PlanMove` extracts on a trial tree, translates pre-move cells and outer edges after removal, validates fit against the real peer box and floors, and returns distinct moved, no-op, or refused outcomes with visible reasons.
- `MoveDirection` owns the direction rule, including the otherwise-unrepresentable new leftmost column.
- `ApplyMove` retains the exact source leaf pointer and ID, grafts it at the accepted destination, restores its carried share when present, and returns the new focus.
- Table coverage exercises the grid shapes, both outer edges, caps, escaped-grid and floor refusals, address translation, outcome separation, ratio carry and clamp, repeated moves, and unchanged-tree refusal.
- Primary and Shell coverage proves pointer, ID, and `LiveLeafCount` preservation.

### M1 — Move mode on the two Workspace surfaces — complete

Implemented in `9743b39f`; `td-7a552b` is closed.

- The twelve pane-leaf contexts expose feature-gated `M`; the shared move context owns `h/j/k/l`, arrows, `M`, Enter, and Escape while active, and input/plugin browse contexts remain untouched.
- `paneframe` owns moving-leaf chrome; project Workspaces and global Sessions own thin command, toast/flash, persistence, projection-adoption, and geometry-sync adapters.
- Local live leaves reconcile geometry through existing host synchronizers. Browsed remote panes do not assert remote geometry, and move code does not call remote resize or lease APIs.
- Isolated `tmux-drive.sh` proof moved Primary off `1.1` and back and carried a dragged 62% pane share across columns and back, with private tmux and Sidecar state roots.

### M2 — The header button and the modal — complete

Implemented in `9743b39f`; `td-90aae8` is closed.

- `ReserveHeaderControls`, the `ReserveHeaderClose` wrapper, `⊞` width assertion, layout-first drop order, and `RegionSink.Layout` registration order are shared and tested.
- Project Workspaces, global Sessions, and plugin content decks expose the header control, hover, declarative miniature, keyboard and mouse targeting, clone-only draft, stale/refusal prevalidation, identity-preserving replay, cancel, and tree-scoped transient zoom.
- Local and remote live-pane modal entry releases interactive ownership first; remote release retains the control subscription while releasing the geometry lease and fencing queued work.
- Paste is absorbed at the actual project, Sessions, and app dispatch boundaries while the modal owns input. The Feature Flags page scrolls its registry and presents `pane_move` as **Move panes**.
- Independent review is clean. Isolated real-app proof clicked the Shell header `⊞`, clicked the Primary destination in the miniature, observed the stacked draft, committed Shell-over-Primary, and stopped the private driver cleanly.

### M3 — Plugin deck keyboard move mode — remaining

- Reuse `panereposition.Mode` through the app-level key-routing boundary when a passive content-deck leaf is focused; the M2 header modal, zoom, deck adoption, and shared chrome are already present.
- Keep entry absent when `PluginContentPanes` is off and do not let plugin list/input contexts leak into move mode.
- **Proof:** a deck test driving the real app key ladder from a focused content-deck leaf, plus a regression that the entry is absent when `PluginContentPanes` is off.

### M4 — `sidecar layout move` — remaining

- The third mode in `internal/layoutapply` over `PlanMove`, the CLI command with `--focused`, cell and direction forms, `--sessions`/`--shell`/`--project` destinations, `StatusUnchanged`/`ItemVerdictUnchanged` for an accepted no-op, and the ack carrying the landed cell and host-scoped surface.
- `--help`, examples, and the `AgentDoc` entry, plus a note in `sidecar agents` output.
- Flip `pane_move` on by default.
- **Proof:** CLI tests proving the verb resolves to the same `MovePlan` the keys and the modal produce, that an explicit remote Sessions row changes only the viewer-owned layout, and that an ambiguous local name cannot select a remote row; a host test for the decline path; a gendoc refresh.

### M5 — Document — remaining

- `.claude/skills/keyboard-shortcuts/SKILL.md`: the `M` assignment table, why `m` was rejected, and the rule that move-mode keys are bound in pane-leaf contexts only.
- `.claude/skills/ui-features/SKILL.md` and `drag-pane/SKILL.md`: the header control reserve and its drop order, and the new region rung.
- `AGENTS.md` / the layout reference: `layout move` beside `layout get` and `layout apply`.

## Acceptance evidence

### Complete

- `PlanMove` table tests cover every direction from every cell of five layout shapes, both outer column edges, pre/post-removal address translation, both caps, the escaped grid, distinct moved/no-op/refused outcomes, and floor refusal with the tree unchanged.
- Ratio-carry tests cover both child positions at extraction and landing, an axis change, a repeated walk, and clamp behavior.
- Live-leaf tests on `Primary` and `Shell` prove node identity and `LiveLeafCount` survive.
- Host tests prove remote `Host`, terminal/deck identity, and control ownership survive; browse-state movement performs no remote resize; modal entry releases remote input and its geometry lease first.
- Keymap parity tests hold project Workspaces and global Sessions to `M` in every pane context and assert input and plugin contexts are unchanged.
- `paneframe` tests pin the `Layout` region rung over nested trees, and header tests pin layout-first narrow-width drop order and `⊞` width.
- Modal tests prove live identity on commit, clone discard on cancel, all-or-nothing stale or late-refusal behavior, tree-scoped zoom, and input/paste ownership at the real host dispatch boundaries.
- Isolated `tmux-drive.sh` proof used private tmux, state, cache, and config roots for both M1 and M2; the default tmux server was untouched and the driver was stopped.
- Integrated verification passed: `go test ./...`, `go vet ./...`, `go build ./...`, `make lint`, `make fmt-check`, `git diff --check`, and `./scripts/test-tmux-drive.sh`.
- M0, M1, and M2 each passed independent review; their td tasks are closed.

### Remaining

- A plugin-deck test must drive move mode through the real app keyboard ladder and prove the entry is absent when `PluginContentPanes` is off.
- A CLI test must prove `layout move` and keyboard or modal movement compile the same plan from the same tree, including left/right outer-edge moves and an explicitly host-scoped Sessions row.
- Final documentation verification must keep shortcut, UI, drag-pane, agent, and layout references aligned with the shipped default-on feature.
