# U0 measurements: the v0 terminal stream under agent-like load

**Status:** Recorded 2026-10-03 for U0-c (td-d8fcb0), automatable half. The tailnet latency run is still to do with Marcus (see "Not measured here"). Parent plan: [Sidecar UI API](../sidecar-ui-api.md), U0 exit and "Terminal protocol v1" item 2.

## How to reproduce

```bash
./scripts/ui-api-measure.sh              # counted run (tmux wrapper on the serve PATH)
./scripts/ui-api-measure.sh --no-count   # same scenarios without the wrapper
```

The script follows the `scripts/ui-api-proof.sh` isolation: a temporary binary built from the working tree, a private tmux socket under `/tmp/sidecar-uiapi-measure.*`, `unset TMUX TMUX_PANE` (and every `SIDECAR_*` shell variable) before anything runs, isolated `XDG_STATE_HOME` and `TMUX_TMPDIR`, a `-config` temp path, `SIDECAR_ISOLATED_STATE=1`, and `sidecar api serve --port 0`. It creates five managed shells, resizes each window to 160x48 (a typical browser terminal), and drives the terminal WebSocket on the Browser listener with a paired origin's bearer token and no `Origin`, as the Node SDK does. The driver is `internal/tools/uiapimeasure`.

Generators, each bounded by `timeout 55`:

- **Line output:** `sh -c 'while :; do date; sleep 0.01; done'`, roughly 60 lines a second.
- **Full-screen redraw:** a Python script on the alternate screen that rewrites every row with colour at 25 Hz, with a spinner and counters changing on most rows.
- **Echo:** `cat` in canonical mode, so the tty driver echoes each byte with no shell highlighting in the way.

How each number is taken:

- **Frames/s and wire bytes/s:** every WebSocket text message is counted over a 15 s window after a 2 s warmup. The server negotiates no compression (`CompressionDisabled` in `internal/uiapi/terminal.go`), so message bytes are wire bytes, less a few bytes of framing each. "VT payload" is the decoded `render_vt_base64`.
- **Gzip estimates:** "gzip/msg" gzips each message on its own. "Deflate stream" runs one flate stream flushed after every message, which is what `permessage-deflate` with context takeover would send.
- **Captures/s:** v0 captures through tmux control-mode clients, so a capture is a `capture-pane` command written to a `tmux -C` client's stdin, not a process. A `tmux` wrapper on the serve process's PATH tees each control client's stdin to a log and records every other tmux spawn. `SIDECAR_TERMINAL_PERF` and `internal/terminalperf` do not count captures.
- **CPU:** cumulative CPU time from `ps -o time=` at the window's start and end, divided by wall time. 100% is one core. The figures cover the `sidecar api serve` process, the private tmux server, and the `tmux -C` clients beneath serve.
- **Keystroke→echo:** the echo terminal takes control at the pane's existing geometry, so nothing resizes. It types one byte at a time with a random 20-60 ms gap, timing each byte from sending the `input` request to the first frame that contains the grown line. Each scenario takes 200 samples. Under load, the four busy terminals keep streaming on their own WebSockets while the fifth is typed into.

## Machine

| | |
| --- | --- |
| Host | aerie, Apple M4 Pro, 14 cores, 64 GB |
| OS | macOS 27.0 (26A428) |
| tmux | 3.7c (Homebrew) |
| Go | 1.27.1 darwin/arm64 |
| Sidecar | `main` at f70b27f3 plus the measure tooling |
| Transport | loopback TCP (Browser listener), WebSocket without compression |

## Results

First counted run:

| Scenario | frames/s | wire KB/s | VT payload KB/s | gzip/msg KB/s | deflate stream KB/s | capture-pane/s | sidecar CPU % | tmux server CPU % | echo p50 ms | echo p95 ms |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 4 generators, nothing attached | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 1.1 | | |
| Idle, 1 attached (echo) | | | | | | | | | 14.4 | 17.0 |
| 1 busy: line output | 60.5 | 669 | 477 | 44.0 | 6.5 | 60.5 | 5.8 | 1.7 | | |
| 1 busy: 25 Hz redraw | 23.1 | 335 | 242 | 42.2 | 14.9 | 23.1 | 3.1 | 1.4 | | |
| 4 busy (2 line + 2 redraw), echo on a 5th | 171.5 | 2057 | 1475 | 175.8 | 43.3 | 172.5 | 16.9 | 5.1 | 13.5 | 14.6 |

Each scenario ran three times: two counted runs and one `--no-count` run. Frames/s, captures/s and CPU varied by at most a few percent between runs, and the tee wrapper made no measurable difference to latency:

| Run | 4-busy frames/s | 4-busy wire KB/s | 4-busy sidecar CPU % | idle echo p50 / p95 ms | 4-busy echo p50 / p95 ms |
| --- | --- | --- | --- | --- | --- |
| counted #1 | 171.5 | 2057 | 16.9 | 14.4 / 17.0 | 13.5 / 14.6 |
| `--no-count` | 164.6 | 1980 | 16.3 | 14.2 / 20.8 | 13.6 / 16.4 |
| counted #2 | 166.1 | 1997 | 16.1 | 14.3 / 19.8 | 13.5 / 16.6 |

Other observations:

- **One capture per frame.** In every busy scenario captures/s equals frames/s within rounding. Each capture is two control commands: `display-message` metadata, then `capture-pane`. Busy terminals spawned no tmux processes, because everything ran over the control clients, one per attached terminal.
- **The output rate sets the capture rate, and the 12 ms coalesce caps it.** Line output produced about 60 captures/s on one pane. The 25 Hz redraw produced 23, so v0 does not over-capture a slower writer. The ceiling is the 12 ms output coalesce in `tty.NewControlManager`, roughly 80 captures/s per pane.
- **Frame size.** A 160x48 frame is 11-15 KB on the wire. That figure includes JSON and base64 overhead, about 40% over the VT payload.
- **Bandwidth is the large number.** Four busy terminals send about 2 MB/s raw. Deflate with context takeover would send about 43 KB/s, 47 times less, because consecutive full frames are nearly identical. Gzipping each message on its own saves only 12 times. For scrolling line output the deflate stream is 100 times smaller than raw.
- **CPU is modest on this machine and grows linearly.** Each busy terminal costs Sidecar about 3-6% of one core, plus about 1% in the tmux server. With nothing attached, the generators cost tmux 1.1%. The `tmux -C` clients rounded to 0%.
- **Load does not affect latency.** Echo p50 is 13.5-14.4 ms whether idle or under the 4-busy load. The minimum is about 13 ms in every run. That floor sits just above the 12 ms output coalesce, so the coalesce is very likely most of the local echo latency. The rest is `send-keys`, a capture and the WebSocket.
- **No stream aborted.** v0's 8-deep outbound queue did not overflow, but this reader keeps up on loopback. A slow socket is a separate risk (v1 item 3).

## Recommendation

Screen-model frames (v1 item 2) can wait. On this machine, four busy agents cost about 17% of one core in Sidecar and 5% in tmux, the capture rate tracks the output rate at one capture per frame, and keystroke echo stays at 13-14 ms p50 under that load. The 2 MB/s that four busy terminals send is the number that matters for a tailnet or phone link, and the cheap fixes for it belong in U1: turn on `permessage-deflate` on the terminal WebSocket (the deflate stream estimate is 47 times smaller), make frames reset-free and coalesce them before the queue (v1 items 1 and 3), and then add changed-row frames. Those changes also fix selection, which v0 resets clear. Move screen-model frames to U3, where pane trees put many terminals on one page and capture cost grows linearly (about 1.5% of a core per extra busy terminal in tmux and Sidecar together). Bring them forward if the live tailnet run or a slower remote host shows CPU or latency trouble. Rerun `scripts/ui-api-measure.sh` on that host to check. The compression CPU cost has not been measured and should be when deflate is enabled.

## Not measured here (the live run with Marcus)

- Keystroke→echo latency over the tailnet through `tailscale serve`, from a real device. These numbers are loopback only.
- Whether the slow-socket abort fires on a real phone or tailnet link at about 2 MB/s.
- Real agent output (Claude Code, Codex) instead of synthetic generators, and Marcus's real session count.
- Whether `tailscaled` can open the 0600 tailnet socket (see the reference, "Listeners and trust").


## U1-d compression measurement

Recorded 2026-10-03 on the same aerie/tmux/Go stack, branch `u1d-presence`, after enabling terminal WebSocket `CompressionContextTakeover`. The client now has `-compression`; the script exposes `--compression`. Receive bytes are counted on the underlying socket before decompression, including WebSocket framing, rather than counting the decoded JSON. Both runs use legacy terminal behavior so the compression comparison excludes negotiated presence/render changes. No Tailscale configuration or live sessions are involved.

```bash
./scripts/ui-api-measure.sh --no-count --window 10 --keystrokes 100
./scripts/ui-api-measure.sh --no-count --compression --window 10 --keystrokes 100
```

| Scenario | Compression | frames/s | actual socket KB/s | Sidecar CPU % of one core | tmux CPU % | echo p50 / p95 ms |
| --- | --- | --- | --- | --- | --- | --- |
| 1 busy: line output | off | 58.59 | 648.01 | 5.5 | 1.5 | |
| 1 busy: line output | context takeover | 57.29 | 6.53 | 5.3 | 1.6 | |
| 1 busy: 25 Hz redraw | off | 21.90 | 318.14 | 2.6 | 1.2 | |
| 1 busy: 25 Hz redraw | context takeover | 21.60 | 21.46 | 2.6 | 1.2 | |
| 4 busy + echo | off | 159.98 | 1921.28 | 15.6 | 5.0 | 13.45 / 15.30 |
| 4 busy + echo | context takeover | 165.80 | 57.35 | 13.0 | 4.9 | 13.73 / 15.14 |
| idle echo | off | | | | | 15.23 / 18.57 |
| idle echo | context takeover | | | | | 15.21 / 17.21 |

Four busy terminals used about **33.5 times fewer actual socket bytes** with compression. The earlier 47-times estimate used Go's default deflate estimator; the actual WebSocket library's compressor and framing produce a different result. No server CPU increase was observable in this pair; the measured net CPU difference was -2.6 percentage points for four busy terminals. These are short sequential loopback runs on a shared machine while other lanes build and test, not a controlled CPU benchmark. The apparent decrease must not be presented as an isolated compression CPU saving. Echo latency remained close to the same 13–14 ms floor. Tailnet latency, actual devices, real-agent output and browser selection behavior remain unverified here.
