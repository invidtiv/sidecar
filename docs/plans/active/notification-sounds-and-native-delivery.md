# Notification sounds and native delivery

**Status:** In progress — M0 through M3 complete **Tracking:** `td-95db32`, implementation epics `td-7b9ccc` and `td-eb6475` **Created:** 2026-08-28 **Updated:** 2026-08-29

This is the controlling plan for Sidecar notification sounds, native operating-system notifications, and their Configuration experience. It supersedes the unimplemented terminal-BEL configuration and OSC desktop-notification phases in [Notifications — toasts, centre, indicator, sources](../implemented/notifications.md). The existing in-app notification centre, toast renderer, JSONL store, calls to action, and agent-transition triggers remain the foundation.

## Outcome

When an agent in a background workspace needs input, finishes a turn, or loses its session, Sidecar can play a short event-appropriate sound and post a native desktop notification. The event still appears in Sidecar's existing toast and notification centre according to the in-app policy; an external delivery never replaces, dismisses, or marks the stored notification read.

A user can turn sounds and native notifications off, limit either channel to background events, or allow it always. They can configure the agent-event rules, preview each sound, send a test desktop notification, inspect provider availability, set quiet hours, and choose custom sound files from the ordinary full-frame Configuration surface. The same owned behavior has deterministic CLI and JSON configuration paths for agents and diagnostics.

The smallest valuable journey is:

1. Open **Configuration → Notifications** and set **Sounds** and **System notifications** to **Background only**.
2. Leave an agent working in one workspace and focus another workspace or blur Sidecar.
3. When the agent settles into **Needs input**, hear the attention cue once, receive one native notification, and retain the sticky Sidecar notification in the centre.
4. Click the native notification to return to the hosting terminal when the platform provider supports activation, then use Sidecar's existing notification target to open the relevant session.
5. Return to Configuration and test, mute, or change the rule without restarting Sidecar.

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
- Focused tests, isolated real-app proof, platform build proof, documentation, independent review, and a user-runnable demo/configuration journey.

### Deliberately out of scope

- Notifications for ordinary `working` or `idle` transitions. Those remain visual state; making every poll-derived state audible would create noise rather than useful attention.
- Replacing Sidecar's notification centre, toast stacking, reveal motion, retention, read semantics, or target activation.
- A second notification store or a second source registry.
- OSC 9, OSC 777, or terminal BEL as fallbacks. They depend on the outer terminal, behave differently over tmux and SSH, and do not satisfy the native-delivery goal.
- Deep-linking directly into a specific Sidecar workspace from a native notification in the first release. macOS can focus a known terminal application; exact target activation stays in Sidecar's existing centre until Sidecar owns a safe registered URL handler.
- Ignoring Do Not Disturb, Focus modes, or the desktop notification daemon's policy.
- Playing audio or notifying on a remote SSH host as a substitute for local delivery. A future Sidecar remote-client boundary can carry delivery events to the local client; this plan does not send escape sequences across SSH.
- Windows adapters while Windows is not a Sidecar release target. The adapter contract must make a later Windows implementation additive.
- Per-agent-family overrides in the first release. The stored notification currently carries a source and origin but no structured provider identity; add that only if real use shows that muting one agent family is worth extending the record contract.
- General Configuration-page scrolling. The root and child routes must fit Sidecar's existing non-scrolling detail pane at the supported 60×24 minimum.

## Current state

The implementation should extend these seams rather than reconstruct them:

- `internal/notify` owns `Notification`, the fixed source registry, expiry resolution, the JSONL `Store`, state-free centre/toast queries, target reconciliation, and `LaneTracker`.
- `internal/plugins/workspace/agent_triggers.go` is the only adapter from resolved workspace agent lanes into `notify.PostMsg`. `LaneTracker` already debounces transitions and emits:
  - `waiting` + warning + sticky for **Needs input**;
  - `session` + info for **Finished**;
  - `session` + error for **Session ended**.
- `internal/app/notifications.go` owns the process-local store and render cache. `postNotification` is the in-process point after a notification is normalized and stored; the one-second reconciliation sweep discovers records appended by another process.
- `internal/notify/store.go` is already append-only JSONL with an exclusive file lock and cross-process refolding. It deduplicates an identical notification ID, but it has no external-delivery receipts or logical-event deduplication.
- The notification log is global by design. Every running Sidecar instance can discover the same record, so external side effects cannot be attached naively to each instance's sweep.
- `sidecar notify post|dismiss|list` already uses the UI request bus when a matching instance is running and appends directly to the JSONL store otherwise. `post` and `list` already support structured output.
- `Model.applicationFocused` is updated by Bubble Tea `FocusMsg`/`BlurMsg`, and the root view already requests focus reporting. This is the correct foreground signal; agent activity, tmux focus, or recent polling are not substitutes.
- `notify.Notification.Origin` records tmux session, project key, and work directory. The project and global Workspaces surfaces already know which shell/worktree is selected, but that visible-origin fact is not yet exposed as app-level attention context.
- `internal/config.NotificationsConfig` currently manages only per-source toast expiry. `notify.ApplyConfig` is called at startup, after Configuration saves, and by the CLI fallback path. `config.Save` intentionally leaves the root `notifications` key unmanaged today, so the new page must make it a managed key without losing configured source entries.
- `internal/configui` is the implemented full-frame Configuration surface. Pages are registered in `pages.go`, searchable controls in `index.go`, content in page files, persistence through targeted `internal/config` save helpers, and host effects through `ConfigSavedMsg`.
- The Configuration detail pane truncates rather than scrolls. Focused child routes are the established way to keep a dense setting understandable and reachable at narrow sizes.
- Sidecar releases `darwin` and `linux` for `amd64` and `arm64`, with `CGO_ENABLED=0`. Delivery must remain subprocess-backed or pure Go and must compile without platform GUI frameworks.

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
- Invalid enum/time/path input is refused before save and leaves the last valid config in force.
- Unknown source IDs survive a read-modify-write. Existing source expiry values survive the schema expansion.
- `config.Save` begins managing `notifications`, and `SaveNotifications(mutate)` reloads immediately before writing like `SaveWorkspace`, `SaveUI`, and `SaveSelection`. No Configuration model writes JSON directly.
- `notify.ApplyConfig` becomes an immutable resolved-policy snapshot rather than a package-level collection of expiry overrides. The app and CLI both replace the snapshot after a successful load/save.

