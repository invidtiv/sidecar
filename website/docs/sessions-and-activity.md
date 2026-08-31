---
sidebar_position: 1
title: Sessions & Activity
---

# Sessions & Activity

Unified mission control for your entire fleet of agents, workspaces, and projects—local and remote—in a single terminal view.

## Overview

As your workflow scales to multiple repositories and background agents, keeping track of what is running becomes a challenge. Sidecar provides two dedicated global surfaces to monitor your fleet:

1. **Sessions Browser (`8`)**: An aggregated list of every workspace, agent shell, and terminal session across all local repositories and connected [Remote Hosts](./remote-hosts).
2. **Activity Board (`9` or `K`)**: A real-time cross-project Kanban board tracking agent lifecycle states across distinct operational lanes.

Both views sit in the global header tier alongside your project tabs and can be accessed at any time.

```
┌────────────────────────────────────────────────────────────────────────────┐
│ [1 Workspaces] [2 Git] [3 Files] [4 Notes] ... | [8 Sessions] [9 Activity] │
├────────────────────────────────────────────────────────────────────────────┤
│ SESSIONS                                │ PREVIEW                          │
│                                         │                                  │
│ 📌 PINNED                               │ $ git status                     │
│   sidecar · main                        │ On branch main                   │
│   ● sidecar-sh-sidecar-1                │ Your branch is up to date.       │
│                                         │                                  │
│ ● LIVE (3)                              │ $ sidecar layout apply --json    │
│   sidecar · fix-auth                    │ {                                │
│   ● claude (Working) · 12m ago          │   "applied": true,               │
│   backend · api-v2                      │   "panes": 3                     │
│   ● codex (Working) · 4m ago            │ }                                │
│   ⇅ book · frontend · redesign          │                                  │
│   ● opencode (Working) · 18m ago        │                                  │
│                                         │                                  │
│ ◆ NEEDS ATTENTION (1)                   │                                  │
│   sidecar · refactor-tui                │                                  │
│   ◆ claude (Blocked on approval)        │                                  │
└─────────────────────────────────────────┴──────────────────────────────────┘
```

## Global Sessions Browser (`8`)

Press `8` from anywhere in Sidecar to open the Sessions browser.

### Visual Card Hierarchy

Sessions presents your workspaces as organized cards with clean visual hierarchy:
- **Status Section Headers**: Headings group sessions into `📌 PINNED`, `● LIVE`, `○ IDLE`, `◆ NEEDS ATTENTION`, and `● WORKING`.
- **Project Color Accent**: Each project's section header and glyph inherit that project's stable theme hue, making it easy to distinguish projects at a glance.
- **Remote Host Indicators**: Sessions running on remote hosts display a `⇅` provenance glyph with a host-derived accent color.

### Live Preview & Instant Attach

- **Zero-Latency Snapshot Previews**: When browsing rows, the right preview pane displays the most recent terminal screen snapshot without establishing extra connections or waking idle sessions.
- **Interactive Attach (`enter`)**: Press `enter` on any row to immediately connect to its live tmux session in control mode. You can type commands, run tests, or interact with the running agent directly.
- **Composed Multi-Pane Views**: Just like project workspaces, you can compose adjacent Document, Diff, Issue, and Resource panes beside any session.

### Persistent Global State

When you close or restart Sidecar, the Sessions browser restores exactly where you left off:
- The selected workspace row is remembered in global `state.json`.
- All composed pane layouts and split configurations beside the session are restored.
- Active leaf focus is preserved.

## Cross-Project Activity Board (`9` / `K`)

Press `9` (or press `K` / click the Sidecar logo) to open the Activity board.

The Activity board organizes all active agent-backed shells across all projects into real-time operational lanes:

| Lane | Description | Indicator |
|------|-------------|-----------|
| **Working** | Agents actively generating code, running tests, or inspecting files | Green glyph (`●`) |
| **Blocked / Needs Attention** | Agents paused and waiting for human approval or input | Yellow glyph (`◆`) |
| **Done** | Agents that have successfully completed their designated task | Blue glyph (`✓`) |
| **Idle** | Shells where an agent process is alive but waiting for a new prompt | Neutral glyph (`○`) |
| **Paused** | Paused or backgrounded workspace sessions | Muted glyph (`◌`) |

### Activity Board Navigation

| Key | Action |
|-----|--------|
| `h`, `l`, `←`, `→` | Move between lanes |
| `j`, `k`, `↓`, `↑` | Move up and down within a lane |
| `enter` | Jump directly to the selected workspace and open its shell |
| `r` | Refresh the board |
| `esc`, `q`, `K` | Close the Activity overlay and return to your previous view |

## Managing Fleet Workspaces from Sessions

You can perform full workspace lifecycle actions directly on any row in the Sessions browser:

| Key | Action | Description |
|-----|--------|-------------|
| `n` | Create Worktree | Opens the unified Worktree creation modal to branch new work in the selected project |
| `ctrl+n` | Create Shell | Instantly creates a new managed shell in the selected project |
| `R` | Rename | Renames the session's display name |
| `D` | Delete | Closes the tmux session and moves the shell record to a recoverable tombstone |
| `O` | Open in Git | Opens the selected worktree in the Git Status plugin |
| `M` | Reposition Pane | Opens the visual layout reposition modal to move composed preview panes |

Actions on remote rows execute transparently on the remote host via secure SSH commands. See [Remote Hosts](./remote-hosts) for full details on remote fleet management.
