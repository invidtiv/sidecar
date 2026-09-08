# Mobile M1-A catalog surface evidence

**Task:** `td-ffe174`. **Production source:** `ba78d35cf836f87f772223f254acda5fae95da20`. **Evidence scope:** the actual desktop Sessions surface and `sidecar mobile sessions --json --sort activity` used the same task-owned two-host config, private tmux servers, isolated state trees, synthetic shell records, and presentation preferences. The tested mobile catalog and terminal implementation is unchanged from the independently reviewed and integrated M1-A/A2 slices.

## Exact all-states parity

The task-owned presentation state enabled idle worktrees. Both synthetic main worktrees had `codex` provider metadata so the desktop's existing inactive-worktree presentation rule included them; no agent conversation or provider process was started. The API and desktop each showed the same four rows in this order:

1. local `Twin` shell, live and attachment-ready
2. `remote-desktop · Twin` shell, live and attachment-ready
3. local `project` worktree on `main`, no session and unavailable
4. `remote-desktop · project` worktree on `main`, no session and unavailable

The desktop grouped them as two **Live Shells** and two **No Session** rows. This is the complete unfiltered row universe from the matching API snapshot, rather than the earlier live-shell-only comparison. Both host identities were online and the API returned no collection failures.

This was an actual desktop run with normal preview behavior. Selecting the local synthetic shell acquired its exact lease and accepted 67x35 geometry from its initial 80x24. The remote synthetic shell remained 80x24 with an empty owner. Terminating the task-owned viewer cleared the local owner token; the local geometry remained at the accepted 67x35 until the private server was destroyed. The proof does not claim a list-only desktop mode or geometry restoration. Before cleanup, both server PID, session creation, session name, and pane identities were rechecked against their original private facts. Cleanup then stopped only those two marked private servers. It did not touch the stable native route fixture or either machine's default tmux server.

## Input-write to post-acknowledgment matching frame sample

While the same two synthetic owners were still live and lease-free, a fresh hub service selected each exact ready row, acquired control, and sent five unique synthetic input tokens. The timer started immediately before writing each input request to the hub service and stopped when the proof received a complete normalized authoritative frame containing the exact token after the correlated acknowledgment. Frames interleaved while the proof awaited that acknowledgment were retained but were not searched for the token, so this does not establish the first matching frame received overall. Token payloads were inspected in memory and were not stored in the result.

The local samples measured 94.460 ms median and 95.311 ms maximum. The routed remote samples measured 36.944 ms median and 45.594 ms maximum. The remote measurement includes the hub-to-owner SSH route. These five-sample observations are not a causal local-versus-remote comparison or a performance service-level claim: the two owners had different capture timing and terminal state. The interval excludes touch handling, the phone-to-hub SSH path, Swift decoding, renderer application, and display presentation, so it is not end-to-end UI latency.

## Evidence and limits

The checked-in payload-free evidence is `catalog-m1-surface-evidence.json`, SHA-256 `91ad374b338d681a6fe5f8c01cb49dec3c097c8d38d5e67f418e4c5cb12c1d2b`. It pins the exact API catalog, ANSI and plain desktop screen, before/during/after identity facts, timing result, proof helper, configs, binary, and guarded cleanup receipt by SHA-256. The transient proof root was `/private/tmp/sidecar-mobile-router-desktop-20260908-0415`; its two private tmux servers are stopped, while the evidence files remain available for independent inspection.

This closes the same-config all-states desktop/API catalog parity gap. Prior reviewed evidence remains authoritative for catalog filtering and identity validation, zero/one/many candidate resolution, local and remote terminal routing, history snapshots, native iPhone/iPad rendering, reconnect, lease takeover, and failure fences. The ordinary cold two-host catalog still may return local rows while a healthy remote owner is explicitly connecting; a refresh within the native request budget promotes that owner. No evidence here claims instantaneous cold remote availability, stable history pagination, notification or push delivery, or complete user-perceived latency.
