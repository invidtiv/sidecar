# Update Flow Redesign — One Modal, Polished

## Outcome

Shipped on `install-modal` (td-c01d67, commits 53601b19..4817f6c3). One phase-driven
`modal.Modal` (internal/app/update_modal.go) carries Overview → Installing → Done/Failed;
`primeUpdateModalFocus`, the per-phase key/mouse switches, the changelog overlay, and the raw
progress box are gone. M2 added two-column rows, library-derived width, a draggable notes bar,
in-place changelog expansion, disabled install buttons, and the Diagnostics Update button.
M3 switched notes to the release body persisted in the version cache and carried per product,
with a tag-pinned disk-cached expansion fetch and styled retry-on-failure. Review cycles caught
and fixed a post-ack updater lockout and two value-copy state bugs. Deferred: notification-centre
dot deep-link to the updater (td-e0512b); retry double-fetch nit. Proof transcripts under
/tmp/update-proof-td-a4f33b/ (runs 14–21 + before-after pair).

The in-app update journey currently walks the user through four separate modal objects — Diagnostics, "Update available", an optional Changelog drawn as a second overlay on top of the first, a hand-rolled progress box that is not a modal at all, and a Complete/Incomplete modal — each with its own width, title style, key handling, and mouse handler. This plan collapses everything after the Diagnostics entry into **one modal that changes phase in place**, and brings it up to the polish standard of the command palette: real scrollbars with draggable thumbs, real buttons (including disabled states), consistent widths and hints, columns instead of hand-indented rows.

The update *engine* (`internal/version`: detection, provenance guards, `Apply`, `verifySuite`, retry/carry semantics) is correct, well-tested, and **out of scope except where the UI reads it**. This is a presentation rewrite.

## Relationship to other plans

- **[modal-redesign.md](modal-redesign.md)** — the aesthetic authority (flat surfaces, columns, minimal form elements). Its inventory already lists `update_modal.go` and `diagnostics_modal.go`; this plan is the application of those rules to the update flow, plus the structural single-modal change that the aesthetic pass alone wouldn't force. If its rules and this plan conflict on a visual detail, that plan wins.
- **[mouse-draggable-scrollbars.md](mouse-draggable-scrollbars.md)** — the modal-viewport scrollbar and `SectionScrollbar` adoption this plan relies on (already shipped in the library; the update modals simply never adopted it).
- One inventory note for modal-redesign.md: its "four hand-rolled overlays" list omits the raw update progress box, a fifth hand-rolled overlay. This plan converts it (M1), so that plan's later `styles.ModalBox`-sweep phase neither double-schedules nor misses it.
- **implemented/td-393e81-tasks-bundled-auto-update.md**, **implemented/spec-auto-update.md**, **implemented/fix-122-self-update-prompt.md** — history of the engine; unchanged here.

## The reference standard

The model to copy is the **command palette** (`internal/palette`): one `modal.Modal` whose sections re-render as state changes — scope header, live input, live count line, list with per-row hover/focus IDs, draggable scrollbar (`ui.RenderScrollbarWithState` + `RegionScrollbarThumb/Track` as `MouseOnly` focusables), a `listWindow()` shared by renderer and pointer handlers so the bar and the list can never disagree. The switcher modals (`worktree_switcher_modal.go`, `theme_switcher_modal.go`) show the same pattern with `modal.SectionScrollbar` routed through the shared core in `internal/app/modal_scrollbar.go`. The `?` help modal is *not* the reference — it looks clean but routes no mouse events at all.

## What's wrong today, concretely

All verified in the tree at the time of writing:

- **Four `*modal.Modal`s, four mouse handlers, four ensure/clear/render triples** (`model.go:326-340`, `update_modal.go`). Width and title change under the user (60 → 70 → 60); the changelog renders as a second overlay with double backdrop (`view.go:266-272`).
- **`primeUpdateModalFocus` exists only because each phase rebuilds a fresh modal** — the new modal has no focus list until it renders once, so the code renders it off-frame to keep Enter working (`update_targets.go:305`).
- **The progress screen is a raw lipgloss box** (`update_modal.go:314`): no hit regions, no buttons, no scroll, no wheel-boundary case. Esc during progress closes the overlay while the batch keeps running; the diagnostics `u`/action paths refuse to reopen it (`u` gated on `!m.updateInProgress`, `update.go:1223`) and the user finds out via toast. Worse, the *other* entry point is gated wrong in the opposite direction: Configuration → About's "Open updater" (`configui/page_about.go:149-154` → `config_surface.go:318-325`) has no in-progress check, reopens the **Preview** phase mid-batch, and "Update Now" there runs `startUpdateBatch` again — incrementing `updatePlanID` (`update_targets.go:143-150`), orphaning the in-flight batch's results while its package-manager subprocess keeps running. That is the concurrent-installer situation the sequential batch design exists to prevent, live today.
- **The Diagnostics "update" action is unreachable by mouse**: both key and mouse paths switch on an `"update"` action that no rendered element ever emits (`diagnostics_modal.go` builds only a Close button); only the bare `u` key works.
- **Release notes are hard-truncated** with a literal `"... (truncated)"` (`update_modal.go:168-178`), and the changelog reimplements scrolling by hand (`changelogViewState`, `changelogWheelAtBoundary`) with a "Lines 12-31 of 402" text line — the modal library's `buildLayout` viewport + draggable bar and `SectionScrollbar` do all of this for free and better.
- **Key/mouse handling is duplicated per phase** in two long switches (`update.go:2507`, `:2656`) with ad-hoc letters (`c`, `q`, `r`) bypassing the modal's action routing.
- **Content correctness**: the changelog is fetched from `main` on GitHub raw (`update_modal.go:21`), so it can show unreleased entries the offered version doesn't contain; fetch failures are stuffed into the body as plain text with no error styling and no retry (`update.go:592-598`); it is cached only for the session (fetched when `m.updateChangelog == ""`), with none of the disk/TTL caching release checks get.
- **Assorted staleness**: fixed 60-column width with a redundant double clamp; `previewChromeLines` hand-estimating chrome the library measures itself; dead-ish helpers (`truncateLine`, `centerText`, `dividerWidth`); hint lines that differ per phase (library default vs hand-written vs none); an inline keyboard-only `[c] View Full Changelog` line rendered as muted text rather than a clickable element; title casing drift ("Update available" / "Update Complete" / "Changelog" / "Updating"); hard-coded `✓ ✗ • ● ○` rows with `\n      ` continuation indents.

## Settled decisions

1. **One `modal.Modal` for the whole journey.** Phases — `Overview → Installing → Done/Failed` — are states of one modal, expressed through `modal.When` and `modal.Custom` sections and refreshed with `Modal.Invalidate()` when async results land. The modal object, its mouse handler, its width, and its title identity persist across phases; buttons change label/disabled state instead of the modal being swapped. `primeUpdateModalFocus`, the four ensure/clear/render triples, and the per-phase key/mouse switches are deleted; keys route through the modal's own action routing.
2. **The changelog is an inline section, not a second modal.** The Overview phase shows the per-target summary (columns, per modal-redesign.md's column behaviour) and the release notes in a scrollable section with a real draggable bar (`SectionScrollbar`, or the body viewport when it's the only scrolling content). "View full changelog" becomes a clickable element that expands the section in place (modal grows within its clamp; no overlay-on-overlay, no width jump). `changelogViewState`, `changelogWheelAtBoundary`, and the truncation string are deleted.
3. **The Installing phase is a real modal phase.** Per-target rows update in place (pending → running with elapsed time → done/failed); `internal/installui`'s idle/busy/failed phase rendering is the in-place model to follow or reuse. Two lifecycle details are part of the decision, not implementation trivia:
   - The 1 Hz elapsed tick currently self-terminates when the modal state leaves Progress (`update.go:583-590`); its continuation condition changes to "batch in flight" so hiding the modal doesn't kill the clock and reopening doesn't need to restart it.
   - The running row's spinner ticks at a normal spinner cadence while the modal is visible (the passive `internal/ui/braille_spinner.go` advanced by a dedicated fast tick, as its other users do) — a spinner advanced once per second reads as frozen. If the fast tick isn't worth its cost here, use a static `●` running marker instead; never a 1 Hz spinner.

   Esc hides the modal (the batch keeps running, as today) — but reopening now works: every updater entry point reopens the in-progress modal in its current phase instead of being gated on `!m.updateInProgress`. The Installing phase renders a phase-specific hint ("Esc hides · update continues" or similar) via `WithHintText`/custom footer — the library's default hint line contains the word "cancel", which would violate the honest no-cancel stance and fail its test. No cancel affordance is added (the stance and `TestProgressModal_NoFalseCancelHint`'s assertion stay).
