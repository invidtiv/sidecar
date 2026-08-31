# Agent lifecycle capability matrix

**Status:** Phase A evidence baseline recorded 2026-08-30; OpenCode cancellation traced and promoted to `full` in Phase B, 2026-08-30. **Plan:** [Deterministic agent lifecycle hooks](../plans/active/notification-agent-lifecycle-hooks.md). **Tracking:** `td-43a93f`.

This document records what each agent provider's own lifecycle events can actually tell Sidecar, how strong the evidence for that claim is, and what authority tier the evidence justifies. It is the prose companion to `internal/agentlifecycle/capabilities.json`, which is the machine-readable version the code reads and the tests police. That file is embedded into the binary and read at runtime through `agentlifecycle.Capabilities()`, so the registry the resolver trusts and the evidence these tests police are one file rather than two that could drift.

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
| cancellation | YES (traced) | YES | NO | PARTIAL |
| session identity | YES (traced) | YES | YES | YES |
| subagent | PARTIAL | YES | YES | NO |
| process exit | YES (traced) | YES | YES | YES |

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

1. ~~Trace OpenCode cancellation.~~ **Done, 2026-08-30.** Cancellation is observable as `session.error` with `error.name = MessageAbortedError`; OpenCode is promoted to `full`. See "Cancellation, traced" above.
2. Confirm whether the blocked lane can be made self-correcting despite gap 1, or whether a bounded reconciliation against screen evidence is needed for that lane specifically.
3. Decide the single owned plugin path and make repair aware of the other, per gap 3.

## Maintaining this document

When a provider version changes, or an integration is added, update `internal/agentlifecycle/capabilities.json` and this document together. The tests will reject a `real-trace` claim with no trace files, a `docs-only` claim with trace files present, an untraced entry claiming no known gaps, and any tier the entry's own coverage does not earn. Capture procedure and sanitization rules are in `internal/agentlifecycle/testdata/README.md`.
