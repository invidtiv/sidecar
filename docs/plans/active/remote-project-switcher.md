# Remote destinations in `@` and `W`

Status: **active; slices 0–3 implemented, and slice 4's Files half complete** on `remote-viewer-screen` (td-f7855c: td-823e94, td-72a679, td-90757e; slice 2.5: td-11d3d3, td-761cea; slice 3: td-af932a; slice 3 review cleanup: td-1d6735; Files: td-bc57bb, planned in [remote-files-plugin.md](remote-files-plugin.md)). Remaining: Git, td, Tasks, and Notes remoting, create shell/worktree from the bound workspace, and slice 5 (docs and isolated proof). **Created:** 2026-09-01 **Verified against the tree on 2026-09-01** after the Files slice.

Related: [Files on a remote-bound project](remote-files-plugin.md) is slice 4's Files half. [Sidecar as its own remote host runtime](sidecar-remote-hosts.md) is the transport and inventory stream. [The viewer owns the screen](../implemented/remote-host-viewer-screen.md) is the lease and `uirequest` announcement the bound project workspace now shares with Sessions. [Remote host content-pane parity](../implemented/remote-host-content-pane-parity.md) is the read path a remote-bound plugin uses. [Agent-facing project CLI](agent-project-cli.md) is the local `sidecar project` surface; it does not grow `--host` in this plan.

## Decision first

`@` and `W` list every project and worktree this Sidecar can actually enter. Local destinations paint on the opening frame. Destinations that live on a connected remote host append as that host’s serve snapshot is already in memory, and they never delay the local list.

Selecting `[aerie] Sidecar` does not focus a Sessions row and does not call `registry.Reinit` on a path that might exist on this disk. It binds **this** TUI as `ScopeProject` for that host’s project: `plugin.Context` carries `HostID`, the workspace plugin shows that host’s shells, content panes load through `contentpanes.SourceContext`, and the geometry lease for those tmux sessions is held here. A host agent that runs `sidecar open` is then talking to this project workspace, through the announcement seam in the viewer-screen plan, not only to Sessions.

Sessions remains the fleet. `@` is how you go to work in a project, including one that is not on this machine.

```text
@  /  W
        |
        +-- local rows (this frame, config.json / captured worktree inventory)
        |
        +-- remote rows (append from in-memory hostproto.Snapshot)
                |
                v
        ScopeProject bound to (HostID, ProjectKey, Worktree)
                |
                +-- workspace plugin  --> host inventory (already on the wire)   [slices 0–2]
                +-- live terminals    --> existing control-mode proxy             [slices 0–2]
                +-- Files / Git / td  --> refuse until slice 4                    [slices 0–2]
                +-- content panes     --> SourceContext with HostID               [slice 3]
                +-- lease + uirequest --> viewer-screen announcement, this TUI    [slice 3]
```

A same-named local checkout is a different destination. Reinit of `~/code/sidecar` because aerie also has a project named sidecar is the failure content-pane already named.

## User contract

