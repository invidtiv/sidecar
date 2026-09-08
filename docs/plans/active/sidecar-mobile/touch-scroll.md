# Sidecar mobile: touch scrolling over a controlled terminal

**Status:** proposed. Nothing here is implemented. **Parent:** [Sidecar mobile](../sidecar-mobile.md), execution in [execution.md](execution.md). **Desktop authority this mirrors:** [Consistent terminal scroll](../../implemented/consistent-terminal-scroll.md) and the shared rule in `internal/tty/wheel.go` and `internal/tty/wheel_route.go`. **Verified against:** sidecar `f5fc0248`, sidecar-mobile `05c68b5`. Every file:line below was read in those trees.

## 0. The one-paragraph answer

On the phone, a person controlling a Claude Code session cannot see anything above the visible grid. The reason is not a missing transport: the frame already reports `mouse_any`, `mouse_sgr` and `alternate_screen`, the `input` request already delivers raw bytes to `tmux send-keys`, and the client already encodes SGR wheel reports and forwards them, but only for a two-finger drag, one report per row of finger travel, each report as its own serialized request with a full acknowledgement round trip, no momentum, and a command buffer that fails the whole session when a fast gesture overflows it. The fix is the desktop's rule carried to touch: **who owns a scroll gesture is a property of the pane, decided from the authoritative frame modes, never from the gesture or the app's name.** A one-finger drag over a pane whose application has asked for mouse reports becomes wheel notches, coalesced and paced exactly as the desktop paces a trackpad flick, with at most one scroll request in flight. A drag over a plain shell keeps doing what it does today: it never sends bytes, and it opens the bounded owner History snapshot. The server changes only in documentation.

## 1. What exists today

### 1.1 Facts on the wire

Every full frame carries `modes.mouse_any`, `modes.mouse_sgr` and `modes.alternate_screen` (`internal/mobileproto/protocol.go:263-265`, produced by `modesFromSnapshot` at `internal/mobile/render.go:149`). They come from tmux's `#{mouse_any_flag}`, `#{mouse_sgr_flag}` and `#{alternate_on}`, the same facts the desktop reads. The reference contract already says "mouse state affects native input encoding" (`docs/reference/mobile-protocol.md`).

The `input` request carries base64 bytes bounded by `MaxInputBytes` = 64 KiB (`protocol.go:11`). The service decodes them and calls `geometry.SendLiteral` (`internal/mobile/service.go:767-796`), which is a lease-conditional `tmux send-keys -t <pane> -H <hex>`. An SGR wheel report is ordinary input: it is bytes a user gesture produced, not a reply the native emulator generated, so it does not touch the v0 non-goal about never forwarding emulator replies. Every request consumes one `operation_sequence`, is answered by one `accepted` response, and the client sends one operation at a time.

### 1.2 The desktop rule

`tty.RouteWheel` (`internal/tty/wheel.go:55`) gives a notch to the application only when the host may write, the app has asked for mouse reports, the pointer is on a pane cell, and no escape modifier is held; everything else scrolls the host's own window. `WheelBurst` (`wheel.go:103-141`) coalesces a flick: held-back delta is never dropped, it rides out with the next flush; once a flush has been forwarded to the pane, later flushes of the same gesture space themselves at least `WheelPaneDebounce` = 30 ms apart (`wheel.go:93`, td-b8c54e); a flush sends at most `MaxWheelNotchesPerFlush` = 10 reports (`wheel.go:29`); all reports of one flush go out in one `send-keys` (`SendSGRWheel`, `internal/tty/session.go:335`). `WheelNotches` (`wheel.go:242`) divides a line delta by `mouse.WheelScrollLines` = 3 because the application applies its own lines per notch. Encoding: `\x1b[<64;col;rowM` up, `\x1b[<65;col;rowM` down, press form only.

### 1.3 The mobile client

All paths are in `App/LiveTerminalViewport.swift` of the mobile repo.

