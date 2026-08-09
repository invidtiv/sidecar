# Slice 2 evidence: shadow comparison of the real workspace journey

**Task:** `td-b06668` — Slice 2 of the byte-fed tmux screen model spike
(`td-64c916`).

**Date:** 2026-08-08 · **tmux:** 3.6b · **Platform:** darwin/arm64 (Apple
silicon) · **Go:** 1.26.5

**Reproduce everything in this document with one command:**

```bash
./scripts/screen-compare-evidence.sh /tmp/sidecar-screen-compare
```

## Verdict

**HOLD AT THE GATE — do not proceed to slice 3 yet.** Three of the eight
decision-gate criteria fail, and one of them (mid-stream attach while an
application owns the alternate screen) fails in the exact place the plan named
as the primary no-go signal. None of the failures is "x/vt cannot do this"; all
three have a bounded, named remedy, and one of those remedies was measured to be
available in tmux itself during this slice. So the correct outcome is the plan's
third option — **hold at the gate until the named fixes exist** — not "adopt"
and not "reopen Herdr".

What went right is substantial and should not be lost in the failures:

- **Steady-state delivery would issue zero `capture-pane` and zero
  `display-message` commands per output burst.** Every one of the 268 capture
  transactions in the application matrix ran while a live model already held the
  same screen; the only irreducible remainder is 10 seed transactions.
- **End-to-end output-to-frame latency is ~4× better** under sustained output:
  22.6 ms mean (model) against 95.2 ms mean (capture), measured on the same byte
  stream in the same process.
- **Retained model memory is bounded and flat** across a 24 s / 37.5 MB soak.
- **Zero model faults, zero discarded bytes, zero seed races and zero
  capture-metadata races** across 268 matrix comparisons plus 219 soak
  comparisons plus a real Sidecar run.
- A **new upstream defect (GAP-10) that would have hard-frozen the user's
  terminal in slice 3** was found and fixed on Sidecar's side of the seam.

What went wrong is equally concrete and is set out in §4.

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

Counters implemented, matching the plan's "Rollback and observability" list:
model seeds, resync reasons, raw bytes, rendered frames, captures avoided,
compare-mode mismatches, discarded bytes, fallbacks, and model memory — plus
seed races, faults, metadata queries, per-path latency, and the
"uncomparable" count described in §3.2.

### 1.6 One-command evidence report

`scripts/screen-compare-evidence.sh` runs the deterministic corpus, the shadow
unit suite, the real application matrix, the alternate-screen attach finding, the
sustained-output soak, and the per-burst benchmarks, and writes a consolidated
markdown report. In a live Sidecar, `SIDECAR_TMUX_SCREEN_COMPARE_REPORT=<path>`
writes the JSON counters every 25 comparisons and on manager stop (periodic,
because a headless proof run is killed rather than shut down cleanly).

---

## 2. Isolation and tmux safety

`./scripts/tmux-drive.sh paths` was run **first**, before anything else, and both
axes were confirmed isolated:

```text
run dir:       /tmp/sidecar-drive-scc-*        (not $HOME)
inner socket:  /tmp/sidecar-drive-scc-*/tmux/tmux-501/default
state home:    /tmp/sidecar-drive-scc-*/state  (not ~/.local/state/sidecar)
config:        /tmp/sidecar-drive-scc-*/config/config.json (not ~/.config/sidecar)
```

The Go matrix harness (`startCompareTmux`) starts its own throwaway server:
explicit `-S` inside the test's own `t.TempDir()`, the socket path asserted to be
under that directory before any command runs, `TMUX` scrubbed from every child
environment, `HOME` redirected into the temp dir, the pane shell started as
`zsh -f` so no personal configuration can reach a capture, and teardown targeting
that same explicit socket. **No bare `kill-server` is ever issued against the
default server.** The developer's default tmux server was listed before and after
the run: all 20 sessions intact.

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

This is an honest weakening of the result and is called out in §7: in the matrix
run, 26 221 cell comparisons were declined on these grounds.

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

### 4.2 Mid-attach on the alternate screen never recovers the main screen ⚠️ GATE

A seed built from `capture-pane` while an application owns the alternate screen
can only carry the **alternate** grid; the model's main screen is left empty.
tmux kept the real one. The instant the application exits the alternate screen,
the two disagree about the whole visible grid — and **nothing triggers a
resync**, so the divergence is permanent.

This is the plan's "mid-stream attach is the central risk" made concrete, and it
accounts for essentially all 1 019 unexplained `cell/grapheme` cells in the
matrix: the editor, `less` and `top` scenarios all attached before or during an
alternate-screen application and never recovered.

