# Doc Panes on a Preview Pane Tree

Click a markdown path in a workspace or shell terminal → the document opens in a
resizable pane beside the terminal, inside the Workspaces tab. The pane is not a
special case: it is one leaf of a general **pane tree** that owns the preview
region's geometry, so a second doc stacked below the first, or a terminal split
next to a doc, are later leaves on the same substrate rather than new layout code.

Supersedes `../deprecated/workspace-tiling-plan.md`. That plan's tree model was
right and its tmux-resize research still holds, but it started from "tile N tmux
sessions", which is the hardest possible first leaf: every tile is a live PTY
that must be sized, polled, focused and generation-tracked. A doc leaf has no
PTY, no poll loop and no resize contract with tmux — it is the cheapest way to
make the tree real and load-bearing. Tmux tiling then becomes "add a second
terminal leaf kind" on a tree that already ships, and is folded in here as
Phase 7.

---

## 1. What exists today (verified against current `main`)

### 1.1 Link clicking already works — it just goes somewhere else

`internal/plugins/workspace/terminal_links.go` already:

- detects URLs and `path:line` links in captured terminal lines
  (`detectTerminalLinks`, `terminal_links.go:56`), decorates them with underline
  and OSC-8 (`decorateTerminalLinks:104`), and strips source-supplied OSC to keep
  untrusted tmux output from forging hyperlinks (`stripSourceOSC8:122`);
- resolves a clicked path against the workspace root with symlink resolution and
  a containment check (`resolveTerminalPath:302`), rejecting escapes and
  non-regular files;
- activates on click from both the passive preview and interactive mode
  (`mouse.go:671`, `mouse.go:692`, `interactive_selection.go:422`).

Today activation calls `openTerminalPath` (`terminal_links.go:264`), which emits
`filebrowser.NavigateToFileMsg` and **switches tabs** to the file browser
(`app.FocusPlugin("file-browser")`), optionally switching worktree first.

So the plumbing this feature needs — detection, safety, hit testing, resolution —
is done. What changes is the *destination* for a subset of links.

### 1.2 Preview geometry has a single authority

`internal/plugins/workspace/terminal_surface.go` is the current source of truth:

- `previewSplitFor(width)` (`:66`) returns `previewSplit{SidebarWidth,
  PreviewX, PreviewWidth, ContentX, ContentWidth, …}` — sidebar/divider/preview
  columns, computed once and read by the render path, the cursor path and the hit
  tests. Floors: `sidebarMinWidth = 15`, `previewMinWidth = 40`.
- `previewContentY()`, `previewBorderRows`, `terminalHeaderRows`,
  `previewTabRows` name the vertical stack.
- `terminalSurfaceGeometry(termPanel bool)` (`:246`) locates one embedded
  terminal and reports its viewport size.

The file's own comment records why: seven call sites used to recompute this and
drift. **Any pane tree must extend this authority, not sit beside it.** A second
independent geometry source in the preview region is the single biggest failure
mode of this work.

### 1.3 The terminal panel is the existing two-pane split

`terminal_panel.go` splits the preview into primary terminal + aux terminal,
`TermPanelRight` (columns) or `TermPanelBottom` (rows):

- box arithmetic in `termPanelBottomBoxes:254` / `termPanelRightBoxes:270`, with
  floors `termPanelMinBoxCols = 10` / `termPanelMinBoxRows = 3` and a `fits`
  verdict that every caller must respect ("no panel is drawn, size nothing");
- divider drag via `regionTermPanelDivider`, percentage-based, `StartDrag` at
  `mouse.go:697`, applied at `mouse.go:1431`, tmux resize only at drag end
  (`mouse.go:1487`);
- persisted as `TermPanelSize` / `TermPanelLayout` / `TermPanelVisible` in
  `internal/state/state.go:23-25`.

This is the shape to generalize: percentage ratio, minimum box floors, a `fits`
verdict, divider hit region registered over the drawn boundary, resize-on-release.

### 1.4 Doc rendering components exist and are reusable

- `internal/markdown` — `Renderer.RenderContent(content, width) []string`, Glamour
  behind a width-keyed cache, theme via `styles.GetMarkdownTheme()`, plain-wrap
  fallback below `MinWidthForMarkdown = 30`.
