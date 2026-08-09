# Plan: Byte-fed tmux screen model (td-64c916)

**Task:** `td-64c916` — Plan byte-fed tmux screen model spike

**Research snapshot:** 2026-08-08

**Source:** [Lessons from Herdr](../../research/active/lessons-from-herdr.md),
suggested-sequencing item 3

## Decision first

Pursue the byte-fed screen model as a measured spike, then as a guarded
migration if it passes. Do not implement a terminal emulator inside Sidecar.
The best current candidate is Charm's new `github.com/charmbracelet/x/vt`
`Emulator`, pinned to an exact commit and wrapped behind a narrow
`internal/tty` adapter. It already owns the hard terminal state: main and
alternate screens, cursor and saved cursor, margins/origin mode, cell styles,
wide characters and graphemes, modes, and scrollback.

`x/ansi`, `x/cellbuf`, and `ultraviolet` alone are not that model. `x/ansi`
provides the sequence parser; `x/cellbuf` and `ultraviolet.TerminalScreen`
provide cell storage/output rendering. Sidecar would still have to implement
the VT state machine if it stopped there. The `x/vt` package appeared after the
original embedded-terminal decision and changes the build-versus-adopt choice.

Keep `capture-pane` authoritative throughout the spike. A shadow model consumes
the same `%output` bytes, and every resulting frame is compared with tmux's
rendered cells and metadata. Only after the decision gate passes should the
terminal panel become the first authoritative byte-fed surface. Workspace
agents and shells follow; file/notes inline editors follow only after the shared
path is proven.

If `x/vt` cannot pass common full-screen TUI, attach/restart, resize, and
resynchronization cases without a growing Sidecar patch layer, stop. Reopen the
Herdr replacement decision rather than turning Sidecar into the owner of a
terminal emulator.

## Phasing

The slices below group into two separately tracked phases:

- **Phase 1 — spike (Slices 0–2 + decision gate).** Everything up to and
  including the recorded go/no-go decision. No user-visible behavior changes;
  the only shipped product change is the tmux-owned paste prerequisite in
  Slice 1. Phase 1 ends when the decision gate outcome is written down and
  independently reviewed, whichever way it goes.
- **Phase 2 — guarded migration (Slices 3–5).** Exists only if the gate
  passes. Tracked as its own epic, created after the gate decision is
  recorded, so its scope can reflect what the spike actually learned.

## User journey and acceptance evidence

For a visible Sidecar terminal, output should feel like a native terminal while
retaining tmux's persistence and attach behavior:

1. A keypress or agent output reaches the visible grid from the `%output` delta;
   steady-state output does not trigger `capture-pane` or a cursor metadata
   query.
2. Cursor position, visibility, mouse mode, alternate-screen state, and future
   bracketed-paste transitions come from the same ordered byte stream as the
   cells, so a frame cannot combine content and metadata from different moments.
3. Vim-like full-screen TUIs, shell prompts, Unicode/wide text, colors, links,
   scrolling, search, selection, and lazy older history remain correct.
4. Switching to an already-running pane, restarting Sidecar, resizing, a paused
   control client, and a dead/restarted control connection produce a seeded or
   resynchronized frame—not a blank, duplicated, or corrupted terminal.
5. If the model cannot establish authority, the existing control-driven
   `capture-pane` path resumes automatically. The ordinary subprocess polling
   fallback still covers control-mode failure.
6. The byte-fed path is default-off until the fidelity gate and independent
   review pass. It can be disabled without changing or restarting tmux and never
   touches the default tmux server during tests.

Proof requires deterministic differential fixtures, isolated real tmux runs,
the real Sidecar viewport driven through `scripts/tmux-drive.sh`, performance
and allocation measurements, and independent review. Unit tests alone are not
enough.

## What exists now

The current architecture is already the right transport shape but stops one
step early:

```text
tmux pane bytes
    -> tmux -C %output / %extended-output
    -> controlEvent.Payload (octal escaped; decoded only on demand)
    -> per-pane dirty/coalesce state
    -> in-band display-message + capture-pane -p -e -S -600
    -> ControlSnapshot
    -> workspace mailbox
    -> OutputBuffer.UpdateSnapshot
    -> terminalViewportInput
    -> Sidecar frame + native cursor
```

Relevant current seams:

- `internal/tty/control_protocol.go` preserves the raw escaped payload and has
  the tested `DecodedPayload()` boundary needed by a model.
- `internal/tty/control_manager.go` pools one control client per visible tmux
  session, coalesces output by pane, and generation-guards subscriber delivery.
  It currently discards payload bytes and captures an authoritative snapshot.
