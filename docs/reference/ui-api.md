# Sidecar UI API v0

`sidecar api serve` exposes Sidecar's Sessions and terminals to UIs that are not the TUI: the reference web UI in `~/code/sidecar-ui`, embedded components in other apps, and agents and scripts. This document is the wire contract. The plan and the reasoning behind it are in [the Sidecar UI API plan](../plans/active/sidecar-ui-api.md).

v0 is the U0 steel thread. It serves the Sessions catalog and the existing terminal protocol over HTTP and WebSocket, with the transports, guards and pairing that every later version keeps. U1 adds catalog and attention events. U3 adds project content, open-pane invalidations and per-viewer layouts. v0 changes in place while the API is private, and fixtures and clients move with it.

## Process and discovery

`sidecar api serve` is one long-running, headless process per user. It never starts, stops or restarts the tmux server. It holds one remote-host registry and catalog router for its lifetime, and gives each terminal connection its own protocol broker, which is the same per-stream model `sidecar mobile serve --stdio` uses.

On start it writes `$STATE/api/endpoint.json` with mode 0600. `$STATE` is `config.StateDir()`. It removes the file on clean exit (SIGINT, SIGTERM, or executable replacement). A second `serve` refuses to start while the first is running: the server holds an exclusive lock on `$STATE/api/serve.lock` for its lifetime, the kernel drops the lock if the process dies, and the refusal names the PID recorded in `endpoint.json`. A client that finds an `endpoint.json` whose PID is not alive treats it as no server.

```json
{
  "pid": 4242,
  "version": "v1.16.0",
  "api_version": 0,
  "api_instance": "opaque per-process id",
  "started_at": "2026-10-03T18:00:00Z",
  "unix_socket": "/…/api/api.sock",
  "tcp": "127.0.0.1:7861",
  "tailnet_socket": "/…/api/tailnet.sock"
}
```

`tailnet_socket` is present only when `--tailnet` is given, and `tailnet_tcp` (for example `"127.0.0.1:7862"`) replaces it when `--tailnet-port` is given. CLI verbs and local SDK users discover the server from this file.

The process decides once, at start, whether terminals and the catalog route through the remote-host hub (the `sidecar_remote_hosts` feature with at least one registered host) or through the local owner service. Registering the first host, or removing the last one, takes effect at the next `serve`.

## Listeners and trust

All three listeners serve the same routes. They differ only in how a caller is trusted.

