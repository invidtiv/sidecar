# Agent lifecycle capability matrix

**Status:** Phase A evidence baseline recorded 2026-08-30; OpenCode cancellation traced and promoted to `full` in Phase B, 2026-08-30; installation, status, and repair added in Phase C, 2026-08-30; Codex and Claude Code traced against their current releases in Phase D, 2026-08-30. **Plan:** [Deterministic agent lifecycle hooks](../plans/active/notification-agent-lifecycle-hooks.md). **Tracking:** `td-43a93f`.

How an integration is installed, inspected, and removed is [Agent lifecycle integrations](agent-lifecycle-integrations.md). This document records what each agent provider's own lifecycle events can actually tell Sidecar, how strong the evidence for that claim is, and what authority tier the evidence justifies. It is the prose companion to `internal/agentlifecycle/capabilities.json`, which is the machine-readable version the code reads and the tests police. That file is embedded into the binary and read at runtime through `agentlifecycle.Capabilities()`, so the registry the resolver trusts and the evidence these tests police are one file rather than two that could drift.

The matrix is evidence, not aspiration. A provider does not get full lifecycle authority because its documentation lists the right event names. It gets full lifecycle authority when sanitized real traces show the transitions arriving, in order, from a released version.

## How a tier is earned

Sidecar recognises four tiers, defined in `internal/agentlifecycle`:

| Tier | What it means |
| --- | --- |
| `full` | A fresh same-run report authors `working`, `blocked`, or `idle`. Screen evidence remains diagnostic and cannot reverse it. |
| `advisory` | A fresh event may confirm what the screen already sees, or speak when the screen has no opinion, but may never contradict it. |
| `session-identity` | The source identifies the provider session reliably but not its state. Screen and process detection stay the sole lifecycle authority. |
| `screen-fallback` | No valid integration evidence applies. Existing detector and tracker behavior is unchanged. |

To hold `full`, a source must have `evidence: real-trace` and must cover every transition in `FullLifecycleTransitions()`: `work_start`, `blocked_on_request`, `unblocked`, `turn_complete`, `cancelled`, `session_identity`, and `process_exit`.

`tool_use` and `subagent` are deliberately excluded from that requirement. Tool use is a refinement of `work_start` rather than a separate lane, and subagent aggregation has no proved cross-provider rule yet, so requiring either would withhold authority for reasons unrelated to the lanes Sidecar renders.

This is enforced rather than documented. `Capability.TierFor` polices every tier boundary, not only the top one: a `full` claim without real traces or complete coverage falls to `advisory`, an `advisory` claim with no evidence or no covered transition falls out to `screen-fallback`, and a `session-identity` claim that does not actually cover session identity falls out too. Only `screen-fallback`, which asserts nothing, needs no evidence. `TestCapabilityMatrixCannotClaimUnearnedAuthority` re-derives every entry's tier from its own recorded evidence and `TestTierForPolicesEveryTierBoundary` covers the lower boundaries directly. An entry cannot be edited to claim authority its evidence does not support, and it cannot escape scrutiny by claiming less either.

## Summary

| Provider | Version seen | Evidence | Tier now | Herdr's tier | Blocking gap |
| --- | --- | --- | --- | --- | --- |
| OpenCode | 1.18.23, 1.18.25 | real-trace | `full` | lifecycle authority | none blocking; see gaps below |
| Codex | 0.151.0 | real-trace | `session-identity` | session only | shipped hook is SessionStart only. The provider's own contract is traced and would support `full`. |
| Claude Code | 2.1.220 | real-trace | `session-identity` | session only | shipped hook is SessionStart only. The provider's ceiling is `advisory`: no cancellation event exists. |
| Pi | 0.84.3 | docs-only | `session-identity` | lifecycle authority | blocking is structurally impossible; ceiling is `advisory` |

