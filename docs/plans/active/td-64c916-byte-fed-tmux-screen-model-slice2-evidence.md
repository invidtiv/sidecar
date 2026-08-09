# Slice 2 evidence: shadow comparison of the real workspace journey

**Task:** `td-b06668` — Slice 2 of the byte-fed tmux screen model spike
(`td-64c916`).

**Date:** 2026-08-08 · **tmux:** 3.6b · **Platform:** darwin/arm64 (Apple
silicon) · **Go:** 1.26.5

**Revision note.** This document was corrected after an independent review of
the first draft. The review found that the application matrix asserted nothing,
never verified that its programs had launched, and shared one model and one
subscription across every scenario — so no scenario's numbers described only
that scenario. It also found that the interactive latency measurement the
harness produced had been omitted in favour of the soak, that two mismatch
classes acted as catch-alls, and that the gate tally over-counted passes. The
harness has been changed and rerun; §4.2, §5, §6 and §7 are the corrected
results, and they are in several places **worse** than the first draft claimed
and in one place (the size of the unexplained total) **better**. Where a number
moved, the reason is stated.

**Reproduce everything in this document with one command:**

```bash
./scripts/screen-compare-evidence.sh /tmp/sidecar-screen-compare
```

## Verdict

**HOLD AT THE GATE — do not proceed to slice 3 yet.** By the honest scoring in
§5 the eight decision-gate criteria come out **two passes, one partial, one not
proven, and four fails**. One of the fails is in the exact place the plan named
as the primary no-go signal: a pane whose model is seeded or resynchronized
while an application owns the alternate screen never recovers its main screen.

None of the failures is "x/vt cannot do this". All have a bounded, named remedy,
and one of those remedies was measured to be available in tmux itself during
this slice. The plan's no-go trigger — *"mid-stream state cannot converge or
x/vt requires a large fork"* — is **not** met: there is no growing Sidecar patch
layer (zero application-specific escape repairs) and no fork. So the correct
outcome is the plan's third option — **hold at the gate until the named fixes
and the missing evidence exist** — not "adopt" and not "reopen Herdr". §9 states
what must be true before slice 3 starts.

What went right is substantial and should not be lost in the failures:

- **Steady-state delivery would issue zero `capture-pane` and zero
  `display-message` commands per output burst.** Every one of the 295 capture
  transactions in the application matrix ran while a live model already held the
  same screen; the only irreducible remainder is 10 seed transactions.
- **Retained model memory was flat** across a 25 s / 38.6 MB soak — though see
  §5 criterion 6 and §7.10 for why "flat" is not the same as "bounded", and why
  this is scored NOT PROVEN rather than PASS.
- **Zero model faults, zero discarded bytes, zero seed races, zero
  capture-metadata races and zero open discard windows** across 295 matrix
  comparisons plus 224 soak comparisons plus a real Sidecar run.
- A **new upstream defect (GAP-10) that would have hard-frozen the user's
  terminal in slice 3** was found and fixed on Sidecar's side of the seam.

What went wrong is equally concrete and is set out in §4. The **latency** result
is more modest than the first draft said: on the interactive matrix the byte-fed
path is at **parity** with the capture baseline, not ahead of it (§6.4).

Shadow code changed nothing the user sees; §6 proves it rather than asserting it.

---

## 1. What was built

### 1.1 Shadow mode, environment variable only

`SIDECAR_TMUX_SCREEN_COMPARE=1` (`internal/tty/screencompare.go`). It is not a
feature flag, is not registered in `internal/features`, and does not appear in
config. With it unset — the default — `ScreenCompareEnabled()` is false and:

- `wantsModelFeed` is false for every subscription (nothing in the workspace
  sets `OnModelFrame`), so no pane model is built, no seed transaction is issued,
  and the `%output` payload is never decoded;
- `buildControlCaptureCommands` emits the **exact** pre-slice-2 command strings,
  asserted character for character by
  `TestCaptureCommandsUnchangedWhenCompareOff`;
- every counter update is behind a nil-map/zero-cost path.

With it set, the control client builds a pane model for **every** visible
subscription. That is deliberately done in `internal/tty` rather than in the
workspace plugin, so **`internal/plugins/workspace/terminal_control.go` is not
modified by this slice at all** — the strongest available form of "the user path
is untouched". The two subscriptions the workspace creates are exactly the two
the plan asks for: the terminal panel (`workspaceControlPanel`) and the primary
workspace pane (`workspaceControlPrimary`).

### 1.2 The comparison point, and why it is exact

The comparison runs inside `captureFinished`, in the capture response callback,
on the control client's single ordered actor. Slice 1 moved every command
response onto that actor precisely so a response occupies its true position in
the byte stream. Therefore **at the instant the capture response is processed,
the model has consumed exactly the bytes preceding that capture and none of the
bytes following it**. The two sides describe the same moment by construction, so
a difference is a fidelity result and not skew. No sleep, retry, settle loop or
tolerance is used anywhere in the comparison.

### 1.3 What is compared

`compareCaptureWithModel` compares, per the plan: canonical cells (grapheme,
width, fg/bg/underline colour, underline style, attribute bitmask, OSC 8 URL and
params), cursor position and visibility, dimensions, alternate-screen state,
mouse modes, and loaded history. It reuses slice 0's `screenmodel.CompareGrids` /
`CompareCursor` / `CompareModes` and **never compares rendered string spelling**.

The capture side is decoded by slice 0's hand-written `DecodeCapture`, which
shares no code with `x/vt` — it does its own escape scanning, its own SGR
interpretation, and uses `rivo/uniseg` rather than the tables `x/ansi` uses. It
moved from `capture_oracle_test.go` to `capture_decode.go` unchanged so the
corpus and the live comparison use one decoder with one set of assumptions.
The model side uses the canonical grid the model already produced, so a defect in
the model's *renderer* cannot cancel itself out.

Not compared, with reasons: **bracketed paste** (tmux exposes no format, and per
plan §4 tmux owns paste correctness) and **DECSET 9 / 1001 mouse modes** (no tmux
format; recorded as a seed gap in slice 1).

### 1.4 No extra tmux command

Shadow mode needs `alternate_on`, `mouse_sgr_flag` and `client_discarded`, none
of which the capture path asked for. Those three fields were added **to the
existing `display-message`**, not as a fourth command
(`captureCompareMetadataFields`). A diagnostic that added a command per burst
would have corrupted the very per-burst command count it exists to produce.
`TestBothMetadataLayoutsProduceTheSameSnapshot` asserts the two layouts yield an
identical `ControlSnapshot` for the same pane, including a pane title containing
a comma.

### 1.5 Privacy

Every recorded value is a count, a dimension, a coordinate, or a fixed class
name. The gap classifier inspects cell values to choose a class, but only the
class name leaves the function. `TestReportAndJSONNeverCarryTerminalText` paints
a screen containing a secret string and an OSC 8 URL, forces mismatches, and
asserts neither the JSON nor the markdown report contains any of it. The real
Sidecar run's report (§6.2) was independently grepped for the pane's content,
the host name and the home directory: **zero hits**.

