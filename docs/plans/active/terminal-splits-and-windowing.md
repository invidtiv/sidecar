# Terminal Splits & General Windowing

Two live terminals side by side, created from the same modal that creates shells and
worktrees, placed automatically — and, behind that, the pane tree's placement policy
growing into a small general windowing system that can eventually host any pane kind
from anywhere in Sidecar.

**Relationship to [workspace-windowing-system.md](../deprecated/workspace-windowing-system.md):**
that document remains the architectural authority for the pane tree, floors,
refuse-don't-squeeze, the live/passive leaf distinction, and the compositor — all of
which have since shipped for passive panes (`internal/panelayout`, `internal/paneframe`,
doc/issue/diff/resource leaves, `PlanOpen`, `sidecar open --split`). What never landed
is its live-terminal half (M1/M2: absorbing `terminal_panel.go`, lifting the
one-terminal cap). This plan delivers that half, and **supersedes its interaction
model**: creation is modal-first with automatic placement, not chord-first. Its
`alt+w` chord vocabulary is demoted to a deferred power-user tier, and its "reject
auto-tiling" scope row is reversed — auto-placement *is* the primary interface here,
with the mitigating difference that placement happens only at creation/close time
(one resize per event), never as continuous reshuffling.

## Settled decisions

These came out of design review (mockups: `~/code/tui/mockups/sidecar-splits.tui.yaml`).

1. **Two phases, A then B, nothing thrown away.** Phase A adds a live Terminal leaf
   kind under today's placement rules. Phase B changes only the *insertion policy*
   (fourth pane → 2×2 grid) and widens where splits can be created from. The tree,
   frame, and content seam are shared throughout.
2. **Creation goes through the existing create modal**, extended with new rows:
   Shell, Worktree, **Terminal split**, then later File / Git diff / td issue / Note.
   The modal stays minimal — no filter until the list earns one, list position is
   remembered, no explainer text.
3. **Placement is a segmented button row in the modal: `Auto · Right · Below`**,
   Auto highlighted as default. Clicking a placement button creates immediately with
   no further confirmation; Enter uses Auto. `Auto` follows the grid rules;
   Right/Below map onto the existing `panelayout.ApplyAxisOverride` vocabulary
   (`--split right|below`), so the modal and the CLI share one placement model.