Two columns are doing different jobs here and it is worth being explicit about which. **Tier now** is what the *shipped Sidecar asset* earns, and for Codex and Claude that is `session-identity` because each ships one `SessionStart` entry and nothing more. **Blocking gap** describes the *provider's* ceiling — the best any Sidecar adapter could honestly claim if one were written today. Codex's ceiling is `full` and Claude's is `advisory`, and neither is reached yet because neither asset asks for it.

"Herdr's tier" is what the Herdr project ships at commit `4a3b04f59ba3b7d8a15cea187b23e1e80c343b0c`. It is included because Herdr has shipped all four integrations in production, so where it disagrees with the published contract that disagreement is itself evidence.

## Transition coverage

`YES` means an official event exists and, where marked traced, was observed. `PARTIAL` means it must be inferred. `NO` means no event exists.

| Transition | OpenCode | Codex | Claude Code | Pi |
| --- | --- | --- | --- | --- |
| work start | YES (traced) | YES (traced) | YES (traced) | YES |
| tool use | YES (traced) | YES (traced) | YES (traced) | YES |
| blocked on request | YES (traced) | YES (traced) | YES (traced) | NO |
| unblocked | YES (traced) | YES (traced) | PARTIAL (traced) | NO |
| turn complete | YES (traced) | YES (traced) | PARTIAL (traced) | YES |
| cancellation | YES (traced) | YES (traced) | NO (confirmed) | PARTIAL |
| session identity | YES (traced) | YES (traced) | YES (traced) | YES |
| subagent | PARTIAL | YES | YES | NO |
| process exit | YES (traced) | YES (traced) | YES (traced) | YES |

Claude Code's `unblocked` and `turn complete` are marked PARTIAL *after* tracing rather than before: both events exist and both fire on the ordinary path, and both go missing on exactly the paths where they would matter most. See the Claude Code section.

## OpenCode

**Source:** <https://opencode.ai/docs/plugins/>, cross-read against `packages/plugin/src/index.ts`. **Traced against 1.18.23 (Phase A) and 1.18.25 (Phase B cancellation) on darwin/arm64, 2026-08-30.** Traces: `internal/agentlifecycle/testdata/traces/opencode/`.

The observed sequence for a turn that used a tool and hit a permission prompt:

```
session.created  ->  chat.message  ->  session.status {"type":"busy"}
                 ->  tool.execute.before (bash)
                 ->  permission.asked  ->  permission.replied
                 ->  session.status {"type":"idle"}  ->  session.idle  ->  dispose
```

A failed turn produced `session.error` followed by `session.status {"type":"idle"}` and `session.idle`, which is the behavior that matters most: an error resolves to idle rather than latching the pane on `working`.

### Why OpenCode is the steel thread

`session.status` is state-shaped rather than transition-shaped. Every emission re-asserts ground truth as `{"type":"busy"}` or `{"type":"idle"}`, so a dropped or reordered event is corrected by the next one instead of leaving a pane stuck. That single property is the difference between an integration that stays true across a long agent run and one that needs a watchdog. It was confirmed by trace, not inferred from documentation.

It is also the only one of the four that emits an explicit unblock (`permission.replied`, `question.replied`, `question.rejected`) rather than requiring the resolution to be inferred, and `session.idle` is a positive readiness signal rather than a mere "stopped generating".

Installation is a single file dropped into a plugin directory with no edits to any existing user configuration file, so there is nothing Sidecar can corrupt on install or fail to restore on uninstall.

### Gaps found at runtime

These were discovered by tracing and are not in the documentation:

1. **The blocked lane is not state-shaped.** `session.status` only ever carried `busy` or `idle`. Blocking is visible solely through the transition-shaped `permission.asked` and `permission.replied` pair, so the self-healing property does **not** extend to the blocked lane. A dropped `permission.asked` will not correct itself.
2. **`tool.execute.after` never fired**, even though `tool.execute.before` did. Any mapping that depends on tool completion is dead on this version.
3. **Both plugin directories load.** `~/.config/opencode/plugin/` and `~/.config/opencode/plugins/` are both read, and an asset present in both fires every event twice. The installer must own exactly one path, and repair must detect the other.

