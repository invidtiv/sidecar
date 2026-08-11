# Privacy

Sidecar is a local-first terminal application. This document describes what data it accesses, what network requests it makes, and what it writes to disk.

## Local Data Access

### Git repository

Runs `git` CLI commands (status, diff, log, show, branch, worktree, stash, rev-parse, rev-list, fetch, tag, blame, check-ignore) in the current project directory. Read-only except when you explicitly stage, commit, push, merge, fetch, or create worktrees.

### AI agent sessions (read-only)

Reads conversation history from local agent data directories to display in the Conversations plugin:

- **Amp** — `~/.local/share/amp/threads/` (or `$AMP_DATA_HOME`) — JSONL thread files
- **Claude Code** — `~/.claude/projects/` and `~/.config/claude/projects/` (JSONL session files), `~/.claude/stats-cache.json` (token usage stats)
- **Codex** — `~/.codex/sessions/` (JSONL)
- **Cursor** — `~/.cursor/chats/` (SQLite per-workspace, read via `modernc.org/sqlite`)
- **Gemini CLI** — `~/.gemini/tmp/` and `~/.gemini/` (JSON session files)
- **Kiro** — `~/.kiro/data.sqlite3` and platform-specific fallbacks (`~/Library/Application Support/kiro-cli/`, `$XDG_DATA_HOME/kiro-cli/`, legacy `~/.amazonq/`)
- **OpenCode** — `~/Library/Application Support/opencode/storage/` (macOS), `$XDG_DATA_HOME/opencode/storage/` (Linux)
- **Pi** — per-project session directories (JSONL, read with incremental parsing)
- **Warp** — `~/Library/Group Containers/2BBY89MBSN.dev.warp/...` (macOS), `$XDG_STATE_HOME/warp-terminal/warp.sqlite` (Linux), `%LOCALAPPDATA%\warp\Warp\data\warp.sqlite` (Windows) — read via `go-sqlite3`

Parsed data includes session metadata (IDs, names, timestamps, duration), messages (text, tool calls, thinking blocks), token counts, model names, and estimated costs. These files are **read-only**. Sidecar never writes to agent data directories.

### File browser

The File Browser plugin reads local files in the project directory for preview and navigation. It reads file contents for syntax-highlighted previews, queries git status and blame information, and watches the filesystem for changes via `fsnotify`. The inline editor can write to files when you explicitly edit them.

### Notes

