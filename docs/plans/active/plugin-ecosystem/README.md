# Plugin ecosystem: protocol plugins, embedded plugins, one host

**Status:** M1 (descriptor and generalized global host, td-01b62b) is implemented on branch `plugin-ecosystem`. M0's mockups are in [mockups/](mockups/) and the protocol revisions they surfaced are still pending the maintainer's confirmation. M2 onward are proposed. Decisions settled with the maintainer on 2026-09-02. **Tracking:** td-f9f007.

**Related:** [Terminal resource providers](../../implemented/terminal-resource-providers.md) built the executable protocol, the `Resource` leaf, and the trust posture this plan extends; its protocol stays frozen and keeps working. [Hosting Herdr plugins in Sidecar](../../deprecated/herdr-plugin-support.md) is superseded by this plan. [Pane switcher everywhere](../pane-switcher-everywhere.md) and [Cross-project td issue links](../cross-project-issue-links.md) are the two nearest live plans and neither conflicts.

**Reading order:** this file, then [protocol.md](protocol.md) (the contract an external plugin author implements), then [host.md](host.md) (what changes inside Sidecar). [mockups/](mockups/) holds the M0 screen mockups once they exist.

## Decision first

Sidecar hosts two classes of plugin through one descriptor:

- **Protocol plugins** are explicitly configured external executables in any language. They answer five one-shot JSON methods (`describe`, `resolve`, `list`, `get`, `act`) and Sidecar renders everything: a navbar tab with a list-and-detail browser, collection and document tabs in the pane decks of both workspace projections, host-owned keys, live refresh, persistence, and content links. Recall, DEX, and ongoing are written against this contract before they have any UI of their own, and every existing terminal resource provider (Jira) is already a protocol plugin that happens to declare only matchers. A plugin the Sidecar release knows nothing about is a config entry.
- **Embedded plugins** are Go modules compiled into Sidecar with their own Bubble Tea UI. Tasks and the td monitor stay in this class with their UIs untouched. What changes is that they are described, enabled, placed, and hosted through the same descriptor as protocol plugins, so "global plugin" stops being a hardcoded special case for Tasks and a second global plugin is a descriptor, not an enum value.

A protocol plugin will never render as richly as Tasks, and this plan does not pretend otherwise: a vocabulary that could express the quadrant board and the agent queue would be a worse Bubble Tea. The vocabulary instead covers what a browser over a tool's data needs, and grows one typed object at a time when a real plugin proves the need.

## Why now

- Recall, DEX, and ongoing each have a complete CLI with `--json` and no TUI. Designing them against a Sidecar contract before writing a UI means their Sidecar presence costs one subcommand, and the protocol gets three real implementers instead of one.
- The resource protocol proved the shape (executable, JSON, host renders, frozen after a live implementation) but stopped at click-to-resolve. Lists, search, actions, and refresh are the same shape with more nouns.
- Tasks as a special-cased global host works but cannot be repeated. The second global plugin needs the generalization regardless of class.

## Settled decisions

