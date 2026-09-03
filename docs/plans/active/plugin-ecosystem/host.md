# Host architecture: one descriptor, two classes, both surfaces

**Status:** M1 (td-01b62b) and M2a (td-a6d276) implemented on branch `plugin-ecosystem`; M2b onward are design. **Controlling document:** [README.md](README.md). **Contract:** [protocol.md](protocol.md).

This document is the Sidecar-side half: how a plugin is described, enabled, placed, rendered, refreshed, persisted, and reached from the CLI, and which existing seams each of those reuses.

## Baseline

What M1 built, and what the remaining milestones turn each of the rest into.

| Seam | Where | State |
| --- | --- | --- |
| Tab list is data: `assembly.Descriptors()` is the ordered catalog and `assembly.Plan(cfg)` filters it to the enabled project tabs | `internal/plugins/assembly/assembly.go` | Built. Protocol descriptors join the same catalog in M2 |
| The global tab row is a descriptor-driven ordered slice of surfaces, each hosted by a generic `globalPluginHost` | `internal/app/scope.go` | Built |
| Enablement is `plugins.<id>.enabled` for every plugin; `tasks_plugin` and `notes_plugin` are read-only aliases | `internal/config/config.go`, each plugin's `descriptor.go` | Built |
| `configui.Integration{ID, Name, Why, Descriptor}` is a projection of the descriptor, carrying only the install route | `internal/configui/integrations.go` | Built |
| External executables: `plugins.external[]` and `terminalResources.providers[]`, `pluginhost.Manager`, describe/resolve/list/get/act | `internal/config/pluginexternal.go`, `internal/config/terminalresources.go`, `internal/pluginhost` | Built in M2a. The leaf still needs collection tabs (M3) |
| The `Resource` leaf, one kind for every external plugin's content | `internal/panelayout/panelayout.go:28` | M3. Gains collection tabs |
| `Resource` leaf has no `livepanes` binding | `internal/livepanes/livepanes.go:11-13` names this as the motivating defect | M3. Gets one, driven by plugin-declared `refresh` |
| `sidecar open --provider ID LOCATOR`, layout spec `{"kind":"resource","provider":…}`, `terminal-links list/check` | `internal/cli/open.go`, `internal/uirequest/types.go:85`, `internal/cli/registry.go` | M3. `sidecar plugin …` family; `--provider` and `terminal-links` kept as aliases |

## The descriptor

One Go type describes every plugin Sidecar can host. It is data, lives in `internal/plugin`, and is the only thing the assembly, the settings page, the global host list, the pane switcher, the palette, and `sidecar plugin list` read.

```go
// Descriptor is what Sidecar knows about a plugin before it runs.
type Descriptor struct {
    ID    string // stable; the config key, the CLI name, and the persisted tab ID
    Name  string // header label
    Icon  string
    Class Class  // Embedded | Protocol
    Scope Scope  // Project | Global
    // Placements the plugin can occupy. Tab is a navbar surface; Panes means its
    // content can open as leaves in the workspace and Sessions decks.
    Placements []Placement
    // Settings-page copy: the one-line detail under the name, the sentence the
    // install route leads with, and whether the surface carries a beta badge.
    Detail string
    Why    string
    Beta   bool
    // Enabled reads plugins.<id>.enabled and any legacy switch this plugin
    // migrated from, so a config written before this plan keeps working.
    // Nil means the plugin has no switch: Workspaces is exactly that.
    Enabled func(*config.Config) bool
    // Preference is what the user chose, ignoring dependencies on other
    // surfaces. Nil means the preference and the effective answer are the same
    // question. Notes is why it exists: it needs the td panel, and a Notes row
    // reading OFF because td is off would claim a choice nobody made.
    Preference func(*config.Config) bool
    // SetEnabled writes plugins.<id>.enabled. It never writes a legacy flag.
    SetEnabled func(*config.PluginsConfig, bool)
    // Embedded only: constructs the in-process plugin.
    New func() Plugin
    // Install and version UX (Homebrew tap, PATH probe). Zero means ships in-repo.
    Integration version.Descriptor
}
```

