# Isolated proof: remote viewer owns the screen

This is the two-machine (or loopback) recipe for [the viewer owns the screen](../../plans/implemented/remote-host-viewer-screen.md) slice 5. It proves that `sidecar open` and `sidecar layout get|apply|move` issued inside a Sidecar-managed pane on a registered host land on the connected viewer's Sessions preview when that viewer holds the geometry lease — and that they refuse rather than queue when the row is off screen, nobody is looking, or the host's own TUI holds the lease.

The helper is [`scripts/remote-content-proof.sh`](../../../scripts/remote-content-proof.sh), the same isolation wrapper the [content-pane click recipe](./remote-content-pane-proof.md) uses. It wraps [`scripts/remote-spike.sh`](../../../scripts/remote-spike.sh) for the host and [`scripts/tmux-drive.sh`](../../../scripts/tmux-drive.sh) for the viewer. Both axes are isolated on both ends: private tmux sockets and private Sidecar state/config/cache trees, with `SIDECAR_ISOLATED_STATE=1`. The default tmux server and `~/.local/state/sidecar` are not in the run.

`SPIKE_HOST` has no default here. Point it at a disposable proof target, not a live workstation Sidecar or its real state tree. Do not execute this recipe against a live host unless that host is already a disposable proof target; writing the recipe is the deliverable.

## Isolation, before anything else

```bash
./scripts/remote-content-proof.sh check-isolation
./scripts/remote-content-proof.sh paths
```

`check-isolation` records a fingerprint of isolation-sensitive paths under the real state tree (`shells.json`, `requests/`, `viewers/`) and the default tmux session list, then asserts that the viewer and host run roots resolve under `/tmp/sidecar-content-proof*` / `/tmp/sidecar-spike*` and nowhere under `~/.local/state/sidecar` or `~/.config/sidecar`. Setting `CONTENT_PROOF_RUN_DIR` or `SPIKE_RUN_DIR` to a real Sidecar tree is refused before any command runs. Re-run it after teardown; default tmux sessions must still be present.

`paths` prints every root the next command would use. Confirm nothing listed is a live Sidecar tree.

Never `tmux kill-server` without `-S` / `-L` aimed at the private socket. Never edit `shells.json` by hand. Never launch a proof sidecar without `SIDECAR_ISOLATED_STATE=1`. A proof that isolates only the viewer's tmux socket can still write the host's real `stateDir/requests` and the host's real `shells.json`. Both axes, both machines.

## Conflicting markers

The same twins the click recipe plants: viewer `twin.txt` is `LOCAL-TWIN`, host `twin.txt` is `REMOTE-MARKER`. A success must not be the same-named local file. `setup` also creates `sidecar-sh-twin-1` on each private tmux server; the host fixture adds Sidecar-managed sessions (`spike-plain` and the replay panes) that `sidecar open` can use as an origin.

## Bring the host up

```bash
export SPIKE_HOST=proof-box          # required; no live-workstation default
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

The viewer's sidecar is launched with `sidecar_remote_hosts` on and `SIDECAR_ISOLATED_STATE=1` by `tmux-drive.sh start` once that config is in the drive run root. Select the remote `spike-plain` row so this instance holds the geometry lease. See [headless testing](./headless-testing.md) for key pacing.

Host `sidecar` invocations below use the isolated binary and tree. Inside a managed pane the origin is the tmux session; from ssh, pass `--shell spike-plain`. Either way the binary must see the isolated state:

```bash
host_sidecar() {
  SPIKE_HOST="$SPIKE_HOST" SPIKE_RUN_DIR="$SPIKE_RUN_DIR" \
    ./scripts/remote-spike.sh ssh \
    "TMUX= TMUX_TMPDIR=$SPIKE_RUN_DIR/tmux XDG_STATE_HOME=$SPIKE_RUN_DIR/state SIDECAR_ISOLATED_STATE=1 \
     $SPIKE_RUN_DIR/sidecar -config $SPIKE_RUN_DIR/config/config.json $*"
}
```

## Prove the lease-holder screen

A success must show `REMOTE-MARKER`, never `LOCAL-TWIN`. A refusal must say why.

1. **Open a host-only file from the host agent onto the viewer.** With the laptop viewing the remote `spike-plain` row, from that pane run `sidecar open twin.txt` (or `host_sidecar open --shell spike-plain twin.txt`). The viewer Document pane shows `REMOTE-MARKER`. The host TUI, if it is running as a lease non-owner, does not also open it. There is no `--host` flag.

2. **Layout get matches the viewer grid.** After the open, `sidecar layout get --json` from the same origin returns the laptop's Sessions grid for that row, including the file pane targeting `twin.txt`. It is not an empty host-TUI tree and not a decline.

3. **Off-screen apply exits 4.** Switch the viewer off that row (another Sessions row, or a different surface). From the same origin, `sidecar layout apply --pane '{"kind":"file","targets":["twin.txt"]}'` exits 4 and names that the row is not on screen. The layout is unchanged. Relayed open of a new target in this state also exits 4; it does not queue.

4. **Host TUI holding the lease keeps the open local.** `SPIKE_HOST=$SPIKE_HOST ./scripts/remote-spike.sh remote-tui` against the same isolated tree. Select `spike-plain` and type so the host instance claims `@sidecar-owner`. `sidecar open twin.txt` from that pane opens on the host TUI. The laptop, now a lease non-owner, must not steal the open.

5. **`n` File lists host files and opens the host body.** On the laptop, with the remote row selected and this instance holding the lease, open the create modal, pick File. The picker lists the host catalog (`twin.txt`), not the viewer's `LOCAL-TWIN` tree. Opening it shows `REMOTE-MARKER`. Pickers stay empty until the host catalog arrives rather than filling from a local twin.

6. **Terminal split's tmux session exists on the host server, not the viewer's.** From the same create modal, pick Terminal split. After it lands, `tmux -S $SPIKE_RUN_DIR/tmux/tmux-$(remote uid)/default ls` names the new session. The viewer's private server (`$CONTENT_PROOF_RUN_DIR/tmux/tmux-$(id -u)/default`) does not. Never `tmux kill-server` without `-S` / `-L` on that private socket.

A missing `UIRequestRelayV1` (older host binary) or a disconnected viewer is a fast refusal naming the machine that holds the screen, not a 1.2s hang. Cron or a random SSH with no Sidecar origin is unchanged: no relay.

## What this environment can run without a second machine

`check-isolation` and `paths` are local. The Go tests under `internal/cli` (`TestLeaseHolderLandingIsDocumented`, the open-relay and layout-relay suites) and `internal/overview` are the contract proof when a live two-machine tmux-drive cannot run. They use conflicting markers and never touch the default tmux server or `~/.local/state/sidecar`.

## Teardown

```bash
./scripts/remote-content-proof.sh teardown
./scripts/remote-content-proof.sh check-isolation   # default tmux sessions still present; run dirs still outside the real tree
```

Teardown kills only the private spike tmux server (by socket path under the spike run root) and removes the proof run roots. It does not call `tmux kill-server` on the default server.
