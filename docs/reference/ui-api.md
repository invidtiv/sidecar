# Sidecar UI API v0

`sidecar api serve` exposes Sidecar's Sessions and terminals to UIs that are not the TUI: the reference web UI in `~/code/sidecar-ui`, embedded components in other apps, and agents and scripts. This document is the wire contract. The plan and the reasoning behind it are in [the Sidecar UI API plan](../plans/active/sidecar-ui-api.md).

v0 is the U0 steel thread. It serves the Sessions catalog and the existing terminal protocol over HTTP and WebSocket, with the transports, guards and pairing that every later version keeps. Later versions add the events stream, workspaces, operations and content. v0 changes in place while the API is private, and fixtures and clients move with it.

## Process and discovery

`sidecar api serve` is one long-running, headless process per user. It never starts, stops or restarts the tmux server. It holds one remote-host registry and catalog router for its lifetime, and gives each terminal connection its own protocol broker, which is the same per-stream model `sidecar mobile serve --stdio` uses.

On start it writes `$STATE/api/endpoint.json` with mode 0600. `$STATE` is `config.StateDir()`. It removes the file on clean exit (SIGINT or SIGTERM). A second `serve` refuses to start while the first is running: the server holds an exclusive lock on `$STATE/api/serve.lock` for its lifetime, the kernel drops the lock if the process dies, and the refusal names the PID recorded in `endpoint.json`. A client that finds an `endpoint.json` whose PID is not alive treats it as no server.

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
- **Origin:** a WebSocket upgrade must carry an allowed `Origin`, and so must any request that is not `GET` or `HEAD`, with one exception on the Browser listener: a request or upgrade that carries `Authorization: Bearer` may omit `Origin` (see Authentication). Allowed origins are the listener's own origins plus paired origins. A listener's own origins are `http://127.0.0.1:<port>` and `http://localhost:<port>` for Browser, and `https://<magicdns>` and `http://<magicdns>` for Tailnet. Any request that carries an `Origin` outside that set, including a `GET`, gets `403 origin_refused`. A `GET` or `HEAD` without an `Origin` goes on to authentication.
- **Mutations:** a non-GET request must be `Content-Type: application/json` and must carry `X-Sidecar-Request: 1`. A browser cannot send either cross-site without a CORS preflight, and the server refuses unpaired preflights.
- **CORS:** only paired origins get CORS headers: the exact `Access-Control-Allow-Origin` with `Vary: Origin`, and on a preflight `Access-Control-Allow-Methods: GET, POST, DELETE`, allowed headers `Authorization, Content-Type, X-Sidecar-Request`, and `Access-Control-Max-Age: 600`. There are no credentials. A preflight from any other origin gets `403 origin_refused`.

### Authentication on each listener

- **Local:** every request is trusted.
- **Browser:** `/api/` routes need `Authorization: Bearer <token>`, where the token is either a browser session token (see Pairing) or a paired origin's token. Nothing ambient travels with a request: every client, same-origin, paired or non-browser, takes this one path. Each token is bound to one origin. A session token is bound to the exact origin that exchanged its pairing code (for example `http://127.0.0.1:7861`, not also `http://localhost:7861`), and a paired origin's token to that origin. A request that carries any other `Origin` gets `403 origin_refused`. A request with no `Origin` is accepted with a valid bearer token, for any method and on the terminal upgrade, because the Origin guard exists to stop a page from using a credential the browser attaches by itself, and a bearer token is never attached by itself. That covers a same-origin `GET`, curl, Node (whose `WebSocket` sends no `Origin`) and native clients. Without a bearer token, a mutation or upgrade with no `Origin` is still refused, and so is a ticket presented with no `Origin`, because a ticket is bound to its origin. The Tailnet listener keeps requiring `Origin`: its login header is added by `tailscale serve` to every request from the device, so it is ambient. Static UI files, `GET /pair` and `POST /api/v0/pairing/exchange` need no credential. An API route without one gets `401 unauthenticated`.
- **Tailnet:** every route, static files included, needs a `Tailscale-User-Login` header that names an allowed login. A missing login gets `401 unauthenticated`, and a login that is not allowed gets `403 tailnet_login_refused`. Bearer tokens are not accepted here, and the header is ignored on every other listener.

### Per-client limits

A client is one credential holder: a browser session, a paired origin, or a tailnet login. Each may hold at most 16 open terminal WebSockets (the 17th closes with `4429` and a reason) and 16 unredeemed tickets (the 17th request gets `429 too_many_outstanding`). Redeeming or expiring a ticket frees its slot. Across all clients there are at most 256 unredeemed tickets and 64 unused pairing codes. Local callers are trusted like the tmux socket and have no terminal limit.

## Pairing

### Same-origin browser (the UI served by `serve`)

