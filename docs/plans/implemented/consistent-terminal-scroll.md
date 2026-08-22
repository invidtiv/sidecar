# Consistent terminal scroll, focused or not

**Status:** shipped — all four slices landed (`05c45cb`, `2a437cc`, `3f87054`, `4ce9cca`), plus the review follow-up that answers the header's "app mouse" fact from the pane and routes the shifted watched keys. The proof transcript is §7. **Scope:** `internal/tty` (where the rule belongs), `internal/plugins/workspace`, `internal/overview` **Verified against:** `cb10aac`. Every file:line below was re-read in the tree at that commit. **Epic:** `td-de1ab2` (finish the shared terminal interaction surface). Folds `td-7425ad` and `td-290b9e`.

---

## 0. The one-paragraph answer

Scrolling differs between a focused and an unfocused pane because **who owns a wheel notch is currently a property of the keyboard rather than of the pane.** Both surfaces ask `tty.RouteWheel` — "has the application asked for mouse reports?" — only while the pane is live. A watched pane always answers `WheelLocal`, so a notch over an unfocused Claude Code slides sidecar's own capture window across the app's alternate-screen frame: torn rows, and a "scrollback" that is whatever was on the main screen before the app started. Focused, the same notch is forwarded as an SGR report and the app scrolls its own transcript. The fix is to ask the question in both states. An unfocused pane *can* honestly forward — `SendSGRWheel` is a target-addressed `send-keys` with no focus or attach requirement, and both surfaces already hold a live producer for the pane they are drawing. Two smaller drifts ride along: how far back a surface can read is a property of the surface rather than of the layer (the global browser dead-ends at 600 lines, the project extends lazily), and the watched key set is hand-rolled per surface.

---

## 1. What exists today

### 1.1 The unfocused (watched) path

**Project surface.** `handleMouseScroll` (`internal/plugins/workspace/mouse.go:1191`) asks `tty.WheelStaysWithPointer(p.viewMode == ViewModeInteractive)` (`mouse.go:1210`). Watched, that is false, so the notch is placed by the region it landed in:

- `regionTermPanelContent` → `p.scrollTerminalWindowByWheel(true, delta)` (`mouse.go:1244`) — **with no burst coalescing at all.**
- `regionPreviewPane` → `p.scrollPreview(delta)` (`mouse.go:1270`) → `p.wheel.Add` (`mouse.go:1402`) → `p.scrollTerminalWindowByWheel(false, delta)` (`mouse.go:1405`).

`scrollTerminalWindowByWheel` (`interactive.go:952`) is the whole of a local notch: thaw, clear the selection, `tty.ScrollWindowRows` via `scrollPreviewWindowRows` (`plugin.go:1375`), and at the bound reach for older history (`interactive.go:973-975`). Depth: the first capture is `captureLineCount = tty.DefaultScrollbackLines` = 600 (`agent.go:245`, `tty/tty.go:15`) taken as `-S -600` (`agent.go:1277`); each bound-hit prepends another `historyLoadChunk = 600` (`plugin.go:36`) via `tty.CapturePaneRange` (`terminal_history.go:119-140`, `tty/capture_range.go:25`), stopping when the buffer's absolute base reaches 0 (`terminal_history.go:183`).

**`RouteWheel` is never reached on this path**, so `PaneMouseReporting`, `PinToLive` and `NoteActivity` are all unasked. The observation the route needs is already being collected and then dropped: `#{mouse_any_flag}` is in the capture's format string (`agent.go:1311`), parsed (`agent.go:1346`), and carried on the poll message (`agent.go:1124`) — and the only assignment of `MouseReportingEnabled` anywhere is from the terminal model into `interactiveState` (`terminal_control.go:352`), which `activeInteractiveTerminal()` refuses to produce outside interactive mode (`terminal_control.go:356-358`).

**Global surface.** Better factored, same outcome. A notch reaches `wheelPreview` in both states — live via `WheelStaysWithPointer` (`overview/workspaces.go:578-582`), watched via `tty.PointerWheel` (`workspaces.go:693-694`) — and `wheelPreview` builds a real `tty.WheelHandler` (`overview/interactive.go:523-537`). But its `MouseReporting` is `m.PreviewInteractive() && m.preview.terminal.PaneMouseReporting()` (`interactive.go:526`), and `previewPaneCoords` refuses outright when not interactive (`preview.go:524-527`). `RouteWheel` (`tty/wheel.go:47`) therefore cannot return anything but `WheelLocal` for a watched pane. Depth: `previewScrollbackLines = tty.DefaultScrollbackLines` (`preview.go:34`) handed to the component as `config.ScrollbackLines` (`interactive.go:61`), **never extended** — at the bound the surface says so with a toast (`interactive.go:559-567`). `NoteActivity` is not supplied at all (`interactive.go:524-532`).

