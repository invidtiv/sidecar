# Host architecture: one descriptor, two classes, both surfaces

**Status:** design, opened 2026-09-02. **Controlling document:** [README.md](README.md). **Contract:** [protocol.md](protocol.md).

This document is the Sidecar-side half: how a plugin is described, enabled, placed, rendered, refreshed, persisted, and reached from the CLI, and which existing seams each of those reuses. File and line references were read from the tree on 2026-09-02.

## Baseline

What exists, and what this plan turns each into.

| Today | Where | Becomes |
| --- | --- | --- |
| Tab list is data: `assembly.Plan(cfg)` returns ordered `{ID, New}` entries gated by config and flags | `internal/plugins/assembly/assembly.go:74` | The same function over a slice of descriptors, embedded and protocol alike |
| Tasks is an app-owned `globalTasksHost` with a hardcoded `GlobalTab` enum and named keys `8`/`9`/`0` | `internal/app/scope.go:74,571` | A generalized global host list built from descriptors with `Scope: Global`; keys assigned in order |
| Enablement is inconsistent: `plugins.<id>.enabled` for some, feature flags for Tasks/Notes, both for Conversations; the settings page hides the seam | `internal/configui/page_panels.go:13-21` | `plugins.<id>.enabled` for every plugin; flags stay only for experimental host features |
| `configui.Integration{ID, Name, Flag, Why, Descriptor}` is the closest thing to a plugin descriptor and was written to be extended | `internal/configui/integrations.go:22` | Folded into the descriptor |
| External executables: `terminalResources.providers[]`, `resourceprovider.Manager`, describe/resolve, the `Resource` leaf | `internal/config/terminalresources.go`, `internal/resourceprovider`, `internal/panelayout/panelayout.go:28` | The protocol class. Config section aliased, manager extended with `list`/`get`/`act`, leaf gains collection tabs |
| `Resource` leaf has no `livepanes` binding | `internal/livepanes/livepanes.go:11-13` names this as the motivating defect | Gets one, driven by plugin-declared `refresh` |
| `sidecar open --provider ID LOCATOR`, layout spec `{"kind":"resource","provider":…}`, `terminal-links list/check` | `internal/cli/open.go`, `internal/uirequest/types.go:85`, `internal/cli/registry.go` | `sidecar plugin …` family; `--provider` and `terminal-links` kept as aliases |

## The descriptor

One Go type describes every plugin Sidecar can host. It is data, lives in `internal/plugin`, and is the only thing the assembly, the settings page, the global host list, the pane switcher, the palette, and `sidecar plugin list` read.

```go
// Descriptor is what Sidecar knows about a plugin before it runs.
type Descriptor struct {
    ID    string // stable; the config key and the CLI name
    Name  string
    Icon  string
    Class Class  // Embedded | Protocol
    Scope Scope  // Project | Global
    // Placements the plugin can occupy. Tab is a navbar surface; Panes means its
    // content can open as leaves in the workspace and Sessions decks.
    Placements []Placement
    // Enabled reads plugins.<id>.enabled and any legacy switch this plugin
    // migrated from, so a config written before this plan keeps working.
    Enabled func(*config.Config) bool
    // Embedded only: constructs the in-process plugin.
    New func() Plugin
    // Protocol only: the configured instance this descriptor projects.
    Instance *config.PluginInstanceConfig
    // Install and version UX (Homebrew tap, PATH probe). Zero means ships in-repo.
    Integration version.Descriptor
}
```

**Class** decides who renders. `Embedded` plugins implement `plugin.Plugin` and own their frame (Tasks, td monitor, and every plugin in `internal/plugins`). `Protocol` plugins are executables; their descriptor is projected from a config entry after `describe`, and the host renders them through one shared browser.

**Scope** decides lifecycle. `Project` plugins live in the registry and are re-initialized on project switch. `Global` plugins are built once, survive project switches and scope toggles, and close once at shutdown, which is the behaviour `globalTasksHost` exists to guarantee (`internal/app/scope.go:571-580`). A protocol plugin's scope comes from its config entry (`"scope": "global"` default) because the host cannot infer it from data.

