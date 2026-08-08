You are working inside the Dispatch Loop — an autonomous coding loop that processes tasks one at a time. You are building **Sidecar**, a terminal-native AI coding companion.

Sidecar is a Go + Bubble Tea TUI application that shows real-time sessions, diffs, file trees, conversations, and git status from multiple AI coding tools (Claude Code, Cursor, Codex, Copilot, Gemini CLI, etc.) in a single dashboard.

## Architecture Overview

| Package | Purpose |
|---------|---------|
| `cmd/sidecar/` | Main entry point with version injection via ldflags |
| `internal/app/` | Bubble Tea app shell — layout, focus management, keybindings, inter-plugin messaging |
| `internal/plugin/` | Plugin interface: `ID()`, `Name()`, `Init()`, `Update()`, `View()`, `Commands()` |
| `internal/plugins/workspace/` | Workspace plugin — tmux capture, diff view, output streaming |
| `internal/plugins/conversations/` | Conversations plugin — session list, message viewer, search |
| `internal/plugins/filebrowser/` | File browser plugin — tree navigation, file preview, CRUD |
| `internal/plugins/gitstatus/` | Git status plugin — staged/unstaged, diff parsing, commit |
| `internal/plugins/notes/` | Notes plugin — freeform documents with markdown preview |
| `internal/plugins/tdmonitor/` | TD monitor plugin — task board, issue details, log viewer |
| `internal/adapter/` | Adapter interface for AI tool session data (Claude, Cursor, Codex, etc.) |
| `internal/adapter/claudecode/` | Claude Code adapter — reads JSONL sessions, parses tool calls |
| `internal/adapter/cursor/` | Cursor adapter — workspace storage, composer sessions |
| `internal/adapter/codex/` | Codex CLI adapter |
| `internal/adapter/copilot/` | GitHub Copilot adapter |
| `internal/adapter/geminicli/` | Gemini CLI adapter |
| `internal/adapter/amp/` | Amp adapter |
| `internal/adapter/warp/` | Warp terminal adapter |
| `internal/config/` | JSON config at `~/.config/sidecar/config.json` |
| `internal/theme/` | Theming system — color schemes, adaptive palettes |
| `internal/keymap/` | Keybinding definitions and context-aware dispatch |
| `internal/styles/` | Lipgloss style helpers using theme tokens |
| `internal/modal/` | Declarative modal system — sections, key-value, textarea |
| `internal/palette/` | Command palette — fuzzy search, categorized commands |
| `internal/state/` | App state management — focus, layout, panels |
| `internal/mouse/` | Mouse event handling and zone management |
| `internal/tty/` | Terminal capability detection |
| `internal/fdmonitor/` | File descriptor monitoring |
| `internal/image/` | Kitty graphics protocol support |
| `internal/migration/` | Config/data migration between versions |

## Plugin System

All UI functionality lives in plugins. Each plugin implements the `Plugin` interface:

```go
type Plugin interface {
    ID() string
    Name() string
    Icon() string
    Init(ctx *Context) error
    Start() tea.Cmd
    Stop()
    Update(msg tea.Msg) (Plugin, tea.Cmd)
    View(width, height int) string
    IsFocused() bool
    SetFocused(bool)
    Commands() []Command
    FocusContext() string
}
```

**Critical rendering rule**: Always constrain plugin output height. The app's header/footer are always visible — plugins must not exceed their allocated `height` parameter or the header scrolls off-screen.

**Footer rule**: Do NOT render footers in plugin `View()`. The app renders a unified footer bar using `plugin.Commands()`. Keep command names short (1 word).

**Inter-plugin communication**: Via `tea.Msg` broadcast. Use `app.FocusPlugin(id)` to switch focus. Plugins receive all messages.

## Adapter System

Adapters provide a uniform interface to read AI session data from different tools:

```go
type Adapter interface {
    ID() string
    Name() string
    Detect(projectRoot string) (bool, error)
    Sessions(projectRoot string) ([]Session, error)
    Messages(sessionID string) ([]Message, error)
    Watch(projectRoot string) (<-chan Event, io.Closer, error)
}
```