Pinned as evidence, not prose, by
`TestAltScreenAttachCannotRestoreTheMainScreen`, which writes main-screen
markers, enters the alternate screen, attaches, exits, and requires the
divergence to reproduce (the test fails loudly if it ever stops reproducing, so
the evidence cannot rot into a stale claim).

**The remedy was measured and exists.** While `alternate_on=1`, tmux's
`capture-pane -a` returns the **saved main screen**, and plain `capture-pane`
returns the alternate grid — both asserted in the same test. A seed transaction
that captured both, and seeded the main screen before switching to the
alternate, would converge. That is a change to the slice-1 seed transaction and a
new `Seed` field, so it is **slice 3 design work and was deliberately not done
here** — doing it would have replaced the measurement with an assumption.

### 4.3 Absolute history size is unreliable ⚠️ GATE

Two distinct defects, both visible in the matrix:

1. **Frozen at seed value on the alternate screen** — the model reported
   `HistorySize = 1` where tmux reported 202 for the same pane, because
   `frame()` used `seedHistorySize` alone on the alternate screen and dropped
   everything that had scrolled off since the seed. **Fixed here** (a one-line
   adapter fix inside two declared corpus categories, alt-screen and history):
   the alternate-screen branch now reports `seedHistorySize + scrolledOff()`,
   frozen at the moment of the switch, which is what tmux does.
2. **Drift past the emulator's scrollback cap — not fixed.** `scrolledOff()` is
   the delta of the emulator's own bounded scrollback, so once
   `DefaultScrollback` (10 000) is reached the absolute count stops tracking
   tmux's. Under the soak the model reported 10 202 against tmux's ~4 900, on
   **216 of 219** comparisons. Slice 0 recorded this as a known limitation; this
   is the first time it has been seen to dominate a real session. It needs real
   absolute-coordinate tracking, not a constant, and it is what the viewport's
   lazy-older-history requests are addressed in.

Classified as `adapter-absolute-history-drift` rather than "unexplained", because
the cause is understood — but it is **Sidecar's defect, not an upstream one, and
it is gate-blocking**.

### 4.4 The alternate-screen frame omits loaded history (known, not fixed)

On the alternate screen the model's `Frame.Output` is the alternate grid only,
while tmux's capture still returns the main screen's loaded history above it. 27
cells, classified `adapter-alt-screen-history-not-rendered`. Under authority this
would change what the user sees (the scrollback above a full-screen application
would disappear), so it must be settled in slice 3. Not fixed here: changing the
frame's shape is beyond this slice's mandate.

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

Four cells across the matrix, all `gap-6/9-grapheme-cluster`: NFD text (`e` +
U+0301) rendered by the model as two cells where tmux renders one. Confirms
slice 0's GAP-6 is reachable in ordinary use, not only in a fixture. GAP-9
(cluster split across a `Write` boundary) was **not** observed in the matrix,
which is expected — the classifier cannot distinguish it from GAP-6, and the
matrix's Unicode content was small.

---

## 5. Decision-gate criteria