| Gesture | Required result | Status |
| --- | --- | --- |
| `@` with no remotes connected | Unchanged: Overview (if available) plus local `config.projects.list`. | Implemented |
| `@` with aerie online | Local rows this frame. Then `[aerie] Sidecar`, `[aerie] td`, … as that host’s snapshot is present. Filter matches host id, project name, and path. | Implemented |
| `@` with aerie connecting / stale / unreachable | Those rows still exist, disabled, with the same health reason Sessions already shows. They are not omitted, and they are not switchable. | Implemented (`hostLastKnown` for `@`; Sessions still drops `hostResults` on `!Shows()`) |
| Enter on `[aerie] Sidecar` | Leave Sessions if you were there. Bind this TUI to that host project. Restore that host’s last worktree for that project if one was remembered. Navbar / title name the host. Toast: `Switched to [aerie] Sidecar`. | Implemented (bind, restore, navbar/title, toast, lease claim, instance `HostID`). |
| `W` while in local Sidecar, aerie online | Local worktrees this frame. Then `[aerie] Sidecar [[feature]]` for linked worktrees of the project aerie registered under the same *name* (decision 10). | Implemented. Host main checkout is not a W row; it remains the `@` destination. |
| `W` while already in `[aerie] Sidecar` | Aerie’s worktrees for that project, plus this machine’s worktrees of the same-named local project as unprefixed rows. | Implemented. Local rows come from the in-memory cache of that named config project; omitted rather than `git worktree list` on open. |
| Enter on `[aerie] Sidecar [[feature]]` | Bind to that host worktree. Plugins reinit against the remote context, not against a local path of the same name. | Implemented via `bindRemoteDestination`. |
| Files / Git / td / Tasks / Notes on a remote-bound project | Honest unavailable state naming the host, until that plugin’s remote slice. They must not walk this disk. | Implemented (Init/Start/View/Update/FocusContext). |
| Workspaces on a remote-bound project | That host’s shells and worktrees for the project, live panes through the existing proxied control channel. | Listing and live attach implemented. Create shell/worktree from this surface is refused naming the host; Phase C Sessions create is unchanged. Content panes opened from this surface use `SourceContext` with `HostID` and `RemoteSource`. |
| Agent on aerie, `sidecar open README.md`, laptop bound to `[aerie] Sidecar` and holding the lease | Document pane on this project workspace (viewer-screen relay), not a Sessions-only landing and not aerie’s own TUI. | Implemented (slice 3). |

Label grammar, one line, no extra punctuation beyond the brackets:

```text
[host] Project
[host] Project [[worktree]]
```

Local destinations stay unprefixed (`Sidecar`, `Sidecar [[feature]]` if the worktree switcher already shows the project). Host is the registered host id (`aerie`), not the SSH target and not the hostname in the lease token. A worktree suffix is present only when the destination is a linked worktree, not the project’s main checkout. `FormatDestination` is the one function `@`, `W`, the navbar, and toasts use.

## Current behavior

`@` paints Overview (if `cross_project_overview`) plus local `config.projects.list`, then remote projects from `overview.Model.HostCatalog()` (`internal/app/model.go` `projectSwitcherDestinations`). Local Enter is still `switchProject(path)`. Remote Enter is `bindRemoteDestination` (`internal/app/model.go`): leave Sessions, set `m.boundDestination`, clear `m.ui.WorkDir` / `ProjectRoot`, persist `state.LastBoundLocation` and `GetLastRemoteWorktree` keyed by `hosts.ScopedKey(HostID, ProjectKey)`, `registry.ReinitHost("", "", hostID, incarnation, projectKey)`, toast `Switched to` + `FormatDestination`, wire navbar/title helpers. A twin local path is not Reinit’d.

`W` is local-first from the captured inventory (`internal/app/worktree_switcher_modal.go`). Same-named host linked worktrees append as `[host] Project [[name]]` (decision 10: `strings.EqualFold` of trimmed names, not `ProjectKey`). Remote Enter reuses `bindRemoteDestination`. Local W Enter is an explicit destination (`restoreLastWorktree=false`) so a pick of local main after a remote bind does not follow `LastWorktreePath`. Opening W never synchronously `git worktree list`.

`plugin.Context` carries `HostID`, `HostIncarnation`, `ProjectKey`, `HostWorkspaces`, and `RemoteControlSpawner` (`internal/plugin/context.go`). The workspace plugin lists that host’s shells/worktrees from the catalog and calls `tty.Model.UseRemoteControl` with the Sessions spawner (`internal/overview/hosts.go` `HostControlSpawner`). Files, Git, td, and Tasks refuse with `plugin.FormatRemoteUnavailable` and do not walk `WorkDir` on Init/Start/View/Update.

Remote rows require `sidecar_remote_hosts` and `cross_project_overview`: the registry still lives in `overview.Model`. `HostCatalog` is a copy; project keys are unscoped (`hosts.SplitScopedKey`) so later persist does not double-scope. Unreachable hosts keep last-known projects in `hostLastKnown` for `@`/`W` only; Sessions still deletes `hostResults`/`hostProjects` when `!Health.State.Shows()` and paints a health row.

