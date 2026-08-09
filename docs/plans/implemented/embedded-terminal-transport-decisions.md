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

`%output` and `%extended-output` bytes are ordered rendering input for the one
shared `tty.Model`. A seeded `screenmodel` adapter owns the live grid, cursor,
modes, and bounded history. Layout, pause/continue, discard, and reconnect
events trigger an ordered reseed where necessary. `capture-pane` remains the
bootstrap/resynchronization, lazy-history, diagnostic, and automatic-fallback
adapter; it is not the healthy steady-state renderer.

Control clients attach with `ignore-size`; a visible consumer supplies the
intended size with `refresh-client -C`. Sidecar feature-detects tmux flow
control by attempting `pause-after` and resumes `%pause` notifications. It does
not version-sniff tmux.

## Why the VT parser is behind an adapter

The terminal model uses Charm's `x/vt` through Sidecar's narrow `screenmodel`
adapter. A newly attached control client receives only future bytes, so the
model is transactionally seeded from tmux's saved main grid, active grid, and
metadata before queued output is released. The adapter keeps upstream parser
changes replaceable and makes capture recovery explicit.

- scroll regions and origin mode;
- saved cursor and saved attributes;
- alternate-screen history and transitions;
- DEC/private modes already enabled before subscription;
- partial escape sequences and parser state at the subscription boundary.

The implemented model was accepted only after:

1. recorded, replayable control-mode byte fixtures;
2. differential tests against tmux's cell grid after output, resize, and mode
   transitions;
3. alternate-screen, scroll-region, saved-cursor, wide-character, grapheme, and
   split-escape coverage;
4. a resynchronization strategy after attach, dropped data, and fallback;
5. removal of the plugin-private terminal-panel capture renderer rather than a
   second permanent presentation path.

Known upstream fidelity gaps remain tracked by `td-a04666`; Sidecar does not
fork the parser or add plugin-specific escape repair around them.

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
