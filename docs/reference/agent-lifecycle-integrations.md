# Agent lifecycle integrations

Sidecar can learn what an agent is doing from the agent's own lifecycle events instead of inferring it from the agent's screen. A small Sidecar-owned file installed beside a supported agent translates that agent's native events into `sidecar agent report`, `end`, and `release` calls. Those reports are evidence; one shared resolver decides whether they are current and complete enough to author the pane's state, and screen and process detection remain the permanent fallback.

This document is the contract two audiences need: someone installing or auditing an integration, and someone writing a custom reporter of their own. The provider evidence behind each tier is in [the capability matrix](agent-lifecycle-capability-matrix.md); the controlling plan is [Deterministic agent lifecycle hooks](../plans/active/notification-agent-lifecycle-hooks.md).

## What is installed, and what it may do

An integration reports lifecycle facts only: a lane (`working`, `blocked`, `idle`), a terminal outcome, a bounded reason code from a frozen allowlist, a monotonically increasing sequence, and — when the provider supplies one — a session identifier of which Sidecar retains only a host-salted digest.

It never sends prompt text, response text, tool arguments or results, file paths, environment values, or credentials. It cannot post a notification, play a sound, emit a terminal escape, or choose delivery policy: those belong to the notification path downstream, which sees only a resolved lane change.

Sidecar never installs an integration on its own, never installs one into a repository, and never writes into a provider's own configuration file from a template. What it installs are versioned, Sidecar-owned assets in the user-level configuration directory the provider already reads.

## Managing an integration

Every fact and action exists on both surfaces over one application service, so an agent and a human get the same answers.

```
sidecar agent integration list [--json]
sidecar agent integration status [PROVIDER] [--json]
sidecar agent integration install   PROVIDER [--dry-run] [--json]
sidecar agent integration update    PROVIDER [--dry-run] [--json]
sidecar agent integration repair    PROVIDER [--dry-run] [--json]
sidecar agent integration uninstall PROVIDER [--dry-run] [--json]
```

In the TUI the same service answers **Configuration → Agents → Integrations**.

`--dry-run` is not a separate code path. The preview and the mutation are produced by one call and rendered by one function, so the ordered file operations, their before/after ownership, and the resulting status are byte-identical between a preview and the run that follows it.

Exit codes follow the repository's convention: `0` success, including a no-op; `1` the change was attempted and failed part-way; `2` usage error; `5` refused.

### Status

Status is decided by inspecting the installed files, never by trusting a version a report claimed. The installed bytes are hashed and compared with the bundled asset's own hash.

| Status | Meaning |
| --- | --- |
| `provider-missing` | The provider CLI was not found on PATH. Any installed asset is still reported so it can be removed. |
| `not-installed` | The provider is present and Sidecar's integration is not installed. |
| `current` | The installed asset is byte-identical to the one this Sidecar build ships. |
| `outdated` | A Sidecar-owned asset at an earlier version is installed. |
| `needs-repair` | The installation is present but damaged, modified, duplicated, or blocked by a file Sidecar does not own. |
| `unsupported` | Sidecar has recorded capability evidence for this provider but ships no asset for it yet. |

The status maps onto an authority tier through the capability registry: `needs-repair`, `not-installed`, `provider-missing`, and `unsupported` all resolve to screen fallback, so a damaged integration never authors a lane.

`status` also reports the newest record the source has written on this machine — its lane or terminal outcome, its sequence, the pane, and how long ago. That is the difference between *installed* and *working*, which are not the same claim: an integration can be `current` at tier `full` and have never reported anything, because reports only come from agents launched in a Sidecar-managed shell after it was installed. The summary deliberately omits the run id and the salted session fingerprint, which answer no question being asked here.

### Which verb applies when

The verbs are distinct because a verb should mean what the user believes the situation to be. All three mutating verbs converge on the same target state — exactly one Sidecar-owned asset, at the bundled version, in the directory Sidecar owns — and differ only in which starting states they accept.

| Status before | `install` | `update` | `repair` | `uninstall` |
| --- | --- | --- | --- | --- |
| `not-installed` | installs | refused, names install | refused, names install | no-op |
| `current` | no-op | no-op | no-op | removes |
| `outdated` | refused, names update | updates | updates | removes |
| `needs-repair` | refused, names repair | refused, names repair | repairs where it can | removes what Sidecar owns |
| `provider-missing` | refused | refused | refused | removes |
| `unsupported` | refused | refused | refused | refused |

## Safety rules the installer enforces

