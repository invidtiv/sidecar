# Mobile M1-B combined catalog fixture

**Task:** `td-f76d95`, under `td-a56f0d`. **Source:** the reviewed mobile implementation at `ba78d35cf836f87f772223f254acda5fae95da20`. **Status:** the isolated fixture, strict SSH endpoint, and signed native phone/iPad journey passed. Native task `td-0635ae` was independently approved by `ses_6e4678` and committed as `d0d23c6ec8f5003a9a3054e59b6e3c79bebf8b6b`. The guarded cleanup completed after the final native review.

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

`/tmp/sidecar-mobile-m1-combined-ssh/cleanup.sh` refused cleanup unless the local and remote ownership markers, socket inodes, tmux server PID and session creation identities, session and pane names, empty owner tokens, SSH PID, listening port, and server command still matched. It stopped only endpoint 22231 and these two exact private tmux servers, then separately required local no-server state plus successful remote SSH transport and an explicit remote tmux no-server result. A network failure therefore could not be mistaken for successful cleanup. The stable endpoints and the default servers remained untouched. The final cleanup script SHA-256 is `89161f57db60fa1be6da8dc0eb6b36f76b9cf405f6f47268cb88524d6f24c4f8`; its output and structured receipt are SHA-256 `8bec3dd3a438e13b09998ea84eab0ef27bbd08d8e6c6f03be49366020a74ae5d` and `b7475aadbdd091f410fa302eb070e1407e344b8c9ed1309fa0e99df8be6d1182`.

## Signed native result

The signed Simulator proof used the exact endpoint and server-returned identities from this fixture without changing production source at native base `fba9baaab89b702e7c2d80e840914fb72a049942`. The phone test passed one test with no failures or skips in 47.437 seconds; the iPad test passed one test with no failures or skips in 35.352 seconds. Both showed all four rows and groups, applied the remote host filter, checked the local and remote Details labels against the expected owner, session, and pane, and opened each ready terminal view-only. The tests invoked no control, input, resize, or History actions; no native protocol wire trace was recorded.

The final native manifest is `/Users/marcus/code/sidecar-mobile/artifacts/native-combined-catalog-freeze-v1.json`, SHA-256 `336f6b04112af3cd875907c8e0a05cb1cd8eedfc84a86e3a8b68fbc8a1b6a7bb`. The native evidence note is `/Users/marcus/code/sidecar-mobile/docs/readiness/native-combined-catalog.md`, SHA-256 `675a3807ffd888a96fcb67d3a5c332abe46c918a94b8a49c1f8a1a78bf3dbe75`. The phone and iPad logs are pinned at SHA-256 `e3b7b503936b792134bbaabf8a46665838d0e01437e5cb89f523818dda47b4fe` and `1b3498947a6ae834deb4a12556b14f811f69ecf8ec4801162744dcd1b8fb7574`. The target fact files before and after the native run are byte-identical at SHA-256 `504d4b75f2992cbfb20a9df328f4d14df994db508a510ae2499af4fdf7a58f3d`: both terminals remained 80x24 with empty owner tokens.

## Evidence

`native-combined-fixture.json` is the payload-free final fixture record. It pins the two immutable Sidecar binaries, configs, shell records, synthetic provider metadata, actual API response, desktop plain and ANSI screens, endpoint refresh cadence, SSH source/binary/wrapper/key, strict checker, exact before/after facts, independently approved native result, cleanup script, and cleanup receipt. Its final SHA-256 is `d5ddc878d3c1d25253544f71f02af0c74cb487f26d7432da523752c1dc56bd40`. The original readiness record remains pinned by the native manifest at SHA-256 `6f82825849e1bc1eefda0f0231260ea57e15c7a52f79cfcc4d0e6b20c63b2d69`.
