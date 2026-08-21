# Plan: Agent View Inspection CLI (`sidecar view`)

**Research snapshot:** 2026-08-15  
**Status:** proposed  
**Scope:** `internal/cli`, `internal/uirequest`, `internal/plugins/workspace`, `internal/plugins/filebrowser`, `internal/plugins/tdmonitor`, `internal/plugins/gitstatus`, `internal/plugins/notes`, `internal/overview`, `internal/docview`, `internal/issueview`, `docs/reference/cli.md`, `AGENTS.md`.

---

## 1. Decision First

Two decisions, one unified plan.

### 1. A new non-interactive inspection command lets an agent query what files, issues, diffs, and split panes the user currently has open and focused.

```bash
sidecar view                       # Overview of user's active viewport & open panes
sidecar view file                  # Quick path:line of the active document
sidecar view issue                 # Quick td-xxxxxx and title of the focused issue
sidecar view diff                  # Quick spec and path of the active diff
sidecar view panes                 # List all split panes and their active tabs
sidecar view --json                # Structured JSON payload with complete inspection data
```

Like `sidecar shell name`, `sidecar shell rename`, and `sidecar open`, `sidecar view` acts on the **Sidecar shell containing the calling process** by default. When an agent is running inside a project shell:
- It immediately inspects the split pane layout attached to that shell (e.g. document panes, issue panes, diff panes opened beside the terminal).
- It also reports the **global user focus** (whether the user is currently looking at this shell, another shell, or a different plugin like File Browser or td Monitor).

When called from an external terminal or script, it accepts explicit target selectors:
```bash
sidecar view --shell "API planning" # Inspect a specific registered shell
sidecar view --project sidecar      # Inspect a project's active workspace surface
sidecar view --json                 # Complete machine-readable snapshot
```

### 2. The CLI registry and `sidecar --agents` surface the inspection command alongside `sidecar open`.

`sidecar open` and `sidecar view` form a natural complementary pair:
- `sidecar open <target>` — **Put content in front of the user** (write/reveal action).
- `sidecar view [target]` — **Inspect what the user is looking at** (read/query action).

`sidecar --agents` output updates to:

```text
Sidecar commands for agents. sidecar open works from any context; shell name and rename act on the shell you are running in.

  sidecar open <path>[:line] | td-xxxxxx | --diff [spec]  Put a file, a td issue, or a git diff in front of the user
  sidecar view [file | issue | diff | panes]              Inspect what the user is looking at (active file, issue, diff, or pane)
  sidecar shell name                                      Read the name this shell shows the user
  sidecar shell rename "<short context>"                  Keep the shell's name describing the work you are doing now

Add --json to any of them for a structured result.
Run "sidecar help <command>" for options and exit codes.
```

---

## 2. Agent Journey & Real-World User Scenarios

### Scenario A: User references a document or section they are reading
- **User prompt:** *"In the markdown file I'm reading, can you summarize the architectural changes in section 2?"*
- **Agent action:** Runs `sidecar view file --json` (or `sidecar view --json`).
- **Inspection result:**
  ```json
  {
    "path": "docs/plans/active/workspace-windowing-system.md",
    "absPath": "/Users/marcus/code/sidecar/docs/plans/active/workspace-windowing-system.md",
    "line": 142,
    "visibleRange": { "top": 142, "bottom": 182 },
    "totalLines": 520,
    "rendered": true,
    "heading": "## Windowing & Split Tree",
    "previewText": "The pane tree represents recursive horizontal/vertical splits..."
  }
  ```
- **Agent outcome:** The agent reads the file at lines 142–182 and answers the question accurately without asking *"Which file?"* or *"What section?"*.

### Scenario B: User references an issue or task on screen
- **User prompt:** *"Let's write a test plan for the issue on screen right now."*
- **Agent action:** Runs `sidecar view issue --json`.
- **Inspection result:**
  ```json
  {
    "id": "td-8492c5",
    "title": "Fix HistorySnapshot.Output race with appendLoadedHistory",
    "status": "in_progress",
    "priority": "P1",
    "type": "bug",
    "parentId": "td-0818ef",
    "selectedSubId": "td-3615a6"
  }
  ```