Relayed `sidecar open` / `layout` (`Origin.HostID != ""`) land through one app-level decider (`uiRequestLanding` in `internal/app/scope.go`). Sessions wins when global Sessions is on screen and the matching row is selected with the preview visible. Otherwise the bound project workspace wins when `ScopeProject`, the bound destination matches origin `(HostID, ProjectKey)`, the origin shell is in that host inventory, this instance holds the lease, and Workspaces is the screen. Otherwise the request is declined and never queued. `overview.handleUIRequest` is extended with `RelayedLanding` so it does not ack-decline a request the bound workspace owns; the workspace plugin never queues a relayed request and never applies a request Sessions owns. `bindRemoteDestination` publishes `uirequest.Instance.HostID` of the bound host (no remote path in `WorkDir`), claims geometry leases for that project's live sessions, and the workspace plugin's `AttentionOrigin` carries `HostID`. Bound content panes use `workspaceSourceContext` with `HostID`/`HostIncarnation`/`ProjectKey` and `RemoteSource` from the Sessions adapter injected on `plugin.Context`; nested links follow the deck's Source. A `HostIncarnation` bump while bound is a re-resolve.

## Scope

Done in slices 0–2:

- Host-qualified destination identity on both switchers, including the label grammar above.
- Opening either modal with local rows on the first frame; remote rows appended from in-memory serve snapshots (and from snapshot updates while the modal is open).
- Binding `ScopeProject` to `(HostID, ProjectKey, Worktree identity)` without treating a remote root as a local `WorkDir`.
- Workspace plugin listing that host’s shells/worktrees; live terminals via the existing control-mode proxy.
- Disabled remote rows when the host is not online, with the existing health sentence.
- Persisting last remote project/worktree as host-qualified identity (`LastBoundLocation`, `GetLastRemoteWorktree`), never as a raw remote path in `LastWorktreePath`.
- Navbar / title reflecting the bound host.
- Honest refusals from Files, Git, td, Tasks, and Notes (Init/Start/View/Update/FocusContext).

Remaining in this plan:

- Files, Git, td, Tasks, Notes as remote-capable plugins (slice 4).
- Create shell/worktree from the bound project workspace (today refused; Phase C Sessions create is the reuse target).
- CLI/help and isolated two-machine proof (slice 5).

Out of scope:

- A public `sidecar project switch --host`. Agent-project-cli stays local until its own follow-on.
- Reverse SSH, a daemon, SSHFS, or serve executing a project switch.
- Changing Sessions’ fleet catalog. Sessions does not go away.
- Auto-adding a remote project to this machine’s `config.projects.list`. The remote config remains the host’s.

## Architecture

### 1. One destination type — implemented

`internal/app/destination.go`:

```go
type Destination struct {
    HostID          string // empty = this machine
    HostIncarnation uint64 // serve incarnation the rows came from; 0 when local
    // ProjectKey is the owning host's inventory key, which is canonical(root)
    // on that machine — a path-shaped string that is NOT a local path. Persist
    // and compare only as hosts.ScopedKey(HostID, ProjectKey).
    ProjectKey  string
    ProjectName string
    WorktreeKey string // canonical worktree path on the owning host; empty = main checkout
    WorktreeName string
    // Root is a hint from the owning host, never passed to local os/git/td.
    Root string
}
```

`HostIncarnation` matches `contentpanes.SourceContext`. A bump while bound is a re-resolve, not a silent continuation (`syncBoundHostIncarnation` plus `Deck.SetContext`).

`@` lists Overview, then one row per local configured project, then one row per `(HostID, ProjectKey)` from `HostCatalog`. `W` lists worktrees of the current project on this machine and its same-named counterpart on connected hosts. A remote worktree of a different project does not appear in `W`. Display is `FormatDestination`.

### 2. Local first, remote append — implemented

`initProjectSwitcher` / `initWorktreeSwitcher` stay synchronous over data this process already has. Remote rows come from `overview.Model.HostCatalog()` (`internal/overview/host_catalog.go`): ID, Health, Incarnation, unscoped project Key/Name/Root, and copied workspaces. Nil when there is no registry. Host updates already arrive in project scope (`overview.IsHostMessage`); an open modal rebuilds remote rows, keeps the filter, and keeps the cursor on the previously highlighted destination identity.

### 3. Entering a remote destination — implemented

