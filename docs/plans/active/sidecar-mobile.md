# Sidecar mobile: Sessions on iPhone and iPad

**Status:** readiness, screen study, M0-A/B terminal strategy, M0-C Go service and M0-D native client independently approved and committed. Physical iPhone input, rotation and desktop handoff are observed, along with a physical fresh-process reconnect. M0-E remains open for capture-coalescing repair review and live retest plus remaining device checks. M1-A1 local catalog is approved and integrated; cross-host routing and the native Sessions browser remain underway. **Created:** 2026-09-07. **Execution epic:** td-5d82c2. **Plan task:** td-09d533. **Original planning task:** td-c2870b.

This is the controlling product and architecture plan. Read [M0/M1 execution](sidecar-mobile/execution.md) next for exact source seams, ownership, task dependencies, readiness gates, and the terminal-seed decision. Implementation is delegated to sub-agents; the coordinator integrates independently reviewed slices. Tooling and screen artifacts live in `../sidecar-mobile`; their existence does not prove terminal attachment.

## Outcome

Open Sidecar on an iPhone or iPad, see the shells and worktrees running across the user's configured hosts, sort or filter that list, tap an agent, and continue working in its existing terminal. Receive an alert when an agent needs input or finishes, and tap it to return to that exact session.

The first product is a native Sessions browser with one embedded terminal at a time. Agents and tmux keep running on their owning machines. The phone is another viewer of those sessions; it does not move a process or resume a second copy of an agent conversation.

## Settled direction and open decisions

- **One universal SwiftUI application for iPhone and iPad, using native Liquid Glass and the actual `sidecar-modern` colors.** Reuse Jumar's SwiftTerm and SSH foundation at the boundaries below. Target iOS/iPadOS 26.0 and later, using the public 26.5 simulator baseline; do not require a beta SDK. The connected personal devices run 27 beta and can provide labeled local proof without raising the deployment target. Pin dependencies before the terminal spike and change them only for a demonstrated build or fidelity need.
- **`aerie`, this Mac, as the first connection hub.** The app connects to it over authenticated SSH on LAN or Tailscale. The hub supplies its local Sessions inventory and the hosts already configured in Sidecar, using the existing Go host registry and SSH routing.
- **A small headless mobile API in the Sidecar binary.** Keep inventory, target resolution, terminal ownership, and notification policy in Go. Expose the API as a versioned framed stream over SSH stdio initially; do not require a new externally listening web service for the first version.
- **Reuse notification events, add mobile delivery.** Foreground alerts are the first slice. Background alerts require an always-on observer and APNs delivery; propose an optional push relay for a distributed app, with generic alert text and no terminal traffic passing through it.
- **Android later.** Preserve the backend contract and fixtures across platforms. Accept a future Android client implementation instead of paying for two platforms before the iPhone and iPad terminal experience is proven.

Marcus selected `aerie`, a universal native app, iOS/iPadOS 26 support, Apple team `<APPLE_TEAM_ID>`, bundle identifier `com.haplab.sidecar`, and native Liquid Glass with Sidecar colors. His connected iPhone and iPad run 27 beta; local device proof records that distinction. Public-supported-OS hardware proof is a distribution gate, not a reason to stop M0/M1. The optional push relay and its operator remain an M3 decision. App Store Connect tooling is useful for distribution, but its authentication and app record are not prerequisites for local M0/M1 work.

### SwiftUI versus React Native

| Choice | Benefit for this product | Cost | Recommendation |
| --- | --- | --- | --- |
| SwiftUI + UIKit terminal | Direct control over text input, keyboard accessories, lifecycle, notifications, and accessibility; a small native list/settings shell | Android would need a separate client and terminal adapter | Start here |
| React Native + terminal component | Shares much of the application shell if Android becomes a near-term commitment | Terminal integration and platform behavior still need native work or a WebView; adds another runtime and integration boundary | Reconsider if simultaneous Android delivery becomes a requirement |

