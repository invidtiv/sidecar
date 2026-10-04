# Sidecar UI API: execution

This is the live execution record for [the Sidecar UI API plan](../sidecar-ui-api.md), which stays the controlling product and architecture plan. It names every work lane, who runs it, where it lives, and how to resume if the orchestrator stops. Keep it current: update a lane's status line when it changes.

## Resuming after an interruption

If the orchestrating session ends (a usage limit, a crash, a restart), nothing is lost. The state lives in four places, and none of them is a conversation:

1. **This file.** It holds lane status and the next actions.
2. **td.** Epic td-921cfd: `td -w ~/code/sidecar show td-921cfd` and `td list --epic td-921cfd`. Each lane's log and handoff are on its task.
3. **comms.** Topic `sidecar-ui-api`, with orchestrator handle `ui-api-orch`. `comms --as ui-api-orch inbox --unread` shows lane reports that have not been handled.
4. **Sidecar.** Lane agents run in Sidecar-managed worktrees: `sidecar agent list --json`. Look for sessions named after the lane ids below.

To resume, start a new orchestrator in Sidecar. Codex is fine for this, for example `sidecar create shell --tab --name "UI API orchestrator" --agent codex`. Tell it to read this file and the plan, `comms join ui-api-orch --replace`, and continue from the first lane whose status is not `merged`. Running lanes do not depend on the orchestrator: they finish, hand off in td, report on comms, and wait.

## Coordination

- **Orchestrator:** `ui-api-orch` on comms. It assigns lanes, starts reviews, merges, and keeps this file current.
- **Lane agents** join comms as `ui-<lane>`, for example `ui-u1a`. Each reports `ready for review`, `blocked: <question>` or `done` with `comms send @ui-api-orch`, and copies the topic.
- **Harnesses.** Codex (`gpt-6.1-sol`, high, the configured default) does most implementation and review, to stay inside Claude subscription limits. Claude subagents take work that benefits from a second model. Check headroom with `codexbar usage`.
- **Reviews cross models where possible.** Codex reviews Claude work and Claude reviews Codex work, especially for anything security-sensitive. One merged gate-plus-review pass per lane, with effort scaled to risk.
- **Merging.** The orchestrator merges each reviewed lane to `main` (`--no-ff`) and re-runs `go build ./...` and `go test ./...` on the result. sidecar-ui lanes merge to its `main` and push, since that repo is private. Lanes rebase or merge `main` before handing off.

## Rules every lane follows

These are not negotiable. Every lane prompt points here.

- Never run `git checkout`, `git restore`, `git stash`, `git reset`, or anything else that discards working-tree changes. To prove that a test fails without its fix, hand-edit the code back and then forward again.
- Never stop, kill, restart or address the default tmux server. Live runs build a temporary binary and isolate both axes exactly as `scripts/ui-api-proof.sh` does:
  - a private tmux socket, with `unset TMUX TMUX_PANE` on every tmux line, cleanup included;
  - `XDG_STATE_HOME`, `TMUX_TMPDIR` and `-config` all pointing at temporary paths;
  - `SIDECAR_ISOLATED_STATE=1`;
  - `--port 0`.
- No `tailscale serve` and no `tailscale funnel`. Never install a real launchd agent or systemd unit; use test labels and fake managers.
- Every load generator is wrapped in `timeout`. Stop every dev server, preview server and test server you start. Never touch port 7871 or anything else Marcus started.
- Work in your lane's worktree, commit as you go, and do not push the Sidecar repo. Commit messages end with `Co-Authored-By:` naming the model that wrote them.
- Track work in td under your lane's task (`td -w ~/code/sidecar`): `start`, `log`, then at the end `handoff` and `review`. Never approve your own lane.
- If you find a Sidecar bug, or friction in the `sidecar agent`, `create` or `comms` commands while working, file a td issue (label `ui-api-friction` or `bug`) with the exact command and output, and mention it in your report. Do not work around it silently.
- Product direction for every user-facing surface: each platform should feel native. Use the terminal look only where the user is actually in a terminal. Show no internal machinery (lease tokens, generations, protocol states, ids). Useful detail belongs in context, such as hover, inspector or detail views, not in chrome.