| # | Criterion | Result | Evidence |
| --- | --- | --- | --- |
| 1 | Deterministic fixtures have zero cell, attribute, cursor, mode, history or split-boundary mismatches | **FAIL as written / PASS as "no unexplained"** — 24 fixtures pass; every remaining mismatch is a named upstream gap (GAP-1…9) with a minimal reproducer, and no new one appeared. GAP-9 still blocks authority. | §3, `go test ./internal/tty/screenmodel` |
| 2 | Common real applications have zero unexplained steady-state mismatches and no persistent mismatch after supported seed/resync events | **FAIL.** 1 045 unexplained cells over 268 comparisons; 109 of 268 comparisons carried at least one mismatch. Root causes are §4.2 (dominant, persistent) and §4.3. zsh alone is clean apart from GAP-6. | §4.2–4.3, matrix table below |
| 3 | Attach/restart converge without injecting keystrokes, signals or fake resizes | **FAIL.** Attach on the main screen converges (attach-switch-restart: 141/168 clean, the remainder history-size drift). Attach while an alternate-screen application is running **never** converges. Remedy measured and available (`capture-pane -a`), not implemented. | §4.2, `TestAltScreenAttachCannotRestoreTheMainScreen` |
| 4 | The ordered barrier and `client_discarded` handling prove no duplicated or omitted bytes under sustained output and `pause-after` | **PASS.** 132 236 bursts / 37.5 MB in the soak: `discarded_bytes = 0`, `seed_races = 0`, `comparisons_in_open_discard_window = 0`, `comparisons_with_capture_metadata_race = 0`. Slice 1's six byte-continuity scenarios still pass. | §6.3, slice-1 evidence §1.2 |
| 5 | Steady-state model delivery performs zero `capture-pane` and `display-message` per output burst; one seed/resync transaction is the expected exception | **PASS.** 268 of 268 capture transactions ran while a live model already held the screen, i.e. 0.000 captures per burst would remain; the only remainder is 10 seed transactions and a 1 s `client_discarded` cadence probe (not per burst). | matrix "Commands" table |
| 6 | Replay work and allocations scale with the byte delta/current grid, not the 600-line capture window; total memory bounded under a sustained soak | **PARTIAL FAIL.** Memory: **PASS** — flat at 51.3 MB estimated across the whole soak, no growth. Work: **FAIL** — `Write` scales with the delta, `Frame()` scales with the capture window (861 µs, 470 KB per frame at 600 lines). | §4.5, §6.3 |
| 7 | End-to-end output latency and CPU improve over the in-band capture baseline, with no startup or idle regression | **PASS on latency, UNMEASURED on CPU.** Soak: output→frame 22.6 ms mean / 97.8 ms max against output→capture 95.2 ms mean / 273.2 ms max — same bytes, same process, same coalescing tick. **CPU under byte-fed authority cannot be measured while capture is authoritative** (see §7). No startup or idle path is touched with the variable unset. | §6.3 |
| 8 | Removing capture authority will eventually delete mouse-fragment regexes, cursor/mouse metadata queries and capture-on-burst code rather than leaving two permanent renderers | **PASS on the transport, NOT YET on the surfaces.** The model already supplies cursor, mouse modes and alternate-screen state in the same ordered stream, so `display-message` per burst becomes deletable (criterion 5). But §4.4 (alt-screen history) and §4.5 (frame shape) mean the model's frame is not yet a drop-in for `ControlSnapshot.Output`, so today it would be a second renderer, not a replacement. | §4.4–4.5 |

**Three fails, one partial fail, four passes.** Criteria 2 and 3 share a single
root cause (§4.2) with a measured remedy; criterion 6's work half and criterion 8
share another (§4.5). That is why the recommendation is *hold*, not *reopen
Herdr*: this is a short, named list, not an open-ended patch layer.

---

## 6. Measurements

### 6.1 Real application matrix

Availability recorded honestly; no synthetic program was substituted for any
application.

| Program | Resolved | Exercised |
| --- | --- | --- |
| `zsh` | `/bin/zsh` (run as `zsh -f`) | yes |
| `nvim` | `/opt/homebrew/bin/nvim` | yes |
| `vim` | not installed (`vim` is an alias to `nvim`, not on `PATH` for `exec.LookPath`) | n/a — nvim covered it |
| `less` | `/usr/bin/less` | yes |
| `top` | `/usr/bin/top` | yes (continuously updating) |
| `fzf` | `/opt/homebrew/bin/fzf` | yes (mouse-aware TUI) |
| `claude` | `/Users/marcus/.local/bin/claude` | **NOT RUN** — see below |
| `codex` | `/Users/marcus/.local/bin/codex` | **NOT RUN** — see below |
| `htop`, `btm`, `aider`, `lazygit`, `glow` | not installed | not run |

**The agent-TUI row of the plan's matrix is not covered.** Both supported agent
CLIs are installed, but starting either needs the developer's real credentials
and network access; running them under the isolated `HOME` the harness requires
would exercise an authentication screen, not an agent's idle/streaming/approval/
interrupt/completion transitions. The harness has the scenario written and gated
behind `SIDECAR_SCREENCOMPARE_AGENT=1`; it was not enabled. **No synthetic
full-screen program was substituted.** This is a real hole in the evidence and it
is a slice-3 blocker in its own right: the agent TUIs are the surface slice 4
targets, they emit the heaviest and most unusual escape traffic of anything
Sidecar hosts, and nothing here says how the model handles them.

| Scenario | Comparisons | Clean | With mismatch | Unexplained cells | Known-gap cells | Seeds |
| --- | --- | --- | --- | --- | --- | --- |
| zsh prompt editing, multiline, completion, wrapped/coloured/wide output | 21 | 18 | 3 | 0 | 3 | 0 |
| nvim: alt screen, insert, `:vsplit`, search, live resize, quit | 15 | 0 | 15 | 182 | 14 | 6 |
| `less`: paging, search, quit | 7 | 0 | 7 | 414 | 5 | 0 |
| `top -l 0 -s 1`: continuous repaint | 21 | 0 | 21 | 448 | 14 | 0 |
| `fzf`: mouse modes on and off | 8 | 0 | 8 | 1 | 13 | 0 |
| alternate-screen cycling ×4, then restored shell history | 28 | 0 | 28 | 0 | 32 | 0 |
| agent TUI | _not run — see above_ | | | | | |
| attach, hide/show, drop and recreate the subscription | 168 | 141 | 27 | 0 | 27 | 4 |
| forced control-client failure and fallback | 0 | 0 | 0 | 0 | 0 | 0 |
| **TOTAL** | **268** | **159** | **109** | **1 045** | — | **10** |

