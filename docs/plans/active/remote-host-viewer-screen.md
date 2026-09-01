# The viewer owns the screen

Status: **active, proposed; decisions settled** **Created:** 2026-08-31 **Scope:** host→viewer relay of `sidecar open` / `layout get|apply|move` from a Sidecar-managed pane; mixed create-modal routing on a remote Sessions row; serve announcement of host `uirequest` files; no public `sidecar open --host`.

Related: [Sidecar as its own remote host runtime](sidecar-remote-hosts.md) is the controlling transport plan. [Remote host content-pane parity](../implemented/remote-host-content-pane-parity.md) is the read path this lands on, and is the plan that deferred relaying `sidecar open` from the remote host. [Pane layout control](../implemented/pane-layout-control.md) owns the layout vocabulary, the never-queue rule, and (with [unified create](../implemented/unified-create-workspace-modal.md)) the `n` switcher this splits by ownership. [Agent-facing open CLI](../implemented/agent-open-in-split-cli.md) owns the `uirequest` bus.

## Decision first

When you are sitting at the laptop looking at a workspace that lives on another machine, **that laptop Sidecar is the screen**. An agent in a Sidecar-managed pane on the host that runs `sidecar open` or `sidecar layout get|apply|move` is talking to that screen — not to a TUI that may not be running on the host, and not to a queued apply that will land on whatever tree happens to be selected later.

The host still owns workspace objects. A new shell, a new worktree, and a tmux split of that remote session come into existence on the host. The viewer then places the resulting leaf on the screen it already holds.

The geometry lease (`@sidecar-owner` on the tmux session, `internal/tty/geometry_lease.go`) already names who last used the pane. That instance is the only consumer of screen-mutating requests for that session. Last input wins; tokens carry durations, not timestamps.

```text
agent in a Sidecar-managed pane on the host
        |
        v
sidecar open / layout get|apply|move
        |
        v
host stateDir/requests   (existing uirequest contract)
        |
        +-- host TUI holds the lease ------------> apply locally, as today
        |
        +-- laptop viewer holds the lease -------> serve announces
                                                      |
                                                      v
                                              viewer Sessions preview
                                              (contentpanes.Source for
                                               file/issue/note/diff/resource;
                                               viewer tree for placement)
                                                      |
                                                      v
                                              one-shot ack on the host
                                              (CLI exit codes unchanged)
```

Serve does not execute layout. It observes a request file the CLI already wrote, tells the connected viewer, and otherwise stays the inventory stream it is. Acks return through a one-shot `hosts.RunSidecar` verb, the same mutation seam Phase C chose instead of a serve request channel.

There is still no public `sidecar open --host`. An agent on the host runs `sidecar open`, the same command it runs locally. Routing is the lease, not a flag.

## User and agent contract

| Who / where | Action | Required result |
| --- | --- | --- |
| Agent in a Sidecar-managed pane on the host; laptop holds the lease and is viewing that Sessions row | `sidecar open README.md` (issue, note, diff, resource too) | The pane opens on the laptop, through the same `contentpanes.Deck` and `SourceContext` a click already uses. The host TUI, if running, does not also open it. |
| Same agent | `sidecar layout get --json` | The JSON is the laptop’s grid for that row, not a host TUI tree and not an empty answer. |
| Same agent | `sidecar layout apply` / `layout move` | The laptop tree changes. Layout never queues: if that row is not on the laptop’s screen, exit 4 with the reason. |
| Same agent; nobody holds a live lease, or the lease holder is not connected | any of the above | Refuse. Do not queue. Do not apply on a host TUI the user is not looking at. |
| Same agent; the host’s own Sidecar TUI holds the lease (you walked over to the mini) | any of the above | Today’s local path. The laptop is a lease non-owner and must not steal the open. |
| Cron, a random SSH, a process with no Sidecar origin | `sidecar open` | Unchanged: no origin shell, or no watching instance. This plan does not grow a relay for arbitrary host processes. |
| You, at the laptop, `n` on a remote Sessions row | Shell / Worktree | Create on the host (Phase C already does this). |
| Same `n` | Terminal split | Create the tmux session on the host; place the leaf on the laptop. |
| Same `n` | File / Issue / Note / Diff / Resource | Pickers list the **host’s** catalogs, then open in the laptop deck through the existing source path. |
| You, at the laptop, keyboard layout / `sidecar layout … --sessions` | get / apply / move | Already mutates the laptop tree for a remote row and sends no layout mutation to the host (td-2ec104). This plan does not change that. |
| Agent on the laptop | `sidecar open --sessions [ROW]` | Same handler the relay lands on. No `--host` flag. |

