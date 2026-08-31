---
sidebar_position: 3
title: Notifications & Toasts
---

# Notifications & Toasts

A rich notification system featuring non-intrusive corner toasts, a dedicated Notification Centre panel, and actionable jumps into files, tasks, diffs, and sessions.

## Overview

Sidecar replaces ephemeral status-line flashes with a robust, persistent notification system. When an AI agent completes a long-running task, requires your approval, or encounters an error, Sidecar delivers a notification toast in the top-right corner and records it in your Notification Centre.

```
┌────────────────────────────────────────────────────────────┐
│ [Workspaces] [Git] [Files] [Notes] ...   🔔 3 ⚙ [10:45 AM] │
├──────────────────────────────────────────┬─────────────────┤
│                                          │ NOTIFICATIONS   │
│ (Active Workspace Content)               │                 │
│                                          │ 📌 AGENTS (2)   │
│                                          │ [1] claude      │
│                                          │ Ready for review│
│                                          │ fix-auth · 2m   │
│                                          │                 │
│                                          │ [2] codex       │
│                                          │ Tests passing   │
│                                          │ api-v2 · 5m     │
│                                          │                 │
│                                          │ 📋 TASKS (1)    │
│                                          │ [3] td-8ec2cc   │
│                                          │ Review required │
└──────────────────────────────────────────┴─────────────────┘
```

## Key Capabilities

- **Corner Toast Stacks**: New notifications appear as bordered toasts in the upper-right corner, stacking neatly and collapsing by source.
- **Notification Centre (`N` / `alt+n`)**: A full-height slide-over panel on the right showing your complete notification history grouped by source.
- **Actionable Jumps**: Notifications can carry numbered targets (files, line numbers, TD tasks, commits, sessions) that jump directly to the relevant view when pressed.
- **Agent Lifecycle Hooks**: Automatic alerts when background agents transition to **Waiting** (blocked on approval) or **Done**.
- **CLI Integration (`sidecar notify`)**: Scripts and agents can post rich notifications, list unread items, and dismiss alerts.

## Viewing & Dismissing Notifications

### Corner Toasts

- **Dismiss**: Press `d` or click the toast to dismiss the top notification.
- **Open Centre**: Press `N` or `alt+n` to expand the full Notification Centre.

### Notification Centre Panel (`N` / `alt+n`)

Press `N` or `alt+n` from any surface (or click the bell icon in the header bar) to open the Notification Centre:

| Key | Action |
|-----|--------|
| `j`, `k`, `↓`, `↑` | Navigate notifications |
| `1` - `9` | Activate the numbered jump action on the selected notification |
| `d` | Dismiss the selected notification |
| `D` | Dismiss all notifications in the current section |
| `esc`, `q`, `N` | Close the Notification Centre |

## Actionable Targets & Jumps

Notifications can link directly to context targets across your projects using the `--target` parameter:

| Target Format | Example | Action |
|---------------|---------|--------|
| `file:path[:line]` | `file:src/auth.go:42` | Opens the file in a Document pane at line 42 |
| `issue:id` | `issue:td-8ec2cc` | Opens the TD issue card with acceptance criteria |
| `task:id` | `task:tsk-123` | Focuses the task in the Tasks tab |
| `commit:hash` | `commit:abc1234` | Opens the commit diff in a Diff pane |
| `session:name` | `session:sidecar-sh-main` | Attaches or focuses the target terminal shell |
| `url:address` | `url:https://github.com/...` | Opens the URL in your default browser |

Target paths can also include cross-project qualifiers (e.g. `file:src/main.go@backend`) to jump into another configured repository.

## Notifications CLI (`sidecar notify`)

Agents and background scripts can post and manage notifications using the `sidecar notify` CLI suite.

### 1. `sidecar notify post`

Post a notification to the running Sidecar instance (or queue it for next start):

```bash
# Post a simple notification
sidecar notify post "Build completed successfully"

# Post with an actionable target jump
sidecar notify post "Auth tests failing" \
  --source "test-runner" \
  --target "file:internal/auth/auth_test.go:88"

# Post with an issue target and high urgency
sidecar notify post "Review required for PR #42" \
  --source "agent" \
  --target "issue:td-8ec2cc" \
  --urgency "high"
```

### 2. `sidecar notify list`

List current active and unread notifications:

```bash
sidecar notify list
sidecar notify list --json
```

### 3. `sidecar notify dismiss`

Dismiss notifications by ID or clear all:

```bash
# Dismiss a single notification
sidecar notify dismiss --id notif_01h8a

# Dismiss all notifications
sidecar notify dismiss --all
```

### 4. `sidecar notify test`

Send a sample notification to verify your sound and visual toast configuration:

```bash
sidecar notify test
```