Optional interfaces: `ProjectDiscoverer`, `TargetedRefresher`, `WatchScopeProvider`, `WatchTierClassifier`.

The adapter layer uses a tiered watcher (`internal/adapter/tieredwatcher/`) with fsnotify for active sessions and polling for cold sessions.

## Build & Test

```bash
go build -o bin/sidecar ./cmd/sidecar    # Build
go test ./...                              # All tests
go test -race ./...                        # Race detector
go test ./internal/plugins/workspace/      # Single package
```

For a managed development install, use `make install-local` from canonical
`main` or deliberately use `make install-worktree` elsewhere, then run
`make install-status`. Plain `make install` remains an unmanaged `go install`.

## Critical Rules

- **Plugin height constraint is sacred.** Never let plugin `View()` exceed the `height` parameter. This causes the header to scroll off-screen.
- **No footers in plugins.** The app renders the footer from `Commands()`. Duplicate footers break the layout.
- **Tests are mandatory.** Every file with logic gets a `_test.go` companion. Use table-driven tests.
- **Error handling:** Return errors, don't panic. Wrap with `fmt.Errorf("context: %w", err)`.
- **Adapter interface compliance.** All adapters must implement the full `Adapter` interface. Optional interfaces are opt-in.
- **No global state.** All state flows through Bubble Tea's `Update`/`View` cycle.
- **Mouse zones must be unique.** Use `mouse.Zone()` with unique IDs to avoid click handler conflicts.

## Coding Conventions

- **Plugins**: One directory per plugin in `internal/plugins/`. Follow the `Plugin` interface. Split into `plugin.go` (state/update), `view*.go` (rendering), `keys.go` (keybindings).
- **Adapters**: One directory per tool in `internal/adapter/`. Follow the `Adapter` interface. Include `testdata/` fixtures.
- **Styles**: Use `internal/styles/` helpers with theme tokens. Never hardcode colors.
- **Modals**: Use the declarative modal system in `internal/modal/`. Sections: List, KeyValue, Textarea, Custom.
- **Config**: JSON at `~/.config/sidecar/config.json`. Add defaults in `internal/config/`. Plugin-specific config under `plugins.<name>`.
- **Keybindings**: Define in `internal/keymap/`. Context-aware — different bindings per focus state.
- **Tests**: Table-driven for parameter variations. Use `testutil` helpers for adapter tests. Mock filesystem operations.

## What to do this iteration

### Step 0: Read TD state

```bash
td usage --new-session
```

Or if resuming: `td usage -q`

### Step 1: Pick a task

If any task is `in_progress`, resume it. Otherwise pick the highest-priority `open` task with the `dispatch` label.

### Step 2: Implement

```bash
td start <id>
```

1. Read the task description carefully: `td show <id>`
2. Explore the relevant code before changing anything
3. Write the complete feature with all edge cases
4. Write tests — table-driven for parameter variations, integration tests for workflows
5. Run quality gates:
   ```bash
   go build ./...
   go test ./...
   ```

### Step 3: Verify

- Run the relevant test suites and capture output
- For UI changes: describe what you verified, run `go build ./...` to confirm compilation
- For adapter changes: verify with test fixtures and edge cases

### Step 4: Commit and close

```bash
git add <specific files>
git commit -m "feat: <summary> (td-<id>)"
td review <id>
```

Use `td review`, not `td close` — self-closing is blocked.

## Rules

- **ONE task per iteration.** Complete it, verify it, commit it, mark it done, then exit.
- **Tests are mandatory.** Every change needs tests. `go test ./...` must pass.
- **Quality gates before every commit.** `go build ./...` and `go test ./...` must pass.
- **Plugin height constraint is sacred.** Never exceed the allocated height.
- **No footers in plugins.** Use `Commands()` method only.
- **Adapter interface compliance.** All adapters must implement the full interface.
- **If stuck, log and skip.** `td log <id> "Blocked: <reason>"` then `td block <id>`.
- **Commit messages reference td.** Format: `feat|fix|chore: <summary> (td-<id>)`
