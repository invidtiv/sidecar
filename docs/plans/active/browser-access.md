# Browser access without the CLI

**Status:** Phases A and B are implemented, independently security-reviewed and merged to main (Sidecar and `sidecar-ui`). Open follow-ups: device label from the pair page, approver device attribution (td-360913), optional device allowlist (td-1e5730), and the Phase B review's low findings. Phase C not started. **Epic:** td-14aabd. Phases: A td-ba7a17, B td-accec4, C td-938935.

Related: [Sidecar UI API](sidecar-ui-api.md) (the API, listeners and guards this plan extends), [UI API reference](../../reference/ui-api.md) (the current pairing protocol, which stays the authority for wire details until each phase updates it).

## Outcome

A person opens `sidecar-ui` in any browser that can reach their Sidecar, whether on the same machine, across a tailnet or across a home LAN, and gets in by following what the page says, without SSH or a terminal on the host. Access stays as hard to obtain as a shell on the host, because that is what it grants. No website, LAN neighbour or local process gets in by being nearby.

Three mechanisms, layered:

1. **Approval from a trusted surface (Phase A).** A browser with no credential asks for access and shows a short code. You approve it from anything already trusted: the Sidecar TUI, an already-signed-in browser, or `sidecar api approve CODE`. This works on every listener and is the general path.
2. **Tailscale identity (Phase B).** On a tailnet, Sidecar listens on the tailnet address itself and recognises the owner's devices by the identity Tailscale assigns each connection. They never see a pairing screen, and no `tailscale serve` setup is needed.
3. **LAN HTTPS (Phase C).** An opt-in listener on the LAN, served over HTTPS with a certificate Sidecar manages, so `https://<ip-or-host>:<port>` works without Tailscale. Access still comes through approval.

The 60-second `sidecar api open` link remains, demoted to a convenience for the host's own browser and a recovery path. It is no longer the instruction a new browser shows.

## Settled decisions

- **No plain HTTP off loopback, ever.** A plain-HTTP LAN origin sends terminal input and credentials in the clear and is not a secure context, so `crypto.subtle` (which the browser key relies on) does not exist there. There is no opt-in for it.
- **The host's own localhost browser is not trusted automatically.** Other local OS users and processes can reach loopback. The host's browser uses `sidecar api open` (one step, already paired) or approval like any other browser. The pairing page's copy must make this effortless rather than mysterious.
- **Approval binds to the requesting browser's key.** The new browser generates its non-extractable P-256 key first and sends the public half with its request. Approving creates a registration for that exact public key and origin in the existing session store. Nothing transferable (no bearer, no link) is ever handed to the new browser by the approver, so approving the wrong request cannot leak a credential to anyone but that request's key holder.
- **The approver types the code.** The new browser shows a six-character code (Crockford base32, displayed `K7Q-4MX`). The approver enters it: as the CLI argument, or in the approval field in the TUI or `sidecar-ui`. A list of pending requests (device label, address, age) is shown for context, but selecting a row alone never approves. With more than one pending, every approval surface says so plainly, since behind a proxy the requests can look identical. This defeats a LAN attacker who races a request in alongside yours.
- **Every capability has a CLI and API path.** Listing requests, approving, denying, listing devices and revoking one device are API routes on the Local socket with CLI verbs over them. The TUI and `sidecar-ui` are projections of those routes. The rules (request limits, code checks, who may approve) live in `internal/uiapi` state-free functions, not in either UI.
- **Rules stay in Sidecar, not in `sidecar-ui`.** `sidecar-ui` renders the states the SDK reports. The SDK owns the request/poll/proof sequence so any embedder gets it for free.
- **Independent security review gates every phase.** Auth bugs here are remote code execution. Each phase merges only after a fresh-context security review of its diff against this plan's threat model.

## Threat model