## Lanes

The status values are `queued`, `running`, `review`, `fixing`, `merged` and `blocked`.

| Lane | td | Repo | Harness | Depends on | Status |
| --- | --- | --- | --- | --- | --- |
| U0-a server | td-ba925d | sidecar | Claude | — | merged (2f0dc1e7) |
| U0-b SDK/element | td-83cce9 | sidecar-ui | Claude | — | merged (sidecar-ui 2c0f521) |
| U0-a follow-ups | td-552e24 | sidecar | Claude | U0-a | running |
| U0-c proof and measurements | td-d8fcb0 | both | Claude, plus Marcus for the live half | U0-a, U0-b | running (automatable half) |
| U1-a events stream | td-aa8756 | sidecar | Codex | U0-a | running (Codex, worktree ~/code/sidecar-ui-u1a-events, branch ui-u1a-events) |
| U1-b schemas, spec, fixtures | td-5ae805 | sidecar | Codex | U0-a | running (Codex, ~/code/sidecar-u1b-spec, branch u1b-spec) |
| U1-c service install | td-d7869b | sidecar | Codex | U0-a | running (Codex, ~/code/sidecar-u1c-service, branch u1c-service) |
| U1-d presence and v1 frames | td-713745 | sidecar | Codex | td-552e24 merged | blocked on td-552e24 |
| U1-e web app shell and Sessions | td-57a73e | sidecar-ui | Codex | U0-b | running (Codex, ~/code/sidecar-ui-u1e-app, branch u1e-app) |
| U1-f SDK adopts v1 | td-820df7 | sidecar-ui | Codex | U1-a, U1-d | queued |
| U1-g iOS adopts presence and events | td-fde8cf | sidecar-mobile | Codex | U1-a, U1-d | queued |
| U1-h security review and three-viewer proof | td-295605 | all | Claude, then Codex | U1-a, U1-d, U1-f | queued |
| U2-a core extraction | td-c709a9 | sidecar | Codex | U0-a | running (Codex, ~/code/sidecar-u2a-core, branch u2a-core) |

U2-b onward (workspace resources, operations, `<sidecar-workspace>`), U3 and U4 are briefed once U2-a and U1 settle.

## Lane briefs

Each brief is the starting point for a lane, not a complete specification. Read the plan, `docs/reference/ui-api.md` and `docs/reference/mobile-protocol.md` first. Write contract changes into `docs/reference/ui-api.md` in the same commit as the code that needs them.

### U1-a: events stream

Add `GET /api/v0/events`. It is a WebSocket on every listener, authenticated exactly like the terminal route (session or paired-origin ticket, bearer without Origin for non-browser clients, the Local socket, tailnet login). It carries one JSON message per text frame, each with `type` and a monotonically increasing `seq`:

- `hello`, sent first.
- `catalog`, the full `CatalogSnapshot` for the connection's query. It is sent once on connect and again whenever the catalog generation changes. Debounce to about 250 ms, and never send an unchanged generation. Query parameters match `GET /api/v0/sessions`.
- `attention`. Emit `needs_input` when a row's attention changes from false to true, and `finished` when an agent goes from working to done. Each carries the row's catalog id, a human title, and the time. These drive browser notifications and the iOS foreground alerts.
- `terminals`. Emit this when open attachments change, and when the geometry holder of a session the client has open changes. It names the holder's kind and a human label, never a token.
- `shutdown`, sent before the server stops.

The change signal must be real, never polling the full catalog on a fast clock:

- shells.json changes, through the existing shellstate watchers;
- agent activity and status transitions, from the same source the desktop Sessions view uses;
- host inventory events, from hostserve's snapshot and diff machinery.

Reuse those mechanisms. Bound buffers per client, and coalesce rather than drop on a slow socket. Write tests, including the isolated live proof extended with an events round trip. Update the contract and `docs/reference/cli.md` where needed.

