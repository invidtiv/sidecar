# Research Report: Herdr Background Session Persistence vs. Sidecar Tmux Architecture

## Executive Summary

This report explores how **[Herdr](https://github.com/herdrdev/herdr)** (an open-source, Rust-based multiplexer for AI coding agents) manages persistent background sessions, and compares its architecture with **Sidecar**'s current `tmux`-backed workspace model. 

In particular, this analysis evaluates the remote workflow scenario where a developer uses a local laptop connected over SSH to a remote server (such as a Mac Mini). While Sidecar allows users to resume work if run inside a remote `tmux` wrapper session, Herdr's native **daemon-client model**, **PTY ownership**, **semantic agent state engine**, and **socket IPC** provide a fundamentally different level of background resilience, multi-agent visibility, and programmatic automation.

---

## 1. How Herdr Keeps Sessions Going in the Background

Herdr is designed as **"tmux for coding agents"**—a runtime environment specifically built to orchestrate, monitor, and persist multiple parallel AI agent sessions (such as Claude Code, Cursor CLI, Aider, or custom scripts).

```
+-----------------------------------------------------------------------+
|                             REMOTE HOST                               |
|                                                                       |
|  +-----------------------------------------------------------------+  |
|  |                      herdrd (Daemon Server)                     |  |
|  |                                                                 |  |
|  |  +-------------------+  +-------------------+                   |  |
|  |  | PTY 1: Agent A    |  | PTY 2: Agent B    |  [Tokio Runtime]  |  |
|  |  | (e.g. Claude Code)|  | (e.g. Cursor CLI) |  [Agent Engine]   |  |
|  |  +-------------------+  +-------------------+  [Attention Q]    |  |
|  |           ^                       ^                             |  |
|  +-----------|-----------------------|-----------------------------+  |
|              | UNIX Domain Socket    | Socket API                     |
|              v                       v                                |
|     +------------------+   +-------------------+                      |
|     |  herdr TUI Client|   |  Agent Socket IPC |                      |
|     +------------------+   +-------------------+                      |
+--------------^--------------------------------------------------------+
               | SSH / Local Connection (Can disconnect anytime)
               |
      +------------------+
      |  Laptop / Client |
      +------------------+
```

### 1.1 Client-Server Architecture (`herdrd` + `herdr` client)
Herdr splits terminal operations into two distinct binaries/components:
1. **`herdrd` (Background Daemon)**: A lightweight, persistent server written in Rust using the Tokio asynchronous runtime. It hosts the Unix domain socket server (`~/.herdr/herdr.sock`), manages workspace state, buffers PTY streams, and monitors agent processes.
2. **`herdr` (Thin TUI Client)**: A Crossterm/Ratatui terminal UI frontend that acts strictly as a viewer and key-event sender.

### 1.2 Native PTY Ownership
Instead of wrapping an external multiplexer binary like `tmux`, `herdrd` creates and owns pseudo-terminals (PTYs) directly via OS system calls (`openpty`/`fork`). 

Because child processes (coding agents, sub-shells, compilation tasks) are direct child processes of the `herdrd` daemon, their life cycle is completely decoupled from any terminal emulator window or SSH connection.

### 1.3 Detachment and Reconnection Mechanics
When a developer closes their laptop lid, drops SSH connection, or exits the terminal:
* **The TUI client exits**, but `herdrd` remains unaffected.
* **Agents continue running**: Agents continue executing LLM calls, tool uses, tests, or file writes inside their respective PTYs.
* **PTY output buffering & state parsing continue**: `herdrd` continues reading stdout/stderr streams from child PTYs, recording history, and updating agent statuses.
* **Instant Reattachment**: Re-running `herdr` (locally or via SSH) reconnects to `herdrd` over the Unix socket. The daemon immediately streams the latest viewport frame, buffer history, and active agent statuses to the client.

### 1.4 Semantic Agent State Engine & Global Attention Queue
Unlike standard terminal multiplexers that view terminal panes as dumb streams of ANSI bytes, Herdr parses PTY output streams and process states in real-time to maintain an explicit agent state machine:
* **Blocked / Input Needed (Red)**: The agent prompt matches pattern signatures requiring human intervention (e.g., `[y/N]`, `Allow command execution?`, confirmation dialogs).
* **Working / Thinking (Yellow)**: The agent is actively generating text or executing tools.
* **Finished / Review Ready (Blue)**: The agent finished its prompt task loop and returned to ready state.
* **Idle (Green)**: Process waiting without active task.

Herdr aggregates all blocked or completed agents across all project tabs into a single top-level **Attention Queue**. A developer managing 10 agents across 5 projects does not need to click into each tab; the Attention Queue instantly flags which agent needs approval.

### 1.5 Socket API & Inter-Agent Programmatic Control
`herdrd` exposes a JSON-RPC / Socket API accessible via UNIX socket or the `herdr api` CLI command. This allows coding agents running *inside* Herdr to programmatically query Herdr's status, spawn new panes, pass context to secondary sub-agents, or request human attention.

---

## 2. How Sidecar Handles Background Workspaces

Sidecar is a Go-based TUI application built with Bubble Tea. It provides a full-featured agent IDE experience—including project management, file browsing with inline editing, git status/diff view, note taking, and `td` (task database) integration.

```
+-----------------------------------------------------------------------+
|                             REMOTE HOST                               |
|                                                                       |
|  +-----------------------------------------------------------------+  |
|  |                 Outer tmux Session (Optional)                   |  |
|  |                                                                 |  |
|  |  +-----------------------------------------------------------+  |  |
|  |  |  Sidecar Go Process (Bubble Tea TUI)                      |  |  |
|  |  |  - In-memory UI State (Modals, Tabs, Focus)               |  |  |
|  |  |  - File Browser, Git, Notes, Task DB                      |  |  |
|  |  |  - ControlManager / Polling Engine                        |  |  |
|  |  +-----------------------------|-----------------------------+  |  |
|  |                                | CLI / Control Socket          |  |
|  |                                v                               |  |
|  |  +-----------------------------------------------------------+  |  |
|  |  |  tmux Server (Managed Workspaces)                         |  |  |
|  |  |  - Session: sidecar-workspace-<id>                        |  |  |
|  |  |  - Panes: Shells, Agents, Inline Editor                  |  |  |
|  |  +-----------------------------------------------------------+  |  |
|  +-----------------------------------------------------------------+  |
+-----------------------------------^-----------------------------------+
                                    | SSH Connection
                                    |
                           +------------------+
                           |  Laptop / Client |
                           +------------------+
```

### 2.1 External `tmux` Integration
Sidecar does not own native PTYs directly. Instead, it delegates terminal workspace execution to system `tmux` ([session.go](file:///Users/marcus/code/sidecar/internal/tty/session.go)):
* On startup, Sidecar calls `PrepareServer()`, setting `start-server` and `exit-empty off` so the tmux daemon stays alive even if sessions are empty.
* Workspace terminals and inline editor instances spawn as background tmux sessions (`tmux new-session -d`).
* Terminal output is captured by polling `tmux capture-pane` ([polling.go](file:///Users/marcus/code/sidecar/internal/tty/polling.go)) or subscribing to tmux control mode notifications (`%output`, `%layout-change`) via [ControlManager](file:///Users/marcus/code/sidecar/internal/tty/control_manager.go).
* Captured text is rendered in Bubble Tea view functions, with a synthetic cursor overlaid on top ([cursor.go](file:///Users/marcus/code/sidecar/internal/tty/cursor.go)).

### 2.2 The Laptop -> Mac Mini SSH Scenario
In your current workflow (Laptop SSH connected to a remote Mac Mini):

#### Case A: Running Sidecar Inside an Outer `tmux` Session on Mac Mini
1. On the Mac Mini, you run `tmux new -s dev`, and inside it launch `sidecar`.
2. When you close your laptop, your SSH session to the Mac Mini drops.
3. The outer `tmux` session on the Mac Mini keeps the `sidecar` Go process alive.
4. `sidecar` keeps its underlying workspace `tmux` sessions alive.
5. When you reopen your laptop and SSH back into the Mac Mini (`tmux attach -t dev`), you resume right where you left off, with all UI state intact (active modals, open tabs, input state).

#### Case B: Running Sidecar Directly in SSH (Without Outer `tmux`)
1. On the Mac Mini, you run `sidecar` directly in your SSH shell.
2. When you close your laptop, the SSH connection disconnects, sending `SIGHUP` to the `sidecar` Go process.
3. **The `sidecar` Go process dies**. 
4. The underlying workspace `tmux` sessions (`sidecar-workspace-...`) stay alive on the Mac Mini because `exit-empty off` was set.
5. When you reconnect over SSH and launch `sidecar` again, a **new Sidecar process starts**. While Sidecar can re-attach to existing workspace tmux sessions, any transient in-memory UI state (active modal dialogs, tab selection, filter queries, uncommitted note drafts) was lost when the previous process terminated.

---

## 3. Comparison Chart: Herdr vs. Sidecar

| Feature / Dimension | **Herdr** (`herdr` / `herdrd`) | **Sidecar** (Go + Bubble Tea + `tmux`) |
| :--- | :--- | :--- |
| **Primary Focus** | **Agent Multiplexer & Orchestrator**<br>Specialized "tmux for AI agents" with agent-state tracking. | **Complete Agent IDE & Workspace Tool**<br>Multiplexer + File Browser + Git Client + Notes + Task Database (`td`). |
| **Architecture** | **Built-in Daemon + Thin Client**<br>Rust binary split into `herdrd` daemon (server) and `herdr` thin client (TUI viewer). | **Single TUI Process + External `tmux` Server**<br>Go application that executes commands against system `tmux` binary. |
| **Native Disconnect Resilience** (Laptop -> Mac Mini) | **Native & Seamless**<br>Closing laptop drops thin client; `herdrd` stays running on server, continuing agent execution, state parsing, and history buffering. Re-attaching restores 100% UI state. | **Requires Outer `tmux` Session**<br>Closing laptop without outer `tmux` kills the Sidecar process. Re-running Sidecar recreates UI state (though underlying tmux workspace panes survive). |
| **PTY Management** | **Native Rust PTY Engine**<br>Direct syscalls (`openpty`/`fork`) managed in-process with Tokio runtime. | **Delegated to `tmux` CLI**<br>Relies on system `tmux` binary for PTY allocation and pane multiplexing. |
| **Agent Prompt & State Awareness** | **Semantic PTY Parser**<br>Parses PTY output for agent prompt patterns (`[y/N]`, tool permissions) and maintains explicit states: *Blocked*, *Working*, *Finished*, *Idle*. | **Output Capture & Task DB**<br>Relays raw terminal output; relies on user visual inspection or `td` task database integration for state tracking. |
| **Multi-Agent Attention Queue** | **Global Priority Attention Queue**<br>Cross-workspace queue that automatically highlights agents waiting for user input or finished with tasks. | **Manual Navigation**<br>User switches between project tabs and workspace preview panes to inspect agent status. |
| **Programmatic API & IPC** | **Unix Socket & JSON-RPC API**<br>Exposes CLI/socket endpoints for running agents to create panes, post notifications, or talk to other agents. | **Internal `tea.Msg` / `td` CLI**<br>Inter-plugin messaging within Go app; external agents interact via standard `tmux` commands or `td` CLI. |
| **Performance & Resource Overhead** | **Zero-Subprocess Socket IPC**<br>In-process PTY buffers and Unix domain sockets; low CPU overhead. | **Subprocess Spawning / Control Stream**<br>Uses `capture-pane` polling (20ms-250ms) or `ControlManager` tmux control sockets. |

---

## 4. Key Takeaways & Opportunities for Sidecar

### 4.1 What Herdr Does Better
1. **Unconditional Background Resilience**: Herdr's daemon-client architecture means users never have to remember to run inside an outer `tmux` session on remote servers. Disconnecting and reconnecting is natively supported at the application layer.
2. **Semantic Agent Prompt Tracking**: Herdr understands *what* is running inside its panes. By detecting when an agent is waiting for permission (`[y/N]`), it eliminates the need to constantly poll multiple workspaces manually.
3. **Global Attention Queue**: Bringing blocked or finished agents to the top level of the UI dramatically increases developer throughput when managing 5-10 parallel coding agents.
4. **First-Class Agent IPC Socket**: Allowing running agents to drive the multiplexer (e.g. "open a new pane and run tests") enables complex multi-agent workflows.

### 4.2 Practical Recommendations for Sidecar Evolution

While Sidecar offers a far richer IDE experience (file browser, git status, inline editor, `td` task management) than Herdr, Sidecar can adopt several of Herdr's strengths:

1. **Option for Headless Daemon Mode / Session Server**:
   * Sidecar could separate its state engine into a lightweight background service (`sidecard`) or daemonize its core event loop.
   * Alternatively, Sidecar could auto-detect if it is running in a remote SSH environment and offer an automatic `tmux` auto-wrap option (`sidecar attach-or-create`).
2. **Agent Prompt Pattern Matcher**:
   * Add a background output scanner to `internal/tty/output_buffer.go` that inspects output lines for interactive prompts (e.g. Claude Code tool approvals, git prompts, confirmation dialogs).
3. **Workspace Attention Indicators**:
   * Surface an "Attention Needed" badge on workspace tabs when a background tmux pane displays an unhandled prompt or task completion marker.
4. **Agent Socket Endpoint**:
   * Expose a local Unix socket endpoint in Sidecar so external agent tools can trigger Sidecar commands (e.g., focusing a file, creating a workspace, or posting a notification to the footer).
