# Sidecar Windowing System — Synthesized Recommendation

Merged from three independent designs (A: architecture, B: incremental, C: prior art/UX). Every load-bearing code claim below was re-read in the tree at `db8c342`. Corrections to the source designs are marked **[correction]**.

---

## 0. The one-paragraph answer

Sidecar already has a general binary split tree; what it lacks is a compositor that can draw more than two leaves, a content seam, and a vocabulary. Build in that order. **Step one is invisible**: realign the box type so the tree stops transitively depending on `internal/tty`, replace the two-leaf renderer with a recursive canvas blit, and introduce a four-method leaf interface. **Step two is the steel thread**: absorb `terminal_panel.go` into the tree so the terminal panel becomes an ordinary leaf, which delivers a user-created split hosting a second live terminal *without one line of new terminal plumbing*, because the second terminal already exists. **Step three** is the mechanical `termPanel bool` → leaf-ID conversion, now motivated by a shipped feature. The keybinding prefix is **`alt+w`, not `ctrl+w`** — `ctrl+w` is readline's `unix-word-rubout` in every shell Sidecar hosts, and Sidecar's panes are pass-through shells. **Superseded:** the global overview *did* get a tree, and as of td-b657fb both surfaces compose panes through `internal/paneframe`. See "Resolved: the global overview has a tree" below.

---

## 1. Recommended core model

### 1.1 Keep the tree that exists

`internal/plugins/workspace/panetree.go` (350 lines) is not a sketch. Verified:

- `LayoutPanes` (`panetree.go:75`) recursively places arbitrary trees on both axes, returns `fits=false` rather than a partial layout, and folds per-kind floors up the tree (`paneMinimum`, `panetree.go:119`).
- `layoutPaneNode` already re-clamps each split's ratio against the folded child minimums (`panetree.go:101`, `:107`).
- `SplitLeaf` / `ClosePane` / `FindPane` / `SetRatio` are pure structural mutations returning the new focus (`:149`, `:251`, `:292`, `:310`), with cycle-safe hostile-input validation (`inspectPaneTree`, `:198`).
- Persistence is already structural and recursive: `state.PaneLayoutJSON` / `PaneSplitJSON` (`internal/state/state.go:77-92`).

All three designs agree on this. **It will lay out a 7-leaf nested tree correctly today.** The work is everything around it.

### 1.2 Three caps and one impostor