**Placements** decide where content shows. A `Tab` placement is a navbar entry. A `Panes` placement means its documents and collections can open in the pane decks of both workspace projections. Tasks is `Tab` only. Jira is `Panes` only. Recall is both: a global tab with a query box, and result panes beside a terminal.

There is no manifest file for embedded plugins: the descriptor is a Go literal in the plugin package. There is no manifest file for protocol plugins either: the config entry is the manifest, and `describe` fills in the rest at run time. A manifest format is what the superseded Herdr plan would have brought, and it is explicitly not adopted, because a config entry plus a `describe` answer is already every fact the host needs.

## Two classes, stated as the user contract

| Gesture | Embedded plugin (Tasks, td) | Protocol plugin (recall, DEX, ongoing, Jira) |
| --- | --- | --- |
| Appears in the navbar | When `plugins.<id>.enabled` and it declares `Tab` | When enabled, `describe` succeeded, and it declares `Tab` |
| Number key | Project tabs `1`–`7`; global tabs `8`, `9`, `0` in descriptor order, then none | Same rule, same pool |
| Keys inside it | Its own, routed through `KeyRouter` as today | Host-owned browser keys; plugin actions through the action menu, palette, or a granted letter |
| Content in a pane | Not in this plan (its TUI is one frame) | Collection tabs and document tabs in the `Resource` leaf, both surfaces |
| Theme | Injected at construction and on theme change, as today | Host renders in the host theme; nothing to inject |
| Refresh | Its own watcher or poll | `refresh.watch`, `refresh.everySeconds`, `sidecar plugin changed` |
| Project switch | `Project` scope: Reinit; `Global`: untouched | Same by scope; `project` context re-sent on the next call |
| Remote-bound surface | Refuses naming the host unless it reads through host verbs | Receives `project.hostId`; refuses or answers on its own terms |
| Unavailable | Its own not-installed or setup card | Setup card from `invalid_config` + `setupHint`, or the transport failure card |
| Settings page | One row: enable, install if missing, restart note | One row: enable, `describe` status, declared context, docs link |
| CLI | `sidecar plugin list` shows it with class `embedded` | `sidecar plugin list|check|call|add|remove|enable|disable|changed` |

A protocol plugin that Sidecar's release knows nothing about is the point of the second column: the user adds a config entry naming an executable, and every row above applies with no Sidecar change.

## Generalizing the global host

`GlobalTab` is an enum with three values and named keys today (`scope.go:74`, `globalTabKey:272`). It becomes an ordered slice of global surfaces built at startup:

1. Sessions and Activity stay app-owned surfaces, first in the order, and keep `8` and `9`.
2. Every enabled descriptor with `Scope: Global` and a `Tab` placement follows, in descriptor order. The first gets `0`; the rest are reachable through `[`/`]`, the palette, and the pane switcher.
3. `tabRef.global` becomes an index into that slice rather than an enum value. `globalTabsVisible`, `ensureVisibleGlobalTab`, `headerEntries`, `globalMouse`, `persistID`, `context`, and `surfacePlugins` (`scope.go:166-775`) read the slice.
4. `globalTasksHost` becomes `globalPluginHost`, one per global descriptor, with the same start-once / forward-every-message / stop-once contract and the same start and stop counters the tests already assert.

The persisted last-scope and last-tab IDs are descriptor IDs, so a global tab that disappears (plugin disabled) falls back to Sessions rather than to an index that now names something else. That is the reason the enum was not an index in the first place, and the slice keeps the property.

## Unifying enablement

Every plugin gets `plugins.<id>.enabled`. Migration is one-directional and silent:

- `tasks_plugin` and `notes_plugin` flags become deprecated aliases: if the flag is set in config and `plugins.tasks.enabled` is absent, the flag value wins; a save writes the config key and leaves the flag untouched. The flags are removed from `allFeatures` one minor release after the settings page stops writing them.
- `conversations_plugin` follows the same path if the Conversations plugin survives its own removal plan; if not, nothing to do.
- `terminalResources.providers[]` entries load into the same list as `plugins.external[]` with `scope: global`, `placements: ["panes"]`. Saving writes `plugins.external`. The old key is read for one minor release after that and then dropped.

The settings page (`page_panels.go`) becomes one loop over descriptors: enable switch, class, status line, and the install route from `configui.Integration` where the descriptor has one. `panelRestartNote` stays until enablement is live; making it live is deliberately out of scope.