### 1.2 The focused (live) path

Project: `forwardScrollToTmux` (`interactive.go:837-861`). Global: the same `wheelPreview`. Both hand a `tty.WheelHandler` (`tty/wheel_route.go:61-97`) the shared order — coalesce, route, pin, note, send.

`RouteWheel` (`tty/wheel.go:47-52`) gives the notch to the application only when it has asked for mouse reports, the pointer is on a pane cell, and neither shift nor alt is held; alt is the documented "give me the terminal, not the app" modifier (`wheel.go:44-46`). A claimed notch becomes `WheelNotches(delta)` whole notches (`wheel.go:148`) capped at `MaxWheelNotchesPerFlush = 10` (`wheel.go:29`), and the window is pinned to the live edge first (`wheel_route.go:85`) because while the app owns the wheel it owns what the pane shows.

Delivery: `Model.SendWheelNotches` (`tty/tty.go:938`) → `SendWheel` (`terminal_surface.go:97`) → `SendSGRWheel` (`session.go:231`) → `SendLiteralToTmux`, i.e. `tmux send-keys -H -t <target>`. **It is addressed to a pane, not to a client** — no focus, no attach — and it schedules an immediate poll (`tty.go:940`).

### 1.3 What the user actually experiences

| Pane runs | Focused | Watched |
|---|---|---|
| Plain shell (no mouse reporting) | local window scroll, 600 lines + lazy extension (project) / 600 and a toast (global) | same on each surface — the shell path is already consistent |
| Agent CLI on the alternate screen (Claude Code, Grok) | notch forwarded; the app scrolls its own transcript; sidecar pins to live and redraws | **notch scrolls sidecar's capture window across the app's live frame** |

The watched agent case is the reported bug, and it is worse than "a different amount of scrollback": the rows above the app's frame are the main screen from before the app started, tmux's history is not growing while the alternate screen is on, and the top of the window is a torn mix of the two. `FitViewport` keeps a live grid's blank rows addressable on purpose (`tty/viewport.go:196-204`, `td-bbbbfe`), which is right for the picture and does nothing for the transcript the user wanted.

### 1.4 Every inconsistency, and its cause

1. **Route asked only while live.** `mouse.go:1210` / `overview/interactive.go:526` + `preview.go:525`. Cause: "the app owns the wheel" was implemented as a property of interactive mode instead of a property of the pane.
2. **Scrollback depth is a property of the surface.** Project extends lazily (`terminal_history.go:102`); global dead-ends at 600 and says so (`interactive.go:559`). Cause: the reach was built inside the plugin that needed it first.
3. **Depth also differs by input device on the project surface.** The wheel reaches for history at the bound (`interactive.go:973`) and the *watched keys* never do (`keys.go:653`, `:685`, `:732`, `:771`) — while the *live* keys do (`interactive.go:915-922`, `:998-1011`).
4. **Watched key set and page size are hand-rolled per surface** (`td-7425ad`). Live: one mapper, shift-only, page = drawn rows − 1 (`tty/keys.go:140-159`). Global watched: j/k/ctrl+d/ctrl+u/pgup/pgdown/g/G/home/end, page = `max(1, previewRows()/2)` (`overview/preview.go:266-288`). Project watched: j/k/g/G/ctrl+d/ctrl+u only, page = `max(p.height/2, 5)` — **the plugin's height, not the drawn preview's** (`keys.go:1056-1058`, `:1081-1083`).
5. **Burst coalescing is applied in three different amounts.** Inside the handler when live (`interactive.go:840`, `overview/interactive.go:525`); by hand on the project's watched preview (`mouse.go:1402`); **not at all** on the project's watched terminal panel (`mouse.go:1244`), so a trackpad flick there travels the raw event count. One `p.wheel` (`plugin.go:457`) is shared by both project surfaces and is only `Reset()` on leaving interactive mode (`interactive.go:617`).
6. **Activity is half-wired** (`td-290b9e`). The project's live handler supplies `NoteActivity` (`interactive.go:850`); the global's supplies none; no watched path on either surface supplies one. A pane being actively read can therefore be repainted at `PollingDecaySlow` = 250ms rather than `PollingDecayFast` = 50ms (`tty/polling.go:10-27`).
7. **The write gate exists on one surface only.** `features.TmuxInteractiveInput` ("enable write support for tmux panes", default true, `features/features.go:22-27`) is consulted before the project enters interactive mode (`workspace/interactive.go:103`, `:210`); `internal/overview` never consults it — it forwards keys, clicks and wheel notches regardless.