**Narrowed claim.** The guarantee above covers *terminal content*: no pane text,
no OSC payload, no captured line. It is not a blanket claim that the reports
contain nothing personal. The matrix's application-availability table records
where each program resolved, and two of them live under the developer's home
directory. The first draft of this document published those absolute paths, and
therefore the developer's account name. The harness now rewrites any path under
`$HOME` to a `~`-relative one before it reaches the report
(`redactHome`), and this document quotes the redacted form. That is a
narrowing of the claim, not a new guarantee: a future field that is neither a
count nor a class name needs the same scrutiny.

Counters implemented, matching the plan's "Rollback and observability" list:
model seeds, resync reasons, raw bytes, rendered frames, captures avoided,
compare-mode mismatches, discarded bytes, fallbacks, and model memory — plus
seed races, faults, metadata queries, per-path latency, the "uncomparable" count
described in §3.1, the compared-cell denominator it must be read against, and a
skipped-comparison counter (§7.13).

### 1.6 One-command evidence report

`scripts/screen-compare-evidence.sh` runs the deterministic corpus, the shadow
unit suite, the real application matrix, the alternate-screen attach finding, the
sustained-output soak, and the per-burst benchmarks, and writes a consolidated
markdown report. In a live Sidecar, `SIDECAR_TMUX_SCREEN_COMPARE_REPORT=<path>`
writes the JSON counters every 25 comparisons and on manager stop (periodic,
because a headless proof run is killed rather than shut down cleanly).

### 1.7 The matrix asserts, and verifies that its programs ran

Two structural defects in the first draft's harness are fixed, because both made
the evidence weaker than it appeared:

- **Launch verification.** Programs were typed as bare shell text and nothing
  checked that they started. A missing, broken or instantly-exiting program
  would have produced a scenario that compared two shell prompts and reported
  itself clean. Every scenario now blocks on `pane_current_command` becoming the
  expected program (`compareTmux.waitForCommand`), fails loudly otherwise, and
  full-screen rows additionally assert tmux's own `alternate_on`. This caught
  two real defects on the first run: `seq 1 500 | less` reports the *shell* as
  the pane's foreground command, so that row could never have been verified as
  written and is now a redirect; and `top -l 0` is macOS **logging** mode, which
  streams plain text, never takes the alternate screen and ignores the keyboard
  — it was not a repainting full-screen program at all. That row is now
  interactive `top -s 1`, which asserts `alternate_on` and quits with `q`.
- **Assertions.** `renderMatrixReport` only printed. The matrix now asserts per
  scenario: zero model faults, zero seed races, zero discarded bytes, zero
  degenerate comparisons, zero comparisons inside an open discard window, a
  non-zero comparison count, zero unexplained cells for every scenario measured
  clean, and — for the one scenario whose divergence is this slice's finding —
  that the divergence still reproduces. That last assertion fails if the defect
  is silently fixed, so this document cannot rot into a stale claim.

A test that cannot fail is not evidence. The matrix can now fail in both
directions.

---

## 2. Isolation and tmux safety

`./scripts/tmux-drive.sh paths` was run **first**, before anything else, and both
axes were confirmed isolated:

```text
run dir:       /tmp/sidecar-drive-501            (not $HOME)
inner socket:  /tmp/sidecar-drive-501/tmux/tmux-501/default
state home:    /tmp/sidecar-drive-501/state      (not ~/.local/state/sidecar)
config:        /tmp/sidecar-drive-501/config/config.json (not ~/.config/sidecar)
```

The Go matrix harness (`startCompareTmux`) starts its own throwaway server **per
scenario**: explicit `-S` inside a fresh temp dir, the socket path asserted to be
under that directory before any command runs, `TMUX` scrubbed from every child
environment, `HOME` redirected into the temp dir, the pane shell started as
`zsh -f` so no personal configuration can reach a capture, and teardown targeting
that same explicit socket. **No bare `kill-server` is ever issued against the
default server.** The developer's default tmux server was listed before and after
every run: 20 sessions before, 20 sessions after, unchanged.

---

## 3. Deterministic corpus

`go test ./internal/tty/screenmodel` — 24 fixtures, replayed whole, at every
split boundary, byte-at-a-time, through a seed round trip and through
`Frame.Output`. All pass. One fixture was **added** by this slice
(§4.1), and slice 0's known-gap signature sets still match exactly.

**Against the gate's wording ("zero cell, attribute, cursor, mode, history, or
split-boundary mismatches") the corpus does not pass**, because slice 0's
GAP-1…GAP-9 are still present upstream and are still asserted as expected. What
the corpus does establish is that there are **no new or unexplained
deterministic mismatches**: every mismatch it produces has a named upstream
defect and a minimal reproducer.

### 3.1 Two decoder-side truths this slice had to establish

**tmux's `capture-pane -e` is lossy at the end of a row.** It trims trailing
blank cells but keeps the SGR change it had already emitted. A row ending
`…ESC[48;2;20;22;27m` is genuinely ambiguous between "the rest of this row is
blank in that background" (what nvim, fzf and every background-painting
application mean) and "that SGR belongs to the next row's first cell" (what
the `sgr_underline_styles` fixture means). Both were observed against tmux 3.6b.

The first draft of this slice assumed the first reading, which made the
`sgr_underline_styles` fixture fail. Assuming the second reading made every
full-screen application report its entire screen as a colour mismatch — 26 424
false cells in one matrix run. **Neither assumption is defensible, so the
comparator asserts neither**: `DecodeCaptureExtent` reports where each row's
content ended, and a *styling* difference at or past that column, where the model
also holds a plain blank, is counted as `UncomparableCells` rather than as
agreement or as a mismatch. A real character the capture does not have is still a
mismatch.

This is an honest weakening of the result, and the honest way to size it is as a
fraction, which the first draft omitted. The harness now records the denominator
(`ComparedCells`, width × height per comparison). In the corrected matrix run,
**74 112 of 567 480 compared cells were declined — 13.1 %**. For scale, the
first draft's alarming-sounding "26 221 declined cells" was 5.1 % of its own
smaller surface. The percentage rose because the corrected matrix spends
proportionally more of its time inside full-screen applications, which is where
trailing-blank styling lives; it is a property of the workload, not a
regression.

**Trailing-blank styling of full-screen applications is therefore not covered by
this slice.** If it matters for slice 3, it needs a different oracle than
`capture-pane -e`.

---

## 4. Findings

### 4.1 GAP-10 — `x/vt` deadlocks on any device query (found, fixed adapter-side)

**This is the most important finding in the slice.** `vt.Emulator` answers device
queries — DSR (`CSI 5 n`, `CSI 6 n`), primary and secondary device attributes,
DECRQM mode reports (`CSI ? 2026 $p`), OSC 10/11/12 colour queries, in-band
resize — by writing to an **unbuffered `io.Pipe`** whose only drain is
`Emulator.Read`. Nothing buffers those bytes. With no reader, the **first** such
query blocks `Emulator.Write` forever.

