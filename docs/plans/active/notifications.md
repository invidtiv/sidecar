# Notifications — toasts, centre, indicator, sources

**Status:** planned, not started
**Created:** 2026-08-18
**Design:** claude.ai/design project `3172ac49-4413-4a60-9235-0afa5c77cf77`, file `Sidecar Notifications.dc.html` (frames 1a–1h). The design is authoritative for visual grammar; where it invents a config screen, we use the existing `internal/configui` instead.

## Summary

A real notification system replacing the single-line footer toast: macOS-style
toasts drawn as bordered floating blocks in the top-right of the content pane,
a notification centre panel grouped by source, an unread indicator in the
header next to the gear, per-source config on the existing config screen, and
an agent-facing `sidecar notify` CLI so agents can post (and dismiss their own)
notifications. Tasks integrate both ways: due tasks post here, and any
notification can be filed back onto a task as a reminder.

Design frames, for reference while implementing:

- **1a** single toast: bordered block, source-hued border + rule line, title,
  body, key row with `enter open · d dismiss · s snooze`, cell-drawn countdown
  `▰▰▰▱▱ 4s`.
- **1b** stacking: newest on top, max 3 on screen; same-source toasts collapse
  into one block with `×N` and a "▾ 2 more · tab expand" peek line.
- **1c** the centre as a right-hand panel (resizable rail, like other split
  panes), opened with `n`, closed with `esc`; sections per source
  (`◆ AGENTS`, `? WAITING`, `✓ SESSIONS`, `○ TASKS`, `■ TD`, `▲ GIT`), unread
  `●`, times in the shared meta column. Footer: `j/k move · enter open ·
  d dismiss · D dismiss group · m mute source · T to task · esc close`.
- **1d** corner indicator next to the gear, ≤5 cells: `·` empty, `●3` unread,
  `?12` agent waiting, red `●99+` on failure, `◌4` muted, inverted when the
  panel is open. Colour carries the loudest unread source.
- **1e** calls to action: every id in a notification is a project-aware jump —
  td issue → td tab, task → tasks tab, commit → git diff, `file:line` →
  files/$EDITOR, session → attach, web → $BROWSER (confirmed). Numbered 1–N
  for keyboard jumps. Cross-project targets render `repo/td-xxxxxx`.
- **1f** tasks both ways: due task posts a notification; `T` on any
  notification writes a REMINDERS entry onto a task.
- **1g** sources config: per-source rows with `toast / centre / bell / expiry`
  columns, plus behaviour block (stacking on/off, max toasts, quiet hours,
  suppress-while-resizing, `t` test toast). "Anything suppressed still lands
  in the centre and still counts in the corner."
- **1h** the reveal spec — the only motion allowed: one whole row per frame,
  ~90ms apart, no subpixel travel, no fades; reveal top-down (4 frames in),
  retract bottom-up (3 out) so the border never redraws twice; countdown ticks
  one cell per second, no tween; skipped entirely on dumb/slow terminals — the
  toast just appears.

## Architecture

One core package, thin surfaces — per the usual rules (core in a library;
every capability has a non-interactive path; store behind a narrow interface,
JSONL first).

- **`internal/notify`** — the model and store. `Notification{ID, Source,
  Severity, Title, Body, Targets []Target, CreatedAt, ReadAt, DismissedAt,
  ExpiresAt, Origin, Sticky}`. Sources are registered (`agent`, `waiting`,
  `session`, `tasks`, `td`, `git`, plus external registrations), each with a
  glyph + hue, mirroring 1c/1g. Store is JSONL under the sidecar state dir
  (`notifications.jsonl`, appended events: posted/read/dismissed/snoozed),
  compacted on load, 24h retention for dismissed items, behind a small
  `Store` interface. State-free resolution logic (what may toast, what
  counts as unread, loudest-source colour) lives here so a headless caller
  could adopt it.
- **Posting API (in-process):** a `notify.PostMsg` tea.Msg + helper in
  `internal/app/commands.go`, exactly like `ToastMsg` today. Existing
  `msg.ShowToast` callers keep working — `ToastMsg` becomes a thin adapter
  that posts a `system`-source notification (footer rendering retired once
  toast blocks land).
- **Posting API (out-of-process):** a new `uirequest.Action` (`notify`) on
  the existing file-RPC bus (`internal/uirequest`), and a `sidecar notify`
  command in `internal/cli/registry.go` — which puts it in `sidecar --agents`
  for free. `sidecar notify post|dismiss|list` with `--json`. Dismissal via
  CLI is allowed only for notifications whose `Origin` matches the caller
  (agents dismiss what they created); `list` reads the JSONL directly so it
  works even with no TUI running.
- **Triggers:** the 1s heartbeat already resolves `agentstatus.Presentation`;
  lane *transitions* (working→blocked, →done, session ended/failed) post
  notifications. Tasks/td/git sources are polled adapters behind the same
  interface, added in later phases.
- **Rendering:** toasts composite over the active plugin's content region via
  `internal/overlay` (command-palette precedent for floating bordered
  surfaces). The centre is a `ModalNotifications` layer in
  `internal/app/model.go`'s modal enum, rendered as a right-hand panel with
  the shared resize-rail grammar.