1. **Two classes, one descriptor.** `plugin.Descriptor` is the only thing the assembly, settings page, global host list, palette, and `sidecar plugin list` read. Class decides who renders; scope decides lifecycle; placements decide where content shows. Its fields are listed in [host.md](host.md#the-descriptor).
2. **The protocol is `sidecar.terminal-resource/v1` grown, not replaced.** Same invocation model, environment allowlist, process-group rules, sanitization, limits, error codes, and matcher rules. Three new methods and a `sections` field. Old providers keep answering the old identifier and keep working unchanged.
3. **Domain-shaped vocabulary, not a generic widget tree.** Collections with columns, views, and sort keys; resources with fields, body, and sections; search as a `list` parameter; typed actions with up to eight inputs. A2UI's posture and action loop are borrowed; its component catalog is not, for the reasons in [protocol.md](protocol.md#why-not-a-generic-ui-catalog).
4. **One-shot invocation in v1.** Live search is debounce plus cancel; mutations are `act`; background updates are plugin-declared `watch` paths and poll intervals through `livepanes`, plus `sidecar plugin changed` on the file bus for a plugin that wants to poke Sidecar itself. Resident mode carries the same objects later and only with measured evidence that process startup, not tool latency, is the cost.
5. **Project context is declared, not granted twice.** A plugin lists the context kinds it reads in `describe`; the settings page and `sidecar plugin list` show them; configuring the plugin is the trust act. Only `project` and `selection` exist in v1.
6. **Panes reuse the Resource leaf.** A collection tab is a second tab shape in the existing leaf, not a new leaf kind, so `panelayout`, `paneframe`, both `pane_host.go` files, and the layout floors do not change and persistence gains fields rather than an array.
7. **Tasks keeps its embedded UI and joins the ecosystem through the descriptor.** A later, evidence-gated milestone ships a protocol-based Tasks provider beside it and measures the gap; retiring the embedded UI is a decision for that evidence, not this plan.
8. **Enablement is `plugins.<id>.enabled` for every plugin.** The `tasks_plugin` and `notes_plugin` flags and the `terminalResources.providers` section become read-only aliases for one minor release and are then removed.
9. **No discovery, no manifest, no sandbox.** Sidecar never scans directories or `PATH`, never auto-enables, never lets a repository declare a plugin, and says plainly that a process boundary is crash isolation. `sidecar plugin add` shows what will run and writes one config entry. A Herdr-manifest adapter could be written against these seams later; it is not part of this plan.
10. **Recall is the reference plugin.** It exercises search, ranked results with excerpts, a `get` that expands a locator, degraded and abstained outcomes, and global scope with optional project context. The protocol freezes after recall and the host have each been revised from what the other found, as `sidecar-jira` did for resource v1.
11. **Every owned capability has a non-interactive path from the first milestone.** Hosting plugins is something Sidecar owns, so the standing "presentation layer, no CLI parity" exception does not apply to `sidecar plugin`.
12. **Plugins are theme-aware without doing anything.** A protocol plugin sends tones, kinds, and text; the host maps them to the active theme, so a theme change re-renders every plugin the same way it re-renders a td issue card. An embedded plugin keeps the existing contract: theme injected at construction and again on `ThemeChangedMsg` without resetting its state. Mockups of either class follow Sidecar's design language reference so what is reviewed on the canvas is what ships.

## Scope boundary

**In scope:** the descriptor and generalized global host; unified enablement; protocol v1 host implementation with the shared browser; collection tabs in the Resource leaf on both surfaces with live refresh; `sidecar plugin` and `sidecar open --plugin`; a fixture plugin and conformance suite; the recall reference plugin's Sidecar subcommand (in recall's own repository); documentation on the site; migration of the resource config section.

**Out of scope:** resident transport; nested trees and boards; running protocol plugins on a remote host's side; porting Tasks or td to the protocol; making enablement live without restart; a Herdr-compatible manifest loader; DEX and ongoing subcommands beyond what M4 needs to confirm the contract generalizes (they are written in their own repositories against the frozen protocol).

## User contract

| Gesture | Required result |
| --- | --- |
| `sidecar plugin add recall --command recall sidecar-plugin` then restart | Recall appears as a global tab after Sessions and Activity, keyed `0` if it is the first plugin-provided global tab. A loading state shows until `describe` lands; a setup card shows if it fails. |
| Open the Recall tab, type `dex` | After a 250 ms pause the results collection lists ranked rows with excerpts. Typing again cancels the in-flight process group. A `degraded` outcome shows its notice line; an `abstained` outcome shows "no matches". |
| `Enter` on a row | The detail box shows the resource with its sections; a second `Enter` focuses it. `o` opens `sourceUrl` through the confirmed path when present. |
| `sidecar open --plugin recall --collection results --query dex --split right` from a terminal pane | A Resource leaf opens beside the terminal with one collection tab, on the viewer's screen, with the same declines `open` gives today. Relaunch restores the tab with its query. |
| `sidecar open --plugin ongoing --collection projects recall` | A document tab for that project; the plugin received `project` context if it declared it. |
| `a` on a row with item actions | An action menu; choosing one with inputs shows a small form; confirm calls `act`; the outcome message flashes and the named collections re-list. |
| A file under a declared `watch` path changes while a collection from that plugin is visible | The list refreshes within the livepanes latency window without the user pressing anything. Nothing refreshes when no tab from that plugin is on screen. |
| `sidecar plugin changed dex --collection people` from a shell hook | Same refresh, through the file bus. |
| Bound to a remote host, open a collection with `context: ["project"]` | The plugin receives `project.hostId` and either answers or refuses naming the host. Sidecar never substitutes a local path. |
| Disable Tasks in settings | `plugins.tasks.enabled` is written; the `tasks_plugin` flag is left alone; after restart the global tab is gone and `0` names the next global plugin or nothing. Implemented in M1. |
| A plugin answers `sidecar.terminal-resource/v1` only | It keeps working exactly as today: matchers, resolve, the Resource card, `--provider`. |

## Delivery

Each milestone ends net-better than the tree before it, lands on main, and is gated by the `plugin_protocol` feature flag until M4 flips it on.

### M0. Mockups and contract review