The Notes plugin reads and writes to a SQLite database at `.todos/issues.db` (shared with [td](https://github.com/marcus/td) via `.td-root` links). Data stored: note ID, title, content, timestamps, pinned/archived/deleted flags. Uses WAL mode for concurrent access.

### TD tasks

If [td](https://github.com/marcus/td) is installed, sidecar runs `td` CLI commands (`td version`, `td search`) and reads the `.todos/issues.db` SQLite database (shared across worktrees via `.td-root`).

### Config and state (read/write)

Sidecar reads and writes its own files under `~/.config/sidecar/`:

| File | Purpose |
|------|---------|
| `config.json` | User configuration (projects, plugin settings, theme, keymaps, feature flags) |
| `state.json` | Persistent UI state (diff modes, pane widths, active plugin, scroll positions, per-project state) |
| `version_cache.json` | Cached sidecar version check result (3-hour TTL) |
| `td_version_cache.json` | Cached td version check result (3-hour TTL) |
| `debug.log` | Debug log output (only when `--debug` flag is used; append-only, 0644 permissions) |

### Project-level dotfiles (read/write)

In workspace directories, sidecar may create:

- `.sidecar/config.json` — per-project configuration (prompts, theme overrides)
- `.sidecar/shells.json` — shell display names and metadata
- `.sidecar-task`, `.sidecar-agent`, `.sidecar-agent-start`, `.sidecar-pr`, `.sidecar-base` — workspace state files
- `.sidecar-start.sh` — temporary agent launcher script
- `.sidecar-rename-tmp` — temporary file for rename operations
- `.td-root` — links worktrees to a shared td database root
- `.worktree-env` — environment variable overrides for worktree isolation (read on worktree creation; format: `KEY=VALUE` pairs)

These are added to `.gitignore` automatically.

### Tmux sessions

The Workspaces plugin creates and controls tmux sessions to run agents and shells. It sends commands via `tmux send-keys`, captures terminal output via `tmux capture-pane` (capped at `tmuxCaptureMaxBytes`, default 2 MB), reads the tmux prefix key via `tmux show-options -g prefix`, and manages session lifecycle.

### Clipboard

Sidecar writes to the system clipboard (via `atotto/clipboard`) for user-initiated copy operations: yanking commit hashes, file paths, session details, resume commands, and note content. It reads from the clipboard for paste operations in interactive/shell mode and the inline editor.

### Filesystem watchers

Sidecar uses `fsnotify` to watch for changes in git repositories, project files, and agent session directories. Watched paths include the `.git` directory, the project file tree, and adapter-specific session directories. These watchers trigger UI refreshes — no data is sent anywhere.

### Environment variables

Sidecar reads:

- `HOME` — base path for all config and data directories
- `EDITOR`, `VISUAL` — to open files in your editor
- `SIDECAR_PPROF` — profiling server port (development only)
- `SIDECAR_WORKSPACE_DEFAULT_AGENT_TYPE`, `SIDECAR_DEFAULT_AGENT_TYPE` — override workspace default agent type at startup
- `XDG_DATA_HOME`, `XDG_CONFIG_HOME`, `XDG_STATE_HOME` — standard directories for locating agent data on Linux. `XDG_STATE_HOME` also relocates sidecar's own state directory; `XDG_CONFIG_HOME` is read for agent data only and does **not** relocate sidecar's `config.json` or `state.json`, which are `$HOME`-based (use the `-config` flag)
- `AMP_DATA_HOME` — Amp-specific data directory
- `APPDATA`, `LOCALAPPDATA` — Windows data directories
- `TMUX` — unset on startup so sidecar's own tmux commands are not treated as coming from an attached client. This is detach semantics, not test isolation: the sessions sidecar creates still land on whichever server `TMUX_TMPDIR` resolves to
- `TMUX_TMPDIR` — read by tmux itself; the only variable that moves sidecar-created tmux sessions to a private server
- `SIDECAR_ISOLATED_STATE` — set to `1` by test and proof harnesses; makes sidecar refuse to read or write anything under the real `~/.local/state/sidecar` or `~/.config/sidecar`
- `SIDECAR_DIAG_PATHS` — set to `1` to print the resolved state, config, tmux socket and manifest paths at startup
- `GOBIN`, `GOPATH`, `GOFLAGS`, `NODE_OPTIONS`, `NODE_PATH`, `PYTHONPATH`, `VIRTUAL_ENV` — read and selectively cleared in worktree environments to prevent build conflicts
- `TD_SESSION_ID` — task tracker session context

Sidecar does **not** read or require API keys or tokens.

### Session export

The Conversations plugin can export a session to a markdown file in the current working directory, or copy it to the clipboard. This is user-initiated only.

### Executable detection

On startup and when needed, sidecar checks `PATH` for: `tmux`, `brew`, `git`, `td`, `go`, and — only when the `tasks_plugin` feature is enabled — `tasks`, `tasks-tui`, `tasks-api`. It reads `os.Executable()` and resolves each product's executable (following symlinks) to determine how that specific binary was installed. Determining Homebrew ownership runs `brew --cellar <formula>` / `brew --prefix <formula>`; this reads local Homebrew metadata and makes no network request.

## Network Requests

Sidecar makes outbound HTTP requests in the following cases:

### Version checks (automatic, cached)

On startup, sidecar checks for updates by fetching the latest release tag from:

- `api.github.com/repos/marcus/sidecar/releases/latest`
- `api.github.com/repos/marcus/td/releases/latest`
- `api.github.com/repos/marcus/tasks/releases/latest` — only when the `tasks_plugin` feature is effectively enabled *and* the standalone `tasks` command is installed. With the feature disabled, no Tasks check, process, or network request happens at all.

These requests use a 5-second timeout, send no authentication, and are cached locally for 3 hours in a separate file per product (`version_cache.json`, `td_version_cache.json`, `tasks_version_cache.json`). After the first successful check, no network call occurs until the cache expires. Development builds (untagged, `devel`, or any version output without a release number — including a local Tasks development selector) skip these checks entirely.

### Changelog fetch (user-initiated)

When you open the changelog from the update modal, sidecar fetches `raw.githubusercontent.com/marcus/sidecar/main/CHANGELOG.md` with a 10-second timeout.

### Self-update (user-initiated)

When you confirm an update from the update modal, sidecar updates the products named in that modal — Sidecar, td, and (when enabled and installed) the standalone Tasks suite — one at a time, using each product's own detected install method:

- Homebrew: one `brew update` for the batch, then `brew upgrade marcus/tap/sidecar`, `brew upgrade marcus/tap/td`, and/or `brew upgrade marcus/tap/tasks`.
- `go install`: `go install github.com/marcus/sidecar/cmd/sidecar@<version>`, `go install github.com/marcus/td@<version>`, and/or `go install github.com/marcus/tasks/cmd/{tasks,tasks-tui,tasks-api}@<version>`.

These commands make their own network requests to Homebrew or the Go module proxy. After each product, sidecar runs the updated binaries with their version flag to verify the exact installed version.

Sidecar never installs a product you do not already have, and never overwrites an executable it does not recognise as Homebrew- or `go install`-managed (for example an active local development build). Those are reported with a manual command instead. No update runs until you choose **Update**.

### External CLI tools

The Workspaces plugin runs `gh` CLI commands (e.g., `gh pr list`, `gh pr create`) using your existing GitHub CLI authentication. These run only in response to explicit user actions such as fetching a PR or creating one from the merge workflow.

Git push, pull, and fetch operations use the local `git` CLI with your configured remotes and credentials.

### Browser URLs

Sidecar opens URLs in your system browser (`open` on macOS, `xdg-open` on Linux, `cmd /c start` on Windows) when you choose to view a commit, PR, or file location on GitHub, or reveal a file in your system file manager. No data is sent by sidecar itself — your browser handles the request.

## What Sidecar Does NOT Do

- No telemetry, analytics, or usage tracking
- No crash reporting
- No data transmitted to any server other than the GitHub API calls listed above
- No account or login required
- No cookies, local storage, or browser fingerprinting
- No reading of SSH keys, credentials, or secrets
- No access to contacts, email, camera, microphone, or system processes

## Opting Out of Network Requests

Version checks are skipped automatically for development builds (untagged or `devel` versions). There is currently no config flag to disable version checks on release builds. If you need fully offline operation, run sidecar with network access blocked at the OS or firewall level.

## pprof Profiling Server

When the `SIDECAR_PPROF` environment variable is set, sidecar starts a Go pprof HTTP server on `localhost` (default port 6060). This is localhost-only and intended for development profiling. It is never started unless you explicitly set the variable.