3a. **Every export of a plugin module must be a plugin factory.** A single non-function export disqualifies the whole module — it is imported and then never called, silently, with no error printed anywhere. Measured on 1.18.25 with four probe plugins: a lone exported function loads; a function plus an exported string does not; a function plus an exported object does not; a function carrying helpers as properties loads. This is the most dangerous gap on the list because it is invisible to every offline test: the plugin installs cleanly, its own unit tests pass, and it reports nothing whatsoever. Sidecar's asset therefore exports exactly one symbol and hangs its testable mapping off that function, enforced by `TestTheAssetExportsOnlyPluginFactories`.

3b. **A hook is a direct child of the pane's root process when tmux launches the agent as the pane command**, which is what a Sidecar-managed agent shell does. Any "walk up to the process below the pane root" rule then resolves to the hook itself, giving every report a different process generation and therefore a different run. Sidecar's ancestry walk special-cases this; see `providerGeneration`.
4. **Cancellation has no dedicated event, and shares its shape with failure.** A user interrupt is observable, but only as `session.error` carrying `error.name = "MessageAbortedError"`, immediately followed by `session.status {"type":"idle"}` and `session.idle`. A provider failure emits the *identical* sequence with a different error name. An adapter that reads only event types can therefore get the lane right and the terminal outcome wrong.

### Cancellation, traced (Phase B entry condition)

Phase A left cancellation untraced, which held OpenCode at `advisory`. It is now traced, on 1.18.25, and the observed sequence is:

```
session.status {"type":"busy"}  ->  ...work...
                               ->  session.error  (error.name = MessageAbortedError)
                               ->  session.status {"type":"idle"}  ->  session.idle
```

The contrast run — the same harness with no credentials for the selected model — produced byte-for-byte the same event *shape* with `error.name = ProviderAuthError`. The bounded error class name is therefore the only thing separating a cancelled turn from a failed one, and it is the discriminator the provider handler must read. Both traces are checked in: `cancelled-turn.tsv` and `provider-error-named.tsv`.

Two practical notes from capturing it. Interrupting a busy turn in the OpenCode TUI takes **two** Escape presses; the first only arms the confirmation, and the footer changes from `esc interrupt` to `esc again to interrupt`. A single Escape is a no-op, which is easy to mistake for "cancellation is not observable". And the abort resolves on the bus a few seconds after the TUI stops showing the busy indicator, so a test that tears the session down as soon as the screen settles will miss the events entirely.

The lane itself is safe even for an adapter that ignores the error name, because `session.status` is state-shaped and re-asserts `idle`. Only the `Outcome` on the end report depends on reading it.

With cancellation covered, OpenCode's evidence now satisfies every entry in `FullLifecycleTransitions()` and `TierFor` derives `full` for it. `TestOpenCodeEarnsFullOnlyFromTheCancellationEvidence` asserts the converse: strip `cancelled` from the covered set and the entry falls back to `advisory`.

## Codex

**Source:** the published config reference redirects and truncates before the hooks section, so the authoritative contract is the upstream `HookEventName` schema and the hook JSON schemas embedded in the shipped binary. **Traced against 0.151.0 on darwin/arm64, 2026-08-30.** Traces: `internal/agentlifecycle/testdata/traces/codex/`.

Codex had the best-shaped event set of the four on paper, and tracing confirms it. Twelve events close the loop, every one carries `session_id` and, from `UserPromptSubmit` onward, `turn_id`, and it is the only provider with a first-class `Interrupt` event — precisely the transition Claude Code cannot express.

The observed sequence for a turn that used a tool:

```
SessionStart  ->  UserPromptSubmit  ->  PreToolUse  ->  PostToolUse  ->  Stop
```

and for one that hit a permission prompt and was approved:

```
UserPromptSubmit  ->  PreToolUse  ->  PermissionRequest  ->  [blocked]  ->  PostToolUse  ->  Stop
```

**Every full-lifecycle transition is now traced**, so the ceiling for a Codex adapter is `full`. What ships is still `SessionStart` alone, so the tier is still `session-identity`.

