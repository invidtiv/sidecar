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
- Until td-ac892f is fixed, never run a Sidecar command that defaults to "the current shell" or "the current project" without an explicit `--target` or `--project`. That includes `shell rename`, `shell name`, `open`, `layout`, and `agent` verbs with no target. Codex agents share one app-server daemon environment, so the default resolves to some other agent's shell. Always set `COMMS_SESSION` for comms.
- Do not edit this execution file. The orchestrator owns it. Put your status in td and comms.
- Track work in td under your lane's task (`td -w ~/code/sidecar`): `start`, `log`, then at the end `handoff` and `review`. Never approve your own lane.
- If you find a Sidecar bug, or friction in the `sidecar agent`, `create` or `comms` commands while working, file a td issue (label `ui-api-friction` or `bug`) with the exact command and output, and mention it in your report. Do not work around it silently.
- Product direction for every user-facing surface: each platform should feel native. Use the terminal look only where the user is actually in a terminal. Show no internal machinery (lease tokens, generations, protocol states, ids). Useful detail belongs in context, such as hover, inspector or detail views, not in chrome.

## Lanes

The status values are `queued`, `running`, `review`, `fixing`, `merged` and `blocked`.

| Lane | td | Repo | Harness | Depends on | Status |
| --- | --- | --- | --- | --- | --- |
| U0-a server | td-ba925d | sidecar | Claude | — | merged (2f0dc1e7) |
| U0-b SDK/element | td-83cce9 | sidecar-ui | Claude | — | merged (sidecar-ui 2c0f521) |
| U0-a follow-ups | td-552e24 | sidecar | Claude, reviewed by Codex | U0-a | merged (e3ca48c2) |
| U0-c proof and measurements | td-d8fcb0 | both | Claude, plus Marcus for the live half | U0-a, U0-b | automatable half done (0b1476b2, a0dfb694); live half with Marcus pending |
| U1-a events stream | td-aa8756 | sidecar | Codex | U0-a | merged (6a11e85e), including the spec integration and CatalogRow.path |
| U1-b schemas, spec, fixtures | td-5ae805 | sidecar | Codex | U0-a | merged (854d108e) |
| U1-c service install | td-d7869b | sidecar | Codex, reviewed by Codex | U0-a | merged |
| U1-d presence and v1 frames | td-713745 | sidecar | Codex | td-552e24 merged | merged (c75857ac) |
| U1-e web app shell and Sessions | td-57a73e | sidecar-ui | Codex | U0-b | merged (sidecar-ui 8d183fc); Claude review fixed 6 UX defects |
| U1-f SDK adopts v1 | td-820df7 | sidecar-ui | Codex | U1-a, U1-d | merged (sidecar-ui fdad866); Claude review fixed the paste-marker strip |
| U1-g iOS adopts presence and events | td-fde8cf | sidecar-mobile | Codex | U1-a, U1-d | merged into sidecar-mobile main locally (not pushed). Claude review fixed 3 bugs, including tolerance for unknown event types. Device proof by Marcus; U1-g2 follow-up for UX |
| U1-i persist browser sessions | td-165353 | sidecar | Codex | U1-a, U1-b merged (both touch internal/uiapi) | merged: server 7f2ce809, SDK sidecar-ui 4a16fbf (the review fixed a P1 infinite reconnect on unknown event kinds) |
| U1-e2 web app polish | td-71e0e5 | sidecar-ui | Codex | U1-e | merged (sidecar-ui 5e0ecb2) |
| U1-c2 socket activation | td-11f799 | sidecar | Codex | U1-c | merged (7f684dc4). Review fixed an IPv4-mapped IPv6 and socket-type masquerade. The fake-supervisor proof refused 827 competing binds across a restart |
| U1-g2 native follow-ups | td-468816 | sidecar-mobile | Codex | U1-g | merged into sidecar-mobile main locally (bba67db, not pushed). Review fixed banner accessibility and an events-fallback recovery gap. Device checks are Marcus's |
| U1-h security review and three-viewer proof | td-295605 | all | Claude, then Codex | U1-a, U1-d, U1-f | done. Security 6176110f; proof 23b5302d; integrated re-run against sidecar-ui 0f9cb5f passes with 0 browser page errors and a fully rendered terminal (u1h-evidence/integrated-*) |
| U2-a core extraction | td-c709a9 | sidecar | Codex | U0-a | merged (d95b66f5) |
| U2-b workspace resources and operations API | td-eb3d80 | sidecar | Codex | U2-a | merged (da497063) |
| U2-c workspace UI | td-37a00e | sidecar-ui | Codex | U2-b | running (Codex, ~/code/sidecar-ui-u2c-workspace-ui) |
| U3-a content and layouts API | td-f8784a | sidecar | Codex | U1-a | merged (05dd383b). Claude review fixed a HIGH arbitrary file write through the diff parent parameter (git --output), a watch fd-exhaustion cap, and an existence oracle through symlinks |
| U3-b pane tree UI | td-cf59cd | sidecar-ui | Codex | U3-a, U1-f | merged (sidecar-ui 13bd63a); 70 e2e tests pass on merged main |
| U3-c pane and content polish | td-c53032 | both | Codex | U3-b | review (Codex, shell "rev U3-c"); sidecar-ui u3c-polish @d4f9c78 and Sidecar u3c-quoted-paths @6b0c51ef |
| U4 viewers agents can target | td-799dd6 | both | Codex | U3 | U4-a merged (53c2d9b2; Claude review fixed 5 issues, including double delivery and terminal-escape injection). U4-b running (Codex, ~/code/sidecar-ui-u4b-viewer) |

