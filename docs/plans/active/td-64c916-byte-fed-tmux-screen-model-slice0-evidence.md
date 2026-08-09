# Slice 0 evidence: qualify `x/vt` and the tmux oracle (td-b1aca1)

**Plan:** [td-64c916 — Byte-fed tmux screen model](./td-64c916-byte-fed-tmux-screen-model.md)
**Slice:** 0 — qualify the dependency and oracle
**Recorded:** 2026-08-08 · macOS (darwin/arm64) · tmux 3.6b · Go 1.26.5

## Verdict

**Proceed to slice 1.** The dependency integrates cleanly, the deterministic byte
corpus reproduces tmux's screen exactly for the ordinary cases, and every
divergence found is a small, individually-nameable defect with a minimal
reproducing byte sequence. None of them requires a broad or unstable patch layer
inside Sidecar, so the plan's no-go condition is not met.

Two caveats the slice 1 owner must carry forward:

- **GAP-9 (grapheme clusters do not survive a `Write` boundary) is a blocker for
  authority, not for slice 1.** Pane bytes arrive from tmux in arbitrary chunks,
  so this defect is reachable in production the moment the model becomes
  authoritative, and it corrupts the **cursor position** as well as the cells.
  It is *not* a mechanical fix like the others: buffering the pending cluster
  across `Write` calls means a cluster that arrives as the last bytes of a burst
  is invisible until more bytes arrive, so upstream has to choose a flush policy
  and Sidecar's adapter has to honour it. That is a semantic design decision —
  see GAP-9 below. It must be settled upstream (or the pin moved to a commit
  that contains the fix) before slice 3 enables any surface.
- **GAP-7 has no adapter-side answer at all.** `vt.Emulator` exposes cursor
  visibility only as a change callback and offers no getter, so any mirror
  desyncs on RIS. This needs an upstream accessor; it cannot be worked around
  without scanning bytes in Sidecar, which the plan forbids.

Nothing in this slice touches user-visible behavior: there is no UI consumer, no
feature flag, and no change to the existing capture path.

## 1. Dependency integration

| Item | Result |
| --- | --- |
| Module | `github.com/charmbracelet/x/vt` |
| Pin | `v0.0.0-20260803091719-3755ebad01b1` (commit `3755ebad01b1366a9eeb5e4e80d664b404ab6eff`) |
| License | MIT, © 2023 Charmbracelet, Inc. Same license and copyright holder as the `x/ansi`, `x/cellbuf`, and `ultraviolet` modules Sidecar already ships. No new license obligation. |
| Declared Go version | `go 1.24.2` — below Sidecar's `go 1.25.8` directive and the local Go 1.26.5 toolchain. No `toolchain` line, no build constraints requiring a newer Go. |
| `ultraviolet` conflict | `x/vt` declares `ultraviolet v0.0.0-20260303162955-0b88c25f3fff`; Sidecar selects the newer `v0.0.0-20260525132238-948f4557a654`. MVS keeps **Sidecar's** version and `x/vt` compiles and passes against it — see below. |
| Other version movement | None. `go mod tidy` promoted `rivo/uniseg` from indirect to direct (the fidelity oracle uses it) and added no other module. `ultraviolet`, `x/ansi`, `colorprofile`, and every existing dependency keep their previous versions. |

Commands run and their results:

```text
go build github.com/charmbracelet/x/vt        # ok — compiles against Sidecar's newer ultraviolet
go test  github.com/charmbracelet/x/vt        # ok  0.241s — upstream suite green under Sidecar's module graph
go test -race github.com/charmbracelet/x/vt   # ok  1.271s
go build ./...                                # ok
go test ./...                                 # ok (all packages)
go test -race ./internal/tty/...              # ok — internal/tty 2.87s, internal/tty/screenmodel 2.74s
go vet ./internal/tty/...                     # ok
git diff --check                              # clean
```