This is a product-specific judgment: the terminal and iOS interaction details dominate the risk, while the shareable list UI is relatively small. [SwiftTerm](https://github.com/migueldeicaza/SwiftTerm) provides an iOS UIKit frontend and an embeddable emulator; it does not supply Sidecar's networking or tmux ownership rules. [React Native's native-platform documentation](https://reactnative.dev/docs/native-platform) describes the native modules and components that remain available when platform-specific integration is required.

### Jumar is the starting point for the iOS client

Marcus has the upstream author's permission to include Jumar in the mobile app. Inspection of the local checkout at `~/code/jumar`, revision `ae72637`, supports reusing it: it is already a SwiftUI iOS SSH terminal client designed for terminal runtimes on a tailnet. Its current navigation is machine → terminal, with a configurable startup command. The native Herdr workspace list and control-stream attachment are roadmap items, not a ready-made Sidecar Sessions implementation.

| Jumar component | Reuse for Sidecar | Integration boundary |
| --- | --- | --- |
| `Packages/JumarCore/Sources/JumarTransport` | Terminal transport/event vocabulary | Implement a Sidecar attachment transport with explicit target generation, control state, and reseed handling |
| `Packages/JumarCore/Sources/JumarSSH` | SwiftNIO SSH connection, host-key prompts, child channels, PTY and exec infrastructure | Carry the mobile API without a PTY; use Sidecar's terminal API instead of a generic startup command for managed attachment |
| `jumar/Features/Terminal` | SwiftTerm wrapper, key accessory bar, touch scrolling, keyboard sizing, display batching, and terminal chrome | Keep Sidecar's session identity and control state separate from rendering; review buffering and lifecycle assumptions |
| `Packages/JumarCore/Sources/JumarModel` | Inspectable JSON persistence and Keychain references | Store phone preferences and connection metadata; do not turn phone records into the authority for host sessions |
| `Packages/JumarCore/Sources/JumarNotify` | Optional terminal attention presentation support | Sidecar lifecycle events remain the authority; terminal escape alerts must not create duplicate lifecycle notifications or stand in for APNs |

Recommend a Sidecar-branded client assembled from these reusable modules and selected terminal UI components, rather than maintaining a wholesale Jumar fork with unrelated Herdr assumptions. Prefer an upstream reusable package when the upstream author's release provides one; otherwise record a small attributed source import and its revision so upstream fixes remain traceable. Keep a `SidecarClient` and Sidecar terminal transport separate from `JumarHerdr`, and retain the upstream author's renderer/SSH adapters rather than rewriting their underlying libraries. Keep `../jumar` read-only. Any local source import belongs to `../sidecar-mobile` with its upstream paths, revision, local changes, and attribution recorded in a provenance manifest; distribution still requires concrete reuse terms.

The baseline app uses password SSH authentication; the SSH layer accepts injected private keys, but app key generation/enrollment is still work for the beta. A controlled M0 proof may use the existing password path. The app project declares iOS 26.0, its README/CLAUDE guidance says 26.5, and its core package declares iOS 18; a core declaration does not establish that the full terminal UI supports older phones. Its resolved SwiftTerm version is 1.18.0 and its NIO SSH dependency is pinned to 0.15.0. Preserve a known baseline for M0 rather than combining the integration with a terminal-library major upgrade. The app already declares device families `1,2`, but that is not iPad interaction proof. Sidecar's selected iOS/iPadOS 26.0 target must compile against the actual reused UI rather than inherit these inconsistent declarations. Guard any APIs introduced after 26.0 or provide a fallback; a 26.5 simulator build alone does not prove the 26.0 availability boundary.

Reuse is not proof of Sidecar behavior. Jumar currently creates an SSH connection per terminal screen, marks a backgrounded connection degraded without implementing automatic session reattachment, and sends PTY window changes without Sidecar's lease policy. Its screen model does not apply the transport's remote-resize event, and the raw event stream is unbounded. The Sidecar adapter must implement accepted-geometry feedback, foreground/reconnect identity validation, lease release/expiry, and bounded flow control with explicit reseeds. `JumarNotify` currently supplies in-app attention and haptics from terminal escape sequences, not native iOS notification delivery. These are integration tasks in M0/M2/M3, not inherited guarantees.

Jumar's root currently has no license file; its README lists licenses for dependencies only. the upstream author's reported permission is sufficient to explore this reuse in the plan. Before distributing imported code, record the concrete reuse terms/license and attribution with the upstream author's release. This is a distribution prerequisite, not a reason to block local exploration, M0, or M1. Do not invent an open-source license or silently apply Sidecar's license to imported Jumar code. Device dictation is already present in Jumar but remains outside Sidecar mobile's required first-version scope.

## First-version experience

### Universal native layout

Use one selected terminal and one shared client state model across device classes. On iPhone and compact iPad windows, use a Sessions navigation stack that pushes a terminal detail. On a regular-width iPad, use a native split view with Sessions in the sidebar and the selected terminal in the detail, with explicit controls to collapse and restore the Sessions sidebar. Resizing a window, hiding the sidebar, or rotating must preserve selection and invoke the same measured-viewport resize policy as the keyboard. Do not create two terminal attachments when layout changes.

Use platform navigation, sheets, menus, materials, accessibility, and SF Symbols for the native chrome. Read the actual palette from [`SidecarModernTheme`](../../../internal/styles/themes.go), not a guessed approximation. Keep the terminal grid opaque and faithful to host colors; Liquid Glass belongs to the controls around it. Browser mockups are for agreeing on screens and states; SwiftUI previews and simulator/device proof settle native material, keyboard, safe-area, split-view, and reduced-transparency behavior.

Use the connection heading “Connect Sidecar to see all your sessions.” Omit a duplicate Sidecar navigation title on this screen. Label password entry “Login password” and explain that a Mac connection uses the login password for the selected Mac user. Keep in-app copy informative and omit marketing taglines, including the Settings subtitle. Explain shared terminal sizing on takeover until the user explicitly selects “Don't show this again”; accepting the explanation without that choice must not suppress later explanations, and cancelling must not save the choice. Persist this explicit preference per device. This supersedes automatic suppression after the first takeover. While controlling a terminal, place Release in the terminal header and remove the large lower control capsule to preserve vertical room for output and the keyboard. In phone landscape, place the Sessions title, search, and filter on one row to conserve vertical space.

Mock these reviewable states first: hub connection and host-key verification; Sessions with needs-input, working, idle, plain-shell, and stale-host rows; ambiguous worktree picker; terminal in view and control modes; reconnect/target-gone states; compact iPhone and regular/narrow iPad layouts. Attention and Settings can be previewed for the full journey without implying M2/M3 implementation.

### Connect

Add one hub by LAN address or Tailscale hostname, authenticate with SSH, verify its host-key fingerprint, and check the Sidecar protocol and host capabilities. An app-generated SSH key with an explicit host-side enrollment step is the preferred onboarding direction; secrets live in the iOS Keychain. A connection failure distinguishes network access, authentication, host-key change, missing Sidecar, and incompatible protocol.

The hub must be reachable and awake. It uses its own existing SSH configuration and credentials to reach registered hosts; the phone does not need a copy of every remote private key. The first version trusts the hub with access to the same sessions that its desktop Sidecar can access. Direct phone-to-each-host connections, multi-hub merging, and automatic network discovery are later options.

On LAN, declare and explain local-network access and provide recovery for a denied permission. Tailscale is an existing network prerequisite, not a VPN built into the app. Test its actual routing and permission behavior on a device; do not assume Bonjour or network membership authenticates a machine. See [Apple's local-network privacy guidance](https://developer.apple.com/documentation/technotes/tn3179-understanding-local-network-privacy).

### Browse Sessions

- Show shells and worktrees across the hub and its enabled hosts, including plain shells. “All agents” means the provider-independent inventory Sidecar can discover, not arbitrary processes outside its supported project/session model.
- Each row shows name, project/worktree, host, provider when known, activity state, and freshness. Preserve unknown/degraded state instead of inventing lifecycle certainty on the phone.
- Offer Activity, Project, Recent, and Name sorting, matching Sessions semantics, plus host/provider/state filters and search. Persist these view preferences on the phone; do not change the desktop's sort order. Extract any Go sort semantics needed by the API rather than maintaining a second activity-ranking rule in Swift.
- Keep unreachable hosts visible with a stale/unavailable state. A connected empty inventory is different from a host that cannot be reached. Cache enough row metadata to explain a disconnect; avoid persistent terminal transcripts by default.
- Tap a live shell to open its terminal. A worktree with several possible panes offers a picker; an ambiguous or non-live target never silently attaches to whichever pane happens to share a name. A worktree with no terminal remains inspectable and says no terminal is running. Starting sessions is outside the initial scope.

### Work in a terminal

Use a full-screen terminal with a small native header for host/session, connection and control state, and back navigation. Supply Esc, Tab, Ctrl, arrows, paste, and keyboard dismissal without requiring a hardware keyboard. Support normal hardware-keyboard input, selection/copy, Unicode and IME composition, multiline bracketed paste, terminal scrollback, alternate-screen programs, and safe link opening.

Tapping a row opens the terminal without immediately showing the keyboard. Deliberately entering input mode claims control and requests the phone's usable cell dimensions. A desktop may show the narrower terminal while the phone owns geometry; tmux has one actual pane size. A non-owner views the owner's size using a fit/pan presentation and does not resize it merely to render a preview.

Back navigation, backgrounding, a lost connection, or target replacement disables input and releases ownership when possible; bounded expiry covers a phone that disappears without sending a release. Foregrounding refreshes target identity and screen state before accepting new input. Never replay unacknowledged keystrokes after reconnect: the command may already have executed.

### Receive and act on alerts

Start with agent-needs-input and turn-finished events. Offer per-device event/source preferences, notification permission status, and a test alert. Suppress a redundant banner for the session actively being viewed while retaining its attention state. Opening an alert revalidates its host and session identity; a deleted session or unreachable hub produces an explanation rather than attaching to a replacement.

Notification taps navigate only. Answering a prompt happens in the terminal; notification actions do not automatically approve an agent operation.

## Existing implementation and the gaps

Source inspection baseline: Sidecar `352c7eb7` and Jumar `ae72637bb88736a71f8ce4822ebe7d7d31f83b5d`, inspected 2026-09-07. Detailed evidence and identified extraction limits are in [M0/M1 execution](sidecar-mobile/execution.md). Related plans retain authority over their own work: [remote hosts](sidecar-remote-hosts.md), [remote shell improvements](remote-shells-improvements.md), [notification delivery](notification-sounds-and-native-delivery.md), and [agent lifecycle hooks](notification-agent-lifecycle-hooks.md). Their presence in `active/` is not evidence that every described phase remains unimplemented. Recheck shipped source before starting a slice.

| Concern | Existing source | Mobile work |
| --- | --- | --- |
| Shared Sessions data | [`workspaceinventory`](../../../internal/workspaceinventory/inventory.go), [`overview/workspaces.go`](../../../internal/overview/workspaces.go), [`workspacelist`](../../../internal/workspacelist/list.go) | Headless aggregate query with stable identities, freshness, capabilities, and shared sort semantics |
| Remote inventory and routing | [`hosts/registry.go`](../../../internal/hosts/registry.go), [`hosts/run.go`](../../../internal/hosts/run.go), [`hostserve`](../../../internal/hostserve/serve.go), [`hostproto`](../../../internal/hostproto/hostproto.go) | Compose local and remote observations without constructing a Bubble Tea model; retain host failure states and version checks |
| Terminal transport and screen continuity | [`tty/control_remote.go`](../../../internal/tty/control_remote.go), [`control_manager.go`](../../../internal/tty/control_manager.go), [`control_model.go`](../../../internal/tty/control_model.go) | Extract/adapt a headless terminal session boundary; supply an emulator-compatible screen seed and ordered output/input transport |
| Geometry ownership | [`tty/geometry_lease.go`](../../../internal/tty/geometry_lease.go) | Represent mobile viewers, foreground/input evidence, disconnect expiry, and resize feedback through the existing ownership policy |
| Agent notification facts | [`hostserve/notify.go`](../../../internal/hostserve/notify.go), [`hostproto.NotifyEvent`](../../../internal/hostproto/hostproto.go), [`notify`](../../../internal/notify) | Consume settled events and withdrawals without inferring alerts from inventory snapshots |
| Delivery and receipts | [`notifydelivery`](../../../internal/notifydelivery) | Device-scoped delivery, a persistent background observer, APNs provider adapter, and notification deep links |

The host observation stream currently has protocol version 2 and no general client request channel. It is not a mobile terminal API. Keep its observation contract intact and introduce the mobile API separately. Remote operations must continue to run on the owning host through shared Sidecar/tmux paths.

The Go terminal code already has valuable ordering, seed/reseed, and lease machinery, but some orchestration lives on `tty.Model`. A Swift terminal cannot directly consume a Go screen model or tmux control-mode framing. Jumar supplies a functioning generic PTY terminal, not this Sidecar attachment bridge. Extract the necessary core seam and prove the terminal adapter before treating mobile attachment as solved.

## Backend and transport shape

```text
iPhone / iPad: SwiftUI + terminal adapter
             |
             | authenticated SSH, private network
             v
Sidecar hub: headless mobile API
   | local inventory / terminal backend
   | existing hosts.Registry + SSH
   +---------------------------------> Sidecar + tmux on other hosts

For optional background alerts:
resident observer on hub -> push provider/relay -> APNs -> iPhone / iPad
```

Use one hub to reuse the configured cross-host Sessions view and keep Go's OpenSSH-dependent host client off the phone. This adds a dependency on hub availability and an extra hop for remote terminals. Measure that hop in the first real-device slice; if it is unacceptable, revisit direct host connections at the transport seam rather than duplicating core rules.

### Shared core and non-interactive access

The new headless session service owns aggregation, validated target handles, mobile terminal lifecycle, and device registrations. Keep it independent of SwiftUI and Bubble Tea. Reuse existing collectors, lifecycle resolution, host routing, and lease decisions. Desktop adoption is required wherever extracting a decision would otherwise leave two copies of that rule; this does not require rewriting Sessions wholesale.

Proposed command shapes, to finalize after the first spike:

```text
sidecar mobile serve --stdio
sidecar mobile sessions --json [--sort activity|project|recent|name]
sidecar mobile status --json
sidecar mobile devices list|revoke ... --json
sidecar mobile notifications test ... --json
```

The stdio API is itself a documented non-interactive path for terminal open/input/resize/release. Do not add a duplicate prompt engine: existing agent CLI verbs remain the path for structured agent actions. Publish protocol fixtures and refusal codes alongside CLI help. New mobile-owned rules belong behind this core API; platform permission dialogs and phone-only presentation remain client responsibilities.

### Initial wire contract

Use a bounded versioned JSONL envelope over an SSH exec channel, with explicit request IDs, errors, and capabilities. Separate inventory/event traffic from the selected terminal's traffic using separate SSH channels so a noisy agent cannot block the list. Base64 terminal byte chunks are acceptable for the first proof; measure throughput before adding a binary transport.

The M0 contract covers hello/capabilities, one-row target resolution, terminal open/close, output reset/seed and chunks, ordered input, resize request/result, and control state. M1 adds catalog snapshots/updates and query preferences. M2 adds attention events/withdrawals; M3 adds device registration and delivery operations. Do not make future milestone messages or storage prerequisites for M0. Key every terminal handle by hub identity, owning host identity/configuration generation, server incarnation, workspace/session identity, pane identity, and attachment generation. A displayed name or bare `%pane` ID is never sufficient authority. Give the hub's own machine an explicit wire identity rather than leaving “local” relative to the phone.

The host validates every input and resize against the current target and attachment. Sequence output within an attachment, acknowledge accepted operations without implying an agent processed them, and invalidate all queued input when the handle or ownership changes. On a gap, overflow, tmux pause, or reconnect, suspend the stream and issue an explicit reset/reseed. Bound buffers and scrollback requests; an invisible terminal should not keep streaming output.

SSH supplies encryption and user authentication on both LAN and Tailscale. Validate host keys, store the device private key in Keychain, and make revocation concrete. Prefer a dedicated mobile key restricted to the Sidecar mobile entry point where practical; explicitly document the account authority granted. Do not disable host-key checking or interpolate client-provided paths and labels into shell command strings. The mobile service accepts validated operations and registered hosts, not an arbitrary SSH destination supplied by an unauthenticated request.

### Terminal fidelity and geometry

The first technical gate is a working emulator stream. Reuse the Go control-mode transaction and seed machinery, but define how a Swift emulator receives the initial screen, cursor, rendition, terminal modes, and ordered subsequent output. `capture-pane` text alone is not a complete terminal seed. Treat mode state, alternate screen, terminal replies, scrollback, and output racing the seed as part of the contract, and ensure only the intended consumer answers terminal queries. Choose the exact seed serialization in the [two-consumer spike](sidecar-mobile/execution.md#terminal-seed-decision-gate) based on real agent TUIs. The current Go seed is not a complete serializable emulator checkpoint: missing state must be proved reconstructable or the transport must use a normalized frame strategy. Do not freeze the production stream contract before that evidence. Do not feed raw tmux control records to SwiftTerm or replay an unbounded transcript to reconstruct the screen.

Keep geometry policy in Go. Compute desired columns/rows from the terminal's measured font and actual viewport after safe areas, header, orientation, and keyboard layout settle. Debounce changes, then apply only with a valid lease and target generation; return the actual applied dimensions. Preserve existing minimums and explain/refit when the requested size cannot be applied. Never enforce a tiny viewport by bypassing tmux or Sidecar floors.

The existing lease identity expects a host/PID shape. Allocate a unique server-side viewer identity per mobile attachment and audit its liveness semantics; do not use the hub process's continued existence as proof that the phone is still present. Mobile heartbeat/input evidence stops when the app backgrounds or disconnects, and explicit takeover/restoration must obey the same rules as desktop viewers. Lease-read failure cannot authorize resize or restoration. A stale cleanup must not restore an old desktop size over a newer owner's viewport.

If the phone owns a geometry lease, host-side `sidecar open` and layout requests must still report truth. The initial mobile viewer does not implement content panes or layouts: advertise that limitation and refuse unsupported requests promptly. Never claim desktop viewer presence or queue a request that cannot be displayed.

## Notifications are reusable events, new delivery

Sidecar already produces settled lifecycle transitions and has desktop delivery policy. A native desktop notification does not become an iPhone notification automatically. An iOS app cannot be treated as an always-running SSH subscriber once backgrounded. Apple's remote notification path uses a provider server and APNs; silent background updates are throttled and not guaranteed, so they are not a substitute for user-visible push alerts. See [remote notification servers](https://developer.apple.com/documentation/usernotifications/setting-up-a-remote-notification-server) and [background update limits](https://developer.apple.com/documentation/usernotifications/pushing-background-updates-to-your-app).

| Mode | Behavior | Requirement |
| --- | --- | --- |
| Private-network foreground mode | Current Sessions, interactive terminal, in-app alerts while connected; refreshed state after reopening | Reachable hub; no push infrastructure |
| Background alerts | User-visible notifications while the phone app is inactive, subject to iOS permission, Focus, network, and APNs delivery behavior | An awake observing hub, outbound internet, device registration, and an APNs provider |

Recommend an optional small authenticated push relay for the distributed app. Keep the app publisher's APNs signing credentials on that provider, never in the phone binary or shipped to users' hubs. A personal developer-signed build can use a self-hosted provider with the user's own Apple credentials. A relay is a deployment choice, not an APNs requirement: the hub itself could be that provider for such a personal build.

Default payload: generic “An agent needs input” or “An agent finished,” an opaque event reference, and a device-specific destination mapping. Do not send terminal contents, prompts, repository paths, or conversation references through the relay. The relay has no credentials or route for terminal access. Register device tokens over the authenticated private connection, bind relay submissions to enrolled devices, and support revocation, token rotation, bounded retention, and basic abuse limits.

Background delivery needs a resident hub observer even when no desktop TUI or phone SSH channel exists. Add that process only with the background-alert slice, using launchd/systemd as appropriate and the same headless core. Once installed, it owns the mobile event stream; foreground connections subscribe to it rather than spawning a second notification tracker. Keep the service's local control socket user-scoped and private. Installation/status/removal need explicit CLI operations with inspectable configuration.

The current `hostserve` notifier baselines existing state on startup and forwards transient events; it is not a durable missed-notification queue. Persist newly observed mobile events and per-device delivery receipts through a narrow store, starting with bounded JSONL. Preserve stable event identity across foreground and push delivery, and keep mobile receipts distinct from desktop receipts so one channel cannot consume another's alert. Reuse the existing waiting/finished/withdrawal semantics. Reconnecting does not synthesize fresh alerts from a snapshot; if the observer was down, show current attention state on reconnect and do not promise recovery of every historical transition.

Withdrawals cancel pending delivery and clear in-app attention. A delivered iOS banner may remain until the app can reconcile it; a notification tap always checks current state. Expire superseded waiting alerts, bound retries, and deduplicate foreground/push races. Report provider acceptance separately from phone delivery. The phone may receive an alert away from LAN/Tailscale but cannot open the terminal until the private route is available.

## Delivery sequence and acceptance evidence

### M0: one real terminal in the universal app

First pass the isolated Go/SwiftTerm seed gate, then establish a reproducible Jumar component baseline with focused offline tests. Build the narrow path from authenticated SSH to a native Sidecar row on `aerie`, using the universal compact/regular layouts. Resolve one existing managed shell, render its terminal, type into an agent, and return safely. Use the proposed Go terminal boundary and Jumar transport adapter, not a prerecorded view or a bare `tmux attach` that bypasses Sidecar ownership. Show initial screen correctness, input ordering, keyboard and orientation resize, background/reconnect behavior, and desktop-to-phone-to-desktop control handoff at deliberately different dimensions. Measure connect-to-usable-screen time, keystroke-to-frame latency, and output throughput. The remote-host extra hop is measured when M1 adds remote terminal attachment.

**Exit:** a real agent conversation is usable on a physical iPhone, survives reconnect without replayed input or a new agent process, and does not fight desktop geometry. Also prove the same client at regular and compact iPad widths in the simulator and on the connected physical iPad. Local physical proof may use the user's 27 beta devices with the 26.5 simulator baseline; record the missing public-supported-OS hardware evidence for M4 before distribution. Record the reused Jumar revision/components, pinned terminal and SSH library choices, supported terminal/mode subset, and observed limitations. If seed fidelity or phone input fails this gate, resolve it before building the full list.

### M1: Sessions across hosts

After M0 terminal evidence establishes the attachment shape, compose the hub's local inventory and registered remote hosts, provide native sorting/filtering/search, and implement stale hosts, ambiguous worktrees, disappeared targets, and provider capability states. Reuse shared ordering and target rules. Exercise local and remote shells, worktrees with zero/one/multiple candidate panes, and multiple supported agent providers.

**Exit:** compare the iPhone/iPad catalog with desktop Sessions over the same configuration; every row resolves to the intended host and pane. Prove iPad split-view collapse/window resizing and iPhone return navigation without creating extra attachments or losing list state. Include identically named sessions and reused pane IDs on two hosts, host retargeting, network loss, and incompatible Sidecar versions. A remote failure must never fall back to local tmux.

### M2: foreground attention and reconnect polish

Consume the existing settled notification events, implement the native attention view, permissions/preferences, active-session suppression, test alerts, and exact-target navigation. Finish selection, copy/paste, Unicode input, hardware keyboard behavior, accessibility, and bounded scrollback.

**Exit:** observe a real needs-input event, navigate to it, answer it, and see attention clear. Reconnect without re-alerting historical state. This is a useful foreground alpha, with background-alert limits stated plainly.

### M3: background notification journey

After settling the relay decision, add the resident observer, device enrollment/revocation, bounded event/receipt persistence, APNs adapter, and provider deployment. Keep terminal access private. Complete the JSON/help surfaces for all mobile-owned operations.

**Exit:** with the phone locked and desktop TUI closed, a real agent transition yields a push alert, tapping it restores the correct live session over LAN/Tailscale, and answering clears current attention. Also prove denied permissions, token rotation, observer restart, duplicate delivery, withdrawal, expired events, unreachable hub, and offline phone. Distinguish APNs acceptance from observed device delivery. If strict private-network-only operation is chosen, mark background alerts unavailable rather than declaring this milestone implemented.

### M4: iOS beta and operational proof

Package a TestFlight build and document hub prerequisites, SSH enrollment, optional observer service, Tailscale/LAN setup, and troubleshooting. Verify the iOS/iPadOS 26.0 deployment target against all pinned libraries and public-supported-OS physical devices. Resolve the stable-device coverage gap from M0 before distributing the beta; the user's 27 beta devices provide additional forward-compatibility evidence. Test on the smallest supported iPhone and an iPad, portrait and landscape, full-screen and narrow iPad windows, software and hardware keyboards, and real macOS/Linux hosts supported by Sidecar. Keep protocol compatibility explicit because app and hub updates happen independently.

**Exit:** a clean-device setup completes the browse → terminal → background alert → return journey, and service/device removal leaves the user's shells running. Capture a short demo and evidence in the plan before calling the first version complete.

## Verification discipline

Add focused Go contract tests for target identity, ownership transitions, stale commands, seed boundaries, and event deduplication; share protocol fixtures with Swift decoding tests. Use native UI automation for list and keyboard flows, and physical-device proof for terminal interaction and push delivery. Check existing desktop Sessions and project Workspace after any shared-core extraction.

Every tmux proof isolates both its tmux socket and Sidecar state/config tree, on the hub and every remote host. Use the repository's isolation helpers and inspect resolved paths first. Clear inherited `TMUX`/`TMUX_PANE` or address the private socket explicitly, including cleanup. Never stop or replace the default tmux server. Extend `scripts/demo.sh` with a mobile-connectable isolated fixture when implementation starts; no demo should attach to production sessions by default.

## Deferred scope and decisions

Defer session/worktree creation and deletion, git/file/note panes, desktop pane layouts, conversation history UI, voice input, widgets, Android, arbitrary internet-exposed terminal hosting, and moving live agent processes between hosts. The terminal remains provider-independent and can interact with whatever supported agent is already running.

The hub is `aerie`; confirm reachability, SSH enrollment, and awake behavior during M0 device preparation. The deployment target is iOS/iPadOS 26.0, with a public 26.5 simulator baseline and labeled 27 beta device proof. Apple team `<APPLE_TEAM_ID>` and bundle identifier `com.haplab.sidecar` are selected; development signing and installation have succeeded on both personal devices. App Store Connect authentication remains a distribution detail. M0-A/B independently approved normalized terminal frames for production extraction. The resulting M0-C Go service (`cf77be7a`) and M0-D native client (`../sidecar-mobile` commit `4335ff9`) are independently approved. The simulator has proved live input, keyboard resize, fresh-process reconnect and desktop/mobile conversation handoff. Physical M0-E is underway with authenticated iPhone input, rotation and desktop takeover observed, along with a physical fresh-process reconnect; iPad checks plus a normal-output capture-coalescing repair remain open. Local catalog child M1-A1 is approved at `4ab09264`; cross-host A2 and native Sessions integration remain M1 work. Resolve the optional relay/operator only for M3, and concrete Jumar reuse terms and App Store distribution requirements before their first dependent action. No cloud account system, push service, database migration, or App Store record is required to start the isolated steel-thread work.
