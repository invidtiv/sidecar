# ADR-0001: Contextual plugin keys take precedence over global keys

Status: Accepted and implemented

Date: 2026-08-10

## Context

Sidecar handled several keys globally before any plugin could see them. That was
workable while every plugin was a small, purpose-built pane whose keys had been
chosen to avoid the globals. It stopped being workable when Sidecar began
embedding a whole foreign application as a tab.

The Tasks plugin embeds the Tasks TUI through its public `pkg/tui` host API.
Tasks brings its own complete keymap — 355 bindings across 14 focus contexts —
designed with no knowledge of Sidecar. Collisions are not incidental; they are
guaranteed. `1`-`6` are Tasks' views and Sidecar's tabs. `@` is Tasks' context
picker and Sidecar's project switcher. `q` quits both. `?` is help in both.

The cross-repo plan of record is
`docs/plans/active/tasks-in-sidecar.md` in the tasks repo, § 1.4. This ADR
records what shipped, which differs from that plan in one respect — see
"Only conflict-table keys may shadow" below.

## Decision

### The precedence ladder

Key input resolves in this order. The first level that handles the key wins.

1. an open Sidecar application modal;
2. the active plugin's text-input or blocking-overlay context;
3. an active plugin contextual binding;
4. Sidecar global bindings;
5. unbound input forwarded to the plugin.

Level 3 above level 4 is the substance of this ADR: **a plugin's contextual
binding beats a Sidecar global.** A plugin that is showing a list of tasks knows
better than the shell does what `j` means at that moment.

### Opt-in interfaces, so existing plugins are untouched

Levels 2 and 3 are driven by two optional interfaces in `internal/plugin`:

```go
type KeyRouter interface {
    BlocksGlobalKeys() bool
    ClaimsKey(key string) bool
    QuitKeyExits() bool
}

type FooterStatusProvider interface {
    FooterStatus() (string, bool)
}
```

Only the Tasks plugin implements them. For every other plugin
`pluginBlocksGlobalKeys()` and `pluginClaimsKey()` are constant false, and quit
falls back to the original `isRootContext(activeContext)`, so git-status,
file-browser, conversations, workspace, notes, and td-monitor behave exactly as
they did. No global `case` was reordered or removed. This was the alternative to
rewriting the global switch, which would have put all seven plugins at risk to
serve one.

`TestPluginsWithoutAKeyRouterAreUnaffected` pins this property.

### The host enforces its own reserved keys

`ctrl+c`, `q`, and `?` are never offered to `ClaimsKey`, whatever a router says.
`internal/keymap.HostReservedKeys` is the single definition, shared by the host
check and the plugin's own filter.

This started as a plugin-side courtesy: the Tasks plugin declined those keys, and
the host filtered only `ctrl+c`. Review demonstrated a router claiming `q` or `?`
swallowing Sidecar's quit flow and merged help entirely. "Sidecar quit flow wins;
the embedded app never exits Sidecar" is a non-negotiable, so it belongs in the
host, not in the goodwill of each plugin. The plugin-side filter stays as defence
in depth.

### Only conflict-table keys may shadow a global

A plugin binding may shadow a Sidecar global **only if that key appears in the
plan's § 1.4 conflict table** — for Tasks, `@` and `1`-`6`. Any other collision
goes to Sidecar, and the plugin's command stays reachable through `?` and the
command palette. Keys that collide with nothing are unaffected and reach the
plugin by level-5 forwarding.

A claim on a global is also **unconditional per context**: availability is not
consulted. Availability-awareness is kept for keys Sidecar does not bind
globally, where it is genuinely useful.

This rule is a departure from the original plan, adopted after review. Claims
were availability-aware for every key, which made routing depend on the
selection: in the same `tasks-list` context, `K`/`W`/`#` reached Sidecar with
nothing selected, and raise-priority / set-work-ref / **delete-selected** with a
task selected. A user reaching for the theme switcher could delete a task. Those
three keys were never in the conflict table — they collided by accident, and
nobody had decided they should be given away. Predictable-per-context routing is
worth more than squeezing every plugin binding through.

### User overrides outrank plugin claims

`keymap.Registry.UserOverride(key)` is consulted before a plugin claim, so
`keymap.overrides` can take a key back from a plugin. The plan names this as the
remedy for a mapping that turns out wrong in live use — "change the mapping
through Sidecar's keymap override rather than forking the plugin's registry" —
and level 3 originally ran ahead of the override lookup, so the remedy did not
exist. An override naming an unregistered command is not treated as a claim; the
plugin still gets the key rather than the key vanishing.

### Root contexts are an allow-list that fails safe

Whether `q` exits is decided per plugin context. For Tasks the root contexts are
an explicit allow-list intersected with what Tasks still exports; anything
unknown or ambiguous is treated as a blocking overlay.

Deriving overlay-ness by inference was tried and rejected: a context was called
"root" when it took no text input and bound no `q`. That uses "binds `q`" as a
proxy for "is an overlay", and the two are not the same property. A future
non-text overlay that happens not to bind `q` — a read-only preview, a diff
viewer, a `y`/`n` confirmation — would have been classified root, letting Sidecar
globals fire underneath a visible overlay and popping quit-confirm on top of it.
The allow-list fails in the harmless direction instead: a missed context
over-blocks rather than under-blocks.

### Toasts outrank a plugin's footer status

`FooterStatus` reports a condition that is true until someone fixes it, so it
cannot own the footer slot outright. A toast is the only surface for something
that just happened and will not repeat, and it expires on its own. Toasts
therefore borrow the slot; the plugin status returns when the toast does.
Otherwise a plugin reporting an unreadable store would silently swallow every
Sidecar toast for that tab, including update-and-restart notices.

## Consequences

- Plugins that want contextual keys implement `KeyRouter`. Plugins that do not
  are unaffected, and nothing forces them to adopt it.
- A plugin cannot capture `ctrl+c`, `q`, or `?`. A plugin that needs its own
  quit-like affordance surfaces it as a command and binds a different key.
- `K`, `W`, and `#` belong to Sidecar inside the Tasks tab. Their Tasks
  equivalents are reachable through `?` and the palette.
- The Tasks routing table is derived at runtime from the Tasks registry rather
  than hardcoded in `internal/app`, so `internal/app` does not import a plugin
  and the table cannot drift as Tasks adds contexts. A Tasks-side context rename
  fails a test rather than silently changing routing.
- Adding a second `KeyRouter` plugin means deciding its conflict table first.
  Two plugins claiming the same global in different contexts is fine — claims are
  only consulted for the active plugin — but two plugins disagreeing about what a
  key means in the same context is a design question, not a routing one.
- `internal/app/key_precedence_test.go` is the table-driven proof of all five
  levels and every conflict-table row.
