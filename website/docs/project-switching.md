---
sidebar_position: 2
title: Project & Worktree Switching
---

# Project & Worktree Switching

Switch back and forth between repositories and git worktrees instantly with full context preservation.

## Overview

Sidecar provides two dedicated switching interfaces to keep your workflow fluid across codebases:

1. **Project Switcher (`@`)**: Jump between different repositories configured on your system.
2. **Worktree Switcher (`W`)**: Switch between parallel git worktrees within the current repository.

Both switchers preserve your exact context per project—including active plugin tab, cursor positions, expanded directory folders, composed pane layouts, and scroll offsets.

```
┌────────────────────────────────────────────────────────────┐
│ SWITCH PROJECT                                             │
│                                                            │
│ > sidecar           ~/code/sidecar         (current)       │
│   frontend          ~/code/frontend                        │
│   backend           ~/work/backend                         │
│   api-gateway       ~/work/gateway                         │
│                                                            │
│ Switch: Enter · Navigate: j/k · Filter: Type · Cancel: Esc │
└────────────────────────────────────────────────────────────┘
```

## Quick Start

### Project Switching (`@`)

1. Register your projects in `~/.config/sidecar/config.json`:
   ```json
   {
     "projects": {
       "list": [
         {"name": "sidecar", "path": "~/code/sidecar"},
         {"name": "frontend", "path": "~/code/frontend"},
         {"name": "backend", "path": "~/work/backend"}
       ]
     }
   }
   ```
2. Press `@` from anywhere in Sidecar (or click the project name in the header bar).
3. Type to filter or use `j`/`k` to navigate.
4. Press `enter` to switch.

### Worktree Switching (`W`)

1. In any repository with git worktrees, press `W` (or click the worktree badge in the header bar).
2. Select the worktree branch you want to switch into and press `enter`.
3. Sidecar restores your last-active worktree whenever you return to that repository.

## What Gets Preserved

When switching projects or worktrees, Sidecar saves and restores your full environment context:

| State | Description |
|-------|-------------|
| **Active Plugin Tab** | Restores whichever tab (Workspaces, Git, Files, Notes, Tasks) you were using |
| **Composed Panes & Layouts** | Restores open Document, Diff, Issue, and Resource panes |
| **Cursor Position** | Restores selected items in file tree, git status, and task lists |
| **Directory Expansion** | Restores expanded/collapsed folder states in File Browser |
| **Sidebar & Split Widths** | Restores custom pane split ratios and divider positions |
| **Last Active Worktree** | Restores your last active worktree branch when switching between repositories |

## Project vs. Worktree Switching

| Feature | Project Switching (`@`) | Worktree Switching (`W`) |
|---------|------------------------|-------------------------|
| **Primary Use** | Switch between different repositories | Switch between branches within the same repository |
| **Setup** | Defined in `~/.config/sidecar/config.json` | Auto-discovered from git worktrees |
| **Scope** | Any configured directory | Git worktrees within the active repo |

## Configuration Options

### Project Definitions & Themes

Projects can specify individual theme overrides so each repository is visually distinct:

```json
{
  "projects": {
    "list": [
      {
        "name": "sidecar",
        "path": "~/code/sidecar",
        "theme": "sidecar-modern"
      },
      {
        "name": "backend",
        "path": "~/work/backend",
        "theme": "monokai"
      }
    ]
  }
}
```

### Uninitialized Project Discovery

Sidecar can resolve and switch to any project listed in `config.projects.list` even before that project has ever been opened or initialized on disk.
