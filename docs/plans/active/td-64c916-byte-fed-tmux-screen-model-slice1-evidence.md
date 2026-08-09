# Slice 1 evidence: ordered control-stream barrier, seed/replay, resync

**Task:** `td-4cfc70` — Slice 1 of the byte-fed tmux screen model spike
(`td-64c916`).

**Date:** 2026-08-08 · **tmux:** 3.6b · **Platform:** darwin 25.5.0

## Verdict

**PASS — the spike continues.** tmux's command/notification boundary does
provide a reliable seed cut, and it is exact rather than approximate: no
heuristic, sleep, retry, or fudge is used anywhere in the implementation.

- No duplicated or omitted bytes across all six required scenarios (attach,
  resize, pause/continue, reconnect, unsubscribe, generation replacement),
  proved by continuously numbered lines written *while* the seed transaction
  runs.
- Existing capture delivery is unchanged when the model path is off, proved by
  a test that asserts the exact `ControlSnapshot` value and asserts that zero
  model-path tmux commands are issued.

Two real defects were found and fixed on the way (§7): a tmux command that has
never parsed, and a seed off-by-one in the slice-0 adapter.

## 1. What the isolated tmux test actually observed

### 1.1 The shell-level probe

Before writing any code, a raw probe attached `tmux -C` to an isolated socket
while a pane wrote `L%06d` lines continuously, issued the seed transaction
mid-stream, and analysed the resulting control stream byte for byte. Observed,
not assumed:

```text
begins [0, 3696, 3699]   ends [1, 3698, 5705]
outputs before / inside / after the transaction: 3692 / 0 / 16142
between the two response blocks: []          (nothing at all)
capture numbers: 2004 lines, ending at 3594
replayed numbers: 16405 lines, starting at 3595
overlap: none        duplicates: none        gap: none
capmax 3594 -> aftermin 3595
```

The two facts that matter:

1. **Zero `%output` notifications appeared inside a response block, and zero
   appeared between the metadata block and the capture block.** tmux queues a
   command list and executes it without returning to its event loop, so a pane
   read cannot interleave.
2. **The cut is exact at the response.** The last line rendered into the
   capture was 3594; the first line delivered as a post-response notification
   was 3595. Every number appeared exactly once across the two halves.

This is the whole basis for the barrier. Bytes seen *before* the capture
response in receive order are provably already rendered into that capture;
bytes seen *after* it are provably not.

### 1.2 The Go integration tests

`internal/tty/control_model_integration_test.go` re-establishes the same
property through the real Sidecar transport, against a private tmux socket in
the test's own temp dir with `TMUX` scrubbed from every child environment.

| Scenario | Test | What it proves |
| --- | --- | --- |
| Attach mid-stream | `TestModelAttachMidStreamLosesNoBytes` | The retained window straddles the attach point — it contains lines tmux had already rendered before the seed *and* lines that only arrived as replayed bytes — and every number in it is consecutive. It then stops the writer and requires the model's tail to equal tmux's own `capture-pane` tail. |
| Resize | `TestModelResizeReseedsAndLosesNoBytes` | A resize reseeds, the model's geometry equals tmux's authoritative `pane_width`/`pane_height`, and the number stream is continuous across the boundary. |
| Pause / continue | `TestModelPauseContinueForcesReseedAndStaysContinuous` | A deliberately slow consumer makes tmux actually pause the pane under `pause-after`. The pause is reported as a resync, the model reseeds, converges on exactly tmux's tail, stays internally continuous, and post-continue bytes still reach it. |
| Reconnect | `TestModelReconnectFallsBackThenReseeds` | Killing the control client falls the consumer back and terminally invalidates the model; a fresh subscription seeds cleanly and continuously. |
| Unsubscribe | `TestModelUnsubscribeStopsFrames` | No frame is delivered after `Close` returns. |
| Generation replacement | `TestModelGenerationReplacementReseeds` | Hide/show replaces the control client, bumps the generation, and the new generation seeds from scratch (`Seeds == 1`) rather than reusing a model. |
| Seed atomicity | `TestSeedTransactionHalvesAreNotInterleaved` | Under maximum-rate output and repeated reseeds, the seed-race detector (§3.2) never fires. |

Each integration test was run three times in a row plus once under `-race`; no
flakes.

## 2. The barrier design and why it is correct

### 2.1 One ordered stream

Before this slice, `processControlChannel.dispatch` invoked command-response
callbacks **inline on the reader goroutine** and pushed notifications onto a
separate event channel drained by the session client. Two consumers, two
orderings: a capture response could be processed before or after pane bytes
that tmux emitted on the other side of it, entirely depending on scheduling.

Now:

