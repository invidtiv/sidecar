# Sidecar-wide content links and passive panes

**Status:** proposed
**Written:** 2026-08-19, against `82a30ced`
**Tracking:** `td-7be1ec`
**Related:** [workspace windowing](workspace-windowing-system.md) · [terminal resource providers](terminal-resource-providers.md) · [agent open](../implemented/agent-open-in-split-cli.md) · [workspace focus ring](../implemented/workspace-focus-ring.md) · [workspace diff pane](../implemented/workspace-diff-pane.md)

## Decision

Extend the link-and-pane behavior already proven in project and global
Workspaces to other Sidecar surfaces through one app-owned composition layer.
Do not add private pane trees to Files, Git, Notes, td, Tasks, or future
plugins.

- The active plugin remains the primary content. When a link is activated,
  Sidecar opens the same Document, Issue, Diff, or Resource content used by
  Workspaces beside it. The first passive pane opens on the right; another
  content kind stacks in the right column; the same kind opens or focuses a tab
  in its existing homogeneous pane.
- `internal/panelayout` remains the structure, geometry, placement, refusal,
  and focus-order authority. `internal/paneframe` remains the only compositor,
  chrome, divider, and pane hit-region authority.
- Link recognition becomes a rendered-surface service, not terminal-only code.
  An optional plugin capability identifies safe, passive text rectangles.
  Sidecar scans and decorates those rectangles after `View`, so an embedded td
  or Tasks model can opt in from its Sidecar wrapper without changing its
  renderer.
- Plugins may later publish exact semantic links using OSC 8 with a
  `sidecar://...` destination. Sidecar consumes those destinations into its own
  hit regions and removes the internal OSC sequence before the host terminal
  sees it. Automatic matching remains available for ordinary text.
- Use one internal URI scheme with a namespaced authority:
  `sidecar://note/nt-4jdj4e`. Do not mint `note://`, `task://`, and one global
  scheme per plugin. Do not put a display title in the identifier. Render a
  label as `[Title](sidecar://note/nt-4jdj4e)` or as OSC-8 link text instead.
- The internal URI identifies an intent; it does not encode layout. Its
  registered handler decides whether the target opens a passive pane or
  navigates a Sidecar-owned surface. Unknown namespaces are inert text.
- Existing `sidecar open` routing remains shell-targeted and never moves the
  user. The CLI, terminal clicks, plugin clicks, and internal URIs share target
  parsing and content-open behavior, but this plan does not invent an
  ambiguous “currently visible plugin” CLI target.

This replaces the old `workspace-windowing-system.md` deferral of app-level
composition. The requirement now exists, but it still does not justify changing
the mandatory `plugin.Plugin` interface or forcing every plugin to participate.

## User journey

The steel thread is a file reference in the Files preview:

1. Marcus opens a Markdown, source, or text file in Files. The preview contains
   `internal/app/model.go:388`, `td-7be1ec`, and a commit hash.
2. Sidecar underlines only references it can activate in the current project.
   Tree rows, tab chrome, search inputs, inline editors, dialogs, images, and
   binary previews remain untouched.
3. Marcus clicks the source path. Files stays on screen and a Document pane
   opens to its right at the requested line.
4. He clicks the td ID. The existing right column gains an Issue split rather
   than replacing the Document pane. Clicking another td ID adds a tab to the
   Issue pane.
5. He presses Tab. Focus walks the Files tree, Files preview, Document leaf, and
   Issue leaf in visual order; Shift+Tab walks backward. A click focuses any
   visible window. The active border and keyboard owner always agree.
6. He clicks the hash. A Diff pane opens or adds a tab using
   `internal/workspacediff`; it is not a second Git renderer.
7. With a terminal resource provider enabled, clicking its locator opens the
   same Resource card and tabs as Workspaces. With no ready matcher, it is plain
   text.
