# Sidecar web: Workspaces and Sessions in a browser

**Status:** draft for discussion, not started. No td epic yet. **Created:** 2026-10-03.

Related: [Sidecar mobile](sidecar-mobile.md) (the headless terminal protocol this plan reuses), [mobile protocol reference](../../reference/mobile-protocol.md), [Sidecar as its own remote host runtime](sidecar-remote-hosts.md), [remote host viewer screen](../implemented/remote-host-viewer-screen.md), [pane layout control](../implemented/pane-layout-control.md), [embedded terminal transport decisions](../implemented/embedded-terminal-transport-decisions.md).

## Outcome

Open a browser on this Mac (`localhost`) or on any device in the tailnet. You see the same shells, worktrees and agents that the TUI shows, both per project and in the global Sessions view, and you can type into the same live tmux sessions. The TUI and the browser are two viewers of one set of sessions. Neither owns the processes, and either can be closed without affecting the other.

The Workspaces and Sessions surfaces follow the TUI's model (the same rows, sort, status, pane kinds and operations). Everything around them is built for the browser: real typography, rendered markdown and diffs, hover, drag, multiple windows, notifications. The plan does not try to reproduce the TUI cell for cell.

## Decision first

**Build a headless `sidecar web serve` process on top of the mobile backend.** Do not build a browser-specific terminal path, and do not serve HTTP from inside the TUI.

Most of what this needs already exists, because the iPhone/iPad work had to solve the same problem.

- `internal/mobile` + `internal/mobileproto` already serve one tmux pane to a non-TUI client. They provide target resolution, the geometry lease, lease-gated input, history and reconnect. `Service.Run` takes any `io.Reader`/`io.Writer` (`internal/mobile/service.go:48-58`), so a WebSocket is a thin adapter.
- `internal/mobilehub` already brokers that stream to the owning host over the existing SSH master. Cross-host sessions come along without extra work.
- Catalog composition reuses the `workspacelist` sort rules the desktop uses, so the browser's Sessions list cannot drift from the TUI's.
- The frames are complete VT replacement streams (`internal/mobile/render.go:14-78`). xterm.js consumes them with `term.write(frame)`.

The work is therefore mostly four things: a listener with auth, a browser client, the protocol additions a richer client needs, and one service layer for workspace operations that is currently written out three times.

### Why not the other shapes

| Shape | Why not |
| --- | --- |
| PTY running `tmux attach` piped to xterm.js (ttyd/gotty style) | The quickest demo, the worst product. It shows a whole tmux window: status bar, every pane, the user's prefix key. Sessions are `window-size manual`, so it fights the TUI over size, and nothing in the repo supports it. |
| Forward one pane's raw control-mode `%output` bytes | Already tried and falsified by the mobile seed spike (`docs/plans/active/sidecar-mobile/proof/seed-spike.md`). Hidden pen, wrap, margin and charset state make the stream diverge from tmux immediately. |
| Serve HTTP from inside the TUI process | The browser would stop working whenever the TUI is closed, and the web would become a privileged path into TUI state. That breaks "UIs are clients of the core". |
| Headless server over the mobile protocol (chosen) | Reuses approved, proof-tested code. The server shares the TUI's lease and identity rules. It runs whether or not the TUI does. |

## Architecture

```text
browser (SvelteKit SPA, embedded in the binary)
   │  HTTPS via tailscale serve, or http://127.0.0.1
   ▼
sidecar web serve   ── one headless process, loopback listener
   ├── /api/*        JSON: catalog, workspaces, ops, content, layouts
   ├── /ws/events    push: catalog changes, attention, content invalidation
   └── /ws/term      one WebSocket per terminal pane = one mobile protocol stream
          │
          ▼
   mobilehub broker ──► local mobile.Service (in-process)
                    └─► remote owner: ssh host sidecar mobile serve --stdio --owner-only
          │
          ▼
   tmux (control mode, ignore-size, @sidecar-owner lease)
```

