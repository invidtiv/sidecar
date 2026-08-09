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
  runs. Four of them (attach, resize, reconnect, generation replacement) assert
  that the checked window **straddles the seed cut**, which is the assertion that
  actually distinguishes replay from a capture; pause/continue is proved instead
  by convergence on tmux's own tail. §1.2 states per test which one applies.
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

The **straddle assertion** is the one that carries the slice's exit criterion, so
it is worth stating precisely what it is. A frame that is merely *continuous*
proves nothing: a window sourced entirely from a seed capture is continuous by
construction. `modelHarness.assertStraddlesSeedCut` therefore waits for reseeding
to settle, re-reads tmux's own tail (every line past that point is provably later
than the settled seed's capture), and then requires **one frame, from that same
seed generation, that contains both** lines older than the seed *and* lines past
that tail — with no gap and no duplicate anywhere between them. Lines past the
tail can only have reached the model as bytes replayed after the capture
response.

| Scenario | Test | What it proves |
| --- | --- | --- |
| Attach mid-stream | `TestModelAttachMidStreamLosesNoBytes` | Straddle assertion across the attach seed. It then stops the writer and requires the model's tail to equal tmux's own `capture-pane` tail — an oracle independent of the model. |
| Resize | `TestModelResizeReseedsAndLosesNoBytes` | A resize reseeds and the model's geometry equals tmux's authoritative `pane_width`/`pane_height`. Straddle assertion across the resize reseed. |
| Pause / continue | `TestModelPauseContinueForcesReseedAndStaysContinuous` | A deliberately slow consumer makes tmux actually pause the pane under `pause-after`. The pause is reported as a resync, the model reseeds, and **converges on exactly tmux's own tail** — that convergence is this test's real proof — and post-continue bytes still reach it. **No straddle assertion:** the seed is provoked by tmux's flow control at a moment the test does not choose, and the frame it checks for continuity has a 600-line window that may be entirely capture-sourced. Read this row as "pause is detected, recovery is a correct reseed", not as a replay proof. |
| Reconnect | `TestModelReconnectFallsBackThenReseeds` | Killing the control client falls the consumer back and terminally invalidates the model. A fresh subscription seeds cleanly, with a straddle assertion across the new subscription's seed. |
| Unsubscribe | `TestModelUnsubscribeStopsFrames` | No frame is delivered after `Close` returns. |
| Generation replacement | `TestModelGenerationReplacementReseeds` | Hide/show replaces the control client, bumps the generation, and the new generation seeds from scratch (`Seeds == 1`) rather than reusing a model. Straddle assertion across the new generation's seed. |
| Seed atomicity | `TestSeedTransactionHalvesAreNotInterleaved` | Under maximum-rate output and repeated reseeds, the seed-race detector (§3.2) never fires against tmux 3.6b. On its own this says nothing about the detector working; that is `TestControlModelSeedRaceIsDetectedAndReseeds` (§3.2). |

Each integration test was run three times in a row plus once under `-race` when
first written, and the whole tmux-touching set five more times after the review
fix pass; no flakes.

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

**This has never fired against tmux 3.6b.** It is a detector for an assumption,
not a workaround: it exists so the assumption fails loudly instead of silently.

Because it has never fired, "it did not fire" is not evidence that it works —
that observation is equally consistent with the detector being dead code, and a
dead detector would let a future tmux seed a stale cursor and mis-place every
replayed byte thereafter, silently. `TestControlModelSeedRaceIsDetectedAndReseeds`
therefore forces the interleaving directly on the ordered event stream (metadata
response → pane bytes → capture response, which no real tmux can be made to
produce on demand) and requires the detector to fire, the raced seed to be
discarded without publishing a frame, and a replacement seed to be issued.
Mutation-checked: with the `rawDuringMeta > 0` branch disabled the test fails.
`TestControlModelBytesBeforeMetadataAreNotASeedRace` pins the other side — bytes
arriving *before* the metadata response are the ordinary mid-stream attach and
must not trigger a reseed.

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

## 5. User-visible change

