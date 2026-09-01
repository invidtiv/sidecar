---
sidebar_position: 3
title: CLI Reference
---

# CLI Reference

Complete reference for Sidecar's command-line interface, structured commands, flags, and exit codes for scripting and automation.

## Command Index

| Command | Purpose |
|---------|---------|
| [`sidecar agent`](#sidecar-agent) | Inspect, prompt, control, and coordinate AI coding agents |
| [`sidecar content`](#sidecar-content) | Internal host content transport (not `sidecar open --host`) |
| [`sidecar create`](#sidecar-create) | Create managed shells, terminal splits, and git worktrees |
| [`sidecar host`](#sidecar-host) | Register, configure, and probe remote hosts over SSH |
| [`sidecar layout`](#sidecar-layout) | Inspect, apply, and reposition multi-pane window layouts |
| [`sidecar notify`](#sidecar-notify) | Post, list, and dismiss notifications with action jumps |
| [`sidecar open`](#sidecar-open) | Open files, tasks, diffs, or resources in adjacent panes |
| [`sidecar session`](#sidecar-session) | Cold session restore planning, execution, and policy |
| [`sidecar setup`](#sidecar-setup) | Launch Sidecar on the setup and environment check screen |
| [`sidecar shell`](#sidecar-shell) | Manage shell records, display names, and send commands |
| [`sidecar terminal-links`](#sidecar-terminal-links) | Verify and inspect external terminal resource providers |

---

## `sidecar agent`

Inspect, start, and coordinate agents in Sidecar-managed shells.

```bash
Usage: sidecar agent <subcommand> [options]
```

### Subcommands

#### `sidecar agent list`
List all active agent sessions across workspaces.
- `--json`: Output stable structured JSON.

#### `sidecar agent get [TARGET]`
Get details for a specific agent by shell or session target.
- `--project NAME`: Target project (slug, basename, or path).
- `--include-session-ref`: Include the bound conversation ID.
- `--json`: Output structured JSON.

#### `sidecar agent start TARGET --kind KIND`
Start an agent provider in a managed shell.
- `--kind KIND`: Provider kind (`claude`, `codex`, `opencode`, `cursor`, etc.).
- `--json`: Output structured JSON.

#### `sidecar agent prompt [TARGET] TEXT`
Send prompt text to an agent.
- `--wait`: Block until the agent finishes processing.
- `--timeout DURATION`: Timeout for completion (e.g. `5m`, `10m`).
- `--json`: Output structured JSON.

#### `sidecar agent read TARGET`
Read the terminal output buffer from an agent shell.
- `--source SOURCE`: `recent-unwrapped` (default) or `scrollback`.
- `--json`: Output structured JSON.

#### `sidecar agent send-keys TARGET KEYS`
Send raw keys or escape sequences to an agent shell.

#### `sidecar agent integration <install|list|status|update|repair|uninstall> [PROVIDER]`
Manage provider lifecycle integration hooks.
- `--dry-run`: Preview file changes before modifying configuration.
- `--json`: Output structured JSON.

---

## `sidecar content`

Internal read-only transport a viewing Sidecar invokes on a registered host to resolve and load files, issues, notes, diffs, and resource documents. This is not a public file browser and not `sidecar open --host`. Agents in a Sidecar-managed pane on that host run `sidecar open` / `layout` onto the lease holder's screen; other processes use ordinary tools over SSH.

The host must advertise `ContentReadV1`. See [Remote Hosts](./remote-hosts#clicks-in-a-remote-terminal) for the user-visible clicks this powers, and [Agent open and layout from a host pane](./remote-hosts#agent-open-and-layout-from-a-host-pane) for the lease-holder rule.

```bash
Usage: sidecar content <describe|resolve|read> --json
```

### Subcommands

#### `sidecar content describe`
Return this host's validated, ordered resource-provider descriptors and a deterministic fingerprint.
- `--if-revision REV`: Return a small `notModified` object when the fingerprint is unchanged.
- `--json`: Write the machine contract (required).

#### `sidecar content resolve`
Resolve a file, issue, note, git spec, or resource locator against a durable workspace identity on this machine. The workspace id is re-resolved to its authoritative root on every request; the target is a hint, never authority.
- `--workspace ID`: Unscoped durable workspace id.
- `--kind KIND`: `file`, `issue`, `note`, `diff`, or `resource`.
- `--target VALUE`: Path, id, git spec, or resource locator.
- `--json`: Write the machine contract (required).

#### `sidecar content read`
Read a bounded document, issue card, note, git diff operation, or resource document.
- `--workspace ID`: Unscoped durable workspace id.
- `--kind KIND`: `file`, `issue`, `note`, `diff`, or `resource`.
- `--operation OP`: Kind-specific read (`document`, `card`, `note`, `resource`, `working-tree`, `commit`, …).
- `--target VALUE`: Path, id, git spec, or resource locator.
- `--if-revision REV`: Return a small `notModified` object when the content is unchanged.
- `--json`: Write the machine contract (required).

Full flags and exit codes: `sidecar content --help`.

---

## `sidecar create`

Create shells, terminal splits, and git worktrees in the running instance.

```bash
Usage: sidecar create <shell|worktree> [options]
```

### Subcommands

#### `sidecar create shell`
Create a new managed shell session.
- `--name NAME`: Display name for the shell.
- `--project NAME`: Target project (defaults to current directory).
- `--split DIR`: Open as a split pane (`left`, `right`, `up`, `down`).
- `--run CMD`: Execute command immediately on creation.
- `--type CMD`: Type command onto the prompt without pressing Enter.
- `--agent KIND`: Seed an agent in the new shell.
- `--auto`: Enable auto-approve for supported agent providers.

#### `sidecar create worktree`
Create a dedicated git worktree workspace.
- `--name NAME`: Branch / worktree name (required).
- `--base BRANCH`: Base branch to create from (default: main/default branch).
- `--plan`: Generate and output the worktree creation plan as JSON without modifying disk.
- `--expect-source-oid OID`: Verify the base branch commit OID matches expected value before creating.
- `--agent KIND`: Seed an agent in the new worktree.

---

## `sidecar host`

Manage and observe remote hosts over SSH.

```bash
Usage: sidecar host <subcommand> [options]
```

### Subcommands

#### `sidecar host add TARGET`
Register a new remote machine over SSH.
- `--id ID`: Unique identifier for the host (defaults to target).
- `--binary PATH`: Explicit path to `sidecar` binary on the remote host.
- `--remote-config PATH`: Path to config file on the remote host.
- `--env "KEY=VAL ..."`: Space-separated environment variables for the remote process.

#### `sidecar host list`
List all registered remote hosts and their enabled status.
- `--json`: Output structured JSON.

#### `sidecar host probe TARGET`
Probe an SSH target to inspect reachability, Sidecar version, protocol compatibility, and tmux status.
- `--json`: Output structured JSON.

#### `sidecar host set ID`
Update settings for an existing registered host.
- `--enabled` / `--disabled`: Toggle host connection state.
- `--target TARGET`: Update SSH destination target.

#### `sidecar host remove ID`
Unregister a remote host.

---

## `sidecar layout`

Inspect and manipulate the multi-pane grid layout. From a Sidecar-managed pane whose geometry lease is held by a connected viewer, these verbs read and mutate that viewer's screen. There is no `--host` flag. Off-screen, or a lease holder that cannot receive pane requests, is exit 4.

```bash
Usage: sidecar layout <get|apply|move> [options]
```

### Subcommands

#### `sidecar layout get`
Get the current layout structure, grid dimensions, and pane targets.
- `--sessions`: Inspect the global Sessions browser layout instead of the project workspace.
- `--json`: Output structured JSON.

#### `sidecar layout apply`
Compose panes additively or replace the layout atomically.
- `--pane DESCRIPTOR`: Add a single pane (repeatable).
- `--spec SPEC`: Complete layout specification string or `-` for standard input.
- `--sessions`: Target the global Sessions browser.
- `--json`: Output structured JSON.

#### `sidecar layout move FROM --to TO`
Reposition an existing pane in the layout.
- `FROM`: Grid cell coordinate (e.g. `2.1`).
- `--to CELL`: Target cell coordinate (e.g. `1.2`) or column number (e.g. `3`).
- `--focused --to DIRECTION`: Move the focused pane in a direction (`left`, `right`, `up`, `down`).
- `--sessions`: Target the global Sessions browser.
- `--json`: Output structured JSON.

---

## `sidecar notify`

Post and manage notifications and toasts.

```bash
Usage: sidecar notify <subcommand> [options]
```

### Subcommands

#### `sidecar notify post MESSAGE`
Post a notification toast.
- `--source SOURCE`: Source category identifier (e.g. `agent`, `tests`, `build`).
- `--target TARGET`: Actionable call to action (`file:path[:line]`, `issue:id`, `task:id`, `commit:hash`, `session:name`, `url:address`).
- `--urgency LEVEL`: `low`, `normal` (default), `high`, `critical`.

#### `sidecar notify list`
List active notifications in the Notification Centre.
- `--json`: Output structured JSON.

#### `sidecar notify dismiss`
Dismiss notifications.
- `--id ID`: Dismiss a specific notification.
- `--all`: Dismiss all active notifications.

#### `sidecar notify test`
Trigger a test notification.

---

## `sidecar open`

Open files, tasks, diffs, notes, or resources in adjacent panes. From a Sidecar-managed pane whose geometry lease is held by a connected viewer, the open lands on that viewer's screen. There is no `--host` flag; routing is the lease. A relayed open never queues. Remote Sessions clicks use the internal `sidecar content` transport.

```bash
Usage: sidecar open TARGET [options]
```

### Options

- `TARGET`: File path (`path/to/file.go[:line]`), TD issue ID (`td-abc123`), note ID, or resource locator.
- `--at CELL`: Place the pane at an exact grid cell (e.g. `1.2`, `2.1`).
- `--split DIR`: Open as a split in a direction (`left`, `right`, `up`, `down`).
- `--diff [REF]`: Open a git diff preview for a ref, commit, or range.

---

## `sidecar session`

Manage cold session recovery and persistence policies.

```bash
Usage: sidecar session <status|restore|policy> [options]
```

### Subcommands

#### `sidecar session status`
Print the ordered cold restore plan for all managed shells.
- `--json`: Output structured JSON.

#### `sidecar session restore`
Execute the restore plan to recreate shells and optionally resume agents.
- `--dry-run`: Print plan without making changes.
- `--shell TARGET`: Restore only the specified shell.
- `--agents`: Resume eligible bound agent conversations.
- `--yes`: Confirm agent resumption non-interactively.
- `--json`: Output structured JSON.

#### `sidecar session policy TARGET <POLICY>`
Set restore policy for a shell.
- `--inherit`: Follow global default policy (ask before resuming agents).
- `--resume`: Always resume the agent automatically.
- `--shell`: Recreate the shell terminal only, never resume the agent.
- `--never`: Never restore this shell on restart.

---

## `sidecar shell`

Manage shell records and send commands to sessions.

```bash
Usage: sidecar shell <subcommand> [options]
```

### Subcommands

#### `sidecar shell list`
List live and forgotten shell records for the resolved project.
- `--json`: Output structured JSON.

#### `sidecar shell name`
Print the current shell's display name.
- `--json`: Output structured JSON.

#### `sidecar shell rename [NAME]`
Rename the current shell or a target session.
- `--target SESSION`: Rename a background tmux session.
- `--json`: Output structured JSON.

#### `sidecar shell send --target SESSION <--run CMD | --type CMD>`
Send a command to a background shell.
- `--run CMD`: Execute command in the shell (presses Enter).
- `--type CMD`: Type command without pressing Enter.
- `--json`: Output structured JSON.

#### `sidecar shell forget TARGET`
Move a shell record to a tombstone.

#### `sidecar shell restore TARGET`
Restore a tombstoned shell record.

#### `sidecar shell delete --target SESSION`
Close a tmux session and tombstone its record.

---

## `sidecar terminal-links`

Inspect and test external terminal resource providers.

```bash
Usage: sidecar terminal-links <check|list> [options]
```

### Subcommands

#### `sidecar terminal-links list`
List all configured resource providers.
- `--describe`: Run each provider's `describe` method.
- `--json`: Output structured JSON.

#### `sidecar terminal-links check INSTANCE`
Check executable resolution and protocol conformance for a provider instance.
- `--resolve LOCATOR`: Also test resolving a sample ticket locator.
- `--json`: Output structured JSON.

---

## `sidecar setup`

Start Sidecar with the Configuration page open on **Sidecar Setup** to inspect your environment, verify color support, and run automated health checks.

```bash
sidecar setup
sidecar setup --project /path/to/project
```