### U1-b: schemas, `sidecar api spec`, fixture mode

- **Schemas.** Generate JSON Schemas from the Go request and response types of the HTTP routes, the terminal protocol (mobileproto) and the events stream. A library like `invopop/jsonschema` is fine; justify the choice.
- **`sidecar api spec [--json]`.** It prints an OpenAPI 3.1 document, with the WebSocket streams described under `x-streams`. Commit the generated document as `docs/reference/ui-api.openapi.json`, with a test that fails when it is stale.
- **CLI and API share types.** Make the CLI's `--json` output for the same resources use the same types. Report any shape that had to change.
- **Fixtures.** Create `testdata/ui-api/v0/` with hello, sessions, status, error examples, an events transcript and a pairing exchange, plus a `SHA256SUMS`. The sidecar-ui SDK's tests will consume these.
- **`sidecar api serve --fixtures DIR`.** It serves the catalog and status from fixtures, and backs terminals with a deterministic echo shell that obeys the real service's ordering rules. It uses the real server's auth, guards and routing, so UI work can run against real server code without tmux.

Coordinate with U1-a by rebasing before handoff. Whichever lands second adds the other's types to the spec.

### U1-c: `sidecar api service`

Add `sidecar api service install|uninstall|status [--json]`.

- **Service managers.** Use an adapter interface with launchd (per-user LaunchAgent) as the macOS default and a systemd user unit on Linux. Add a test adapter.
- **UI location.** The service runs `sidecar api serve` with the UI directory from config `api.uiDir`, when that is set.
- **Upgrades.** The server notices when its own executable changes (a Homebrew upgrade, `make install-local`, `make install-worktree`) and exits cleanly so the manager restarts it. It reports that in its log, and never touches tmux.
- **Homebrew.** Add a `service` block to the Homebrew formula template used by goreleaser, so `brew services start sidecar` runs the same command.
- **Errors.** `status --json` reports installed, loaded, running, PID, version and last exit. Every failure says what to do.

Tests use the fake adapter. No real launchd or systemd changes.

### U1-d: presence-based geometry and v1 frames

This starts after td-552e24 merges to main. Implement the plan section "Geometry: the screen you are using wins" and the frame items of "Terminal protocol v1".

- **Presence.** Add a `presence` operation: focused, visible, idle_ms, and per-attachment fitted columns and rows. The owning service runs `DecideGeometryLease` for each attachment, with its own lease identity, on presence changes and heartbeats.
  - Input claims first and then delivers, so a keystroke is never refused because of the lease.
  - Losing focus releases.
  - Expose the holder's kind and label to viewers.
- **Frames.**
  - Reset-free frames: home the cursor and repaint, with no `ESC c`.
  - Coalesce frames before the outbound queue, so a slow peer gets the latest frame rather than an abort.
  - Add a server-side `paste` operation using `load-buffer` and `paste-buffer -p`.
- **Changed-row frames from the screen model.** These are conditional on the U0 measurements in `u0-measurements.md`.

Negotiate everything through capabilities, so v0 clients, including today's iOS build, keep working. Update `mobile-protocol.md` and the fixtures.

### U1-e: sidecar-ui web app shell and Sessions

The repo is `~/code/sidecar-ui`. Build the reference app into something that feels made for a browser, starting from Sessions. This is the surface Marcus sees first. It must feel finished, not like a demo.

- **Layout.** A Sessions sidebar grouped the way desktop Sessions groups (by activity or by project, switchable), with search and filters, and a main area holding the selected terminal. On narrow screens (a phone over the tailnet) it becomes a single-column list-then-detail flow.
- **Keyboard first, never stealing from terminals.**
  - `⌘K` (`Ctrl+K` off macOS) opens a command palette: jump to a session by fuzzy name, switch grouping, toggle the sidebar.
  - Arrow keys and `j`/`k` move through the list while the list has focus. `Enter` moves focus into the terminal, and `/` starts a search.
  - A browser-safe chord returns focus from a terminal to the list. Do not use `Esc`, which belongs to vim and to agents. Pick one and document why.
  - `⌘1`…`⌘9` jump to pinned or top sessions, if that does not fight the browser.
  - `?` shows a shortcuts sheet.
  - While a terminal has focus, every key goes to the terminal except the documented `⌘` chords.
