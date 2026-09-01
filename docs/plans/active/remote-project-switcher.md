# Remote destinations in `@` and `W`

Status: **active; slices 0–2 implemented** on `remote-viewer-screen` (td-f7855c: td-823e94, td-72a679, td-90757e). Remaining: slice 3 (viewer-screen landing on the bound project), slice 4 (Files/Git/td/Tasks remoting), slice 5 (docs and isolated proof). **Created:** 2026-09-01 **Verified against the tree on 2026-09-01** after slices 0–2.

Related: [Sidecar as its own remote host runtime](sidecar-remote-hosts.md) is the transport and inventory stream. [The viewer owns the screen](../implemented/remote-host-viewer-screen.md) is the lease and `uirequest` announcement this bind must still grow onto the project workspace. [Remote host content-pane parity](../implemented/remote-host-content-pane-parity.md) is the read path a remote-bound plugin must use. [Agent-facing project CLI](agent-project-cli.md) is the local `sidecar project` surface; it does not grow `--host` in this plan.

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
                +-- content panes     --> SourceContext with HostID               [remaining]
                +-- lease + uirequest --> viewer-screen announcement, this TUI    [slice 3]
```

A same-named local checkout is a different destination. Reinit of `~/code/sidecar` because aerie also has a project named sidecar is the failure content-pane already named.

## User contract

| Gesture | Required result | Status |
| --- | --- | --- |
| `@` with no remotes connected | Unchanged: Overview (if available) plus local `config.projects.list`. | Implemented |
| `@` with aerie online | Local rows this frame. Then `[aerie] Sidecar`, `[aerie] td`, … as that host’s snapshot is present. Filter matches host id, project name, and path. | Implemented |
| `@` with aerie connecting / stale / unreachable | Those rows still exist, disabled, with the same health reason Sessions already shows. They are not omitted, and they are not switchable. | Implemented (`hostLastKnown` for `@`; Sessions still drops `hostResults` on `!Shows()`) |
| Enter on `[aerie] Sidecar` | Leave Sessions if you were there. Bind this TUI to that host project. Restore that host’s last worktree for that project if one was remembered. Navbar / title name the host. Toast: `Switched to [aerie] Sidecar`. | Implemented (bind, restore, navbar/title, toast). Lease claim and honest instance presence are slice 3. |
| `W` while in local Sidecar, aerie online | Local worktrees this frame. Then `[aerie] Sidecar [[feature]]` for linked worktrees of the project aerie registered under the same *name* (decision 10). | Implemented. Host main checkout is not a W row; it remains the `@` destination. |
| `W` while already in `[aerie] Sidecar` | Aerie’s worktrees for that project, plus this machine’s worktrees of the same-named local project as unprefixed rows. | Implemented. Local rows come from the in-memory cache of that named config project; omitted rather than `git worktree list` on open. |
| Enter on `[aerie] Sidecar [[feature]]` | Bind to that host worktree. Plugins reinit against the remote context, not against a local path of the same name. | Implemented via `bindRemoteDestination`. |
| Files / Git / td / Tasks on a remote-bound project | Honest unavailable state naming the host, until that plugin’s remote slice. They must not walk this disk. | Implemented (Init/Start/View/Update). Notes is not yet in this refusal set. |
| Workspaces on a remote-bound project | That host’s shells and worktrees for the project, live panes through the existing proxied control channel. | Listing and live attach implemented. Create shell/worktree from this surface is refused naming the host; Phase C Sessions create is unchanged. Content panes opened from this surface still build a local `SourceContext` (no `HostID`). |
| Agent on aerie, `sidecar open README.md`, laptop bound to `[aerie] Sidecar` and holding the lease | Document pane on this project workspace (viewer-screen relay), not a Sessions-only landing and not aerie’s own TUI. | Remaining (slice 3). |

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

### What slice 3 still has to change

These are the remaining seams the bind did not close:

1. **Instance presence publishes no host.** `bindRemoteDestination` calls `announceInstanceCmd("", "")` (`internal/app/model.go`). `uirequest.Instance` has no `HostID` field; its `ProjectKey` is a local basename from `projectdir.Lookup`. Publishing empty identity while bound avoids advertising a remote project as this machine’s, but `sidecar project current` then reports nothing, and a local agent’s `sidecar open` cannot match the bound host. Slice 3 adds `HostID` to the record (same shape as `uirequest.Origin`) and publishes the bound host.
2. **Workspace `AttentionOrigin` is still local.** `internal/plugins/workspace/agent_triggers.go` `AttentionOrigin()` does not set `HostID`. `attentionOriginTransport` already has the field. Without it, an aerie agent’s `sidecar open` cannot tell that this TUI is looking at its shell.
3. **The landing gate is still Sessions-only.** `overview.handleUIRequest` / `relayedOpenNotOnScreenReason` refuse unless the Sessions preview is visible and the row is selected (`internal/overview/ui_requests.go`). Once bound, the same announcement must apply to this project workspace when the origin matches `(HostID, ProjectKey)` and this instance holds the lease. One shared decider over the two surfaces, in the shape of `sessionsOwnsCreateSplit` (`internal/app/scope.go`), not a second copy inside the workspace plugin.
4. **Geometry leases are not claimed on bind.** Architecture §3 step 5 — claim leases for live sessions of that project as Sessions does on row select — is not in `bindRemoteDestination`.
5. **Bound content panes are still local `SourceContext`.** `workspaceSourceContext` (`internal/plugins/workspace/content_deck.go`) sets `Root` / `ProjectRoot` from `ctx.ProjectRoot` (empty while bound) and does not set `HostID` / `HostIncarnation`. Nested links from a remote shell in the bound workspace would miss the content-pane source seam Sessions already has.

## Scope

Done in slices 0–2:

- Host-qualified destination identity on both switchers, including the label grammar above.
- Opening either modal with local rows on the first frame; remote rows appended from in-memory serve snapshots (and from snapshot updates while the modal is open).
- Binding `ScopeProject` to `(HostID, ProjectKey, Worktree identity)` without treating a remote root as a local `WorkDir`.
- Workspace plugin listing that host’s shells/worktrees; live terminals via the existing control-mode proxy.
- Disabled remote rows when the host is not online, with the existing health sentence.
- Persisting last remote project/worktree as host-qualified identity (`LastBoundLocation`, `GetLastRemoteWorktree`), never as a raw remote path in `LastWorktreePath`.
- Navbar / title reflecting the bound host.
- Honest refusals from Files, Git, td, and Tasks (Init/Start/View/Update).

Remaining in this plan:

- Lease claim on bind, instance `HostID`, workspace `AttentionOrigin.HostID`, and one landing decider so relayed `sidecar open` / `layout` target the bound project workspace (slice 3).
- Content-pane `SourceContext` on the bound project workspace (slice 3 or with it: the source seam already exists).
- Files, Git, td, Tasks as remote-capable plugins (slice 4). Notes refusal or remoting.
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

`HostIncarnation` matches `contentpanes.SourceContext`. A bump while bound is a re-resolve, not a silent continuation (slice 3 must honor this when wiring the source).

`@` lists Overview, then one row per local configured project, then one row per `(HostID, ProjectKey)` from `HostCatalog`. `W` lists worktrees of the current project on this machine and its same-named counterpart on connected hosts. A remote worktree of a different project does not appear in `W`. Display is `FormatDestination`.

### 2. Local first, remote append — implemented

`initProjectSwitcher` / `initWorktreeSwitcher` stay synchronous over data this process already has. Remote rows come from `overview.Model.HostCatalog()` (`internal/overview/host_catalog.go`): ID, Health, Incarnation, unscoped project Key/Name/Root, and copied workspaces. Nil when there is no registry. Host updates already arrive in project scope (`overview.IsHostMessage`); an open modal rebuilds remote rows, keeps the filter, and keeps the cursor on the previously highlighted destination identity.

### 3. Entering a remote destination — bind implemented; presence and leases remaining

`bindRemoteDestination` does **not** call `switchProject(path)`:

1. Leave Sessions (`leaveOverview`) if needed. — done
2. Set `m.boundDestination`. `m.ui.WorkDir` does not become the remote root. Persist `{HostID, ProjectKey, WorktreeKey}` as `LastBoundLocation` and last worktree per `hosts.ScopedKey`. — done
3. `plugin.Context` has `HostID` (empty for local). `Epoch` still increments. — done (`ReinitHost`)
4. Plugins that need a real directory see an empty local workdir and a non-empty `HostID`. Files/Git/td/Tasks must not `os.Stat` the remote root. — done for those four
5. Claim geometry leases for live sessions of that project as Sessions already does on row select. — remaining
6. Restore last worktree **on that host** for that project key, unless the user picked an explicit worktree destination. — done (`applyLastRemoteWorktree`)
7. Republish presence honestly. Today: `announceInstanceCmd("", "")`. Remaining: add `HostID` to `uirequest.Instance` and publish the bound host, or keep publishing no project identity only as an explicit choice that `sidecar project current` documents. Getting this wrong is an agent-visible bug.
8. Workspace `AttentionOrigin` must carry the bound `HostID`. — remaining

Returning to a local project is today’s `switchProject(path)` and clears `HostID`, the incarnation, and the bound destination (`clearBoundDestination` + `UseLocalControl`).

### 4. The screen is this TUI — remaining (slice 3)

Viewer-screen lands a relayed `sidecar open` on the Sessions preview of `(HostID, TmuxSession)`, and refuses when that preview is not visible or the row is not selected. Once this TUI is bound to that host project, the same announcement applies here: the origin shell’s project matches the bound `(HostID, ProjectKey)`, the lease owner is this instance, and the pane opens in the project workspace deck.

“Which surface is this request’s screen” is a decision with two possible answers, and it must be **one** decider that both surfaces call, in the shape of `sessionsOwnsCreateSplit` (`internal/app/scope.go`). A second landing rule is the same class of bug the pane-parity rule in `CLAUDE.md` exists to prevent.

If the user is in a *different* local project while an aerie agent opens a file, the request is off-screen for this TUI (exit 4 on layout; relayed open does not queue). Sessions can still show the row; it is not the bound screen unless the user is in Sessions looking at it.

Sitting at aerie’s own TUI still wins the lease by typing, per geometry_lease.go. This plan does not steal a screen the human at the host is using.

### 5. Plugins, in order

| Plugin | Remote-bound behaviour now | Remaining |
| --- | --- | --- |
| Workspaces | Host inventory for that project; live terminals via `UseRemoteControl` + Sessions spawner. | Create shell/worktree from this surface (refused today). Content `SourceContext` with `HostID`. `AttentionOrigin.HostID`. |
| Content panes | Sessions already loads remote files through `SourceContext`. Bound project workspace does not yet pass `HostID`. | Wire `workspaceSourceContext` from the bound destination. Nested links stay on that host. |
| Files | Unavailable view naming the host. No local tree of a twin path. | Slice 4 remoting. |
| Git | Same. | Slice 4. |
| td / Tasks | Same. | Slice 4, then host td store via content verbs and `RunSidecar`. |
| Notes | Still assumes a local tree. | Refusal (same as Files) or remoting in slice 4. |
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

### Slice 3 — viewer-screen lands on the bound project

Relayed `sidecar open` / `layout` from a host pane whose project matches the bound remote destination apply to this project workspace when this instance holds the lease. Off-screen remains exit 4. Sessions landing remains for when the user is in Sessions, not bound to that project.

The work is one shared decider over the two surfaces plus the presence changes listed under [What slice 3 still has to change](#what-slice-3-still-has-to-change): instance `HostID`, workspace `AttentionOrigin.HostID`, lease claim on bind, and `workspaceSourceContext` carrying the bound host. The refusal reasons stay the ones viewer-screen already ships. Its landing gate lives in `overview.handleUIRequest` and must be extended, not duplicated.

### Slice 4 — Files (then Git, td, Tasks) as remote-capable plugins

One plugin at a time, each through `Source` / `RunSidecar` / host content verbs, each with the twin-path tripwire. Not a second compositor, not a mounted FS. Notes gets the same refusal-or-remote treatment the steel thread already gave Files.

### Slice 5 — docs, agent-project-cli note, isolated proof

CLI/help: `@` and `W` name remote destinations, and help states the flag requirement from decision 11. Isolated two-machine proof: local `@` stays fast with a slow host; Enter on `[host] Project` does not open the local twin; Workspaces shows the host’s shells; `sidecar project current` on a local shell does not report the bound remote project as this machine’s. The two-machine recipe extends `docs/guides/active/remote-viewer-screen-proof.md` rather than adding a third.

## Proof and isolation

Same bar as remote content panes: private tmux sockets and private Sidecar state on both machines, `SIDECAR_ISOLATED_STATE=1`, no default of a live workstation. A proof that Reinit’s a path under the viewer’s `$HOME` because the host reported that string has failed even if the tests are green.

Slices 0–2 are covered by package tests (`go test ./internal/app ./internal/overview ./internal/plugin ./internal/plugins/workspace ./internal/plugins/filebrowser ./internal/plugins/gitstatus ./internal/plugins/tdmonitor ./internal/plugins/tasks ./internal/state`). Isolated two-machine proof is slice 5.

## Related plan updates

- [sidecar-remote-hosts.md](sidecar-remote-hosts.md): complete remote Files/Git browsing is this plan’s slice 4, entered through `@`.
- [remote-host-viewer-screen.md](../implemented/remote-host-viewer-screen.md): the bind exists (`ScopeProject` + `HostID`). Slice 3 extends the announcement target from Sessions-only to the bound project workspace.
- [remote-host-content-pane-parity.md](../implemented/remote-host-content-pane-parity.md): Sessions remains the implemented remote content surface. The bound project Workspace lists host shells; its `SourceContext` still needs `HostID` (slice 3).

## Changelog

- **2026-09-01** — Slices 0–2 implemented on `remote-viewer-screen` (td-f7855c). Plan rewritten as current state: bind, catalog, `@`/`W`, Workspaces listing and live attach, Files/Git/td/Tasks refusals. Remaining: slices 3–5.
- **2026-09-01** — Reviewed against the tree. Corrections: the host registry is owned by `internal/overview`, so slice 1 needs an accessor and inherits the `cross_project_overview` dependency (decision 11); inventory `ProjectKey` is a path on the owning machine, so `W` pairs by project name and identity persists through `hosts.ScopedKey` (decision 10); `Destination` carries `HostIncarnation` to match `contentpanes.SourceContext`; binding must publish `HostID` on instance presence and on the workspace plugin's `AttentionOrigin`, neither of which carries one today; viewer-screen is implemented, and its landing gate is extended by one shared decider rather than copied; theme preview and cursor-on-current stop keying off a path.
- **2026-09-01** — Created. `@`/`W` list host-qualified destinations asynchronously; entering one binds this TUI as that remote project’s screen; plugin remoting after Workspaces is sliced.