- `filebrowser.LoadPreview(rootDir, path, epoch) tea.Cmd` → `PreviewLoadedMsg`
  with `PreviewResult{Lines, HighlightedLines, IsBinary, IsImage, IsTruncated,
  …}`; size cap 500 KB, line cap 10 000, chroma highlighting via
  `filebrowser.Highlight`.
- `internal/image` for image files.
- The workspace plugin **already imports `filebrowser`** (`terminal_links.go:17`),
  so reuse costs no new dependency direction.

What does *not* exist as a reusable unit is the viewport: filebrowser's scroll,
search-highlight and render loop are methods on its `Plugin` (`operations.go`,
`view.go`, `ansi_highlight.go`), reading `p.previewLines`, `p.markdownRendered`,
`p.markdownRenderMode`. Extracting all of that now would be a large refactor of a
working plugin with no forcing function yet. See §3.2 for the chosen middle path.

### 1.5 Hit regions, drag, persistence

- `internal/mouse` `HitMap.AddRect`, reverse-order `Test` (last registered wins);
  regions cleared at the top of every `View` (`view_list.go:52`).
- Region IDs are plain string constants in `plugin.go:42-116`.
- Per-project workspace state is `state.WorkspaceState` keyed by project root
  (`state.go:58`), currently selection only.

### 1.6 Feature flags

`internal/features/features.go` — a `Feature{Name, Default, Description}` literal
plus an entry in `allFeatures`. Add `workspace_doc_panes`, default **off** until
Phase 5.

---

## 2. Goals and non-goals

**Goals**

1. Clicking a markdown link in a workspace or shell terminal opens that document
   in a pane to the right of the terminal, inside the Workspaces tab, without a
   tab switch.
2. The boundary is drag-resizable with the same feel as the existing dividers, and
   the ratio persists per project.
3. The pane closes (`q` / `esc` when focused, or a close affordance), and focus
   moves back to the terminal.
4. The layout is a **tree**, not a boolean. Nothing in the render path, the hit
   testing, the sizing or the persistence format assumes "one terminal, optionally
   one doc on the right".
5. Terminal behavior is byte-for-byte unchanged when no doc pane is open.

**Non-goals for this plan**

- Tiling multiple tmux sessions (that is Phase 7 / the folded tiling plan).
- Making the doc pane editable — it is a preview. The inline editor stays in the
  file browser.
- A CLI/API surface. Per `CLAUDE.md` §2, Sidecar is a presentation layer over
  capabilities it does not own; opening a viewer pane is not an owned capability.
  The layout math and link routing still land in state-free functions (§3.1,
  §3.5) so they are testable and portable if that ever changes.
- Floating/zoomed panes, pane drag-to-rearrange, named layouts.

---

## 3. Design

### 3.1 The pane tree

New file `internal/plugins/workspace/panetree.go` — pure data and pure functions,
no `Plugin` receiver, fully unit-testable.

```go
type PaneKind int

const (
    PaneTerminal PaneKind = iota // the workspace/shell terminal surface
    PaneDoc                      // a document viewer
)

type SplitAxis int

const (
    SplitCols SplitAxis = iota // children side by side, vertical divider
    SplitRows                  // children stacked, horizontal divider
)

// PaneNode is a leaf when Split == nil, an internal node otherwise.
type PaneNode struct {
    ID    int // stable identity for focus, close, hit-region Data
    Kind  PaneKind
    DocID int // PaneDoc only: index into the plugin's doc registry

    Split *PaneSplit
}

type PaneSplit struct {
    Axis  SplitAxis
    Ratio int // percent of the box given to A, clamped [minRatio, maxRatio]
    A, B  *PaneNode
}

// Box is a rectangle in preview-content coordinates (origin = ContentX,
// previewContentY()).
type Box struct{ X, Y, W, H int }

type Placement struct {
    Node *PaneNode
    Box  Box
}

type Divider struct {
    SplitID int // ID of the internal node
    Axis    SplitAxis
    Box     Box // 1 cell thick, full length of the boundary
}

// LayoutPanes places every leaf and every divider inside box.
// fits is false when the floors cannot be met — the caller then falls back to
// rendering the focused leaf full-box, exactly as termPanelSplitBoxes does today.
func LayoutPanes(root *PaneNode, box Box, floors Floors) (leaves []Placement, dividers []Divider, fits bool)
```