That is not a corner case: every full-screen application in the matrix emits one
before drawing a cell. nvim's opening burst is
`ESC[?1049h … ESC[?69$p ESC[?2026$p ESC[?2027$p ESC[?2031$p ESC[?2048$p ESC[?u OSC 11;? BEL ESC[5n`.

In slice 1's design the writer is the control client's **single ordered actor**,
so the deadlock stops that client's entire event loop. Observed directly with a
goroutine dump: opening nvim froze the actor, the reader goroutine then filled
its event channel and blocked, and the pane stopped updating — **no error, no
fallback, no diagnostic, forever**. In slice 3 that is a user's terminal
silently freezing the moment they open an editor.

Slice 0 did not find it because the corpus contained no query. It does now:
`device_status_queries` (a new declared corpus category, recorded from an
isolated tmux; the queries must leave nothing on the screen on either side),
plus `TestDeviceQueriesDoNotBlockTheModel` for each sequence individually.

**Fixed on Sidecar's side of the seam** (`internal/tty/screenmodel/replies.go`):
a per-model drain goroutine consumes the reply stream and **discards** it. Not
forwarded — tmux is the real terminal for this pane and has already answered the
application's query; injecting a second answer would corrupt the application's
parser. This is the adapter honouring the emulator's `io.ReadWriter` contract,
not an application-specific escape repair. The drain also owns `Emulator.Close`,
because the emulator tracks its closed state in an unsynchronised field and
closing from the model actor would race an in-flight `Read`; teardown is proved
race-free under `-race`, and `TestReseedingDoesNotLeakDrainGoroutines` proves 50
reseeds leak neither goroutines nor emulators (each reseed previously leaked an
emulator and a 4 MB parser buffer — a second, quieter bug the same fix closes).

### 4.2 A seed or resync taken on the alternate screen never recovers the main screen ⚠️ GATE

**This finding is real, and it is the only source of unexplained cells in the
corrected matrix — but its scope and its size are both different from what the
first draft said, and the difference matters.**

The mechanism: a seed built from `capture-pane` while an application owns the
alternate screen can only carry the **alternate** grid; the model's main screen
is left empty. tmux kept the real one. The instant the application exits the
alternate screen, the two disagree about the whole visible grid — and **nothing
triggers a resync**, so the divergence persists until something else reseeds the
model.

Pinned as evidence, not prose, by
`TestAltScreenAttachCannotRestoreTheMainScreen`, which writes main-screen
markers, enters the alternate screen, attaches, exits, and requires the
divergence to reproduce (the test fails loudly if it ever stops reproducing, so
the evidence cannot rot into a stale claim).

**What the first draft got wrong.** It attributed "essentially all" 1 045
unexplained cells to this cause, and named `less` and `top` among the scenarios
it had ruined. That attribution could not have been supported by the experiment
that produced it: every scenario shared **one** persistent model and **one**
subscription, and only the counters were reset between them. `less` and `top`
recorded **zero seeds**, so no attach or reseed happened inside either of them;
whatever they were counting, they had inherited it. Two later scenarios recorded
zero unexplained cells, which also contradicted the claim that the divergence is
permanent.

**The scenario-isolated rerun.** Every scenario now runs against its own tmux
server, control client, subscription and pane model. The unexplained total falls
from 1 045 to **127**, and its distribution collapses onto a single scenario:

| Scenario | Unexplained cells, shared harness (first draft) | Unexplained cells, isolated harness |
| --- | --- | --- |
| zsh prompt editing | 0 | 0 |
| nvim (with a mid-application tmux resize) | 182 | **126** |
| `less` | 414 | **0** |
| `top` | 448 | **0** |
| `fzf` | 1 | 1 |
| alternate-screen cycling ×4 | 0 | 0 |
| attach / hide / show / re-subscribe | 0 | 0 |
| **TOTAL** | **1 045** | **127** |

So `less` and `top` were never divergent. Roughly 88 % of the first draft's
headline mismatch number was one scenario's divergence being carried forward
into every scenario that followed it. The corrected number is smaller, and it is
the one the gate should be judged on.

**A controlled experiment now settles the cause.** The matrix contains two
editor rows that differ in exactly one respect:

| Row | What it does | Seeds | Unexplained cells |
| --- | --- | --- | --- |
| `editor-nvim-or-vim` | open nvim, insert, `:vsplit`, search, **resize the tmux window twice**, quit | 6 | **126** |
| `editor-no-resync-control` | open nvim, insert, `:vsplit`, search, quit — **no resize, no layout event** | 0 | **0** |

Same application, same edits, same search, same quit, same alternate-screen
entry and exit. The only difference is whether a seed/resync was taken while
`alternate_on=1`. With one, the main screen is lost and 126 cells diverge after
the editor exits; without one, the row is clean.

**The finding is therefore sharper than "mid-stream attach is the risk".** The
trigger is **any** seed or resync taken while the pane is on the alternate
screen — first attach, pane switch, Sidecar restart, layout change, resize,
`client_discarded` growth, or reconnect. In the isolated matrix the initial
attach happened on the main screen and the damage came from the *resize* path,
which the plan lists as a routine, conservative, expected event. That makes the
defect easier to hit in production than the first draft implied, even though the
cell count is smaller.

**The remedy was measured and exists.** While `alternate_on=1`, tmux's
`capture-pane -a` returns the **saved main screen**, and plain `capture-pane`
returns the alternate grid — both asserted in the same test. A seed transaction
that captured both, and seeded the main screen before switching to the
alternate, would converge. That is a change to the slice-1 seed transaction and a
new `Seed` field, so it is **slice 3 design work and was deliberately not done
here** — doing it would have replaced the measurement with an assumption.

### 4.3 Absolute history size is unreliable ⚠️ GATE

Two distinct defects:

1. **Frozen at seed value on the alternate screen** — the model reported
   `HistorySize = 1` where tmux reported 202 for the same pane, because
   `frame()` used `seedHistorySize` alone on the alternate screen and dropped
   everything that had scrolled off since the seed. **Fixed here** (a one-line
   adapter fix inside two declared corpus categories, alt-screen and history):
   the alternate-screen branch now reports `seedHistorySize + scrolledOff()`,
   frozen at the moment of the switch, which is what tmux does. The corrected
   matrix records **zero** `history/size` mismatches across 295 comparisons,
   which is the first direct evidence that this fix holds in real applications.
2. **Drift past the emulator's scrollback cap — not fixed.** `scrolledOff()` is
   the delta of the emulator's own bounded scrollback, so once
   `DefaultScrollback` (10 000) is reached the absolute count stops tracking
   tmux's. Under the soak the model reported ~10 202 against tmux's ~4 900, on
   **221 of 224** comparisons. Slice 0 recorded this as a known limitation; this
   is the first time it has been seen to dominate a real session. It needs real
   absolute-coordinate tracking, not a constant, and the viewport's
   lazy-older-history requests are what depend on it.