`bindRemoteDestination` does **not** call `switchProject(path)`:

1. Leave Sessions (`leaveOverview`) if needed. — done
2. Set `m.boundDestination`. `m.ui.WorkDir` does not become the remote root. Persist `{HostID, ProjectKey, WorktreeKey}` as `LastBoundLocation` and last worktree per `hosts.ScopedKey`. — done
3. `plugin.Context` has `HostID` (empty for local). `Epoch` still increments. — done (`ReinitHost`)
4. Plugins that need a real directory see an empty local workdir and a non-empty `HostID`. Files/Git/td/Tasks must not `os.Stat` the remote root. — done for those four
5. Claim geometry leases for live sessions of that project via `tty.ClaimGeometryLease` (the same unambiguous-local-action seam interactive attach uses). `@` Enter is that action. — done (`claimBoundProjectLeases`)
6. Restore last worktree **on that host** for that project key, unless the user picked an explicit worktree destination. — done (`applyLastRemoteWorktree`)
7. Republish presence with `uirequest.Instance.HostID` of the bound host and empty `WorkDir`/`ProjectKey`, so a remote path is never advertised as this machine’s local identity. — done (`announcePresenceCmd`)
8. Workspace `AttentionOrigin` carries the bound `HostID` from `p.ctx.HostID`. — done

Returning to a local project is today’s `switchProject(path)` and clears `HostID`, the incarnation, and the bound destination (`clearBoundDestination` + `UseLocalControl`).

### 4. The screen is this TUI — implemented (slice 3)

`uiRequestLanding` is the one decider over the two surfaces, in the shape of `sessionsOwnsCreateSplit`. Relayed open/layout (`Origin.HostID != ""`) never queue.

- If global Sessions is on screen and the matching row is selected with preview visible → Sessions is the screen (`overview.RelayedRowOnScreen`).
- Else if `ScopeProject`, the bound destination matches origin `(HostID, ProjectKey)`, the origin shell is in that host inventory, this instance holds the lease, and the project workspace is the screen → bound workspace is the screen.
- Else → decline, do not queue.

`overview.handleUIRequest` is extended (`RelayedLanding`): when the bound workspace owns the request it returns without acking. Host-stream announcements that belong to the bound workspace are forwarded as `uirequest.RequestMsg` so the workspace plugin receives them. The workspace plugin never queues a relayed request and never applies a request Sessions owns.

If the user is in a *different* local project while an aerie agent opens a file, the request is off-screen for this TUI (exit 4 on layout; relayed open does not queue). Sessions can still show the row; it is not the bound screen unless the user is in Sessions looking at it.

Sitting at aerie’s own TUI still wins the lease by typing, per geometry_lease.go. This plan does not steal a screen the human at the host is using. `@` Enter is an unambiguous local action and claims live sessions of the bound project the way interactive attach does.

### 5. Plugins, in order

| Plugin | Remote-bound behaviour now | Remaining |
| --- | --- | --- |
| Workspaces | Host inventory for that project; live terminals via `UseRemoteControl` + Sessions spawner. Relayed open/layout land here when this workspace is the screen. `AttentionOrigin.HostID` is the bound host. | Create shell/worktree from this surface (refused today). |
| Content panes | Sessions already loads remote files through `SourceContext`. Bound project workspace uses the same adapter: `workspaceSourceContext` carries `HostID`/`HostIncarnation`/`ProjectKey`; `workspaceDeckConfig` uses `RemoteSource`; nested links follow the deck's Source. | — |
| Files | The host's tree through `sidecar content tree`, previews and find-by-name through the existing content verbs. Writes, blame, and project search refuse naming the host. | Done ([remote-files-plugin.md](remote-files-plugin.md)). |
| Git | Same. | Slice 4. |
| td / Tasks | Same. | Slice 4, then host td store via content verbs and `RunSidecar`. |
| Notes | Unavailable view naming the host. No local td store. | Slice 4 remoting. |
| Conversations | Remains demand-gated per the remote-hosts plan. | Unchanged. |

A plugin that reads `ctx.WorkDir` without checking `HostID` is a bug this plan’s tests must catch at the Reinit boundary, the way `localSelectedRoot` already refuses a remote Sessions row.