### 1.5 The two open tickets

- **`td-7425ad`** — *same root*: a rule the shared layer owns for the live state and each host hand-rolls for the watched one. Its acceptance ("one mapper answers both sets, hosts supply only page size, page derived the same way") is slice 3 verbatim.
- **`td-290b9e`** — *not the same root*; it is one missing field on one host's handler. Folded anyway, because slice 1 rewires both hosts' `WheelHandler` construction and a forwarded *watched* notch makes the activity signal load-bearing in a state that did not exist before. Doing it later means touching the same six lines twice.

---

## 2. Chosen design

> **Law 1. Who owns a wheel notch is a property of the pane, not of the keyboard.**
> `RouteWheel` is asked in every state. `MouseReporting` and `PaneCoords` answer about the pane
> under the pointer; neither may consult `interactiveState` or `PreviewInteractive()`.

> **Law 2. A forwarded notch is input, and input to an unfocused pane is gated exactly as typing
> is.** `features.TmuxInteractiveInput` becomes an input to `RouteWheel` rather than a per-host
> precondition, so no host can forward without saying it may write, and neither host can forget to
> ask. With writes off, every surface in every state falls back to the local window.

> **Law 3. How far back a surface can read is a property of the layer, not of the surface.** One
> reach, adopted by both hosts, ends at tmux's real end of history and nowhere earlier.

> **Law 4. `alt`+wheel scrolls sidecar's own window, in every state.** It is the existing escape
> hatch (`wheel.go:44-46`) and it becomes the answer to "let me read the capture behind this
> alt-screen app."

**Mouse-over scroll without focus stays.** Nothing here focuses a pane, rebinds the keyboard, or moves a selection cursor; `WheelStaysWithPointer` (`wheel_route.go:9-17`) keeps its current meaning. What changes is only the *outcome* of the notch.

### 2.1 Can an unfocused pane honestly forward? Yes.

- The send is target-addressed (`session.go:231` → `send-keys -H -t <pane>`); tmux neither knows nor cares which pane sidecar considers focused.
- Both surfaces already hold a live producer for the pane they are drawing while unfocused: `desiredPrimaryTerminal` is gated on `terminalOutputSurfaceVisible()`, which includes `ViewModeList` (`terminal_control.go:173-181`, `:199-225`); the global's `syncPreviewTerminal` opens on visibility (`overview/interactive.go:115-151`) and `ReleaseInput` drops the keyboard *without closing the watched producer* (`interactive.go:82-84`).
- Where no model owns the pane, the poll capture already reads `#{mouse_any_flag}` (`agent.go:1124`) and `tty.SendSGRWheel(target, …)` needs nothing else.

What honesty costs, stated as behaviour: a hover-scroll over an unfocused agent pane changes what that agent shows, and it stays changed after the pointer leaves. That is the point — it is what the focused pane does. It is bounded by being the *only* input a watched pane accepts, by Law 4, and by Law 2's flag.

### 2.2 Alternatives rejected

- **Require focus to scroll.** Consistent by deletion; the mouse-over behaviour is deliberate and the windowing plan keeps it.
- **Never forward; make the focused pane scroll locally too.** Reintroduces the torn alt-screen frame the forwarding path was built to fix, on the one path that works today.
- **Auto-focus the pane under the pointer, then forward.** A stray trackpad notch would silently move the keyboard into a pane — exactly the failure `WheelStaysWithPointer` exists to prevent.
- **Give the global a bigger fixed capture (5 000 lines) instead of a reach.** Moves the dead end rather than removing it, and pays for it on every poll of every pane.
- **Put the watched pane into tmux copy-mode and scroll there.** A mode change on a pane another client may be attached to, with a UI of its own, fighting the model buffer.

---

## 3. Slices