Aggregate classes: `unexplained` 1 045 (all §4.2),
`adapter-absolute-history-drift` 77, `adapter-alt-screen-history-not-rendered`
27, `gap-6/9-grapheme-cluster` 4. Signatures: `cell/grapheme` 1 019,
`history/size` 90, `history/rows` 32, `cursor/position` 10, `history/grapheme` 2.
Resyncs: first-seed 2, layout 6, resize 2. Zero faults, zero discarded bytes,
one fallback (the deliberately forced one), zero open discard windows, zero
capture-metadata races.

Commands: 2 231 `%output` bursts, 1 849 478 raw bytes, 268 `capture-pane`, 268
`display-message`, 268 captures avoidable, 10 seed transactions, 278 model frames.
26 221 cells declined as uncomparable (§3.1).

### 6.2 The real Sidecar, driven headlessly

`scripts/tmux-drive.sh`, both axes isolated (§2), same binary, same key
sequence, run twice — once with `SIDECAR_TMUX_SCREEN_COMPARE` unset and once with
it set to 1. The sequence reaches the workspaces plugin, opens a shell
(`ctrl+n`), enters interactive mode (`E`), runs a marker script producing
`SHADOWPROOF` lines, `seq 1 60` and coloured/bold text, then opens and quits
nvim.

**The rendered Sidecar screens are identical.** Diffing the two captures after
stripping SGR leaves only the wall clock and the git diff-stat line (the working
tree changed between the two runs). The whole terminal output region — the marker
lines, the wrapped sequence, the colours — is byte-identical. `panes` reports the
same pane geometry, cursor and history size in both runs.

The shadow run's JSON report (50 comparisons, from the real app rather than the
harness):

```json
seeds: 5,  resyncs: {first-seed: 1, layout: 3, resize: 1}
raw_events: 154,  raw_bytes: 3670,  model_frames: 50
captures: 50,  metadata_queries: 50,  captures_while_model_live: 50, seed_captures: 5
comparisons: 50,  comparisons_clean: 49,  frames_with_mismatch: 1
mismatches_by_class: {gap-8-ris-history: 1, unexplained: 1}
mismatches_by_signature: {history/rows: 1, history/size: 1}
discarded_bytes: 0, faults: 0, fallbacks: 0, model_bytes_peak: 504832
```

49 of 50 clean in the real application; the single mismatch is the
alternate-screen history pair from §4.3/§4.4 at the nvim transition. The report
was grepped for the pane's content, the host name and the home directory:
**zero hits**.

### 6.3 Sustained-output soak

24 s of continuous fast output through the real transport:

| Measure | Value |
| --- | --- |
| `%output` bursts / bytes | 132 236 / 37 513 032 |
| comparisons / clean / mismatched | 219 / 1 / 218 |
| mismatch classes | `adapter-absolute-history-drift` 216, `unexplained` 2 (all `history/size`) |
| discarded bytes | **0** |
| captures / metadata queries / seeds | 219 / 219 / 0 — all 219 avoidable under authority |
| retained model memory (2 s samples, 24 s) | 51 322 880 bytes, **flat**, no growth |
| output → model frame | **22 550 µs mean, 97 790 µs max** |
| output → capture snapshot (baseline) | **95 240 µs mean, 273 224 µs max** |
| model write / model render / shadow compare | 92 µs / 8 441 µs / 4 125 µs mean |

Every cell mismatch in the soak is `history/size`: the *content* never diverged
across 37.5 MB of continuous output. That is the strongest single fidelity result
in the slice.

Two cautions on the memory number. It is an **estimate**
(`Model.Footprint()`: rows × columns × a fixed 64 B weight), not a heap profile —
it proves boundedness and shape, not an exact figure. And 51 MB per pane is
large: it is the emulator's default 10 000-line scrollback, against a 600-line
capture window Sidecar actually uses. The real Sidecar run measured 505 KB for a
116×44 pane that had not yet filled its scrollback. Sizing the model's scrollback
to what Sidecar needs is a slice-3 item.

---

## 7. Where this evidence is weaker than it looks

Stated plainly, because an overstated pass is the worst outcome of this slice.

