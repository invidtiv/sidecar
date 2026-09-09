# Backend input and scrolling performance

Status: backend implementation and refusal tests independently reviewed by `render_scroll` on 2026-09-08. This proof measures the local Go service with synthetic terminal workloads. It does not establish physical-device, Wi-Fi, SSH, native-renderer, or real Codex CLI performance.

## Cause and change

The mobile service revalidated a shell by rebuilding the shared global managed-target catalog on every input operation. That catalog discovers Git worktrees for every registered project. Input and frame publication share the attachment operation lock, so this repeated discovery delayed both typing acknowledgements and the visible echo. A 21-project isolated empty-shell reproduction made the delay hundreds of milliseconds even without a network.

Dedicated managed shells (`sidecar-sh-*`) now re-read their exact durable shell authority before and after live pane inspection. The project, project root, namespace, session, durable creation time, duplicate-source refusal, server incarnation, exact pane, session creation, pane count, owner configuration, operation sequence, frame checkpoint, and conditional geometry lease remain checked. Source reads are fresh on every operation; there is no time-based authority cache. Renaming a shell's display label does not invalidate its exact attachment. Legacy shell records outside the dedicated namespace retain the complete shared resolver because they can collide with worktree session names.

Live pane inspection and worktree candidate pane listings use the existing owner control connection once attached. A failed or unavailable active control connection refuses instead of spawning a fallback tmux process. Before attachment, normal local probes still apply. Candidate membership continues through the shared collector, with fresh Git inventory and pane membership before and after inspection. This removes three tmux subprocesses per candidate operation while retaining both Git reads.

The public protocol, complete normalized frames, capture coalescing, ownership transactions, presence deadlines, and reset boundaries are unchanged. The remaining roughly 14 milliseconds between acknowledgement and visible echo in the dedicated-shell fixture includes the established 12-millisecond capture coalescing window.

## Matched synthetic evidence

Baseline source: `985e9c11bbbe7eba7a59a1c54310bba788f90b55`. Baseline binary SHA-256: `8d86818072206bd1b103d01f1ac3d327594acfa3499afdb16d1b495d75e8fd28`. Measured working-tree binary SHA-256: `bb26b46e7b42ac73d659b5e91a5d33251b1b32539b9b9420bf483e84ec96318d`.

The performance sample preceded a final refusal-classification correction: when a bound control connection disappears, source revalidation reports `identity_changed` rather than a generic backend error. The corrected tree passes the existing same-name replacement live proof.

The matched fixture has 21 registered synthetic Git projects, an 80×24 pane, 40 sequential operations per workload, and a 45-millisecond pause after each visible echo. Acknowledgement measures client request to service acceptance. Visible echo waits for the exact expected synthetic text in a received full frame, rather than treating any newer frame as proof. The harness fixture uses an alternate screen and mouse reporting, redraws 23 rows per input, and processes alternating SGR wheel-up/wheel-down events. It is deliberately provider-neutral and does not impersonate actual Codex CLI evidence.

| Dedicated-shell workload | Ack mean before → after | Ack p95 before → after | Echo mean before → after | Echo p95 before → after |
| --- | ---: | ---: | ---: | ---: |
| Empty shell typing | 338.713 → 2.269 ms | 405.408 → 2.601 ms | 352.766 → 15.972 ms | 420.549 → 16.898 ms |
| Harness typing | 297.018 → 2.425 ms | 358.337 → 2.832 ms | 311.266 → 16.149 ms | 371.413 → 17.143 ms |
| Harness wheel scrolling | 324.529 → 2.656 ms | 517.066 → 3.293 ms | 338.674 → 16.376 ms | 532.857 → 17.528 ms |

Dedicated-shell echo mean improves by about 95% in these fixtures. An initial one-project empty-shell sample was much faster before the fix (21.35-millisecond median acknowledgement), confirming that project-count amplification was important to the reported symptom. Initial 21-project measurements reached 624.20-millisecond p95 acknowledgement before the change.

Worktree candidates show a smaller improvement because their source authority still requires two fresh Git inventories. Initial matched candidate medians decreased from 61.484 to 51.887 milliseconds for empty-shell echo, from 62.663 to 53.395 milliseconds for harness typing, and from 63.280 to 50.688 milliseconds for wheel echo. Tail timings varied with concurrent machine work; no general candidate p95 improvement is claimed from that first sample. Removing the remaining Git reads requires an authoritative source-validation change with corresponding correctness proof, not a short-lived cache.

## Reproduce

Run from the Sidecar repository. Keep the baseline binary separately before editing or rebuilding; `--binary` selects it explicitly. The helper replaces only the named terminal in a fixture prepared by the existing isolation script. All terminal text is synthetic; the report contains only timings and binary provenance.

```sh
./scripts/mobile-service-proof.sh prepare /tmp/sidecar-mobile-performance-example
python3 scripts/mobile-performance-proof.py /tmp/sidecar-mobile-performance-example --binary /absolute/path/to/baseline-sidecar
python3 scripts/mobile-performance-proof.py /tmp/sidecar-mobile-performance-example
python3 scripts/mobile-performance-proof.py /tmp/sidecar-mobile-performance-example --candidate --binary /absolute/path/to/baseline-sidecar
python3 scripts/mobile-performance-proof.py /tmp/sidecar-mobile-performance-example --candidate
./scripts/mobile-service-proof.sh stop /tmp/sidecar-mobile-performance-example
```

The fixture isolates the tmux socket, configuration, and Sidecar state and clears inherited `TMUX` and `TMUX_PANE`. It never reads or controls the default tmux server. The preparation marker and restricted temporary path are required. Call the final stop command after an interrupted proof too.

## Verification and review

`go test ./...` and `go build ./...` pass. Race checks pass for the complete mobile and mobilehub packages plus the focused CLI target-validation and tty headless tests. The same focused race checks, workspace inventory tests, and full build also pass with `GOWORK=off` against the pinned module graph. New tests cover fresh manifest authority without subprocess discovery, changed creation/project identity, source removal during inspection, duplicate shell claims, legacy shell/worktree collisions, exact in-band pane inspection, shared pane inventory, and failure without fallback.

The first full-suite run exposed an existing backpressure-test race: its producer could overflow and cancel the writer before the writer was scheduled, failing the assertion that the writer entered its blocked state. The test now establishes that state with the first frame and a real barrier, then sends the remaining frames. Thirty consecutive repetitions pass, and independent review approved the change without weakening the owner-close or overflow assertions.

The existing isolated local service proof passes connect, exact input, Unicode/background fidelity, resize, heartbeat, fresh-process reconnect without replay, release, and cleanup. The existing isolated candidate proof passes complete membership changes, same-name session replacement, exact split-pane input/geometry, and sibling preservation. These live proofs use the corrected final source tree.