- **One process, thin shell.** `internal/web` holds only transport, auth, and the mapping from HTTP/WS to library calls. Business rules stay in `workspaceops`, `shellstate`, `agentcontrol`, `contentservice`, `mobile`, and `mobilehub`.
- **Terminal streams reuse `mobileproto` verbatim.** A `/ws/term` socket carries the same JSONL envelopes the native app sends over SSH. The `testdata/mobile-protocol/v0` fixtures and proof scripts then cover the web client too, and a protocol change is made once for both clients.
- **Everything except terminals is plain JSON.** Catalog snapshots, `contentservice` DTOs (`DiffDTO`, `IssueDTO`, previews), workspace operations and the pane tree. The browser renders these natively. It does not get ANSI strings.
- **Writes go through the same locked stores the CLI uses.** Shells go through `shellstate` (flock-serialized, and already watched by the TUI through fsnotify), so a shell created in the browser appears in the TUI through the existing watch.

### Adapters at the seams

| Seam | Default | Alternates kept possible |
| --- | --- | --- |
| Listener / exposure | Loopback bind plus `tailscale serve` in front of it | Embedded `tsnet` node; a plain LAN bind with TLS |
| Auth | Token cookie on loopback; Tailscale identity headers when proxied | Passkey/WebAuthn later |
| Browser terminal | xterm.js with the WebGL renderer | A libghostty WASM emulator, if it matures |
| Frame source | Capture-based normalized frames (mobile v0) | Frames from the byte-fed screen model (`screenmodel.Frame`) |
| Layout store | New locked per-viewer store | Shared with the TUI once `state.json` is fixed |

## Security model

This is remote shell access by design: anyone who can load the page can run commands as Marcus. Treat it that way.

- **Bind to loopback only.** Never bind `0.0.0.0` by default. Tailscale reach comes from `tailscale serve` proxying the tailnet to `127.0.0.1:<port>`. That gives HTTPS (which browser notifications and clipboard need) and a verified `Tailscale-User-Login` header without Sidecar handling TLS or keys.
- **Use a login token on loopback.** `sidecar web serve` prints a one-time login link. It sets an `HttpOnly`, `SameSite=Strict` session cookie. Other local users and other browser tabs on the machine cannot drive shells without it.
- **Defend against DNS rebinding and CSRF.** Requests must carry an exact allowlisted `Host`. Every mutation and every WebSocket upgrade must carry an exact allowlisted `Origin`. Mutations accept no form posts, only JSON with a header token.
- **Use an identity allowlist when proxied.** Configuration lists the tailnet logins allowed in. Any other login is refused, even inside the tailnet.
- **Keep the existing approval stance.** The web surface never auto-answers an agent approval, the same as `agent send-keys`.

## Geometry: who decides the terminal size

tmux has one size per pane. The TUI, mobile, and now the browser share it through the existing `@sidecar-owner` lease (`internal/tty/geometry_lease.go`). The rules carry over unchanged.

- **Viewing never resizes.** A browser pane that is only watching renders the owner's columns and rows. Unlike the TUI, it can scale the font to fit its box. That makes "watching an agent on the TV" free and leaves the desktop untouched.
- **Typing claims control.** Focusing a terminal and typing (or an explicit "take control") calls `control`. That claims the lease and resizes tmux to the browser's fitted columns and rows. The TUI, now a non-owner, letterboxes the pane the way it already does for mobile and remote viewers.
- **Idle releases control.** Presence heartbeats expire after 15s, so a closed laptop lid releases control without leaving the pane stuck at a phone's width.

Marcus to confirm: whether "focus + type" should claim automatically, or whether the browser should always need an explicit click to take control.

## What the mobile protocol needs for a browser

These go in as negotiated capabilities, so the native app keeps working on v0.

