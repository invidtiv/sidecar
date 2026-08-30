# Agent lifecycle capability matrix

**Status:** Phase A evidence baseline, recorded 2026-08-30. **Plan:** [Deterministic agent lifecycle hooks](../plans/active/notification-agent-lifecycle-hooks.md). **Tracking:** `td-43a93f`.

This document records what each agent provider's own lifecycle events can actually tell Sidecar, how strong the evidence for that claim is, and what authority tier the evidence justifies. It is the prose companion to `internal/agentlifecycle/testdata/capabilities.json`, which is the machine-readable version the code reads and the tests police.

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

This is enforced rather than documented. `Capability.TierFor` demotes any `full` claim that lacks real traces or complete coverage, and `TestCapabilityMatrixCannotClaimUnearnedAuthority` re-derives every entry's tier from its own recorded evidence. An entry cannot be edited to claim authority its evidence does not support.

## Summary

| Provider | Version seen | Evidence | Tier now | Herdr's tier | Blocking gap |
| --- | --- | --- | --- | --- | --- |
| OpenCode | 1.18.23 | real-trace | `advisory` | lifecycle authority | cancellation untraced |
| Codex | 0.151.0 | docs-only | `advisory` | session only | no traces; feature-flagged off |
| Claude Code | 2.1.220 | docs-only | `session-identity` | session only | no cancellation event exists |
| Pi | 0.84.3 | docs-only | `session-identity` | lifecycle authority | no portable blocked signal |

"Herdr's tier" is what the Herdr project ships at commit `4a3b04f59ba3b7d8a15cea187b23e1e80c343b0c`. It is included because Herdr has shipped all four integrations in production, so where it disagrees with the published contract that disagreement is itself evidence.

## Transition coverage

`YES` means an official event exists and, where marked traced, was observed. `PARTIAL` means it must be inferred. `NO` means no event exists.

| Transition | OpenCode | Codex | Claude Code | Pi |
| --- | --- | --- | --- | --- |
| work start | YES (traced) | YES | YES | YES |
| tool use | YES (traced) | YES | YES | YES |
| blocked on request | YES (traced) | YES | YES | PARTIAL |
| unblocked | YES (traced) | PARTIAL | PARTIAL | PARTIAL |
| turn complete | YES (traced) | YES | PARTIAL | YES |
| cancellation | PARTIAL | YES | NO | PARTIAL |
| session identity | YES (traced) | YES | YES | YES |
| subagent | PARTIAL | YES | YES | NO |
| process exit | YES (traced) | YES | YES | YES |

## OpenCode

**Source:** <https://opencode.ai/docs/plugins/>, cross-read against `packages/plugin/src/index.ts`. **Traced against 1.18.23 on darwin/arm64, 2026-08-30.** Traces: `internal/agentlifecycle/testdata/traces/opencode/`.

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
4. **Cancellation is untraced.** There is no dedicated cancel event; the expectation is that an interrupt resolves through `session.status`, but that was not observed.

Gap 4 is why OpenCode ships at `advisory` today rather than `full`. It is the single item blocking promotion, and Phase B should trace it first.

## Codex

**Source:** the published config reference redirects and truncates before the hooks section, so the authoritative contract is the upstream `HookEventName` schema and the generated hook JSON schemas. **Not traced.**

Codex has the best-shaped event set of the four on paper. Twelve events close the loop, and it is the only provider with a first-class `Interrupt` event, which is precisely the transition Claude Code cannot express. Payloads are strict generated JSON schemas, so the contract is unambiguous.

Two things keep it out of the steel-thread position. Hooks are disabled by default behind `features.hooks`, so an integration must modify `config.toml` as well as install a hook file, which is two owned mutations instead of one. And Herdr, having shipped a Codex integration through eight asset versions, deliberately removed its `PreToolUse`/`working`, `PermissionRequest`/`blocked` and `Stop`/`idle` hooks and now installs only `SessionStart` for session identity. That rollback is not explained by anything in the published contract, and it should be reproduced or refuted empirically before Codex is trusted for state.

## Claude Code

**Source:** <https://code.claude.com/docs/en/hooks>. **Not traced.**

Claude Code has by far the richest event surface, including the best subagent model of the four, and it has the largest user base. It is nonetheless the wrong provider to go first, for one disqualifying reason: **there is no user-cancellation event at all.** A state machine driven only by these hooks latches on `working` when a user interrupts a turn. That is a contract gap rather than a tracing gap, so no amount of tracing can close it.

Two further limits matter. `Stop` means "stopped generating", not "ready for input", so completion cannot be taken from it without reconciliation against screen state. And hook configuration merges additively across five layers, so Sidecar can add entries but can never own the effective hook set.

Herdr shipped a full-lifecycle Claude hook set and then removed it, keeping only `SessionStart`, citing missed permission results and escape interrupts. Sidecar should reach the same place deliberately: Claude Code is a strong `advisory` candidate whose events reconcile against screen state, and a strong `session-identity` source today.

## Pi

**Source:** <https://pi.dev/docs/latest/extensions>. **Not traced.** Upstream type definitions were not read, so this entry is one notch less certain than Codex.

Pi has the cleanest agent-flow abstraction of the four. `agent_start` and `agent_settled` are exactly the right pair, and `session_shutdown` closes the session cleanly.

The blocked lane is the problem. The one working production integration obtains it from a `pi.events` bus message named after that consumer rather than from a documented lifecycle event, and nothing in the shipped asset emits that message. Sidecar cannot build on a channel named for another tool, and `ui_prompt_start` covers only extension-driven prompts. Until a portable blocked signal is identified, Pi is a `session-identity` source.

## What Phase B should do first

1. Trace OpenCode cancellation. It is the only thing between the recorded evidence and `full` authority, and `TierFor` will keep OpenCode at `advisory` until `cancelled` appears in its covered set.
2. Confirm whether the blocked lane can be made self-correcting despite gap 1, or whether a bounded reconciliation against screen evidence is needed for that lane specifically.
3. Decide the single owned plugin path and make repair aware of the other, per gap 3.

## Maintaining this document

When a provider version changes, or an integration is added, update `internal/agentlifecycle/testdata/capabilities.json` and this document together. The tests will reject a `real-trace` claim with no trace files, a `docs-only` claim with trace files present, an untraced entry claiming no known gaps, and any tier the entry's own coverage does not earn. Capture procedure and sanitization rules are in `internal/agentlifecycle/testdata/README.md`.
