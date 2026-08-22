---
name: shell-integration
description: >
  Interactive shell/TTY integration with tmux session management, shell command
  execution, control-mode output capture with polling fallback, native cursor
  rendering, lazy scrollback, selection, paste handling, and inline editing.
  Use when working on shell integration, tmux features, command execution, or
  interactive mode.
user-invocable: false
---

# Shell Integration

Sidecar's interactive shell allows users to type directly into tmux sessions
from within the TUI. Tmux remains the PTY backend. Sidecar renders ordered
control-mode bytes through the shared `tty.Model`, whose VT behavior is behind
the `screenmodel` adapter rather than implemented in plugin code.

## Package Structure

```
internal/tty/                    # Shared tmux terminal abstraction
  tty.go                         # Core Model and State types
  keymap.go                      # Bubble Tea -> tmux key translation
  messages.go                    # Owner/target/generation-scoped messages
  session.go                     # tmux operations (send-keys, capture-pane, resize)
  scheduler.go                   # Keyed fallback-poll generation ownership
  control_*.go                   # Session-keyed tmux -C transport and manager
  capture_range.go               # Atomic bounded history capture
  cursor.go                      # Cursor positioning helpers
  paste.go                       # Paste handling (clipboard, bracketed paste)
  terminal_mode.go               # Capture-fallback mode recovery
  output_buffer.go               # Absolute, bounded live/history buffer
  editor_session.go              # Shared inline-editor tmux lifecycle

internal/plugins/workspace/
  interactive.go                 # Workspace-specific interactive mode logic
  interactive_selection.go       # Text selection in interactive mode
  terminal_viewport.go           # Pure shared terminal viewport renderer
  terminal_control.go            # Workspace target/layout policy for tty.Model
  terminal_history.go            # Lazy absolute scrollback loading
  terminal_search.go             # Loaded-history search
  terminal_links.go              # Safe URL/path detection and activation
  native_terminal.go             # Native cursor and contextual mouse mode
  view_preview.go                # Agent/shell preview composition
  mouse.go                       # Scroll handling
  types.go                       # InteractiveState type

internal/plugins/filebrowser/
  inline_edit.go                 # Inline editor mode using tty.Model
  handlers.go                    # Message handling for inline edit
```

## Data Flow

```
User Keypress -> handleInteractiveKeys() -> tty.MapKeyToTmux() -> tmux send-keys

Pane output -> tmux -C ordered %output bytes
            -> session-pooled control actor
            -> seeded screenmodel adapter
            -> owner/target/generation-scoped Tea message
            -> OutputBuffer + cursor/modes/history
            -> pure terminal viewport + native Bubble Tea cursor

Open/resync/history -> bounded capture seed/range
Control unavailable/dead -> one scoped capture-poll fallback + clean reseed
```

## Core Abstractions

### tty.Model

Embeddable component for interactive tmux functionality:

```go
type Model struct {
    Config   Config        // Exit key, copy/paste keys, scrollback lines
    State    *State        // Current interactive state
    Width    int
    Height   int
    OnExit   func() tea.Cmd
    OnAttach func() tea.Cmd
}

// Usage:
p.inlineEditor = tty.New(&tty.Config{
    ExitKey: "ctrl+\\",
    ScrollbackLines: 600,
})
cmd := p.inlineEditor.Enter(sessionName, paneID)
```

### tty.State

```go
type State struct {
    Active        bool
    TargetPane    string      // tmux pane ID (e.g., "%12")
    TargetSession string
    LastKeyTime   time.Time   // Input timing and fallback polling decay
    CursorRow, CursorCol int
    CursorVisible        bool
    PaneHeight, PaneWidth int
    BracketedPasteEnabled bool
    MouseReportingEnabled bool
    OutputBuf      *OutputBuffer
    PollGeneration int          // For invalidating stale fallback polls
}
```

### tty.OutputBuffer

Thread-safe bounded buffer with hash-based change detection:

```go
func (b *OutputBuffer) Update(content string) bool {
    rawHash := maphash.String(seed, content)
    if rawHash == b.lastRawHash { return false }  // Skip ALL processing
    content = mouseEscapeRegex.ReplaceAllString(content, "")
    b.lines = strings.Split(content, "\n")
    return true
}
func (b *OutputBuffer) LinesRange(start, end int) []string
```