Classified as `adapter-absolute-history-drift` rather than "unexplained", because
the cause is understood — but it is **Sidecar's defect, not an upstream one, and
it is gate-blocking**. §4.7 records how that class was tightened so it cannot
absorb causes it does not explain.

### 4.4 The alternate-screen frame omits loaded history (known, not fixed)

On the alternate screen the model's `Frame.Output` is the alternate grid only,
while tmux's capture still returns the main screen's loaded history above it.
Classified `adapter-alt-screen-history-not-rendered`.

The scenario-isolated rerun **promotes this from a footnote to the most
widespread defect in the matrix**: 55 cells across 5 of the 8 scenarios, and it
is the *only* mismatch class in `less`, `top`, `alt-screen-cycling` and the
no-resync editor control. In the first draft it was 27 cells and read as an
edge case, because the much larger inherited divergence was drowning it out.

Under authority this would change what the user sees — the scrollback above a
full-screen application would disappear — so it must be settled in slice 3. Not
fixed here: changing the frame's shape is beyond this slice's mandate.

### 4.5 Frame rendering does not scale with the byte delta ⚠️ GATE

`publishModelFrames` re-renders the whole loaded history plus the grid on every
coalesced tick. Measured:

| Path | ns/op | B/op | allocs/op |
| --- | --- | --- | --- |
| capture path: parse a 624-line capture response | 14 031 | 57 488 | 2 |
| model: `Write` a 64 B burst | 45 492 | 6 588 | 54 |
| model: `Write` a 512 B burst | 378 955 | 56 908 | 459 |
| model: `Write` a 4 096 B burst | 3 891 395 | 434 886 | 3 681 |
| model: `Frame()` with no history | 104 391 | 235 801 | 346 |
| model: `Frame()` with 600 history lines | 860 957 | 469 932 | 5 147 |
| shadow compare (diagnostic only) | 148 520 | 954 570 | 240 |

The **write** half does scale with the byte delta, as required. The **render**
half does not: it scales with the 600-line capture window, at 861 µs and 470 KB
per frame. So the gate's "replay work and allocations scale with the byte
delta/current grid, not the 600-line capture window" is **half-satisfied**. The
remedy is a frame that emits only the live grid plus newly scrolled-off lines,
which is a slice-3 change to `Frame`/`ControlSnapshot`.

Read the write numbers carefully: they are dominated by row scrolling (a 4 KB
burst is ~410 scroll operations), and they are measured against a persistent
model at its scrollback cap, which is the worst case.

### 4.6 GAP-6 reproduces in a real shell

Three cells in the corrected matrix, all `gap-6/9-grapheme-cluster`: NFD text
(`e` + U+0301) rendered by the model as two cells where tmux renders one.
Confirms slice 0's GAP-6 is reachable in ordinary use, not only in a fixture.
GAP-9 (cluster split across a `Write` boundary) was **not** observed, which is
expected — the classifier cannot distinguish it from GAP-6, and the matrix's
Unicode content is small.

### 4.7 Two mismatch classes were acting as catch-alls; both are tightened

An "explained" class is only worth having if it can be *earned*. Two could not
be, and both are now gated on positive evidence of the mechanism they name:

- **`gap-8-ris-history`** was awarded whenever the model's history was zero and
  tmux's was not, with nothing checking that a RIS had ever occurred. Any cause
  that zeroed the model's history was amnestied by it. The model now counts RIS
  (`ESC c`) sequences seen since the last seed (`Model.scanHardResets`, surfaced
  as `Frame.HardResets`), and the class requires that count to be non-zero. The
  scan is a two-byte match rather than a full parse, which can only ever
  *under*-count and therefore only ever move a mismatch back into `unexplained`.
- **`adapter-absolute-history-drift`** was awarded whenever the model's reported
  history was `>= DefaultScrollback`, in either direction, at any magnitude,
  regardless of cause — and that threshold is precisely the soak's steady state,
  so it absorbed almost every soak mismatch. The precondition of the defect is
  not "the number is large", it is "the emulator's own scrollback is pinned at
  its cap, so the delta has stopped tracking". The model now reports
  `Frame.ScrollbackAtCap` directly and the class requires it.

**Re-reported soak number after the tightening: 224 comparisons, 2 clean, 222
with a mismatch, `adapter-absolute-history-drift` 221 and `unexplained` 1 — all
of them `history/size`.** The number did not move, because during the soak the
emulator genuinely *is* pinned at its cap, so the class is earned rather than
assumed. That is the useful outcome of the tightening: the same number now means
something. It does not rehabilitate the class — §4.3.2 is still a gate-blocking
Sidecar defect, and the soak still shows it on 99 % of comparisons.

### 4.8 Unexplained mismatches that are not attributed to anything

Stated plainly rather than left to be inferred from a table. In the corrected
matrix the 127 unexplained cells are:

- **124 `cell/grapheme` and 1 `history/rows`**, all inside
  `editor-nvim-or-vim`, all after the alternate-screen reseed — §4.2, with the
  controlled experiment above as the attribution.
- **2 `cursor/position`**, one in `editor-nvim-or-vim` and one in
  `fzf-mouse-aware`. **These are unexplained.** Cursor position is a field the
  decision gate names explicitly, so it is worth being precise about what is and
  is not ruled out: both occurred with `comparisons_with_capture_metadata_race =
  0`, so neither is the known metadata-skew window that `CursorTrustworthy`
  guards; and neither scenario recorded a grapheme-cluster split at the time, so
  the GAP-9 cursor-advance explanation does not apply either. Two cells out of a
  566-cursor-comparison run is small, but "small and unexplained" is not
  "explained", and this document does not claim otherwise. Reproducing them
  deterministically is outstanding work.

The first draft left roughly 28 mismatches, including 8 `cursor/position`,
unaddressed anywhere in the text. That is the gap this section closes.

---

## 5. Decision-gate criteria

Scoring rule used here: a criterion is a PASS only if the whole of it is
measured and met. "Measured on half, unmeasured on the other half" is NOT
PROVEN, not PASS; "the mechanism exists but the stated outcome does not hold
yet" is PARTIAL.