- **Agent outcome:** The agent immediately formulates a plan for `td-8492c5` and references its parent/child relationships.

### Scenario C: User asks about a git diff or commit
- **User prompt:** *"In the diff I have open, why did we add that mutex?"*
- **Agent action:** Runs `sidecar view diff --json`.
- **Inspection result:**
  ```json
  {
    "spec": "HEAD~1",
    "path": "internal/plugins/workspace/doc_panes.go",
    "scope": "commit",
    "mode": "side-by-side",
    "scrollRow": 28
  }
  ```
- **Agent outcome:** The agent analyzes the exact commit and file diff the user is viewing.

### Scenario D: User asks what is open beside their terminal
- **User prompt:** *"What panes do I have open right now?"*
- **Agent action:** Runs `sidecar view panes`.
- **Human output:**
  ```text
  Shell: "shell rename implementation" (focused)
  Pane 1 [terminal]: shell:sidecar-sh-sidecar-3
  Pane 2 [doc] (focused): docs/plans/active/workspace-sidebar-drag-reorder.md:42 (lines 42-82 of 306)
  Pane 3 [issue]: td-8492c5 "Fix HistorySnapshot.Output race with appendLoadedHistory" (P1 · in_progress)
  ```

### Scenario E: User is working in another plugin (e.g. File Browser)
- **User prompt:** *"Look at the file I'm previewing in the file browser."*
- **Agent action:** Runs `sidecar view --json`.
- **Inspection result:**
  ```json
  {
    "activePlugin": "file-browser",
    "isCallerFocused": false,
    "fileBrowser": {
      "activePane": "preview",
      "selectedFile": "internal/cli/registry.go",
      "previewFile": "internal/cli/registry.go",
      "line": 88,
      "visibleRange": { "top": 88, "bottom": 128 },
      "totalLines": 254
    }
  }
  ```

---

## 3. What Exists Now & Required Seams

| Component | Current Seam & State | Required Enhancement |
| :--- | :--- | :--- |
| **`internal/uirequest`** | Only supports `ActionOpen` (`"open"`). Instances ack with basic status (`opened`, `queued`, `declined`). | Add `ActionView` (`"view"`). Enhance `Ack` to carry a complete `ViewSnapshot` payload. |
| **`internal/cli`** | Defines `RootCommand()`, `openCmd`, `shellCmd`, `agentsCmd`, `helpCmd`. | Add `viewCmd` (`sidecar view [file\|issue\|diff\|panes]`) with full flag parsing, human formatting, and `--json` serialization. |
| **`internal/plugins/workspace`** | Tracks `p.paneRoot`, `p.docs` (`docPane`), `p.issues` (`issuePane`), `p.diffs` (`diffPane`), `p.paneFocus`, `p.activePane`. | Implement `InspectView()` on `workspace.Plugin` to serialize active and caller surface pane trees and viewport states into `SurfaceSnapshot`. |
| **`internal/docview`** | Tracks `m.path`, `m.scroll`, `m.rendered`, `m.wrap`, `m.targetLine`, `m.display().starts`, `m.renderedLines`. | Add `ViewportSnapshot()` method returning 1-based source line, visible range `[top, bottom]`, total lines, rendered flag, and visible heading/text. |
| **`internal/issueview`** | Tracks `m.issueID`, `m.title`, `m.data` (`*Data`), `m.selectedID`, `m.scroll`. | Add `Snapshot()` method returning issue metadata, focused sub-item, and scroll state. |
| **`internal/plugins/filebrowser`** | Tracks `p.selectedFile`, `p.previewFile`, `p.docView`, `p.tabs`, `p.activePane`, `p.editor`. | Implement `InspectView()` on `filebrowser.Plugin` to report tree cursor, previewed file, docview viewport, and inline editor state. |
| **`internal/plugins/tdmonitor`** | Embeds `monitor.Model` with selected issue, lane, and view mode. | Implement `InspectView()` on `tdmonitor.Plugin` to extract currently focused issue in the td kanban/list. |
| **`internal/overview`** | Tracks global workspace list, active tab (`"workspaces"`, `"agents"`, `"tasks"`), preview panes. | Implement `InspectView()` on `overview.Model` to report focused workspace card and overview doc/issue/diff tabs. |
| **`internal/state`** | Persists `PaneLayoutJSON`, `FileBrowserState`, `NotesState`, `ActivePlugin`. | Add an offline helper `InspectPersistedState(stateDir, projectKey, shellName)` used as a fallback when no live instance is running or when `--persisted` is passed. |
| **`AGENTS.md` & `docs/reference/cli.md`** | Documents agent tools and CLI specifications. | Update generated documentation and agent guidelines to include `sidecar view`. |

