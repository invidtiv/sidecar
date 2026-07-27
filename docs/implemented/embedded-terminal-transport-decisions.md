# Embedded terminal transport decisions

**Status:** Accepted
**Date:** 2026-07-26
**Tracking:** `td-bcd2d4`
**Source:** `docs/implemented/embedded-terminal-audit.md`

## Decision

Sidecar uses one persistent tmux control-mode client per active tmux session.
Only visible terminal consumers keep a client attached. Client startup happens
off the Bubble Tea update path, and the final subscriber leaving a session
closes that session's client.

`%output`, `%extended-output`, and layout notifications are change signals, not
rendering input. Notifications are coalesced per pane and trigger an in-band
`display-message` cursor query followed by `capture-pane -p -e`. The two
commands have distinct tmux response frames and therefore use distinct FIFO
callbacks. The existing subprocess polling path remains the fallback when
control mode cannot start or its connection dies.

Control clients attach with `ignore-size`; a visible consumer supplies the
intended size with `refresh-client -C`. Sidecar feature-detects tmux flow
control by attempting `pause-after` and resumes `%pause` notifications. It does
not version-sniff tmux.

## Why not render `%output` with a VT parser now

The authoritative rendering source remains `capture-pane`. A newly attached
control client receives only future bytes. Seeding a terminal emulator from a
rendered capture cannot recover all state required for exact replay, including:

- scroll regions and origin mode;
- saved cursor and saved attributes;
- alternate-screen history and transitions;
- DEC/private modes already enabled before subscription;
- partial escape sequences and parser state at the subscription boundary.

Adding a parser now would create two subtly different terminal models and make
fallback behavior harder to reason about. Notification-driven capture removes
the process-spawn and idle-polling costs without changing Sidecar's rendered
screen semantics.

A future parser evaluation should require:

1. recorded, replayable control-mode byte fixtures;
2. differential tests against tmux's cell grid after output, resize, and mode
   transitions;
3. alternate-screen, scroll-region, saved-cursor, wide-character, grapheme, and
   split-escape coverage;
4. a resynchronization strategy after attach, dropped data, and fallback;
5. demonstrated removal of existing capture/escape heuristics rather than a
   second permanent rendering path.

## Bubble Tea terminal capabilities

Focus reporting is enabled and raw focus/blur messages are broadcast to
plugins. Polling and control captures can therefore stop while the application
is unfocused and refresh on focus return.

The focused plugin may provide the real terminal cursor. The app translates its
plugin-local row through the application header and suppresses it under modals,
size warnings, and nonterminal surfaces.

Mouse mode is selected by context. Sidecar retains all-motion tracking for
surfaces with hover behavior and allows a focused terminal surface to request
cell-motion tracking. This is a deliberate refinement of the audit's global
cell-motion recommendation: globally dropping motion would regress existing
hover interactions, while terminal contexts still receive the lower-volume
mode that avoids unnecessary escape traffic.

No extra keyboard event types are requested. Bubble Tea v2's default enhanced
keyboard negotiation already provides key disambiguation where supported.
Requesting all keys or release events would expand the event stream and require
new release/repeat semantics throughout existing key handling without improving
the terminal forwarding path.
