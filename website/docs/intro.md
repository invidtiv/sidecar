---
sidebar_position: 1
title: Getting Started
---

# Sidecar

**A terminal dashboard for monitoring AI coding agents.**

Watch your agents work in real-time: see git changes, track tasks, and manage parallel workspaces—all from a split-screen terminal UI that complements your agent workflow.

![Sidecar Git Status](/img/sidecar-git.png)

## The Problem

AI coding agents are powerful but opaque. When Claude Code or Cursor makes changes, you're often waiting for a summary or switching contexts to check git status. Sidecar gives you **continuous visibility** into agent activity without interrupting your flow.

**Key capabilities:**

- **Real-time git monitoring** - Stage files, review diffs, commit changes while your agent works
- **Multi-agent support** - Run Claude Code, Cursor, Gemini CLI, and more in workspaces; optional Conversations tab for session history
- **Parallel development** - Run multiple agents across git worktrees with live output streaming
- **Instant project switching** - Jump between repos with `@`. State, cursor, and preferences restore per-project
- **Task integration** - Connect workspaces to TD tasks for context tracking across sessions
- **Zero context switching** - Everything in your terminal, no editor required

## Quick Install

```bash
curl -fsSL https://raw.githubusercontent.com/marcus/sidecar/main/scripts/setup.sh | bash
```

**Requirements:** macOS, Linux, or WSL.

<details>
<summary>Alternative install methods</summary>

**Homebrew:**
```bash
brew install marcus/tap/sidecar
```

