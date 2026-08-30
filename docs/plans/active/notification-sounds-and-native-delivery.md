# Notification sounds and native delivery

**Status:** In progress — M0 through M3 complete; M4 local release readiness, M5 SSH delivery, and M6 deterministic agent lifecycle reporting remain **Tracking:** `td-95db32`, implementation epics `td-7b9ccc` and `td-eb6475`, SSH planning `td-58679c`, lifecycle-hook planning `td-43a93f` **Created:** 2026-08-28 **Updated:** 2026-08-30

This is the controlling plan for Sidecar notification sounds, native operating-system notifications, and their Configuration experience. It supersedes the unimplemented terminal-BEL configuration and OSC desktop-notification phases in [Notifications — toasts, centre, indicator, sources](../implemented/notifications.md). The existing in-app notification centre, toast renderer, JSONL store, calls to action, and agent-transition triggers remain the foundation.

## Outcome

When an agent in a background workspace needs input, finishes a turn, or loses its session, Sidecar can play a short event-appropriate sound and post a native desktop notification. The event still appears in Sidecar's existing toast and notification centre according to the in-app policy; an external delivery never replaces, dismisses, or marks the stored notification read.

A user can turn sounds and native notifications off, limit either channel to background events, or allow it always. They can configure the agent-event rules, preview each sound, send a test desktop notification, inspect provider availability, set quiet hours, and choose custom sound files from the ordinary full-frame Configuration surface. The same owned behavior has deterministic CLI and JSON configuration paths for agents and diagnostics.

When work runs on a registered Sidecar remote host, its live notification event can cross the existing SSH host stream to the local Sidecar viewer. The local process remains responsible for storing the notification, deciding whether it is background work, claiming delivery, and invoking local sound/native adapters. A separately enabled terminal transport gives a Sidecar process running directly inside an unmanaged SSH session a best-effort outer-terminal notification without invoking desktop or audio services on the remote machine.

The smallest valuable journey is:

1. Open **Configuration → Notifications** and set **Sounds** and **System notifications** to **Background only**.
2. Leave an agent working in one workspace and focus another workspace or blur Sidecar.
3. When the agent settles into **Needs input**, hear the attention cue once, receive one native notification, and retain the sticky Sidecar notification in the centre.
4. Click the native notification to return to the hosting terminal when the platform provider supports activation, then use Sidecar's existing notification target to open the relevant session.
5. Return to Configuration and test, mute, or change the rule without restarting Sidecar.

The remote-host steel thread is:

1. Register a remote host, enable managed-host notifications locally, and keep a local Sidecar viewer attached.
2. Let an agent on that host settle into **Needs input** while its remote workspace is not the visible origin.
3. The remote `sidecar host serve --stdio` process emits one bounded typed notification event over its existing SSH stream.
4. The local viewer records the notification and invokes its existing local sound/native delivery path once.
5. Focusing that remote workspace suppresses `background` delivery; disconnecting and reconnecting does not replay events that occurred while no viewer was attached.

## Progress

| Milestone | Status | Current evidence or remaining outcome |
| --- | --- | --- |
| M0 — contracts, config, dedupe | Complete | Shared policy, attention records, JSONL claims/receipts, logical dedupe, and restart-safe waiting lifecycle are implemented and reviewed. |
| M1 — macOS steel thread | Complete | Embedded cues, sound arbitration, native adapters, app/CLI delivery, root Configuration, and focused tests are implemented and reviewed. |
| M2 — full Configuration | Complete | Child routes, source rules, quiet hours, custom sounds, CLI parity, live saves, responsive rendering, and isolated UI proof are complete. |
| M3 — Linux and hardening | Complete | Linux providers, failure/degradation paths, release-architecture builds, burst behavior, and broad automated gates are complete. |
| M4 — integrated release proof | In progress | Automated integrated gates and isolated Configuration proof are recorded; real sound/native background-journey proof plus final documentation and release-readiness closure remain. |
| M5 — SSH delivery | Planned | Managed-host typed forwarding and opt-in direct-terminal delivery are specified below and not yet implemented. |
| M6 — deterministic agent lifecycle reporting | Planned | [Agent lifecycle hooks](notification-agent-lifecycle-hooks.md) controls the separate initiative for Herdr-like hook reporting, authority arbitration, integration management, and screen-detection fallback. |

## Scope

### In scope

- Sounds for the three agent transitions Sidecar already turns into notifications: needs input, finished, and session ended/failed.
- Native notifications for every registered Sidecar notification source, with conservative defaults that enable agent-transition sources first when the channel is turned on.
- One shared, state-free delivery policy across the TUI, CLI fallback, tests, and future headless callers.
- macOS and Linux adapters, matching Sidecar's release targets. macOS is the first real-platform steel thread.
- Background-only gating using Sidecar's existing terminal focus reports plus the currently visible workspace/session.
- Cross-process claiming so one logical notification produces at most one sound and one native notification on a host even when several Sidecar processes are running.
- A searchable **Notifications** Configuration page with focused child routes for agent events, other sources, sound choices, and provider status.
- Live config application, read-only capability reporting, test actions, and non-interactive configuration through `sidecar notify`.
- Built-in sound assets plus optional custom sound paths.
- Quiet hours for external channels.
- Managed remote-host notification events over the existing `sidecar host serve --stdio` SSH stream, delivered by the local viewer through the same store, policy, claim, and provider path as local events.
- An explicit terminal-notification transport for a Sidecar process running directly inside SSH, with a fixed supported-terminal matrix and tmux passthrough.
- Focused tests, isolated real-app proof, platform build proof, documentation, independent review, and a user-runnable demo/configuration journey.

### Deliberately out of scope

