---
sidebar_position: 1
title: Agent Coordination & Automation
---

# Agent Coordination & Automation

Programmatically start, prompt, monitor, and coordinate multiple AI coding agents across Sidecar-managed shells using the non-interactive CLI.

## Overview

Sidecar enables multi-agent orchestration, allowing an agent or automated script to safely drive a secondary coding agent in a separate shell. Agent coordination is designed with strict boundaries to ensure the user's focus is preserved and commands are never sent blindly.

```
┌────────────────────────────────────────────────────────────┐
│ $ sidecar agent list                                       │
│                                                            │
│ TARGET          KIND        STATE      SESSION             │
│ reviewer        claude      Working    sess_981113         │
│ test-runner     codex       Idle       sess_0f3b3f         │
│ backend-lead    opencode    Waiting    sess_3cc0d9         │
└────────────────────────────────────────────────────────────┘
```

## The Safe Coordination Sequence

When scripting agents, always follow the standard coordination lifecycle:

```
1. Discover ────► 2. Create Layout ────► 3. Start Agent ────► 4. Prompt & Wait ────► 5. Read Output ────► 6. Send Keys
   (agent list)      (create shell)         (agent start)        (agent prompt)         (agent read)         (agent send-keys)
```

1. **Discover**: Check running agents and available shells with `sidecar agent list --json`.
2. **Create the Layout**: Create a dedicated shell using `sidecar create shell --name "Reviewer"`. Never launch an agent directly into the user's active shell.
3. **Start the Provider**: Start the agent using `sidecar agent start TARGET --kind KIND`. This command blocks until the agent CLI is fully initialized and ready for input.
4. **Prompt & Wait**: Send the initial task instructions with `sidecar agent prompt TARGET "prompt text" --wait --timeout 5m`.
5. **Read Before Sending Keys**: Always inspect the terminal buffer with `sidecar agent read TARGET --source recent-unwrapped` before sending any interactive keystrokes.
6. **Send Keys / Approvals**: Answer confirmation prompts or send keystrokes with `sidecar agent send-keys TARGET "yes\n"`.

## Agent CLI Reference (`sidecar agent`)

### 1. `sidecar agent list`

List all active agent sessions across your workspaces:

```bash
sidecar agent list
sidecar agent list --json
```

### 2. `sidecar agent get`

Inspect a single managed agent by shell name or session target:

```bash
sidecar agent get reviewer --json
```

### 3. `sidecar agent start`

Start a supported agent provider in an existing Sidecar shell:

```bash
# Start Claude Code in shell "reviewer"
sidecar agent start reviewer --kind claude

# Start Codex in a background shell
sidecar agent start test-worker --kind codex
```

### 4. `sidecar agent prompt`

Send a task prompt to a running agent:

```bash
# Send prompt and wait for completion
sidecar agent prompt reviewer "Refactor the error handling in internal/app" --wait --timeout 3m

# Send prompt to the current shell
sidecar agent prompt "Run unit tests and report failures"
```

### 5. `sidecar agent read`

Capture the output buffer from an agent shell:

```bash
# Read recent un-wrapped output lines
sidecar agent read reviewer --source recent-unwrapped

# Read full terminal scrollback
sidecar agent read reviewer --source scrollback
```

### 6. `sidecar agent send-keys`

Send raw keystrokes to a running agent:

```bash
# Send Enter key
sidecar agent send-keys reviewer "Enter"

# Send Ctrl+C to cancel
sidecar agent send-keys reviewer "C-c"
```

### 7. `sidecar agent explain`

Explain the lifecycle authority and state evidence for a pane:

```bash
sidecar agent explain --current --json
sidecar agent explain --shell reviewer
```

## Agent Lifecycle Integrations (`sidecar agent integration`)

Sidecar includes official lifecycle integrations for supported agent providers (such as Codex, Claude Code, and OpenCode). When installed, the agent directly reports its lifecycle events (working, blocked on approval, completed) and session identity to Sidecar without relying on screen heuristics.

### Installing Integrations

```bash
# List available integrations and their installation status
sidecar agent integration list

# Preview file operations before installing (dry-run)
sidecar agent integration install claude --dry-run

# Install integration for Claude Code
sidecar agent integration install claude

# Install integration for Codex
sidecar agent integration install codex
```

### Managing Integrations

```bash
# Check status of installed integrations
sidecar agent integration status

# Update integrations to match the latest Sidecar build
sidecar agent integration update claude

# Repair broken hooks
sidecar agent integration repair claude

# Uninstall Sidecar hooks
sidecar agent integration uninstall claude
```

## In-Shell Helper (`sidecar agents`)

When working interactively inside a Sidecar shell, run `sidecar agents` to see quick actions available in your current environment, including renaming your shell to match your task and opening files for review.