In scope: any website the owner visits (CSRF, DNS rebinding, cross-origin WebSocket); any device on the LAN or tailnet (unsolicited requests, request flooding, racing a legitimate request, passive sniffing); other local OS users (loopback access, port takeover while Sidecar is stopped, already documented in the reference). Out of scope: an attacker who already has a shell as the owner; a user who clicks through a certificate warning on a network an attacker controls (see Phase C's limit).

Tailscale identity (Phase B) identifies a device, not a person. Any process, container or VM on an untagged device of an allowed login can drive the API, and outside a browser it can send any `Origin`, so the Origin and mutation guards stop web pages, not software on that device. This is accepted, and it is the same trust model as the serve-mode Tailnet listener, where `tailscale serve` vouches for the same device with a header. A device allowlist is deferred; recording the approving device on Phase A approvals is a follow-up after both phases merge.

## Phase A: approve a new browser from a trusted surface (td-ba7a17)

Works on the existing Browser listener (localhost and `api.browserProxyOrigin`), and later on the LAN listener. It does not widen the Tailnet listener: a login that is not allowed there is still refused, though an allowed Tailnet login may act as an approver.

### Server (`internal/uiapi`)

- **Requests are process-local and short-lived**, like pairing codes: kept in `authStore`, expiring after 5 minutes, dropped on restart (the SDK re-requests). Limits: 16 pending in total, 2 per source address. Capacity never refuses a request: the oldest pending request in the full bucket is evicted (its poll reports `expired`), so a neighbour can race a request in but cannot lock everyone out; behind a proxy every request shares the proxy's address. Flooding new keys can still keep evicting other requests: a known denial-of-service limitation, never a way in. A request from the same public key and origin replaces that browser's earlier pending one, at most once a second (`429 too_many_outstanding`). A burst of requests coalesces into one notification.
- `POST /api/v0/pairing/requests`, no bearer, own-origin and mutation headers required (same guards as `/pairing/exchange`). Body `{public_key, label}`; `label` is a client-suggested device name ("Safari on macOS"), length-capped and control-stripped, shown to approvers as a claim, never as proof. Returns `{request_id, poll_secret, code, expires_at}`. The server records origin and source address itself.
- `GET /api/v0/pairing/requests/{id}` with `Authorization: Request <poll_secret>`: `{status: pending|approved|denied|expired, expires_at, registration_id?}`. On `approved` the browser runs the existing session-proof renewal for `registration_id`; its registration now exists.
- Approver routes, available to Local, browser sessions and allowed Tailnet logins (not paired origins): `GET /api/v0/pairing/requests` lists pending requests without their codes; `POST /api/v0/pairing/requests/approve {code, surface?}` approves the matching request (`surface`, `cli` or `tui`, is accepted only on Local; admission is re-checked after the body arrives, so a credential revoked mid-request changes nothing, and non-stream bodies must arrive within 10 seconds) (wrong codes count toward a limit of 5 per minute per approver, then `429`; the code of a request that just expired or was evicted gets `404 access_request_expired` and does not count); `POST /api/v0/pairing/requests/deny {request_id}`.
- **Devices.** Registrations gain `label`, `approved_via` (`link`, `cli`, `tui`, `browser:<registration>`, `tailnet:<login>`) and `approved_at` in `sessions.json` (bump the store version; migrate in place). `GET /api/v0/pairing/sessions` lists them for approvers; `DELETE /api/v0/pairing/sessions/{id}` revokes one, with the same stream-closing behaviour as the existing bulk revoke. Revoking a device does not cascade to devices it approved; `approved_via` shows the approver.
- **Signals.** A new request emits an `access_requested` event on the events stream and writes a Sidecar notification (source `access_request`, with no calls to action, hidden from paired origins) through the existing notification store, so the TUI, `sidecar-ui` and native delivery all show it with no new channel between processes. Approval, denial and expiry withdraw it.
- Update `docs/reference/ui-api.md`, the OpenAPI spec (`sidecar api spec`) and fixtures in the same change.

### CLI

`sidecar api requests [--json]`, `sidecar api approve CODE [--json]`, `sidecar api deny REQUEST_ID`, `sidecar api devices [--json]`, `sidecar api devices revoke ID`. All over the Local socket, with named refusal codes and good `--help`. Add them to `sidecar agents` and `docs/reference/cli.md`.

### SDK (`sidecar-ui/packages/client`, `browser-session.ts`)

A browser with no usable registration moves through explicit states the UI can render: `requesting` → `waiting {code, expires_at}` → `approved` (then the normal session) or `denied` / `expired` (offer a fresh request). It keeps the key in IndexedDB as today, renews the request automatically when it expires while the page is open, and survives a server restart by re-requesting. Approver helpers: `listAccessRequests`, `approveAccess(code)`, `denyAccess(id)`, `listDevices`, `revokeDevice(id)`.

### `sidecar-ui`

- Replace the `pair` welcome state with the waiting screen: the code, large and copyable; one sentence on how to approve ("Open Sidecar on **aerie** and approve this browser, or run `sidecar api approve K7Q-4MX` there"); the live status; and a secondary "On the computer running Sidecar? Run `sidecar api open`" line.
- For signed-in browsers: an access-request notification opens an approval dialog with the pending list and a code field. Preferences gets a **Devices** section listing registrations with label, origin, last used and Revoke.
- Follow `docs/reference/design-language.md` and the UI's existing Preferences and notification patterns.

### TUI

The `access_request` notification appears like any other. Activating it opens an approval modal (built with `internal/modal`) with the pending list and a code input that calls the Local socket's approve route. No other TUI surface.

### Acceptance evidence

- Go tests for request limits, expiry, code matching, wrong-code limits, approver authorization (paired origin refused), key binding (approval creates a registration only for the requesting key and origin), device revoke closing streams, store migration.
- SDK unit tests for the state sequence, including expiry renewal and restart.
- A Playwright journey in `sidecar-ui` against a real isolated `sidecar api serve`: browser B requests, browser A (signed in) approves by code, B reaches Sessions; then A revokes B and B returns to the waiting screen.
- CLI approval proof and a TUI approval proof via `scripts/tmux-drive.sh`, both fully isolated.
- Independent security review of the diff.

## Phase B: Tailscale identity as zero-click access (td-accec4)

**Phase status:** implemented and security-reviewed on its branch, pending merge. The direct listener, its admission rules and the setting `api.tailnetMode` are described in the [reference](../../reference/ui-api.md#direct-mode).

### What the probe established (aerie, 2026-10-08, Tailscale 1.102.4 standalone macOS build)

- `tailscale serve` cannot proxy to a Unix socket on this build in any location or mode: the Sidecar Tailnet socket, a 0600 or 0666 socket under `/private/tmp`, `/tmp` and the state directory all return `502`, while a TCP target on the same Serve port returns `200`. The existing socket-based Tailnet listener is therefore unusable with the standalone macOS app, which is why aerie proxies Serve to the Browser listener with `api.browserProxyOrigin`.
- A user process can bind the node's tailnet address directly (`100.89.245.23:<port>`), serve TLS with a certificate from `tailscale cert <magicdns>` (a real, publicly trusted certificate for the MagicDNS name), and see each peer's true tailnet address. `tailscale whois --json <peer-ip>` maps it to the owning login and device: a request from MarcusBook arrived from `100.117.87.108` and resolved to `marcus@vorwaller.net on marcusbook-pro`. A request from aerie itself arrives from aerie's own tailnet address.

### Design: a direct Tailnet listener

The Tailnet listener gains a `direct` mode, the default whenever Tailnet access is enabled. It needs no `tailscale serve` configuration, so enabling Tailnet access is the whole setup.

- **Bind** the node's tailnet IPv4 (and IPv6, if present) on `api.tailnetHTTPSPort` (default 7861). Discover the addresses from `tailscale status --json`; when Tailscale is not running or the address changes, retry and rebind with backoff and report the state in `sidecar api status`. If a `tailscale serve` route already holds that port, refuse with a message naming the exact `tailscale serve --https=<port> off` command.
- **TLS** from `tailscale cert`, written 0600 under `$STATE/api/tls/tailnet/` and renewed before expiry. The origin is `https://<magicdns>[:port]`, the same own origin the Tailnet listener guards today.
- **Identity per connection**, not per header: on accept, resolve the peer address with Tailscale's whois and cache the answer by address for a short time. Admit only an allowed login (`api.tailnetLogins`, defaulting to the node owner) on a device that is not tagged. Refuse a connection from any of the node's own tailnet addresses, so local processes keep using the Local socket and Browser listener. `Tailscale-User-Login` headers are ignored in this mode; the identity comes from WireGuard, which a page or a LAN device cannot forge.
- **Everything else matches the Tailnet listener as it exists:** Host and Origin guards, mutation headers, Origin required on mutations and upgrades because the identity is ambient, paired-origin requests still needing their own bearer, and allowed logins acting as approvers for Phase A requests. The whois call and the certificate source sit behind adapters (`TailnetIdentityFunc` already exists as the seam) so a `tsnet` or LocalAPI implementation can replace the CLI.
- **The socket mode stays** as `serve` for platforms whose Tailscale can proxy to a Unix socket; `direct` is the default and the documented path.
- **Aerie's migration:** once `direct` is verified, remove the Serve route on 7861 and `api.browserProxyOrigin`, and restart the service.

Acceptance: with only `--tailnet` enabled and no Serve route, a fresh browser on MarcusBook and on the iPhone reaches Sessions with no pairing screen; a connection from aerie's own tailnet address is refused; a non-allowed or tagged peer is refused; the Host, Origin and mutation guard tests pass against the direct listener; Tailscale stopping and starting is survived without restarting Sidecar. Independent security review, including the whois cache and the own-address refusal.

## Phase C: opt-in LAN listener with Sidecar-managed HTTPS (td-938935)

- **Opt-in.** `api.lan.enabled` (default off) plus `api.lan.port` (default 7863) and optional `api.lan.hostnames`. Exposed in Configuration and `sidecar api status`. Nothing listens beyond loopback and the tailnet until it is enabled.
- **Listener.** A fourth listener, `LAN`, with Browser authentication (origin-bound bearer, session-proof renewal, single-use WebSocket tickets) and approval as its only pairing path besides `sidecar api open --lan`. It never honours Tailscale headers. Socket activation classifies it by address like the others.
- **Host and Origin guards.** Allowed hosts are the IP literals of the machine's up, non-loopback interfaces (raw IPs cannot be rebound), `<LocalHostName>.local`, and configured hostnames, each with the port. The set is recomputed when interfaces change. Origins are the `https://` forms of the same.
- **Certificates.** On first enable Sidecar creates a local CA (P-256, `CN=Sidecar local CA (<hostname>)`, private key 0600 in `$STATE/api/tls/`) and issues a leaf for the allowed hosts, reissued when the host set changes or the leaf nears expiry. The CA certificate is public and served unauthenticated at `/sidecar-ca.pem` and as an iOS/macOS configuration profile, with the waiting screen linking to "Trust this Sidecar on this device" instructions. Certificate handling sits behind an adapter so a user-supplied certificate or an ACME adapter can replace it.
- **The honest limit.** Until a device trusts the CA, the first visit shows the browser's certificate warning, and clicking through it on a network an attacker controls defeats everything after it, because page code cannot see which certificate served it. The trust instructions say so plainly, and installing the CA removes it.
- **Discovery (optional, last).** Advertise `_https._tcp` over Bonjour so the service shows up as `<hostname>.local`.

Acceptance: from a second machine on the LAN, `https://<ip>:7863` shows the waiting screen after a one-time certificate step, approval from the TUI signs it in, and terminals work; a request with a foreign `Host` or `Origin` is refused; disabling the setting closes the listener. Independent security review.

## Sequencing and ownership

Phase A first; it is useful on today's listeners and is the base for C. Phase B runs in parallel with A, in its own worktree. Phase C after A. Each phase is implemented by a delegated agent from a committed checkpoint, reviewed by a fresh-context reviewer, then merged to main in both repos.