- **Reveal framework:** a tiny `internal/reveal` package implementing the 1h
  spec — a row-count state machine driven by a tagged tick (~90ms), generic
  over "a block of N rows". Nothing else in sidecar animates today except the
  intro; this becomes the reusable primitive if anything else ever needs it.
  Auto-disabled when the terminal is slow/dumb (and under
  `SIDECAR_STARTUP_TRACE`-style escape hatch).
- **Config:** a top-level `Notifications` section in `internal/config`
  (pattern: `Selection`, `TerminalResources`) + a new `configui` page with the
  1g table. Suppressed ≠ dropped: everything lands in the centre and counts.

## Steel thread (Phase 1)

Smallest end-to-end slice that proves the whole pipe: **an agent posts a
notification from a shell; a bordered toast appears top-right; the header
shows `●1`; `n` opens the centre listing it; `d` dismisses it; it persists
across restart until dismissed.**

1. `internal/notify`: model, source registry (hardcoded set), JSONL store,
   unread/loudest resolution. Unit tests.
2. `notify.PostMsg` handling in `internal/app/update.go`; store owned by the
   app model; posted-on-1s-tick expiry sweep.
3. `uirequest` `notify` action + `sidecar notify post` (title, `--body`,
   `--source agent`, `--expiry`) and `sidecar notify dismiss <id>`
   (origin-checked). Fallback: if no TUI instance is running, write straight
   to the JSONL so nothing is lost.
4. Toast rendering: single bordered block, top-right of the content region,
   source hue, key row, cell countdown. **No stacking, no reveal animation
   yet** — it just appears (which is also the spec'd degraded mode, so
   nothing is thrown away later).
5. Header indicator: `●N` / `·` next to the gear inside `headerGeometry()`,
   with a degradation rank (survives longer than the clock, drops before the
   gear); click opens the centre.
6. Centre panel (minimal): `n` toggles a `ModalNotifications` right panel;
   flat list grouped by source with unread `●`, `j/k`, `enter` no-ops for
   now, `d` dismiss, `D` dismiss group, `esc` close.
7. Route existing `ToastMsg` through the new system (footer path removed).
8. Proof run via `scripts/tmux-drive.sh`: post from a second shell, snap the
   toast, the indicator, the centre, dismiss, restart, verify persistence.

Deliberately excluded from the thread: stacking, reveal animation, session/
waiting triggers, CTA jumps, tasks/td/git sources, config page, OS toasts.

## Enhancements

Ordered so each phase ships something visible and none blocks the next.

**Phase 2 — session & waiting triggers.** Post from `agentstatus` lane
transitions: agent finished (`✓ SESSION`, with pass/fail colour), agent
waiting (`? WAITING`, sticky — no countdown, "stays"), session died. Debounce
so a flapping status doesn't spam; the `agentstatus` "done inbox" TTL and the
notification are the same fact, surfaced twice consistently.

**Phase 3 — stacking + reveal.** Max 3 toasts, newest pushes others down;
same-source collapse to `×N` with peek line and `tab` expand (1b).
`internal/reveal` row-machine per the 1h spec; wire toasts (and centre-open,
if it feels right) through it. Suppress-while-pane-resizing guard.

**Phase 4 — config page.** `Notifications` config section + configui page:
per-source `toast/centre/bell/expiry` table, behaviour block, quiet hours,
`t` test toast (1g). Bell column = terminal BEL (and is how "notify me only
by bell" users get their wish). Everything suppressed still lands in centre.

**Phase 5 — calls to action.** Reuse `internal/terminallink` scanning over
notification title/body: td ids, commits, `file:line`, session ids, URLs
become numbered, underlined, project-aware jumps (1e); `enter` activates the
first/selected target. Cross-project prefix rendering.

**Phase 6 — tasks & td both ways.** `tasks` source adapter posts due/reminder
notifications; `T` files a notification onto a task (REMINDERS block, 1f) via
the tasks CLI. `td` source for assigned/reviewable. `git` source for
checks/PR events where we already have the data.

**Phase 7 — OS integration (maybe).** Emit OSC 9 / OSC 777 desktop
notifications for sources with `bell` (or a new `os` column) enabled, so
tmux/iTerm/Ghostty surface them as real macOS notifications when sidecar is
unfocused. Cheap, adapter-shaped, and entirely optional per source.

## Questions (non-blocking; answers refine, don't gate, Phase 1)

1. **`n` as the global centre key** — the design uses it, but `n` is common
   plugin vocabulary. Claim it globally, or make it context-safe (global with
   plugin override, like other level-5 keys)?
2. **Snooze** (1a/1f `s snooze`, "again in 1h"): worth carrying in the model
   from day one (a `SnoozedUntil` field is cheap) or defer entirely to the
   tasks phase?
3. **Centre as modal panel vs. real plugin tab**: the design shows a
   transient right panel (esc closes). Fine to commit to that, or do you also
   want it reachable as a pinned tab eventually?
4. **Waiting notifications vs. the workspace's existing attention UI**: when
   an agent is blocked, sidecar already shows attention state in workspaces.
   Default the `waiting` source to toast+bell on, or centre-only to avoid
   double-alerting?
5. **Scope of `git` source**: sidecar doesn't poll GitHub today. Limit to
   locally-observable facts (checks via `gh` on demand?) or leave `git` out
   until there's a real data source?