U2-b onward are briefed below.

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
- **Compression.** Turn on `permessage-deflate` on the terminal WebSocket, which is `CompressionDisabled` today. [U0 measurements](u0-measurements.md) show four busy terminals sending about 2 MB/s raw, against about 43 KB/s estimated with deflate. Measure the server CPU it adds with `scripts/ui-api-measure.sh`.
- **Screen-model frames are deferred to U3** by the U0 measurements: four busy agents cost about 17% of one core in Sidecar and 5% in tmux, and echo stays at 13-14 ms p50 under load. Changed-row frames may still land here if they can be built from captures; otherwise they move with the screen model.

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

Replace the U0 client-side focus approximation with the server's `presence` operation. Adopt the events stream (`Session.events`), reset-free and changed-row frames, server-side paste, the holder label for the "sized for …" hint, and the fixtures from U1-b. Swap the hand-written types for the generated schemas. Wire app notifications to attention events. Also close two gaps from the U0 shadow-DOM checks. First, forward mouse-tracking reports (clicks and drags, not only the wheel) when the frame says the app has mouse reporting on. Second, keep a selection the user started from being wiped by the redraw when the terminal takes the size.

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

### U2-b: workspace resources and operations over the API

This builds on U2-a's `workspaceops.Service`, `agentresolve` and the pure `workspacelist` rules.

- **Read routes.**
  - `GET /api/v0/projects` lists the configured projects.
  - `GET /api/v0/projects/{project}/workspace` returns that project's worktrees and shells, with agent state, ordered and grouped by the same pure rules the TUI uses.
  - Push changes for both through the events stream (`workspace` messages), driven by the existing watchers.
- **Operations.** Add `POST` routes for shell create, rename, delete and restore; worktree create (plan, then confirmed execute, with the `--expect-source-oid` guard), rename and delete (the dirty probe comes first and is refused unless confirmed); and agent start and prompt.
  - Each calls the same service the CLI calls.
  - Each returns the CLI's `--json` shape and refusal codes.
  - Each needs a new `workspace:write` scope. `full` implies it.
- **Remote hosts.** Mutations on a remote host go through the owning host's CLI, exactly as Sessions does today.
- **Contract.** Regenerate the spec and fixtures. Add tests, including concurrent-writer tests against the TUI watcher, and extend the live proof.

### U2-c: `<sidecar-workspace>` and project workspaces in sidecar-ui

A project page with worktrees and shells, and native, keyboard-first create, rename and delete flows.

