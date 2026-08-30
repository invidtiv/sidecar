# Remote shells: freshness, deletion, visibility, and configuration

Status: **active, planned**, 2026-08-30

Source: note `nt-b4db4e` — seven observations from the first real use of remote shells after [Sidecar as its own remote host runtime](sidecar-remote-hosts.md) Phase C landed.

Related: [Sidecar as its own remote host runtime](sidecar-remote-hosts.md) is the parent plan. This document does not restate its architecture; it closes the gaps that only appeared once a human created, used, and tried to remove a remote shell.

Evidence: every claim below is cited to the code on `remotes-improvements` at `09d36cbd`. Where a cause is inferred rather than observed, the plan says so and makes the diagnosis a work item rather than an assumption.

## Decision first

The remote host feature works. What it does not yet do is *feel* like the local one. Five of the seven items in the note are the same defect wearing different clothes: **the remote side has no change signal, only a clock.** A local Sidecar learns that a shell was created from an fsnotify watch on `shells.json` and learns that one died from a reap sequence; the remote serve loop has neither, so it discovers both on a 60-second full-inventory tick and never removes a record at all.

So the shape of this work is: give `hostserve` the two mechanisms the local surface already has (a watch, and a reap), then treat the remaining items — delete, board parity, visual identity, configuration, docs — as ordinary feature work on top of a remote surface that is finally current.

Nothing here changes the transport, the protocol's direction of travel, or the "serve has no request channel" decision from Phase C. The mutation path stays one-shot `ssh <target> sidecar <verb> --json` invocations, and the freshness fix works precisely *because* it does: a remote `sidecar create shell` writes the same `shells.json` that the watch is watching, so the create and the row that proves it are one causal chain rather than two systems that have to be told about each other.

## The seven items, and what each one actually is

| # | Note | Diagnosis | Workstream |
| --- | --- | --- | --- |
| 1 | Creating a remote shell works but takes time to appear | `hostserve` re-reads durable state only every `InventoryEvery` = 60s (`internal/hostserve/serve.go:75, 285-291`). A new `shells.json` record is invisible until that tick. | A |
| 2 | `exit` in a remote shell eventually removes the row, and it is unresponsive meanwhile | Serve never reaps (`serve.go:15`). Liveness flips within one poll (5–30s) so the row goes dead quickly, but the *record* survives until the remote machine's own Sidecar reaps it. | A |
| 3 | Users should be allowed to delete remote shells | `remoteVerbs` admits only `rename` (`internal/overview/global_actions.go:94-96`), because delete had no host-side CLI verb. The gate is correct; the missing piece is the verb. | B |
| 4 | Remote shells do not appear in the Activity kanban | Remote workspaces *do* reach `syncBoard` (`internal/overview/model.go:1536-1551`), gated on `HasAgent()`. The reported gap is therefore either a plain shell behaving as local plain shells do, or a field lost in transit. Diagnose before fixing. | C |
| 5 | Remote shells should be visually distinct | `workspaceinventory.Item.HostID` already exists and already crosses the wire (`inventory.go:110`, `hosts/registry.go:421-422`); `listItem` simply drops it (`overview/workspaces.go:151-163`). | D |
| 6 | A configuration screen for remotes | `configui` has no remotes page (`internal/configui/pages.go:16-27`); `config.HostsConfig` is editable only by hand (`internal/config/config.go:44-81`). | E |
| 7 | Update the docs site | `website/docs` has no remote-hosts page. | F |

## Workstream A — a remote host that is current

### A1. Serve watches the state it reports

`hostserve` gains an fsnotify watch over the same paths the local browser watches for the same reason. `internal/overview/live_shells.go:17-44` states that reason in full — a second Sidecar writing into the same state tree is invisible until something forces a cycle — and remote creation is exactly that case, with the extra 60-second penalty of the inventory tick.

- Watch each configured project's `shells.json` with `livewatch.PathWatcher` (`internal/livewatch`), which already owns the quiet period, the latency cap, and descriptor return, and whose package doc records that it must never run on a startup path.
- A signal sets `fullInventory` for the next cycle rather than triggering an immediate collection of its own, so the burst coalescing and the single-flight discipline stay where they already are.
- The cycle's tail `select` (`serve.go:391-395`) gains the watcher channel beside `time.After(pollInterval(...))` and `ctx.Done()`, so a create wakes the loop instead of waiting out the poll.
- Worktrees are deliberately **not** watched, for the reason `live_shells.go:39-44` gives: one descriptor per file per watched directory on kqueue, growing with worktree count. The existing staggered sweep is the worktree answer; if remote worktree creation feels slow after A1, that is a separate measured decision, not a reflex.