---

## 4. CLI Command Specification

### Syntax

```text
Usage: sidecar view [options] [target]

Inspect what the user currently has open and focused in Sidecar.

Targets:
  file            Show only the active document path and scrolled line
  issue           Show only the active td issue
  diff            Show only the active git diff
  panes           List all open split panes and their tabs

Options:
  --file          Filter output to active document (alias for target "file")
  --issue         Filter output to active td issue (alias for target "issue")
  --diff          Filter output to active diff (alias for target "diff")
  --panes         Filter output to open panes (alias for target "panes")
  --all           Include full details of all open panes and background tabs
  --shell NAME    Target a registered shell by display name or tmux name
  --project NAME  Target a project's Workspaces surface (slug, basename, or path)
  --instance PID  Target a specific running Sidecar process
  --persisted     Read persisted state directly without waiting for a live instance
  --wait DURATION Time to wait for instance response (default 1200ms)
  --json          Write one structured result object to stdout
  -h, --help      Show this help

Exit codes:
  0  success (information found and returned)
  1  not found (e.g. "sidecar view file" when no file is open) or state failure
  2  usage or validation error
  3  no running Sidecar instance found (and --persisted not specified)
```

---

## 5. Output Design

### A. Human-Readable Formats

#### 1. Default `sidecar view` (Document open in split pane)
```text
Shell: "shell rename implementation" (focused)
Focused Pane: Document [Pane 2]
  File: docs/plans/active/workspace-sidebar-drag-reorder.md:42
  Visible: lines 42-82 of 306 (rendered markdown)
  Heading: "## Design > ### A. Pure reorder helpers"
  Preview: "Add small pure functions (new file e.g. internal/plugins/workspace/reorder.go):"

Other Panes:
  Pane 1 [terminal]: shell:sidecar-sh-sidecar-3
  Pane 3 [issue]: td-8492c5 "Fix HistorySnapshot.Output race with appendLoadedHistory" (P1 · in_progress)
```

#### 2. Default `sidecar view` (User focused in another plugin, e.g. File Browser)
```text
Active Plugin: file-browser (focused)
  File: internal/cli/registry.go:88
  Visible: lines 88-128 of 254 (code)
  Mode: preview (tree cursor at internal/cli/registry.go)

Caller Shell: "shell rename implementation" (background)
  Panes: 1 terminal
```

#### 3. Filtered `sidecar view file`
```text
docs/plans/active/workspace-sidebar-drag-reorder.md:42
```
*(If no file is open, prints `no file open` to stderr and exits 1).*

#### 4. Filtered `sidecar view issue`
```text
td-8492c5 "Fix HistorySnapshot.Output race with appendLoadedHistory" (P1 · in_progress)
```
*(If no issue is open, prints `no issue open` to stderr and exits 1).*

#### 5. Filtered `sidecar view diff`
```text
HEAD~1 (internal/plugins/workspace/doc_panes.go, side-by-side, line 28)
```
*(If no diff is open, prints `no diff open` to stderr and exits 1).*

