# Plan: Deliver Sidecar themes to the embedded td monitor

**Status:** complete **Research snapshot:** 2026-08-17 **Scope:** Sidecar portion only **Canonical full plan:** `~/code/td/docs/plans/active/sidecar-theme-parity.md` **Related td issue:** `td-8d698b`

## Outcome

The td monitor embedded in Sidecar receives the active resolved Sidecar palette at construction and whenever that palette changes. Theme preview, cancel, confirmation, project switching, configuration changes, and restart all repaint the existing td model without resetting its UI state or polling lifecycle.

td owns the semantic theme contract and all td renderer migration. Sidecar owns only:

- translating its normalized palette into that contract;
- delivering initial and live theme changes through the plugin lifecycle;
- retaining its optional gradient-border renderers;
- pinning the released td producer version and proving the real embedded journey.

Do not copy td styles into Sidecar or make Sidecar reach into td package globals.

## Current Sidecar boundary

`internal/plugins/tdmonitor/plugin.go` currently constructs td with:

- `styles.CreateTDPanelRenderer()`, whose closure reads the current Sidecar theme each time and therefore gives panel borders live gradient colors;
- `styles.CreateTDModalRenderer()`, which similarly themes some outer legacy modal borders but still contains hardcoded special/depth gradients;
- `buildMarkdownTheme()`, which snapshots part of the Sidecar palette once at model construction.

The rest of td's colors are internal defaults. There is no complete palette adapter or live notification to the embedded model. Direct calls to `theme.ApplyResolved` / `styles.ApplyTheme` occur across startup, project and worktree switching, theme-switcher preview/restore/confirm, project-add preview, and the configuration surface; changing package-level Sidecar styles does not tell td to rebuild its own derived styles or cached markdown.

The Tasks plugin is the closest current embedding precedent: `internal/plugins/tasks/theme.go` projects Sidecar colors into an owned public theme contract rather than importing Tasks' internal styles. Reuse that shape, but td additionally needs explicit live retheming because Sidecar's theme switcher previews an already-running monitor.

## Design decisions

### A narrow palette adapter

Add a helper beside the plugin (for example `internal/plugins/tdmonitor/theme.go`):

```go
func buildTheme() monitor.Theme
```

It reads `styles.GetCurrentTheme().Colors`, which has already passed through Sidecar resolution, community conversion, overrides, and normalization, and maps semantic slots explicitly. It must not pass a Sidecar theme name and must not depend on td's defaults.

Keep the mapping reviewable as named fields, like the Tasks adapter. Avoid reflection or JSON conversion: the two palettes are separate contracts and a new field on either side should require an intentional mapping decision.

Recommended mappings, subject to the final td field names:

| td semantic role | Sidecar source |
| --- | --- |
| primary / secondary / accent | `Primary` / `Secondary` / `Accent` |
| success / warning / error / info | same semantic Sidecar slots |
| text primary / secondary / muted / subtle / selection | matching text ramp slots |
| background / surface / selection | `BgPrimary` / `BgSecondary` / `BgTertiary` |
| raised surface | `SurfaceRaised` |
| border / muted / active | `BorderNormal` / `BorderMuted` / `BorderActive` |
| foreground on filled semantic states | `OnPrimary`, `OnWarning`, and `TextInverse` for error/danger fills |
| link | `Link` |
| syntax / markdown style | `SyntaxTheme` / `MarkdownTheme` |

The adapter should pass a complete td theme. Do not set “replace all” or invent fallback behavior in Sidecar; td owns normalization and forward-compatible defaults for its contract.

### Explicit live delivery

Theme application in Sidecar must produce one host-level notification after the new palette is active. Prefer one centralized application/broadcast path over adding td-monitor calls to every theme-switcher branch.

The notification must cover:

- startup theme resolution;
- global and project theme preview movement;
- preview cancel/restore;
- confirmed global or project selection;
- project/worktree switching and Overview → project entry;
- project-add theme preview and cancellation;
- Configuration applying theme changes.

The td plugin consumes that notification and synchronously calls td's state-preserving runtime theme method on the Bubble Tea goroutine. If the model is still loading, no command is queued: the eventual construction reads the latest `buildTheme()`. If the model is unavailable/setup/not-installed, the Sidecar-owned fallback views already read `styles.GetCurrentTheme()` during render and require no td call.

Do not solve live updates by re-running plugin `Init`/`Start`, recreating `monitor.Model`, or starting a new polling chain. A theme change is presentation state only.

The notification should be usable by other embedded plugins if they later need live retheming, but keep it small: a theme-changed event or optional host capability is enough. Do not create a general event bus or move palette ownership out of `internal/styles` for this feature.

### Preserve host-owned chrome

Continue supplying `CreateTDPanelRenderer` and `CreateTDModalRenderer` for Sidecar's gradient borders. Update their hardcoded cyan/orange/green/red special gradients to derive from the current normalized Sidecar palette or its existing color blend helpers, so community and light themes do not retain frozen dark theme accents.

The renderers remain closures over current Sidecar style state and retain their existing geometry/state contract. td's new semantic theme owns inner text, fills and interaction states; the Sidecar renderers own only the outer border.

Retire `buildMarkdownTheme()` after the td contract includes markdown slots and the released API no longer needs the compatibility field. Until then, avoid passing two contradictory sources: the complete `Theme` is authoritative.

