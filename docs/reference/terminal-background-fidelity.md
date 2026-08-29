# Terminal background fidelity

How Sidecar decides what colour every cell of an embedded terminal is, why it used to get that wrong, and how to check it is still right.

The one-line rule: **every cell inside a pane's grid takes its colour from tmux, and nothing else.** If you are about to add code that decides a background from anything other than what tmux said, that is the mistake this document exists to stop you repeating.

## How a pane gets drawn

Sidecar does not emulate a terminal for display. It runs a tmux pane, reads it back with `capture-pane`, and re-renders the resulting SGR text into a Bubble Tea view.

```
tmux pane ──capture-pane -p -e -N──▶ tty.OutputBuffer ──▶ termpreview.DrawRows ──▶ rows
```

`DrawRows` (`internal/termpreview/rows.go`) is the single place every rule about what a row looks like lives: carried backgrounds, tab stops, decoration, selection, horizontal offset, truncation, letterboxing and the box fill. Both surfaces that show a terminal bind to it in one file each (`internal/plugins/workspace/terminal_viewport.go`, `internal/overview/preview_tabs.go`). A second answer anywhere else is how the two surfaces come to draw the same buffer differently.

## What `capture-pane` actually tells you

This is the part that was misunderstood for a long time, so it is worth being precise.

tmux emits only SGR *changes* as it walks a row's cells, and it trims each row's trailing blank cells. Three cases, verified against tmux 3.6:

```sh
printf "\033[42mGRN\033[44m\033[K\033[0m\n"   # green text, blue erased to end of line
printf "\033[43mYEL only\033[0m\n"            # yellow text, default after it
printf "AB\033[42m     \033[44m\033[K\033[0m\n" # text, 5 green blanks, blue to EOL
```

```
capture-pane -p -e            capture-pane -p -e -N
\x1b[42mGRN\x1b[44m           \x1b[42mGRN\x1b[44m<spaces to width>
\x1b[43mYEL only\x1b[49m      \x1b[43mYEL only\x1b[49m<spaces to width>
AB\x1b[42m     \x1b[44m       AB\x1b[42m     \x1b[44m<spaces to width>
```

So for any row with at least one non-blank cell, the trimmed capture is **information-complete**: a run that ends mid-row closes with `\x1b[49m`, and a run that reaches the edge is left open. The open SGR *is* tmux telling you the rest of the row is that colour. The long-standing comment claiming "the styling of trailing blanks is unknowable" was too broad.

It is exactly true for one shape: a row with **no** non-blank cells.

```
row of blanks in the carried colour   ->  ""
row of blanks in the terminal default ->  ""
```

Both are the empty string. Codex shows both on one screen (the composer is the first, the separator between two diff blocks is the second), so no rule over the trimmed capture could be right for both. `-N` keeps the blanks and separates them:

```
carried colour   ->  "<spaces to width>"     (state carries from the row above)
terminal default ->  ""                      (nothing was ever written here)
```

`-N` costs roughly a quarter more capture bytes. It does **not** change row counts: trailing blank *rows* are still trimmed, which is why nothing in the viewport, geometry or cursor paths had to move. Do not use `-J`; it joins wrapped lines and drops the trailing SGR entirely, which destroys exactly the information this depends on.

Captures are taken with `-N` at all four call sites (`internal/tty/capture_range.go`, `control_model.go`, `control_manager.go`, `session.go`). `CapturePaneOutput` carries the rationale.

## The two rules that follow

**Carry.** tmux writes one continuous SGR stream, so a row's opening state is whatever the row above left behind. `RowAnalyzer` (`internal/termpreview/row_analysis.go`) resolves that per row and prepends it, bounded by `rowBackgroundLookback` so cost does not scale with history.

**Tail.** A row is drawn out to the pane edge in its own trailing background, then closed. Under `-N` this is usually a no-op because the cells are already there; it still matters after truncation or a horizontal offset. A row the capture spells as nothing at all is left alone, because nothing was written to it.

That is the whole model. There is no step that guesses.

## What was wrong, and what it looked like

Three commits in sequence. Each one is worth understanding as a class of mistake, not just a fix.

### 1. The tail landed one row late

`DrawRows` closed the background at the end of a row's *text*, padded the rest with default, and passed the colour on only as a carry into the next row. So the colour appeared one row below the cells it belonged to.

Visible as: diff hunks stopping mid-row, and a colour bleeding onto the context line under a hunk.