The screen model itself is invisible, but the slice is **not** a no-op for the
user, and an earlier draft of this section (and of `10e94d6`'s commit message)
wrongly said it was. Two behaviors changed:

1. **A paused pane now actually resumes.** §7.1: `refresh-client -A %N:continue`
   has never parsed, so a pane tmux paused under `pause-after` stayed paused for
   the life of the control client. Because `controlEventPause` does not
   `markDirty`, the view then froze — no output notifications, no capture. The
   quoted target fixes a live product bug that predates this work.
2. **Paste no longer guesses at bracketing.** `d706bd5` moved the decision to
   tmux (`paste-buffer -p`). A pane whose application requested DECSET 2004 now
   reliably gets the bracket codes even when Sidecar's inferred mode was wrong,
   and vice versa. Line endings are unchanged: `-r` is deliberately not passed
   (§8).

What is unchanged is the screen model's own footprint:

- There is no feature flag, no UI consumer, and no shadow comparison. Nothing
  in the workspace plugin sets `OnModelFrame`.
- With `OnModelFrame` nil the client builds no model, issues no seed
  transaction, runs no discard probe, and **does not even decode the `%output`
  payload** — the deferred-decode property of `controlEvent.Payload` is
  preserved.
- `TestCaptureDeliveryUnchangedWhenModelPathOff` asserts the delivered
  `ControlSnapshot` as an exact struct value, and asserts zero seed
  transactions and zero discard probes.
- That identity is a claim about **content, not concurrency**. The refactor moved
  every command-response callback — the capture path's included — off the reader
  goroutine onto the ordered actor, which is precisely what makes the seed cut
  exact. The consequence for existing consumers: a blocking `OnSnapshot` now
  backpressures the control reader, so a slow consumer can make tmux pause the
  pane or discard bytes for this client. That was not possible before. No
  Sidecar consumer blocks in `OnSnapshot` today; a future one must not.
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

> **PRECONDITION FOR SLICE 2 — read before interpreting any shadow-comparison
> result.** tmux offers no notification for `client_discarded`. It is read only
> at a seed and on a 1 s cadence, so there is always an unobserved window of up
> to `discardProbeInterval` in which tmux may have dropped bytes for this client
> and `publishModelFrames` will publish frames built from an incomplete byte
> stream anyway. In slice 1 this is harmless — there is no consumer. In slice 2
> it is **not**: a discarded-byte window and a genuine model defect look
> identical in a shadow comparison, and counting the former as the latter would
> make the spike fail for the wrong reason (or, worse, mask a real defect behind
> an assumed discard).
>
> Slice 2 must therefore discriminate them, not average over them. Every
> `ModelFrame` carries `Discarded` (the last observed counter) and
> `DiscardCheckedAt` (when it was observed) for exactly this purpose. The rule:
> **a mismatch observed at time T is unattributable unless the next successful
> check confirms the counter did not move across T** — i.e. a mismatch may only
> be scored against the model once a check *later* than T reports the same
> `Discarded` value the frame carried. Mismatches in an open window are reported
> as a separate, counted category. Any slice-2 mismatch number published without
> that split should be treated as unproven.
>
> Two cheap mitigations are available if the window turns out to dominate:
> shorten the cadence for the panes under comparison, or issue an on-demand
> `client_discarded` probe at each comparison point. Neither is per-output-burst,
> so neither violates the decision gate.

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
| **`paste-buffer -r` is not passed, although the plan specifies it** (plan §"prerequisite", and the input row of the surface table) | `-r` disables tmux's default LF→CR translation. A raw-mode program that has not requested bracketed paste reads **CR**, not LF, as "submit this line"; measured, such a pane received `41 0d 42` before and `41 0a 42` with `-r`, so a multi-line paste stopped submitting its lines. That is a real regression for every non-bracketed app, and it buys nothing: `-p` is the part that matters, and it is unaffected. Sidecar therefore issues `paste-buffer -p -d -b <name> -t <pane>` and line endings behave exactly as they always have. Pinned in both directions (plain pane receives CR; bracketed pane still receives the `ESC[200~ … ESC[201~` wrapper) by `TestSendPasteToTmuxBracketedAndPlain`. **The plan text should be corrected when it is next revised.** |

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
7. **A control client whose tmux dies with a saturated event channel can freeze
   the pane with no fallback.** `processControlChannel.dispatch` *receives* from
   the single-value `done` channel to avoid blocking on a full `events` channel;
   if it does, `sessionControlClient.run`'s own `Done()` case never sees the
   error, `clientFailed` and `OnFallback` never fire, and the pane stops updating
   silently with no reconnect. The shape predates this slice, but routing every
   command response through the same `select` made it materially more reachable.
   **Filed as `td-58cebc` (P1, tmux/terminal) and deliberately not fixed here** —
   the fix is to broadcast the death signal rather than consume it, which is its
   own change with its own test.
8. **`ControlManager.add` completes with a blocking `post()` while the manager
   mutex is held** (`control_manager.go`, called from `activate` and
   `startClient`), and the actor can concurrently enter `clientFailed`, which
   takes that same mutex. This is a genuine lock-order coupling introduced by
   this slice. It requires the 256-slot action queue to be saturated at the
   moment tmux dies, and — in this slice — it is unreachable in production at
   all: the `post` is guarded by `sub.request.OnModelFrame != nil`, which nothing
   sets. **Recorded rather than fixed:** every candidate fix moves the `add` call
   outside the manager lock, which changes when a client becomes visible to
   concurrent activation and opens a double-add window on the same subscription
   id (two delivery gates, one orphaned). That trade is not worth making for an
   unreachable path in a spike slice. It must be resolved before any consumer
   sets `OnModelFrame` — i.e. it is a slice-3 blocker, not a slice-2 one.
9. **A partial write no longer unregisters callbacks** (fixed here, noted for the
   record). `processControlChannel.write` used to truncate the last N pending
   callbacks on any write error, assuming nothing reached tmux; `io.WriteString`
   can report an error after delivering whole command lines, which tmux will
   still answer, shifting every later response one slot down the FIFO. The window
   widened from one callback to two when `SendPair` was added. Callbacks are now
   unregistered only when the write delivered zero bytes; on a partial write they
   are left in place and die with the channel. Pinned by
   `TestProcessControlChannelPartialWriteKeepsTheFIFOAligned`.

## 10. Verification

```text
go build ./...                             ok
go test ./...                              ok (all packages)
go test -race ./internal/tty/...           ok — internal/tty 31.5s, screenmodel 3.2s
go vet ./...                               ok
git diff --check                           clean
tmux integration set, 5 consecutive runs   ok — 15.7s/25.9s/16.0s/15.7s/15.8s, no flakes
```

The 5× sweep covered every tmux-touching test in the slice (the six model
scenarios, seed atomicity, and both paste tests). Two assertions were
mutation-checked rather than merely observed to pass: disabling the
`rawDuringMeta > 0` branch fails `TestControlModelSeedRaceIsDetectedAndReseeds`,
and pinning the straddle's post-seed tail to an unreachable number fails
`TestModelResizeReseedsAndLosesNoBytes`.

Every tmux invocation in the new tests carries an explicit `-S` inside the
test's own temp dir and runs with `TMUX` scrubbed; `newProcessControlChannelForSocket`
now scrubs `TMUX` too. The developer's default tmux server was verified intact
after the run.
