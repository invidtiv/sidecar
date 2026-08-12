# Plan: Modal redesign — flat surfaces, columns, minimal form elements

**Research snapshot:** 2026-08-11
**Status:** agreed — ready to implement.
**Scope:** sidecar only. `~/code/td` modals are explicitly out of scope.

## Decision first

Rebuild the visual language of `internal/modal` around three rules, then migrate
every modal in sidecar onto it. Behaviour — keybindings, mouse hit regions,
focus order, scrolling, actions — stays identical unless listed under
**Open questions**.

1. **One surface, one background.** A modal draws exactly one background
   (`BgSecondary`) across its whole box. Nothing nested inside may set a
   background except two sanctioned emphases: the *selection band* (full
   content-width, one row or item block) and *danger* affirmation. Every chip,
   pill, button box, and key-hint badge inside a modal loses its fill and
   becomes coloured text. This is what kills the conflicting-background class of
   bug at the root rather than patching it.
2. **Columns, not wrapped lines.** Items that today stack a primary and a
   secondary value on two lines become one row: primary left, secondary
   right-aligned or in a fixed second column, middle-truncated to fit. Section
   headers become a small-caps label with a rule to the right edge and optional
   right-aligned meta (`KIND ─────── optional`).
3. **Form elements are minimal but legible.** Boxed `NormalBorder` inputs
   become a flat single-rule field with a cursor prefix; button rows become
   plain text actions (`enter create   esc cancel`); selects become cursor-led
   choice rows with a dim description column. Focus is shown by cursor + colour
   + weight, never by a drawn box.

A fourth rule governs how all of this behaves as the terminal narrows:

4. **Responsive, mobile-web style.** As width shrinks, content stays *available*
   and the layout adapts — columns collapse to stacked lines, secondary values
   elide rather than disappear, nothing becomes unreachable. This is a general
   sidecar principle, applied here without over-engineering it: one breakpoint
   per component, chosen from the content's own minimum, not a global grid
   system.

Target look is the reference screenshots: the command palette and Create-new
form (image 1), applied to the project switcher (image 2) and theme switcher
(image 3), which are the two worst current offenders.

## Root cause of the "conflicting backgrounds" bug

Verified empirically by rendering a modal and dumping the raw ANSI:

```
line 5: …\x1b[48;2;55;65;81m  \x1b[m\x1b[38;2;156;163;175;48;2;55;65;81mOK\x1b[m\x1b[48;2;55;65;81m  \x1b[m                            \x1b[0m…
                                                                                        ^^^^^^^^^^^^^^^^ no background
```

Two independent defects:

- `styles.FillBackground` re-applies the background after every `\x1b[0m`, but
  **lipgloss v2 emits `\x1b[m`** (implicit-zero form). The `strings.ReplaceAll`
  never matches, so every run after the first nested styled element renders on
  the terminal default background. That is precisely the grey/black splotching
  in images 2 and 3.
- `FillBackground` then appends a trailing `\x1b[0m` to each line, so the
  modal box's own `Padding(1, 2)` and `Width()` padding, emitted *after* that
  reset by `modalStyle`, also fall back to terminal default.

Fixing the sequence matching is a one-line change and should land first as its
own commit, because it makes the *current* modals immediately correct and gives
the redesign a clean baseline. But it is a patch, not the design: rule 1 above
removes the need for `FillBackground` inside modal content almost entirely,
because nested backgrounds are what create resets in the first place.

## Inventory

`modal.New` is used at **52 call sites across 25 files** — the whole modal
surface already runs on the newer library:

| Area | Files |
| --- | --- |
| app | `view.go` (project switcher, shortcuts), `model.go` (quit), `theme_switcher_modal.go`, `worktree_switcher_modal.go`, `project_add_modal.go`, `open_in_modal.go`, `issue_preview_modal.go`, `update_modal.go`, `diagnostics_modal.go` |
| workspace | `view_modals.go` (8), `create_modal.go` (5), `agent_config_modal.go`, `prompt_picker_modal.go`, `fetch_pr_view.go`, `task_picker.go` |
| gitstatus | `pull_menu.go`, `push_menu.go`, `branch_picker.go`, `commit_view.go`, `confirm_discard.go`, `confirm_stash_pop.go`, `error_modal.go` |
| notes | `delete_modal.go`, `info_modal.go`, `task_modal.go` |
| conversations | `resume_modal.go`, `content_search_view.go` |
| filebrowser | `view_blame.go`, `view_info.go`, `project_search_view.go` |
| tdmonitor | `setup_modal.go` |
| ui | `confirm_dialog.go` |