- `internal/plugins/workspace/terminal_control.go` transfers ownership from
  fallback polling only after the first accepted control snapshot, then applies
  snapshots to agent, shell, and terminal-panel buffers.
- `internal/tty/output_buffer.go` and
  `internal/plugins/workspace/terminal_history.go` give the viewport absolute
  line coordinates, overlap-safe history prepends, search/selection stability,
  and lazy older `capture-pane` ranges. Preserve those user-facing contracts.
- `internal/plugins/workspace/terminal_viewport.go` is a pure renderer over the
  buffer and cursor metadata. The first migration should continue producing its
  existing input rather than rewriting the viewport at the same time.
- `internal/tty/tty.go` still polls for file-browser and notes inline editors.
  It is a later consumer of the proven model, not part of the first canary.

The accepted decision in
`docs/plans/implemented/embedded-terminal-transport-decisions.md` deliberately
made `capture-pane` authoritative because a newly attached client only sees
future bytes. This plan does not silently reverse that decision. It defines the
evidence needed to supersede it.

## New evidence since the Herdr research

### A candidate emulator now exists

As of this snapshot, `github.com/charmbracelet/x/vt` exposes an input-side
`Emulator` with `Write([]byte)`, `Render`, cursor/mode callbacks, alternate
screen, resize, cell access, and scrollback. The latest module is the untagged
pseudo-version:

```text
github.com/charmbracelet/x/vt
v0.0.0-20260803091719-3755ebad01b1
commit 3755ebad01b1366a9eeb5e4e80d664b404ab6eff
```

Its own suite passes locally. It is still a young, untagged dependency with a
small direct test corpus. It also declares an older `ultraviolet` pseudo-version
than Sidecar currently selects. The first slice must therefore test the exact
version under Sidecar's module graph; the package name or shared Charm origin is
not qualification by itself.

`github.com/charmbracelet/x/vttest` uses the same emulator internally. It is
useful for driving PTY applications and serializing snapshots, but it is not an
independent fidelity oracle for `x/vt`. Tmux remains the oracle in this spike.

### Tmux provides ordering but no output sequence number

The installed tmux is 3.6b. Control mode preserves stream order and never
inserts a notification inside a command response block. `%extended-output`
adds buffered age when `pause-after` is enabled, and `client_discarded` exposes
bytes dropped because a client fell behind. It does not attach a pane-output
sequence number that can prove continuity.

The current transport weakens that ordering: response callbacks run on the
reader goroutine while notifications are handed to a separate event channel.
For dirty flags that is fine. For a byte-fed model it can race the seed capture
against bytes around the capture boundary. Establishing one ordered delivery
actor and proving the seed barrier is a prerequisite, not an implementation
detail.

### Mid-stream attach is the central risk

Sidecar keeps control clients only for visible terminal consumers. That is good
resource behavior and should remain true, but it means every pane switch and
every Sidecar restart attaches after the application has already emitted state.
Tmux can provide rendered cells, cursor, geometry, history size, alternate
screen, and mouse flags. It cannot reconstruct every hidden VT state, including
the active scrolling margins, saved cursor/attributes, parser partial state,
and every private mode.

The spike therefore needs to demonstrate that a rendered seed plus known tmux
metadata converges for Sidecar's real applications. It must not claim that a
capture magically reconstructs an emulator. Common, persistent divergence here
is the primary no-go signal.

## Proposed architecture

### 1. A Sidecar-owned pane model adapter

Add a small internal package such as `internal/tty/screenmodel`. It owns the
pinned emulator and is the only package allowed to import `x/vt`. Do not leak
`vt.Emulator`, `uv.Cell`, or dependency-specific mode types into the workspace
plugin.

The adapter contract is behavior-shaped:

```go
type Seed struct {
    Output        string // bounded capture-pane -e snapshot
    CaptureBase   int
    HistorySize   int
    Width, Height int
    CursorRow     int
    CursorCol     int
    CursorVisible bool
    AltScreen     bool
    Mouse         MouseState
}

type Frame struct {
    Output        string // ANSI-rendered loaded history + live pane rows
    CaptureBase   int
    HistorySize   int
    Width, Height int
    CursorRow     int
    CursorCol     int
    CursorVisible bool
    AltScreen     bool
    Mouse         MouseState
}

type PaneModel interface {
    Seed(Seed) error
    Write([]byte) error
    Resize(width, height int) error
    Frame() (Frame, error)
    Close()
}
```

The concrete adapter may refine this shape during the first slice, but it must
preserve three properties:

- one goroutine/actor owns each emulator; no concurrent `Write`, resize, or
  frame read;