- Mock up the browser in both placements using the TUI mockup tool the maintainer uses for screen design: a global tab (recall results plus detail), a narrow pane with a collection tab (ongoing projects with views and sort), a document tab with sections (a DEX person with fields and timeline), the action form, the degraded and setup states. Files land in [mockups/](mockups/) as `.tui.yaml` with rendered text snapshots beside them.
- Walk [protocol.md](protocol.md) as recall's author: write recall's `describe` and one `list` and `get` response by hand against the real CLI's `--json` output. Every field recall cannot fill or needs and cannot express is a protocol revision before any host code.
- Do the same on paper for DEX (`context`, `timeline`, `log` as an `act` with `multiline`) and ongoing (`list --view --sort`, `show`, `favorite`/`set` as actions with `choice` inputs) to confirm the vocabulary generalizes. Record what each needed in the protocol's changelog.
- **Evidence:** three mockup files reviewed on the canvas; a protocol revision commit that cites what recall, DEX, and ongoing each forced.

### M1. Descriptor and generalized global host (embedded class) — implemented, td-01b62b

- `plugin.Descriptor` in `internal/plugin`; one per plugin in `internal/plugins`; `assembly.Descriptors()` is the ordered catalog and `assembly.Plan` filters it. Tab order and IDs are unchanged and the existing ordering tests prove it.
- The `GlobalTab` enum and `globalTasksHost` are gone: the global tab row is a descriptor-driven ordered slice and each hosted plugin has a `globalPluginHost`. Sessions and Activity keep `8` and `9`; the first plugin-provided global tab keeps `0`; a second takes no number key and is reached by `[`/`]`, the palette command `focus-<id>`, or a click. The start/stop counters and every scope test pass unchanged.
- Unified enablement: `plugins.notes.enabled` and `plugins.tasks.enabled` with the two flags as read-only aliases; the settings page is one loop over descriptors; `sidecar plugin list [--json]`.
- **Evidence:** `go test ./...` green; an isolated `tmux-drive.sh` run at 160x45 whose stripped header capture is byte-identical before and after, with `0`/`8`/`9` opening Tasks, Sessions, and Activity in both builds, and with `plugins.tasks.enabled: false` removing the Tasks tab while `tasks_plugin` is still true; `TestSecondGlobalPluginGetsNoNumberKeyAndIsReachableByCycling` proving the fourth global entry takes no number key.

### M2. Protocol host and the browser in a tab