---

### B. Machine-Readable Structured JSON Schema (`--json`)

```json
{
  "status": "ok",
  "live": true,
  "instance": {
    "pid": 48120,
    "host": "macbook-pro.local"
  },
  "project": "sidecar",
  "activePlugin": "workspace",
  "isCallerFocused": true,
  "focusedSurface": "shell:sidecar-sh-sidecar-3",
  "focusedPane": {
    "id": 2,
    "kind": "doc"
  },
  "file": {
    "path": "docs/plans/active/workspace-sidebar-drag-reorder.md",
    "absPath": "/Users/marcus/code/sidecar/docs/plans/active/workspace-sidebar-drag-reorder.md",
    "line": 42,
    "scrollRow": 38,
    "visibleRange": {
      "top": 42,
      "bottom": 82
    },
    "totalLines": 306,
    "rendered": true,
    "wrap": false,
    "heading": "## Design > ### A. Pure reorder helpers",
    "previewText": "Add small pure functions (new file e.g. internal/plugins/workspace/reorder.go):"
  },
  "issue": {
    "id": "td-8492c5",
    "title": "Fix HistorySnapshot.Output race with appendLoadedHistory",
    "status": "in_progress",
    "priority": "P1",
    "type": "bug",
    "parentId": "td-0818ef",
    "selectedSubId": "td-3615a6"
  },
  "diff": null,
  "panes": [
    {
      "id": 1,
      "kind": "terminal",
      "focused": false,
      "surface": "shell:sidecar-sh-sidecar-3"
    },
    {
      "id": 2,
      "kind": "doc",
      "focused": true,
      "activeTab": 0,
      "tabs": [
        {
          "path": "docs/plans/active/workspace-sidebar-drag-reorder.md",
          "absPath": "/Users/marcus/code/sidecar/docs/plans/active/workspace-sidebar-drag-reorder.md",
          "line": 42,
          "visibleRange": { "top": 42, "bottom": 82 },
          "totalLines": 306,
          "rendered": true,
          "wrap": false
        },
        {
          "path": "README.md",
          "absPath": "/Users/marcus/code/sidecar/README.md",
          "line": 1,
          "visibleRange": { "top": 1, "bottom": 40 },
          "totalLines": 180,
          "rendered": true,
          "wrap": false
        }
      ]
    },
    {
      "id": 3,
      "kind": "issue",
      "focused": false,
      "activeTab": 0,
      "tabs": [
        {
          "id": "td-8492c5",
          "title": "Fix HistorySnapshot.Output race with appendLoadedHistory",
          "status": "in_progress",
          "priority": "P1",
          "type": "bug"
        }
      ]
    }
  ]
}
```

---

## 6. Technical Architecture & Component Design

```
+-------------------------------------------------------------------------+
|                              Agent / Caller                             |
|           sidecar view [--json] [file | issue | diff | panes]            |
+------------------------------------+------------------------------------+
                                     |
                          1. Write Request (ActionView)
                                     v
+-------------------------------------------------------------------------+
|                  File-Based Request Bus (internal/uirequest)            |
|       $XDG_STATE_HOME/sidecar/requests/<req-id>-view.json               |
+------------------------------------+------------------------------------+
                                     |
                          2. fsnotify trigger (<50ms)
                                     v
+-------------------------------------------------------------------------+
|                   Running Sidecar TUI Instance (Bubble Tea)             |
|                                                                         |
|  +---------------------+  +--------------------+  +------------------+  |
|  |  Workspace Plugin   |  | FileBrowser Plugin |  | tdMonitor Plugin |  |
|  |  - Pane tree & tabs |  | - Preview viewport |  | - Focused task   |  |
|  |  - DocView model    |  | - Tree cursor      |  | - Kanban lane    |  |
|  |  - IssueView model  |  | - Inline editor    |  |                  |  |
|  |  - DiffView model   |  +--------------------+  +------------------+  |
|  +---------------------+                                                |
|                                                                         |
|  3. Serialize ViewSnapshot across focused surface & active plugins      |
+------------------------------------+------------------------------------+
                                     |
                          4. Write Ack with ViewSnapshot
                                     v
+-------------------------------------------------------------------------+
|       $XDG_STATE_HOME/sidecar/requests/<req-id>-view.acks/<inst>.json   |
+------------------------------------+------------------------------------+
                                     |
                          5. CLI gathers Ack, formats & exits 0
                                     v
+-------------------------------------------------------------------------+
|                            Agent Output Stream                          |
|         stdout: human summary or exact JSON payload (--json)            |
+-------------------------------------------------------------------------+
```