## Key Mapping (`keymap.go`)

```go
func MapKeyToTmux(msg tea.KeyPressMsg) (key string, useLiteral bool) {
    if msg.Mod.Contains(tea.ModCtrl) && msg.Code >= 'a' && msg.Code <= 'z' {
        return "C-" + string(msg.Code), false
    }
    switch msg.Code {
    case tea.KeyEnter:     return "Enter", false
    case tea.KeyBackspace: return "BSpace", false
    case tea.KeyTab:       return "Tab", false
    case tea.KeyUp:        return "Up", false
    }
    if msg.Text != "" {
        return msg.Text, true // Literal mode
    }
    return "", true
}
```

Modified keys use CSI sequences:
```go
case "shift+up":   return "\x1b[1;2A", true
case "ctrl+up":    return "\x1b[1;5A", true
case "alt+up":     return "\x1b[1;3A", true
case "shift+tab":  return "\x1b[Z", true
```

For printable characters, `tmux send-keys -l` prevents interpretation.

## Capture fallback and semantic observation

```go
const (
    PollingDecayFast   = 50ms    // During active typing
    PollingDecayMedium = 200ms   // After 2s inactivity
    PollingDecaySlow   = 250ms   // After 10s inactivity
    KeystrokeDebounce  = 20ms    // Delay after keystroke
)
```

Control-mode bytes are the ordinary presentation source for every visible
terminal surface. Adaptive capture polling exists only until the first seeded
frame and after control/model failure. Workspace agent and shell observation
continues independently for provider activity evidence; those captures never
overwrite a model-owned presentation buffer.

Set `SIDECAR_TERMINAL_TRACE=1` only in an isolated proof run to log privacy-safe
capture metadata (`surface`, `role`, `reason`, and `generation`). It never logs
session or pane identity, terminal text, commands, paths, titles, or provider
payloads. This distinguishes intentional semantic observation from presentation
fallback.

### Visibility and focus

| State | Active | Idle |
|-------|--------|------|
| Visible + focused | 200ms | 2s |
| Visible + app unfocused | clamped to unfocused cadence | clamped |
| Not visible | 10-20s | 10-20s |

### Keyed generation ownership

`tty.KeyedScheduler` owns a generation per logical source
(`agent:<name>`, `shell:<tmuxName>`, `terminal-panel`). Every schedule allocates
a fresh token, and the token travels through capture, result, retry, and
continuation messages. Reset invalidates pending timers and in-flight results.

```go
token, cmd := scheduler.Schedule(key, delay, makeMessage)
if scheduler.IsCurrent(key, token) { /* apply result */ }
```

Control subscriptions are pooled by tmux session because a control client cannot
observe panes in another session. Subscription close and manager stop invalidate
and drain queued callbacks before returning.

## Cursor Positioning (`cursor.go`)

### Query

```go
func QueryCursorPositionSync(target string) (row, col, paneHeight, paneWidth int, visible, ok bool) {
    cmd := exec.Command("tmux", "display-message", "-t", target,
        "-p", "#{cursor_x},#{cursor_y},#{cursor_flag},#{pane_height},#{pane_width}")
}
```

### Rendering

Focused live terminal surfaces expose a `tea.Cursor` through the plugin
`CursorProvider` capability. Workspace, filebrowser, and notes compute exact
application coordinates and suppress the cursor under modals, while scrolled
back, outside the visible slice, or when another surface owns focus. A painted
cursor is not added to native-cursor content.

### Height Mismatch Adjustment

When display height differs from tmux pane height:
```go
if paneHeight > displayHeight {
    relativeRow = cursorRow - (paneHeight - displayHeight)
} else if paneHeight > 0 && paneHeight < displayHeight {
    relativeRow = cursorRow + (displayHeight - paneHeight)
}
```

## Scrolling

Scrolling operates on the captured buffer. No tmux copy-mode involved.

```go
type Plugin struct {
    previewOffset    int   // Lines from bottom (0 = at bottom/live)
    autoScrollOutput bool  // Auto-follow new output?
}
```