| # | Criterion | Result | Evidence |
| --- | --- | --- | --- |
| 1 | Deterministic fixtures have zero cell, attribute, cursor, mode, history or split-boundary mismatches | **FAIL as written.** 24 fixtures pass; every remaining mismatch is a named upstream gap (GAP-1…9) with a minimal reproducer, and no new one appeared. It passes only under the weaker reading "no *unexplained* deterministic mismatches", which is not what the gate says. GAP-9 still blocks authority. | §3, `go test ./internal/tty/screenmodel` |
| 2 | Common real applications have zero unexplained steady-state mismatches and no persistent mismatch after supported seed/resync events | **FAIL.** 127 unexplained cells over 295 comparisons; 59 of 295 comparisons carried at least one mismatch. The unexplained cells are one scenario (§4.2); on top of them, 55 cells of a *known* Sidecar defect (§4.4) appear in 5 of 8 scenarios, and the criterion's second half — "no persistent mismatch after supported seed/resync events" — is exactly what §4.2 fails. | §4.2, §4.4, §4.8, §6.1 |
| 3 | Attach/restart converge without injecting keystrokes, signals or fake resizes | **FAIL.** Attach on the main screen converges cleanly (attach-switch-restart: 182/182 clean, including a full drop-and-recreate of the subscription — an improvement on the first draft's 141/168, which was contaminated). A seed or resync taken while an alternate-screen application is running **never** converges. Remedy measured and available (`capture-pane -a`), not implemented. | §4.2, `TestAltScreenAttachCannotRestoreTheMainScreen` |
| 4 | The ordered barrier and `client_discarded` handling prove no duplicated or omitted bytes under sustained output and `pause-after` | **PASS.** 131 605 bursts / 38.6 MB in the soak: `discarded_bytes = 0`, `seed_races = 0`, `comparisons_in_open_discard_window = 0`, `comparisons_with_capture_metadata_race = 0`; the matrix asserts all four are zero per scenario. Slice 1's six byte-continuity scenarios still pass. | §6.3, slice-1 evidence §1.2 |
| 5 | Steady-state model delivery performs zero `capture-pane` and `display-message` per output burst; one seed/resync transaction is the expected exception | **PASS.** 295 of 295 capture transactions ran while a live model already held the screen, i.e. 0.000 captures per burst would remain; the only remainder is 10 seed transactions and a 1 s `client_discarded` cadence probe (not per burst). | §6.1 "Commands" table |
| 6 | Replay work and allocations scale with the byte delta/current grid, not the 600-line capture window; total memory bounded under a sustained soak | **FAIL on work; NOT PROVEN on memory.** Work: `Write` scales with the delta, `Frame()` scales with the capture window (861 µs, 470 KB per frame at 600 lines) — a measured failure. Memory: the series is flat at 51.3 MB across the soak, but the number is `Model.Footprint()` = `(rows + scrollback.Len()) × cols × 64`, which is **bounded by construction** because the emulator caps scrollback at 10 000. It cannot do anything but be flat once the cap is reached, and it is provably blind to the exact leak this slice found and fixed (a leaked emulator and 4 MB parser buffer per reseed, §4.1). A metric that could not have detected the leak is not evidence of boundedness. A heap profile is required. | §4.5, §6.3, §7.10 |
| 7 | End-to-end output latency and CPU improve over the existing in-band capture baseline, with no startup or idle regression | **NOT PROVEN.** CPU is unmeasured and unmeasurable while capture is authoritative (§7.1). Latency does **not** improve on the interactive workload: 13 710 µs mean (model) against 13 822 µs mean (capture) over 292/291 samples — parity, 0.8 %. The 4× figure comes only from the 38 MB soak, which is the least representative workload in the slice and is measured against a self-inflated baseline (§6.4, §7.9). No startup or idle path is touched with the variable unset. | §6.4, §7.1, §7.9 |
| 8 | Removing capture authority will eventually delete mouse-fragment regexes, cursor/mouse metadata queries and capture-on-burst code rather than leaving two permanent renderers | **PARTIAL.** The model already supplies cursor, mouse modes and alternate-screen state in the same ordered stream, so `display-message` per burst becomes deletable (criterion 5). But §4.4 (alt-screen history, now the most widespread defect in the matrix) and §4.5 (frame shape) mean the model's frame is not a drop-in for `ControlSnapshot.Output`, so **today it would be a second renderer, not a replacement** — which is the condition the criterion is written to exclude. | §4.4–4.5 |

**Tally: two passes (4, 5), one partial (8), one not proven (7), four fails (1,
2, 3, 6).**

The first draft scored this "three fails, one partial fail, four passes", which
required counting criteria 7 and 8 as full passes while its own cells for them
read "PASS on latency, UNMEASURED on CPU" and "today it would be a second
renderer, not a replacement". Criterion 6 was scored PARTIAL FAIL with its
memory half marked PASS; §7.10 explains why that half is not proven either. The
corrected tally is the one to take to the gate.

Criteria 2 and 3 share a single root cause (§4.2) with a measured remedy;
criterion 6's work half and criterion 8 share another (§4.5). That is still a
short, named list rather than an open-ended patch layer, which is why the
recommendation remains *hold* rather than *reopen Herdr* — but it is four fails,
not three, and two of the remaining four criteria are not passes.

---

## 6. Measurements

All numbers in §6.1 and §6.4 come from the scenario-isolated matrix run
(`-screencompare`, one fresh tmux server, control client, subscription and model
per scenario).

### 6.1 Real application matrix

Availability recorded honestly; no synthetic program was substituted for any
application. Paths under `$HOME` are shown `~`-relative (§1.5).

| Program | Resolved | Exercised |
| --- | --- | --- |
| `zsh` | `/bin/zsh` (run as `zsh -f`) | yes |
| `nvim` | `/opt/homebrew/bin/nvim` | yes — twice, as the test and the control of §4.2 |
| `vim` | not installed (`vim` is an alias to `nvim`, not on `PATH` for `exec.LookPath`) | n/a — nvim covered it |
| `less` | `/usr/bin/less` | yes |
| `top` | `/usr/bin/top` | yes (interactive, continuously repainting) |
| `fzf` | `/opt/homebrew/bin/fzf` | yes (mouse-aware TUI) |
| `claude` | `~/.local/bin/claude` | **NOT RUN** — see below |
| `codex` | `~/.local/bin/codex` | **NOT RUN** — see below |
| `htop`, `btm`, `aider`, `lazygit`, `glow` | not installed | not run |

**The agent-TUI row of the plan's matrix is not covered.** Both supported agent
CLIs are installed, but starting either needs the developer's real credentials
and network access; running them under the isolated `HOME` the harness requires
would exercise an authentication screen, not an agent's idle/streaming/approval/
interrupt/completion transitions. The harness has the scenario written, with
launch verification, gated behind `SIDECAR_SCREENCOMPARE_AGENT=1`; it was not
enabled. **No synthetic full-screen program was substituted.** This is a real
hole in the evidence and it is a slice-3 blocker in its own right: the agent TUIs
are the surface slice 4 targets, they emit the heaviest and most unusual escape
traffic of anything Sidecar hosts, and nothing here says how the model handles
them.

| Scenario | Comparisons | Clean | With mismatch | Unexplained cells | Upstream-gap cells (x/vt) | Adapter-defect cells (Sidecar) | Seeds |
| --- | --- | --- | --- | --- | --- | --- | --- |
| zsh prompt editing, multiline, completion, wrapped/coloured/wide output | 19 | 16 | 3 | 0 | 3 | 0 | 0 |
| nvim: alt screen, insert, `:vsplit`, search, **live resize**, quit | 15 | 1 | 14 | 126 | 0 | 13 | 6 |
| nvim control: same, **no resize/layout reseed** | 12 | 2 | 10 | 0 | 0 | 10 | 0 |
| `less lines.txt`: paging, search, quit | 9 | 4 | 5 | 0 | 0 | 5 | 0 |
| `top -s 1`: continuous repaint | 8 | 2 | 6 | 0 | 0 | 6 | 0 |
| `fzf`: mouse modes on and off | 9 | 4 | 5 | 1 | 0 | 5 | 0 |
| alternate-screen cycling ×4, then restored shell history | 41 | 25 | 16 | 0 | 0 | 16 | 0 |
| agent TUI | _not run — see above_ | | | | | | |
| attach, hide/show, drop and recreate the subscription | 182 | 182 | 0 | 0 | 0 | 0 | 4 |
| forced control-client failure and fallback | 0 | 0 | 0 | 0 | 0 | 0 | 0 |
| **TOTAL** | **295** | **236** | **59** | **127** | **3** | **55** | **10** |

The "known gap" column of the first draft mixed upstream `x/vt` defects with
Sidecar's own, which flattered the result: it read as "explained, therefore not
ours". It is split here. **Only 3 of the 58 explained cells are upstream. The
other 55 are Sidecar's.**

Aggregate classes: `unexplained` 127, `adapter-alt-screen-history-not-rendered`
55, `gap-6/9-grapheme-cluster` 3. Signatures: `cell/grapheme` 126,
`history/rows` 56, `cursor/position` 2, `history/grapheme` 1. **No
`history/size` mismatch at all**, which is §4.3.1's fix holding.
Resyncs: first-seed 2, layout 6, resize 2. Zero faults, zero discarded bytes,
one fallback (the deliberately forced one), zero open discard windows, zero
capture-metadata races, zero degenerate comparisons.

Commands: 1 092 `%output` bursts, 102 820 raw bytes, 295 `capture-pane`, 295
`display-message`, 295 captures avoidable, 10 seed transactions, 294 model
frames. 74 112 of 567 480 compared cells (13.1 %) declined as uncomparable
(§3.1).

### 6.2 The real Sidecar, driven headlessly

`scripts/tmux-drive.sh`, both axes isolated (§2), same binary, same key
sequence, run twice — once with `SIDECAR_TMUX_SCREEN_COMPARE` unset and once with
it set to 1. The sequence reaches the workspaces plugin, opens a shell
(`ctrl+n`), enters interactive mode (`E`), runs a marker script producing
`SHADOWPROOF` lines, `seq 1 60` and coloured/bold text, then opens and quits
nvim.

**The rendered Sidecar screens are identical.** Diffing the two captures after
stripping SGR and the wall clock leaves **zero differing lines**. The whole
terminal output region — the marker lines, the wrapped sequence, the colours,
and the shell prompt restored after nvim exits — is identical.

The shadow run's JSON report (from the real app rather than the harness), rerun
after the classifier tightening of §4.7:

```json
seeds: 5,  resyncs: {first-seed: 1, layout: 3, resize: 1}
raw_events: 249,  raw_bytes: 29236,  model_frames: 49
captures: 50,  metadata_queries: 50,  captures_while_model_live: 50, seed_captures: 5
comparisons: 50,  comparisons_clean: 48,  comparisons_skipped: 0, frames_with_mismatch: 2
mismatches_by_class: {adapter-alt-screen-history-not-rendered: 1, unexplained: 2}
mismatches_by_signature: {history/rows: 2, history/size: 1}
comparisons_in_open_discard_window: 0, comparisons_with_capture_metadata_race: 0
uncomparable_cells: 7395 of 255200 compared cells (2.9%)
discarded_bytes: 0, faults: 0, seed_races: 0, fallbacks: 0, model_bytes_peak: 534528
output_to_frame_us: n=46 mean=13279 | output_to_capture_us: n=47 mean=13219
```

48 of 50 clean in the real application. The two mismatched frames are the
alternate-screen history pair from §4.3/§4.4 at the nvim transition.

**One of them moved class as a direct result of §4.7,** and it is worth
recording: before the tightening this run reported
`mismatches_by_class: {gap-8-ris-history: 1, unexplained: 1}`. No RIS was ever
sent. The class was being awarded purely because the model's history was zero
where tmux's was not, which is exactly the amnesty §4.7 removes. The same
mismatch is now `unexplained`, which is what it always was. The real-application
result is one cell worse than the first draft reported, and correctly so.

The report was grepped for the pane's content, the host name, the home
directory and the account name: **zero hits**.

The latency figures corroborate §6.4 independently, in the real application
rather than the harness: **13 279 µs mean to a model frame against 13 219 µs
mean to a capture snapshot** — parity, with the model marginally *behind*.

### 6.3 Sustained-output soak

25 s of continuous fast output through the real transport:

| Measure | Value |
| --- | --- |
| `%output` bursts / bytes | 131 605 / 38 627 292 |
| comparisons / clean / mismatched | 224 / 2 / 222 |
| mismatch classes | `adapter-absolute-history-drift` 221, `unexplained` 1 (all `history/size`) |
| discarded bytes | **0** |
| captures / metadata queries / seeds | 224 / 224 / 0 — all 224 avoidable under authority |
| retained model memory (2 s samples, 24 s) | 51 322 880 bytes, flat, no growth — but see §7.10 |
| output → model frame | **22 380 µs mean, 34 647 µs max** |
| output → capture snapshot (baseline) | **93 329 µs mean, 227 476 µs max** |
| model write / model render / shadow compare | 94 µs / 8 291 µs / 3 773 µs mean |

Every cell mismatch in the soak is `history/size`: the *content* never diverged
across 38.6 MB of continuous output on an unloaded machine. That is the
strongest single fidelity result in the slice, and §7.12 records the load
condition it depends on.

Two cautions on the memory number. It is an **estimate**
(`Model.Footprint()`: `(rows + scrollback.Len()) × cols × 64`), not a heap
profile. And 51 MB per pane is large: it is the emulator's default 10 000-line
scrollback, against a 600-line capture window Sidecar actually uses. The real
Sidecar run measured ~0.5 MB for a 116×44 pane that had not yet filled its
scrollback. Sizing the model's scrollback to what Sidecar needs is a slice-3
item. §7.10 explains why this number is scored NOT PROVEN rather than PASS.

### 6.4 Latency: the interactive number, and the soak number

The harness produces two latency measurements. The first draft quoted only the
second. Both belong in the record, and the first is the more representative one:

| Workload | output → model frame | output → capture (baseline) | Result |
| --- | --- | --- | --- |
| **Interactive application matrix** (shells, editors, pagers, TUIs — n=292 / 291) | **13 710 µs mean, 16 688 µs max** | **13 822 µs mean, 16 739 µs max** | **parity** (0.8 %) |
| Sustained-output soak (38.6 MB firehose — n≈224) | 22 380 µs mean, 34 647 µs max | 93 329 µs mean, 227 476 µs max | ~4× better |

The two are not in conflict; they measure different things. On interactive
traffic both paths are dominated by the same 12 ms coalescing tick and tmux's
own scheduling, and the model has no room to win. Under a firehose the capture
path's single-flighted request/response queues behind the output it is trying to
describe, and the model's continuous feed does not. **The 4× is real, and it is
real only for sustained bulk output.** A user typing in a shell or scrolling an
editor should expect the byte-fed path to feel the same, not four times faster.

**The baseline is also inflated by the measurement.** Both numbers are taken in
shadow mode, where `publishModelFrames`' `Frame()` (8.3 ms mean in the soak) and
the comparison itself (3.8 ms mean) run on the **same single ordered actor** as
the capture response
(`internal/tty/control_manager.go:584`, `internal/tty/control_model.go:550`). A
capture response therefore waits behind work that would not exist in
production. The direction of that bias is knowable even though its size is not:
it makes the capture baseline look **slower** than it is, so the model's
advantage is at most what is reported and probably less. That applies to the
soak's 4× as much as to the interactive parity — the parity result is if
anything the more conservative of the two, since a deflated baseline could only
have made the model look better.

The first draft carried this measurement as "~4× better" in the verdict, the
criterion-7 cell and §6.3, without the interactive row its own harness had
generated. That is corrected here and in §5.

---

## 7. Where this evidence is weaker than it looks

Stated plainly, because an overstated pass is the worst outcome of this slice.

1. **CPU is not measured.** The gate asks whether CPU improves over the capture
   baseline. It cannot be answered while `capture-pane` is authoritative: shadow
   mode runs *both* paths, so a process-level measurement is (capture + model +
   comparison), not (model). The per-burst benchmarks in §4.5 isolate Sidecar's
   own work on each path, and the latency figures in §6.4 include tmux's side —
   but no number here is "CPU under byte-fed authority". Only slice 3 can produce
   that.
2. **The agent-TUI row is missing entirely** (§6.1). It is the surface with the
   heaviest escape traffic and the one slice 4 targets.
3. **13.1 % of compared cells were declined as uncomparable** (§3.1), all of them
   trailing blanks in full-screen applications. "236 clean comparisons" therefore
   means "clean over the cells `capture-pane -e` can describe". Background
   styling of blank regions in nvim/fzf/top is genuinely untested.
4. **GAP-9 was not exercised.** The matrix's Unicode content is a handful of
   cells, and the classifier cannot separate GAP-6 from GAP-9. The slice-1
   precondition (a cluster split across an `%output` boundary) remains a
   theoretical exposure with no live measurement.
5. **The mismatch counts are cell counts, and cells are correlated.** One wrong
   screen contributes hundreds. `FramesWithMismatch` (59 of 295) is the honest
   denominator for "how often was the model wrong"; the cell totals say "how
   badly", not "how often". Several scenarios are also *small*: `less`, `top`
   and `fzf` contribute 8–9 comparisons each, so "clean" there means "clean over
   a handful of frames", not a statistically strong result.
6. **One platform, one tmux, one machine.** darwin/arm64, tmux 3.6b. Slice 1's
   ordering guarantee is still empirical rather than documented; nothing here
   strengthens it. The `top` row is additionally **macOS-only as written** —
   `top -s 1` is BSD syntax; Linux spells the interval `-d` — so that scenario
   is skipped rather than adapted on other platforms.
7. **The scenarios are scripted key sequences, not a human.** They are
   deterministic enough to compare across runs and short enough to be honest
   about — a few seconds per application, not a full working session. Run-to-run
   totals varied by roughly ±5 % across three matrix runs.
8. **`forced-control-failure` produced zero comparisons.** It proves the fallback
   fires (asserted), but it contributes no fidelity evidence, and the model's
   behavior across a control-client death is covered only by slice 1's
   integration tests.
9. **The comparison itself is expensive** (3.8 ms mean, ~950 KB per call) and it
   runs on the same ordered actor as the capture response. It is diagnostic-only
   and excluded from every model-path number, but it **inflates the capture
   baseline it is compared against** — see §6.4. The direction of the bias is
   known (baseline too slow), the size is not.
10. **The memory result is bounded by construction, not by measurement.**
    `Model.Footprint()` is `(rows + scrollback.Len()) × cols × 64`, and
    `scrollback.Len()` cannot exceed `DefaultScrollback` (10 000). Once the cap
    is reached the series *must* be flat; a flat series is therefore not
    evidence that retention is bounded, it is a restatement of the cap. The
    metric is also demonstrably blind to real leaks: the per-reseed emulator and
    4 MB parser-buffer leak found in §4.1 would not have moved it by one byte.
    Criterion 6's memory half is scored NOT PROVEN for this reason, and a heap
    profile under the soak is required before the gate is revisited.
11. **The slice-1 lock-order risk was reduced, not eliminated.** `ControlManager`
    no longer calls the blocking `post()` while holding its mutex — the model
    start moved to `startModel` after the unlock, and `startModelFeed`
    revalidates the subscription to cover the window that opens. Slice-1
    evidence §9 item 8 is closed. Item 7 (a control client whose tmux dies with a
    saturated event channel can freeze with no fallback, `td-58cebc`) is **not**
    fixed and is now reachable whenever shadow mode is on.
12. **The soak headline does not survive load.** Rerun under `-race`, the same
    soak produced `unexplained: 1605` — signatures `history/grapheme` 1519,
    `cell/grapheme` 83, `history/size` 5, `cursor/position` 1 — across only 6
    comparisons, with a mid-run reseed visible in the memory series (51.3 MB →
    25.8 MB → back up). That is **content divergence**, which the unloaded soak
    never showed. `-race` starves the ordered actor badly (output → capture mean
    rises from 93 ms to **3.93 s**, max 8.75 s), so this is **not a refutation**
    of §6.3 — it is effectively a different machine, and the reseed suggests the
    model was invalidated and rebuilt mid-stream rather than silently drifting.
    But the honest reading is that "content never diverged across 38.6 MB" holds
    **on an unloaded machine**, and slice 3 would run on loaded ones. The load
    sensitivity of the fidelity result is itself unmeasured, and measuring it
    properly needs a load generator that does not also instrument the runtime.
13. **Two structural hazards in the comparator, one fixed and one disclosed.**
    - *Disclosed:* `attributable` (`screencompare.go`) defaults to **false** when
      discard metadata is missing or stale, and an unattributable mismatch is
      re-classed under a `discard-window/` prefix that both the unexplained tally
      and the evidence table exclude. A break in `client_discarded` parsing would
      therefore **zero the headline mismatch number** while every comparison
      still counted, and it would look like a perfect result. It did not fire —
      `comparisons_in_open_discard_window` is 0 in every run — and the matrix now
      asserts that counter is zero per scenario, so the silent-amnesty path
      cannot be entered without failing the evidence run. The design remains
      fail-quiet and should be inverted in slice 3.
    - *Fixed:* a comparison whose capture reported degenerate geometry
      (`w < 1 || h < 1`) returned an empty result that was scored as
      `ComparisonsClean++` — a comparison that never happened counting as a
      clean one. It is now marked invalid and counted separately
      (`comparisons_skipped`, 0 in every run), and the matrix asserts it stays 0.
14. **The configuration excludes the heaviest real escape traffic by
    construction.** The harness runs `zsh -f` (no prompt framework, no
    zsh-syntax-highlighting, no right-prompt redraws), `nvim -u NONE` (no
    plugins, no treesitter, no status line), `set -g mouse off`, and
    `set -g status off`. Those settings exist to keep the fixtures free of
    personal configuration and are correct for that purpose, but they mean the
    matrix never sees the escape streams that dominate a real terminal: a
    themed prompt repainting on every keystroke, an editor with syntax
    highlighting and a status line, or mouse tracking under a status bar. This
    **compounds** the missing agent-TUI row (§7.2) rather than being independent
    of it: between them, the two most demanding classes of real traffic Sidecar
    hosts are absent from this evidence.

---

## 8. Recommended handling

Ordered by what the gate needs, not by size.

1. **Blocking the gate, Sidecar-side:** seed the main screen from
   `capture-pane -a` whenever `alternate_on=1` — for **every** seed and resync
   path, not only first attach, since §4.2 shows resize is the trigger that
   fired in practice; real absolute history-size tracking (§4.3.2); decide the
   alternate-screen frame's history shape (§4.4, now the most widespread defect
   in the matrix); make frame publication incremental (§4.5).