4. **Done/Failed is the same modal's final phase.** Success: results summary + "Quit & Restart" (when Sidecar was updated) + Close. Failure: failed targets with their `FailureDetail` tails and per-target manual-fix commands in columns, Retry (failures only, existing `RetryTargets` semantics) + Close. One title style throughout (settle on sentence case: "Update available", "Updating…", "Update complete", "Update incomplete").
5. **Diagnostics gets a real Update button.** The dead `"update"` action paths get a rendered `Btn` to emit them; keyboard `u` keeps working. Diagnostics otherwise stays a separate modal — it is a different tool (status for all products) that happens to be the updater's entry point.
6. **Width and measurement come from the library.** Replace `updateModalWidth`'s fixed 60 and `previewChromeLines`'s guesswork with `modal.ContentBoxWidth`/`PreferredListRows` and the library's own layout measurement. One width for the whole journey, responsive to the terminal within the library's clamps.
7. **Release notes come from the release, not `main`.** The Overview's notes render the release body from the GitHub release API, per product, not the raw `CHANGELOG.md` from `main`. This needs plumbing that doesn't exist yet: `ReleaseNotes` only arrives on a fresh API fetch — the cache-hit path returns no notes and `CacheEntry` has no notes field — and the app keeps only Sidecar's notes in a single string, discarding td/tasks notes. So this decision includes persisting notes in the version cache and carrying them per product (on `version.Target` or equivalent). The full-changelog expansion may still fetch the file, but pinned to the offered release's tag, disk-cached like release checks, and rendered with an error state + retry on failure instead of error text as body copy.
8. **The engine's semantics and guards are untouched; additive plumbing is allowed.** `DetectInstallation`'s provenance classification, `Apply`'s pre-flight re-detection, `verifySuite`, the dev-build short-circuit, and the unmanaged-install releases-URL hint are load-bearing and tested (they are what keeps `make install-local` builds from self-destructing) — none of their behavior changes. Decision 7's notes persistence (a `CacheEntry` field, per-product notes on the status/target types) is additive data plumbing and is explicitly permitted.
9. **The implementing agent has license to widen scope within the update/version UI.** The system hasn't had UI attention in a while; adjacent problems discovered during implementation — dead helpers, stale copy, `configui` About-page drift, toast/notification wording, the notification-centre dot lighting for updates while clicking it opens the notification centre rather than the updater — may be folded in when small, or filed as td issues when not. The boundary is decision 8: nothing in `internal/version`'s semantics, and nothing that contradicts modal-redesign.md.

## Unresolved questions

- Whether the notification-centre entry (clicking the header `●N` dot when the only notification is the update toast) should deep-link straight to the updater. Leaning yes-if-small under decision 9; the toast's CTA machinery (`notify/cta.go`) may already support it. Implementer decides.
- Whether Diagnostics and the update modal eventually merge into one surface. Out of scope here; revisit after this ships if the two-modal entry still feels like a seam.

## Work sequence

### M1 — Single-modal restructure

- Introduce the phase-driven single modal in `update_modal.go`: one build function with `When`-gated sections per phase, one render path, one mouse handler, actions routed through the modal. Port Overview and Done/Failed first (they are already `modal.Modal`s), then replace the raw progress box.
- Delete: per-phase ensure/clear caches, `primeUpdateModalFocus`, the per-phase key/mouse switches (fold surviving keys — `q`, `r`, Esc — into modal actions/hints), the changelog modal and its scroll machinery.
- Fix the reopen gates — both directions: the diagnostics `u`/action paths stop refusing while a batch runs, and the Configuration → About "Open updater" path stops reopening the Preview phase mid-batch (today it can start a second concurrent batch via `startUpdateBatch`, orphaning the in-flight one — a live bug this milestone closes). Every entry point converges on "reopen the modal in whatever phase the batch is in"; `startUpdateBatch` additionally refuses while a batch is in flight, as defense in depth.
- **Proof:** unit tests for phase transitions on one modal instance (focus survives phase change without priming); isolated `tmux-drive.sh` run against a fixture release API (`SIDECAR_RELEASE_API_BASE`) walking Overview → Installing → Complete with snapshots at each phase, including Esc-then-reopen mid-install.

### M2 — Polish pass

- Scrollable notes/changelog sections with draggable bars via the library viewport/`SectionScrollbar`; clickable "View full changelog" expansion; columns for target and result rows; disabled-state buttons during install; library-derived width; one hint style; title casing settled; dead helpers removed.
- Build rows and footers on modal-redesign.md's new section primitives (`Header`, `Row`, `Choice`, `Actions`) where its Phase 1 has landed; otherwise match their spec so the later migration is mechanical.
- Diagnostics Update button (decision 5).
- **Proof:** snapshots showing the bar thumb mid-drag (the mouse-draggable-scrollbars plan's proof pattern); a long-changelog fixture exercising the expansion without width jump; mouse-only walkthrough — indicator to installed update without touching the keyboard.

### M3 — Content correctness

- Release-notes source switched to the release body, including the decision-7 plumbing (notes persisted in the version cache, carried per product); tag-pinned, disk-cached, error-styled full-changelog fetch with retry.
- Sweep for decision-9 adjacencies; fold in or file td issues.
- **Proof:** fixture where `main`'s changelog is ahead of the offered release — the modal shows only the release's notes; a fetch-failure fixture showing the styled error + working retry.

## Acceptance evidence

- The existing update test suites pass unchanged where they assert engine behavior (`TestProgressModal_NoFalseCancelHint` may move but its assertion survives; provenance/guard tests untouched).
- Phase-transition unit tests on the single modal (no rebuild between phases, focus list intact, `Invalidate` on async arrivals).
- `tmux-drive.sh` transcripts for the M1–M3 proofs, fully isolated (both tmux socket and state tree — `paths` check first), including the failure path (one target fails → Failed phase → Retry succeeds).
- A before/after screenshot pair for the modal-redesign.md inventory, since this plan discharges that plan's `update_modal.go` and `diagnostics_modal.go` rows.
