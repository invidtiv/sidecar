# Plan: Content links in the Files rendered-Markdown preview and app-deck panes

**Status:** implemented — all three phases shipped in v1.5.0. **Written:** 2026-08-24, at `614729aa` **Tracking:** `td-d2c999` **Issue:** [marcus/sidecar#306](https://github.com/marcus/sidecar/issues/306) **Related:** [sidecar-wide content links](sidecar-wide-content-links.md) step 7 · [terminal resource providers](terminal-resource-providers.md)

## Outcome

Phases 1–3 landed as written. Three things are worth recording because the plan did not anticipate them:

- **The Phase 2 gate is only half-satisfiable for a Jira-shaped provider.** "The bare URL Glamour prints beneath it does too" cannot hold for an issue-key matcher — it never matches a whole browse URL, so that row correctly stays a browser link. The destination branch is real and covered, but only reachable for `sidecar-github`-shaped matchers.
- **Taking a link over must not remove the browser escape hatch.** A label claim discards the destination from `Value`, which silently killed the cmd-click that `Decorate`'s own contract promises for claimed URLs. The destination is retained on the span and the hyperlink re-synthesized from it.
- **"Renderer-owned" is narrower than "the viewer is in rendered mode."** Below `MinWidthForMarkdown` — reachable at a Document leaf's own floor — `internal/markdown` returns the plain-wrap fallback over the source, and Notes' preview also serves its raw view. Both now gate on `markdown.RendersMarkdownAt`. Glamour does not strip escape sequences already in the source, so a document that writes its own OSC-8 still reaches the widened branch on a genuinely renderer-owned frame; that is bounded rather than overlooked, because the branch cannot yield anything bare automatic matching would not already yield for the same text.

## Goal

Reading a Markdown file in Files should recognize the same activatable references every other Sidecar reading surface already recognizes. Today the readable view — `m` / Render — is the one place they go dead.

Three gaps, in the order a user hits them:

1. **Files rendered Markdown scans nothing.** `filebrowser.contentLinksSafe` returns false for rendered Markdown. Raw source scans all six kinds, so toggling Render *removes* the links you were about to click.
2. **A Markdown link label opens the browser, not the provider.** Glamour emits real OSC-8 hyperlinks, and explicit destinations always beat automatic matching, so `[ZMS-37161](https://avalara.atlassian.net/browse/ZMS-37161)` — how a Jira key normally appears in a brief — can never become a Resource pane on any surface.
3. **Panes the deck opens beside Files scan nothing.** The app content deck scans only its primary plugin. A file opened as a Document leaf next to Files is inert, while the byte-identical Workspace document pane is fully live.

Out of scope: issue-card, note-card, resource-card, and diff bodies. Those scan on no surface today (not Workspace, not Overview, not the deck); making them scan is a separate, wider change.

---

## Current behavior (baseline)

| Surface | Raw source | Rendered Markdown | Where |
|---|---|---|---|
| Workspace terminal panes | ✅ | n/a | `internal/terminallink` |
| Workspace document panes | ✅ | ✅ | `internal/plugins/workspace/doc_links.go:36` → `docview.ScanContentLinks` |
| Global Sessions preview docs | ✅ | ✅ | `internal/overview/preview_doc_links.go:45` |
| Notes preview | n/a | ✅ | `internal/plugins/notes/capabilities.go:57` |
| Git diff / commit body | ✅ | n/a | `internal/plugins/gitstatus/capabilities.go:73` |
| **Files preview** | ✅ | ❌ | `internal/plugins/filebrowser/capabilities.go:109` |
| **App-deck secondary leaves** | ❌ | ❌ | `internal/app/content_deck.go:541` scans `scanPrimary` only |

Supporting facts that shape the work:

- **`docview` has no rendered-mode opt-out.** `docview.ContentLinksSafe` (`internal/docview/content_links.go:44`) rejects only placeholders and unsized models. Workspace and Overview have been scanning Glamour output in production since the content-links steel thread; the technique is proven, and Files is the outlier.
- **Glamour v2 emits OSC-8.** Verified directly against `internal/markdown`: `[CASH-1245](https://avalara.atlassian.net/browse/CASH-1245)` renders as `ESC]8;id=…;https://…BEL` around the label *and* again around the URL text Glamour prints on the following line.
- **`extractExplicit` claims those cells** (`internal/contentlink/render.go:134`) and `yieldClaimedURLs` skips `span.Explicit` by contract (`internal/contentlink/scan.go:156`). Adding `claimHosts` alone therefore changes nothing for a Markdown link.
- **`claimHosts` is host-side config**, read from `terminalResources.providers[].claimHosts` (`internal/config/terminalresources.go:77`), not from a provider's `describe`. No `sidecar-jira` release is required for anything in this plan.
- **`sidecar-jira`'s matcher is issue-key shaped**, so it will never match an entire browse URL. Yielding on the *destination* (the `sidecar-github` path) cannot work for Jira. Yielding on the *label* can, and needs no provider change.
- **Latent geometry bug.** `previewRenderedRows` (`internal/plugins/filebrowser/capabilities.go:164`) bounds its loop with `len(p.previewLines)` — source rows — while reading rows through `previewRenderLine`, which returns `p.markdownRendered` in render mode. Harmless only because render mode currently opts out.

---

## Settled decisions

1. **Files scans rendered Markdown by scanning what was drawn.** Rows are the Glamour output rows, columns are their visual columns. No source-row mapping is attempted or needed: content-link recognition is column-based on an already-rendered frame, and `Extra.Line` is only ever populated by `file:line` matching within a row. This is exactly what Notes and `docview` already do.
2. **Every other opt-out stays.** Inline edit, images, binaries, errors, empty/loading previews, tree search, content search, quick open, project search, info, blame, file operations, and line jump continue to return no surface. Only the rendered-Markdown clause is removed.
3. **Renderer-generated OSC-8 is trustworthy; terminal OSC-8 is not.** The never-rewrite-an-explicit-destination rule exists because a program writing to a PTY can lie about where its label points. Sidecar's own Glamour renderer is not that adversary. Frames drawn by `internal/markdown` may therefore let a claiming provider reclassify an explicit hyperlink; terminal frames keep today's rule unchanged. This is expressed as an explicit opt-in flag, never as a default.
4. **The yield matches the label, or the destination, or it does not happen.** A claimed explicit link becomes a resource span only when the destination's host is listed in that instance's `claimHosts` **and** that same instance's matcher matches, in full, either the rendered label text or the whole destination URL. The label branch is what makes Jira work; the destination branch keeps `sidecar-github` behaving the same in Markdown as it does in a terminal.
5. **All Glamour surfaces opt in together.** Files, Notes, `docview` (Workspace and Overview) set the flag in the same change. A Jira key clickable in a Workspace document and inert in Files — or the reverse — is the bug this plan exists to close, and shipping the flag on one surface would recreate it.
6. **Deck document leaves reuse the existing hit path.** Scanned spans are appended to `appContentDeck.links` with the current generation, so press/drag/release, click-vs-selection, and `openAppContent` activation are inherited rather than reimplemented. `docview.ContentLinkRect` already excludes the gutter, the scrollbar column, and the header row, so a link hit cannot steal a tab, close, or scrollbar click.
7. **No new plugin-side pane machinery.** Files exports geometry; the app deck scans and activates. That is the division the content-links steel thread established, and nothing here changes it.

## Open questions

- Whether the `claimHosts` entry for Atlassian ships as documentation only, or whether Sidecar should suggest it when a Jira provider is configured without one. Documentation-only for this plan; revisit if it proves to be a footgun.
- Whether a token Glamour word-wraps across a row boundary should be recognized. It is not today on any surface, and this plan does not change that. A long path or a key at the right margin simply does not match.

---

## Phase 1 — Files scans rendered Markdown

Closes the issue as written.

1. **Fix the row geometry first.** In `internal/plugins/filebrowser/capabilities.go`, bound `previewRenderedRows` by the length of the slice actually displayed rather than `p.previewLines`. Introduce a single accessor for "the lines the preview is currently drawing" and use it in both `previewRenderedRows` and `previewGutter`, so geometry and rendering cannot disagree again.
2. **Remove the rendered-Markdown clause** from `contentLinksSafe`. Replace it with a positive guard: in render mode the surface requires `len(p.markdownRendered) > 0`, so a preview that has not rendered yet still opts out.
3. **Confirm the rectangle in render mode.** `previewGutter` already returns a zero-width gutter, so `previewTextRect.X` is the panel's content origin and `W` is the full content width. Glamour renders at `previewContentWidth()+2` and its two-column document margin lands inside that box — the margin is visual indent, not chrome, and is inside the scan rect on purpose. Assert this rather than assume it.
4. **Update `TestContentLinkSurfaceOptsOutOfUnsafePreviewStates`.** Remove the `rendered markdown` case; add a positive test that rendered Markdown *does* export a surface, and a case proving render mode with an empty `markdownRendered` still opts out.
5. **Add a geometry test** that a rendered document longer than its source (or shorter) exports a height equal to the rendered rows on screen, not the source line count.

**Gate:** open a Markdown file in Files with Render on; a `td-*` id, a source path, a commit hash, and a configured provider key each underline and activate; the pane opens beside Files with Files' scroll, tab, and tree selection unchanged; a drag across a token selects instead of opening; toggling Render off keeps raw behavior byte-identical.

## Phase 2 — Provider keys inside Markdown links

Makes `[ZMS-37161](https://avalara.atlassian.net/browse/ZMS-37161)` open the ticket card.

1. **Retain the label.** `extractExplicit` currently discards the rendered label text once it has the columns. Carry it on the span — `Extra.Raw` is already defined as "the token as rendered before ready resolution" and is the correct home. Bound it by `MaxExplicitLabelColumns`, which the extractor already enforces on the column span.
2. **Add the opt-in.** A new `FrameOptions` field (`TrustedOSC`, or whichever name survives review) threaded into `Options` and read by `yieldClaimedURLs`. Default false. Document it beside the existing "Why URL yield" comment block, stating precisely why a renderer-owned frame is a different trust domain from a PTY.
3. **Extend the yield.** With the flag set, an explicit span with `Kind == KindURL` and no provider becomes a candidate. Conditions 1 and 3 of the existing rule are unchanged in spirit; condition 2 widens to "the instance's matcher matches the entire label **or** the entire destination". On a label match the span's `Value` becomes the label (the locator the provider is invoked with); on a destination match it stays the URL, exactly as terminal yield behaves today.
4. **Set the flag on Glamour surfaces**: `filebrowser.ContentLinkSurfaces`' consumer in `app.scanPrimary`, `notes`, and `docview.ScanContentLinks`. Terminal scanning (`internal/terminallink`) does not set it. Because `scanPrimary` builds `FrameOptions` from a `contentlink.Surface`, the surface needs to say whether its frame is renderer-owned — one boolean on `contentlink.Surface`, set by the plugins that draw through `internal/markdown`.
5. **Guard the precedence contract.** Existing tests in `internal/contentlink/yield_test.go` and the `AllowedKinds`/claimed-URL pairing test in `internal/docview/content_links_test.go` must still pass untouched. Add cases proving: terminal frames never yield an explicit span; a claimed host with a non-matching label and non-matching destination keeps the browser link; an unclaimed host never yields whatever the label says; an invalid or unterminated destination stays an inert claim.
6. **Document the config.** `claimHosts` for a Jira instance in the terminal-resources reference and the provider docs, with the Atlassian example.

**Gate:** with a Jira provider configured and `claimHosts: ["<site>.atlassian.net"]`, clicking the label in a rendered Markdown link opens the Resource card; the bare URL Glamour prints beneath it does too; the same file in a Workspace document pane behaves identically; removing `claimHosts` restores the browser link; a terminal printing the same OSC-8 sequence still opens the browser.

## Phase 3 — Document leaves in the app content deck

Makes a file opened *beside* Files behave like the same file opened in a Workspace pane.

1. **Scan the leaf body.** In `appDeckContent.View`, for `*docview.Model` leaves, route the body through `ScanContentLinks` with the deck's `resolution` snapshot, `resourceMatchers`, `docview.ContentLinkKinds()`, and `Decorate: true` — the shape `workspace.decorateDocBody` already uses.
2. **Register the hits.** Append each hit to `h.links` with `h.generation`, offset into deck coordinates. Activation, drag suppression, and generation invalidation come free from `appContentMouse`.
3. **Resolve pending file/diff work.** `resolveAppContentLink` is keyed on a `contentlink.Surface` for its `WorkDir`; add a root-keyed variant (or give the existing one a root parameter) so a doc leaf resolves against `h.workdir`. Dedup through the existing `h.pending` map.
4. **Do not scan the other leaf kinds.** Issue, note, resource, and diff bodies stay unscanned, matching Workspace and Overview. Say so in a comment so the omission reads as a decision rather than an oversight.
5. **Tests** in `internal/app/content_deck_test.go`: a doc leaf with a `td-*` id registers a hit at the expected deck coordinates; a hit never lands on the leaf's header, tab strip, or scrollbar column; a drag across a link does not activate; activating from a doc leaf opens into the same deck.

**Gate:** from Files, click a path to open a Document leaf; a token inside that document activates and stacks a tab; Tab still walks tree → preview → each leaf in visual order; the leaf's own scrollbar drag, tab clicks, and close button are unaffected.

---

## Verification

Unit and geometry coverage per phase above, plus one real-app proof once all three land, per `AGENTS.md`:

```bash
./scripts/tmux-drive.sh paths      # confirm BOTH tmux socket and state tree are isolated
SIDECAR_BIN=… ./scripts/tmux-drive.sh start 200 50
… keys / snap …
./scripts/tmux-drive.sh stop
```

The proof fixture is one Markdown file containing, in prose: a bare provider key, a Markdown-linked provider key, a `td-*` id, a `path/file.go:42` reference, a commit hash, a bare URL, and a `sidecar://` internal URI. Capture it in Files raw, Files rendered, a deck Document leaf, and a Workspace document pane — the four projections must agree.

`scripts/demo.sh` gets a corresponding sample document so the feature can be tried without a configured Jira instance (the `td`, file, diff, and URL kinds all demo without a provider).

## Risks

- **Widening `yieldClaimedURLs` touches shared precedence.** It is the single documented exception to built-ins-keep-precedence, and every condition in it is load-bearing. Phase 2 must not weaken conditions 1 or 3, and the flag must be impossible to reach from terminal scanning. If review is uneasy, Phase 1 and Phase 3 ship without it.
- **Glamour's OSC-8 `id=` parameter** is a session-scoped grouping hint. `parseOSC8` already skips the parameter block correctly, but the destination-length and parameter-length bounds should be re-read against real Glamour output rather than assumed.
- **Row-count drift** between what Files draws and what it exports is the failure mode that makes links land one row off. The single-accessor change in Phase 1 step 1 exists specifically to make that unrepresentable.
