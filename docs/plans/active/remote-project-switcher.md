# Remote destinations in `@` and `W`

Status: **active; slices 0–2 implemented** **Created:** 2026-09-01 **Verified against the tree on 2026-09-01** (`remote-viewer-screen`; citations inline in [Current behavior](#current-behavior)) **Scope:** host-qualified rows in the project and worktree switchers; entering a remote project as `ScopeProject` with the laptop as its screen; async catalog so local listings do not wait on SSH. Plugin remoting beyond Workspaces and the existing content-pane source is phased. Slices 0–2 are on this branch (`dc5e3be0`, `cfb2fd89`+`6dd5b02d`, `813169a3`+`9844590d`). Remaining work is slice 3 (viewer-screen landing on the bound project), slice 4 (Files/Git/td/Tasks remoting), and slice 5 (docs and isolated proof).

Related: [Sidecar as its own remote host runtime](sidecar-remote-hosts.md) is the transport and inventory stream. [The viewer owns the screen](../implemented/remote-host-viewer-screen.md), implemented in full on `remote-viewer-screen`, is the lease and `uirequest` announcement this binds a project workspace to. [Remote host content-pane parity](../implemented/remote-host-content-pane-parity.md) is the read path a remote-bound plugin must use; it already kept the source seam presentation-neutral “so a future remote project Workspace can reuse them.” [Agent-facing project CLI](agent-project-cli.md) is the local `sidecar project` surface; it does not grow `--host` in this plan.

## Decision first

`@` and `W` list every project and worktree this Sidecar can actually enter. Local destinations paint on the opening frame. Destinations that live on a connected remote host append as that host’s serve snapshot is already in memory, and they never delay the local list.

Selecting `[aerie] Sidecar` does not focus a Sessions row and does not call `registry.Reinit` on a path that might exist on this disk. It binds **this** TUI as `ScopeProject` for that host’s project: `plugin.Context` carries `HostID`, the workspace plugin shows that host’s shells, content panes load through `contentpanes.SourceContext`, and the geometry lease for those tmux sessions is held here. A host agent that runs `sidecar open` is then talking to this project workspace, through the announcement seam in the viewer-screen plan, not only to Sessions.

That is the gap this closes. Sessions remains the fleet. `@` is how you go to work in a project, including one that is not on this machine.

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
                +-- workspace plugin  --> host inventory (already on the wire)
                +-- content panes     --> existing SourceContext
                +-- Files / Git / td  --> refuse until their remote slice
                +-- lease + uirequest --> viewer-screen announcement, this TUI
```

A same-named local checkout is a different destination. Reinit of `~/code/sidecar` because aerie also has a project named sidecar is the failure content-pane already named.

## User contract

| Gesture | Required result |
| --- | --- |
| `@` with no remotes connected | Unchanged: Overview (if available) plus local `config.projects.list`. |
| `@` with aerie online | Local rows this frame. Then `[aerie] Sidecar`, `[aerie] td`, … as that host’s snapshot is present. Filter matches host id, project name, and path. |
| `@` with aerie connecting / stale / unreachable | Those rows still exist, disabled, with the same health reason Sessions already shows. They are not omitted, and they are not switchable. |
| Enter on `[aerie] Sidecar` | Leave Sessions if you were there. Bind this TUI to that host project. Restore that host’s last worktree for that project if one was remembered. Navbar / title name the host. Toast: `Switched to [aerie] Sidecar`. |
| `W` while in local Sidecar, aerie online | Local worktrees this frame. Then `[aerie] Sidecar [[feature]]` for worktrees of the project aerie registered under the same *name* (decision 10: names join across machines, keys never do). |
| `W` while already in `[aerie] Sidecar` | Aerie’s worktrees for that project, plus this machine’s worktrees of the same-named local project as unprefixed rows. |
| Enter on `[aerie] Sidecar [[feature]]` | Bind to that host worktree. Same rules as a local worktree switch: plugins reinit against the remote context, not against a local path of the same name. |
| Files / Git / td / Tasks on a remote-bound project | Honest unavailable state naming the host, until that plugin’s remote slice. They must not walk this disk. |
| Workspaces on a remote-bound project | That host’s shells and worktrees for the project, live panes through the existing proxied control channel. |
| Agent on aerie, `sidecar open README.md`, laptop bound to `[aerie] Sidecar` and holding the lease | Document pane on this project workspace (viewer-screen relay), not a Sessions-only landing and not aerie’s own TUI. |

Label grammar, one line, no extra punctuation beyond the brackets:

```text
[host] Project
[host] Project [[worktree]]
```

Local destinations stay unprefixed (`Sidecar`, `Sidecar [[feature]]` if the worktree switcher already shows the project). Host is the registered host id (`aerie`), not the SSH target and not the hostname in the lease token. A worktree suffix is present only when the destination is a linked worktree, not the project’s main checkout.

## Current behavior

`@` builds destinations from `m.cfg.Projects.List` plus an Overview row (`projectSwitcherDestinations` in `internal/app/model.go`). Enter calls `switchProject(destination.Path)`, which saves the old plugin, optionally restores `state.GetLastWorktreePath` for the main repo, sets `m.ui.WorkDir`, and `registry.Reinit(targetPath)`. Every plugin `Init`s against that local directory. `plugin.Context` has `WorkDir` and `ProjectRoot` and no `HostID` (`internal/plugin/context.go`).

`W` opens on `m.worktreeInventory()` already captured for the current repository (`internal/app/worktree_switcher_modal.go`). It must not synchronously `git worktree list` on open; that rule stays. It has no host column.

Sessions already lists every connected host’s projects, worktrees, and shells from `hostproto.Snapshot`. `AttentionOrigin.HostID` already exists so a local workspace cannot answer for a remote one of the same session name. Content panes already load remote files through `SourceContext`. None of that is reachable from `@` or `W`.

`switchProject` identity is a path. Two machines’ `sidecar` checkouts do not compare equal, and a remote root that happens to exist here would win. Last-worktree memory is also a path under `state.json` (`state.GetLastWorktreePath`, keyed by main repo path). A remote destination cannot be stored that way.

### What already exists, and where it actually lives

Five facts from the tree that change the shape of the work:

1. **The host registry is owned by `internal/overview`, not by the app.** `hosts.NewRegistry` is created in `overview.startHosts` (`internal/overview/hosts.go:108`), and snapshots, health, and derived project rows live in that model’s `hostRegistry` / `hostHealth` / `hostProjects` fields. The app never touches `hosts.*` today. So “read the snapshot from the app model” is not a lookup, it is a new accessor on `overview.Model` (health, project rows, incarnation) or a lift of registry ownership into the app. Prefer the accessor: one owner of the connection, one reader, no second `Sync` loop.
2. **Connections are already scope-independent and already after the first frame.** `SyncHosts` runs from `Start` in a command (`internal/app/model.go:720`), host stream messages reach the browser whatever is on screen (`internal/app/update.go:238`, `overview.IsHostMessage`), and `Stop` on leaving the tab deliberately does not disconnect (`overview.StopHosts` is separate). A snapshot is therefore in memory while the user sits in a project. That is what makes “local first, remote append” cheap.
3. **Two feature flags gate this, and one of them is not the obvious one.** `hosts.FromConfig` returns nil unless `sidecar_remote_hosts` is enabled (default off), and `m.overview` is only constructed when `cross_project_overview` is enabled (default true, `internal/app/model.go:576`). With Overview off there is no registry at all, so `@` has no remote rows. Either accept that (remote destinations require both flags, stated in help) or move registry ownership up. Decide in slice 1, do not discover it in slice 3.
4. **`ProjectKey` in the inventory is a filesystem path.** `workspaceinventory` sets `ProjectKey: canonical(root)` and worktree `Key: canonical(wt.Path)` (`internal/workspaceinventory/inventory.go:521`, `:578`), and `IsMain` is `canonical(wt.Path) == ProjectKey`. A host’s project key is a path on *that* machine. `hosts.ScopedKey(hostID, key)` already exists for scoping it and is what persistence and comparison must use. Note also that `uirequest.Instance.ProjectKey` is a different thing entirely, a basename from `projectdir.Lookup` (`internal/app/model.go:625`); do not let the two senses meet.
5. **The relay’s landing decision is one gate in `overview.handleUIRequest`.** A relayed open or layout is refused when the Sessions preview is not visible or the resolved row is not selected (`internal/overview/ui_requests.go:41`, `relayedOpenNotOnScreenReason`). That gate, not the transport, is what slice 3 changes.

Two smaller ones the switcher itself will trip over: `previewProjectTheme` resolves the theme from `destination.Path` (`internal/app/model.go:1391`), and `initProjectSwitcher` places the cursor by comparing `destination.Path == m.ui.WorkDir`. Both need a destination-aware form, or a remote row will resolve a remote path against this machine’s per-project theme config.

## Scope

In scope:

- Host-qualified destination identity on both switchers, including the label grammar above.
- Opening either modal with local rows on the first frame; remote rows appended from in-memory serve snapshots (and from snapshot updates while the modal is open).
- Binding `ScopeProject` to `(HostID, ProjectKey, Worktree identity)` without treating a remote root as a local `WorkDir`.
- Workspace plugin and existing content-pane source on that bound project. Lease claim and viewer-screen relay targeting this project workspace rather than only Sessions.
- Disabled remote rows when the host is not online, with the existing health sentence.
- Persisting last remote project/worktree as host-qualified identity, never as a raw remote path.
- Navbar / title / instance presence reflecting the bound host.
- Honest refusals from plugins that still assume a local tree.

Out of scope:

- Turning Files, Git, td, or Tasks into complete remote browsers in the first slices. Each is a follow-on slice on this plan, not a different plan, because the switcher is what makes them reachable.
- A public `sidecar project switch --host`. Agent-project-cli stays local until its own follow-on.
- Reverse SSH, a daemon, SSHFS, or serve executing a project switch.
- Changing Sessions’ fleet catalog. Sessions does not go away.
- Auto-adding a remote project to this machine’s `config.projects.list`. The remote config remains the host’s.

## Architecture

### 1. One destination type

Replace path-as-identity with a host-qualified destination both switchers share:

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

`HostIncarnation` is not decoration: `contentpanes.SourceContext` already carries one (`internal/contentpanes/source.go:26`) and deck identity compares it, so a bound destination that omits it cannot tell a reconnect from a host that restarted underneath the binding. Carry the incarnation the rows were read at, and treat a bump while bound as a re-resolve, not a silent continuation.

`@` lists Overview, then one row per local configured project, then one row per `(HostID, ProjectKey)` from each connected host’s snapshot. `W` lists worktrees of the **current project** on this machine and its same-named counterpart on connected hosts. A remote worktree of a different project does not appear in `W`; it appears under `@` for that project, then `W` once you are there.

Display is a pure function of `Destination` so `@`, `W`, the navbar, and toasts cannot drift.

### 2. Local first, remote append

`initProjectSwitcher` / `initWorktreeSwitcher` stay synchronous over data this process already has: `config.projects.list` and the captured local worktree inventory. That is the first paint.

Remote rows come from the snapshot the serve loop already keeps current, read through a new read-only accessor on `overview.Model` (project rows plus `hosts.Health` plus incarnation per host), not by giving the app a second registry. If the snapshot is present at open, the rows join the first paint too, and there is no extra round trip. If the host is still connecting, the modal opens with locals and a later host update inserts the remote rows, preserves the filter, and does not yank the cursor off a local row the user is already highlighting. Those updates already arrive in project scope (`overview.IsHostMessage`), so the insert needs no new stream, only a redraw of an open modal.

Opening `@` never waits on SSH, `git worktree list` on a remote root, or `RunSidecar`. That is the startup-latency rule applied to a modal: the first frame is local.

### 3. Entering a remote destination

`activateProjectSwitcherDestination` grows a remote branch that does **not** call `switchProject(path)`:

1. Leave Sessions (`leaveOverview`) if needed.
2. Set the app’s bound source to that `Destination`. `m.ui.WorkDir` must not become the remote root. Persist `{HostID, ProjectKey, WorktreeKey}` as the last location.
3. `plugin.Context` gains `HostID` (empty for local). `Epoch` still increments so in-flight local cmds die.
4. `registry.Reinit` still runs, but plugins that need a real directory see an empty local workdir and a non-empty `HostID`, and must not `os.Stat` the remote root.
5. Claim geometry leases for live sessions of that project as Sessions already does on row select.
6. Restore last worktree **on that host** for that project key, unless the user picked an explicit worktree destination.
7. Republish presence honestly. `uirequest.Instance` has no host field today and its `ProjectKey` is a local basename (`announceInstanceCmd`), so a bound viewer would otherwise advertise a remote project as if it were this machine’s, and a *local* agent’s `sidecar open` could match it. Add `HostID` to the record (additive, same shape as `uirequest.Origin` already has) and publish the bound host, or publish no project identity at all while bound. `sidecar project current` reads this record, so getting it wrong is an agent-visible bug, not a cosmetic one.
8. The workspace plugin’s `AttentionOrigin` must carry the bound `HostID`. It hard-codes local origins today (`internal/plugins/workspace/agent_triggers.go:188`), and `attentionOriginTransport` already has the field. Without this, an aerie agent’s `sidecar open` cannot tell that the laptop is looking at its shell, which is exactly what slice 3 needs.

Returning to a local project is today’s `switchProject(path)` and clears `HostID`, the incarnation, and the bound-destination presence.

### 4. The screen is this TUI

Viewer-screen, now implemented, lands a relayed `sidecar open` on the Sessions preview of `(HostID, TmuxSession)`, and refuses when that preview is not visible or the row is not selected (`overview.handleUIRequest`, `relayedOpenNotOnScreenReason`). Once this TUI is bound to that host project, the same announcement applies here: the origin shell’s project matches the bound `(HostID, ProjectKey)`, the lease owner is this instance, and the pane opens in the project workspace deck.

“Which surface is this request’s screen” therefore becomes a decision with two possible answers, and it must be **one** decider that both surfaces call, in the shape of `sessionsOwnsCreateSplit` (`internal/app/scope.go`), not a copy of the gate inside the workspace plugin. A second landing rule is the same class of bug the pane-parity rule in `CLAUDE.md` exists to prevent, and it fails as a request that lands twice or nowhere.

If the user is in a *different* local project while an aerie agent opens a file, the request is off-screen for this TUI (exit 4 on layout; relayed open does not queue). Sessions can still show the row; it is not the bound screen unless the user is in Sessions looking at it.

Sitting at aerie’s own TUI still wins the lease by typing, per geometry_lease.go. This plan does not steal a screen the human at the host is using.

### 5. Plugins, in order

| Plugin | First remote-bound behaviour |
| --- | --- |
| Workspaces | Host inventory for that project; live terminals via existing control-mode proxy. Create shell/worktree already exists on a remote Sessions row (Phase C) and is reused. |
| Content panes | Existing `SourceContext`. Nested links stay on that host. |
| Files | Hidden or a single unavailable view until its slice. No local tree of a twin path. |
| Git | Same. |
| td / Tasks / Notes | Same, then host td store via the content verbs and `RunSidecar`. |
| Conversations | Remains demand-gated per the remote-hosts plan. |

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
9. **Files/Git/td/Tasks remoting is this plan’s later slices**, not a surprise side effect of the switcher. The steel thread may ship with those plugins refusing.
10. **Names join projects across machines; keys never do.** An inventory `ProjectKey` is `canonical(root)` on the machine that produced it, so the local checkout and aerie’s can never compare equal, and a key that *does* compare equal across hosts means two machines happen to use the same path, which is the twin-path failure, not a match. `W` pairs the current project with the host project of the same registered name (case-insensitive, trimmed), and a rename on either side visibly breaks the pairing rather than silently pointing at the wrong tree. Identity in state and in comparisons is always `hosts.ScopedKey(HostID, key)`.
11. **Remote destinations require `sidecar_remote_hosts` and, today, `cross_project_overview`.** The registry lives in the Overview model, so with Overview off there is nothing to read. Slice 1 either states that dependency in help and refuses cleanly, or moves ownership. It does not leave it implicit.

## Slices

### Slice 0 — destination identity and labels — implemented (`dc5e3be0`, td-823e94)

`Destination` type, display function, tests for local, remote project, remote worktree, and filter. Navbar/title helpers exist. Read-only `overview.Model.HostCatalog()` hands the app host project rows, health, incarnation, and in-memory workspaces. Project keys are unscoped; persist/compare via `hosts.ScopedKey`.

### Slice 1 — steel thread: `@` lists remotes, Enter binds Workspaces — implemented (`cfb2fd89`, refusal fix `6dd5b02d`, td-72a679)

`@` paints local rows immediately, appends connected-host projects from snapshots, Enter on `[aerie] Sidecar` binds `ScopeProject`+`HostID`, workspace plugin shows that host’s shells via the existing control-mode proxy, Files/Git/td/Tasks refuse with the host named (Init/Start/View/Update). Local `@` path unchanged when no host is connected. Theme preview and cursor-on-current stop keying off `destination.Path`. Unreachable hosts keep last-known catalog rows for `@` only; Sessions still drops `hostResults` on `!Shows()`. Create shell/worktree from a bound project workspace is refused naming the host; Phase C Sessions create is unchanged.

### Slice 2 — `W` across hosts for the current project key — implemented (`813169a3`, W-Enter fix `9844590d`, td-90757e)

Worktree switcher local-first, then `[aerie] Sidecar [[feature]]` for linked worktrees of the same-named host project (decision 10). Host main checkout is not a W row. Enter binds through `bindRemoteDestination`. Last-worktree memory is per `hosts.ScopedKey(HostID, ProjectKey)` (`GetLastRemoteWorktree`); `@` Enter restores it when still in the catalog. W Enter is an explicit destination (`restoreLastWorktree=false`) so a local main pick after a remote bind does not follow `LastWorktreePath`.

### Slice 3 — viewer-screen lands on the bound project

Relayed `sidecar open` / `layout` from a host pane whose project matches the bound remote destination apply to this project workspace when this instance holds the lease. Off-screen remains exit 4. Sessions landing remains for when the user is in Sessions, not bound to that project. The work is one shared decider over the two surfaces plus the two presence changes from Architecture §3 (instance `HostID`, workspace `AttentionOrigin.HostID`); the refusal reasons stay the ones viewer-screen already ships.

Viewer-screen is implemented, so this dependency is satisfied. Its landing gate lives in `overview.handleUIRequest` and must be extended, not duplicated.

### Slice 4 — Files (then Git, td, Tasks) as remote-capable plugins

One plugin at a time, each through `Source` / `RunSidecar` / host content verbs, each with the twin-path tripwire. Not a second compositor, not a mounted FS.

### Slice 5 — docs, agent-project-cli note, isolated proof

CLI/help: `@` and `W` name remote destinations, and help states the flag requirement from decision 11. Isolated two-machine proof: local `@` stays fast with a slow host; Enter on `[host] Project` does not open the local twin; Workspaces shows the host’s shells; `sidecar project current` on a local shell does not report the bound remote project as this machine’s. The two-machine recipe extends `docs/guides/active/remote-viewer-screen-proof.md` rather than adding a third.

## Proof and isolation

Same bar as remote content panes: private tmux sockets and private Sidecar state on both machines, `SIDECAR_ISOLATED_STATE=1`, no default of a live workstation. A proof that Reinit’s a path under the viewer’s `$HOME` because the host reported that string has failed even if the tests are green.

## Related plan updates

- [sidecar-remote-hosts.md](sidecar-remote-hosts.md): complete remote Files/Git browsing is no longer “outside all three”; it is this plan’s later slices, entered through `@`.
- [remote-host-viewer-screen.md](../implemented/remote-host-viewer-screen.md): once a remote project is bound, the announcement target is this project workspace, not only Sessions.
- [remote-host-content-pane-parity.md](../implemented/remote-host-content-pane-parity.md): the “future remote project Workspace” the source seam was saved for is this plan.

## Changelog

- **2026-09-01** — Slices 0–2 implemented on `remote-viewer-screen` (td-f7855c). Remaining: slices 3–5.
- **2026-09-01** — Reviewed against the tree. Corrections: the host registry is owned by `internal/overview`, so slice 1 needs an accessor and inherits the `cross_project_overview` dependency (decision 11); inventory `ProjectKey` is a path on the owning machine, so `W` pairs by project name and identity persists through `hosts.ScopedKey` (decision 10); `Destination` carries `HostIncarnation` to match `contentpanes.SourceContext`; binding must publish `HostID` on instance presence and on the workspace plugin's `AttentionOrigin`, neither of which carries one today; viewer-screen is implemented, and its landing gate is extended by one shared decider rather than copied; theme preview and cursor-on-current stop keying off a path.
- **2026-09-01** — Created. `@`/`W` list host-qualified destinations asynchronously; entering one binds this TUI as that remote project’s screen; plugin remoting after Workspaces is sliced.
