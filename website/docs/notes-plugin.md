---
sidebar_position: 4
title: Notes Plugin
---

# Notes Plugin

A persistent project scratchpad and notes organizer with theme-matched Markdown rendering, clickable content links, and seamless editor integration—right in your terminal.

## Overview

The Notes plugin provides a distraction-free environment to jot down thoughts, draft technical specifications, record debugging notes, and organize project tasks. Notes are stored per-project in `.todos/notes/` (or `.todos/` alongside your TD task database), so your notes stay version-controlled or local to your workspace.

```
┌─────────────────────────┬──────────────────────────────────────────┐
│ Notes (3)               │ Note Preview / Editor                    │
│                         │                                          │
│ • Architecture Plan     │ # Architecture Plan                      │
│ • Release Checklist     │                                          │
│ • Debugging Auth Flow   │ See td-847b0c for implementation steps.  │
│                         │ Check internal/tty/tty.go:212 for the    │
│ Archive (12)            │ terminal buffer allocation logic.        │
└─────────────────────────┴──────────────────────────────────────────┘
```

## Key Capabilities

- **Persistent per-project notes**: Notes live with your project and persist across restarts.
- **Theme-derived Markdown**: Headings, lists, code blocks, and blockquotes automatically adopt your active Sidecar color theme.
- **Interactive content links**: References like `path/to/file.go:42`, `td-abc123`, git commits (`abc1234`), and URLs are automatically recognized and clickable, opening adjacent preview panes without losing your place.
- **Built-in & external editing**: Edit directly in the terminal using standard navigation keys or launch your configured `$EDITOR` in place.
- **Native mouse support**: Full mouse text selection, click-to-place cursor, and smooth scrollbar dragging.
- **Convert thoughts to tasks**: Turn bullet points and notes directly into structured TD tasks or git worktree specifications.

## Quick Start

1. Switch to the Notes tab by pressing `4` or stepping with `[` / `]`.
2. Press `a` to create a new note and type a title.
3. Type your note content in the editor pane.
4. Press `esc` to finish editing and view the rendered Markdown.

## Keyboard Navigation & Actions

### Note List (Left Pane)

| Key | Action |
|-----|--------|
| `j`, `↓` | Move down the list |
| `k`, `↑` | Move up the list |
| `g` | Jump to the top note |
| `G` | Jump to the bottom note |
| `a` | Create a new note |
| `D` | Delete the selected note (with confirmation) |
| `A` | Toggle archive section / view archived notes |
| `/` | Search and filter notes by title or content |
| `enter`, `l` | Focus the note preview / editor pane |
| `r` | Refresh note index from disk |

### Note Editor & Preview (Right Pane)

| Key | Action |
|-----|--------|
| `e`, `enter` | Enter edit mode in the built-in editor |
| `E` | Open the current note in your external `$EDITOR` (e.g. Neovim, Helix, Vim) |
| `m` | Toggle between raw text editing and rich Markdown rendering |
| `j`, `k`, `↓`, `↑` | Scroll through rendered notes |
| `ctrl+d` / `ctrl+u` | Page down / Page up |
| `h`, `esc` | Return focus to the note list |
| `y` | Copy note contents to the system clipboard |
| `Y` | Copy note file path to the system clipboard |

## Theme-Matched Markdown Rendering

When viewing rendered Markdown (`m`), Sidecar uses a specialized terminal Markdown renderer tuned to your active color palette:

- **Headings & Accents**: Styled with the theme's primary and accent colors.
- **Syntax Highlighting**: Fenced code blocks (` ```go `, ` ```typescript `, etc.) use Chroma with the same syntax highlighting theme as the File Browser and Git diffs.
- **Bullet Lists & Outlines**: Clean indentation and bullet markers that preserve hierarchy.
- **Blockquotes & Tables**: Clean 1px borders and formatted columns.

Switching color themes with `#` instantly repaints on-screen notes without discarding your scroll position or active selection.

## Interactive Content Links

Notes act as a springboard into your codebase and task workflows. The preview engine automatically detects and styles live references:

- **File Paths & Line Numbers**: Mentions such as `internal/plugins/notes/plugin.go:85` underline when they point to existing files. Clicking or pressing Enter on them opens a Document pane immediately to the right.
- **TD Issue IDs**: Mentions like `td-fd674e` or `td-8ec2cc` open the corresponding TD task card in a side pane, showing task description, current status, and acceptance criteria.
- **Git Commits & Hashes**: 7+ character hexadecimal hashes open a side-by-side or unified diff pane showing the commit.
- **Hyperlinks & Resource Keys**: External URLs and configured resource keys (such as Jira or Linear tickets) open their respective browser URLs or resource cards.

## Built-In Editor & `$EDITOR` Integration

Sidecar offers two flexible ways to edit notes:

1. **Built-in Terminal Editor**: Fast, lightweight editor supporting standard cursor movements, text editing, multi-line wrapping, and Mac/Emacs navigation shortcuts (`ctrl+a`, `ctrl+e`, `ctrl+k`, `ctrl+u`). Clicking anywhere in the text places the cursor at that exact position.
2. **External Editor (`$EDITOR`)**: Pressing `E` suspends the preview and opens the note in your environment's `$EDITOR` (such as `nvim`, `vim`, `helix`, or `code --wait`). When you save and exit your editor, Sidecar instantly reloads and re-renders the note.

You can configure your default editing preference in `~/.config/sidecar/config.json`:

```json
{
  "plugins": {
    "notes": {
      "editor": "builtin"
    }
  }
}
```

Set `"editor": "external"` to automatically launch `$EDITOR` when pressing `e` or Enter on a note.