## Settled decisions

1. **`@`/`W` are destination pickers over every connected Sidecar, not over this machine’s `config.json` alone.**
2. **Labels are `[host] Project` and `[host] Project [[worktree]]`.** Local unprefixed. Host id, not SSH target.
3. **First frame is local.** Remote rows append from in-memory snapshots. The modal is not a loading spinner.
4. **Entering remote is a context bind, not `Reinit(remotePath)`.**
5. **This TUI is the screen** for the bound remote project, using the viewer-screen lease and announcement. Sessions is the fleet, not the only remote workspace.
6. **Same project name on two machines is two destinations.**
7. **Disconnected hosts remain visible and disabled.**
8. **Last location is host-qualified identity**, never a remote filesystem path stored where a local restore would follow it.
9. **Files/Git/td/Tasks remoting is this plan’s later slices**, not a surprise side effect of the switcher. The steel thread ships with those plugins refusing.
10. **Names join projects across machines; keys never do.** An inventory `ProjectKey` is `canonical(root)` on the machine that produced it. `W` pairs the current project with the host project of the same registered name (case-insensitive, trimmed). Identity in state and in comparisons is always `hosts.ScopedKey(HostID, key)`.
11. **Remote destinations require `sidecar_remote_hosts` and `cross_project_overview`.** The registry stays in the Overview model. Slice 1 kept that dependency rather than lifting ownership. Help (slice 5) must state it.

## Slices

### Slice 0 — destination identity and labels — implemented (`dc5e3be0`, td-823e94)

`Destination`, `FormatDestination`, `DestinationMatches`, unwired-then-later-wired navbar/title helpers in `internal/app/destination.go`. `overview.Model.HostCatalog()` returns host ID, health, incarnation, unscoped project keys, and copied workspaces.

### Slice 1 — steel thread: `@` lists remotes, Enter binds Workspaces — implemented (`cfb2fd89`, refusal fix `6dd5b02d`, td-72a679)

`@` paints local rows immediately, appends connected-host projects from snapshots. Enter on `[host] Project` binds `ScopeProject`+`HostID` without Reinit of a twin local path. Workspaces lists that host’s shells via the existing control-mode proxy. Files/Git/td/Tasks refuse naming the host on Init/Start/View/Update. Theme preview and cursor-on-current stop keying off `destination.Path`. Unreachable hosts keep last-known catalog rows for `@` only. Create shell/worktree from a bound project workspace is refused naming the host.

### Slice 2 — `W` across hosts for the current project — implemented (`813169a3`, W-Enter fix `9844590d`, td-90757e)

Worktree switcher local-first, then `[host] Project [[feature]]` for linked worktrees of the same-named host project. Host main checkout is not a W row. Enter binds through `bindRemoteDestination`. Last-worktree memory is `GetLastRemoteWorktree` / `SetLastRemoteWorktree` per `hosts.ScopedKey`; `@` Enter restores it when still in the catalog. W Enter is an explicit destination (`restoreLastWorktree=false`).

### Slice 2.5 — portable loopback remote for agents — implemented (`scripts/loopback-remote.sh`, td-11d3d3, td-761cea)

An agent (or any other developer) can bring up and tear down a simulated remote on the machine they are sitting at, with a small injected delay, and drive `@` / `W` against it. No named workstation, no live `~/.local/state/sidecar`, no default tmux server.

```bash
./scripts/loopback-remote.sh up [--delay 40ms] [--no-drive]
./scripts/loopback-remote.sh paths
./scripts/loopback-remote.sh status
./scripts/loopback-remote.sh down
```