1. `sidecar api open` asks the server for a one-time code over the Local socket (`POST /api/v0/pairing/codes`). The code is single-use and expires after 60 seconds.
2. `api open` then opens `http://127.0.0.1:<port>/pair#code=<code>&next=<path>` in the default browser. `--print` prints the URL instead. The code is in the fragment, which a browser never sends, so it never appears in a request line, a proxy log or a `Referer`.
3. `GET /pair` serves a small built-in page. Loading it (`GET` or `HEAD`) reads nothing from the request and consumes nothing. The page is locked down: `Content-Security-Policy` allows only its own hashed script and same-origin `fetch`, and forbids framing; `Cache-Control: no-store`; `Referrer-Policy: no-referrer`.
4. The page's script reads `code` and `next` from the fragment, removes them from the address bar and history, and sends `POST /api/v0/pairing/exchange`.
5. The exchange returns a session token. The page stores it in `localStorage` under the key `sidecar.session` and navigates to `next`, after checking that `new URL(next, location.origin)` is still on this origin.

`localStorage` is scoped to scheme, host and port, so a page served on any other port of `127.0.0.1`, including another local user's, cannot read the token. A UI served by `serve` reads `localStorage.getItem("sidecar.session")` and sends it as `Authorization: Bearer` on HTTP calls, and spends it on a ticket for each terminal WebSocket.

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

### Another origin (an embedding app)

`sidecar api pair --origin https://app.example:5173` registers the origin over the Local socket (`POST /api/v0/origins`) and prints a bearer token. Registrations persist in `$STATE/api/origins.json` with mode 0600, as `{origin, token_sha256, scopes, created_at}`. The server keeps only the token hash. `sidecar api pair --list` and `--revoke ORIGIN` manage registrations.

The only v0 scope is `full`. Narrower scopes arrive with the routes they protect.

HTTP calls from a paired origin send `Authorization: Bearer <token>`. Pairing an origin again rotates its token. Browsers cannot set headers on a WebSocket, so a paired origin first gets a ticket with `POST /api/v0/ws-tickets` (bearer-authenticated, single-use, 30-second expiry). It then connects with `?ticket=<ticket>`. A ticket is bound to the listener and the origin it was issued to. The same-origin UI does the same with its session token. A non-browser client may instead send `Authorization: Bearer <token>` on the upgrade itself. It either sends no `Origin` or sends the origin that token is bound to (the paired origin, or for a session token the origin that exchanged it); any other `Origin` closes with `4403`.

## HTTP routes

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
| `too_many_outstanding` | 429 | Over a limit: 64 unused pairing codes, 256 unredeemed tickets, or 16 unredeemed tickets for this client. |
| `upgrade_required` | 426 | `GET /api/v0/terminal` without a WebSocket upgrade. |
| `backend` | 503 | The catalog failed. Other catalog refusals keep their protocol code, such as `overflow`. |

| Route | Listener | Returns |
| --- | --- | --- |
| `GET /api/v0/hello` | any | `{api_version, api_instance, server_version, capabilities: ["sessions", "status", "terminal", "ws_tickets"], terminal: {protocol: "mobile", version: 0}}` |
| `GET /api/v0/sessions` | any | The Sessions catalog: the same `mobileproto.CatalogSnapshot` JSON as `sidecar mobile sessions --json`. Query parameters map to `catalog_query`: `sort`, `search`, repeatable `host`, `provider`, `state`, and `show_idle_sessions=true\|false`. It is served by the same code path as the CLI. Any other parameter, or a repeated `sort`, `search` or `show_idle_sessions`, gets `400 invalid_request`. |
| `GET /api/v0/status` | any | `{api_version, api_instance, server_version, pid, started_at, listeners: [{name, network, address, host?}], clients: [{id, kind, listener, auth, origin?, login?, since}], terminals: [{client_id, owner_host_id?, workspace_id?, session?, pane?, display_name?, control}]}`. A client is one open terminal WebSocket, and `auth` is `local`, `session`, `bearer`, `ticket` or `tailnet`. `terminals` lists the clients with an open attachment, and `control` is observed from the stream's own responses. `sidecar api status --json` prints this document byte for byte. |
| `POST /api/v0/ws-tickets` | Browser, Tailnet | Body `{}` or empty. Returns `{ticket, expires_at}`. At most 16 unredeemed per client. |
| `POST /api/v0/pairing/codes` | Local only | Body `{next?}`, default `/`. Returns `{code, url, expires_at}`, where `url` is `http://127.0.0.1:<port>/pair#code=…&next=…`. |
| `POST /api/v0/origins` | Local only | Body `{origin, scopes?}`. The origin is normalized (lowercase, default port dropped) and must be only `scheme://host[:port]`. Returns `{origin, token, scopes}`. The token is shown only once. |
| `GET /api/v0/origins`, `DELETE /api/v0/origins?origin=…` | Local only | Lists registrations as `{origins: [{origin, scopes, created_at}]}`, or revokes one and returns `{origin, revoked: true}`. Tokens are never listed. |
| `POST /api/v0/pairing/exchange` | Browser | Body `{code, next?}` from the listener's own origin. Returns `{token, next}` (see Pairing). |
| `GET /pair` | Browser | The pairing page (see Pairing). Sets nothing and consumes nothing. |
| `GET /*` | Browser, Tailnet | The static UI directory from `--ui DIR`, which must contain `index.html`, with the SPA fallback to `index.html` for any path that is not a file. Paths under `/api/` never fall back. Files are served only from inside `DIR`: a symlink that leads outside it is not followed. Every static response carries `Content-Security-Policy: frame-ancestors 'self'` and `X-Frame-Options: SAMEORIGIN`, so another site cannot frame the UI. Without `--ui`, a small plain page explains how to pair and where the UI lives. |