## Implementation phases

### Phase 1 — Prepare the consumer against local td

- Wait for the td plan's contract/default steel thread to be independently reviewed.
- Use a temporary local `replace github.com/marcus/td => ../td` to compile and exercise the consumer before the td release; never commit or ship the replace.
- Add the explicit `buildTheme()` adapter and pass it in `monitor.EmbeddedOptions` from `buildMonitor()`.
- Add mapping tests that use an unmistakable test palette and assert every td slot. Test the adapter itself rather than duplicating td rendering tests.
- Keep async construction guarantees intact: reading the in-memory current theme is fine; do not add filesystem work to `Init` or the first-frame path.

### Phase 2 — Centralize and broadcast theme changes

- Inventory every current `theme.ApplyResolved` and direct `styles.ApplyTheme` caller. Route user-visible theme changes through one Model-level helper that applies the palette and emits/delivers the notification.
- Preserve immediate same-frame theme previews in Sidecar's own chrome and ensure the td plugin receives the update before its next `View`.
- Make the plugin apply live themes only when `p.model != nil`; construction and unavailable fallback paths use the latest current palette naturally.
- Add tests for preview → second theme → cancel restore and for confirmed project switching. Assert the td model instance and polling lifecycle are not replaced.
- Ensure theme changes are broadcast to inactive plugins as well as the active tab, so returning to td never reveals a stale frame.

### Phase 3 — Finish host chrome and compatibility cleanup

- Derive td panel/modal special gradients from current theme semantic colors, with contrast-safe blending where a second stop is needed.
- Remove the markdown-only builder once Sidecar pins a td release whose complete theme owns markdown; retain no duplicate palette mapping.
- Document the td embedding/theme contract near the plugin or in an active embedding guide, including producer-first release order and live-update expectations.

### Phase 4 — Pin the producer release and prove the real app

- After td completes its full plan, passes independent review, and publishes a verified release, remove the local replacement and update Sidecar's td dependency to that tag.
- Run focused td-monitor/plugin and app theme-switching tests, then Sidecar's full build, test and vet gates.
- Independently review the Sidecar adapter, notification coverage, plugin lifecycle behavior, and dependency diff.
- Run the affected journey through the actual embedded tab using an isolated tmux server and isolated Sidecar state/config. Capture ANSI-aware or PNG evidence; plain text capture does not prove colors.

## Sidecar-focused verification

### Automated contracts

- `buildTheme()` maps every current Sidecar semantic source to the intended td field and does not pass names/registries/config objects.
- Initial model construction receives the active project-resolved palette.
- A live theme event updates the same td model without `Init`, `Start`, database reopen, duplicate tick command, selection loss, or modal/form reset.
- Preview cancellation reapplies the original palette.
- Project switch/reinit cannot let a stale async `MonitorReadyMsg` adopt a model built for the prior project/theme; the eventual model uses the current lifecycle palette.
- Theme notifications reach the td plugin while it is inactive.
- Not-installed, setup, loading, and `.todos` conflict views continue to follow the current Sidecar theme.
- Host border gradients contain no unexplained fixed semantic colors.

### Gates

```sh
go test ./internal/plugins/tdmonitor/ ./internal/app/ ./internal/theme/ ./internal/styles/
GOWORK=off go build ./...
go test ./...
go vet ./...
```

### Real journey matrix

Exercise Sidecar Modern, a materially different light theme, a community theme, and a project theme with explicit overrides. For each, inspect td's list, kanban, selected rows, statuses, issue/nested modals, form, help, markdown/code, confirmations, hover and error/loading states. Include:

- live theme-switcher movement;
- Esc restore;
- Enter confirmation;
- global versus project scope;
- switching between two projects with different themes;
- restart persistence;
- an ordinary and a narrow terminal size.

Use `./scripts/tmux-drive.sh paths` before proof, confirm both tmux and Sidecar state are isolated, and stop the isolated run afterward. Never stop, restart, kill, or replace the default tmux server.

## Release order

1. td implements and independently reviews the complete theme contract and all renderer migrations.
2. Prove Sidecar locally against td with a temporary `replace`.
3. td publishes and verifies its release/tag.
4. Sidecar removes the replacement, pins the public td release, runs all gates, and completes independent review and isolated real-app proof.
5. Commit the focused Sidecar consumer change. Do not push or release Sidecar unless separately requested.

## Definition of done for Sidecar

- The plugin passes a complete semantic theme at construction and updates the same live td model on every resolved-theme transition.
- No theme path relies on plugin restart, model rebuild, or td package globals.
- Sidecar's td-specific outer chrome derives from the active palette.
- Tests cover initial, preview, restore, confirm, inactive-tab, project-switch, late async adoption, and fallback-view behavior.
- The dependency points to a verified td release and no local `replace` remains.
- Isolated visual proof shows full embedded td parity across dark, light, community, and overridden project themes.
- The Sidecar portion is independently reviewed after integration.

## Out of scope here

- Migrating td's internal renderers; that belongs to the canonical td plan.
- Sharing Sidecar theme registries or config types with td.
- Redesigning the theme switcher or td monitor layout.
- Adding new CLI/API surfaces for a host-owned presentation choice.
- Changing Tasks theming except where a shared Sidecar theme notification can remain harmless and future-compatible.
