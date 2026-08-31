---
sidebar_position: 2
title: Worktree Setup & Hooks
---

# Worktree Setup & Hooks

Configure automatic workspace initialization, environment variable propagation, shared directory symlinks, and agent startup scripts for new git worktrees.

## Overview

When Sidecar creates a new git worktree, it automatically executes a sequence of setup steps to ensure the workspace is immediately ready for coding and agent execution—without manual dependency installation or copying configuration files.

```
┌────────────────────────────────────────────────────────────┐
│ $ sidecar create worktree --name feature-auth              │
│                                                            │
│ 1. Git branch & worktree created at ../feature-auth        │
│ 2. Copied environment files (.env, .env.local)             │
│ 3. Linked shared directories (node_modules)                │
│ 4. Executed setup hook (.worktree-setup.sh)                │
│ 5. Launched agent (Claude Code) in isolated tmux session   │
└────────────────────────────────────────────────────────────┘
```

## What Happens Automatically

Every time a worktree is created, Sidecar automatically:

1. **Copies Environment Files**: Propagates local environment variables and secrets from the main worktree.
2. **Creates Symlinks**: Links shared large directories (such as `node_modules` or `.venv`) if configured.
3. **Executes Setup Scripts**: Runs `.worktree-setup.sh` or your configured `setupScript`.
4. **Applies Agent Overrides**: Checks for `.sidecar-agent-start` or `.sidecar-agent` in the worktree root.

Setup script failures are non-fatal—Sidecar logs the output warning and continues, ensuring the worktree remains accessible.

## Environment File Propagation

Sidecar automatically copies the following files from the main repository worktree if they exist:

- `.env`
- `.env.local`
- `.env.development`
- `.env.development.local`

Files are copied preserving permissions. Missing files are silently skipped. Your local secrets and database credentials remain available in new workspaces without committing them to git.

## Shared Directory Symlinks

To save disk space and eliminate repetitive package installations across worktrees, configure `symlinkDirs` in `.sidecar/config.json`:

```json
{
  "plugins": {
    "workspace": {
      "symlinkDirs": [
        "node_modules",
        ".venv"
      ]
    }
  }
}
```

Sidecar creates symlinks in the new worktree pointing back to the main worktree's copies, saving gigabytes of disk space across parallel branches.

## Setup Script Hooks

### 1. `.worktree-setup.sh` (Repository Root)

Create an executable `.worktree-setup.sh` script in your project root. Sidecar runs this script inside the newly created worktree directory with bash.

**Available Environment Variables:**

| Variable | Value |
|----------|-------|
| `MAIN_WORKTREE` | Absolute path to the main repository worktree |
| `WORKTREE_BRANCH` | Name of the newly created branch |
| `WORKTREE_PATH` | Absolute path to the new worktree directory |
| `SIDECAR_BASE_BRANCH` | Name of the base branch the worktree was created from |

**Example Script:**

```bash
#!/bin/bash
set -e

echo "Initializing worktree: $WORKTREE_BRANCH"

# Install dependencies if not symlinked
npm install

# Start local test database if needed
docker-compose up -d db

echo "Worktree setup complete."
```

### 2. Configured `setupScript`

Alternatively, specify a custom script path in `~/.config/sidecar/config.json`:

```json
{
  "plugins": {
    "workspace": {
      "setupScript": ".sidecar/setup-workspace.sh"
    }
  }
}
```

## Per-Worktree Agent Overrides

You can customize the agent and startup command used in a specific worktree by placing configuration files in the worktree root:

- **`.sidecar-agent`**: Contains the agent type name (e.g. `codex`, `claude`, `opencode`).
- **`.sidecar-agent-start`**: Single-line custom command prefix for launching the agent (e.g. `opencode --profile fast` or `claude --dangerously-skip-permissions`).

### Startup Command Precedence

When launching an agent in a worktree, Sidecar evaluates startup commands in this order:

1. `.sidecar-agent-start` in the worktree root
2. `plugins.workspace.agentStart[<selectedAgent>]` from configuration
3. `plugins.workspace.agentStart["*"]` default fallback
4. Built-in default command for the agent provider