| Listener | Address | Caller is trusted when | Guards |
| --- | --- | --- | --- |
| Local | Unix socket `$STATE/api/api.sock`, mode 0600 | Always. File permissions limit it to the user, the same trust as the tmux socket. | None. Pairing and origin management are served only here. |
| Browser | TCP `127.0.0.1:<port>`, default 7861, `--port 0` picks a free port | It carries a bearer token (a browser session token or a paired origin's token) or a ticket, bound to the request's origin | Host, Origin and mutation guards |
| Tailnet | Unix socket `$STATE/api/tailnet.sock`, mode 0600, proxied by `tailscale serve unix:<path>` | The `Tailscale-User-Login` header names an allowed login. The header is honored only on this listener. | Host, Origin and mutation guards |

`serve` never binds a non-loopback address in v0.

With `--tailnet`, `serve` creates the tailnet socket and prints the `tailscale serve` command that exposes it. It does not change the Tailscale configuration itself. Allowed logins come from config `api.tailnetLogins`. If that is unset, the default is the login that owns the local Tailscale node (`tailscale status --json`). If `tailscaled` cannot open a 0600 user socket, as can happen with a sandboxed Tailscale build, the fallback is `--tailnet-port N`: the Tailnet listener, with the same guards, on a dedicated loopback port for the proxy to target (`tailscale serve --bg http://127.0.0.1:N`). That fallback is weaker, and `serve` prints a warning when it is used. The Tailnet listener trusts `Tailscale-User-Login` because only `tailscaled` can reach its 0600 socket, but any local process or OS user can connect to a loopback port, send an allowed login and the MagicDNS `Host`, and drive terminals. A browser cannot do this, because it cannot set that header or that `Host`. Use the fallback only on a machine where every local user and process is already trusted. `serve --tailnet` prints both commands. `serve` cannot detect the failure itself without changing Tailscale configuration, which it never does.

Recorded on aerie on 2026-10-03: the standalone Tailscale build 1.102.4 (`io.tailscale.ipn.macsys`), whose network extension runs as root. Whether it can open the 0600 socket under `~/.local/state/sidecar/api/` is not yet verified, because the U0-a proofs do not change Tailscale configuration. The first live `tailscale serve unix:` run settles it. If it fails, use `--tailnet-port`.

### Guards on the Browser and Tailnet listeners

- **Host:** the `Host` header must exactly match an allowed value. For Browser that is `127.0.0.1:<port>` and `localhost:<port>`. For Tailnet it is the node's MagicDNS name (from `tailscale status --json`), bare or with `:443` or `:80`. Any other value gets `421 host_refused`, on every route including the terminal upgrade. This defeats DNS rebinding.
- **Origin:** a WebSocket upgrade must carry an allowed `Origin`, and so must any request that is not `GET` or `HEAD`, with one exception on the Browser listener: a request or upgrade that carries `Authorization: Bearer <token>` with a non-empty token may omit `Origin` (see Authentication). Any other `Authorization` scheme, such as `Basic`, or an empty bearer token does not qualify, because a browser can attach those by itself. Allowed origins are the listener's own origins plus paired origins. A listener's own origins are `http://127.0.0.1:<port>` and `http://localhost:<port>` for Browser, and `https://<magicdns>` and `http://<magicdns>` for Tailnet. Any request that carries an `Origin` outside that set, including a `GET`, gets `403 origin_refused`. A `GET` or `HEAD` without an `Origin` goes on to authentication.
- **Mutations:** a non-GET request must be `Content-Type: application/json` and must carry `X-Sidecar-Request: 1`. A browser cannot send either cross-site without a CORS preflight, and the server refuses unpaired preflights.
- **CORS:** only paired origins get CORS headers: the exact `Access-Control-Allow-Origin` with `Vary: Origin`, and on a preflight `Access-Control-Allow-Methods: GET, HEAD, POST, PUT, DELETE`, allowed headers `Authorization, Content-Type, X-Sidecar-Request, If-Match, If-None-Match` and exposed response header `ETag`, and `Access-Control-Max-Age: 600`. There are no credentials. A preflight from any other origin gets `403 origin_refused`.

### Authentication on each listener

- **Local:** every request is trusted.
- **Browser:** `/api/` routes need `Authorization: Bearer <token>`, where the token is either a browser session token (see Pairing) or a paired origin's token. Nothing ambient travels with a request: every client, same-origin, paired or non-browser, takes this one path. Each token is bound to one origin. A session token is bound to the exact origin that exchanged its pairing code (for example `http://127.0.0.1:7861`, not also `http://localhost:7861`), and a paired origin's token to that origin. A request that carries any other `Origin` gets `403 origin_refused`. A request with no `Origin` is accepted with a valid bearer token, for any method and on the terminal upgrade, because the Origin guard exists to stop a page from using a credential the browser attaches by itself, and a bearer token is never attached by itself. That covers a same-origin `GET`, curl, Node (whose `WebSocket` sends no `Origin`) and native clients. Without a bearer token, a mutation or upgrade with no `Origin` is still refused, and so is a ticket presented with no `Origin`, because a ticket is bound to its origin. The Tailnet listener keeps requiring `Origin`: its login header is added by `tailscale serve` to every request from the device, so it is ambient. Static UI files, `GET /pair` and `POST /api/v0/pairing/exchange` need no credential. An API route without one gets `401 unauthenticated`.
- **Tailnet:** every route, static files included, needs a `Tailscale-User-Login` header that names an allowed login. A missing login gets `401 unauthenticated`, and a login that is not allowed gets `403 tailnet_login_refused`. The header is ignored on every other listener. The login is ambient (`tailscale serve` adds it to every request from the owner's devices, whatever page sent it), so it authorizes only the listener's own origins and same-origin requests without an `Origin`. A request whose `Origin` is a paired origin must also carry that origin's own `Authorization: Bearer <token>`, and its WebSocket upgrades must spend a ticket bought with that token on this listener; the login alone gets `401 unauthenticated` or close `4401`. Its streams then belong to the paired origin, so revoking or re-pairing it closes them. Browser session tokens are not accepted here.

### Per-client limits

A client is one credential holder: a browser session, a paired origin, or a tailnet login. Each may hold at most 16 open events WebSockets and, separately, 16 open terminal WebSockets (the 17th closes with `4429` and a reason) and 16 unredeemed tickets (the 17th request gets `429 too_many_outstanding`). Redeeming or expiring a ticket frees its slot. Across all clients there are at most 256 unredeemed tickets and 64 unused pairing codes. Local callers are trusted like the tmux socket and have no terminal limit. HTTP content and tree reads share a separate limit of four in-flight requests per credential holder across all projects, including the trusted Local caller. A fifth read gets `429 too_many_outstanding` immediately, without starting or queueing backend work; completion, errors and cancellation free the slot. Layout reads do not spend this budget.

## Pairing

### Same-origin browser (the UI served by `serve`)

1. `sidecar api open` asks the server for a one-time code over the Local socket (`POST /api/v0/pairing/codes`). The code is single-use and expires after 60 seconds.
2. `api open` then opens `http://127.0.0.1:<port>/pair#code=<code>&next=<path>` in the default browser. `--print` prints the URL instead. The code is in the fragment, which a browser never sends, so it never appears in a request line, a proxy log or a `Referer`.
3. `GET /pair` serves a small built-in page. Loading it (`GET` or `HEAD`) reads nothing from the request and consumes nothing. The page is locked down: `Content-Security-Policy` allows only its own hashed script and same-origin `fetch`, and forbids framing; `Cache-Control: no-store`; `Referrer-Policy: no-referrer`.
4. The page's script reads `code` and `next` from the fragment, removes them from the address bar and history, and sends `POST /api/v0/pairing/exchange`.
5. The exchange returns a session token. The page stores it in `localStorage` under the key `sidecar.session` and navigates to `next`, after checking that `new URL(next, location.origin)` is still on this origin.

`localStorage` is scoped to scheme, host and port, so a page served on any other port of `127.0.0.1`, including another local user's, cannot read the token. A UI served by `serve` reads `localStorage.getItem("sidecar.session")` and sends it as `Authorization: Bearer` on HTTP calls, and spends it on a ticket for each terminal or events WebSocket.

`POST /api/v0/pairing/exchange` is served only on the Browser listener. It needs no credential, because the code is one, but it is guarded like any mutation (`Content-Type: application/json`, `X-Sidecar-Request: 1`), and its `Origin` must be one of the listener's own origins. A paired origin gets `403 origin_refused`; it has its own token.

Request. `code` is required; `next` is optional:

```json
{"code": "<code from the fragment>"}
```

Success, `200`. `token` is the contract; `next` is additive (the validated path, `/` when the request gave none), and clients may ignore it and use the `next` from the fragment:

```json
{"token": "<session token>", "next": "/"}
```

`next` defaults to `/`. It must be a same-origin path: it starts with exactly one `/`, and has no scheme, host, backslash or control character. A bad `next` gets `400 invalid_request` and does not consume the code. An unknown, used or expired code gets `401 pairing_code_invalid`. The session token is bound to the request's `Origin`.

Sessions are kept in memory in v0, so a server restart means pairing again, and a client that gets `401 unauthenticated` with a stored token should discard it and ask the user to run `sidecar api open`. Persisting sessions is a later decision.

`sidecar api pair --revoke-sessions` signs out browser sessions without a restart (`DELETE /api/v0/pairing/sessions`, Local only). With `--origin URL` (`?origin=URL`) it revokes only the sessions bound to that origin. A revoked token gets `401 unauthenticated` on its next request, tickets it issued and has not redeemed stop working, and every terminal it opened, directly or through a ticket, closes at once with `4401`, including streams whose session token was evicted from the bounded in-memory session store. `revoked` counts stored session tokens; `terminals_closed` also includes those older streams. Paired origins are not sessions: their tokens survive, and `--revoke URL` manages them.

### Another origin (an embedding app)

`sidecar api pair --origin https://app.example:5173 [--scopes content:read]` registers the origin over the Local socket (`POST /api/v0/origins`) and prints a bearer token. Registrations persist in `$STATE/api/origins.json` with mode 0600, as `{origin, token_sha256, scopes, created_at}`. The server keeps only the token hash. `sidecar api pair --list` lists registrations. `sidecar api pair --revoke ORIGIN` (`DELETE /api/v0/origins?origin=…`, Local only) removes the registration, invalidates its unused tickets, and closes every terminal authenticated with that origin's token or tickets with `4401`. A concurrent request authorized before revocation cannot issue a new ticket or register a terminal afterward. Re-pairing the same URL does not revive the old credential or its tickets. Other origins and browser sessions remain valid.

`full` implies every scope. `content:read` grants project content, tree, content invalidations and that credential holder's own layout preferences. It does not grant Sessions, status, terminal access or credential management. Browser sessions, Local callers and allowed Tailnet logins retain full access. Paired-origin scope checks apply equally to bearer and ticket event connections.

HTTP calls from a paired origin send `Authorization: Bearer <token>`. Pairing an origin again rotates its token: the old token, its unredeemed tickets, and every terminal and events stream it opened stop working at once (`4401`), as with revocation. Browsers cannot set headers on a WebSocket, so a paired origin first gets a ticket with `POST /api/v0/ws-tickets` (bearer-authenticated, single-use, 30-second expiry). It then connects with `?ticket=<ticket>`. A ticket is bound to the listener and the origin it was issued to. The same-origin UI does the same with its session token. A non-browser client may instead send `Authorization: Bearer <token>` on the upgrade itself. It either sends no `Origin` or sends the origin that token is bound to (the paired origin, or for a session token the origin that exchanged it); any other `Origin` closes with `4403`.

## Workspace operation core

Workspace writes are prepared for U2 through `workspaceops.Service`, a transport-neutral service with no viewer, selection, or in-flight operation state. The CLI, project Workspaces TUI, and global Sessions view use the same local operation boundary; remote mutations call it on the owning host through the CLI. U2-a adds no HTTP routes, capabilities, or wire fields.

Worktree creation uses a confirmed `WorktreePlan`: plan, begin (Git execution and recovery journal), identity and configured setup, finalization, then launch. The non-interactive `CreateWorktree` composes the same phases the TUI calls separately for progress and recovery. A partial Git success is journaled even after cancellation. Required journal or setup failures retain the created identity and prevent normal finalization and launch. Retry runs setup against that identity; the TUI retains its explicit open-anyway decision and can finalize before opening. Optional warnings retain each surface's existing presentation policy. Task start remains explicit: the project form starts its linked task; CLI and Sessions link without an additional task-start subprocess during setup.

Shell create, rename, delete, and tombstone restore, and worktree display-name rename and deletion, go through that service. Shell persistence and locking live only in `shellstate`; the plugin's `ShellManifest` is a compatibility projection with local revision tracking. Tombstone restore restores a durable shell record without starting tmux; cold session recreation remains the existing `sessionrestore` executor. Worktree launch reconnects an existing session rather than creating another one. No operation restarts the tmux server.

Asynchronous viewer completions carry the requesting project context and are rejected before applying state when that context changes. Durable shell operations retain the original project's adapter; dropping a stale UI reply never writes the next project's manifest or starts a continuation there. Sessions additionally fences changed project configuration and replacement operation dialogs while retaining valid replies across ordinary refresh polls. The completion audit and regression contract are in [Workspace asynchronous completion ownership](workspace-completion-fences.md). This is an internal ownership fix: it adds no HTTP routes, capabilities, schemas, fixtures, or mobile protocol fields.

Create shell/worktree JSON with `--agent` includes `agent_start: {kind, status, error?}`. `ready` means Sidecar positively identified the requested provider as idle or done; `failed` includes the existing agent error code/message and the command exits 1 while retaining the created identity for recovery; `not_started` means Sidecar did not request a provider start (for example an explicit `--run`, disabled shell agent control, or required setup failure). Without `--agent` the field is absent. `acked` describes viewer placement only and never provider readiness. Startup waits for positive provider evidence within its existing caller deadline; a shell that has not consumed queued launch input is not declared an exited provider on an elapsed-time heuristic. A provider observed running and then returning to an idle shell still fails immediately. The local launch adapter also records completion of the exact launch attempt in one pane option after its command returns, so a provider that exits wholly between observations fails on the next inspection instead of waiting for the deadline. Each launch uses a fresh random identity; previous completions cannot fail a later launch. This CLI result addition changes no HTTP route or mobile envelope in U2-a; workspace operation API callers should adopt the same outcome when those routes land.

`workspaceops.AgentLauncher` shares readiness and provider start sequencing, retaining each caller's resolved argv, deadlines, target policy, and error wording. Reconnecting worktree sessions skip shell-readiness waiting. `agentresolve.ResolveTarget` accepts explicit caller context and a target lookup adapter, so headless callers share the CLI's target-required and project/shell scoping rules. `workspacelist.Projected`, `SectionsAt`, the pin helpers, and `Hidden` provide state-free list policy; clocks, pins, and source-resolved visibility facts are caller inputs. Human selection, scrolling, collapsed sections, and presentation remain in their models.

## HTTP routes

Every HTTP `GET` route also accepts `HEAD` with the same listener and authentication rules and no response body. The terminal WebSocket handshake remains `GET`-only. The generated spec lists both methods for resources, the pairing page and static UI files.

All JSON, encoded exactly as the CLI's `--json` output: one object and a trailing newline. Successful responses are `200`. Errors are `{"error": {"code": "snake_case_code", "message": "One human sentence that says what to do."}}` with a fitting status. Codes match the CLI's refusal vocabulary where one exists. The API adds these:

| Code | Status | When |
| --- | --- | --- |
| `host_refused` | 421 | The `Host` guard. |
| `origin_refused` | 403 | The `Origin` guard, an unpaired preflight, or a token used from another origin. |
| `mutation_refused` | 403 | A non-GET request without `Content-Type: application/json` and `X-Sidecar-Request: 1`. |
| `unauthenticated` | 401 | No usable bearer token or tailnet login. |
| `tailnet_login_refused` | 403 | A tailnet login that is not allowed. |
| `local_only` | 403 | A Local-only route on another listener. |
| `not_served_here` | 403 | A route this listener does not serve, such as tickets on Local or `/pair` off Browser. |
| `not_found` | 404 | No such `/api/` route. |
| `method_not_allowed` | 405 | The route exists, but not for this method. `Allow` lists the methods. |
| `invalid_request` | 400 | A malformed body, query or `next`. Catalog query refusals keep this code. |
| `pairing_code_invalid` | 401 | An unknown, used or expired pairing code. |
| `origin_not_found` | 404 | Revoking an origin that is not paired. |
| `too_many_outstanding` | 429 | Over a pairing, ticket, authentication or concurrent content-read limit. Content and tree reads share four in-flight requests per credential holder. |
| `upgrade_required` | 426 | `GET /api/v0/terminal` or `GET /api/v0/events` without a WebSocket upgrade. |
| `backend` | 503 | The catalog failed. Other catalog refusals keep their protocol code, such as `overflow`. |

| Route | Listener | Returns |
| --- | --- | --- |
| `GET /api/v0/hello` | any | `{api_version, api_instance, server_version, capabilities: ["sessions", "status", "terminal", "ws_tickets", "events", "content", "layouts"], terminal: {protocol: "mobile", version: 0}}` |
| `GET /api/v0/sessions` | any | The Sessions catalog: the same `mobileproto.CatalogSnapshot` JSON as `sidecar mobile sessions --json`. Query parameters map to `catalog_query`: `sort`, `search`, repeatable `host`, `provider`, `state`, and `show_idle_sessions=true\|false`. It is served by the same code path as the CLI. Any other parameter, or a repeated `sort`, `search` or `show_idle_sessions`, gets `400 invalid_request`. |
| `GET /api/v0/events` | any | The events WebSocket described below; query parameters match `sessions`, plus a single optional `ticket` and repeated `content` references. |
| `GET /api/v0/status` | any | `{api_version, api_instance, server_version, pid, started_at, listeners: [{name, network, address, host?}], clients: [{id, kind, listener, auth, origin?, login?, since}], terminals: [{client_id, owner_host_id?, workspace_id?, session?, pane?, display_name?, control, holder?: {kind, label}}]}`. A client is one open terminal or events WebSocket, and `auth` is `local`, `session`, `bearer`, `ticket` or `tailnet`. `terminals` lists the clients with an open attachment, and `control` is observed from the stream's own responses. `sidecar api status --json` prints this document byte for byte. |

| `POST /api/v0/ws-tickets` | Browser, Tailnet | Body `{}` or empty. Returns `{ticket, expires_at}`. At most 16 unredeemed per client. |
| `POST /api/v0/pairing/codes` | Local only | Body `{next?}`, default `/`. Returns `{code, url, expires_at}`, where `url` is `http://127.0.0.1:<port>/pair#code=…&next=…`. |
| `POST /api/v0/origins` | Local only | Body `{origin, scopes?}`. The origin is normalized (lowercase, default port dropped) and must be only `scheme://host[:port]`. Returns `{origin, token, scopes}`. The token is shown only once. |
| `DELETE /api/v0/pairing/sessions[?origin=…]` | Local only | Revokes every browser session, or only those bound to `origin` (normalized like `POST /api/v0/origins`). Returns `{origin?, revoked, terminals_closed}`: how many sessions were revoked and how many open terminals they held were closed with `4401`. Revoking none is not an error. Any other query parameter gets `400 invalid_request`. |
| `GET /api/v0/origins`, `DELETE /api/v0/origins?origin=…` | Local only | Lists registrations as `{origins: [{origin, scopes, created_at}]}`, or revokes one, invalidates its unused tickets and closes its terminals with `4401`, then returns `{origin, revoked: true}`. Tokens are never listed. |
| `POST /api/v0/pairing/exchange` | Browser | Body `{code, next?}` from the listener's own origin. Returns `{token, next}` (see Pairing). |
| `GET /pair` | Browser | The pairing page (see Pairing). Sets nothing and consumes nothing. |
| `GET /*` | Browser, Tailnet | The static UI directory from `--ui DIR`, which must contain `index.html`, with the SPA fallback to `index.html` for any path that is not a file. Paths under `/api/` never fall back. Files are served only from inside `DIR`: a symlink that leads outside it is not followed. Every static response carries `Content-Security-Policy: frame-ancestors 'self'` and `X-Frame-Options: SAMEORIGIN`, so another site cannot frame the UI. Without `--ui`, a small plain page explains how to pair and where the UI lives. |

Catalog rows optionally include `path`: the owning workspace path, falling back to its project root. This is presentation metadata on the row's `owner_host_id`; remote hub remapping preserves the owner's real path verbatim, never replacing it with a scoped key. It grants no attachment authority. Older producers may omit it, and clients accept rows with or without it. The same field appears in HTTP Sessions, event catalogs, CLI catalogs and mobile protocol catalogs.

## Project content and viewer layouts

All three listeners serve these resources. `{project}` is an exact, unique configured project name (URL-encoded). There is no current-project default. Reads use `contentservice` and return its existing CLI DTOs with camelCase content metadata intact. Optional `workspace` names a durable shell/worktree ID belonging to that project; omission reads the configured project root. Worktree roots are resolved from current registered Git membership. Remote hub-scoped IDs are refused rather than read as local paths; remote content forwarding is not added in this lane.

| Route | Returns |
| --- | --- |
| `GET /api/v0/projects/{project}/content` | `contentservice.ReadResult`. Required `kind`: `file`, `issue`, `note`, or `diff`. `target` names a relative file, issue/note ID or diff spec. Markdown uses `kind=file` and is rendered by the client. Optional `operation` defaults to `document`, `card`, `note`, or `working-tree` respectively. Diff operations also accept `path`, `parent`, nonnegative `offset` and `limit`. `if_revision` returns the ordinary `notModified` DTO on a matching revision. |
| `GET /api/v0/projects/{project}/tree` | `contentservice.TreeResult`. Repeat `path` for expanded directories (at most 256); omission lists the root. Each directory has entries, truncation or a directory error, using the existing DTO. |
| `GET /api/v0/projects/{project}/layout` | `{layout: PaneLayoutJSON\|null}` plus a quoted `ETag`. An absent saved layout is null. Matching `If-None-Match` returns 304. |
| `PUT /api/v0/projects/{project}/layout` | The saved layout and new `ETag`. Requires the exact ETag from GET in `If-Match`: missing is 428 `precondition_required`, stale or wildcard is 412 `precondition_failed`. Re-read and reconcile before retrying. The ordinary JSON and mutation-header guards apply. |

Content and layout routes require `content:read`; `full` implies it. Unknown/repeated scalar queries get 400 `invalid_request`. Content JSON uses the same bounded encoder as the CLI (at most 768 KiB, with explicit truncation/oversize flags). Git patches are streamed into a bounded 768 KiB prefix with one byte of lookahead, and Git is stopped and reaped once that bound is exceeded. `diff.truncated` reports omitted patch bytes even when the JSON encoder needs no further clipping. File rows and patch statistics describe only the retained prefix; the total omitted size is unknown. Desktop diff readers share the same bounded core and show a truncation notice. Full-file diffs retain their existing 1 MiB per-file bound and report clipping explicitly in both `diff.truncated` and `fullFile.truncated`. When source content is clipped, `pageTotal` is a lower bound from retained content, rather than the full file line count. Core refusals retain `usage`, `unknown-kind`, `rejected` and `internal` codes, mapped to 400, 400, 403 and 503 respectively. `scope_refused` is 403. No path may be absolute, home-relative, contain a `..` component, or escape its resolved workspace through a symlink. Root-handle file and directory reads retain that boundary during concurrent symlink replacement. Resolved API paths open without following newly substituted links at any component, so an ordinary path cannot be redirected into Git metadata after its policy check. Bare repositories whose project root is the Git administrative directory refuse file and tree reads. Content previews and diffs keep the existing size/paging limits. No file, issue or note write route is added.

API content reads, diffs, tree expansion and content subscriptions refuse Git administrative content with `403 rejected` (subscriptions close with `4400`). This includes `.git` at any path component, a worktree's resolved Git directory and common Git directory, and in-root symlink aliases to them. Project tree listings omit those entries. Aggregate working-tree, range and commit responses exclude protected paths before Git emits patches or statistics and before untracked-file readers inspect or open them. Git config may contain remote URLs with embedded credentials, so this protection applies by default even with `content:read` or `full`. Ordinary project files such as `.env` remain readable and watchable: users may need to inspect them as project content, and granting content access grants access to those files. Desktop file browsing retains its existing behavior. Git patches may include tracked symlink target strings; they do not dereference those links into administrative content. Aggregate subscriptions omit protected alias content watches while retaining Git invalidation. Diff invalidation may internally watch Git administrative files without exposing their contents.

The layout store lives under `$STATE/api/layouts/`, with private directories, mode-0600 JSON and lock files, cross-process read/compare/write locking and atomic replacement. Authentication selects the viewer: one browser session, one paired origin (stable through token rotation), one Tailnet login, or the trusted Local caller. No caller-supplied viewer parameter is accepted. Layout preferences grant no terminal or file authority. Each viewer and project has its own file; the TUI's state remains separate. The store uses the shared `panecodec`/`panelayout` tree vocabulary: `split: {axis: "cols"|"rows", ratio: 15..85, a, b}`, leaves `terminal`, `shell`, `doc`, `issue`, `note`, `diff`, `resource`, and their existing tab arrays. Multiple terminal/shell leaves remain distinct. There is no cell geometry, host/source restore metadata or legacy issue leaf. Bounds are 16 split levels, 127 nodes, 64 tabs per content leaf and a 64 KiB request body. `{layout:null}` clears the preference with the same conditional-write rule. Invalid trees return 400 `invalid_request`; storage failures return 503 `internal`.

Open content panes subscribe on the events URL using repeated `content` parameters, each a URL-encoded JSON `ContentRef`: `{project, workspace?, kind, target?, operation?, path?}`. `kind` also accepts `tree`, whose target is its expanded directory. Up to 32 refs are allowed per socket; each encoded ref is at most 8192 bytes. Each ref owns its own watcher, and a remote credential may hold at most 64 directory registrations across all of its events streams (a file registers its parent directory); a subscription beyond that closes with 4429 and its reason. The bound exists because kqueue spends a descriptor on every entry of a watched directory. For example, `content={"project":"sidecar","kind":"file","target":"README.md"}` watches that document. Reconnect with a new set when panes open, close or navigate, then refetch the open panes to establish a fresh baseline. Watchers are registered before hello, stopped on disconnect/revocation/shutdown, and never attached for closed panes. A content-only origin receives hello and its terminal baseline, but no catalog or attention data.

File watches cover the path and its resolved in-root symlink target; an absent in-root file remains watchable for creation, while a missing child beneath an escaping symlink is refused by a rooted stat; tree watches cover requested directories; issue/note watches reuse the td store-directory targets (including WAL writes). Diff watches cover resolved Git administrative paths and the files already under review, at most 64 directories. Like the desktop diff watcher, they do not recursively monitor every previously clean directory: the first edit there is noticed after a Git operation or a manual refetch. Signals coalesce and pending content batches contain only the latest set of invalidated subscribed refs. There is no content polling clock; a slow 30-second registration check only reattaches known watch targets after directory replacement. Refetch through the matching content/tree route, using revisions if available. Immutable fixture content uses the recorded `content-*.json` DTOs and never falls through to real host content or td.

## Terminal stream

`GET /api/v0/terminal` upgrades to a WebSocket. Each connection is one terminal protocol stream: the [mobile terminal protocol](mobile-protocol.md), with unchanged v0 behavior and capability-negotiated v1 presence, reset-free/coalesced frames, holder labels and server-side paste. Every rule in that document applies, including `hello` first, `operation_sequence`, heartbeats, the geometry lease, `history`, `reconnect`, and the refusal of `sessions` while an attachment is open (use `GET /api/v0/sessions` instead).

- Each WebSocket **text** message carries exactly one protocol JSON envelope, with no trailing newline. The 8 MiB line bound applies per message. A binary message, an empty message, or one that contains a CR or LF closes the connection with code `4400`. A message over the bound closes with `1009`.
- Closing the socket is end-of-stream. The server releases that stream's lease exactly as it does on stdin EOF.
- The server pings every 30 seconds. A peer whose pong does not arrive within 15 seconds is dropped, which is end-of-stream too, so a half-open socket (a laptop that slept, a proxy that lost the peer) cannot hold a terminal or its lease. Browsers answer pings automatically; other clients must keep reading. The server reads a pong only while it is reading the socket, and the protocol service handles requests one at a time, so while the server's own inbound side is blocked handing a request to a busy service it skips the ping and excuses a missed pong. That exemption lasts at most one minute since connection start or the last successful pong, checked on each ping interval. The server then closes the socket, breaks the blocked request write, and cancels the backend, so a peer that disappears during a permanent service stall cannot retain a terminal indefinitely.
- The `Host` guard answers `421` before the upgrade. Every other refusal happens after the upgrade, as a close code, because a browser cannot read the status of a failed handshake.
- Close codes:
  - `1000`: the protocol stream ended normally.
  - `4400`: protocol violation. This includes a stream the service ends right after an `invalid_request`, `protocol_mismatch` or `handshake_required` error, which is delivered before the close.
  - `4401`: unauthenticated: no usable ticket, bearer token or tailnet login, or a used or expired ticket. An open terminal also closes with `4401` when the browser session it was opened with is revoked.
  - `4403`: origin refused: no `Origin` without a bearer token (a ticket alone needs its `Origin`), an origin that is not allowed, or a ticket or token used from another origin.
  - `4409`: the server is shutting down.
  - `4429`: too many terminals: this client already holds 16 open terminal WebSockets (the WebSocket form of `too_many_outstanding`). Close one and retry.
  - `1011`: internal error.

  The close reason is one human sentence of at most 123 bytes.
- When remote hosts are configured, the stream is brokered to the owning host exactly as `sidecar mobile serve --stdio` does. When they are not, it is served by the local owner service in-process.

Unnegotiated v0 inherits the mobile service's bounded outbound queue. Clients that negotiate `coalesced_frames` receive latest-wins full frames while control/reset ordering remains intact. The terminal WebSocket offers `permessage-deflate` with context takeover; clients opt into compression in the WebSocket handshake. See [negotiated terminal v1](mobile-protocol.md#negotiated-terminal-v1) for the exact hello, presence, holder, frame flags and paste contract.

## Events stream

Credential revocation closes event sockets with `4401`, including sockets opened through a ticket or with a browser session that has since been evicted from the auth store. Revocation interrupts the writer independently of queued events and stalled data writes. If the peer has stopped reading, delivery of the close frame is best-effort and the socket closes within the five-second control-write deadline. Admission rechecks the credential under the same lock as revocation, so an already-authorized upgrade cannot connect after sign-out. `terminals_closed` in a session-revocation response counts terminal sockets only; event sockets close too.

`GET /api/v0/events` upgrades to a server-to-client WebSocket on every listener. Authentication, Host and Origin guards, tickets, keepalive and close codes are the same as the terminal stream. A browser spends a fresh ticket for this socket; tickets remain single-use across both stream routes. A non-browser client can send a bearer token without Origin. Tailnet upgrades still require the allowed Origin and login. This route accepts only GET. After authentication, an invalid query closes with `4400`. Client data messages are refused with `4400`; clients only read and answer WebSocket pings.

Every text frame is one `uiapi.EventMessage` JSON object with `api_version: 0`, `type`, and `seq`. `hello` is first at sequence 1. Sequence increases contiguously on delivery within this connection and starts again on reconnect. There is no replay cursor: reconnect establishes a fresh catalog and terminal baseline, without replaying old attention.

| Type | Payload | When |
| --- | --- | --- |
| `hello` | `api_instance`, `server_version`, `capabilities: ["catalog", "attention", "terminals", "content", "shutdown"]` | First message. |
| `catalog` | `catalog: CatalogSnapshot` | Once on connection, then when the full authorized catalog generation changes. Its `query` and rows use the same path as `sessions`. |
| `attention` | `attention: {kind: "needs_input"\|"finished", catalog_id, title, time}` | A previously observed live row gains attention, or changes from `working` to `done`. `time` is the server's UTC observation time; `title` is the human session name. Initial, newly appearing and stale rows do not replay alerts. Alerts use the connection's catalog query. |
| `terminals` | `terminals: [{client_id, owner_host_id?, session, pane, display_name?, holder: {kind, label}\|null}]` | Initial baseline, attachment open/close/disconnect, or an observed holder change. Empty is `[]`. Only attachments belonging to the same credential holder are included; Local connections share the trusted local credential. |
| `content` | `content: {resources: [ContentRef]}` | One or more open panes need refetching after a `livewatch` path signal; only refs subscribed on this socket appear. |
| `error` | `error: {code, message}` | Catalog collection failure (`backend`), or attention pending-bound overflow (`overflow`). The connection stays open; clients can reconcile through `sessions` and reconnect. |
| `shutdown` | `reason` | Before orderly close `4409` when the server stops. Delivery to an unresponsive peer is best-effort, bounded by the socket write deadline. |

Negotiated terminal holder events supply `tui`, `browser`, `ios`, `cli`, or `unknown` kinds and the viewer label, as specified in [mobile-protocol.md](mobile-protocol.md#negotiated-terminal-v1). Events project that observed holder without exposing a lease token, control epoch, attachment handle or instance id; an empty holder becomes null. For legacy terminal streams without holder negotiation, the read-only holder adapter supplies `tui` (for example `TUI on aerie`), `api` (`API terminal`), or `mobile` (`Mobile app`), and a missing or unreadable holder is null.

One in-process hostserve observer supplies local invalidations, reusing its shells.json watchers and the same agent activity/status collector desktop Sessions uses. Its observation-only mode never reaps shell records or registers a viewer inherited from the launching shell. Remote changes come from the existing host registry's snapshot/diff updates, including health and incarnation changes. Observation clocks and terminal preview bytes do not invalidate the catalog. Configuration changes refresh the observer's project set. Catalog invalidations coalesce over 250 ms; unchanged generations are never resent. Agent transitions retain hostserve's adaptive 5/10/30-second status cadence, and worktree inventory and degraded watches retain its 60-second reconciliation. There is no fast full-catalog polling loop. Holder observation checks only open attachment leases once per second and after attachment changes; it never captures terminal output or collects a catalog.

Legacy observations are shared across all event streams and credentials by owning host and session, including concurrent reads and null results, and cached for one second after each read completes. The cache holds at most 1024 session observations and permits at most four concurrent adapter reads, each with a two-second deadline. At either bound, a stream retains the last observation (or null until its first successful observation) and checks again on its next refresh; fresh and in-flight entries are never evicted to admit another read. A disconnect cancels only that stream's wait; the shared read lives until completion, its deadline, or API shutdown. Negotiated holder messages remain authoritative and bypass the legacy cache.

Each connection has bounded pending state: latest catalog, latest terminal snapshot, one error, and at most 4,096 attention keys (catalog row id plus attention kind), with a 1 MiB total encoded attention bound. Repeated alerts for a key retain the latest observation. Catalog and terminal snapshots coalesce while a socket is slow; they do not abort on queue overflow. A changing row population that exceeds the attention bound emits an explicit `overflow` error, never silently discards an alert. Sequence is assigned at delivery, so coalescing creates no sequence gaps. A peer whose network write exceeds 15 seconds is disconnected.

`sidecar api events --stdio` bridges this exact Local event stream to JSONL for agents and native SSH clients. The API service must already be running on the SSH target. Run it without a PTY; ending the SSH exec or closing stdout ends only this event connection. Terminal SSH streams remain separate.

The typed transcript is `testdata/ui-api/v0/events-stream.jsonl`; it is checked for event ordering and included in the SHA-256 fixture manifest.

## CLI

| Command | Does |
| --- | --- |
| `sidecar api serve [--port N] [--ui DIR] [--fixtures DIR] [--tailnet] [--tailnet-port N] [--json]` | Runs the server in the foreground until SIGINT or SIGTERM. `--json` writes the endpoint object as one line once every listener is bound. |
| `sidecar api open [--print] [--path P]` | Pairs this machine's browser and opens the UI, or prints the `/pair#code=…` URL. |
| `sidecar api pair --origin URL [--scopes LIST]` / `--list` / `--revoke URL` | Manages paired origins. `--json` gives structured output. |
| `sidecar api events --stdio [--sort MODE] [--search TEXT] [--host ID] [--provider ID] [--state STATE] [--show-idle-sessions true\|false]` | Streams events as JSONL over the Local socket until disconnect or shutdown. Repeat host, provider and state filters. |
| `sidecar api pair --revoke-sessions [--origin URL] [--json]` | Signs out browser sessions from `sidecar api open`, all of them or one origin's, and closes their terminals, without restarting the server. |
| `sidecar api service install\|uninstall\|status [--json]` | Manages or inspects the per-user background service (see below). |
| `sidecar api status [--json]` | Reads the status route over the Local socket. Exits non-zero with a clear message when no server is running. |

`sidecar api spec [--json]` prints the OpenAPI 3.1 document without a running server. Both forms print JSON.

## Schemas and fixture development

[ui-api.openapi.json](ui-api.openapi.json) is generated from the Go HTTP wire types, `uiapi.EventMessage` and `mobileproto.Request`/`Response`. Components use JSON Schema 2020-12; the terminal and events WebSockets are described under `x-streams`. The events upgrade reuses the Sessions query parameters and stream authentication parameters; it is GET-only. The generator uses `invopop/jsonschema` v0.13.0 because it reflects the same JSON tags used by `encoding/json` and supports the OpenAPI 3.1 schema dialect. Schema objects describe serialization; operation-specific field requirements, bounds, and ordering remain in this reference and [mobile-protocol.md](mobile-protocol.md). The route/method inventory and all schema references are checked, and a test fails if the committed document is stale. Regenerate the spec, fixture examples, checksums and CLI reference together with `./scripts/update-ui-api-contract.sh`. Staleness failures point to this same command.

Security alternatives describe only the listeners that serve an operation. Remote-only ticket issuance always requires credentials. The combined document uses `x-listeners` and `x-local-auth` to distinguish the Local socket's credential-free access from authenticated Browser and Tailnet requests on shared resources; clients must honor those listener annotations.

The CLI and HTTP catalog remain `mobileproto.CatalogSnapshot`. Status remains `uiapi.Status`; origin registration/list/revocation use the same named types on the CLI and HTTP. `CatalogRow.path` is optional in the generated schema. HTTP hello and pairing request bodies now also have named Go wire types instead of anonymous maps/structs.

`testdata/ui-api/v0/` contains synthetic HTTP hello, sessions, status, error, pairing, terminal and event examples with `SHA256SUMS`. Tokens, handles, paths and terminal text are synthetic. The terminal transcript is generated through the real mobile service with handles normalized. SDK tests can read the corpus directly. The same `./scripts/update-ui-api-contract.sh` command updates the resource inputs, records the real-Service terminal transcript, refreshes the checksums and verifies the corpus. Every JSON/JSONL corpus file is included in the manifest, including stream transcripts added by later lanes.

Fixture development requires `SIDECAR_ISOLATED_STATE=1` and temporary state/config paths. Startup refuses paths inside the real Sidecar state/config trees, including symlink aliases and API authority files linked into those trees, before it reads config or writes discovery. For an automatically cleaned proof run `./scripts/ui-api-fixture-proof.sh`. To develop a UI against a foreground fixture server:

```sh
fixture_root=$(mktemp -d /tmp/sc-fixtures.XXXXXX)
mkdir -p "$fixture_root/state" "$fixture_root/tmux"
printf '{}\n' > "$fixture_root/config.json"
unset TMUX TMUX_PANE
SIDECAR_ISOLATED_STATE=1 XDG_STATE_HOME="$fixture_root/state" TMUX_TMPDIR="$fixture_root/tmux" sidecar -config "$fixture_root/config.json" api serve --fixtures testdata/ui-api/v0 --port 0 --ui DIR
# After stopping the server, remove only this temporary tree.
rm -rf "$fixture_root"
```

`sessions.json` must contain an unfiltered Project-order catalog; queries use the real shared sorting/filtering/grouping functions. Ready rows use the deterministic synthetic identity issued by `mobile.FixtureIdentity`. `status.json` supplies recorded metadata, while listener addresses and connected clients/attachments describe the actual running fixture server. Pairing codes, tokens, tickets and browser sessions are always issued by the real server; fixture examples are never usable credentials. All real listener authentication, Host/Origin guards, mutation guards, limits, static routing and shutdown behavior apply.

Terminals run the real mobile protocol service against a capture/geometry adapter that echoes input bytes as normalized frames. Fixture panes are shared across connections to the same target and begin with an 80×24 grid and supports real request validation, control, input, resize/reset ordering, heartbeat, release and reconnect. Capability negotiation additionally enables presence arbitration through the shared lease policy, holder-label events, reset-free/coalesced frames, view-only input takeover and server paste. Server-paste bytes use the shared marker-removal and CRLF-normalization policy in [mobile-protocol.md](mobile-protocol.md#server-side-paste), including marker-only refusal; ordinary input bytes echo unchanged. No application is launched to interpret bracketed paste, and tmux's final newline translation and paste boundaries are absent from fixture output. It launches no shell and executes no commands. History is explicitly unavailable in this first echo adapter; it never falls through to tmux. Fixture mode is opt-in and does not replace the real backend unless `--fixtures DIR` is supplied.

### Per-user background service

`sidecar api service install|uninstall|status [--json]` uses one service-manager adapter: a launchd LaunchAgent on macOS, a systemd user unit on Linux. `install` writes a private definition, loads/enables it and starts it at login; repeating install unloads only that API job before replacing its definition. `uninstall` stops that job and removes its definition, preserving Sidecar state, paired origins and tmux. It is safe to repeat uninstall. No service command starts, stops or restarts tmux. Run as the login user without sudo. Linux needs a running systemd user manager; this command does not enable lingering or configure system services.

The macOS label is `com.haplab.sidecar.api`, in `~/Library/LaunchAgents/com.haplab.sidecar.api.plist`; the Linux unit is `sidecar-api.service`, in `$XDG_CONFIG_HOME/systemd/user/` (default `~/.config/systemd/user/`). macOS stdout/stderr go to `$STATE/api/service.log`; Linux logs go to `journalctl --user -u sidecar-api.service`. Manager failures name the failed operation and where to inspect logs. A definition is retained if unloading fails, so a retry can recover it. An unrelated foreground API server causes install to refuse with its PID and instructions to stop that API process first; it is never killed by install. The refusal checks the held server lock even when discovery is missing, stale or unreadable (including a server still starting).

The definition records the current absolute `-config` path, state root and PATH, without inheriting tmux, agent identity or secrets. PATH keeps only absolute, existing directories owned by root or the user and not world-writable, so no other local user can plant a `tmux`, `git` or agent binary the service would run (empty, relative, missing, world-writable or foreign-owned entries are dropped; if none remain the job uses `/usr/bin:/bin:/usr/sbin:/sbin`). Its executable retains the launch/PATH symlink when that link names this exact binary. Use an installed stable `sidecar` link when installing, rather than a version-specific binary path. The service runs `sidecar -config PATH api serve`; the default Browser port remains 7861 and it does not enable Tailnet or change Tailscale configuration. The static UI directory comes from config `api.uiDir`. Both foreground and service starts read it; explicit `serve --ui DIR` overrides it (an empty value disables it). Prefer an absolute UI directory with `index.html`. Config changes take effect at the next server start; rerun install to restart with changed config.

The release pipeline renders a Homebrew `service` block from `packaging/homebrew/sidecar.rb.tmpl`. `brew services start sidecar` uses the same labels and `sidecar api serve` command, following the Homebrew-prefix `bin/sidecar` link that `make install-local` and `make install-worktree` activate. Choose either `brew services` or `api service` as the manager of that job; switching managers means stopping/uninstalling the old one first. Homebrew services use the default Sidecar config/state paths.

Every `serve` watches the original stable executable path once per second. An atomic binary replacement, symlink retarget, or changed executable size/mtime logs the replacement and shuts down normally: WebSockets close, attachments release, listeners and discovery files are removed, then launchd's KeepAlive or systemd's Restart=always launches the new binary. A briefly missing path during upgrade is ignored until a replacement exists. A foreground `serve` also exits cleanly on replacement; its caller must restart it. No restart changes tmux. Browser session tokens are still in-memory v0 state, so a restarted server requires browser pairing again; persisted paired-origin tokens survive.

`status --json` reports the manager's job, including when it is absent or stopped (exit 0):

```json
{"manager":"launchd","label":"com.haplab.sidecar.api","file":"/…/Library/LaunchAgents/com.haplab.sidecar.api.plist","installed":true,"loaded":true,"running":true,"pid":4242,"version":"v1.16.0","last_exit":{"code":0},"log":"/…/api/service.log","message":"API service is running; open it with `sidecar api open`."}
```

`installed` means the definition exists, `loaded` means the manager reports it loaded, and `running` means the manager reports a running process with a positive PID. A stopped job has PID 0. `version` is the running server version read through the Local API only when its PID matches the manager; it is an empty string when unknown, stopped, or still starting. `last_exit` is null when the manager has no termination record, otherwise `{code, signal?}`; a signal termination carries `code: 0` and the manager's signal name or number. A healthy current process can retain a previous failed exit record. Install/uninstall with `--json` return the same status shape after the operation. Manager errors exit 1 with an actionable stderr message, usage errors exit 2. Installation can return a loaded job before its asynchronous startup finishes; use `api status` to verify API readiness.

Service-manager access is refused under `SIDECAR_ISOLATED_STATE=1`. Tests inject fake managers; proofs run foreground servers on port 0.

## Proofs

Live proofs follow the `scripts/tmux-drive.sh` isolation rules: a private tmux socket, `unset TMUX TMUX_PANE`, an isolated `XDG_STATE_HOME`, a `-config` temp path, and `SIDECAR_ISOLATED_STATE=1`. The Unix sockets and `endpoint.json` live under the isolated state tree, so a proof can never reach the user's real server. Unix socket paths are limited to 103 bytes, so a proof keeps its state tree short, under `/tmp`.

`scripts/ui-api-proof.sh` is the v0 proof. It builds a temporary binary, creates one managed shell on a private tmux server, runs `sidecar api serve`, and checks the Local routes with `curl --unix-socket`, the Browser guards, `sidecar api open` pairing, origin pairing with a ticket, and terminal round trips over the WebSocket through `internal/tools/uiapiproof`. It also runs `internal/tools/uieventsproof`: a paired-origin events ticket, hello and catalog ordering, a CLI shell rename pushed through the manifest watch, attachment and holder changes during terminal proofs, and shutdown before close 4409. The Local JSONL bridge is checked separately. `internal/tools/uicontentproof` additionally checks live file/diff DTOs, oversized working-tree/range/commit/full-file diff truncation, Git administrative content refusal and ordinary `.env` reads, tree reads, traversal and symlink refusals, layout ETags and stale-write refusal, and an open document edit pushed as a content event followed by a fresh read. `TestAPITerminalRoundTripAgainstLocalOwner` in `internal/cli` covers the same terminal sequence in process.

`scripts/ui-api-fixture-proof.sh` proves the real CLI spec, fixture server, catalog/status, origin pairing/ticket and legacy and negotiated v1 terminal echo journeys with isolated state/config, a private tmux namespace, port 0, a bounded client and a failing tmux shim. It checks that no tmux command ran and that shutdown removed discovery/socket files.

`scripts/workspace-operations-proof.sh` proves U2-a through the real TUI and CLI: project shell creation, CLI rename visible in Workspaces and Sessions, Sessions shell creation, shell deletion and tombstone restore, and planned worktree creation and identity-pinned deletion. It builds a temporary binary and uses `tmux-drive.sh` to isolate both servers and state and clean up on exit. Set `WORKSPACE_PROOF_OUTPUT` to a directory to retain the captured text and PNGs. Pure-core regression tests cover interrupted creation, required-failure journal retention, readiness failures, and the stale-refresh fence after service deletion.

`scripts/ui-api-service-proof.sh` covers fake launchd/systemd and CLI lifecycles, config-driven UI serving, explicit UI override, replacement of the stable launch link, clean exit/discovery cleanup, and restart against the same isolated state tree. It makes no service-manager or tmux changes.

`scripts/ui-api-measure.sh` uses the same isolation to measure the terminal stream under agent-like load: frames, wire bytes, captures, CPU and keystroke-to-echo latency, through `internal/tools/uiapimeasure`. Results are in [U0 measurements](../plans/active/sidecar-ui-api/u0-measurements.md).

To extend the contract, add the named Go request/response or stream envelope to `Spec` in `internal/uiapi/spec.go`, describe its route or `x-streams` entry there, and add typed corpus examples/tests. Then run `./scripts/update-ui-api-contract.sh`. Later stream lanes own their envelope/transcript; this command includes their committed JSON/JSONL transcript in `SHA256SUMS`.
