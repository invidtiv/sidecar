# Herdr gap closure: agent control and cold session restoration

**Status:** active; M0–M3 implemented on 2026-08-30, M4 onward planned. **Research baseline:** Sidecar `main` at `13ddaaa6` (2026-08-29); Herdr v0.8.2 at commit `9eb52145`. Herdr is the feature benchmark this plan measures against — the bar is parity, and where Sidecar's runtime allows it, better. It is not a runtime Sidecar depends on: the Herdr remote-hosts plan is deprecated, and Sidecar is its own remote host runtime.

One sentence: **an agent working inside Sidecar should be able to start and coordinate another managed agent through provider-aware commands, and a machine restart should reconstruct Sidecar's durable workspace shape and optionally resume the exact agent conversations that were running, without replacing tmux or pretending tmux can live-handoff its PTYs.**

Related plans:

- [Pane repositioning](../implemented/pane-repositioning.md) (implemented) owns interactive and agent-driven pane movement. This plan composes with its `layout get` / `layout apply` / `layout move` surface and adds no second layout grammar.
- [Sidecar as its own remote host runtime](sidecar-remote-hosts.md) owns host registration, SSH transport, remote inventory, remote terminal control, and — as of its completed Phase C — remote mutation, shipped as one-shot `ssh <target> sidecar <verb> --json` invocations over the existing ControlMaster. Because this plan's commands are headless, target-taking CLI verbs, that seam carries them remotely without new protocol work; see [Interaction with remote hosts](#interaction-with-remote-hosts).
- [Hosting Herdr plugins in Sidecar](../hold/herdr-plugin-support.md) is on hold and orthogonal. A plugin may eventually call the same agent-control core, but plugin hosting is not part of this plan.
- [Deterministic agent lifecycle hooks](notification-agent-lifecycle-hooks.md) owns lifecycle reporting, authority arbitration, provider integration installation/status, and screen fallback. This plan owns the session-identity and resume semantics that use the same reporting envelope without claiming lifecycle authority.
- [Native Agent Orchestration in Sidecar](../deprecated/agent-orchestration-integration.md) remains deprecated. This plan deliberately exposes small coordination primitives and does not revive a Sidecar-owned plan/build/review engine, task policy, validator topology, or merge loop.

## Decision first

Close the useful Herdr gaps in three bounded pieces:

1. **Provider-aware agent control.** Add a headless `sidecar agent` command group over Sidecar-managed shells: list/get, start, prompt, wait, read, and logical-key input. These commands resolve a durable managed-shell target, verify the live pane occupant, reuse Sidecar's existing activity semantics, and return structured outcomes. They do not require a running Sidecar TUI.
2. **Exact session identity and cold restore.** Extend `shells.json` with structured agent launch/resume metadata reported by official per-provider integrations. On the next Sidecar start after a confirmed tmux server replacement, recreate eligible managed shells and their persisted pane layouts. Resume an agent conversation only from a validated, exact session reference and only under the configured restore policy.
3. **One transport-neutral core.** Put target resolution, refusal rules, lifecycle waits, resume planning, and restore idempotency in library packages. The local adapter speaks tmux; the remote-host plan can add a host adapter without recreating the rules.

Do **not** replace tmux, add a Sidecar daemon, or build a native PTY runtime. Sidecar application updates already leave tmux and its children running. A tmux server replacement cannot preserve arbitrary live processes because tmux exposes no supported transfer of its PTY master file descriptors. Cold reconstruction and native agent conversation resumption are the honest fallback; an SCM_RIGHTS-style live handoff is not implementable from Sidecar without owning the PTYs.

## Research basis

The Herdr comparison is against the checked-out v0.8.2 tag, not a feature list or the current `main` branch:

- [`skills/herdr/SKILL.md`](https://github.com/herdrdev/herdr/blob/v0.8.2/skills/herdr/SKILL.md) defines the agent-facing workflow: discover IDs, split layout separately, start a named agent in an available pane, prompt/wait/read it through lifecycle state, and use raw pane commands only for raw terminal work.
- [`docs/preview/website/src/content/docs/agent-automation.mdx`](https://github.com/herdrdev/herdr/blob/v0.8.2/docs/preview/website/src/content/docs/agent-automation.mdx) is the detailed CLI contract, including target pinning, ready detection, blocked refusals, prompt-stall detection, passive reads, and ordinary-command primitives.
- [`docs/preview/website/src/content/docs/session-state.mdx`](https://github.com/herdrdev/herdr/blob/v0.8.2/docs/preview/website/src/content/docs/session-state.mdx) distinguishes detach, cold server restore, optional screen-history replay, native agent resume, and experimental live handoff.
- [`src/persist/snapshot.rs`](https://github.com/herdrdev/herdr/blob/v0.8.2/src/persist/snapshot.rs) persists workspace/tab/pane topology, cwd, agent name and kind, exact official agent session reference, and structured launch argv.
- [`src/agent_resume.rs`](https://github.com/herdrdev/herdr/blob/v0.8.2/src/agent_resume.rs) accepts session references only from allowlisted official integration sources, validates ID/path shape, builds argv as an argument vector, and deduplicates a native session across panes.
- [`src/server/handoff.rs`](https://github.com/herdrdev/herdr/blob/v0.8.2/src/server/handoff.rs) shows why handoff belongs to the PTY owner: the old process passes PTY file descriptors over a Unix socket, with an explicit 64-FD cap and reconnect semantics for interrupted requests.

The Sidecar baseline is the checked-out source, not whichever development binary wins `PATH`. The research snapshot predates the remote-hosts merges; `main` now registers the `host` command group and the Phase A–C remote-host capability behind `features.SidecarRemoteHosts`, along with the headless `shell rename --target`, `shell send --target`, and `create worktree --plan` verbs Phase C added. Where those change a matrix row below, the row says so.

## The journeys this plan must make real

### 1. Start a helper agent without taking the user's focus

An agent in the current Sidecar shell creates a sibling terminal through the existing layout surface, starts Codex or Claude in that managed shell, and receives a stable target only after Sidecar has verified that the expected provider owns the pane and is ready for input:

```bash
created=$(sidecar create shell --split right --name reviewer --json)
target=$(printf '%s\n' "$created" | jq -r '.shell.session')
sidecar agent start "$target" --kind codex --timeout 30s
```

`agent start` never creates or moves layout. That separation is one of Herdr's strongest interaction decisions and fits Sidecar's existing ownership boundaries: `create shell` owns managed-shell creation and pane placement; `agent start` owns provider identity and readiness.

### 2. Coordinate work through lifecycle state

The caller prompts the helper, waits until it settles, and reads the result without scraping an arbitrary focused pane:

```bash
sidecar agent prompt "$target" "Review the current diff and report only actionable findings." --wait --timeout 2m
sidecar agent read "$target" --source recent-unwrapped --lines 120
```

If the agent is blocked before the prompt, Sidecar refuses without writing bytes. If the wait returns blocked, the caller inspects the current screen and uses validated logical keys only after deciding what the UI needs. The target remains pinned to the same tmux session, pane incarnation, and provider; a replacement occupant cannot satisfy the old wait.

### 3. Recover from a machine or tmux server restart

Before the restart, Sidecar has already persisted shell identity, cwd, pane trees, provider kind, and an exact provider-reported native session reference. On the next Sidecar launch:

1. The tmux server incarnation check proves this is a cold replacement rather than an ordinary Sidecar restart.
2. Sidecar paints its first frame before restore work begins.
3. Eligible managed shells are recreated under their same tmux session names and cwd, making existing `PaneLayoutJSON.Session` selectors valid again.
4. Project and global Sessions pane trees decode through `panecodec` and reattach to those sessions.
5. Shell-only restoration happens automatically; known agent sessions are either left ready for one-click/CLI resume, or resumed automatically when the user has explicitly selected the `auto` policy.

No arbitrary `--run` command, dev server, test watcher, or unstructured custom agent start string is replayed after a cold restart. A missing worktree is a refusal, not a fallback to `$HOME` or the main checkout: resuming into the wrong repository is worse than leaving a clearly recoverable row.

### 4. Upgrade Sidecar without dropping work

Replacing the Sidecar executable does not touch the default tmux server and does not stop agents, shells, compiles, or dev servers. The currently running Sidecar process may continue until the user relaunches it; a new Sidecar instance reattaches to the same tmux sessions. If tmux itself must be replaced, Sidecar reports that a cold restart is required and offers the restore preview. It never restarts, kills, or replaces the default tmux server automatically.

## Capability gap matrix

`Covered` means the checked-out Sidecar `main` already has the user/agent outcome. `Partial` means useful machinery exists but the Herdr journey is not possible end to end. `Gap` is work owned by this plan. `Other plan` and `Non-goal` keep the scope honest.

| Capability | Herdr v0.8.2 | Sidecar `main` | Decision / owner |
| --- | --- | --- | --- |
| Discover agent-facing commands and structured output | `herdr --help`; most control commands return JSON | `sidecar agents`, command help/JSON metadata, `--json` on agent-facing commands | Covered |
| Caller context | Injected workspace/tab/pane IDs; `--current` | `SIDECAR_SHELL`, shell name, project and shell selectors | Covered for Sidecar's simpler model; do not copy Herdr's hierarchy |
| Create an ordinary managed terminal | Workspace/tab/pane create and split | `sidecar create shell`, including `--split`, `--tab`, `--run`, `--type` | Covered |
| Create a worktree and agent shell | `herdr worktree` plus pane/agent commands | `sidecar create worktree --agent`, using Sidecar's setup/journal pipeline | Covered, but readiness becomes this plan's shared start core |
| Read and replace pane topology | Workspace/tab/pane commands | `sidecar layout get` / `layout apply` | Covered |
| Move an existing pane | `pane move` | `sidecar layout move` shipped | Other plan: [pane repositioning](../implemented/pane-repositioning.md), implemented |
| Start a known agent in an existing idle terminal | `agent start`, provider allowlist, readiness wait | `shell send --target --run` starts one headlessly (and remotely) but proves no provider identity or readiness | Gap narrowed to the readiness contract; `agent start` supersedes the raw send for agents |
| Address an agent independently of UI focus | Unique live name or pane ID | Phase C's `shell rename --target` / `shell send --target` resolve durable managed targets headlessly (`internal/cli/shell_target.go`) | Partial; agent commands reuse that resolver rather than inventing a second alias namespace |
| Query one agent's provider, lifecycle, freshness, and evidence | `agent get` / `list` | `agentactivity`, `agentstatus`, and `workspaceinventory` compute this for TUI rows | Gap only at the application/CLI boundary |
| Guarded prompt submission | `agent prompt`; blocked refusal; bracketed-paste-aware input | Interactive TUI input exists; no provider-aware headless prompt | Gap |
| Wait for lifecycle state | Event-driven `agent wait`; target occupant pinned | TUI polling sees working/blocked/done/idle; no targeted wait API | Gap |
| Read live terminal output by semantic agent target | `agent read`, visible/recent/recent-unwrapped/detection | tmux captures and screen model exist; no agent-facing read command | Gap |
| Send validated logical keys to an agent UI | `agent send-keys` | TUI maps keys through terminal input; no headless semantic target | Gap |
| Run/read/wait on an arbitrary non-agent terminal | First-class pane run/read/wait-output | Agent can already use `tmux` after `sidecar shell list --json` | Deliberate non-goal: tmux owns raw terminal control; Sidecar documents the recipe instead of wrapping it |
| Global attention view | Sidebar rollups and done/unseen state | Global Agents lanes, blocked attention, done TTL, notifications | Covered with different semantics |
| Native conversation transcripts | Screen reads; alternate-screen paging for supported agents | Local adapter stack can return structured session messages | Sidecar advantage; expose only when an exact live-session binding exists |
| Persist project/global pane trees across Sidecar restart | `session.json` | Per-surface `PaneLayoutJSON`, `panecodec`, `state.json`, live-leaf session selectors | Covered |
| Preserve managed shell definitions across tmux death | Snapshot restores pane cwd/shape | `shells.json` v2, tombstones, orphan rows, manual recreate | Covered as data durability; Partial as automatic reconstruction |
| Reconstruct layout after a cold tmux restart | Automatic fresh shells in saved cwd | Terminal splits may recreate; managed shells otherwise remain offline until recreated | Gap |
| Bind a live pane to an exact native agent session | Official integrations report ID/path into pane snapshot | Conversation adapters discover sessions; no authoritative live-pane binding | Gap |
| Resume exact agent conversations after cold restart | Default on for supported official integrations | Manual Conversations UI builds resume commands for some adapters; no automatic binding or cold restore | Gap |
| Persist terminal screen history across cold restart | Optional `session-history.json`, off by default | tmux scrollback dies with the server; Sidecar stores no output bodies | Non-goal for first delivery; transcripts are the better agent-history source and shell output may contain secrets |
| Live server binary handoff without process loss | Experimental Unix SCM_RIGHTS transfer by the PTY owner | Sidecar does not own tmux PTY FDs | Non-goal; impossible through supported tmux interfaces |
| Local application update without process loss | Live handoff may replace Herdr server | Sidecar binary replacement leaves the external tmux server and children untouched | Covered already; document and test the release path |
| Remote observation/control | Native remote Herdr flow | Shipped by the Sidecar remote-hosts plan (Phases A–C) behind a flag | Other plan; agent verbs ride its one-shot CLI seam when they exist |
| Host plugins | Herdr plugin manifest/runtime | On-hold Herdr-plugin support plan | Other plan |

## Product boundary: what Sidecar owns

Sidecar remains a presentation-layer tool over files, git, tmux, and harness CLIs. This plan adds a CLI only where Sidecar has accumulated rules and durable state of its own:

- Sidecar owns managed-shell identity, display name, cwd, provider preference, tombstones, and layout attachment.
- Sidecar owns its cross-provider lifecycle vocabulary and conservative blocked/done/freshness rules.
- Sidecar owns the refusal that says a prompt may not be sent to a blocked or replaced agent, and the contract that a start does not succeed until the expected provider is ready.
- Sidecar owns its exact binding between a managed shell and an officially reported provider session reference, plus the policy deciding whether that reference may be resumed after a cold restart.

Sidecar does not own arbitrary tmux pane input, command execution, output matching, or process supervision. An agent that wants to run a command in a raw terminal can use tmux. The remote-hosts Phase C added `sidecar shell send --target --run/--type`, and it stays on the right side of this line for a specific reason: it refuses any session that is not a live Sidecar-managed record for the resolved project, and refuses one recorded on a different tmux server — it is Sidecar's ownership rules wrapped around a send, not a raw pane wrapper. `shell wait-output` and output matching remain non-goals. The new agent commands earn their place because they enforce provider-aware rules that neither raw tmux nor `shell send` can.

Sidecar also does not own the workflow being coordinated. A caller may use `td`, `tasks`, a markdown plan, another harness, or no task engine at all. This plan does not choose planners, reviewers, models, timeouts for an entire workflow, merge policy, or retry topology.

## Agent command contract

### Commands

```text
sidecar agent list [--project PROJECT] [--json]
sidecar agent get [TARGET] [--json]
sidecar agent start [TARGET] --kind KIND [--timeout DURATION] [-- AGENT_ARG ...]
sidecar agent prompt [TARGET] TEXT [--wait] [--until STATUS]... [--timeout DURATION] [--json]
sidecar agent wait [TARGET] [--until STATUS]... [--timeout DURATION] [--json]
sidecar agent read [TARGET] [--source visible|recent|recent-unwrapped|detection|transcript] [--lines N] [--ansi] [--json]
sidecar agent send-keys [TARGET] KEY [KEY ...] [--json]
sidecar agent report-session --kind KIND (--id ID | --path ABS_PATH) [--source SOURCE] [--json]
```

`report-session` is public because provider hooks need a stable executable boundary, but it is described as an integration command rather than a general coordination command and is omitted from the short `sidecar agents` happy-path list. Its help names the trust rules and the current shell requirement.

### Target resolution

The target is a Sidecar-managed shell, not a tmux pane number and not a second agent-alias database:

1. Omitted target inside a managed shell resolves `SIDECAR_SHELL` plus the current tmux namespace.
2. An explicit tmux session name resolves exactly within its namespace/project.
3. A display name is accepted only when unique under the selected project/host.
4. Outside a managed shell, `--project` or a globally unique explicit target is required; no command falls back to the user's currently focused TUI row.
5. The target result carries `HostID`, project key, namespace, tmux session name, pane ID, and server/pane incarnation. Local v1 uses the local host; the identity is host-shaped now so remote support is additive.

The existing shell display name is the human coordination alias. `sidecar shell rename reviewer` already persists and advertises it. A separate ephemeral agent-name namespace would create two names for one Sidecar unit and is not justified.

### `agent start`

- Requires an existing, live, Sidecar-managed target whose foreground process is its interactive shell. A running command, editor, agent, copy mode, or ambiguous process is `agent_pane_busy`; `--force` is intentionally absent.
- Resolves the provider from the shared catalog and constructs a structured argv. Provider arguments after `--` stay separate argv entries; they are never concatenated into a shell command for persistence or resume.
- Sends the launch through the terminal adapter, pins the pane incarnation, and waits until the expected provider is positively identified and reaches `idle` or `done`. `blocked` returns `agent_not_ready` but keeps the target inspectable. A different provider is `agent_kind_mismatch`; process exit is `agent_start_failed`; timeout is bounded and explicit.
- Records provider kind and structured launch metadata only after the command is accepted. It does not claim a native session until an official integration report arrives.
- Returns the same `Agent` JSON shape `list`, `get`, `prompt`, and `wait` use.

`sidecar create shell --agent KIND` becomes the convenient composed path: create a managed shell through the current core, then call the same start service and return only after readiness. `sidecar create worktree --agent KIND` is changed from "command bytes were sent" success to the same readiness contract. Existing `--run` remains ordinary, unclassified command execution and does not imply restore eligibility.

### `agent get` and `agent list`

The machine contract includes:

```json
{
  "target": {
    "host": "local",
    "project": "sidecar",
    "session": "sidecar-sh-sidecar-4",
    "name": "reviewer"
  },
  "agent": {
    "kind": "codex",
    "status": "blocked",
    "freshness": "current",
    "attention": true,
    "evidence": "codex.approval.command",
    "changedAt": "...",
    "capturedAt": "...",
    "interactiveReady": false,
    "sessionRef": {"kind": "id", "reported": true}
  }
}
```

Session reference values are included only for the current shell's own query or an explicit `--include-session-ref` form; ordinary list output reports capability/presence without spraying conversation identifiers across logs. Human output remains compact and `--json` carries the full stable schema.

### `agent prompt` and `agent wait`

- `prompt` accepts `idle`, `done`, or `working`; it refuses `blocked`, unknown identity, stale status, dead target, or replaced occupant before sending input.
- Prompt input uses the same bracketed-paste-aware, ordered send path as the embedded terminal. Extract that behavior from the TUI path rather than writing a second encoder in the CLI.
- `--wait` combines submission and wait under one pinned target, avoiding a race between two commands. Default settled states are `idle`, `done`, and `blocked`; repeated `--until` narrows or widens the accepted set explicitly.
- A prompt begun from a non-working state must produce an observed lifecycle change within five seconds or returns `agent_prompt_stalled`. It does not pretend to identify an agent turn when the target was already working; completion of the existing turn may satisfy the wait, and help text says so.
- Waits are driven by a targeted terminal observer, not the TUI's 5/10/30-second inventory cadence. A polling fallback is allowed for the steel thread, but Phase 0 measures its spawn/CPU cost and the implementation moves to the existing control-mode event stream if polling cannot meet the latency/cost gate.
- Timeout and transport failures are JSON errors on stderr with exit 1; CLI usage is exit 2. There is no implicit timeout.

### `agent read` and `agent send-keys`

- `visible`, `recent`, `recent-unwrapped`, and `detection` are passive terminal snapshots. `recent-unwrapped` joins soft wraps for logs and agent answers. `--ansi` preserves styling only where the source has it.
- Reads never scroll or otherwise manipulate the agent's alternate-screen UI. When terminal history is insufficient, `--source transcript` uses the adapter stack only if the managed shell has an exact provider-reported session binding. It returns structured messages or a clear `transcript_unavailable`; it never guesses the newest same-cwd session.
- `send-keys` accepts a documented logical-key allowlist (`enter`, `esc`, arrows, page keys, `ctrl+<key>`, and other keys the shared terminal mapper can encode). It validates the complete list before writing any bytes. Prompt text goes through `agent prompt`, not a raw string-key escape hatch.
- If a wait or prompt returns blocked, the documented sequence is read first, decide, then send keys. Sidecar does not auto-answer approvals or questions.

## One application core

```text
sidecar agent CLI ─┐
create shell/worktree ─┼─> internal/agentcontrol.Service
future TUI actions ────┘           │
                                   ├─ target resolver (managed shells, host-scoped identity)
                                   ├─ provider registry (launch/resume argv, capabilities)
                                   ├─ activity/status resolver (existing agentactivity + agentstatus)
                                   ├─ session binding store (shellstate v3)
                                   └─ Terminal adapter
                                      ├─ local tmux
                                      └─ future remote adapter (hosts.RunSidecar one-shot verbs)
```

### Package responsibilities

- **`internal/agentcontrol`** owns typed commands/outcomes, target pinning, shell-readiness checks, prompt/wait refusal policy, lifecycle monitoring, and read/key operations. It imports no Bubble Tea package and no conversation plugin.
- **`internal/agentsession`** owns `SessionRef` validation (`id` or absolute `path`), official-source trust, provider resume planning, deduplication keys, and cold-restore decisions. It works on structured values and argv, not shell strings.
- **`internal/agentcatalog`** remains the single family catalog but grows capability-bearing provider entries or small provider adapters: canonical ID/aliases, launch argv builder, resume argv builder, supported session-ref kinds, skip-permissions argument, and the provider metadata consumed by the lifecycle integration manager. The current resume switch in `internal/plugins/conversations/view_content.go` moves here; the Conversations UI, restore coordinator, CLI, and [lifecycle-hook plan](notification-agent-lifecycle-hooks.md) become clients of the same catalog rather than defining parallel provider registries.
- **`internal/agentactivity` and `internal/agentstatus`** keep their existing evidence and presentation jobs. Control code consumes them; it does not add a second lifecycle classifier.
- **`internal/shellstate`** remains the only writer of managed-shell persistence. No command edits `shells.json` directly.
- **Terminal adapter.** The default implementation resolves the tmux session's sole managed pane, foreground process identity, capture sources, control-mode output events, ordered paste, and logical keys. Tests use a fake adapter. The remote-host plan supplies the second implementation through its shipped transport: the proxied control-mode channel for terminal I/O and one-shot CLI verbs for commands.

Do not turn the conversation-history `adapter.Adapter` into the live terminal adapter. Conversation stores and terminal control are independent seams. `agentsession` may query a matching history adapter after an exact binding exists, but a missing/disabled Conversations plugin must not disable start, prompt, wait, or restore metadata.

## Persistence and restore design

### Keep the existing stores; do not add a competing `session.json`

Sidecar already has the state Herdr puts in one file, separated by ownership:

- `shells.json` is durable managed-shell identity and recreation data.
- global/project `state.json` holds selection and per-surface `PaneLayoutJSON` trees.
- tmux holds the live processes, PTYs, and scrollback.

Adding a fourth aggregate `session.json` would create two authorities for shell identity and pane layout. Evolve the existing stores instead. `shells.json` moves from schema v2 to v3 with additive structured agent/restore fields; `PaneLayoutJSON` does not change for this plan because it already stores tree shape, ratios, focus, cwd-owned surface identity, and tmux session selectors.

Proposed v3 shape, illustrative rather than a locked Go struct:

```json
{
  "tmuxName": "sidecar-sh-sidecar-4",
  "displayName": "reviewer",
  "namespace": "/private/tmp/tmux-501/default",
  "createdAt": "...",
  "workDir": "/Users/marcus/code/sidecar",
  "agent": {
    "kind": "codex",
    "launchArgv": ["codex", "-m", "gpt-5.4"],
    "session": {
      "source": "sidecar:codex",
      "kind": "id",
      "value": "019f...",
      "reportedAt": "..."
    }
  },
  "restore": {
    "policy": "inherit",
    "eligible": true,
    "lastSeenIncarnation": "...",
    "lastSeenAliveAt": "..."
  }
}
```

Rules:

- Schema v3 keeps v2's newer-writer refusal. A v2 reader must never rewrite and drop v3 fields; compatibility tests exercise old/new binaries' allowed direction explicitly.
- `launchArgv` is recorded only for catalog-built provider launches. Existing arbitrary configured `agentStart` shell strings may still launch interactively, but they are not automatically replayable and are stored only as a display diagnostic if needed.
- Session IDs are capped, reject control characters, and are never interpolated into a shell string. Session paths must be absolute, normalized, and within a provider-approved store root before automatic resume.
- Only an installed official Sidecar integration source may set an auto-resumable reference. Same-cwd adapter discovery may propose a manual candidate but never marks it `reported` or auto-resumable.
- A new report replaces the prior reference only when it comes from the current pinned provider/process generation. Late hook output from an exited/replaced process is ignored.
- Session deduplication is global per host: one exact native session reference may resume into at most one managed shell. Duplicates restore as plain shells and report the conflicting target.
- Shell records are written on meaningful transitions—launch accepted, exact session reference changed, restore policy changed, confirmed live/dead incarnation transition—not on every output capture.

### Restore policy

Configuration exposes two independent choices:

```json
{
  "plugins": {
    "workspace": {
      "sessionRestore": {
        "recreateShells": true,
        "resumeAgents": "ask"
      }
    }
  }
}
```

- `recreateShells` defaults **true**. It recreates only interactive managed shells that were confirmed live in the prior tmux server incarnation, under their same name and existing cwd. It never replays arbitrary foreground commands.
- `resumeAgents` is `off | ask | auto` and defaults **ask**. `ask` paints the restored shell/layout and presents one grouped restore summary after the first frame; nothing paid or agent-mutating starts until the user confirms or runs the CLI. `auto` is explicit user authorization for exact, official session references only.
- A per-shell policy `inherit | shell | resume | never` exists in v3 so long-running servers, disposable helpers, and sensitive agent sessions can differ without changing the machine default. The TUI setting is added only after the headless policy is implemented and tested; the CLI can set it first.
- A missing cwd, deleted worktree, unavailable provider binary, invalid/stale reference, duplicate reference, or provider mismatch degrades to a visible offline/restored-shell result with an exact reason. No fallback directory and no fresh replacement conversation.

### Restore entry points

```text
sidecar session status [--json]
sidecar session restore [--dry-run] [--shell TARGET] [--agents] [--yes] [--json]
sidecar session policy [TARGET] [--shell|--resume|--never|--inherit] [--json]
```

- `status` and `restore --dry-run` are read-only and work without a running TUI. Their ordered plan names every shell as `reattach`, `recreate-shell`, `resume-agent`, `manual`, `skip`, or `refuse`, with the reason and whether external agent execution would occur.
- Plain `restore` follows configured policy. `--agents` requests eligible agent resumes; `--yes` is required when no TUI can confirm and the effective policy is `ask`.
- Automatic startup restoration calls the same planner/executor after first frame. It is not a hidden TUI implementation.
- The tmux session name is the idempotency key. Every step rechecks `has-session`, foreground occupant, server incarnation, and session binding under the shell manifest lock before mutation. A crash after session creation but before completion converges on retry rather than creating a second shell or agent.
- The restore executor never kills a conflicting live session. A name collision is a refusal shown to the user.

### Cold-restore ordering

1. Read state and shell manifests; validate schema versions; compute the current tmux server incarnation through `internal/tmuxserver`.
2. Paint the first frame. Startup tracing must show no tmux spawn, provider-store walk, or restore write before `first ready frame`.
3. Build a pure restore plan from the prior confirmed-live set, current tmux inventory, exact cwd existence, policy, provider capability, and dedupe set.
4. Recreate eligible shell sessions with `workspaceops` under the same names and environment identity. Never stop or restart the tmux server.
5. Let existing project/global pane decoding reattach by session name. Do not write a parallel tree or compositor.
6. Resume eligible exact agent sessions according to policy through `agentcontrol.StartResume`, then wait for provider identity/readiness with the same contract as `agent start`.
7. Persist one outcome per shell and surface a grouped summary. Failures are retryable and never prune shell records or pane layouts.

## Provider integrations and session identity

The steel thread supports Codex and Claude because Sidecar already has launch, activity, transcript, and resume knowledge for both. The interface must not hardcode those two:

1. Add `sidecar agent report-session` and the `agentsession` trust/validation core.
2. Install minimal provider hooks that call the command from inside the managed shell. The command derives the shell target from `SIDECAR_SHELL`; hooks do not receive a writable path to `shells.json`.
3. Record source/version so an outdated integration can be reported honestly and upgraded without changing the shell schema.
4. Expand to every catalog provider for which Sidecar can build and verify a native resume argv. Providers without native resume still gain start/get/prompt/wait/read and restore as plain shells.

Integration installation, status, versioning, safe configuration merge, and CLI/Configuration surfaces are controlled by [Deterministic agent lifecycle hooks](notification-agent-lifecycle-hooks.md). This plan contributes the session-reference validator and resume capability metadata to that shared application service. `sidecar agent integration status [--json]` reports provider, installed version, lifecycle authority, session-identity capability, and minimum version; session-only hooks report identity only and existing screen/process detection remains the status authority.

## Interaction with remote hosts

The local steel thread ships independently. [sidecar-remote-hosts.md](sidecar-remote-hosts.md) has since completed its mutation Phase C, and its shape settles how agent control travels: mutations are one-shot `ssh <target> sidecar <verb> --json` invocations over the existing ControlMaster (`hosts.RunSidecar`), not a request channel in `hostproto` — `hostserve` stays read-only by construction. That is exactly the seam this plan's commands were designed for. Every `sidecar agent` verb is headless, target-taking, and `--json` from birth, so remote agent control is transport plumbing plus host-scoped target identity, not new protocol work:

- `agent list/get/start/prompt/wait/read/send-keys` run on the host as ordinary CLI invocations through `hosts.RunSidecar`, following the exit-code discipline the Phase C verbs established (2 usage/version skew, 5 value rejected) and decoding results through its banner-tolerant result decoder. An older host answers a verb it lacks with a usage error the viewer already renders as version skew — capability negotiation falls out of the exit-code contract.
- Waits are the one shape that strains a one-shot invocation. `agent wait --timeout` runs as a bounded invocation whose deadline the caller owns, matching `hosts.RunSidecar`'s deadline discipline; a resident subscription channel is not built unless real usage shows the bounded form is insufficient.
- Target identity includes `HostID` from the beginning, so local and remote shells with the same tmux name cannot collide.
- Session references stay on the host that owns the provider store. The viewer receives presence/capability by default; exact IDs/paths cross SSH only for an explicit operation and are not persisted into the viewer's local `shells.json`.
- Cold restore executes on the host. A remote viewer may request/observe it, but it never reconstructs the host's state locally.

## Live handoff: explicit non-goal and operational posture

Herdr can attempt live server handoff because Herdr itself holds each PTY master FD and can pass those descriptors to a replacement process. Sidecar is a tmux client. It can capture bytes, send input, and recreate sessions, but it cannot obtain or transfer tmux's PTY ownership through a supported interface.

Therefore:

- **Sidecar updates:** supported without process loss today. Installation replaces an executable on disk; the old Sidecar process and the independent tmux server continue. Relaunch reattaches.
- **tmux package updates:** leave the old server running until a user-chosen maintenance point. Do not call `kill-server`, restart the default server, or claim the update is complete if the running server remains old; report the distinction.
- **tmux server restart/reboot:** cold reconstruction only. Agents with exact native session references may resume conversations; arbitrary compiles, servers, editors, and in-memory shell jobs are lost and are never described as preserved.
- **Future revisit trigger:** only a supported tmux upstream handoff/export API, or a deliberate Sidecar-owned PTY runtime, changes this conclusion. Neither is proposed here.

## Work sequence

### M0 — Contract spike and current-state fixtures

- Capture the current `sidecar agents`, create-shell/worktree JSON, shell manifest v2, project/global pane-layout round trips, and activity states as compatibility fixtures.
- On an isolated tmux server, prove the reliable shell-readiness signal on macOS and Linux: shell foreground process group, current command, pane ID/incarnation, copy mode, and a busy foreground process. A false ready verdict is a hard stop.
- Prototype a targeted lifecycle watcher after one prompt. Compare bounded polling against the existing control-mode event stream for transition latency, process spawns, idle CPU, and behavior under an output burst. Record the choice in this plan before M1 implementation.
- Prove bracketed-paste-safe prompt submission through the extracted terminal sender, including multiline text, Unicode, shell metacharacters, and a prompt whose contents resemble tmux format syntax.
- Define and freeze the `Agent`, target, status, and error JSON schemas. Error codes include `agent_not_found`, `agent_pane_busy`, `agent_kind_mismatch`, `agent_not_ready`, `agent_start_failed`, `agent_blocked`, `agent_prompt_stalled`, `agent_replaced`, `transcript_unavailable`, `timeout`, `transport_failed`, and the rollout-gate code `feature_disabled`.

**M0 observer decision (measured 2026-08-30):** `TestM0ObserverPollingVersusControlManagerMeasurement` ran both candidates against the same private tmux server and a 250-line output burst. At a 100 ms polling cadence, a 300 ms idle window spawned 6 tmux children and consumed 17,640 µs of child CPU; the burst was observed in 110 ms and required 2 more children. One existing `tty.ControlManager` client produced 0 idle snapshots, 0 additional spawns, and 0 µs of measured CPU during the same 300 ms idle window; it observed the burst in 42 ms with 0 additional spawns. The test also proves the control client drains the burst and closes through the subscription/manager lifecycle. Decision: use the existing pooled `ControlManager` for sustained targeted prompt/wait observation in M2; its event-driven latency and zero idle spawn cost are materially better, and a second read-only tmux client is not justified. M1's start-only wait may retain 100 ms bounded polling because it exists only between an explicit launch and its explicit timeout, has a simple pinned-occupant preflight, and leaves no resident idle poller; it is not acceptable for general lifecycle watching.

**M0 compatibility fixtures:** `TestM0AgentDiscoveryCompatibilityFixture` pins the agent-facing discovery lines, `TestM0CreateJSONCompatibilityFixtures` pins create-shell/worktree JSON, `TestM0VersionTwoManifestCompatibilityFixture` pins `shells.json` v2, `TestWorkspacePaneLayoutJSONRoundTrip`, `TestSessionsPaneLayoutsRoundTripAndSkipUnchanged`, and `TestDecodeEncodeRoundTripStable` pin project/global pane-layout persistence, and the provider fixture suites under `internal/agentactivity/testdata` plus their detector/tracker tests pin current activity states. The new `TestAgentAndErrorJSONContracts` and `TestStatusAndErrorCodeVocabularyIsFrozen` fixtures freeze the M0 target, `Agent`, status, and error envelope rather than deriving a new wire shape from the CLI renderer.

**Exit gate:** one fake provider driven through real isolated tmux reaches shell-ready → start → detected idle → prompt → working → done/blocked → read, with the pane target pinned throughout and zero access to the default tmux server or real Sidecar state tree.

**Implemented proof:** `TestIsolatedTmuxFakeProviderSteelThread` exercises that complete journey through `agentcontrol.Service` and `LocalTerminal` on the package's private tmux server, including exact multiline Unicode, shell-metacharacter, and tmux-format-like paste contents. The target's namespace, server incarnation, pane ID, and pane PID remain pinned through every transition. `TestIsolatedTmuxRefusesBusyForegroundAndCopyMode` proves the real tmux preflight rejects both a foreground `sleep` and copy mode. The Linux-only `TestForegroundShellReadyLinuxFixtureMatrix`, executed in a Linux container as well as cross-compiled from macOS, proves the sole-shell positive case and foreground-command, shared-helper, unknown-process, and current-command-mismatch refusals against a fixture `/proc` tree.

### M1 — Agent query and start steel thread

- Add `internal/agentcontrol` with injected terminal/clock/provider dependencies and a local tmux adapter.
- Extract target resolution from the existing shell CLI paths into one host-shaped resolver. Add `agent list`, `agent get`, and `agent start` with JSON/human output and gendoc metadata.
- Extend `agentcatalog` with typed launch capabilities; route workspace/TUI start, `create worktree --agent`, and new `create shell --agent` through the same service. Delete readiness-free command-send paths where they are no longer needed; keep ordinary `--run` separate.
- Reuse `agentactivity`/`agentstatus` for provider and lifecycle truth. Do not let launch preference claim the live provider before process evidence.
- Add only provider-neutral TUI affordances needed to show start/refusal outcomes; do not add an orchestration plugin.

**Exit gate:** from a Sidecar-managed shell, one command creates a sibling shell and starts a fake provider; `agent start` returns only at ready. Codex and Claude live opt-in proofs confirm the expected process and idle state, with no paid prompt submitted.

**Implemented proof:** project Workspace, global Sessions, `create shell --agent`, `create worktree --agent`, and `sidecar agent start` now converge on the same service and typed launch catalog. `TestLiveProviderStartGet`, run with `SIDECAR_LIVE_AGENT_PROVIDERS=codex,claude`, launched Codex CLI 0.151.0 and Claude Code 2.1.220 on the private tmux server, observed each at an interactive idle composer, and passively fetched the same pinned target without submitting a prompt. Codex's stable no-marker composer is reported as positively identified inferred idle, never as a manufactured completion.

### M2 — Prompt, wait, read, and logical keys

- Extract ordered bracketed-paste and logical-key encoding into the terminal adapter shared by TUI and CLI.
- Add `agent prompt`, combined prompt+wait, standalone `agent wait`, passive `agent read`, and validated `agent send-keys` with pinned-occupant semantics.
- Add the targeted observer chosen in M0, with cancellation/timeout cleanup and no leaked control clients or goroutines.
- Add exact-transcript reads behind an injected session-message reader, disabled until M3 supplies an exact binding.
- Update `sidecar agents`, CLI reference, AGENTS guidance, and a repository skill based on the Herdr skill's safe sequence: discover, create layout separately, start, prompt/wait, read before keys, preserve focus, never close a target the caller did not create. The tracked skill source remains canonical and any harness mirrors are generated/copied by the repository's existing skill workflow.

**Exit gate:** two isolated managed agents run concurrently; the caller prompts each, one returns done and one blocked, waits cannot be satisfied by replacement processes, and reading/sending keys to the blocked agent affects only the named shell. A `tmux-drive.sh` demo puts both panes in front of the user without stealing focus during creation.

**Implemented proof (2026-08-30):** `TestTwoIsolatedAgentsPromptWaitReadAndKeysInvolveOnlyTheirOwnShell` runs both agents concurrently on the package's private tmux server, settles one at `done` and one at `blocked`, reads the blocked one, answers it with logical keys, and proves the untargeted shell's screen is byte-identical before and after. `TestWaitCannotBeSatisfiedByARealReplacementOccupant` hands the pane to a `respawn-pane -k` process that prints the exact marker the wait is looking for and the wait returns `agent_replaced`. `TestObserverLeavesNoControlClientOrGoroutineBehind` exercises the timed-out, cancelled, and satisfied exits from the observer and proves no control client or goroutine survives.

The ordered input encoding lives in `internal/tty/agentinput.go`: `EncodeLogicalKey` builds a `tea.KeyPressMsg` and hands it to the existing `MapKeyToTmux`, so a headless key and a typed key are the same bytes by construction — pinned in both directions by `TestEncodeLogicalKeyMatchesTheEmbeddedTerminalEncoding`. `PromptSteps` is the ordered paste-then-Enter submission both the TUI launch path and `agent prompt` use.

The observer follows M0's decision with one split: the pooled `tty.ControlManager` is the *signal* that decides when to look, and `Inspect` remains the *truth* that decides what is so. Control events carry the screen, title, and current command but not pane identity, death, copy mode, or the server, so every verdict is confirmed by a full `Inspect` before it is returned and a slow verification heartbeat runs regardless of signals.

**Correction found in M2:** the M1 occupant pin compared `ServerIncarnation`, whose string embeds the socket's ctime. tmux rewrites its socket's permission bits whenever the set of attached clients changes, so attaching M2's own control-mode observer bumped the ctime and made every observed target report itself replaced the instant it began being observed. `Target` now carries `ServerPID` and `sameOccupant` compares that; the incarnation stays as reported evidence about the socket, and is no longer compared.

### M3 — Exact session reporting and unified resume registry

- Add `internal/agentsession`, v3 shell manifest fields, `agent report-session`, generation fencing, validation, redacted list behavior, and global deduplication.
- Move every resume command builder out of the Conversations plugin into the provider registry as structured argv. The optional Conversations UI consumes the registry and keeps its current user-confirmed behavior.
- Build and test Codex and Claude session-report mappings first through the shared lifecycle integration assets; then add providers with proven native resume commands one adapter at a time. `sidecar agent integration status` reports unsupported/missing/outdated honestly.
- Add an exact-bound transcript reader that resolves the bound session through the provider's existing history adapter without constructing the Conversations plugin.

**Exit gate:** a fake hook can report, rotate, clear, and attempt a stale late update; only the current process generation wins. Real Codex and Claude sessions bind to the exact current conversation and `agent read --source transcript` returns that conversation, not the newest other session in the same cwd.

**M3 implemented 2026-08-30 (`td-8ec2cc`); exit gate passed live on both halves.**

`internal/agentsession` owns the rules as values: reference validation (byte caps, C0/C1/DEL rejection, a character set containing no path separator and no shell metacharacter, absolute and normalized paths confined to per-provider approved store roots), official-source trust, generation fencing, dedup identity, and structured resume planning. It imports no tmux, CLI, or provider-config package, so every rule is testable without a terminal. `shells.json` is schema v3: `agent` and `restore` are additive pointer objects, so a v3 rewrite of a record that has never run an agent is byte-identical to what v2 wrote, and v2's newer-writer refusal is preserved — which matters more here than it did for tombstones, because a v2 binary rewriting a v3 file would drop a session reference and the symptom would not be an error but a restore that quietly declines to resume. `shellstate` remains the only writer, and the fence is evaluated *inside* the manifest lock: it is a read-decide-write, so two concurrent hook processes evaluating it outside the lock would both believe they won.

`sidecar agent report-session` reads the provider's hook payload as bounded JSON on **stdin**, so the installed hook command is fixed argv with no provider data interpolated into it — the shape the [lifecycle plan](notification-agent-lifecycle-hooks.md) asks for, and the reason no interpreter or script asset is needed for Codex or Claude. It decodes only four fields, so prompt and tool content cannot reach a record even by accident; it suppresses sub-agent events; and its result never carries the reference value. Redaction is asymmetric by design: `kind` and `reported` answer "can this be resumed and did an official integration vouch for it", while the value appears only for a shell's own query or `--include-session-ref`, because list output lands in logs and CI artifacts and a conversation identifier written there cannot be unwritten.

Every resume command builder moved out of `internal/plugins/conversations/view_content.go` into `agentcatalog` as structured argv with canonical aliases, and the plugin file no longer imports the workspace plugin at all. The agent-launch path now has exactly two argv-to-shell renderers, both in the catalog: `ShellCommand` always quotes and is execution-only, `DisplayCommand` quotes only entries containing a character a shell acts on. (`hosts/ssh.go` has its own renderer for a different transport, and `workspace/env.go`'s `shellQuote` is a single-value quoter for `export K=V` rather than an argv renderer; neither is in this path.) `agentcontrol.quoteArgv` delegates to the first. `internal/agenttranscript` implements the `TranscriptReader` seam M2 left injected, over the existing adapter stack and without constructing the Conversations plugin.

**A defect the proof found that no test would have.** The generation fence compares what the reporting hook derives about itself against what currently occupies the pane. `lifecycleenv.Context.ProcessGeneration` *falls back* to the pane's root process when it cannot walk from the hook to the pane, and `LiveProviderGeneration` returns the pane's root process when nothing is running there. Those are exactly the two conditions that hold for a hook left behind by a provider that has exited: it has been reparented away from the pane, and the pane is empty. Both sides fell back, compared equal, and the late report was accepted — in precisely the case the fence exists to reject. `ReportingProviderGeneration` is the same walk with no fallback: a reporter that cannot prove which provider it belongs to is refused. Two fallbacks agreeing is not evidence. `TestTheStrictGenerationRefusesWhereTheLenientOneFallsBack` pins the difference rather than the fix.

**Exit gate, first half (fake hook, live).** On a sandbox isolating HOME, all four XDG roots, `TMUX_TMPDIR`, `-config`, and `SIDECAR_ISOLATED_STATE=1`, a fake provider owning a real Sidecar-created pane called the real CLI as its own child: report bound `sess-alpha` at v3 with `reported=true`; an identical replay left the manifest byte-identical; a rotate moved it to `sess-beta`; an orphaned reporter armed inside the provider and fired six seconds after the provider was killed was refused with `stale_generation` and left the manifest byte-identical; a new provider generation took the pane over and rotated to `sess-gamma`; a clear from the live generation unbound it.

**Exit gate, second half (real providers, live).** Two managed shells in **one** working directory, per provider. Claude Code 2.1.220 bound `83a5396b…` and `d7c2d784…`; the bound shell's transcript is the **older** of the two files on disk and the newest in that cwd is the other one, so a newest-same-cwd guess would have returned the wrong conversation and the exact binding returned the right one. Codex CLI 0.151.0 bound `01a05614-0ca7…` and `01a05614-1cdf…` with the same ordering, and authenticated, so its bound transcript carries a real model reply rather than only the user's message. Redaction was checked on the same run: `agent get` default returned `{"kind":"id","reported":true}`, `--include-session-ref` added the value, and `agent list` carried no value for either shell.

**Codex needs three owned mutations, and the third one is a hash.** Codex records a per-hook `trusted_hash` in `config.toml`; without a valid one it refuses to run the hook and prompts instead. The algorithm is not documented, and an initial black-box attempt to recover it failed — sha256 over the command string, the state key, their concatenations, the hook object in several JSON encodings, and the whole `hooks.json` file all missed. It was then read out of the `codex-rs` source: the hashed value is a canonical serde encoding of the hook group, `{"event_name":"session_start","hooks":[{"async":false,"command":…,"timeout":…,"type":"command"}]}`, with serde's non-HTML-escaping string encoding. It is verified against two independent real pairs on this machine: the developer's own installed third-party hook, and — from the live proof — the hash **Codex itself wrote** after being asked to trust Sidecar's hook interactively. That second pair is the useful one, because it was produced by Codex for Sidecar's exact command rather than recovered from someone else's installation.

The behavior when the entry is absent was measured rather than assumed: Codex does not silently refuse, it shows an interactive "Hooks need review" prompt, and answering it makes Codex write the entry itself. So the trust entry is a convenience the installer can now provide correctly rather than a wall — and the fallback, if the encoding ever drifts, is a one-time prompt rather than a broken integration. That is the right shape for a security control to fail into.

**Codex and Claude ship at session-identity tier through the Phase C installer.** Both are new `agentintegration.Adapter` implementations, registered in `DefaultAdapters()`, and `agent integration list/status/install/update/repair/uninstall` and the Configuration route pick them up without provider branching. Their capability entries are corrected to what Sidecar actually ships — tier `session-identity`, covering `session_identity` only, asset version 1 — so a hook that reports which conversation is running can never be read as lifecycle authority; full lifecycle for these two is Phase D of the [lifecycle plan](notification-agent-lifecycle-hooks.md) and deliberately not here. Neither needs a script asset: because `report-session` reads the provider payload from bounded stdin, the installed hook command is fixed argv, so there is nothing to interpolate and no interpreter to depend on. Claude's entry carries a `matcher`, Codex's does not, matching each provider's schema. Verified live in the sandbox: `install --dry-run` produced an op list byte-identical to the real run, install left the sandbox `~/.codex` and `~/.claude` correct, uninstall backed up and removed only Sidecar's own content, and the developer's real provider trees were never written — their mtimes predate the work.

**Adapter-interface friction, since this was the interface's first test beyond OpenCode.** The ordered `Op` engine held without a new op kind, and the dry-run guarantee stayed structural. What did not fit is `Asset`: it is singular and its ownership model is a marker comment in the file's own bytes, which works for OpenCode because OpenCode's integration *is* one Sidecar-owned file dropped in a directory. Codex and Claude instead add an entry to a shared, user-owned config file that has no comment syntax, and Codex needs two files plus a hash. The adapters therefore overload `FileState.Owned` to mean "this file contains our entry" and return a synthetic `Asset` that nothing consumes, and ownership becomes a content rule — the entry whose command invokes `sidecar agent report-session` — rather than a marker. That rule is test-pinned against lookalikes, but it is a second ownership model living behind an interface that only describes the first. Phase D should either make `Asset` plural and let an adapter declare its own ownership predicate, or admit entry-in-file integrations as a distinct adapter shape. One further limitation is recorded rather than fixed: `config.toml` is edited by a line scanner that refuses anything it cannot parse, including multiline strings, because adding a TOML dependency was out of scope for that change.

**The Codex config editor is a line writer with a parser as its oracle.** `config.toml` is a shared user file, so it is edited surgically to preserve comments and ordering rather than by serializing a parsed document. An independent review found that the line scanner *guessed* where its own contract said it refused: four legal TOML spellings — a quoted table name, a quoted key, an inline-array element line read as a header for want of bracket-depth tracking, and a spaced dotted header — were neither recognised nor refused, so the composer appended a duplicate table or key and produced a file Codex cannot parse, silently costing the user every Codex setting. A second finding: a state table's span ran to the next header or EOF and comments never closed it, and since Sidecar appends its table last, anything a user parked at the end of the file was destroyed on uninstall.

Both are fixed, and the shape of the fix is the durable part. `github.com/pelletier/go-toml/v2` is now a **validation oracle only** — read-only, never used to serialize, so format preservation remains the writer's job. The file is parsed before planning, and a file that is not valid TOML is refused rather than edited around. The composed output is parsed again and semantically diffed against the pre-image: every key the user owned must still be present and unchanged, Sidecar's own entry present or absent as intended, or the plan refuses and nothing enters the Op list. The scanner's own honesty is fixed too, so the oracle is a second line of defence rather than the only one, and a table's span now ends at its last real content so trailing comments belong to the file. `TestCodexOracleRejectsARewriteThatLosesAUserKey` is the proof the oracle is not decorative: it first asserts that every check the old scanner-only verifier made *passes* on a doctored image, and only then that the oracle rejects it and names the setting that would have been lost.

Verified live as well as in tests: on the sandbox, seeding a realistic user `config.toml`, installing, and starting real Codex 0.151.0 against the result gave exactly one `[features]` table and one `hooks` key, a clean start, and Codex displaying the user's own `model` setting — a duplicated table or key is precisely what would have stopped it starting. Parking a comment and a `[scratch]` table at EOF after install and then uninstalling left both intact.

**Two limits worth stating.** A `config.toml` containing a multi-line string is refused outright: the oracle can confirm such a file is valid, but the line scanner cannot safely locate headers when a `"""` block may contain text that looks like one, so Codex integration is unavailable for that user rather than merely careful. And the scanner now refuses any `key = value` line it cannot normalise, not only ones under `features` or `hooks`, which is stricter than necessary. Both fail in the safe direction — refusing to edit — and both are recorded rather than hidden.

**Two deviations from the illustrative v3 shape.** `restore.lastSeenIncarnation` is named `lastSeenServer` and holds the tmux server **pid**, following Phase B of the lifecycle plan: `tmuxserver.Incarnation.String()` embeds the socket's ctime, which tmux rewrites whenever the set of attached clients changes, so a field keyed on it would go stale the moment a user attached a client. The pid is stable for the server's lifetime and new after a restart. And `agent.kind` is stored alongside — not instead of — the existing v2 `agentType`: the older field stays authoritative for everything that already reads it, and the single writer keeps the two in step.

`restore` is defined and round-tripped but nothing writes it yet. Populating eligibility is the job of the component that consumes it, which is M4's planner; shipping the field now is what lets M4 be additive rather than another schema bump.

**Honest limits.** The copied OAuth credentials did not authenticate inside the sandbox HOME for Claude, so each Claude assistant turn is a 401 error string rather than a model reply. The gate does not depend on it — the two conversations are distinguished by their user messages, both transcripts are real files written by real Claude sessions, and the adapter read them through the real binding — but only the Codex half proves the path with a genuine model response. Dedup is implemented and unit-tested as a pure function over holders, and `report-session` reports a conflicting target, but nothing consumes the verdict yet because the component that would act on it is M4's restore planner. `--clear` is an addition to the command surface the plan sketched, needed because the exit gate requires a hook to be able to clear a binding.

### M4 — Cold shell/layout restoration

- Add the pure restore planner, `sidecar session status/restore/policy`, v3 per-shell policy, and executor over `workspaceops` plus `agentcontrol`.
- Run startup restoration asynchronously after first frame; show an `ask` summary through the existing notification/modal system and keep the CLI path authoritative.
- Recreate only previously confirmed-live managed shells under the same name and cwd. Let existing project/global pane persistence reattach them; add no new layout store.
- Resume exact sessions only after all refusals/dedupe checks and explicit policy. Store outcomes without pruning any shell or pane state.
- Cover crash/retry points after plan, after shell creation, after layout attachment, after resume send, and while waiting for provider readiness.

**Exit gate:** on a fully isolated tmux socket and state tree, create a project surface and global Sessions surface with mixed passive panes, two managed shells, and one bound fake agent; terminate only the isolated tmux server; relaunch Sidecar; confirm first-frame timing, exact tree/focus/ratios, same session names/cwds, shell-only automatic restoration, ask-policy non-execution, confirmed resume into the exact conversation, and idempotent second restore. The default tmux server is never inspected or mutated.

### M5 — Remote adapter and rollout

- The remote-host plan's mutation seam exists (Phase C: one-shot CLI verbs over `hosts.RunSidecar`). Add the remote terminal/session adapters over it; keep exact session values host-local by default.
- Run the local/remote parity suite over start/get/prompt/wait/read/key behavior and restore-plan reporting.
- Keep agent control behind a default-off feature flag through M2; enable by default after the live provider matrix passes. Keep `resumeAgents=ask` even after rollout; `auto` remains explicit.
- Add release notes and a demo recipe. Move this plan to implemented only after local agent control, exact session binding, and cold restore all ship; remote support may remain a linked follow-on.

## Verification and acceptance evidence

### Contract and unit coverage

- Provider table tests: every catalog family has one launch builder; native-resume providers have one structured resume builder and allowed session-ref kind; aliases and display labels cannot drift.
- Target tables: current shell, explicit tmux name, unique/ambiguous display name, wrong namespace, same name on two hosts, replaced pane, dead session, missing project.
- Start tables: initial shell, shell still initializing, editor/command/agent busy, expected provider ready, wrong provider, blocked during startup, exit, timeout, replacement during wait.
- Prompt/wait tables: each starting lifecycle state, blocked preflight, stalled transition, already-working caveat, multiple `--until` states, cancellation, timeout, replacement, stale capture.
- Input tables: bracketed paste on/off, multiline/Unicode/metacharacters, every logical key, reject-one-reject-all validation, no partial send.
- Read tables: source selection, ANSI stripping/preservation, unwrapping, line bounds, alt-screen passive limit, exact transcript, missing/disabled adapter, mismatched session binding.
- Manifest tables: v2→v3 migration, newer-version write refusal, unknown fields, official/unofficial reports, ID/path validation, stale generation, duplicate refs, tombstones retaining agent/restore fields.
- Restore-plan tables: same live server (reattach only), absent/replaced server, prior-live eligibility, intentional tombstone, missing cwd, name collision, unavailable binary, policy matrix, duplicate session, partial failure, retry at every step.

### Real consumer proof

- `go test` focused packages during each milestone; full `go test ./...` and the repository's lint/format gates on the integrated candidate.
- `./scripts/tmux-drive.sh paths` before every real-app proof, followed by start/keys/snap/stop; both tmux server and Sidecar state paths must be private.
- A purpose-built reboot harness may kill only its named isolated tmux server and preserves its temp state directory between Sidecar launches. It must prove the server socket/incarnation changed before claiming cold restore.
- Live provider matrix: Codex, Claude Code, Cursor Agent CLI, Grok, and at least one hook-authoritative provider where installed. Start/get/status/read are non-mutating after launch; prompt/resume proofs require explicit operator opt-in because they can create paid or externally mutating work.
- Startup trace with multiple restore candidates proves first frame is not delayed.
- Remote proof, when available, runs the same JSON contract as one-shot `sidecar agent` invocations over `hosts.RunSidecar` against a real second machine and confirms a blocked prompt can be inspected and deliberately answered.

### Safety invariants

- Never stop, restart, kill, or replace the default tmux server. Tests clean up only named isolated servers/sessions they created.
- Never infer an exact agent session from "newest file in cwd" for automatic resume.
- Never resume into a missing/fallback cwd.
- Never interpolate session IDs, paths, prompts, or provider args into a persisted/replayed shell command.
- Never send prompt bytes to a blocked, stale, ambiguous, or replaced target.
- Never let a restore failure delete a shell record, tombstone, pane layout, or conversation record.
- Never describe cold reconstruction as process preservation, or a Sidecar executable update as a tmux server handoff.

## Deferred work and revisit triggers

- **Persisted terminal screen bodies:** defer. Revisit only if transcript coverage and shell-only reconstruction leave a demonstrated recovery gap worth the secret-bearing storage and retention policy.
- **Sidecar-owned orchestration workflows:** defer indefinitely. A real demand should first prove that CLI primitives plus `td`/harness orchestration are insufficient; any future workflow engine is a separate product plan over this core.
- **Raw shell pane wrappers:** do not build while tmux remains the owner and its CLI is available. Revisit only if remote transport makes raw tmux unreachable to agents and a typed host protocol becomes the actual owning boundary.
- **Live tmux handoff:** no planned work. Revisit only on a supported tmux export/handoff interface or an explicit decision to own PTYs.
- **Automatic provider integration updates:** start with explicit status/install. Revisit background updates only after version drift causes observed restore failures and the security/update policy is settled.

## Open questions to settle in M0

1. **Targeted observer implementation.** Can the existing control-mode manager be extracted without inheriting TUI geometry ownership, or is a smaller read-only control client warranted? Decide from the latency/spawn measurements, not package aesthetics.
2. **Exact transcript output shape.** Reuse the adapter `Message` representation directly or define a smaller stable agent-control projection. Prefer the smaller projection unless an existing public JSON contract already exists by M3.
3. **Prior-live marker source.** Confirm whether the existing tmux-server incarnation tracker plus shell liveness transitions can record eligibility without an extra process spawn or write amplification. If not, add the smallest manifest transition needed; do not introduce a second runtime ledger.