- Notifications for ordinary `working` or `idle` transitions. Those remain visual state; making every poll-derived state audible would create noise rather than useful attention.
- Replacing Sidecar's notification centre, toast stacking, reveal motion, retention, read semantics, or target activation.
- A second notification store or a second source registry.
- Unconditional OSC or BEL fallback. Direct SSH terminal delivery is a separate opt-in transport limited to known OSC 9/99 encoders; Sidecar does not emit OSC 777 or BEL and never guesses an unsupported terminal.
- Deep-linking directly into a specific Sidecar workspace from a native notification in the first release. macOS can focus a known terminal application; exact target activation stays in Sidecar's existing centre until Sidecar owns a safe registered URL handler.
- Ignoring Do Not Disturb, Focus modes, or the desktop notification daemon's policy.
- Invoking native desktop services or audio players on a remote SSH host. Managed remote-host delivery executes both channels on the local viewer; direct terminal delivery emits only a sanitized notification sequence and does not promise Sidecar sound, activation, or removal.
- A new daemon, listening port, push service, offline queue, or arbitrary remote command channel. Managed delivery reuses the authenticated SSH stdio connection and remains one-way from host server to viewer.
- Windows adapters while Windows is not a Sidecar release target. The adapter contract must make a later Windows implementation additive.
- Per-agent-family overrides in the first release. The stored notification currently carries a source and origin but no structured provider identity; add that only if real use shows that muting one agent family is worth extending the record contract.
- General Configuration-page scrolling. The root and child routes must fit Sidecar's existing non-scrolling detail pane at the supported 60×24 minimum.

## Current state

M0 through M3 are implemented and independently reviewed. The remaining plan should extend these seams rather than reconstruct them:

- `internal/notify` owns the notification record, source registry, immutable resolved policy, JSONL store, state-free centre/toast queries, target reconciliation, logical transition metadata, and `LaneTracker`.
- `internal/notifydelivery` owns the state-free external-delivery decision, host attention records, cross-process JSONL claim ledger, lazy capability probes, platform adapters, asynchronous sound arbitration, and delivery receipts. Direct native and sound delivery deliberately reports unavailable when the current process is inside SSH.
- `internal/app/notifications.go` stores first, renders through the existing toast/centre path, and schedules eligible external work asynchronously. Fresh records discovered through the existing reconciliation sweep use the same live-event and claim rules; old records never replay externally.
- `internal/config.NotificationsConfig`, targeted save helpers, Configuration routes, and `sidecar notify` now cover modes, source rules, quiet hours, custom sounds, provider status, and tests without requiring restart. JSON remains the inspectable repair path.
- macOS and Linux adapters compile for the release architectures without CGO. Fake-runner coverage proves command arguments, degradation, timeouts, replacement/removal, custom-sound fallback, SSH refusal, and non-blocking execution.
- The project Workspace and global Sessions surfaces publish the same selected-origin attention model, so one focused visible origin suppresses duplicate `background` delivery across local Sidecar instances.
- Registered remote hosts already use one long-lived SSH process running `sidecar host serve --stdio`. `internal/hostproto` version 1 sends snapshots plus typed lane transition events, and `internal/hosts.Client` projects them into the local viewer. The stream is read-only and already carries `from`/`to` lane data, but it has no notification event identity/payload and no app binding into the local notification store.
- Remote pane selection is already known locally. Extending notification origin with a stable host identity lets the existing attention model decide whether a remote event is visible without teaching the remote server about local focus.
- The external delivery host starts no background loop of its own. SSH support must continue to ride the existing transition stream, post path, and reconciliation mechanisms.

## Settled product behavior

### Event mapping

The first version derives an external cue from the notification fields Sidecar already stores. It must not parse titles or bodies.

| User event | Existing notification | Default native rule after native delivery is enabled | Default sound rule after sounds are enabled | Sound cue |
| --- | --- | --- | --- | --- |
| Agent needs input | source `waiting`, warning, sticky | On | On | `attention` |
| Agent finished a turn | source `session`, info | On | On | `done` |
| Agent session ended or failed | source `session`, error | On | On | `failure` |
| Agent/CLI informational post | source `agent`, info | On | Off | `none` |
| TD, Tasks, or System | matching source | Off until enabled per source | Off until enabled per source | `none` |

`failure` wins over a configured event-aware source cue whenever severity is error. A user may explicitly select another cue or `none` for a source. Unknown future sources remain stored in the centre and default to no external delivery.

### Channel modes and defaults

Both external channels have the same global mode: `off`, `background`, or `always`.

- **Fresh and existing configurations default to `off`.** A Sidecar upgrade must not suddenly make noise or place potentially sensitive text in Notification Center.
- **Background only** is the recommended UI choice. It delivers when Sidecar is blurred, or when Sidecar is focused but the notification's origin is not the workspace/session currently in front of the user.
- **Always** includes a notification whose origin is visible in the focused Sidecar.
- If origin visibility cannot be resolved, treat the event as background. Missing a requested alert is worse than one conservative extra alert, while the cross-process claim still prevents duplicates.
- A native or sound source switch can only narrow the global channel mode. Turning a channel off globally is authoritative.
- Quiet hours suppress sounds and native delivery, never the in-app toast, centre record, unread indicator, or target.
- An explicit **Test** action bypasses background and quiet-hours suppression so it cannot appear broken while the user is looking at the setting. It still honors channel enablement and provider availability, creates no unread centre item, and reports success/failure as a status flash.

### Foreground and multiple instances

Background is a host-wide fact, not one process's private guess.

1. Each live Sidecar instance publishes a small attention record beside its existing instance-presence record: process ID, focus state, selected origin identity when a workspace/session is actually visible, and update time.
2. The record is updated only on focus/blur, selected workspace/session changes, project/worktree switches, and orderly withdrawal. It is not rewritten on every poll or render.
3. Before resolving a background-only event, a caller lists live same-host attention records. If any focused instance is visibly showing the event's origin, the event is foreground.
4. If no focused instance shows the origin, the event is background even when another Sidecar tab such as Git or Files is focused.
5. Stale or dead records are discarded through the same PID-liveness rule as `internal/uirequest.ListInstances`.