- frame output can still populate `ControlSnapshot` and `OutputBuffer`, keeping
  the current viewport/history/search/selection journey intact;
- the dependency can be replaced or removed without changing workspace code.

Seed the visible cell grid and the bounded loaded history separately. Track the
absolute tmux `history_size` independently of retained emulator lines so lazy
older history keeps using tmux coordinates. When the model scrolls a row off the
main screen, append it to the loaded history and advance the absolute history
count. Keep the existing `tmuxCaptureMaxBytes` presentation cap and
`OutputBuffer` overlap rules until the separate byte-bounded scrollback work is
designed; do not smuggle a second history system into this spike.

### 2. An ordered control-stream barrier

Refactor `processControlChannel` so command responses and notifications enter
one ordered stream (or carry a monotonic receive ordinal consumed by one actor).
FIFO command callbacks may remain an internal convenience, but they must not
bypass pane-byte ordering.

For a new visible subscription:

1. Attach the control client and start buffering pane output events.
2. Issue one seed transaction containing metadata and a bounded rendered
   capture. Add `alternate_on`, individual mouse flags, and
   `client_discarded` to the metadata already captured.
3. Establish, with an isolated tmux test, which events are before and after the
   completed capture response. Discard only bytes proven included in the seed;
   replay every byte proven after it, in receive order.
4. Publish the first model frame only after seeding and replay finish. Until
   then, keep polling/capture ownership exactly as today.

The protocol test must continuously write uniquely numbered lines while the
seed command runs, repeat under `pause-after`, and prove no number is duplicated
or omitted. If tmux's command/notification boundary cannot provide a reliable
cut, the spike stops before any UI rollout.

### 3. Explicit resynchronization states

Each pane model has a simple lifecycle:

```text
capturing seed -> shadow/live -> resync required -> capturing seed
                         \-> control failed -> existing polling fallback
```

Require a fresh seed on:

- first subscribe, Sidecar restart, or pane switch;
- control-client reconnect or nonzero `client_discarded` growth;
- `%pause`/`%continue` until the no-loss behavior is proven by the harness;
- pane identity change;
- resize/layout change in the first implementation.

Resize is intentionally conservative at first. Resize tmux, wait for the
authoritative resulting geometry, then reseed the emulator. Pure in-model
resize can replace this only after differential tests cover wrapping,
alternate-screen applications, and shared-pane geometry ownership.

Malformed payloads, parser errors, impossible dimensions, or model panics must
invalidate only that pane model and return the consumer to capture/polling.
Never let an emulator failure kill the control-client reader or Bubble Tea
loop.

### 4. Let tmux own bracketed paste at the input seam

Initial bracketed-paste state is not available as a tmux format and cannot be
reconstructed from `capture-pane -e`. Do not make correct paste depend on a
mid-stream model knowing that hidden mode.

As a small prerequisite, change the shared paste adapter
(`internal/tty/paste.go`, which today wraps bracket sequences manually around
a `load-buffer`/`paste-buffer` pair on the unnamed buffer) to use a uniquely
named tmux buffer and `paste-buffer -p -d -t <pane>`. Tmux already knows
whether the pane requested bracketed paste and inserts the control codes only
when appropriate. This also removes manual three-command bracket wrapping and
avoids using the server-global unnamed buffer. Keep mode reporting from the
byte model for UI/diagnostics, but not as the correctness authority for paste.

This is tmux-owned input behavior in `internal/tty`, not workspace business
logic.

### 5. Shadow comparison before authority

Add a diagnostic shadow mode, enabled only by an explicit environment variable
such as `SIDECAR_TMUX_SCREEN_COMPARE=1`. In shadow mode:

- `capture-pane` remains the delivered frame;
- `%output` also advances the model;
- every coalesced capture compares canonical cells, style/link attributes,
  cursor, dimensions, alternate-screen state, mouse modes, and loaded history;
- diagnostics record counts, dimensions, sequence classes, and mismatch
  coordinates, never raw terminal text or OSC payloads.

Do not make shadow mode a permanent public renderer. It exists to produce the
decision evidence and may remain only as an opt-in diagnostic after rollout.

The eventual authority flag is one normal default-off feature,
`tmux_byte_screen`, registered in the existing `internal/features` registry
alongside `tmux_interactive_input` and `tmux_inline_edit`. The first enabled surface is the terminal panel. A model
frame continues through `ControlSnapshot`, so the terminal panel canary tests
the new transport/model without simultaneously replacing the workspace
mailbox, output buffer, viewport, selection, or search.

## Fidelity harness

### Deterministic byte corpus

