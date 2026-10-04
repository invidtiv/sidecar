# U1-h three-viewer proof

Lane `u1h-three-viewer`, task `td-295605`. The security review is already merged; this document records the real three-viewer proof and its evidence limits. The orchestrator owns approval and closure.

## Run

From a Sidecar checkout initialized with `make worktree-init`, run `THREE_VIEWER_OUTPUT=/tmp/three-viewer-results timeout 420 ./scripts/three-viewer-proof.sh`. Dependencies are Go, tmux with the authoritative input-mode probes, GNU timeout, Node, pnpm, Python 3, and installed Playwright Chromium in sidecar-ui. `SIDECAR_UI_REPO` overrides `~/code/sidecar-ui`; the UI checkout must be on main. The script builds fresh Sidecar and Go protocol-peer binaries and runs `pnpm build` on the UI checkout. It serves that built app directly through Sidecar on `--port 0`, without a preview server.

The real TUI runs inside the private outer `sidecar-drive` server, with a PTY tmux client attached. The proof writes focus escape sequences into that client, and tmux focus events reach Bubble Tea. The browser uses the real app, SDK and element, paired through `api open --print`; headless Chromium focus is simulated with the app's normal window focus events and `document.hasFocus`, matching sidecar-ui's own Playwright convention. The Go iOS peer negotiates v1 presence/frames/holder labels over `mobile serve --stdio` without a PTY, continuously reads frames and sends heartbeats. This is the native app's terminal transport, not a simulator or hardware-device run.

All three view the same CLI-created managed shell. A raw byte sink records exactly what the foreground process receives, independently of terminal echo or input acknowledgment. No proof injects test bytes directly into that shell: TUI input enters through the outer TUI, browser input enters through xterm and the real SDK, and iOS-protocol input enters through stdio. One private run tree contains both tmux sockets, Sidecar state/config/cache, auth discovery, raw input and logs. The script scrubs inherited identities, uses explicit project arguments, bounds binaries and requests, and stops only the servers and children it creates. It never addresses the default tmux server, installs a service, changes Tailscale or touches port 7871.

## Measurements

Final post-merge run on 2026-10-04 used Sidecar `86205ffed374bb0350adb5c4eae6153c7743bc7f` (main merged at `c3ac2418`) and sidecar-ui main `fdad8665c3f846591d743b91ac01ca4fb3ae6253`. Full Go tests and lint were running concurrently. The machine-readable record is [final-results.json](u1h-evidence/final-results.json); [initial-results.json](u1h-evidence/initial-results.json) preserves the pre-merge run.

| Journey | Result |
| --- | --- |
| Fitted geometry | TUI 91×39, browser 121×41, iOS-protocol peer 73×19 |
| Alternating input | 12 viewer handoffs; exact 40 expected / 40 received bytes including two additional focused input takeovers; no loss, duplication or reordering |
| Recorded focus/alternating-input handoffs | 14 measured handoffs at 14–210 ms including blur/focus dispatch and exact byte receipt; each fit converged inside the proof's 5–6 second bound |
| Competing focused input | Two additional iOS/browser takeovers while both remained focused; 1,657 ms total including holder checks; all four additional bytes arrived exactly |
| Browser / iOS blur | 6 / 7 ms to an unowned pane |
| Holder labels | 18 matched wire observations; foreign-holder TUI chrome checked; active losing browser showed “Sized for iPhone proof” |
| Abandoned focused peer | iOS kept heartbeating without input; advertised idle passed six seconds after 8,043 ms; active browser presence then won in 194 ms without terminal input |
| Quiet contention window | 6,578 ms with all viewers connected and zero `resize-window` calls; browser remained the holder and geometry never changed |
| Resize-counter positive control | An explicit same-size `resize-window` was observed by the same hook, proving the counter covers real tmux commands, including control actors |

The five-second idle margin compares durations advertised by the holder, sampled at lease refresh. It is not a promise of transfer exactly five seconds after the last physical keypress. The proof waits for that evidence before activating the competing viewer, records the actual abandoned time, and bounds active-to-holder latency. Blur and input takeovers are tested independently and require no idle wait.

## Sidecar defects fixed