`up` creates `/tmp/sidecar-loopback-$USER/{host,viewer}/` (override `LOOPBACK_RUN_DIR`, still refused unless it matches `/tmp/sidecar-loopback*` / `/private/tmp/sidecar-loopback*`). It builds this worktree’s `sidecar` into the run root, writes two isolated configs, starts a private host tmux server with the Loopback sample project (REMOTE-MARKER), a linked `feature` worktree (so `W` has a `[loopback] Loopback [[feature]]` row), and two hold shells, plants a same-named viewer twin (LOCAL-TWIN), inits td in both projects when `td` is on PATH (so the first TUI frame is not the setup modal), and installs `scripts/loopback-ssh.sh` as `ssh` on the viewer PATH only. Caller `SIDECAR_SHELL` / `SIDECAR_NAMESPACE` / `SIDECAR_MANAGED_SHELL` / `SIDECAR_TMUX_SERVER` / `SIDECAR_HOST` are unset so an agent running `up` from a live Sidecar pane does not leak that identity into the fixture. Host env keeps real tmux/git. `--delay` is a spawn delay on each fake-ssh invocation (`SIDECAR_LOOPBACK_SSH_DELAY`). `--no-drive` stands up the trees without a TUI; otherwise `tmux-drive.sh` starts the viewer against the twin with `sidecar_remote_hosts` and `cross_project_overview` on. `paths` refuses unless both axes are isolated. `down` kills only sockets this script created (`tmux -S` / tmux-drive stop) and deletes the run root.

Go tests in `internal/cli/agent_remote_loopback_test.go` install the same `scripts/loopback-ssh.sh` rather than generating a second script body. Thin CI: `scripts/test-loopback-remote.sh` (refuse-run-dir, `up --no-drive`, isolation fingerprint; no TUI). `scripts/remote-spike.sh` requires `SPIKE_HOST` (no workstation default).

Journeys 2–3 (`@` then Enter on `[loopback] Loopback`) are runnable on this fixture and remain slice 5’s isolated proof. Out of this slice: sshd, ControlMaster, packet loss, a second physical machine.

### Slice 3 — viewer-screen lands on the bound project — implemented (td-af932a)

Relayed `sidecar open` / `layout` from a host pane whose project matches the bound remote destination apply to this project workspace when this instance holds the lease. Off-screen remains exit 4. Sessions landing remains for when the user is in Sessions, not bound to that project.

`uiRequestLanding` (`internal/app/scope.go`) is the one decider. `overview.handleUIRequest` is extended with `RelayedLanding` rather than copied into the workspace plugin. `uirequest.Instance` has `HostID` (same json as `Origin`); `bindRemoteDestination` publishes the bound host and does not write a remote path into `WorkDir`. Workspace `AttentionOrigin` sets `HostID` from `p.ctx.HostID`. Live session leases are claimed on bind through `tty.ClaimGeometryLease`. `workspaceSourceContext` carries `HostID`/`HostIncarnation`/`ProjectKey`; `workspaceDeckConfig` uses `RemoteSource` from the Sessions adapter injected on `plugin.Context` (`RemoteRunner` / `HostVerbs`); nested links follow the deck's Source. A `HostIncarnation` bump while bound re-resolves. Relayed requests never use the workspace plugin's local pending-view queue. The refusal reasons stay the ones viewer-screen already ships.

### Slice 4 — Files (done), then Git, td, Tasks

One plugin at a time, each through `Source` / `RunSidecar` / host content verbs, each with the twin-path tripwire. Not a second compositor, not a mounted FS. Notes already refuses like Files while bound.

Files is **implemented**, in its own document: [Files on a remote-bound project](remote-files-plugin.md). It is the largest of the four because it is the only one that needs a new host verb (`sidecar content tree`, capability `ContentTreeV1`) alongside a plugin-wide source seam at `FileTree.loadChildren` and a bound worktree key on `plugin.Context`. Git, td, and Tasks follow it and reuse whatever it establishes.

### Slice 5 — docs, agent-project-cli note, isolated proof

CLI/help: `@` and `W` name remote destinations, and help states the flag requirement from decision 11. Isolated proof uses the slice 2.5 loopback recipe as the default: local `@` stays fast with `--delay`; Enter on `[loopback] Project` does not open the local twin; Workspaces shows the host’s shells; `sidecar project current` on a local shell does not report the bound remote project as this machine’s. A real second machine remains opt-in via `scripts/remote-content-proof.sh` with an explicit `SPIKE_HOST` (no workstation default).

## Proof and isolation