- **Information design.**
  - Each row shows the session's name, its project or worktree, and its agent state as a quiet indicator. "Needs input" stands out clearly and "working" is subtle.
  - Host, branch and path appear on hover or in a detail popover, never as row chrome.
  - Show no ids, generations, lease or protocol state.
  - Empty, unpaired and offline states are friendly and say what to do (`sidecar api open`).
- **Platform.**
  - Deep links: `/s/<host>/<session>` opens that session, and Back/Forward work.
  - The document title shows the selected session and the attention count.
  - Hooks for the browser Notification API. Attention events arrive in U1-f; poll `sessions` until then.
  - Light and dark mode follow the system, using Sidecar's palette from `~/code/sidecar/docs/reference/design-language.md` and the `sidecar-modern` theme colours.
  - System UI font for chrome, monospace only inside terminals.
  - Icons from `~/code/roc`.
  - Respect reduced motion.
- **Quality.**
  - Playwright tests for the keyboard model, deep links and the narrow layout, against the mock server.
  - Screenshots at desktop and phone widths, attached to your report under `docs/screens/`.
  - Keep the layering rule: the app imports only the two packages, and anything it needs from the server goes into the contract.

### U1-f: SDK and element adopt v1

Replace the U0 client-side focus approximation with the server's `presence` operation. Adopt the events stream (`Session.events`), reset-free and changed-row frames, server-side paste, the holder label for the "sized for …" hint, and the fixtures from U1-b. Swap the hand-written types for the generated schemas. Wire app notifications to attention events.

### U1-g: native app adopts presence and events

The repo is `~/code/sidecar-mobile`; read its AGENTS.md first.

- **Presence.** Replace explicit take-control and release UI with the `presence` model: foreground and visible state, the displayed terminal, taps and keystrokes as input.
- **Events.** Consume the events stream over the SSH stdio transport, which needs a stdio events mode in Sidecar. Coordinate that with U1-a through the orchestrator. Turn attention events into foreground alerts.
- **Native, not TUI.** Remove UI that only explains internals. Use native idioms throughout.
- **Proof.** Simulator proof, with device proof by Marcus through `./scripts/deploy.sh`.

### U1-h: security review and three-viewer proof

- **Security review.** An independent review of everything added to the API in U1 (events auth, presence, paste, service install) and of the U0 trust model, run by a different model from the one that wrote each part.
- **Three-viewer proof.** In isolation, run the TUI, a browser through the SDK, and an iOS-protocol client together. Show that the screen in use always wins, that no keystroke is lost, and that the size never ping-pongs.

### U2-a: core extraction

Make one state-free workspace-operation service for creating, renaming, deleting and restoring shells and worktrees. Today the create-worktree and create-shell sequence (plan, execute, journal, identity, setup, launch) is written three times: in `internal/cli/create_worktree.go`, `internal/plugins/workspace/create_operation.go` and `internal/overview/global_create.go`. The plugin also writes shells through its own `ShellManifest` instead of `shellstate`.

- The CLI, TUI and Sessions view must all call the new service. Behaviour does not change, and the existing tests prove that.
- Move `resolveAgentTarget` (`internal/cli/agent.go`) into `agentresolve`.
- Extract filtering, grouping and pinning from `workspacelist.Model` and `overview.Model` into pure functions that both models use.
- Run the full suite and the TUI proof scripts.

This is a large refactor in shared code, so keep commits small and reviewable.

## Bugs and friction found along the way

Each one is a td issue with the exact command and output. Fixes run as their own lanes.

| td | What | Status |
| --- | --- | --- |
