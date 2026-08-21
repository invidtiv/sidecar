# Files plugin: Apple Mouse scroll stall

> **Implemented — superseded by the global contract.** The Files-specific fix
> landed as part of the repository-wide wheel boundary contract described in
> [Complete inertial wheel boundary coverage](../active/scroll-inertia-complete-coverage.md):
> `tea.WithFilter(app.FilterInput)`, `plugin.WheelBoundaryConsumer` (implemented
> by `filebrowser.Plugin.WheelAtBoundary`), shared `internal/scroll.Bounds`, and
> the declared policy registry in `internal/plugins/assembly`. Read that plan for
> the current design; this document is kept as the original diagnosis.

**Date:** 2026-08-13
**Status:** implemented — promises satisfied by the global wheel boundary contract
**Scope:** Files plugin wheel handling. No code in this pass; another agent was in the tree when this was written.

## Goal

A Magic Mouse (or trackpad) flick over Files should stay live. The cursor or preview offset should land near the end of the gesture, and Sidecar should not freeze until a backlog of wheel events has been painted off.

Keep the current mapping: wheel over the tree moves the cursor (same as `j`/`k`), and the preview follows. Wheel over the preview scrolls the file. Do not change this to VS Code-style viewport-only tree scrolling.

## What the user sees

A cheap USB wheel sends a handful of notches and is fine. An Apple Mouse is a trackpad surface with inertia, so a flick becomes dozens to hundreds of discrete `tea.MouseWheelMsg`s. Sidecar queues all of them. The UI locks until the Bubble Tea loop has drained the queue one full frame at a time.

Tree scrolling is the worse case: each notch also starts a preview load, so the queue grows *after* the flick with `PreviewLoadedMsg`s. Preview-only scrolling can still stall on paint alone.

## Cause

Three things stack.

### 1. Every notch is a full navigation step

`filebrowser.handleMouseScroll` has no burst handling:

```go
// internal/plugins/filebrowser/mouse.go
delta := 3
if action.Type == mouse.ActionScrollUp {
    delta = -3
}

if inTreePane {
    p.treeCursor += delta
    p.ensureTreeCursorVisible()
    return p, p.loadPreviewForCursor()
}

p.previewScroll += delta
return p, nil
```

It ignores `action.Delta` (already `±mouse.WheelScrollLines`) and hardcodes 3.

On the tree, `loadPreviewForCursor` → `openTab(path, TabOpenPreview)` → `applyActiveTab`. That path:

- replaces the ephemeral preview tab (`Loaded` and `Result` wiped)
- `resetPreviewModes` / `resetPreviewContent`
- `updateWatchedFile` → `SetPreviewFile` → possible fsnotify add/remove
- starts `filepreview.LoadPreview` (up to 500KB read + chroma, no cancel, no generation token beyond project epoch)

Stale `PreviewLoadedMsg`s whose path is no longer `p.previewFile` are ignored, but they still go through Update and a full paint. Markdown render mode is worse: `renderMarkdownContent()` (glamour) runs on the Update thread when a matching result lands.

`j`/`k` use the same `loadPreviewForCursor` path. Key repeat is milder (~30/s) but should get the same debounce.

### 2. Bubble Tea paints after every message

```go
// charm.land/bubbletea/v2 Program.eventLoop
model, cmd = model.Update(msg)
p.render(model)
```

There is no skip-render. A `tea.WithFilter` that returns `nil` is the only hook that drops a message without painting (`continue` before Update). Sidecar starts with `tea.NewProgram(model)` and does not install one.

A flick of ~150 events is ~150 Files frames. At the 10–15ms View cost measured in the older files-responsiveness work, that is 1.5–2+ seconds of catch-up after the finger has stopped. That is the “halt until they’re cleared out” symptom.

### 3. This class of bug is already solved elsewhere

Files never got either treatment.

- Inline-editor drag already throttles at 16ms (`dragForwardThrottle`): “every mouse motion event (~100+/sec) spawns a subprocess, causing 10–30s hangs.”
- Workspace terminal scrolling already uses `tty.WheelBurst` (`td-3b15ee`) for the same “trackpad flick is a long run of tiny events, each one a repaint” problem. `workspace.scrollPreview` calls `p.wheel.Add` before placing a local notch.

`tty.WheelBurst` first-notch-applies-immediately, sums held-back deltas, and tightens the window once a flick is underway (`WheelDebounceInterval` 16ms, `WheelBurstDebounce` 12ms, `WheelBurstThreshold` 3, `WheelBurstTimeout` 500ms). Tests in `internal/tty/interaction_test.go` pin the “don’t drop distance” rule.

