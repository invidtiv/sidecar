---
sidebar_position: 2
title: Git Plugin
---

# Git Plugin

A terminal-native git interface with live diff preview, syntax highlighting, intelligent auto-refresh, and adjacent content panes. Watch your agent's changes as they happen—no context switching required.

![Git Status](/img/sidecar-git.png)

## Overview

Traditional git workflows force you to juggle multiple commands: `git status` → `git diff` → `git add` → `git commit`. When AI agents are modifying multiple files in the background, you need continuous visibility into what is changing in real time.

The Git Status plugin provides:

- **Live Diff Preview**: Split-pane interface updates diffs automatically as you navigate files.
- **Syntax Highlighting**: Language-aware coloring for clean, readable code review across 100+ languages.
- **Adjacent Content Panes**: Click any file path, commit hash, or task ID in a diff to open an adjacent Document or Task pane without leaving your git review.
- **Unified & Side-by-Side Views**: Toggle between traditional unified diffs and comparative side-by-side views (`v`).
- **Interactive Scrollbars**: Grab and drag scrollbars on file lists, commit history, and diff viewports.
- **Smart Staging & Commit**: One-key staging (`s`), folder staging, unstaging (`u`), discard protection (`D`), and commit dialog (`c`).

```
┌──────────────────────────────────────┬──────────────────────────────────────────┐
│ Files & Commits                      │ Diff Preview                             │
│                                      │                                          │
│ • Staged (2)                         │ @@ -85,6 +85,12 @@                       │
│   M internal/git/diff.go             │  func RenderDiff(hunk *Hunk) string {    │
│ • Modified (3)                       │ +    // Support adjacent content panes   │
│   M website/docs/git-plugin.md       │ +    if hunk.HasLink() {                 │
│   ? internal/plugins/notes/plugin.go │ +        return renderLinked(hunk)       │
│                                      │ +    }                                   │
│ Recent Commits                       │      return renderStandard(hunk)         │
│ • abc1234 Add reposition modal       │  }                                       │
└──────────────────────────────────────┴──────────────────────────────────────────┘
```

## Quick Start

1. Switch to Git Status by pressing `2` or clicking the Git tab.
2. Navigate modified files with `j`/`k` or arrow keys. The diff pane updates instantly.
3. Press `s` to stage a file or directory. The cursor automatically advances to the next item.
4. Press `c` to open the commit dialog, enter your message, and commit.
5. Press `P` to push staged commits to your remote.

## Core Concepts

### Two-Pane Layout & Auto-Refresh

- **Left Pane**: Organized tree of staged, modified, and untracked files alongside recent commit history.
- **Right Pane**: Live syntax-highlighted diff preview or commit detail.
- **Auto-Refresh Engine**: Watches the `.git` directory and debounces filesystem events (500ms). Diffs refresh automatically when agents edit code, switch branches, or complete operations.

### File Status Indicators

| Symbol | Status | Meaning |
|--------|--------|---------|
| `M` | Modified | Existing file modified in working tree |
| `A` | Added | New file staged for commit |
| `D` | Deleted | File removed |
| `R` | Renamed | File renamed |
| `?` | Untracked | New file not yet tracked |
| `U` | Unmerged | Merge conflict requiring resolution |

Each file displays `+/-` line count badges for quick impact assessment.

## Staging & File Operations

| Key | Action |
|-----|--------|
| `s` | Stage selected file or directory |
| `u` | Unstage selected file or directory |
| `S` | Stage all modified and untracked files |
| `D` | Discard changes to selected file (with confirmation) |
| `c` | Open commit dialog |
| `b` | Open branch switcher modal |
| `P` | Push committed changes to remote repository |
| `p` | Pull latest changes from remote |
| `z` | Open stash menu (save, pop, apply, drop) |

Staging works on entire folders by selecting the folder header and pressing `s`. After staging, the cursor automatically advances to the next unstaged file to speed up batch reviews.

## Diff Viewing & Content Panes

### View Modes

Press `v` to toggle between:

- **Unified View**: Traditional patch format showing additions and deletions sequentially.
- **Side-by-Side View**: Dual-column comparative view showing before and after versions aligned.

Your preferred diff view mode persists across sessions.

### Adjacent Content Panes

When reviewing diffs, clicking any underlined reference—such as a file path (`src/auth.go:42`), a commit hash (`abc1234`), or a TD task ID (`td-8ec2cc`)—opens an adjacent Document, Diff, or Issue pane immediately to the right.

This allows you to inspect full source files or verify task requirements while keeping your Git Status review active.

### Diff Navigation & Expansion

| Key | Action |
|-----|--------|
| `d` | Expand diff to full-screen review mode |
| `v` | Toggle unified / side-by-side mode |
| `j`, `k`, `↓`, `↑` | Scroll diff vertically |
| `h`, `l`, `←`, `→` | Scroll wide lines horizontally |
| `0` | Reset horizontal scroll offset |
| `ctrl+d` / `ctrl+u` | Page down / Page up |
| `g` / `G` | Jump to top / bottom of diff |
| `esc`, `q` | Exit full-screen diff or return focus to file tree |

## Interactive Commit History

The lower section of the left pane lists recent commits:

- Navigate commit history with `j`/`k`.
- The right pane displays full commit details, author info, timestamp, commit message, and per-file diffs.
- Press `enter` on a commit to open a dedicated commit diff tab.
- Press `d` for full-screen inspection of historical commits.