Each is independently commit-able, independently testable, and leaves the tree green.

### Slice 1 — Ask who owns a notch in both states

The user-visible fix. `internal/tty`: add `WritesEnabled` to `WheelInput`/`WheelHandler` so `RouteWheel` returns `WheelLocal` unless the caller has said writes are allowed (Law 2), and restate the `MouseReporting`/`PaneCoords` contract as being about the pane (Law 1).

- **workspace**: delete the two watched wheel branches (`mouse.go:1244`, `mouse.go:1399-1405`) and route both terminal regions through one handler that names the surface (preview vs panel) instead of reading `interactiveState`; give `interactiveMouseCoords` (`interactive.go`) the surface as a parameter; answer `MouseReporting` from `primaryTerminal`/`panelTerminal` when a model owns the pane and from a newly *stored* capture flag otherwise (stop dropping `agent.go:1124`).
- **overview**: drop the `PreviewInteractive() &&` conjunct (`interactive.go:526`) and the interactive gate in `previewPaneCoords` (`preview.go:525`); supply `NoteActivity` (**closes `td-290b9e`**) and consult the write flag.

*Tests.* `tty`: `RouteWheel` refuses to forward without `WritesEnabled`; forwards on a watched-shaped input otherwise. Per surface: a watched mouse-reporting pane receives notches at its own 1-indexed coordinates and its window stays at offset 0; a watched pane with no reporting scrolls locally exactly as it does today; `alt`+wheel is local in both states; with the flag off nothing is sent.

### Slice 2 — One reach, both surfaces

Hoist the lazy-history orchestration out of `workspace/terminal_history.go` into `internal/tty` (request generation, pending scroll, exhausted, rebase-after-prepend). `terminal_history.go` becomes the adapter that names buffer and target; the overview gains one, so a pane reads the same distance back on both surfaces in both states. `notePreviewScrollbackLimit` stops meaning "this surface gives up at 600" and starts meaning "tmux has no more history", said the same way on both.

*Tests.* `tty`: one range request per bound-hit, a pending scroll coalesced rather than lost, a stale generation ignored, a hard stop at absolute base 0. Per surface: a *watched* global preview scrolled to the bound extends past 600 lines and lands on the same row the project surface lands on.

### Slice 3 — One key set in both states (`td-7425ad`)

Extend the shared mapper to answer the watched set as well as the shifted live set — the shift requirement is a property of the *state* (an unshifted key belongs to a live pane), not of the key — and have both hosts supply only a page size derived the same way: the drawn rows of the surface under the keys, not the plugin's height. Route both hosts' watched key moves through the same placement and the same reach the wheel uses, so `k` at the bound loads history exactly as a notch does (fixes §1.4 item 3).

*Tests.* A `tty` table over (state, key) → move. Per surface: `pgup`/`pgdown`/`home`/`end` work on a watched project preview; `ctrl+d` moves the same number of rows on both surfaces; the watched and live sets differ only by shift.

### Slice 4 — One flick, one cadence, and proof

- Give the terminal panel the burst it has never had (`mouse.go:1244`), and hold one burst per terminal surface rather than one per plugin (`plugin.go:457`), reset when the pointer crosses between surfaces rather than only on leaving interactive mode (`interactive.go:617`).
- Note activity for every notch, in both states, on both surfaces, so a pane being read repaints at `PollingDecayFast` (`polling.go:10-27`).
- Visual proof via `scripts/tmux-drive.sh` on a private socket: the same flick over the same agent pane, watched and focused, on both surfaces, landing in the same place.

*Tests.* Per surface: N wheel events over the panel travel the same total distance as over the preview. Plus the recorded proof transcript.

---

## 4. Acceptance criteria

1. Over a pane whose application has asked for mouse reports, one notch does the same thing focused or not, on both surfaces: the application scrolls, sidecar's window stays at the live edge, and the next capture is taken at the fast tier.
2. Over a plain shell, one notch moves sidecar's window the same number of rows in every state on both surfaces, and reaches the same distance back before tmux's history is exhausted.
3. `alt`+wheel scrolls sidecar's own window in every state on both surfaces.
4. With `tmux_interactive_input` disabled, no notch is forwarded from any surface in any state.
5. The same scrollback key does the same thing in the same state on both surfaces; the watched and live key sets differ only by the shift requirement, and both reach for history at the bound.
6. Nothing about a wheel notch is decided outside `internal/tty` except which surface is under the pointer and how big it is.
7. `go build ./...`, `go vet ./...` and `go test -count=1 ./internal/tty/... ./internal/plugins/workspace/... ./internal/overview/... ./internal/ui/...` are green at every slice, and a proof transcript shows the watched and focused flick landing on the same row.

