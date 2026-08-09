# Cross-Project Overview — Vision & Exploration

> **DEPRECATED 2026-08-09:** Superseded by the current
> [Cross-project agent overview plan](../active/cross-project-overview.md). This
> snapshot is retained for historical context; its Git/PR/CI-centered scope and
> proposed project mode no longer describe the intended first implementation.

## The Big Idea

Sidecar currently operates in **single-project mode** — you switch between projects, but you only see one at a time. The vision is three layers:

### Layer 1: Project Overview Dashboard
A new top-level view (maybe `0` or a dedicated key) that shows **all configured projects at a glance**:

```
┌─────────────────────────────────────────────────────────────┐
│  📊 Project Overview                                         │
├─────────────────────────────────────────────────────────────┤
│                                                              │
│  sidecar          td               nightshift    betamax    │
│  ├─ main ✓       ├─ main ✓        ├─ main ✓    ├─ main ✓  │
│  ├─ fix/122 🔄   ├─ feat/sync 🔄  └─ fix/path  └──────    │
│  ├─ feat/hooks   ├─ fix/board                               │
│  └─ 8 open PRs   └─ 6 open PRs    4 open PRs   4 open PRs │
│                                                              │
│  Active worktrees: 7 │ Open PRs: 22 │ Failing CI: 3        │
├─────────────────────────────────────────────────────────────┤
│  Recent activity:                                            │
│  • sidecar#125 — fuzzy search (yashas) — 2h ago             │
│  • td#19 — remove .td-root (yashas) — 3h ago                │
│  • nightshift — Event Taxonomy timed out — 6h ago            │
└─────────────────────────────────────────────────────────────┘
```

Each project card shows:
- Branch/worktree list with status indicators
- Open PR count
- CI status (green/red/yellow)
- Last activity timestamp

Press Enter on a project → switches to it (existing behavior).

### Layer 2: Cross-Project Kanban
A board view showing **worktrees as cards**, grouped by status:

```
┌──────────────┬───────────────┬──────────────┬──────────────┐
│  🆕 New       │  🔄 Active     │  📝 Review    │  ✅ Done      │
├──────────────┼───────────────┼──────────────┼──────────────┤
│              │ sidecar       │ sidecar      │ sidecar      │
│              │  fix/122      │  feat/hooks  │  #112 merged │
│              │  "3 commits"  │  "PR #105"   │  "2d ago"    │
│              │               │              │              │
│              │ td            │ td           │              │
│              │  feat/sync    │  #19 PR open │              │
│              │  "wip"        │              │              │
├──────────────┼───────────────┼──────────────┼──────────────┤
│ nightshift   │ betamax       │              │              │
│  fix/path    │  feat/flaky   │              │              │
│  "blocked"   │  "CI failing" │              │              │
└──────────────┴───────────────┴──────────────┴──────────────┘
```

Status derived from:
- **New**: worktree exists, no commits ahead of main
- **Active**: commits ahead of main, no PR
- **Review**: PR open
- **Done**: PR merged (show recent)

This is essentially a Kanban of your git workflow across all repos.

### Layer 3: AI Integration (Kestrel / OpenClaw)
This is where it gets really interesting. With the cross-project view, an AI agent (via OpenClaw, conversation adapter, or future chat plugin) could:

- **Report**: "You have 7 active worktrees across 4 projects, 3 PRs need review"
- **Navigate**: "Show me the failing CI on sidecar/fix-122" → jumps to that worktree
- **Triage**: "What should I work on next?" → prioritizes by staleness, CI status, PR reviews
- **Move work**: Close stale worktrees, create new ones from issues
- **Context**: Already has conversation history across all projects (via conversation adapter)

The conversation search DB we built (1,977 sessions across Claude Code, Codex, OpenClaw) feeds directly into this — Kestrel already has cross-project context that sidecar could surface.

## Current Architecture (what exists)

```go
// config.go
type ProjectsConfig struct {
    Mode string          `json:"mode"` // "single" for now
    Root string          `json:"root"`
    List []ProjectConfig `json:"list"` // project switcher
}

type ProjectConfig struct {
    Name  string       `json:"name"`
    Path  string       `json:"path"`
    Theme *ThemeConfig `json:"theme,omitempty"`
}
```

- Projects are configured in `sidecar.json` under `projects.list`
- `Mode: "single"` — one project active at a time
- Project switcher: fuzzy-filtered list, switches working directory
- Each plugin (git, td, conversations, workspace) operates on current project only
- Worktree management is per-project in the workspace plugin

## What Needs to Change

### Phase 1: Data Layer
- New `ProjectOverview` struct that aggregates data across all projects:
  ```go
  type ProjectOverview struct {
      Projects []ProjectStatus
  }
  type ProjectStatus struct {
      Config     ProjectConfig
      Worktrees  []WorktreeStatus  // from git
      OpenPRs    int               // from gh CLI or API
      CIStatus   string            // from gh CLI
      LastCommit time.Time
  }
  type WorktreeStatus struct {
      Path       string
      Branch     string
      CommitsAhead int
      PRNumber   int    // 0 if no PR
      PRState    string // open, merged, closed
      CIStatus   string
  }
  ```

### Phase 2: Overview Plugin
- New plugin alongside git-status, td-monitor, conversations, workspace
- Polls all configured projects periodically
- Renders the dashboard and kanban views
- Keyboard: navigate between projects, press Enter to switch

### Phase 3: Cross-Project Mode
- `projects.mode: "overview"` enables the new views
- Project switcher enhanced with status indicators
- Optional: aggregate td boards across projects

### Phase 4: AI Bridge
- Expose overview data via a local API or file
- Kestrel reads it during sit rep or on-demand
- Future: sidecar chat plugin that talks to OpenClaw

## Relation to Existing Work

| Existing | How it connects |
|----------|----------------|
| Project switcher | Enhanced with status badges, becomes the entry point |
| Workspace plugin | Worktree data feeds into the kanban |
| Git status plugin | Per-project git data aggregated to overview |
| td-monitor | Task counts per project shown in overview |
| Conversations adapter | Cross-project conversation context (already built!) |
| Sit rep script | Overview data could feed into sitrep.py |
| @SilentCommandoGames comment | "30 repos, microservices" — this is exactly what he wants |

## Open Questions

1. How much of this is sidecar-native vs. a separate tool?
2. Should the overview be a new plugin or a new "mode"?
3. GitHub API rate limits for polling PRs/CI across many repos?
4. Should this integrate with td boards for task status, or stay git-focused?
5. How does this relate to td-watch (the admin dashboard)?

## Related
- td-10bd20: YouTube video about cross-project AI management
- td-ee717a: Sidecar adapter for conversation search DB
- Issue #126: Multi-repo project requests
- @SilentCommandoGames: "30 repos, microservice architecture"
