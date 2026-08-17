# External terminal resource providers

**Status:** proposed
**Written:** 2026-08-17, at `78ea23d`
**Tracking:** `td-26230f`
**Related:** [workspace windowing](workspace-windowing-system.md) · [configuration design](../implemented/sidecar-configuration-design.md) · [configuration seam map](sidecar-configuration-seam-map.md)

## Decision

Build a narrow, versioned **terminal resource provider** protocol, not a general
Sidecar plugin runtime.

- Sidecar detects configured resource-key shapes in terminal output, owns link safety,
  opens one generic Resource pane, and renders a typed resource document.
- External executables own service-specific matching rules, authentication,
  network calls, field mapping, and provider-specific configuration.
- The first real provider is a separate `sidecar-jira` package. It is not linked
  into Sidecar and Jira credentials never enter Sidecar configuration.
- Version 1 uses a short-lived subprocess with one JSON request on stdin and one
  JSON response on stdout. Matching never starts a process or performs I/O; a
  provider is invoked only when its locator is activated, refreshed, or explicitly
  checked.
- The same scanner, provider manager, Resource view, and pane behavior serve the
  project Workspace and global Workspaces/Sessions surface. A feature landing on
  only one is incomplete.

This is the conventional boundary for a Go desktop/TUI host. Go's native
`plugin` package requires tightly matched toolchains and shared dependencies and
its own documentation recommends IPC as the practical alternative. Established
plugin systems and editor integrations isolate extensions in processes and use
versioned RPC/JSON contracts. Sidecar does not yet need a resident extension
host, bidirectional RPC, or arbitrary contributed UI, so a one-request process
keeps the useful boundary without inheriting that machinery.

Primary references:

- [Go `plugin` warnings](https://go.dev/pkg/plugin/?m=old#hdr-Warnings)
- [HashiCorp's subprocess/RPC plugin architecture](https://github.com/hashicorp/go-plugin#architecture)
- [Language Server Protocol overview](https://microsoft.github.io/language-server-protocol/)
- [VS Code extension-host isolation and lazy activation](https://code.visualstudio.com/api/advanced-topics/extension-host#_stability-and-performance)
- [Jira Cloud REST API v3](https://developer.atlassian.com/cloud/jira/platform/rest/v3/intro/)

## The user journey

The steel thread is deliberately one ticket, one click, one useful pane:

1. Marcus installs and configures `sidecar-jira` for the work Jira instance,
   including the project keys `CASH`, `GRES`, and `AVATAXUI`.
2. Sidecar is configured with one provider instance and its executable argv.
   Sidecar asynchronously asks the executable to describe itself. Until that
   succeeds, ordinary terminal output remains ordinary text.
3. An agent prints `CASH-1245`, `GRES-4433`, or `AVATAXUI-4323`. Sidecar's local
   compiled matcher underlines the exact resource key. No terminal line, credentials,
   project files, or network request leave Sidecar during recognition.
4. Marcus clicks `CASH-1245`. Sidecar immediately opens or focuses a Resource
   pane tab showing a loading state, then invokes `sidecar-jira` with only the
   provider instance, matcher ID, locator, and explicitly allowed context.
5. The pane shows the ticket key and summary, status, type, assignee, priority,
   updated time, selected fields, and a rendered description. `o` opens the Jira
   page; `r` refreshes; `{` / `}` cycle tabs; `x` closes the active tab; `q` or
   `esc` follows the existing content-pane close/hide rule.
6. Clicking the same key focuses the existing tab. Clicking another Jira key
   appends a tab in the same Resource pane. A GitHub, Buildkite, Sentry, or other
   future provider uses that pane rather than adding another pane kind or UI
   implementation.
7. In project Workspace, identifiers, active tab, and scroll position survive a
   relaunch but ticket bodies do not. Global Workspaces keeps the same state only
   in memory, matching its document/issue/diff lifetime.
8. Missing credentials, an incompatible provider, a timeout, a 401, a 404, or a
   rate limit becomes a bounded error card with Retry, copyable setup guidance,
   and a validated documentation link where applicable. It never freezes
   terminal rendering or crashes Sidecar.
9. An agent can put the same resource in front of Marcus without a mouse or
   keypress: `sidecar open --provider jira-work CASH-1245 --json` sends the
   existing Sidecar instance a typed Resource target. The provider's own CLI is
   still the structured path for an agent that wants the Jira data itself.

The journey must behave the same whether the terminal is selected through the
project Workspace or the global Workspaces browser. It must also preserve the
current click journey: capture the exact terminal viewport before a new pane
resizes tmux, transfer focus out of interactive input, and keep the clicked
context coherent after the resize.

## What exists today

### Recognition is a closed switch

`internal/terminallink.Scan` strips ANSI and emits a closed `Kind` set: URL,
file, td issue, and git diff. It resolves overlap in a deliberate order. File and
diff candidates are existence-gated by host callbacks; td issue shape is built
in. `Decorate` accepts the same closed kinds and synthesizes OSC-8 only for safe
HTTP URLs.

Both terminal surfaces correctly refuse to underline a resource key they cannot
activate. Project Workspace adapts spans into another closed `terminalLinkKind`
in `internal/plugins/workspace/terminal_links.go`; global Workspaces switches on
the same four kinds in `internal/overview/preview_links.go`. Matching is repeated
for drawing and click resolution, with buffer-revision caches for file and git
lookups.

### Panes share geometry, not content orchestration

`internal/panelayout` owns a binary pane tree with fixed integer kinds:
Terminal, Document, Issue, and Diff. `internal/paneframe` owns the shared chrome,
compositor, geometry, and hit-region order. This is the correct parity seam, but
each surface still has its own switch from kind to content and its own document,
issue, and diff host state.

Adding one enum value and copying the td issue implementation twice would make
Jira work, but every later integration would reopen the same switches. Making
external packages implement `paneframe.Content` would be worse: it would expose
Bubble Tea/rendering internals across a process boundary and allow untrusted
render bytes to own layout and input.

### Persistence is kind-specific

`state.PaneLayoutJSON` stores a string kind plus separate document, issue, and
diff tab fields. Project Workspace persists layouts per terminal surface; global
Workspaces intentionally keeps preview-pane state in memory. Unknown or empty
leaves can already collapse during decode, but the fixed kind switches and
whole-tree support check need to learn the one generic Resource kind.

### Startup is sensitive

Provider discovery cannot enter `plugin.Init()`, config loading, app
construction, or a render path. The first frame must not wait on `LookPath`, a
subprocess, provider config, the network, or credentials. Provider description
must be an asynchronous command, and a provider that never becomes ready simply
contributes no matcher.

Make that sequencing explicit rather than relying on Bubble Tea command timing:
the app owns a one-shot readiness latch, closes it from the same ready-frame
branch that marks `first ready frame`, and starts `DescribeAll` only from a
cancellable command waiting on that latch. A command returned from `Init` may
start before the first render, so it must not launch providers directly.

## Scope and vocabulary

Call the extension a **terminal resource provider**, not a parser or an external
Sidecar plugin:

- **matcher:** a provider-declared RE2 regular expression compiled and executed
  by Sidecar over ANSI-stripped terminal text;
- **resource reference:** `{provider instance, matcher, locator}` produced by a
  match;
- **resolver:** the provider operation that turns a reference into a typed
  document;
- **Resource pane:** Sidecar's generic, passive, tabbed content leaf;
- **provider executable:** an explicitly configured local program implementing
  the protocol.

“Parser” suggests access to raw terminal bytes or the VT parser. Providers get
neither. They work after the shared screen model has produced visible text, so
they cannot affect terminal emulation, cursor state, input, or control-mode
transport.

The v1 capability is read-only resource lookup. It does not let a provider:

- render arbitrary ANSI, HTML, or Bubble Tea components;
- register keys, modals, app tabs, panes, or mouse regions;
- receive a whole terminal line, scrollback, repository contents, tmux identity,
  or the arbitrary host/TUI environment; the only environment is the documented
  base plus explicitly named `passEnv` variables;
- execute host callbacks or arbitrary commands through Sidecar;
- mutate Jira tickets or any other external system; or
- run merely because an untrusted repository contains a file.

## Architecture

```text
terminal text
    |
    v
terminallink.Scanner -- built-in matchers (URL/file/td/diff)
    |               \-- ready provider matchers (pure RE2, no I/O)
    v
Span{resource reference}
    |
    | click
    v
resourceprovider.Manager -- explicit config, timeout, process, protocol, cache
    |
    v
resource.Document -- typed, bounded, presentation-neutral
    |
    v
resourceview.Model/Tabs -- loading/error/card/scroll/keys
    |
    v
paneframe Resource leaf -- project Workspace and global Workspaces
```

There are two extension seams, each narrow for a reason:

1. `resourceprovider.Provider` is the in-process adapter interface consumed by
   the manager. The default implementation is `CommandProvider`; tests use an
   in-memory fake. A future built-in provider or resident transport implements
   the same interface.
2. The executable protocol is the language-agnostic boundary. It returns match
   declarations and resource data, never a Sidecar interface implementation.

The manager is host-owned and long-lived. Inject its read-only matcher snapshot
and resolve methods through app/plugin context so project and global callers use
one provider status, cache, and process policy. It must not be registered as a
normal Sidecar project plugin: it has no project tab or independent View.

### One generic Resource pane kind

Add one fixed `panelayout.Resource` kind and one `resource` persistence key.
Every external provider shares it. A Jira provider, a CI-build provider, and a
Sentry provider become tabs in the same leaf; installing another provider does
not modify pane layout code.

The fixed pane kind is intentional. The extension point is **which resource is
recognized and resolved**, not arbitrary window types. Provider-specific panes
would require dynamic floors, keymaps, persistence, hit regions, and trusted
rendering contracts and would turn this focused integration seam into an
extension platform.

Create `internal/resourceview` as a reusable component over the existing generic
tabs package and a resource-specific safe-markdown adapter. It consumes
`resource.Document` and owns:

- loading, typed error, and retry states;
- title and tab label;
- bounded field grid and resource-sanitized markdown body;
- vertical scrolling and selected/clickable source URL;
- keys and action hints independent of either host; and
- request generation so a late result cannot land in a closed or retargeted
  tab.

Project/global host files only bind leaf identity, focus, pane placement,
terminal viewport freeze, persistence lifetime, and Tea-message scoping. Put
shared pane chrome and region ordering in `paneframe`; do not introduce a second
compositor or border rule.

## Executable protocol v1

Publish the schema as `docs/reference/terminal-resource-provider-protocol.md`
and keep JSON fixtures in the repository. The canonical protocol identifier is
`sidecar.terminal-resource/v1`.

The configured executable receives exactly one JSON object on stdin and must
write exactly one JSON object to stdout. A provider may write diagnostic text to
`stderr`, which Sidecar drains boundedly but does not surface or retain. A valid
typed success or failure response exits 0; a non-zero exit, malformed JSON,
extra stdout, timeout, or oversize output is a provider transport failure.

The executable is short-lived in v1:

- `describe` runs asynchronously when configuration activates or changes;
- `resolve` runs lazily on click, explicit refresh, or a protocol-check command;
- Sidecar deduplicates identical in-flight resolves and caches successful
  results in memory; and
- every invocation gets a Sidecar-owned process group; cancellation or timeout
  kills that group, concurrently drains bounded stdout/stderr, waits/reaps the
  child, and never targets any process outside that invocation.

Raw stderr is never copied into logs, crash output, a toast, diagnostics, or the
protocol result. Sidecar drains it into a bounded discard/counting sink and
records only byte count and exit metadata; provider authors reproduce failures
by running the headless check or provider CLI deliberately. Drain stdout and
stderr concurrently so either stream cannot deadlock the other.

This avoids a server handshake, stream framing, restart supervisor, request-ID
multiplexing, and shutdown protocol before load justifies them. Keep method and
result types transport-neutral so a future `serve` capability can carry the same
objects over JSON-RPC without changing matchers, documents, or panes.

### `describe`

Request:

```json
{
  "protocol": "sidecar.terminal-resource/v1",
  "method": "describe",
  "instance": "jira-work",
  "host": {"name": "sidecar", "version": "0.0.0"}
}
```

Response:

```json
{
  "protocol": "sidecar.terminal-resource/v1",
  "provider": {
    "kind": "jira",
    "name": "Jira",
    "version": "1.2.0",
    "docsUrl": "https://example.test/sidecar-jira/setup"
  },
  "matchers": [
    {
      "id": "issue-key",
      "pattern": "\\b(?:CASH|GRES|AVATAXUI)-[1-9][0-9]*\\b",
      "priority": 100
    }
  ]
}
```

`describe` must be local and non-interactive. It may read the provider's own
configuration to build instance-specific patterns, but must not require a
credential prompt or network call. Sidecar validates and compiles patterns
before publishing a new immutable matcher snapshot. A failed replacement keeps
the last valid snapshot only within the current process and reports the new
failure; relaunch starts clean.

Matcher rules:

- syntax is Go/RE2, guaranteeing linear-time matching;
- the whole match is the locator in v1—no replacement templates or provider code
  run in the scanner;
- patterns, pattern bytes, locator bytes, matches per line, and providers are
  bounded;
- matching is case-sensitive unless the expression opts into RE2 flags;
- built-ins retain precedence; external matchers run afterward in ascending
  configured-provider order, then priority, then matcher ID; and
- overlaps are first-wins through the same visual-column overlap function the
  current scanner uses.

The host instance ID comes from Sidecar config. The executable's `kind` is
informational and cannot rename or collide with another configured instance.
`docsUrl`, when present, passes the same `http`/`https` validation as resource
source URLs and is the only executable-declared Setup action Sidecar follows.

### `resolve`

Request:

```json
{
  "protocol": "sidecar.terminal-resource/v1",
  "method": "resolve",
  "instance": "jira-work",
  "params": {
    "matcher": "issue-key",
    "locator": "CASH-1245"
  }
}
```

The initial request deliberately contains no terminal line, scrollback, tmux
target, environment, or repository path. If a future provider genuinely needs
project context, add a named capability and an explicit per-instance permission
rather than silently widening this request.

Success response:

```json
{
  "protocol": "sidecar.terminal-resource/v1",
  "resource": {
    "identity": "CASH-1245",
    "title": "Refund totals differ after partial capture",
    "subtitle": "Bug",
    "status": {"label": "IN PROGRESS", "tone": "info"},
    "fields": [
      {"label": "Assignee", "value": "Marcus"},
      {"label": "Priority", "value": "High"},
      {"label": "Updated", "value": "2026-08-17T17:31:00Z"}
    ],
    "body": {"format": "markdown", "text": "Ticket description..."},
    "sourceUrl": "https://jira.example.test/browse/CASH-1245",
    "freshForSeconds": 60
  }
}
```

Typed failure response:

```json
{
  "protocol": "sidecar.terminal-resource/v1",
  "error": {
    "code": "unauthorized",
    "message": "Jira credentials are missing or expired",
    "retryable": false,
    "setupHint": "Run sidecar-jira configure --profile work"
  }
}
```

Stable v1 error codes are `not_found`, `unauthorized`, `forbidden`,
`rate_limited`, `invalid_config`, `unavailable`, and `internal`. Human messages
are display text, not control flow. Unknown codes map to `internal`.

### Document contract and bounds

`resource.Document` is not a Jira DTO. It has only portable presentation data:
identity, title, optional subtitle/status, ordered label/value fields, optional
markdown body, validated source URL, update time, and a freshness hint.

Sidecar enforces limits before state reaches the view: total response and body
bytes, number/length of fields, title/locator/URL lengths, valid UTF-8, controls,
and `http`/`https` URL schemes. Unknown fields are ignored for forward
compatibility. Provider text never becomes ANSI and source-supplied OSC is
removed by the same security posture as terminal text.

The shared Markdown renderer is not itself a trust boundary: parsed Markdown
links can synthesize OSC-8 even when the input contains no escape bytes. Before
rendering a provider body, a resource-specific Markdown parser/sanitizer must
drop raw HTML, reduce images to inert alt text, and rewrite links/autolinks to
plain visible text with no destination node. After rendering, strip all OSC as
defense in depth. In v1 the separately typed, validated `sourceUrl` is the only
resource action that can open a URL. Body-link activation can be added later
only through Sidecar-owned hit regions over validated HTTP(S) destinations; the
renderer never gets to create an active link by itself.

The provider may suggest freshness, but Sidecar clamps it. There is no durable
body cache in v1. Debug logs record provider ID, method, duration, outcome code,
and byte counts—not the locator, title, body, URL, credentials, stdout, or stderr.

## Configuration and discovery

Use an app-level `terminalResources` section because providers serve both
workspace projections and are not project-tab plugins:

```json
{
  "terminalResources": {
    "providers": [
      {
        "id": "jira-work",
        "command": ["sidecar-jira", "sidecar-provider", "--profile", "work"],
        "passEnv": ["JIRA_API_TOKEN"],
        "enabled": true,
        "timeout": "10s"
      }
    ]
  }
}
```

Rules:

- configuration is explicit; do not scan plugin directories or execute every
  `sidecar-*` binary on `PATH`;
- `command` is an argv array and is executed without a shell;
- the first element may be an absolute path or resolve through `PATH`;
- working directory is a neutral Sidecar config directory, not an untrusted
  selected repository;
- do not support inline secret environment values; every child receives only
  `PATH`, `HOME`, `TMPDIR`, locale variables, XDG config/cache/state variables,
  and documented proxy/CA variables (`HTTP_PROXY`, `HTTPS_PROXY`, `NO_PROXY`,
  `SSL_CERT_FILE`, `SSL_CERT_DIR`, `GIT_SSL_CAINFO`) when present;
  `passEnv` names additional variables whose current values may be inherited;
  values are never rendered or logged, and the provider still owns credential
  interpretation and lookup;
- IDs are unique stable instance keys; order is matcher precedence;
- timeout and output limits have safe defaults and bounded overrides; and
- load/merge/save tests cover the new top-level section so the Configuration UI
  does not erase it.

Do not build automatic marketplace discovery, installs, upgrades, trust prompts,
or per-repository provider declarations in v1. A repository must never be able
to cause Sidecar to execute code merely by being opened. A process boundary is
crash isolation, not a sandbox: configuration and docs must state that enabling
a provider trusts that executable with the user's OS privileges.

### Setup and diagnostics

Configuration's Integrations area lists each explicitly configured instance:
enabled/disabled, command resolution, protocol/provider version, matcher count,
last describe result, and a Recheck action. It may open the provider's setup
documentation after confirmation or copy a setup command/hint, but it does not
execute a provider-supplied command, render or edit Jira-specific fields, or ask
Sidecar for Jira credentials. This follows the existing configuration decision:
Sidecar presents integration status and hands off setup while the external
system owns configuration and repair.

Add deterministic non-interactive protocol tooling:

```text
sidecar terminal-links list --json
sidecar terminal-links check jira-work --json
sidecar terminal-links check jira-work --resolve CASH-1245 --json
```

`list` and the default `check` run only configuration/command/describe checks.
`--resolve` is explicit because it can perform network access and print private
ticket data. These commands dispatch before TUI, tmux, state, or log setup, as
the current CLI does. They are protocol/admin surfaces, not a replacement Jira
CLI.

Extend the existing UI-request surface as well:

```text
sidecar open --provider jira-work CASH-1245 --json
```

This writes a typed Resource target through the same request transport already
used for files, td issues, and diffs. The running app matches the locator against
that provider instance, opens the same Resource tab as a terminal click, and
returns the normal structured delivery result. Keep v1 explicit: bare
`sidecar open CASH-1245` does not start provider discovery or guess among
providers in the short-lived CLI process. Add automatic unambiguous dispatch
only if real use shows the explicit flag is friction.

## Jira stays external

Create `sidecar-jira` as a separate executable/package while the host contract
is still a draft, then retain a synthetic fixture after the real provider has
shaped and frozen v1. It owns:

- Jira Cloud v3 versus Data Center/server API adapters;
- base URL, profile, project-key allowlist, field selection, and display labels;
- authentication through a provider-owned profile: prefer OAuth 2.0 (3LO) or
  macOS Keychain for long-lived Jira Cloud use; allow API-token environment
  auth only through explicit Sidecar `passEnv`; never support account passwords;
  keep Data Center auth in its own adapter because its supported mechanisms
  differ;
- Atlassian Document Format to safe markdown conversion;
- Jira error/rate-limit mapping into protocol error codes;
- human and structured agent commands such as `sidecar-jira issue show
  CASH-1245 --json` and `sidecar-jira doctor --json`; and
- the `sidecar-provider` protocol mode.

This separation is valuable even if `sidecar-jira` initially lives under the
same GitHub owner and Homebrew tap. Company auth, custom fields, Jira Cloud/Data
Center differences, and API churn can release independently of Sidecar. Another
team can implement the protocol around an existing corporate Jira CLI without
writing Go or importing Sidecar.

Sidecar's own documentation should show the external package as the reference
implementation, not promise it is bundled. Distribution can be Homebrew or a
standalone binary; Sidecar only cares that the configured argv resolves.
`sidecar-jira doctor --json` validates the provider's direct profile. The
authoritative host-environment proof is `sidecar terminal-links check jira-work
--resolve CASH-1245 --json`, because it launches with the exact base and
`passEnv` policy Sidecar will use in the TUI.

## State, refresh, and failures

### Tab identity

Before resolve, a tab key is the configured instance ID, matcher ID, and exact
matched locator. A successful response supplies a provider-stable identity. If it
differs, re-key the tab and merge with an already-open canonical tab rather than
creating duplicates. Never let a response change its provider instance.

Every request carries host-owned model ID, request generation, surface/workspace
identity, and project epoch outside the provider payload. A response for a
closed tab, superseded refresh, switched project, different global workspace, or
stopped manager is discarded.

### Persistence

Add a provider-neutral shape:

```go
type PaneResourceTabJSON struct {
    Provider string `json:"provider"`
    Matcher  string `json:"matcher"`
    Locator  string `json:"locator"`
    Scroll   int    `json:"scroll,omitempty"`
}
```

Project Workspace writes only references, active index, scroll, and the normal
pane tree. A reference necessarily includes the non-secret resource locator
such as `CASH-1245`. Relaunch re-resolves the active tab lazily; other restored
tabs remain armed until selected. It does not write resource titles, fields,
bodies, errors, URLs, auth state, credentials, or provider stdout. Global
Workspaces uses its existing per-workspace memory cache and writes nothing.

The UI-request spool also carries the locator for
`sidecar open --provider ...`; disclose its state-tree location and normal
delete-after-delivery lifetime in `PRIVACY.md`. Treat resource locators as
potentially sensitive identifiers, but do not call them auth tokens: they are
the minimum state needed to restore or deliver the user's requested pane.

Provider readiness is an explicit state machine: `unchecked`, `ready`,
`temporarily-failed`, `incompatible`, `disabled`, and `removed`. Restore never
interprets “describe has not returned yet” as “matcher was deleted.” Preserve
armed references in every non-ready state and render an appropriate
waiting/error/setup card when selected. A later successful `describe` validates
the stored matcher and resumes resolution.

Do not silently prune references or collapse the Resource leaf for a transient
failure, disabled provider, incompatible upgrade, missing command, removed
config entry, or no-longer-declared matcher. Only the user's normal tab close or
an explicit, confirmed cleanup removes them. This favors recovery after a config
rollback or provider reinstall over automatic tidiness and prevents startup
timing from becoming data loss.

### Refresh and cache

- In-memory cache key: provider instance + canonical identity, scoped to the
  provider description generation.
- Cache only successful sanitized documents; clamp provider freshness.
- `r` bypasses freshness, preserves scroll/focus, and retains the last good
  document behind a refreshing indicator.
- A transient refresh error leaves the last good document visible with an
  error status. An initial failure shows the error card.
- Deduplicate identical in-flight resolves. Start with a small global and
  per-provider concurrency cap; excess work queues and remains cancellable.
- No provider filesystem watcher, polling loop, webhook, or push notifications
  in v1. Add resident transport only after measured repeated-start or refresh
  pressure.

## Incremental delivery

Each milestone is independently reviewable. User-visible work stays behind a
`terminal_resource_providers` feature flag, default off, until the Jira journey
and both workspace projections are proven.

### M0 — Thin contract and a real Jira probe

Do not build the generic host against only a fixture. Draft the smallest
describe/resolve/document contract, exercise it with one real provider, revise
it from that evidence, and only then freeze v1 before the pane/view API spreads
through Sidecar.

1. Draft the minimal protocol schema and JSON request/response examples without
   declaring v1 frozen.
2. Create the external `sidecar-jira` package with a narrow direct command
   (`issue show KEY --json`) and protocol mode implementing `describe` and
   `resolve` for the work deployment actually required. Use an HTTP adapter and
   fake server; do not generalize Cloud/Data Center or field mapping yet.
3. Run one private, credentialed read through protocol mode to confirm the real
   base URL, auth mechanism, issue endpoint, key shape, description format, and
   minimum useful fields. Record only structural findings—no ticket payload or
   credentials—in the plan/task.
4. Revise the schema from the real response, then freeze and publish v1 with its
   protocol reference and canonical JSON fixtures.
5. Add `internal/resource` domain types and validation/sanitization against the
   now-frozen contract.
6. Add `internal/resourceprovider` with the narrow in-process interface,
   immutable matcher snapshot, `CommandProvider`, process runner adapter,
   timeout/output limits, and typed errors.
7. Add a tiny test fixture executable that describes `CASH|GRES|AVATAXUI` and
   resolves from deterministic synthetic JSON. It keeps Sidecar CI credential
   free but is no longer the only design input.
8. Add the top-level config model/loader/saver/validation and
   `sidecar terminal-links list/check --json`.
9. Start provider description asynchronously after the first ready frame and
   expose diagnostics without blocking startup or rendering. Implement the
   app-owned readiness latch described above; do not infer readiness from when
   an `Init` command happens to run.

**Gate:** `sidecar-jira issue show CASH-1245 --json` (or a safe accessible work
key) is independently useful and protocol mode resolves the same live issue.
Sidecar protocol goldens work against a real child process; malformed,
oversize, hung, crashing, noisy-stdout, incompatible, duplicate-ID, invalid-RE2,
stderr-flooding, forked-descendant, and cancellation cases fail boundedly and
leave no child behind. Startup trace has no provider process before the first
ready frame. The CLI never touches tmux or TUI state.

### M1 — Steel thread through both workspace projections

1. Extend `terminallink` with `KindResource` and typed resource-reference
   metadata. Accept an immutable list of compiled external matchers while
   retaining one overlap/column authority and current built-in precedence.
2. Extend decoration and hit testing to the resource kind. Matching remains
   pure; a missing/unready provider contributes no underline.
3. Build `resourceview.Model` and tabs with loading, typed errors, fields,
   markdown, scrolling, Retry, and Open Source.
4. Add `panelayout.Resource`, its floor, `resource` content key, and shared
   frame support. No provider-specific pane kind enters layout.
5. Bind the Resource leaf in the project Workspace and global Workspaces
   `pane_host.go` files; update leaf/body/tab/close region switches together.
6. Route activation through one host-independent intent carrying the resource
   reference. Each host attaches it to its surface, opens/focuses a tab, and
   applies the scoped async response.
7. Extend `uirequest` and `sidecar open --provider` with the same typed intent;
   keep file/td/diff precedence and request routing unchanged.
8. Preserve terminal viewport freeze, selection clearing, interactive exit,
   focus transfer, and tmux resize behavior when the new leaf opens.
9. Add project reference-only persistence and global in-memory cache behavior.
10. Register identical resource-pane commands and footer hints on both surfaces.

**Gate:** the synthetic fixture proves repeatable CI, and the minimal
`sidecar-jira` from M0 opens one live work ticket from real terminal output in
both project and global Workspaces. Same-locator focus, multi-tab, resize,
scroll, refresh, close, project relaunch, global row switch, provider failure,
and stale-result journeys pass. Existing URL/file/td/diff tests remain green
without changed precedence.

### M2 — Configuration, diagnostics, and hardening

1. Add the Integrations status/repair UI without absorbing provider-specific
   setup or secrets.
2. Complete privacy-safe diagnostics and protocol/version mismatch guidance.
3. Add cache/concurrency/cancellation behavior and stress it with rapid clicks,
   row/project switches, provider restart/failure, and Sidecar shutdown.
4. Add hostile matcher/document fuzzing and render bounds at small terminal
   sizes.
5. Document provider trust, configuration, authoring, protocol compatibility,
   and troubleshooting. Add a minimal provider-author template in a separate
   examples directory only if it materially reduces duplicated boilerplate.
6. Update `PRIVACY.md`: enabled executables receive the clicked locator and may
   contact their configured service; Sidecar does not persist returned bodies
   or send terminal context.

**Gate:** no auth credential or returned resource title/field/body/URL appears
in logs, pane state, crash output, or default diagnostics. Reference-only pane
state and the bounded UI-request spool may contain the resource locator and are
documented as such. Disabling/removing/breaking a provider removes its live
matchers but preserves armed tabs until the user closes or explicitly cleans
them. Provider work remains absent from the first-frame startup path.

### M3 — Productionize and release the `sidecar-jira` reference provider

1. Expand the M0 external package with a complete human/JSON CLI,
   doctor/configure journey, and durable network/auth adapter seams.
2. Harden Cloud v3 and the work deployment actually required; add Data
   Center support only if the real work instance needs it or another user can
   exercise it.
3. Generate exact Jira-key matchers from the configured project-key allowlist.
4. Map selected standard/custom fields and ADF description into the bounded
   resource document. Keep provider config responsible for company-specific
   fields.
5. Prove API behavior against an HTTP test server and sanitized real response
   fixtures; never require work credentials in Sidecar CI.
6. Release/install the provider independently, configure it in Sidecar, and run
   a credentialed work proof: `CASH-1245` printed by an agent → click → correct
   live ticket → refresh → open in Jira.

**Gate:** independent reviews cover both repositories. The Sidecar host is
proven with its fixture and the Jira package with fake/sanitized API data; the
integration is not called complete until the credentialed real journey succeeds
without exposing secrets or ticket bodies in captured artifacts.

### M4 — Evidence-driven evolution only

Do not schedule this milestone merely because the protocol exists. If measured
process startup or frequent refresh is material, add an optional resident
`serve` capability using the same describe/resolve/document objects over a
versioned JSON-RPC transport. If two non-Jira providers need project context,
design a least-privilege capability. If providers need richer safe interaction,
add one typed action at a time.

None of those pressures justify arbitrary external UI or in-process code.

## Verification strategy

### Pure and contract tests

- matcher order, visual columns, ANSI, tabs, Unicode graphemes, punctuation,
  overlaps, invalid regex, pattern/span limits, and provider-generation swaps;
- document validation, bounds, controls, URL schemes, markdown sanitization,
  timestamp/tone/error coercion, forward-compatible unknown fields, raw
  HTML/images, `file:`/`ssh:`/`data:`/malformed destinations, embedded controls,
  and generated-OSC removal;
- process argv (no shell), stdin/stdout contract, stderr separation, exit codes,
  concurrent bounded stream draining, process-group timeout/cancellation,
  kill-and-wait cleanup (including forked descendants), base/pass-through
  environment, working directory, and output caps;
- config defaults/merge/save preservation, duplicate IDs, command validation,
  timeout clamps, and disabled providers;
- cache, dedupe, concurrency, refresh, canonical re-key/merge, and no secret
  logging; and
- persistence round-trip plus preservation through delayed describe, transient
  failure, disabled/removed/incompatible providers, and later recovery; only
  explicit close/confirmed cleanup prunes.

### Host parity tests

Use a shared journey table against project and global host adapters wherever
lifetime intentionally agrees:

- link becomes decorated only after a provider is ready;
- exact cell hit resolves the same reference that was decorated;
- click opens/focuses Resource, clears selection, freezes viewport, exits
  interactive mode, and focuses the new leaf;
- second locator adds a tab; duplicate focuses; `{`/`}`/`x`/`q`/`esc`/`r`/`o`
  have the documented result;
- response identity and generation route only to the requesting tab/workspace;
- loading/error/refresh content is height-constrained and footer-owned; and
- close/tab/body/divider hit-region priority comes from `paneframe` on both.

Test the intentional lifetime difference separately: project persists only
references; global caches only in memory.

### Real app proof

Use `scripts/tmux-drive.sh` only after `paths` confirms isolated tmux **and**
state/config trees. Always stop it. The proof fixture must be an executable
child process so it exercises argv, JSON, timeout, and lifecycle rather than
injecting an in-memory fake.

Capture at least:

1. provider not ready: ticket plain text, terminal usable;
2. provider ready: resource key underlined;
3. click in project Workspace: loading then details, coherent pre-click terminal
   viewport after resize;
4. same in global Workspaces;
5. multiple resource tabs and keyboard/mouse tab selection;
6. provider timeout/error then Retry;
7. narrow resize/zoom/close and return to live output; and
8. project relaunch restoring identifiers without a ticket body on disk.
9. delayed and temporarily failing `describe` preserving the armed tab and
   recovering it when the provider becomes ready.

Do not stop, restart, or replace the default tmux server for any proof. Do not
record real work ticket contents in repository fixtures, screenshots, logs, or
review artifacts.

Run the ordinary gates on the integrated Sidecar candidate:

```bash
go build ./...
go vet ./...
go test ./...
git diff --check
```

Every milestone receives independent review. M1 and M3 need explicit review of
privacy, stale-result routing, process cleanup, and project/global parity in
addition to green tests.

## Risks and mitigations

| Risk | Mitigation |
| --- | --- |
| A matcher slows every frame | RE2 only; compile once; immutable snapshots; strict provider/pattern/span/locator limits; no I/O in scan/decorate/click hit testing |
| An external process freezes the TUI | All process work in Tea commands; immediate loading state; context timeout/cancel; concurrent bounded stdout/stderr draining; bounded concurrency/output; never invoke from `View` or `Update` |
| A provider crashes, forks, or prints garbage | Per-invocation process group, kill-and-wait/reap, strict one-object stdout, no raw stderr in default output, typed protocol errors, retry, and no host crash |
| A malicious repo triggers code | Providers only from user config; no repo manifests, plugin-dir scans, or auto-discovery; neutral cwd; matching alone never executes |
| “Out of process” is mistaken for a sandbox | Configuration and docs explicitly say the executable has the user's OS privileges; no auto-install/enable |
| Auth secrets or returned work-ticket content enter Sidecar state/logs | Provider owns auth; request/reference stores only the locator; sanitized document is memory-only; metadata-only logs; privacy and persistence tests |
| Jira-specific fields leak into the host contract | Generic `resource.Document`; provider maps Cloud/Data Center/custom fields outside Sidecar |
| Project/global surfaces drift | Shared manager/scanner/view/frame, one parity journey suite, both hosts required in M1 |
| Late results land in another tab/project | model ID + request generation + provider generation + surface/workspace + project epoch; discard on every mismatch |
| Provider startup/removal destroys saved references | explicit readiness states; preserve armed tabs through unchecked/failure/disabled/removed/incompatible states; prune only on close or confirmed cleanup |
| The generic pane becomes an arbitrary extension API | v1 typed passive document and one safe URL action; no raw rendering, keys, modals, host callbacks, or mutations |
| One-shot launch becomes expensive | measure; cache and dedupe first; optional resident transport only after evidence, preserving the same domain contract |
| Active windowing/config work conflicts | Land shared `panelayout`/`paneframe` and config changes against their current canonical shapes; do not resurrect private compositors or bypass config save/merge seams |

## Deliberately deferred

- arbitrary external Sidecar plugins or contributed UI;
- Go shared-object plugins, embedded JavaScript/TypeScript, WebAssembly, gRPC, or
  a marketplace;
- provider discovery from repositories or automatic trust/install/update;
- resident providers, bidirectional callbacks, streaming, push refresh, or
  webhooks;
- provider-defined colors, ANSI, keybindings, actions, pane kinds, floors, or
  persistence blobs;
- mutation of Jira or other resources;
- automatic project-root, terminal-line, scrollback, file, environment, or tmux
  context sharing;
- durable caches of resource bodies;
- converting URL/file/td/diff built-ins onto the executable protocol; and
- a bundled Jira client in the Sidecar binary.

Built-ins should use the same scanner-span vocabulary where it removes closed
switches, but they remain fast in-process adapters. Do not force files, git, or
td through subprocess protocol ceremony to claim architectural purity.

## Completion criteria

This initiative is complete when:

- an explicitly configured external executable can declare a safe matcher and
  resolve it through the documented v1 contract;
- `CASH-1245`, `GRES-4433`, and `AVATAXUI-4323` open correct live Jira details
  through the separately released provider;
- the project and global workspace journeys have matching behavior and shared
  implementation at the scanner/view/frame seams;
- terminal recognition remains pure and bounded, provider work remains off the
  first-frame path, and failures cannot stall or crash the TUI;
- Sidecar does not own Jira auth/config/API DTOs and does not persist or log
  ticket bodies;
- provider discovery timing or failure cannot delete saved resource references;
- the protocol has a headless JSON check surface and a real-process fixture;
- agents can address the same pane deterministically with
  `sidecar open --provider ... --json`, while provider data remains available
  through the external provider's own structured CLI;
- existing URL, file, td issue, and diff behavior is unchanged;
- docs, configuration status, privacy disclosure, tests, isolated real-app
  proof, and independent reviews are complete; and
- no code path touches or restarts the default tmux server.