M2 adds `Instance *config.PluginInstanceConfig` for the protocol class: the configured entry a protocol descriptor is projected from.

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

The global tab row is an ordered slice of `globalSurface` values built from descriptors at startup:

1. Sessions and Activity are app-owned surfaces, first in the order, and keep `8` and `9`.
2. Every enabled descriptor with `Scope: Global` and a `Tab` placement follows, in descriptor order. The first gets `0`; the rest are reachable through `[`/`]`, the palette command `focus-<id>`, and a click. There is no fourth number key: renumbering the three that exist would move Sessions under the user, which is the same reason the positional project keys stop at 7.
3. `tabRef.global` is the surface ID. `globalTabsVisible`, `ensureVisibleGlobalTab`, `headerEntries`, `globalMouse`, `activeGlobalSurface`, and `surfacePlugins` all read the slice by ID, so nothing can be shifted onto a different action by a tab disappearing.
4. `globalPluginHost` is one per global descriptor, with the start-once / forward-every-message / stop-once contract and the start and stop counters the tests assert.

Number keys belong to entries rather than to positions: `8`, `9`, and `0` never change meaning, and a key whose entry is not on the row does nothing at all rather than falling through to a project tab.

The persisted last-scope and last-tab values are descriptor IDs. A global tab that disappears — its plugin disabled — falls back to Sessions rather than to an index that now names something else. The two surface names state.json wrote before the header settled on the fleet vocabulary (`workspaces`, `agents`) still read back.

`internal/app` keeps its own list of global descriptors (`app.GlobalDescriptors()`) because the plugin packages import it, so it cannot import the assembly. Both lists call the same per-plugin `Descriptor()`, and an assembly test fails if they ever name different plugins.

## Unifying enablement

Every plugin has `plugins.<id>.enabled`. Migration is one-directional and silent:

- `tasks_plugin` and `notes_plugin` are deprecated aliases. Both config keys are pointers, so "absent" stays a third answer: while the key is absent the flag decides, and a save writes the key and leaves the flag untouched. The flags are removed from `allFeatures` one minor release after the settings page stopped writing them.
- `conversations_plugin` is deliberately not an alias. It is the preview opt-in, and the panel needs both it and `plugins.conversations.enabled`; turning the panel off clears only the plugin key so the opt-in is not silently revoked.
- `terminalResources.providers[]` entries load into the same list as `plugins.external[]` with `scope: global`, `placements: ["panes"]` (M4). Saving writes `plugins.external`. The old key is read for one minor release after that and then dropped.

The settings page (`page_panels.go`) is one loop over descriptors: the enable switch, the detail line, the beta badge, the missing-command note, and the install route from `configui.Integration` where the descriptor has one. Per-plugin settings that are not a switch — a refresh interval, a database path, an editor choice — are the one place the loop is not uniform. A descriptor with no `SetEnabled` has no row, because a control that cannot change anything is worse than no control.

The catalog reaches the page through `app.WithPluginDescriptors`, from the process that owns both: `internal/configui` cannot import the assembly, because the plugin packages import `internal/app`, which owns this surface. An assembly test renders the real page with the real catalog so configui's own fixture cannot drift.

`panelRestartNote` stays until enablement is live; making it live is deliberately out of scope.

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

`Config.PluginInstances()` is the one ordered list the host reads. `plugins.external` entries lead, because order is precedence, and each `terminalResources.providers` entry follows projected onto the same type with the defaults its protocol implies: `scope: "global"`, `placements: ["panes"]`, no navbar tab. Every instance carries the section it was read from, and that section — never anything the executable says — decides which protocol identifier it is dispatched with. An ID configured in both sections is one plugin: `plugins.external` wins and the legacy entry is dropped, so a half-finished migration cannot start two child processes under one identity.

Validation:

- Bounds are the resource section's: 16 instances, a 64-character ID, a timeout clamped to [1s, 60s], 16 claimed hosts each a bare hostname, and `passEnv` names only — an entry containing `=` is refused loudly, with the value redacted out of the message, because a credential pasted into the config file needs removing rather than silently ignoring.
- `scope` defaults to `global`. `project` is refused with a message saying to remove the key and read project context per call, rather than coerced: a project-scoped plugin would be re-described on every switch and would see a different world each time, so running it as global would answer a question nobody asked. Any other value is refused naming the one that works.
- `placements` defaults to `["tab", "panes"]` for an entry a user wrote deliberately, and is exactly `["panes"]` for a projected `terminalResources` entry.
- The saver writes `plugins.external` inside the existing `plugins` block, which is re-marshalled whole on every save, so an emptied list disappears with it and unmanaged top-level sections are preserved as before.

Everything the protocol host does is behind the `plugin_protocol` feature flag, default off. It gates only `plugins.external`: `terminal_resource_providers` still governs the frozen section on its own, so turning the draft protocol off cannot take a working Jira provider down with it.

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

| Verb | Does | Talks to | State |
| --- | --- | --- | --- |
| `sidecar plugin list [--describe] [--json]` | Every descriptor: class, scope, placements, enabled, and for an external one the config section and protocol identifier. `--describe` opts in to running `describe` | config, optionally subprocess | Built |
| `sidecar plugin check ID [--list COLLECTION [--query Q]] [--get COLLECTION ID] [--json]` | `describe` plus an explicit call, for authors | subprocess | Built |
| `sidecar plugin call ID METHOD [--params JSON] [--json]` | One raw method call with the host's envelope and validation, printing what the host would have kept | subprocess | Built |
| `sidecar plugin add ID --command ARGV... [--pass-env V]... [--scope] [--placement]... [--timeout] [--claim-host] [--disabled] [--yes]` | Appends a config entry after printing exactly what will run; `--yes` skips the confirm | config | Built |
| `sidecar plugin remove\|enable\|disable ID` | Config edits through the saver, never dropping unknown sections | config | Built |
| `sidecar plugin changed ID [--collection C]` | One `uirequest` on the file bus; a running Sidecar re-lists that plugin's visible tabs | uirequest bus | M3 |
| `sidecar open --plugin ID [--collection C] [--query Q] [LOCATOR_OR_ID] [--split\|--at]` | Opens a document or collection tab on the viewer's screen; `--provider` stays as an alias for the document form | uirequest bus, same declines as today | M3 |
| `sidecar layout apply --spec` | `{"kind":"resource","provider":"recall","collection":"results","query":"dex"}` beside the existing `targets` form | uirequest bus | M3 |
| `sidecar terminal-links …` | The surface for `terminalResources.providers`, unchanged | config, subprocess | Built (kept) |

`recall query dex` from the request becomes `sidecar open --plugin recall --collection results --query dex`, and `ongoing show recall` becomes `sidecar open --plugin ongoing --collection projects recall`. Both are one line an agent can run from any pane with no keypress.

Exit codes are uniform across the family: `0` success, `1` a call or a write failed, `2` usage, `3` no plugin with that id is configured, `4` refused — the `plugin_protocol` flag is off, or the entry belongs to a section the verb does not own.

Two rules the verbs hold rather than merely document:

- **`list` starts nothing** without `--describe`, and **`add` starts nothing at all.** `add` validates the entry, then prints the argv one element per line — a joined line hides where an argument containing a space begins — plus the working directory, the protocol, and the variables passed by name, and then asks. Without a readable stdin it refuses and says to pass `--yes`.
- **Only what the host kept is printed**, never the plugin's raw stdout: everything on screen has been through the same sanitization and bounds a pane would apply, which is what makes `call` an authoring loop rather than a pretty-printer.

`remove`, `enable`, and `disable` refuse a `terminalResources` entry rather than editing it. That section belongs to the frozen protocol and `terminal-links` is its surface; two commands owning one section is how they start disagreeing.

The `Agent` doc on each verb and `sidecar --agents` cover them; `docs/reference/cli.md` carries the generated family.

## The manager