Plus pure mutators, each returning a new focus target so callers never guess:
`SplitLeaf(root, leafID, axis, newLeaf) (*PaneNode, int)`,
`ClosePane(root, leafID) (*PaneNode, int)` (sibling subtree replaces the parent),
`FindPane(root, id)`, `Neighbor(root, fromID, dir)` (geometric, from the
`LayoutPanes` boxes), `SetRatio(root, splitID, ratio)`.

Why a tree and not a list: closing a leaf must collapse its parent into its
sibling, and "split the right half again" must not require re-deriving structure.
Both are three lines on a tree and a rewrite on a list.

**Floors** come from the existing constants rather than new ones: a terminal leaf
gets `termPanelMinBoxCols`/`termPanelMinBoxRows`; a doc leaf gets
`markdown.MinWidthForMarkdown` columns (below that Glamour is abandoned anyway)
and 3 rows. `LayoutPanes` returns `fits == false` rather than drawing a box below
its floor — same contract as `termPanelSplitBoxes`, for the same reason.

### 3.2 The doc pane component

New package `internal/docview`. Deliberately *not* a plugin and not tied to
either plugin's struct:

```go
package docview

// Model is one document in one box: load state, render cache, scroll position.
type Model struct { /* unexported */ }

func New(renderer *markdown.Renderer) *Model

func (m *Model) Load(rootDir, relPath string, line int) tea.Cmd // wraps filebrowser.LoadPreview
func (m *Model) SetResult(res filebrowser.PreviewResult, path string)
func (m *Model) SetSize(width, height int)   // invalidates the render cache on width change
func (m *Model) View() string                // exactly height rows, never wider than width
func (m *Model) HandleKey(k tea.KeyMsg) (handled bool)
func (m *Model) Scroll(delta int)
func (m *Model) ToggleRenderMode()           // rendered markdown ⇄ raw/highlighted
func (m *Model) Title() string               // relative path, for the header chip
```

It composes what already exists: `filebrowser.LoadPreview` for IO,
`markdown.Renderer` for rendering, `filebrowser.Highlight` output for raw mode,
`internal/image` for images. It owns only the part that has no home today —
scroll window, mode toggle, header row, and clamping output to exactly the box.

Import direction is `docview → filebrowser` (loading) and
`workspace → docview`. That is one new edge and no cycle. Moving `LoadPreview`
down into `docview` and having filebrowser depend on it is the tidier end state
and the natural Phase 7 cleanup, but doing it now widens the blast radius into a
plugin this feature otherwise does not touch — so: not now, noted as follow-up.
Likewise, filebrowser adopting `docview.Model` for its own preview pane is the
payoff that would justify the extraction; it is explicitly out of scope here and
should only happen once `docview` has proven itself under the workspace plugin.

Header row: one row, same `terminalHeaderRow` helper the terminal surfaces use
(`terminal_surface.go:118`) — chips left (path, `raw`/`rendered` mode), hints
right (`q close · r raw`). This keeps every pane in the preview region on the
same one-header-row stack, so `previewContentY() + terminalHeaderRows` stays a
universal fact.

### 3.3 Plugin state

On `Plugin`:

```go
paneRoot   *PaneNode          // nil ⇒ legacy single-terminal path (flag off)
paneFocus  int                // ID of the focused leaf
paneNextID int
docs       map[int]*docview.Model // DocID -> viewer
```