8. `q` or `esc` hides the focused passive pane according to the existing
   Workspaces rule; `x` closes a tab, and closing the last tab forgets and
   collapses that leaf. Files keeps its own state throughout.
9. On a narrow screen, Sidecar refuses the split with the existing fit message
   and leaves Files, focus, and scroll unchanged.

The second journey is Git. References in passive diff and commit-description
text activate identically. Git's file list, stage controls, minimap, commit
form, search inputs, confirmation dialogs, and other existing interactive
targets retain precedence.

The third journey is opt-in embedding. td and Tasks initially need no upstream
rendering change: their Sidecar wrappers expose safe read-only rectangles and
Sidecar scans the rendered ANSI text. Exact semantic links can be added later
through OSC 8. Full combined Tab behavior lands only when their small embedded
focus contract can name and focus their internal stops; Sidecar does not steal
Tab from an opaque nested TUI.

## What exists today

### Workspaces already owns the content model

Project Workspaces and global Workspaces/Sessions already support five leaf
kinds through a binary pane tree:

| Kind | Shared viewer/model | Current open targets |
| --- | --- | --- |
| Primary terminal | `internal/termpreview`, `internal/tty` | selected shell/worktree |
| Document | `internal/docview` | file and `path:line` |
| Issue | `internal/issueview` | `td-...` |
| Diff | `internal/workspacediff` | working tree, commit, range |
| Resource | `internal/resourceview` | configured provider reference |

`panelayout.PlanOpen` retargets an existing same-kind leaf, opens the first
content beside the primary leaf, and stacks later kinds in the content column.
Both hosts trial a split against content floors and refuse rather than squeeze.
`paneframe` composes the exact boxes it laid out, owns leaf borders and widened
divider targets, and registers hit regions in one order. `panelayout.Ring`
defines the current workspace focus order.

The reusable viewers are farther along than their hosts. `resourceview.Pane`
is already host-independent. Documents, issues, and diffs have shared viewer
and tab primitives, while project/global hosts still own kind-specific open,
async-routing, header, persistence, and close plumbing. Adding a third host by
copying those switches would make future parity harder.

### Recognition is reusable but named for terminals

`internal/terminallink.ScanWith` already recognizes safe HTTP(S) URLs,
existence-gated files, td IDs, existence-gated git specs, and configured
external resource matchers. It strips ANSI, reports inclusive visual columns,
resolves overlaps in a fixed order, and bounds external matches. Project and
global Workspaces each adapt the resulting spans into their own output hit
regions.

The scanner is not tied to tmux data, but its callers are. It has no concept of
a plugin-local safe rectangle, an already-rendered multi-line frame, or an
internal `sidecar://` intent. `Decorate` is line-oriented and the two terminal
hosts still own activation switches.

### Files and Git have the needed geometry

- Files renders tree and preview panes itself. `regionPreviewLine` already maps
  visible preview rows to source rows; markdown/raw rendering, scroll, tabs,
  inline edit, search, images, and overlays are distinguishable in state.
- Git renders sidebar and diff/commit panes and already registers their exact
  boxes. Its modes distinguish passive status/diff content from commit forms,
  searches, menus, confirms, and errors.
- Notes has a stable editor layout and separates read-only preview from textarea
  and tmux editor modes. It is a natural later Sidecar-owned adopter.

These plugins must return link zones derived from the same geometry they draw.
The app must not infer a preview rectangle from width percentages or scan a
whole frame containing controls.

### td and Tasks are embedded components

The Sidecar td wrapper embeds `td/pkg/monitor.Model`; the Tasks wrapper embeds
`tasks/pkg/tui.Model`. Each package owns its rendering, mouse model, overlays,
storage, and internal focus. Sidecar currently receives an ANSI string and
stable host metadata such as focus contexts and commands. Scanning that string
inside the wrapper is a viable zero-renderer-change integration. Pretending the
host knows the nested model's exact interactive rows or Tab boundary is not.

