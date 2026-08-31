---
sidebar_position: 2
title: Session Durability & Recovery
---

# Session Durability & Recovery

Sidecar provides comprehensive shell and session durability, ensuring that terminal sessions, running agents, and workspace records survive tmux server restarts, system reboots, and unexpected crashes.

## Overview

In traditional terminal multiplexer workflows, a crashed tmux server or system reboot wipes away all active session names, working directories, and running agent contexts. Sidecar decouples the **durable shell record** from the live tmux server process.

```
┌────────────────────────────────────────────────────────────┐
│ $ sidecar session status                                   │
│                                                            │
│ RESTORE PLAN (3 shells found):                             │
│   • sidecar-sh-sidecar-1  recreate-shell  ~/code/sidecar   │
│   • sidecar-sh-sidecar-2  resume-agent    ~/code/sidecar   │
│     (Claude Code · session: sess_981113)                   │
│   • sidecar-ws-fix-auth   recreate-shell  ~/code/sidecar-1 │
└────────────────────────────────────────────────────────────┘
```

## Key Capabilities

- **Survives Tmux Restarts**: Shell definitions and metadata are stored durably in `shells.json` (schema v3). When a tmux server dies, records are marked as restorable candidates rather than deleted.
- **Tombstone Retention**: Forgetting or deleting a shell moves its record to a tombstone, remaining restorable for 14 days by default (`shells.tombstoneRetention` in `config.json`).
- **Cold Restoration Engine**: `sidecar session status` prints an ordered, read-only recovery plan, and `sidecar session restore` executes it safely.
- **Granular Restoration Policies**: Configure per-shell restore policies (`--inherit`, `--shell`, `--resume`, `--never`) via `sidecar session policy`.
- **Exact Agent Conversation Resuming**: Official provider hooks bind exact native conversation IDs to Sidecar shells, allowing agents to resume their exact state on restart.

## Cold Restore CLI (`sidecar session`)

### 1. `sidecar session status`

Inspect what a cold restore would do without mutating any state:

```bash
sidecar session status
sidecar session status --json
```

Each shell is classified as:
- `reattach`: The tmux session is already running; reattach immediately.
- `recreate-shell`: Recreate the shell terminal in its recorded working directory.
- `resume-agent`: Recreate the shell and resume the exact bound agent conversation.
- `manual`: A custom run command was configured; require manual start.
- `skip` / `refuse`: The working directory no longer exists or the name is held by an unrelated session.

### 2. `sidecar session restore`

Execute the restore plan to bring back your shells and agents:

```bash
# Recreate eligible shells without starting agents
sidecar session restore

# Dry run to preview the exact actions
sidecar session restore --agents --dry-run

# Recreate shells and resume eligible agent conversations (with confirmation)
sidecar session restore --agents --yes

# Restore a single specific shell
sidecar session restore --shell reviewer --agents --yes
```

### 3. `sidecar session policy`

Set the restore policy for individual shells:

```bash
# Inherit default behavior (ask before resuming agents)
sidecar session policy reviewer --inherit

# Always resume the agent in this shell automatically
sidecar session policy reviewer --resume

# Recreate the terminal shell only, never resume the agent
sidecar session policy build-server --shell

# Never restore this shell on reboot
sidecar session policy scratch-pad --never
```

## Shell Management CLI (`sidecar shell`)

Manage shell records and interact with running sessions from scripts and agents:

### 1. `sidecar shell list`

List all live and restorable forgotten shell records in the current project:

```bash
sidecar shell list
sidecar shell list --json
```

### 2. `sidecar shell rename`

Rename your current shell or a target session:

```bash
# Rename the shell you are currently typing in
sidecar shell rename "Auth Refactor"

# Rename a background session by target name
sidecar shell rename --target sidecar-sh-sidecar-2 "Release Verification"
```

### 3. `sidecar shell send`

Send or type commands into a background shell without switching focus:

```bash
# Execute a command in a background shell (presses Enter)
sidecar shell send --target sidecar-sh-sidecar-2 --run "go test ./..."

# Type a command onto the prompt for the user to review before running
sidecar shell send --target sidecar-sh-sidecar-2 --type "git push origin main"
```

### 4. `sidecar shell forget` and `sidecar shell restore`

Temporarily hide a shell record or restore a tombstoned record:

```bash
# Forget a shell record (moves to tombstone)
sidecar shell forget sidecar-sh-sidecar-1

# Restore a forgotten record
sidecar shell restore sidecar-sh-sidecar-1
```

### 5. `sidecar shell delete`

Close a running tmux session and move its record to a tombstone:

```bash
sidecar shell delete --target sidecar-sh-sidecar-2
```

## Exact Agent Session Binding

Sidecar ensures that when an agent conversation is resumed, it reopens the **exact conversation** rather than guessing the newest session in the directory.

### Provider Integration Hooks

Install official lifecycle hooks to enable session binding:

```bash
# Install session hook for Codex
sidecar agent integration install codex

# Install session hook for Claude Code
sidecar agent integration install claude
```

When installed, each new agent conversation automatically reports its unique session ID to Sidecar via `sidecar agent report-session`.

### Structured Resume Catalog (`agentcatalog`)

All resume commands are built through a centralized, structured argv catalog. Resuming never splices arbitrary strings into a shell prompt, eliminating injection risks and ensuring that resuming from the TUI, the CLI, and cold restore behaves identically.