## Configuration experience

Add **Notifications** after **Agents** in the Sidecar sidebar group. Add every visible control and child route to the static settings index with terms such as `sound`, `audio`, `native`, `desktop`, `system notification`, `waiting`, `finished`, `quiet hours`, and provider names.

The root page must fit the supported 60×24 detail pane. It summarizes; child routes carry the dense controls.

```text
Notifications
Choose how Sidecar gets your attention when work changes state.

Delivery
System notifications    Background only ▾
Sounds                  Background only ▾
Quiet hours             Off             ›

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
- **Agent activity** opens a focused list of Needs input, Finished, and Session ended. Each row summarizes Toast / Native / Sound and opens one compact rule editor.
- A rule editor contains In-app toast, System notification, Sound cue, Toast duration, and **Test this event**. The Session rule explains that an error automatically uses the failure cue while `event` is selected.
- **Other sources** uses the same rule editor for Agent posts, TD, Tasks, and System. This is one reusable source-rule view over the source registry, not duplicated page code.
- **Delivery status** runs capability probes in a `tea.Cmd` only after the route opens. It reports the selected native provider, sound player, custom-path validation, local/SSH context, and repair guidance.
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
sidecar notify config set [--native off|background|always] [--sound off|background|always] [--quiet-hours off|HH:MM-HH:MM] [--json]
sidecar notify source set <source> [--toast on|off] [--native on|off] [--sound none|event|attention|done|failure] [--expiry DURATION|sticky] [--json]
sidecar notify status [--json]
sidecar notify test --channel native|sound|all [--event waiting|done|failure] [--json]
```

- `config` prints resolved settings and defaults without modifying the file.
- `config set` and `source set` call the same validation and targeted save functions as Configuration. They accept no interactive prompts.
- `status` probes adapters and reports availability, selected provider/player, reason when unavailable, and whether the current process appears remote. It does not send anything.
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

The external delivery host must start no background loop of its own. It rides the existing post path and one-second notification reconciliation.

## Security, privacy, and failure rules

- Never shell-compose notification text, paths, app identifiers, or player arguments.
- Strip control characters and OSC sequences before sending text to an OS service.
- Do not include full terminal output, agent prompts, environment variables, or hidden metadata. Use only the already-stored notification title/body and stable local replacement key.
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

### M4 — Integrated proof, documentation, review, and release readiness

1. Update Configuration docs, `docs/reference/cli.md`, example config, and the notification architecture plan's superseded pointers.
2. Run focused package tests, `go test ./...`, `go build ./...`, repository lint gates, and a GoReleaser snapshot.
3. Run `scripts/tmux-drive.sh paths` first, then a fully isolated Configuration and notification-centre proof; always run `stop` afterward.
4. Use fake provider runners for automated argv/claim/receipt evidence. Perform real sound and native-notification tests only as explicit user-visible manual actions; record the provider, OS, terminal, focus state, and result.
5. Verify the config page through `./scripts/demo.sh` and give the user the exact route/CLI test commands.
6. Independently review policy semantics, multi-instance claiming, privacy, subprocess safety, platform degradation, Configuration keyboard/mouse behavior, CLI parity, and proof evidence. Fix findings and rerun affected gates.

Exit gate: the integrated candidate has independent approval and evidence for the real background-agent journey, not only adapter unit tests.

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
- [ ] macOS and Linux release builds pass; focused, integrated, isolated, and manual real-provider proof is recorded.
- [x] All substantive implementation is independently reviewed and approved before completion.

## References

- [Herdr configuration: Notifications and Sound](https://herdr.dev/docs/configuration/) is the product reference for background agent cues, native-vs-terminal delivery, and custom sounds. Sidecar intentionally keeps its existing notification centre and uses separate native/sound channel modes rather than Herdr's single popup-delivery selector.
- [terminal-notifier](https://github.com/julienXX/terminal-notifier/blob/master/README.markdown) documents macOS Notification Center delivery, grouping/replacement, sound, and application activation.
- [Desktop Notifications Specification](https://specifications.freedesktop.org/notification/latest-single/) defines Linux app name, urgency, replacement ID, and notification-server behavior.
