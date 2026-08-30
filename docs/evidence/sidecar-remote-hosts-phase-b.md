# Sidecar remote hosts Phase B evidence

Date: 2026-08-29 (America/Los_Angeles)

Final candidate: `7cff6d1e4423fdeb8475ef71f882490f560c4721`

Tasks: `td-f10d6f`, `td-998e58`, `td-42b724`

## Outcome

Phase B's exit gate passed. A local Sidecar entered and drove a pane on a second real machine through the existing SSH-proxied tmux control channel. Ordered input arrived exactly, both machines could take geometry ownership back merely by typing, each takeover restored that machine's current viewport, and ownership cleanup preserved a peer's lease while clearing the exiting owner's lease.

The implementation remained read-only at the Sidecar host protocol boundary. Phase C mutations were not added.

## Isolation

The final proof used both required isolation axes on both machines:

| Axis | Local viewer | Remote host |
| --- | --- | --- |
| Machine | `aerie` | `marcusbook` |
| Sidecar binary | `/tmp/sidecar-phase-b-local` | `/tmp/sidecar-spike-phase-b-501/sidecar` |
| Reported build | `7cff6d1e4423` | `7cff6d1e4423` |
| State root | `/private/tmp/sidecar-drive-phase-b-501/state` | `/tmp/sidecar-spike-phase-b-501/state` |
| Config | `/private/tmp/sidecar-drive-phase-b-501/config/config.json` | `/tmp/sidecar-spike-phase-b-501/config/config.json` |
| Private tmux | `/private/tmp/sidecar-drive-phase-b-501/tmux/tmux-501/default` plus outer `-L sidecar-drive` | `/tmp/sidecar-spike-phase-b-501/tmux/tmux-501/default` |
| Human-TUI driver | n/a | local outer server `-L sidecar-remote-human`, SSH-attached to the isolated remote TUI |

`SIDECAR_ISOLATED_STATE=1` guarded both Sidecar runs. `tmux-drive.sh paths` and `remote-spike.sh paths` were checked before launch. Teardown addressed only the explicit private sockets and run roots. The local default tmux server remained at 41 sessions; the remote default server remained at 2 sessions after teardown. The remote run root was removed. The local proof artifacts remain under `/private/tmp/sidecar-drive-phase-b-501/out` for inspection.

## Final live journey

1. The local Sessions browser showed the registered remote project and shell, loaded the remote pane over proxied control mode, and entered the ordinary `typing · ctrl+\ or esc esc to stop` chrome.
2. Fast literal input produced the exact remote command and output `FINAL-7CFF-ORDER-abcdefghijklmnopqrstuvwxyz0123456789`.
3. The remote human entered the same pane from the isolated on-host Sidecar at a deliberately different terminal size. Ownership moved to `MarcusBook-Pro.local-22768` and the pane became 73×30.
4. After the remote human was idle, the already-interactive viewer typed `FINAL-7CFF-VIEWER-RESTORES-103X45`. Ownership moved to `aerie-72755`, the command arrived exactly, and the pane restored to 103×45 without re-entry.
5. After the viewer was idle, the already-interactive remote human typed `FINAL-7CFF-HUMAN-RESTORES-73X30`. Ownership moved back to `MarcusBook-Pro.local-22768`, the command arrived exactly, and the pane restored to 73×30 without re-entry.
6. Exiting the viewer left the remote human's token intact. Exiting the remote human made `show-options -v @sidecar-owner` return no value, proving ownership-safe release.

The `dc6ea45a` integrated proof also exercised bracketed paste and complete-history search through the remote channel: searching `history-0001` reported `1/1 matches` and `868 lines back`, while the query bytes never reached the pane. The `e5759a21` and `7cff6d1e` geometry-only follow-ups did not touch search, capture, or input ordering; their final-package and full-repository gates revalidated those paths in the integrated candidate.

## Automated and review evidence

- `go test ./... -count=1 -timeout=180s` passed on `7cff6d1e`; `go build ./...` passed immediately afterward.
- Focused changed-package gates passed for `internal/tty`, `internal/overview`, and `internal/app`.
- Independent focused race review passed on the final candidate. It covered ordered backend replacement and clear barriers, blocked activation/release, late size-query fencing, settled lease refresh, bidirectional takeover restoration, and the rule that an unresolved lease read cannot authorize a resize.
- Config-reload tests prove feature resolution and `SyncHosts` run after config saves, removed and same-ID-retargeted hosts close selected controls, queued updates from the old client incarnation are rejected, and old lease teardown is ordered ahead of replacement activation.
- The remote-open safety regression proves a remote target with nonzero dimensions cannot query or resize an ambient local tmux pane with the same pane ID.
- Sessions search tests cover host-aware complete-history capture, match navigation and status, stale-result fencing, highlighting, and frozen selection without a local capture fallback.

## Retained proof artifacts

The local isolated output directory contains text and PNG captures for the final run, including:

- `final-7cff-typing`
- `final-7cff-order`
- `final-7cff-viewer-restore`
- `final-7cff-viewer-exit`
- `final-7cff-human-restore.txt`

The proof environments were stopped after capture. No private Sidecar, SSH serve, control client, or tmux process from the run remained.