A success must not be a same-named local file, issue, or tmux session. A refusal must say why.

## Current behavior

The click path is done. [Content-pane parity](../implemented/remote-host-content-pane-parity.md) resolves and loads on the owning host and renders in the viewer’s shared deck. What is not done is the agent-command path, and the create modal still pretends a remote row is a local one for every kind except shell and worktree.

**An agent on the host talks to the host’s state tree.** `sidecar open` / `layout` write `uirequest` files under that machine’s `stateDir/requests` (`internal/uirequest/bus.go`). Every live Sidecar on **that** machine may pick them up. The laptop never sees those files: there is no SSHFS, and the host cannot SSH back. The only host→viewer pipe is `sidecar host serve` JSONL.

**Overview drops remote rows on open.** `internal/overview/ui_requests.go` skips `ws.Remote()` when binding an `ActionOpen` to a Sessions row, so a session name that exists on two machines cannot attach at random. That guard is right for a *local* request. It is also why a request that *should* name a remote row currently cannot.

**Layout from the laptop already knows the screen is here.** `sidecar layout get|apply|move --sessions` mutates this machine’s viewer tree for a remote row and sends no layout mutation to that host (CHANGELOG, td-2ec104). `layout` never queues (`AGENTS.md`, `docs/reference/cli.md`). Keyboard placement on the laptop is the same tree. What the host agent cannot do is read or compose that tree.

**The create modal is mixed in the wrong places.** Phase C remotes shell and worktree create from Sessions (`internal/overview/remote_actions.go`). File / issue / note / diff pickers deliberately answer nothing for a remote row (`internal/overview/create_picker.go`: `loadCreatePickerData` returns nil, `localSelectedRoot` is empty) so they cannot fill from a local twin — but they also cannot fill from the host. Resource rows are the **viewer’s** `configuredProviders()`, not the host’s describe matchers. Terminal split is offered whenever `WorkspaceTerminalPanel` is on (`global_create.go`), and `createPreviewTerminalSplit` calls `termpanes.EnsureSession`, which is local `tmux new-session` (`internal/termpanes/session.go`). On a remote row that would create a session on the laptop named after the host’s tmux session, with `-c` pointed at a path that may not exist here.

**Serve is one-way observation.** `hostproto.Version` is 2 because `KindNotify` was a bump: a v1 viewer would silently drop notifications. Hello capabilities are additive (`ContentReadV1` is the content-pane precedent). Serve reads stdin only as the stdio pipe; after hello it encodes outbound JSONL and does not take a request channel. `TestServeIsReadOnly` still forbids mutating tmux. The one write serve already has is the reap, through `shellstate`’s guarded writer.

**The lease is already the cross-machine owner.** `selfID` is `hostname-pid` (`geometry_lease.go`). A foreign token is treated as live; input evidence is local to each claimant. That is the same fact this plan uses for UI requests.

## Scope

In scope:

- Relaying `sidecar open` and `sidecar layout get|apply|move` issued **inside a Sidecar-managed pane** on a registered host, when the geometry lease for that tmux session is held by a connected viewer, onto that viewer’s Sessions preview of the matching `(HostID, TmuxName)` row.
- Binding those requests on the viewer through the existing content source for file / issue / note / diff / resource, and through the existing Sessions layout host for get / apply / move.
- `sidecar open --sessions [ROW]` on the viewer, so a laptop-side agent targets the same handler without a `--host` flag.
- Honest refusals: not a Sidecar origin, nobody viewing, lease holder not connected, viewer or host too old for the relay, row not on screen (layout, and open-on-relay).
- Mixed `n` on a remote Sessions row: host catalogs then viewer open for File / Issue / Note / Diff / Resource; host tmux + viewer leaf for Terminal split; Phase C shell / worktree create unchanged.
- `layout apply` of a new shell pane on a remote row: same mixed split as `n` (host tmux, viewer leaf), once the split path exists.
- Docs: CLI reference, agent instructions, `website/docs/remote-hosts.md`.
- Isolated proof. No default of a live workstation.

Out of scope:

- Relaying from an arbitrary process on the host (cron, a login SSH that is not a Sidecar-managed pane). That is the original content-pane deferral, still declined.
- A public `sidecar open --host` / `layout --host`.
- Serve executing layout, taking a geometry lease, or growing a general request channel.
- Queuing a relayed open or apply for when the user comes back to that row. Layout already refuses off-screen; the relay uses that rule for open as well.
- Changing local `sidecar open` pending-view queueing when the lease holder is a Sidecar on the same machine.
- Remote Files / Git / Tasks / td plugin browsing, remote inline edit, host-backed `ctrl+p` / project search (still the content-pane follow-ons).
- Reverse SSH, a daemon, SSHFS, or a second streaming protocol.
- Plugin-deck `ctrl+n` on a remote project Workspace. Remote workspaces exist only in Sessions today.

## Architecture

### 1. Ownership split

Two questions, one lease:

| Object | Owner | Why |
| --- | --- | --- |
| Pane tree, placement, focus, scroll, which file is on screen | The Sidecar that holds `@sidecar-owner` for the origin tmux session | Sidecar owns the tree. The tree the user is looking at is the one that last claimed geometry. |
| Shell record, worktree, tmux session / split | The machine whose tmux server holds that session | Those objects vanish if that Sidecar/tmux is uninstalled. Phase C already creates them with one-shot `sidecar --json` over ControlMaster. |
| File / issue / note / diff / resource **bytes** | The host, via `sidecar content resolve\|read` | Content-pane parity. The relay must not pass a remote path to local I/O. |

An `layout apply` that asks for both a file and a new shell pane is one request with two owners: the viewer commits the tree; the shell leaf’s tmux session is created on the host, then attached as a remote terminal the way a Sessions primary already is.

### 2. The CLI contract does not change

The host agent still writes a `uirequest` file and waits for acks. Origin resolution is still `SIDECAR_SHELL` / `--shell` / LookupOrigin on **that** machine. Targets still resolve against **that** machine’s workdir. Exit codes stay: 0 opened, 2 usage, 3 no instance, 4 declined.

What changes is who is allowed to ack, and how a foreign lease holder is reached.

The host CLI, before waiting out the full `--wait` budget, reads `@sidecar-owner` for the origin session and the live viewer-presence file (below):

1. Lease owner is this machine and a local Sidecar is alive → today’s write-and-wait. Local pending-view for `open` is unchanged.
2. Lease owner matches a live viewer presence that advertises the relay capability → write the request, wait for that viewer’s ack.
3. Lease owner is foreign but no live capable presence → refuse immediately, naming the machine that holds the screen and that it cannot receive pane requests (too old, disconnected, or presence expired).
4. No live lease, and no local instance → exit 3, same as today (“no instance watching”).
5. No live lease, but a local TUI is announced → today’s local path (open may queue, layout declines if off-screen). This is “you are on this machine but looking at a different shell,” not the remote relay.

A relayed `open` does **not** queue. If the matching remote row is not on the viewer’s screen, the viewer acks declined (exit 4) with that reason. A queued remote open would apply against whatever row is selected later; layout already refused that class of bug, and the relay does not reintroduce it for open.

### 3. Serve announces; it does not apply

Follow the shells.json watch, not a new protocol:

- Serve already watches manifests (`internal/hostserve/manifest_watch.go`) and pushes `KindNotify` as a one-shot event that must not be folded into snapshot state (`hostproto.KindNotify` comments). A UI request is the same shape: it happened once, a reconnect must not replay it, and it is not inventory.
- Watch `stateDir/requests` for new `*-open.json` / `*-layout.json` files. Ignore `*.acks`. Coalesce. Never start a collection of its own from a request signal.
- Emit a new message kind, working name `KindUIRequest`, carrying the request id, action, origin, target/payload, and TTL. Serve stamps the **viewer’s registered HostID** (the connection’s host, not anything the host CLI wrote). `Origin.HostID` on the host file is empty because from the host’s point of view this is local.
- Cap the encoded announcement well under `MaxLineBytes`. A layout spec that will not fit is a decline the CLI can report, not a dropped line.
- Forward only when the origin session’s lease owner equals this serve’s viewer instance. Two viewers each spawn their own serve; only the lease holder’s serve announces. The other viewer is a pane-fit non-owner and must not open a parallel tree.

**Not a protocol bump.** `KindNotify` moved `Version` to 2 because a v1 viewer would silently drop a notification the user had enabled. A v2 viewer that ignores an unknown `KindUIRequest` does not claim success: the host CLI never sees an ack, and the presence file is missing because an old viewer does not set `SIDECAR_VIEWER_INSTANCE`. The CLI fast-refuses rather than waiting to time out. Additive JSON, decoder ignores unknown kinds and fields, same as `ContentReadV1`.

**Not `KindNotify`.** That payload is a bounded agent-lifecycle event with withdrawal and a 15s dedupe key. A layout spec is a different product. Overloading notify would mix a toast the user can ignore with a command the agent is blocked on.

**No request channel on serve.** Serve does not read a viewer “please apply this” message and then mutate a tree it does not have. The viewer already has the tree. Serve does not write ack files either. The viewer acks with a one-shot `hosts.RunSidecar` verb (`sidecar request ack … --json`) into the host `*.acks` directory, which is the Phase C mutation seam (one ssh exec, measured 82–383 ms on a real link; `open` already waits 1200 ms by default). `TestServeIsReadOnly` keeps its tmux forbidden list. Presence files under `stateDir/viewers/` are the only new serve write besides reap; they are ephemeral, isolation-gated, and not shells.json.

### 4. Viewer identity, without a handshake flag old binaries reject

Old `sidecar host serve` must keep spawning. A new required flag would fail the connection against every host not yet updated.

The viewer sets `SIDECAR_VIEWER_INSTANCE` to its lease `selfID` (`hostname-pid`) in the environment of the serve spawn. Old serve ignores the variable. New serve writes and refreshes `stateDir/viewers/<selfID>.json` `{instance, pid, capabilities: ["uiRequestRelayV1"], expiresAt}` each cycle, TTL just above the live poll so a connected viewer never looks gone. `config.AssertIsolatedPath` on that tree. Hello grows an additive `Verbs.UIRequestRelayV1` so a new viewer talking to an old host knows not to expect announcements.

The host CLI’s lease-vs-presence check in §2 is what makes mixed versions honest instead of a 1.2s hang.

### 5. Viewer apply is the Sessions preview, host-qualified

Relayed requests are never applied by the viewer’s project Workspaces plugin. Remote workspaces exist only in Sessions. The handler maps `(announcement.HostID, origin.TmuxSession)` to the catalog row the same way `attachPreviewSession` now matches `(source HostID, TmuxName)` (content-pane slice 7, `4c713ca9`). Display-name fallback still skips remote rows; the pair is the identity.

`ActionOpen` goes through the same `openPreviewTarget` / deck path a click uses, so a remote file cannot become local I/O. `ActionLayout` goes through `applyLayoutRequest`, which already requires `preview.visible` and never queues. `ResolveTargets` for a remote row must not treat `ws.Path` as a local root; file and diff targets resolve through `contentpanes.Source`, the same containment the content verbs already enforce.