## Recommended fix (files-local)

Three slices. Do them in this order. Do not invent a second burst type.

### Slice 1 — Debounce preview load above `openTab`

Cursor movement stays immediate. Preview I/O does not.

- On tree wheel (and held `j`/`k`): move `treeCursor`, keep the old preview on screen.
- Start or reset an ~80ms quiet timer (`tea.Tick` + a generation counter).
- Only the last generation calls `openTab` / `LoadPreview`.
- Do **not** call `applyActiveTab`, `resetPreviewContent`, or `SetPreviewFile` until that timer fires. Those are what flicker the pane and start watcher + I/O mid-flick.

A flick then loads the file it landed on, not every file it flew over.

### Slice 2 — Use `tty.WheelBurst` in `handleMouseScroll`

Copy the workspace preview pattern, do not fork it.

- Give the files plugin a `tty.WheelBurst` field (and a `now`/`clock` func if tests need to drive a flick without sleeping — workspace already does this).
- First notch applies immediately.
- Later notches inside the window sum into one delta.
- Use `action.Delta` so a flushed burst of +27 is one apply, not nine.

Tree: one cursor jump per flushed burst. Preview: one scroll apply per flushed burst.

Held-back events still run Update+View. Those frames are cheap only if View does not rebuild when nothing changed — that is slice 3.

### Slice 3 — Cache View when nothing changed

`Plugin.View` always `renderView()`s, clears the hit map, rebuilds both panels, and wraps with lipgloss `Width`/`Height`/`MaxHeight`. After a no-op burst event none of that is needed.

Flip a dirty flag (or a small generation) when cursor, scroll, tabs, or preview content change. If width, height, and generation match the last frame, return the cached string.

Together with slice 2, a 150-event flick becomes ~60 real paints plus cache hits, instead of 150 full layouts. That is what stops the post-flick freeze on preview-only scrolling.

## Out of scope

- App-level `tea.WithFilter` that coalesces `MouseWheelMsg` before Update. That is the only way to skip Update+View entirely, and it would help Git (`autoLoadDiff` on every notch) and Notes (`loadNoteIntoEditor` on every notch). Do it later if those surfaces show the same stall. Not required to fix Files.
- Changing tree wheel to scroll the viewport without moving selection.
- Cancelling in-flight chroma. The debounce in slice 1 means only one load starts.
- Magic Mouse OS settings, a general event-queue system, or processing wheel in a goroutine.

## Acceptance

- A hard Magic Mouse / trackpad flick over the tree keeps the UI live, lands the cursor near the end of the gesture, and loads **one** preview (the landing file).
- The same flick over the preview scrolls smoothly and does not freeze after the finger stops.
- A single cheap-mouse notch still moves immediately (burst first-event rule).
- Held `j`/`k` does not start one `LoadPreview` per key-repeat tick; it loads when the repeat stops.
- Existing tree click-to-preview, tab pin/replace, and preview-pane keyboard scroll stay as they are.

Proof: unit tests for burst apply/hold/flush on the files handlers (drive time through a clock, do not sleep), and a debounce test that N tree-wheel events produce one `LoadPreview` after the quiet period. A live `tmux-drive` flick is optional and must use an isolated tmux socket **and** isolated Sidecar state (`scripts/tmux-drive.sh paths` first).

## Key files

- `internal/plugins/filebrowser/mouse.go` — `handleMouseScroll`
- `internal/plugins/filebrowser/handlers.go` — `loadPreviewForCursor`, `j`/`k`
- `internal/plugins/filebrowser/tabs.go` — `openTab`, `applyActiveTab`
- `internal/plugins/filebrowser/plugin.go` — `View`, `PreviewLoadedMsg`
- `internal/filepreview/preview.go` — `LoadPreview` (leave the loader; change who calls it)
- `internal/tty/wheel.go` — reuse `WheelBurst` as-is
- `internal/plugins/workspace/mouse.go` — `scrollPreview` is the pattern to copy

## Adoption note (post-implementation)

Files adopted slices 1–3, and the burst pattern has since spread by reference
rather than by copy: every surface that hosts a scrollable content area is
expected to apply wheel deltas through `tty.WheelBurst`/`WheelBursts` **and**
answer `ScrollAtBoundary` — the pairing is now the documented default for new
scrollable surfaces in `.claude/skills/ui-features/SKILL.md` ("Wheel burst
coalescing"). The td issue viewer's hosts (workspace issue leaf, overview
Workspaces preview, app content deck, app preview modal) are the most recent
adopters, each tested beside its host with a clock-driven flick.