### The finding that decides the blocked lane

Approval and denial do **not** converge on the same event. Denying a permission request emits `Interrupt` — not `PostToolUse`, and not `Stop`:

```
UserPromptSubmit  ->  PreToolUse  ->  PermissionRequest  ->  [blocked]  ->  Interrupt
```

`Interrupt` is therefore Codex's universal "this turn ended without completing" signal, covering both a mid-turn user interrupt and a refused request. That is what makes the blocked lane safe: it always resolves. An adapter that treated only `PostToolUse` as the unblock would unblock on every turn that never blocked and latch forever on the ones that did — which is exactly the failure that caps Claude Code below `full`.

### Gaps found at runtime

These came from tracing and appear in no documentation:

1. **`trusted_hash` is computed over the effective hook, not the declared one.** Codex clamps `SessionEnd` and `Interrupt` hook timeouts to 3s and prints a warning naming `hooks.json` on every start. A trust record pre-written for those events at Sidecar's canonical timeout of 10 does not match, and the user gets the interactive "Hooks need review" prompt on every launch. Proved by letting Codex write its own records and diffing them: the identity hashed with `timeout=3` matches byte-for-byte and `timeout=10` does not. **Any lifecycle adapter must declare `timeout` ≤ 3 on those two events.**
2. **`SessionEnd` does not fire under `codex exec` at all.** It was observed only in the interactive TUI. Process exit on a non-interactive run is detectable only through process liveness.
3. **Nothing re-asserts state.** Unlike OpenCode's `session.status`, every Codex event is transition-shaped. A dropped event does not self-correct, so a Codex adapter has no equivalent of the property that made OpenCode the steel thread and would need a bounded reconciliation against screen evidence for a long-running turn.
4. **`PermissionRequest` carries no `tool_use_id`** although `PreToolUse` does, so correlating a block with the specific tool call that caused it relies on turn ordering rather than an identifier.

Also confirmed, and good news for the existing installer: the reproduced `trusted_hash` algorithm is now live-verified across **eleven** distinct event names in a single run, rather than the one `session_start` vector Phase C pinned. Ten of twelve pre-written records were accepted silently; the two that were not are gap 1.

Herdr, having shipped a Codex integration through eight asset versions, deliberately removed its lifecycle hooks and now installs only `SessionStart`. Sidecar's traces do **not** reproduce a reason for that rollback: every transition such a hook set needs is observable on 0.151.0. It stays recorded as unexplained rather than refuted, because a contract that held in one capture session is not the same as one that holds across versions.

**What ships (asset version 1):** a single `SessionStart` entry in `~/.codex/hooks.json` invoking `sidecar agent report-session --kind codex --hook-stdin` (fixed argv, payload on stdin, no matcher key — Codex groups carry none), `features.hooks = true` in `config.toml`, and a `[hooks.state]` trust record. Codex only auto-runs a hook whose `trusted_hash` matches its normalized identity; an untrusted hook raises an interactive "Hooks need review" prompt (observed live on 0.151.0). Sidecar pre-writes the record with the algorithm reproduced from codex-rs (`hook_hash` + `version_for_toml`: sha256 over the key-sorted canonical JSON of `{"event_name","hooks":[normalized handler]}`) and verified byte-for-byte against a live 0.151.0 record; if the algorithm drifts on a future version, the failure mode is that visible one-time prompt. The trust key is positional (`<hooks.json path>:session_start:<group>:<hook>`), so edits that reorder `hooks.json` invalidate trust records for every hook that shifted. The tier is `session-identity` because that is all the shipped hook exercises — the wider event vocabulary stays unclaimed until Phase D traces it.

## Claude Code

**Source:** <https://code.claude.com/docs/en/hooks>. **Traced against 2.1.220 on darwin/arm64, 2026-08-30.** Traces: `internal/agentlifecycle/testdata/traces/claude/`. Hooks were injected with the additive `--settings` flag, so nothing in the user's `~/.claude` was written.

