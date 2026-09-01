# Pane Layout Control — Agent CLI and the Pane Switcher

Two halves of one capability, sharing one placement model:

1. **Agents compose layouts non-interactively.** A single CLI call opens several panes at once — a files pane holding two file tabs, a td-issue pane, a fresh shell — placed automatically or at explicit grid positions, up to a capped grid. Agents can also read the current layout back as JSON before deciding what to change.
2. **Users create splits of any kind from the keyboard.** The create modal grows into a pane switcher: pick a kind (Shell, Worktree, Terminal split, File, Git diff, td issue, configured resource providers like Jira, Note), pick a target where the kind needs one, pick placement — one modal, two steps, no chords required.

```bash
# Read the current layout (agents: read before you write)
sidecar layout get --json

# Open three panes at once, auto-placed
sidecar layout apply --pane "file:internal/palette/list.go:112,internal/palette/state.go" \
                     --pane "issue:td-756c34" \
                     --pane "shell?run=make dev&name=dev server"

# Or specify the full grid explicitly (columns of stacked panes)
sidecar layout apply --spec '{"columns":[
  {"panes":[{"kind":"primary"}]},
  {"panes":[{"kind":"file","targets":["internal/palette/list.go:112"]},
            {"kind":"issue","targets":["td-756c34","td-9d3b09"]}]},
  {"panes":[{"kind":"shell","run":"make dev","name":"dev server"}]}
]}'

# Incremental single open at an explicit cell (extends today's --split)
sidecar open internal/palette/list.go --at 2.1
```

**Why this earns a CLI despite Sidecar being presentation-layer:** the ownership test. The pane tree, its placement policy, its persistence, and the switcher's refusal rules are capabilities Sidecar owns — they vanish if Sidecar is uninstalled. An agent can already open one pane (`sidecar open --split`); composing a working set is the same capability at its natural grain.

## Relationship to other plans

