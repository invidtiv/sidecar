---
sidebar_position: 1
title: Workspaces Plugin
---

# Workspaces Plugin

Parallel development environments with integrated AI agents. Create isolated workspaces, launch coding agents (Claude Code, Codex, Gemini CLI, Cursor, OpenCode, Pi, Aider), stream real-time output, tile multi-pane layouts, and merge when ready—all from one terminal interface.

![Workspaces Plugin](/img/sidecar-workspaces.png)

## Overview

The Workspaces plugin turns git worktrees into managed development environments. Each workspace gets its own directory, branch, and optional AI agent. You can work on multiple features in parallel, watch agent output in real time, review diffs, compose side-by-side document and task panes, and merge via PR—all without leaving Sidecar.

**Key capabilities:**

- **Create Workspaces & Shells**: Open isolated branches or terminal shells with custom names, base branches, and agent configurations.
- **Card-Based Sidebar**: Clean visual cards with category glyphs (`📌 PINNED`, `● LIVE`, `○ IDLE`, `◆ NEEDS ATTENTION`, `● WORKING`) and project theme hues.
- **Multi-Pane Windowing**: Tile shells, code files, diffs, TD issues, notes, and external resources in custom 2×2 grid layouts.
- **Visual Pane Repositioning (`M` / `⊞`)**: Rearrange panes using vim direction keys (`h/j/k/l`), zoom (`z`), and atomic commit (`enter`).
- **Universal Pane Switcher (`n`)**: Open any pane kind directly from whatever pane you are focused on.
- **Real-Time Output Streaming**: Sub-500ms adaptive terminal captures with synchronized redraws (DEC mode 2026) and truecolor support.
- **Durable Session Persistence**: Shells survive tmux crashes and reboots; resume exact agent conversations with `sidecar session restore`.
- **Review & Merge Workflow**: Integrated diff reviews, commit staging, GitHub PR creation, and automatic branch cleanup.

## Prerequisites

**Required:**
- Git 2.25+ (for worktree support)
- Tmux 3.0+ (for background session management)

**Supported Agents (Installed as needed):**
- Claude Code (`claude`)
- Codex (`codex`)
- Cursor CLI (`cursor-agent`)
- Gemini CLI (`gemini`)
- OpenCode (`opencode`)
- Pi Agent (`pi`)
- Aider (`aider`)

Sidecar automatically detects installed CLIs and populates the agent selection picker accordingly.

## Quick Start

1. Press `n` to open the **Create Workspace** modal.
2. Select **Worktree**, enter a name (e.g. `feature-auth`), select your base branch (`main`), choose your agent, and optionally enable Auto-approve.
3. Press Enter. Sidecar creates the worktree and starts the agent in an isolated tmux session.
4. Watch output stream live in the preview pane or press `enter` to type into the shell.
5. When finished, press `m` to review the diff, push to remote, and open a GitHub PR.

## Workspace Layout & Sidebar

The Workspaces interface features an adaptable two-pane or multi-pane layout:

- **Left Pane (Sidebar)**: List or Kanban board of active workspaces and shells.
- **Right Pane (Content Deck)**: Live terminal output, code documents, diffs, task cards, notes, and resource cards.
- **Draggable Dividers**: Adjust column and row widths with the mouse or keyboard (`+` / `-`).

### Card-Based Sidebar

The sidebar presents workspaces as distinct cards separated by blank lines and section dividers:
- **Category Glyphs**: `📌 PINNED`, `● LIVE`, `○ IDLE`, `◆ NEEDS ATTENTION`, `● WORKING`.
- **Section Headers**: Flush-left uppercase headers with project-stable theme colors for instant recognition.
- **Selection Fill**: Full-width active row fill that softens to outline when focus moves into the terminal or document panes.

### View Modes

Press `v` to toggle between **List View** and **Kanban View**:

- **List View (Default)**: High-density vertical list showing workspace name, branch, agent kind, task link, relative time, and live status.
- **Kanban View**: Multi-column board categorizing workspaces by state (Active, Waiting, Done, Paused). Navigate columns with `h`/`l` and cards with `j`/`k`.

## Multi-Pane Windowing & Layouts

Workspaces supports a unified multi-pane tiling system. You can open multiple panes alongside your active terminal:

- **Document Panes**: Syntax-highlighted code or rendered Markdown with in-file search (`/`) and inline editing (`e`).
- **Diff Panes**: Side-by-side or unified diff previews for commits, branches, and working tree changes.
- **Issue Panes**: TD task cards with acceptance criteria and execution logs.
- **Note Panes**: Project notes and scratchpads.
- **Resource Panes**: External tickets from Jira, Linear, or custom providers.

### Universal Pane Switcher (`n`)

Press `n` from any focused pane or sidebar to open the Pane Switcher. Choose any pane kind and select your target via fuzzy search.

### Visual Repositioning Modal (`M` / `⊞`)

Press `M` from any pane (or click the `⊞` icon on the pane header) to rearrange your layout:
- `h`, `j`, `k`, `l` move the draft layout.
- `z` toggles zoom.
- `enter` commits the new layout atomically.
- `esc` cancels.
- Active inline text edits are safely guarded with a save/discard confirmation dialog.

