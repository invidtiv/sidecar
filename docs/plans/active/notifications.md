# Notifications — toasts, centre, indicator, sources

**Status:** Phase 1 (steel thread) **done**; Phase 1.5 **done**; Phase 2 **done** (both halves — see the two "Phase 2 as built" sections); Phase 3 **done** (see "Phase 3 as built"); Phases 4–7 planned, not started
**Created:** 2026-08-18
**Design:** claude.ai/design project `3172ac49-4413-4a60-9235-0afa5c77cf77`, file `Sidecar Notifications.dc.html` (frames 1a–1h). The design is authoritative for visual grammar. Two deliberate deviations, decided by Marcus: the sources config lives on the existing config screen (`internal/configui`), not the design's invented one; and the notification centre is an **app-level right panel that pushes all content left** (see "The centre" below), not the in-pane split the design's frame 1c sketches.

## Ground rules for implementing agents

- **Follow the design as closely as possible** — glyphs, hues, section
  grammar, spacing, the countdown cells, the exact footer key rows. When the
  design and this plan conflict, this plan wins (it encodes later decisions).
- **Respect the existing sidecar UI.** Use shared components (`internal/ui`,
  `internal/modal`, the shared meta-column/section-rule grammar, styles/theme
  keys) and plug into canonical systems (`headerGeometry`, `internal/mouse`
  hit maps, `internal/uirequest`, `internal/terminallink`, `internal/config` +
  `configui`, the 1s heartbeat and tagged ticks). Inventing new infrastructure
  is allowed **only when necessary** — and the necessity should be stated in
  the commit or plan update. A second compositor, border rule, or key-routing
  scheme is a bug.
- **Keyboard shortcuts: the implementing agent has autonomy.** Choose keys by
  availability (check `.claude/skills/keyboard-shortcuts` and the keymap
  registry), with limited permission to rebind an *existing* shortcut when it
  is obscure and the newcomer is clearly more frequently used — note any such
  rebind in the plan update. Keys named in this plan are suggestions:
  lowercase `n` is probably taken; `N` very well may be free and is the
  expected choice for toggling the centre. The design's per-surface key rows
  (`d`, `D`, `m`, `T`, `s`, `tab`, digits) follow the same rule.

## Summary

A real notification system replacing the single-line footer toast:
macOS-style toasts drawn as bordered floating blocks in the top-right of the
content region, a notification centre right panel, an unread indicator in the
header next to the gear, per-source config on the existing config screen, and
an agent-facing `sidecar notify` CLI so agents can post (and dismiss their
own) notifications. Tasks integrate both ways: due tasks post here, and any
notification can be filed back onto a task as a reminder.

**Every current toast becomes a notification.** There is no legacy footer
toast path once this lands, and therefore no double-alerting: the old-style
alerts *are* the new system. The workspace list keeps its attention icons
next to shell names — that is list-view state, not an alert — and the same
underlying event additionally posts a notification. A per-source preference
(1g table) lets users quiet the notification without touching the list icons.

Design frames, for reference while implementing:

- **1a** single toast: bordered block, source-hued border + rule line, title,
  body, key row `enter open · d dismiss · s snooze`, cell-drawn countdown
  `▰▰▰▱▱ 4s`. (Snooze is deferred — render the key row without it until
  Phase 6; tasks handle their own snoozing by re-posting.)
- **1b** stacking: newest on top, max 3 on screen; same-source toasts collapse
  into one block with `×N` and a "▾ 2 more · tab expand" peek line.
- **1c** the centre's *content* grammar: sections per source (`◆ AGENTS`,
  `? WAITING`, `✓ SESSIONS`, `○ TASKS`, `■ TD`), unread `●`, times in the
  shared meta column, "Dismissed items clear after 24h" footer note. Footer
  keys: `j/k move · enter open · d dismiss · D dismiss group · m mute source ·
  T to task · esc close`. (The *container* differs from 1c — see below.)
- **1d** corner indicator next to the gear, ≤5 cells: `·` empty, `●3` unread,
  `?12` agent waiting, red `●99+` on failure/clamp, `◌4` muted, inverted while
  the panel is open. Colour carries the loudest unread source.
- **1e** calls to action: every id in a notification is a project-aware jump —
  td issue → td tab, task → tasks tab, commit → git diff, `file:line` →
  files/$EDITOR, session → attach, web → $BROWSER (confirmed). Numbered 1–N
  for keyboard jumps. Cross-project targets render `repo/td-xxxxxx`.
- **1f** tasks both ways: a due task posts a notification; `T` on any
  notification writes a REMINDERS entry onto a task.
- **1g** sources config: per-source rows with `toast / centre / bell / expiry`
  columns, plus a behaviour block (stacking on/off, max toasts, quiet hours,
  suppress-while-resizing, `t` test toast). "Anything suppressed still lands
  in the centre and still counts in the corner."
- **1h** the reveal spec — the only motion allowed: one whole row per frame,
  ~90ms apart, no subpixel travel, no fades; reveal top-down (4 frames in),
  retract bottom-up (3 out) so the border never redraws twice; countdown
  ticks one cell per second, no tween; skipped entirely on dumb/slow
  terminals — the toast just appears.