1. **CPU is not measured.** The gate asks whether CPU improves over the capture
   baseline. It cannot be answered while `capture-pane` is authoritative: shadow
   mode runs *both* paths, so a process-level measurement is (capture + model +
   comparison), not (model). The per-burst benchmarks in §4.5 isolate Sidecar's
   own work on each path, and the latency figures in §6.3 include tmux's side —
   but no number here is "CPU under byte-fed authority". Only slice 3 can produce
   that.
2. **The agent-TUI row is missing entirely** (§6.1). It is the surface with the
   heaviest escape traffic and the one slice 4 targets.
3. **26 221 cell comparisons were declined as uncomparable** (§3.1), all of them
   trailing blanks in full-screen applications. "159 clean comparisons" therefore
   means "clean over the cells `capture-pane -e` can describe". Background
   styling of blank regions in nvim/fzf/top is genuinely untested.
4. **GAP-9 was not exercised.** The matrix's Unicode content is a handful of
   cells, and the classifier cannot separate GAP-6 from GAP-9. The slice-1
   precondition (a cluster split across an `%output` boundary) remains a
   theoretical exposure with no live measurement.
5. **The mismatch counts are cell counts, and cells are correlated.** One wrong
   screen contributes hundreds. `FramesWithMismatch` (109 of 268) is the honest
   denominator for "how often was the model wrong"; the cell totals say "how
   badly", not "how often".
6. **One platform, one tmux, one machine.** darwin/arm64, tmux 3.6b. Slice 1's
   ordering guarantee is still empirical rather than documented; nothing here
   strengthens it.
7. **The scenarios are scripted key sequences, not a human.** They are
   deterministic enough to compare across runs and short enough to be honest
   about — a few seconds per application, not a full working session. Run-to-run
   totals varied by roughly ±5 % across three matrix runs.
8. **`forced-control-failure` produced zero comparisons.** It proves the fallback
   fires (asserted), but it contributes no fidelity evidence, and the model's
   behavior across a control-client death is covered only by slice 1's
   integration tests.
9. **The comparison itself is expensive** (4.1 ms mean, 954 KB per call). It is
   diagnostic-only and excluded from every model-path number, but it does perturb
   the process it measures: shadow mode makes Sidecar do meaningfully more work
   than either path alone, which is one more reason §6.2's identity result is
   about *what is rendered*, not about timing.
10. **`Model.Footprint()` is an accounting estimate, not a measurement** (§6.3).
11. **The slice-1 lock-order risk was reduced, not eliminated.** `ControlManager`
    no longer calls the blocking `post()` while holding its mutex — the model
    start moved to `startModel` after the unlock, and `startModelFeed`
    revalidates the subscription to cover the window that opens. Slice-1
    evidence §9 item 8 is closed. Item 7 (a control client whose tmux dies with a
    saturated event channel can freeze with no fallback, `td-58cebc`) is **not**
    fixed and is now reachable whenever shadow mode is on.

---

## 8. Recommended handling

Ordered by what the gate needs, not by size.

1. **Blocking the gate, Sidecar-side:** seed the main screen from
   `capture-pane -a` when `alternate_on=1` (§4.2); real absolute history-size
   tracking (§4.3.2); decide the alternate-screen frame's history shape (§4.4);
   make frame publication incremental (§4.5).
2. **Blocking the gate, upstream:** GAP-9 (grapheme across `Write`) and GAP-7
   (no cursor-visibility getter) as slice 0 recorded; GAP-3/GAP-4 (OSC 8) and
   GAP-6 (NFD), the last of which now has live evidence.
3. **Blocking the gate, evidence:** run the agent-TUI matrix row deliberately
   (§6.1), and measure CPU once a byte-fed surface exists (§7.1).
4. **Also worth upstreaming:** GAP-10's underlying design — an emulator that
   deadlocks when its reply stream is not drained is a sharp edge for every
   caller, and a small internal buffer or a documented contract would remove it.
5. **Carry forward:** `td-58cebc` (§7.11) must be fixed before any surface is
   enabled.

Nothing in this slice justifies vendoring or forking the emulator, and no
application-specific escape repair exists anywhere in
`internal/tty/screenmodel` — that remains zero lines.

---

## 9. Reproducing this

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

## 10. Verification

```text
go build ./...                                   ok
go vet ./...                                     ok
go test ./...                                    ok (all packages)
go test -race ./internal/tty/...                 ok — tty 26.5s, screenmodel 4.0s
go test -race ./internal/tty -screencompare      ok — matrix + soak + alt-screen finding, 74.2s
git diff --check                                 clean
```

The developer's default tmux server was listed before and after every run in this
slice: 20 sessions before, 20 sessions after, unchanged.