See [Panes & Layouts](./layout-and-panes) for full details.

## Shell & Session Management

In addition to git worktrees, Workspaces manages standalone terminal shells for manual tasks:

- **Instant Shell (`ctrl+n`)**: Instantly creates a new managed shell in the current project directory.
- **Create Form (`n` → Kind: Shell)**: Create a named shell or seed an agent with auto-approve.
- **Rename Shell (`R`)**: Rename the shell display name (persisted across restarts).
- **Send Command (`sidecar shell send`)**: Send commands into background shells programmatically.
- **Delete Shell (`D`)**: Close the tmux session and move the record to a recoverable tombstone.

### Cold Session Restoration

Shell records survive tmux crashes and reboots via `shells.json` (v3). Use the session restore engine to recover lost sessions:

- `sidecar session status`: Inspect the read-only restore plan.
- `sidecar session restore --agents --yes`: Recreate shells and resume exact agent conversations.
- `sidecar session policy <target> --inherit|--shell|--resume|--never`: Configure per-shell recovery behavior.

See [Session Durability & Recovery](./session-durability) for full details.

## Agent Lifecycle & Integration

Workspaces runs agents inside isolated tmux sessions and monitors their execution in real time:

- **Real-Time Streaming**: Sub-500ms captures with synchronized redraws (DEC mode 2026) and native background color inheritance.
- **Status Detection**: Automatically detects when agents are Working (`●`), Waiting on approval (`◆`), Done (`✓`), or Idle (`○`).
- **Approval Shortcuts**: Press `y` to approve pending prompts, `Y` to approve all, or `N` to reject.
- **Interactive Mode (`enter`)**: Connect directly to the terminal session to type commands or provide feedback.
- **Session-Identity Hooks**: Install official hooks via `sidecar agent integration install <provider>` so agents report their exact conversation IDs.

## Configuration

Configure workspace options in `~/.config/sidecar/config.json` or `.sidecar/config.json`:

```json
{
  "plugins": {
    "workspace": {
      "dirPrefix": true,
      "defaultAgentType": "claude",
      "autoCreateShell": false,
      "agentStart": {
        "claude": "claude --dangerously-skip-permissions",
        "opencode": "opencode --profile fast",
        "*": "claude"
      },
      "setupScript": ".sidecar/setup-workspace.sh",
      "sessionRestore": {
        "resumeAgents": "ask"
      }
    }
  }
}
```

### Configuration Options

| Option | Type | Description |
|--------|------|-------------|
| `dirPrefix` | bool | Prefix workspace directory with repository name |
| `autoCreateShell` | bool | Automatically create a shell when opening Workspaces if none exist |
| `defaultAgentType` | string | Default agent selected in creation modal (`claude`, `codex`, etc.) |
| `agentStart` | object | Startup command prefixes mapped by agent type |
| `setupScript` | string | Script executed in newly created worktree directories |
| `sessionRestore.resumeAgents` | string | Agent resume policy on cold restart (`ask`, `always`, `never`) |

### Per-Worktree Agent Overrides

To customize agent commands for a specific worktree, create `.sidecar-agent-start` in the worktree root with your custom command prefix.

## Merge Workflow

Press `m` on any workspace to launch the multi-step merge workflow:

1. **Diff Review**: Inspect all changes made on the branch.
2. **Strategy Selection**: Choose merge commit, squash merge, or rebase.
3. **PR Creation**: Create a GitHub PR automatically using `gh pr create`.
4. **Branch Cleanup**: Safely delete local and remote branches and remove the worktree directory.

## Keyboard Reference

### Sidebar Context (`workspace-sidebar`)

| Key | Action |
|-----|--------|
| `j`, `k`, `↓`, `↑` | Move down / up |
| `g` / `G` | Jump to top / bottom |
| `h` / `l` | Previous / next column (Kanban) or focus preview |
| `v` | Toggle List / Kanban view |
| `n` | Open Create Workspace / Pane Switcher |
| `ctrl+n` | Create Shell immediately |
| `P` | Fetch remote GitHub PR as a workspace |
| `D` | Delete workspace / Delete shell |
| `R` | Rename shell display name |
| `m` | Launch merge & PR workflow |
| `T` | Link / unlink TD task |
| `s` / `S` | Start / Stop agent |
| `y` / `Y` / `N` | Approve / Approve All / Reject pending agent prompt |
| `enter` | Enter interactive terminal mode |
| `\` | Toggle sidebar visibility |

### Document Context (`workspace-doc`)

| Key | Action |
|-----|--------|
| `j`, `k`, `↓`, `↑` | Scroll down / up |
| `ctrl+d` / `ctrl+u` | Page down / Page up |
| `ctrl+p` | Find file by name |
| `f` | Search project with ripgrep |
| `/` | In-file search with `n`/`N` navigation |
| `e` | Edit file inline |
| `E` | Open file in external `$EDITOR` |
| `m` | Toggle rendered / raw Markdown |
| `{` / `}` | Previous / next file tab |
| `x` | Close active tab |
| `M` | Open visual reposition modal |
| `q`, `esc` | Hide pane |