## Protocol plugin configuration

```json
{
  "plugins": {
    "external": [
      {
        "id": "recall",
        "command": ["recall", "sidecar-plugin"],
        "passEnv": ["RECALL_PROFILE"],
        "enabled": true,
        "scope": "global",
        "placements": ["tab", "panes"],
        "timeout": "10s",
        "claimHosts": []
      },
      {
        "id": "jira-work",
        "command": ["sidecar-jira", "sidecar-provider", "--profile", "work"],
        "passEnv": ["JIRA_API_TOKEN"],
        "enabled": true,
        "placements": ["panes"],
        "claimHosts": ["example.atlassian.net"]
      }
    ]
  }
}
```

Same fields and bounds as the resource section plus `scope` and `placements`. The discovery policy is unchanged and restated because it is the standing decision a plugin-directory proposal has to argue against: Sidecar never scans a directory, never executes every `sidecar-*` binary on `PATH`, never auto-enables, and never lets a repository declare a plugin. `sidecar plugin add` writes one entry after showing exactly what will run; that is the whole install flow.

## The shared browser

One package, `internal/pluginbrowser`, renders a protocol plugin. It is host-independent in the same way `resourceview` is (`internal/resourceview/doc.go`): it knows `pluginhost` types and nothing about panes, tmux, or which surface is showing it.

```text
┌ Recall ─────────────────────────────────────────────────────────────────┐
│ ▸ query: dex_                                    Results · 7 · degraded │
│ ─────────────────────────────────────────────────────────────────────── │
│  # Title                      Source    Excerpt                          │
│ ❯1 DEX schema notes           notes     …people are tiered, and the…    │
│  2 dex context --json         shell     …the star command…              │
│ ...                                                                      │
│ ⚠ 1 of 4 sources did not answer (mail: checkpoint stale)                │
├──────────────────────────────────────────────────────────────────────────┤
│  DEX schema notes                                     exact · notes      │
│  Source notes  Locator rc:notes/2026-08-14-dex-design  Updated 2w ago    │
│  ── Evidence ─────────────────────────────────────────────────────────── │
│  rendered markdown …                                                     │
└──────────────────────────────────────────────────────────────────────────┘
```