These are enforced while a plan is built, before any operation runs, so a refusal never leaves a partial change behind.

- **Ownership is content, not a filename.** Every bundled asset carries a marker line — `// sidecar-integration: id=<source> schema=<n> version=<n>` — and Sidecar writes, replaces, or removes only files carrying it. A file with exactly the name Sidecar would have chosen, but without the marker, is refused with `foreign_file` and left untouched. Sidecar never adopts a similarly named existing script and never deletes one.
- **Symlinks at an asset path are refused.** An ordinary `stat` through a symlink reports a healthy regular file while the write lands wherever the link points. Inspection uses `lstat`, and a symlinked asset path is `unsafe_path`. A symlinked *configuration directory* is fine, as long as it resolves to a directory this user owns — dotfile repositories do that routinely.
- **Ownership and permissions are checked.** A path owned by another user is `unsafe_owner`. A group- or world-writable plugin directory is `unsafe_mode`: anyone in that group could replace the file the provider loads and executes, so Sidecar declines to install into it rather than silently narrowing a directory the user widened deliberately.
- **Writes are atomic.** Every write goes to a temporary file in the same directory and is renamed over the target, so a reader never sees a partial asset and an interrupted write never destroys the file it was replacing.
- **Replacements are recoverable.** Replacing an installed asset first copies it to `<name>.sidecar-backup` beside it. Uninstalling removes that backup too, so a "clean" tree has no Sidecar file left in it.
- **Uninstall removes only what Sidecar installed.** The asset, a duplicate copy Sidecar owns, and the backup. The provider's own configuration and every unrelated plugin are untouched. The plugin directory is removed only when removing Sidecar's files leaves it empty.
- **New directories inherit their parent's mode.** A user who keeps `~/.config/<provider>` at `0700` does not find a world-readable directory inside it after installing.

## OpenCode

The first shipped integration. Its asset is `sidecar-lifecycle.js`, installed as:

```
$XDG_CONFIG_HOME/opencode/plugin/sidecar-lifecycle.js
```

falling back to `~/.config/opencode/plugin/` when `XDG_CONFIG_HOME` is unset.

**OpenCode loads both `plugin/` and `plugins/`.** This is not documented anywhere by the vendor; it was found by tracing. A copy of the asset in each fires every event twice, which doubles every sequence number and makes the store's ordering contract meaningless. Sidecar therefore owns exactly one of those directories and treats anything with the asset's name in the other as damage: it reports `needs-repair`, `repair` removes the copy Sidecar owns, and a copy Sidecar does not own is refused rather than deleted.

One more measured constraint shapes the asset: **OpenCode's plugin loader silently skips any module with a non-function export.** A single string or object export disqualifies the whole module — it is imported and then never called, with no error. The asset therefore hangs its helpers off the plugin function rather than exporting them beside it.

## The managed-shell environment contract

An integration is only permitted to report from inside a Sidecar-managed shell. Sidecar publishes the following variables when it creates one, and a hook that finds `SIDECAR_MANAGED_SHELL` unset exits `0` silently and does nothing.

| Variable | Meaning |
| --- | --- |
| `SIDECAR_MANAGED_SHELL` | Set to `1`. The single check a hook makes before doing anything at all. |
| `SIDECAR_HOST` | The host that owns the pane. Reports never cross hosts. |
| `SIDECAR_TMUX_SERVER` | The tmux server's PID, which namespaces stored records by server incarnation. |
| `SIDECAR_NAMESPACE` | The tmux socket path identifying this host-local namespace. An identifier only, never a location anything is written to. |
| `SIDECAR_BIN` | The absolute path of the Sidecar binary that created the shell, so an asset never has to guess a path or search PATH. |
| `SIDECAR_SHELL` | The tmux session that owns the shell. |
| `SIDECAR_SHELL_NAME` | The shell's display name. |

Two rules shape that list, and both matter to anyone writing a reporter:

**Nothing in it is a writable path.** The report command resolves Sidecar's state directory itself, so provider input can never redirect where lifecycle records are written.

**Nothing in it is trusted on its own.** The report command re-derives the live pane, tmux server, and the provider process generation from the environment and live tmux, and refuses a report whose claimed context does not match. There is no flag that selects a host, server, pane, process generation, or run id — a hook can only ever report about the pane it is running in. Existing shells created before this contract keep working through screen detection until they are recreated.

