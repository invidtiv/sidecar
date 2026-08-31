---
sidebar_position: 5
title: Tasks Plugin
---

# Tasks

A full personal task manager built into Sidecar for planning work, triaging incoming requests, organizing projects, and maintaining daily agendas across all your repositories.

## Overview

While [TD](./td) is purpose-built as structured memory and verification for AI coding agents, **Tasks** is built for you. It gives you a complete Getting Things Done (GTD) task tracking system with projects, tags, priorities, due dates, and daily agendas—available as both a dedicated project tab and a global cross-project surface.

```
┌──────────────────────────────┬──────────────────────────────────────────┐
│ Tasks                        │ Task Detail                              │
│ [Outline] [Inbox] [Next] ... │                                          │
│                              │ Refactor layout engine fit calculations  │
│ [HIGH PRIORITY]              │ Project: sidecar · P1 · Due: Tomorrow    │
│ • Write documentation site   │                                          │
│ • Fix layout engine bounds   │ Ensure 2x2 grid fits within terminal     │
│                              │ floors and caps.                         │
│ [PROJECT: sidecar]           │                                          │
│ • Add notification center    │ Subtasks (2/3):                          │
│ • Test remote host mutations │ [x] Calculate column widths              │
│                              │ [x] Fit row heights                      │
│ 4 · 2 closed                 │ [ ] Handle resize debounce               │
└──────────────────────────────┴──────────────────────────────────────────┘
```

## Key Capabilities

- **Unified personal task hub**: Organize personal todos, feature backlogs, and multi-project tasks in one place.
- **GTD Next-action triage**: Press `N` on any open task to immediately mark it as a next action.
- **Multiple focused views**: Outline, Inbox, Next, Agenda, and Projects views for every stage of planning.
- **Show & hide closed work**: Press `C` to reveal or collapse completed (`DONE`) and cancelled tasks in place.
- **Sub-second synchronization**: Subscribes to live event streams so changes made in CLI, TUI, or other tabs appear within a second.
- **Timezone-aware delegation**: Delegation stamps and handoff timestamps render formatted in your local timezone.
- **Convert to agent issues**: Seamlessly promote personal tasks to tracked TD agent issues when ready for autonomous coding.

## Quick Start

1. Press `0` to open global Tasks, or step through tabs with `[` / `]` to open the project Tasks tab.
2. Press `a` to create a new task.
3. Enter a title, project, priority, and optional due date.
4. Use `j`/`k` to navigate and `N` to mark high-priority items as Next actions.
5. Press `space` or `x` to mark a task as complete.

## Core Views

The Tasks interface includes dedicated tabs for every stage of your workflow:

### 1. Outline

The hierarchical master view of your tasks, organized by project and parent-child task relationships.
- Press `C` to toggle the visibility of completed and cancelled tasks. When hidden, section badges show counts such as `4 · 2 closed`.
- Reordering tasks with keys anchors directly onto visible siblings, preventing unintended structural rewrites.

### 2. Inbox

The landing ground for new thoughts, incoming review requests, and untriaged tasks.
- Tasks are grouped under project headings so triage passes stay within a consistent visual theme.
- Triage quickly using keyboard shortcuts to assign projects, set priorities, or promote to Next actions.

### 3. Next (GTD Next Actions)

Your active execution list. Shows all tasks marked as immediate next actions alongside scheduled items.
- Press `N` from any list or detail view to mark an open task as a Next action.
- If the Next tab is empty, it clearly displays how many dated items are waiting on the Agenda and how to mark tasks for action.

### 4. Agenda

A date-oriented view showing tasks scheduled for today, upcoming deadlines, and overdue commitments.

### 5. Projects

A high-level overview grouping all tasks by their designated project and milestone. Pressing `C` nests closed tasks cleanly under their respective project hierarchy rather than pruning them.

## Keyboard Reference

### Navigation & Views

| Key | Action |
|-----|--------|
| `j`, `k`, `↓`, `↑` | Move through the task list |
| `h`, `l`, `←`, `→` | Switch between tabs (Outline, Inbox, Next, Agenda, Projects) |
| `g` / `G` | Jump to the first / last task |
| `ctrl+d` / `ctrl+u` | Page down / Page up |
| `enter` | Open or focus task detail |
| `C` | Toggle display of closed/completed tasks |
| `N` | Mark selected open task as GTD Next action |
| `/` | Filter and search tasks |
| `esc` | Clear search filter or close detail view |

### Task Management

| Key | Action |
|-----|--------|
| `a` | Create a new task |
| `e` | Edit the selected task |
| `space`, `x` | Toggle task completion (Open ↔ Done) |
| `D` | Delete task (with confirmation) |
| `p` | Change task priority (P0, P1, P2, P3) |
| `d` | Set due date |
| `m` | Move task to a different project |
| `t` | Add or edit tags |

## Embedded Tab vs. Standalone Tools

Sidecar includes the **Tasks tab** embedded directly within the application binary. No external dependencies are required to use the tab in Sidecar.

For CLI and standalone terminal workflows outside Sidecar, the companion `tasks` tools are available:

- `tasks` — Fast CLI for querying, creating, and completing tasks from scripts or shell prompts.
- `tasks-tui` — Fullscreen dedicated terminal user interface.
- `tasks-api` — Local API service for synchronizing task databases across machines.

### Installing Standalone Tools

To install or update the standalone CLI tools:

```bash
# macOS / Linux via Homebrew
brew install marcus/tap/tasks

# From source (requires Go 1.21+)
go install github.com/marcus/tasks/cmd/tasks@latest
```

Inside Sidecar, open Configuration (`,`) → **Panels & Integrations** to check installation status or trigger a one-click install.