- `dispatch` still correlates a response with its FIFO callback on the reader
  goroutine — receive order is authoritative there and nowhere else — but it
  **attaches the callback to the event** (`controlEvent.Callback`) and places
  the response on the same `events` channel as `%output`.
- `sessionControlClient.run` is the single ordered actor. It drains that one
  channel and invokes response callbacks itself, so a response occupies exactly
  its position in the byte stream.
- FIFO command callbacks remain as an internal convenience, as the plan allows,
  but they no longer bypass pane-byte ordering.

Correctness argument:

1. tmux writes one client output buffer, so stream order equals processing
   order.
2. tmux never emits a notification inside a command block (documented, asserted
   by the parser, and observed in §1.1).
3. The reader parses lines in order and enqueues in order onto one channel.
4. One actor dequeues in order.

Therefore: **received-before-the-capture-response ⇒ in the capture; received
after ⇒ not in the capture.** The seeding state discards the former and the
live state replays the latter. Nothing else is required, and nothing is
timing-dependent.

Lifecycle calls that arrive on other goroutines (`add`, `remove`, `resize`,
frame coalescing, the discard probe) are funnelled onto the actor through an
`actions` channel and a `modelTick` channel, so model state and pane bytes are
only ever touched in one place.

**Constraint the code depends on:** a response callback must do its work
*inline* on the actor. Deferring it back onto the action queue would let a
queued `%output` overtake it and destroy the barrier. This is stated in the
code comment on `handleEvent`'s response case.

### 2.2 Deadlock analysis of the write path

Routing responses through the actor means the actor can call `channel.Send`
while the reader is blocked on a full `events` channel. That is safe because
**tmux never blocks writing to a control client**: it buffers client output in
memory and, when a client falls behind, either discards bytes (counted by
`client_discarded`) or pauses the pane — which is precisely why those two
mechanisms exist. Its event loop therefore keeps draining our stdin, so a
command write cannot deadlock against a stalled reader. This is recorded in a
comment on `processControlChannel.write`; a dedicated writer goroutine was
considered and rejected as unnecessary complexity given that guarantee.

### 2.3 The seed transaction

One transaction, written as a **single `io.WriteString`** containing two command
lines (`SendPair`), so tmux reads and queues them together and executes them
back to back:

```text
display-message -p -t %N '#{cursor_x},#{cursor_y},#{cursor_flag},#{pane_height},
  #{pane_width},#{history_size},#{alternate_on},#{mouse_standard_flag},
  #{mouse_button_flag},#{mouse_all_flag},#{mouse_sgr_flag},#{client_discarded},#{pane_id}'
capture-pane -p -e -S -<scrollback> -t %N
```

New metadata relative to the capture path, as the plan requires:
`alternate_on`, the individual mouse flags, and `client_discarded`. `pane_id`
was added on top so a pane replaced under the same target is detected.

**Why two lines rather than one `;` command list.** A `;` list is atomic in
tmux, but a *parse* error anywhere in it collapses the whole list to a single
error block (observed: `display-message ... ; capture-pane --nope` produced one
`%begin`/`%error` pair, not two). That would permanently desynchronise the
positional FIFO. Two lines are two independent commands: each always produces
its own block, so the FIFO stays 1:1, and writing them together preserves the
atomicity that matters.

## 3. Resync triggers and how each is detected

| Trigger | Detection | Effect |
| --- | --- | --- |
| First subscribe / Sidecar restart / pane switch | A new `ControlRequest` with `OnModelFrame` set creates a feed in `modelIdle` | `ResyncFirstSeed` seed |
| Control-client reconnect | `channel.Done()` or `%exit` reaches the actor | All feeds terminally invalidated (`ResyncReconnect`); recovery is a new subscription |
| `client_discarded` growth | Compared at every seed, plus a 1 s cadence probe (`display-message -p '#{client_discarded}'`) while any feed is live | `ResyncDiscarded` reseed |
| `%pause` | Notification | Quoted `refresh-client -A '%N:continue'` **and** a `ResyncPause` reseed — tmux drops the pane's buffered output while paused, so continuity is broken either way |
| `%continue` | Notification | `ResyncPause` reseed |
| Pane identity change | Seed metadata `#{pane_id}` differs from the subscribed pane | `ResyncPaneIdentity`, terminal |
| Resize | `ControlSubscription.Resize` | tmux is resized first; the reseed reads back the authoritative resulting geometry. tmux's own `%layout-change` for the applied size then triggers a second reseed, which is what makes the final geometry authoritative. No in-model resize in this slice. |
| Layout / window-pane change | `%layout-change`, `%window-pane-changed` | `ResyncLayout` reseed |
| Seed metadata race | §3.2 | `ResyncSeedRace` reseed |

A resync requested while a seed is already in flight is collapsed into that
seed and re-issued once when it completes, so reasons are never lost and seeds
never stack.