`sidecar open --sessions [ROW]` is the laptop-side entry to that same handler. `--sessions` stays mutually exclusive with `--shell` / `--project`, matching layout.

Host TUI consumption: if this instance does not hold the origin session’s lease, it does not apply `ActionOpen` / `ActionLayout` for that origin and does not ack. The foreign viewer will, or nobody will. Rename / create / notify / config-reload are not screen ownership and stay as they are.

### 6. Mixed `n` (viewer-driven, opposite direction)

The create modal already runs on the laptop. It does not need the host→viewer relay. It needs the ownership split at submit time:

| Kind | Catalog | Mutation |
| --- | --- | --- |
| Shell, Worktree | Phase C (host project list, branches) | Host `workspaceops` via one-shot create. Already shipped. |
| Terminal split | none | Viewer plans and fit-tests the leaf against the preview tree (already does). Create the tmux session on the **host** (not `termpanes.EnsureSession` locally), then attach as a remote terminal leaf. Same path as `layout apply` of `kind: shell` without a carried session. |
| File, Diff, Issue, Note | Host list, bounded | Viewer opens through `openPreviewTarget` + `SourceContext`. |
| Resource | Host `content describe` matchers, already cached per host incarnation | Viewer opens a Resource pane against that host, never the viewer’s provider snapshot. |

Catalog fetch is viewer→host `hosts.RunSidecar`, a small `sidecar content catalog --workspace ID --kind … --json` (or an extension of `content describe` if the payload stays small). The local functions already exist: `filefind.ScanPaths`, `workspaceops.RecentDiffRefs` / `RecentIssues` / `RecentNotes`. They must run on the host. Until the catalog returns, those picker steps stay empty — the current fail-closed behaviour — rather than filling from a local twin.

Until the host-tmux split path exists, Terminal split on a remote row must be disabled with the reason, not offered and then created locally.

## Settled decisions

1. **The lease holder is the screen.** Geometry lease, not “is a TUI running on this hostname,” not “who spawned serve.”
2. **Host uirequest files stay the agent-facing bus.** The host agent does not learn about SSH, serve, or HostID.
3. **Serve announces; the viewer applies; acks are one-shot CLI on the host.** No serve request channel, no serve-executed layout, no proto bump.
4. **Relayed open never queues.** Off-screen is exit 4. Local same-machine pending-view is unchanged.
5. **Only a Sidecar-managed origin relays.** LookupOrigin (or `SIDECAR_SHELL`) must succeed. Arbitrary host processes stay refused.
6. **No `sidecar open --host`.** `--sessions` on the viewer is the laptop-side targeting flag, already the layout precedent.
7. **Create-modal kinds split by ownership**, table in §6. Phase C shell/worktree create is not redone.
8. **A new shell pane on a remote row is a host tmux object plus a viewer leaf**, whether it came from `n` or from `layout apply`. Until that split path ships, the kind is declined on remote rows rather than calling local `tmux new-session`.
9. **Failure isolation is unchanged:** a dead relay marks that request declined; it does not block a frame, wipe a manifest, or restart tmux.

## Slices

Slices 0–1 are the steel thread. Pause there if the announcement-plus-one-open-path needs a real two-machine pass before the rest of the vocabulary.

### Slice 0 — announcement seam

Presence file, `SIDECAR_VIEWER_INSTANCE`, additive `UIRequestRelayV1`, `KindUIRequest` + `Validate`, serve watch on `stateDir/requests` that emits and does not apply. Tests: unknown kind ignored by a v2 decoder; serve does not issue forbidden tmux; a request whose lease owner is not this viewer is not announced; isolation refuses a viewers path outside the isolated tree. No user-visible behaviour yet except Hello capability.

### Slice 1 — steel thread: `sidecar open` of a file

Host agent in a Sidecar-managed pane, laptop holds the lease and is viewing that row, `sidecar open path/to/file[:line]` opens a Document pane on the laptop via `SourceContext`. Host TUI does not double-open. Viewer acks via the one-shot verb. Fast-refuse when presence is missing. Overview binds `(HostID, TmuxSession)` and stops skipping every remote row for a *relayed* request. `sidecar open --sessions` uses the same handler.