Tasks is an app-global hosted surface rather than a project registry plugin,
but `focusedSurface`, `surfacePlugins`, mouse routing, help, and footer handling
already treat it as a visible plugin. The outer content host must use the same
surface abstraction rather than special-case registry indexes.

## Product rules

### One outer deck, existing inner layouts

Call the new app-owned object a **content deck**. A deck is attached to a stable
visible surface key:

- project plugin: `{project root, plugin ID}`;
- app-global hosted surface: `{global, stable tab ID}`.

The deck's primary leaf contains the plugin's existing `View`. It is opaque to
layout: Files may still draw its tree/preview split and Git its sidebar/diff
split. Passive Document, Issue, Diff, and Resource leaves sit beside that
primary leaf through the shared pane tree.

Do not wrap project Workspaces or global Workspaces in another deck. They
already are content-deck hosts and remain the parity reference. Configuration,
Activity, modals, the intro, and placeholders are not linkable deck surfaces.

Generalize `panelayout.Terminal` to the presentation-neutral name `Primary`
without changing its numeric value or persisted workspace key. Keep
`Terminal = Primary` as a migration alias until workspace callers are renamed.
Likewise, make the floor field primary-content vocabulary. `PlanOpen` then
means “content beside the primary leaf” for every host; it does not learn plugin
IDs.

### Homogeneous content panes

Keep one leaf per passive content kind per deck:

- all files are tabs in one Document leaf;
- all td issues are tabs in one Issue leaf;
- all git targets are tabs in one Diff leaf;
- all external providers are tabs in one Resource leaf.

This is the existing Workspaces rule. Do not mix files, issues, diffs, and
provider resources in one universal tab strip. Sharing the manager and tab
mechanics does not erase type-specific commands, loading, or persistence.

### Focus is one ring, including a plugin's inner stops

The visual requirement is stronger than “the outer plugin is one pane.” Files,
Git, and Notes already draw internal windows, and the user expects Tab to visit
every visible window.

Add an optional plugin capability:

```go
type PaneFocusProvider interface {
    PaneFocusStops() []PaneFocusStop // stable IDs, current visual order
    PaneFocus() string
    SetPaneFocus(id string) tea.Cmd
    SetPaneFocusActive(active bool) // mute inner active chrome off-primary
}
```

The app builds one ring from the primary surface's inner stops followed by the
outer passive leaves in `panelayout` placement order. When no provider exists,
the primary surface is one stop. When no passive leaf exists, current plugin Tab
handling stays unchanged.

Files, Git, and Notes implement the capability from their existing focus enums;
they do not introduce new focus state. Workspaces keeps its current
`panelayout.Ring` host. A click is resolved from the geometry actually drawn and
passes through the same setter as Tab.

Opaque embedded TUIs are gated more carefully. Do not enable their outer deck
until the embedded package exports the equivalent small focus contract. td can
project its existing panel IDs and `next-panel`/`prev-panel` behavior; Tasks can
add stable spatial stop IDs alongside its existing host focus contexts. The
contract must set a stop directly, not ask Sidecar to replay Tab and guess
whether the child wrapped.

Text inputs, inline terminals/editors, and blocking overlays keep their current
key precedence. While one owns the keyboard, Tab remains input/widget behavior
and never escapes to the deck. Footer and help contexts come from the currently
focused inner stop or passive leaf.

### Link zones are explicit and optional

Add a second optional capability:

```go
type ContentLinkProvider interface {
    ContentLinkSurfaces() []contentlink.Surface
}

type Surface struct {
    ID          string
    Rect        mouse.Rect // plugin-local, exact rendered text box
    WorkDir     string     // file containment and git resolution root
    ProjectRoot string     // project-scoped issue/note intent context
    Kinds       KindSet    // file, issue, diff, resource, URL, internal
    ReadOnly    bool
}
```