Record raw byte fixtures and expected tmux results from an isolated tmux server.
Store only generated, non-personal fixtures. For every fixture, replay the same
bytes as one write and at every meaningful split boundary to catch partial CSI,
OSC, UTF-8, and grapheme state.

Cover at minimum:

- CR/LF, tabs, backspace, autowrap, phantom final-column cursor;
- relative and absolute cursor motion, save/restore cursor and attributes;
- erase/insert/delete cell and line operations;
- full and partial scroll regions with origin mode;
- main/alternate-screen entry, exit, and repeated transitions;
- SGR reset, 16/256/truecolor, underline styles, inverse, dim, hidden;
- OSC 8 links and hostile/nested OSC termination forms already covered by the
  Sidecar sanitizer oracle;
- ASCII, combining marks, CJK wide cells, emoji clusters, variation selectors,
  and split UTF-8;
- mouse modes/encodings, cursor visibility/style, bracketed paste,
  synchronized output, and terminal reset;
- resize wider/narrower and taller/shorter while output is active;
- control payload octal escapes, long notifications, pause/continue, and a dead
  control connection.

Compare canonical cell values rather than rendered string spelling: grapheme,
cell width, foreground/background/underline color, attributes, and hyperlink.
Cursor and modes are separate assertions. Tmux's `capture-pane -e` plus format
metadata is the independent oracle; `x/vttest` is not.

### Real application matrix

Run common applications against isolated tmux and compare for their entire
interaction, not one final screenshot:

- zsh prompt editing, multiline commands, completion, and long wrapped output;
- Sidecar-supported agent TUIs that are safely available, including idle,
  streaming, approval/input, interrupt, and completion transitions;
- vim or nvim editing, splits, search, and resize;
- `less`, a continuously updating program, and at least one mouse-aware TUI;
- enter/exit alternate screen repeatedly, then inspect restored shell history;
- attach mid-session, switch away/back, restart Sidecar's model, and reconnect
  after a forced control-client failure.

Availability must be recorded rather than silently substituting synthetic
programs. Real Sidecar proof uses `scripts/tmux-drive.sh`; first run `paths` and
confirm both the tmux socket and state/config tree are isolated.

## Implementation sequence

### Slice 0 — qualify the dependency and oracle

- Pin `x/vt` at the researched commit in a temporary implementation branch and
  verify it under Sidecar's selected `ultraviolet`, Go version, race suite, and
  licenses.
- Build the Sidecar-owned adapter with no UI consumer.
- Add the generated tmux fixture recorder and canonical frame comparator.
- Run the deterministic corpus against the adapter, including split-boundary
  replay.
- Record missing sequences/API limitations. Prefer an upstream contribution or
  a newer pinned commit for isolated defects; do not vendor or fork the whole
  emulator.

**Exit:** the ordinary byte corpus is exact, dependency integration is clean,
and any remaining gaps are bounded enough to test in the live protocol. A broad
or unstable patch layer is a no-go.

### Slice 1 — prove ordered bootstrap and resync

- Serialize control notifications and responses through one ordering boundary.
- Add pane-scoped raw delivery with subscription generation and activation
  identity carried end to end.
- Implement seed capture, event barrier, post-seed replay, conservative resize
  reseed, discard detection, and fallback.
- Add isolated integration tests for continuous numbered output around each
  boundary.
- Move paste to pane-targeted `paste-buffer -p -d` with a unique buffer.

**Exit:** no duplicated/missing bytes across attach, resize, pause, reconnect,
unsubscribe, or generation replacement; existing capture delivery is unchanged.

### Slice 2 — shadow the actual workspace journey

- Feed models for the currently visible terminal panel and primary workspace
  pane while continuing to deliver capture snapshots.
- Add privacy-safe mismatch/performance counters and a one-command evidence
  report.
- Exercise the deterministic and real application matrices, including the
  current `OutputBuffer`, history, search, selection, viewport, native cursor,
  mouse forwarding, and agent-activity consumers.
- Fix adapter/ordering defects only when the behavior is part of the declared
  corpus; do not grow application-specific escape repairs.

**Exit:** the decision evidence below is complete. Shadow code has not changed
what the user sees.

### Decision gate

Proceed only when all are true:

- deterministic fixtures have zero cell, attribute, cursor, mode, history, or
  split-boundary mismatches;
- common real applications have zero unexplained steady-state mismatches and no
  persistent mismatch after supported seed/resync events;
- attach/restart can converge without injecting keystrokes, signals, or fake
  resizes into the application;
- the ordered barrier and `client_discarded` handling prove no duplicated or
  omitted bytes under sustained output and `pause-after`;