Each external channel then takes an atomic claim on `(notification ID, channel)` before invoking an adapter. Exactly one process wins. A claim is a short lease followed by a success or failure receipt, so a process crash before invocation can be retried but a slow adapter cannot lead another process to double-deliver. Receipts are compacted with the notification's retention window.

Agent triggers additionally carry a short-lived logical dedupe key derived from stable origin + transition class. The notification store suppresses a second equivalent post inside the existing debounce/delivery window even when two Sidecar processes independently observe the same lane change and assign different notification IDs. The dedupe window must allow the same agent to finish another later turn; it is not a permanent identity.

### Live-event rule

External channels announce live events, not backlog.

- A record delivered directly through `notify.PostMsg` is eligible immediately.
- A record discovered by the cross-process sweep or appended by `sidecar notify post` without a TUI is eligible only inside a small configured constant grace window (recommended: 15 seconds from `CreatedAt`).
- Older unread records remain in the centre and unread count but never cause a burst of sound or desktop banners at startup.
- The short-lived CLI process may deliver its own freshly stored record when no TUI accepts it. `background` resolves true when no focused Sidecar instance shows its origin.
- Marking a native delivery or sound receipt never marks the Sidecar notification read. Existing paint/selection semantics remain authoritative.

### Audio behavior

- Ship three short, restrained Sidecar-owned cues: `attention`, `done`, and `failure`, stored as small WAV assets embedded in the binary and materialized into a versioned cache only when first needed. Record the asset source/license in the asset directory.
- Sounds use the system output device and volume. Do not add a Sidecar volume control in the first version.
- An audio arbiter batches events that arrive within a short interval and plays only the highest-priority cue (`failure` > `attention` > `done`). Sounds never overlap or queue into a long sequence.
- Playback is asynchronous. No process lookup, file materialization, or player invocation runs in `Init`, `Start`, `View`, or the synchronous part of `Update`.
- A custom path may override each cue. Resolve `~`; resolve relative paths against the directory containing Sidecar's config file; require a readable regular file; keep the previous value when validation fails.
- Built-in assets use WAV for common player support. Custom paths may be WAV or MP3 where the selected adapter reports support; the Configuration status must say when a file's format is unsupported rather than silently falling back.
- If a custom sound fails at runtime, report the failure once through diagnostics/logging and use the built-in cue for that event. A broken customization should not make the entire channel permanently silent.

### Native notification behavior

- Title and body are sanitized with the same control-character/whitespace rules as in-app notification rendering, then truncated to provider-safe bounds. Do not shell-interpolate user text; always pass argv elements.
- Native urgency follows severity where the provider supports it. Sidecar never bypasses Do Not Disturb or Focus modes.
- Use a stable provider group/replacement key for one workspace origin so a later state can replace a stale earlier banner. When a sticky waiting notification self-dismisses, remove the provider banner where supported.
- Clicking should focus the detected hosting terminal application on macOS when the provider supports an activation bundle ID. It must not execute a notification-supplied shell command. The centre remains the exact-target activation surface.
- Native delivery is privacy-sensitive because the OS may retain text or show previews on a lock screen. The page explains that the title/body leave Sidecar's terminal surface for the local OS notification service; it never uploads content.

### SSH and remote-host delivery

SSH delivery means local attention for remote work. It does not mean attempting to reach a desktop session or audio device on the server.

For registered Sidecar remote hosts, follow the same shape Herdr uses for headless sessions: the remote server forwards a typed semantic event to the attached client, and the attached local client performs delivery.

1. `sidecar host serve --stdio` emits a notification event only from a live lane transition. Initial snapshots, reconnect snapshots, and reconstructed current state never synthesize one.
2. The wire payload contains a stable event key, occurrence time, source, severity/event class, bounded title/body, and a stable remote origin. It does not contain terminal output, prompts, environment data, or a command to execute.
3. `internal/hosts.Client` maps the payload into the same local `notify.Notification` post path used by local work. The local process owns storage, live-event filtering, source/channel policy, quiet hours, visible-origin resolution, claims, and provider invocation.
4. Add host identity to `notify.Origin` and the attention record. A focused local Sidecar showing that remote workspace makes it foreground; merely having the SSH stream connected does not.
5. The remote event key must be stable across concurrent `host serve` processes and reconnects. The local notification ID/dedupe key combines the configured host identity with that remote key, so two local Sidecar processes connected to the same host still produce one local record and one claim winner. Two different local computers may each notify their user; the dedupe boundary is intentionally one destination host.
6. If no local viewer is attached when the transition happens, there is no external delivery and no later replay. The remote agent's current state remains visible when the viewer reconnects.
7. Native and sound providers always execute on the local viewer. M3's fail-closed SSH check remains in both platform adapters and cannot be bypassed by a forwarded payload.

For a Sidecar process running directly inside an ordinary SSH terminal without a registered local viewer, provide a separate opt-in terminal transport modeled on Herdr's terminal notifier:

- Support fixed encoders for Ghostty, iTerm2, WezTerm, and Kitty. Ghostty, iTerm2, and WezTerm use OSC 9; Kitty uses its structured OSC 99 title/body form.
- Auto-detect only from known environment markers. Because SSH commonly drops `TERM_PROGRAM`, allow an explicit fixed terminal choice; `auto` must report unavailable when it cannot identify a supported outer terminal.
- Sanitize ESC, BEL, string terminators, controls, newlines, and tabs before encoding. Keep the encoder state-free and test it byte-for-byte.
- When inside tmux, wrap the sequence with tmux DCS passthrough and double embedded ESC bytes. Tests use an isolated tmux socket; implementation and proof never touch the default tmux server.
- Write through an injected terminal writer and flush from asynchronous delivery work. Do not shell out, write to an arbitrary TTY path, or mix protocol bytes into structured CLI output.
- This transport is best effort: it does not promise Sidecar-owned sound, click activation, replacement/removal, or delivery receipts from the outer terminal. Unsupported terminals remain visibly unavailable instead of falling back to BEL or a generic escape.