- Destructive actions confirm in context. Worktree delete shows exactly what the dirty probe found.
- Agent start and prompt are available from a shell.
- The `<sidecar-workspace>` element is embeddable like the others.
- The same no-internals rule and phone layout apply.
- This starts after U2-b's contract lands on main.

### U3-a: content panes and layouts over the API

- **Content routes.** Read routes over `contentservice` cover file previews, markdown docs, diffs, issues, notes and the project file tree. They return the existing DTOs, and they check paths against project roots: no traversal, and symlink escapes refused.
- **Live refresh.** Content invalidation goes on the events stream (`content` messages), from `livewatch` path watchers scoped to the panes that clients have open.
- **Layouts.** A locked, per-viewer layout store under `$STATE/api/layouts/` uses the shared `panelayout` and `panecodec` tree format, with no cell geometry. `GET` and `PUT` it per project and per viewer, with conditional writes (ETag/If-Match). Viewers are the browser session and the paired origin.
- **Scopes.** Add `content:read`; `full` implies it.
- **Contract.** Regenerate the spec and fixtures, and extend the proof.

### U3-b: pane tree and content panes in sidecar-ui

- **Pane tree.** Splits, holding several terminals and content panes, built from the layout store. Panes can be dragged between splits, and tabs can be moved.
- **Content panes.** Rendered natively: markdown as typography, diffs with syntax highlighting side by side, issues as cards, and a file tree.
- **Platform.** Pop-out windows, live refresh from content events, and keyboard navigation between panes.
- This starts after U3-a lands. Screen-model frames are measured again here, only if several visible terminals show cost.

### U4: API clients as viewers that agents can target

- **Server (U4-a).** An API client that holds the screen announces itself on the `uirequest` bus as a viewer with `uiRequestRelayV1`. `sidecar open` and `sidecar layout get/apply/move` then reach it, with the same decline-don't-queue rules and exit codes.
- **Client (U4-b).** The browser receives those requests over the events stream, applies them to its pane tree, and acknowledges.
- **Result.** An agent says "open this diff" and it appears in whichever UI Marcus is using.

## Accepted risks (from the U1-h security review)

- Any credential grants full control until narrower scopes land with U2-b and U3-a. `readonly` on `<sidecar-terminal>` is a client promise; the server does not enforce it.
- A viewer chooses its own holder label, so the "sized for …" hint can be spoofed by another client the owner has paired.
- `--tailnet-port` lets any local process act as the owner on the tailnet listener. It is documented, and warned at start.
- Browser sessions remain exposed to an origin takeover only when the port is free: foreground `api serve` restarts, or a stopped or uninstalled service. Under the socket-activated service the supervisor holds the port continuously.
- A paired origin trusts everything served from that origin, including whatever serves that port next.
- The static UI has no script CSP. sidecar-ui must render every server and agent string as text. Reviews have confirmed it does.
- `content:read` can read everything under a project root except Git metadata, which the API refuses (b73eb69a). `.env` stays readable on purpose: it is ordinary project content. Diffs are bounded at 768 KiB, and content reads are capped at four concurrent requests per credential.
- Saved layouts hold client-supplied paths. Every consumer must treat them as untrusted and re-validate them through the API.

## Bugs and friction found along the way

Each one is a td issue with the exact command and output. Fixes run as their own lanes.