## The centre: an app-level right panel

Per Marcus's screenshot (2026-08-18): the centre is a full-height right panel
with its own title ("Notifications") and close affordance, **owned by the app
shell, not by any plugin**, that pushes the active plugin's content to the
left — the same visual family as the workspaces plugin's right panels. It is
not an overlay and not a modal.

Consequences the implementing agents must take seriously:

- **This is the largest architectural piece of the feature.** Today plugins
  receive the full content `(width, height)` in `View`. The app must be able
  to reserve a right-hand column and hand every plugin a narrower width —
  uniformly, for td, git, files, notes, workspaces, and the kanban/task
  views. The correct mechanism is the canonical one: shrink the `width`
  passed to `plugin.View()` and re-emit size updates when the panel opens,
  closes, or resizes, exactly as a terminal resize would. Plugins that
  mishandle a narrow width are pre-existing bugs to fix, not reasons to
  special-case the panel.
- Reuse the shared pane grammar for the panel itself: the resize rail
  (`internal/paneframe` / drag-pane machinery) for width adjustment, shared
  section rules and meta columns for content. Panel width persists in state.
- **No navbar tab.** The centre is reached only via the header indicator
  (click) and its shortcut (likely `N`). `esc` or the close affordance
  closes it. Focus routing follows the existing key-precedence rules
  (`plugin.KeyRouter` docs); while the panel has focus it consumes list keys,
  and clicking back into content returns focus without closing the panel.
- **The panel stays open until the user closes it — across all navigation.**
  Switching plugins/tabs, opening files, changing projects or worktrees,
  entering and leaving modals: none of these close the panel. It is app-shell
  state (like the header), not per-plugin state, and its open/width state
  survives plugin `Reinit` on project/worktree switches. This is a real
  integration cost — every navigation path must re-emit the narrowed size
  rather than resetting to full width, and transitions that rebuild the
  plugin registry must restore the reservation before the next frame — and
  it is part of the plan, not an edge case. Only an explicit close (`esc`
  with panel focus, the close affordance, or the indicator toggle) dismisses
  it. Unread state updates live (the store is in the app model, no polling
  needed).

## Architecture

One core package, thin surfaces — core in a library; every capability has a
non-interactive path; store behind a narrow interface, JSONL first.

- **`internal/notify`** — the model and store. `Notification{ID, Source,
  Severity, Title, Body, Targets []Target, CreatedAt, ReadAt, DismissedAt,
  ExpiresAt, Origin, Sticky}`. Sources are registered (`agent`, `waiting`,
  `session`, `tasks`, `td`, `system`, plus external registrations), each with
  glyph + hue matching 1c/1g. Store is JSONL under the sidecar state dir
  (`notifications.jsonl`, appended events: posted/read/dismissed), compacted
  on load, 24h retention for dismissed items, behind a small `Store`
  interface. State-free resolution logic (what may toast, what counts as
  unread, loudest-source colour, quiet hours) lives here so a headless caller
  could adopt it unchanged.
- **Posting API (in-process):** a `notify.PostMsg` tea.Msg + helper in
  `internal/app/commands.go`, like `ToastMsg` today. `msg.ShowToast` becomes
  a thin adapter posting a `system`-source notification; the footer status
  rendering path (`internal/app/view.go` toast block) is removed in the same
  phase. Callers do not change.
- **Posting API (out-of-process):** a new `uirequest.Action` (`notify`) on
  the existing file-RPC bus (`internal/uirequest`), and `sidecar notify
  post|dismiss|list` in `internal/cli/registry.go` — which lands it in
  `sidecar --agents` automatically. `--json` on `list`. Dismissal via CLI is
  origin-checked: agents may dismiss only notifications they created. `list`
  reads the JSONL directly so it works with no TUI running; `post` falls back
  to a direct JSONL append when no instance is announced, so nothing is lost.
- **Triggers:** the 1s heartbeat already resolves
  `agentstatus.Presentation`; lane *transitions* (working→blocked, →done,
  session ended/failed) post notifications. The tasks source is a polled
  adapter behind the same interface. **No git source and no GitHub polling**
  — deliberately out of scope; the source registry makes it a later add if a
  real local data source ever appears.
- **Rendering (toasts):** composited over the active content region via
  `internal/overlay` (the command palette is the precedent for floating
  bordered surfaces). Toasts render whether or not the centre panel is open.
- **Reveal framework:** a tiny `internal/reveal` package implementing the 1h
  spec — a row-count state machine driven by a tagged tick (~90ms), generic
  over "a block of N rows". This is the one piece of genuinely new
  infrastructure, justified because nothing in sidecar animates today except
  the intro; it becomes the reusable primitive for any future motion.
  Auto-disabled on dumb/slow terminals (toast just appears).
- **Config:** a top-level `Notifications` section in `internal/config`
  (pattern precedent: `Selection`, `TerminalResources`) + a new `configui`
  page rendering the 1g table with the existing form components
  (`toggleRow`, `selectRow`, `FormRow`). Suppressed ≠ dropped: everything
  still lands in the centre and counts in the corner. Includes the per-source
  switch that quiets agent-waiting notifications for users who find the
  workspace list icons sufficient.