- steady-state model delivery performs zero `capture-pane` and
  `display-message` commands per output burst; one seed/resync transaction is
  the expected exception;
- replay work and allocations scale with the byte delta/current grid, not the
  600-line capture window, and total memory remains bounded under a sustained
  output soak;
- end-to-end output latency and CPU improve over the existing in-band capture
  baseline on this Mac, with no startup or idle regression;
- removing capture authority will eventually delete mouse-fragment regexes,
  cursor/mouse metadata queries, and capture-on-burst code rather than leaving
  two permanent renderers.

If the gate fails because mid-stream state cannot converge or x/vt requires a
large fork, document the failing fixtures and reopen the Herdr plan. If it fails
for a bounded set of named defects with identified remedies — whether upstream
in the emulator or in Sidecar's own adapter and seed logic — hold at the gate
until each has a pinned fix.

The reopen trigger is the operative test, not the shape of the failure. The
original wording here anticipated "one narrow, upstreamable emulator defect";
the actual Phase 1 outcome was a bounded set of defects whose sharpest member is
in-repo. That is still a hold, because neither half of the reopen trigger fired.

Criterion 1 above is read as **zero unexplained mismatches** — every remaining
deterministic mismatch must be a named defect with a tracked remedy, or a
documented by-design difference between tmux and the emulator (tmux reflows on
resize; the emulator truncates). It is not read as requiring an empty gap list,
which no pinned dependency could satisfy.

### Decision gate outcome — HOLD AT THE GATE (recorded 2026-08-08, `td-2bed64`)

**Outcome: hold. Do not adopt, do not reopen Herdr.** Phase 2 (Slices 3–5) does
not start. `capture-pane` remains authoritative; shadow comparison stays
`SIDECAR_TMUX_SCREEN_COMPARE=1`-only. The hold is tracked as its own epic,
**`td-b7aa77`**, whose four children are the preconditions listed below.

Evidence judged: [slice 0](./td-64c916-byte-fed-tmux-screen-model-slice0-evidence.md),
[slice 1](./td-64c916-byte-fed-tmux-screen-model-slice1-evidence.md),
[slice 2](./td-64c916-byte-fed-tmux-screen-model-slice2-evidence.md), and the
implementation in `internal/tty/screenmodel/`, `internal/tty/control_model.go`,
`internal/tty/screencompare.go` (commits `2369a6d` … `325d17c`).

#### Per-criterion verdict

| # | Criterion | Verdict | Evidence |
| --- | --- | --- | --- |
| 1 | Zero deterministic-fixture mismatches | **FAIL as written** | slice 0 §4–§5 (GAP-1…GAP-9 still present upstream), slice 2 §3. 24 fixtures pass whole, at every split boundary, byte-at-a-time, through the seed round trip and through `Frame.Output`; every remaining mismatch is a named upstream defect with a minimal reproducer and no new one appeared. That is "no *unexplained* mismatches", not "zero mismatches". GAP-9 (grapheme cluster across a `Write`, which also mis-places the cursor) blocks authority on its own. |
| 2 | Zero unexplained steady-state real-application mismatches; no persistent mismatch after supported seed/resync | **FAIL** | slice 2 §4.2, §4.4, §4.8, §6.1. 127 unexplained cells over 295 comparisons; 59 of 295 comparisons carried a mismatch; 55 further cells of a known Sidecar defect (alt-screen frame history) in 5 of 8 scenarios. The second half is exactly what §4.2 fails. |
| 3 | Attach/restart converge without injected keystrokes, signals or fake resizes | **FAIL** | slice 2 §4.2 and `TestAltScreenAttachCannotRestoreTheMainScreen` (reproduced independently at this gate on an isolated socket: 2 comparisons, 0 clean, `cell/grapheme` 98). Attach on the **main** screen converges cleanly — 182/182 clean including a full drop-and-recreate of the subscription. Any seed or resync taken while `alternate_on=1` never recovers the main screen. |
| 4 | Ordered barrier and `client_discarded` prove no duplicated or omitted bytes under sustained output and `pause-after` | **PASS** | slice 1 §1.1–§1.2 (six scenarios, four with the straddle assertion, two mutation-checked), slice 2 §6.3 (131 605 bursts / 38.6 MB, `discarded_bytes = 0`, `seed_races = 0`, zero open discard windows, zero capture-metadata races). Caveat recorded, not disqualifying: the tmux ordering guarantee is empirical on 3.6b/darwin-arm64, and the pause row proves correct recovery rather than replay. |
| 5 | Zero `capture-pane`/`display-message` per output burst in steady state | **PASS** | slice 2 §6.1 "Commands". All 295 capture transactions ran while a live model already held the same screen; the irreducible remainder is 10 seed transactions plus a 1 s `client_discarded` cadence probe, which is not per burst. |
| 6 | Work/allocations scale with the byte delta, not the 600-line window; memory bounded under soak | **FAIL on work; NOT PROVEN on memory** | slice 2 §4.5, §6.3, §7.10. `Write` scales with the delta; `Frame()` scales with the capture window (861 µs, 470 KB per frame at 600 lines). The flat memory series is `Model.Footprint()`, bounded by construction by the emulator's scrollback cap and provably blind to the per-reseed emulator + 4 MB parser-buffer leak this spike found. A heap profile is required. |
| 7 | Latency and CPU improve over the in-band capture baseline, no startup/idle regression | **NOT PROVEN** | slice 2 §6.4, §7.1, §7.9. CPU is unmeasurable while capture is authoritative. Interactive latency is at **parity** (13 710 µs vs 13 822 µs; 13 279 vs 13 219 in the real app), against a baseline the diagnostic itself inflates. The ~4× holds only for the 38 MB soak. No startup or idle path is touched with the variable unset. |
| 8 | Removing capture authority will delete the old heuristics rather than leave two renderers | **PARTIAL** | slice 2 §4.4–§4.5. The model already supplies cursor, mouse modes and alt-screen state in the same ordered stream, so the per-burst `display-message` becomes deletable. But the alt-screen history shape and the non-incremental frame mean the model's frame is not a drop-in for `ControlSnapshot.Output` today — i.e. a second renderer, which is what this criterion excludes. |