| td | What | Status |
| --- | --- | --- |
| td-0fd8fb | `sidecar open` refuses a relative file; a managed shell bound to clara-home despite its cwd being a u1c worktree (may relate td-e3a93d) | open |
| td-836fca | `TestPrefillInputEmptyRealShells` readiness flake under a full-suite run | open |
| td-0502f3 | golangci-lint global lock contention across parallel worktree gates and pre-commit | open |
| (comms) | Two Codex lanes collided on comms identity at join; worked around with COMMS_SESSION. Codex shell commands apparently do not carry the pane identity comms relies on | to file |
| td-303037, td-2a7c8a | lint lock held by a parallel lane; shell-readiness flakes under concurrent full suites (folded into the friction lane) | open |
| (sidecar) | `sidecar create worktree --json` printed two JSON documents once, breaking a strict parser | to verify |
| td-e930bb | Friction lane: flakes, lint lock, prune safety (td-8e99af, td-cd833a, td-60fb5c were real production risks), Codex `--no-daemon` launch, implicit-caller identity guard, per-shell COMMS_SESSION | merged (70eaa52e); takes effect for Codex agents started by a Sidecar built from main |
| td-11138b | `sidecar agent prompt` reported `working`, but the Codex session later showed no conversation and the lane never ran. Orchestrator now confirms every lane on screen after prompting | open |
| td-eeb7e8 | P1: a stale ShellCreatedMsg arriving after a project switch writes the next project's shells manifest | merged (7d9b53f7), with completion fences across workspace and Sessions |
| td-8e99af | A worktree-prune safety test sometimes judges a moved active worktree an orphan; checking whether production prune can do the same (friction lane, first priority) | running |
| td-87ef7e, td-d77e97, td-cce9f6, td-5e7e28, td-9339ae, td-6db2ce | Load-dependent test flakes found under parallel lane gates (friction lane) | running |
| td-ac892f | Fixed in 70eaa52e for newly launched agents. P1 root cause of several items above: Codex sessions share one `codex app-server` daemon env, so "current shell/project" defaults resolve to another agent (a reviewer renamed U1-d's shell). Friction lane | running |
| td-090b9d | Not a comms bug. The orchestrator's watcher script crashed on an untitled message and skipped reports. Fixed in the watcher | invalid |
| td-eeb7e8 lane | Codex bug lane (~/code/sidecar-bug-eeb7e8) with a completion fence for stale async messages across workspace and overview | merged (7d9b53f7) |
| td-ae18e4, td-87dd09 | `comms publish` refused with "author does not follow topic" and no recovery hint (comms) | open |
| td-6153d0 | `create worktree --agent codex` sometimes leaves the shell without Codex and reports success; under load. Recovered with `agent start --kind codex` | open |
| td-275a14 | Friction lane 3 (Codex, ~/code/sidecar-friction-3): remaining load flakes, diff memory bound td-0e9748, refuse .git internals over content:read, setupIsolatedCLI tmux isolation | merged (b73eb69a). Review fixed a P1 Git-metadata bypass through pathspec selectors |
| td-ab3af0 | Friction lane 2 (Codex, ~/code/sidecar-friction-2): notes test hang td-aa4fb7, loopback/tmux-drive load flakes td-d881e2, silent Codex start failure td-6153d0, project reorder must not cancel operations, shared events holder polling, server paste-marker strip | merged (c3ac2418) |
| td-58caeb | `comms send @ui-u2b` returned agent-not-found from another lane; peer handles are not reliably discoverable | open |
| td-07f7b1 | Eight simultaneous valid `create shell` calls gave 1 success and 7 generic exit-1 errors (allocation race). Fixed on bug-07f7b1 @d375077a with atomic allocation under the shellstate lock | merged (94f34653); review added a 2 s tmux budget under the lock |
| td-945516 | P1: after geometry handoffs xterm throws cell exceptions and the browser terminal goes blank (sidecar-ui). Fixed on bug-945516 (reset before resize, one atomic write); docs on Sidecar branch bug-945516-docs | merged (sidecar-ui 0f9cb5f, docs 6d247084). Review closed a server-content CSI injection |
| td-9fe445 | internal/app startup test hung in a full suite run under heavy load; passes alone on main and on the branch (27 s) | open (load-dependent) |
| td-f0f340 | `create worktree --project PATH` refused because several Sidecar instances show the same project; needed --shell | open |
| td-17b5e2 | Worktree delete leaves other projects' shells rooted in the removed worktree live, with a vanished cwd | open |
| td-00b64e | shell list/rename resolve the current project from env, not cwd; shell list has no --project | open |
| td-f9306b | Shell renames keep no history; the td-ac892f collateral rename of sidecar-sh-clara-home-23 could only be guessed back to 'Shell 23' | open |