- `internal/pluginhost` (a rename and extension of `resourceprovider`): the new envelope, `list`/`get`/`act`, the describe snapshot with collections and actions, cancellation of superseded `list` calls, the new limits. Old providers are dispatched with the old identifier by the same manager.
- `internal/pluginbrowser`: the shared list-and-detail browser with host-owned keys, view pills, sort picker, query line, notices, action menu and input form, and the capability interfaces implemented once.
- Protocol descriptors projected from `plugins.external[]`; a global tab for each enabled one with a `tab` placement.
- The fixture plugin, extended from the resource fixture, with the hostile cases in [protocol.md](protocol.md#fixtures); conformance tests over `testdata/protocol/`.
- `sidecar plugin check|call|add|remove|enable|disable`.
- **Evidence:** the fixture plugin driven end to end in an isolated run: query, open, action with inputs, degraded notice, setup card; every hostile fixture case produces a bounded card and never a frozen frame.

### M3. Panes, persistence, refresh, and `open`

- Collection tabs in the Resource leaf on both surfaces through `contentpanes`; `resource.Reference` and `PaneResourceTabJSON` grow the alternative shape; decode refuses ambiguous tabs.
- The `resources` livepanes binding on both surfaces: declared `watch` paths, poll intervals, and `sidecar plugin changed`.
- `sidecar open --plugin`, layout spec `collection`/`query`, `--provider` as alias; remote twin through `contentpanes.Source`.
- **Evidence:** parity tests that the Resource viewer answers both tab shapes on both surfaces and that `livepanes.Set.Kinds()` lists `resources` on both; relaunch restores a collection tab with its query; a watched-file change re-lists within the latency window in an isolated run; `sidecar open --plugin` from a terminal pane lands beside it.

### M4. Recall, freeze, migrate, document

- Recall's `sidecar-plugin` subcommand in its own repository against the draft; revise host and protocol from what it finds; freeze `sidecar.plugin/v1`.
- DEX and ongoing subcommands follow against the frozen contract, each in its own repository. Anything they cannot express is a v2 note, not a v1 change.
- `terminalResources.providers` migration; `terminal-links` aliases; flag flip; site docs (a "Plugins" page replacing "Terminal resources", with the protocol reference linked); `docs/reference/cli.md`.
- Move this plan set to `docs/plans/implemented/` and fix inbound links.
- **Evidence:** recall, DEX, and ongoing each listed by `sidecar plugin list --describe` on a machine with all three; the three mockups from M0 compared against real screenshots.

### M5. Evidence-driven only

Do not schedule these because the protocol exists:

- Resident transport, when measured process startup (not tool latency) is the cost on a real plugin.
- A protocol-based Tasks provider beside the embedded one, to measure the gap before any retirement decision.
- Nested trees and boards, when a plugin needs them.
- Host-side execution of protocol plugins for remote-bound surfaces.

## Risks

| Risk | Mitigation |
| --- | --- |
| The vocabulary is too small and plugins route around it with markdown bodies | M0 writes three real plugins' responses by hand before host code; each missing noun is a protocol change while changing is cheap |
| The vocabulary grows toward a widget tree | Every addition is a domain noun with host-owned behaviour on both surfaces, or it goes to the embedded class |
| Generalizing the global host regresses Tasks | M1 changes no behaviour and is proven by the existing start/stop and scope tests plus a before/after header capture |
| Live search spawns a process per keystroke | Debounce, cancel superseded calls, keep the previous page visible; measure on recall before considering resident mode |
| A second livepanes binding per surface drifts | One `Binding` literal per surface, `Kinds()` parity test, and the Resource viewer shared through `contentpanes` |
| Config migration loses a provider | Alias reads for one minor release; the saver never drops unknown sections; `sidecar plugin list` shows where each entry was read from |
| A plugin's `watch` path is outside the home directory or is a whole disk | Validated at describe time, bounded to 8, rejected with a typed reason shown in `plugin list --describe` |

## Protocol revisions pending from the M0 recall mockup

Writing recall's screens against its real `--help` surfaced facts the draft cannot carry. Each is a proposed revision to [protocol.md](protocol.md), applied after the maintainer confirms; none blocks M1.

| Gap | Proposed revision |
| --- | --- |
| recall's exit state `failed` (every source asked failed, so "no results" claims nothing) has no `outcome` value; a typed `unavailable` error loses the partial page | Add `failed` to `page.outcome`; the host renders it as an error card over an empty list, never as "no matches" |
| Excerpts carry a kind (matched span, record opening, unmarked) that the mockup fakes with `›`/`~` prefixes the host cannot explain | Add column `kind: "excerpt"` and an optional per-cell `{text, mark}` shape for that kind; the host draws the mark and owns the legend |
| Suppressed (below relevance floor) and dropped (budget) counts are pushed into free-text notices | Add optional `page.omitted: {suppressed, dropped}` so the footer can say "7 shown · 3 below floor · 2 over budget" as data |
| `--as-of` changes what every row means and must survive refresh, but `list.params` has only `query`, `view`, `sort`, `cursor` | Add `asOf` (RFC 3339) to `list.params` and to the persisted collection tab; the header shows it as a chip |
| Scope is a conjunction of up to six keys, not a single-select view, and an applied scope has nowhere to live across refresh | Add `filters[]` to a collection's describe (`{id, label, kind: choice|text, choices?}`) and `params.filters{}` to `list`, persisted with the tab; the host renders applied filters as a chip row under the query |
| `status.label` length is unbounded but the host must fit it in a reserved column | Add a 24-char bound under Limits |
| The narrow reflow rule names only the secondary column | State it fully: rank and primary on line one; status label, the remaining short columns, and the secondary text folded into line two |
| An empty detail box in a `Tab` placement | Host rule, for [host.md](host.md): show the plugin's next collection (recall's `sources`) rather than a blank card, so `abstained` is verifiable in place |

## Open questions

Not blocking M0 or M1; each has a default the plan proceeds under.

- Should a global protocol tab remember its last query and view across relaunch, the way project panes persist? Default: yes, in the same per-plugin state file the browser uses for column widths.
- Should `selection` context be offered in v1 at all, or deferred with resident mode? Default: declared in the protocol, implemented in M3 only for actions invoked from a text selection in a document pane.
- Whether `plugins.external` is the right key name versus a top-level `plugins.protocol` or `extensions`. Default: `plugins.external`, because the settings page already groups everything under plugins.
- Whether a `Project`-scoped protocol plugin (re-described on project switch) is worth supporting in v1, or every protocol plugin is global and reads `project` context per call. Default: global only in v1; `scope: "project"` is accepted by config validation and rejected with a clear message until there is a plugin that needs it.

## Changelog

- 2026-09-02: opened. Decisions 1–11 settled in conversation with the maintainer; Herdr plugin-hosting plan superseded.
- 2026-09-02: decision 12 (theme awareness) and the pending-revisions table added from the M0 recall mockup.
- 2026-09-02: M1 implemented on branch `plugin-ecosystem` (td-01b62b). One deviation from the design: `tabRef.global` is the surface ID rather than an index into the global slice, because the persisted value is an ID already and carrying one identity instead of two removes a whole class of staleness.
