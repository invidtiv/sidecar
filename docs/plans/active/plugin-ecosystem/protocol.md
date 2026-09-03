# Sidecar plugin protocol v1 (draft)

**Status:** draft contract, opened 2026-09-02. Nothing here is frozen. It freezes the way `sidecar.terminal-resource/v1` did: after the host implements it, one real external plugin (recall) implements it against a live tool, and both revise what they found. Until then the identifier is `sidecar.plugin/v1-draft` and a host may refuse it at any point. **Controlling document:** [README.md](README.md).

**Related:** [Terminal resource provider protocol](../../../reference/terminal-resource-provider-protocol.md) is the frozen v1 this grows from. Everything in that document that this one does not restate still holds: invocation model, process groups, the execution environment, stderr handling, transport failure rules, sanitization, and the safety posture.

## What this protocol is

A Sidecar plugin is an explicitly configured local executable that gives Sidecar content to render and typed actions to offer. Sidecar owns rendering, keys, focus, tabs, persistence, theme, and safety. The plugin owns its data, its rules, its credentials, and its network access.

The plugin never sends a user interface. It sends content in a small declarative vocabulary Sidecar knows how to draw well: collections of rows, documents with fields and sections, search results with excerpts, and actions with a few typed inputs. That vocabulary is deliberately domain-shaped rather than a generic widget tree, for the reasons recorded under [Why not a generic UI catalog](#why-not-a-generic-ui-catalog).

Every response is data. Provider text never becomes ANSI, never binds a key, never chooses a colour, and never opens a URL except through the separately validated `sourceUrl`.

## Invocation model

Unchanged from the resource protocol, restated for one reason: a plugin author reading this document alone must not be surprised.

- Sidecar runs the configured argv directly. No shell. Working directory is a neutral Sidecar config directory, never the selected repository.
- One JSON request object on stdin, then EOF. Exactly one JSON object on stdout. stderr is drained and discarded.
- Every invocation is its own process group, killed as a group on timeout or cancel.
- A typed success or typed failure exits `0`. Non-zero exit, malformed or multiple stdout values, oversize stdout, timeout, or a mismatched `protocol` field is a transport failure attributed to the plugin.
- The environment is the documented allowlist plus `passEnv`. `SIDECAR_PLUGIN=1` is added so a tool's ordinary CLI can tell it is being driven by the host.

Plugins are one-shot in v1. There is no resident process, no framing, no multiplexing. Live behaviour (search-as-you-type, background refresh, mutations) is built from one-shot calls plus the host-side mechanisms in [Freshness](#freshness-how-live-behaviour-works-without-a-resident-process). A resident transport is a declared future capability that carries these same objects; see [Deferred](#deferred-to-evidence).

## Methods

| Method | Runs when | Network | Mutates |
| --- | --- | --- | --- |
| `describe` | after the first ready frame; on config change; on recheck | no | no |
| `resolve` | a matcher span is activated (unchanged from resource v1) | may | no |
| `list` | a collection is shown, refreshed, re-sorted, re-filtered, searched, or paged | may | no |
| `get` | a collection row is opened, or a document tab is refreshed | may | no |
| `act` | the user confirms an action | may | yes |

`describe` is the only method whose absence is an error. A plugin that declares no collections and no actions is exactly a resource provider, and a resource provider that answers `sidecar.terminal-resource/v1` keeps working unchanged: the host sends the old protocol identifier to entries that came from the old config section and translates the response into the same host types.

### Request envelope

| Field | Present on | Meaning |
| --- | --- | --- |
| `protocol` | all | `sidecar.plugin/v1` (draft: `sidecar.plugin/v1-draft`). Unsupported values get `invalid_request` naming what is supported. |
| `method` | all | One of the five. Unknown methods return `invalid_request`, not a crash. |
| `instance` | all | The configured instance ID. Informational; argv selection wins. |
| `deadlineMs` | all | Advisory but accurate. Budget inside it and return a typed `unavailable` rather than be killed. |
| `host` | `describe` only | `{name, version}`. |
| `context` | `list`, `get`, `act`, `resolve` | Present only for context kinds the plugin declared in `describe` **and** the host chose to send. See [Context](#context). |
| `params` | all but `describe` | Method-specific. |

### Response shape

Exactly one of: a describe result, a `resource` (from `resolve` and `get`), a `page` (from `list`), an `outcome` (from `act`), or an `error`. Every response carries `protocol`. Unknown JSON fields are ignored for forward compatibility.

## `describe`

Reports identity, what context the plugin reads, what it can recognise in terminal output, what collections it offers, and what actions it exposes. Local, fast, no network, no credential prompt. A plugin that is installed but unconfigured returns a typed `invalid_config` with a single-line `setupHint`.

```json
{
  "protocol": "sidecar.plugin/v1-draft",
  "plugin": {"kind": "recall", "name": "Recall", "version": "0.4.0", "docsUrl": "https://example.test/recall/sidecar"},
  "context": ["project"],
  "matchers": [
    {"id": "locator", "pattern": "\\brc:[a-z0-9]+:[A-Za-z0-9/_.-]+\\b", "priority": 50}
  ],
  "collections": [
    {
      "id": "results",
      "title": "Results",
      "search": "required",
      "columns": [
        {"id": "rank", "label": "#", "width": 3, "align": "right"},
        {"id": "title", "label": "Title", "primary": true},
        {"id": "source", "label": "Source", "width": 14},
        {"id": "excerpt", "label": "Excerpt", "secondary": true}
      ],
      "views": [],
      "sort": [],
      "detail": true,
      "refresh": {}
    },
    {
      "id": "sources",
      "title": "Sources",
      "search": "none",
      "columns": [
        {"id": "name", "label": "Source", "primary": true},
        {"id": "health", "label": "Health", "kind": "status"},
        {"id": "fresh", "label": "Fresh", "kind": "timestamp"}
      ],
      "refresh": {"everySeconds": 120}
    }
  ],
  "actions": [
    {"id": "refresh-source", "title": "Refresh source", "on": "item", "collection": "sources", "mutates": true, "confirm": true}
  ]
}
```

### `plugin`

As `provider` in resource v1: informational display strings, bounded, never able to rename or collide with the configured instance ID. `docsUrl` is the only executable-declared setup action Sidecar follows, after confirmation.

### `context`

The kinds of host context the plugin reads. Declared here so the settings page and `sidecar plugin list` can show them before anything runs. Configuring the plugin is the trust act; there is no second per-field grant. Kinds in v1:

| Kind | What the host sends | Why a plugin would want it |
| --- | --- | --- |
| `project` | `{root, workDir, name, branch}` for the surface the request came from; absent on a global surface with no project | `ongoing show <this project>`, `recall --scope project=` |
| `selection` | `{text}` when the user activated an action over selected text | search the selection, log it as a note |

Nothing else in v1. Terminal lines, scrollback, tmux targets, file contents, and environment are not context kinds, and adding one is a protocol revision, not a field.

An undeclared kind is never sent. A declared kind is sent whenever the host has it. On a remote-bound surface `project` carries the host-side path and the host ID, so a plugin that runs locally can tell it is being asked about another machine.

### `matchers`

Unchanged from resource v1: Go/RE2, whole match is the locator, no plugin code in the scanner, bounded, validated all-or-nothing.

### `collections`

A collection is a named, listable set of rows the host can show as a table with a cursor.

| Field | Meaning |
| --- | --- |
| `id`, `title` | Stable ID (persisted in pane state) and display title. |
| `search` | `none`, `optional`, or `required`. `required` means the collection is empty until there is a query (recall). `optional` filters. |
| `columns[]` | Ordered. `{id, label, width?, align?, kind?, primary?, secondary?}`. Exactly one `primary` column names the row; an optional `secondary` column is rendered under it when the pane is too narrow for a table. `kind` is `text` (default), `status`, `timestamp`, `user`, `number`, `badge`. Width is a hint in cells; the host reflows. |
| `views[]` | Named preset filters: `{id, title}`. The host shows them as a pill row; the selected view ID goes back in `list`. ongoing's attention/rising/dormant, DEX's tiers. |
| `sort[]` | Sortable keys: `{id, label, default?: "asc"|"desc"}`. The host offers them in a sort picker; the chosen key and direction go back in `list`. |
| `detail` | Whether `get` is meaningful for rows. `false` means Enter does nothing but a `sourceUrl` on the row can still open. |
| `refresh` | `{everySeconds?, watch?[]}`. See [Freshness](#freshness-how-live-behaviour-works-without-a-resident-process). |
| `context` | Optional narrowing: `["project"]` means this collection is meaningful only when project context exists, so a global surface hides it. |

### `actions`

A typed operation the user can invoke. The plugin declares it; the host decides how it is reached.

| Field | Meaning |
| --- | --- |
| `id`, `title` | Stable ID and display title. |
| `on` | `item` (a collection row), `collection` (the whole list, e.g. "capture"), `resource` (a matcher-resolved document, e.g. "transition ticket"), or `global` (no subject). |
| `collection` | Required for `item` and `collection`; the collection this applies to. Absent for `resource` and `global`. |
| `inputs[]` | Up to 8 typed inputs the host collects before calling `act`: `{id, label, kind, required?, choices?[], default?}` with `kind` in `text`, `multiline`, `choice`, `confirm`. `choice` needs `choices`. |
| `mutates` | Whether it changes the plugin's data. Mutating actions with no inputs get a confirm step unless `confirm: false` is stated explicitly. |
| `key` | Optional single lowercase letter the plugin would like. Honoured only if the host's reserved set and the surface's own bindings leave it free; otherwise the action is reachable through the action menu and palette only. Never guaranteed, never persisted. |

Actions never carry code, keys the host did not grant, or colours.

## `list`

```json
{
  "protocol": "sidecar.plugin/v1-draft",
  "method": "list",
  "instance": "recall",
  "deadlineMs": 10000,
  "context": {"project": {"root": "/path/to/checkout", "workDir": "/path/to/checkout", "name": "sidecar", "branch": "main"}},
  "params": {
    "collection": "results",
    "query": "dex",
    "view": "",
    "sort": {"key": "", "dir": "asc"},
    "cursor": "",
    "limit": 100
  }
}
```

```json
{
  "protocol": "sidecar.plugin/v1-draft",
  "page": {
    "outcome": "answered",
    "items": [
      {
        "id": "rc:notes:2026-08-14-dex-design",
        "cells": {"rank": "1", "title": "DEX schema notes", "source": "notes", "excerpt": "…people are tiered, and the tier drives what brief shows…"},
        "status": {"label": "exact", "tone": "success"},
        "sourceUrl": ""
      }
    ],
    "nextCursor": "",
    "total": 7,
    "notices": [
      {"tone": "warning", "text": "1 of 4 sources did not answer (mail: checkpoint stale)"}
    ]
  }
}
```

| Field | Meaning |
| --- | --- |
| `outcome` | `answered`, `abstained` (nothing matched, sources fine), `degraded` (some eligible source could not answer). These are recall's exit states lifted into data; a plugin whose CLI would have exited non-zero for one of these must still exit `0` here and say it in `outcome`. The host renders each honestly: an empty list under `abstained` is "no matches", under `degraded` it is "no matches, and coverage was incomplete". |
| `items[]` | Bounded rows. `id` is what `get` and item actions receive. `cells` is keyed by column ID; missing cells render blank. `status` is an optional pill. `sourceUrl` is an optional validated http(s) URL. |
| `nextCursor` | Opaque; empty means no more. The host pages on demand, never eagerly. |
| `total` | Optional count for the footer. |
| `notices[]` | Up to 4 single-line `{tone, text}` rows the host shows above or below the list. Where recall's coverage notes and ongoing's scan-health line go. |

A query on a `search: required` collection with an empty string is answered by the host without calling the plugin: an empty list with a prompt.

## `get`

```json
{"method": "get", "params": {"collection": "results", "id": "rc:notes:2026-08-14-dex-design"}}
```

Returns a `resource`, the same object `resolve` returns, extended with `sections`:

```json
{
  "protocol": "sidecar.plugin/v1-draft",
  "resource": {
    "identity": "rc:notes:2026-08-14-dex-design",
    "title": "DEX schema notes",
    "subtitle": "notes · 2026-08-14",
    "status": {"label": "exact", "tone": "success"},
    "fields": [{"label": "Source", "value": "notes"}, {"label": "Locator", "value": "rc:notes:2026-08-14-dex-design"}],
    "body": {"format": "markdown", "text": "…"},
    "sections": [
      {"title": "Evidence", "body": {"format": "markdown", "text": "…"}},
      {"title": "Timeline", "items": [
        {"when": "2026-08-14T10:02:00Z", "title": "Note added", "text": "…"},
        {"when": "2026-08-20T16:40:00Z", "title": "Linked from td-3fa2c1", "text": ""}
      ]}
    ],
    "sourceUrl": "",
    "updatedAt": "2026-08-20T16:40:00Z",
    "freshForSeconds": 60
  }
}
```

`sections[]` is bounded (8) and each is exactly one of `{body}`, `{fields[]}`, or `{items[]}` (a timeline: `when` is RFC 3339, rendered relatively). A resource with no `sections` renders exactly as a resource v1 card does today, which is how Jira keeps working.

## `act`

```json
{
  "method": "act",
  "context": {"project": {"…": "…"}},
  "params": {
    "action": "log-note",
    "collection": "people",
    "id": "p:ada",
    "inputs": {"text": "Met at the conference; follow up about the retrieval eval pack."}
  }
}
```

```json
{
  "protocol": "sidecar.plugin/v1-draft",
  "outcome": {
    "status": "done",
    "message": "Logged a note for Ada",
    "refresh": ["people"],
    "open": {"collection": "people", "id": "p:ada"}
  }
}
```

| Field | Meaning |
| --- | --- |
| `status` | `done` or `failed`. A `failed` outcome is a typed failure with a message, not a transport failure. |
| `message` | Single line, shown as a toast or status flash. |
| `refresh[]` | Collections the host should re-list if visible. Documents whose `identity` matches an affected item are re-fetched. |
| `open` | Optional: a row to open after the action (capture-then-show). |

For `on: "resource"` actions, `params` carries `{action, matcher, locator, inputs}` instead of `{collection, id}`. That is how "transition this Jira ticket" fits without a new method.

## Errors

Unchanged codes and semantics from resource v1: `not_found`, `unauthorized`, `forbidden`, `rate_limited`, `invalid_config`, `invalid_request`, `unavailable`, `internal`, with the per-response `retryable` authoritative and `setupHint` single-line and never executed.

## Freshness: how live behaviour works without a resident process

The user's stated bar is that one-shot invocation is acceptable only if live search, mutations, and background updates are still reachable. Each is built from one-shot calls plus a host mechanism Sidecar already has.

| Need | Mechanism | Host owner |
| --- | --- | --- |
| Search as you type | The host debounces (250 ms), sends `list` with the new `query`, and kills the previous in-flight process group for that pane. Results are applied only if their query matches the current input. | the manager (`internal/resourceprovider`) |
| Edit or transition a ticket | An `act` with `on: "resource"`; the response's `refresh` re-fetches the document. | `act` plus the existing resolve cache |
| A plugin's data changed in the background | `refresh.watch[]`: absolute paths (files or directories, `~` expanded, must be under the user's home) the host watches through `internal/livewatch` while a collection or document from that plugin is on screen. A change re-lists visible collections and re-fetches visible documents. | `internal/livepanes` binding for the Resource leaf |
| The plugin cannot name a path | `refresh.everySeconds` (clamped to [15, 900]), polled only while visible, like td monitor. | `livepanes` |
| The plugin wants to poke Sidecar itself | `sidecar plugin changed <instance> [--collection ID]` writes one `uirequest` on the file bus every running Sidecar already watches. A plugin's own daemon or a shell hook can call it with no protocol change. | `internal/uirequest` |
| A slow tool | The user sees the previous page with a "refreshing" badge; the host never blanks a list to wait. | `resourceview` |

Nothing here needs a socket. When a measured case (recall live search over a slow source, an event-driven service like ongoing's scanner) shows the one-shot cost is the problem rather than the tool's own latency, [resident mode](#deferred-to-evidence) carries these same messages over a long-lived stdio JSON-RPC connection with one added notification, `changed`.

## Context

The host sends `context` on `list`, `get`, `act`, and `resolve` only for kinds the plugin declared. The `project` object:

```json
{"root": "/path/to/main/checkout", "workDir": "/path/to/worktree", "name": "sidecar", "branch": "feature/x", "hostId": ""}
```

`hostId` is non-empty on a remote-bound surface and the paths are then paths on that host. A plugin that only knows this machine should say so with a typed `unavailable` naming the host, which is the same rule Sidecar's own plugins follow.

## Limits

Everything in resource v1 plus:

| Bound | Default |
| --- | --- |
| Collections per plugin | 16 |
| Columns per collection | 12 |
| Views / sort keys per collection | 32 / 16 |
| Actions per plugin / inputs per action | 32 / 8 |
| Items per page / `limit` clamp | 500 |
| Cell length | 512 chars |
| Notices per page / notice length | 4 / 200 chars |
| Sections per resource / timeline items per section | 8 / 200 |
| `refresh.watch` paths per plugin | 8 |
| `refresh.everySeconds` | clamped to [15, 900] |
| `list` / `get` / `act` timeout | 10 s (configurable, clamped to 60 s) |

Over-limit content is truncated and marked, as in resource v1. Only stdout size and structural violations refuse a response.

## Why not a generic UI catalog

A2UI and its Bubble Tea renderer (a2tea) were considered as the vocabulary. They share this protocol's posture: a pre-approved component catalog, no code across the trust boundary, an action loop with IDs and responses. Three things are borrowed: that posture, the action/response shape (`act` returns an outcome addressed to the surface that asked), and the principle that the host renders the catalog in its own theme with monochrome-safe fallbacks.

What is not borrowed is the catalog itself. A2UI's components (buttons, text fields, sliders, images, modals in a tree) describe a form an agent generated for one turn of a chat. Sidecar's plugins are browsers over a tool's data that live for a session: they need a cursor, sorting, views, paging, tabs, persistence across relaunch, content links, and live refresh, all owned by the host so they behave identically across every plugin and both workspace projections. A generic widget tree pushes those into each plugin and makes parity a per-plugin promise. Domain-shaped objects (collection, row, resource, section, action) keep them host-owned.

If a plugin ever needs a layout the vocabulary cannot express, the answer is one new typed object with host rendering, or the embedded class. The `body` field's `format` is the extension point if a block vocabulary is ever wanted: a `blocks` format could carry an A2UI-style adjacency list without changing any envelope.

## Deferred to evidence

- **Resident mode.** `describe` gains `"transport": ["oneshot", "resident"]`; the host keeps one process per instance speaking newline-delimited JSON-RPC with the same method and result objects, plus a `changed` notification from plugin to host. Added only when measured startup cost, not tool latency, is the problem.
- **Nested trees and boards.** A `children` cursor on rows, and grouped columns. Added when a real plugin needs them.
- **Plugin-declared content links inside bodies.** Bodies are sanitized to plain text links today; body-link activation arrives only through host-owned hit regions over validated destinations.
- **Selection context on remote surfaces**, streaming pages, and binary attachments.

## Fixtures

Canonical request and response JSON will live at `internal/pluginhost/testdata/protocol/` and may be vendored by plugin authors. The reference fixture executable simulates every hostile case the resource fixture does, plus: an `act` that never returns, a `list` whose `nextCursor` loops, a collection that declares 13 columns, a `watch` path outside the home directory, and a `describe` that answers `sidecar.terminal-resource/v1` only.