One `pluginhost.Manager` answers both dialects, with one cache, one dedupe table, and one global and per-instance concurrency budget. A plugin's `list` is a child process exactly as a provider's `resolve` is, so one budget has to cover both or the budget covers nothing.

What the plugin protocol added to it:

- `Description` grew `Context`, `Collections`, and `Actions`. A resource provider leaves all three empty, which is what "a plugin that declares no collections and no actions is exactly a resource provider" means inside the host.
- `List`, `Get`, and `Act`. `List` refuses a collection the newest successful `describe` did not declare, before spawning anything: the declaration is what says which columns exist, and a page sanitized against no declaration would carry cells the host has nowhere to paint.
- `Get` shares the resolve cache and dedupe under a `get`-prefixed key, so a second Enter on the same row costs no process. `Act` shares neither — two identical acts are two intentions, and collapsing them would drop a change the user asked for twice on purpose.
- A `ListRequest` carries a `PaneKey`. A second list for the same pane cancels the first, which kills its process group; `CancelPane` does the same when a pane closes. That is what makes search-as-you-type affordable before there is any resident transport.
- Context is filtered at the process boundary, in `CommandProvider`, against what the plugin declared in its last successful describe — so "an undeclared kind is never sent" is a property of the host rather than a promise each caller keeps, and a plugin that has never described successfully receives nothing.

## Startup

The startup posture is unchanged: no subprocess, no `LookPath`, no config read of either plugin section before the first ready frame, enforced by the same explicit latch the resource providers use (`internal/app/resourceproviders.go`). The describe pass builds from `Config.PluginInstances()` filtered by each section's own feature flag, inside the command, after the latch opens. Building a `globalPluginHost` constructs the plugin value and does no I/O; its model is built by the command `start` returns, after the first frame. `assembly.Plan`, `assembly.Descriptors`, and `plugin.ProtocolDescriptors` read only config and construct nothing. A global protocol tab will render a loading state until its describe snapshot lands.

## Deviations from the design, recorded in M2a

1. **`internal/resourceprovider` was renamed, not wrapped.** The plan allowed either. Renaming is what makes "one manager, one cache, one process policy" literally true rather than a convention two packages agree to keep.
2. **The descriptor carries `*config.PluginInstance`, not `*config.PluginInstanceConfig`.** The wider type also knows which config section the entry came from, which is what lets `plugin list` report it — the mitigation the plan's risk table asks for.
3. **Describe validation is all-or-nothing for the whole result**, not just for matchers. A plugin that declares a 13-column collection, an action naming a collection it never declared, or a watch path outside the home directory is refused entirely. Publishing the rest would hide a bug while changing what the scanner recognises and what the host watches on disk.
4. **An unrecognised `page.outcome` coerces to `degraded`.** The protocol names three values; a fourth from a later version is a claim this host cannot read, and of the two ways to be wrong, "coverage may be incomplete" is the one that does not invent a guarantee on the plugin's behalf.
5. **The identity block is accepted under either spelling.** `plugin` wins, `provider` is honoured — an author who copied a resource provider's response is describing the same thing under the older name.
6. **The home directory itself is not a valid `watch` path**, only somewhere under it. Watching a whole home directory is watching a whole disk with extra steps.
7. **A cell keyed by an undeclared column is dropped.** It has no width, no header, and no place in the row; keeping it would be keeping a string nothing can paint.
8. **M2 was split** into M2a (this) and M2b (the browser and the tab), because the host half is independently useful and independently provable.

None of the [pending protocol revisions](README.md#protocol-revisions-pending-from-the-m0-recall-mockup) was implemented. The draft is implemented as written.

## What does not change

- `paneframe`, `panelayout` kinds and floors, both `pane_host.go` files.
- The embedded plugins' own UIs, key tables, themes, and watchers.
- The trust posture: process boundary is crash isolation, not a sandbox; explicit config is the install step; no discovery.
- `contentlink`/`terminallink` scanning: matchers are the only thing a plugin contributes to it.
- Remote hosts: a protocol plugin runs on the viewer's machine and gets `project.hostId`; running plugins on the host side is a separate plan.