## Configuration model

Extend the existing `notifications` section instead of adding `sound`, `alerts`, or platform-specific root sections.

```json
{
  "notifications": {
    "native": {
      "mode": "background",
      "provider": "auto"
    },
    "sound": {
      "mode": "background",
      "attentionPath": "",
      "donePath": "",
      "failurePath": ""
    },
    "quietHours": {
      "enabled": false,
      "start": "22:00",
      "end": "08:00"
    },
    "ssh": {
      "managedHosts": false,
      "terminal": "off"
    },
    "sources": {
      "waiting": {
        "toast": true,
        "native": true,
        "sound": "attention",
        "expiry": "sticky"
      },
      "session": {
        "toast": true,
        "native": true,
        "sound": "event",
        "expiry": "10s"
      },
      "agent": {
        "toast": true,
        "native": true,
        "sound": "none",
        "expiry": "12s"
      }
    }
  }
}
```

Exact field names may change during implementation only if the final names remain plain, stable, and reflected together in loader, saver, Configuration, CLI help, examples, and tests.

Validation rules:

- Modes are exactly `off`, `background`, or `always`.
- Native provider is `auto` in the first version. Provider-specific forcing may exist as a diagnostic-only option, not an ordinary user setting.
- Source `toast` and `native` are booleans. `sound` is `none`, `event`, `attention`, `done`, or `failure`.
- Existing expiry parsing remains: Go duration, `sticky`, `never`, or `0`.
- Quiet-hour times are local wall-clock `HH:MM`. Equal start/end means quiet all day and must be rendered explicitly, not guessed as disabled. The overnight case is supported.
- `ssh.managedHosts` is an explicit boolean and defaults off so an upgrade does not move remote notification text onto the local desktop unexpectedly. It controls local consumption/delivery, not whether the read-only remote status stream exists.
- `ssh.terminal` is `off`, `auto`, `ghostty`, `iterm2`, `wezterm`, or `kitty` and defaults off. A forced provider selects only a fixed encoder; no command, terminal path, or raw escape string is configurable.
- Invalid enum/time/path input is refused before save and leaves the last valid config in force.
- Unknown source IDs survive a read-modify-write. Existing source expiry values survive the schema expansion.
- `config.Save` begins managing `notifications`, and `SaveNotifications(mutate)` reloads immediately before writing like `SaveWorkspace`, `SaveUI`, and `SaveSelection`. No Configuration model writes JSON directly.
- `notify.ApplyConfig` becomes an immutable resolved-policy snapshot rather than a package-level collection of expiry overrides. The app and CLI both replace the snapshot after a successful load/save.

## Configuration experience

Keep **Notifications** after **Agents** in the Sidecar sidebar group. Add every visible control and child route to the static settings index with terms such as `sound`, `audio`, `native`, `desktop`, `system notification`, `waiting`, `finished`, `quiet hours`, `SSH`, `remote host`, `terminal notification`, and provider names.

The root page must fit the supported 60×24 detail pane. It summarizes; child routes carry the dense controls.

```text
Notifications
Choose how Sidecar gets your attention when work changes state.

Delivery
System notifications    Background only ▾
Sounds                  Background only ▾
Quiet hours             Off             ›
SSH delivery            Off             ›

Rules
Agent activity          3 events         ›
Other sources           Agent, TD, Tasks ›

Status & test
Delivery status         Native ready · Sound ready ›
T  Test enabled channels
```

Interaction and layout rules:

- Use the existing `paneBuilder`, `FormRow`, selector/dropdown, button, child-route, editor, confirmation, and mouse-region patterns. Do not create a settings schema, generic table framework, modal, or second key-routing path.
- Global mode selectors save immediately and apply live. The root summary updates from the config reloaded by `ConfigSavedMsg`, never from optimistic local state.
- **Quiet hours** opens a focused child route with Enabled, Start, End, and an explanation that in-app notifications remain. Typed times use `config-edit`; Escape cancels an unsaved edit.
- **SSH delivery** opens a focused child route with separate **Managed hosts** and **Direct terminal** controls. The page explains that managed-host text crosses the existing SSH connection and is delivered by this local Sidecar, while direct-terminal mode emits a sanitized escape sequence and cannot provide Sidecar sound or exact activation.
- **Agent activity** opens a focused list of Needs input, Finished, and Session ended. Each row summarizes Toast / Native / Sound and opens one compact rule editor.
- A rule editor contains In-app toast, System notification, Sound cue, Toast duration, and **Test this event**. The Session rule explains that an error automatically uses the failure cue while `event` is selected.
- **Other sources** uses the same rule editor for Agent posts, TD, Tasks, and System. This is one reusable source-rule view over the source registry, not duplicated page code.
- **Delivery status** runs capability probes in a `tea.Cmd` only after the route opens. It reports the selected native provider, sound player, custom-path validation, local/SSH context, managed-host stream state, selected/detected terminal encoder, and repair guidance.
- On macOS, status distinguishes `terminal-notifier` from the `osascript` fallback and explains the fallback's sender/activation limitation. Offer copy/open guidance for `brew install terminal-notifier`; any install action must use the existing explicit confirmation pattern and must never run automatically.
- On Linux, status names missing `notify-send` or audio players and gives neutral package guidance because package names differ by distribution.
- **Test enabled channels** and each rule's test action use the real resolved policy and real adapters with the explicit-test override. The footer advertises `t Test` only on routes that can test; command IDs, contexts, keymap bindings, handlers, and visible hints must agree.
- Keep the standard `config`, `config-edit`, and `config-confirm` contexts. Ordinary typing must never leak to app or plugin shortcuts.
- At 60×24, help copy may become focus-only detail, but every setting and route remains reachable. At 100×30 and 160×45, the page uses the ordinary fixed label/control grid and does not stretch controls to the right edge.
- The page reports external delivery as disabled/unavailable honestly. An ON pill beside an unavailable provider is a configuration intent plus a warning, not a claim that the last test succeeded.