Claude Code has by far the richest event surface, including the best subagent model of the four, and the largest user base. On the ordinary path it is excellent:

```
SessionStart  ->  UserPromptSubmit  ->  PreToolUse  ->  PostToolUse  ->  Stop  ->  SessionEnd
```

Tracing corrected the previous docs-only reading in one direction and confirmed the disqualifying gap in the other. **Claude Code's ceiling is `advisory`.**

### What tracing corrected

`PermissionRequest` **does** fire, and so does a `Notification`. The earlier entry recorded Claude as offering session identity and nothing else; blocking is in fact first-class on the current release:

```
UserPromptSubmit  ->  PreToolUse  ->  PermissionRequest  ->  Notification  ->  [blocked]
```

### The two findings that cap it at advisory

1. **`PostToolUse` is silently skipped for any tool that went through the permission prompt.** The same tool with no prompt fires it normally. This is worse than a missing event: the event exists, works, and disappears on exactly the turns where it would carry information. An adapter that unblocks on `PostToolUse` unblocks only on turns that never blocked.
2. **No user-cancellation event exists at all.** Interrupting a live turn with Escape produced *no hook event whatsoever* — not `Stop`, and not any of the speculative event names Claude accepted into its settings without complaint. Denying a permission request, or Escape-cancelling the prompt, is equally silent. Because `Stop` does not fire on those paths either, a state machine driven only by these hooks latches on `working` or `blocked` indefinitely.

Finding 2 is a contract gap rather than a tracing gap, so no amount of further tracing can close it. It is the reason Claude Code cannot reach `full` however carefully an adapter is written, and it is why an `advisory` Claude adapter must let screen detection speak: advisory is not a consolation prize here, it is a precise description of an integration whose events are true when they arrive and absent exactly when the user needs the pane to stop saying "working".

`Stop` also means "stopped generating", not "ready for input", so completion cannot be taken from it without reconciliation against screen state. And hook configuration merges additively across five layers, so Sidecar can add entries but can never own the effective hook set.

Herdr shipped a full-lifecycle Claude hook set and then removed it, keeping only `SessionStart`, citing missed permission results and escape interrupts. **Both halves of that are now independently reproduced on the current release**, so the rollback is explained rather than merely cited — and Sidecar reaches the same conclusion from its own evidence rather than by deference.

**What ships (asset version 1):** a single `SessionStart` group in `~/.claude/settings.json` — matcher `"*"`, one entry invoking `sidecar agent report-session --kind claude --hook-stdin` (fixed argv, payload on stdin). The installer owns exactly that entry, identified by its command being an invocation of Sidecar's own report-session verb; every other setting and hook in the file is preserved token-for-token and in order, and uninstall removes only the entry and any container it alone occupied. The tier is `session-identity` because that is all the shipped entry exercises.

## Pi

**Source:** the released TypeScript definitions in the installed package (`dist/core/extensions/types.d.ts`), read rather than inferred from prose. **Not traced.**

Pi has the cleanest agent-flow abstraction of the four. `before_agent_start`/`agent_start`/`agent_end`/`agent_settled`, `turn_start`/`turn_end`, the `tool_execution_*` family, and `session_start`/`session_shutdown` are all typed events with a documented loading contract (`--extension`/`-e`, repeatable; `--no-extensions`; discovery from `~/.pi/agent/extensions/`). Session identity is solid: `ctx.sessionManager.getSessionId()`.

**Correction to the Phase A entry.** That entry cited `ui_prompt_start` as partial cover for blocking. **No such event exists anywhere in the 0.84.3 package.** The blocked lane is `NO`, not `PARTIAL`, and the correction matters more than a table cell: it is the difference between a gap that tracing might close and one that cannot be closed at all.

**Blocking is structurally impossible, not merely absent.** Pi deliberately ships no permission system, so there is nothing to be blocked on. An extension may open its own `ctx.ui` dialog, but that is invisible to every other extension, so no portable blocked signal exists for Sidecar to consume. **Pi's ceiling is therefore `advisory`**, and no amount of tracing will raise it.