### A. Protocol Types (`internal/uirequest`)

```go
package uirequest

const (
    ActionOpen Action = "open"
    ActionView Action = "view"
)

// ViewTargetFilter specifies what portion of the view to inspect.
type ViewTargetFilter string

const (
    FilterAll   ViewTargetFilter = "all"
    FilterFile  ViewTargetFilter = "file"
    FilterIssue ViewTargetFilter = "issue"
    FilterDiff  ViewTargetFilter = "diff"
    FilterPanes ViewTargetFilter = "panes"
)

// ViewSnapshot is the structured inspection payload returned in Ack.
type ViewSnapshot struct {
    ActivePlugin    string           `json:"activePlugin"`             // "workspace", "file-browser", "td-monitor", etc.
    FocusedSurface  string           `json:"focusedSurface,omitempty"` // "shell:sidecar-sh-sidecar-3", "workspace:main"
    IsCallerFocused bool             `json:"isCallerFocused"`          // true if user is looking at calling shell
    FocusedPane     *PaneRef         `json:"focusedPane,omitempty"`
    File            *DocSnapshot     `json:"file,omitempty"`           // Top-level shortcut to active file
    Issue           *IssueSnapshot   `json:"issue,omitempty"`          // Top-level shortcut to active issue
    Diff            *DiffSnapshot    `json:"diff,omitempty"`           // Top-level shortcut to active diff
    Panes           []PaneSnapshot   `json:"panes,omitempty"`          // All panes in the active split
    CallerPanes     []PaneSnapshot   `json:"callerPanes,omitempty"`    // Panes in caller shell if different from active
    FileBrowser     *FileBrowserSnap `json:"fileBrowser,omitempty"`
    TDMonitor       *TDMonitorSnap   `json:"tdMonitor,omitempty"`
    GitStatus       *GitStatusSnap   `json:"gitStatus,omitempty"`
    Notes           *NotesSnap       `json:"notes,omitempty"`
}

type PaneRef struct {
    ID   int    `json:"id"`
    Kind string `json:"kind"` // "terminal", "doc", "issue", "diff"
}

type PaneSnapshot struct {
    ID        int             `json:"id"`
    Kind      string          `json:"kind"` // "terminal", "doc", "issue", "diff"
    Focused   bool            `json:"focused"`
    Surface   string          `json:"surface,omitempty"`
    ActiveTab int             `json:"activeTab"`
    DocTabs   []DocSnapshot   `json:"docTabs,omitempty"`
    IssueTabs []IssueSnapshot `json:"issueTabs,omitempty"`
    DiffTabs  []DiffSnapshot  `json:"diffTabs,omitempty"`
}

type DocSnapshot struct {
    Path         string    `json:"path"`         // Relative path to project root
    AbsPath      string    `json:"absPath"`      // Absolute file path
    Line         int       `json:"line"`         // 1-based source line (or approximate if rendered markdown)
    ScrollRow    int       `json:"scrollRow"`    // 0-based visual scroll offset
    VisibleRange LineRange `json:"visibleRange"` // {top: 42, bottom: 82}
    TotalLines   int       `json:"totalLines"`
    Rendered     bool      `json:"rendered"`     // True if rendered markdown
    Wrap         bool      `json:"wrap"`
    Heading      string    `json:"heading,omitempty"`     // Current visible section heading
    PreviewText  string    `json:"previewText,omitempty"` // First line of visible text
}

type LineRange struct {
    Top    int `json:"top"`
    Bottom int `json:"bottom"`
}

type IssueSnapshot struct {
    ID            string `json:"id"`       // "td-8492c5"
    Title         string `json:"title"`
    Status        string `json:"status"`   // "in_progress", "needs_review", "todo", "done"
    Priority      string `json:"priority"` // "P0", "P1", "P2", "P3"
    Type          string `json:"type"`     // "task", "bug", "feature", "chore"
    ParentID      string `json:"parentId,omitempty"`
    SelectedSubID string `json:"selectedSubId,omitempty"`
    ScrollRow     int    `json:"scrollRow"`
}

type DiffSnapshot struct {
    Spec      string `json:"spec"` // "HEAD~1", "main..feature", "wt"
    Path      string `json:"path,omitempty"`
    Scope     string `json:"scope"` // "commit", "worktree"
    Mode      string `json:"mode"`  // "unified", "side-by-side"
    ScrollRow int    `json:"scrollRow"`
}

type FileBrowserSnap struct {
    ActivePane   string       `json:"activePane"` // "tree", "preview", "editor"
    SelectedFile string       `json:"selectedFile"`
    PreviewFile  string       `json:"previewFile"`
    Line         int          `json:"line"`
    VisibleRange LineRange    `json:"visibleRange"`
    TotalLines   int          `json:"totalLines"`
    Editor       *EditorSnap  `json:"editor,omitempty"`
}

type EditorSnap struct {
    Path       string `json:"path"`
    CursorLine int    `json:"cursorLine"`
    CursorCol  int    `json:"cursorCol"`
    Modified   bool   `json:"modified"`
}

type TDMonitorSnap struct {
    ViewMode      string         `json:"viewMode"` // "kanban", "list", "details"
    FocusedLane   string         `json:"focusedLane"`
    SelectedIssue *IssueSnapshot `json:"selectedIssue,omitempty"`
}

type GitStatusSnap struct {
    SelectedFile string `json:"selectedFile"`
    Status       string `json:"status"`
    Staged       bool   `json:"staged"`
}

type NotesSnap struct {
    SelectedNoteID string `json:"selectedNoteId"`
    Title          string `json:"title"`
    Editing        bool   `json:"editing"`
}
```