## Agent and CLI surfaces

The notification capability is Sidecar-owned, so Configuration cannot be its only mutation surface.

Retain the existing commands and add:

```text
sidecar notify config [--json]
sidecar notify config set [--native off|background|always] [--sound off|background|always] [--quiet-hours off|HH:MM-HH:MM] [--ssh-managed on|off] [--ssh-terminal off|auto|ghostty|iterm2|wezterm|kitty] [--json]
sidecar notify source set <source> [--toast on|off] [--native on|off] [--sound none|event|attention|done|failure] [--expiry DURATION|sticky] [--json]
sidecar notify status [--json]
sidecar notify test --channel native|sound|all [--event waiting|done|failure] [--json]
```

- `config` prints resolved settings and defaults without modifying the file.
- `config set` and `source set` call the same validation and targeted save functions as Configuration. They accept no interactive prompts.
- `status` probes adapters and reports availability, selected provider/player/terminal encoder, managed-host configuration and connection state, reason when unavailable, and whether the current process appears remote. It does not send anything.
- `test` is the one explicit external side effect. Human output says what ran; JSON includes per-channel `attempted`, `provider`, `delivered`, and `error` fields with stable exit codes.
- `notify post` continues to post one stored notification. External delivery follows configured policy automatically; do not add force-sound or force-native flags that let an agent bypass user policy.
- All new commands appear in `sidecar agents`, ordinary help, generated CLI reference, and tests.
- JSON configuration remains an inspectable repair path. CLI writes preserve unrelated root keys and unknown source entries.

## Delivery architecture

### Pure policy core

Add a small application-neutral policy layer under `internal/notify` or `internal/notifydelivery/policy.go`:

```text
Resolve(notification, resolved config, runtime context, capabilities, explicit test)
  -> {native decision, sound decision, sound cue, suppression reasons}
```

The function accepts plain values and performs no I/O. It owns global mode, source rules, severity-to-cue mapping, focus/origin visibility, quiet hours, live-event grace, and reason codes. Configuration summaries, CLI status/test output, TUI delivery, and tests consume the same vocabulary.

Use stable suppression reasons such as `channel_off`, `source_off`, `foreground`, `quiet_hours`, `stale`, `unavailable`, `already_claimed`, and `rate_limited`. A later API can expose them without extracting rules from Bubble Tea.

### Adapter boundary

Create `internal/notifydelivery` with narrow replaceable seams:

```go
type NativeNotifier interface {
    Probe(context.Context) Capability
    Deliver(context.Context, Message) (ProviderReceipt, error)
    Remove(context.Context, string) error
}

type SoundPlayer interface {
    Probe(context.Context) Capability
    Play(context.Context, Cue) (ProviderReceipt, error)
}
```

`Message`, `Cue`, `Capability`, and `ProviderReceipt` contain no Bubble Tea, config-loader, platform-command, or terminal-rendering types. The app adapter turns `notify.Notification` plus visible-origin/focus state into a delivery request. The CLI adapter supplies the same request with host attention records.

Inject a runner, clock, asset cache, and capability probe into platform adapters. Tests never invoke a real player, notification service, package manager, browser, or the user's default tmux server.

### macOS adapters

Native provider order:

1. `terminal-notifier` when present and its probe succeeds. Pass title/body/severity/group as argv, use a known terminal bundle identifier for click activation, and never use `-execute`, `-open` with untrusted values, `-sender`, or `-ignoreDnD`.
2. `/usr/bin/osascript` `display notification` as a no-dependency fallback. Build the script as a constant and pass user strings as arguments rather than interpolating AppleScript. Report that the notification may be attributed to Script Editor and cannot return to the hosting terminal.

Sound provider: `/usr/bin/afplay` against the materialized built-in/custom file. Run asynchronously and retain a process handle so the arbiter can prevent overlap. `afplay` availability is expected on supported macOS, but still probe and report rather than assume.

Map only known `TERM_PROGRAM` values to fixed bundle identifiers. An unknown terminal gets a notification without activation; never accept a bundle ID from notification text.

### Linux adapters

Native provider: `notify-send` when a display session exists. Pass Sidecar app name, severity-derived urgency, expiry hint, icon when packaged, and replacement ID where supported. Absence of `DISPLAY` and `WAYLAND_DISPLAY` is an unavailable capability, not a reason to try terminal escapes.

Sound provider order should be a short documented list selected by actual format support, for example `paplay`, `pw-play`, `aplay`, then an already-installed general player such as `ffplay` or `mpv`. Do not add a new Go audio/CGO dependency merely to avoid a subprocess. Probe lazily and cache the answer; retry capability discovery after Configuration asks to recheck.

### SSH transport and local delivery

Extend `internal/hostproto` with a typed notification message and deliberately bump the protocol version. The payload is server-to-viewer only, so this does not wait for or introduce the Phase C request/mutation direction in [Remote hosts](sidecar-remote-hosts.md). Older and newer binaries must fail with the existing actionable version-mismatch error rather than silently dropping a notification kind.

The remote status adapter is the second consumer of `notify.LaneTracker`. It turns a live remote lane transition into a wire event at the authority that observed it, using stable origin + transition class + transition occurrence time for the event key rather than the per-connection sequence or snapshot generation. `internal/hosts.Client` exposes the event as plain data; a small app adapter adds the configured host identity and calls the ordinary local notification post seam. No host protocol, SSH, or remote-pane type enters the policy/store/provider packages.

The direct terminal transport is another narrow notifier adapter with an injected environment, encoder selection, writer, and flush function. It participates in the native-channel decision only when `ssh.terminal` is enabled and the process is remote; it does not weaken the existing remote refusal in macOS/Linux native providers or sound players. Human TUI output and terminal notification bytes share one process, so only the app-owned asynchronous terminal writer may emit the sequence. Machine-readable CLI commands never emit one unless the explicit `notify test` side effect is requested and protocol output remains on a separate stream.