The method is read after `View`, so rectangles are the ones that frame drew.
Only `ReadOnly` surfaces are scanned; returning nil opts out for the frame. A
plugin may return nil while editing, searching, displaying a modal, showing an
image/binary preview, or otherwise unable to distinguish passive text safely.

The app scans only declared rectangles, decorates the already-rendered ANSI
frame, and stores hit regions tagged with `{surface key, render generation,
reference}`. A link from a previous render, project, plugin tab, width, scroll
position, or overlay generation cannot activate.

Scanning is pure and performs no filesystem, git, database, provider, or
network work. Each deck owns a bounded resolution index like the terminal-link
caches: a scan uses only ready resolver snapshots and returns unresolved
candidates as pending work. The app queues that work for the next update and
re-renders when results arrive. An unseen path/hash stays plain for one frame
rather than making `View` block or spawning a subprocess.

The convenience helper for a whole passive component should make adoption one
method, but whole-frame scanning is not the default. Future plugins choose their
zones; the app never grows a plugin-ID allowlist.

### Click versus selection and existing controls

Only left-click release with no drag activates a link. Reuse the text-selection
gesture arbitration: press records the candidate and origin, motion past the
drag threshold transfers to selection, and release over the same span activates.
This is the browser-like rule needed by file previews and diffs, where users
also select text.

Registration precedence, from lowest to highest:

1. primary/passive leaf body;
2. pane divider;
3. pane tab and close controls;
4. link-versus-selection arbitration inside a declared read-only zone.

The app sees the outer deck before it forwards an event to the plugin, so it
cannot discover afterward that a nested button also consumed the click. A link
zone therefore contains passive text and selection only. It must exclude inline
editors, inputs, modals, buttons, actionable file rows, minimaps, and every
other plugin-owned control. If a plugin cannot isolate that geometry, it returns
no zone. The outer ordering and link/selection gesture live in one app/content-
deck function; Files and Git answer only their safe rectangles.

### Target and activation core

Extract a presentation-neutral reference from the overlap among
`terminallink.Span`, `uirequest.Target`, and the workspace host switches:

```go
type Ref struct {
    Kind      Kind // URL, File, Issue, Diff, Resource, Internal
    Value     string
    Line      int
    Provider  string
    Matcher   string
    Namespace string // Internal only
}
```

`contentlink` owns parsing, overlap, visual-column coordinates, rendered-frame
decoration, and strict URI parsing. It does not fetch files/issues/resources,
run git, navigate plugins, or mutate a pane tree.

`contentpanes` owns target-to-view adapters and deck lifecycle. Its application
operation is one typed call:

```go
Open(ctx SurfaceContext, ref contentlink.Ref, placement Placement) Outcome
```

The same call is used by Workspaces clicks, app deck clicks, and UI requests
after their routing decision. Host adapters add only their genuine differences:
terminal viewport freeze/resize in Workspaces, plugin primary focus in the app,
and project-persisted versus global in-memory lifetime.

URL remains an action, not a leaf: validated HTTP(S) opens through the existing
browser path. File, Issue, Diff, and Resource open passive leaves. Internal goes
through a registered Sidecar intent handler.

## Internal link protocol v1

### Canonical form

```text
sidecar://<namespace>/<percent-encoded-id>[?<bounded-options>]
```

Examples:

```text
sidecar://note/nt-4jdj4e
[Release checklist](sidecar://note/nt-4jdj4e)
OSC 8 ;; sidecar://note/nt-4jdj4e ST Release checklist OSC 8 ;; ST
```

The first namespace is singular, lowercase ASCII, and registered in Sidecar.
The ID is opaque to the generic parser and is validated again by its handler.
Query parameters are namespace-owned, allowlisted, bounded, and excluded from
stable identity when they are presentation-only. Fragments are rejected in v1.
Credentials, shell commands, absolute filesystem paths, and arbitrary plugin
IDs are never accepted as generic parameters.

The proposed `note://nt-4jdj4e:"title"` form is not canonical:

- one scheme per feature creates an unbounded global scheme surface;
- `//` gives `nt-4jdj4e:` authority/host semantics rather than a Sidecar
  namespace and opaque ID; and
- a mutable display title must not change target identity.

One `sidecar` scheme follows the generic URI component model in RFC 3986 and
keeps ownership obvious. OSC 8 is the conventional terminal annotation for
binding arbitrary visible text to a URI; Sidecar consumes only its own scheme
and continues to validate external HTTP(S) itself.

Primary references:

- [RFC 3986 generic URI syntax](https://www.rfc-editor.org/rfc/rfc3986)
- [OSC 8 hyperlink format and URI behavior](https://iterm2.com/feature-reporting/Hyperlinks_in_Terminal_Emulators.html)

### Registry and first namespace

Use a static in-process registry, not a dynamic plugin protocol:

```go
type IntentHandler interface {
    Namespace() string
    Parse(id string, query url.Values) (Intent, error)
    Activate(AppContext, Intent) tea.Cmd
}
```

Handlers are Sidecar-owned application behavior and are registered at assembly.
They do not receive arbitrary callbacks or render bytes. Duplicate namespaces
fail tests/build assembly; unknown or invalid URIs stay visible but inert and
are never underlined.

The steel thread is `note`:

1. Parse and validate an existing `nt-...` ID.
2. Emit an app message that focuses Notes and selects the note by stable ID.
3. If the note no longer exists, keep the current surface and show a bounded
   error toast.
4. Do not create a Note pane in v1. The URI identifies the note, not a promise
   about presentation. A future host-independent Note viewer may change the
   handler to open a pane without changing stored links.

The existing `NavigateToFileMsg` pattern is the precedent for the activation
message. Add a matching Notes message rather than reaching into the plugin
model from the registry.

### Explicit OSC 8 handling

`contentlink` needs an ANSI walker that preserves visual columns and extracts
OSC 8 destinations before generic ANSI stripping. For `sidecar://`:

- validate the URI and visible label bounds;
- remove the OSC open/close controls from final output;
- decorate the visible label with Sidecar's link style;
- register exactly its visible cells; and
- let an explicit span beat automatic matches inside the same label.

HTTP(S) OSC 8 keeps the existing safe-URL behavior. Unknown/custom schemes are
stripped or left inert according to the current Sidecar OSC security policy;
they never become internal actions by prefix resemblance.

## State and lifecycle

Project plugin decks persist by stable project root and plugin ID, never tab
index. Store only:

- pane tree, split ratios, and outer focus;
- Document paths/lines/tab order/scroll;
- Issue IDs/tab order/scroll;
- Diff target identities/tab order/view state;
- Resource provider/matcher/locator references and scroll; and
- hidden-pane snapshot according to the existing `q`/`esc` behavior.

Do not persist loaded bodies, diff output, provider documents, errors, OSC
labels, detected spans, or rendered frames. Apply the same path containment,
resource-locator privacy, and stale-result identity rules as Workspaces.

Global hosted decks are in-memory in v1, matching global Workspaces. If real
Tasks use shows restart persistence matters, add it deliberately under a stable
global surface ID; do not smuggle it into a project state entry.

Only the visible deck owns live rendering/link regions. Inactive project tabs
retain model/reference state but clear render generations and hit maps. Bind
document live refresh through `internal/livepanes` only while its leaf is on
screen. Project switch, plugin removal, feature disable, or shutdown closes
watchers/editors and invalidates async messages without deleting persisted
references.

## Incremental delivery

User-visible work is gated by a `plugin_content_panes` feature flag, default
off, until Files and Git pass the real journeys. Existing Workspaces links and
panes are not gated or changed by that flag.

### M0 — Shared target and passive-pane core, no visible change

1. Add `internal/contentlink` with `Ref`, strict internal URI parsing, explicit
   OSC-8 extraction, and the existing match/overlap/visual-column behavior.
2. Keep `internal/terminallink` as a compatibility facade while workspace
   callers migrate; do not move every file and change behavior in one commit.
3. Add the bounded per-deck resolution index and pending-work queue. Reuse
   in-memory file trees and already-loaded Git identities where possible;
   filesystem/git fallbacks run only in cancellable commands.
4. Extract host-independent Document, Issue, and Diff pane/tab lifecycle beside
   the existing `resourceview.Pane`. Keep fetch/load functions behind adapters.
5. Add `internal/contentpanes.Deck`: primary leaf, typed passive leaves,
   `PlanOpen` trial/refusal, homogeneous tabs, focus, hide/close, async routing,
   and reference-only encode/decode.
6. Migrate project and global Workspaces to the core one kind at a time. Each
   migration must preserve current pane trees, terminal viewport freeze,
   resize, scroll, project persistence, global memory cache, hit-region order,
   and `sidecar open` outcomes.
7. Generalize `panelayout.Terminal` vocabulary to `Primary` with compatibility
   aliases and no persistence migration.

**Gate:** Workspaces render/interaction tests remain behaviorally unchanged;
link click and `sidecar open` for file/issue/diff/resource produce the same
`contentpanes` operation and tree. Project persistence fixtures round-trip
without change. No new subprocess, filesystem walk, database open, or provider
work enters `Init`, `Start` before command return, or render.

### M1 — App deck and Files steel thread

1. Add an app-owned deck host around eligible project plugins. Its primary
   content calls the plugin's existing `View` with the leaf's inner size.
2. Route WindowSize, keys, mouse, commands, focus, and footer context to the
   focused primary/passive leaf. Do not change the mandatory `plugin.Plugin`
   interface.
3. Add `PaneFocusProvider` and `ContentLinkProvider` optional capabilities.
4. Implement both in Files from its existing `PaneTree`/`PanePreview` state and
   exact preview geometry. Link only read-only text/Markdown source previews;
   explicitly opt out for inline edit, images, binary, info/blame modals,
   searches, file operations, and empty/loading/error surfaces.
5. Activate file, td, diff, provider, URL, and internal refs through the shared
   operation. Preserve Files tabs, tree selection, preview scroll, searches,
   and watcher lifecycle when the outer deck changes size.
6. Add reference-only deck persistence keyed by project root + `files`.

**Gate:** the full steel-thread journey passes at real terminal sizes. Tab and
Shift+Tab cycle Files tree → Files preview → every outer leaf in visual order;
click focus matches; same-kind opens tab; different-kind stacks; small-width
refusal is non-mutating; plugin/project/tab switches restore the correct state;
dragging across a link selects rather than opens.

### M2 — Git diff parity

1. Implement the two optional capabilities in Git from its existing
   `PaneSidebar`/`PaneDiff` state and rendered diff geometry.
2. Enable zones only for passive diff, commit message/body, and other plain
   read-only text. Exclude commit form, path/history filters, branch/push/pull
   menus, confirm/error modals, file rows whose existing click action should
   win, minimap, divider, and staging controls.
3. Resolve file paths relative to Git's project/worktree and git specs through
   `workspacediff.ResolveSpec`; retain built-in-before-provider precedence.
4. Reuse the shared Diff leaf for clicked hashes/ranges. Do not reuse Git's
   full-screen diff mode or duplicate its renderer in the leaf.

**Gate:** Files and Git expose identical target activation and outer focus
behavior; Git operations, staging identity `(path, staged)`, modal keys, diff
scroll/wrap/minimap, and existing click regions are unchanged. A link in a
staged or unstaged diff resolves against the correct worktree and cannot stage,
unstage, or select a neighboring file as a side effect.

### M3 — Intentional internal links and Notes

1. Freeze and document `sidecar://` v1 in
   `docs/reference/internal-links.md`, including URI/OSC examples, bounds,
   namespaces, security, and compatibility behavior.
2. Add the static handler registry and the `note` steel thread through a
   `NavigateToNoteMsg`-style message.
3. Teach Sidecar's Markdown adapters to recognize internal destinations without
   emitting them to the host terminal as raw actionable OSC.
4. Let Notes preview opt into automatic/explicit content links while editing,
   search inputs, task/delete/info modals, and inline tmux editing remain inert.
5. Decide from use whether a host-independent Note viewer is valuable. It is a
   separate pane-kind decision, not required to freeze the URI.

**Gate:** Markdown label and plain URI activate the same note; unknown,
malformed, deleted, foreign-project, oversize, encoded-control, fragment, and
duplicate-namespace cases fail safely. Stored links remain valid when a note
title changes.

### M4 — Embedded td and Tasks opt-in

1. Add safe rendered link zones in the Sidecar wrappers first; no td/Tasks
   renderer changes. Begin with detail/description/transcript surfaces where
   matching a path, issue, hash, or provider locator is useful and existing
   row actions are not ambiguous.
2. Add a tiny host focus API to `td/pkg/monitor` and `tasks/pkg/tui`: enumerate
   visible stable stops, report current stop, focus a stop directly, and report
   when an input/overlay owns Tab. Keep storage, actions, validation, and
   rendering in their owning packages.
3. Adapt that API to `PaneFocusProvider` in Sidecar. Do not infer focus from
   ANSI colors, replay Tab until something changes, or import internal model
   fields.
4. Add precise OSC-8 `sidecar://` emission upstream only if automatic matching
   produces real false positives or lacks a needed label. Make it an embedded
   host option so standalone td/tasks do not emit Sidecar-only links by default.

**Gate:** zero renderer changes are enough for automatic matches; the small
focus contract is independently tested by each owning repository and its
Sidecar consumer. Tab visits nested stops and outer leaves exactly once without
wrapping inside the child, and inputs/modals retain Tab. No task or issue
mutation occurs from a link click.

### M5 — Graduate and extend from evidence

1. Dogfood with the flag enabled and record false-positive, fit-refusal,
   focus, resize, and performance observations.
2. Enable by default only after Files, Git, and Workspaces parity evidence and
   independent review. td/Tasks may remain separately opt-in if their host
   contracts are not ready.
3. Add future plugin namespaces or link zones through the registries and
   optional capabilities. Do not add branches to app rendering by plugin ID.
4. Consider a structured non-interactive “open beside this visible surface”
   only when there is a deterministic caller identity and a never-move-user
   routing rule. Existing `sidecar open` remains the supported agent path now.

## Verification strategy

### Pure and contract tests

- ANSI/OSC parsing, wide characters, combining marks, tabs, wrapped rows,
  inclusive visual columns, punctuation, overlaps, match limits, explicit over
  automatic precedence, malformed/unterminated OSC, and stripping internal
  destinations from final output;
- URI parsing and normalization: scheme/namespace case policy, percent
  encoding, empty/oversize IDs, controls, fragments, query allowlists, unknown
  namespaces, and title-independent identity;
- `Ref` parity among rendered scan, terminal scan, internal URI, and
  `uirequest.Target` for every common kind;
- `Deck` tree placement, same-kind retarget, different-kind split, largest-leaf
  choice, fit refusal, close/hide/reopen, focus order, encode/decode, unknown
  content kind collapse, and stale async results;
- `PaneFocusProvider` ring composition with zero, one, and many primary stops;
  forward/reverse wrap; hidden sidebar; passive leaf close; and input/overlay
  ownership; and
- link-zone render generation and exact-coordinate activation after scroll,
  resize, project switch, plugin switch, overlay open, and view replacement.

### Surface regressions

- Files: raw text, rendered Markdown, wrapping, tabs, content search, inline
  edit, image/binary, tree hidden, divider drag, selection drag, watcher refresh;
- Git: status diff, full diff, commit preview/body, staged/unstaged duplicate
  paths, history, wrap/minimap, modals, filters, staging and write-in-progress;
- Notes: preview/edit parity, search, modals, external sync, inline editor;
- td/Tasks: embedded focus API, every exported input/overlay context, mouse
  coordinate offsets, nested quit suppression, lifecycle and state isolation;
- project/global Workspaces: current file/issue/diff/resource clicks,
  sidecar-open parity, terminal resize/freeze, pane persistence/cache, live
  refresh, focus, scrolling, and hit-region order.

### Real-app proof

Use `scripts/tmux-drive.sh` only after `paths` proves both the tmux server and
Sidecar state/config are isolated. Never touch the default tmux server.

At representative `200x50`, `120x40`, and the fit boundary:

1. Files: open a fixture whose visible preview contains every target kind;
   activate file → issue → diff → provider; Tab and click around the full ring;
   drag-select a linked label; hide, restore, close, switch plugins, switch
   projects, and relaunch.
2. Git: repeat from staged and unstaged diffs and a commit body; stage/unstage
   still targets the selected entry and links never fire from modals.
3. Notes internal link: label opens exact note; deleted note refuses without
   moving the user.
4. td/Tasks when enabled: internal focus stops plus outer leaves, with Tab kept
   inside input/form/modal contexts.

Stop the driver on success and on error. Pair screenshots/transcripts with
focused pointer/model tests because exact mouse synthesis remains a harness
limitation. Final implementation gates are focused repeated/race tests, full
`go test ./...`, `go vet ./...`, `go build ./...`, repository lint, gofmt, and
`git diff --check`, followed by independent review of the integrated candidate.

## Risks and controls

| Risk | Control |
| --- | --- |
| A scanner turns controls or editable text into links | Plugins declare exact read-only zones after rendering; nil opts out for the frame; interactive regions retain higher precedence |
| Clicking a link also triggers a plugin action | Shared click/drag arbiter and one region-order authority; exclude ambiguous interactive rows |
| A third pane host drifts from Workspaces | Extract `contentpanes` first and migrate the two existing hosts before adding the app host |
| Outer resizing breaks plugin layout or mouse coordinates | Primary content receives its real leaf size; zone/focus geometry comes from that same render; window and pointer tests cover the inset |
| Tab gets trapped or skips nested plugin panes | Explicit direct-focus capability; no replay-and-guess; opaque embedded TUIs stay disabled until they can export it |
| Internal links become arbitrary app RPC | Static namespace registry, strict parser and handler validation, no generic command/plugin/path action |
| OSC internal URIs escape to the host terminal | Consume and remove `sidecar://` OSC before final composition; tests inspect raw output |
| Persisted references leak content | Persist identities and view state only; retain existing resource-locator disclosure and never store bodies/rendered frames |
| Startup or rendering regresses | No scanning before a visible `View`; no link resolution during render; file/git/provider existence work stays cached/asynchronous and bounded |
| Windowing work collides with the active workspace plan | Reuse current `panelayout`/`paneframe`; this plan adds an app host and passive-content core, not live-terminal splitting |

## Settled and deferred

Settled:

- Files and Git are the first supported surfaces.
- The app owns the outer deck; plugins opt into exact link/focus capabilities.
- Automatic rendered-output matching is the low-change path.
- Explicit `sidecar://` over Markdown or OSC 8 is the intentional path.
- `sidecar://note/<id>` navigates to Notes in v1; it does not require a Note
  pane.
- Homogeneous content tab groups and current Workspaces placement/refusal rules
  remain the product grammar.
- Existing shell-targeted `sidecar open` stays the agent path.

Deferred until evidence:

- a passive Note pane kind;
- internal link namespaces beyond `note`;
- default-on td/Tasks link zones;
- a dynamic internal-intent extension protocol;
- a CLI target for an arbitrary visible plugin deck;
- floating panes or a second compositor; and
- automatic scanning of an entire plugin frame with no declared zone.