The important qualification result is the third row of the table: the older
`ultraviolet` pseudo-version `x/vt` declares is not what gets built. Running
`x/vt`'s own suite from inside Sidecar's module is what proves the upgrade does
not break it, and it passes with the race detector on.

## 2. What was built

`internal/tty/screenmodel` — the only package permitted to import `x/vt`.

- `model.go` — `Seed`/`Write`/`Resize`/`Frame`/`Close` per plan §1, plus the
  three required invariants:
  - **Single actor.** `Model` does not serialize; it *detects* a violation. A
    concurrent entry returns `ErrConcurrentUse` instead of interleaving with
    emulator state. An emulator panic becomes a sticky `ErrModelFault` rather
    than escaping into the control reader or the Bubble Tea loop.
  - **`ControlSnapshot`-shaped frames.** `Frame` carries `Output`,
    `CaptureBase`, `HistorySize`, `HasHistory`, `Width`/`Height`, cursor
    row/col/visibility, `AltScreen`, and mouse state — the fields
    `tty.ControlSnapshot` already has, so the viewport/history/search/selection
    journey needs no change.
  - **No dependency types leak.** Colors are canonical strings, attributes are
    Sidecar's own bitmask, modes are plain bools. No `vt.Emulator`, `uv.Cell`,
    or `ansi.Mode` value crosses the package boundary. "Only this package may
    import `x/vt`" is enforced by a test (`TestOnlyThisPackageImportsVT`, a
    `go list` sweep of every package's imports and test imports), not by the
    package comment alone — slices 1–5 add consumers at exactly that boundary.
  - **`Close` is release, not an operation.** It deliberately bypasses the
    single-actor wrapper's sticky-error check: a fault is the state a consumer
    closes *from*, so routing `Close` through the same guard as `Write` would
    mean a faulted pane — the fallback path production actually takes — never
    released its emulator's pipe or buffers. It is idempotent and terminal from
    any state, faulted included.
- `cell.go` — canonical `Cell`/`Grid` and colour normalization.
- `compare.go` — the canonical cell comparator. It compares grapheme, cell
  width, fg/bg/underline colour, underline style, attributes, and hyperlink. It
  never compares rendered string spelling. Cursor (`CompareCursor`) and modes
  (`CompareModes`) are separate assertions, as the plan requires.

Two refinements to the plan's sketch, both additive:

- `Frame` gained `Cells Grid` (the canonical grid the harness and future shadow
  mode compare), `HasHistory`, `CursorStyle`, and `BracketedPaste`. The last two
  are model-only diagnostics; tmux exposes no format for either, and per plan §4
  tmux — not this model — stays the authority for paste.
- Invalid geometry returns a non-sticky `ErrInvalidGeometry` rather than a
  fault, so a seed can be retried instead of forcing a fallback.

## 3. The harness

**Recorder** (`record_test.go`, run with `-record`). Starts a throwaway tmux
server, writes each fixture's bytes into a pane, and records `capture-pane -p -e`
plus `cursor_x`, `cursor_y`, `cursor_flag`, `alternate_on`, `mouse_any_flag`,
`history_size`, `pane_width`, `pane_height`.

Isolation: every invocation passes an explicit `-S <socket>` inside the test's
own `t.TempDir()`, the path is asserted to be under that directory before any
command runs, `TMUX` is cleared from the child environment, and teardown targets
that same explicit socket path. The default tmux server is never contacted.

Two recording details that matter for fidelity:

- The pane driver runs `stty -opost -echo` first. Left alone, the pty's ONLCR
  would turn every LF in the corpus into CR LF and silently rewrite the LF-only
  fixtures.
- tmux processes the pty asynchronously, so the recorder waits for the rendered
  pane *and* its metadata to be byte-identical across three consecutive polls
  before capturing.

**Oracle decoder** (`capture_oracle_test.go`, test-only). Turns `capture-pane -e`
back into canonical cells with its own escape scanner and SGR interpreter, and
uses `rivo/uniseg` for grapheme segmentation. Decoding the capture with the
emulator under test would be circular; a hand-written decoder that shares no
code with `x/vt` is what makes an SGR or link defect visible. Two behaviors of
tmux's capture format had to be discovered empirically and are encoded there:
a literal TAB means "advance to the next 8-column stop", and the SGR pen
**carries across line boundaries** rather than resetting per line.

