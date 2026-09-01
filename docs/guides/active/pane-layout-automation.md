# Pane Layout Automation & Composition Guide

Sidecar workspaces support flexible multi-pane arrangements (splits and tabs) beside the active terminal. Agents and scripts can programmatically inspect, compose, and reposition these panes using non-interactive CLI commands.

All layout commands act directly on the surface showing the active shell (or, with `--sessions`, the global Sessions browser) and never queue: a request whose destination is off-screen declines with an actionable reason.

## Core Commands

| Command | Action | Key Flags |
|---|---|---|
| `sidecar layout get` | Read the active grid layout, open panes, targets, and bounds | `--json`, `--sessions` |
| `sidecar layout apply` | Add panes or apply a full replacement spec atomically | `--pane '<json>'`, `--spec '<json>'`, `--json` |
| `sidecar layout move` | Reposition an open pane directionally or to a specific cell | `CELL --to CELL\|COLUMN\|left\|right\|up\|down`, `--focused` |
| `sidecar open` | Convenience command to open or focus a single target in a split | `<target>`, `--split auto\|right\|below`, `--at COL.ROW` |

---

## 1. Inspecting Layout: `layout get`

Always **read before you write**. Inspecting the active layout reveals current grid geometry, pane kinds, tabs, and layout limits.

```bash
# Read structured layout info
sidecar layout get --json
```

Example JSON output structure:
```json
{
  "grid": {
    "columns": [
      {
        "panes": [
          { "kind": "primary", "cell": "1.1", "session": "sidecar-sh-sidecar-1" }
        ]
      },
      {
        "panes": [
          { "kind": "file", "cell": "2.1", "targets": ["README.md"] },
          { "kind": "issue", "cell": "2.2", "targets": ["td-abc123"] }
        ]
      }
    ]
  },
  "caps": { "maxColumns": 4, "maxRowsPerColumn": 4 }
}
```

---

## 2. Composing Panes: `layout apply`

`layout apply` validates and fit-tests the entire change before altering the screen. If any requirement fails (such as grid caps or min dimensions), nothing changes and the refusal describes the first violation.

### Additive Composition (`--pane`)

Use `--pane` to add one or more panes without closing existing panes:

```bash
# Open a file at a specific line in column 2, row 1
sidecar layout apply --pane '{"kind":"file","targets":["internal/app/model.go:42"],"at":"2.1"}'

# Open multiple panes in one atomic call (tabs joined on first target's pane)
sidecar layout apply \
  --pane '{"kind":"file","targets":["cmd/sidecar/main.go","internal/cli/cli.go"]}' \
  --pane '{"kind":"shell","run":"go test -v ./...","name":"test runner"}'
```

Supported pane kinds for `--pane`:
* `{"kind":"file","targets":["path:line", ...]}` — Relative file paths with optional line numbers.
* `{"kind":"issue","targets":["td-xxxxxx"]}` — `td` task issues.
* `{"kind":"note","targets":["nt-xxxxxx"]}` — `td` notes.
* `{"kind":"diff","targets":["HEAD~1..HEAD"]}` — Git diffs (empty targets = working tree).
* `{"kind":"resource","provider":"jira-work","targets":["PROJ-123"]}` — External provider locators.
* `{"kind":"shell","run":"...","name":"..."}` — Terminal split running a command.

### Full Layout Replacement (`--spec`)

Use `--spec` when you want to define the exact multi-column grid from scratch.

> [!IMPORTANT]
> A valid `--spec` MUST include exactly one `{"kind":"primary"}` pane and MUST carry over any existing live terminal splits (`{"kind":"shell","session":"<tmux-session>"}`). Sidecar will refuse specs that attempt to destroy active shell sessions.

```bash
# Example: Left column primary terminal, right column top file preview over bottom diff
sidecar layout apply --spec '{
  "columns": [
    {
      "panes": [
        { "kind": "primary" }
      ]
    },
    {
      "panes": [
        { "kind": "file", "targets": ["internal/ui/styles.go:20"] },
        { "kind": "diff", "targets": [] }
      ]
    }
  ]
}'
```

You can also pipe a spec directly via stdin with `--spec -`:
```bash
cat layout.json | sidecar layout apply --spec -
```

---

## 3. Repositioning Panes: `layout move`

Move open panes without recalculating or rebuilding the full grid spec.

```bash
# Move the pane at cell 2.1 to cell 1.2 (under the primary terminal)
sidecar layout move 2.1 --to 1.2

# Move the currently focused pane to the right (creates a new column if at edge)
sidecar layout move --focused --to right

# Append pane at 2.1 to the bottom of column 3
sidecar layout move 2.1 --to 3

# Shift pane leftward (can open a new leftmost column)
sidecar layout move 1.1 --to left
```

* Directional moves (`left`, `right`, `up`, `down`) follow the same rules as the reposition modal (`M` or `h/j/k/l`).
* A move that is already in place reports `unchanged` and exits `0`.

---

## 4. Quick Split Helper: `sidecar open`

For single targets, `sidecar open` provides a clean shortcut:

```bash
# Open a file at line 88
sidecar open internal/cli/cli.go:88

# Open a git working tree diff in a split below
sidecar open --diff --split below

# Open a td issue
sidecar open td-b922d8

# Open a note
sidecar open sidecar://note/nt-4jdj4e

# Open an external ticket
sidecar open --provider jira-work PROJ-101
```

---

## 5. Common Agent Workflow Recipes

### Recipe A: Code Review Workspace
Set up a side-by-side review environment showing the diff and relevant test file beside the working agent.

```bash
sidecar layout apply \
  --pane '{"kind":"diff","targets":[],"at":"2.1"}' \
  --pane '{"kind":"file","targets":["internal/app/model_test.go"],"at":"2.2"}'
```

### Recipe B: Task Investigation Setup
Display the task details and referenced implementation file beside the terminal.

```bash
sidecar layout apply \
  --pane '{"kind":"issue","targets":["td-1987bf"],"at":"2.1"}' \
  --pane '{"kind":"file","targets":["internal/shellstate/session.go:120"],"at":"2.2"}'
```

---

## 6. Exit Codes and Error Handling

| Exit Code | Meaning | Remediation |
|---|---|---|
| `0` | Success (applied, moved, or unchanged) | Proceed with workflow. |
| `1` | State / tmux failure | Check tmux server health. |
| `2` | Usage or validation error | Check JSON syntax and flag formats. |
| `3` | No running instance | Ensure Sidecar TUI is running if targeting active workspace. |
| `4` | Refusal / fit-test failure | Screen is too small, target cell out of bounds, or shell off-screen. Read the error message. |
| `5` | Unknown target project or shell | Verify project slug and session names. |