### Delivery coordinator and receipts

Use a separate JSONL-backed delivery ledger under the Sidecar state directory, behind a narrow interface. Do not make transient provider state part of `Notification` or overload read/dismiss events.

Required operations:

- `Claim(notificationID, channel, owner, now, lease) (won bool, reason string, error)` under an exclusive file lock;
- `Complete(notificationID, channel, receipt)`;
- `ReleaseExpired(now)`;
- `List(notificationID)` for diagnostics/tests;
- compaction bounded by notification retention.

The in-memory implementation drives tests. The JSONL implementation follows the locking and repairable-fold conventions in `internal/notify/store.go`. An unreadable line is skipped; failure to open the ledger disables external side effects rather than risking duplicate delivery, while the in-app notification continues normally.

### App lifecycle integration

1. Normalize and store the notification first.
2. Learn whether the post created a new record. Change `Store.Post` to return an explicit created/existing result so an idempotent post cannot replay external channels.
3. Broadcast the existing posted message for in-app rendering and schedule external delivery as a `tea.Cmd`; never invoke providers synchronously in `Update`.
4. On a sweep, compare cache IDs before/after and offer only newly discovered, still-live records to delivery.
5. Resolve host foreground from live attention records, resolve policy, and claim each eligible channel.
6. Invoke native and sound adapters independently. One channel failing must not cancel the other.
7. Complete the receipt and report an adapter failure through debug logs plus a deduplicated diagnostic/status warning. Do not file a stored notification about every notification-delivery failure and create a feedback loop.
8. Reload resolved config and lazy adapter capabilities after `ConfigSavedMsg`; no restart.

For a forwarded remote event, steps 1 through 8 run entirely on the local viewer. The remote host performs no claim and records no delivery receipt; this preserves the ledger's existing one-destination-host semantics and lets each local computer independently decide whether its user needs attention.

The external delivery host must start no background loop of its own. It rides the existing post path and one-second notification reconciliation.

## Security, privacy, and failure rules

- Never shell-compose notification text, paths, app identifiers, or player arguments.
- Strip control characters and OSC sequences before sending text to an OS service.
- Do not include full terminal output, agent prompts, environment variables, or hidden metadata. Use only the already-stored notification title/body and stable local replacement key.
- Treat remote notification text as data crossing an authenticated SSH trust boundary. The wire schema is bounded and typed, the local viewer sanitizes it again before storage/delivery, and no field can select a command, executable, TTY path, bundle identifier, or raw escape sequence.
- Terminal delivery encodes sanitized title/body itself and supports only fixed known sequences. Never pass through notification text that already contains OSC/DCS bytes.
- A reconnect snapshot is state, not an event. Refusing remote backlog prevents a stale prompt from unexpectedly crossing onto the local desktop after attachment.
- Do not expose a notification-click command runner.
- Do not follow symlinks into non-regular custom sound targets without resolving and validating the final file. A config path is user-controlled local input, not permission to execute it.
- External provider failure never drops, dismisses, reads, or delays the in-app notification.
- Capability probes are bounded with context timeouts and run only on demand or first external delivery.
- Log provider, channel, reason, and notification ID; do not log full title/body or custom file contents.
- Native delivery remains off by default and the UI explains OS retention/preview behavior before enabling it.
- Respect the main tmux-server rule throughout implementation and proof. No delivery test needs to restart or replace tmux.

## Work sequence

### M0 — Contracts, configuration, and duplicate prevention — Complete

1. Extend `NotificationsConfig`, loader/raw types, validation, defaults, managed save path, `SaveNotifications`, and config round-trip tests.
2. Extract immutable resolved delivery policy and its reason codes; retain `notify.ExpiryFor` behavior through the new snapshot.
3. Add structured transition/dedupe metadata without changing user-visible notification titles, centre grouping, or CLI list compatibility.
4. Add host attention records and expose the selected visible workspace/session from both project Workspace and global Sessions projections through one app-level shape.
5. Add the delivery-ledger interface, JSONL and memory implementations, lease/receipt compaction, and two-process claim tests.
6. Change store posting to distinguish created from idempotent existing records and add short-window logical-event dedupe.
7. Close the existing restart hole for sticky waiting notifications: seed the lane tracker from the retained logical waiting record so leaving the blocked lane after a restart dismisses the same notification and removes its native replacement group. A user-dismissed wait must stay dismissed until the agent leaves and later enters a new blocked episode.

Exit gate: a pure test matrix can prove foreground/background decisions and one claim winner across two stores/process models before any test can make sound or post to the OS.

### M1 — macOS steel thread through both channels — Complete

1. Add embedded `attention`, `done`, and `failure` assets plus the lazy versioned cache.
2. Implement the asynchronous audio arbiter and macOS `afplay` adapter behind a fake runner.
3. Implement `terminal-notifier` + safe `osascript` fallback, stable grouping/removal, severity mapping, terminal activation mapping, and sanitized bounds.
4. Attach delivery to new in-process posts, freshly swept records, and the no-TUI CLI fallback.
5. Add the root Notifications Configuration page with global modes, concise summaries, delivery status, and explicit Test.
6. Add `sidecar notify config`, global `config set`, `status`, and `test` so the steel thread is independently operable.

Exit gate: on macOS, one blurred/background agent transition produces exactly one appropriate sound and one native notification across two running isolated Sidecar processes; the focused visible-origin case produces neither; both cases retain the normal Sidecar centre record.

### M2 — Full Configuration rules and custom sounds — Complete

1. Add Agent activity, Other sources, Quiet hours, Delivery status, and source-rule child routes.
2. Add source toggles, cue selection, expiry editing, custom cue path editing/validation, per-event tests, and provider repair guidance.
3. Add `sidecar notify source set` and quiet-hours parity; regenerate help and CLI reference.
4. Apply saves live to the app, the CLI fallback, and capability summaries.
5. Add narrow/medium/wide render, keyboard, mouse, hover, editor-precedence, dropdown, search-index, back-stack, and config-preservation coverage.