**Binary download:** Grab a pre-built binary from [GitHub Releases](https://github.com/marcus/sidecar/releases).

**From source** (requires Go 1.21+):
```bash
go install github.com/marcus/sidecar/cmd/sidecar@latest
```

</details>

## Quick Start

Run from any project directory:

```bash
sidecar
```

Sidecar auto-detects your git repo and active agent sessions. No configuration needed.

### Recommended Workflow

Split your terminal horizontally: agent on the left, sidecar on the right.

```
┌─────────────────────────────┬─────────────────────┐
│                             │                     │
│   Claude Code / Cursor      │      Sidecar        │
│                             │                     │
│   $ claude                  │   [Git] [Files]     │
│   > fix the auth bug...     │   [TD]  [Workspaces] │
│                             │                     │
└─────────────────────────────┴─────────────────────┘
```

**As the agent works:**

- Watch files appear in Git Status with live diffs
- See tasks progress through workflow stages in TD Monitor
- Browse and edit code yourself in File Browser
- Launch parallel agents in workspaces for multi-branch work

:::tip
You can run two sidecar instances side-by-side to create a dashboard view. For example, keep one on the TD plugin and the other on Git or Workspaces to monitor tasks and code changes simultaneously.
:::

This setup provides full transparency into agent actions without breaking focus.

## Core Plugins

Sidecar's modular architecture provides focused tools for each aspect of your workflow. All plugins auto-refresh and support mouse + keyboard navigation.

### Git Status

**A full-featured git interface with live diff preview and commit management.**

Watch your agent's changes in real-time with syntax-highlighted diffs, stage files with a keypress, and commit without leaving the dashboard. Supports unified and side-by-side diff views, commit history with search, and branch switching.

![Git Status with Diff](/img/sidecar-git.png)

**Essential shortcuts:**

| Key | Action |
|-----|--------|
| `s` | Stage file or folder |
| `u` | Unstage file |
| `d` | View full-screen diff |
| `v` | Toggle unified/side-by-side diff |
| `c` | Commit staged changes |
| `b` | Switch branches |
| `P` | Push to remote |

[Full Git Plugin documentation →](./git-plugin)

### Workspaces

**Run parallel AI agents across git worktrees with real-time output streaming.**

Create isolated branches, launch agents with custom prompts, and watch their progress in a Kanban board. Each workspace streams agent output live, shows diffs, and links to TD tasks for context. Perfect for multi-branch development or testing multiple approaches simultaneously.

**Essential shortcuts:**

| Key | Action |
|-----|--------|
| `n` | Create new workspace |
| `s` | Start agent in workspace |
| `enter` | Attach to running agent |
| `v` | Toggle list/Kanban view |
| `m` | Start merge workflow |
| `t` | Link TD task |

**Supported agents:** Claude Code, Cursor, Gemini CLI, OpenCode, Codex, Aider

[Full Workspaces Plugin documentation →](./workspaces-plugin)

### Conversations (opt-in)

**Browse session history from all your AI agents with search and token tracking.**

Off by default — enable the `conversations_plugin` feature flag (config or `--enable-feature=conversations_plugin`). When disabled, Sidecar does not read agent session stores.

Unified view of sessions across Claude Code, Cursor, Gemini CLI, OpenCode, Codex, Pi, and Warp. Search by message content, expand to see full conversations, and track token usage per session. Useful for reviewing what your agents accomplished or resuming previous work.

![Conversations](/img/sidecar-conversations.png)

**Essential shortcuts:**

| Key | Action |
|-----|--------|
| `/` | Search sessions |
| `enter` | Expand/collapse messages |
| `j/k` | Navigate sessions |

[Full Conversations Plugin documentation →](./conversations-plugin)

### TD Monitor

**Task management for AI agents working across context windows.**

Integration with [TD](https://marcus.github.io/td/), a purpose-built task system that helps agents maintain context across sessions. View the current focused task, track activity logs, and submit reviews—all synchronized with your agent's workflow.

![TD Monitor](/img/sidecar-td.png)

**Essential shortcuts:**

| Key | Action |
|-----|--------|
| `r` | Submit review |
| `enter` | View task details |

[Full TD documentation →](./td)

### File Browser

**Navigate and preview project files with syntax highlighting.**

Collapsible directory tree with live code preview. Browse your codebase while your agent works, open files in your editor, or search by name. Auto-refreshes when files change.

![File Browser](/img/sidecar-files.png)

**Essential shortcuts:**

| Key | Action |
|-----|--------|
| `enter` | Open/close folder |
| `/` | Search files |
| `h/l` | Switch tree/preview focus |

[Full File Browser documentation →](./files-plugin)

## Global Navigation

These shortcuts work across all plugins:

| Key | Action |
|-----|--------|
| `q`, `ctrl+c` | Quit sidecar |
| `[` / `]` | Step through the whole header row (project tabs and Sessions / Activity / Tasks) |
| `1`-`7` | Jump to a project tab by number |
| `8` / `9` / `0` | Jump to Sessions / Activity / Tasks |
| `@` | Open project switcher |
| `W` | Open worktree switcher |
| `#` | Open theme switcher |
| `j/k`, `↓/↑` | Navigate items in lists |
| `ctrl+d/u` | Page down/up |
| `g` / `G` | Jump to top/bottom |
| `?` | Toggle help overlay |
| `r` | Refresh current plugin |
| `!` | Open diagnostics modal |
| `K` | Open/close the cross-project agent Overview |

Each plugin adds its own context-specific shortcuts shown in the footer bar.

### Agent Overview

Press `K` (or click the Sidecar logo) for the cross-project agent board. While it is open it
owns the keyboard, so shortcuts work even if an embedded shell was focused underneath:

| Key | Action |
|-----|--------|
| `h/l`, `←/→` | Move between lanes |
| `j/k`, `↓/↑` | Move within a lane |
| `enter` | Open the selected workspace |
| `r` | Refresh the board |
| `esc`, `q`, `K` | Close the Overview |

Global shortcuts (`@`, `#`, `W`, `?`, `!`, `[` / `]`, `1`-`7`, `8` / `9` / `0`) keep working; switching plugins
closes the Overview. In the Overview, `q` closes the board rather than quitting sidecar.

### Project Switching

Press `@` to switch back and forth between projects instantly. Your context is preserved per-project:

- **State per project** - Cursor position, expanded directories, and sidebar widths
- **Active plugin memory** - Restores whichever plugin you were using
- **Instant switch** - No re-scanning or loading delays

Configure projects in `~/.config/sidecar/config.json`:

```json
{
  "projects": {
    "list": [
      {"name": "frontend", "path": "~/code/frontend"},
      {"name": "backend", "path": "~/code/backend"}
    ]
  }
}
```

### Worktree Switcher

Press `W` to switch between git worktrees within the current repository. This is useful when you're working with multiple worktrees for parallel development.

Sidecar remembers your last active worktree per project. When you switch away and later return, sidecar automatically restores the worktree you were working in—no need to manually navigate back.

Both project and worktree switching preserve your full context, so you can jump between codebases without losing your place.

## Themes

Sidecar ships with built-in themes plus a community theme browser with live previews.

- Press `#` to open the theme switcher
- Press `tab` to toggle between built-in and community themes
- Press `enter` to apply the highlighted theme

Themes cover rendered Markdown too. Headings, links, inline code, quotes, rules,
and tables come from the theme's semantic colors, and fenced code blocks use the
same `syntaxTheme` as file previews and diffs. Documents already on screen
repaint as you preview a theme — no reopening or resizing.

## Configuration

Press `,` or select the gear in the header to open Configuration in the app. It opens on
**Sidecar Setup**, which lists whatever is left to finish — adding a project, installing tmux,
connecting agent instructions — and opens a focused repair for each one. `esc` returns to
exactly where you were.

From a terminal, `sidecar setup` starts Sidecar with that page already open.

Sidecar runs with sensible defaults. Create `~/.config/sidecar/config.json` only if you need customization:

```json
{
  "plugins": {
    "git-status": { "enabled": true, "refreshInterval": "1s" },
    "td-monitor": { "enabled": true, "refreshInterval": "2s" },
    "conversations": { "enabled": true },
    "file-browser": { "enabled": true },
    "workspaces": { "enabled": true }
  },
  "features": {
    "flags": {
      "conversations_plugin": false
    }
  },
  "ui": {
    "showClock": true,
    "nerdFontsEnabled": false,
    "terminalTitle": "{project}{worktree}"
  }
}
```

Set `conversations_plugin` to `true` to show the Conversations tab (off by default).

### UI Options

| Option | Default | Description |
|--------|---------|-------------|
| `showClock` | `true` | Show clock in header bar |
| `nerdFontsEnabled` | `false` | Enable Nerd Font glyphs for enhanced visuals |
| `terminalTitle` | `"{project}{worktree}"` | Template for the terminal window/tab title. Set to `""` to leave the title alone |

### Terminal Title

Sidecar names your terminal window and tab after the project it is showing, so several
sidecars in several tabs are tellable apart at a glance — and the label follows along when
you switch projects or worktrees from inside sidecar.

Template variables:

| Variable | Expands to |
|----------|------------|
| `{project}` | Project name (`sidecar`) |
| `{worktree}` | ` [branch]` when you're in a linked worktree, empty otherwise |
| `{plugin}` | Active tab (`workspaces`, `git`, `files`, …) |
| `{dir}` | Base name of the working directory |

```json
{ "ui": { "terminalTitle": "{project}{worktree} · {plugin}" } }
```

If a template renders to nothing — `{project}` outside a git repository, say — sidecar falls
back to the directory name rather than clearing the title your shell set.

Sidecar sets the icon name and window title together (OSC 0), which covers tab labels in
Ghostty, WezTerm, kitty, Alacritty, Terminal.app and iTerm2. The previous title is restored on
exit in terminals that implement the title stack. Programs that take over the terminal — an
editor, an attached session — set titles of their own; sidecar takes the title back when they
finish, within ten seconds at worst.

**Under tmux**, those sequences set the *pane* title, not the window name. To surface it in
the status line, put `#{pane_title}` in your `window-status-format`, or set
`set -g allow-rename on` and let tmux follow the pane.

### Nerd Fonts

Set `"nerdFontsEnabled": true` if you have a [Nerd Font](https://www.nerdfonts.com/) installed in your terminal. This enables:

- **Pill-shaped tabs** - Rounded edges on header tabs
- **Pill-shaped buttons** - Rounded buttons in sidebars

Popular Nerd Fonts: JetBrains Mono, FiraCode, Hack, Meslo. Without a Nerd Font, leave this `false` or the glyphs will render as boxes.

**Plugin-specific config:** Workspace prompts support project-level overrides via `.sidecar/config.json`. See [Workspaces documentation](./workspaces-plugin#custom-prompts) for details.

## Command-Line Options

```bash
sidecar                      # Run in current directory
sidecar --project /path      # Specify project root explicitly
sidecar --debug              # Enable debug logging to stdout
sidecar --version            # Print version and exit
```

## Updates

Sidecar checks for new versions on startup and shows a notification when updates are available. Press `!` for diagnostics, then `u` to review and confirm.

### What one confirmation updates

Diagnostics lists every product Sidecar knows how to update, with its version and how it was installed:

- **Sidecar** itself
- **td**
- **Tasks** — the standalone `tasks`, `tasks-tui`, and `tasks-api` commands, listed only when the `tasks_plugin` feature is enabled

The preview names each product that will change, from which version to which, and the method Sidecar will use, for example `Sidecar 0.95.0 → 0.96.0 · Homebrew`. Nothing is installed until you choose **Update**. Products are then updated one at a time and each is verified against the exact released version afterward.

Sidecar's embedded Tasks tab and the standalone Tasks commands are different artifacts: updating Sidecar refreshes the embedded tab, updating Tasks refreshes the standalone commands.

### Per-product install methods

Provenance is detected per product, so a Homebrew Sidecar can sit alongside a `go install`ed td. Sidecar only updates an executable it recognises as Homebrew- or `go install`-managed. Anything else — a downloaded binary, or an active local development build — is listed as **manual** with the command to run yourself, and is never overwritten.

### When something goes wrong

If a product fails, earlier successful updates are kept. The result screen shows the outcome per product with a manual command for each failure, and **Retry** runs only the failed products. A restart is requested only when Sidecar itself changed; a td- or Tasks-only update needs no restart.

### If Tasks is enabled but not installed

Sidecar's Tasks tab is embedded in the binary. The standalone `tasks` / `tasks-tui` / `tasks-api` commands are a separate install. Configuration → **Panels & Integrations** offers **Install Tasks**: it shows the exact command, waits for confirmation, then runs Homebrew (`brew install marcus/tap/tasks`) or `go install` if brew is missing. The td tab has the same one-click path when `td` is not on PATH.

Diagnostics still reports `embedded only · standalone not installed` until those commands resolve. If neither Homebrew nor Go is available, copy the command from Panels and install it yourself:

```bash
brew install marcus/tap/tasks
```

**Update methods:**
- **Setup script:** `curl -fsSL https://raw.githubusercontent.com/marcus/sidecar/main/scripts/setup.sh | bash`
- **Homebrew:** `brew upgrade marcus/tap/sidecar`
- **Binary:** Download the latest from [GitHub Releases](https://github.com/marcus/sidecar/releases)

## What's Next?

- **[Git Plugin](./git-plugin)** - Full reference for staging, diffing, and commits
- **[Workspaces Plugin](./workspaces-plugin)** - Parallel agent setup and management
- **[Project Switching](./project-switching)** - Multi-repo workflow configuration
- **[TD Integration](./td)** - Task tracking across sessions
- **[GitHub Repository](https://github.com/marcus/sidecar)** - Source code and issues

**Build from source:**

```bash
git clone https://github.com/marcus/sidecar.git
cd sidecar
make build
make install
```

Requires Go 1.21+. See the [GitHub README](https://github.com/marcus/sidecar#development) for development setup.
