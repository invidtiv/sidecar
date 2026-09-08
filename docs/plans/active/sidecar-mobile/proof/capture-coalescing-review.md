# M0 capture coalescing independent review

Task `td-342c45`; implementation owner `/root/terminal_seed_spike`; independent reviewer `/root/apple_readiness`, context `mobile-capture-overflow-review`, initial review session `ses_3019be`, final approval session `ses_b4852d`. The reviewer owns this note and temporary review overlays only, and has not edited the implementation or the active device helper.

## Verdict

Approved for the bounded capture-coalescing repair. The final source, targeted independent regression, affected-package race gates and isolated live-service proof are clean. A refreshed physical-device run against the integrated helper remains the parent M0-E acceptance step.

## Observed failure

The physical iPhone trace for service PID 86386 shows an 84.537 ms heartbeat request-to-ACK interval. An `overflow` reset follows its ACK by 0.173 ms, while frame sequence remains contiguous from 106 to 107 and geometry stays 46×23. This is separate evidence from the earlier unexplained, untraced view-only event; this review does not assign a cause to that earlier event.

The attachment holds `opMu` during heartbeat identity revalidation and tmux acknowledgement, while frame publication needs the same lock. Its one-slot capture queue previously marked replacement of any unpublished complete snapshot in the same reset generation as overflow. Publication then revoked control and advanced the reset, despite no assigned wire frame having been dropped. The changed reset could also discard a newer queued capture as belonging to the previous generation.

An independent temporary Go overlay exercised the production attachment goroutine with that operation lock held, then offered three same-identity, same-geometry, same-reset complete captures. The reviewed M0 implementation failed the expected-control-retention assertion with `control=false`, `reset=2`, and an overflow reset. The attachment implementation tested was verified byte-identical to `cf77be7a`. Failure output is `/tmp/sidecar-mobile-capture-coalesce-review/result.log`, SHA-256 `879cf596edd634172ed645b2d24aec72faae3489af9722aa08b646b0cd8adf06`; source pins are `source-pins.json`, SHA-256 `077a01a4212e0e62df62f29e8d43da83cb04dd618c3ca508a64ceb817e747e58`, in the same directory. The sanitized physical timing excerpt and source comparison are `diagnosis.json`, SHA-256 `4962ef8217fd13278f2ae3a57cc1d95b73b0792464eec1b3ac3d18f0e40b5e62`.

## Repair boundary

The candidate retains the one-slot bound and replaces ordinary unpublished complete captures with the newest one. Output sequence numbers are assigned only during publication, so that replacement creates no wire sequence gap. Skipped identity, alternate-screen, and geometry discontinuities remain sticky on the queued record, including chained replacements; identity has highest priority. The existing deliberate-resize generation barrier remains in place, and true outbound queue failure still terminates/revokes the stream.

The reviewer inspected expected-size and same-size resize behavior, a superseded geometry or alternate-screen change, a superseded server-identity change, operation-lock contention, and the unchanged outbound overflow path. No active app, signed build, helper process, tmux server, or user data was changed by this review.

## Independent checks

The original independent overlay regression passes 20 times against the candidate. A separate race run passes 20 repetitions of `Test(FullCaptureCoalescing|SnapshotOffer|Skipped|ExpectedResize|OutboundOverflow|UnexpectedGeometry)` in `internal/mobile`. Output is retained under `/tmp/sidecar-mobile-capture-coalesce-review/candidate/` as `result.log` and `race.log`; `source-pins.json` identifies the candidate source used by the independent overlay. The working diff passes `git diff --check`.

These targeted checks validate the discovered timing failure and preserve the material discontinuity fences. They do not claim a new device run or a repeated full-repository suite. The active physical helper will only be replaced through the coordinator's separately controlled proof step after final review.

## Final candidate and live proof

The reviewer verified all three owner files against `/tmp/sidecar-mobile-output-coalescing-source-manifest.json`, SHA-256 `a62137d2aea27bf785f9247e710cf68269f4cf9ae3aff7b68727a186dd88dc17`, on base `4ab09264eba6bc7ecb9b4a2ff8da48e95c609030`. The implementation hash matches the source used for the independent overlay. The prior manifest is archived at `/tmp/sidecar-mobile-output-coalescing-source-manifest-a8909a6c.json`, SHA-256 `987573315c158826d7b7afbf9d505cf012af9606aa3ee437a8f5c8d46bfdf836`; its only subsequent change clarified that physical retest belongs to parent M0-E. This reviewer-owned note is excluded from both manifests.

The independent candidate regression log has SHA-256 `76522ba028291a6d2a10ba0bd3ea445f5afc89793117a7b01deacc453deaa344`; the race log has SHA-256 `415da2fa1170e3cb917e30d05c3e674795f6c17eddb64f568f36c47e1cff915d`. Owner affected-package tests, full mobile/tty race, build and zero-issue lint records are pinned by `/tmp/sidecar-mobile-output-coalescing-owner-gates.json`, SHA-256 `81c501adcec3cc9a747156f4fdf0074914a667171735314dad6bb61daed6d4aa`.

The reviewer inspected the actual isolated ordinary journey result and transcript, with distinct service processes, attachment generation 1→2, resize, exact identity and no input replay. Their SHA-256 values are `188009b8c915b8288fd6ee83d90242a4b405caa0a4b581850944b773b3d992c5` and `35b4f8a311a706aa1ff1242f47742b465dcbe3eccdfb3dea5e947009212e1b02` under `/tmp/sidecar-mobile-overflow-real/`.

The separate stress result `/tmp/sidecar-mobile-overflow-stress3/stress-result.json`, SHA-256 `7910f49cc64f4960606e528ef7a6748db67bc4d79e15ed45af6b31b102b528b4`, records 240 synthetic updates at 8 ms intervals, two input operations, 30 heartbeats and 128 active output frames over 2.660 seconds. Published sequences 1 through 130 remain contiguous, no reset occurs, the newest completion marker is visible, and release and process exit succeed. Its exact executed driver is retained as `stress-driver.sh` in that directory, SHA-256 `8fc62d35816931f8a82750ad05c43dd7a7c7b1f9b89f6809b2ba4612c4e29604`. The reviewer inspected its request-error, reset, marker, sequence and process assertions, separate state/config/socket, explicit private owner check and cleanup trap. The tested binary has SHA-256 `e66953ed4f11aee07ade700b854b6fe85303b64e5f9437ddc1fd45e1c4878d07`.

These are local service observations over a short synthetic burst, not network latency, a physical-device retest, or an overnight endurance claim. No source change was needed after the independent regression passed; only the owner's proof-boundary wording changed before this verdict.
