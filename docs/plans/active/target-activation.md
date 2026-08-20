# Unified target activation — one jump service for every surface

**Status:** Sequence steps 1–3 **done** — see the "as built" sections;
step 4 (regression proof) planned, not started
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

## Step 1 as built

The steel thread landed as three pieces, no behaviour change:

- **`internal/targetactivation`** (new, state-free, no bubbletea, no
  filesystem): `Resolve(uirequest.Target) (Plan, error)`. A `Plan` is data —
  `{Kind, PluginID, Path, Line, URL}` — and the shell turns it into commands, so
  a headless caller can act on the same answer. `PlanOpenFile` names
  `FileBrowserPluginID`; `PlanOpenURL` carries an already-validated URL.
  Unrouted kinds return `ErrUnsupportedKind` (a sentinel, not a malformed-target
  error) so a surface migrating one kind at a time keeps its own branch for the
  rest. The package doc is where the safety discipline lives: URL activation
  refuses anything `terminallink.SafeHTTPURL` rejects, and the
  StripOSC8-before-Decorate rule is written down for every render site that
  feeds the service.
- **Vocabulary.** `uirequest` gained `TargetKindURL` and
  `TargetFromSpan(terminallink.Span) (Target, bool)` — the one span→target
  mapping, replacing what each surface was about to keep privately. It resolves
  nothing (spans arrive resolved), so it is safe in a render pass. `Raw` wins
  over `Value` for file and diff spans, matching what both surfaces already did.
  Resource spans return false: `Target` has no matcher field yet, which is step
  2's job.
- **`app.ActivateTargetMsg{Target, Project}`** + `ActivateTarget` /
  `ActivateTargetIn` helpers, handled in `update.go` by
  `internal/app/activate_target.go`. File plans become `FocusPlugin` +
  `NavigateToFileMsg`. Deviations worth naming: (a) a `Project` naming another
  project is refused out loud with `msg.Blocked` rather than silently activated
  against the wrong project — step 3 replaces that refusal with the
  pending-target slot; (b) URL execution is wired through `terminallink.OpenHTTP`
  rather than stubbed, because the refusal rule was the point and no surface
  sends a URL target yet.

Overview migration: only the FILE branch of `activatePreviewLinkAt` moved. It
now maps the span with `uirequest.TargetFromSpan` and calls
`openPreviewDocTarget(uirequest.Target)` (was `openPreviewDoc(span)`). The two
`ui_requests.go` file sites that used to *build* a synthetic span out of a
`uirequest.Target` just to call the opener now pass the target straight through
— a small deletion the shared vocabulary paid for immediately. Overview keeps
opening its own document pane; the file browser route is the shell's path for
other consumers, and forcing overview onto it would have changed behaviour.
Existing overview tests updated mechanically via one test-local
`openPreviewDocSpan` helper.

Tests: `internal/targetactivation/activate_test.go` (routing, file refusals —
absolute, `~`, escaping `..`, control chars, negative line — URL safety,
unsupported kinds), `internal/uirequest/target_span_test.go` (span mapping),
`internal/app/activate_target_test.go` (the shell route and both refusals).
`go build ./... && go test ./...` green.

## Step 2 as built

Both surfaces now activate through the service; the local kind switches are
gone. No user-visible behaviour changed — mouse entry points, pane placement and
focus outcomes are the ones the existing surface tests already asserted, and
those assertions were not touched.

- **Vocabulary.** `uirequest.Target` gained `Matcher` (the provider-stable
  matcher ID a resource *span* already knows, because a live matcher claimed the
  locator to produce it; the CLI still leaves it empty and the host fills it in)
  and `TargetKindSession`. `TargetFromSpan` therefore maps resource spans and now
  returns true for every activatable span kind.
- **The service** gained `PlanOpenIssue`, `PlanOpenDiff`, `PlanOpenResource` and
  `PlanAttachSession`, plus `PlanForSpan(span)` — the whole span→target→plan path
  in one call, which is what both surfaces use — and `PlanKindsFromSpans()`, the
  parity contract.