Exit gate: every control visible in Configuration has the same validation and mutation through the CLI, every route fits at 60×24, and no setting requires restart.

### M3 — Linux parity and operational hardening — Complete

1. Implement `notify-send` capability/delivery and replacement behavior with a fake runner.
2. Implement ordered Linux sound-player capability and playback with format-aware selection.
3. Prove no-display, SSH, missing-player, missing-notify-send, invalid-custom-file, timeout, and provider-failure outcomes.
4. Add `GOOS=darwin` and `GOOS=linux` build/lint coverage for all release architectures already exercised by release tooling.
5. Measure a burst of simultaneous agent transitions and verify batching, no overlapping player processes, no startup subprocesses, bounded ledger growth, and no extra workspace polling.

Exit gate: Darwin and Linux expose the same policy/config/CLI contract, while status explains platform-specific capability rather than pretending parity where a desktop service is absent.

M2/M3 evidence recorded 2026-08-29: focused Configuration, app, CLI, config, keymap, and delivery suites pass; `make lint`, `go test ./...`, and `go build ./...` pass; release-command builds pass for Darwin amd64/arm64 and Linux amd64/arm64; fake-runner tests cover provider argv, capability degradation, timeouts, SSH refusal, custom-sound fallback, replacement compatibility, and live provider removal; isolated `tmux-drive` proof covers the Notifications root and every child route from 60×24 through 160×45 with private state and tmux servers; the demo dry run builds and tears down cleanly. Real provider delivery remains an explicit user-visible M4 action and was not fired during automated proof.

### M4 — Integrated proof, documentation, review, and release readiness — In progress

1. Update Configuration docs, `docs/reference/cli.md`, example config, and the notification architecture plan's superseded pointers.
2. Run focused package tests, `go test ./...`, `go build ./...`, repository lint gates, and a GoReleaser snapshot.
3. Run `scripts/tmux-drive.sh paths` first, then a fully isolated Configuration and notification-centre proof; always run `stop` afterward.
4. Use fake provider runners for automated argv/claim/receipt evidence. Perform real sound and native-notification tests only as explicit user-visible manual actions; record the provider, OS, terminal, focus state, and result.
5. Verify the config page through `./scripts/demo.sh` and give the user the exact route/CLI test commands.
6. Independently review policy semantics, multi-instance claiming, privacy, subprocess safety, platform degradation, Configuration keyboard/mouse behavior, CLI parity, and proof evidence. Fix findings and rerun affected gates.

Exit gate: the integrated candidate has independent approval and evidence for the real background-agent journey, not only adapter unit tests.

### M5 — SSH delivery through local viewers and supported terminals — Planned

1. Add the versioned `hostproto` notification event with stable transition identity, bounded payload validation, server/client fixtures, and explicit old/new protocol mismatch tests. Emit it only for live `LaneTracker` transitions, never snapshots or reconnect state.
2. Extend notification origin and attention identity with a remote host ID, adapt received events into the ordinary local post seam, and prove that local source rules, quiet hours, foreground detection, stale-event rejection, claims, centre records, and providers remain the only policy path.
3. Add `ssh.managedHosts` configuration, the SSH delivery child route, CLI mutation/status parity, live application, privacy copy, connection state, and search terms. Keep it off by default.
4. Prove stable same-machine deduplication with two local Sidecar processes and two independent `host serve` streams. Prove that another local computer may independently deliver, a visible remote pane suppresses `background`, and reconnect/offline state does not replay.
5. Add the opt-in direct-terminal adapter for Ghostty, iTerm2, WezTerm, and Kitty with fixed encoders, explicit/automatic provider selection, byte sanitization, tmux DCS passthrough, injected writer tests, honest unsupported status, and no sound/native-provider fallback on the remote host.
6. Run focused protocol, host-client, policy, config, CLI, and encoder suites; all repository gates; isolated two-process/tmux proof; and a deliberate real SSH proof for one managed host plus each available outer-terminal family. Record unavailable terminal families as untested, not passing.
7. Update remote-host and notification documentation, generated CLI help, example config, and security/privacy language. Independently review protocol compatibility, duplicate prevention, escape safety, focus semantics, config parity, and real proof evidence.

Exit gate: a live remote needs-input transition reaches exactly one local centre record and one set of locally configured external channels without invoking a remote desktop/audio provider; the visible-origin and reconnect cases remain silent; and direct-terminal mode emits exactly one valid sanitized sequence only when explicitly enabled for a supported terminal.

### M6 — Deterministic agent lifecycle reporting — Planned separately

[Agent lifecycle hooks](notification-agent-lifecycle-hooks.md) is the controlling plan for this major initiative. Provider hooks report structured lifecycle facts into Sidecar's shared agent-state core; they never post notifications directly. One authority resolver combines those facts with process and screen observation, and the resulting lane transitions continue through the existing `notify.LaneTracker`, notification store, delivery policy, claims, and M5 transport without a second notification path.

M6 depends on the implemented M0–M3 state and notification seams. Its local steel thread can ship independently of M5; once M5 exists, the remote host resolves hook-backed state locally and forwards the same semantic notification event as any screen-backed transition. Screen detection remains the honest fallback for missing, partial, stale, conflicting, or failed integrations.

Exit gate: an installed full-lifecycle integration can drive one needs-input and one finished transition without screen-text heuristics, disagreement from the screen cannot duplicate or reverse a fresh authoritative report, and removing or breaking the integration returns that pane to the existing fallback with an actionable status.

## Test matrix

### Policy and config

- All global modes × source enabled/disabled × focused/blurred × origin visible/hidden.
- Quiet-hours outside, inside, overnight, and equal-start/end all-day behavior.
- Waiting/info/error cue selection and explicit source cue overrides.
- Stale discovered records, live direct posts, explicit tests, unknown sources, and unavailable providers.
- Existing expiry-only config loads unchanged; save preserves unrelated root keys and unknown source IDs.
- Invalid mode, time, expiry, cue, and custom path leave prior config untouched.