Four **hand-rolled** overlays still bypass the library and must be converted as
part of this work, or they will be the only inconsistent surfaces left:

- `internal/app/view.go:544` — project-add theme picker
- `internal/plugins/filebrowser/view.go:1447`
- `internal/plugins/gitstatus/history_search.go:217` and `:402`

Many call sites also use `modal.Custom` with hand-built strings (project
switcher, theme switcher, palette). Those are where the new primitives pay off
most: most of their body collapses into `Header` + `Choice` rows.

## Design specification

### Surface and chrome

- Box: `RoundedBorder`, border colour = variant colour, `Background(BgSecondary)`,
  `Padding(1, 2)` unchanged.
- Title: bold `TextPrimary`, no `MarginBottom`; a single blank line is emitted
  by the layout instead (today `ModalTitle.MarginBottom(1)` plus the layout's
  own blank line produce inconsistent spacing).
- Backdrop: unchanged (`ui.OverlayModal`).
- Scrollbar: unchanged glyphs, but drawn with the modal background.

### New style tokens (in `internal/styles`)

All derived from the active theme — no hardcoded colours, so themes keep
working.

| Token | Value | Use |
| --- | --- | --- |
| `ModalRule` | `TextSubtle` fg on `BgSecondary` | section header rule, field underline |
| `ModalSectionLabel` | `TextMuted`, uppercase by caller | `KIND`, `NAME`, `AGENT` |
| `ModalSectionMeta` | `TextSubtle`, right-aligned | `optional`, counts |
| `ModalSelectionBand` | `Background(SelectionBandBg)` | selected row, full content width |
| `SelectionBandBg` | derived: `BgSecondary` lifted ~6% toward `TextPrimary`, contrast-checked in `normalize.go` | the *only* nested background allowed |
| `ModalCursor` | `Primary`, bold | `❯` |
| `ModalKey` | `Accent` (no background) | `enter`, `esc`, `tab` in hint rows |
| `ModalKeyLabel` | `TextMuted` | the word after a key |
| `ModalSecondary` | `TextSubtle` | right-hand/description column |
| `ModalFieldValue` | `TextPrimary` | input text |
| `ModalFieldPlaceholder` | `TextSubtle` | input placeholder |

`SelectionBandBg` goes through `NormalizePalette` so every one of the ~450
themes gets a band that is visible against its `BgSecondary` and keeps
`TextPrimary` readable on it. Add it to the contrast table in
`internal/styles/normalize_test.go`.

Retire *inside modals* (keep the exported styles for non-modal callers):
`styles.Button`, `ButtonFocused`, `ButtonHover`, `styles.KeyHint`, and
`ListItemFocused`'s `Background(Primary)` fill. `ButtonDanger*` survives — see
the danger exception below.

`styles.KeyHint` and `BarChip` keep their fills in the **header and footer**,
which are out of scope here. Sidecar will briefly mix the chip idiom (chrome)
with the flat idiom (modals); unifying them is a separate, global pass.

### New / changed section primitives (`internal/modal`)

Additive API — existing sections keep working during migration.

```go
// Header renders "LABEL ──────────────────────── meta" at content width.
func Header(label string, opts ...HeaderOption) Section
func HeaderMeta(meta string) HeaderOption

// Row renders left/right columns on one line with middle-truncation of
// whichever side is marked shrinkable.
func Row(left, right string, opts ...RowOption) Section
func RowShrink(side Side) RowOption      // default: shrink right
func RowGap(n int) RowOption             // default: 2
func RowAlign(a Align) RowOption         // right-aligned or fixed-column

// Choice is the cursor-led selectable list: "❯ label      description".
// Replaces most Custom-built lists; keeps List's focus/scroll semantics.
func Choice(id string, items []ChoiceItem, selected *int, opts ...ChoiceOption) Section
type ChoiceItem struct{ ID, Label, Desc, Right string; Data any }
func ChoiceDescColumn(col int) ChoiceOption   // fixed column for Desc
func ChoiceRightAligned() ChoiceOption        // put Right flush to content edge
func ChoiceMaxVisible(n int) ChoiceOption

// Field is the flat input: optional cursor prefix, no border, rule underneath.
func Field(id string, model *textinput.Model, opts ...FieldOption) Section

// Actions replaces Buttons: plain text "enter create   esc cancel",
// still focusable, still mouse-clickable.
func Actions(items ...ActionDef) Section
type ActionDef struct{ Key, Label, ID string; IsDanger bool }
```