### 3.2 The seed-race detector

If pane bytes were ever observed **between** the metadata response and the
capture response, the metadata would describe an older screen than the capture.
Those bytes are correctly discarded (they are in the capture), but the cursor
would be stale — and a stale cursor corrupts every replayed byte after it. The
feed counts raw events in that window and, if any occur, discards the seed and
reseeds rather than accepting a screen it cannot trust.

**This has never fired.** It is a detector for an assumption, not a workaround:
it exists so the assumption fails loudly instead of silently.

## 4. Failure modes

Every one of these invalidates **only** the affected pane model. None of them
touches the control-client reader, the Bubble Tea loop, the capture path, or
any other subscription — asserted by
`TestControlModelFaultIsolatesOnlyThatPane`, which faults a model and then
proves the capture path still delivers, no fallback fired, and the subscription
is still `UsingControl`.

| Failure | Handling |
| --- | --- |
| Malformed / short / non-numeric seed metadata | `ResyncModelFault`, terminal for that feed |
| Impossible dimensions (`width < 1`, `height < 1`, negative history) | Rejected in `parseSeedMetadata`; `ResyncModelFault` |
| `%error` on either half of the seed transaction | `ResyncModelFault` |
| Capture response without its metadata | `ResyncModelFault` |
| Emulator write/render error or panic | `screenmodel.Model` converts a panic into a sticky `ErrModelFault`; the feed is faulted |
| Unsafe pane target | Rejected by `controlPanePattern` before any command is built |
| Control client death | All feeds terminally invalidated; the consumer's existing `OnFallback` fires exactly as before |

## 5. No user-visible change

- There is no feature flag, no UI consumer, and no shadow comparison. Nothing
  in the workspace plugin sets `OnModelFrame`.
- With `OnModelFrame` nil the client builds no model, issues no seed
  transaction, runs no discard probe, and **does not even decode the `%output`
  payload** — the deferred-decode property of `controlEvent.Payload` is
  preserved.
- `TestCaptureDeliveryUnchangedWhenModelPathOff` asserts the delivered
  `ControlSnapshot` as an exact struct value, and asserts zero seed
  transactions and zero discard probes.
- The whole pre-existing `internal/tty` suite passes unchanged except for the
  two assertions that encode the behavior this slice deliberately changed (the
  response-callback delivery point, and the pause-continue quoting fix).

## 6. Exposure to GAP-9 and to `client_discarded`

### GAP-9 (grapheme clusters do not survive a `Write` boundary)

`x/vt` does not carry a partial grapheme cluster across a `Write` call, and
tmux chunks `%output` arbitrarily. **This design is exposed to it**, in exactly
one place and unavoidably: `feedModels` writes each `%output` notification's
decoded bytes to the model as one `Write`. A cluster split across two
notifications will therefore be mis-rendered — cells *and* cursor position.

Not worked around here, per the slice-0 finding that this is a semantic
upstream decision rather than a mechanical fix. It does not affect this slice's
result: the model is not authoritative, and the numbered-line proof uses ASCII.
It **must** be settled upstream (or the pin moved) before any surface is
enabled in slice 3. Reseeding does clear it, since every seed rebuilds the
emulator from scratch, so the blast radius is bounded to one pane between
resyncs.

Partial *escape sequences* split across notifications are fine — the emulator's
parser holds that state across `Write` calls. Only grapheme clusters are
affected.

### `client_discarded` gaps

- The counter is only read at a seed and on a 1 s cadence while a model is
  live. **Growth is therefore detected late, not at the moment of loss.** The
  seed that follows is the recovery, so the model is never left drifting, but
  frames published between the loss and the next probe can be stale by the
  discarded bytes. Sidecar enables `pause-after=5`, which normally pauses a
  pane rather than discarding for it, so this is a backstop rather than the
  primary path.
- `#{client_discarded}` resolves against the control client running the
  command. It returns empty outside a control client (verified); that is parsed
  as zero rather than as a fault.
- The cadence probe is deliberately **not** per output burst, so it does not
  violate the decision gate's "zero commands per output burst" requirement. It
  runs only while at least one model is live, which in this slice is only under
  test.

### Other recorded seed gaps

- **DECSET 9 (X10) and DECSET 1001 (highlight) mouse modes cannot be seeded.**
  tmux exposes no format for either. The model learns them only if the
  application re-enables them after the seed. `mouse_standard_flag` (1000),
  `mouse_button_flag` (1002), `mouse_all_flag` (1003) and `mouse_sgr_flag`
  (1006) are seeded.
- **Bracketed paste state is not seeded**, by design — the plan moved paste
  correctness to tmux at the input seam (shipped in `d706bd5`).
