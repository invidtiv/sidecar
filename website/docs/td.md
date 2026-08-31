---
sidebar_position: 6
title: TD - Task Management for AI Agents
---

# TD

**Durable external memory and verification for AI coding agents working across context windows.**

When an AI agent's context window resets, its memory vanishes. TD captures structured work state—completed milestones, remaining items, architectural decisions, and open uncertainties—so subsequent agent sessions resume exactly where previous work stopped.

**No hallucinated progress. No lost decisions. No duplicated effort.**

![TD Monitor in Sidecar](/img/sidecar-td.png)

## Why Task Management for AI Agents?

Autonomous AI coding agents face a fundamental constraint: **context resets between sessions**. Without persistent external memory:

- **Agents hallucinate status** — guessing what is completed versus pending.
- **Architectural decisions are lost** — leading to re-litigating design choices.
- **Work gets repeated** — re-implementing already-completed components.
- **Handoffs break** — lacking structured continuity between prompt compacting.

TD solves this with **persistent, structured memory** stored in a local SQLite database (`.todos/issues.db`):

| Feature | Benefit |
|---------|---------|
| **Structured Handoffs** | Next agent session receives exact state, remaining items, and decisions |
| **Decision Logs** | Prevents overturning established architectural patterns |
| **Dependency Tracking** | Manages multi-issue blockers and critical-path execution |
| **Review Workflows** | Enforces separation between implementation and verification review |
| **Live Event Sync** | Sub-second updates via background event streams |
| **Cross-Project Resolution** | Unresolved issue IDs automatically resolve across other configured projects |

**[View TD source on GitHub →](https://github.com/marcus/td)**

## Installation

From Sidecar, opening the TD tab when `td` is uninstalled displays an **Install TD** button (Homebrew, or `go install` if brew is missing).

```bash
# macOS / Linux via Homebrew
brew install marcus/tap/td

# From source (requires Go 1.21+)
go install github.com/marcus/td@latest
```

Initialize TD in your repository:

```bash
cd your-project/
td init
```

This creates `.todos/issues.db` (automatically added to `.gitignore`).

## Quick Start for AI Agents

Add to your `CLAUDE.md`, `GEMINI.md`, or agent system prompt:

```markdown
## Working with td

td keeps task context durable across sessions. In a new context, run:
  td usage --new-session -q

For substantive work:
  td start <id>
  td log "Progress description..."
  td handoff <id> --done "..." --remaining "..." --decision "..." --uncertain "..."
  td review <id>

Closing needs a review:
  td approve <id> --self-review --reason "Verified all tests pass"
```

### 1. Start of Every Session

```bash
td usage --new-session   # View current state and assigned tasks
```

### 2. Before Context Window Ends

```bash
td handoff td-abc123 \
  --done "Implemented token verification and JWT signing" \
  --remaining "Add unit tests and refresh endpoint" \
  --decision "Used HMAC-SHA256 for signing tokens" \
  --uncertain "Need clarification on refresh token expiration"
```

## Core Concepts

### Sessions

Every agent or terminal gets a unique session ID scoped by **git branch + agent type**. The same agent on the same branch maintains consistent identity across restarts.

```bash
td whoami                    # Show current session identity
td usage --new-session       # Start fresh session, view current work
```

### Issue Lifecycle & State Machine

TD enforces a strict lifecycle state machine to prevent invalid transitions:

```
open ────► in_progress ────► in_review ────► closed
                │                 ▲
             blocked ─────────────┘ (reject)
```

**Review Verification Requirement**: Substantive tasks must undergo a review step before closing. Closing commands record who performed the review (e.g. an independent session, a subagent, or an explicit self-review with reason).

### Epics, Subtasks & Dependencies

```bash
# Create an epic
td epic create "Authentication System" --priority P0

# Create child task
td create "Implement OAuth2 flow" --parent td-abc123

# Add task dependency
td dep add td-xyz789 td-abc456   # xyz depends on abc

# Show critical path sequence
td critical-path
```

## Sidecar TD Monitor Integration

When viewing the TD Monitor tab in Sidecar:

- **Sub-Second Sync**: Subscribes to live event streams so tasks modified by background agents update in real time.
- **Cross-Project Issue Links**: Clicking or resolving an issue ID not present in the current repository automatically searches across other configured projects.
- **Tab Focus Navigation**: Press `tab` to walk focus into monitor sub-panels (current focused task, task list, activity logs).
- **Adjacent Issue Panes**: Clicking any `td-*` link in Git, Files, Notes, or Workspaces opens a side-by-side Issue card showing full description, acceptance criteria, and activity logs.

## Essential Commands Reference

| Command | Action |
|---------|--------|
| `td create "Title" --type feature --priority P1` | Create a new issue |
| `td start <id>` | Begin working on an issue (`open` → `in_progress`) |
| `td focus <id>` | Set active focus on an issue |
| `td log "Note"` | Record progress log entry |
| `td handoff <id> --done ... --remaining ...` | Save structured context handoff |
| `td review <id>` | Move issue to review (`in_progress` → `in_review`) |
| `td approve <id> --self-review --reason "..."` | Approve and close issue |
| `td list` | List open issues |
| `td show <id>` | Display full issue details and logs |