- Scroll UP: pause auto-scroll, increment `previewOffset`
- Scroll DOWN: decrement `previewOffset`, re-enable auto-scroll at 0
- Live capture stays bounded at 600 lines; older 600-line ranges load lazily
- Absolute buffer coordinates keep selections and search matches stable
- Scrollback is bounded by the shared 10,000-line tmux history policy
- Instant response (pure state manipulation, no subprocess calls)

## Copy/Paste

- Copy: `alt+c` (configurable via `interactiveCopyKey`), or `super+c` (Cmd+C) as a
  built-in that a configured key does not replace. Cmd+C only arrives when the
  emulator passes it through — terminals that keep it for themselves (iTerm2)
  never deliver it, so `alt+c` stays the portable chord.
- Paste: `alt+v` (configurable via `interactivePasteKey`)

Paste wraps text with bracketed paste sequences (`\x1b[200~`...`\x1b[201~`) when the application has enabled bracketed paste mode.

## Fallback Terminal Mode Recovery (`terminal_mode.go`)

When capture fallback owns presentation, detects bracketed paste and mouse
reporting modes by scanning the fallback snapshot. Healthy model-backed
presentation receives these modes from the shared screen model.

## Width Synchronization

Tmux panes are resized in background at all times (not just interactive mode):

```go
func ResizeTmuxPane(paneID string, width, height int) {
    // resize-window, fallback to resize-pane for older tmux
}
```

Resize triggers: window resize, sidebar toggle/drag, selection change, agent/shell creation, interactive mode entry.

## Inline Edit Mode (Filebrowser)

Uses `tty.Model` plus `tty.EditorSession` for vim/nano/emacs editing in the file
preview pane. Session creation is history-safe and asynchronous:

```go
func (p *Plugin) enterInlineEditMode(path string) tea.Cmd {
    return func() tea.Msg {
        session, err := tty.StartEditorSession(tty.EditorSessionOptions{Path: path})
        return InlineEditStartedMsg{Session: session, Err: err}
    }
}
```

## Entry and Exit

**Workspace Plugin:**
- Enter: `enter` / `E` when preview pane focused with output tab
- Exit: `Ctrl+\` (instant) or double-Escape (150ms delay)
- Attach: `Ctrl+]` / `t` only when `tmux_full_attach` is on (default off)

**Filebrowser Plugin:**
- Enter: `e` or `Enter` on a file (if inline edit enabled)
- Exit: `Ctrl+\` or double-Escape
- Attach: `Ctrl+]` only when `tmux_full_attach` is on (default off)

## Feature Flags

```json
{
  "features": {
    "tmux_interactive_input": true,
    "tmux_inline_edit": true,
    "tmux_full_attach": false,
    "workspace_terminal_panel": true
  }
}
```

## Configuration

```json
{
  "plugins": {
    "workspace": {
      "interactiveExitKey": "ctrl+\\",
      "interactiveAttachKey": "ctrl+]",
      "interactiveCopyKey": "alt+c",
      "interactivePasteKey": "alt+v",
      "tmuxCaptureMaxBytes": 2097152,
      "copyOnSelect": false
    }
  }
}
```

## Critical Rules

1. **Scope every async result** by owner, target/role, activation, and keyed generation.
2. **Carry the generation end to end** through capture, result, retry, and continuation.
3. **Never call subprocesses from `Init()` or `View()`**; use `Start()`/`tea.Cmd`.
4. **Do not mutate model state in `tea.Cmd` callbacks**; return a scoped message.
5. **Hash raw capture before cleaning/splitting**, but mode changes must still clear coordinate state.
6. **Preserve newer live overlap** when delayed history ranges prepend.
7. **Control clients are per tmux session** and close/stop must invalidate and drain deliveries.
8. **Keep keyed capture fallback working** until the first accepted model frame and after control failure; never run it as the healthy renderer.
9. **Use native cursor only for the focused live viewport**; hide it offscreen, under modals, and in scrollback.
10. **Treat source OSC as untrusted**; the sanitizer and independent fuzz oracle must cover nested 7-bit/C1 forms and removal-boundary synthesis.
11. **Width sync matters**; resize panes/control clients when terminal geometry changes.

## References

- [Tmux integration notes](references/tmux-notes.md) -- detailed tmux CLI techniques, cursor tracking, resize sync, bracketed paste, mouse forwarding, modified keys, adaptive polling, debugging
- Original spec: `docs/plans/implemented/spec-tmux-interactive-input.md`