2. **Blocking the gate, upstream:** GAP-9 (grapheme across `Write`) and GAP-7
   (no cursor-visibility getter) as slice 0 recorded; GAP-3/GAP-4 (OSC 8) and
   GAP-6 (NFD), the last of which now has live evidence.
3. **Blocking the gate, evidence:** run the agent-TUI matrix row deliberately
   (§6.1); measure CPU once a byte-fed surface exists (§7.1); heap-profile the
   soak instead of trusting `Footprint()` (§7.10); re-measure latency on an
   interactive workload with the shadow comparison disabled, so the baseline is
   not inflated by the diagnostic (§6.4); reproduce the two unexplained
   `cursor/position` cells (§4.8).
4. **Also worth upstreaming:** GAP-10's underlying design — an emulator that
   deadlocks when its reply stream is not drained is a sharp edge for every
   caller, and a small internal buffer or a documented contract would remove it.
5. **Carry forward:** `td-58cebc` (§7.11) must be fixed before any surface is
   enabled.

Nothing in this slice justifies vendoring or forking the emulator, and no
application-specific escape repair exists anywhere in
`internal/tty/screenmodel` — that remains zero lines. This is why the plan's
"reopen Herdr" trigger is not met even with four failing criteria.

---

## 9. Hold conditions — what must be true before slice 3 starts

