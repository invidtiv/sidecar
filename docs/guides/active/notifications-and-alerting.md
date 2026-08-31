# Notifications & Alerting Guide

Sidecar provides an integrated notification system for alerts, task status updates, and agent lifecycle signals. Notifications appear as in-app toast overlays in the running Sidecar instance, accumulate in the Notification Centre (with unread badge counts in the header), and can trigger native desktop notifications or sound cues.

## Core Commands

| Command | Action | Key Flags |
|---|---|---|
| `sidecar notify post` | Post a notification toast and notification centre entry | `<title>`, `--body TEXT`, `--target SPEC`, `--source ID`, `--expiry DURATION` |
| `sidecar notify list` | List recent notifications from the log | `--all`, `--unread`, `--json` |
| `sidecar notify dismiss` | Dismiss a notification previously posted by this caller | `<id>`, `--json` |
| `sidecar notify config` | Inspect resolved notification delivery modes and cues | `--json` |
| `sidecar notify config set` | Update delivery modes, quiet hours, sound cues, or SSH forwarding | `--native MODE`, `--sound MODE`, `--quiet-hours RANGE`, `--ssh-managed-hosts on\|off` |
| `sidecar notify source set` | Configure per-source rules (toasts, sounds, native, expiry) | `<source>`, `--toast on\|off`, `--native on\|off`, `--sound CUE`, `--expiry DURATION` |
| `sidecar notify test` | Directly test enabled providers without creating a log entry | `--channel native\|sound\|all`, `--event waiting\|done\|failure` |
| `sidecar notify status` | Probe provider availability and audio engine health | `--json` |

---

## 1. Posting Notifications: `sidecar notify post`

Notifications can be posted from any shell (local or inside Sidecar). If Sidecar is not running, the notification is durably appended to the notification log and displayed when Sidecar next starts.

```bash
# Simple toast notification
sidecar notify post "Build Succeeded" --body "All 84 unit and integration tests passed"

# Notification requesting user attention (sticky toast until clicked/reviewed)
sidecar notify post "Decision Needed" \
  --body "Merge conflict detected on branch feature-auth" \
  --source waiting \
  --expiry never
```

### Actionable Jump Targets (`--target`)

The notification centre numbers attached targets (1–N), allowing the user to jump directly to the referenced context by pressing `Enter` or a digit key.

Supported target specifications (`kind:value[:line][@project]`):
* `file:<path>[:<line>]` — Jump to a file and line in the file browser or editor split.
* `issue:<td-id>` — Open the specified `td` task in the task monitor.
* `commit:<oid>` — Reveal the commit diff in the Git plugin.
* `session:<session-name>` — Jump focus to the named managed tmux shell session.
* `url:<https://...>` — Open external link in default browser.
* Cross-project jump: append `@project` (e.g. `issue:td-99aabb@braid`) to switch project workspaces before landing.

Example:
```bash
sidecar notify post "Review Ready" \
  --body "Unit tests passed; ready for review" \
  --target issue:td-4c1f9a \
  --target file:internal/app/model.go:42
```

---

## 2. Notification Sources & Lifecycle Cues

Sidecar supports pre-configured notification sources:

| Source ID | Typical Use | Default Sound Cue |
|---|---|---|
| `agent` | Routine agent activity or status changes | `event` |
| `waiting` | Agent is blocked, requires review or human approval | `attention` |
| `session` | Shell or worktree lifecycle changes | `event` |
| `tasks` / `td` | Task transitions, review requests, approvals | `done` / `attention` |
| `system` | App updates, errors, diagnostic warnings | `failure` |

Customize sound cues per source without restarting:
```bash
# Make waiting tasks play the attention cue and trigger native system alerts
sidecar notify source set waiting --sound attention --native on --expiry sticky
```

---

## 3. Global Notification Settings

Configure delivery modes (`off`, `background`, `always`) and quiet hours:

```bash
# Send system and sound notifications only when Sidecar is in background
sidecar notify config set --native background --sound background

# Set quiet hours (suppresses sound and native toasts between 22:00 and 08:00)
sidecar notify config set --quiet-hours 22:00-08:00

# Enable SSH remote notification forwarding
sidecar notify config set --ssh-managed-hosts on
```

---

## 4. Testing & Diagnostics

Verify that system notification banners and audio cues work properly on your machine:

```bash
# Probe audio and system notification providers
sidecar notify status --json

# Trigger an immediate test sound/native alert
sidecar notify test --channel all --event attention
```
