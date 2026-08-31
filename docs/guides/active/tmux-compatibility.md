# Tmux compatibility

Sidecar supports tmux 3.4 and newer. The compatibility matrix continuously exercises tmux 3.4 as the supported minimum and tmux 3.7c as the latest tested stable release. The latest-tested release is not a maximum: Sidecar does not reject newer tmux versions.

Compatibility is maintained at the observable tmux CLI and control-mode boundary. Prefer capability probes when optional behavior differs; do not branch on a version merely because a newer release added a command, option, key representation, or pane type. The existing control-mode flow-control probe is the model for a behavior that genuinely needs detection.

## Version manifest

[`compat/tmux-versions.tsv`](../../../compat/tmux-versions.tsv) is the only version source for local tooling and CI. Each of its two rows has a role, exact upstream release, and SHA-256 for the official tmux release archive. `./scripts/test-tmux-compat-manifest.sh` rejects missing or duplicate roles, duplicate releases, malformed versions or checksums, and unsupported roles.

## Run the matrix locally

The source builder requires a C toolchain, `make`, `curl`, `tar`, libevent, ncurses, and utf8proc development files. On macOS, install `libevent`, `ncurses`, and `utf8proc` with Homebrew; the helper reads their Homebrew prefixes directly and does not require `pkg-config`. On Ubuntu or Debian, install `libevent-dev`, `libncurses-dev`, `libutf8proc-dev`, `bison`, and `pkg-config`.

Choose private build prefixes. Before creating anything, the builder rejects dot-segment traversal, resolves symlinks and the nearest existing ancestor, and requires the canonical destination to remain under `/tmp`, `/private/tmp`, `TMPDIR`, or `RUNNER_TEMP`. It verifies the archive checksum before extraction and never contacts a tmux server.

```bash
minimum_prefix=$(mktemp -d "${TMPDIR:-/tmp}/sidecar-tmux-minimum.XXXXXX")
latest_prefix=$(mktemp -d "${TMPDIR:-/tmp}/sidecar-tmux-latest.XXXXXX")

./scripts/build-tmux-compat.sh minimum "$minimum_prefix"
./scripts/build-tmux-compat.sh latest "$latest_prefix"

./scripts/test-tmux-compatibility.sh minimum "$minimum_prefix/bin/tmux"
./scripts/test-tmux-compatibility.sh latest "$latest_prefix/bin/tmux"
./scripts/test-tmux-compatibility.sh latest "$latest_prefix/bin/tmux" \
  --server-role minimum --server-tmux "$minimum_prefix/bin/tmux"
```

The same-version runs assert the exact selected release, create only private tmux servers, and run the high-signal Sidecar integration tests for control attach and output, flow-control recovery, geometry, capture/cursor semantics, literal and bracketed paste, tmux metadata, terminal passthrough, and shell lifecycle. `SIDECAR_REQUIRE_TMUX_COMPAT=1` makes a missing tmux prerequisite or a failed isolated-server setup a test failure rather than a skip.

The skew run models a package-manager upgrade while an older server remains alive: the latest client talks to a private server started by the minimum binary. It proves the server's exact reported version, metadata reads, literal input, resize, and captured terminal content. It also makes a bounded control-mode attach attempt. If tmux negotiates control mode, the proof requires command and output notifications; tmux 3.7c currently answers a 3.4 server with an explicit `%exit`, so the proof then requires a new post-exit marker to arrive through `capture-pane`. This is the supported temporary degraded state after a client upgrade: Sidecar's existing control-death behavior uses capture fallback until the server is restarted separately. Both same-version endpoints retain the complete control-mode proof.

The skew check is deliberately a protocol/CLI smoke rather than the full Go suite because package-level test isolation starts a new server through the client on `PATH`; that new server would no longer represent upgrade skew.

Every run places `TMUX_TMPDIR` under a fresh temporary root and tears down by the explicit socket path. It never stops, restarts, kills, or replaces the default tmux server.

## Test a future stable release

1. Read every upstream `CHANGES` section since the currently tested latest release, focusing on control mode, keys, capture/rendering, formats, and pane lifecycle.
2. Replace only the `latest` row in `compat/tmux-versions.tsv` with the new release and the SHA-256 of its official archive.
3. Run the manifest test, build the latest role, and run the latest same-version compatibility proof.
4. Build the minimum role and run the latest-client/minimum-server skew proof.
5. Let CI exercise both same-version endpoints and upgrade skew. Add a focused behavior regression only when an upstream change affects a Sidecar-owned path.
6. Update the latest-tested release stated in this guide and record the proof in the task or change that performs the upgrade. Do not raise the supported minimum without an explicit product decision.
7. Upgrade a local client only after private-server proof. Treat restarting the default server as a separate, user-authorized operation.

Homebrew can replace a client binary without changing a server that is already running. Therefore client version evidence (`tmux -V`) and server version evidence (`tmux display-message -p '#{version}'` against a known socket) answer different questions and should always be recorded separately during an upgrade.