## Terminal stream

`GET /api/v0/terminal` upgrades to a WebSocket. Each connection is one terminal protocol stream: the [mobile terminal protocol v0](mobile-protocol.md), unchanged. Every rule in that document applies, including `hello` first, `operation_sequence`, heartbeats, the geometry lease, `history`, `reconnect`, and the refusal of `sessions` while an attachment is open (use `GET /api/v0/sessions` instead).

- Each WebSocket **text** message carries exactly one protocol JSON envelope, with no trailing newline. The 8 MiB line bound applies per message. A binary message, an empty message, or one that contains a CR or LF closes the connection with code `4400`. A message over the bound closes with `1009`.
- Closing the socket is end-of-stream. The server releases that stream's lease exactly as it does on stdin EOF.
- The server pings every 30 seconds. A peer whose pong does not arrive within 15 seconds is dropped, which is end-of-stream too, so a half-open socket (a laptop that slept, a proxy that lost the peer) cannot hold a terminal or its lease. Browsers answer pings automatically; other clients must keep reading.
- The `Host` guard answers `421` before the upgrade. Every other refusal happens after the upgrade, as a close code, because a browser cannot read the status of a failed handshake.
- Close codes:
  - `1000`: the protocol stream ended normally.
  - `4400`: protocol violation. This includes a stream the service ends right after an `invalid_request`, `protocol_mismatch` or `handshake_required` error, which is delivered before the close.
  - `4401`: unauthenticated: no usable ticket, bearer token or tailnet login, or a used or expired ticket.
  - `4403`: origin refused: no `Origin` without a bearer token (a ticket alone needs its `Origin`), an origin that is not allowed, or a ticket or token used from another origin.
  - `4409`: the server is shutting down.
  - `4429`: too many terminals: this client already holds 16 open terminal WebSockets (the WebSocket form of `too_many_outstanding`). Close one and retry.
  - `1011`: internal error.

  The close reason is one human sentence of at most 123 bytes.
- When remote hosts are configured, the stream is brokered to the owning host exactly as `sidecar mobile serve --stdio` does. When they are not, it is served by the local owner service in-process.

v0 inherits the mobile service's bounded outbound queue, so a peer that stops reading ends the stream. U0 measures this, and v1 coalesces frames before the queue.

## CLI

| Command | Does |
| --- | --- |
| `sidecar api serve [--port N] [--ui DIR] [--tailnet] [--tailnet-port N] [--json]` | Runs the server in the foreground until SIGINT or SIGTERM. `--json` writes the endpoint object as one line once every listener is bound. |
| `sidecar api open [--print] [--path P]` | Pairs this machine's browser and opens the UI, or prints the `/pair#code=…` URL. |
| `sidecar api pair --origin URL` / `--list` / `--revoke URL` | Manages paired origins. `--json` gives structured output. |
| `sidecar api status [--json]` | Reads the status route over the Local socket. Exits non-zero with a clear message when no server is running. |

`sidecar api spec` and `sidecar api service install|uninstall|status` arrive in U1.

## Proofs

Live proofs follow the `scripts/tmux-drive.sh` isolation rules: a private tmux socket, `unset TMUX TMUX_PANE`, an isolated `XDG_STATE_HOME`, a `-config` temp path, and `SIDECAR_ISOLATED_STATE=1`. The Unix sockets and `endpoint.json` live under the isolated state tree, so a proof can never reach the user's real server. Unix socket paths are limited to 103 bytes, so a proof keeps its state tree short, under `/tmp`.

`scripts/ui-api-proof.sh` is the v0 proof. It builds a temporary binary, creates one managed shell on a private tmux server, runs `sidecar api serve`, and checks the Local routes with `curl --unix-socket`, the Browser guards, `sidecar api open` pairing, origin pairing with a ticket, and one terminal round trip over the WebSocket through `internal/tools/uiapiproof`. `TestAPITerminalRoundTripAgainstLocalOwner` in `internal/cli` covers the same terminal sequence in process.
