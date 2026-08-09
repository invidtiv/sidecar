<!-- td-agent-instructions:start -->
<!-- td-agent-instructions:version=3 -->

## Working with td

td keeps task context durable across sessions. In a new context, run `td usage --new-session -q` to see current work.

Use your judgment about how much tracking a task needs. For substantive work: `td start <id>`, record progress with `td log`, hand off with `td handoff <id>`, then `td review <id>`.

Closing needs a review. Say who did it (default trusted mode; delegated/strict allow only the first):

- independent session: `td approve <id> --reason "..."`
- a sub-agent: `td approve <id> --reviewed-by "<who>"`
- you: `td approve <id> --self-review --reason "..."`

Prefer a reviewer with its own `TD_CONTEXT_ID`; never name one who did not review.

Run `td usage` or `td <command> --help`.

<!-- td-agent-instructions:end -->

## Build & Versioning

```bash
# Build
go build ./...

# Run tests
go test ./...

# Managed install from the canonical main checkout
make install-local

# Deliberately activate the current branch/worktree
make install-worktree

# Inspect the managed link and actual shell resolution
make install-status

# Restore the installed Homebrew release
make use-homebrew

# Tag a release (prefer the one-shot path)
RELEASE_VERSION=v0.1.0 make release
```

See `docs/guides/active/releasing.md` and `.claude/skills/release-sidecar/SKILL.md`.
Version is set via ldflags at build time. Without it, sidecar shows git revision info.
`make install-dev` is a compatibility alias for `make install-local`. Plain
`make install` is an unmanaged `go install` into `GOBIN`; it does not alter
Homebrew links or guarantee which `sidecar` wins PATH precedence.

## Keyboard Shortcut Parity

See .claude/skills/ui-features/SKILL.md

See td-331dbf19 for diff paging implementation.

## Startup Latency

Everything a plugin does in `Init()` — and everything `Start()` does before
returning its `tea.Cmd` — runs before the first frame is painted. Keep that path
free of filesystem walks, database opens, and subprocess spawns; do that work in
a `tea.Cmd` and render a loading state until the result message arrives. This
matters most on machines running an endpoint security agent (e.g. CrowdStrike),
where every file open and process spawn carries a large fixed tax.

To measure:

```bash
SIDECAR_STARTUP_TRACE=stderr sidecar 2> trace.out   # or =1 to log instead
SIDECAR_STARTUP_TRACE_DELAY=10s                     # dump later to catch async work
SIDECAR_DIAG_PATHS=1 sidecar                        # print the state/config/tmux paths resolved
```

The trace lists each phase with its offset from process start and its duration,
ending with the `first ready frame` marker. See `internal/startuptrace`.

To count subprocess spawns, put logging shims for `git`/`tmux`/`td` ahead of the
real binaries on `PATH` — duplicated git invocations are the usual finding.

## Verifying Changes in the Real App

`scripts/tmux-drive.sh` runs sidecar in a headless tmux session, sends it
keystrokes, and captures the screen as text and PNG, so a UI change can be
verified without a human at the keyboard:

```bash
./scripts/tmux-drive.sh paths                       # confirm the run is isolated first
SIDECAR_BIN=$HOME/go/bin/sidecar ./scripts/tmux-drive.sh start 200 50
./scripts/tmux-drive.sh keys 5 && ./scripts/tmux-drive.sh snap workspaces
./scripts/tmux-drive.sh stop
```

**A proof run must isolate BOTH the tmux server and the Sidecar state tree.**
They are independent axes, and isolating only one is how td-8d18de destroyed six
of a live user's shells: a private tmux socket did nothing to stop the run from
rewriting the real `~/.local/state/sidecar/projects/sidecar/shells.json` that the
developer's running Sidecar was watching. `tmux-drive.sh` now does both by
default — run `./scripts/tmux-drive.sh paths` before you trust it and confirm
that nothing resolves under `~/.local/state/sidecar` or `~/.config/sidecar`.
Never launch sidecar for a proof by hand without `XDG_STATE_HOME`, `TMUX_TMPDIR`,
`-config <temp path>` and `SIDECAR_ISOLATED_STATE=1` (that last one makes the
binary refuse to start rather than touch the real tree). Note that
`XDG_CONFIG_HOME` moves nothing — config and `state.json` are `$HOME`-based, so
`-config` is the only lever for them.

Note that the embedded terminal's cursor is a **native** cursor drawn by the host
terminal, so `capture-pane` cannot see it; checking cursor placement needs an
attached viewer client. See `docs/guides/active/headless-testing.md` for that, for the
key-pacing rules, and for the tmux coordinate spaces the terminal code works in.

## Plugin View Rendering

**Critical: Always constrain plugin output height.** The app's header/footer are always visible - plugins must not exceed their allocated height or the header will scroll off-screen.

In `View(width, height int)`:

1. Store dimensions: `p.width, p.height = width, height`
2. Calculate internal layout respecting `height` (e.g., `contentHeight := height - headerLines - footerLines`)
3. Either use `lipgloss.Height(height).Render(content)` to enforce height, or manually limit rendered lines
4. Never rely on the app to truncate - it wraps with Height() but edge cases cause rendering bugs

This bug manifests as "top bar disappears" after state transitions (commits, refreshes, mode switches).

## Footer Hints

**Do NOT render footers in plugin View().** The app renders a unified footer bar using `plugin.Commands()` and keymap bindings. Plugins should:

1. Define commands with short names in `Commands()` method
2. Never render their own footer/hint line - this creates duplicate footers

Keep command names short (1 word preferred) to prevent footer wrapping:

- "Stage" not "Stage file"
- "Diff" not "Show diff"
- "History" not "Show history"

The footer auto-truncates hints that exceed available width.

## Inter-Plugin Communication

Plugins communicate via tea.Msg broadcast - all plugins receive all messages.

**App-level messages** (`internal/app/commands.go`):

- `FocusPluginByIDMsg{PluginID}` - switch focus to a plugin by ID
- `app.FocusPlugin(id)` - helper to create the above

**File browser messages** (`internal/plugins/filebrowser/plugin.go`):

- `NavigateToFileMsg{Path}` - navigate to and preview a file (relative path)

**Usage pattern** (e.g., git → file browser):

```go
func (p *Plugin) openInFileBrowser(path string) tea.Cmd {
    return tea.Batch(
        app.FocusPlugin("file-browser"),
        func() tea.Msg { return filebrowser.NavigateToFileMsg{Path: path} },
    )
}
```

Workspace tmux preview capture cap is configurable via `plugins.workspace.tmuxCaptureMaxBytes` in `~/.config/sidecar/config.json`.