The verdict is HOLD, and the hold is firmer than the first draft framed it.
Slice 3 must not start until **all** of the following are true. Each is a
consequence of §5 or §7, not a wish list.

1. **The agent-TUI row has actually been run.** It is the surface slice 4
   targets and the heaviest escape traffic Sidecar hosts, and there is currently
   no evidence of any kind about it (§6.1, §7.2). It cannot be run inside this
   harness's isolated `HOME`, because both agent CLIs need real credentials and
   network — under the isolated home the scenario would exercise an
   authentication screen and record it as an agent, which is worse than no
   evidence. **It was deliberately not attempted here.** It needs a decision
   about how to run a credentialed application safely, and that decision is
   outstanding.
2. **The scenario-isolated matrix stays isolated, verified and asserting.**
   Launch verification and per-scenario harnesses are in place (§1.7); any
   future scenario added without them re-creates the contamination that made the
   first draft's headline number wrong by ~8×.
3. **The alternate-screen seed/resync fix exists**, covering every seed and
   resync path, and `TestAltScreenAttachCannotRestoreTheMainScreen` has been
   rewritten to assert convergence rather than divergence (§4.2). The matrix's
   `editor-nvim-or-vim` row must then be clean, and its `editor-no-resync-control`
   row must stay clean.
4. **Criterion 7 is re-measured on an interactive workload**, with the shadow
   comparison disabled so the baseline is not inflated by the diagnostic, and
   with CPU included (§6.4, §7.1, §7.9). The soak is not an acceptable stand-in:
   it is the least representative workload in the slice.