## Steel thread (Phase 1)

Smallest end-to-end slice proving the whole pipe: **an agent posts a
notification from a shell; a bordered toast appears top-right; the header
shows `●1`; the centre shortcut opens the right panel listing it (content
pushed left on every plugin); `d` dismisses it; it persists across restart
until dismissed.**

1. `internal/notify`: model, source registry (hardcoded set), JSONL store,
   unread/loudest resolution. Unit tests.
2. `notify.PostMsg` handling in `internal/app/update.go`; store owned by the
   app model; expiry sweep on the 1s tick.
3. `uirequest` `notify` action + `sidecar notify post` (title, `--body`,
   `--source`, `--expiry`) and `sidecar notify dismiss <id>`
   (origin-checked), with the no-instance JSONL fallback.
4. Toast rendering: single bordered block, top-right of the content region,
   source hue, key row, cell countdown. No stacking, no reveal yet — it just
   appears (which is also the spec'd degraded mode, so nothing is thrown
   away later).
5. Header indicator: `●N` / `·` next to the gear inside `headerGeometry()`,
   with a degradation rank (outlives the clock, drops before the gear);
   click toggles the centre; inverted style while open.
6. **The right panel**: app-level width reservation, plugin `View` width
   reduction + resize propagation across all plugins, resize rail, persisted
   width; centre content as a flat list grouped by source with unread `●`,
   `j/k`, `d` dismiss, `D` dismiss group, close via `esc`/click. `enter` is
   a no-op for now. This step is the bulk of Phase 1; verify every plugin
   (td, git, files, notes, workspaces, kanban/task views) at narrow widths,
   and verify the panel stays open (and correctly sized) across tab
   switches, project/worktree switches, and modal open/close.
7. Route `ToastMsg` through the new system and delete the footer toast path.
8. Proof run via `scripts/tmux-drive.sh` (isolated state — see AGENTS.md):
   post from a second shell, snap the toast, the indicator, the open panel on
   at least three different plugins, dismiss, restart, verify persistence.

## Phase 1 as built — decisions and deviations

Landed in commits a7bcbe9f, f6540456, 5c3020c2 plus a review/proof fix pass.
Everything in the steel thread above is implemented. What follows is what a
later phase needs to know that the plan above does not already say.

**Read state.** A notification is marked read when it is selected in the centre,
or when a toast that was *actually painted* expires. The "actually painted" gate
matters: expiry alone silently reads what the user never saw — a notification an
agent posted while sidecar was closed arrives already past its countdown, and
with one toast slot a burst would read everything queued behind the newest.

**Keys** (the plan gave the implementing agent autonomy here):

- **`alt+n` always toggles the notification centre.** `N` also does, but it
  yields to any context that rebinds it — git's `N` is prev-match and is worth
  more there than this toggle. Since the centre has no navbar tab, `alt+n` is
  what guarantees a keyboard route on every tab.
- **`N` toggles the notification centre**, as the plan expected. It is also the
  way *back* into an open panel: with the panel open but blurred, `N` refocuses
  it; pressing it again closes. Nothing was rebound to make room.
- **`d` dismisses the toast on screen** — design 1a's key row, and the same key
  the centre uses, so one key means one thing. It is a global fallback and
  yields to any focused context that binds `d` for itself (`git-status`,
  `workspace-list`, `workspace-preview`, `config`, …) via
  `contextRebindsKey`. Precedence level 3 was *not* enough on its own: it only
  covers plugins implementing `plugin.KeyRouter`, which is `tasks` alone, and
  the rest bind `d` after the global switch.