- The hidden VT state the plan already called out (scrolling margins, saved
  cursor and attributes, charsets, private modes) is still not reconstructible
  from a capture. Slice 2 measures how much that matters against real
  applications; this slice makes no claim about it.
- **One model per subscription, not per pane.** Two subscriptions to the same
  pane each seed independently. Correct but slightly wasteful; revisit if a
  real consumer ever needs it.

## 7. Defects found and fixed

### 7.1 `refresh-client -A %N:continue` has never parsed

The existing pause handler sent `refresh-client -A %7:continue` unquoted. tmux's
command parser reads a bare leading `%` as the start of a conditional directive,
so the command is rejected:

```text
%begin 1786237819 289 1
parse error: syntax error
%error 1786237819 289 1
```

Quoting the target (`refresh-client -A '%7:continue'`) works. **A paused pane
was previously never resumed** — it stayed paused for the life of the control
client, silently starving the capture path of output notifications for that
pane. This is a live-product bug independent of the screen model, fixed here
because the pause resync path depends on it.

### 7.2 Seed dropped the final row (slice-0 adapter)

`screenmodel.seedBody` trimmed one trailing newline from `Seed.Output`. tmux's
capture ends with a blank row whenever the cursor sits on an empty line below
the content, so trimming wrote one row too few and shifted the whole screen up
by one **against the cursor position reported in the same transaction**. The
first real mid-stream seed lost a line to it: the model rendered
`… L000009, L000011 …`, with `L000010` overwritten by the first replayed byte.

`Seed.Output` is now documented and implemented as **row separated, not row
terminated**: N-1 newlines mean N rows and the seed writes exactly that many.
Callers holding a shell-style newline-terminated capture strip the terminator
themselves (the fixture harness does). Pinned by
`TestSeedHonoursATrailingBlankRow`.

This was found only because the numbered-line integration test exists — no unit
fixture in slice 0 had a trailing blank row *and* a cursor below the content.

## 8. Deviations from the plan

| Deviation | Justification |
| --- | --- |
| The seed is two command lines written together, not one `;` command list | A `;` list collapses to a single error block on a parse error, which would permanently desynchronise the response FIFO. Two lines in one write keep the FIFO 1:1 and still execute back to back (§2.3). |
| `client_discarded` is polled on a 1 s cadence, not observed continuously | tmux offers no notification for it. The cadence is not per burst, so it does not violate the decision gate. Only runs while a model is live. |
| One model per subscription, not per pane | Simpler ownership for a slice with no real consumer; noted as a gap. |
| `Seed.Output` semantics changed in the slice-0 package | Required to fix a real off-by-one (§7.2). Slice-0 fixtures adapt by stripping their shell terminator; no fidelity result changed. |
| A seed-race detector exists although the race was never observed | It makes the ordering assumption fail loudly rather than silently. It is a detector, not a retry heuristic: it fires at most once per genuine race and never on a timer. |

## 9. Unproven claims and open risks

Stated plainly, because a recorded gap is worth more than an overstated pass:

1. **The tmux ordering guarantee is empirical, not documented.** tmux's man page
   does not promise that a command response cuts the output stream exactly. The
   evidence here (§1) is strong and the mechanism is understood, but it is an
   observation of tmux 3.6b on one platform. The seed-race detector is the
   standing guard.
2. **`%output` chunk boundaries are not controlled by Sidecar** — GAP-9 remains
   a blocker for authority (§6).
3. **Absolute history coordinates past the emulator's scrollback cap are still
   approximate** (slice-0's known `scrolledOff` limitation). Untouched here;
   Sidecar's 600-line capture window keeps it far from the cap.
4. **No real application matrix was run.** This slice proves byte continuity
   with numbered lines only. Full-screen TUIs, alternate screen, wide/combining
   text, and margins are slice 2's job, and nothing here should be read as
   evidence about them.
5. **Frame publication cost is not measured.** `publishModelFrames` renders the
   whole grid on a coalescing tick. That is fine at test volume; the decision
   gate's performance criteria are slice 2's.
6. **The pause test relies on making the consumer slow enough that tmux
   pauses.** It reproduced reliably (three consecutive runs plus a race run),
   but it is timing-shaped by nature and could become flaky on a much faster or
   much slower machine.

## 10. Verification

```text
go build ./...                             ok
go test ./...                              ok (all packages)
go test -race ./internal/tty/...           ok — internal/tty 23.6s, screenmodel 3.1s
go vet ./...                               ok
git diff --check                           clean
```

Every tmux invocation in the new tests carries an explicit `-S` inside the
test's own temp dir and runs with `TMUX` scrubbed; `newProcessControlChannelForSocket`
now scrubs `TMUX` too. The developer's default tmux server was verified intact
after the run.