5. **Memory is heap-profiled**, not estimated by a metric that is flat by
   construction and was blind to the leak this slice found (§7.10).
6. **`td-58cebc` is fixed** (§7.11) — it is reachable whenever shadow mode is on
   and would be reachable on the user path under authority.

Items 1 and 3 are the two that would most change the decision. Item 1 is
currently **blocked** on the credentials question and is the single largest hole
in this evidence.

---

## 10. Reproducing this

```bash
# Everything, one command (writes /tmp/sidecar-screen-compare/report.md)
./scripts/screen-compare-evidence.sh

# Individual pieces
go test ./internal/tty/screenmodel                                    # deterministic corpus
go test ./internal/tty -run TestScreenCompareRealApplicationMatrix -screencompare -v
go test ./internal/tty -run TestAltScreenAttachCannotRestoreTheMainScreen -screencompare -v
go test ./internal/tty -run TestScreenCompareSustainedOutputSoak -screencompare -v
go test ./internal/tty -run XXX -bench 'BenchmarkCapturePath|BenchmarkModelPath|BenchmarkShadowCompare'

# The real Sidecar, both axes isolated. Check the paths first, every time.
./scripts/tmux-drive.sh paths
SIDECAR_TMUX_SCREEN_COMPARE=1 SIDECAR_TMUX_SCREEN_COMPARE_REPORT=/tmp/scc.json \
  SIDECAR_BIN=$PWD/sidecar ./scripts/tmux-drive.sh start 200 50
```

`-screencompare` runs **fail** rather than skip when tmux is absent. The
matrix is the decision-gate evidence, and a skip on a machine without tmux
would otherwise report a fully green run that performed zero comparisons.

## 11. Verification

```text
go build ./...                                   ok
go vet ./...                                     ok
go test ./...                                    ok (all packages)
go test -race ./internal/tty/...                 ok — tty 25.3s, screenmodel 3.8s
go test ./internal/tty -screencompare            ok — matrix (54s), soak, alt-screen finding
go test -race ./internal/tty -screencompare      ok as a test; see §7.12 — under -race the
                                                 soak's *fidelity* result does not hold
git diff --check                                 clean
```

`TestControlModelFaultIsolatesOnlyThatPane` is a pre-existing flake, not a
regression from this slice; it was cleared over 200 runs plus `-race -count=60`
during the independent review.

The developer's default tmux server was listed before and after every run in this
slice: 20 sessions before, 20 sessions after, unchanged.
</content>
</invoke>