- **The `notification-centre` keymap context** owns `j/k`, `d`, `D`, `enter`
  (inert until Phase 5), `esc` and — since Phase 2 — `tab`/`shift+tab`,
  registered in `internal/keymap/bindings.go` like every other context.
  Navigation keys — the tab digits, `[`/`]`, `` ` ``/`~`, `K`, `@`, `W`, `^`,
  `?`, `,` — are deliberately *not* claimed: they blur the panel and run
  normally, so a keyboard-only user can leave the panel without closing it.
  `tab` was on that list in Phase 1.5 and no longer is: it is now the focus
  cycle's move onward and is consumed rather than released (see Phase 2 as
  built). The panel still closes only on `esc`, the close affordance, or the
  toggle.

**Read semantics.** A notification is marked read when it is selected in the
centre (on open, on cursor move, on click) and when its toast countdown runs
out. Sticky notifications have no countdown and stay unread until answered.
Without this nothing ever set `ReadAt`, the header climbed all session now that
every `ShowToast` is a notification, and unexpired notifications toasted again
after a restart.

**Store concurrency.** `JSONLStore` re-folds the file from disk under an
exclusive `flock` (the same shape as `internal/shellstate`'s manifest lock)
before every write and before every rewrite. Folding once at open and re-emitting
memory silently deleted anything another process had appended — the CLI's
no-instance fallback, or a second Sidecar sharing the global state dir. `Sweep`
(the 1s heartbeat) is also the cross-process read point: a record another
process appended becomes visible to a running instance within a second.

**Cross-project posts — decided.** The store and the centre are **global in
Phase 1, deliberately**. `notify` requests are still routed by the caller's
origin, but that check answers "which instance acknowledges this request" and
"who may dismiss this record", *not* "who may see it". A post from a project no
running instance is showing is declined on the bus, falls back to the direct
JSONL append, and — since `Sweep` re-folds — surfaces in every running instance
on the next heartbeat rather than being delivered nowhere until a restart. That
is the smallest honest behaviour for a global store with no per-project filter
in the centre. `Origin.ProjectKey` is recorded on every record, so per-project
filtering (or a "this project / everything" toggle) is a later view change, not
a data migration. Revisit alongside the Phase 4 config page.

**Dismissal origin.** `sidecar notify dismiss` sends the *caller's* origin in
`Request.Origin` with the target id in `Target.Value`. It used to send the
target's origin, which made the host's `MayDismiss` compare the record against
itself and pass unconditionally — anything able to write a request file could
dismiss anyone's notification.

**`-config` before a subcommand.** `internal/cli.Run` now strips leading global
flags (`-config`, `-project`, `-debug`, `-enable-feature`, `-disable-feature`,
either spelling) before matching a command, and applies `-config`. Without it
`sidecar -config <path> notify post` fell through to TUI startup and died with
"Sidecar requires an interactive terminal" — which is exactly the invocation an
isolated proof run needs.

**Other deviations.** The toast's countdown renders minutes and hours above 60s
rather than raw seconds (`5m`, not `290s`). The toast block's lipgloss `Width`
is its outer width — passing the interior width left every toast's title rule
two cells too wide, wrapping a `──` stub onto the next row. `internal/termpreview`
took a narrow-width fix so the workspace preview lays out correctly inside the
content region the panel leaves behind.

**Deferred from Phase 1.** Dragging the panel's resize rail emits a resize
storm: every drag frame re-sizes every plugin, and live panes re-read on each.
Phase 3 already owns the fix ("suppress-while-pane-resizing guard") and the
config toggle for it (1g); it is not worth a second mechanism here.

## Phase 1.5 — two-tier notifications and centre polish

Driven by first real use of Phase 1 and by the call-site audit at
`docs/reference/audits/notification-inventory.md` (85 toast call sites:
24 keep, 15 consider, 46 remove). All decisions below are settled with Marcus
(2026-08-19).

**1. Two tiers: notifications and status flashes.**

- **Notification** — the Phase 1 artifact, unchanged in kind: bordered toast,
  lands in the centre, counts in the header. For agent events, real errors,
  blocked actions with a reason, and surprising state changes.
- **Status flash** — new, lightweight, **not stored**: a single line at the
  top-right of the content region (same corner as toasts, for spatial
  consistency), starting with a colored source glyph, fading in and out. No
  centre entry, no history, no unread count. This is the home for routine
  confirmations — "Saved", yank/copy, sidebar toggles — that deserve feedback
  but not persistence.
- Call sites choose the tier explicitly: keep `msg.ShowToast` → notification,
  add a parallel flash message/helper. The flash path never touches the store.
- **Flashes replace, never queue:** a new flash immediately supersedes the one
  on screen. (Notification stacking/queueing — max 3 vertical then queue,
  macOS-style — is Phase 3's spec and is not pulled forward.)
- **Fade** is real color interpolation: 2–3 luminance steps in and out using
  theme colors, driven by a tagged tick; degrade to plain appear/disappear on
  dumb/slow terminals. If it proves too costly it can be backed out to
  appear/disappear, but start with the interpolation.

**2. Re-tier the audited call sites.** Work from the audit doc's tables:

- **REMOVE rows (46):** pure no-ops are deleted outright — "Already on this
  project/worktree", "Nothing to undo", "No title/content to copy",
  "Showing all/X sessions", "Nothing to commit". Every other REMOVE row
  (yank/copy confirmations, "Saved", sidebar toggles, "Opened …", move
  success) becomes a status flash.
- **KEEP rows (24):** stay full notifications. Route the blocked-action and
  merge/commit-lifecycle rows (audit rows 32–35, 72, 79) through more specific
  sources than `system` so hue/priority distinguishes "act on this" from FYI.
- **CONSIDER rows (15):** resolve during implementation by reading the actual
  dynamic strings at each site, per the questions in the audit doc; split
  mixed sites (error branch → notification, routine branch → flash or delete).

**3. Centre polish.**

- Gradient border on the centre panel, matching every other content pane
  (shared border styling, not a second border rule).
- Centre entries go to **two lines** (title + body) so the CTA isn't lost.
- **Re-show as detail view:** `enter` on a centre entry re-presents that
  notification as a toast — this is the "view details" action and gives
  `enter` a job before Phase 5 rebinds it to target activation (digit keys
  and target jumps remain Phase 5; re-show stays as a secondary key then).
- **Shared symbol logic:** one helper renders a source's glyph + hue, used by
  toast, flash, and centre alike, so an item looks the same everywhere.

**4. Toast presentation tweaks.**

- Countdown made less prominent: dimmer/smaller cells, same tick behaviour.
- Default expiries lengthened: 12s for `agent`, 10s for `system` / `session` /
  `td` / `tasks`; `waiting` stays sticky. **These live in `internal/config`
  from day one** (a minimal `Notifications` section with per-source expiry),
  not as constants — no configui page yet, but the values must be
  user-editable in the config file, ahead of the full Phase 4 page which
  will render them.

**5. Simplify toast interaction — toasts never take focus.** Drop the
focusable-toast model entirely: toasts have **no focus context** and must
never steal focus from whatever the user is doing, including at the moment
they appear. The only interactions are **click to dismiss** and the global
`d` fallback where the focused context allows it (not while an interactive
terminal or editor owns keys — the existing `contextRebindsKey` yielding
already encodes this). A toast appearing must not change key routing, cursor
position, or the focused context in any way. (`alt+n`/`N` opening the centre
remains the deliberate, user-initiated route to interact further.)

**6. Housekeeping.** Commit `docs/reference/audits/notification-inventory.md`
alongside this plan so the phase references a stable document.

## Phase 1.5 as built — decisions and deviations

Landed in commits 1fe1497b (flash tier, config expiries, shared glyph),
73871d81 (centre polish, quieter countdown, click-to-dismiss) and 6bb0d44c
(re-tiering every audited call site), plus a review fix pass. All six items are
implemented. What follows is what a later phase needs and the plan above does
not say.

**The flash tier.** `msg.FlashMsg` / `msg.ShowFlash` / `msg.ShowFlashFrom`,
re-exported as `app.FlashMsg` / `app.ShowFlash` / `app.ShowFlashFrom`. All flash
state and rendering is `internal/app/flash.go`: one slot, replace-never-queue,
tagged `flashTickMsg{seq}` so a burst cannot leave two animations fighting over
the line. It never touches `notify.Store` — no centre entry, no unread count.
Fade is real sRGB interpolation (3 steps in, ~2s hold, 3 out) at a 90ms cadence,
composited by `internal/overlay` against the *content region*, so the centre
narrowing content moves it too. `flashAnimated()` is the degraded-terminal check
(`TERM` empty/`dumb`, or `SIDECAR_NO_ANIMATION`), resolved once via `sync.Once`;
Phase 3's `internal/reveal` should move that helper somewhere shared rather than
adding a second one.

- **Deviation:** the flash sits one row *below* a toast when both are painted
  rather than in the same cell. The spec's "same corner" is honoured; overlap
  would be unreadable.

**Source-aware notifications.** `notify.Alert(source, severity, title)` builds
the `PostMsg`; `msg.Alert(...)` is the `tea.Cmd`, and `msg.Blocked(reason)` is
the refusal shape — a `waiting`/warning with an explicit 6s lease. Use `Blocked`
for every refusal: `waiting` is sticky by default, so a bare waiting alert on a
keypress-frequency refusal leaves one permanent unread entry per keystroke.
(That was the one real defect the review pass found, in gitstatus'
`writeBusyToast`.)

**Config.** `config.NotificationsConfig{Sources: {id: {Expiry}}}`; `"sticky"` /
`"never"` / `0` mean no countdown; an unparseable value is warned and skipped,
never a load failure. `notify.ApplyConfig` binds it — called in `app.New` before
the store opens, on the config-screen save, and in `runNotifyPost` so the CLI's
fallback path completes records the same way. `notify.ExpiryFor` is now the only
correct way to ask how long a source toasts for; `Source.DefaultExpiry` is the
built-in floor. `config.Save` does not manage the `notifications` key, so it
survives on its unknown-key preservation path. Phase 4's configui page renders
over this struct — extend it, do not add a parallel one.

**Centre.** Panel body goes through `styles.RenderPanel` (the shared gradient
pane border, active while the panel is focused), which costs 2 rows and 4
columns — hence `notificationCentreDefaultWidth` 34 → 38 (min/max unchanged) and
the interior geometry shift the hit regions follow. Entries are two rows (title +
`TextSubtle` body) carrying the same item index, so the cursor spans both and a
click on either selects the entry; an entry with no body stays one row. `enter`
= "view details": `reshowNotification` re-presents the selection as a toast.
That is presentation only — a copy with a fresh countdown, no re-post, no
un-dismiss, no unread change — and a sticky re-show gets a `system`-length lease
so the slot is never held forever. When Phase 5 rebinds `enter` to target
activation, move re-show to a secondary key and update both
`keymap/bindings.go` and `notificationCentreCommands`.

**Toasts.** Countdown cells are `▪`/`▫` at `TextSubtle`/`BorderNormal` (tick
math unchanged). There was **no focusable-toast model to remove** — Phase 1 never
gave toasts a focus or keymap context — so item 5 reduced to adding the missing
click route (`regionToast`, registered and cleared inside `renderToastOverlay`,
tested after the centre's column and before content) and dropping the `enter
open` hint that nothing ever honoured. The key row is now `click or d dismiss`.

**Re-tiering.** Sources chosen for the KEEP rows: "Reviewed source changed" →
`waiting`/warning (sticky — a stale review is a correctness risk); every other
refusal → `msg.Blocked`; agent fallback → `agent`/warning; merge aborted,
catalog drift, session ended → `session`; terminal errors → `session`/error; td
setup/status failures → `td`/error; task created → `td`/info. The routine half
of every mixed site became a flash, and the enumerated pure no-ops ("Already on
this project/worktree", "Nothing to undo", "No title to copy", "Nothing to
commit", filter confirmations) were deleted outright.

- **Deviations worth Marcus's eye:** the once-ever default-theme notice (row 27)
  is now a flash and leaves no trace — a one-line revert if that reads wrong.
  Notes' "Archived/Deleted notes are read-only" is a flash rather than a
  `Blocked` because it fires on every keystroke into a read-only buffer.
  `docview`/`filebrowser` still flash "No content to copy" where notes deletes
  it (rows 43/44): silence on a copy key that did nothing reads as a broken
  key, and a flash costs nothing persistent.
- `internal/msg` now imports `internal/notify`, and `internal/notify` imports
  `internal/config` — both leaves, no cycle — so every plugin can post a
  source-specific notification without importing `internal/app`.

## Enhancements

Ordered so each phase ships something visible and none blocks the next.

**Phase 2 — session & waiting triggers.** Post from `agentstatus` lane
transitions: agent finished (`✓ SESSION`, pass/fail colour), agent waiting
(`? WAITING`, sticky — no countdown, "stays"), session died. Debounce so a
flapping status doesn't spam. Workspace list icons are untouched; the
per-source config (Phase 4) is the off-switch for users who don't want both.
Also in Phase 2 (decided 2026-08-19, **built** — see "Phase 2 as built: tab as
a focus stop"): **`tab` cycles focus through the open centre like any other
pane.** With the centre open, the app-level focus cycle
includes it as a stop — tab into it, tab onward back into content — exactly as
other panes participate. This replaces the Phase 1 decision to leave `tab`
unclaimed; `alt+n`/`N`/click remain as direct routes. (When a collapsed toast
is on screen, its `tab expand` (1b) must not fight the focus cycle — prefer a
different expand key if both can be live at once.)


### Phase 2 as built: tab as a focus stop

Only the tab item; the agentstatus triggers are the section after this one.

- **The mechanism is the surfaces' own ring, extended — not a second cycle.**
  `plugin.FocusCycler` (`internal/plugin/plugin.go`) is a new optional
  capability with `AtFocusCycleEnd(reverse) bool` and
  `FocusCycleStart(reverse) tea.Cmd`. A surface that implements it keeps
  cycling its panes exactly as before; the press that *would have wrapped* its
  ring goes to the centre instead, and the next press hands focus back to the
  window the ring resumes at. `panelayout.AtRingEnd` / `panelayout.RingStart`
  answer both questions from the same ring `CycleTarget` walks, so the stop
  can only land where the wrap was.
- **Implemented for the parity pair**: `internal/plugins/workspace`
  (`focus.go`) and `internal/overview` (`focus.go`), each in the one file that
  already owned focus, with a compile-time `var _ plugin.FocusCycler`
  assertion. Workspace declines while a doc or terminal search input is live —
  that surface uses `tab` to leave the input, and a shell stop taking the key
  would strand a drawn search box.
- **Everything else**: `notificationCentreTabKey` (`internal/app/notification_centre.go`)
  runs in `handleKeyMsg` right after the panel's own key block and before any
  surface sees the key. With no `FocusCycler` it takes `tab` only where the
  focused context has not bound it (`contextRebindsKey` / `pluginClaimsKey`),
  and it always stands aside for text input, blocking overlays, interactive
  panes, and any context that binds `tab` to something that is not
  `next-pane`/`switch-pane`.
  **Known limit, deliberate:** on surfaces that own `tab` for a two-pane
  toggle rather than a ring (`git-status`, `notes`, `file-browser`,
  `conversations`, the hosted `td` tab) the centre is *not* a tab stop — those
  toggles have no wrap point to insert into. `alt+n`, `N` and the pointer are
  the routes there. Turning those toggles into rings is a follow-up, and it is
  one `FocusCycler` implementation each, no shell change.
- Leaving is symmetric and consumed: `notificationCentreKey` answers
  `tab`/`shift+tab` itself (`leaveNotificationCentreFocus`) instead of
  releasing them, so one press = one stop and the surface underneath does not
  also act on the key. The panel is never closed by `tab`; `esc`/close/toggle
  are still the only closes. Focusing by tab is the same focused state as a
  click — gradient border active, list keys live, selection marked read.
- Footer/help: `focus-content` is a real command on the panel
  (`notificationCentreCommands`) bound to `tab`/`shift+tab` in
  `keymap/bindings.go`, so the hint row reads `… esc Close · tab, shift+tab
  Content` and the binding is reboundable like any other.
- Verified in the real app through `scripts/tmux-drive.sh` (isolated tmux +
  state): on Workspaces, `tab` walks sidebar → preview → **centre** → sidebar
  with the panel staying open throughout; on the hosted td tab `tab` still
  drives td's own panels, as designed.

### Phase 2 as built: session & waiting triggers

The agentstatus half of Phase 2. Both halves are now done.

- **The rules live in `internal/notify/triggers.go`** (`LaneTracker`,
  `LaneObservation`, `LaneEvents`) and know nothing about tmux, plugins, or
  Bubble Tea. A caller hands `Observe` the **complete** set of workspaces it can
  speak for plus a clock; it gets back notifications to post and ids to
  withdraw. The package imports `internal/agentstatus` (a pure leaf) rather than
  stringly-typing lanes.
- **Transitions, not states, and only settled ones.** A lane must hold for
  `Debounce` (`DefaultLaneDebounce` = 3s) before it is committed. A flap
  working→blocked→working inside that window posts nothing. A committed lane
  becomes the tracker's truth, so the same logical event cannot post twice: the
  next post needs a *different* settled lane first. That is the debounce and the
  dedupe in one mechanism rather than two.
- **First sight is a baseline, never a notification.** Starting Sidecar beside
  four already-blocked agents must not open with four toasts about states the
  user already knew.
- **What posts.** blocked → `waiting`/warning, **sticky**, "`<name>` needs
  input". working|blocked → done → `session`/info, "`<name>` finished" (the
  pass/fail split the plan asked for: a finish is info). working|blocked|done →
  paused **with `Presentation.Health`** → `session`/error, "`<name>` session
  ended". Plain paused (no health) is not a death. idle→done is the done-TTL
  bookkeeping, not a finish, and posts nothing.
- **Self-dismiss — decided.** The tracker **assigns the notification id itself**
  (the store's `Post` is id-preserving and idempotent) precisely so it can name
  the waiting notification later. Any settled transition *out of* blocked
  withdraws it, and so does the workspace disappearing from the observation set.
  A "needs input" toast that outlives the wait is worse than no toast. A
  workspace that simply vanishes gets its waiting withdrawn but earns **no**
  death notification — a shell the user closed is not an incident, and a session
  that really failed reaches the paused/health lane while still observed.
- **Body/identity.** Title carries the shell or worktree name; body is
  `provider · project[/branch] · evidence`, so one of five agents is
  identifiable from the toast alone. `Origin` is `{TmuxSession, ProjectKey,
  WorkDir}` — the same shape the CLI sends, so an agent's own `sidecar notify
  dismiss` and a lane trigger agree on identity.
- **Wiring — a deviation worth recording.** The plan said "the app wires it to
  the heartbeat". The app shell has no per-shell agent state at all: the only
  place `agentstatus.Presentation` exists per workspace is the workspace plugin,
  which already polls. So the adapter is
  `internal/plugins/workspace/agent_triggers.go`, called from the single sweep
  seam at the bottom of `Plugin.Update` (`terminal_control.go`) beside the focus
  rule and the live-watch reconcile — the three status-apply sites each end in a
  dozen early returns, so hooking them individually would have been three
  chances to miss one. It emits ordinary `notify.PostMsg` / `notify.DismissMsg`
  commands; nothing new was added to the app shell.
- **Only readable agents are observed.** A workspace with no `Agent`, or one
  whose provider `agentactivity` cannot read, produces no observation — its lane
  is a projection of legacy status and would announce transitions nobody made.
  Worktrees, top-level shells, and nested (sibling) shells are all in the set;
  leaving nested shells out would make every sibling look vanished and withdraw
  live notifications.
- **Workspace list icons untouched**, as specified. Phase 4's per-source config
  is the off-switch for users who find the icons sufficient.
- Tested without tmux: `internal/notify/triggers_test.go` (baseline, flap,
  once-only, self-dismiss, vanish, per-workspace independence, the
  finish/death lane rules) and
  `internal/plugins/workspace/agent_triggers_test.go` (observation filtering,
  nested shells, the post→dismiss round trip through real `tea.Cmd`s).

**Phase 3 — stacking + reveal.** Max 3 toasts on screen, newest on top;
posts beyond 3 **queue** (macOS-style, decided 2026-08-19) and surface as
slots free, oldest queued first. Same-source collapse to `×N` with peek line
and expand (1b) — this is also where repeated `waiting` refusals dedupe.
`internal/reveal` row machine per the 1h spec; wire toast entry/exit through
it (adopt `flashAnimated()`'s degraded-terminal check via a shared home).
Suppress-while-pane-resizing guard.

### Phase 3 as built: stacking + reveal

- **The stacking rules are state-free and live in `internal/notify/stack.go`**
  (`Stack`, `Layout`, `StackToasts`). The app shell asks what belongs on screen
  and gets back an answer any surface could have computed, exactly as with
  `Toastable` and the lane triggers.
- **Collapse is per *source*, and the source id is the block's identity** — the
  collapse key, the reveal key, and the pointer target are one string. That is
  what makes a block keep its animation when a second notification joins it, and
  it is the mechanism by which repeated `waiting` refusals dedupe: they are one
  block with `×N`, not a column of near-identical ones. Consequence worth
  knowing: there are six sources, so the queue only engages when more than three
  sources are live at once.
- **Admission is first-come-first-served; display is newest-on-top.** Two
  different orderings on purpose: a stack is admitted by its *oldest* member (so
  a chatty source cannot shove a block off the screen the instant before it is
  read) and the admitted blocks are painted newest first per 1b. A freed slot is
  filled by the heartbeat's sweep, not only by the next post.
- **The read gate survives stacking, which was the risk.** Only what is legible
  is recorded as painted: the lead of each *visible* block, plus the listed
  members when the block is expanded. A queued block, a block that did not fit
  the remaining height, and a collapsed member are all unpainted, so expiry
  cannot read them — they stay unread and wait in the centre.
- **Expand key: `alt+e`.** Design 1b says `tab`; Phase 2 spent `tab` on the
  focus cycle whenever the centre is open, and one key cannot mean two things.
  `alt+e` sits in the same family as the centre's guaranteed `alt+n`, is global
  (a toast can be on screen on any tab and has no focus context of its own), and
  falls through untouched when nothing on screen is collapsed. It is registered
  as `expand-toast` in `keymap/bindings.go` like every other binding. The peek
  line renders the real key, not the design's.
- **`internal/reveal`** is the 1h row machine and nothing else: `New/Advance/
  Leave/Resize/Rows/Clip`, one integer, generic over "a block of N rows".
  Because reveal is top-down and retract is bottom-up, both directions are "how
  many rows from the top are painted", so `Clip` is the whole renderer contract
  and a border is never redrawn mid-motion. `reveal.Animated()` is the **shared
  home** for the degraded-terminal check that Phase 1.5 asked for;
  `flashAnimated()` is now a one-line call into it, so the two motions cannot
  disagree about a dumb terminal. `SetAnimatedForTests` exists because the real
  answer is (deliberately) resolved once per process.
- **Wiring.** `internal/app/toast_stack.go` owns the column: `toastStacks` (the
  store's layout plus the presentation-only re-show slot, which takes over its
  source's block rather than opening a second one), `syncToastReveal` (called on
  every path that can change the column — post, `ToastMsg`, dismiss, re-show,
  the 1s sweep, and the reveal tick itself), and the `revealTickMsg` loop, which
  stops the moment every block has settled rather than holding a 90ms timer over
  a still screen. Blocks are rendered at sync time and cached, so the render
  path stays pure.
- **Dismissal — decided.** Click or `d` on a collapsed block dismisses **all**
  its members, mirroring the centre's `D dismiss group`. Making the user clear
  the same block five times to empty it would be a worse bargain, and the `×N`
  told them what they were clearing. Dismissing a re-shown block clears the
  presentation copy only, never the record behind it.
- **Suppress-while-resizing** (1g, and the storm deferred from Phase 1) is
  `Model.overlaysSuppressed()`: while a resize rail is being dragged neither
  toasts nor flashes paint, nothing is recorded as painted (so nothing is read),
  and everything still lands in the centre and the header count. The app knows
  its own centre rail directly; surfaces report theirs through a new optional
  capability, **`plugin.ResizeDragReporter`** — implemented for the parity pair
  (`workspace`, `overview`) by reusing each surface's existing divider-drag
  predicate. Two-pane plugins with their own rails (`notes`, `conversations`,
  `git-status`) are a one-method follow-up each, the same shape the Phase 2
  `FocusCycler` limit has.
- Toasts still take no focus, still never steal it, and click-to-dismiss/`d`
  work per block. Tested in `internal/reveal/reveal_test.go`,
  `internal/notify/stack_test.go`, `internal/app/toast_stack_test.go`; verified
  in the real app through `scripts/tmux-drive.sh` (isolated tmux + state): six
  notifications posted from a second shell drew three blocks newest-on-top with
  `waiting ×3` and its `▾ 2 more · alt+e expand` peek line, the fourth source
  queued, and `alt+e` listed the hidden members.

**Phase 4 — config page.** `Notifications` config section + configui page:
per-source `toast/centre/bell/expiry` table, behaviour block, quiet hours,
`t` test toast (1g). Bell column = terminal BEL. Everything suppressed still
lands in the centre and counts in the corner.

**Phase 5 — calls to action.** Reuse `internal/terminallink` scanning over
notification title/body: td ids, commits, `file:line`, session ids, URLs
become numbered, underlined, project-aware jumps (1e); `enter` activates the
selected/first target; digit keys jump. Cross-project prefix rendering.

**Phase 6 — tasks both ways + td.** `tasks` source adapter posts
due/reminder notifications; `T` files a notification onto a task (REMINDERS
block, 1f) via the tasks CLI. Tasks own snooze semantics on their side —
sidecar never snoozes; a snoozed task simply re-posts later. `td` source for
assigned/reviewable. (No `git` source — see Architecture.)

**Phase 7 — OS integration (optional).** Emit OSC 9 / OSC 777 desktop
notifications for sources that opt in, so tmux/iTerm/Ghostty surface them as
real macOS notifications when sidecar is unfocused. Adapter-shaped, per
source, off by default.