- **[terminal-splits-and-windowing.md](terminal-splits-and-windowing.md)** — its Phase A (Terminal leaf, live cap, modal placement row) has shipped and this plan builds on it. This plan **absorbs and supersedes its Phase B1 (fourth-pane grid rule) and B2 (create modal grows pane-kind rows)**; B3 (splits outside the workspace surfaces) and B4's deferred chord tier stay with that plan. Placement vocabulary (`auto|right|below`, `panelayout.ApplyAxisOverride`) is shared and extended here with grid cells.
- **implemented/agent-shell-create-cli.md** — shipped in full, including `create shell --split` (beside-the-session is the default from inside a Sidecar shell; `--tab` asks for a workspace row). This plan generalizes that pattern: the host owns creation, the CLI sends a request and reads acks.
- **implemented/agent-open-in-split-cli.md** — the `uirequest` transport this plan extends with one new action. Its unbuilt M3 siblings (`sidecar panes`, `sidecar close`) are subsumed by `layout get`/`layout apply` here.
- **[The viewer owns the screen](../active/remote-host-viewer-screen.md)** — when those same `open` / `layout` verbs run inside a Sidecar-managed pane on a registered host, they apply on the lease holder's Sessions preview rather than on a host TUI that may not be running. This plan's never-queue rule is the relay's off-screen refusal.
- **deprecated/workspace-windowing-system.md** — architectural authority for the tree, floors, refuse-don't-squeeze, zoom, and the deferred `alt+w` chord vocabulary. Nothing here contradicts it; the chord tier remains deferred and its §3 rules remain the spec for whenever it lands.
- **Mockups:** `~/code/tui/mockups/sidecar-pane-switcher.tui.yaml` (the switcher: kind list, disabled-row treatment, File and td-issue target steps) building on `~/code/tui/mockups/sidecar-splits.tui.yaml` (grid progression, state 5's modal sketch).

## Current state this plan builds on

Verified in the tree at the time of writing:

- `panelayout` has seven kinds (`Primary`, `Document`, `Issue`, `Diff`, `Resource`, `Shell`, `Note`); only `Shell` is `Duplicable`; `IsLive` = `Primary|Shell`; `LiveLeafCap = 2` with the refusal/disabled-reason pattern (`shell_leaf.go`).
- `PlanOpen` retargets any passive kind that already has a leaf — so "two files in one pane" is already the natural result of two opens (tabs via `contentpanes.Deck.openTab`); what's missing is doing it in one call and choosing *where* the pane goes beyond a binary `right|below` axis override.
- There is **no cap on tree depth or leaf count** in the structure layer; the effective cap is the one-leaf-per-passive-kind policy (max ≈ 7 leaves). Floors + refuse-don't-squeeze (`LayoutPanes` fit-tested on a cloned trial tree before every commit) are the true governors.
- `SplitLeaf` accepts a whole subtree and `inspect` validates it — the existing primitive for grafting a prebuilt column.
- Two validating tree decoders exist and are the shape an "apply" needs: `workspace.restorePaneLayout`/`decodePaneNode` (any depth, exactly one Terminal leaf, unknown kind ⇒ drop and collapse) and `contentpanes.Decode` (fresh IDs, collapses invalid nodes, enforces one-per-kind via `seen`).
- The `uirequest` bus carries `open`/`create`/`rename-*`/`notify` actions with a single-status ack; `Payload json.RawMessage` is the designed extension seam. The workspace plugin verifies the tree after an open rather than trusting the returned command, and queues requests for unselected shells (`pendingView`, `queued` ack, `◫` badge).
- The create modal (`internal/workspacecreate`) is already data-driven: `kindCatalog` (Shell, Worktree, Terminal split), `placementCatalog` (Auto/Right/Below with `--split` mappings), remembered `lastKind`, disabled-row plumbing for the live cap. Placement is currently shown only for Terminal split, and the kind row is a horizontal toggle, not the vertical list the mockups show.
- The modal library has no built-in filtered list; the working precedent for filter-input + custom scrollable list is `internal/app/worktree_switcher_modal.go` (with `modal.SectionScrollbar`).
- Jira/GitHub are **not** native kinds: they are configured terminal-resource providers opened as `TargetKindResource` (`--provider <instance> <locator>`, matcher filled host-side). td issues are native (`TargetKindIssue`). A switcher shows both, but they route to two different open paths.

## Settled decisions

1. **The layout vocabulary is columns-of-rows, projected onto the binary tree.** A layout is 1–`MaxGridColumns` columns; each column stacks 1–`MaxGridRows` panes. Cells are addressed `col.row` (1-based, left-to-right, top-to-bottom): `2.1` = second column, top pane. This is a *vocabulary*, not a new structure: the grid compiles to nested `Columns`/`Rows` splits, and `layout get` projects the tree back to it. The projection must flatten same-axis nesting (a 3-pane column is `Rows` splits chained inside `Rows` — that is grid-shaped); the shape that escapes the vocabulary is axis alternation below depth 2, e.g. a `Columns` split nested inside a column's row stack. A tree that escapes is reported with `"grid": null` plus the raw tree, and is still valid; only `apply --spec` output is guaranteed grid-shaped.
2. **Caps: `MaxGridColumns = 4`, `MaxGridRows = 4`.** Expressed exactly like `LiveLeafCap`: constants + predicate + visible refusal with reason. The caps are a sanity bound for the feature, not the real constraint — floors refuse most large grids on ordinary terminals first, and the refusal message says which floor didn't fit. `LiveLeafCap = 2` stands unchanged: a spec asking for more live terminals than the cap is declined (raising it stays gated on the terminal-splits plan's load proof).
3. **Multiple targets in one pane are tabs.** A spec pane carries `targets: [...]`; the first target opens the pane, the rest join as tabs of the same kind (the existing retarget/`openTab` path). Mixed kinds never share a leaf. `resourceview.MaxTabs = 16` and the uncapped doc/issue/note/diff tab strips stay as they are.
4. **`layout apply` is all-or-nothing.** The host builds the full trial tree, fit-tests it against floors (`LayoutPanes` on a clone), checks caps and live-session rules, then commits atomically — or declines with a reason naming the first violation, leaving the layout untouched (Law 2: refuse, don't squeeze). No partial application; an agent that wants best-effort issues sequential `sidecar open` calls, which keep today's semantics.
5. **Apply never destroys a live terminal implicitly.** A `--spec` full layout must account for every existing live leaf (Primary and any Shell leaves — carry them with `{"kind":"primary"}` / `{"kind":"shell","session":"<tmux-session>"}` as `layout get` prints them). A spec that omits a live leaf is declined naming the session. Passive panes not present in the spec are closed freely — their content is re-openable. The batch form (`--pane`, no `--spec`) only adds panes and closes nothing.
6. **One new `uirequest` action: `layout`.** The CLI creates nothing; the host does all creation/placement (the `create shell --split` precedent). Payload carries the parsed spec; the ack gains an additive, versioned `items` array (one entry per requested pane: `opened|retargeted|declined` + pane cell + surface), alongside the existing single `status` which reports the overall outcome. On an all-or-nothing decline, `items` still lists every requested pane with its individual verdict as evaluated during validation (would-open vs `declined` + its own reason), and the top-level reason is the first violation — an agent sees everything wrong with its spec in one round trip. Both `get` and `apply` resolve to the origin shell's surface through the same ladder `open` uses; **`layout` requests never queue**. If the origin shell isn't on screen, both modes decline with that reason (exit 4) — a deliberate divergence from `open`'s `pendingView` queueing, because a queued atomic apply would validate against a tree that no longer exists by selection time, and a stale `get` answer is worse than a refusal. Exit codes match `open` otherwise: 0 applied, 2 usage/validation (CLI-side), 3 no instance, 4 declined (host-side refusal, reason verbatim).
7. **CLI surface is a `layout` command group plus one `open` flag.**
   - `sidecar layout get [--json]` — current layout: grid projection, per-pane kind/targets/tabs/session, geometry, caps and floors in effect. Human output is a small ASCII sketch plus a table; `--json` is the contract.
   - `sidecar layout apply` — `--spec <json>` (or `-` for stdin) for full layouts; repeatable `--pane <descriptor>` for the additive batch. Both use the same pane descriptor fields: `kind`, `targets`, `at` (optional cell), and for shells `run`/`type`/`name`. The flag descriptor is a compact string form of the JSON pane (exact grammar settled in M3; JSON is the canonical form and ships first).
   - `sidecar open <target> --at <col>[.<row>]` — explicit-cell placement for the single-open path. `--at` and `--split` are mutually exclusive. `--at` on a kind that would retarget is an error — a deliberate divergence from `--split`, which silently no-ops on a retarget (`ApplyAxisOverride`'s existing rule, unchanged): `--split` expresses a preference, `--at` expresses a requirement, and an unhonorable requirement should fail loudly rather than land the pane somewhere else.
   - All of it declares `AgentDoc` and regenerates `docs/reference/cli.md`.
8. **Auto placement completes the grid rule.** `PlanOpen`'s step-3 fallback changes per the terminal-splits plan's B1: when the right column already holds two content panes, the next open splits the primary/left column (→ 2×2) instead of stacking a third row. Beyond four, auto placement fills the emptiest grid column, then refuses at the caps. This single policy serves the CLI batch, single opens, and the switcher identically.
9. **Explicit placement is a new planner entry, not a second planner.** `PlanOpenAt(root, kind, contentID, cell)` sits beside `PlanOpen` in `panelayout`, resolving the cell against the current grid projection with these semantics:
    - **Occupied cell** (`2.1` exists): insert at that position — a `Rows` split on the occupant with the new pane taking the addressed cell and the occupant (and everything below it) shifting down one row. Overflow past `MaxGridRows` is a validation error.
    - **One-past-the-end row** (`2.3` in a 2-row column): append at the bottom of that column.
    - **One-past-the-end column** (`3.1` beside 2 columns, row must be 1): append a new column.
    - Anything further out of range is a validation error.

    `OpenPlan.Split` today names a *leaf* and every consumer feeds it to `SplitLeaf`, which refuses non-leaf targets — but a column append and the flattened-stack insert both require splitting an **internal node** (the column's subtree or the root), or the result nests a wrong-axis split and escapes the grid projection. So M1 extends the plan/`SplitLeaf` contract to target internal nodes (`SplitLeaf` already accepts grafting a subtree; the extension is letting the split point be a split node, validated by `inspect` as today), and the `OpenPlan` consumers (`contentpanes.Deck.Open`, `workspace.splitOnPlannedLeaf`, `shell_leaf.go`) are updated with it — the "callers unchanged" property holds only for plans that split a leaf, i.e. everything `PlanOpen` produces today.
10. **The switcher is the create modal grown, not a new modal.** Two steps in one modal, per the mockup:
    - **Step 1 — kind list.** `kindCatalog` gains File, Git diff, td issue, one row per configured terminal-resource provider (label = the instance ID — `TerminalResourceProviderConfig` has no display-name field today; add one in M2 if IDs read poorly), and Note (only when the notes plugin is present). The horizontal toggle becomes a vertical list with aligned descriptions once the row count passes ~4 (the mockup's layout); selection is remembered (`lastKind`), disabled rows stay visible with the reason inline (live cap, reusing the shipped string: "Two terminals are already on screen — close one first").
    - **Step 2 — target picker,** only for kinds that need one: filter input + suggestion list + count line, the `worktree_switcher_modal` pattern. File: fuzzy path match, recent-first, `path:line` accepted. Git diff: working-tree default plus recent commits/refs. td issue: in-progress + recent from td, or paste an id. Resource provider: locator input (validated host-side by the provider, as today). Note: recent notes. Esc returns to step 1. Shell/Worktree keep their existing single-screen forms untouched — they create workspace rows, not panes, and show no placement.
    - **Placement row on every pane-kind step.** `ShowPlacement()` extends from Terminal-split-only to all pane kinds. Click a placement = create immediately with it; Enter = Auto. Placement can grow a fourth segment later (explicit cell) but does not in this plan — the modal stays gesture-simple; cells are the CLI's precision tool.
11. **Keyboard entry stays modal-first; chords stay deferred.** The switcher opens via the existing `n` (new) binding wherever the create modal opens today, and gains a binding in the workspace-preview context so it's reachable without moving focus to the sidebar (candidate: `o` "Open" — the mockups' footer hint; verify against the keymap registry at implementation time and pick the first free key from `o`, `alt+o`, `alt+n`). The `alt+w` chord tier remains deferred per the windowing plan §3; nothing here claims keys or gestures it reserves (pane headers/dividers stay `paneframe` hit regions with leaf IDs as data).
12. **Passive kinds stay one-leaf-per-kind in this plan.** Arbitrary same-kind duplication (two independent file panes side by side) is deliberately out of scope: `contentpanes` hard-codes one pane per kind in its `hidden` map, `Leaf(kind)` lookups, and `Decode`'s dedup, and that blast radius deserves its own plan once this vocabulary has proven itself. The caps (decision 2) are sized so nothing needs renumbering when duplication lands. Until then the practical grid is bounded by kind count (~7 leaves), which the 4×4 cap comfortably contains.

## Unresolved questions

- The compact `--pane` descriptor grammar (M3). JSON via `--spec` is canonical and ships first; the flag form follows once real agent usage shows which shape reads best.
- Whether `layout get` should also be answerable headlessly (from persisted state, no running instance). Deferred: acks require a live instance today, and stale-state answers are worse than exit 3.

## Work sequence

Milestones are ordered so each ships alone. M2 (switcher) and M3–M4 (CLI) are independent tracks after M1.

### M1 — Placement policy: grid rule and `PlanOpenAt`

- Implement the fourth-pane grid rule in `PlanOpenContent` (terminal-splits B1) and the fills-emptiest-column continuation; add `MaxGridColumns`/`MaxGridRows` constants and predicates with refusal reasons.
- Add the grid projection (`panelayout.GridOf(root)` or similar): tree → columns-of-rows with same-axis flattening, `nil` where the tree escapes the vocabulary. Add `PlanOpenAt` with the internal-node split extension from decision 9 (extend `SplitLeaf`/`OpenPlan` to target split nodes; update the three `OpenPlan` consumers).
- Placement unit tables: every auto step 1–4+, every `--at` cell class (occupied, one-past-end, out of range, retarget-conflict), caps, projection round-trips over nested trees including non-grid shapes.
- **Proof:** existing `plan_test.go` suites extended; no UI change yet beyond the fourth pane forming a 2×2 (isolated `tmux-drive.sh` snapshot).

### M2 — The pane switcher (absorbs terminal-splits B2)

- `kindCatalog` rows for File, Git diff, td issue, resource providers (from `config.TerminalResources`, `HostScoped` like Terminal split), Note; vertical-list rendering with descriptions per the mockup; placement row for all pane kinds.
- Step-2 target pickers on the `worktree_switcher_modal` pattern; each picker resolves to the same `uirequest`-shaped target the CLI produces and routes through the existing per-surface open paths (`openDocPaneForSurface` et al.) — the modal is an entry point, not a new capability.
- The workspace-preview entry binding; footer hints via `Commands()`; keymap-context registration so `?` discovers it.
- Parity: rows and pickers work identically from the project workspace and the global Sessions browser (minus `HostScoped` rows where no pane tree exists), enforced by a parity test as with the `create` action today.
- **Proof:** isolated `tmux-drive.sh` run — open switcher from the preview, arrow to File, type a fragment, Enter; snapshot shows the doc pane beside the terminal. Second snapshot for a placement-button click (Right). Third for the disabled Terminal-split row with two live terminals on screen.

### M3 — `sidecar layout get` and `apply` (batch form)

- New `layout` command group in `internal/cli/registry.go` with `AgentDoc`s; `uirequest` action `layout` with payload `{mode: "get"|"apply", panes: [...], columns: [...]}` and the additive `items` ack array.
- `get`: host answers from the focused surface's tree — grid projection, panes with kind/targets/active tab/session, geometry, caps/floors. CLI renders the sketch; `--json` passes the payload through.
- `apply` batch form (`--pane`, additive only): host resolves each descriptor through the same target-resolution the CLI's `open` uses (file paths workspace-relative, diffs re-resolved host-side, providers validated), plans placements (auto or `--at`-style cells per pane), fit-tests the composed trial tree once, commits, acks per item. Shell panes route through `createTerminalSplit`/`CreateManagedShell` exactly as `create shell --split` does.
- The compact `--pane` grammar is settled here with real usage; until then `--pane` accepts the JSON pane object verbatim as its value.
- **Proof:** agent inside a Sidecar shell runs one `layout apply` with three panes (file with two targets, issue, shell with `run`); snapshot shows the composed layout, ack JSON shows three `opened` items; `layout get --json` round-trips it. Refusal proof: a spec exceeding the live cap declines with reason, layout untouched.

### M4 — `apply --spec` (full layouts) and `open --at`

- Full-layout mode: validate the spec (exactly one `primary`, every existing live leaf accounted for per decision 5, caps, kinds known), build the tree via the `Decode`-shaped path (validate → build → fit-test → commit → `LoadVisible`), close passive panes not present, ack per item.
- `open --at` wiring: flag parsing, mutual exclusion with `--split`, `Options` extension (additive field), host-side `PlanOpenAt`.
- Persistence: the applied tree is ordinary `PaneLayoutJSON`/deck state — no new format; relaunch restores it (existing rules).
- **Proof:** `layout get` → edit JSON → `apply --spec` round-trip in an isolated run, including a spec that moves the primary terminal to the right column; decline proofs for omitted live shell and out-of-cap grids; relaunch restores the applied layout.

### Later, elsewhere

- Duplicable passive kinds (true N×M same-kind grids) — its own plan; the vocabulary, caps, and ack shapes here are sized for it.
- The `alt+w` chord tier and drag-to-rearrange — stay with the terminal-splits/windowing plans.
- `create worktree --split` — still refused (exit 2), unchanged by this plan.

## Acceptance evidence

- Placement unit tables (M1) covering auto steps, `PlanOpenAt` cell classes, caps, and projection round-trips.
- Switcher parity test between the two surfaces; picker-to-target unit tests asserting each kind resolves to the same target shape the CLI produces.
- `tmux-drive.sh` transcripts and snapshots for the M2–M4 proofs, fully isolated (both tmux socket and state tree — `paths` check first).
- Regenerated `docs/reference/cli.md` including `layout get`, `layout apply`, and `open --at`; `sidecar agents` lists them.
- Ack-contract tests: `items` array shape, all-or-nothing decline leaves the tree byte-identical (encode before/after).
