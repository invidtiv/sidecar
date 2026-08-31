# Selectable rendered Markdown in Files

- **Status:** Implemented
- **Tracking:** `td-a2b617`
- **Scope:** The primary Files plugin preview and document panes opened beside Files

## Outcome

A user can drag across rendered Markdown in either Files surface, see the selected rendered characters highlighted, copy them with the normal selection chords, and paste plain text that matches what was visible rather than the Markdown source or terminal styling.

This is presentation-layer behavior. Sidecar does not need a new CLI or API: an agent already reads the underlying file directly, while selection exists to let a human copy the rendered presentation.

## Failure addressed

The two surfaces fail at different adapter boundaries:

- The primary Files preview's hit testing returns early when rendered Markdown removes the line-number gutter. Its highlight branch has the same `showLineNumbers` gate, and its copy path slices `previewLines`, so even a forced selection would copy source rows rather than rendered rows.
- A document pane already renders and copies through the shared `internal/docview` and `internal/textselect` implementation used by Workspace and Sessions. The Files content-deck host registers the pane body, but forwards only scrollbar regions to `docview.HandleSelectionMouse`, so a body press never arms the selector.

## Decisions

- Rendered selection is over visual rendered rows. The clipboard receives ANSI-free rendered text, including meaningful indentation and excluding renderer padding.
- Raw Files preview selection keeps its existing source-line and wrapped-line behavior. The focused change will make its row, hit-test, highlight, and copy accessors choose the same active representation instead of replacing that mature coordinate model.
- Document panes continue to use `docview` and `textselect`; the Files host owns only event routing, drag ownership, focus, and link activation.
- Scrollbar regions retain priority over document selection. A plain document click still focuses or activates a link; only pointer movement promotes the gesture to selection.
- Starting in a link remains selectable by dragging. Releasing without a drag settles the selection gesture before activating the link so no stale drag survives.
- `y`, the configured selection-copy chord, and the platform copy chord keep their established meanings. Pasting is provided by the existing shared native plus OSC 52 clipboard pipeline; the viewer itself remains read-only outside inline-edit mode.

## Implementation

### 1. Primary Files preview

One active-row accessor is authoritative for hit testing, row registration, selection painting, and clipboard extraction. In rendered mode it exposes Glamour's rows; in raw mode it preserves source rows and syntax styling. Pointer columns map against the active plain row, selection paints after Markdown styling, and copied rows pass through the shared text-selection clipboard formatter.

Regressions use a rendered document whose row count and text differ from its source. They prove that dragging creates a visible highlight, copy emits rendered plain text with no ANSI or trailing renderer padding, and clicks below the rendered body do not select the last row.

### 2. Files document pane

Content-deck gesture routing sends a focused document leaf's body press, motion, and release to `docview.HandleSelectionMouse`, using the same drag-source identity as its scrollbar. Header, gutter, scrollbar, close, tab, divider, and non-document panes remain outside the selection surface.

The host settles document and primary-plugin gestures before a no-drag content-link activation. Host-level tests use rendered Markdown, not only direct `docview` tests, and cover drag highlight, copy, link activation, release settlement, and scrollbar priority.

### 3. Proof and completion

Verification covers the focused packages, race detection, repository build and vet, lint, diff hygiene, and an isolated real-app journey. Final review checks representation drift, stale drag state, link/scrollbar regressions, and clipboard fidelity.

## Acceptance evidence

- Primary Files rendered Markdown can be drag-selected and visibly highlighted.
- A Files document pane can be drag-selected through the real content-deck host.
- Copying either selection produces pasteable, ANSI-free rendered text and does not copy Markdown markers or invisible right padding.
- Raw preview selection, wrapping, content search, scrollbars, content links, focus, and click-without-drag behavior remain intact.
- Both project/global document hosts remain on the shared `docview` implementation; no second selection engine or Files-only document rule is introduced.

## Evidence

- `go test -race ./internal/plugins/filebrowser ./internal/docview ./internal/app -count=1`
- `go vet ./...`
- `go build ./...`
- `golangci-lint run ./internal/app/... ./internal/plugins/filebrowser/...`
- `git diff --check`
- An isolated `tmux-drive.sh` run used a temporary binary, private tmux socket, and private state/config roots. In the primary Files preview, Quick Open loaded this Markdown plan, `m` rendered it, an SGR mouse drag selected two rendered rows, `y` copied the visible prose without source markers or ANSI, and the UI showed the selection-copy hint. In a Files document pane opened through `sidecar open`, select-all plus `y` copied the rendered document text. The runner was stopped after capture.
- `go test ./...` passed every task package and the rest of the repository except the current-main `internal/cli` test `TestALifecycleReportFromARemoteAgentStaysInTheHostsOwnStore`. It failed again in isolation because its claimed private tmux server ID differed from the live server ID; this task does not touch the CLI, lifecycle environment, or tmux test harness.
