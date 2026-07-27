# Embedded Terminal Audit

**Scope:** `internal/tty/`, `internal/plugins/workspace/` (interactive mode, terminal
panel, output preview), `internal/plugins/filebrowser/inline_edit.go`,
`internal/plugins/notes/inline_edit.go`, `internal/ui/selection*.go`,
`internal/mouse/`.

**Date:** 2026-07-26 · **Tracking:** td-acaed2 · **Baseline:** `c097a33`

---

## 1. How it works today

Sidecar is not a terminal emulator. tmux is the PTY backend; sidecar is a
poll-based input/output relay:

```
keypress ──► MapKeyToTmux ──► `tmux send-keys` (subprocess)
                                     │
            keystroke debounce (20 ms) ▼
             `tmux capture-pane -p -e -S -600` (subprocess)
             `tmux display-message #{cursor_x},…` (subprocess)
                                     │
                        OutputBuffer.Update (hash gate)
                                     │
                      render lines + overlay a ▉ glyph
                                     │
                      tea.Tick(50–500ms) ──► repeat
```

Four independent implementations of this loop exist:

| Consumer | Poll driver | Buffer | Cursor |
|---|---|---|---|
| Workspace agent pane | `scheduleInteractivePoll` / `pollGeneration` | `workspace.OutputBuffer` | `workspace.renderWithCursor` |
| Workspace shell pane | `scheduleShellPollByName` / `shellPollGeneration` | `workspace.OutputBuffer` | `workspace.renderWithCursor` |
| Workspace terminal panel | `scheduleTermPanelPoll` / `termPanelGeneration` | `workspace.OutputBuffer` | `workspace.renderWithCursor` |
| filebrowser + notes inline edit | `tty.Model.schedulePoll` | `tty.OutputBuffer` | `tty.RenderWithCursor` |

`internal/tty` was extracted to unify this (commit `295daab`, "Initial
abstraction of the tty plugin") but the workspace plugin was never migrated. It
still carries a near-verbatim private copy of the package. That fork is the root
cause of most of what follows: fixes land in one copy and not the other.

---

## 2. Correctness bugs

Ordered by user impact. Each has a file:line anchor.

### 2.1 Live output freezes after any scroll in interactive mode 🔴

`pollInteractivePane` (`interactive.go:1343`) and `pollInteractivePaneImmediate`
(`interactive.go:1415`) return `nil` when a scroll happened within
`scrollBurstTimeout` (500 ms):

```go
if time.Since(p.lastScrollTime) < scrollBurstTimeout && p.scrollBurstCount > 0 {
    return nil
}
```

These two functions are the sole continuation of the poll chain in interactive
mode (`update.go:609`, `:654`, `:1053`, `:1569` — all four `return` immediately
after the call). Returning `nil` doesn't skip one tick — it terminates the
chain. There is no periodic heartbeat anywhere in the plugin that would restart
it (verified across `agent.go`, `shell.go`, `terminal_panel.go`,
`internal/app/`); the only restart paths are a keystroke
(`scheduleDebouncedPoll`, `interactive.go:1375`, which has no burst gate), a
resize, or a click.

**Effect:** scroll up to read, scroll back down — the pane is now a frozen
screenshot. It stays frozen until you press a key, resize, or leave interactive
mode. Since `scrollBurstCount` is only ever reset to `1` (`interactive.go:1141`
and its copy-pasted twin `mouse.go:1205`), never `0` — not even on
interactive-mode exit — the `> 0` half of the guard is permanently true after
the first scroll of the session and the gate is purely time-based.

**Fix:** the guard should skip the *capture*, not the *chain*. Always schedule
the next tick; make the poll body a no-op when the user is mid-flick.

### 2.2 Typing after a scroll silently loses `m`, `M`, `;`, `<` 🔴

`interactive.go:925-937`:

```go
timeSinceScroll := time.Since(p.lastScrollTime)
if timeSinceScroll < postScrollFilterWindow /* 500ms */ && len(msg.Text) > 0 {
    if strings.ContainsAny(s, "<;Mm") || !isNormalTyping(s) {
        return nil   // dropped
    }
}
```

For a **single-character** keypress this drops the literal characters `m`, `M`,
`;`, and `<` for half a second after every scroll event. Type `make test` right
after scrolling and you get `ake test`. There is no feedback that a keystroke was
eaten.

`tty.LooksLikeMouseFragment` (the other filter, `output_buffer.go:197`) is
correctly conservative — it never matches a 1-rune string. This ad-hoc
`ContainsAny` check in the workspace copy is not, and it is the more aggressive
of the two gates.

**Fix:** delete the `ContainsAny` branch and rely on `LooksLikeMouseFragment` +
the existing `[`-proximity gates — `LooksLikeMouseFragment` structurally cannot
match a 1-rune string, so plain typing survives while multi-character fragments
are still caught. Guard the change with leak-regression tests: feed the split
SGR sequences that motivated the filter (`[<65;33;12M` with and without the
leading ESC, split at each byte boundary) through the input path and assert
none reach `send-keys` as literal text. **The leak-regression tests are a
blocker: they land in the same change that removes the filter, not as a
follow-up.** Longer term, `MouseModeCellMotion` (§3.3) cuts the volume of mouse
reports crossing sidecar's stdin by an order of magnitude, which is what makes
split fragments likely in the first place.

### 2.3 Scrolling is ~12× slower than intended 🔴

Three multiplicative losses:

1. `internal/mouse/mouse.go:210,215` computes a proper `Delta: ±3` for wheel
   events (`±10` for horizontal).
2. `workspace/mouse.go:1020-1025` **discards it** and substitutes `±1`:
   ```go
   var delta int
   if action.Type == mouse.ActionScrollUp { delta = -1 } else { delta = 1 }
   ```
3. `forwardScrollToTmux` (`interactive.go:1168-1183`) and `scrollPreview`
   (`mouse.go:1223-1239`) then move **one line per event** regardless of `delta`
   — they branch on the *sign* only.
4. On top of that, burst debouncing (`interactive.go:1151-1153`, twin at
   `mouse.go:1215-1217`) *drops* events entirely rather than accumulating them:
   at 12 ms burst debounce (`scrollBurstDebounce`, `interactive.go:72` — the
   comment above it still says 32 ms), a trackpad emitting events every ~4 ms
   loses two of every three.

Net: a flick that should travel ~100 lines travels ~8. This is the "scrollback
isn't natural" complaint, precisely. The ±1 flattening is specific to workspace
vertical scroll: `conversations/mouse.go:177-193` and `gitstatus/mouse.go:233-243`
pass `action.Delta` through, and workspace itself honors it for horizontal
scroll (`mouse.go:1124`).

**Fix:** honor `action.Delta`; accumulate dropped deltas into a pending counter
that is applied on the next accepted frame instead of discarding them.

### 2.4 CSI-u / modified-key forwarding is dead code since the v2 upgrade 🔴

`tty.ExtractUnknownCSIBytes` (`csiu.go:14`) identifies unknown sequences by
reflecting for a `[]byte`-kinded value — matching bubbletea **v1**'s unexported
`unknownCSISequenceMsg []byte`.

In the pinned v2 (`charm.land/bubbletea/v2 v2.0.7`), unparsed input arrives as
`ultraviolet.UnknownEvent` / `UnknownCsiEvent`, both of which are **`string`**
types (`ultraviolet/event.go:27,35`), passed through untouched by
`bubbletea/v2/input.go:translateInputEvent`. `reflect.Kind()` is `String`, not
`Slice` — and no `[]byte`-kinded event type exists anywhere in bubbletea v2 or
ultraviolet — so `ExtractUnknownCSIBytes` always returns `nil` and
`handleUnknownSequence` (`interactive.go:1081`, reached from the `default:`
branch at `update.go:1610`; its doc comment still cites the v1 premise) never
forwards anything.

The unit tests pass because `csiu_test.go:7` defines its own local
`type csiBytes []byte` rather than exercising the real message type — a green
test guarding dead code.

**Effect:** kitty-protocol / `modifyOtherKeys` keys that bubbletea doesn't parse
are silently swallowed instead of reaching tmux. (`shift+enter` still works
because `keymap.go:43` special-cases it.)

**Fix:** match on `uv.UnknownEvent` / `uv.UnknownCsiEvent` (or a `fmt.Stringer`
whose value starts with `ESC [`), and add a test using the real type. Consider
also opting into `tea.View.KeyboardEnhancements` so these arrive as real
`KeyPressMsg` with modifiers instead of unknown sequences.

### 2.5 Data race + nil-pointer panic in `forwardClickToTmux` 🔴

`interactive.go:1203-1220` mutates plugin state from inside a `tea.Cmd`, which
bubbletea runs on its own goroutine:

```go
return func() tea.Msg {
    if err := sendSGRMouse(...); err != nil {
        p.exitInteractiveMode()          // writes p.interactiveState, p.viewMode
        ...
    }
    ...
    p.interactiveState.LastKeyTime = time.Now()   // read-after-nil possible
    return nil
}
```

Every other write to `p.interactiveState` happens on the event-loop goroutine
(in `Update` or — see §3.6 — in `View`); this closure is the only write from a
`tea.Cmd` goroutine. `exitInteractiveMode` (`interactive.go:805-814`) sets
`p.interactiveState = nil`, and the nil check at `:1191` runs before the closure
does — so if the user exits interactive mode (or the session dies) between the
click and the command running, the last line panics. The race detector doesn't
catch it because no test forwards a click.

**Fix:** return a message (`interactiveClickSentMsg{Err}`) and mutate state in
`Update`, as every other path already does.

### 2.6 Text selection is silently broken in the terminal panel 🟠

`mouse.go:527` starts a drag-selection when the click lands in
`regionTermPanelContent`, but the selection plumbing only knows about the
agent/shell buffer:

- `interactiveOutputBuffer()` (`interactive_selection.go:117`) returns the
  worktree's or shell's `OutputBuf` — never `p.termPanelOutput`.
- `interactiveLineIndexAtY` (`interactive_selection.go:58`) bails when
  `VisibleEnd <= VisibleStart`; `renderTermPanelOutput` (`terminal_panel.go:375`)
  never sets them, and the two renderers that do skip that bookkeeping in panel
  mode (`!p.interactiveState.TermPanel`, `view_preview.go:275`, `:483`), so both
  stay `0` for the entire panel session.

So a click in the panel is swallowed outright — `prepareInteractiveDrag` gets
`!ok`, clears the selection, and returns `nil`
(`interactive_selection.go:89-93`), never falling through to click-forwarding —
dragging selects nothing, and `alt+c` hits the empty-range fallback and toasts
"No output to copy" (`interactive_selection.go:169-192`). (`alt+v` works: paste
targets `interactiveState.TargetSession`, which panel entry sets to the panel
session, `interactive.go:511,530`.)

**Fix:** make the selection source a function of `interactiveState.TermPanel`;
have `renderTermPanelOutput` publish `VisibleStart`/`VisibleEnd`/`ContentRowOffset`
the same way `renderOutputContent` does.

### 2.7 Selection is impossible whenever the child app enables mouse reporting 🟠

`mouse.go:527,539` gate drag-selection on `!p.interactiveState.MouseReportingEnabled`,
and `handleMouseDrag` (`mouse.go:1342`) does the same. When the child app turns on
mouse tracking (Claude Code, vim, htop, anything setting `?1000h`/`?1002h`), all
clicks and drags are forwarded to the app and sidecar-side selection is entirely
unreachable. The user has no way to override.

This is the likeliest source of "text selection doesn't show up naturally
sometimes" — it depends on what the child process happens to have enabled, which
is invisible from the UI.

**Fix:** add a modifier escape hatch — the standard terminal convention is
**shift+drag selects locally even when the app owns the mouse** (iTerm2, Ghostty,
Kitty, GNOME Terminal all do this). Show a one-character indicator in the hint
line when the pane owns the mouse.

### 2.8 Cursor is drawn at a fabricated position while scrolled back 🟠

`view_preview.go:375-404` (and the identical block at `:580-609`,
`terminal_panel.go:465-489`) computes the on-screen cursor row purely from
`paneHeight` vs `displayHeight`, then **clamps into range**:

```go
if relativeRow < 0            { relativeRow = 0 }
if relativeRow >= displayHeight { relativeRow = displayHeight - 1 }
```

That math assumes the viewport is pinned to the bottom. When `previewOffset`
puts the viewport 300 lines up in scrollback, the cursor still gets painted —
pinned to the top or bottom edge of whatever you're reading. A ▉ block appears in
the middle of historical output, in a place no cursor is.

The clamp was introduced deliberately (`td-16bfa6`, "cursor disappearing
permanently") — but the fix over-corrected: it should hide the cursor when it is
genuinely off-screen, and only clamp for sub-line pane/display mismatches.

**Fix:** only draw when `autoScrollOutput` is true (or when the live pane's row
range actually intersects `[start,end)`), and render a "scrolled back — N lines
from live" affordance instead.

### 2.9 Selected worktree's status indicator freezes during interactive mode 🟡

`agent.go:993`: status detection is wrapped in `if !interactiveCapture`, and
`interactiveCapture` is true for the selected worktree whenever interactive mode
is active — **including when interactive mode is targeting the terminal panel**
(there is no `TermPanel` check at `agent.go:908-916`). The sidebar dot for the
selected workspace stops updating for the entire interactive session.

### 2.10 `OutputBuffer.Clear()` leaves `lastRawHash` stale 🟡

`workspace/types.go:539-545` resets `lastHash` and `lastLen` but not
`lastRawHash`; `tty/output_buffer.go:170-177` resets all three. Only the
`lastLen` guard prevents a stuck-blank buffer after `Clear()` — which
`terminal_panel.go:147` and `:714` call on every panel session switch. It's
correct today by accident, and the two copies disagree.

### 2.11 `tty.Model.View()` renders the *top* of scrollback, not the bottom 🟡

`tty/tty.go:226-227` joins **all** buffered lines (up to 600) and hands them to
`RenderWithCursor` with a row computed relative to `m.Height`. The consumer then
trims from the front (`filebrowser/inline_edit.go:362-364`):

```go
if len(lines) > contentHeight { lines = lines[:contentHeight] }
```

(`notes/inline_edit.go:374` has the identical trim.) For a fresh editor session
tmux history is empty, so this happens to work. The moment the editor session
accumulates scrollback (`:!`, `:term`, a shell escape), the inline editor shows
the oldest 40 lines with the cursor overlaid at a meaningless offset.

**Fix:** `View()` should slice the last `m.Height` lines and compute the cursor
row against that slice.

### 2.12 `history-limit` is set inconsistently 🟡

Set: `agent.go:380`, `agent.go:696`, `shell.go:1108` — in all three cases via
`set-option` *after* `new-session` has already created the pane. Not set:
`terminal_panel.go:190-204`, `shell.go:560-566` (the main `createShell` path),
`shell.go:664`, `shell.go:783`, `agent.go:751`, `notes/inline_edit.go:89`,
`filebrowser/inline_edit.go:98`. Those sessions get the tmux default (2000) — so
the terminal panel and most shells have shallower history than the agent panes.

Modern tmux (verified on 3.7b) applies a raised `history-limit` to existing
panes, so set-after-create works there; older releases applied it only to panes
created after the option was set, and sidecar never checks the tmux version.
Setting it once, globally, before any session exists (or chaining
`new-session \; set-option` in one invocation) is correct on both.

### 2.13 Batch capture shells out to `bash -c` with Go-quoted names 🟡

`agent.go:1126-1190` builds a shell script and runs it via `bash -c`:

```go
quotedSessions = append(quotedSessions, fmt.Sprintf("%q", s))
```

`%q` is *Go* quoting, not shell quoting. Inside bash double quotes, `$(…)`,
backticks and `${…}` still expand, and git ref names and directory names may
legally contain `$`, `(`, `)`. `sanitizeName` (`agent.go:807`) only strips `.`,
`:`, `/` — so a hostile branch or checkout-directory name reaches the script
intact. Only reachable with 2+ active sessions (the 0/1-session path at
`agent.go:1132` captures directly via argv), but it's arbitrary command
execution driven by a filename.

It's also the slowest possible way to do this: `bash` + N × `tmux` processes.
tmux accepts multiple commands in one invocation:

```
tmux display-message -p -t s1 'SIDECAR<nonce>:s1' \; capture-pane -p -e -S -600 -t s1 \; \
     display-message -p -t s2 'SIDECAR<nonce>:s2' \; capture-pane -p -e -S -600 -t s2 \; …
```

One process, no shell, argv-only quoting. Note the chained captures concatenate
on stdout with no separator of their own — the interleaved `display-message -p`
markers are what delimit them, and a per-invocation random nonce makes the
marker collision-proof against pane content (the current `===SIDECAR_SESSION:`
literal is guessable and collidable).

### 2.14 Dead code: interactive branches inside `handleListKeys` 🟢

`keys.go:901`, `:915`, `:923` check `p.viewMode == ViewModeInteractive` inside
`handleListKeys`, but `handleKeyPress` (`keys.go:47`) routes `ViewModeInteractive`
to `handleInteractiveKeys` unconditionally. Those branches are unreachable.

Consequence worth noting: `+`, `-`, and `\` (sidebar resize / toggle) are *not*
available in interactive mode at all — they go straight to tmux. That's arguably
correct, but the dead code implies otherwise.

### 2.15 Selection anchors drift while output arrives 🟠

`SelectionPoint.Line` is a buffer line index (`ui/selection.go:8-14`), but
`OutputBuffer.Update` (`types.go:427-467`) replaces `b.lines` wholesale from
each capture and trims to capacity from the front (`:459-464`). Line indices are
not stable across polls: hold a selection while the pane is producing output and
the highlight — and what `alt+c` copies — silently slides onto different text.

**Fix:** anchor selections in absolute pane coordinates. tmux exposes
`#{history_size}`; the absolute index of the first captured line is
`history_size − min(captureDepth, history_size)`, so storing
`absoluteOffset + bufferIndex` at selection time and re-deriving the buffer
index each frame keeps the anchor pinned to content. (Freezing capture updates
while a selection is held was considered and rejected — the pane would visibly
stall.)

---

## 3. Performance

### 3.1 Subprocess volume is the dominant cost

Per poll cycle on the hot path:

| Call | Subprocesses |
|---|---|
| `tmux send-keys` per keystroke | 1 (2 when flushing a pending escape; 2–3 per paste) |
| `capture-pane` | 1 |
| `display-message` (cursor + pane size; interactive only) | 1 |
| `queryPaneSize` when `directCapture` (`agent.go:949`; non-interactive selected pane — mutually exclusive with the cursor query) | 1 |
| `maybeResizeInteractivePane` on size change (500 ms throttle) | up to 6 |

At `pollingDecayFast = 50 ms` that is 20 polls/s × 2 spawns — **≈40 process
spawns/second sustained while typing** — dropping to ~10 spawns/s after 2 s idle
and ~4/s after 10 s. `CLAUDE.md` already flags that every spawn carries a large
fixed tax on machines running an endpoint security agent. This is the "sluggish"
feeling, and no amount of interval tuning fixes it — the architecture is wrong
for the cadence.

Two immediate reductions before the architectural fix in §6.1:

- **Merge capture + cursor into one invocation.** `tmux display-message -p` and
  `capture-pane` can be chained with `\;` in a single `tmux` call, halving the
  steady-state spawn rate:
  ```
  tmux display-message -t T -p '#{cursor_x},#{cursor_y},#{cursor_flag},#{pane_height},#{pane_width}' \; \
       capture-pane -p -e -S -600 -t T
  ```
- **Drop the re-query inside the resize path.** `maybeResizeInteractivePane`
  already receives `PaneWidth`/`PaneHeight` from the cursor query and compares
  them (`interactive.go:670`) — then `resizeTmuxTargetCmd` immediately
  re-queries the same values (`interactive.go:638`) and can run query → resize →
  re-query → conditional second resize, where each resize is itself 1–2 spawns
  via the `resize-window` → `resize-pane` fallback (`interactive.go:695-708`).
  Trust the values already in hand.

### 3.2 No focus/blur awareness

Nothing in the codebase handles `tea.FocusMsg` / `tea.BlurMsg`, and
`internal/app/view.go:40-52` never sets `v.ReportFocus = true`. Sidecar polls
tmux at full rate while its terminal window is in the background, behind another
app, or on another desktop. Setting `ReportFocus` and clamping to
`pollIntervalUnfocused` on blur is a few lines for a large idle-CPU win.

### 3.3 Always-on all-motion mouse tracking

`internal/app/view.go:50` sets `MouseModeAllMotion` unconditionally. Every pixel
of mouse movement produces an SGR sequence → a `MouseMotionMsg` → a full
hit-test + re-render, and is the origin of every split-CSI heuristic in §2.2.
All-motion is only needed for idle hover feedback — which sidecar does not rely
on — while `MouseModeCellMotion` still reports clicks and drags. **Decision:
switch to cell-motion.** No dynamic promotion to all-motion is needed; the lost
idle-motion events are an accepted tradeoff.

### 3.4 Render path does the same work two to three times

Per frame, for the visible output:

1. `renderOutputContent` (`view_preview.go:349,358`): `ui.ExpandTabs` per line,
   then `truncateCache.Truncate` per line.
2. `renderPreviewContent` (`view_preview.go:90`) then calls `truncateAllLines`
   over the joined result — which runs `ui.ExpandTabs` **again** and
   `lipgloss.Width` + `Truncate` **again** on every line
   (`view_preview.go:179-203`).
3. `renderWithCursor` (`interactive.go:1502,1534`) splits the joined string back
   into lines and re-joins it, to modify exactly one line.

Cheap fixes: skip `truncateAllLines` for content the pane already truncated;
apply the cursor to `displayLines[row]` before the join.

### 3.5 Full-buffer copies in the render path

`view_preview.go:297`, `view_preview.go:505` and `terminal_panel.go:402` call
`OutputBuf.Lines()` — which allocates and copies **all 500 lines** — solely to
find `lastNonEmptyLine`. Every frame, per pane. (`terminal_panel.go:402` copies
unconditionally but consumes the result only when `!interactive`, so every
interactive frame copies the whole panel buffer and throws it away.) Add
`OutputBuffer.LastNonEmptyLine()` that scans under the existing mutex without
copying, or cache the index at `Update()` time (it can only change when content
changes).

### 3.6 Rendering mutates model state

`renderOutputContent`/`renderShellOutput` write `p.previewOffset`
(`view_preview.go:322`, `:529`) and
`p.interactiveState.VisibleStart/VisibleEnd/ContentRowOffset` (`:279-281`,
`:341-342`, `:487-489`, `:547-549`); `renderPreviewContent` writes
`ContentRowOffset` again (`:20`, `:39`, `:63-75`); `renderTermPanelOutput`
clamps `p.termPanelScroll` (`terminal_panel.go:427-429`). Mouse-to-buffer
mapping therefore depends on the last
painted frame. It works because `View()` runs on the Update goroutine, but it
makes selection coordinates unreproducible in tests and couples hit-testing to
render order. Compute a `previewLayout` struct in `Update` and let `View` read it.

### 3.7 Buffer/capture constants don't line up

`captureLineCount = 600`, `outputBufferCap = 500`, `tmuxHistoryLimit = 10000`.
tmux retains 10 000 lines; sidecar throws away 95% of them on every capture and
keeps 500. Scrollback in the preview is therefore capped at 500 lines, with no
indication when you hit the ceiling (§5.2). (`internal/tty` sizes differently
again: `Config.ScrollbackLines = 600` is both its capture depth and its buffer
cap, `tty.go:35`.)

The `shell-integration` skill also documents `tmuxCaptureMaxBytes` as
`600` "scrollback lines"; it is actually a **byte** cap defaulting to 2 MB
(`config.go:85-86`, `agent.go:211`). Worth correcting in
`.claude/skills/shell-integration/SKILL.md`.

---

## 4. Duplication / cleanup

The workspace plugin re-implements `internal/tty` almost line for line. Both
copies have since drifted.

| Concern | `internal/tty` | `workspace` duplicate |
|---|---|---|
| `OutputBuffer` (+`Update`/`Write`/`Lines`/`LinesRange`/`Clear`) | `output_buffer.go:35-184` | `types.go:404-552` |
| mouse/terminal-mode regexes | `output_buffer.go:11-33` | `types.go:11-20` |
| `sendKeyToTmux` / `sendLiteralToTmux` (incl. `;` hex fallback) | `session.go:26-46` | `interactive.go:166-186` |
| `SendKeysCmd` / `KeySpec` | `session.go:51-66`, `keymap.go:139` | `interactive.go:197-212`, `:189-192` |
| `IsSessionDeadError` | `session.go:13` | `interactive.go:147` |
| `ResizeTmuxPane` | `session.go:70-96` | `interactive.go:683-709` |
| `QueryPaneSize` | `session.go:106-125` | `interactive.go:711-730` |
| `SendSGRMouse` | `session.go:131-141` | `interactive.go:1223-1233` |
| `QueryCursorPositionSync` | `cursor.go:70-97` | `interactive.go:1464-1491` |
| `RenderWithCursor` / `CursorStyle` | `cursor.go:17-64` | `interactive.go:1439-1445`, `:1497-1535` |
| bracketed-paste + mouse-mode constants & detectors | `terminal_mode.go` | `interactive.go:246-355` |
| `IsPasteInput`, paste senders | `paste.go:17-99` | `interactive.go:216-289` |
| polling constants | `polling.go:8-33` | `interactive.go:23-58` |
| `SessionDeadMsg`, `EscapeTimerMsg`, `PasteResultMsg`, `PaneResizedMsg` | `messages.go` | `interactive.go:96-100`, `messages.go` |

That's roughly **500 lines of exact-duplicate logic**, plus divergences already
noted (`Clear()` §2.10, the `ContainsAny` filter §2.2, `pollingDecaySlow` is
500 ms in workspace vs 250 ms in tty). The workspace plugin already imports
`internal/tty` (`interactive.go:18`) — but only for the keymap, the
mouse-fragment regexes, and the CSI-u helpers; the send/resize/cursor/paste
surface is all local copies.

Additional duplication:

- **`renderOutputContent` vs `renderShellOutput`** (`view_preview.go:222-407` and
  `:440-612`) are ~95% identical — 190 lines duplicated, differing only in which
  struct supplies `OutputBuf` and the hint text. `renderTermPanelOutput`
  (`terminal_panel.go:375-492`) is a third, slightly-diverged copy of the same
  viewport/cursor logic. All three should collapse into one
  `renderTerminalViewport(src, opts)`.
- **`notes/inline_edit.go` vs `filebrowser/inline_edit.go`** — 773 and 742
  lines; ~170 effectively identical (`normalizeEditorName`, the three
  mouse-forwarding functions, exit-confirmation flow, `exitInlineEditMode`,
  height calculation) and another ~300 structurally parallel but diverged
  (`enterInlineEditMode`, `sendEditorSaveAndQuit`, mouse-coordinate mapping,
  render). filebrowser has `isSessionAlive`/`killSession` helpers; notes inlines
  the same tmux calls. All of it belongs in `internal/tty` as an
  `EditorSession` helper.
- **Four poll schedulers over three generation counters.** `scheduleAgentPoll`
  and `scheduleInteractivePoll` (sharing `pollGeneration`),
  `scheduleShellPollByName` (`shellPollGeneration`), `scheduleTermPanelPoll`
  (`termPanelGeneration`), with a documented "don't mix them" rule that has
  already caused a 200% CPU bug (`td-97327e`, `interactive.go:477-486`,
  `:1389-1391`). `tty.CalculatePollingInterval` is additionally re-implemented
  inline at `interactive.go:1348-1355`. One `paneSource` interface with one
  scheduler removes the whole class.
- **Scroll-burst detection is copy-pasted** in `forwardScrollToTmux`
  (`interactive.go:1136-1154`) and `scrollPreview` (`mouse.go:1197-1219`).

**Recommendation:** migrate the workspace plugin onto `internal/tty` and delete
the fork. It is the single highest-leverage change in this document — most bugs
in §2 exist in exactly one of the two copies.

---

## 5. Feature gaps

### 5.1 No keyboard scrolling in interactive mode

Every key is forwarded to tmux, so the only way to reach sidecar's own scrollback
is the mouse wheel. There is no keyboard path at all. Standard terminals reserve
`shift+PgUp`/`shift+PgDn` (and `shift+Home`/`shift+End`) for the emulator's own
scrollback; sidecar should intercept those before forwarding, plus
`shift+↑`/`shift+↓` for line-wise.

### 5.2 No scroll position indicator

The sidebar renders a scrollbar (`view_list.go:403`); the terminal pane renders
nothing. There is no way to tell you're scrolled back, how far, or that you've
hit the 500-line ceiling. `internal/ui/RenderScrollbar` already exists — wire it
in, plus a `▲ 143 lines back · end to jump to live` marker on the hint line.

### 5.3 Scrollback is 500 lines when tmux holds 10 000

See §3.7. tmux is already holding the history (10 000 lines for agent panes) —
sidecar just never reads past 600. Deepening is mostly a matter of raising
`outputBufferCap` and capturing lazily: keep the live screen on the fast path,
and fetch older ranges with explicit bounds
(`capture-pane -p -e -S -1200 -E -601`) only when the user scrolls past what's
buffered. §2.12 has to land first or most panes have no history to fetch.

### 5.4 No search in scrollback

There's no `/`-style find in terminal output — a routine need when reading a long
agent transcript. `internal/plugins/filebrowser` already has fuzzy + content
search primitives (`fuzzy.go`, `project_search.go`) to borrow from.

### 5.5 Selection only exists inside interactive mode

`p.selection` is cleared on entry (`interactive.go:473`, `:535`) and the
highlight is only rendered `if interactive && p.selection.HasSelection()`
(`view_preview.go:351`, `:557`). Outside interactive mode a click on the preview
*enters* interactive mode (`mouse.go:570-583`; with the terminal panel visible
it merely moves focus, `:566-569`) rather than starting a selection. Read-only
selection in the normal preview is a reasonable expectation.

### 5.6 Missing standard selection gestures

`SelectionState` supports only character-range drag. Missing:
double-click-selects-word (double-click currently *enters interactive mode*,
`mouse.go:876-897`), triple-click-selects-line, shift+click-extends,
`ctrl+a`-style select-all, and copy-on-select. All are cheap on top of the
existing `SelectionPoint` model. Copy-on-select ships behind a config flag,
default off — expectations differ by platform and it must never surprise a
macOS-convention user.

### 5.7 No block/rectangular selection

Copying a column out of `git log --graph` or a table currently requires manual
cleanup. `alt+drag` for rectangular selection is a well-understood convention and
`VisualSubstring` (`selection_render.go:76`) already does column-precise
extraction.

### 5.8 No OSC-8 / URL / file-path affordances

Agent output is full of `file.go:123` references and URLs. Sidecar already knows
how to route to the file browser (`filebrowser.NavigateToFileMsg`, per
`CLAUDE.md`) — detecting `path:line` in terminal output and making it clickable
would be a genuinely differentiating feature and reuses existing plumbing.

### 5.9 Terminal panel is a second-class citizen

No selection and no working copy (§2.6 — paste does work), no scrollback
indicator, no `history-limit` (§2.12), single instance only, and its poll loop
runs a second cursor query alongside the agent poll (§2.9). Unifying it onto the
shared viewport (§4) fixes most of this for free.

### 5.10 No visible indication of who owns the mouse

`MouseReportingEnabled` silently changes what a click does (§2.7) with zero UI
signal. A small glyph in the hint line ("app mouse — shift+drag to select") makes
the behavior legible.

---

## 6. Architectural recommendations

### 6.1 Replace polling with a persistent tmux control-mode connection ⭐

The single biggest win available. `tmux -C` (control mode, tmux ≥ 1.8) keeps
**one** long-lived process; commands go in over stdin (replies framed by
`%begin`/`%end`/`%error`), and tmux pushes `%output`, `%layout-change`,
`%window-pane-changed`, `%exit` notifications out over stdout as they happen.

| | Today | Control mode |
|---|---|---|
| Processes while typing | ~40/s | 1, for the app's lifetime |
| Output latency | 20 ms debounce + 0–50 ms poll + spawn | push notification + in-band capture, no spawn |
| Idle CPU | continuous `capture-pane` | zero |
| Change detection | hash the whole screen every poll | tmux tells you |
| Cursor position | separate `display-message` | in-band |

One thing to be precise about: `%output` carries the pane's **raw byte stream**
— escape sequences and all, non-printables octal-escaped — not rendered lines.
Until sidecar has a VT parser (§6.5) it cannot render from `%output` directly,
so the design is *notification-driven capture*: treat `%output` as the change
signal, coalesce per frame, and issue `capture-pane` **over the control
channel** to fetch the rendered screen. Same rendering pipeline as today, but
push-driven, in-band, and spawn-free. The entire
adaptive-decay/stagger/generation/throttle machinery (`polling.go`,
`staggerOffset`, three generation maps, runaway detection) collapses, and with
it the freeze in §2.1. This shape also makes §6.5 a drop-in later: the parser
replaces the capture step and nothing else moves.

Design notes:

- A control-mode client is a real attached client; size it explicitly
  (`refresh-client -C WxH`) so it can never clamp the window.
- Enable flow control where available (tmux ≥ 3.2: `refresh-client -f
  pause-after=N`, `%pause`/`%continue`) so a stalled reader degrades gracefully
  instead of ballooning tmux's buffers.
- Keep the existing `capture-pane` polling path as the fallback when the control
  process dies, and as the degraded mode on old tmux — feature-detect rather
  than version-sniff (the codebase currently checks no tmux version at all).

Practical shape: `internal/tty/control.go` owns the `tmux -C` process and a
`map[paneID]*paneSubscriber`. Migrate one consumer (terminal panel — smallest
blast radius) first.

If control mode proves awkward, the intermediate step is still worth it:
batching every per-poll query into one `tmux` invocation with `\;` separators
(§3.1; delimiting caveat in §2.13), which alone halves spawn count.

### 6.2 Use bubbletea v2's real cursor instead of a painted ▉ ⭐

`tea.View.Cursor` (`bubbletea/v2/tea.go:131`; the `Cursor` struct at
`tea.go:357`) accepts a position, a shape
(`CursorBlock`/`CursorUnderline`/`CursorBar`), a color, and `Blink`. Setting it
places the **actual terminal cursor** in the pane.

That directly addresses "text selection cursors don't show up naturally":

- it blinks the way the user's terminal blinks, at the user's rate;
- it honors the user's cursor shape and theme;
- it doesn't overwrite the character underneath (today `RenderWithCursor`
  replaces whitespace with `█`, `cursor.go:57-59`);
- it survives being on the last column, past EOL, and inside SGR runs without
  the `ansi.Cut` splicing in `cursor.go:44-61`;
- screen readers and terminal cursor-tracking features work.

Path: plumb a `*tea.Cursor` up from the plugin's `View()` to `app.Model.View()`
alongside the string. There is exactly one real cursor per application, so
ownership follows focus: fall back to the painted block when the pane isn't the
focused surface, and yield the cursor entirely under modals.

### 6.3 Opt into keyboard enhancements

Setting `tea.View.KeyboardEnhancements` requests kitty-protocol disambiguation
from the terminal. Modified keys then arrive as real `KeyPressMsg` with
modifiers rather than unknown CSI blobs — which makes §2.4 moot and lets
`keymap.go`'s hand-maintained CSI table (`keymap.go:17-45`) shrink to a
generated encoder.

### 6.4 Enable focus reporting

`v.ReportFocus = true` + `FocusMsg`/`BlurMsg` handling in
`internal/app/update.go`, gating poll intervals. Small change, large idle-CPU
win (§3.2).

### 6.5 Longer term: consider an in-process terminal emulator

The heuristics in `LooksLikeMouseFragment` / `partialMouseEscapeRegex` /
`ContainsMouseSequence` (`output_buffer.go:186-264`) exist because sidecar parses
a *rendered screen* as text. A real VT parser over a control-mode `%output`
stream (`hinshun/vt10x`, `charmbracelet/x/vt`) would give a proper cell grid:
exact cursor state, per-cell attributes, correct wide-char and grapheme handling,
alternate-screen awareness, real scrollback, and unambiguous selection
coordinates — deleting essentially all of the escape-sequence guessing.

One seam to plan for: `%output` streams only from subscription time, so each
pane's emulator must be seeded from a `capture-pane -e` snapshot plus queried
state (`#{alternate_on}`, cursor position, pane modes). A capture cannot fully
reconstruct emulator internals (scroll regions, saved cursors), so expect minor
drift until the next full-screen redraw.

That's a large change and shouldn't be attempted before §6.1. But §6.1 is the
prerequisite for it, so it's worth choosing the control-mode design with this
endpoint in mind.

---

## 7. Suggested sequencing

**Phase 1 — stop the bleeding (small, independent, high impact)**

1. §2.1 poll chain dies after scroll
2. §2.2 post-scroll character filter eats `m`/`M`/`;`/`<`
3. §2.3 honor wheel delta + accumulate dropped scroll events
4. §2.5 race/panic in `forwardClickToTmux`
5. §2.8 don't paint a cursor while scrolled back
6. §3.1 merge cursor query into the capture invocation; drop the resize path's
   re-query
7. §6.4 focus reporting
8. §2.13 batch-capture command injection — argv-only, nonce-delimited markers
   (small, isolated, and it's command execution from a filename)

**Phase 2 — consolidate**

8. §4 migrate workspace onto `internal/tty`; delete the fork
9. §4 collapse the three viewport renderers into one
10. §4 one poll scheduler / one generation counter
11. §2.6, §2.7, §2.9, §2.10, §2.11, §2.12, §2.14 — most become trivial or
    disappear once there's one implementation
12. §3.4, §3.5, §3.6 render-path cleanups
13. Fix `csiu` detection (§2.4) and its test

**Phase 3 — make it feel like a terminal**

14. §5.1 keyboard scrolling · §5.2 scroll indicator · §5.3 deeper scrollback
15. §5.5–5.7 selection gestures, selection outside interactive mode, block
    select; fix selection anchor drift (§2.15)
16. §5.4 scrollback search
17. §5.8 clickable `path:line` and URLs
18. §6.2 real cursor via `tea.View.Cursor`

**Phase 4 — architecture**

19. §6.1 tmux control mode
20. §6.3 keyboard enhancements
21. §6.5 evaluate an in-process VT parser

---

## 8. Test coverage notes

Current: `internal/tty` has 5 test files (~800 lines); workspace has
`interactive_test.go` (1221), `interactive_selection_test.go` (660),
`scroll_test.go` (440), `mouse_test.go` (213). All green, including under `-race`.

Gaps that let the bugs above through:

- **Poll-chain continuity.** Nothing asserts that a poll is always rescheduled.
  A test that drives `AgentOutputMsg` after a simulated scroll and asserts a
  non-nil command would have caught §2.1.
- **Key fidelity.** No test types a literal `m` shortly after a scroll (§2.2).
  A table of "these characters must always reach tmux" is cheap insurance.
- **Scroll magnitude.** `scroll_test.go` asserts direction, not distance (§2.3).
- **Real message types.** `csiu_test.go` uses a stand-in type instead of the
  actual one (§2.4) — the test passes while production is dead.
- **Terminal panel selection.** No coverage at all (§2.6).
- **Cursor suppression while scrolled back** (§2.8).
- **Mouse-leak regressions.** No test feeds split SGR mouse sequences (with and
  without the leading ESC, split at each byte boundary) through the interactive
  input path and asserts nothing reaches `send-keys` as literal text — the
  filters in §2.2 exist precisely for this and have no safety net.
- **Selection stability across captures.** Nothing pins a selection to content
  while the buffer updates underneath it (§2.15).
- **Commands that mutate plugin state** — a lint or review rule that `tea.Cmd`
  closures may only read captured values would have caught §2.5.