---

### B. Viewport & Line Number Mapping (`internal/docview`)

One subtle technical challenge in terminal TUIs is mapping visual scroll rows to 1-based source lines in documents, especially when markdown is rendered via Glamour.

#### 1. Raw Source / Code Mode (`m.Rendered() == false`)
In raw source mode, `docview.Model` builds a `displayRows` struct where `starts[n-1]` stores the exact visual row where source line `n` begins.
- **Top visible line:** Binary search in `starts` for the largest source line `line` where `starts[line-1] <= m.scroll`. (Exact 1-based source line).
- **Bottom visible line:** Binary search for the source line where `starts[line-1] <= m.scroll + m.height`.
- **Target line:** If a prior jump set `m.targetLine`, that line is preserved as the target.

#### 2. Rendered Glamour Markdown Mode (`m.Rendered() == true`)
When markdown is rendered, Glamour transforms markdown AST into formatted terminal blocks with headings, lists, tables, and blank padding rows. Lines no longer have a strict 1:1 line number match with raw markdown.

To give the agent **100% reliable context**, `docview` provides a **triple-anchor strategy**:
1. **Source Line Estimation:** Calculates proportional line position: $$\text{EstimatedLine} = \max\left(1, \operatorname{round}\left(\frac{\text{scroll}}{\max(1, \text{totalRenderedRows})} \times \text{totalSourceLines}\right)\right)$$
2. **Semantic Heading Anchor (`heading`):** Parses the markdown AST to identify the innermost `#`, `##`, or `###` heading that precedes the estimated line (e.g. `## Design > ### A. Pure reorder helpers`).
3. **Visual Text Snippet (`previewText`):** Extracts the first non-blank line of text currently rendered at row `m.scroll` in the viewport.

