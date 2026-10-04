# Sidecar UI API: build your own Sidecar UI, plus a reference web UI

**Status:** shape agreed with Marcus, not started. No td epic yet. **Created:** 2026-10-03.

Related: [Sidecar mobile](sidecar-mobile.md) (the headless terminal protocol this plan grows), [mobile protocol reference](../../reference/mobile-protocol.md), [Sidecar as its own remote host runtime](sidecar-remote-hosts.md), [remote host viewer screen](../implemented/remote-host-viewer-screen.md), [pane layout control](../implemented/pane-layout-control.md), [embedded terminal transport decisions](../implemented/embedded-terminal-transport-decisions.md).

## Outcome

Building a UI for Sidecar becomes a feature of Sidecar. Sidecar ships a documented, versioned API for its Workspaces and Sessions features: shells, worktrees, agents, live terminals, content panes and layouts. That API works on this machine, over `localhost`, and across the tailnet. Three things are built on it:

- **`sidecar-ui`**, a private reference web UI in a new repo at `~/code/sidecar-ui`. It is generic and deliberately small, a starting point to fork or learn from. Marcus's own UI (expected to grow into the clara-home web UI) can fork it or borrow from it.
- **Embeddable pieces.** A TypeScript SDK and framework-agnostic web components (`<sidecar-sessions>`, `<sidecar-terminal>`, `<sidecar-workspace>`). Another app (clara-home, or an OpenClaw/Hermes-style assistant UI) can include Sidecar's sessions as one feature among its own.
- **Anything an agent builds.** The API is specified, typed and curl-able. A skill teaches an agent how to build a Sidecar UI from it.

The TUI, the native mobile app and every web client are peer viewers of the same tmux sessions. None of them owns the processes, and any of them can close without affecting the others.

## The controlling priority: the client chain has to feel effortless

The chain is a long one: a UI, built from components, which use the SDK, which talks to Sidecar, which may be brokering to another host's Sidecar, which talks to tmux. It only works if every link is smooth for the human using it (UX), for the agent building on it (AX), and for the developer composing it (DX). When a design choice trades against this, this wins. Concretely, "smooth" means:

- **Zero configuration on the owner's own machine and tailnet.** You never type a token to use your own sessions.
- **One schema everywhere.** The JSON from `sidecar agent list --json` is the JSON from the HTTP API, and it is the payload in a protocol frame. You learn it once.
- **The SDK owns the hard parts.** Sequencing, heartbeats, taking and releasing control, reconnecting and resyncing are invisible to an embedder. Dropping `<sidecar-terminal target="…">` into a page should be enough.
- **Reconnects are seamless.** A sleeping laptop, a tailnet blip or a Sidecar restart returns the same view, without a reload and without replaying input.
- **Refusals say what to do.** API errors use the same named refusal codes as the CLI (decline, don't queue), each with a human sentence and a machine code.
- **You can build without live tmux.** A fixture mode serves recorded sessions so a UI, or an agent building one, can be developed and tested against a fake.

The contract is not frozen, and changing it is cheap. Marcus is the only user of the mobile app and of this API, so this plan includes changing the mobile protocol, the native app, and Sidecar's internals wherever that makes the chain smoother. Breaking changes are acceptable while the API is private, as long as fixtures and all clients move together.

## Decision first

**Sidecar hosts an API, not a UI.** A headless `sidecar api serve` process exposes the core over HTTP and WebSocket. The web UI is a separate client that uses only what any other client can use. It grows out of the mobile backend, which already solved the hard part: serving one tmux pane to a client that is not the TUI, with target resolution, the shared geometry lease, lease-gated input, history, reconnect and cross-host owner forwarding (`internal/mobile`, `internal/mobileproto`, `internal/mobilehub`). `Service.Run` already takes any `io.Reader`/`io.Writer` (`internal/mobile/service.go:48-58`), so adding a WebSocket transport is a thin adapter.

```text
 apps:        sidecar-ui (reference)   clara-home   someone's own UI   an agent with curl
                      │                    │              │                  │
 components:   <sidecar-sessions> <sidecar-terminal> <sidecar-workspace>     │
                      │                                                       │
 SDK:          @marcusv/sidecar-client (typed, owns terminal-stream rules)    │
                      │                                                       │
 contract:     HTTP resources · events stream · terminal stream  (spec + JSON Schemas + fixtures)
                      │
 API host:     sidecar api serve   (unix socket · loopback TCP · tailscale serve · ssh stdio)
                      │
 core:         workspaceops · shellstate · agentcontrol · contentservice · mobile · mobilehub
                      │
               tmux (control mode, ignore-size, @sidecar-owner lease)  ── and other hosts over SSH
```

Each layer uses only the layer below it. The reference app gets no private endpoint and no private component API. If it needs something the layers lack, that gap is fixed in the layer, which is what keeps "you can build your own" true.

### Why not the other shapes

| Shape | Why not |
| --- | --- |
| A web UI bundled inside the Sidecar binary | It couples UI release to backend release, and the bundled UI becomes the privileged client. It remains possible later as packaging (`sidecar api serve --ui <dir>` already serves any bundle), but it is not the architecture. |
| A PTY running `tmux attach` piped to xterm.js (ttyd/gotty style) | It shows a whole tmux window (status bar, every pane, the user's prefix key), fights the TUI over terminal size, and cannot be embedded per pane. |
| Forward one pane's raw control-mode `%output` bytes | Already falsified by the mobile seed spike (`docs/plans/active/sidecar-mobile/proof/seed-spike.md`). Hidden pen, wrap, margin and charset state make the stream diverge from tmux. |
| Serve HTTP from inside the TUI process | The API would disappear whenever the TUI closes, and the TUI would become a privileged path. |

## The API contract

### One schema, several transports

Request and response types are Go types in one package. JSON Schemas are generated from them, and `sidecar api spec` prints the OpenAPI document plus the stream schemas. The CLI's `--json` output, the HTTP bodies, and protocol payloads all use those same types. A CLI command that prints a different shape for the same thing is a bug. This also means the API adds no owned capability that the CLI lacks. Sidecar stays a presentation layer, and the API is one more door into the same core.

The API has three kinds of surface:

- **Resources (HTTP JSON):** hosts, the Sessions catalog, projects and their workspaces, shells, worktrees, agents, content (doc, diff, issue, file and note previews from `contentservice`), and per-viewer layouts. Reads are `GET`. Operations (create, rename, delete, restore, agent start/prompt) are `POST` to the same service the CLI calls.
- **Events stream (one WebSocket per client):** generation-numbered catalog changes, attention (an agent needs input or finished), content invalidation from `livewatch`, and lease/control changes. Every message names the resource it invalidates, so clients refetch or patch instead of polling.
- **Terminal streams (one WebSocket per open terminal):** the mobile protocol, promoted to v1 (see below). One stream is one attachment, as it is today.

### Transports

All transports run the same handlers.

| Transport | Who uses it | Notes |
| --- | --- | --- |
| Unix socket in the state dir | Local agents, scripts, the CLI | File-mode protected, the same trust as the tmux socket. No auth. |
| Loopback TCP | Browsers on this machine | Never binds `0.0.0.0` unless explicitly asked. |
| `tailscale serve` → loopback | Browsers and apps across the tailnet | Provides HTTPS (needed for notifications and clipboard) and a verified tailnet identity, without Sidecar handling TLS. `sidecar api serve --tailscale` configures it. |
| SSH stdio | The native mobile app today; remote owners | `sidecar mobile serve --stdio` keeps working. It speaks the v1 terminal protocol. |

### Always-on service

`sidecar api serve` runs as a per-user background service, so the UI, widgets and background push work whenever the machine is up, whether or not a TUI is open. It is a standard feature, not a setup peculiar to this Mac.

- `sidecar api service install|uninstall|status` sets the service up. The service manager is an adapter: launchd user agent on macOS (the default), systemd user unit on Linux. The Homebrew formula's `service` block runs the same command, so `brew services start sidecar` works too.
- The service restarts itself when its binary changes, which covers a Homebrew upgrade, `make install-local` and `make install-worktree`. It keeps no state that a restart loses: clients reconnect through the SDK.
- The service never starts, stops or restarts the tmux server. It only talks to the server that is already there. If tmux is not running, it reports that, and a create operation starts tmux the same way the CLI does.
- Proofs never install the real service. They run `sidecar api serve` in the foreground against an isolated tmux server and state tree, or install under a test-only service label that the proof removes.

### Versioning and fixtures

`hello` returns the protocol version and capabilities, as it does today. Inside a version, changes are additive and capability-gated. Golden fixtures live in Sidecar (`testdata/mobile-protocol/` grows into `testdata/ui-api/`), and the SDK's CI in `sidecar-ui` and the native app's tests both run against them. A contract change lands in one Sidecar commit with its fixtures, and the clients follow.

## Trust and auth: open by default

The premise holds: whoever can drive this API can open a shell as Marcus, so the API grants no less than Sidecar already does. Default access is therefore full, with no scope prompts and no confirmations. The goal is that the owner never meets a login screen on their own machine or tailnet.

| Caller | Default |
| --- | --- |
| Local process over the Unix socket | Full access, no auth. |
| Tailnet browser via `tailscale serve`, login matches the configured owner | Full access, no auth. Identity comes from the Tailscale header. |
| Browser on this machine, the UI served by `sidecar api serve` | Full access after one-time pairing per browser. `sidecar api open` opens the UI already paired. |
| Another origin, such as clara-home on its own port | Pair the origin once (`sidecar api pair --origin URL`). Full access by default; narrower scopes (`sessions:read`, `terminal:control`, `workspace:write`) are available for widgets that should only watch. |

One correction to the "same machine means trusted" premise is worth keeping: a browser runs untrusted code from every website you visit. Same-origin rules stop a page from *reading* a `localhost` response, but not from *sending* the request. A plain GET or a form POST still executes, and WebSockets are not covered by CORS at all, so a page can open `ws://localhost:N` and drive it unless the server checks `Origin`. DNS rebinding can get around the read protection too. Some browsers now ask before a public site reaches local addresses, but that is not uniform and the server cannot rely on it. So a small set of guards is always on. None of them is visible to the owner:

- An exact `Host` allowlist, which defeats DNS rebinding.
- An exact `Origin` allowlist on every WebSocket upgrade and every mutation.
- No mutation over a request a browser can send cross-site without a preflight: JSON bodies plus a custom header only.

Browser pairing on loopback exists for the same reason. It is the one thing that separates "the owner's browser" from "any process or page that can reach port N".

The web surface never auto-answers an agent approval, the same as `agent send-keys`.

## Geometry: who decides the terminal size

tmux has one size per pane. Every viewer shares it through the existing `@sidecar-owner` lease (`internal/tty/geometry_lease.go`), with the rules unchanged:

- **Viewing never resizes.** A watching client renders the owner's columns and rows. A browser can scale the font to fit its box, so watching costs the desktop nothing.
- **Taking control resizes.** `control` claims the lease and resizes tmux to the client's fitted size. Other viewers letterbox, as the TUI already does for mobile and remote viewers.
- **Idle releases control.** Presence heartbeats expire after 15 seconds, so a closed laptop lid does not leave the pane stuck at a phone's width.

The SDK makes this a property of the component. `<sidecar-terminal control="auto|manual|never">` decides whether focus-and-type claims control, and the component shows who holds it.

## Terminal protocol v1

The mobile protocol becomes the terminal protocol for every client, and the native app moves to v1 alongside Sidecar.

1. **Reset-free frames.** Every v0 frame starts with `ESC c` (`internal/mobile/render.go:14-78`), which wipes selection and causes flashes in xterm.js. v1 frames home the cursor and repaint rows. Changed-row frames follow (the reserved `changed_row_frames` capability).
2. **Frames from the screen model.** v0 runs a `capture-pane` for every output burst. A page with four busy agents runs four capture loops. The desktop already keeps a byte-fed `screenmodel` per visible pane. Building frames from `screenmodel.Frame` removes the capture cost and makes changed-row frames natural. Measure in U0, then decide whether this lands in U1 or later.
3. **Slow-socket behavior.** The outbound queue holds 8 responses and an overflow aborts the stream (`internal/mobile/service.go:104-108`). Frames are latest-wins, so coalesce them before the queue and keep the abort only for control responses.
4. **Server-side paste.** Add a `paste` operation that uses `load-buffer` + `paste-buffer -p`, as the TUI does, so tmux decides bracketing. Clients stop wrapping bracketed paste themselves.
5. **Catalog push and attention** move to the events stream, which the native app also consumes. This is mobile M2's attention work, designed once for every client.
6. **Reconnect continuity.** The SDK reconnects with the attachment's identity and resumes view-only, as v0 `reconnect` already does. It then re-claims control only if the user still has the terminal focused.

Inherited and unchanged for now: tmux 3.4 is view-only (no `bracket_paste_flag` probe), and History refuses on the alternate screen and has no pagination. Clients show these refusals as they are.

## Core coupling to fix first

These are core fixes. Each one also helps the TUI and CLI.

- **One workspace-operation service.** The create-worktree and create-shell sequence (plan, execute, journal, identity, setup, launch) is written three times: `internal/cli/create_worktree.go`, `internal/plugins/workspace/create_operation.go`, `internal/overview/global_create.go`. The plugin also writes shells through its own `ShellManifest` instead of `shellstate`. Pull these into one state-free service that the CLI, TUI and API all call.
- **Agent target resolution out of `internal/cli`.** `resolveAgentTarget` (`internal/cli/agent.go:339`) moves to `agentresolve`, so the API calls `agentcontrol.Service` directly.
- **Sidebar rules as pure functions.** Filtering, grouping and pinning move out of `workspacelist.Model` and `overview.Model`, so every client's list agrees with the TUI's. Sorting is already shared through the mobile catalog.
- **A locked layout store.** `state.json` is a process-global singleton that `Save()` rewrites with no lock (`internal/state/state.go:574-594`). API clients get a locked per-viewer layout store from the start. Layouts are per viewer: the TUI keeps its own, and each API client keeps its own in the shared tree format, so a layout can be copied across but is never fought over.

Not reused: the TUI pane runtime (`paneframe`, `contentpanes.Deck`, `docview`, `issueview`, `workspacediff`, `livepanes`). It is built on `tea.Cmd` and renders ANSI. API clients get `contentservice` DTOs and render them natively. The pane tree itself (`panelayout` nodes, kinds and ratios, `panecodec`) is presentation-neutral and is part of the contract. Cell geometry and cell floors stay TUI-only.

## The `sidecar-ui` repo

A new private repo at `~/code/sidecar-ui`, created in U0. It sits beside `~/code/sidecar-mobile` and is a pnpm workspace with three packages:

- **`@marcusv/sidecar-client`, the SDK.** Types are generated from Sidecar's schemas. It has a `Session` for resources and events and a `Terminal` that owns sequencing, heartbeats, control and reconnect. It runs in browsers and in Node, so agents and scripts can use it too. It does not depend on any framework.
- **`@marcusv/sidecar-elements`, the web components.** They are written in Svelte and compiled to custom elements, so they work in Svelte, React or plain HTML. Theme comes from CSS custom properties (seeded from the active Sidecar theme via the API) plus `::part` hooks, so a host app can restyle them. Components emit DOM events (`sidecar-attention`, `sidecar-open`) and take attributes, so a host can wire them into its own navigation. xterm.js with the WebGL and fit addons sits inside `<sidecar-terminal>`, at zero scrollback, since History is a separate request.
- **`apps/sidecar-ui`, the reference app.** SvelteKit with `adapter-static`. It provides a Sessions view, project workspaces, and a pane tree with terminals and native content panes (rendered markdown, highlighted side-by-side diffs, issue cards). It also does things only a browser can: pop-out windows, dragging tabs between splits, deep links (`/s/<host>/<session>`), notifications. It follows `docs/reference/design-language.md` in spirit, with icons from `roc`. It imports only the two public packages.

All of it stays private until the API settles. CI runs the SDK against Sidecar's fixtures, plus an end-to-end proof against `sidecar api serve --fixtures`.

## Tools for people and agents building UIs

- `sidecar api spec` prints OpenAPI plus the stream schemas, so an agent can read the whole contract in one command.
- `sidecar api serve --fixtures` serves recorded sessions with no tmux, for UI development, tests and demos. It also plugs into `./scripts/demo.sh`, so a demo shows the TUI and a browser on the same isolated sessions.
- A Sidecar skill, `build-sidecar-ui`, covers the layers, the trust model, the geometry rules and the components. It includes a working example: a plain HTML page that embeds a live terminal in about twenty lines.
- `sidecar api status --json` reports listeners, connected clients, and who holds control of which terminal.

## Milestones

Each milestone ends in something Marcus can use. Every live proof isolates both the tmux server and the Sidecar state tree (the `scripts/tmux-drive.sh` rules), never the default server.

### U0: Steel thread through every layer

Create the `sidecar-ui` repo. `sidecar api serve` runs on the Unix socket and loopback, with the always-on guards and browser pairing. A terminal WebSocket bridges the unchanged v0 protocol. A minimal `@marcusv/sidecar-client` `Terminal` and `<sidecar-terminal>`. Two consumers: the plain-HTML example, and the same element embedded in a page served from a second origin.

Exit:
- From a tailnet device through `tailscale serve`, type into a running agent session. The desktop TUI shows the same session and is never resized until control is taken.
- xterm.js works inside the component's shadow DOM: focus, selection, IME, paste.
- Measurements are recorded: captures per second and bytes per second for one busy agent and for four, and keystroke-to-echo latency over the tailnet. These decide when v1 item 2 lands.

### U1: Contract v1 and Sessions

Generated schemas and `sidecar api spec`. The events stream with catalog push and attention. v1 frames (reset-free, coalesced, server-side paste). Fixture mode. `<sidecar-sessions>` and the Sessions view in `sidecar-ui`, including cross-host rows through the hub, History and notifications. The native app moves to v1 and the events stream. `sidecar api service install` with the launchd and systemd adapters. Two proofs: a three-viewer lease proof (TUI, phone, browser), and an independent security review of the auth and guards.

### U2: Project workspaces and operations

The core extraction lands first: one workspace-operation service, agent resolution in `agentresolve`, and sidebar rules as pure functions. Then workspace resources and `<sidecar-workspace>`. Create, rename, delete and restore shells and worktrees, and start or prompt agents, all through the service the CLI calls.

### U3: Pane tree and content panes

Splits holding several terminals and content panes, from `contentservice`. Live refresh from the events stream. The locked layout store. Pop-out windows in the reference app.

### U4: Clients as targets for agents

An API client that holds the screen announces itself on the `uirequest` bus as a viewer with `uiRequestRelayV1`. `sidecar open` and `sidecar layout apply/move` then reach it, with the same decline-don't-queue rules. An agent says "open this diff", and the diff appears in whichever UI Marcus is using. The `build-sidecar-ui` skill is finished, with clara-home as its first outside consumer.

The Fractal model in `docs/diagrams/fractal/` gains the API host, its transports and the external clients in U0, and is kept current after that.

## Settled decisions

- **Layouts are per viewer,** in the shared tree format (see "A locked layout store").
- **The API host is an always-on service,** and every user can install it (see "Always-on service").
- **npm scope `@marcusv`:** `@marcusv/sidecar-client` and `@marcusv/sidecar-elements`. The packages are not published while the repo is private. The reference app and clara-home consume them through the workspace or a git dependency.
- **The native app keeps SSH stdio as its default transport** and also speaks v1 and the events stream. Revisit WebSocket through `tailscale serve` once that path has run for a while.

## Risks

- **Contract churn across three repos.** Sidecar, `sidecar-ui` and `sidecar-mobile` all move with the contract. Fixtures are the shared truth, and each contract change lands with its fixtures in one Sidecar commit.
- **Capture cost with many visible terminals.** Measured in U0; the screen-model frames are the mitigation.
- **Lease contention with three viewers.** The rules exist but have not been exercised with three viewers at once. U1 adds the proof.
- **Auth bugs are remote code execution.** The guards are a U0 gate, and U1 needs an independent security review.
- **The reference app drifting into Marcus's personal UI.** It stays generic. Personal work happens in a fork or in clara-home.
