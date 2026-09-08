# Mobile terminal seed spike

**Task:** td-415417. **Baseline:** Sidecar `c8766af06d9362868518aad7b7da500f123a9019`. **Worktree:** `/Users/marcus/.codex/worktrees/mobile-seed/sidecar`. **Data:** synthetic only. **Result:** the current capture seed plus raw output strategy is falsified; server-normalized presentation frames are the candidate for SwiftTerm evaluation. This is decision evidence, not a frozen protocol.

## Decision

Do not expose the current `screenmodel.Seed` reconstruction followed by tmux `%output` bytes as the mobile stream. It preserves enough state for Sidecar's existing Go model to render many panes, but it is not an emulator checkpoint. Two histories can produce the same capture and 15-field metadata while leaving different hidden state, then render the same next byte differently. The focused fixtures demonstrate this for current SGR rendition and DECAWM autowrap.

Evaluate server-normalized ANSI presentation frames next. The proposed producer would use ordered `%output` only as a dirty signal, coalesce work, capture the authoritative tmux screen with `capture-pane -p -e -N`, read explicit tmux mode/cursor metadata in the same ordered transaction, and emit either a complete replacement generation or a changed-row repaint. It would never forward application output bytes to the mobile emulator. The fixture expects a gap to suspend deltas and requires the next accepted message to be a complete frame with a new reset generation. The production input path is proposed to stay attachment-scoped, acknowledge acceptance, and avoid replay after ambiguity; none of this mobile producer/input behavior ships in this spike.

The fixture [normalized-frames.json](../../../../../testdata/mobile-protocol/v0/experimental-terminal/normalized-frames.json) is the smallest candidate proof: full alternate-screen frame at sequence 1, a changed-row frame at sequence 2, and a full saved-main replacement at sequence 4 after the missing sequence 3 forces generation 2. A separate full frame pins a wide CJK cell, its continuation, a combining grapheme, and three blue trailing blank cells. The producer-side capture oracle preserves every expected cell; the Go x/vt diagnostic consumer drops the combining mark, which is another reason it is not the mobile fidelity oracle. A new mobile emulator receives each full frame. The SwiftTerm consumer must independently prove the resulting cells, cursor, modes, and absence of forwarded terminal replies before this strategy passes.

## Current seed audit

`buildSeedCommands` issues one ordered triple: 15-field metadata, saved-main `capture-pane -a`, then active `capture-pane`. The capture response position is the barrier. Output observed before that response is already represented by the capture and is discarded; output after it is replayed in actor order. Bytes between metadata and capture are detected as a seed race and force another seed. Discard-counter growth, pause, reconnect, layout change, resize, and pane identity changes also invalidate or reseed.

The current seed explicitly preserves active and saved-main grids, active and saved-main cursor coordinates, cursor visibility, geometry, bounded history, alternate-screen state, and tmux's 1000, 1002, 1003, and 1006 mouse flags. It detects a replaced pane and captures `client_discarded` as a continuity baseline.

It does not serialize the current rendition pen, DECAWM autowrap, scroll margins/origin, ordinary saved cursor/rendition, cursor style, bracketed paste, application cursor keys, application keypad, character sets, synchronized-output state, partial parser state, or mouse modes 9 and 1001. Resetting the consumer before seed deliberately removes stale hidden state, but it also establishes defaults for every missing field. A later raw byte stream can therefore diverge immediately.

The query rule remains sound only while the model is passive: tmux already answers terminal queries from the real application, and `screenmodel` drains and discards replies its observer emulator generates. A separate SwiftTerm consumer must do the same. Forwarding its replies would give the application two reply owners.

## Falsifying examples

The rendition fixture gives one reference history `SGR 31` and one default history. Both are blank at row 0, column 0 and have identical seed inputs. After `X`, the first reference cell is palette red while the current candidate seed renders terminal-default foreground.

The autowrap fixture gives one reference history with mode 7 reset and one with mode 7 set. Both are blank with the cursor at the rightmost column. After `AB`, the default/current candidate wraps `B` to the next row; the no-wrap reference keeps output at the last column. A capture cannot distinguish those histories.

Bracketed paste is a third direct gap: a model with mode 2004 set reports it before seed and reports the default after the current seed. Application cursor and keypad modes have the same capture ambiguity, although the Go presentation frame does not expose those two fields.

## Why normalized frames are credible

An isolated tmux 3.7c format inventory exposed more state than the current 15-field seed asks for: `bracket_paste_flag`, `keypad_cursor_flag`, `keypad_flag`, `wrap_flag`, `origin_flag`, `insert_flag`, `scroll_region_upper`, `scroll_region_lower`, `cursor_shape`, `alternate_on`, saved-main cursor, mouse flags, and synchronized-output flags. Current rendition no longer needs serialization because no raw application continuation reaches the consumer; every painted run states its own cell rendition from the authoritative `capture-pane -e -N` result. Application queries also stay between the application and tmux.

The production seam should remain one ordered attachment actor around existing control machinery. It owns a validated target handle, attachment identity, server incarnation, target generation, output sequence, reset generation, bounded dirty/coalescing state, and the last complete canonical frame. It asks the existing control client for an ordered metadata/capture transaction, converts capture rows to explicit presentation VT, and computes changed rows only against its own last complete frame. It emits complete frames on open, reconnect, resize, alternate/main transition, overflow, sequence discontinuity, and any unavailable delta base. It emits changed rows only inside one continuous generation. The mobile transport never receives raw `%output` payloads.