### A2. Serve reaps dead shells, using the local reap and not a second one

Phase C's plan said reaping arrives "only by porting the overview's guards … never fresh logic" (`sidecar-remote-hosts.md`, work items). That is the constraint, and the way to honour it is to move the sequence rather than copy it: extract the decision half of `internal/overview/shell_liveness.go` into a state-free function a headless caller adopts unchanged, then call it from both the overview and serve.

The guards that must survive the move, each of which exists because of a real incident:

- **Never act on an empty pane listing.** `tmux kill-server` does not unlink its socket, so a dead server and a server with no sessions are indistinguishable by identity alone; acting on the empty listing is how td-8d18de destroyed six live shells.
- **Incarnation fencing.** A listing observed under one server incarnation may not be used to judge records under another.
- **Tombstones, never deletion.** Reaping writes a tombstone through `shellstate`'s flock + read-modify-write path so `sidecar shell restore` still works, on the remote host as on the local one.

This is the highest-risk item in the plan. It gets its own review pass and its own two-machine proof, and it lands after A1 so that a wrong reap is visible within seconds rather than a minute.

### A3. An exiting remote shell stops being interactive

The note's "not responsive" needs one observation before it gets a fix: whether the stale row is merely listed, or whether the preview terminal stays in interactive mode swallowing keystrokes after the remote session dies. Phase A recorded a closely related defect — a `tty.Model` made remote stayed remote forever, and input was silently swallowed — so the failure mode is known to be reachable here.

Reproduce first (`scripts/tmux-drive.sh` against an isolated remote, per the isolation discipline in AGENTS.md), then fix what is actually there: the preview must leave interactive mode and say why when its remote session goes away, exactly as the local path does.

## Workstream B — deleting a remote shell

### B1. `sidecar shell delete`

A new CLI verb, because the guard's own comment says a verb "becomes supported by gaining a host-side CLI verb and an entry here — not by relaxing the guard" (`global_actions.go:86-87`).

```
sidecar shell delete [--target SESSION [--project NAME]] [--json] 
```

It wraps `workspaceops.DeleteManagedShell(projectRoot, sessionName, namespace)` — already the exact function the Sessions browser calls (`overview/global_actions.go:24, 428`) — so the remote path and the local path are one implementation. `Mutates: true`, structured `--json` result with a `ValidRemoteResult()` discriminator like `remoteShellResult` (`overview/remote_actions.go:348`), and the same exit-code vocabulary `shell forget` uses.

This also closes a local parity gap: today an agent can `forget` a shell record but cannot delete a shell, though deleting one is a thing the human UI does.

Scope note: **shells only.** Worktree delete stays refused. It resolves a path against a git repository and carries branch-cleanup decisions, and the note asks for shells. `remoteVerbs` keeps refusing `merge` and `open` for the reasons already written there.

### B2. The Sessions browser offers it

- `remoteActionRefusal` learns that `delete` is permitted for `KindShell` and still refused for `KindWorktree`, so the footer stops offering what the confirmation would take back.
- `applyDeleteAction` routes a remote shell through `runRemoteSidecar` with the same incarnation and stale-reply guards rename uses (`hostReplyStale`, `remoteReplyDropped`, `remoteHostUnavailable` — `remote_actions.go:141-230`).
- The confirmation names the host. Deleting something on another machine should read as such.
- On success the row is dropped optimistically; A1's watch confirms it within the coalesce window, so the optimistic drop is a latency mask rather than a source of truth.

## Workstream C — the Activity board

Diagnose, then fix. Remote workspaces already reach `syncBoard`, and remote agent presentation already crosses the wire (`hostserve/serve.go:488-490`, `hosts/registry.go:444-445`), so one of three things is true, and which one decides the fix:

1. The observed shell had no agent, and plain shells — local or remote — are correctly absent from an agent board. Then the item is a parity question about plain shells generally, answered once for both surfaces or not at all.
2. `Provider` is empty on the remote item, so `HasAgent()` is false for a shell that does have an agent. Then the fix is in the remote collection path.
3. Something else drops the card between `eachHostWorkspace` and the lane.

Whichever it is, the workstream ends with a test that a remote agent-backed shell lands in the correct lane, sorted after local projects, because that is the assertion nobody can currently point at.

## Workstream D — a remote row that looks remote