**Tally: 2 pass (4, 5), 1 partial (8), 1 not proven (7), 4 fail (1, 2, 3, 6).**
This is the slice-2 §5 scoring, re-derived independently at the gate rather than
adopted; it stands.

#### Resolving the tension: a convergence failure that is not "cannot converge"

Criterion 3 fails at the place this plan named as the primary no-go signal, and
the plan's reopen trigger is *"mid-stream state cannot converge or x/vt requires
a large fork"*. Both halves of that trigger were tested against the code, not
assumed:

- **The state can converge.** While `alternate_on=1`, tmux hands over *both*
  grids: `capture-pane -a` returns the saved main screen and plain
  `capture-pane` returns the alternate one. That is measured on tmux 3.6b and
  asserted in `TestAltScreenAttachCannotRestoreTheMainScreen`, and it was
  re-run at this gate. Nothing is missing from tmux; Sidecar's seed transaction
  simply never asks for the main screen.
- **The defect is Sidecar's, not x/vt's.** `seedFromResponses`
  (`internal/tty/control_model.go:677`) builds the seed from a single
  `capture-pane`, and `screenmodel.Model.Seed`
  (`internal/tty/screenmodel/model.go:281`) writes `ESC[?1049h` and then paints
  that grid — leaving the emulator's **main** screen empty. x/vt keeps a correct
  main screen: the slice-0 fixtures `alt_screen_active` and
  `alt_screen_transitions` are exact whole, at every split boundary,
  byte-at-a-time *and* through the seed round trip with `alternate_on=1`. The
  main screen is empty only because Sidecar never wrote one. The fix is in-repo:
  a second capture in the seed transaction, one new `Seed` field, and seeding
  main before switching to alternate.
- **No fork, no growing patch layer.** 131 non-test lines in the adapter, zero
  application-specific escape repairs in `internal/tty/screenmodel`, one pinned
  upstream module, and the one upstream sharp edge that was hit (GAP-10, `x/vt`
  blocking forever on the first device query) was fixed cleanly at the adapter
  seam by draining the emulator's reply stream — honouring its `io.ReadWriter`
  contract, not repairing an application's escapes.

So the failure is a *convergence bug*, not an *inability to converge*, and the
reopen branch does not apply. Equally, four failing criteria — one of them the
central risk — cannot be adopted. Hold is the correct branch.

One honest imprecision in this plan's own text, recorded rather than papered
over: the hold branch is written as *"one narrow, upstreamable emulator
defect"*, and this hold does not match that wording. The sharpest defect is
narrow but **Sidecar-side and in-repo**, and there are further named blockers
(upstream GAP-9/GAP-7/GAP-3/GAP-4/GAP-6; in-repo absolute-history drift,
alt-screen frame history, non-incremental frames) plus missing evidence. That
makes the hold *broader* than the plan anticipated, but it does not move it
toward reopening: every item is named, bounded and has an identified remedy, and
the discriminator the plan actually reasons from — convergence and fork size —
points the other way. Treat the branch wording as "hold until the named,
bounded blockers are closed", and this section as the correction to it.