The production owner must capability-probe the added tmux format fields against the supported 3.4 role. Required input probes are `bracket_paste_flag`, `keypad_cursor_flag`, and `keypad_flag`; an unknown value may still allow viewing but must refuse the affected paste/key input instead of guessing. Mouse tracking/encoding flags are required only before mobile mouse forwarding is advertised. Delta-presentation probes are `alternate_on`, `wrap_flag`, `origin_flag`, `insert_flag`, `scroll_region_upper`, and `scroll_region_lower`; an unknown value must fall back to complete frames or refuse deltas. `cursor_x`, `cursor_y`, and `cursor_flag` are required screen facts. `cursor_shape` and `cursor_blinking` are cosmetic and may fall back without blocking control. `synchronized_output_flag` affects publication timing rather than input encoding. Horizontal margins and active character set were not present in the observed inventory. Full replacements reset emulator-owned scrollback and selection, trailing colored blanks need `-N`, and 30 Hz capture/coalescing cost remains unmeasured. Those are the next experiment's required evidence.

## Native tmux repaint alternative

A native tmux client was evaluated because it naturally emits a complete client repaint with modes, cursor state, colors, and terminal queries. On private socket `/tmp/sidecar-mobile-seed-native-4242.sock`, `attach-session -f ignore-size` emitted a complete alternate-screen repaint, but the first attach changed a detached 20×6 pane to the 80×23 client geometry. With `window-size manual` and an explicit leased 20×6 resize, reattach retained 20×6 but rendered the pane inside an 80×24 tmux client canvas with tmux border/filler and status UI. A read-only linked session can isolate its status option, but it still does not isolate one pane in a multi-pane window and it introduces tmux client controls into Sidecar's terminal stream.

Reject this as the M0 strategy. It becomes worth another bounded experiment only if Sidecar intentionally wants a tmux-window client and can prove read-only client output, separately gated user input, terminal-reply routing, selected-pane isolation, presentation-session cleanup, and lease-safe geometry without mutating the owner's session/window options. No production option or source was changed in this spike.

## Geometry and stale ownership

`DecideGeometryLease` already makes the arbitration rule state-free. The fixture uses two identities, `aerie-mobile-attach-a-4242` and `aerie-mobile-attach-b-4242`, which remain distinct inside one hub process and parse under the existing token shape. Including the attachment component before the final numeric component prevents hub PID liveness from proving a phone attachment alive; a stopped heartbeat becomes claimable after five unchanged observation ticks or 60 seconds elapsed after a prior unchanged observation. The fixture exercises only the elapsed arm with one prior observation and 61 seconds. Observation cadence, idle preemption, and mobile background/disconnect handling remain production obligations. This encoding is experimental and needs an owned constructor if selected; callers must not hand-build it across packages.

A fresh attachment A token causes attachment B to decline resize. After A backgrounds or disconnects and stops refreshing, B may claim once the observed token is unchanged for the stale budget. An explicit background/release can clear sooner only when the lease read succeeds and the current token still belongs to that same attachment.

The proposed release predicate is fenced by lease-read success, server incarnation, target generation, and current token owner. The test-only fixture predicate rejects a release after B has acquired ownership, a lease-read failure, a changed server incarnation, and a changed target generation. It permits only the current owner on the same target/server generation to clear. M0-C still has to integrate that predicate with real attachment state and prove host-side `sidecar open`/layout refusal while a fresh mobile owner holds the lease.

## Fixtures

All fixtures are under `testdata/mobile-protocol/v0/experimental-terminal/` and declare the reviewed baseline plus synthetic provenance:

- `seed-cases.json`: one positive current-seed case, two indistinguishable-state counterexamples, and missing input/query state.
- `normalized-frames.json`: the proposed full/delta/full-after-gap consumer sequence.
- `stream-gap.json`: sequence 41 followed by 43 must suspend and reseed; queued/unacknowledged input is never replayed.
- `geometry-lease.json`: two mobile attachments, disconnect expiry, stale release, lease-read failure, wrong server incarnation, and changed target generation.
- `manifest.json`: producer and SHA-256 pins for consumer vendoring.

## Reproduction

The focused synthetic proof is:

```sh
go test ./internal/tty -run '^TestMobileSeedSpike' -count=1 -v
```

It passes the positive capture state, rendition and autowrap falsifications, missing bracketed-paste reset, gap reset, normalized full/delta/replacement sequence, exact wide/combining/colored-blank cells, two-attachment lease arbitration, and release fencing.

The native-client observation used only explicit private sockets. Every tmux invocation included `-S /tmp/sidecar-mobile-seed-native-4242.sock` or `-S /tmp/sidecar-mobile-seed-native-readonly-4242.sock` and ran with `TMUX` and `TMUX_PANE` removed. Both private servers were killed through their exact socket after the attached clients detached. No Sidecar binary was launched, so no Sidecar state/config tree was opened or changed. The default tmux server was never addressed.

## Gate

The raw seed strategy fails now. The normalized experiment passes this Go fixture producer, and the independently implemented SwiftTerm 1.18.0 harness agrees on initial and continued cells, Unicode widths and combining marks, trailing colored blanks, cursor and modes; drops its emulator replies; and records bounded full/delta application measurements. That consumer evidence does not complete M0: real ordered tmux capture, supported-tmux capability probes, capture/coalescing latency at realistic dimensions, selection/scrollback behavior, transport, and device proof remain outstanding. If changed-row ANSI destabilizes selection/history or the required tmux modes cannot be obtained on 3.4, the next adjustment is canonical full/delta cell frames with a native Swift renderer adapter, not more guessed VT checkpoint fields.