`SIDECAR_TMUX_SERVER` carries the server PID rather than a fuller incarnation string on purpose. The obvious alternative embeds the socket's ctime, which tmux bumps every time the attached-client set changes; namespacing records by it would orphan every report the moment a user attached or detached a client, silently returning a healthy pane to screen fallback.

## Writing a custom reporter

Any program running inside a Sidecar-managed shell may report, using the same three commands the bundled assets use:

```
sidecar agent report  --state working|blocked|idle --source SOURCE --provider PROVIDER --seq N [--session-id ID] [--reason CODE] [--json]
sidecar agent end     --outcome completed|cancelled|failed|unknown --source SOURCE --provider PROVIDER --seq N [...]
sidecar agent release --source SOURCE --provider PROVIDER --seq N [...]
```

`sidecar agent explain [--current | --shell TARGET] [--json]` reports the effective state, which evidence authored it, the exercisable tier, the last valid report, and — when lifecycle evidence did not win — exactly why not. It is read-only and is the first thing to run when a reporter appears to be doing nothing.

### The rules a reporter must follow

**Pick a stable source identifier.** `--source` names the integration, not the provider. Authority is granted to a source at a version, never to a provider name forever, so one provider may have more than one integration shape over time without the older one inheriting what the newer one proved. Pass `--source-version` as well.

**Sequences are strictly increasing within `(server incarnation, pane, source, run)`.** A report whose sequence does not advance is rejected as stale. Assign the sequence at the point where you decide to send, and then **serialize the sends**: each report is a subprocess taking an exclusive lock on an append-only store, so spawning them concurrently assigns sequences in order and delivers them out of order, and the store correctly rejects the loser. This was observed silently dropping a terminal `end` report in two runs out of three before the bundled asset serialized its queue.

**Latch a run closed after a terminal outcome.** Once `end` has been reported, do not let a trailing status event reopen the run. A provider that emits an error followed immediately by an idle status will otherwise have a cancelled turn announced to the user as a clean completion.

**Fail open, always.** A reporting failure is diagnostic and must never change what the agent does, delay it, or appear in its output. Bound each report subprocess with a timeout, swallow failures, and never block the next event behind a hung one.

**Send no content.** Lanes, outcomes, reason codes, sequences, and identifiers only. The report command bounds and sanitizes everything it accepts, but a reporter that gathers content in order to send it has already read something it did not need to.

### Custom sources are advisory

A report from a source that is not in Sidecar's bundled capability registry is accepted and recorded, and resolves at **screen fallback**: it enriches diagnostics and appears in `agent explain`, and it does not author the pane's lane.

This is deliberate and it is not a slight. Full lifecycle authority means a fresh report overrides contradictory screen evidence, and a source that holds it while missing an event freezes a pane in a state nothing will ever update. Sidecar grants that only from recorded real traces covering work, blocking, unblocking, completion, cancellation, session change, and process exit — which is why every entry in the capability matrix re-derives its tier from its own evidence, and why an entry claiming more than it can show is demoted rather than trusted and audited later.

**The future trust boundary.** A later slice may add an explicit configuration or CLI trust record granting a custom source a higher tier. When it arrives it must name the source, the version range, the covered events, and the revocation path, and it must be an explicit act: accepting a report will never implicitly grant authority. Until then, a custom reporter that needs lifecycle authority is a request for a bundled adapter, and the way to make that case is the same recorded evidence every shipped provider had to produce.

## Troubleshooting

| Symptom | What to check |
| --- | --- |
| The integration installed but nothing happens | `sidecar agent explain --shell TARGET --json`. A `fallbackReason` names the cause exactly. Reports only come from agents launched in a Sidecar-managed shell *after* the integration was installed. |
| `status` says `needs-repair` | The message names the file. A duplicate copy or a modified asset is fixed by `repair`; a file Sidecar does not own has to be moved by you. |
| Authority is `advisory` when the matrix says `full` | `tierReason` will say. The usual cause is a provider version outside the range Sidecar has proved, which is honest rather than broken. |
| A pane went back to screen detection mid-run | Process exit, pane replacement, a tmux server restart, an explicit `release`, or an identity mismatch all clear authority immediately. This is the designed behavior: stale state fails toward current observation, never toward a guessed lane. |

## See also

- [Agent lifecycle capability matrix](agent-lifecycle-capability-matrix.md) — the per-provider evidence, gaps, and earned tier.
- [CLI reference](cli.md) — generated help for every command above.
- [Deterministic agent lifecycle hooks](../plans/active/notification-agent-lifecycle-hooks.md) — the controlling plan.