| Issue | Failing-first evidence | Fix |
| --- | --- | --- |
| `td-959739` | `TestHostingPaneIsScopedToItsServer`: foreign-server `%0` was incorrectly considered the hosting pane; live TUI stayed 80×24 and timed out | Remember the hosting socket before clearing TMUX; scope the hosting-pane guard to the addressed server (`bdb8b674`) |
| `td-34e576` | `TestFocusedAttachmentsDoNotPingPongAndPasteCannotInjectCommands`: three settled presence calls produced three `resize-window` calls | Conditional single-pane resize inside the identity/lease transaction |
| `td-ccc940` | `TestLocalApplicationFocusRefitsUnchangedViewport`: local focus returned no refit command; real idle TUI stayed at iOS 73×19 for over six seconds | Reconcile actual pane geometry on local application focus, and activate project interactive geometry arbitration |
| `td-4a352f` | Real proof timed out waiting for TUI chrome to name holder Browser | Cache advisory metadata in the existing lease observation; use the same holder interpretation as headless streams and one shared `termpreview` hint formatter in project Workspaces and global Sessions |

The latter three fixes are in `6af48fd7`. Focused regressions pass. No wire types, capabilities, API routes, fixture shapes or generated schemas change. References clarify single-pane settled resizing, sampled idle evidence and TUI holder/focus behavior.

Gate friction `td-b2336e` exposed an existing notification test setup race twice: its real 75 ms playback timer could expire before the test finished establishing the blocked ledger and dispatching cancellation. `725456aa` replaces that timer with the existing manual clock, waits for timer registration, and fires only after persisted cancellation. It retains the assertion that sound cancels while native-ledger removal remains blocked; 25 focused repetitions passed. Notification production behavior is unchanged. The concurrent full suite also hit a remote-agent five-second prompt-stall threshold; its focused retry and subsequent full CLI package run passed. [gate-failures.txt](u1h-evidence/gate-failures.txt) preserves the initial failures and retry output.

## Gates and review

All five required gates passed after merging main `c3ac2418` into `86205ffe`: `go build ./...`, full `go test ./...`, `make lint` (zero issues), `./scripts/ui-api-proof.sh`, and `./scripts/three-viewer-proof.sh`. The full suite uses `GOFLAGS=-p=1` to run one package at a time; no tests are skipped. The final successful full command is `GOFLAGS=-p=1 timeout 1800 go test ./...`. Its previous 900-second run reached the terminal/API packages without a reported failure before hitting the process bound; the longer retry completed successfully with cached completed packages. Earlier failures and the manual-clock correction are recorded above. An intermediate already-red run crossed the subsequent main merge and is excluded from final validation.

[gates.txt](u1h-evidence/gates.txt) records commands, exits and retained logs. Source and proof evidence were reviewed in this lane; independent review belongs to the orchestrator. Both private tmux servers and all three-viewer children were stopped, the API endpoint discovery file was removed, and the sidecar-ui working tree is clean. The lane never approves itself and leaves `td-295605` open for the orchestrator.

## External findings and limits

`td-945516` records a sidecar-ui defect: foreign geometry resets trigger xterm exceptions reading `loadCell` and `setCellFromCodepoint`. Both complete runs recorded seven exceptions even though lease/input assertions passed. Visual inspection of [final-browser.png](u1h-evidence/final-browser.png) confirms the terminal content area is blank after these exceptions; [initial-browser.png](u1h-evidence/initial-browser.png) shows the same result before merging. [browser-errors.json](u1h-evidence/browser-errors.json) preserves the initial exact messages and browser stacks. The proof reports exceptions in `results.json` and retains its real wire transcript, so a geometry/input PASS does not certify browser rendering. No sidecar-ui source was edited. Its td database is uninitialized, so the finding is in the canonical Sidecar task store and was sent to `@ui-api-orch`.

This certifies a bounded local loopback journey through the real three transports and viewers, with the iOS transport represented by a Go client. It does not certify the native app's UI, simulator or device, remote-owner forwarding, tailnet latency, reconnects, paste/IME/selection, sustained typing load or multiple terminals. Those require their own lanes or proofs. The existing UI API proof also exercises negotiated paste and transport/security behavior. The quiet-window counter covers one managed single-pane shell, not arbitrary tmux layouts.