Proof: unit/integration with a stub serve announcement and a conflicting local twin (`LOCAL-TWIN` vs `REMOTE-MARKER`, the content-pane fixture shape). Isolated two-machine can wait for slice 5, but this slice must not be green only because `openPreviewDoc` was called directly — drive `handleUIRequest` / the announcement message.

### Slice 2 — the rest of `open`, and `layout get`

Issue, note, diff, resource opens through the same relay, each using the content-pane source (resource matchers from the host cache, never the viewer snapshot). `layout get` returns the viewer’s `layoutreport` for that row. Off-screen get is exit 4. Host-TUI-holds-lease still answers locally.

### Slice 3 — `layout apply` / `layout move` of the viewer tree

Apply and move relay onto `overviewLayoutHost`. Passive kinds resolve through `Source`. All-or-nothing stays all-or-nothing. A spec or `--pane` that would create a new shell leaf declines with an actionable reason until slice 4; carrying an existing live shell session still works, because that object already exists on the host. Move is viewer-tree only and should already be correct for `--sessions`; the relay is the new caller.

### Slice 4 — host tmux splits and the `n` catalogs

`termpanes.EnsureSession` is not the remote path. A host-aware ensure (one-shot `tmux new-session` on the host through the existing transport, then attach with the host-aware control factory) serves both `n` Terminal split and `layout apply` `kind: shell`. Disable the split row on a remote selection until that path is wired.

Picker catalogs via `content catalog` (or equivalent) over `RunSidecar`. Resource rows from the host describe cache. Submit still goes through `openPreviewTarget`. Shell/worktree create remains Phase C.

### Slice 5 — docs and isolated proof

Regenerate `docs/reference/cli.md` and `sidecar --agents` copy: same verbs, new landing rule, refusal reasons. `website/docs/remote-hosts.md` currently says agents should `cat` / `td show` rather than ask Sidecar to put a pane in front of the user — that sentence is wrong once slice 1 ships; replace it with the lease-holder rule and keep the “no `--host`” sentence. `.claude/skills/coordinate-agents` and `AGENTS.md` get one paragraph: from a Sidecar pane on a host you are viewing, `open` / `layout` are the laptop screen.

Isolated proof: private sockets and private state on **both** machines, `SIDECAR_ISOLATED_STATE=1`, `SPIKE_HOST` required with no live-workstation default, same discipline as `docs/guides/active/remote-content-pane-proof.md`. Journeys: open a host-only file from the host agent onto the viewer; layout get matches the viewer grid; off-screen apply exits 4; host TUI holding the lease keeps the open local; `n` File lists host files and opens the host body; Terminal split’s tmux session exists on the host server, not the viewer’s. Never `tmux kill-server` without `-S` / `-L` on the private socket.

## Proof and isolation

A proof that isolates only the viewer’s tmux socket can still write the host’s real `stateDir/requests` and the host’s real `shells.json`. Both axes, both machines. `tmux-drive.sh paths` / the content-pane `check-isolation` recipe are the pattern; extend, do not invent a third.

Default tmux servers stay up. Installing this branch on a host does not authorize restarting tmux.

## Related plan updates

- [sidecar-remote-hosts.md](sidecar-remote-hosts.md): this plan is the follow-on for agent UI commands; serve gains an observation of `uirequest` files and an additive message kind; it still does not execute layout or take a lease.
- [remote-host-content-pane-parity.md](../implemented/remote-host-content-pane-parity.md): the deferred “relaying `sidecar open`” bullet now points here, still excluding arbitrary processes.
- Website and CLI docs update in slice 5, not in this file.

## Changelog

- **2026-08-31** — Created. Viewer owns the screen (lease holder); host owns workspace objects; serve announces host `uirequest` files and does not apply them; relayed open never queues; mixed `n` routing; no `--host` flag; steel thread is `sidecar open` of a file onto the Sessions preview.