The three structural blockers (Design B's finding, verified):

| Cap | Location | Verified |
|---|---|---|
| Renderer composes exactly two leaves | `doc_panes.go:629` — `if len(leaves) != 2 \|\| len(dividers) != 1 { return "", false }` followed by four hand-written join orderings (`:661-672`) | ✓ |
| Restore refuses any tree that isn't unnested terminal+doc | `supportedDocPaneTree`, `doc_panes.go:413` | ✓ |
| Exactly one terminal, exactly one doc | `terminalLeafID` returns the *first* terminal leaf (`doc_panes.go:179`); `activeDocPane` returns the *first* doc (`doc_panes.go:61`) | ✓ |

And the impostor: **`terminal_panel.go` is a second, hand-rolled split system doing the tree's job.** The evidence is stronger than any of the designs stated:

- Its clamps are *the same numbers*: `termPanelMinSize = 15` / `termPanelMaxSize = 85` (`terminal_panel.go` const block) vs `paneMinRatio = 15` / `paneMaxRatio = 85` (`panetree.go:5-8`).
- Its floors are *the same constants object*: `paneTreeFloors()` reads `termPanelMinBoxCols` / `termPanelMinBoxRows` (`doc_panes.go:73`).
- Its window state is duplicated function-for-function: `scrollPreviewWindow`/`pinPreviewWindow`/`releasePreviewWindowPin`/`thawPreviewWindow`/`thawPreviewGesturePin` (`plugin.go:1350-1421`) against `scrollTermPanelWindow`/`pinTermPanelWindow`/`releaseTermPanelWindowPin`/`thawTermPanelWindow`/`thawTermPanelGesturePin` (`terminal_panel.go:330-402`) — **six pairs, verified**. These are byte-equivalent calls into the same `internal/tty` rules over different fields, because `td-6b3fe5` unified the *rules* and left the *state* hand-copied.
- Its geometry is a bespoke offset walk inside `terminalSurfaceGeometry` (`terminal_surface.go:200-224`) rather than a leaf-box lookup.

**Two split systems in one region is the exact shape of the epic that just closed.** All three designs independently reached "absorb the panel first." That agreement is the strongest signal in the whole exercise, and it sets the steel thread.

### 1.3 The box-type realignment — a prerequisite, not a nicety

**[correction to Design A]** Design A proposes that `internal/pane` be compiler-prevented from importing `internal/tty`, and treats `termpreview.Box = mouse.Rect` as a mechanical cleanup. It is load-bearing. Verified: `panetree.go:47` aliases `Box = termpreview.Box`, and **`internal/termpreview` imports `internal/tty`** (`termpreview/render.go` and `termpreview/rows.go` both import `github.com/marcus/sidecar/internal/tty` and `internal/ui`). So the "pure" tree already transitively depends on `tty` today. Extracting it to a new package without fixing the alias just relocates the coupling.

The fix is one line and every existing `Box{X:…, Y:…, W:…, H:…}` literal keeps compiling, because the structs are field-identical:

```go
// internal/termpreview/termpreview.go
type Box = mouse.Rect   // was: struct{ X, Y, W, H int }
```

Verified: `mouse.Rect` is `struct{ X, Y, W, H int }` (`mouse/mouse.go:10-13`), `internal/mouse` imports only `time` and bubbletea, there is no cycle, and `Rect` already carries `Contains` (`mouse.go:15`) which is what hit testing wants. `termpreview` already pulls lipgloss and bubbletea transitively, so this adds no weight.

Do this first. It costs nothing and it is what makes the eventual package extraction real rather than cosmetic.

### 1.4 Where the code lives — B wins on timing, A/C win on the destination

Designs A and C both want `internal/panetree` extracted in the first commit. Design B says leave it in `workspace` until a second consumer exists. **B wins on sequencing**: the extraction buys nothing until something outside `workspace` calls it, and §4 below argues the global overview deliberately should not. A package move as commit one is ceremony.

But A and C are right about the destination and the law:

> **Law 1.** The layer that owns pane *structure* never learns what a pane is *showing*. It has no `Offset`, no `Follow`, no `Freeze`, no `PaneKind` behavior — only `ContentID`s and boxes. In particular, `internal/tty` stays where it is: the terminal content adapter is the only code that knows what a bottom-relative offset means.

This is the entire lesson of `td-6b3fe5`, and Design A's rejected-alternative is worth quoting as a standing rule: *"a terminal's offset is bottom-relative with a freeze; a doc's is absolute from the top; a td issue's is a rendered-line offset that changes with width. One field for three meanings is how you get three meanings for one field."*

Enforce Law 1 with the §1.3 alias change now, and extract the package at M3 when the third content kind gives it a second consumer.

### 1.5 Structural decisions

**Binary, not n-ary.** All three designs land here; A argues it best. Binary is what exists and what all the ratio arithmetic assumes. Its only user-visible cost is that three equal columns read as 50/25/25 unless hand-tuned — recovered entirely by an `Equalize` operation (ratios derived from leaf counts per subtree). N-ary would turn every ratio into a weight vector, every divider into an ordinal, and would change the persistence schema shape.

**Zoom is promoted from an ad-hoc fallback to a layout mode.** Today "the tree doesn't fit, render the focused leaf full-box" is written twice, in `terminalLeafBox` (`terminal_surface.go:~160`) and `renderDocumentSplit` (`doc_panes.go:620-628`), and the two are not obviously the same rule. Make it `Layout.Zoomed` — one rule, tested once. Design C's observation makes this the cheapest high-value feature in the design: **user-facing zoom (`alt+w z`) is literally the code path `fits=false` already takes.** It makes 80-column terminals viable with a 3-leaf layout, and it is the stand-in for floating panes.

**Refuse, don't squeeze — elevate to a law.**

> **Law 2.** A split that cannot meet its floors is not created, and the refusal is explained. A window shrink that breaks an existing layout degrades to zoomed-focused-leaf *without destroying the tree*; widening restores it. Drag clamps at the floor and never closes a pane by dragging it to zero.

The refusal machinery exists (`fits=false`, plus the toast pattern at `doc_panes.go:124-130`: *"Document pane needs a wider window; terminal left unchanged"*). This is tmux's "no space for new pane" and emacs's "Window too small to split", not i3's silent squeeze or VS Code's sliver. For a pane holding a live agent session, VS Code's drag-to-zero-closes is destructive-feeling; closes stay explicit.

**Ratios, not absolute cells.** Percentages survive constant TUI resize; cells don't. Already the model; keep it.

**Scheduled deletion of `paneMinRatio`/`paneMaxRatio`.** Design A is right that these are a second, weaker constraint system that can disagree with floors — and `layoutPaneNode` already re-clamps against floors anyway (`panetree.go:101`, `:107`). But the 15/85 clamp is *user-visible today on the terminal panel* and preserving it byte-for-byte is M1's ship criterion. **Resolution: keep it through the panel migration, delete it in the milestone that admits a third leaf**, where a 15% floor across three columns becomes unreachable and actively wrong. Scheduled deletion, not deferred decision.

### 1.6 The compositor: canvas blit, not lipgloss joins

Design B wins this outright; A and C don't address it.

`LayoutPanes` already returns an absolute `Box` per leaf and per divider. Composing with `JoinHorizontal`/`JoinVertical` re-derives that geometry a second time in string space, which requires exact per-row widths at *every* nesting level — `enforceLineWidths` (`terminal_panel.go:637`) and `padToHeight` exist purely to patch that at *one* level. At three levels the failure mode is a divider that walks sideways, visible only in a real terminal.

The canvas is ~80 lines and the ANSI-aware row splice already exists in prototype form: `compositeRow` in `internal/ui/overlay.go:65` (verified — it uses `ansi.Strip`, `ansi.Truncate`, `ansi.StringWidth` and pads). Generalize it; don't reinvent it.

```go
type canvas struct{ rows []string; w, h int }
func (c *canvas) blit(box mouse.Rect, content string)  // ANSI-aware clip + pad per row
func (c *canvas) String() string
```

The payoff beyond correctness: **hit regions register from the same `[]Placement`/`[]Divider` the canvas drew from** — one source for pixels and clicks, which `doc_panes.go:598-660` already does for the two-leaf case.

---

## 2. Content-type interface

### 2.1 Small core, optional capabilities

Design A's interface is the right end state. Design B's "per-kind switch until M3" is one milestone too conservative — the compositor is written against *something*, and a 4-method interface with two implementations costs no more than a switch while deleting the switch.

The core is deliberately minimal; capability is expressed by **optional interfaces**, following the precedent `internal/plugin` already sets (`TextInputConsumer`, `GlobalKeyBlocker`, `KeyRouter`, `FooterStatusProvider`). That precedent is the argument: a doc leaf has no native cursor, no transport to gate, no tmux geometry to assert, and a mandatory method invites a wrong stub.

```go
// Ships at M0 with two implementations (terminal, doc).
type Content interface {
    Kind() string                    // stable persistence + registry key
    Title() string                   // header identity
    SetSize(Size) tea.Cmd            // box MINUS the header row; cmd lets a terminal assert tmux geometry
    View(Render) string              // exactly Size.Height rows of exactly Size.Width columns
}

// Grows as milestones need them — none before its first real implementation.
type Updater    interface{ Update(tea.Msg) tea.Cmd }
type Focusable  interface{ Focus(bool) tea.Cmd }
type Closer     interface{ Close() }              // idempotent; safe before any SetSize
type Chrome     interface{ Chips(w int) []Chip; Hints(w int) string }
type Keys       interface{ HandleKey(tea.KeyPressMsg) (tea.Cmd, bool) }
type KeyOwner   interface{ OwnsKeyboard() bool; ClaimsKey(string) bool }
type Pointer    interface{ HandlePointer(PointerEvent) tea.Cmd; ClaimsPress() bool }
type Scrollable interface{ Scroll(rows int) bool }   // positive = down, RENDERED rows
type Visible    interface{ SetVisible(bool) tea.Cmd }
type Cursored   interface{ Cursor() *tea.Cursor }
type Sized      interface{ Floor() Floor }
type Persistable interface{ Encode() (json.RawMessage, bool) }
```

`Render` carries what the manager knows and the content does not: `Focused`, `Zoomed`, `Origin mouse.Rect` (absolute, for hit math), theme refs.

### 2.2 The terminal adapter and the vocabulary boundary

The mapping from the generic contract to the already-shared `internal/tty` rules is total, and this table *is* the design (Design A's, verified against `tty/window.go`):

| Contract method | Implementation |
|---|---|
| `Scroll(rows)` | `tty.ScrollWindowRows(&freeze, offset, rows, bound)` (`tty/window.go:65`) |
| `Focus(false)` | `tty.LeaveLiveWindow(&freeze, offset, bound)` (`tty/window.go:81`) |
| `View(r)` | `tty.PlaceWindow(&freeze, offset)` (`tty/window.go:26`) → `tty.FitViewport` → `termpreview.RenderBody` |
| `SetSize(s)` | `tty.ContentWidth` → lease-arbitrated `ResizeTmuxPane` |
| `SetVisible(v)` | `term.SetVisible(v)` |
| `HandlePointer` | `tty.PointerIntentFor` → `tty.Pointer.Press/DragTo/Release` |
| `HandleKey` | `Config.ResolveSurfaceChord` (`tty/surface_chords.go:39`) → `tty.MapScrollbackKey` |
| `Chrome.Hints` | `tty.AppendStatus(hint, tty.WindowStatus(…), …)` |

> **Law 3.** The manager's scroll vocabulary is `Scroll(rows int) bool`, positive downwards, rendered rows. If a future feature seems to need the manager to know *where a window sits* — a frame-drawn scrollbar, synced scrolling — the answer is a new capability interface answered by the content, never a field on the tree. The manager also never sees a `*tty.Model`.

### 2.3 Two classes of leaf, not one enum

Design C's sharpest contribution. The physical constraint: **a tmux session has one window with one size**, and every geometry change means a real resize, which means SIGWINCH into an agent that redraws. Existing mitigations — no resize during drag (`mouse.go:1519-1534`), 500 ms debounce — are not polish; they're why the feature is usable.

| Property | Live leaves (terminal) | Passive leaves (doc, diff, td issue, file) |
|---|---|---|
| Costs a tmux resize on geometry change | Yes — batch through `docTerminalResizeCmds` (`doc_panes.go:234`) | No |
| Duplicable in one layout | **No.** One session = one size. Selecting an already-displayed session focuses that leaf | Yes, freely |
| Closable with bare `q`/`esc` | No — `alt+w c` or the `×` chip | Yes (already the doc rule) |
| Width floor | 40 cols when not alone; hard floor from `termPanelMinBoxCols` | `markdown.MinWidthForMarkdown` |

The no-duplicate rule (all three designs' "Policy 1") is **forced by tmux, not chosen**. Making it per-class rather than global is what lets three td previews sit side by side while two views of one agent cannot. `SplitLeaf` refuses a duplicate live target.

**At most one live terminal at a time.** All three designs agree. The keyboard has one destination; a second live pane means a second escape-gate state machine and user-visible ambiguity about where the next keystroke lands. Cost: one keystroke to switch. tmux charges the same.

---

## 3. Interaction model

### 3.1 The prefix — Design C wins decisively

Designs A and B both propose `ctrl+w`. **Reject it.** `ctrl+w` is readline's `unix-word-rubout`, used constantly in every shell in every pane Sidecar hosts. Design C found that the repo's own deprecated tiling plan flagged this and shipped anyway: *"`C-w` followed by a non-tile key within 150 ms will not reach tmux. That is user-surprising."* tmux chose `C-b` specifically to dodge readline; vim's terminal mode needs `C-w .` as an escape hatch for exactly this. Do not re-learn it.

**Recommendation: `alt+w`, registered as a two-key sequence, configurable as `windowPrefixKey`.** Three verified grounds:

1. `alt` is already Sidecar's "the surface, not the pane" modifier during typing: `alt+c` copy, `alt+v` paste, `alt+t` panel layout.
2. Sequences already work — `internal/keymap/registry.go:11` sets `sequenceTimeout = 500 * time.Millisecond`, with `pendingKey`/`pendingTime` state (`registry.go:32-33`). Browse-state `alt+w v` needs **no new dispatch machinery**.
3. `w` is vim's window mnemonic, so the whole second-key vocabulary transfers free.

Configurability follows an exact existing precedent: `InteractiveExitKey` (`config/config.go:151`, `loader.go:317`, `saver.go:66`). A user whose inner app owns `alt+w` rebinds one string; a tmux-muscle-memory user sets `ctrl+b`.

**Timeout fallthrough is the honest behavior**: in typing state, `alt+w` followed by an unrecognized key or 500 ms of silence forwards *both* keystrokes to the pane. That is vim's `C-w .` problem solved without a second escape chord.

**Sequencing (Design B's correction to C):** browse-state prefix first — it's free. The typing-state pending-prefix is real work for a rarer journey; defer it one milestone. When it lands, it goes in `internal/tty` as a `SurfaceChords` member (`tty/surface_chords.go:24-52`), not in the plugin — that is the direct application of `td-de1ab2`'s thesis, and the file's own doc comment already states the rule: *"a rule expressed once, called by both surfaces, with no per-host translation."*

### 3.2 Binding table

**Prefixed (`alt+w …`), identical in browse and typing state:**

| Chord | Action | Prior art |
|---|---|---|
| `alt+w v` / `alt+w s` | Split side-by-side / stacked | tmux `%`/`"`, vim `:vsplit`/`:split` |
| `alt+w enter` | Open the sidebar selection in a split beside the focused leaf | VS Code "Open to the Side" |
| `alt+w c` | Close focused leaf (never the last) | vim `C-w c` |
| `alt+w o` | Close all others | vim `:only` |
| `alt+w z` | **Zoom** focused leaf ↔ restore | tmux `C-b z` |
| `alt+w h/j/k/l` | Focus geometric neighbour | vim / tmux |
| `alt+w w` | Focus next leaf, cycling | vim `C-w w` |
| `alt+w =` | Equalize ratios | vim `C-w =` |
| `alt+w H/J/K/L` | Resize focused leaf 5% toward that edge | vim `C-w +/-/</>` |

**Unprefixed in browse state — reuse of existing bindings, no new keys:**

| Key | Today | Under the tree |
|---|---|---|
| `tab` / `shift+tab` | Cycles sidebar → terminal → doc (`doc_panes.go:507`) | Cycles all leaves plus sidebar, in layout order |
| `h` / `l` / `←` / `→` | `focus-left` / `focus-right` between sidebar and preview | Geometric neighbour; falling off the left edge lands on the sidebar |
| `+` / `-` | Resize sidebar / resize doc split (`doc_panes.go:303`) | Same rule generalized: resize the focused thing's enclosing split |
| `ctrl+t` | Toggle terminal panel | Toggle a shell split beside the focused leaf — **same verb, tree implementation** |

`j`/`k` stay **scroll**, not navigation, matching what they mean in a focused pane today. Vertical navigation takes the prefix. The asymmetry mirrors vim, where `j`/`k` are cursor motion and window motion needs `C-w`.

Geometric neighbour is a pure function over placed boxes, not tree shape: *the leaf to the right is the one whose box starts at or after this box's right edge and whose vertical span covers this box's vertical centre.* Tree-shape navigation ("go to my parent's sibling") is what users complain about in tmux.

**Discoverability**, following the repo's own convention: register everything in a `workspace-window` keymap context so `?` auto-discovers it, and surface the focused subset through `Commands()` (`commands.go:9-24` already does this for `workspace-doc`). Never render hints in `View` — that's the app footer's job.

### 3.3 Key precedence ladder

Extend the app's existing levels with the pane layer slotted between plugin bindings and globals:

1. app modal
2. focused content's text-input/overlay context (`KeyOwner.OwnsKeyboard()`)
3. focused content's own bindings (`Keys.HandleKey`)
4. **pane chords** (`alt+w` prefix)
5. plugin contextual bindings
6. sidecar globals
7. unbound → forwarded to focused content

Level 2 is why the prefix exists: a live terminal owns essentially every key, so per-action chords would steal ~8 readline bindings from every shell. One prefix costs one chord.

### 3.4 "Open X in a split" — a verb, never a rule table

Take emacs's separation of *what to show* from *where it goes*; reject its `display-buffer-alist` indirection. **Placement is always the direct consequence of a gesture.** Three ways a split is born, each mapping onto an action Sidecar already has:

1. `alt+w v`/`s` on a live leaf → a new shell in the same workdir (tmux's semantic; `ctrl+n` new-shell already exists).
2. `alt+w enter` on a sidebar row → that workspace/shell opens beside the focused leaf.
3. Activating content inside a pane → it opens beside it (already shipped for markdown links → `openDocPaneForSurface`, `doc_panes.go:85-152`).

> **Law 4. Never create an empty pane.** tmux, vim, emacs and VS Code all avoid empty containers, because an empty box demands a second decision before it does anything. Every split is born with content.

**One extension worth taking**, because it removes an existing wart: the preview tab row (`,`/`.` → Output / Diff / Task) currently *hides* an open doc pane when you leave Output (`docVisible`, `doc_panes.go:262-265`). Make `alt+w enter` on the tab row **promote a tab into its own leaf**. That's how td previews and diffs become panes without inventing a picker, and it dissolves the hide-on-tab-switch quirk. Tabs stay "which content"; panes stay "where on screen."

### 3.5 Mouse — 80% already built

Keep exactly as-is: click-to-focus per leaf, 3-cell divider hit targets, live ratio during drag, **tmux resize only on release** (`mouse.go:1519-1534`), wheel scrolls the leaf under the pointer without focusing it. Four additions:

1. **Hover-lit dividers.** The only "cursor: col-resize" a TUI can offer, and the cheapest discoverability win available. **[correction to Design C]** — C claims `ActionHover` "exists and is unused here." It is *not* unused: `workspace/mouse.go:130` routes it to `handleMouseHover`, and `mouse.go:102`/`:107` use it for drag-abandon detection. What's missing is hover *styling* on dividers. The idea survives; the justification is wrong, so the work is "add a style branch," not "wire up hover."
2. **Double-click a divider = equalize that split** (the mouse's `alt+w =`). VS Code and i3 both do reset-on-double-click.
3. **Post-order divider registration.** With nesting, a point can fall within 3 cells of two dividers. `HitMap.Test` scans in reverse (`mouse/mouse.go:58` — verified), so registering innermost-last makes it win. Assert it in a test over a nested tree; this is the single highest-probability regression in the compositor work.
4. **One `regionPaneLeaf` with the leaf ID as `Data`**, replacing the per-kind region proliferation. Terminal leaves keep their gesture semantics through the existing shared arbiter (`terminalPointerIntent` → `tty.PointerIntentFor`).

**Gesture ownership** (Design A): the manager records the leaf on press and routes every subsequent drag/drag-end there regardless of where the pointer travelled — *"a selection dragged off the pane is still that pane's selection."* This deletes the per-host string lists of terminal region IDs and generalizes `tty.PressLeavesTerminal`.

**Not in v1: drag a pane to rearrange.** It needs drop-target hit testing and a distinct visual language, and `shift`/`alt` drag are already terminal selection gestures.

### 3.6 Focus indication — three signals, zero cells

Hard constraint: N leaves live inside **one** outer panel, and nested `RenderPanel` chrome is forbidden — 2 columns and 2 rows per leaf is fatal at 80 columns.

| Signal | Mechanism | Existing code |
|---|---|---|
| Focused leaf's header chip renders active | `styles.BarChipActive` | Already the doc-pane rule (`doc_panes.go:545-549`); extend to every kind |
| Dividers **adjacent to focus** render `BorderActive` | Generalize `paneTreeDividerStyle(focused)` from "doc focused" to "adjacent to focus" | `doc_panes.go:579-596` — tmux's active-border cue, in cells already spent |
| Outer panel border | Unchanged: sidebar vs preview, interactive gradient when any leaf is typing | `view_list.go:183-192` |

Plus the typing leaf's header keeps its existing hint string, so "which pane am I typing into" is answerable without moving.

**Explicitly rejected: dimming unfocused panes.** Agent output is the product. Dimming ANSI content is expensive per frame and destroys the color fidelity that makes agent output readable. tmux doesn't; neither should Sidecar.

### 3.7 Header ownership

Move the header row out of the content and into the frame. Today `renderDocPane` draws its own (`doc_panes.go:559-568`) and `termpreview.RenderBuffer` draws one inside the terminal body. Making it the frame's gives, once instead of per-content: focus indication, close affordance, click-header-to-focus, chip hit-region registration, and the guarantee that every leaf's body starts on the same relative row — the property `termpreview.HeaderRows` exists to state. Cost: a contained split of `RenderBuffer` into `RenderHeader` + `RenderBody`, with `RenderBuffer` kept as their composition.

**Do this in M0 while it's small**, not later when it's entangled with new features — it changes rendered bytes and forces re-recording the visual proof transcripts.

---

## 4. Scope: what we deliberately do not build

| Not building | Why |
|---|---|
| **A pane tree in `internal/overview`** | ~~Deferred.~~ **Overtaken by events:** overview has a tree. The concern this row raised was right about the mechanism and wrong about the mitigation — keeping the tree out did not prevent a second geometry computation, it just delayed it. The mitigation that works is a shared presentation layer both surfaces are obliged to use: `internal/paneframe` (td-b657fb). |
| **App-level windowing across plugins** | **Superseded by [Sidecar-wide content links and passive panes](sidecar-wide-content-links.md).** The requirement now exists for passive Document/Issue/Diff/Resource leaves beside Files, Git, and opt-in surfaces. The new plan reuses `panelayout`/`paneframe` through an app-owned host and optional plugin capabilities; it does not change the mandatory `plugin.Plugin` interface or put live-terminal splitting outside Workspaces. |
| **The sidebar as a tree node** | Its lifetime is per-plugin; the tree's is per-selection (`{Root, Surface}`). Merging would tie sidebar width to which shell is selected. It has its own drag and its split is shared with overview via `termpreview.SplitFor`. It is chrome, not content. |
| **Multiple simultaneous live panes** | §2.3. |
| **Duplicate views of one tmux session** | Forced by tmux. Refuse at split time. |
| **Floating panes** | Genuinely attractive for transient content (a td issue is often a glance, not a resident, and a float avoids N SIGWINCHes). But it is a *second geometry system*, which is this codebase's recurring failure mode. **`alt+w z` is the cheap stand-in** and uses a code path that already exists. Defer. |
| **Auto-tiling (bspwm-style)** | Its payoff is smallest at N=2–4 and its cost is highest when every reshuffle is a resize into a redrawing agent. Manual placement (i3-style) matches a workspace tool where each pane is a deliberate choice. |
| **Named/saved layouts, layout picker** | VS Code's implicit restore wins and Sidecar already implements it. `alt+w =` delivers ~90% of tmux's five presets at 2% of the UX surface. |
| **New tmux sessions on split** | A split shows an *existing* session or the panel session. Session creation stays where it is. |
| **A CLI surface for splitting** | **Overtaken by `sidecar open`.** Sidecar does own presentation and pane placement, so agents may request a file, issue, diff, or provider resource through the implemented shell-targeted UI request path. Arbitrary pane-tree manipulation and ambiguous “current visible plugin” targeting remain out of scope. |

---

## 5. Incremental delivery plan

Each milestone compiles, ships, and leaves the product working. Gate user-visible ones behind a new `workspace_splits` flag (default **false**) alongside `WorkspaceDocPanes` (verified: `features/features.go:69-73`, default true).

### M0 — Compositor and seam (invisible; no rendered-byte change)

1. `termpreview.Box = mouse.Rect` (§1.3). Prerequisite for everything.
2. Split `termpreview.RenderBuffer` into `RenderHeader` + `RenderBody`; move header drawing to the frame (§3.7).
3. Replace the two-leaf renderer with the recursive **canvas blit** (§1.6). Delete `doc_panes.go:629`'s bail and the four ordering branches at `:661-672`.
4. Introduce the 4-method `Content` interface with two implementations; the per-kind switch disappears.
5. Promote `fits=false` to `Layout.Zoomed` — one rule replacing the two copies at `terminal_surface.go:~160` and `doc_panes.go:620-628`.
6. Register hit regions from the same `[]Placement`/`[]Divider` the canvas drew from, **post-order for dividers** (§3.5).
7. Fix the floors duplication: `terminalLeafBox` builds a `Floors` literal inline (verified, `terminal_surface.go:~150`) instead of calling `paneTreeFloors()` (`doc_panes.go:73`). Two copies, one authority needed.

**Ship criterion:** `doc_panes_test.go` (785 lines), `panetree_test.go`, and `pane_tree_geometry_test.go` pass unedited. New tests compose a 3-leaf and a nested 4-leaf tree and assert every cell. Re-record visual proof transcripts here, while the diff is small.

**Why first:** this is the only place the code makes a *structural* two-pane assumption. Everything after assumes two *terminals*, which is a different and more mechanical problem.

### M1 — STEEL THREAD: the terminal panel becomes a tree leaf, and the user creates it with a split gesture

**The demo:** in the workspace preview, press `alt+w s`. A second live terminal appears below the first. Drag the divider — both tmux panes resize once, on release, never during the drag. Press `alt+w z` to zoom one full-box and again to restore. Quit and relaunch; the layout comes back.

This costs **zero new terminal plumbing**, because the second terminal already exists as `p.panelTerminal` (verified, `terminal_control.go`).

1. Give the panel a leaf via `Slot int` on `PaneNode` (0 = primary, 1 = panel), so `termPanel bool ≡ slot == 1` — a **value-preserving** change that leaves all 468 existing `termPanel` sites valid.
2. Route panel geometry through the tree: `terminalLeafBox()` → `terminalLeafBoxFor(slot)`; `calculateTermPanelDimensions` / `calculateAgentPaneDimensions` become `termpreview.SurfaceIn(leafBox)` readers; `terminalSurfaceGeometry`'s bespoke offset walk (`terminal_surface.go:200-224`) collapses to a leaf-box lookup.
3. **Delete, don't wrap:** `termPanelSplitBoxes`/`termPanelBottomBoxes`/`termPanelRightBoxes` (`terminal_panel.go:215-284`); `renderOutputWithTermPanel`/`renderShellWithTermPanel` (`:489-596`); `regionTermPanelDivider` and its inverted drag math (`mouse.go:1443-1466`); `TermPanelLayout` and `alt+t`. Bottom ≡ `SplitRows`, right ≡ `SplitCols`, `termPanelSize` ≡ ratio, clamps already identical.
4. Migrate persisted `termPanelLayout` + `termPanelSize` → a `PaneLayoutJSON` split, once. **Table-test both layouts** — A/B ordering inverts the ratio, and getting it wrong flips every existing user's panel to the other side on upgrade.
5. Keys: `alt+w` prefix in the keymap registry for browse state only (§3.1); `v`/`s`/`c`/`w`/`z`/`=`/`hjkl`. `ctrl+t` **keeps working**, redefined as "split with / close the panel terminal at the persisted axis and ratio" — its tests stay meaningful. Inside interactive mode, `alt+w` still goes to tmux; exit first. Defer the pending-prefix.
6. With one panel slot, a second `alt+w v` refuses with a toast. Honest, testable, removed by M2.
7. Widen `supportedDocPaneTree` (`doc_panes.go:413`) to allow a second terminal leaf.

**Ship criterion:** `terminal_window_parity_test.go`, `interaction_parity_test.go`, `terminal_surface_test.go`, `scroll_test.go` pass unedited. Real-app proof **on a private tmux socket** showing `alt+w s` → two live terminals → divider drag → one resize per pane on release.

**What the user gains that they cannot do today:** split in either direction *at a chosen position* rather than bottom/right of the whole preview; *terminal | doc* and then *terminal above / terminal below* inside the terminal's half; one uniform divider drag; one uniform focus model; zoom.

### M2 — Lift the two-terminal cap

Now the mechanical conversion, motivated by a shipped feature rather than speculation.

- Extract `termPane{ leafID, target, model, output, scroll, freeze, freezeDoc }`; `p.terms map[int]*termPane`. **This deletes the six duplicated window-function pairs** (`plugin.go:1350-1421` vs `terminal_panel.go:330-402`) — one implementation, twelve functions become six.
- `workspaceTerminalRole` → map key; `reconcileTerminalModels` ranges over terminal leaves. Verified: the reconcile body is already role-generic (`reconcileTerminalModel(role, desired, wanted)`, `terminal_control.go:237`), and `terminalModelAndTarget`/`setTerminalTarget`/`bindTerminalBuffer` are two-armed switches whose bodies are untouched by the change.
- The per-surface caches are **already string-keyed, not bool-keyed** — verified: `terminalHistoryKey(kind, target)` (`terminal_history.go:33`), `recordPaneGeometry` (`pane_geometry.go:17`), `pollScheduler tty.KeyedScheduler` (`plugin.go:245`). These are N-terminal-ready today.
- `Slot int` → `TermRef` on the leaf; `InteractiveState.TermPanel bool` (verified, `types.go:357`) → `PaneID int`.
- `termPanel bool` → `*termPane`, file by file behind transitional shims. **[correction to Design B]** — the actual spread is **468 occurrences across 22 non-test files, in 31 `termPanel bool` signatures** (B said 400/20/31). Requirement: every slice deletes its own shim in the same commit that adds the typed signature. A shim surviving a review round is the smell.
- Enforce the one-session-one-leaf refusal in `SplitLeaf`.
- Delete `paneMinRatio`/`paneMaxRatio` (§1.5); floors become the only constraint.
- Cap live terminal leaves (4) and `SetVisible(false)` the least-recently-focused beyond it.

### M3 — More content kinds, and the package extraction

- Extract `internal/panetree` (now genuinely shared) and `internal/pane`; grow the optional capability interfaces as their first implementations arrive.
- `alt+w enter` from the sidebar; tab promotion from the preview tab row (§3.4).
- td issue leaves (extract fetch/render from `internal/app/issue_preview.go` into a component — a prerequisite regardless), file leaves, diff leaves.
- Multiple docs are nearly free: `p.docs` is already `map[int]*docPane`; only `activeDocPane` (`doc_panes.go:61`) and the retarget early-return (`:99-114`) enforce the cap.
- Typing-state pending-prefix into `tty.SurfaceChords`.
- Mouse polish: hover-lit dividers, double-click equalize.

### Persistence (lands across M1–M3)

Evolve `state.PaneLayoutJSON` rather than replacing it. **[correction to Design A]** — the current struct has `Root`, `Surface`, `Kind`, `Split`, `Tabs`, `Active` and **no `Focus` field** (verified, `state.go:77-92`); focus persistence is an addition, and it must be a **path of child indices**, not a NodeID, because IDs are re-minted at decode.

- **Content owns its own state blob.** `Persistable.Encode()` returns opaque JSON; a registry keyed by `Kind` decodes it. Keep `Tabs`/`Active` readable for one release.
- **Terminal leaves persist a target selector, not a pane id.** A tmux pane id is meaningless across restarts. Verified: `workspaceTerminalTarget` already carries `Source`/`SourceID` (`terminal_control.go:46-53`) — that's the durable identity.
- **Unknown kind ⇒ drop the leaf, collapse its split, keep the rest**, with a toast naming what went away. Today an unsupported tree resets to a single terminal (`doc_panes.go:400-403`) — far too blunt once kinds are open; one future kind in a hand-edited file would discard a working four-pane layout. Exactly one hard refusal survives: zero restorable leaves ⇒ default leaf.
- **Scope becomes a map with a bounded LRU (~32).** Today `persistedPaneLayout` refuses on selection change (`doc_panes.go:339-357`), so switching shell A → B → A loses A's layout. A free user-visible improvement.
- No save command, no named layouts, no picker. Per project root, never synced.

---

## 6. Testing

The design's value is that the interesting logic is pure and headless.

- **Tree:** table-driven goldens — tree × box × floors → placements and dividers. No bubbletea, no tmux.
- **Manager with a `fakeContent`** recording `SetSize`/`Focus`/`View`/`Close`: proves the sizing fan-out (one `SetSize` per *changed* leaf, none for unchanged), close ordering, focus routing, gesture ownership across leaf boundaries, and zoom degrade — without a tmux server. `docTerminalResizeCmds` exists for exactly this reason (`doc_panes.go:231-245`); preserve the inspectable-slice-of-cmds technique and generalize it.
- **Hit-region ordering over a nested tree** — post-order dividers (§3.5). Highest-probability regression.
- **Persistence round-trip + hostile input:** cycles, shared nodes, duplicate IDs, unknown kinds, a 3% ratio, an unresolvable target. `inspectPaneTree` (`panetree.go:198`) was clearly written against hostile input; keep it as-is.
- **The panel migration table-test**, both layouts, ratio inversion (§5 M1.4).
- **tmux isolation is non-negotiable.** Anything touching tmux keeps the private-server `TestMain` discipline (`internal/plugins/workspace/tmux_isolation_test.go`). N terminals multiplies the blast radius.

---

## 7. Risks

| Risk | Mitigation |
|---|---|
| **Resize storms into agents** | Never resize during drag (`mouse.go:1519-1534`); per-leaf 500 ms debounce; batch through the inspectable `docTerminalResizeCmds` so a test proves one command per visible surface |
| **N control subscriptions.** Four visible terminals = four control clients, mailboxes, poll schedules. `ControlManager` multiplexes but has never been asked for four at once from one plugin | Load proof before M2 ships; live-leaf cap with LRU `SetVisible(false)` |
| **N geometry leases.** A pane whose size never changes still needs `TouchGeometryLease` or it looks abandoned to a peer sidecar | Test it explicitly per adapter; it's the easiest thing to miss |
| **A second geometry authority reappears** | Everything reads its container from the leaf box; assert `terminalSurfaceGeometry` agrees with `LayoutPanes` for every live leaf |
| **The M2 conversion stalls half-done** | Every slice deletes its shim in the same commit |
| **Panel migration flips sides on upgrade** | Table-test both layouts, both ratio directions |
| **Header move changes rendered bytes** | Re-record proof transcripts in M0, while the change is isolated |
| **Prefix collides with an inner app** | 500 ms fallthrough forwards both keys; `windowPrefixKey` configurable per the `InteractiveExitKey` precedent |
| **Two split systems drift further** | M1 exists solely to prevent this; it is the `td-6b3fe5` failure re-run |
| **M1 lands on ground that's still moving** | The open children of `td-de1ab2` — `td-7425ad`, `td-947d3a`, `td-bda06e`, `td-323afe`, `td-19c9cb` (verified open) — are all *"this rule still lives in two places"* items, and **every one is fixed for free by there being one `termPane` instead of two field families**. Landing them first is the same work done incrementally against tests that already exist. Strong candidate for pre-M1 or in-parallel work. |

---

## 8. Open decisions for Marcus

1. **Prefix key.** `alt+w` default (recommended), `ctrl+b` for tmux muscle memory, or ship `windowPrefixKey` **empty** so window commands work only in browse state until opted in? The empty default is zero-collision but loses the one-vocabulary-in-both-states property that holds the design together. A week of dogfooding decides it; the config key makes reversal a one-line default change.
2. **Unprefixed `h`/`l` becoming geometric neighbour.** This is the best single UX call in the design (no new key, no new concept, the sidebar already *is* the leftmost pane) — but it redefines existing `focus-left`/`focus-right` bindings. Accept the behavior change, or keep `h`/`l` as sidebar↔preview only and put all navigation behind the prefix?
3. **What `alt+w v` does on a live leaf.** New shell in the same workdir (tmux's answer, recommended), or duplicate-the-view? Duplication is impossible for live leaves and trivially useful for passive ones, so kind can decide it — at the cost of one rule users must learn. A uniform "always a new shell" is simpler to explain.
4. **Live-leaf cap.** 4? And do unfocused live leaves keep polling, or drop to `SetVisible(false)` past a threshold? This is the main cost driver and it needs a load proof before M2.
5. **Tab promotion.** Should `,`/`.` tabs (Output / Diff / Task) become promotable to leaves, dissolving the `docVisible` hide-on-tab-switch quirk? It's the cleanest way for diffs and td previews to become panes without a picker, but it makes tabs and panes overlap conceptually.
6. **Does the global overview ever get splits?** **Resolved: yes, it has them.** The recommendation here was no, and it did not hold — the global Workspaces browser ("Sessions") grew a `panelayout` tree with document, issue and diff leaves. What the recommendation was protecting against — a second independent geometry computation — was then real for a while: the global surface drew one outer frame around borderless leaves while the project surface gave every leaf its own chrome and shared drag handles.

td-b657fb closes that by extracting the presentation half of the tree into `internal/paneframe` and driving both surfaces from it. The extraction now targets **two** consumers, and the invariant is stated where an implementer will hit it: `AGENTS.md`, `.claude/skills/drag-pane/SKILL.md`, and each surface's `pane_host.go`. Anything affecting panes, splits, handles, borders, focus chrome, or pane hit regions belongs in `paneframe` and reaches both surfaces at once.
7. **M2 as one branch or a rolling series?** 468 occurrences across 22 files. A rolling series with shim-deletion discipline is safer but keeps two vocabularies alive for weeks.
8. **Should the `td-de1ab2` open children land before M1?** They are the same work at smaller grain, they have existing tests, and M1 subsumes them. Doing them first de-risks M1 substantially but delays the demo.

---

## 9. Scorecard — who won what

| Question | Winner | Why |
|---|---|---|
| Core layout model | **all three agree** — keep the binary tree | It's shipped, tested, and already general |
| Package extraction timing | **B** (defer) with **A/C**'s destination | The `Box` alias must be fixed now; the package move buys nothing until a second consumer exists |
| Compositor: canvas vs joins | **B** | Boxes are already absolute; joins re-derive geometry in string space and need `enforceLineWidths` at every nesting level |
| Content abstraction | **A**, introduced one milestone earlier than **B** wanted | A 4-method interface with two implementations costs no more than the switch it deletes |
| Prefix key | **C**, decisively | `ctrl+w` is `unix-word-rubout` in every shell Sidecar hosts; A and B both propose it without addressing that |
| Steel thread | **B** | Absorbing the panel delivers a real user-created split with zero new terminal plumbing; A's equivalent is four refactor phases out |
| `Slot int` before `TermRef` | **B** | Value-preserving against 468 sites; the typed conversion cannot be inside the steel thread |
| Global overview gets the tree | **C** (no) over **A** (yes, at P4) | `termpreview`'s package doc records this as a deliberate decision, not an omission |
| Persistence rules | **A** | Drop-and-collapse beats whole-tree reset; the scope LRU is a free user-visible win |
| Focus indication, refuse-don't-squeeze, zoom | **C** | Only design to address them, and it connects zoom to the `fits=false` path that already exists |
| Two classes of leaf (live vs passive) | **C** | The tmux one-session-one-size constraint is forced, and per-class beats global |