The real mockups are the M0 deliverable and live in [mockups/](mockups/); the sketch above only fixes the parts: a query or filter line (shown when the collection's `search` is not `none`), a view pill row (when `views` is non-empty), a table with a cursor that reflows to primary/secondary rows when narrow, a notice line, and a detail deck below or beside it depending on the box. In a `Tab` placement the browser owns the whole content box and shows list and detail together. In a pane the same package renders either a collection tab or a document tab, because a pane is usually too small for both.

Keys are host-owned and identical for every plugin: `j`/`k` move, `Enter` opens (a document tab in the same leaf, or the detail box in a tab placement), `/` edits the query, `v` cycles views, `s` opens the sort picker, `r` refreshes, `a` opens the action menu, `o` opens `sourceUrl` through the confirmed path, `n` opens the pane switcher as it does in every deck-eligible plugin. A plugin-suggested action `key` is granted only if none of these, none of `keymap.HostReservedKeys`, and none of the surface's bindings use it.

`PaneFocusProvider`, `ContentLinkProvider`, `WheelBoundaryConsumer`, `FooterStatusProvider`, and `DiagnosticProvider` are implemented once by the browser, so every protocol plugin gets the app focus ring, content links in rendered bodies, correct wheel behaviour, a footer status when `describe` fails, and diagnostics without doing anything.

## Panes: reuse the Resource leaf, add a tab shape

Adding a leaf kind touches 26 non-test files for the most recent kind (Note) and forces a persistence schema change, because every kind has its own `*TabJSON` array (`internal/state/state.go:169-251`). The Resource leaf already exists for exactly this purpose: "the single leaf kind every external terminal resource provider shares" (`panelayout.go:28-32`). So:

1. `PaneResourceTabJSON` gains optional `collection`, `query`, `view`, `sort`, and `cursorId`. A tab is a document tab when `matcher`+`locator` are set (today's shape, unchanged) or a collection tab when `collection` is set. Decode refuses a tab with both or neither.
2. `resource.Reference` grows the same alternative; `Valid()` accepts exactly one shape. It stays the only plugin-shaped value that reaches persisted state.
3. `contentpanes` keeps one `Resource` viewer; it dispatches on the reference shape. `contentpanes.Source` gains `ListCollection` beside `ResolveResource`, with the remote twin routing through the host verb family the same way `ResolveResource` does.
4. Both `content.go` files map the Resource kind to the same viewer as today; both `pane_host.go` files add nothing. The parity guard is that neither file changes, and a test asserts the Resource viewer answers both tab shapes on both surfaces.
5. The livepanes binding is one `Binding` literal per surface (`internal/plugins/workspace/live_panes.go:53-94`, `internal/overview/live_preview.go:78-113`) with `Kind: "resources"`, `Targets` reading the visible tabs' plugins' declared `watch` paths from the cached describe snapshot (never resolving anything on the update goroutine), `Prepare` expanding and validating those paths once per describe generation, and `Refresh` re-listing visible collections and re-fetching visible documents. `everySeconds` polling is a ticker inside the same binding, active only while a tab from that plugin is visible.
6. Enter on a collection row opens a document tab in the same leaf, re-keyed to the resource's `identity` as resolve already does; a second Enter on the same row focuses that tab.

Floors, dividers, chrome, drag, and the compositor are untouched: `paneframe` does not know a new tab shape exists.

## CLI

`sidecar plugin` is the owned surface. Hosting plugins is a capability Sidecar owns, so every operation has a non-interactive path from the first milestone.

| Verb | Does | Talks to |
| --- | --- | --- |
| `sidecar plugin list [--describe] [--json]` | Every descriptor: class, scope, placements, enabled, status; `--describe` opts in to running `describe` on protocol plugins | config, optionally subprocess |
| `sidecar plugin check ID [--list COLLECTION [--query Q]] [--get COLLECTION ID] [--json]` | `describe` plus an explicit call, for authors | subprocess |
| `sidecar plugin call ID METHOD [--params JSON] [--json]` | One raw method call with the host's envelope and validation, printing what the host would have kept | subprocess |
| `sidecar plugin add ID --command ARGV... [--pass-env V]... [--scope] [--placement]...` | Appends a config entry after printing exactly what will run; `--yes` skips the confirm | config |
| `sidecar plugin remove|enable|disable ID` | Config edits through the saver, never dropping unknown sections | config |
| `sidecar plugin changed ID [--collection C]` | One `uirequest` on the file bus; a running Sidecar re-lists that plugin's visible tabs | uirequest bus |
| `sidecar open --plugin ID [--collection C] [--query Q] [LOCATOR_OR_ID] [--split|--at]` | Opens a document or collection tab on the viewer's screen; `--provider` stays as an alias for the document form | uirequest bus, same declines as today |
| `sidecar layout apply --spec` | `{"kind":"resource","provider":"recall","collection":"results","query":"dex"}` beside the existing `targets` form | uirequest bus |
| `sidecar terminal-links …` | Kept as an alias of the matching `plugin` verbs for one minor release, then removed | |

`recall query dex` from the request becomes `sidecar open --plugin recall --collection results --query dex`, and `ongoing show recall` becomes `sidecar open --plugin ongoing --collection projects recall`. Both are one line an agent can run from any pane with no keypress.

The `Agent` doc on each verb and `sidecar --agents` cover them; `docs/reference/cli.md` gains the family.

## Startup

Nothing changes in the startup posture: no subprocess, no `LookPath`, no config read of the plugin section before the first ready frame, enforced by the same explicit latch the resource providers use (`internal/app/resourceproviders.go:19-46`). A global protocol tab renders a loading state until its describe snapshot lands. `assembly.Plan` reads only config and constructs nothing.

## What does not change

- `paneframe`, `panelayout` kinds and floors, both `pane_host.go` files.
- The embedded plugins' own UIs, key tables, themes, and watchers.
- The trust posture: process boundary is crash isolation, not a sandbox; explicit config is the install step; no discovery.
- `contentlink`/`terminallink` scanning: matchers are the only thing a plugin contributes to it.
- Remote hosts: a protocol plugin runs on the viewer's machine and gets `project.hostId`; running plugins on the host side is a separate plan.