- **Route.** `sidecarTerminalScrollRoute(mouseReporting:alternateScreen:)` (`:47-53`) answers `.remoteWheel`, `.applicationArrows` (alternate screen without mouse: arrow keys), or `.unavailable` (normal buffer). It reads only the authoritative modes of the last applied frame. This is the correct rule and it stays.
- **Two-finger vertical pan** (`installTerminalScrollGesture`, `:99-107`; `handleTerminalScroll`, `:174-189`) quantizes cumulative translation to lines at one line per cell height (`SidecarTerminalScrollAccumulator`, `:56-73`) and calls `sendScroll` (`:239-259`) on every `.changed` event that crosses a cell. `sendMouseWheel` (`:261-284`) emits one report per line, so one row of finger travel is one notch, and the application then applies its own lines per notch on top.
- **One-finger vertical pan** (`handleOneFingerPan`, `:195-232`): the route is fixed at `.began`. Keyboard focused: a 12 pt downward drag resigns first responder and does nothing else. Keyboard hidden on a normal buffer with History available: a downward drag requests History once. Otherwise ignored. Over a mouse-reporting or alternate-screen pane, one finger does nothing.
- **Delivery.** `onScrollInput` → `TerminalViewportContainer.sendInput` (`:493-500`) → `LiveSessionModel.sendInput` (`App/LiveSessionModel.swift:205-226`) → `LiveCommandBuffer.append(.input(data))`. The buffer holds 64 commands and 256 KiB (`App/LiveLifecycleFence.swift:53-56`), `drainCommands` (`LiveSessionModel.swift:480-538`) sends one command and awaits its acknowledgement before the next, and an overflow fails the session (`LiveSessionModel.swift:468-478`). The only coalescing anywhere is `replacePendingResize` (`LiveLifecycleFence.swift:72-79`).
- **Measured cost per request.** 27 ms median, 137 ms p95 request-to-ack ([execution.md](execution.md), M0 evidence). A drag that crosses 30 rows queues 30 requests and drains them for roughly a second after the finger stopped; a fast drag through many rows can hit the 64-command ceiling.
- **Momentum.** None. `UIPanGestureRecognizer` stops emitting when the finger lifts.
- **Recorded decisions this plan must respect** (`docs/readiness/native-terminal-interactions.md` in the mobile repo): SwiftTerm scrollback stays zero because replacement frames cannot establish history; History is a protocol-backed snapshot, never accumulated frames; the gesture never infers an application from its name or content; a drag that starts focused is dismissal-only today.

### 1.4 What the user experiences, and why

Observed on this Mac's live default tmux server, read-only, 2026-09-08:

| Pane | Application | `mouse_any_flag` | `alternate_on` | tmux `history_size` |
|---|---|---|---|---|
| a current Claude Code (2.1.263) | agent harness | 1 | 1 | 24 |
| an older node-based harness | agent harness | 0 | 0 | 2 |
| interactive shells | zsh | 0 | 0 | 2 to 11 |

Current Claude Code owns the wheel and keeps its transcript inside the pane; tmux holds nothing to page. The phone's two-finger route is therefore right, but two fingers is not the gesture anyone reaches for, one finger over that pane does nothing, and even two fingers arrive one serialized round trip per row with no momentum, so a natural gesture moves a few rows and stops. That is "no way to scroll beyond the first screen."

The older harness in the second row asks for neither mouse reports nor the alternate screen and leaves tmux with two rows of history. No client, desktop included, can scroll that transcript; this plan does not claim to.

## 2. Chosen design

> **Law 1. Who owns a scroll gesture is a property of the pane.** The route is `sidecarTerminalScrollRoute` over the last applied frame's authoritative modes, re-evaluated on every flush, never fixed for a gesture and never inferred from content. This is `tty.RouteWheel` with `WritesEnabled` = control held and `InPane` = the touch is on the terminal grid.

> **Law 2. A forwarded notch is input, coalesced before the wire.** Every forwarded flush is one `input` request carrying every report of that flush, paced at least 30 ms apart, capped at 10 notches, with at most one scroll request in flight and at most one scroll flush pending in the command buffer. Held-back notches ride out with the next flush; they are never dropped and never fail the session.

> **Law 3. A pane that has not asked for the wheel never receives scroll bytes.** The normal-buffer route stays `.unavailable`: one finger keeps opening the bounded owner History snapshot, exactly as approved under td-3fb657, and zero input requests are produced. Nothing here adds client-side scrollback.

> **Law 4. The natural gesture scrolls; the explicit gesture stays.** One finger scrolls a pane that owns the wheel. The two-finger gesture keeps working unchanged as the always-available explicit form.

### 2.1 Gesture to notches

