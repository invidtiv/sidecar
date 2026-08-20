# Unified target activation — one jump service for every surface

**Status:** planned, not started
**Created:** 2026-08-19
**Depended on by:** `notifications.md` Phase 5 (calls to action) — that phase
assumes everything here is done. This plan is pure architecture: no new
user-visible behaviour, no new link kinds. Behaviour lands in the consumer
plans.

## Problem

"Jump to the thing this text names" is implemented twice, each copy entangled
with its host model:

- `internal/overview/preview_links.go:202` (`activatePreviewLinkAt`) — a kind
  switch whose every branch mutates `*overview.Model` (pane tree, preview
  state, focus), mouse-entry only.
- `internal/plugins/workspace/terminal_links.go` — its own span→link
  translation, then `app.NavigateToFileMsg` for files but plugin-private
  `attachIssuePane` / `attachDiffPane` / `attachResourcePane` for the rest.

Consequences: a third surface (the notification centre) cannot activate
anything without a third copy; the two copies already drift (entry points,
kind coverage); cross-project jumps are impossible because neither copy can
switch projects; and session attach is reachable only through
workspace-plugin-private methods with no lookup-by-name.

## Design

**Vocabulary.** `uirequest.Target{Kind, Value, Line, ...}` is already the
cross-surface target vocabulary (`internal/uirequest/types.go`) and
`uirequest.ResolveTarget` a partial span→target mapping. Extend, don't invent:
add whatever kinds/fields activation needs (session; project qualifier) to
*this* type. `notify.Target` and `terminallink.Span` both map into it.

**The service.** A new app-level activation route — a `tea.Msg`
(`app.ActivateTargetMsg{Target, Project}` + helper in
`internal/app/commands.go`) handled by the app shell, because only the shell
can both switch projects and focus plugins. Resolution logic (which plugin,
which message, is this target well-formed) lives in a state-free function a
headless caller could adopt; the shell only executes the result.

Same-project activation dispatches to the canonical per-kind mechanisms that
already exist: `FocusPlugin` + `NavigateToFileMsg` for files, and new public
`tea.Msg` entries for what is plugin-private today (issue pane, diff pane,
resource pane, session attach). Those messages are the surface-parity seam:
any plugin, the centre, or a future CLI action can send them.

**Cross-project landing.** `switchProject` does a full `registry.Reinit` that
destroys plugin state; the only post-switch hand-off today is the
workspace-specific pending-selection pair (`internal/app/model.go:1042`).
Generalize it: a single **pending-target** slot on the app model — set before
the switch, applied after Reinit (re-emitting the activation msg against the
rebuilt registry), cleared on apply or on any user navigation. One slot, not
a queue: a newer jump supersedes an older one. The workspace
pending-selection pair becomes a client of (or is absorbed by) this slot
rather than remaining a parallel mechanism. Targets whose project no longer
resolves fail with a notification, never a silent drop.

**Session attach.** Public entry on the workspace plugin: attach by tmux
session name (message-shaped, not method-shaped), gated on
`fullTmuxAttachEnabled()` exactly like the private path it wraps.

**Migration.** Overview's `activatePreviewLinkAt` branches and the workspace
plugin's non-file link handling move onto the service; the two local
span→link translations collapse into the shared span→`uirequest.Target`
mapping. The old paths are deleted, not left as fallbacks. Parity rule
applies: a kind that activates on one surface activates identically on the
other.

**Safety.** The scanner does not sanitize; the surfaces do. Centralize the
discipline: activation refuses non-`SafeHTTPURL` URLs, and any surface
rendering untrusted text through `terminallink.Decorate` must `StripOSC8`
first. Put the rule next to the service so a new consumer cannot miss it.

## Sequence

1. **Steel thread:** `ActivateTargetMsg` + state-free resolution + file-kind
   dispatch, same project. Overview's file branch migrates onto it. Proves the
   route without touching project switching.
2. Public messages for issue/diff/resource panes and session attach-by-name;
   migrate both surfaces' remaining branches; delete both local copies.
3. Pending-target slot + cross-project activation; absorb the workspace
   pending-selection pair.
4. Regression proof: every kind × (overview, workspace) × (same project,
   cross-project where meaningful), via isolated tmux-drive.

## Non-goals

New link kinds (session/task *detection* patterns), notification-centre
integration, numbered/digit-key jump UI — all belong to `notifications.md`
Phase 5, which starts only when this plan is done.