### Coordination

- Two live instances race for sound and native: one winner per channel.
- A sound winner does not imply a native winner; channels fail independently.
- Expired lease is recoverable; completed receipt is not redelivered after restart.
- Idempotent notification ID and two logical duplicate transition posts do not replay delivery.
- The same workspace can finish a later turn outside the dedupe window.
- A focused instance showing the origin suppresses background delivery from a blurred sibling instance.
- A focused instance showing Git while the origin is a background workspace allows delivery.
- A CLI fallback with no TUI delivers once if fresh and never replays when a TUI starts.

### Adapters

- Every provider receives argv elements with control/OSC-free, bounded text; no shell command string.
- macOS provider preference, `osascript` fallback, known/unknown terminal activation, group removal, `afplay`, timeout, and failure.
- Linux display/no-display, `notify-send` urgency/replacement, player order, file-format capability, timeout, and failure.
- Custom sound failure falls back once to built-in without overlapping playback.
- Audio burst selects the highest-priority cue and does not accumulate a long queue.

### SSH delivery

- A live registered-host transition becomes one typed event and one local centre record; initial/reconnect snapshots and events older than the live grace window become none.
- Two `host serve` streams for the same configured host produce the same event key. Two local Sidecar processes race to one local record/claim winner, while a viewer on a different local computer may deliver independently.
- A focused viewer showing the remote origin suppresses `background`; a focused local workspace or a different remote workspace allows it; `always` delivers either way.
- Remote source switches, quiet hours, provider failure, channel independence, and explicit test behavior resolve through the existing local policy without remote provider execution.
- Host protocol malformed/bounded-field cases and old/new version mismatches fail closed with actionable errors and no stored or external notification.
- Ghostty, iTerm2, WezTerm, and Kitty encoders produce exact sanitized bytes with and without tmux passthrough. Unknown/ambiguous terminals report unavailable; JSON output contains no protocol escape bytes.
- ESC, BEL, string terminators, multiline text, tabs, long Unicode, and adversarial title/body input cannot break out of the selected terminal sequence.

### Configuration and CLI

- Notifications is in sidebar order and search results under all intended terms.
- Root and child routes fit and remain reachable at 60×24, 100×30, and 160×45.
- Dropdowns, text editors, confirmations, Escape/back, Tab/focus, mouse hit regions, hover, and footer commands remain synchronized.
- Capability probes do not run at startup or in `View`.
- Test actions bypass foreground/quiet suppression but honor disabled/unavailable channels and create no centre record.
- TUI and CLI mutations produce byte-equivalent resolved config and take effect live.
- Human and JSON CLI output distinguish disabled, suppressed, unavailable, attempted, delivered, and failed.

## Acceptance evidence

- [ ] A needs-input transition in a background workspace produces one attention cue, one native banner, one sticky Sidecar notification, and no duplicates across two Sidecar processes.
- [ ] A finished transition uses the done cue; a failed session uses the failure cue and critical/error native urgency where supported.
- [ ] The same transitions are externally silent while their origin is visible in a focused Sidecar under `background`, and deliver under `always`.
- [x] Quiet hours suppress only external channels.
- [ ] Old unread records never play or post native banners at startup.
- [ ] Native click focuses a supported macOS terminal; unsupported/fallback providers degrade visibly and safely.
- [x] Built-in and valid custom sounds work; invalid paths are refused without losing the previous setting.
- [x] Configuration, CLI, and JSON config agree on every setting and validation rule.
- [x] No new work occurs on the startup paint path, no extra workspace polling is added, and provider work never blocks `Update` or `View`.
- [x] macOS and Linux release-architecture builds and focused/integrated automated gates pass.
- [ ] Manual real-provider proof records native/sound delivery, focus behavior, and safe degradation on the available macOS and Linux environments.
- [x] All substantive implementation is independently reviewed and approved before completion.
- [ ] A registered remote needs-input transition creates one local centre record and invokes only the local viewer's enabled sound/native providers; two local viewers on the same destination host do not duplicate it.
- [ ] Showing the remote origin suppresses managed `background` delivery, while reconnect snapshots and transitions that occurred without an attached viewer never replay.
- [ ] Direct SSH terminal delivery is off by default, emits one sanitized supported-terminal sequence when enabled, works through isolated tmux passthrough, reports unsupported terminals honestly, and never invokes a remote sound/native provider.
- [ ] A full-lifecycle agent integration drives needs-input and finished notifications through the existing lane/store/delivery path without parsing screen text or posting directly from the hook.
- [ ] Missing, partial, stale, conflicting, or failed lifecycle integrations fall back to current screen/process detection without duplicate or replayed notifications.

## References

- [Herdr configuration: Notifications and Sound](https://herdr.dev/docs/configuration/) is the product reference for background agent cues, native-vs-terminal delivery, and custom sounds. Herdr commit `4a3b04f59ba3b7d8a15cea187b23e1e80c343b0c` was also inspected for its headless server-to-foreground-client notification forwarding and its Ghostty/iTerm2/WezTerm/Kitty terminal encoders with tmux passthrough. Sidecar intentionally keeps its existing notification centre and separate native/sound channel modes.
- [Remote hosts](sidecar-remote-hosts.md) controls Sidecar's SSH stdio protocol, host registration, local viewer lifecycle, and remote pane identity. M5 adds a one-way typed notification event without introducing remote mutation.
- [Agent lifecycle hooks](notification-agent-lifecycle-hooks.md) controls M6 lifecycle reporting, authority arbitration, integration management, diagnostics, and fallback behavior.
- [terminal-notifier](https://github.com/julienXX/terminal-notifier/blob/master/README.markdown) documents macOS Notification Center delivery, grouping/replacement, sound, and application activation.
- [Desktop Notifications Specification](https://specifications.freedesktop.org/notification/latest-single/) defines Linux app name, urgency, replacement ID, and notification-server behavior.