Three further limits are worth recording because each is a trap for an adapter written from the event names alone. Turn completion must come from `agent_settled` rather than `agent_end`, because `agent_end` can be followed by an automatic retry or a compaction — it means "this attempt stopped", not "the turn is over". Process exit is `session_shutdown`, which fires for `quit`, `reload`, `new`, `resume` and `fork`, so three of its five reasons are not an exit at all and an adapter must read the reason. And cancellation has no event: it is inferable from a `StopReason` of `"aborted"` on the final assistant message, with `agent_settled` emitted from a `finally` so it arrives however the turn ended — which is exactly the shape OpenCode's cancellation had before it was traced, sharing its shape with `"error"`.

Pi remains a `session-identity` entry until real traces exist.

## Catalog agents evaluated but not built

These are recorded rather than omitted so that "evaluated, and deliberately not built" is distinguishable from "never looked at". All are `screen-fallback` with `evidence: none`: **none is trace-backed**, so each selects a candidate rather than earning a tier, and `TierFor` would refuse them anything else regardless.

| Agent | Seen | Hook surface | Ceiling on paper | Why it is not built |
| --- | --- | --- | --- | --- |
| grok | 1.0.13 | strongest in the catalog | `full` | Untraced. The only provider anywhere with a dedicated cancellation event: `StopCancelled` carrying `user_interrupt`, `permission_rejected` and `permission_cancelled`, alongside `PermissionDenied` and `Notification{permission_prompt, idle_prompt}`. |
| cursor | 2026.08.25 | full registry in the shipped bundle | `full` | Untraced, and there are user reports of events being omitted on particular versions, so tracing is mandatory rather than a formality. |
| copilot | not installed | GA hooks incl. `permissionRequest` | `full` | Not installed on any surveyed machine, so nothing is verified even against a shipped artifact — the weakest evidence here. Interrupt also appears to be session-granular rather than per turn. |
| amp | not installed | TypeScript plugin process | `advisory` | Not installed and not traced. No permission event and no reliable process-exit signal, so two lanes would stay with screen detection anyway. |
| antigravity | 1.1.22 | five events | `advisory` | Untraced. No session start/end, no blocking, no cancellation, and the hooks configuration path has already moved between releases. |

Two findings from this sweep are worth more than the table.

**No provider except OpenCode emits an approval-*resolved* event.** Codex, Claude Code, grok, cursor and antigravity all announce that permission is being requested and none announces the reply as its own event. Resolution has to be inferred from whatever happens next, and what happens next differs per provider — `PostToolUse` for Codex on approval, `Interrupt` on denial, and nothing at all for Claude Code on either. This is the single most consistent gap across the catalog, and it is the reason the `unblocked` transition is the one that separates the tiers in practice.

**grok reads `~/.claude/settings.json` by design.** Its shipped documentation carries a "Claude Code Compatibility" section for exactly this. Sidecar's installed Claude hook entry therefore also fires inside grok sessions, and because `--kind` is a flag rather than something checked against the pane's actual occupant, a grok session can be bound as `kind=claude` carrying grok's session identifier — after which a cold restore would offer to resume it with the wrong CLI. Tracked as `td-11040b`.

## What Phase B should do first

1. ~~Trace OpenCode cancellation.~~ **Done, 2026-08-30.** Cancellation is observable as `session.error` with `error.name = MessageAbortedError`; OpenCode is promoted to `full`. See "Cancellation, traced" above.
2. Confirm whether the blocked lane can be made self-correcting despite gap 1, or whether a bounded reconciliation against screen evidence is needed for that lane specifically.
3. Decide the single owned plugin path and make repair aware of the other, per gap 3.

## Requalifying a provider version

Authority belongs to a source at a version against a provider at a version, never to a provider name forever. This section is the procedure for the two events that can invalidate a recorded tier: **the provider ships a new version**, or **Sidecar changes a bundled asset**. Both end in the same place — evidence on disk that a test reads back — but they start differently.