Rendering rules:

- `Header`: `SECTIONLABEL` in `ModalSectionLabel`, one space, `─` repeated in
  `ModalRule` to fill, optional right meta in `ModalSectionMeta`.
- `Choice` row: `cursor(2) + label + gap + secondary`. Selected row draws
  `ModalSelectionBand` across the **full content width** (padded with spaces in
  the band style, so there is no ragged edge — this is what image 1 shows).
  Unselected rows draw no background at all.
- Focus of a `Choice` when the modal has other focusables: cursor switches from
  `›` (unfocused) to `❯` bold (focused); no colour inversion.
- `Field`: `❯ ` prefix when focused, two spaces otherwise; value in
  `ModalFieldValue`; a `─` rule under the field in `ModalRule` when focused,
  in a dimmer rule when not. No border box.
- `Actions`: keys in `ModalKey`, labels in `ModalKeyLabel`, two spaces between
  pairs, three between actions. Focused action underlines its label; hovered
  action brightens the label. Each action keeps **one cell of horizontal
  padding** on each side — invisible against the flat surface, but it preserves
  a comfortable mouse target now that the button box is gone. The registered
  hit region covers key, label, and that padding.
- **Danger is the one exception to rule 1.** Destructive affirmatives
  (`Delete worktree`, `Discard changes`, `Delete note`) keep a filled
  `ButtonDangerFocused` background plus the `Error` modal border. Flattening
  them would make destroying things visually easier than not destroying them.
  The fill is a single, deliberately-placed background on an otherwise flat
  surface, so it does not reintroduce the splotching class. Non-destructive
  affirmatives stay flat. Worth revisiting later as a config option, not now.
- The built-in hint line (`renderHintLine`) is replaced by an `Actions`-style
  row using the same tokens, so a modal's hints and its actions look identical.

### Column behaviour

`Row` and `Choice` share one truncation helper:

- Right/secondary column is measured first; if `left + gap + right` exceeds
  content width, shrink the marked side by **middle-elision** for paths
  (`/Users/marcus/…/sidecar`) and tail-elision for prose.
- Below a `minLeft` (default 12 cells) the row degrades to two lines — the
  current appearance — rather than hard-truncating the primary value. Per rule
  4, the secondary value stays on screen in a stacked form instead of being
  sacrificed to keep one line. Each component owns this single breakpoint; it
  is a property of the row, not a global layout mode.

### Width policy

Today most modals pass a fixed `WithWidth`. Add `modal.WithWidthPolicy` sizing
to `clamp(fraction*screenW, min, max)` with per-modal `min`/`max`, so
two-column rows have room on wide terminals and still fit at 80 cols. Existing
`WithWidth` keeps working; migrate list-style modals (project switcher, theme
switcher, branch picker, prompt picker, task picker) to the policy.

## Reference redesigns

**Project switcher** (image 2 → target)

```
 Switch Project

 ❯ Filter projects…
 ──────────────────────────────────────────────────────────
 PROJECTS ──────────────────────────────────────── 12 total
 ❯ ◫ Overview                          All configured projects
   sidecar          (current)          /Users/marcus/code/sidecar
   td                                  /Users/marcus/code/td
   ▾ 5 more below

 enter switch   ↑/↓ navigate   ctrl+a add   esc close
```

Name left, `(current)` marker inline, path right-aligned and middle-elided;
selection band spans the full width; no chips in the footer.

**Theme switcher** (image 3 → target): scope selector becomes a text segmented
row (`Global · This project`, active one bold + `Primary`, inactive
`TextMuted`) instead of two filled pills; the swatch strip keeps its colours
(swatches are legitimately backgrounds and are exempt, being the content
itself) but sits inside the selection band rather than beside a competing fill.