1. **Catalog push.** Clients poll `sessions` today. Add a subscription carrying generation-numbered snapshots or diffs. `hostserve` already streams inventory snapshots and diffs in a shape worth copying. The TUI gets the same signal from fsnotify.
2. **Attention events.** Mobile M2 needs the same thing: an agent needs input, or an agent finished. The browser turns them into tab badges and `Notification`s.
3. **Frames without a reset.** Every v0 frame starts with `ESC c`, which wipes xterm.js selection and causes flashes. Add a frame variant that homes the cursor and repaints rows, and later changed-row frames (the reserved `changed_row_frames` capability).
4. **Slow-socket behaviour.** The outbound queue holds 8 responses, and an overflow aborts the stream (`internal/mobile/service.go:104-108`). Over Wi-Fi or tailnet that is a dropped terminal. Frames are already latest-wins, so the fix is to coalesce frames before they reach the queue and keep the abort only for control responses.
5. **Many attachments per page.** A workspace page shows several terminals at once. One WebSocket per pane keeps the "one attachment per stream" rule intact. The thing to measure is cost: each output burst triggers a `capture-pane`, and four busy agents means four capture loops. Frames from the byte-fed screen model remove that cost; move to them if W0 measurements show it is needed.

Also inherited: tmux 3.4 is view-only (no `bracket_paste_flag` probe), and History refuses on the alternate screen and has no pagination. Both stay as they are for now. The browser shows the same honest refusals.

## Coupling to fix in the core first

These are core fixes, not web features. Each one also helps the TUI and CLI.

- **One workspace-operation service.** The create-worktree and create-shell sequence (plan, execute, journal, identity, setup, launch) is written three times: `internal/cli/create_worktree.go`, `internal/plugins/workspace/create_operation.go`, `internal/overview/global_create.go`. The plugin also writes shells through its own `ShellManifest` instead of `shellstate`. Pull these into one state-free service. The CLI, the TUI and the web server all call it. Without this, the web would be a fourth copy.
- **Agent target resolution out of `internal/cli`.** `resolveAgentTarget` (`internal/cli/agent.go:339`) belongs in `agentresolve` so the server can call `agentcontrol.Service` directly.
- **Sidebar rules out of Bubble Tea models.** Filtering, grouping and pinning sit inside `workspacelist.Model` and `overview.Model`. Extract them as pure functions so the browser's list cannot disagree with the TUI's. Sorting is already shared through the mobile catalog.
- **Layout persistence.** `state.json` is a process-global singleton that `Save()` rewrites with no lock (`internal/state/state.go:574-594`). A second writer would silently clobber pane layouts. The web needs its own locked layout store from day one. Moving TUI layouts into it is a separate decision (see below).

Explicitly *not* reused: the pane runtime (`paneframe`, `contentpanes.Deck`, `docview`, `issueview`, `workspacediff`, `livepanes`). It is built on `tea.Cmd` and renders ANSI. The browser builds pane content from `contentservice` DTOs instead. The pane *tree* (`panelayout` nodes, kinds, ratios, `panecodec`) is presentation-neutral and is reused. Cell geometry and cell floors are not: the browser lays out with CSS and has its own pixel floors.

## Browser client

- **Stack:** SvelteKit + TypeScript built with `adapter-static`, embedded with `go:embed`. This keeps the single Homebrew binary. The release pipeline gains a Node build step: goreleaser builds the assets before `go build`, and CI checks that the embedded assets match the sources.
- **Design:** `docs/reference/design-language.md` translated to the web, with theme colors from the active Sidecar theme exported as CSS tokens so the browser matches the TUI. Icons come from `roc`.
- **Terminal panes:** xterm.js at zero scrollback (History is a separate request), with the WebGL addon and the fit addon. Emulator replies are dropped, never forwarded to tmux. Bracketed paste comes from the frame's `modes`. Wheel input becomes SGR mouse reports when the app has mouse reporting on, and opens History otherwise. This is the same wheel routing the TUI uses.
- **Content panes:** markdown rendered as HTML, diffs with syntax highlighting and side-by-side view, issues as structured cards, file previews. Live refresh comes from `livewatch.NewPathWatcher` invalidations on `/ws/events`.
- **Where the browser goes beyond the TUI:** pop a pane out into its own browser window, drag tabs between splits, real text selection and links, scale-to-fit terminals, deep links (`/s/<host>/<session>`) that can be bookmarked or opened from a notification.