`Item.HostID` is already present and already populated; only the projections ignore it.

- **List rows:** a host glyph plus the host name as a `NameMeta` chip in `listItem`, in a colour derived deterministically from the host ID so it is stable across restarts and consistent between the two projections. The gutter marker stays the agent's lane marker — status is what that gutter means, and overloading it with provenance would cost the thing it is for.
- **Kanban cards:** the same glyph and colour on remote cards in `cardLines`.
- **Colour:** drawn from the theme's palette rather than hard-coded, so it survives a theme switch and stays legible in light and dark. Deterministic assignment from the host ID, with the palette as the cycle.
- **Parity:** both projections change in the same commit. A visual distinction that lands in the list and not the board is exactly the bug the shared-catalog rule in CLAUDE.md exists to prevent.

## Workstream E — configuring remotes

### E1. The Configuration page

A new `PageRemotes` ("Remote Hosts") in the Sidecar group of `configui`, listing every registered host with its live health state and offering add, edit, remove, and enable/disable. The form fields are `config.HostConfig` exactly: ID, target, binary, config path, env, disabled — with the doc comments on that struct as the field help, since they already explain why reachability is *not* configured here.

- Writes go through `configui`'s existing Load→mutate→Save boundary; the surface keeps no copy of a setting.
- After a save the app already calls `SyncHosts()` (`internal/app/config_surface.go:476`), so a host added in Configuration connects without a restart. Verify this rather than assume it.
- The page is visible when the `sidecarRemoteHosts` feature flag is on, and says how to turn it on when it is off — a configuration surface that hides the thing being configured is how a flag becomes a secret.
- Health is read from the running registry, not re-probed by the page.

### E2. The agent's path to the same capability

Sidecar owns the host registry, so per the ownership test in the global design principles it owes a non-interactive path. `sidecar host` already exists as a group with `serve` and `probe` (`internal/cli/host.go:93-105`), so this is three siblings, not a new surface:

```
sidecar host list [--json]
sidecar host add <target> [--id NAME] [--binary PATH] [--config PATH] [--env KEY=VALUE]... [--disabled] [--json]
sidecar host remove <id> [--json]
sidecar host set <id> [--target ...] [--enabled|--disabled] [--json]
```

Validation and normalisation — ID defaulting to target, duplicate-ID refusal, env shape — live in one state-free function both the CLI and the Configuration page call, so the two surfaces cannot drift into accepting different things.

## Workstream F — documentation

`website/docs/remote-hosts.md`, linked from `intro.md` and from `workspaces-plugin.md`:

- What the feature is and the one-sentence architecture: Sidecar on both machines, SSH, no daemon.
- Prerequisites: `ssh <target>` already working, Sidecar installed on the host, tmux there, the login-shell PATH requirement and why `Binary` exists as the escape hatch.
- Turning the flag on; registering a host in Configuration and from the CLI.
- What you can do to a remote workspace: watch, open a live pane, type into it, create, rename, delete a shell — and what you cannot: merge, open-as-project, delete a worktree, with the reason.
- Every health state and its fix, matching `hosts.Health.Fix()` so the docs and the row say the same thing.
- Isolation and safety: `Env` for pinning a proof host to its own tmux server and state tree.

## Sequencing

A1 first — it is small, it is the fix with the widest blast radius on how the feature feels, and it makes every later workstream observable in seconds instead of a minute. A2 follows it for the same reason: a reap is far safer to review when its effects are immediately visible.

B, C, D, E and F are independent of each other and depend only on A1 for pleasant testing. E1 and E2 land together — a screen without its CLI sibling is the parity bug the design principles name.

## Exit gate

From a local Sidecar against a real second machine, with both default tmux servers and both real state trees provably untouched:

1. Create a remote shell; it appears in Sessions within the coalesce window, not on the next minute boundary.
2. Type `exit` in it; the row leaves within one poll, and the preview does not swallow keystrokes on the way out.
3. Delete a remote shell from the Sessions browser; the tmux session dies on the host, the record is tombstoned there, and `sidecar shell restore` on the host can still bring the record back.
4. A remote agent-backed shell appears in the correct Activity lane.
5. A remote row is identifiable as remote at a glance in both the list and the board.
6. A host added in Configuration connects without a restart; the same host can be added, listed, and removed with `sidecar host` and the two agree.
7. The docs page describes what the build actually does, including every refusal.

Plus the standing constraint from the parent plan: **no change to local behavior.** Flag off, nothing new runs; flag on with no host registered, no ssh child is ever spawned.