### What actually goes stale

A recorded tier rests on three things, and each can rot on its own:

| What | Where it lives | How it goes stale |
| --- | --- | --- |
| The provider's event contract | `covered`, the traces, this document | The provider adds, removes, renames, or re-times an event. |
| The asset that consumes it | `assetVersion`, the bundled asset bytes | Sidecar changes what it installs. |
| The mechanism that makes the asset run | provider-specific: Codex's `trusted_hash`, OpenCode's plugin-loading rules | The provider changes a rule Sidecar reproduced rather than one it was given. |

The third is the dangerous one, because nothing in a Sidecar release notices it. Codex's trust hash is reproduced from provider source, not from a published contract; the Codex timeout-clamping finding above is exactly this category, and it was invisible until a live run showed a user-facing prompt no test would ever produce.

### When a provider ships a new version

1. **Check the range first.** `sidecar agent integration status PROVIDER` reports whether the installed provider version falls inside `testedProviderRange`. Outside it, the resolver already treats the source as unproved rather than trusting it — that is the safety net, not the answer.
2. **Re-trace the promotion-gate cases**, not just the happy path. The set is the one in `FullLifecycleTransitions()` plus the two that only appear under stress: a **denied or cancelled** permission request, and a **mid-turn user interrupt**. Every provider-specific finding recorded on this page came from one of those two, and none of them came from a turn that went well.
3. **Trace into an isolated configuration tree**, never the user's own. `CODEX_HOME`, `CLAUDE_CONFIG_DIR`, `XDG_CONFIG_HOME`, or an additive injection flag where the provider has one (`claude --settings`, `pi --extension`). Anything interactive runs on a private tmux socket. Capture event names and field *names* only.
4. **Update `capabilities.json` and this document together**, then check the sanitized traces in under `internal/agentlifecycle/testdata/traces/<provider>/`.
5. **Let the tests decide the tier.** Do not write a tier; write the evidence and let `Capability.TierFor` derive it. `TestCapabilityMatrixCannotClaimUnearnedAuthority` re-derives every entry from its own record, and a `real-trace` claim with no files on disk fails.

### When Sidecar changes an asset

1. Bump the asset version constant (`OpenCodeAssetVersion`, `CodexAssetVersion`, `ClaudeAssetVersion`).
2. Append the superseded entry to that adapter's canonical history, so an installed copy of the old version reads as `outdated` rather than as damage.
3. Move `assetVersion` in `capabilities.json` to match.
4. Requalify against the traces — a new asset consuming the same events still needs to be shown to consume them correctly.
5. Update the golden checksum in `asset_golden_test.go`, which is the guard that makes steps 1–4 unskippable: changing the bytes without bumping the version fails the build.

The order matters. Updating the golden first turns the guard into a formality.

### What a requalification must be able to fail

A requalification that can only confirm is not one. These are the outcomes it must be able to reach, and each has a home:

- **A transition disappeared.** Remove it from `covered`; `TierFor` demotes the entry on its own. Add a gap saying what used to work.
- **A transition appeared.** Add it, and add a trace. A provider that starts emitting a cancellation event is the single most valuable thing this procedure can discover — `TestClaudeCancellationEmitsNothingAtAll` exists to fail loudly when that day comes, because a gap recorded as permanent is exactly the kind that nobody re-checks.
- **A reproduced mechanism drifted.** The failure is usually visible to the user rather than to a test, so record what the user would see. Codex's is a "Hooks need review" prompt; that is the right direction for a security control to fail, and the wrong thing to discover from a bug report.
- **Nothing changed.** Widen `testedProviderRange` and say which version was checked.

## Maintaining this document

When a provider version changes, or an integration is added, update `internal/agentlifecycle/capabilities.json` and this document together. The tests will reject a `real-trace` claim with no trace files, a `docs-only` claim with trace files present, an untraced entry claiming no known gaps, and any tier the entry's own coverage does not earn. Capture procedure and sanitization rules are in `internal/agentlifecycle/testdata/README.md`.