This allows the agent to locate the exact section whether indexing by line number, by heading title, or by text search.

---

### C. Workspace Plugin State Inspection (`internal/plugins/workspace`)

The Workspace plugin inspects its pane tree:
1. Identifies the **caller surface** (e.g. `shell:sidecar-sh-sidecar-3`) by matching `req.Origin.TmuxSession` against `p.shells`.
2. Identifies the **active surface** currently drawn on screen (`p.selectedTerminalSurface()`).
3. If the calling shell matches the active surface, `IsCallerFocused = true`.
4. Traverses `p.paneRoot` using `WalkPanes`:
   - For `PaneDoc`: inspects `p.docs[contentID]` and all tabs. Calls `docview.ViewportSnapshot()`.
   - For `PaneIssue`: inspects `p.issues[contentID]` and all tabs. Calls `issueview.Snapshot()`.
   - For `PaneDiff`: inspects `p.diffs[contentID]` and all tabs.
   - For `PaneTerminal`: inspects terminal session name and active status.
5. Populates top-level shortcuts (`File`, `Issue`, `Diff`) pointing to the currently focused pane (or the primary preview pane if terminal has focus).

---

### D. Multi-Plugin & App Shell Inspection

At the application level (`internal/app`):
- When a UI request with `ActionView` arrives:
  - If `p.activePlugin == "workspace"`, delegates to the Workspace plugin.
  - If `p.activePlugin == "file-browser"`, queries `filebrowser.Plugin.InspectView()`.
  - If `p.activePlugin == "td-monitor"`, queries `tdmonitor.Plugin.InspectView()`.
  - If `p.activePlugin == "git-status"`, queries `gitstatus.Plugin.InspectView()`.
  - If `p.activePlugin == "notes"`, queries `notes.Plugin.InspectView()`.
- If running in Global Workspaces (`internal/overview`), queries `overview.Model.InspectView()`.

---

### E. Persisted State Fallback (`--persisted` or Offline Mode)

When Sidecar is not running (e.g. in headless test environments, CI scripts, or after the user quits):
- The CLI automatically reads:
  1. `$XDG_STATE_HOME/sidecar/projects/<project>/shells.json`
  2. `$XDG_STATE_HOME/sidecar/state.json` (`Workspace[workDir].PaneLayouts`, `FileBrowser[workDir]`, `Notes[workDir]`, `ActivePlugin[workDir]`)
- It reconstructs the last known layout, tabs, and scroll offsets.
- The JSON output includes `"live": false, "source": "persisted"`.
- This ensures agent workflows never crash or fail silently when querying offline state.

---

## 7. Implementation Plan

```
Phase 1: Bus & Types       Phase 2: Viewport Models     Phase 3: Plugin Seams      Phase 4: CLI & Docs
+--------------------+     +---------------------+     +--------------------+     +-------------------+
| internal/uirequest | --> | internal/docview    | --> | workspace plugin   | --> | internal/cli/view |
| - ActionView       |     | - ViewportSnapshot  |     | filebrowser plugin |     | registry & agents |
| - ViewSnapshot     |     | internal/issueview  |     | tdmonitor plugin   |     | docs/ref & AGENTS |
| - Ack extensions   |     | - Snapshot()        |     | overview model     |     | end-to-end tests  |
+--------------------+     +---------------------+     +--------------------+     +-------------------+
```

### Phase 1: Request Bus Protocol & Types (`internal/uirequest`)
1. Add `ActionView` constant and filter constants to `internal/uirequest/types.go`.
2. Define `ViewSnapshot`, `SurfaceSnapshot`, `PaneSnapshot`, `DocSnapshot`, `IssueSnapshot`, `DiffSnapshot`, `LineRange`, etc.
3. Update `Ack` to include `Snapshot *ViewSnapshot`.
4. Unit tests in `internal/uirequest/bus_test.go` verifying serialization, ack collection, and TTL handling for `ActionView`.