Fixed by rebuilding the trimmed cells from the row's own trailing state before anything else reads the row's shape.

### 2. Blank rows were a coin flip

With the trimmed capture, a wholly blank row is ambiguous (above). Carrying painted every blank separator in a transcript; not carrying left a default stripe through Codex's composer. Fixed by `-N`, which removes the choice rather than improving it.

### 3. The canvas vote

The worst of the three, and the one to actually internalise.

Because the tails were missing, Sidecar compensated with `inferCanvas`: it took **one vote** over the live grid for a single pane-wide background, then repainted every default-background cell in every visible row with the winner. Its inputs were the live rows, so:

- One column of resize could flip it, because the coverage and span rules were measured against the visible grid. Codex's composer flipped between accepted and rejected on a single column.
- One row of streamed output could flip it, so it flickered while an agent worked.
- When it won wrongly it flooded, which is what "the whole Codex surface goes black" was.
- It was gated on `PaneHeight`, which changes when a pane is focused, so focusing a pane changed its colours. That is the "extra half row of fill under the input on focus" report.

It is gone. Cells the child left at the terminal default now resolve to `RowsInput.DefaultBackground`, which is the host terminal's real background reported by `tea.BackgroundColorMsg` and converted once at the app boundary (`internal/app/update.go`). That is a fact, it does not move, and it is the same colour those cells would have if the child ran in that terminal directly.

Measured before removing it: with `-N` captures and the tail reconstruction *disabled*, real Codex renderings still round-trip with zero background cells disagreeing at every width. tmux alone describes the grid completely, so the vote could only ever overrule a fact.

## Checking it

The oracle is `capture-pane -e -N` of the same pane. Both sides are decoded to cell grids by `internal/tty/screenmodel` and compared with `CompareGrids`, so a disagreement names a row and column rather than a difference in escape-byte spelling. Cell-exact beats pixel comparison here: it is the same information, exact rather than thresholded, and fast enough to live in `go test`.

Regression tests, run by the ordinary suite:

```sh
go test ./internal/termpreview -run TestDrawRowsMatchesPaddedCaptureBackgrounds
```

The fixtures in `internal/termpreview/testdata/capture` are real Codex renderings of one file edit at three widths, recorded off an isolated tmux server. Each width is checked twice: `padded/wN` is what production reads, `trimmed/wN` proves the reconstruction still reaches the same answer.

A live pane, read-only, no input and no resize:

```sh
./scripts/terminal-fidelity.sh live %42
```

A real TUI swept across adjacent widths on a throwaway tmux server, which is where this breaks first:

```sh
./scripts/terminal-fidelity.sh sweep --command codex --widths 84,85,86 --height 45
```

Both run `TestFidelityProbeLive` in `internal/termpreview`, which you can also point at a directory of `<name>.trim` / `<name>.pad` pairs.

For a visual check, `scripts/tmux-drive.sh` can paint all four shapes into a real pane at once:

```sh
printf "\033[48;2;74;34;29m- removed\033[K\n\033[48;2;33;58;43m+ added\033[K\n\033[49m context\n\n\033[48;2;30;30;30m\033[K\n\033[48;2;30;30;30m> composer\033[K\n\033[48;2;30;30;30m\033[K\n\033[49m  status line\n"
```

Correct output is: two full-width diff bars, an uncoloured context row, an uncoloured blank row, three full-width composer bars, and an uncoloured status row. It must look identical focused and unfocused.

## If you are about to change this

- Do not add a second place that decides a background. `DrawRows` is the only one.
- Do not infer a colour from the content. If tmux did not say it, the answer is the host default.
- If a capture flag changes, re-record the fixtures with `scripts/terminal-fidelity.sh sweep` and say what moved.
- Widths one column apart are the useful test. Anything that depends on a coverage ratio or a row count will pass at one width and fail at the next.

## Known residue

A drawn row closes its background at the end of the row but not its *attributes*, so padding on a row following one that left faint or italic set inherits that attribute. It is invisible for faint on a default background, which is the only case seen in practice, but reverse video would show. The fix is for every drawn row to end in a full SGR reset rather than `\x1b[49m`.

## History

- `cbd07652` paint each captured row's trailing background to the pane edge
- `501b6661` capture panes with `-N` so blank rows say what colour they are
- `e3d60644` remove the canvas vote: tmux already says what every cell is
