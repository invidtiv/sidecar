# Tmux version compatibility

## Outcome

Sidecar supports tmux 3.4 and newer. Every change to the tmux integration is tested against the oldest supported release and the newest stable release, while future tmux releases remain allowed unless a real incompatibility is found. A developer can update the tested ceiling in one small manifest change, review upstream behavior changes, and run the same compatibility proof locally and in CI.

## User journey

Users on tmux 3.4 can continue to create, observe, resize, interact with, and restore Sidecar-managed shells. Users can upgrade to tmux 3.7c without Sidecar changing behavior or requiring a tmux-specific setting. Developers and agents can prove both ends of the supported range on private tmux servers without touching the machine's default server or Sidecar state.

## Decisions

- **Supported floor:** tmux 3.4. Sidecar Setup reports older versions as unsupported. This replaces the historical 3.0 floor with the oldest release that is continuously exercised.
- **Tested ceiling:** tmux 3.7c, the current stable Homebrew and upstream release at implementation time. This is a tested release, not a maximum; later versions are not rejected.
- **Compatibility strategy:** test observable behavior through tmux's CLI and control-mode seam. Do not add version branches for additive upstream changes. Keep capability probes such as the existing control-mode flow-control probe where tmux behavior genuinely differs.
- **Matrix shape:** exercise the oldest supported release and newest stable release, plus the real upgrade transition in which the newest client talks to a still-running oldest server. In that transition tmux 3.7c accepts ordinary commands against a 3.4 server but declines control-mode attach with an explicit `%exit`; Sidecar must continue through its existing capture fallback until the server is restarted. Intermediate releases are covered by the contract established at both ends and are added temporarily only when an upstream regression or semantic transition requires it.
- **Version source:** keep the compatibility roles, release strings, and source archive checksums in one inspectable manifest. The local harness and CI consume that manifest rather than maintaining separate version lists.
- **Isolation:** every compatibility process gets a private `TMUX_TMPDIR`; tests may stop only servers below that directory. The default tmux server is never restarted or stopped by the harness.
- **Local upgrade:** Homebrew may replace the `tmux` client binary, but the currently running default server remains on its existing executable until the user deliberately restarts it. Sidecar proof uses private 3.7c and 3.4 servers, so no live sessions need to be sacrificed for validation.

## Upstream risk review

The significant 3.4-to-3.7 changes for Sidecar are tmux 3.5's extended-key representation and mode-2 behavior, later modified-key refinements, control-mode additions, terminal rendering changes, and new pane types. Sidecar sends explicit keys and literal text through `send-keys`, consumes the control protocol through its shared `tty` model, feature-detects optional flow control, and uses tiled panes only. The compatibility suite must therefore cover control attach and output, pane geometry, capture semantics, cursor state, literal paste, shell lifecycle, and metadata formats. Floating panes and tmux key-binding syntax are not Sidecar-owned behavior and require no product abstraction.

## Implementation

1. Add a two-row compatibility manifest for `minimum = 3.4` and `latest = 3.7c`, including SHA-256 checksums for the official release archives.
2. Add a portable source-build helper that installs one manifest release into a caller-selected temporary prefix. It must never install globally or address a tmux server.
3. Add a compatibility test driver that verifies the selected binary's exact version, creates an isolated tmux namespace, and runs the high-signal packages that exercise control mode, terminal behavior, tmux metadata, shell operations, and terminal passthrough. Required tmux integration tests must fail rather than silently skip when the matrix contract is enabled.
4. Add an oldest/latest CI matrix that builds the requested tmux release, prepends it to `PATH`, and invokes the compatibility driver. Add a private-socket upgrade-skew proof in which a 3.4 server remains running while 3.7c supplies the client commands, observes either control notifications or tmux's explicit control-mode exit, and proves the capture path still supplies terminal content. Keep the normal repository-wide Go and lint jobs unchanged.
5. Raise Sidecar Setup's minimum from 3.0 to 3.4 and update focused tests.
6. Document the support contract, local matrix command, and future-upgrade procedure in the active developer guidance.
7. Upgrade the local Homebrew client to 3.7c. Record the client version and read the default server version before and after; do not restart the server.

## Verification

- Manifest validation rejects missing roles, duplicate roles or releases, malformed checksums, and unsupported requests.
- The build helper produces binaries reporting exactly `tmux 3.4` and `tmux 3.7c`.
- The compatibility suite passes with real private servers for both releases and records the exact version under test.
- The 3.7c client can drive required metadata, input, resize, and capture commands against a private server started by tmux 3.4. The proof records tmux's explicit mixed-version control-mode exit and confirms the fallback path Sidecar uses during the period between a package upgrade and a deliberate server restart.
- `go test ./...` and `go build ./...` pass for the integrated repository state.
- `tmux -V` resolves to 3.7c after the Homebrew upgrade, while a read-only query confirms the default server PID and version were not changed by this work.
- An independent reviewer checks the policy, isolation guarantees, CI behavior, tests, and upgrade evidence before the task closes.

## Future stable upgrade procedure

1. Confirm the new upstream stable release and read every `CHANGES` section since the currently tested latest release.
2. Update only the manifest's `latest` row with the release and official archive checksum.
3. Run the local latest-role compatibility proof. Add a focused regression test if an upstream change affects a Sidecar-owned behavior.
4. Let the oldest/latest CI matrix prove the supported floor did not regress.
5. Upgrade a local client and validate a private server first. Restart a default server only as a separate, user-authorized operation after its sessions have been handled.
6. Update the tested-ceiling statement and record the evidence. Do not add version checks unless a capability cannot be detected behaviorally.

## Completion evidence

- Official release archives built into private prefixes and reported exactly `tmux 3.4` and `tmux 3.7c`.
- The same-version compatibility suite passed on both releases across control mode, terminal model recovery, geometry, cursor behavior, paste, metadata, terminal passthrough, and shell lifecycle.
- The upgrade-skew proof passed with a tmux 3.7c client and tmux 3.4 server: ordinary commands remained available, control mode returned the observed explicit `%exit`, and a post-exit marker was recovered through `capture-pane`.
- Manifest validation, canonical temporary-prefix traversal and symlink regressions, script syntax, `git diff --check`, `go test ./...`, and `go build ./...` passed.
- Homebrew installed the tmux 3.7c client. The default server remained the same PID `87110`, version 3.6b, and start time throughout the upgrade and proof.
- Independent adversarial review found and drove fixes for prefix-containment and manifest-authority defects; the focused re-review verdict was clean.