## Surface parity

Sidecar is a presentation layer, so the web adds no owned capability. Everything it does already has a CLI path, or gets one in the core-extraction step: `create shell`, `create worktree`, `agent *`, `session restore`, `layout *`. New CLI is limited to running the server:

- `sidecar web serve [--port N] [--tailscale]`
- `sidecar web status --json` (listen address, the connected viewers, and who holds control where)
- `sidecar web login` (prints a fresh one-time login link)

## Milestones

Each milestone ends in something Marcus can use, proved with `scripts/tmux-drive.sh`-style isolation (a private tmux socket and a private state tree, never the default server).

### W0: Steel thread

`sidecar web serve` on loopback with token auth. One page that lists sessions, using the existing `sessions` poll, and opens one shell in xterm.js over `/ws/term` bridging the unchanged mobile protocol. View, take control, type, release.

Exit: from a second tailnet device through `tailscale serve`, type into a running agent session while the desktop TUI shows the same session and is never resized until control is taken. Record frame cost (captures per second and bytes per second) for one busy agent and for four, and record keystroke-to-echo latency over the tailnet. Those numbers decide whether item 5 above moves forward.

### W1: Global Sessions

Catalog push, attention events and the reset-free frame variant land in the protocol. A full Sessions page: grouped and filtered list, status, cross-host rows through the hub, History, and notifications. The Origin, Host and identity hardening is complete.

### W2: Project workspaces and operations

The core extraction lands first: one workspace-operation service, agent resolution in `agentresolve`, sidebar rules as pure functions. Then a per-project workspace page with worktrees and shells. Create, rename, delete and restore shells and worktrees. Start or prompt an agent. Every operation goes through the same service the CLI calls.

### W3: Pane tree and content panes

Splits holding several terminals plus doc, diff, issue, file and note panes, rendered from `contentservice`. Live refresh. The locked web layout store. Pop-out windows.

### W4: The browser as a viewer agents can target

`web serve` announces itself on the `uirequest` bus as a viewer with `uiRequestRelayV1`. `sidecar open` and `sidecar layout apply/move` then work against the browser when it holds the screen, with the same decline-don't-queue rules. An agent can then say "open this diff" and it appears in the browser.

## Open decisions for discussion

1. **Layout sharing.** Should a project's pane layout be one shared model between the TUI and the browser, or should each viewer keep its own? Shared is more coherent but needs `state.json` locking and cell↔pixel reconciliation. Per-viewer is simpler and matches how differently the two surfaces will lay out. The plan recommends per-viewer, with the tree format shared so a layout can be copied across.
2. **Auto-claim on type.** Should the browser take control automatically when you type, or only on an explicit action?
3. **Relationship to the native app.** Over Tailscale, the web UI also works on an iPhone or iPad. Native stays better for keyboard handling, background push and rotation, but the web covers Android and any laptop at no extra cost. Should mobile M2/M3 (attention events, push) be designed once for both clients? The plan assumes yes.
4. **Exposure default.** Should `--tailscale` (which manages `tailscale serve` for you) be the documented default, or should Sidecar only print the `tailscale serve` command and leave networking to the user?
5. **Always-on.** Should `web serve` run only on demand, or as a launchd agent so the browser works when nothing else is running? Always-on fits the "push for background alerts" observer that mobile M3 already needs.

## Risks

- **Capture cost with many visible terminals.** Measure it in W0. The screen-model frame source is the mitigation.
- **Lease contention.** With TUI, phone and browser all active, control changes hands more often. The rules exist and are tested, but have not been exercised with three viewers. Add a three-viewer proof in W1.
- **Accidental exposure.** Auth bugs here are remote code execution. The hardening list above is a W0/W1 gate, not polish, and needs an independent security review before W1 is called done.
- **A second frontend toolchain.** Node enters the release path. Keep it confined to `web/` and to one goreleaser hook.
