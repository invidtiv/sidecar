# M1-A1 local Sessions catalog proof

This slice implements the local catalog/query half of M1-A. It does not implement hub-routed remote catalog collection or remote terminal control.

## Implemented boundary

- `internal/workspacecatalog.ProjectItem` is the state-free workspace-to-list projection used by both desktop Overview and the mobile catalog. The extraction preserves the previous Overview status, group, marker, detail, host, and stale semantics; `workspacelist` remains the only search/sort/group implementation.
- `sidecar mobile sessions --json` and the `sessions` JSONL request return bounded snapshots with hub/config authority, explicit host and project failures, final attachment verdicts, and Activity, Project, Recent, or Name ordering.
- The local provider uses the configured project list, one tmux pane inventory, `CollectProjectInventory`, global shell claims, and `RefreshProjectStatus`. It retains plain worktrees and durable shells.
- A catalog candidate is resolved through the production managed-shell resolver before filtering or generation. A ready row carries the exact `expected_target`; selection sends both `target` and `expected_target`, and `resolve` refuses a same-name replacement. Duplicate target names, stale rows, ambiguous panes, missing durable identity, missing panes, and unsupported worktree attachments never carry authority.
- Catalog collection is refused on a service stream with an active terminal attachment so project/process/tmux reads cannot delay that attachment's input and heartbeat loop.

## Canonical synthetic corpus

`testdata/mobile-protocol/v0/sessions-catalog.json` is emitted by the Go producer test from synthetic inventory only. Its SHA-256 is `082ecc241e76ad5b2733fbad591e0508d248e58ad3d8ac38140b1377f6954373`. The ten cases pin all four sort modes, host search, provider filtering, and ready/ambiguous/stale/unsupported state filters. The full snapshots cover agent and plain worktrees, a durable managed shell, a missing durable identity, ambiguous panes, a duplicate terminal target across projects, a stale project, and a missing configured project. `internal/mobileproto` verifies the manifest and selection invariants; `internal/mobile` verifies the checked-in bytes still exactly match its producer.

## Isolated real-service proof

The proof reused the existing task-owned private M0 tmux/state fixture read-only. The candidate binary ran with inherited `TMUX` and `TMUX_PANE` removed, `XDG_STATE_HOME=/tmp/sidecar-mobile-m0c-live-final/state`, `TMUX_TMPDIR=/tmp/sidecar-mobile-m0c-live-final/tmux`, `SIDECAR_ISOLATED_STATE=1`, and an explicit temporary config containing the CLI-created disposable project. It did not change the installed Sidecar binary, global config, default tmux server, helper target, or manifest.

The JSON CLI returned the disposable main worktree as `unavailable` and its CLI-created live managed shell as `ready`. A fresh stdio service then completed `hello → sessions --state ready → resolve` and exact-matched the row's expected identity: workspace `desktop-project`, session `sidecar-sh-desktop-project-1`, pane `%1`, server incarnation `pid=8416`, and the same target generation. The service exited 0 with empty stderr after stdin closed. Evidence is `/tmp/sidecar-mobile-catalog-a1-service-proof.json` (SHA-256 `59543d9b8ab895181818a7ba785383e7d13d34336d410fabdc50f922cb152fba`); the proof binary is `/tmp/sidecar-mobile-catalog-a1-sidecar` (SHA-256 `2b4fcf840281a65bf6a9f13c04325eb6628aa6133208e8ab5c0a34a415b69653`).

Two separate CLI processes queried the same unchanged idle shell with a 335 ms interval; their recorded observation times advanced by 471 ms. `changed_at`, `expected_target`, and catalog generation remained identical because the provider loaded the desktop activity store. `/tmp/sidecar-mobile-catalog-a1-repeat.json` records the comparison (SHA-256 `49192d44536dc409d9a842b6e9896482b474cbf4d0bb56988b89c58251a80627`). A headless hub may have no activity history: in that case the catalog leaves `changed_at` empty until its retained provider observes a real state/evidence transition, rather than presenting the first observation time as the unknown start of an existing state. Focused production-provider regressions cover repeated reads in one process, two independent cold processes with stable content generations, and the later transition that establishes a real change time.

## Remaining M1 work

M1-A2 must compose registered-host snapshots and preserve host outage, stale-client, retargeting, and configuration-generation refusals. A following reviewed Go slice must route remote resolve/open/control through the existing host transport; displaying remote rows does not make the M0 local managed-shell resolver remote-aware. Worktree terminal candidates and their multi-pane picker also remain unsupported. M1-B owns the native Sessions surface only after these server capabilities have reviewed wire fixtures and real cross-host proof.

## M1-A2 implementation boundary

These are proposed seams for the next slice and require independent design review before implementation, including a decision about remote owner/configuration authority.

A2 should compose `hosts.Registry` snapshots through `hosts.ProjectResults` into this same `CatalogInput`, preserving disabled, connecting, stale, unreachable, incompatible, and unavailable host states. `hostproto.Item` must carry the durable managed-shell creation identity that A1 requires; the snapshot already carries the remote tmux server incarnation. Only a current online row with complete source identity may become ready.

The same v0 resolve/open/control/input/resize/release messages should route by an opaque server-owned target handle. That handle must bind the hub configuration generation, host ID, registry client incarnation, durable shell identity, pane, and remote tmux server incarnation. Every operation rechecks that authority and refuses host removal, retargeting, stale health, server restart, or same-name replacement without falling back to the local resolver. One candidate is a narrow target-scoped manager factory for `mobile.Service` that reuses `hosts.Client.ControlCommand` with `tty.NewRemoteControlManager`; design review must compare it with reusing the owning host's mobile service before selecting a route. Native code should not receive SSH credentials or implement routing.

Injected registry snapshots, client incarnations, and control spawners can prove this boundary while a registered remote machine is offline, including same-name cross-host collisions and the no-local-fallback rule. A real isolated cross-host catalog-to-terminal journey remains the A2 completion gate when a registered host is reachable.
