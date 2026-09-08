# Mobile M1-B combined catalog fixture

**Task:** `td-f76d95`, under `td-a56f0d`. **Source:** the reviewed mobile implementation at `ba78d35cf836f87f772223f254acda5fae95da20`. **Status:** the isolated fixture and strict SSH endpoint are ready; a native result is not claimed until the signed app completes its separate Simulator or device journey.

## Purpose and catalog

This task-owned fixture presents one local owner and one routed MarcusBook owner through the same hub configuration. Each owner has a synthetic `Twin` managed shell and one inactive `project` main worktree with `codex` presentation metadata. The actual desktop Sessions surface and mobile API both show the same four rows in this order:

1. local `Twin` shell, live and attachment-ready
2. `remote-combined · Twin` shell, live and attachment-ready
3. local `project` worktree, session ended and unavailable
4. `remote-combined · project` worktree, session ended and unavailable

Desktop groups them into two **Live Shells** and two **No Session** rows. Both ready rows point to distinct owner-scoped `sidecar-sh-project-1` / `%0` identities. Both private terminals are currently 80x24 with empty owner tokens. Their tmux server PIDs, session creation times, durable shell creation times, socket identities, public targets, and full expected target identities are pinned in `native-combined-fixture.json`.

The first request from a fresh hub service can honestly return only the two local rows while the remote owner is `connecting`. The strict endpoint check observed totals 2, 2, then 4 across 1.2-second same-process refreshes. The final snapshot has both hosts online, all four ordered rows, and no collection failure. Native must consume the server response and refresh; it must not synthesize the remote rows or selectors.

## Native endpoint

The task-only SSH server listens on loopback `127.0.0.1:22231`, accepts the synthetic username `proof` and password `PROOF-SSH-PASSWORD-REDACTED`, and allows only the no-PTY exec command `/tmp/sidecar-mobile-m1-combined-ssh/serve-combined`. Its Ed25519 key is `SSH-HOST-KEY-REDACTED`, with fingerprint `SHA256:SSH-HOST-KEY-FINGERPRINT-REDACTED`. A strict client pinned that key, authenticated with the synthetic password, executed the fixed command, completed mobile hello, refreshed to the four-row catalog, closed normally, and changed no terminal lease or geometry.

The local fixture root is `/private/tmp/sidecar-mobile-m1-combined-20260908`; the remote owner root is `/private/tmp/sidecar-mobile-m1-combined-owner-20260908`; and the SSH root is `/tmp/sidecar-mobile-m1-combined-ssh`. They use separate configs, state trees, binaries, tmux sockets, host key, and server identity from ports 22229 and 22230. Neither default tmux server, global Sidecar config, installed binary, stable native fixture, nor user session was modified.

## Desktop preview and cleanup

The actual desktop proof used a separate task-owned viewer session on the local private tmux server. Its four-row screenshot and matching API snapshot are pinned. At the screenshot, the selected local preview reported no output captured and both terminal owner tokens were empty. Closing the disposable viewer later left its exact private token after the process had exited; setup verified that PID was absent, conditionally cleared only that known token on the marked private server, and restored the local target to the recorded 80x24 empty-owner native baseline. This setup cleanup is recorded rather than presented as a graceful viewer release result; graceful desktop and mobile ownership cleanup are proved separately in the reviewed M1 evidence.

The fixture stays alive until the native owner and root finish. `/tmp/sidecar-mobile-m1-combined-ssh/cleanup.sh` refuses cleanup unless the local and remote ownership markers, socket inodes, tmux server PID and session creation identities, session and pane names, empty owner tokens, SSH PID, listening port, and server command still match. It then stops only endpoint 22231 and these two exact private tmux servers. The stable endpoints and the default servers remain untouched.

## Evidence

`native-combined-fixture.json` is the payload-free readiness record. It pins the two immutable Sidecar binaries, configs, shell records, synthetic provider metadata, actual API response, desktop plain and ANSI screens, endpoint refresh cadence, SSH source/binary/wrapper/key, strict checker, baseline facts, and deferred cleanup script. Its readiness SHA-256 is `6f82825849e1bc1eefda0f0231260ea57e15c7a52f79cfcc4d0e6b20c63b2d69`. The record remains a fixture handoff until native evidence replaces `native_result: pending` with an independently reviewed result.
