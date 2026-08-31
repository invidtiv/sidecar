---
sidebar_position: 1
title: Panes & Layouts
---

# Panes & Layouts

Sidecar features a unified, multi-pane windowing system that lets you arrange interactive shells, code files, git diffs, tasks, notes, and external resources side-by-side in custom grid layouts.

## Architecture

Every view in Sidecar—Project Workspaces, the global Sessions browser, and auxiliary content decks—shares a single underlying pane tree architecture. Panes are arranged in columns of rows (such as a 2×2 grid) with consistent borders, draggable split dividers, interactive scrollbars, and focus indicators.

```
┌──────────────────────────────┬──────────────────────────────┐
│ [Shell 1] [Shell 2]          │ internal/panelayout/tree.go  │
│                              │                              │
│ $ sidecar layout get --json  │ func (t *Tree) Fit() error { │
│ {                            │     // Calculate column and  │
│   "grid": "2x2",             │     // row geometry bounds.  │
│   "panes": 4                 │ }                            │
│ }                            │                              │
├──────────────────────────────┼──────────────────────────────┤
│ td-fd674e: Card layout       │ Diff: HEAD~1 (2 files)       │
│                              │                              │
│ Priority: P1 · Status: DONE  │ @@ -42,6 +42,9 @@            │
│ The workspace sidebar reads  │ + // Synchronized redraw     │
│ as cards instead of text.    │                              │
└──────────────────────────────┴──────────────────────────────┘
```

## Supported Pane Kinds

Sidecar supports eight distinct pane kinds that can be combined in any layout:

| Kind | Description | Source / Target |
|------|-------------|-----------------|
| **Shell** | Interactive terminal session | Local or remote tmux session |
| **Worktree** | Dedicated git worktree session | Git branch / worktree directory |
| **Terminal split** | Auxiliary interactive shell split | Workspace-scoped tmux session |
| **Document** | Syntax-highlighted code or Markdown file | File path (e.g. `src/main.go:42`) |
| **Diff** | Syntax-highlighted git diff | Commit, ref, or range (e.g. `HEAD~1`) |
| **Issue** | Structured task card with acceptance criteria and logs | TD issue ID (e.g. `td-8ec2cc`) |
| **Note** | Project scratchpad note | Note ID or title |
| **Resource** | External card from ticket integrations | Resource locator (e.g. `jira:PROJ-123`) |

## Opening Panes with the Pane Switcher (`n`)

Press `n` from any focused pane or sidebar to open the universal **Pane Switcher**.

- **Kind Selection**: Choose any pane kind using the arrow keys or number shortcuts.
- **Target Pickers**:
  - **Files**: Interactive fuzzy file finder with instant preview.
  - **Diffs**: Recent commits, branches, and staged/working tree changes.
  - **Issues**: In-progress and recently modified TD tasks.
  - **Notes**: Recent project notes.
  - **Resources**: Configured resource provider instances (Jira, Linear, etc.).
- **Placement Indicator**: Shows where the new pane will be placed (next column, split below, or new tab).

## Visual Repositioning Modal (`M` / `⊞`)

Press `M` from any pane—or click the `⊞` icon on the pane header (left of the `×` close button)—to open the visual layout reposition modal:

```
┌────────────────────────────────────────────────────────────┐
│ Reposition Pane: [1.2] Diff: HEAD~1                        │
│                                                            │
│ Move: h/j/k/l · Zoom: z · Commit: Enter · Cancel: Esc      │
└────────────────────────────────────────────────────────────┘
```

- **Vim Navigation (`h`, `j`, `k`, `l`)**: Move the draft layout left, down, up, or right. The pane is lifted out and grafted back into the target column or row.
- **Zoom (`z`)**: Temporarily maximize the pane to inspect full contents.
- **Atomic Commit (`enter`)**: Applies the entire layout sequence atomically without losing running terminals, scrollback, or cursor state.
- **Safety Guard**: If you have an unsaved inline file edit active, opening the reposition modal prompts you to save or discard before rearranging panes.

## Grid Auto-Placement & Rules

When opening panes without explicit coordinates, Sidecar applies a balanced auto-placement algorithm:
1. **Emptiest Column**: Places new panes in the column with the fewest active rows.
2. **2×2 Grid Formation**: Automatically forms a clean 2×2 grid once four panes are open (two columns of two rows).
3. **Cap & Floor Enforcement**: Layouts are capped at a maximum of 4 columns and 4 rows per column, with a maximum of 2 active live terminal leaves (`LiveLeafCap = 2`) to ensure optimal performance.
4. **Dividers & Hit Regions**: All column and row dividers can be grabbed and dragged with the mouse to adjust sizing.

## Layout CLI (`sidecar layout`)

Agents and scripts can inspect and manipulate the pane layout non-interactively using the `sidecar layout` CLI suite.

### 1. `sidecar layout get`

Inspect the current layout grid, cell geometry, caps, and open panes:

```bash
sidecar layout get --json
```

**Output:**
```json
{
  "columns": 2,
  "rows": 2,
  "panes": [
    {"cell": "1.1", "kind": "shell", "target": "sidecar-sh-main"},
    {"cell": "1.2", "kind": "issue", "target": "td-8ec2cc"},
    {"cell": "2.1", "kind": "doc", "target": "internal/app/app.go"},
    {"cell": "2.2", "kind": "diff", "target": "HEAD~1"}
  ],
  "focused": "1.1"
}
```

Target the global Sessions browser by adding `--sessions`:
```bash
sidecar layout get --sessions --json
```

### 2. `sidecar layout apply`

Compose panes additively or replace the entire layout in one atomic operation:

```bash
# Add a single pane
sidecar layout apply --pane doc:internal/app/app.go

# Replace the entire layout with a specification
sidecar layout apply --spec "shell:sidecar-sh-main,doc:src/main.go;issue:td-123,diff:HEAD"
```

Apply operations are all-or-nothing: Sidecar validates every descriptor against terminal floors and caps, ensuring live sessions are preserved before committing.

### 3. `sidecar layout move`

Reposition an existing open pane by cell or direction:

```bash
# Move pane from cell 2.1 to cell 1.2
sidecar layout move 2.1 --to 1.2

# Append pane to column 3
sidecar layout move 2.1 --to 3

# Move focused pane using direction rules
sidecar layout move --focused --to right
```

### 4. `sidecar open --at`

Place a specific target at an exact grid cell:

```bash
sidecar open internal/tty/tty.go:212 --at 2.1
sidecar open td-8ec2cc --at 1.2
```