## 5. Risks

- **A hover changes an unfocused agent's view, and it stays changed.** Deliberate (Law 1), bounded by being the only input a watched pane takes, by Law 4's opt-out, and by Law 2's flag. This is the one behavioural change a user could dislike; it is the one the goal asks for.
- **Send volume.** A flick over a watched pane becomes one `send-keys` per flush. The burst plus `MaxWheelNotchesPerFlush` (`wheel.go:29`) bound it, but the watched path has been free until now — measure the rate under a real trackpad flick in slice 4 before calling it done.
- **The `-J` capture has no usable split.** With `tmux_interactive_input` off the poll capture is joined and `RowsJoined` carries no history/pane split (`agent.go:927-929`, `:980`, `:1370`). That is the same state in which Law 2 forbids forwarding, so the fallback must be local-only and must not attempt pane coordinates at all.
- **Slice 2 moves buffer-mutating code between packages.** Selection anchors and search matches live in absolute coordinates rebased by the prepend (`terminal_history.go:190-205`), and `td-19c9cb` ("selection is not revalidated when a capture renumbers a relative buffer") is open against exactly that. Move the *request state* and leave the rebase where it is, or fold `td-19c9cb` in deliberately — do not straddle.
- **Panes attached elsewhere.** A forwarded notch reaches a pane another client is attached to. Already true of the focused path; it is a `send-keys` to a target, not a client action.

## 6. Non-goals

No change to whether hovering focuses a pane. No new keybindings. No change to the window-position model (bottom-relative, `0` = live) settled by `td-6b3fe5`. No change to what a *click* over a pane means.

## 7. Proof

Driven end to end through `scripts/tmux-drive.sh` on its private sockets (outer `-L sidecar-drive`, inner `TMUX_TMPDIR` under `/tmp/sc-scrollproof`), against a stand-in agent CLI holding the pane: alternate screen (`?1049h`), SGR mouse reporting (`?1000h ?1006h`), and a transcript of its own whose first row states where it is — `VIEW-TOP: nnn`. tmux confirms the fixture (`mouse_any_flag=1 alternate_on=1`), so the wheel over this pane is genuinely the application's.

One notch is one SGR report delivered to sidecar's own input at the same screen cell in every run (`\e[<65;100;10M`), five of them, 600ms apart so no two coalesce. What the notch did is read from the application, not from sidecar: the pane's own first row.

| Surface | State | Header's mouse note | Application after five notches |
|---|---|---|---|
| project workspaces preview | watched | `app mouse • ⌥wheel scrolls back` | `VIEW-TOP: 027` |
| project workspaces preview | live (`E`) | `app mouse • ⇧drag select` | `VIEW-TOP: 027` |
| global overview preview | watched | `app mouse • ⌥wheel scrolls back` | `VIEW-TOP: 027` |
| global overview preview | live (`enter`) | `app mouse • ⇧drag select` | `VIEW-TOP: 027` |

Same pane, same gesture, four states, one landing row — acceptance criterion 1, and the reported bug closed: before slice 1 the watched rows moved sidecar's window across the app's live frame and left `VIEW-TOP` at `000`.

`alt`+wheel, watched, on the global preview (`\e[<72;100;10M` ×3): the application stayed at `VIEW-TOP: 027` and sidecar's own window moved — the header read `▲ 1 lines back • end live`. Criterion 3, and the escape hatch the watched note now names.

The shifted navigation keys on a *watched* project preview, over a pane with 300 lines of ordinary shell scrollback: `⇧PgUp` → `▲ 43 lines back • G live`, `⇧Home` → `▲ 260 lines back`, `⇧End` → the live edge. Criterion 5; these did nothing at all before the review follow-up, because this surface dispatches on a key's name and never named their shifted forms.

The executable form of the same claim, run on every build, is `internal/tty/wheel_state_test.go`, `internal/plugins/workspace/terminal_wheel_state_parity_test.go` (both terminal surfaces) and `internal/overview/wheel_state_parity_test.go`: one notch, watched against live, compared outcome for outcome rather than against a number written twice.
