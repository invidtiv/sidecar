# Isolated proof: remote Sessions content clicks

This is the two-machine (or loopback) recipe for [remote host content-pane parity](../../plans/implemented/remote-host-content-pane-parity.md) slice 7. It proves that a click in a remote Sessions terminal loads that host's file, issue, note, diff, resource, or tmux session, and never this machine's same-named twin.

The helper is [`scripts/remote-content-proof.sh`](../../../scripts/remote-content-proof.sh). It wraps [`scripts/remote-spike.sh`](../../../scripts/remote-spike.sh) for the host and [`scripts/tmux-drive.sh`](../../../scripts/tmux-drive.sh) for the viewer. Both axes are isolated on both ends: private tmux sockets and private Sidecar state/config/cache trees, with `SIDECAR_ISOLATED_STATE=1`. The default tmux server and `~/.local/state/sidecar` are not in the run. The same helper is the isolation wrapper for [the viewer-screen open/layout recipe](./remote-viewer-screen-proof.md).

`SPIKE_HOST` has no default here. Point it at a disposable proof target, not a live workstation Sidecar or its real state tree.

## Isolation, before anything else

```bash
./scripts/remote-content-proof.sh check-isolation
./scripts/remote-content-proof.sh paths
```

`check-isolation` records hashes of the real state tree and the default tmux session list, then asserts that the viewer and host run roots resolve under `/tmp/sidecar-content-proof*` / `/tmp/sidecar-spike*` and nowhere under `~/.local/state/sidecar` or `~/.config/sidecar`. Re-run it after teardown; the hashes must match.

`paths` prints every root the next command would use. Confirm nothing listed is a live Sidecar tree.

Never `tmux kill-server` without `-S` / `-L` aimed at the private socket. Never edit `shells.json` by hand. Never launch a proof sidecar without `SIDECAR_ISOLATED_STATE=1`.

## Conflicting markers

Prepare data that makes a local fallback look like a failure, not a success:

| Kind | Viewer (isolated) | Host (isolated) |
| --- | --- | --- |
| File `twin.txt` | `LOCAL-TWIN` | `REMOTE-MARKER` |
| td issue | absent, or a different title | host-only title |
| td note | absent | host-only title |
| Git | clean / different hash | dirty file or host-only commit |
| Resource provider | none, or a different instance | host-only matcher |
| Tmux session `sidecar-sh-twin-1` | exists on the viewer's private server | exists on the host's private server |

On two machines, also create the host's reported project path on the viewer with `LOCAL-TWIN` only when that path is not a real checkout. On loopback SSH the host path is this disk, so a same-path file twin cannot be proven that way; the session-name twin still can, because the two private tmux servers are distinct.

`./scripts/remote-content-proof.sh setup` plants the file, git, and session markers in both isolated trees. Issue, note, and provider fixtures are documented in the script and need `td` / a provider binary on the host.

## Bring the host up

```bash
export SPIKE_HOST=proof-box          # required; no marcusbook default
export SPIKE_RUN_DIR=/tmp/sidecar-spike-$USER
./scripts/remote-content-proof.sh setup
./scripts/remote-spike.sh probe
```

Register the isolated host in the *viewer's* isolated config, not in `~/.config/sidecar`:

```bash
SIDECAR_DRIVE_RUN_DIR=/tmp/sidecar-content-proof-$(id -u) \
  ./scripts/tmux-drive.sh cli host add "$SPIKE_HOST" --id proof \
    --binary /tmp/sidecar-spike-$USER/sidecar \
    --remote-config /tmp/sidecar-spike-$USER/config/config.json \
    --env TMUX_TMPDIR=/tmp/sidecar-spike-$USER/tmux \
    --env XDG_STATE_HOME=/tmp/sidecar-spike-$USER/state \
    --env SIDECAR_ISOLATED_STATE=1
```

The viewer's sidecar is launched with `sidecar_remote_hosts` on and `SIDECAR_ISOLATED_STATE=1` by `tmux-drive.sh start` once that config is in the drive run root. See [headless testing](./headless-testing.md) for SGR clicks.

## Prove the clicks

Print a line containing each token in the remote pane (`twin.txt:20`, a `td-*` id, `sidecar://note/nt-*`, a host-only git hash, a provider key, `sidecar-sh-twin-1`, and an `https://` URL). Select the remote Sessions row. Click with a real SGR mouse event, not a key that happens to sit on the same cell.

1. **File.** Document pane shows `REMOTE-MARKER` at the target line, never `LOCAL-TWIN`. Host glyph names the host. Change the file on the host; the same pane refreshes inside the conditional-refresh window.
2. **Issue and note.** Cards show host-only titles. A nested issue or note link from the document stays on the host. `O` on the issue card refuses Open in td.
3. **Diff.** A scanner-supported commit/range/revision opens a Diff pane with host-only data. A working-tree Diff pane (opened or restored, not implied by a terminal token) refreshes after a host index change.
4. **Resource.** Only the host provider claims the key. Manual refresh stays on the host. A validated HTTP(S) source URL opens in the viewer's browser.
5. **Session.** Clicking `sidecar-sh-twin-1` in the remote pane selects the remote row. The local private server's same-named session is untouched.
6. **URL.** `https://example.com` opens locally after the shared safety check.
7. **Missing capability.** Against an older host binary, the terminal stays live and the click toasts `Update Sidecar on <host> to open files and issues from that host.`
8. **Refusals.** On a remote Document, `e` / `ctrl+p` / `f` toast that the action is not available on that host. `/` still searches the loaded body.

A content-verb timeout must leave the terminal interactive, keep the last good body, and mark the pane stale. Disconnecting or disabling the host tears the preview down without rebinding it to a local source.

## What this environment can run without a second machine

`check-isolation` and `paths` are local. The Go tests under `internal/overview` (`TestSessionLinkAttachesMatchingHostRowOnly`, the remote document/issue/note/diff/resource suites, `TestRemoteActionAuditsHoldAfterResourceAdmission`) are the contract proof when a live two-machine tmux-drive cannot run. They use conflicting markers and never touch the default tmux server or `~/.local/state/sidecar`.

## Teardown

```bash
./scripts/remote-content-proof.sh teardown
./scripts/remote-content-proof.sh check-isolation   # hashes must match the start snapshot
```

Teardown kills only the private spike tmux server (by socket path under the spike run root) and removes the proof run roots. It does not call `tmux kill-server` on the default server.