- **Public messages** (`internal/app/commands.go`): `OpenIssuePaneMsg`,
  `OpenDiffPaneMsg`, `OpenResourcePaneMsg`, `AttachSessionMsg` and their command
  helpers, handled in `internal/plugins/workspace/target_panes.go`. Each opens
  against the plugin's selected surface using the same functions the
  `sidecar open` journey uses; attach-by-name is a lookup over shells and
  worktree agents, gated on `fullTmuxAttachEnabled()` exactly like the private
  path it wraps. Nothing creates a session that does not exist.
- **Deletions.** The workspace plugin's private `terminalLinkKind` enum and both
  translations are gone: `terminalLink` now carries `terminallink.Kind` plus the
  one thing the scanner cannot know (the canonical root it was resolved
  against), and `spansFromTerminalLinks` is a rebuild rather than a switch.
  `openFileBrowserIfCurrentProject` no longer builds its own
  `FocusPlugin`+`NavigateToFileMsg` pair; it sends `app.ActivateTarget`.
- **URL safety** is now enforced on both surfaces, because both get their URL
  from `Plan.URL` and `Resolve` refuses anything `SafeHTTPURL` rejects.

Deviations worth naming:

1. **File-path containment moved out of `Resolve`.** Step 1 refused absolute,
   `~` and `..`-escaping file targets inside `Resolve`. That is wrong for a
   terminal surface, which legitimately resolves an absolute token against the
   root it scanned — keeping it would have been a regression the moment the
   workspace file branch migrated. `Resolve` now keeps only the surface-neutral
   checks (non-empty, no control characters, non-negative line) and returns the
   token as written; the project-relative rule lives in the new exported
   `targetactivation.RelativeProjectPath`, applied by the one execution that
   needs it (`app.NavigateToFileMsg`). The user-visible refusal is unchanged.
2. **Each surface still executes into its own panes.** The shared half is the
   decision (`PlanForSpan`); the pane opening stays local, because routing
   overview through the workspace plugin's panes would change behaviour, which
   this plan forbids.
3. **`openFileBrowserIfCurrentProject` keeps its current-project guard.** Sending
   the target with a `Project` qualifier instead would have turned today's silent
   no-op into a refusal toast. Step 3 is where that guard becomes the
   pending-target slot.

Parity: `PlanKindsFromSpans()` is asserted complete against
`terminallink.Activatable` in `targetactivation`, and the mirrored pair
`TestPreviewDispatchesEveryPlanKind` (`internal/overview`) /
`TestTerminalDispatchesEveryPlanKind` (`internal/plugins/workspace`) asserts each
surface dispatches all of it — so a new kind cannot reach one surface and miss
the other. Also added: `TestActivatePaneTargetsSendPublicMessages` (the shell
seam) and `TestPublicPaneMessagesOpenTheSamePanesAsAClick` plus the attach gate
test (the workspace handlers). `go build ./... && go vet ./... && go test ./...`
green; no proof run, because nothing user-visible changed and tmux was not
touched.

## Step 3 as built

Cross-project jumps land, and there is now exactly one hand-off across a
project switch.

- **The slot** (`internal/app/pending_target.go`): `Model.pendingActivation`,
  one nullable `pendingActivation{target, selection}`, with
  `setPendingActivation` (newest wins, no queue), `clearPendingActivation`,
  `takePendingActivation` and the single apply site `applyPendingActivation()
  []tea.Cmd`. `switchProjectWithSelection` no longer applies anything itself: a
  caller-supplied `PendingWorkspaceSelection` is *stored in the slot* at the top
  of the switch and applied — with the target, if any — where the old inline
  block was, right after `registry.Reinit`. The same-project branch of
  `navigateFromOverviewAction` also goes through the slot (set, then apply
  immediately), so the workspace pending-selection pair is a client of the slot
  everywhere and no parallel mechanism remains. A target is *re-emitted* as an
  `ActivateTargetMsg` (with `Project` cleared) rather than executed inline, so
  the landing goes back through the ordinary activation route and its guards,
  against the rebuilt registry.
- **Cross-project activation** (`activateTargetInOtherProject`): validate the
  target *before* switching (a malformed target refuses where the user is,
  rather than after tearing down their plugins), resolve the project, park the
  jump, switch, land. `resolveProjectPath` matches configured projects by exact
  path, normalized path, name (case-insensitive) or base name, and accepts an
  unconfigured but real absolute checkout; anything else is declined out loud
  with `msg.Blocked`, never dropped. It also reports whether the qualifier named
  a *checkout* (a path) or a *project* (a name): a path is an exact destination,
  so the remembered last worktree must not override it — the same rule worktree
  cards already follow, and the one that keeps a relative file target resolving
  where it was meant to.