`paneRoot == nil` is the pre-flag path and must stay working through Phase 5.
When the tree holds exactly one `PaneTerminal` leaf, the render path must produce
**identical bytes** to today (Phase 1's acceptance test).

### 3.4 Geometry: extending the existing authority

`terminalSurfaceGeometry(termPanel bool)` keeps its signature and its meaning; its
*implementation* gains one step. Today it starts from
`previewSplit().ContentX` / `previewContentY()` and hands the whole preview to the
terminal (or to the term-panel split). With a tree it starts from the
`PaneTerminal` leaf's `Box` as returned by `LayoutPanes`, then applies the
existing term-panel subdivision inside that box unchanged.

Concretely, one new private helper is the seam:

```go
// terminalLeafBox returns the box the terminal surface occupies inside the
// preview content area. Without a tree, or with a single-terminal tree, it is
// the whole preview content area — which is what every caller assumed before.
func (p *Plugin) terminalLeafBox() Box
```

`terminalSurfaceGeometry`, `calculatePreviewDimensions`,
`calculateAgentPaneDimensions` and `calculateTermPanelDimensions` all take their
container from `terminalLeafBox()` instead of from the raw preview split. That is
the whole geometry change, and it is why the terminal panel does **not** need to
become a tree node in v1: it keeps subdividing its own box, which merely got
smaller. Folding it into the tree is Phase 6, where it earns its keep.

**Consequence to handle deliberately:** opening or resizing a doc pane changes the
terminal's box, so it must trigger the same tmux resize path as a sidebar drag —
`resizeSelectedPaneCmd()` + `resizeTermPanelPaneCmd()`, on open, on close, and on
divider release (never during drag; see `mouse.go:1487` for the precedent).

### 3.5 Link routing

`openTerminalPath` (`terminal_links.go:264`) gains a branch *before* the existing
tab-switch path. The decision is a pure function so it is testable and has one
definition:

```go
// docPaneTarget reports whether a resolved link should open in a doc pane rather
// than switching to the file browser.
func docPaneTarget(rel string, sameWorktree bool) bool
```

v1 rule: **`.md` / `.markdown`, and only when the file resolves inside the current
worktree.** Everything else keeps today's behavior exactly, including the
cross-worktree `SwitchWorktree → FocusPlugin → NavigateToFileMsg` sequence, which
a pane in *this* worktree's tab cannot honor.

Modifier override, both directions, so neither destination is a trap:

| Gesture | Result |
|---|---|
| Click a `.md` link | Opens in a doc pane |
| `alt`-click a `.md` link | Opens in the file browser (today's behavior) |
| Click any other file link | File browser (unchanged) |

`alt` is chosen because `mouse.go:669` already excludes `alt` and `shift` from
link activation to reserve them for selection — so the doc-pane branch reads the
same guard and inverts only inside it. (If `alt`-click turns out to be taken by a
terminal selection gesture in interactive mode, fall back to `shift`; the check
lives in one place either way.)

Which text is *recognized* as a link is a separate question from where it opens,
and v1 inherits today's `path:line` requirement — bare `foo.md` mentions become
clickable in Phase 6.

Opening reuses an existing doc leaf if the tree already has one: the newest doc
pane is retargeted to the new path rather than accumulating panes on every click.
A second pane only appears when the user explicitly splits (Phase 7), and a
second document in the same pane when tabs land (Phase 8). This is the
same "don't surprise the user with unbounded panes" instinct as Policy 1 in the
tiling plan, and it keeps v1's tree at most two leaves deep in practice while the
code stays general.

`NavigateToFileMsg` carries `Line`; the doc pane honors it by scrolling the
target line into view (raw mode) or by falling back to the top in rendered mode,
where source line numbers do not survive Glamour.

### 3.6 Focus, keys, mouse

Focus is `p.paneFocus`. In v1 the focusable set is {terminal leaf, doc leaf}.

| Key | Context | Action |
|---|---|---|
| `tab` | list view, preview focused | cycle focus between panes (only bound when >1 leaf) |
| `q` / `esc` | doc pane focused | close the pane, focus the terminal |
| `j`/`k`, `ctrl+d`/`ctrl+u`, `g`/`G`, `pgup`/`pgdn` | doc pane focused | scroll |
| `r` | doc pane focused | toggle rendered ⇄ raw |
| `+` / `-` | doc pane focused | resize the enclosing split by 5% |

Registered under a new keymap context `workspace-doc` so the footer hints come
from `Commands()` (`commands.go`) and nothing is rendered as a footer by the
plugin (`AGENTS.md`). Existing `workspace-list` / `workspace-preview` /
`workspace-interactive` bindings are untouched.

**Interactive mode is the sharp edge.** When the terminal leaf is interactive,
keys are forwarded to tmux — so doc-pane keys must not be. Rule: while the
terminal leaf is interactive, the doc pane is visible but not focusable by
keyboard; clicking it exits interactive mode first (this is exactly what
`handleMouseClick`'s `default:` branch already does for clicks outside both
terminal panes, `mouse.go:641`). No prefix chord in v1. `ctrl+w` stays reserved
for Phase 6, unbound until then.

Mouse:

- `regionDocPane` per doc leaf, `Data` = leaf ID → click focuses, wheel scrolls.
- `regionPaneTreeDivider`, `Data` = split ID → `StartDrag` with the current ratio,
  ratio updated during drag (cheap: doc panes re-render locally), tmux resize
  emitted only on release.
- Registration order in `renderListView`: pane bodies first, then dividers, then
  the preview tab chips — preserving the existing "last registered wins" rule and
  the note in `drag-pane/SKILL.md`.

### 3.7 Persistence

Extend `state.WorkspaceState` (per project root):

```go
PaneLayout *PaneLayoutJSON `json:"paneLayout,omitempty"`
```

serialized structurally, not positionally:

```json
{"split":{"axis":"cols","ratio":60,
  "a":{"kind":"terminal"},
  "b":{"kind":"doc","active":0,
       "tabs":[{"path":"docs/plans/active/foo.md","mode":"rendered"}]}}}
```

The doc leaf carries a `tabs` list rather than a single `path` even though v1
never writes more than one entry — see §3.8.

Restore in `restoreSelectionState` (`plugin.go`): validate each doc leaf's path
still exists and still resolves inside the worktree (reuse `resolveTerminalPath`);
drop leaves that fail and collapse the dangling split. Save on open, close, retarget
and divider release — the same set of moments that already call
`saveSelectionState`.

Ratios are percentages, matching `TermPanelSize`, so the same clamp
(`[15, 85]`) and the same drag math apply.

### 3.8 Tabs inside panes (design now, build later)

Panes and tabs are orthogonal axes and the design must keep them that way: a pane
is *where* on screen, a tab is *which document* in that place. The end state is
that any doc pane can hold several open documents with a tab strip, and the file
browser's preview pane is one such pane — so "three markdown files open in the
files plugin, one pane" and "one markdown file beside a terminal in the
workspaces tab" are the same mechanism at different settings.

Prior art already exists and should be generalized rather than reinvented:
`internal/plugins/filebrowser/tabs.go` has a working file-tab implementation —
`FileTab`, preview vs. pinned open modes (`TabOpenMode`), `openTab` /
`switchTab` / `cycleTab` / `closeTab`, a scrolling tab strip with overflow
indicators (`renderPreviewTabs`, `visibleTabRange`), per-tab scroll and edit
state, and invalidation when files are deleted or their directories change. That
is most of a tab model; what it lacks is independence from the filebrowser
`Plugin` struct.

What this plan commits to now, so the later change is additive:

- **`docview.Model` is one document, not one pane.** It holds no tab state. A
  pane that wants tabs owns a `[]*docview.Model` plus an active index. This is
  the single most important boundary — putting a tab strip inside `Model` would
  force every consumer into the tab world and make the workspaces-tab case pay
  for machinery it does not use in v1.
- **The pane tree is unaware of tabs.** A `PaneDoc` leaf's `DocID` is already an
  indirection into a plugin-side registry, not an inline document. Turning that
  registry entry from "one model" into "a tab group of models" changes no tree
  code and no layout math.
- **The header row is the future tab strip.** §3.2 gives every doc pane exactly
  one header row via `terminalHeaderRow`. A tab strip is that same one row with
  chips per document instead of one path chip — same height, same hit-region
  pattern (`regionDocTab`, `Data` = tab index), so the geometry stack
  (`previewContentY() + terminalHeaderRows`) never changes. `layoutHeaderChips`
  already drops whole chips rather than clipping, which is the overflow behavior
  a tab strip wants.
- **Persistence is a list from day one.** `PaneLayoutJSON`'s doc leaf serializes
  `{"kind":"doc","tabs":[{"path":…,"mode":…}],"active":0}` rather than a bare
  `path`, even though v1 only ever writes one entry. A one-element list costs
  nothing now and avoids a state migration later.

Not in scope here: the tab strip UI, tab keybindings, per-tab scroll retention,
or moving filebrowser onto the shared implementation. Those land after `docview`
has proven itself, and the natural sequencing is: extract `docview` (Phase 2) →
ship single-doc panes (Phases 3–5) → move filebrowser's `tabs.go` model down into
`docview` as a `TabGroup` and have both consumers use it.

---

## 4. Phases

Each phase is a shippable PR. The flag stays off through Phase 4.

**Phase 0 — tree + flag, nothing wired.**
`panetree.go` with `LayoutPanes`, `SplitLeaf`, `ClosePane`, `FindPane`,
`Neighbor`, `SetRatio`, floors and clamping. Feature `workspace_doc_panes`
(default false) in `features.go`. Table tests for layout math: single leaf gets
the whole box; ratio rounding never loses or duplicates a column; floors produce
`fits == false` rather than an under-floor box; close collapses to the sibling;
neighbor lookup across a nested split. No plugin changes.

**Phase 1 — geometry seam, single terminal leaf, zero visible change.**
Add `paneRoot`/`paneFocus`; initialize to a single `PaneTerminal` leaf when the
flag is on. Introduce `terminalLeafBox()` and route
`terminalSurfaceGeometry` / `calculatePreviewDimensions` /
`calculateAgentPaneDimensions` / `calculateTermPanelDimensions` through it.
Acceptance: with the flag on and one leaf, `renderListView` output is byte-for-byte
identical to flag-off, with and without the terminal panel, in both layouts — a
golden test over the existing testdata fixtures.

**Phase 2 — `internal/docview`.**
The `Model` from §3.2, tested standalone: loads a fixture markdown file, renders
at a given width, emits exactly `height` rows, clamps width, scrolls, toggles
raw/rendered, degrades below `MinWidthForMarkdown`, handles binary/image/missing
files without panicking.

**Phase 3 — open, render, close.**
`docPaneTarget`, the `openTerminalPath` branch, the doc leaf render inside
`renderListView`, `regionDocPane`, focus, close, the `workspace-doc` keymap
context, and the resize commands on open/close. Fixed 50/50 ratio; no drag yet.
End-to-end: click a `.md` path in a shell → pane appears right, terminal reflows,
tmux pane is resized once.

**Phase 4 — divider drag + keyboard resize + persistence.**
`regionPaneTreeDivider`, drag with live ratio and resize-on-release, `+`/`-`,
`WorkspaceState.PaneLayout` save/restore with stale-path pruning.

**Phase 5 — polish, then flip the default.**
Focused-pane border treatment (reuse `styles.RenderPanel(..., focused)`), header
chips and hints, empty/error states, `alt`-click override documented in the
help/keyboard docs and the website. Flip `workspace_doc_panes` to default true
once it has been lived with.

**Phase 6 — bare markdown paths.**

Today `terminalPathPattern` (`terminal_links.go:37`) only matches
`path:line`, so `docs/plans/active/foo.md` written on its own — by far the most
common way an agent names a document — is not a link. This phase makes bare
markdown paths clickable, and *only* markdown: the `:line` requirement is what
currently keeps prose like "see config.yaml" from becoming a link, so lifting it
for every extension would flood the terminal with false positives.

Rule, deliberately conservative — **resolve or do nothing**:

1. Match candidates with a second pattern: a token ending in `.md` or
   `.markdown`, bounded by whitespace or `(`/`[`/`` ` ``, with trailing
   punctuation trimmed the way `safeHTTPURL` already trims it. No `:line` needed.
   Skip any candidate overlapping a link the existing patterns already found.
2. Resolve against a short, ordered candidate list, first hit wins:
   - the selected workspace/shell's working directory (already
     `openTerminalPath`'s `base`);
   - the project root (`p.ctx.WorkDir`);
   - for an absolute path, itself.

   That is the whole heuristic. No walking up parents, no globbing, no searching
   the tree — those turn a mis-typed word into a surprise navigation.
3. Every candidate goes through the existing `resolveTerminalPath`, so symlink
   resolution, worktree containment and the regular-file check are unchanged and
   unduplicated.
4. **If nothing resolves, render no link at all.** Not a dead underline, not a
   toast — the text stays plain text. A false positive here is worse than a miss,
   because the underline is a promise.

Because resolution now happens at *decoration* time rather than only at click
time, `decorateTerminalLinks` gains a resolver callback and the plugin memoizes
`candidate → resolved | miss` per capture generation. Terminal output is
re-decorated on every poll, so an unmemoized `os.Stat` per candidate per frame is
not acceptable; the memo is invalidated with the same generation counters the
capture pipeline already carries.

Tests: a table of realistic agent output lines — plain prose containing
"README.md", a real relative path, a real path in backticks, a path in a
sentence with a trailing period, a path outside the worktree, a `.md` directory,
a dangling path — asserting exactly which become links. Plus a benchmark on the
decorate path to prove the memo holds.

**Phase 7 — fold in tmux tiling (the deprecated plan).**
Add `PaneTerminal` leaves that point at other workspaces/shells; migrate the
terminal panel to be a tree node and deprecate `termPanel*`; add the `ctrl+w`
prefix, splits, `Neighbor` navigation, per-leaf resize debounce and per-leaf poll
generations. All of the tiling plan's §1.2 (tmux sizing), §2.2 (per-tile PTY
sizing, one-workspace-per-tile Policy 1) and §4 (risks) apply unchanged.

**Phase 8 — tabs in panes, and shared adoption.**
Move `LoadPreview` into `docview`; lift filebrowser's `tabs.go` model into a
`docview.TabGroup` per §3.8; give doc panes a tab strip; have the file browser's
preview pane become a `docview`-backed pane so multiple markdown files can be
open in one panel there. Doc panes in other plugins follow from the same seam.

---

## 5. Risks

- **Two geometry authorities.** The failure this codebase already paid for
  (td-73fa86, documented in `terminal_surface.go`). Mitigation: the tree feeds
  `terminalLeafBox()`, and every existing sizer reads the container from there —
  no call site computes a box from `previewSplit()` directly after Phase 1. Worth
  a test that asserts `terminalSurfaceGeometry` agrees with `LayoutPanes` for the
  terminal leaf.
- **Terminal reflow cost on every open/close/drag-release.** Each is a real
  `tmux resize-window`, and agents like Claude Code redraw on SIGWINCH. Mitigation
  is the existing one: never resize during a drag, and reuse the 500 ms debounce
  in `maybeResizeInteractivePane`.
- **Narrow terminals.** `previewMinWidth` is 40 and Glamour wants 30; on an 80-col
  terminal with the sidebar open there is not room for both. `LayoutPanes` must
  return `fits == false` and the renderer must fall back to the focused leaf
  full-box, with a toast explaining why the pane did not open. Do not silently
  draw a 12-column document.
- **Rendered markdown loses line numbers**, so `path:line` links land at the top
  in rendered mode. Acceptable; mitigate by opening in raw mode when the link
  carries a line number, rendered mode otherwise.
- **Bare-path false positives (Phase 6).** Making `foo.md` clickable without a
  `:line` suffix drops the strongest signal the current pattern leans on. The
  mitigation is that a candidate becomes a link only if it actually resolves to a
  regular file inside the worktree against a two- or three-entry candidate list,
  and silently stays plain text otherwise. The cost is an `os.Stat` on the
  decorate path, which is why the memo is part of Phase 6's design rather than a
  later optimization. Until Phase 6 ships, links need the `foo.md:1` form.
- **Untrusted content.** Markdown comes from files inside the worktree, resolved
  through the existing containment check, and Glamour output is ANSI that lands in
  our own viewport — but the doc pane must clamp every line to its box width the
  same way the terminal surfaces do, or a long code fence will shift the divider.
- **`alt`-click collision** with terminal selection gestures in interactive mode
  (§3.5). Decide with one grep during Phase 3; the check is in one function.
- **Scope creep into filebrowser.** The temptation in Phase 2 is to refactor
  filebrowser onto `docview` immediately. Resist: that plugin works, and this
  feature does not need it to change.

## 6. Critical files

| File | Role |
|---|---|
| `internal/plugins/workspace/terminal_surface.go` | geometry authority; gains `terminalLeafBox()` |
| `internal/plugins/workspace/view_list.go` | `renderListView`; where the tree is drawn and regions registered |
| `internal/plugins/workspace/terminal_links.go` | link routing; the `docPaneTarget` branch |
| `internal/plugins/workspace/terminal_panel.go` | the split precedent; subdivides its leaf box after Phase 1 |
| `internal/plugins/workspace/mouse.go` | click/drag dispatch for the new regions |
| `internal/plugins/workspace/panetree.go` | **new** — the tree |
| `internal/docview/` | **new** — the document viewer |
| `internal/markdown/renderer.go` | Glamour renderer, reused as-is |
| `internal/plugins/filebrowser/preview.go` | `LoadPreview`, reused as-is |
| `internal/plugins/filebrowser/tabs.go` | working file-tab model; generalized into `docview.TabGroup` in Phase 8 |
| `internal/state/state.go` | `WorkspaceState.PaneLayout` |
| `internal/features/features.go` | `workspace_doc_panes` |