4. **Auto placement rules** (phase A keeps steps 1–3, which are `PlanOpen`'s current
   behavior; phase B adds step 4):
   1. One content pane → splits the primary column vertically (side by side).
   2. Second content pane → the right column splits horizontally (stacked).
   3. Third content pane → stacks on the largest content leaf (today's rule).
   4. **Phase B:** fourth pane → the primary/left column splits too → 2×2 grid.
5. **Sidebar representation is a badge, not a child row.** The workspace's row gets
   a compact layout glyph (`◧◨` for a two-way split, `⊞3`/`⊞4` for more). Rows stay
   1:1 with workspaces; no `⤷` indent — that cue stays reserved for worktree shells.
6. **A split terminal is a peer in the workspace, not owned by its neighbor.** It
   records which shell it was created beside (display label only), but its lifecycle
   belongs to the workspace: closing the shell next to it does not close it. A pane
   opened as an accessory often becomes primary; forced cascade-close is arbitrary.
7. **Interaction tiers, in priority order:** (1) auto layout — creation and close
   re-flow the grid, nothing to learn; (2) mouse — divider drag-resize already works
   via `paneframe`; drag-to-rearrange is deferred but must not be precluded (see
   Constraints); (3) key chords — deferred power-user parity for the mouse
   operations, never required for anything.

## Adopted unchanged from the windowing plan

- Binary tree, ratios not cells, floors folded up the tree, refuse-don't-squeeze
  (Law 2), zoom as the doesn't-fit degrade mode.
- Structure layer never learns content semantics (Law 1); content behind the
  `Content`-style seam with optional capability interfaces.
- Live vs passive leaf classes: live leaves cost a tmux resize on geometry change,
  are never resized mid-drag (resize on release + debounce), and one tmux session
  is never shown in two leaves at once.
- Never create an empty pane (Law 4): every split is born with content — which the
  modal-first flow satisfies by construction.
- Focus indication via header chip + adjacent-divider highlight; no dimming.
- All pane/split/chrome/hit-region work lands in `paneframe`/`panelayout` once and
  reaches both the project workspace and the global Sessions browser (AGENTS.md
  parity rule).

## Phase A — Terminal leaf kind (steel thread)

**Demo:** in a project workspace, open the create modal, arrow to "Terminal split",
Enter. A live terminal appears beside the shell per the auto rules. Run a dev server
in it while the agent works in the main pane. Drag the divider; both tmux panes
resize once, on release. Close the shell it was created beside; the terminal stays.
Quit and relaunch; it comes back, session intact. The sidebar row shows `◧◨`.

### A1 — `Terminal` becomes a real content kind

Today `panelayout.Terminal` is an alias for `Primary` (the host's single owned
terminal), and `terminal_panel.go` is a second hand-rolled split system beside the
tree. Phase A gives the tree a genuine live-terminal content kind:

- New `panelayout.Kind` (working name `Shell`) distinct from `Primary`, with its own
  `Floors` entry (reuse the panel's existing min box constants).
- A live-terminal content implementation backed by the same `internal/tty` machinery
  the primary terminal and the old panel use. The windowing plan's M1 finding holds:
  the second terminal's plumbing already exists as the panel terminal
  (`terminal_control.go`); this is absorption, not new plumbing.
- **Session identity:** each terminal leaf owns a workspace-scoped tmux session
  (created in the workspace's workdir), persisted as a durable target selector —
  never a tmux pane id. Peer lifecycle: the leaf's close closes its session prompt
  (explicit, with confirm if a process is running); closing neighbors never does.
- `terminal_panel.go`, `termPanel bool`, `alt+t`, and the panel's duplicated
  window-state functions are deleted as the leaf replaces them, per the windowing
  plan's M1 steps 2–4 (geometry through the tree, persisted `termPanelLayout` →
  a `PaneLayoutJSON` split, table-tested both directions). `ctrl+t` keeps working,
  redefined as "toggle a terminal split at the persisted axis/ratio".

### A2 — Placement for duplicable live leaves

`PlanOpen` currently retargets when a leaf of the requested kind exists — correct
for Document/Diff/Issue (one leaf per kind, content swaps in), wrong for terminals
(each request is a *new session*). Add a duplicable-kind path: terminal opens never
retarget; they always split, following the auto rules. The one-session-one-leaf
refusal stays (it's forced by tmux and applies to *showing the same session twice*,
not to having several sessions).

- Live-leaf cap: at most 2 live terminals **on screen** at a time (primary + one
  split terminal). Any number of screens/workspaces may each hold 1–2; off-screen
  terminals hold no control-mode subscription (already the rule), so on-screen load
  equals today's primary + panel. The cap is a refusal-with-toast, not a hidden
  rule. Raising it to 3–4 on-screen live panes (an all-terminal 2×2) is a phase-B+
  decision gated on a load proof of N simultaneous control-mode subscriptions.
- No idle-throttling policy: unfocused on-screen live leaves stay fully live; add
  `SetVisible(false)` demotion only if a measured performance problem appears.
- Focus/keyboard: exactly one live leaf owns the keyboard at a time (existing rule);
  click or Tab moves it. No new key machinery in phase A.

### A3 — Create modal rows and placement buttons

Extend `internal/workspacecreate` / `create_modal.go`:

- New list entry **Terminal split** alongside Shell and Worktree. Selecting it needs
  at most a name field (default: auto-name like `term · <workdir base>`); the modal
  stays one screen, no new required input.
- The **`Auto · Right · Below`** segmented row at the bottom, rendered as buttons.
  Click = create now with that placement; Enter = Auto. Wire Right/Below through
  `ApplyAxisOverride`. Remember last list selection across opens.
- The row set is data-driven so phase B rows (File, Git diff, td issue, Note) are
  additions to a table, not new modal code.

### A4 — Rename via clickable pane title

Non-primary terminal leaves have no sidebar row to select (badge-only grouping),
so rename lives on the pane itself: the title label at the top-left of the pane
header is a click target that opens the existing shell-rename modal. Cheap by
construction — pane headers are already `paneframe` hit regions (click-to-focus),
so this adds one region on the title text and reuses the rename flow. Names default
to an auto-name (e.g. `term · <workdir base>`); agent-driven renames simply show up
in the header. Mouse-only in phase A; a keyboard path arrives with the deferred
chord tier. Per the parity rule, the clickable-title region lands in `paneframe`.

### A5 — Sidebar badge

- Workspace rows gain the layout glyph (`◧◨` / `⊞n`) derived from the persisted
  pane tree's content-leaf count, rendered in the existing row style (rows keep
  their current two-line design; the glyph joins the existing badge/metadata area,
  it does not restructure the row).
- Applies to both the project workspace sidebar and the global Sessions browser
  list, from one shared helper.

### A6 — Parity and persistence

- Both surfaces bind through their `pane_host.go`. The rule is parity with the
  primary terminal: on each surface, a split terminal leaf gets exactly the
  treatment the primary terminal gets there — interactive where the primary is
  interactive (project workspace), capture-preview where the primary is a preview
  (global Sessions browser tiles). No special case in either direction.
- Persistence evolves `state.PaneLayoutJSON` in place; unknown kind ⇒ drop the
  leaf and collapse its split (windowing plan persistence rules).

**Ship criteria:** existing terminal parity/scroll/surface test suites pass;
panel-migration table test covers both old layouts and both ratio directions;
real-app proof on a fully isolated run (`tmux-drive.sh`, both axes isolated) showing
create-via-modal → two live terminals → divider drag → one resize per pane on
release → neighbor close leaves the terminal → relaunch restores it.

## Phase B — Grid policy and splits from anywhere

### B1 — Fourth-pane grid rule

Change `PlanOpen`'s step-3 fallback: when the right column already holds two
content panes, the next open splits the primary/left column (→ 2×2) instead of
stacking a third row on the right. This is an insertion-policy change only — no
tree, frame, or persistence changes. Existing three-pane layouts restore unchanged.

### B2 — Create modal grows pane-kind rows

Add File / Git diff / td issue (Note when the notes plugin supports it) to the
modal's list. Each row reuses the already-shipped passive leaf kinds and the
`sidecar open` resolution path — the modal is a new entry point, not a new
capability. Add the filter input only if/when the list outgrows one screen.

### B3 — Terminal splits outside the workspace surfaces

Let Files, Git, td, and Notes host a terminal split beside their content, via the
same app-owned pane host that the content-links plan built for passive leaves. This
is the step the windowing plan scoped out ("does not put live-terminal splitting
outside Workspaces") and this plan deliberately re-opens — but only after phase A
proves the terminal leaf and its lifecycle inside Workspaces. Sessions created here
are project-scoped peers, listed and badged like any other.

### B4 — Deferred, but not precluded (design constraints)

- **Drag-to-rearrange:** not built yet. Constraint to preserve: pane headers and
  dividers remain `paneframe`-registered hit regions with leaf IDs as data, so
  title-drag → drop-target → leaf-swap can be added in `paneframe` later without a
  second geometry or hit-testing system. Nothing may claim header-drag gestures for
  another purpose in the meantime.
- **Key chords (`alt+w` tier):** deferred power-user parity for mouse operations
  (focus-direction, resize, equalize, zoom already has bindings where shipped).
  When added, follow the windowing plan's §3 (prefix, timeout fallthrough,
  precedence ladder) unchanged.
- **Equalize:** ships whenever the 2×2 grid does (double-click divider and/or a
  command), since binary-tree ratios otherwise read as 50/25/25.

## Resolved questions

1. **Live-leaf cap:** 2 live terminals on screen at a time; unlimited across
   screens; no idle `SetVisible(false)` demotion unless a measured performance
   problem forces it (see A2).
2. **Global Sessions browser:** split terminals mirror the primary terminal's
   treatment per surface — no separate liveness decision (see A6).
3. **Rename:** clickable pane-title label opens the rename modal, shipped in
   phase A (see A4). Sidebar grouping stays badge-only.

## Acceptance evidence

- Phase A demo transcript + snapshots from an isolated `tmux-drive.sh` run.
- Placement unit tables: `PlanOpen` with duplicable kinds (A2) and the grid rule
  (B1), every step asserted.
- Panel-migration table test (both layouts × both ratio directions).
- Parity test asserting the sidebar badge and pane composition agree between the
  two surfaces.