**Fixtures**: 23 generated JSON files in `testdata/corpus/`, 92 KB total, no
personal data (a test asserts no home directory, user name, or host name appears
in any capture). Each fixture stores a SHA-256 fingerprint of its input; a
corpus edit without a re-record fails the run instead of comparing against a
stale oracle.

**Replay modes**, all three run on every fixture:

1. whole — one `Write` per corpus step;
2. every split boundary — each step split at every byte offset, compared with
   the whole-write result;
3. byte-at-a-time — one `Write` per byte.

Plus two assertions the plan did not require:

4. **seed round trip** — feed each recorded capture back in as a `Seed` and
   require the model to reconstruct the same cells and cursor. The attach story
   depends on it.
5. **`Frame.Output` fidelity** (`TestFrameOutputRendersTheFrame`) — `Output` is
   the field that becomes `tty.ControlSnapshot.Output`, so it is the only part
   of a frame today's viewport, search, and selection journey actually reads,
   and it is held to its own claim rather than smoke-tested. Three parts: its
   **shape** (one line per loaded scrolled-off row, then exactly `Height` live
   rows; `Height` rows and no history on the alternate screen), its **spelling**
   (decoding the visible rows with the independent capture decoder must
   reproduce the frame's own canonical cells), and its **fixed-point** property
   (re-seeding from those rows must give the same cells again, which is exactly
   what reattach does). Shape and spelling are exact for all 23 fixtures. The
   fixed point is exact for all but the three OSC 8 fixtures, where **GAP-3
   reaches the `Output` path** — see GAP-3.

Known gaps are declared per fixture as *signature sets* and asserted to match
exactly. A new mismatch fails, and so does a gap that stops reproducing — an
upstream fix cannot quietly sit behind an excuse.

## 4. Per-category results

`crlf/tab/backspace/wrap/phantom`, `cursor motion`, `erase/insert/delete`,
`scroll regions + origin`, `alt screen`, `sgr colors/attrs`, `unicode`,
`modes/cursor/paste/sync/reset`, `resize`, `osc8` — every fixture in the table
below is exact except where a GAP is named.

| Category | Fixture | Whole | Split / byte-at-a-time | Seed round trip |
| --- | --- | --- | --- | --- |
| CR/LF, tabs, backspace, bare LF | `controls_crlf_tab_backspace` | exact | exact | exact |
| Autowrap + phantom column, DECAWM off | `autowrap_phantom_column` | exact | exact | exact |
| Relative/absolute motion, clamping | `cursor_motion` | exact | exact | exact |
| Save/restore cursor and attributes | `save_restore_cursor_and_attrs` | **GAP-1** | exact | exact |
| ED/EL/ECH/ICH/DCH | `erase_insert_delete` | exact | exact | exact |
| IL/DL | `insert_delete_lines` | exact | exact | exact |
| DECSTBM, SU/SD, DECOM | `scroll_region_and_origin` | exact | exact | exact |
| Alt screen enter/exit ×2 | `alt_screen_transitions` | exact | exact | exact |
| Alt screen **still active at capture** | `alt_screen_active` | exact | exact | exact |
| SGR reset, 16 colour, bright | `sgr_basic_and_bright` | exact | exact | exact |
| SGR 256 + truecolor | `sgr_256_and_truecolor` | exact | exact | exact |
| Underline styles + underline colour | `sgr_underline_styles` | **GAP-2** | exact | exact |
| Inverse, dim, hidden, italic, blink, strike | `sgr_attributes` | exact | exact | exact |
| OSC 8 links, incl. `id=` | `osc8_links` | **GAP-3** | exact | **GAP-3** |
| OSC 8 hostile/nested termination | `osc8_hostile_termination` | **GAP-3, GAP-4** | exact | GAP-3/4 |
| OSC 8 raw C1 ST | `osc8_c1_st_terminator` | **GAP-5** | exact | exact² |
| CJK, combining, emoji, VS, ZWJ | `unicode_wide_combining_emoji` | **GAP-6** | **GAP-9** | GAP-6 |
| Mouse modes, cursor style, paste, sync | `modes_cursor_paste_sync_reset` | exact | exact | exact |
| DECSTR soft reset | `soft_reset_decstr` | exact | exact | exact |
| RIS hard reset | `terminal_reset` | **GAP-7, GAP-8** | exact | exact |
| Resize wider then narrower | `resize_wider_then_narrower` | by design¹ | exact | exact |
| Resize taller then shorter | `resize_taller_then_shorter` | by design¹ | exact | exact |
| History scroll-off | `history_scrolloff` | exact | exact | exact |

¹ Not a defect — see "Resize" below.

² Exact against the tmux capture, which carries no link here; the `Frame.Output`
fixed point still shows GAP-3 on this fixture because `x/vt` did store a
(swapped) link of its own.

`alt_screen_active` is the fixture that makes the alternate screen a tested
state rather than a tested *transition*. Every other fixture, including
`alt_screen_transitions`, is back on the main screen by the time the recorder
captures, so `alternate_on` is `false` on both sides and the mode assertion
never sees a true value. This one paints a full-screen TUI — clear, hidden
cursor, mouse tracking on, a reverse-video status bar spanning the full width,
256-colour and truecolour runs, an underline run, and a wide-CJK row — and
**never leaves the alternate screen**, so tmux records `alternate_on=1`,
`mouse_any_flag=1`, `cursor_flag=0`, a frozen `history_size`, and the alternate
buffer's own cells. The comparator runs whole, at every split boundary,
byte-at-a-time, through the seed round trip and through `Frame.Output` against
it. All exact: **the alternate screen revealed no new emulator gap.** That
matters because it is where every full-screen TUI in the slice 2 matrix lives.

Split-boundary replay is exact for **every fixture except the Unicode one**.
A targeted test (`TestSplitUTF8AndCSISurviveWriteBoundaries`) pins the safe
classes down separately, since the fixture-level allowance is fixture-wide:
splitting a multi-byte rune, a CSI sequence, an SGR run, an OSC 8 link, or a
DECSET toggle at *every* byte offset is invisible. Only multi-rune grapheme
clusters are not (GAP-9), and there the damage is cells **and cursor** — the
Unicode fixture's final row is deliberately left unterminated so the cursor
effect is observable at all.

Seed round trip is exact for **every fixture except the two that hit an OSC 8
defect and the NFD one**. Specifically, it reconstructs from `capture-pane -e`
plus tmux metadata:

- a pane **on the alternate screen**, with the alternate buffer's own cells,
  hidden cursor and mouse tracking restored (`alt_screen_active`) — before this
  fixture existed no seed test had ever been given `alternate_on=1`;
- non-default scroll margins, a hidden cursor, and post-resize geometry.

That is the strongest single result in this slice: reconstructing a running pane
from a capture plus metadata works, including the state every full-screen TUI
runs in.

### Not covered here, and why

The plan's `control payload octal escapes, long notifications, pause/continue,
dead control connection` bullet has no fixture. Those are properties of the
control protocol, not of the screen model — there is no pane byte stream to
record for them. They are slice 1's ordered-barrier tests. This omission is
asserted in code (`TestCorpusCoversPlanCategories` requires a written reason for
any uncovered category), not just stated here.

Bracketed-paste and SGR-mouse-encoding state are tracked by the model but not
compared against tmux: tmux exposes no format for either. `alternate_on` and
`mouse_any_flag` are compared.

Wide-character *column placement* is a shared-assumption area: `capture-pane`
emits a wide character once with no padding, so the decoder must apply a width
table to lay cells out, and no width table available here is fully independent
of the one `x/vt` uses. tmux's own column arithmetic is observed independently
through the `cursor_x` assertion, which is exact on every Unicode fixture.

## 5. Emulator gaps

Each gap below is bounded: a named handler or a few lines, with a minimal
reproducer. None needs a Sidecar-side patch layer, and none was patched around.

### GAP-1 — `CSI u` (SCORC) is unimplemented

`x/vt` registers a `CSI s` handler (SCOSC, saving the cursor) but registers no
`'u'` handler at all, so the matching restore is silently dropped and everything
written afterwards lands at the wrong position.

```text
write: ESC[6;1H ESC[s ESC[1;10H "moved" ESC[u "back"
tmux:  "back" at row 5, col 0
x/vt:  "back" at row 0, col 14 (cursor never restored)
```

Fix: register a `'u'` handler calling `e.scr.RestoreCursor()`, mirroring the
existing `'s'` handler. `handlers.go`, next to line 888.

### GAP-2 — SGR 21 is not double underline

`ultraviolet`'s `ReadStyle` has no `case 21`, so `CSI 21 m` is ignored. tmux
renders it as `4:2`.

```text
write: ESC[21m "double"
tmux:  underline=double
x/vt:  underline=none
```

Fix: one `case 21:` in `ultraviolet/styled.go` setting `UnderlineDouble`.

### GAP-3 — OSC 8 URL and params are swapped

`osc.go:127` splits `8;params;uri` into three fields and then assigns
`Link.URL = parts[1]` (the params) and `Link.Params = parts[2]` (the URI).

```text
write: ESC]8;id=xyz;https://example.com/b ESC\ "linkB"
tmux:  url="https://example.com/b" params="id=xyz"
x/vt:  url="id=xyz"                params="https://example.com/b"
```

Fix: swap the two assignments. This is the defect most likely to matter to
Sidecar users, since the terminal's link handling reads the URL.

**It reaches `Frame.Output`, not just `Frame.Cells.`** `ultraviolet`'s renderer
spells the link out *correctly* as `OSC 8;<Params>;<URL>` from whatever cell it
is handed — so the swapped cell is rendered as a well-formed link with the two
fields exchanged, and the harness's independent decoder reads `Output` back
exactly as `Cells` holds it (the "spelling" assertion is exact everywhere,
including here). The damage shows up on the **fixed point**: feeding `Output`
back through `Seed` runs it through the same defective handler again, swapping a
second time — 42 mismatched cells on `osc8_links` alone. Since `Frame.Output` is
what becomes `ControlSnapshot.Output`, any consumer that round trips it sees the
URL and the params exchanged. Declared per fixture as `KnownOutputGaps` and
asserted as an exact signature set, so the upstream fix will fail the test and
force this paragraph to be deleted.

### GAP-4 — OSC 8 payloads with extra `;` are dropped

The same handler requires exactly three semicolon-separated fields, so any URI
containing a `;` loses its link entirely. It also discards an OSC abandoned by
CAN (0x18) where tmux keeps the link it had already parsed.

```text
write: ESC]8;;https://example.com/f?a=1;b=2 ESC\ "L3"
tmux:  url="https://example.com/f?a=1;b=2"
x/vt:  no link
```

Fix: split on the first two `;` only (`bytes.SplitN(data, ';', 3)`).

### GAP-5 — raw C1 `ST` (0x9C) accepted inside a UTF-8 stream

`x/vt` terminates a string on a raw 0x9C byte. tmux 3.6 in UTF-8 mode does not —
0x9C is not valid UTF-8 there — and keeps consuming the OSC. Recorded as a
divergence rather than a defect claim: which behavior is "right" is arguable,
and Sidecar's own OSC sanitizer already treats every C1 form as hostile.
Documented so slice 2 does not rediscover it as noise.

### GAP-6 — combining marks after an ASCII base character are lost

`handlePrint` commits every printable ASCII rune as its own grapheme
immediately, so a following combining mark can never attach to it. NFD text —
which is what macOS filesystems and many inputs produce — renders wrong.

```text
write: "e" U+0301 (NFD "é")
tmux:  "é" in one cell
x/vt:  "e" in one cell; the mark is emitted separately
```

Fix: do not fast-path ASCII into `handleGrapheme` when the next rune may
continue the cluster — i.e. buffer it like any other base character. Related to
GAP-9 and probably the same fix.

### GAP-7 — cursor visibility is unreadable after a reset

`Emulator` reports cursor visibility only through the `CursorVisibility`
callback, and only *on change*. `RIS` calls `Screen.Reset()`, which assigns
`Cursor{}` directly; the later `resetModes()` then sees no change and fires no
callback. An adapter mirror is left reporting "hidden" while the emulator
considers the cursor visible. There is no exported getter to re-read it from —
`Screen.Cursor()` is exported but `Emulator.scr` is not.

```text
write: ESC[?25l ... ESC c
tmux:  cursor_flag=1
model: CursorVisible=false
```

Fix (upstream, required): add `Emulator.CursorVisible() bool` (or
`Emulator.Cursor() Cursor`), or fire the visibility callback unconditionally
from `fullReset`. **No adapter-side workaround exists** that does not involve
Sidecar scanning the byte stream for `ESC c`, which the plan forbids.

### GAP-8 — `RIS` discards the screen instead of pushing it to history

tmux pushes the cleared screen's non-empty lines into history on `RIS`;
`x/vt`'s `fullReset` calls `Screen.Reset()`, which clears the buffer with no
scrollback push.

```text
write: "colored\r\nsecond\r\n" ... ESC c
tmux:  history_size=2
x/vt:  history_size=0
```

Fix: `fullReset` should use the existing `ClearWithScrollback` path for the main
screen. Low severity for Sidecar, since history is reseeded from tmux anyway,
but it is a real divergence and slice 2 would otherwise report it as noise.

### GAP-9 — grapheme clusters do not survive a `Write` boundary ⚠️

`Emulator.Write` flushes its pending grapheme buffer when it reaches the end of
the byte slice (`i == len(p)-1`). A cluster split across two `Write` calls is
therefore committed as two separate cells. Pane bytes arrive from tmux in
arbitrary chunks, so this is reachable in production, not a synthetic case.

```text
write, one call:  "❤️ vs ❤"         -> "❤️" as one 2-wide cell
write, split at byte 3: same bytes -> "❤" and the variation selector as
                                      separate 1-wide cells, shifting the
                                      rest of the row
```

The same happens to ZWJ emoji (`👩‍💻` becomes `👩` plus the rest) at every
split offset inside the cluster, and under byte-at-a-time replay.

**It corrupts the cursor, not only the cells.** The two wrongly-committed cells
advance the cursor differently from the single cell they should have formed:
splitting `"❤️ vs ❤"` at bytes 3–5 leaves the cursor at column **6** where a
whole write leaves it at column **7**. This was invisible until now because
every step of the `unicode_wide_combining_emoji` fixture ended in `\r\n`, which
parks the cursor at column 0 and hides any column error. The fixture now ends
with a deliberately unterminated row, and `TestCorpusSplitBoundaries` reports
`cursor/position` as a declared GAP-9 signature alongside `cell/grapheme` and
`cell/width`. For a model that is meant to place a *live cursor*, a column error
from an arbitrary chunk boundary is at least as serious as the cell damage.

**This is the one gap that is not a mechanical fix.** The others are a handler
registration, a `case`, a swapped assignment, a `SplitN`. This one is a semantic
design decision, because buffering the pending cluster across `Write` calls has
a direct consequence: a cluster arriving as the *last* bytes of a burst is no
longer displayed at all until more bytes arrive. Something has to decide when
the buffer is released, and each option costs something:

| Flush policy | Cost |
| --- | --- |
| Explicit `Flush()` | Correct and predictable, but a new API contract: every caller (Sidecar's adapter included) must call it before reading the screen, and one that forgets silently loses the last cluster. |
| Flush on `Render`/`CellAt` | No caller change, but read methods mutate parser state, which is surprising and is a data race waiting to happen for anything reading concurrently. |
| Timer | No API change and no dropped output, but it makes the emulator time-dependent — the same bytes no longer produce the same screen, which would end this harness's determinism. |

Sidecar's adapter has to honour whichever is chosen — most likely by flushing
immediately before building a `Frame`. So the upstream change is small in lines
and non-trivial in contract, and it must be settled before slice 3 makes any
surface authoritative. Treat "~5 lines" as wrong for this gap.

### Non-gap: DECSTR

`x/vt` registers no `CSI ! p` handler — but tmux 3.6 does not act on one either.
Neither clears the scroll region nor restores cursor visibility, and the two
agree exactly. Recorded as a tested observation (`soft_reset_decstr`) so that
"DECSTR is unimplemented" is not carried forward as an untested claim.

### Resize is not a gap

On resize, tmux reflows and rewraps existing rows and pushes the overflow into
history; `x/vt` truncates in place. The two disagree, as the fixtures record.
This is **by design in the plan**, not a defect: plan §3 already requires
resizing tmux, waiting for the authoritative geometry, and reseeding the
emulator. The model is never asked to reproduce tmux's reflow. The seed round
trip for both resize fixtures is exact, which is exactly the property the
reseed-on-resize design depends on.

## 6. Assessment against the slice 0 exit criterion

> **Exit:** the ordinary byte corpus is exact, dependency integration is clean,
> and any remaining gaps are bounded enough to test in the live protocol. A
> broad or unstable patch layer is a no-go.

- **Ordinary corpus exact** — yes. Every non-Unicode, non-OSC-8 fixture matches
  tmux cell-for-cell, including cursor and modes, whole and at every split
  boundary, and reseeds from its own capture exactly.
- **Dependency integration clean** — yes. Builds and passes its own suite under
  Sidecar's newer `ultraviolet`, under Go 1.26.5, with `-race`. MIT, matching
  the Charm modules already vendored. No other module version moved.
- **Gaps bounded** — yes, but not uniformly. Eight of the nine are mechanical:
  a handler registration (GAP-1), a `case` (GAP-2), a swapped assignment
  (GAP-3), a `SplitN` (GAP-4), an accessor (GAP-7). Those are tens of lines
  across `x/vt` and `ultraviolet`. **GAP-9 is in a different class**: the code
  change is small but it forces a flush-policy decision that changes the
  emulator's contract with its callers, and Sidecar's adapter has to be updated
  to match (see GAP-9). It is bounded — it is one decision in one place, not an
  open-ended patch layer — but it is a design question, not a diff. No
  Sidecar-side escape repair was written, and none is needed to proceed.
- **No broad or unstable patch layer** — confirmed. Zero lines of
  application-specific escape handling exist in `internal/tty/screenmodel`.

### Recommended handling

1. **Upstream, blocking slice 3:** GAP-9 (grapheme across writes) and GAP-7
   (no cursor-visibility getter). Neither has an acceptable Sidecar-side
   workaround.
2. **Upstream, blocking the decision gate:** GAP-3 and GAP-4 (OSC 8), because
   Sidecar's terminal surfaces links to users, and GAP-6 (NFD text).
3. **Upstream, nice to have:** GAP-1 (`CSI u`), GAP-2 (SGR 21), GAP-8 (RIS
   history).
4. **No action:** GAP-5 (raw C1 ST) and the resize reflow difference — recorded,
   explained, and handled by the plan's reseed design.

Prefer a newer pinned commit once these land upstream. Nothing here justifies
vendoring or forking the emulator.

## 7. Reproducing this

```bash
# Fidelity suite (no tmux needed; replays the committed fixtures)
go test ./internal/tty/screenmodel

# Re-record the tmux oracle (isolated throwaway server, never the default one)
go test ./internal/tty/screenmodel -run TestRecordCorpus -record

# Full verification
go build ./... && go test ./... && go test -race ./internal/tty/...
```
