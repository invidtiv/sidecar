---
title: Build your own Sidecar UI
description: A recipe for agents and humans building a Sidecar client over the public UI API.
---

# Build your own Sidecar UI

The Sidecar UI API lets you build a browser app, embedded terminal, native viewer, or agent client over the same Sessions, workspace, content, and terminal capabilities used by Sidecar. The server owns target resolution, operations, and terminal authority. Your UI consumes those capabilities and presents their results.

The reference web UI, TypeScript SDK, and web components exist in the separate `sidecar-ui` repository. They are private for now. You can build a client from the public contract without access to them. To run a UI you already have, see [Use the web UI](web-ui.md).

## Start with the contract

Run `sidecar api spec --json` to discover the generated OpenAPI 3.1 resources and the terminal and events schemas under `x-streams`. This command needs no running server or tmux. Read [docs/reference/ui-api.md](https://github.com/marcus/sidecar/blob/main/docs/reference/ui-api.md) for listener rules, pairing, ordering, refusals, content boundaries, and viewer relay. Read [mobile-protocol.md](https://github.com/marcus/sidecar/blob/main/docs/reference/mobile-protocol.md) for the terminal protocol, identity, geometry, and reconnect rules. The schemas describe serialization; the written contracts supply the behavioral rules.

Use `GET /api/v0/hello` to discover the running server's capabilities. Terminal envelopes retain protocol version 0 while negotiating additive v1 capabilities. Require the capability you use rather than assuming every server has it.

## Choose a transport and credential

| Client | Transport | Authentication |
| --- | --- | --- |
| Local CLI or agent | HTTP and WebSocket over the mode-0600 Unix socket | No application authentication; access to the socket grants trust |
| Browser UI served by Sidecar | Loopback HTTP and WebSocket, normally `http://127.0.0.1:7861` | Pair with `sidecar api open`; use renewable browser bearer tokens |
| UI hosted on another origin | Browser listener with CORS for the paired exact origin | `sidecar api pair --origin URL`; use that origin's bearer token |
| Tailnet viewer | Separate listener behind `tailscale serve` | Allowed Tailscale login; paired external origins also need their bearer token |

The Unix socket address is in `$STATE/api/endpoint.json`. Local callers can use `curl --unix-socket SOCKET http://sidecar.local/api/v0/hello`, substituting that socket path. HTTP resources and streams use the same listener trust model.

Browser API calls send `Authorization: Bearer TOKEN`. Mutations also need `Content-Type: application/json` and `X-Sidecar-Request: 1`. Host and Origin guards apply; paired origins get CORS for that exact origin. Browser WebSockets cannot set an Authorization header, so first buy a single-use ticket through `POST /api/v0/ws-tickets`, then redeem it on the intended stream with the same Origin. Buy a new ticket for each connection, including reconnects. See the contract for ticket body fields and scopes.

Same-origin pairing uses a one-use code in the URL fragment. The built-in pairing page stores a non-extractable P-256 private key in IndexedDB. A custom UI must implement the [signed browser renewal algorithm](https://github.com/marcus/sidecar/blob/main/docs/reference/ui-api.md#renewing-a-browser-session-the-sdk-algorithm): get a challenge, sign it, exchange the proof, and retain the 15-minute bearer only in memory. Pairing the browser does not make later API calls ambiently authenticated. Keep paired-origin tokens out of published bundles and logs.

## Develop without tmux

Fixture mode serves recorded resources and deterministic echo terminals through the real API guards and terminal service. It launches no shell and executes no terminal commands. Run the following from a Sidecar checkout, with a built UI directory substituted for `/absolute/path/to/ui`:

```sh
fixture_root=$(mktemp -d /tmp/sc-ui.XXXXXX)
mkdir -p "$fixture_root/state" "$fixture_root/tmux"
printf '{}\n' > "$fixture_root/config.json"
unset TMUX TMUX_PANE
SIDECAR_ISOLATED_STATE=1 XDG_STATE_HOME="$fixture_root/state" TMUX_TMPDIR="$fixture_root/tmux" \
  sidecar -config "$fixture_root/config.json" api serve \
  --fixtures testdata/ui-api/v0 --port 0 --ui /absolute/path/to/ui
```

This uses a temporary config, state tree, and tmux namespace. Port 0 chooses an unused loopback port. In another shell, use the same config and `XDG_STATE_HOME` to run `sidecar api open`, or `sidecar api open --print` to get a pairing URL. After stopping the foreground server, remove only the temporary tree you created. The [fixture guide in the contract](https://github.com/marcus/sidecar/blob/main/docs/reference/ui-api.md#schemas-and-fixture-development) describes the recorded files and their limits.

## Implement the three surfaces

| Surface | Start here | Client responsibility |
| --- | --- | --- |
| Resources | `/api/v0/hello`, `/status`, `/sessions`, `/projects`, and project workspace/content/layout routes | Read data, invoke typed operations, and honor identity checks, root boundaries, and ETags |
| Events stream | `/api/v0/events` | Apply the hello and catalog baseline, process ordered catalog/attention/content changes, and reconnect to a fresh baseline |
| Terminal streams | `/api/v0/terminal` | One connection per protocol stream; resolve, attach, render frames, report presence, send input, and release |

Use the events stream to invalidate or replace resource views instead of polling Sessions. Event sequence numbers belong to that connection. Reconnect starts at a new sequence-1 baseline; do not replay old attention or client actions. Terminal streams have their own protocol sequencing and checkpoints. Keep expensive catalog reads off an attached terminal stream.

## Rules every client must follow

- **Carry catalog authority.** Select a ready row or explicit ready candidate, and send its opaque `target` plus complete `expected_target` in `resolve`. Do not parse selectors or reconstruct identity from a display name. `identity_changed` means reread the catalog and resolve and attach again using the fresh identity. Retryable `backend` failures mean the lookup could not finish; they do not establish that a terminal was replaced.
- **Keep content scope separate.** Use catalog-issued `content_workspace_id` for content, tree, layouts, and viewer presence. It is separate from terminal `workspace_id` and `expected_target`. Omitted remote content authority does not authorize reading a local project. Revalidate saved terminal attachment hints against the current catalog before opening or reconnecting.
- **Respect geometry authority.** Render the server's accepted geometry and holder. With presence negotiated, report fitted terminal cells, display visibility, window focus, and idle time. Release on blur or removal. Input takeover follows the negotiated protocol; reconnect begins as a viewer. Do not resize repeatedly to compete with another holder, and never buffer disconnected input for later delivery.
- **Keep heartbeats alive.** Follow the advertised terminal cadence, currently five seconds. Fifteen seconds without heartbeat, presence, or input expires an owned lease. A presence-capable heartbeat remains valid while viewing without control. WebSocket pings do not replace protocol heartbeats. Apply operation sequences, reset generations, and frame checkpoints exactly as the terminal contract requires.
- **Decline instead of queueing.** Off-screen, stale, unfocused, busy, and unauthorized operations have explicit refusals. Show the reason and leave the proposed action unapplied. Do not replay an uncertain mutation or deliver keystrokes after reconnect. Use layout ETags and reread after conflicts.

## Let agents open panes in your UI

Advertise `uiRequestRelayV1` on the events connection with `/api/v0/events?viewer=uiRequestRelayV1`. A `full` or `ui:control` credential is required. The stream sends a `viewer` message with a connection ID. Save your displayed project/workspace layout, then report that viewer's focused and visible presence through `POST /api/v0/viewers/presence`. Pane viewports use CSS pixels; terminal geometry uses cells.

Send presence when focus, visibility, project, workspace, session, viewport, or pane focus changes, and every five seconds while connected. Presence expires after fifteen seconds. The latest transition into focused and visible wins; a heartbeat does not steal focus. A focused registered viewer lets `sidecar open` and `sidecar layout get|apply|move` reach your UI's screen.

For each `ui_request`, check expiry and that its origin remains displayed. Adopt the proposed layout without a competing PUT, then acknowledge through `POST /api/v0/viewers/ack` as `opened` or `declined`. Reconcile with the saved layout after conflict, uncertain acknowledgement, focus loss, or reconnect. Never replay an uncertain acknowledgement. Requests decline when delivery cannot complete; they are never queued for later. See the [viewer relay contract](https://github.com/marcus/sidecar/blob/main/docs/reference/ui-api.md#focused-api-viewers-and-agent-pane-requests) for exact messages, limits, and supported pane kinds.

## A recipe for an agent

1. Save `sidecar api spec --json` and read both written contracts. Choose your listener, credential flow, and required capabilities.
2. Start the isolated fixture server above. Read hello, status, and Sessions over the Local socket to establish a resource client.
3. Add browser pairing or exact-origin pairing, mutation headers, and per-connection tickets. Verify missing or invalid credentials are refused.
4. Subscribe to events and render a catalog. Select a ready row, carry its `expected_target`, and attach one terminal. Render its first frame before enabling input.
5. Implement sequencing, heartbeats, geometry presence, blur/release, reconnect, and identity replacement. Test two viewers and a dropped connection with fixture terminals.
6. Add project content and layouts with catalog-issued scope and conditional writes. If your UI supports agent pane requests, implement viewer presence and acknowledgements.
7. Build static files with `index.html`, then configure `sidecar api service install --ui DIR`. Pair with `sidecar api open` and inspect `sidecar api status --json`. Verify real terminal behavior in an isolated tmux/state environment before claiming live-shell support.