### Phase 2: Viewport Model Enhancements (`internal/docview`, `internal/issueview`)
1. Implement `ViewportSnapshot()` on `docview.Model`:
   - Calculate exact source line from `starts` when raw.
   - Calculate proportional source line and extract semantic heading/preview text when rendered markdown.
   - Return `DocSnapshot`.
2. Implement `Snapshot()` on `issueview.Model`:
   - Extract `Issue.ID`, `Title`, `Status`, `Priority`, `Type`, `Parent`, and selected sub-item under cursor.
   - Return `IssueSnapshot`.
3. Unit tests in `internal/docview/model_test.go` and `internal/issueview/model_test.go`.

### Phase 3: Plugin & Host Seams
1. Implement `InspectView()` on `workspace.Plugin`:
   - Inspect `p.paneRoot`, `p.docs`, `p.issues`, `p.diffs`, `p.paneFocus`.
   - Handle `ActionView` in `internal/plugins/workspace/ui_requests.go` and write `Ack`.
2. Implement `InspectView()` on `filebrowser.Plugin`:
   - Inspect tree cursor, previewed file, docview snapshot, and inline editor state.
3. Implement `InspectView()` on `tdmonitor.Plugin`:
   - Extract selected issue and focused column from embedded `monitor.Model`.
4. Implement `InspectView()` on `overview.Model`:
   - Extract global overview card and preview tabs.
5. Unit tests for workspace pane tree inspection with single/multiple splits.

### Phase 4: CLI Implementation & Offline Fallback (`internal/cli`)
1. Create `internal/cli/view.go`:
   - Parse sub-targets (`file`, `issue`, `diff`, `panes`) and flags (`--json`, `--shell`, `--project`, `--persisted`, etc.).
   - Send `uirequest.Request` with `ActionView` and await Acks.
   - Fallback to `InspectPersistedState` when offline or `--persisted`.
   - Format human output and JSON output.
2. Register `viewCmd` in `internal/cli/registry.go`:
   - Add `AgentDoc` for `sidecar view`.
   - Add flags, examples, and exit codes.
3. Update `docs/reference/cli.md` and verify with `internal/cli/registry_test.go`.
4. Update `AGENTS.md` to document the new `sidecar view` capability.

---

## 8. Verification & Test Plan

### A. Unit Tests
- **`internal/cli/view_test.go`**:
  - Test argument parsing (`sidecar view`, `sidecar view file`, `sidecar view issue`, `sidecar view --json`).
  - Test exit codes (0 for success, 1 for not found, 2 for syntax error, 3 for no running instance).
  - Test persisted fallback rendering when no instances are running.
- **`internal/docview/model_test.go`**:
  - Test viewport source line calculations for raw files, wrapped lines, and Glamour rendered markdown.
  - Test heading extraction for markdown files at various scroll positions.
- **`internal/plugins/workspace/ui_requests_test.go`**:
  - Test `ActionView` request handling across single terminal, terminal + doc split, and terminal + doc + issue multi-split layouts.

### B. Headless TUI Verification (`scripts/tmux-drive.sh`)
Verify the entire live path without a human at the keyboard:
1. Start headless Sidecar in tmux:
   ```bash
   ./scripts/tmux-drive.sh start 200 50
   ```
2. Split a doc pane showing `docs/plans/active/workspace-windowing-system.md:142`:
   ```bash
   sidecar open docs/plans/active/workspace-windowing-system.md:142
   ```
3. Run `sidecar view --json` from inside the shell and assert:
   - `file.path` == `"docs/plans/active/workspace-windowing-system.md"`
   - `file.line` == `142`
   - `focusedPane.kind` == `"doc"`
4. Switch to File Browser, preview `README.md`, run `sidecar view file` and assert output is `README.md:1`.
5. Stop headless session cleanly:
   ```bash
   ./scripts/tmux-drive.sh stop
   ```
