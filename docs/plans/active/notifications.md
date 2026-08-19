# Notifications — toasts, centre, indicator, sources

**Status:** planned, not started
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
- The panel can stay open indefinitely; unread state updates live (the store
  is in the app model, no polling needed).

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
   (td, git, files, notes, workspaces, kanban/task views) at narrow widths.
7. Route `ToastMsg` through the new system and delete the footer toast path.
8. Proof run via `scripts/tmux-drive.sh` (isolated state — see AGENTS.md):
   post from a second shell, snap the toast, the indicator, the open panel on
   at least three different plugins, dismiss, restart, verify persistence.

## Enhancements

Ordered so each phase ships something visible and none blocks the next.

**Phase 2 — session & waiting triggers.** Post from `agentstatus` lane
transitions: agent finished (`✓ SESSION`, pass/fail colour), agent waiting
(`? WAITING`, sticky — no countdown, "stays"), session died. Debounce so a
flapping status doesn't spam. Workspace list icons are untouched; the
per-source config (Phase 4) is the off-switch for users who don't want both.

**Phase 3 — stacking + reveal.** Max 3 toasts, newest pushes others down;
same-source collapse to `×N` with peek line and `tab` expand (1b).
`internal/reveal` row machine per the 1h spec; wire toast entry/exit through
it. Suppress-while-pane-resizing guard.

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