#### Hold conditions (epic `td-b7aa77`)

Phase 2 must not start until all of these are closed:

1. **`td-744b34`** — seed the main screen from `capture-pane -a` whenever
   `alternate_on=1`, on **every** seed and resync path. The trigger observed in
   practice was the routine, plan-sanctioned **resize** reseed, not mid-stream
   attach: `editor-nvim-or-vim` (6 seeds) diverged by 126 cells where
   `editor-no-resync-control` (same edits, 0 seeds) was clean.
   `TestAltScreenAttachCannotRestoreTheMainScreen` must then be rewritten to
   assert convergence.
2. **`td-3c0696`** — repin `x/vt` to a commit fixing GAP-9 (with a stated flush
   policy the adapter honours), GAP-7, GAP-3/GAP-4 and GAP-6. **GAP-9 is still
   unexercised live** — the classifier cannot separate it from GAP-6 — and needs
   a deliberate test of a cluster split across an `%output` boundary. No
   vendoring, no fork.
3. **`td-09fc80`** — fix absolute history tracking past the emulator scrollback
   cap, decide the alternate-screen frame's history shape, and make frame
   publication incremental (criteria 2, 6, 8).
4. **`td-2d167d`** — produce the missing evidence: the **agent-TUI row, which
   was never run** (it needs real credentials and network; under the harness's
   isolated `HOME` it would exercise an authentication screen, so it was
   correctly not attempted and no synthetic program was substituted — the
   credentialed-run decision is outstanding); **CPU**, which is unmeasurable
   while capture is authoritative; **interactive latency re-measured with the
   shadow comparison disabled**, since the diagnostic inflates the baseline it
   is compared against; and a **heap profile** in place of `Model.Footprint()`.
5. **`td-58cebc`** — control-client silent freeze on a saturated event queue is
   **unfixed**, is reachable whenever shadow mode is on, and would be reachable
   on the user path under authority. The epic depends on it.

#### No experimental renderer on the user path

Verified from the code at this gate, not assumed:

- `SIDECAR_TMUX_SCREEN_COMPARE` is read only in `internal/tty/screencompare.go`;
  it is not a `internal/features` flag and does not appear in config.
- **No `tmux_byte_screen` flag exists** anywhere in the tree; the registry still
  holds only `tmux_interactive_input`, `tmux_inline_edit`, `files_auto_refresh`
  and `notes_plugin`.
- **Nothing outside `internal/tty` sets `OnModelFrame`** — a tree-wide sweep
  returns no hits outside that package, so `wantsModelFeed`
  (`internal/tty/control_manager.go:679`) is false by default: no model is
  built, no seed transaction is issued, and the `%output` payload is never
  decoded.
- `TestCaptureCommandsUnchangedWhenCompareOff` asserts the pre-slice-2 command
  strings character for character, and
  `TestCaptureDeliveryUnchangedWhenModelPathOff` asserts the delivered
  `ControlSnapshot` as an exact struct value.
- `internal/plugins/workspace/terminal_control.go` is unmodified by the spike.
  The only shipped user-visible change is the plan's tmux-owned paste
  prerequisite (`internal/tty/paste.go`, `tty.go`, `workspace/interactive.go`)
  plus the `refresh-client -A '%N:continue'` quoting fix — both intended
  Slice 1 product changes, neither a renderer.

### Slice 3 — terminal-panel canary

- Register `tmux_byte_screen`, default false.
- When enabled, make only the terminal panel accept model frames as authority.
- Retain seed/resync capture and the current polling fallback.
- Prove live input, paste, mouse, scroll/search/selection, attach, panel resize,
  hide/show, and control failure in the real isolated app.
- Independently review the transport ordering, fallback, memory bounds, and
  default-off behavior.

### Slice 4 — workspace agents and shells

- Enable the same authoritative path for the primary visible agent/shell pane.
- Preserve `ControlSnapshot` semantic metadata needed by `agentactivity`; do
  not make the terminal model infer agent state.
- Switch activity detection to the model's live bottom while keeping pane title
  and current command from tmux metadata at seed/resync or a cheap independent
  status cadence. Do not reintroduce a full capture to refresh those fields.
- Prove List/Interactive/Kanban status, unseen-done semantics, shell parity,
  selection/search/history, and shared-geometry clipping.
- After an evidence window with the flag enabled locally, make the flag default
  true only through a separately reviewed change.

### Slice 5 — finish the shared tmux integration

- Move the proven pane-model service to a lifecycle shared by `tty.Model`
  consumers without making it global mutable UI state.