- **Guards.** A plan naming a plugin the (possibly just rebuilt) registry lacks
  is refused out loud instead of focusing nothing. `FocusPluginByID` and
  `activateProjectSwitcherDestination` clear the slot: a user who navigated by
  hand is not waiting for a jump they no longer asked for. A landing takes the
  slot before it emits its own focus, so it never eats itself. A second jump
  arriving before the first lands overwrites the slot. A qualifier that resolves
  to where the user already is (or a switch that declines) activates
  immediately rather than parking a hand-off nothing would apply.

Deviation worth naming: **the workspace guard's silent no-op became a jump.**
`openFileBrowserIfCurrentProject` is now `activateFileForRoot` and sends
`app.ActivateTargetIn(target, root)` when the terminal's scanned root is not the
current project, instead of returning nil. This is the one user-visible change
in the whole plan, and it is the point of step 3: that guard was the last place
a link named something real and nothing happened. The host still owns the only
thing the shell cannot know (which root the terminal was scanned against); it
just stopped swallowing the answer.

Tests (`internal/app/pending_target_test.go`): apply-once-and-clear, newest
wins, cleared by navigation, selection delivered through the slot, unresolvable
project declined out loud, malformed cross-project target refused *before* any
switch, absent plugin declined, current-project qualifier lands immediately, and
path-vs-name resolution. Plus
`TestForeignRootFileLinkAsksForACrossProjectJump` (workspace). The stale
`TestActivateOtherProjectIsRefusedForNow` is deleted — that refusal is what this
step replaced. `go build ./... && go vet ./... && go test ./...` green; no proof
run yet (step 4 owns it) and tmux was not touched.

## Review of steps 1–3 (independent, fix-then-ship)

The full diff since `feeba195` was reviewed against this plan: migration drift
kind by kind on both surfaces, deleted-vs-still-reachable old paths, the parity
pair, the pending-target slot's lifetime, the safety rules, state-freeness and
startup latency. Findings:

- **Fixed — a path qualifier naming the main repo was "current" inside a linked
  worktree.** `targetProjectIsCurrent` matched the qualifier against
  `m.ui.ProjectRoot` as well as `m.ui.WorkDir`. Step 3 turned the workspace
  guard into a real jump that sends the *scanned root* as the qualifier, so a
  terminal rooted at the main repo, activated from a linked worktree, would be
  called "already here" and its **relative** file target resolved against the
  worktree — silently opening a different branch's copy of the file. A qualifier
  given as a path now only matches the checkout the user is actually in;
  qualifiers given as a *name* still match either, since a name names the
  project. (`TestPathQualifierForMainRepoIsNotCurrentInAWorktree`,
  mutation-checked.)
- Verified sound: `terminallink.Activatable` covers exactly the five kinds the
  workspace plugin's deleted enum covered, so the `activatableTerminalLinks`
  rebuild filters identically; the diff `Raw`-or-`Value` fallback and the file
  `Raw`-wins rule survive the span↔link round trip; both surfaces `StripOSC8`
  before `Decorate` and both take their URL from `Plan.URL`; the private
  `terminalLinkKind` enum, `openPreviewDoc(span)` and
  `openFileBrowserIfCurrentProject` are gone with no callers left; the public
  pane handlers open through the same `…ForSurface` functions and the same
  `WorkspaceDocPanesDisabledDiff` gate as the `sidecar open` journey; there is
  exactly one `SetPendingWorkspaceSelection` call site in the shell, so the
  absorbed pair left no twin; `switchProjectWithSelection` has no early return
  between storing the slot and applying it, so nothing can strand a hand-off for
  a later, unrelated switch; `targetactivation` imports nothing stateful; and
  nothing was added to any `Init`/`Start` path.

Remaining risk for step 4: `previewHandlesPlanKind` / `terminalHandlesPlanKind`
are hand-written mirrors of the real dispatch switches, so the parity pair
proves a *declaration*, not the dispatch itself. A new kind added to the helper
but not to the switch would pass. The regression proof should therefore exercise
each kind on both surfaces for real, which is what step 4 already promises.