One notch per `linesPerNotch` × cell height of finger travel, with `linesPerNotch` = 3 to match `mouse.WheelScrollLines`: the application applies its own lines per notch, so one row of travel per notch makes content run about three times faster than the finger. The existing accumulator keeps its remainder semantics (a 7 pt then 10 pt drag at a 16 pt cell must not emit a line; `native-keyboard-review.md`). `.applicationArrows` stays at one arrow per cell height because an arrow is one line. The constant is a named tuning value with a device check in §5, not a hard-coded literal.

### 2.2 Coalescing and pacing: `SidecarScrollBurst`

A clock-injected value type in the mobile repo mirroring `tty.WheelBurst`, with the same constants and the same tests translated: base window 16 ms, 12 ms once a burst is under way, 30 ms minimum spacing between forwarded flushes, 500 ms burst timeout, 10 notches per flush, pending carried into the next flush, `reset()` on control loss or route change. Its output is a flush of `(direction, notches)`; a flush with mixed directions since the last send nets them and sends the remainder in one direction, so a finger that reverses does not send both.

Below it, the command buffer gains `mergePendingScroll`: when the tail command of `LiveCommandBuffer` is a scroll flush of the same direction, add the notches and re-encode instead of appending, so scrolling occupies at most one slot and cannot reach the 64-command ceiling. A scroll flush is a distinct `LivePendingCommand` case (encoded to `input` bytes at drain time, using the current frame's `mouse_sgr` and the touch column and row) rather than pre-encoded `Data`, so the merge is arithmetic and the encoding uses the freshest modes. The one-operation-at-a-time drain then gives natural back pressure tied to the real round trip: while one scroll request awaits its acknowledgement, everything further accumulates into the single pending flush.

### 2.3 One-finger routing table

| Pane route | Keyboard focused | One-finger vertical drag |
|---|---|---|
| `.remoteWheel` | either | scrolls the application (this plan) |
| `.applicationArrows` | either | arrows to the application, finger-tracked, no momentum (this plan) |
| `.unavailable` (normal buffer) | focused | dismiss the keyboard (unchanged) |
| `.unavailable` (normal buffer) | hidden | downward drag opens History once (unchanged) |
| any | control not held | nothing is sent; unchanged |

Over a wheel-owning pane with the keyboard up, keyboard dismissal is left to UIKit's `keyboardDismissMode = .interactive`, which is already set (`LiveTerminalViewport.swift:122`) and engages when the finger drags down past the keyboard's top edge, plus the existing header keyboard button. The explicit 12 pt resign stays only on the normal-buffer route, where there is nothing else for a drag to mean. This is the one place the plan changes an approved interaction; see §6.

Gesture arbitration: SwiftTerm's own single-finger pan recognizers stay disabled while focused (`:167-172`) and the downward veto while first responder (`:134-147`) is narrowed to the normal-buffer route, so a downward one-finger drag over Claude Code reaches the scroll handler instead of being swallowed. The two-finger recognizer keeps `minimumNumberOfTouches = 2`.

### 2.4 Momentum

`.remoteWheel` only. On `.ended`, take the recognizer's vertical velocity and run a decay animator on `CADisplayLink` with UIScrollView's normal deceleration rate (0.998 per millisecond), feeding synthetic translation into the same accumulator and burst. It stops when velocity falls below half a cell per second, after 1.5 s, when control is lost, when the route stops being `.remoteWheel`, or when a new touch lands. Momentum never applies to `.applicationArrows`: arrows are keystrokes, and a decaying tail of arrows into vim or less would keep moving a cursor after the finger left.

### 2.5 Interplay with frames and fences

- Scroll bytes do not depend on the frame content, so `LiveInputReseedBuffer` holding while a frame is stale is unnecessary for them; a pending scroll flush waits only for the drain like any other command. It still carries a valid `last_output_sequence`, which the model already maintains.
- The application redraws after each forwarded flush and the owner emits coalesced full frames; frame application remains back-pressured on the renderer (`publishAndWaitForApplication`). No change.
- A mode change mid-gesture (the app exits to the shell) is seen on the next applied frame; the next flush routes `.unavailable`, sends nothing, and the burst resets. A control loss resets the burst, drops the pending flush, and stops momentum.

### 2.6 Server

No protocol change. `docs/reference/mobile-protocol.md` gains two sentences: wheel reports are ordinary `input` bytes the client encodes from the frame's mouse modes, and clients are expected to coalesce them so that one request carries a whole flush. The `hello` capability set is unchanged.

## 3. Alternatives rejected

- **A `scroll` request type routed on the server with `tty.RouteWheel`.** The server has no local window to scroll for a mobile client and would answer with the same facts the frame already delivers. It adds protocol surface, a hub forwarding case, and a fixture, for no behavior the client cannot decide from `modes`.
- **Driving SwiftTerm's own `UIScrollView` with a tall content size and reading `contentOffset` deltas.** Gives free deceleration and rubber banding, but SwiftTerm uses that offset to scroll its own buffer, and the recorded decision keeps that buffer at zero rows. Two owners of one offset is how the desktop pane used to tear.
- **Client-side scrollback from accumulated frames.** Rejected in `native-terminal-interactions.md` and reaffirmed: replacement frames cannot establish history.
- **Sending one report per gesture event with no pacing.** Reproduces td-b8c54e over a network: one lease-checked `send-keys` per tick, and on mobile one acknowledged round trip per tick as well.
- **Keeping two fingers as the only scroll gesture.** It is already implemented and already fails the person, because it is not the gesture anyone uses.

## 4. Work sequence

Each slice is a reviewed commit in the mobile repo unless noted; the parent plan's delegation and independent-review rules apply.

1. **S1 Throughput.** `SidecarScrollBurst` with translated `WheelBurst` tests; the scroll `LivePendingCommand` case, drain-time encoding, and `mergePendingScroll`; the two-finger path adopts both. Behavior visible immediately: a two-finger flick travels its full distance in a handful of requests and can no longer overflow the buffer.
2. **S2 One finger.** The routing table in §2.3, arbitration narrowing, `linesPerNotch`, and the education sheet copy in `App/TerminalPreviewView.swift:289` changed from "Use two fingers to scroll a full-screen terminal app" to describe one-finger scrolling with two fingers as the alternative. Update `docs/readiness/native-terminal-interactions.md` to the new current state.
3. **S3 Momentum** for `.remoteWheel` per §2.4, with an injected clock so tests drive a whole decay without sleeping.
4. **S4 Proof and docs.** The device proof in §5; the two-sentence addition to `docs/reference/mobile-protocol.md` in this repo; status updates in this file, [sidecar-mobile.md](../sidecar-mobile.md) and [execution.md](execution.md).

S1 is independent and the highest value per risk. S2 and S3 depend on S1. S4 closes the plan.

## 5. Acceptance evidence

Unit, in the mobile repo's focused viewport and model suites:

- Burst: a held event carries its notches into the next flush; a flush never exceeds 10 notches; forwarded flushes of one gesture are at least 30 ms apart; a reversal nets to one direction; `reset()` on control loss drops pending notches; a route change to `.unavailable` sends nothing.
- Buffer: a second scroll flush merges into a pending one and the buffer's command count does not grow; a keystroke between two flushes is not reordered; a resize still coalesces as before.
- Routing: the §2.3 table, row by row, including "normal buffer, keyboard hidden, downward drag requests History exactly once and produces zero input bytes" (the existing td-3fb657 tests must keep passing unchanged).
- Encoding: SGR and legacy reports unchanged from today's tests; a flush of N notches encodes N reports in one `Data`.
- Momentum: a decay from a known velocity produces a bounded, monotone notch series and stops at the thresholds in §2.4.

Live, on a personal device against a Claude Code 2.1.x pane on the hub, with the same isolation rules as every other mobile proof:

- A one-finger drag and a one-finger flick each move the transcript and stop when the gesture does; the number of `input` requests per flick, counted from the client's protocol metadata log, is at most one per 30 ms of gesture plus momentum, and the command buffer never overflows.
- The same drag over a plain zsh pane opens History and the metadata log shows zero `input` requests.
- The two-finger gesture still works over the same pane.
- `linesPerNotch` = 3 is confirmed or adjusted from the observed ratio of transcript movement to finger travel, and the chosen value is recorded here.

## 6. Open decisions

- **One finger over a wheel-owning pane with the keyboard up scrolls rather than dismisses** (§2.3). Recommended, because UIKit's interactive dismissal already covers dragging into the keyboard and the header button covers the rest, and because a drag over an app that owns scrolling means scroll everywhere else on iOS. The alternative keeps the approved dismissal-first rule everywhere and accepts that scrolling Claude Code requires the keyboard to be hidden first. Marcus decides; the default is the recommendation.
- **Momentum in the first delivery** (§2.4). Recommended for `.remoteWheel` because a flick is how a long transcript is read; it can be deferred to S3 without affecting S1 and S2.
- **`linesPerNotch` default of 3** pending the §5 device check.