**Create New / forms** (`workspace/create_modal.go`,
`agent_config_modal.go`, `notes/task_modal.go`, `gitstatus/commit_view.go`):
`Header` per field group, `Field` for text, `Choice` for kind/agent, `Actions`
footer — i.e. exactly image 1's second panel.

## Phases

Each phase builds, tests, and is separately committable. Visual proof after
phases 1, 3, and 5 via `scripts/tmux-drive.sh` + `scripts/tmux-screenshot.sh`
on the isolated test tmux socket (never the default server).

**Phase 0 — Fix the background bug. DONE.**
`styles.FillBackground` now matches both `\x1b[0m` and `\x1b[m`, and opens each
line with the background so unstyled leading content does not depend on the
enclosing container. `internal/modal/background_test.go` and
`internal/styles/fill_background_test.go` parse the rendered ANSI and assert no
cell inside the modal border is emitted without a background; they fail against
the pre-fix code in 5 of 6 cases.

Measured on a live 120x40 project switcher (`scripts/tmux-drive.sh`, isolated
socket), counting cells with no background set inside the modal interior:
**281 across 7 lines → 0**. The only fills left inside modals are deliberate:
the `SurfaceRaised` key chips and the boxed input border, both of which this
plan replaces on aesthetic grounds rather than correctness. This test is the
guard rail for everything after it.

**Phase 1 — Tokens and primitives.** Add the style tokens + `normalize.go`
derivation + contrast tests. Add `Header`, `Row`, `Choice`, `Field`, `Actions`
to `internal/modal` with unit tests for truncation, band width, focus cursors,
and focusable/hit-region offsets. Replace `renderHintLine`. No call sites
change yet.

**Phase 2 — Retire nested fills in the modal library.** `Buttons` and
`Checkbox` render flat (keeping their IDs, focus order, and hit regions);
`Input`/`Textarea` drop their border boxes for the flat rule; `List` selection
uses the band. Existing call sites inherit the new look for free. Run the full
test suite — this is where behaviour regressions would surface.

**Phase 3 — The three flagship modals.** Project switcher, theme switcher,
command palette. These are the highest-traffic surfaces and validate the column
system against real data (long paths, 450 themes, 90 commands).

**Phase 4 — Forms.** workspace `create_modal.go`, `agent_config_modal.go`,
`fetch_pr_view.go`, notes `task_modal.go`, gitstatus `commit_view.go`,
`project_add_modal.go`.

**Phase 5 — Pickers and confirms.** gitstatus menus + `branch_picker`,
workspace `prompt_picker`/`task_picker`, conversations `resume_modal` +
`content_search_view`, filebrowser `project_search_view`, `ui/confirm_dialog`,
notes `delete_modal`/`info_modal`, app `open_in_modal`, `worktree_switcher`,
quit confirm.

**Phase 6 — Convert the four hand-rolled overlays** to `modal.New` so
`styles.ModalBox` has no remaining callers, then delete it.

**Phase 7 — Sweep and lock.** Grep for `Background(` under `internal/modal` and
in modal render paths; add a lint-style test that fails if a modal section
renders a background other than the sanctioned tokens. Update the
`create-modal` skill and `docs/features.md`.

## Verification

- `go test ./...` at every phase; existing modal tests (`internal/modal`,
  `internal/app/modal_focus_test.go`, `internal/ui/overlay_test.go`) must pass
  unchanged — they encode the functional contract.
- New: ANSI background-uniformity test; column truncation tests; a focus-order
  golden test per migrated modal asserting `focusIDs` and hit-region rects are
  unchanged before/after migration.
- Manual/visual: tmux-driven screenshots of each migrated modal at 80×24 and
  160×48, checked into `docs/screenshots/`. The 80×24 pass is what proves rule
  4 — every modal must still expose all of its content there.

## Explicitly out of scope

- **`~/code/td` modals.** Some are on an older idiom; they are a separate job.
- **Header and footer chips.** `styles.KeyHint` / `BarChip` keep their fills.
- **Any behaviour change.** Keys, focus order, mouse targets, scrolling, and
  actions are preserved. The two places this plan touches interaction at all
  are the one-cell action padding (which enlarges click targets) and the narrow
  -width row stacking (which preserves content that would otherwise truncate).