- Migrate file-browser and notes inline editors from polling to the same
  subscribe/seed/model/fallback contract.
- Delete capture-on-burst, synthetic terminal-mode detection, mouse-fragment
  cleanup, and redundant cursor/mouse side queries only after every consumer is
  migrated and fallback tests still pass.
- Keep `capture-pane` for seed, explicit resync, lazy older history, diagnostics,
  and control-mode fallback. It is no longer the steady-state renderer.
- Update `embedded-terminal-transport-decisions.md`, the shell-integration skill,
  headless-testing guidance, config/feature documentation, and diagnostics to
  state the new authority and rollback.

## Test and proof matrix

| Layer | Required evidence |
| --- | --- |
| Parser/model | Generated corpus; every split boundary; canonical cells/styles/links/cursor/modes; fuzz malformed and nested control strings |
| Protocol | Ordered response/notification barrier; numbered continuous output; pause/continue; discard; reconnect; stale generation; close drains delivery |
| Seed/resync | Existing shell/TUI/agent; main and alt screen; non-default margins; saved cursor; resize; Sidecar restart; pane switch |
| History | Absolute base/history size; scroll-off append; byte trimming; lazy older range; delayed prepend overlap; search and selection coordinates |
| Input | Literal keys, enhanced keys, multiline paste via `paste-buffer -p`, mouse modes, shift-drag escape hatch, attach/detach |
| Performance | Bytes/event, model write/render time, allocations, retained memory, captures/metadata queries, output-to-frame latency, sustained-output CPU |
| Real UI | Terminal panel first, then agent and shell; native cursor; colors/Unicode; geometry lease owner/non-owner; app blurred; modal suppression |
| Failure | Unsupported tmux/control start failure, malformed payload, model error/panic, killed control client, deleted pane/session, flag disabled |

Run focused tests first, then `go test ./...`, `go build ./...`, race tests for the
new adapter/control packages, and `git diff --check`. All tmux integration tests
must use a unique `-L`/`-S` server and clean up only their own sessions. The real
Sidecar proof must isolate both tmux and the Sidecar state tree exactly as
documented in `AGENTS.md`.

## Rollback and observability

- The public rollout control is `tmux_byte_screen`. Disabling it returns the
  next subscription to current capture authority; it does not restart or alter
  tmux.
- A pane-local model fault falls back immediately and records one rate-limited
  diagnostic with pane identity, lifecycle state, byte count, and error class.
  Never log terminal contents or decoded OSC data.
- Expose diagnostic counters for model seeds, resync reasons, raw bytes,
  rendered frames, captures avoided, mismatches in compare mode, discarded
  bytes, fallbacks, and model memory.
- Keep the existing control/poll fallback until every consumer has migrated and
  the byte-fed path has survived a local evidence window. Removing fallback is
  not part of this plan.

## Explicit non-goals

- Replacing tmux, changing the default tmux server, or deleting geometry leases.
- Writing or vendoring a Sidecar terminal emulator.
- Persisting emulator internals across Sidecar restarts; seed/resync is the
  restart contract.
- Rewriting the terminal viewport, search, selection, link handling, or agent
  activity model during the transport spike.
- Making background/invisible panes keep one control client each merely to
  avoid seeding on the next view.
- Building a Sidecar CLI/API for terminal presentation behavior; tmux already
  owns the headless capability.

## References

- [Charm `x/vt` source](https://github.com/charmbracelet/x/tree/main/vt)
- [`x/vt` package API](https://pkg.go.dev/github.com/charmbracelet/x/vt)
- [Charm `x/vttest` source](https://github.com/charmbracelet/x/tree/main/vttest)
- Local tmux 3.6b `CONTROL MODE`, `refresh-client`, and `paste-buffer`
  documentation (`man tmux`), checked 2026-08-08

## Final completion criteria

The work is complete when the decision gate has been recorded and one of two
outcomes is independently reviewed:

1. **Adopt:** all tmux-backed Sidecar terminal consumers use ordered `%output`
   bytes for steady-state rendering, with `capture-pane` limited to
   seed/resync/history/fallback, the old heuristics are removed, real isolated
   proof passes, and rollback is documented; or
2. **Do not adopt:** the failing fidelity/bootstrap evidence is durable, no
   experimental renderer remains on the user path, and the Herdr replacement
   decision is reopened with the concrete failure as its leading rationale.

**Status:** neither outcome was reached. The recorded gate decision is the third
branch — **hold**, see "Decision gate outcome" above. Phase 1 ends there; the
plan is complete only when the hold conditions (`td-b7aa77`) close and the gate
is re-adjudicated.