Same bar as remote content panes: private tmux sockets and private Sidecar state on both machines, `SIDECAR_ISOLATED_STATE=1`, no default of a live workstation. A proof that Reinit’s a path under the viewer’s `$HOME` because the host reported that string has failed even if the tests are green.

Slices 0–3 are covered by package tests (`go test ./internal/app ./internal/overview ./internal/plugin ./internal/plugins/workspace ./internal/plugins/filebrowser ./internal/plugins/gitstatus ./internal/plugins/tdmonitor ./internal/plugins/tasks ./internal/state ./internal/uirequest ./internal/contentpanes`). Slice 2.5 is implemented as `scripts/loopback-remote.sh` plus `scripts/test-loopback-remote.sh`; slice 4–5 proofs use that fixture. Slice 5’s isolated proof runs on it by default.

## Related plan updates

- [sidecar-remote-hosts.md](sidecar-remote-hosts.md): complete remote Files/Git browsing is this plan’s slice 4, entered through `@`.
- [remote-host-viewer-screen.md](../implemented/remote-host-viewer-screen.md): the bind exists (`ScopeProject` + `HostID`). Slice 3 extends the announcement target from Sessions-only to the bound project workspace via one shared landing decider.
- [remote-host-content-pane-parity.md](../implemented/remote-host-content-pane-parity.md): Sessions remains the fleet remote content surface. The bound project Workspace lists host shells and loads through the same `SourceContext` + `RemoteSource` adapter.

## Changelog

- **2026-09-01** — Slice 4's Files half split into [remote-files-plugin.md](remote-files-plugin.md) and implemented there: it needed a new host verb (`sidecar content tree`, `ContentTreeV1`), not just a plugin change. Git, td, Tasks, and Notes remain. Proving it on the loopback fixture surfaced td-055768 — the serve stream could not skip a login banner, so any host with a chatty profile showed no remote rows at all — fixed in `internal/hostproto` and now covered end to end by `internal/cli/serve_stream_loopback_test.go`. Slice 3 review cleanup (td-1d6735): `layoutapply.ResolveRemoteTargets` and `hosts.OriginNamesProject` are the single remote resolver and the single origin/project predicate both surfaces use.
- **2026-09-01** — Slice 3 implemented (td-af932a): bound `@` destination is the screen for relayed open/layout. Instance `HostID`, workspace `AttentionOrigin.HostID`, lease claim on bind, shared `uiRequestLanding` decider, `workspaceSourceContext` + `RemoteSource`. Remaining: slices 4–5.
- **2026-09-01** — Slice 2.5 implemented: `scripts/loopback-remote.sh` (`up`/`paths`/`status`/`down`, `--no-drive`, `--delay`), shared `scripts/loopback-ssh.sh`, `scripts/test-loopback-remote.sh`. `remote-spike.sh` requires `SPIKE_HOST`. Default proof path for later slices; real SSH stays opt-in with no workstation hostname default.
- **2026-09-01** — Slice 2.5 added: portable loopback remote (extract existing `loopbackHost` + fake ssh, optional spawn delay, agent up/down). Default proof path for later slices; real SSH stays opt-in with no workstation hostname default.
- **2026-09-01** — Slices 0–2 implemented on `remote-viewer-screen` (td-f7855c). Plan rewritten as current state: bind, catalog, `@`/`W`, Workspaces listing and live attach, Files/Git/td/Tasks refusals. Remaining: slices 3–5.
- **2026-09-01** — Reviewed against the tree. Corrections: the host registry is owned by `internal/overview`, so slice 1 needs an accessor and inherits the `cross_project_overview` dependency (decision 11); inventory `ProjectKey` is a path on the owning machine, so `W` pairs by project name and identity persists through `hosts.ScopedKey` (decision 10); `Destination` carries `HostIncarnation` to match `contentpanes.SourceContext`; binding must publish `HostID` on instance presence and on the workspace plugin's `AttentionOrigin`, neither of which carries one today; viewer-screen is implemented, and its landing gate is extended by one shared decider rather than copied; theme preview and cursor-on-current stop keying off a path.
- **2026-09-01** — Created. `@`/`W` list host-qualified destinations asynchronously; entering one binds this TUI as that remote project’s screen; plugin remoting after Workspaces is sliced.
